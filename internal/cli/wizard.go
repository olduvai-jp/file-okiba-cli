package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"github.com/olduvai-jp/file-okiba-cli/internal/config"
	"github.com/olduvai-jp/file-okiba-cli/internal/transfer"
)

func (a *app) wizard(ctx context.Context) error {
	for {
		fmt.Fprintln(a.io.Err, "\nfile-okiba\n  1. プロファイル登録・コピー\n  2. プロファイル切り替え\n  3. アップロード\n  4. ダウンロード\n  5. 詳細・一覧・リンク\n  6. グループ管理\n  0. 終了")
		choice, e := a.prompt("操作", "")
		if e != nil {
			return e
		}
		if choice == "0" {
			return nil
		}
		switch choice {
		case "1":
			e = a.wizardProfile()
		case "2":
			e = a.wizardUse()
		case "3":
			e = a.wizardUpload(ctx)
		case "4":
			e = a.wizardDownload(ctx)
		case "5":
			e = a.wizardInspect(ctx)
		case "6":
			e = a.wizardAdmin(ctx)
		default:
			fmt.Fprintln(a.io.Err, "番号を選んでください")
			continue
		}
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			ReportError(a.io.Err, e, false)
		}
		a.settings = a.conf.Resolve(a.profile, os.Getenv)
		if a.baseURL != "" {
			a.settings.BaseURL = a.baseURL
		}
		if a.groupURL != "" {
			a.settings.GroupURL = a.groupURL
		}
	}
}
func (a *app) profileNames() []string {
	names := []string{}
	for n := range a.conf.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
func (a *app) wizardProfile() error {
	name, e := a.prompt("新しいプロファイル名", "")
	if e != nil {
		return e
	}
	fmt.Fprintf(a.io.Err, "既存: %s\n", strings.Join(a.profileNames(), ", "))
	from, e := a.prompt("コピー元（空欄なら新規）", "")
	if e != nil {
		return e
	}
	c := a.conf
	if c.Profiles == nil {
		c.Profiles = map[string]config.Profile{}
	}
	profiles := map[string]config.Profile{}
	for n, p := range c.Profiles {
		profiles[n] = p
	}
	c.Profiles = profiles
	if e = c.Copy(name, from); e != nil {
		return e
	}
	p := c.Profiles[name]
	base, e := a.prompt("接続先（空欄で引き継ぎ）", p.BaseURL)
	if e != nil {
		return e
	}
	p.BaseURL = base
	group, e := a.prompt("グループURL（空欄で引き継ぎ、-で削除）", "")
	if e != nil {
		return e
	}
	if group == "-" {
		p.GroupURL = ""
	} else if group != "" {
		p.GroupURL = group
	}
	token, e := a.secret("管理トークン（空欄で引き継ぎ、-で削除）")
	if e != nil {
		return e
	}
	if token == "-" {
		p.AdminToken = ""
	} else if token != "" {
		p.AdminToken = token
	}
	days, e := a.prompt("保持日数", strconv.Itoa(p.RetentionDays))
	if e != nil {
		return e
	}
	p.RetentionDays, e = strconv.Atoi(days)
	if e != nil {
		return api.Err("invalid_retention", "保持日数は数値です")
	}
	if e = validateProfile(p); e != nil {
		return e
	}
	c.Profiles[name] = p
	if e = config.Save(a.configPath, c); e != nil {
		return e
	}
	a.conf = c
	fmt.Fprintln(a.io.Err, "プロファイルを保存しました")
	return nil
}
func (a *app) wizardUse() error {
	fmt.Fprintf(a.io.Err, "プロファイル: %s\n", strings.Join(a.profileNames(), ", "))
	name, e := a.prompt("使用する名前", a.conf.Default)
	if e != nil {
		return e
	}
	if _, ok := a.conf.Profiles[name]; !ok {
		return api.Err("profile_not_found", "プロファイルがありません")
	}
	a.conf.Default = name
	a.profile = name
	return config.Save(a.configPath, a.conf)
}
func (a *app) wizardUpload(ctx context.Context) error {
	group, e := a.prompt("グループURL（空欄で設定値を使用）", "")
	if e != nil {
		return e
	}
	c, t, e := a.target(group, false)
	if e != nil {
		return e
	}
	paths := []string{}
	for {
		p, e := a.prompt("ファイルのパス（1つずつ入力、空欄で確定）", "")
		if e != nil {
			return e
		}
		if p == "" {
			break
		}
		paths = append(paths, strings.Trim(p, `"`))
	}
	if len(paths) == 0 {
		return api.Err("usage", "ファイルが選択されていません")
	}
	days, e := a.prompt("保持日数", strconv.Itoa(a.settings.RetentionDays))
	if e != nil {
		return e
	}
	n, e := strconv.Atoi(days)
	if e != nil {
		return api.Err("invalid_retention", "保持日数が不正です")
	}
	password, e := a.secret("ダウンロードパスワード（空欄なら環境変数の値）")
	if e != nil {
		return e
	}
	if password != "" {
		c.Password = password
	}
	fmt.Fprintf(a.io.Err, "接続先: %s / ファイル数: %d / 保持: %d日\n", t.Origin, len(paths), n)
	if e = a.confirm("アップロードしますか"); e != nil {
		return e
	}
	options := transfer.UploadOptions{StateDir: a.stateDir(), RetentionDays: n, Concurrency: 1, Progress: a.io.Err}
	r, e := transfer.Upload(ctx, c, t, paths, options)
	if e != nil {
		if ae, ok := e.(*api.Error); ok && (ae.Status == 410 || ae.Code == "session_expired") {
			if confirmErr := a.confirm("再開期限切れです。新しいバッチで再送しますか"); confirmErr != nil {
				return confirmErr
			}
			options.Restart = true
			r, e = transfer.Upload(ctx, c, t, paths, options)
		}
	}
	if e != nil {
		return e
	}
	return a.emit(r)
}
func (a *app) wizardDownload(ctx context.Context) error {
	raw, e := a.prompt("バッチURL", "")
	if e != nil {
		return e
	}
	c, t, e := a.target(raw, true)
	if e != nil {
		return e
	}
	password, e := a.secret("ダウンロードパスワード（空欄なら環境変数の値）")
	if e != nil {
		return e
	}
	if password != "" {
		c.Password = password
	}
	files, e := transfer.ListFiles(ctx, c, t)
	if e != nil {
		return e
	}
	for i, f := range files {
		fmt.Fprintf(a.io.Err, "%d. %q (%d bytes)\n", i+1, f.FileName, f.SizeBytes)
	}
	selection, e := a.prompt("取得する番号（カンマ区切り、空欄で全件）", "")
	if e != nil {
		return e
	}
	tokens := []string{}
	if selection != "" {
		for _, s := range strings.Split(selection, ",") {
			n, e := strconv.Atoi(strings.TrimSpace(s))
			if e != nil || n < 1 || n > len(files) {
				return api.Err("usage", "選択番号が不正です")
			}
			tokens = append(tokens, files[n-1].FileToken)
		}
	}
	out, e := a.prompt("保存先", ".")
	if e != nil {
		return e
	}
	existing, e := a.prompt("同名ファイル: rename/skip/overwrite/error", "rename")
	if e != nil {
		return e
	}
	r, e := transfer.Download(ctx, c, t, transfer.DownloadOptions{Output: out, Existing: existing, FileTokens: tokens, Progress: a.io.Err})
	if e != nil {
		return e
	}
	return a.emit(r)
}
func (a *app) wizardInspect(ctx context.Context) error {
	raw, e := a.prompt("グループ／バッチURL（空欄で設定値）", "")
	if e != nil {
		return e
	}
	c, t, e := a.target(raw, false)
	if e != nil {
		return e
	}
	action, e := a.prompt("操作: inspect/list/links", "inspect")
	if e != nil {
		return e
	}
	switch action {
	case "links":
		f, e := transfer.Links(ctx, c, t, nil)
		if e != nil {
			return e
		}
		return a.emit(map[string]any{"files": f})
	case "list":
		if t.BatchToken != "" {
			f, e := transfer.ListFiles(ctx, c, t)
			if e != nil {
				return e
			}
			return a.emit(map[string]any{"items": f})
		}
		m, e := transfer.ListBatches(ctx, c, t)
		if e != nil {
			return e
		}
		return a.emit(map[string]any{"items": m})
	case "inspect":
		m, _, e := c.Do(ctx, "GET", t.Path(""), nil, true)
		if e != nil {
			return e
		}
		return a.emit(m)
	default:
		return api.Err("usage", "操作が不正です")
	}
}
func (a *app) wizardAdmin(ctx context.Context) error {
	kind, e := a.prompt("対象: groups/files/batches", "groups")
	if e != nil {
		return e
	}
	if kind != "groups" && kind != "files" && kind != "batches" {
		return api.Err("usage", "対象が不正です")
	}
	allowed := []string{"list", "inspect", "delete"}
	if kind == "groups" {
		allowed = []string{"list", "create", "inspect", "update", "disable", "enable", "rotate-url", "delete"}
	}
	verb, e := a.prompt("操作: "+strings.Join(allowed, "/"), "list")
	if e != nil {
		return e
	}
	valid := false
	for _, v := range allowed {
		if v == verb {
			valid = true
		}
	}
	if !valid {
		return api.Err("usage", "操作が不正です")
	}
	ids := []string{}
	o := adminOptions{Limit: 50}
	if kind != "groups" || verb != "list" && verb != "create" {
		s, e := a.prompt("グループID", "")
		if e != nil {
			return e
		}
		ids = append(ids, s)
	}
	if kind != "groups" && verb != "list" {
		s, e := a.prompt("ファイル／バッチID", "")
		if e != nil {
			return e
		}
		ids = append(ids, s)
	}
	if verb == "create" || verb == "update" {
		o.Name, e = a.prompt("名前", "")
		if e != nil {
			return e
		}
	}
	if verb == "delete" && kind == "groups" {
		force, e := a.prompt("中身の削除も許可しますか [y/N]", "")
		if e != nil {
			return e
		}
		o.Force = strings.ToLower(force) == "y"
	}
	if verb == "list" {
		o.Cursor, e = a.prompt("カーソル（空欄で先頭）", "")
		if e != nil {
			return e
		}
	}
	if kind == "groups" {
		kind = ""
	}
	return a.admin(ctx, kind, verb, ids, o)
}
