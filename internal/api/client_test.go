package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTargetAndOrigin(t *testing.T) {
	for _, s := range []string{"http://example.com/g/t", "https://user:pass@example.com/g/t", "https://example.com/g/a%2fb", "https://example.com/g/t?x=1"} {
		if _, e := ParseTarget(s); e == nil {
			t.Fatal(s)
		}
	}
	v, e := ParseTarget("https://example.com/g/shared/b/token")
	if e != nil || v.BatchToken != "token" || v.Path("/files") != "/api/v1/groups/shared/upload-batches/token/files" {
		t.Fatal(v, e)
	}
}
func TestNoRedirectAndNoCreateRetry(t *testing.T) {
	n := 0
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n++; w.Write([]byte(`{}`)) }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL, 307) }))
	defer src.Close()
	c, _ := New(src.URL, "secret")
	_, _, e := c.Do(context.Background(), "POST", "/create", map[string]any{}, false)
	if e == nil || n != 0 {
		t.Fatal(e, n)
	}
}
func TestErrorsAreConstrained(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"code":"bad\nsecret"}}`))
	}))
	defer s.Close()
	c, _ := New(s.URL, "")
	_, _, e := c.Do(context.Background(), "GET", "/", nil, false)
	var ae *Error
	if !errors.As(e, &ae) || ae.Status != 401 || ae.Code != "request_failed" {
		t.Fatal(e)
	}
}
