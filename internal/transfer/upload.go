package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"github.com/olduvai-jp/file-okiba-cli/internal/config"
)

type UploadOptions struct {
	StateDir      string
	RetentionDays int
	Concurrency   int
	Restart       bool
	Progress      io.Writer
}
type FileResult struct {
	FileToken string `json:"fileToken"`
	FileName  string `json:"fileName"`
	SizeBytes int64  `json:"sizeBytes"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
	Path      string `json:"path,omitempty"`
}
type UploadResult struct {
	BatchToken string       `json:"batchToken"`
	BatchURL   string       `json:"batchUrl"`
	Completed  []FileResult `json:"completed"`
	StatePath  string       `json:"statePath"`
}
type part struct {
	Number int    `json:"partNumber"`
	ETag   string `json:"etag"`
}
type session struct {
	Token          string `json:"sessionToken"`
	FileToken      string `json:"fileToken"`
	PartSize       int64  `json:"partSizeBytes"`
	TotalParts     int    `json:"totalParts"`
	MaxConcurrency int    `json:"maxConcurrency"`
	Size           int64  `json:"sizeBytes"`
	Status         string `json:"status"`
	Parts          []part `json:"uploadedParts"`
	ExpiresAt      int64  `json:"expiresAt"`
}
type uploadFile struct {
	InvalidSource bool     `json:"invalidSource,omitempty"`
	Path          string   `json:"path"`
	Size          int64    `json:"size"`
	Modified      int64    `json:"modified"`
	Digest        string   `json:"sha256"`
	Session       *session `json:"session,omitempty"`
	Done          bool     `json:"done"`
	Creating      bool     `json:"creating,omitempty"`
}
type uploadState struct {
	Version       int          `json:"version"`
	Target        api.Target   `json:"target"`
	RetentionDays int          `json:"retentionDays"`
	Files         []uploadFile `json:"files"`
	Result        UploadResult `json:"result"`
	Creating      bool         `json:"creating,omitempty"`
}

func ValidRetention(n int) bool {
	for _, v := range []int{1, 3, 5, 7, 14, 30, 60, 100} {
		if n == v {
			return true
		}
	}
	return false
}
func lockState(path string) (*flock.Flock, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	l := flock.New(path+".lock", flock.SetPermissions(0600))
	ok, e := l.TryLock()
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, api.Err("transfer_busy", "同じ転送を別のプロセスが実行しています")
	}
	return l, nil
}
func saveState(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return config.WritePrivate(path, b)
}
func hashFile(path string) (uploadFile, error) {
	p, e := filepath.Abs(path)
	if e != nil {
		return uploadFile{}, e
	}
	info, e := os.Lstat(p)
	if e != nil {
		return uploadFile{}, api.Err("invalid_file", "ファイルを開けません: "+path)
	}
	if !info.Mode().IsRegular() {
		return uploadFile{}, api.Err("invalid_file", "通常ファイルを指定してください。ディレクトリはzip/tar化して送信してください")
	}
	if info.Size() <= 0 {
		return uploadFile{}, api.Err("empty_file", "現在のAPIは0バイトファイルに対応していません")
	}
	f, e := os.Open(p)
	if e != nil {
		return uploadFile{}, e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return uploadFile{}, e
	}
	return uploadFile{Path: p, Size: info.Size(), Modified: info.ModTime().UnixNano(), Digest: hex.EncodeToString(h.Sum(nil))}, nil
}
func Upload(ctx context.Context, c *api.Client, t api.Target, paths []string, o UploadOptions) (result UploadResult, err error) {
	if t.BatchToken != "" || !api.ValidToken(t.GroupToken) {
		return result, api.Err("group_url_required", "アップロードにはグループURLが必要です")
	}
	if len(paths) == 0 {
		return result, api.Err("usage", "ファイルを指定してください")
	}
	if !ValidRetention(o.RetentionDays) {
		return result, api.Err("invalid_retention", "保持日数は1,3,5,7,14,30,60,100から選んでください")
	}
	if o.Concurrency < 1 || o.Concurrency > 32 {
		return result, api.Err("invalid_concurrency", "並列数は1〜32です")
	}
	if o.StateDir == "" {
		o.StateDir = config.StateDir()
	}
	files := make([]uploadFile, 0, len(paths))
	keyPaths := []string{}
	for _, p := range paths {
		f, e := hashFile(p)
		if e != nil {
			return result, e
		}
		files = append(files, f)
		keyPaths = append(keyPaths, f.Path)
	}
	key, _ := json.Marshal([]any{t, keyPaths, o.RetentionDays})
	h := sha256.Sum256(key)
	statePath := filepath.Join(o.StateDir, "upload-"+hex.EncodeToString(h[:16])+".json")
	l, e := lockState(statePath)
	if e != nil {
		return result, e
	}
	defer l.Unlock()
	state := uploadState{Version: 1, Target: t, Files: files, RetentionDays: o.RetentionDays, Result: UploadResult{Completed: []FileResult{}, StatePath: statePath}}
	if b, e := os.ReadFile(statePath); e == nil && !o.Restart {
		if json.Unmarshal(b, &state) != nil || state.Version != 1 || len(state.Files) != len(files) {
			return result, api.Err("invalid_state", "転送状態が不正です")
		}
		for i, f := range files {
			if state.Files[i].InvalidSource {
				return state.Result, api.Err("source_changed", "前回の転送中にファイルが変更されました。このセッションは再利用できません。--restart で新規送信してください")
			}
			if f.Path != state.Files[i].Path || f.Size != state.Files[i].Size || f.Digest != state.Files[i].Digest {
				return state.Result, api.Err("source_changed", "前回からファイル内容が変わっています。新しい転送には --restart を指定してください")
			}
		}
	} else if e != nil && !os.IsNotExist(e) {
		return result, e
	}
	result = state.Result
	defer func() {
		result = state.Result
		if err != nil {
			if errors.Is(err, context.Canceled) {
				err = &api.Error{Code: "cancelled", Message: "転送を中止しました。同じコマンドで再開できます", Details: state.Result}
				return
			}
			var ae *api.Error
			if errors.As(err, &ae) {
				copy := *ae
				copy.Details = state.Result
				err = &copy
			} else {
				err = &api.Error{Code: "upload_failed", Message: "アップロードに失敗しました。状態を保存したので同じコマンドで再開できます", Details: state.Result}
			}
		}
	}()
	save := func() error { return saveState(statePath, state) }
	if state.Creating {
		return result, api.Err("outcome_unknown", "前回のバッチ作成結果が不明です。管理一覧で確認してから --restart で新規送信してください")
	}
	if state.Result.BatchToken == "" {
		state.Creating = true
		if e = save(); e != nil {
			return result, e
		}
		body := map[string]any{}
		if c.Password != "" {
			body["downloadPassword"] = c.Password
		}
		m, _, e := c.Do(ctx, "POST", t.Path("/upload-batches"), body, false)
		if e != nil {
			return result, e
		}
		var b struct {
			Token      string `json:"token"`
			BatchToken string `json:"batchToken"`
		}
		if e = api.Decode(m, "batch", &b); e != nil {
			return result, e
		}
		if b.Token == "" {
			b.Token = b.BatchToken
		}
		if !api.ValidToken(b.Token) {
			return result, api.Err("invalid_response", "バッチトークンが不正です")
		}
		state.Result.BatchToken = b.Token
		state.Result.BatchURL = t.Origin + "/g/shared/b/" + b.Token
		state.Creating = false
		if e = save(); e != nil {
			return result, e
		}
	}
	t.BatchToken = state.Result.BatchToken
	for i := range state.Files {
		f := &state.Files[i]
		if f.Done {
			continue
		}
		if f.Creating {
			return result, api.Err("outcome_unknown", "前回のセッション作成結果が不明です。管理一覧で確認してから --restart を指定してください")
		}
		if f.Session == nil {
			f.Creating = true
			if e = save(); e != nil {
				return result, e
			}
			m, _, e := c.Do(ctx, "POST", t.Path("/uploads"), map[string]any{"fileName": filepath.Base(f.Path), "size": f.Size, "contentType": "application/octet-stream", "retentionDays": o.RetentionDays}, false)
			if e != nil {
				return result, e
			}
			s := session{}
			if e = api.Decode(m, "upload", &s); e != nil {
				return result, e
			}
			if !api.ValidToken(s.Token) {
				return result, api.Err("invalid_response", "セッショントークンが不正です")
			}
			f.Session = &s
			f.Creating = false
			if e = save(); e != nil {
				return result, e
			}
		} else {
			s, e := getSession(ctx, c, f.Session.Token)
			if e != nil {
				return result, e
			}
			if s.FileToken != f.Session.FileToken || (s.Size != 0 && s.Size != f.Size) {
				return result, api.Err("invalid_response", "再開先のファイル情報が一致しません")
			}
			s.ExpiresAt = f.Session.ExpiresAt
			s.MaxConcurrency = f.Session.MaxConcurrency
			f.Session = s
		}
		if f.Session.Status != "ready" {
			e = uploadOne(ctx, c, f, o)
			info, statErr := os.Stat(f.Path)
			var ae *api.Error
			changed := statErr != nil || info.Size() != f.Size || info.ModTime().UnixNano() != f.Modified
			if changed || (errors.As(e, &ae) && ae.Code == "source_changed") {
				f.InvalidSource = true
				if saveErr := save(); saveErr != nil {
					return result, saveErr
				}
				return result, api.Err("source_changed", "転送中にファイルが変更されたため、このセッションを再利用できません。--restart で新規送信してください")
			}
			if e != nil {
				return result, e
			}
		}
		state.Result.Completed = append(state.Result.Completed, FileResult{FileToken: f.Session.FileToken, FileName: filepath.Base(f.Path), SizeBytes: f.Size, ExpiresAt: f.Session.ExpiresAt})
		f.Done = true
		if e = save(); e != nil {
			return result, e
		}
	}
	return state.Result, nil
}
func getSession(ctx context.Context, c *api.Client, token string) (*session, error) {
	m, _, e := c.Do(ctx, "GET", "/api/v1/uploads/"+token, nil, true)
	if e != nil {
		return nil, e
	}
	s := &session{}
	if e = api.Decode(m, "upload", s); e != nil {
		return nil, e
	}
	if s.Token != token {
		return nil, api.Err("invalid_response", "セッション情報が一致しません")
	}
	return s, nil
}
func uploadOne(ctx context.Context, c *api.Client, f *uploadFile, o UploadOptions) error {
	s := f.Session
	if s.Status != "" && s.Status != "pending" && s.Status != "uploading" {
		return api.Err("session_expired", "セッションを再開できません。再送するには --restart を指定してください")
	}
	if s.ExpiresAt > 0 && time.Now().UnixMilli() >= s.ExpiresAt {
		return api.Err("session_expired", "保持期限を過ぎています。再送するには --restart を指定してください")
	}
	if s.PartSize <= 0 || s.TotalParts < 1 || s.TotalParts > 10000 || ((f.Size-1)/s.PartSize+1) != int64(s.TotalParts) {
		return api.Err("invalid_response", "分割情報が不正です")
	}
	file, e := os.Open(f.Path)
	if e != nil {
		return e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil || info.Size() != f.Size || info.ModTime().UnixNano() != f.Modified {
		return api.Err("source_changed", "転送前にファイルが変更されました")
	}
	done := map[int]part{}
	for _, p := range s.Parts {
		if p.Number < 1 || p.Number > s.TotalParts || p.ETag == "" {
			return api.Err("invalid_response", "記録済みpartが不正です")
		}
		done[p.Number] = p
	}
	missing := []int{}
	for n := 1; n <= s.TotalParts; n++ {
		if _, ok := done[n]; !ok {
			missing = append(missing, n)
		}
	}
	concurrency := o.Concurrency
	if s.MaxConcurrency > 0 && concurrency > s.MaxConcurrency {
		concurrency = s.MaxConcurrency
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				if workCtx.Err() != nil {
					return
				}
				p, e := sendPart(workCtx, c, file, s, n, f.Size)
				mu.Lock()
				if e != nil {
					if first == nil {
						first = e
					}
					cancel()
				} else {
					done[n] = p
					if o.Progress != nil {
						fmt.Fprintf(o.Progress, "送信 %s: %d/%d parts\n", filepath.Base(f.Path), len(done), s.TotalParts)
					}
				}
				mu.Unlock()
				if e != nil {
					return
				}
			}
		}()
	}
loop:
	for _, n := range missing {
		select {
		case jobs <- n:
		case <-workCtx.Done():
			break loop
		}
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		return first
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	info, e = file.Stat()
	if e != nil || info.Size() != f.Size || info.ModTime().UnixNano() != f.Modified {
		return api.Err("source_changed", "転送中にファイルが変更されました")
	}
	all := make([]part, 0, len(done))
	for _, p := range done {
		all = append(all, p)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Number < all[j].Number })
	for attempt := 0; attempt < 3; attempt++ {
		m, _, e := c.Do(ctx, "POST", "/api/v1/uploads/"+s.Token+"/complete", map[string]any{"parts": all}, false)
		if e == nil {
			if v, ok := m["expiresAt"].(float64); ok {
				s.ExpiresAt = int64(v)
			}
			return nil
		}
		var ae *api.Error
		if !errors.As(e, &ae) || ae.Code != "outcome_unknown" {
			return e
		}
		current, statusErr := getSession(ctx, c, s.Token)
		if statusErr != nil {
			return api.Err("outcome_unknown", "完了処理の結果を確認できません。同じコマンドで状態を確認してください")
		}
		if current.Status == "ready" {
			return nil
		}
		if current.Status != "pending" && current.Status != "uploading" {
			return e
		}
		if attempt == 2 {
			return e
		}
		if e = api.Backoff(ctx, attempt, ""); e != nil {
			return e
		}
	}
	return api.Err("outcome_unknown", "完了処理の結果が不明です")
}
func sendPart(ctx context.Context, c *api.Client, file *os.File, s *session, n int, total int64) (part, error) {
	size := min(s.PartSize, total-int64(n-1)*s.PartSize)
	base := "/api/v1/uploads/" + s.Token
	for refresh := 0; refresh < 2; refresh++ {
		m, _, e := c.Do(ctx, "POST", base+"/parts", map[string]any{"parts": []map[string]any{{"partNumber": n, "sizeBytes": size}}}, true)
		if e != nil {
			return part{}, e
		}
		var signed struct {
			Parts []struct {
				Number  int               `json:"partNumber"`
				Method  string            `json:"method"`
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
			}
		}
		if e = api.Decode(m, "", &signed); e != nil {
			return part{}, e
		}
		if len(signed.Parts) != 1 || signed.Parts[0].Number != n || signed.Parts[0].Method != "PUT" {
			return part{}, api.Err("invalid_response", "part署名の応答が不正です")
		}
		p := signed.Parts[0]
		if e = api.ValidateStorageURL(p.URL); e != nil {
			return part{}, e
		}
		expired := false
		for attempt := 0; attempt < 3; attempt++ {
			req, e := http.NewRequestWithContext(ctx, "PUT", p.URL, io.NewSectionReader(file, int64(n-1)*s.PartSize, size))
			if e != nil {
				return part{}, api.Err("invalid_response", "署名URLが不正です")
			}
			req.ContentLength = size
			for k, v := range p.Headers {
				switch strings.ToLower(k) {
				case "content-type", "x-amz-content-sha256", "x-amz-date", "authorization":
					req.Header.Set(k, v)
				case "content-length":
					if v != fmt.Sprint(size) {
						return part{}, api.Err("invalid_response", "partサイズが一致しません")
					}
				default:
					return part{}, api.Err("unsafe_signed_header", "不正な署名ヘッダーを拒否しました")
				}
			}
			resp, e := c.HTTP.Do(req)
			if e != nil {
				if ctx.Err() != nil {
					return part{}, ctx.Err()
				}
				if attempt < 2 {
					if e = api.Backoff(ctx, attempt, ""); e != nil {
						return part{}, e
					}
					continue
				}
				return part{}, api.Err("part_upload_failed", "ストレージへの送信に失敗しました")
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				expired = true
				break
			}
			if api.Retryable(resp.StatusCode) && attempt < 2 {
				if e = api.Backoff(ctx, attempt, resp.Header.Get("Retry-After")); e != nil {
					return part{}, e
				}
				continue
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return part{}, &api.Error{Code: "part_upload_failed", Message: "ストレージへの送信に失敗しました", Status: resp.StatusCode}
			}
			etag := resp.Header.Get("ETag")
			if etag == "" {
				return part{}, api.Err("missing_etag", "ストレージがETagを返しませんでした")
			}
			if !strings.HasPrefix(etag, `"`) {
				etag = `"` + etag + `"`
			}
			record := part{n, etag}
			_, _, e = c.Do(ctx, "POST", base+"/parts/complete", map[string]any{"parts": []part{record}}, true)
			return record, e
		}
		if !expired {
			break
		}
	}
	return part{}, api.Err("part_upload_failed", "partの署名を更新しても送信できませんでした")
}
