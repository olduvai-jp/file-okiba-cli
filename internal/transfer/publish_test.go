package transfer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishWithoutClobberOrHardLinks(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "part"), filepath.Join(dir, "file")
	os.WriteFile(src, []byte("new"), 0600)
	os.WriteFile(dst, []byte("old"), 0600)
	if e := publishNoReplace(src, dst); !os.IsExist(e) {
		t.Fatal("must preserve existing", e)
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "old" {
		t.Fatal(string(b))
	}
	os.Remove(dst)
	if e := publishNoReplace(src, dst); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(src); !os.IsNotExist(e) {
		t.Fatal("source not moved", e)
	}
	b, _ = os.ReadFile(dst)
	if string(b) != "new" {
		t.Fatal(string(b))
	}
}
