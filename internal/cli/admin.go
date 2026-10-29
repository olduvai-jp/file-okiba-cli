package cli

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"github.com/spf13/cobra"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,120}$`)

func validateIDs(ids []string) error {
	for _, s := range ids {
		if !idPattern.MatchString(s) {
			return api.Err("invalid_id", "固定の管理IDを指定してください")
		}
	}
	return nil
}

type adminOptions struct {
	Name   string
	Limit  int
	Cursor string
	Force  bool
}

func (a *app) groupCommand() *cobra.Command {
	group := &cobra.Command{Use: "group", Short: "管理トークンで全グループを管理"}
	for _, verb := range []string{"list", "create", "inspect", "update", "disable", "enable", "rotate-url", "delete"} {
		verb := verb
		o := adminOptions{}
		count := 1
		if verb == "list" || verb == "create" {
			count = 0
		}
		cmd := &cobra.Command{Use: verb + map[bool]string{true: " ID", false: ""}[count == 1], Short: adminDescription(verb), Args: cobra.ExactArgs(count), RunE: func(cmd *cobra.Command, args []string) error {
			if verb == "update" && !cmd.Flags().Changed("name") {
				return api.Err("usage", "--name を指定してください")
			}
			return a.admin(cmd.Context(), "", verb, args, o)
		}}
		adminFlags(cmd, verb, &o)
		group.AddCommand(cmd)
	}
	for _, kind := range []string{"files", "batches"} {
		kind := kind
		child := &cobra.Command{Use: kind, Short: map[string]string{"files": "ファイル管理", "batches": "バッチ管理"}[kind]}
		for _, verb := range []string{"list", "inspect", "delete"} {
			verb := verb
			o := adminOptions{}
			count := 2
			use := " GROUP_ID ITEM_ID"
			if verb == "list" {
				count = 1
				use = " GROUP_ID"
			}
			cmd := &cobra.Command{Use: verb + use, Short: adminDescription(verb), Args: cobra.ExactArgs(count), RunE: func(cmd *cobra.Command, args []string) error { return a.admin(cmd.Context(), kind, verb, args, o) }}
			adminFlags(cmd, verb, &o)
			child.AddCommand(cmd)
		}
		group.AddCommand(child)
	}
	return group
}
func adminDescription(v string) string {
	return map[string]string{"list": "一覧取得", "create": "グループ作成", "inspect": "詳細取得", "update": "名前変更", "disable": "利用を無効化", "enable": "利用を再開", "rotate-url": "グループURLを再発行（旧URLを無効化）", "delete": "完全削除（復元不可）"}[v]
}
func adminFlags(cmd *cobra.Command, v string, o *adminOptions) {
	if v == "create" || v == "update" {
		cmd.Flags().StringVar(&o.Name, "name", "", "名前（80文字以内）")
	}
	if v == "list" {
		cmd.Flags().IntVar(&o.Limit, "limit", 50, "取得件数（1〜200）")
		cmd.Flags().StringVar(&o.Cursor, "cursor", "", "次ページのカーソル")
	}
	if v == "delete" {
		cmd.Flags().BoolVar(&o.Force, "force", false, "中身のあるグループの削除を許可")
	}
}
func (a *app) admin(ctx context.Context, kind, verb string, ids []string, o adminOptions) error {
	if e := validateIDs(ids); e != nil {
		return e
	}
	c, e := a.client(true)
	if e != nil {
		return e
	}
	p := "/api/v1/admin/groups"
	method := "GET"
	var payload any
	if len(ids) > 0 {
		p += "/" + ids[0]
	}
	if kind != "" {
		p += "/" + kind
		if len(ids) > 1 {
			p += "/" + ids[1]
		}
	}
	if verb == "list" {
		if o.Limit < 1 || o.Limit > 200 {
			return api.Err("invalid_limit", "limitは1〜200です")
		}
		p += fmt.Sprintf("?limit=%d", o.Limit)
		if o.Cursor != "" {
			if e = validateIDs([]string{o.Cursor}); e != nil {
				return e
			}
			p += "&cursor=" + url.QueryEscape(o.Cursor)
		}
	}
	if verb == "create" || verb == "update" {
		if len([]rune(o.Name)) > 80 {
			return api.Err("invalid_name", "名前は80文字以内です")
		}
		payload = map[string]any{"name": o.Name}
		method = "POST"
		if verb == "update" {
			method = "PATCH"
		}
	}
	if verb == "disable" || verb == "enable" || verb == "rotate-url" {
		p += "/" + verb
		payload = map[string]any{}
		method = "POST"
		if verb != "enable" {
			fmt.Fprintf(a.io.Err, "接続先: %s / 対象ID: %s / 操作: %s\n", c.Base, ids[0], verb)
			if e = a.confirm("この変更を実行しますか"); e != nil {
				return e
			}
		}
	}
	if verb == "delete" {
		preview, status, e := c.Do(ctx, "GET", p, nil, true)
		if e != nil {
			if status == 404 {
				return a.emit(map[string]any{"deleted": true, "id": ids[len(ids)-1]})
			}
			return e
		}
		key := "group"
		if kind == "files" {
			key = "file"
		} else if kind == "batches" {
			key = "batch"
		}
		target, ok := preview[key].(map[string]any)
		if !ok || target["id"] != ids[len(ids)-1] {
			return api.Err("invalid_response", "削除対象の応答が一致しません")
		}
		filtered := map[string]any{"origin": c.Base}
		for _, k := range []string{"id", "groupId", "batchId", "name", "status", "fileCount", "batchCount", "sizeBytes", "totalSizeBytes", "deletingAt"} {
			if v, ok := target[k]; ok {
				filtered[k] = v
			}
		}
		fmt.Fprintf(a.io.Err, "削除対象: %v\n", filtered)
		if kind == "" {
			if n, ok := target["fileCount"].(float64); ok && n > 0 && !o.Force {
				return api.Err("group_not_empty", "中身のあるグループの削除には --force が必要です")
			}
		}
		if e = a.confirm("完全削除しますか"); e != nil {
			return e
		}
		method = "DELETE"
		if o.Force {
			p += "?force=true"
		}
	}
	m, status, e := c.Do(ctx, method, p, payload, method == "GET")
	if e != nil {
		return e
	}
	for verb == "delete" && status == 202 {
		if m["deleting"] != true || m["id"] != ids[len(ids)-1] {
			return api.Err("invalid_response", "削除継続の応答が一致しません")
		}
		fmt.Fprintln(a.io.Err, "削除処理を継続しています")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		m, status, e = c.Do(ctx, method, p, nil, false)
		if e != nil {
			return e
		}
	}
	if verb == "delete" && m["deleted"] != true {
		return api.Err("invalid_response", "削除完了を確認できませんでした")
	}
	return a.emit(m)
}
