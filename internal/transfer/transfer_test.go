package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/olduvai-jp/file-okiba-cli/internal/api"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type routeStorage struct {
	base string
	next http.RoundTripper
}

func (r routeStorage) RoundTrip(q *http.Request) (*http.Response, error) {
	if q.URL.Host == "test.r2.cloudflarestorage.com" {
		v := q.Clone(q.Context())
		u := *q.URL
		v.URL = &u
		v.URL.Scheme = "http"
		v.URL.Host = strings.TrimPrefix(r.base, "http://")
		return r.next.RoundTrip(v)
	}
	return r.next.RoundTrip(q)
}
func TestMultipartResumeAfterInterruptedTransfer(t *testing.T) {
	data := []byte("abcdefghi")
	parts := map[int]string{}
	creates := 0
	fail := true
	ready := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/store":
			b, _ := io.ReadAll(r.Body)
			if r.URL.Query().Get("n") == "2" && fail {
				w.WriteHeader(400)
				return
			}
			n := 1
			fmt.Sscan(r.URL.Query().Get("n"), &n)
			parts[n] = string(b)
			w.Header().Set("ETag", fmt.Sprintf(`"%d"`, n))
			return
		case strings.HasSuffix(r.URL.Path, "/upload-batches"):
			creates++
			fmt.Fprint(w, `{"batch":{"token":"batch"}}`)
		case strings.HasSuffix(r.URL.Path, "/uploads"):
			creates++
			fmt.Fprint(w, `{"upload":{"sessionToken":"session","fileToken":"file","partSizeBytes":3,"totalParts":3,"maxConcurrency":4}}`)
		case strings.HasSuffix(r.URL.Path, "/parts/complete"):
			fmt.Fprint(w, `{}`)
		case strings.HasSuffix(r.URL.Path, "/parts"):
			var q struct {
				Parts []struct {
					PartNumber int `json:"partNumber"`
				}
			}
			json.NewDecoder(r.Body).Decode(&q)
			fmt.Fprintf(w, `{"parts":[{"partNumber":%d,"method":"PUT","url":"https://test.r2.cloudflarestorage.com/store?n=%d","headers":{"content-type":"application/octet-stream"}}]}`, q.Parts[0].PartNumber, q.Parts[0].PartNumber)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			ready = true
			fmt.Fprint(w, `{"fileToken":"file"}`)
		default:
			status := "uploading"
			if ready {
				status = "ready"
			}
			done := []map[string]any{}
			for n := range parts {
				done = append(done, map[string]any{"partNumber": n, "etag": fmt.Sprintf(`"%d"`, n)})
			}
			json.NewEncoder(w).Encode(map[string]any{"upload": map[string]any{"sessionToken": "session", "fileToken": "file", "size": 9, "partSizeBytes": 3, "totalParts": 3, "status": status, "uploadedParts": done}})
		}
	}))
	defer s.Close()
	c, _ := api.New(s.URL, "")
	c.HTTP.Transport = routeStorage{s.URL, http.DefaultTransport}
	target, _ := api.ParseTarget(s.URL + "/g/group")
	dir := t.TempDir()
	p := filepath.Join(dir, "input")
	os.WriteFile(p, data, 0600)
	o := UploadOptions{StateDir: filepath.Join(dir, "state"), RetentionDays: 7, Concurrency: 1}
	if _, e := Upload(context.Background(), c, target, []string{p}, o); e == nil {
		t.Fatal("expected interrupted transfer")
	}
	fail = false
	got, e := Upload(context.Background(), c, target, []string{p}, o)
	if e != nil {
		t.Fatal(e)
	}
	if creates != 2 || len(got.Completed) != 1 || parts[1]+parts[2]+parts[3] != string(data) {
		t.Fatal(creates, got, parts)
	}
}
func TestRejectInvalidInputsBeforeBatchCreation(t *testing.T) {
	c, _ := api.New("http://localhost:1", "")
	for _, p := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if _, e := Upload(context.Background(), c, api.Target{}, []string{p}, UploadOptions{StateDir: t.TempDir(), RetentionDays: 7}); e == nil {
			t.Fatal(p)
		}
	}
}
func TestDownloadCollisionPolicies(t *testing.T) {
	for _, policy := range []string{"rename", "skip", "overwrite", "error"} {
		t.Run(policy, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/store" {
					w.Header().Set("ETag", `"tag"`)
					fmt.Fprint(w, "new")
					return
				}
				if strings.HasSuffix(r.URL.Path, "download-urls") {
					fmt.Fprint(w, `{"files":[{"fileToken":"file","name":"a.txt","sizeBytes":3,"url":"https://test.r2.cloudflarestorage.com/store"}]}`)
					return
				}
				fmt.Fprint(w, `{"items":[{"fileToken":"file"}],"nextCursor":null}`)
			}))
			defer s.Close()
			c, _ := api.New(s.URL, "")
			c.HTTP.Transport = routeStorage{s.URL, http.DefaultTransport}
			target, _ := api.ParseTarget(s.URL + "/g/shared/b/batch")
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old"), 0600)
			got, e := Download(context.Background(), c, target, DownloadOptions{Output: dir, Existing: policy})
			if policy == "error" {
				if e == nil {
					t.Fatal("expected conflict")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
			if policy == "overwrite" {
				if string(b) != "new" {
					t.Fatal(string(b))
				}
			} else if string(b) != "old" {
				t.Fatal("overwrote existing")
			}
			if policy == "rename" && (len(got.Downloaded) != 1 || got.Downloaded[0].Path == filepath.Join(dir, "a.txt")) {
				t.Fatal(got)
			}
		})
	}
}
func TestSafeName(t *testing.T) {
	for _, s := range []string{"../x", "C:\\x", "/x", "CON", "a\x1bb", ".."} {
		if _, e := safeName(s); e == nil {
			t.Fatal(s)
		}
	}
}
func TestBatchOutcomeUnknownNeverRecreates(t *testing.T) {
	calls := atomic.Int32{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		h := w.(http.Hijacker)
		conn, _, _ := h.Hijack()
		conn.Close()
	}))
	defer s.Close()
	c, _ := api.New(s.URL, "")
	target, _ := api.ParseTarget(s.URL + "/g/group")
	p := filepath.Join(t.TempDir(), "data")
	os.WriteFile(p, []byte("x"), 0600)
	o := UploadOptions{StateDir: t.TempDir(), RetentionDays: 7, Concurrency: 1}
	for i := 0; i < 2; i++ {
		_, e := Upload(context.Background(), c, target, []string{p}, o)
		if e == nil {
			t.Fatal("unknown result accepted")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("recreated batch", calls.Load())
	}
}
func TestDownloadResumesWithIfRange(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/store" {
			requests++
			w.Header().Set("ETag", `"tag"`)
			if requests == 1 {
				w.Header().Set("Content-Length", "6")
				w.Write([]byte("abc"))
				return
			}
			if r.Header.Get("Range") != "bytes=3-" || r.Header.Get("If-Range") != `"tag"` {
				t.Errorf("incorrect range: %v", r.Header)
			}
			w.Header().Set("Content-Range", "bytes 3-5/6")
			w.WriteHeader(206)
			w.Write([]byte("def"))
			return
		}
		if strings.HasSuffix(r.URL.Path, "download-urls") {
			fmt.Fprint(w, `{"files":[{"fileToken":"file","fileName":"a","sizeBytes":6,"url":"https://test.r2.cloudflarestorage.com/store"}]}`)
			return
		}
		fmt.Fprint(w, `{"items":[{"fileToken":"file"}],"nextCursor":null}`)
	}))
	defer s.Close()
	c, _ := api.New(s.URL, "")
	c.HTTP.Transport = routeStorage{s.URL, http.DefaultTransport}
	target, _ := api.ParseTarget(s.URL + "/g/shared/b/batch")
	out := t.TempDir()
	o := DownloadOptions{Output: out}
	if _, e := Download(context.Background(), c, target, o); e == nil {
		t.Fatal("expected interruption")
	}
	if _, e := Download(context.Background(), c, target, o); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(out, "a"))
	if string(b) != "abcdef" || requests != 2 {
		t.Fatal(string(b), requests)
	}
}

