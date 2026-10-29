package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"github.com/olduvai-jp/file-okiba-cli/internal/config"
)

type RemoteFile struct {
	FileToken string `json:"fileToken"`
	Token     string `json:"token,omitempty"`
	FileName  string `json:"fileName"`
	Name      string `json:"name,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	URL       string `json:"url,omitempty"`
}
type DownloadOptions struct {
	Output     string
	Existing   string
	FileTokens []string
	Progress   io.Writer
}
type DownloadResult struct {
	Downloaded []FileResult `json:"downloaded"`
	Skipped    []FileResult `json:"skippedExisting"`
}
type downloadState struct {
	Token       string `json:"fileToken"`
	Destination string `json:"destination"`
	Size        int64  `json:"size"`
	ETag        string `json:"etag"`
	Policy      string `json:"policy"`
}

func ListFiles(ctx context.Context, c *api.Client, t api.Target) ([]RemoteFile, error) {
	if t.BatchToken == "" {
		return nil, api.Err("batch_url_required", "バッチURLを指定してください")
	}
	files := []RemoteFile{}
	cursor := ""
	seen := map[string]bool{}
	for {
		p := t.Path("/files") + "?limit=200"
		if cursor != "" {
			p += "&cursor=" + url.QueryEscape(cursor)
		}
		m, _, e := c.Do(ctx, "GET", p, nil, true)
		if e != nil {
			return nil, e
		}
		var page struct {
			Items []RemoteFile `json:"items"`
			Next  string       `json:"nextCursor"`
		}
		if e = api.Decode(m, "", &page); e != nil {
			return nil, e
		}
		for _, f := range page.Items {
			if f.FileToken == "" {
				f.FileToken = f.Token
			}
			if f.FileName == "" {
				f.FileName = f.Name
			}
			if !api.ValidToken(f.FileToken) {
				return nil, api.Err("invalid_response", "ファイルトークンが不正です")
			}
			files = append(files, f)
		}
		if page.Next == "" {
			return files, nil
		}
		if seen[page.Next] {
			return nil, api.Err("invalid_response", "一覧カーソルが循環しています")
		}
		seen[page.Next] = true
		cursor = page.Next
	}
}
func Links(ctx context.Context, c *api.Client, t api.Target, tokens []string) ([]RemoteFile, error) {
	listed, e := ListFiles(ctx, c, t)
	if e != nil {
		return nil, e
	}
	selected := map[string]bool{}
	for _, s := range tokens {
		selected[s] = true
	}
	chosen := []string{}
	for _, f := range listed {
		if len(tokens) == 0 || selected[f.FileToken] {
			chosen = append(chosen, f.FileToken)
			delete(selected, f.FileToken)
		}
	}
	if len(selected) > 0 {
		return nil, api.Err("file_not_found", "指定ファイルがバッチにありません")
	}
	files := []RemoteFile{}
	for start := 0; start < len(chosen); start += 32 {
		chunk := chosen[start:min(start+32, len(chosen))]
		m, _, e := c.Do(ctx, "POST", t.Path("/download-urls"), map[string]any{"fileTokens": chunk}, true)
		if e != nil {
			return nil, e
		}
		var response struct {
			Files []RemoteFile `json:"files"`
		}
		if e = api.Decode(m, "", &response); e != nil {
			return nil, e
		}
		if len(response.Files) != len(chunk) {
			return nil, api.Err("invalid_response", "ダウンロードURLの件数が一致しません")
		}
		for i, f := range response.Files {
			if f.FileName == "" {
				f.FileName = f.Name
			}
			if f.FileToken != chunk[i] || f.SizeBytes < 0 {
				return nil, api.Err("invalid_response", "ダウンロード対象が一致しません")
			}
			if e = api.ValidateStorageURL(f.URL); e != nil {
				return nil, e
			}
			files = append(files, f)
		}
	}
	return files, nil
}

var reservedName = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\..*)?$`)

