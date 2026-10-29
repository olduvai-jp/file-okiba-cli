package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"github.com/olduvai-jp/file-okiba-cli/internal/config"
	"github.com/olduvai-jp/file-okiba-cli/internal/transfer"
	"github.com/spf13/cobra"
)

func (a *app) profileCommand() *cobra.Command {
	p := &cobra.Command{Use: "profile", Short: "接続設定の登録・コピー・切り替え"}
	p.AddCommand(&cobra.Command{Use: "list", Short: "秘密を伏せて設定を表示", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		var v any
		json.Unmarshal(a.conf.Redacted(), &v)
		return a.emit(v)
	}})
	for _, verb := range []string{"create", "update"} {
		verb := verb
		var from, tokenEnv string
		var retention int
		cmd := &cobra.Command{Use: verb + " NAME", Short: map[string]string{"create": "設定を登録（--fromでコピー）", "update": "設定を変更"}[verb], Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" || strings.ContainsAny(args[0], "\r\n\t") {
				return api.Err("invalid_name", "プロファイル名が不正です")
			}
			if verb == "create" {
				if e := a.conf.Copy(args[0], from); e != nil {
					return e
				}
			} else {
				if _, ok := a.conf.Profiles[args[0]]; !ok {
					return api.Err("profile_not_found", "プロファイルがありません")
				}
			}
			profile := a.conf.Profiles[args[0]]
			if cmd.Flags().Changed("base-url") {
				profile.BaseURL = a.baseURL
			}
			if cmd.Flags().Changed("group-url") {
				profile.GroupURL = a.groupURL
			}
			if cmd.Flags().Changed("retention-days") {
				profile.RetentionDays = retention
			}
			if tokenEnv != "" {
				profile.AdminToken = os.Getenv(tokenEnv)
				if profile.AdminToken == "" {
					return api.Err("admin_token_required", "指定環境変数に管理トークンがありません")
				}
			}
			if e := validateProfile(profile); e != nil {
				return e
			}
			a.conf.Profiles[args[0]] = profile
			if e := config.Save(a.configPath, a.conf); e != nil {
				return e
			}
			return a.emit(map[string]any{"saved": args[0], "default": a.conf.Default})
		}}
		if verb == "create" {
			cmd.Flags().StringVar(&from, "from", "", "既存プロファイルからコピー")
		}
		cmd.Flags().StringVar(&tokenEnv, "admin-token-env", "", "管理トークンを読む環境変数名")
		cmd.Flags().IntVar(&retention, "retention-days", 7, "既定の保持日数")
		p.AddCommand(cmd)
	}
	p.AddCommand(&cobra.Command{Use: "use NAME", Short: "既定プロファイルを選択", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if _, ok := a.conf.Profiles[args[0]]; !ok {
			return api.Err("profile_not_found", "プロファイルがありません")
		}
		a.conf.Default = args[0]
		if e := config.Save(a.configPath, a.conf); e != nil {
			return e
		}
		return a.emit(map[string]any{"default": args[0]})
	}})
	p.AddCommand(&cobra.Command{Use: "delete NAME", Short: "保存済み設定を削除", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if _, ok := a.conf.Profiles[args[0]]; !ok {
			return api.Err("profile_not_found", "プロファイルがありません")
		}
		if e := a.confirm(fmt.Sprintf("プロファイル %q を削除しますか", args[0])); e != nil {
			return e
		}
		delete(a.conf.Profiles, args[0])
		if a.conf.Default == args[0] {
			a.conf.Default = ""
		}
		if e := config.Save(a.configPath, a.conf); e != nil {
			return e
		}
		return a.emit(map[string]any{"deleted": args[0]})
	}})
	return p
}
func validateProfile(p config.Profile) error {
	if _, e := api.Origin(p.BaseURL); e != nil {
		return e
	}
	if p.GroupURL != "" {
		t, e := api.ParseTarget(p.GroupURL)
		if e != nil {
			return e
		}
		if t.BatchToken != "" {
			return api.Err("group_url_required", "プロファイルにはグループURLを指定してください")
		}
	}
	if !transfer.ValidRetention(p.RetentionDays) {
		return api.Err("invalid_retention", "保持日数が不正です")
	}
	if strings.ContainsAny(p.AdminToken, "\r\n") {
		return api.Err("invalid_token", "管理トークンが不正です")
	}
	return nil
}
