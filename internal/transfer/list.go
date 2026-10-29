package transfer

import (
	"context"
	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"net/url"
)

func ListBatches(ctx context.Context, c *api.Client, t api.Target) ([]map[string]any, error) {
	if t.BatchToken != "" {
		return nil, api.Err("group_url_required", "グループURLを指定してください")
	}
	items := []map[string]any{}
	cursor := ""
	seen := map[string]bool{}
	for {
		path := t.Path("/upload-batches") + "?limit=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		m, _, e := c.Do(ctx, "GET", path, nil, true)
		if e != nil {
			return nil, e
		}
		var page struct {
			Items []map[string]any `json:"items"`
			Next  string           `json:"nextCursor"`
		}
		if e = api.Decode(m, "", &page); e != nil {
			return nil, e
		}
		items = append(items, page.Items...)
		if page.Next == "" {
			return items, nil
		}
		if seen[page.Next] {
			return nil, api.Err("invalid_response", "一覧カーソルが循環しています")
		}
		seen[page.Next] = true
		cursor = page.Next
	}
}
