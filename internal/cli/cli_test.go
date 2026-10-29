package cli

import (
	"bytes"
	"context"
	"fmt"
	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"github.com/olduvai-jp/file-okiba-cli/internal/transfer"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := New(IO{In: strings.NewReader(""), Out: &out, Err: &errOut})
	c.SetArgs(args)
	e := c.ExecuteContext(context.Background())
	return out.String(), errOut.String(), e
}
func TestProfileCopyCLI(t *testing.T) {
	t.Setenv("FILE_OKIBA_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if _, _, e := run(t, "profile", "create", "a", "--group-url", "https://example.com/g/secret"); e != nil {
		t.Fatal(e)
	}
	if _, _, e := run(t, "profile", "create", "b", "--from", "a"); e != nil {
		t.Fatal(e)
	}
	s, _, e := run(t, "profile", "list", "--json")
	if e != nil || strings.Contains(s, "secret") || !strings.Contains(s, `"b"`) {
		t.Fatal(s, e)
	}
}
func TestNoArgsWithoutTerminalPrintsHelp(t *testing.T) {
	s, _, e := run(t)
	if e != nil || !strings.Contains(s, "upload") {
		t.Fatal(s, e)
	}
}
func TestAdminDeleteContinuationAndConfirm(t *testing.T) {
	deletes := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("auth missing")
		}
		if r.Method == "GET" {
			fmt.Fprint(w, `{"group":{"id":"abc","fileCount":2}}`)
			return
		}
		deletes++
		if deletes == 1 {
			w.WriteHeader(202)
			fmt.Fprint(w, `{"id":"abc","deleting":true}`)
		} else {
			fmt.Fprint(w, `{"id":"abc","deleted":true}`)
		}
	}))
	defer s.Close()
	t.Setenv("FILE_OKIBA_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("FILE_OKIBA_BASE_URL", s.URL)
	t.Setenv("FILE_OKIBA_ADMIN_TOKEN", "token")
	if _, _, e := run(t, "group", "delete", "abc", "--force"); e == nil || deletes != 0 {
		t.Fatal(e, deletes)
	}
	out, _, e := run(t, "group", "delete", "abc", "--force", "--yes", "--json")
	if e != nil || deletes != 2 || !strings.Contains(out, `"deleted":true`) {
		t.Fatal(out, e, deletes)
	}
}
func TestWizardProfile(t *testing.T) {
	t.Setenv("FILE_OKIBA_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	var out bytes.Buffer
	c := New(IO{In: strings.NewReader("1\na\n\n\nhttps://example.com/g/group\n\n7\n0\n"), Out: &out, Err: &out, Interactive: true})
	c.SetArgs([]string{})
	if e := c.Execute(); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "保存") {
		t.Fatal(out.String())
	}
}
func TestProfileFlagsAreSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("FILE_OKIBA_CONFIG", path)
	if _, _, e := run(t, "profile", "create", "a", "--base-url", "https://example.com", "--group-url", "https://example.com/g/first"); e != nil {
		t.Fatal(e)
	}
	if _, _, e := run(t, "profile", "create", "b", "--from", "a", "--group-url", "https://example.com/g/second"); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "first") || !strings.Contains(string(b), "second") {
		t.Fatal(string(b))
	}
}
func TestPartialFailureExitCode(t *testing.T) {
	e := &api.Error{Code: "network_error", Details: transfer.UploadResult{Completed: []transfer.FileResult{{FileToken: "done"}}}}
	if ExitCode(e) != 6 {
		t.Fatal(ExitCode(e))
	}
}
func TestWizardInputCancelledByContext(t *testing.T) {
	t.Setenv("FILE_OKIBA_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	c := New(IO{In: input, Out: io.Discard, Err: io.Discard, Interactive: true})
	c.SetArgs([]string{"wizard"})
	go func() { done <- c.ExecuteContext(ctx) }()
	cancel()
	select {
	case e := <-done:
		if ExitCode(e) != 130 {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("wizard ignored cancellation")
	}
}
