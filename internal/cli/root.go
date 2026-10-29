package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"github.com/olduvai-jp/file-okiba-cli/internal/config"
	"github.com/olduvai-jp/file-okiba-cli/internal/transfer"
	"github.com/spf13/cobra"
)

var Version = "dev"

type IO struct {
	In          io.Reader
	Out         io.Writer
	Err         io.Writer
	Interactive bool
	ReadSecret  func(string) (string, error)
}
type app struct {
	ctx                                    context.Context
	io                                     IO
	reader                                 *bufio.Reader
	root                                   *cobra.Command
	configPath, profile, baseURL, groupURL string
	json, yes                              bool
	conf                                   config.Config
	settings                               config.Profile
}

func New(streams IO) *cobra.Command {
	if streams.In == nil {
		streams.In = strings.NewReader("")
	}
	if streams.Out == nil {
		streams.Out = io.Discard
	}
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	a := &app{io: streams, reader: bufio.NewReader(streams.In), ctx: context.Background()}
	root := &cobra.Command{Use: "file-okiba", Short: "file-okibaの転送・管理CLI", Long: "file-okiba: URLでファイルを受け渡し、管理トークンでグループを管理します。\n引数なしの端末実行で対話ウィザードを開きます。", Version: Version, SilenceUsage: true, SilenceErrors: true, RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return api.Err("usage", "不明なコマンドです")
		}
		if a.json {
			return api.Err("usage", "コマンドを指定してください")
		}
		if !a.io.Interactive {
			return cmd.Help()
		}
		return a.wizard(cmd.Context())
	}}
	a.root = root
	root.SetIn(streams.In)
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)
	root.CompletionOptions.DisableDefaultCmd = true
	f := root.PersistentFlags()
	f.StringVar(&a.configPath, "config", config.Path(), "設定ファイル")
	f.StringVar(&a.profile, "profile", "", "プロファイル名")
	f.StringVar(&a.baseURL, "base-url", "", "管理APIの接続先")
	f.StringVar(&a.groupURL, "group-url", "", "転送先グループURL")
	f.BoolVar(&a.json, "json", false, "機械処理用JSONを出力")
	f.BoolVarP(&a.yes, "yes", "y", false, "確認を承認（自動化用）")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error { return a.load(cmd) }
	root.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
		return api.Err("usage", "オプションが不正です。--help で確認してください")
	})
	root.AddCommand(a.profileCommand(), a.uploadCommand(), a.inspectCommand(), a.linksCommand(), a.downloadCommand(), a.listCommand(), a.groupCommand())
	root.AddCommand(&cobra.Command{Use: "wizard", Short: "対話ウィザード", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !a.io.Interactive {
			return api.Err("terminal_required", "ウィザードは端末で実行してください")
		}
		if a.json {
			return api.Err("usage", "ウィザードと --json は併用できません")
		}
		return a.wizard(cmd.Context())
	}})
	localize(root)
	return root
}

