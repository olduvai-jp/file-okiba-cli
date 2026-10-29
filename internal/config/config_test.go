package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProfilesCopyAndSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Config{Profiles: map[string]Profile{"a": {BaseURL: "https://example.com", GroupURL: "https://example.com/g/secret", AdminToken: "secret-token", RetentionDays: 14}}, Default: "a"}
	if err := c.Copy("b", "a"); err != nil {
		t.Fatal(err)
	}
	p := c.Profiles["b"]
	p.GroupURL = "https://example.com/g/other"
	c.Profiles["b"] = p
	if c.Profiles["a"].GroupURL == p.GroupURL {
		t.Fatal("copy mutated source")
	}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Profiles["b"].RetentionDays != 14 {
		t.Fatal(got)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if strings.Contains(string(got.Redacted()), "secret") {
		t.Fatal("secret leaked")
	}
	resolved := got.Resolve("b", func(k string) string {
		if k == "FILE_OKIBA_BASE_URL" {
			return "https://env.example"
		}
		return ""
	})
	if resolved.BaseURL != "https://env.example" || resolved.RetentionDays != 14 {
		t.Fatal(resolved)
	}
}
func TestMissingConfigAndInvalidDefault(t *testing.T) {
	c, e := Load(filepath.Join(t.TempDir(), "missing"))
	if e != nil || c.Profiles == nil {
		t.Fatal(c, e)
	}
	if e := c.Copy("new", "missing"); e == nil {
		t.Fatal("missing source accepted")
	}
}
