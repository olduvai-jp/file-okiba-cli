package cli

import (
	"github.com/olduvai-jp/file-okiba-cli/internal/api"

	"github.com/olduvai-jp/file-okiba-cli/internal/transfer"
	"github.com/spf13/cobra"
	"strings"
)

func (a *app) inspectCommand() *cobra.Command {
	return &cobra.Command{Use: "inspect [URL]", Short: "グループまたはバッチの詳細", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s := ""
		if len(args) > 0 {
			s = args[0]
		}
		c, t, e := a.target(s, false)
		if e != nil {
			return e
		}
		m, _, e := c.Do(cmd.Context(), "GET", t.Path(""), nil, true)
		if e != nil {
			return e
		}
		return a.emit(m)
	}}
}
func (a *app) listCommand() *cobra.Command {
	return &cobra.Command{Use: "list [URL]", Short: "グループのバッチ一覧／バッチのファイル一覧", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s := ""
		if len(args) > 0 {
			s = args[0]
		}
		c, t, e := a.target(s, false)
		if e != nil {
			return e
		}
		if t.BatchToken != "" {
			files, e := transfer.ListFiles(cmd.Context(), c, t)
			if e != nil {
				return e
			}
			return a.emit(map[string]any{"items": files})
		}
		m, e := transfer.ListBatches(cmd.Context(), c, t)
		if e != nil {
			return e
		}
		return a.emit(map[string]any{"items": m})
	}}
}
func (a *app) uploadCommand() *cobra.Command {
	var retention, concurrency int
	var stateDir string
	var restart bool
	cmd := &cobra.Command{Use: "upload [GROUP_URL] FILE...", Short: "ファイルを送信（同じコマンドで中断再開）", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s := ""
		if strings.HasPrefix(args[0], "https://") || strings.HasPrefix(args[0], "http://") {
			s = args[0]
			args = args[1:]
		}
		c, t, e := a.target(s, false)
		if e != nil {
			return e
		}
		if !cmd.Flags().Changed("retention-days") {
			retention = a.settings.RetentionDays
		}
		if restart {
			if e = a.confirm("保存済み状態を使わず、新しいバッチとして送信しますか"); e != nil {
				return e
			}
		}
		if stateDir == "" {
			stateDir = a.stateDir()
		}
		r, e := transfer.Upload(cmd.Context(), c, t, args, transfer.UploadOptions{StateDir: stateDir, RetentionDays: retention, Concurrency: concurrency, Restart: restart, Progress: a.io.Err})
		if e != nil {
			return e
		}
		return a.emit(r)
	}}
	cmd.Flags().IntVar(&retention, "retention-days", 7, "保持日数")
	cmd.Flags().IntVar(&concurrency, "concurrency", 1, "part並列数（1〜32、サーバー上限以内）")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "アップロード状態の保存先")
	cmd.Flags().BoolVar(&restart, "restart", false, "再開せず新規送信（要確認）")
	return cmd
}
func (a *app) linksCommand() *cobra.Command {
	var tokens []string
	cmd := &cobra.Command{Use: "links BATCH_URL", Short: "期限付きダウンロードURLを取得", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, t, e := a.target(args[0], true)
		if e != nil {
			return e
		}
		files, e := transfer.Links(cmd.Context(), c, t, tokens)
		if e != nil {
			return e
		}
		return a.emit(map[string]any{"files": files})
	}}
	cmd.Flags().StringSliceVar(&tokens, "file", nil, "ファイルIDを選択（複数指定可）")
	return cmd
}
func (a *app) downloadCommand() *cobra.Command {
	var out, existing string
	var tokens []string
	cmd := &cobra.Command{Use: "download BATCH_URL", Short: "バッチ全件または選択ファイルを取得", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, t, e := a.target(args[0], true)
		if e != nil {
			return e
		}
		r, e := transfer.Download(cmd.Context(), c, t, transfer.DownloadOptions{Output: out, Existing: existing, FileTokens: tokens, Progress: a.io.Err})
		if e != nil {
			return e
		}
		return a.emit(r)
	}}
	cmd.Flags().StringVarP(&out, "output", "o", ".", "保存先ディレクトリ")
	cmd.Flags().StringVar(&existing, "existing", "rename", "同名ファイル: rename|skip|overwrite|error")
	cmd.Flags().StringSliceVar(&tokens, "file", nil, "ファイルIDを選択（複数指定可）")
	return cmd
}

var _ = api.Err