func (a *app) stateDir() string {
	if p := os.Getenv("FILE_OKIBA_STATE_DIR"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(a.configPath), "state")
}
func (a *app) load(cmd *cobra.Command) error {
	a.ctx = cmd.Context()
	c, e := config.Load(a.configPath)
	if e != nil {
		return e
	}
	a.conf = c
	if a.profile != "" {
		if _, ok := c.Profiles[a.profile]; !ok {
			return api.Err("profile_not_found", "指定プロファイルがありません")
		}
	}
	a.settings = c.Resolve(a.profile, os.Getenv)
	if a.baseURL != "" {
		a.settings.BaseURL = a.baseURL
	}
	if a.groupURL != "" {
		a.settings.GroupURL = a.groupURL
	}
	return nil
}
func (a *app) emit(v any) error {
	enc := json.NewEncoder(a.io.Out)
	enc.SetEscapeHTML(false)
	if !a.json {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}
func (a *app) confirm(message string) error {
	if a.yes {
		return nil
	}
	if !a.io.Interactive {
		return api.Err("confirmation_required", "非対話の変更操作には --yes が必要です")
	}
	s, e := a.prompt(message+" [y/N]", "")
	if e != nil {
		return e
	}
	if strings.ToLower(s) != "y" && strings.ToLower(s) != "yes" {
		return api.Err("cancelled", "操作を中止しました")
	}
	return nil
}
func (a *app) prompt(label, def string) (string, error) {
	if !a.io.Interactive {
		return "", api.Err("terminal_required", "対話入力には端末が必要です")
	}
	if def != "" {
		fmt.Fprintf(a.io.Err, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(a.io.Err, "%s: ", label)
	}
	type inputResult struct {
		value string
		err   error
	}
	ready := make(chan inputResult, 1)
	go func() { s, e := a.reader.ReadString('\n'); ready <- inputResult{s, e} }()
	var s string
	var e error
	select {
	case <-a.ctx.Done():
		return "", api.Err("cancelled", "入力を中止しました")
	case r := <-ready:
		s, e = r.value, r.err
	}
	if e != nil {
		return "", api.Err("cancelled", "入力を終了しました")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = def
	}
	return s, nil
}
func (a *app) secret(label string) (string, error) {
	if a.io.ReadSecret != nil {
		type secretResult struct {
			value string
			err   error
		}
		ready := make(chan secretResult, 1)
		go func() { s, e := a.io.ReadSecret(label); ready <- secretResult{s, e} }()
		select {
		case <-a.ctx.Done():
			return "", api.Err("cancelled", "入力を中止しました")
		case r := <-ready:
			return r.value, r.err
		}
	}
	return a.prompt(label, "")
}
func (a *app) client(admin bool) (*api.Client, error) {
	token := ""
	if admin {
		token = a.settings.AdminToken
		if token == "" || strings.ContainsAny(token, "\r\n") {
			return nil, api.Err("admin_token_required", "管理トークンを設定してください")
		}
	}
	c, e := api.New(a.settings.BaseURL, token)
	if e == nil && !admin {
		c.Password = os.Getenv("FILE_OKIBA_DOWNLOAD_PASSWORD")
	}
	return c, e
}
func (a *app) target(s string, batch bool) (*api.Client, api.Target, error) {
	if s == "" {
		s = a.settings.GroupURL
	}
	t, e := api.ParseTarget(s)
	if e != nil {
		return nil, t, e
	}
	if batch && t.BatchToken == "" {
		return nil, t, api.Err("batch_url_required", "バッチURLを指定してください")
	}
	c, e := api.New(t.Origin, "")
	if e == nil {
		c.Password = os.Getenv("FILE_OKIBA_DOWNLOAD_PASSWORD")
	}
	return c, t, e
}
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var e *api.Error
	if errors.As(err, &e) {
		if e.Details != nil {
			switch d := e.Details.(type) {
			case transfer.UploadResult:
				if len(d.Completed) > 0 {
					return 6
				}
			case transfer.DownloadResult:
				if len(d.Downloaded) > 0 {
					return 6
				}
			}
		}
		if e.Code == "cancelled" {
			return 130
		}
		if e.Code == "usage" || strings.HasPrefix(e.Code, "invalid_") {
			return 2
		}
		if e.Status == 401 || e.Status == 403 || e.Code == "admin_token_required" {
			return 3
		}
		if e.Code == "outcome_unknown" {
			return 5
		}
		if e.Code == "network_error" || e.Code == "part_upload_failed" || e.Code == "download_interrupted" {
			return 4
		}
		if e.Details != nil {
			switch d := e.Details.(type) {
			case transfer.UploadResult:
				if len(d.Completed) > 0 {
					return 6
				}
			case transfer.DownloadResult:
				if len(d.Downloaded) > 0 {
					return 6
				}
			}
		}
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}
func ReportError(w io.Writer, err error, jsonMode bool) {
	var e *api.Error
	if !errors.As(err, &e) {
		e = api.Err("command_failed", "操作に失敗しました。引数と設定、ファイルの権限を確認してください")
	}
	if jsonMode {
		json.NewEncoder(w).Encode(map[string]any{"error": e})
	} else {
		fmt.Fprintf(w, "エラー: %s\n", e.Error())
		if e.Details != nil {
			b, _ := json.MarshalIndent(e.Details, "", "  ")
			fmt.Fprintln(w, string(b))
		}
	}
}
