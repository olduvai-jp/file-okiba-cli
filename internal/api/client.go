package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var errorPattern = regexp.MustCompile(`^[a-z_]{1,80}$`)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status,omitempty"`
	Details any    `json:"details,omitempty"`
}

func (e *Error) Error() string        { return e.Message + " (" + e.Code + ")" }
func Err(code, message string) *Error { return &Error{Code: code, Message: message} }

type Client struct {
	Base     string
	Token    string
	HTTP     *http.Client
	Password string
}

func New(base, token string) (*Client, error) {
	origin, e := Origin(base)
	if e != nil {
		return nil, e
	}
	return &Client{Base: origin, Token: token, HTTP: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func Origin(s string) (string, error) {
	u, e := url.Parse(s)
	if e != nil || u == nil {
		return "", Err("invalid_url", "URLが不正です")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && local)) {
		return "", Err("invalid_url", "HTTPSの接続先を指定してください（localhostのみHTTP可）")
	}
	return u.Scheme + "://" + u.Host, nil
}

type Target struct {
	Origin     string
	GroupToken string
	BatchToken string
}

func ParseTarget(s string) (Target, error) {
	u, e := url.Parse(s)
	if e != nil || u == nil {
		return Target{}, Err("invalid_url", "共有URLが不正です")
	}
	base := *u
	base.Path = ""
	base.RawPath = ""
	o, e := Origin(base.String())
	if e != nil {
		return Target{}, e
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	if len(parts) != 3 && len(parts) != 5 {
		return Target{}, Err("invalid_url", "グループまたはバッチURLを指定してください")
	}
	if parts[0] != "" || parts[1] != "g" || !tokenPattern.MatchString(parts[2]) {
		return Target{}, Err("invalid_url", "共有URLの形式が不正です")
	}
	t := Target{Origin: o, GroupToken: parts[2]}
	if len(parts) == 5 {
		if parts[3] != "b" || !tokenPattern.MatchString(parts[4]) {
			return Target{}, Err("invalid_url", "バッチURLが不正です")
		}
		t.BatchToken = parts[4]
	}
	return t, nil
}
func (t Target) Path(suffix string) string {
	p := "/api/v1/groups/" + url.PathEscape(t.GroupToken)
	if t.BatchToken != "" {
		p += "/upload-batches/" + url.PathEscape(t.BatchToken)
	}
	return p + suffix
}
func ValidToken(s string) bool { return len(s) >= 1 && len(s) <= 128 && tokenPattern.MatchString(s) }
func (c *Client) Do(ctx context.Context, method, path string, payload any, retry bool) (map[string]any, int, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, 0, Err("invalid_url", "APIパスが不正です")
	}
	var b []byte
	var e error
	if payload != nil {
		b, e = json.Marshal(payload)
		if e != nil {
			return nil, 0, e
		}
	}
	attempts := 1
	if retry {
		attempts = 3
	}
	for i := 0; i < attempts; i++ {
		req, e := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(b))
		if e != nil {
			return nil, 0, Err("invalid_request", "リクエストが不正です")
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		if c.Password != "" {
			req.Header.Set("x-download-key", c.Password)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			if i+1 < attempts {
				if e = Backoff(ctx, i, ""); e != nil {
					return nil, 0, e
				}
				continue
			}
			code := "network_error"
			if method != "GET" && !retry {
				code = "outcome_unknown"
			}
			return nil, 0, Err(code, "通信に失敗しました。変更操作の成否は再実行前に確認してください")
		}
		data := map[string]any{}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&data)
		resp.Body.Close()
		if Retryable(resp.StatusCode) && i+1 < attempts {
			if e = Backoff(ctx, i, resp.Header.Get("Retry-After")); e != nil {
				return nil, 0, e
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			code := "request_failed"
			if v, ok := data["error"].(map[string]any); ok {
				if s, ok := v["code"].(string); ok && errorPattern.MatchString(s) {
					code = s
				}
			}
			if resp.StatusCode >= 500 && !retry && method != "GET" {
				code = "outcome_unknown"
			}
			return nil, resp.StatusCode, &Error{Code: code, Message: fmt.Sprintf("APIがHTTP %dを返しました", resp.StatusCode), Status: resp.StatusCode}
		}
		if decodeErr != nil || data == nil {
			code := "invalid_response"
			if method != "GET" && !retry {
				code = "outcome_unknown"
			}
			return nil, resp.StatusCode, Err(code, "APIのJSON応答が不正です")
		}
		return data, resp.StatusCode, nil
	}
	return nil, 0, Err("network_error", "通信に失敗しました")
}
func Retryable(s int) bool { return s == 408 || s == 429 || s >= 500 }
func Backoff(ctx context.Context, n int, header string) error {
	d := time.Duration(100*(1<<n)) * time.Millisecond
	if header != "" {
		if sec, e := time.ParseDuration(header + "s"); e == nil {
			d = sec
		} else if t, e := http.ParseTime(header); e == nil {
			d = time.Until(t)
		}
	}
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	if d < 0 {
		d = 0
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
func Decode(m map[string]any, key string, out any) error {
	var v any = m
	if item, ok := m[key]; ok {
		v = item
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, out); e != nil {
		return Err("invalid_response", "APIのデータ形式が不正です")
	}
	return nil
}

// ValidateStorageURL keeps capability and admin credentials away from untrusted endpoints.
func ValidateStorageURL(s string) error {
	u, e := url.Parse(s)
	if e != nil || u == nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || !strings.HasSuffix(u.Hostname(), ".r2.cloudflarestorage.com") {
		return Err("unsafe_storage_url", "信頼できないストレージURLを拒否しました")
	}
	return nil
}
