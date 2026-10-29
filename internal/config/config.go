package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultBaseURL = "https://file-okiba.oldv.org"

type Profile struct {
	BaseURL       string `json:"baseUrl,omitempty"`
	GroupURL      string `json:"groupUrl,omitempty"`
	AdminToken    string `json:"adminToken,omitempty"`
	RetentionDays int    `json:"retentionDays,omitempty"`
}
type Config struct {
	Default  string             `json:"default,omitempty"`
	Profiles map[string]Profile `json:"profiles"`
}

func Path() string {
	if p := os.Getenv("FILE_OKIBA_CONFIG"); p != "" {
		return p
	}
	d, e := os.UserConfigDir()
	if e != nil {
		d = "."
	}
	return filepath.Join(d, "file-okiba", "config.json")
}
func StateDir() string {
	if p := os.Getenv("FILE_OKIBA_STATE_DIR"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(Path()), "state")
}
func Load(path string) (Config, error) {
	c := Config{Profiles: map[string]Profile{}}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, fmt.Errorf("設定ファイルが不正です")
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	return c, nil
}
func Save(path string, c Config) error {
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	return WritePrivate(path, b)
}

// WritePrivate replaces a private file atomically. The temporary file is never world readable.
func WritePrivate(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".file-okiba-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = RestrictFile(f.Name()); e != nil {
		f.Close()
		return e
	}
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (c *Config) Copy(name, from string) error {
	if name == "" {
		return fmt.Errorf("プロファイル名が必要です")
	}
	if _, ok := c.Profiles[name]; ok {
		return fmt.Errorf("同名のプロファイルが存在します")
	}
	p := Profile{BaseURL: DefaultBaseURL, RetentionDays: 7}
	if from != "" {
		var ok bool
		p, ok = c.Profiles[from]
		if !ok {
			return fmt.Errorf("コピー元が存在しません")
		}
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	c.Profiles[name] = p
	if c.Default == "" {
		c.Default = name
	}
	return nil
}
func (c Config) Resolve(name string, env func(string) string) Profile {
	if name == "" {
		name = c.Default
	}
	p := c.Profiles[name]
	if p.BaseURL == "" {
		p.BaseURL = DefaultBaseURL
	}
	if p.RetentionDays == 0 {
		p.RetentionDays = 7
	}
	for k, v := range map[string]*string{"FILE_OKIBA_BASE_URL": &p.BaseURL, "FILE_OKIBA_GROUP_URL": &p.GroupURL, "FILE_OKIBA_ADMIN_TOKEN": &p.AdminToken} {
		if s := env(k); s != "" {
			*v = s
		}
	}
	return p
}
func (c Config) Redacted() []byte {
	copy := Config{Default: c.Default, Profiles: map[string]Profile{}}
	for n, p := range c.Profiles {
		if p.GroupURL != "" {
			p.GroupURL = "[redacted]"
		}
		if p.AdminToken != "" {
			p.AdminToken = "[redacted]"
		}
		copy.Profiles[n] = p
	}
	b, _ := json.MarshalIndent(copy, "", "  ")
	return b
}