func TestDetectedSourceMutationCannotResumeAfterRestoringOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source")
	os.WriteFile(path, []byte("abcdef"), 0600)
	orig, _ := os.Stat(path)
	parts := map[int]string{}
	mutated := false
	completed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/store":
			n := 1
			fmt.Sscan(r.URL.Query().Get("n"), &n)
			b, _ := io.ReadAll(r.Body)
			parts[n] = string(b)
			if !mutated {
				os.WriteFile(path, []byte("abcXYZ"), 0600)
				os.Chtimes(path, orig.ModTime().Add(1000000000), orig.ModTime().Add(1000000000))
				mutated = true
			}
			w.Header().Set("ETag", fmt.Sprintf(`"%d"`, n))
		case strings.HasSuffix(r.URL.Path, "/upload-batches"):
			fmt.Fprint(w, `{"batch":{"token":"batch"}}`)
		case strings.HasSuffix(r.URL.Path, "/uploads"):
			fmt.Fprint(w, `{"upload":{"sessionToken":"session","fileToken":"file","partSizeBytes":3,"totalParts":2}}`)
		case strings.HasSuffix(r.URL.Path, "/parts/complete"):
			fmt.Fprint(w, `{}`)
		case strings.HasSuffix(r.URL.Path, "/parts"):
			var q struct {
				Parts []struct {
					PartNumber int `json:"partNumber"`
				}
			}
			json.NewDecoder(r.Body).Decode(&q)
			fmt.Fprintf(w, `{"parts":[{"partNumber":%d,"method":"PUT","url":"https://test.r2.cloudflarestorage.com/store?n=%d"}]}`, q.Parts[0].PartNumber, q.Parts[0].PartNumber)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			completed = true
			fmt.Fprint(w, `{"ok":true}`)
		default:
			fmt.Fprint(w, `{"upload":{"sessionToken":"session","fileToken":"file","sizeBytes":6,"partSizeBytes":3,"totalParts":2,"status":"uploading","uploadedParts":[{"partNumber":1,"etag":"one"},{"partNumber":2,"etag":"two"}]}}`)
		}
	}))
	defer server.Close()
	c, _ := api.New(server.URL, "")
	c.HTTP.Transport = routeStorage{server.URL, http.DefaultTransport}
	target, _ := api.ParseTarget(server.URL + "/g/group")
	o := UploadOptions{StateDir: filepath.Join(dir, "state"), RetentionDays: 7, Concurrency: 1}
	_, err := Upload(context.Background(), c, target, []string{path}, o)
	if err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("expected source_changed: %v", err)
	}
	t.Logf("first upload: %v; stored bytes=%q", err, parts[1]+parts[2])
	os.WriteFile(path, []byte("abcdef"), 0600)
	os.Chtimes(path, orig.ModTime(), orig.ModTime())
	_, err = Upload(context.Background(), c, target, []string{path}, o)
	t.Logf("resume error=%v completed=%v stored=%q source=abcdef", err, completed, parts[1]+parts[2])
	if err == nil || completed {
		t.Fatal("reused contaminated parts", err, completed, parts)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDownloadDoesNotAdvanceETagBeforePartialCanBeReset(t *testing.T) {
	calls := atomic.Int32{}
	out := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/store" {
			n := calls.Add(1)
			if n == 1 {
				w.Header().Set("ETag", `"old"`)
				w.Header().Set("Content-Length", "6")
				w.Write([]byte("abc"))
				return
			}
			w.Header().Set("ETag", `"new"`)
			w.Write([]byte("UVWXYZ"))
			return
		}
		if strings.HasSuffix(r.URL.Path, "download-urls") {
			fmt.Fprint(w, `{"files":[{"fileToken":"file","name":"a","sizeBytes":6,"url":"https://test.r2.cloudflarestorage.com/store"}]}`)
			return
		}
		fmt.Fprint(w, `{"items":[{"token":"file"}]}`)
	}))
	defer server.Close()
	c, _ := api.New(server.URL, "")
	base := routeStorage{server.URL, http.DefaultTransport}
	blocked := false
	c.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		resp, e := base.RoundTrip(r)
		if r.URL.Host == "test.r2.cloudflarestorage.com" && calls.Load() == 2 && !blocked {
			paths, _ := filepath.Glob(filepath.Join(out, ".file-okiba", "*.part"))
			if len(paths) != 1 {
				t.Fatal(paths)
			}
			os.Rename(paths[0], paths[0]+".backup")
			os.Mkdir(paths[0], 0700)
			blocked = true
		}
		return resp, e
	})
	target, _ := api.ParseTarget(server.URL + "/g/shared/b/batch")
	opts := DownloadOptions{Output: out}
	if _, e := Download(context.Background(), c, target, opts); e == nil {
		t.Fatal("expected interrupted download")
	}
	if _, e := Download(context.Background(), c, target, opts); e == nil {
		t.Fatal("expected unwritable partial")
	}
	paths, _ := filepath.Glob(filepath.Join(out, ".file-okiba", "*.json"))
	b, _ := os.ReadFile(paths[0])
	var state downloadState
	json.Unmarshal(b, &state)
	if state.ETag != `"old"` {
		t.Fatal("new ETag bound to old bytes", state.ETag)
	}
}
func TestListBatchesTraversesPages(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("cursor") == "" {
			fmt.Fprint(w, `{"items":[{"token":"first"}],"nextCursor":"next+/="}`)
		} else {
			if r.URL.Query().Get("cursor") != "next+/=" {
				t.Error(r.URL.Query())
			}
			fmt.Fprint(w, `{"items":[{"token":"last"}],"nextCursor":null}`)
		}
	}))
	defer s.Close()
	c, _ := api.New(s.URL, "")
	target, _ := api.ParseTarget(s.URL + "/g/group")
	items, e := ListBatches(context.Background(), c, target)
	if e != nil || len(items) != 2 || calls != 2 {
		t.Fatal(items, e, calls)
	}
}