func safeName(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:<>\"|?*") || strings.TrimRight(name, ". ") != name || reservedName.MatchString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", api.Err("invalid_file_name", "安全に保存できないファイル名です")
	}
	return name, nil
}
func Download(ctx context.Context, c *api.Client, t api.Target, o DownloadOptions) (result DownloadResult, err error) {
	result = DownloadResult{Downloaded: []FileResult{}, Skipped: []FileResult{}}
	defer func() {
		if err != nil {
			if ae, ok := err.(*api.Error); ok {
				copy := *ae
				copy.Details = result
				err = &copy
			} else {
				err = &api.Error{Code: "download_failed", Message: "ダウンロードに失敗しました。同じコマンドで再開できます", Details: result}
			}
		}
	}()
	if o.Existing == "" {
		o.Existing = "rename"
	}
	if o.Existing != "rename" && o.Existing != "skip" && o.Existing != "overwrite" && o.Existing != "error" {
		return result, api.Err("usage", "existingはrename,skip,overwrite,errorです")
	}
	if o.Output == "" {
		o.Output = "."
	}
	root, e := filepath.Abs(o.Output)
	if e != nil {
		return result, e
	}
	if e = os.MkdirAll(root, 0755); e != nil {
		return result, e
	}
	stateDir := filepath.Join(root, ".file-okiba")
	if info, e := os.Lstat(stateDir); e == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return result, api.Err("unsafe_destination", "状態保存先が通常のディレクトリではありません")
		}
	} else if !os.IsNotExist(e) {
		return result, e
	}
	if e = os.MkdirAll(stateDir, 0700); e != nil {
		return result, e
	}
	listed, e := ListFiles(ctx, c, t)
	if e != nil {
		return result, e
	}
	selected := map[string]bool{}
	for _, token := range o.FileTokens {
		selected[token] = true
	}
	for _, f := range listed {
		delete(selected, f.FileToken)
	}
	if len(selected) > 0 {
		return result, api.Err("file_not_found", "指定ファイルがバッチにありません")
	}
	for _, meta := range listed {
		if len(o.FileTokens) > 0 {
			match := false
			for _, token := range o.FileTokens {
				if token == meta.FileToken {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		links, e := Links(ctx, c, t, []string{meta.FileToken})
		if e != nil {
			return result, e
		}
		f := links[0]
		name, e := safeName(f.FileName)
		if e != nil {
			return result, e
		}
		key := sha256.Sum256([]byte(t.Origin + "\n" + t.BatchToken + "\n" + f.FileToken))
		stem := filepath.Join(stateDir, hex.EncodeToString(key[:16]))
		lock, e := lockState(stem)
		if e != nil {
			return result, e
		}
		item, skipped, e := downloadFile(ctx, c, f, root, name, stem, o)
		lock.Unlock()
		if e != nil {
			return result, e
		}
		if skipped {
			result.Skipped = append(result.Skipped, item)
		} else {
			result.Downloaded = append(result.Downloaded, item)
		}
	}
	return result, nil
}
func downloadFile(ctx context.Context, c *api.Client, f RemoteFile, root, name, stem string, o DownloadOptions) (FileResult, bool, error) {
	state := downloadState{Token: f.FileToken, Size: f.SizeBytes, Policy: o.Existing}
	res := FileResult{FileToken: f.FileToken, FileName: name, SizeBytes: f.SizeBytes}
	b, e := os.ReadFile(stem + ".json")
	resuming := e == nil
	if resuming {
		if json.Unmarshal(b, &state) != nil || state.Token != f.FileToken || state.Size != f.SizeBytes || state.Policy != o.Existing || filepath.Dir(state.Destination) != root {
			return res, false, api.Err("invalid_state", "ダウンロード状態が一致しません")
		}
	} else if !os.IsNotExist(e) {
		return res, false, e
	}
	if !resuming {
		dest := filepath.Join(root, name)
		for n := 1; ; n++ {
			info, e := os.Lstat(dest)
			if os.IsNotExist(e) {
				break
			}
			if e != nil {
				return res, false, e
			}
			switch o.Existing {
			case "skip":
				if !info.Mode().IsRegular() {
					return res, false, api.Err("download_conflict", "保存先が通常ファイルではありません")
				}
				res.Path = dest
				return res, true, nil
			case "overwrite":
				if !info.Mode().IsRegular() {
					return res, false, api.Err("download_conflict", "保存先が通常ファイルではありません")
				}
			case "error":
				return res, false, api.Err("download_conflict", "同名ファイルが存在します")
			case "rename":
				ext := filepath.Ext(name)
				dest = filepath.Join(root, strings.TrimSuffix(name, ext)+fmt.Sprintf(".%d", n)+ext)
				continue
			}
			break
		}
		state.Destination = dest
		if e = saveState(stem+".json", state); e != nil {
			return res, false, e
		}
	}
	res.Path = state.Destination
	res.FileName = filepath.Base(res.Path)
	partPath := stem + ".part"
	offset := int64(0)
	if info, e := os.Lstat(partPath); e == nil {
		if !info.Mode().IsRegular() {
			return res, false, api.Err("unsafe_destination", "途中ファイルが通常ファイルではありません")
		}
		offset = info.Size()
	} else if !os.IsNotExist(e) {
		return res, false, e
	}
	if offset > f.SizeBytes {
		return res, false, api.Err("invalid_state", "途中ファイルが予定サイズを超えています")
	}
	if state.ETag == "" || strings.HasPrefix(state.ETag, "W/") {
		offset = 0
	}
	if offset != f.SizeBytes || offset == 0 {
		var response *http.Response
		for attempt := 0; attempt < 3; attempt++ {
			req, e := http.NewRequestWithContext(ctx, "GET", f.URL, nil)
			if e != nil {
				return res, false, api.Err("invalid_response", "ダウンロードURLが不正です")
			}
			if offset > 0 {
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
				req.Header.Set("If-Range", state.ETag)
			}
			response, e = c.HTTP.Do(req)
			if e != nil {
				if attempt < 2 {
					if e = api.Backoff(ctx, attempt, ""); e != nil {
						return res, false, e
					}
					continue
				}
				return res, false, api.Err("network_error", "ダウンロード通信に失敗しました")
			}
			if api.Retryable(response.StatusCode) && attempt < 2 {
				response.Body.Close()
				if e = api.Backoff(ctx, attempt, response.Header.Get("Retry-After")); e != nil {
					return res, false, e
				}
				continue
			}
			break
		}
		defer response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 206 {
			return res, false, &api.Error{Code: "download_failed", Message: "ストレージから取得できませんでした", Status: response.StatusCode}
		}
		if response.StatusCode == 206 {
			expected := fmt.Sprintf("bytes %d-%d/%d", offset, f.SizeBytes-1, f.SizeBytes)
			if offset == 0 || response.Header.Get("Content-Range") != expected || response.Header.Get("ETag") != state.ETag {
				return res, false, api.Err("invalid_range", "再開応答の範囲またはETagが一致しません")
			}
		} else {
			offset = 0
		}
		mode := os.O_WRONLY | os.O_CREATE
		if offset == 0 {
			mode |= os.O_TRUNC
		}
		out, e := os.OpenFile(partPath, mode, 0600)
		if e != nil {
			return res, false, e
		}
		if e = config.RestrictFile(partPath); e != nil {
			out.Close()
			return res, false, e
		}
		if offset > 0 {
			if _, e = out.Seek(offset, io.SeekStart); e != nil {
				out.Close()
				return res, false, e
			}
		}
		// Commit the partial reset before binding it to a different object's ETag.
		// A crash here leaves either the old identity+old bytes or an empty prefix.
		if e = out.Sync(); e != nil {
			out.Close()
			return res, false, e
		}
		state.ETag = response.Header.Get("ETag")
		if e = saveState(stem+".json", state); e != nil {
			out.Close()
			return res, false, e
		}
		if o.Progress != nil {
			fmt.Fprintf(o.Progress, "取得 %s (%d/%d bytesから)\n", name, offset, f.SizeBytes)
		}
		count, copyErr := io.Copy(out, io.LimitReader(response.Body, f.SizeBytes-offset+1))
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil {
			return res, false, api.Err("download_interrupted", "取得が中断されました。同じコマンドで再開できます")
		}
		if syncErr != nil {
			return res, false, syncErr
		}
		if closeErr != nil {
			return res, false, closeErr
		}
		if count+offset != f.SizeBytes {
			return res, false, api.Err("size_mismatch", "取得サイズが一致しません。同じコマンドで再開できます")
		}
	}
	if o.Existing == "overwrite" {
		if info, e := os.Lstat(res.Path); e == nil && !info.Mode().IsRegular() {
			return res, false, api.Err("download_conflict", "保存先が通常ファイルではありません")
		}
		if e = os.Rename(partPath, res.Path); e != nil {
			return res, false, e
		}
	} else {
		for n := 1; ; n++ {
			e = publishNoReplace(partPath, res.Path)
			if e == nil {
				break
			}
			if !os.IsExist(e) {
				return res, false, api.Err("publish_failed", "保存先への配置に失敗しました。ファイルシステムと権限を確認してください")
			}
			if o.Existing != "rename" {
				return res, false, api.Err("download_conflict", "保存先が転送中に作成されました")
			}
			ext := filepath.Ext(name)
			res.Path = filepath.Join(root, strings.TrimSuffix(name, ext)+fmt.Sprintf(".%d", n)+ext)
			res.FileName = filepath.Base(res.Path)
			state.Destination = res.Path
			if e = saveState(stem+".json", state); e != nil {
				return res, false, e
			}
		}
	}
	if e = os.Remove(stem + ".json"); e != nil {
		return res, false, e
	}
	return res, false, nil
}
