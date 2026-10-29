// Build the exact release matrix without requiring Go on the destination machine.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	version := flag.String("version", "dev", "バージョン")
	out := flag.String("output", "dist", "出力先")
	flag.Parse()
	if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(*version) {
		return fmt.Errorf("バージョンが不正です")
	}
	if e := os.MkdirAll(*out, 0755); e != nil {
		return e
	}
	work, e := os.MkdirTemp("", "file-okiba-build-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(work)
	sums := []string{}
	for _, target := range []struct{ os, arch, arm string }{{"linux", "amd64", ""}, {"linux", "arm64", ""}, {"linux", "arm", "7"}, {"darwin", "arm64", ""}, {"windows", "amd64", ""}, {"windows", "arm64", ""}} {
		name := "file-okiba"
		if target.os == "windows" {
			name += ".exe"
		}
		binary := filepath.Join(work, name)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+*version, "-o", binary, "./cmd/file-okiba")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.os, "GOARCH="+target.arch, "GOARM="+target.arm)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if e = cmd.Run(); e != nil {
			return e
		}
		arch := target.arch
		if target.arm != "" {
			arch = "armv" + target.arm
		}
		base := fmt.Sprintf("file-okiba_%s_%s_%s", *version, target.os, arch)
		ext := ".tar.gz"
		if target.os == "windows" {
			ext = ".zip"
		}
		path := filepath.Join(*out, base+ext)
		files := map[string]string{name: binary, "README.md": "README.md", "LICENSE": "LICENSE"}
		if e = archive(path, target.os == "windows", files); e != nil {
			return e
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil {
			return e
		}
		sums = append(sums, fmt.Sprintf("%x  %s", h.Sum(nil), filepath.Base(path)))
		fmt.Println(path)
	}
	return os.WriteFile(filepath.Join(*out, "SHA256SUMS"), []byte(strings.Join(sums, "\n")+"\n"), 0644)
}
func archive(path string, isZip bool, files map[string]string) error {
	f, e := os.Create(path)
	if e != nil {
		return e
	}
	defer f.Close()
	names := []string{"file-okiba", "README.md", "LICENSE"}
	if isZip {
		names[0] += ".exe"
		w := zip.NewWriter(f)
		for _, n := range names {
			data, e := os.ReadFile(files[n])
			if e != nil {
				return e
			}
			h := &zip.FileHeader{Name: n, Method: zip.Deflate}
			h.SetMode(0755)
			if n == "README.md" || n == "LICENSE" {
				h.SetMode(0644)
			}
			out, e := w.CreateHeader(h)
			if e != nil {
				return e
			}
			if _, e = out.Write(data); e != nil {
				return e
			}
		}
		return w.Close()
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		data, e := os.ReadFile(files[n])
		if e != nil {
			return e
		}
		mode := int64(0644)
		if n == "file-okiba" {
			mode = 0755
		}
		if e = tw.WriteHeader(&tar.Header{Name: n, Mode: mode, Size: int64(len(data))}); e != nil {
			return e
		}
		if _, e = tw.Write(data); e != nil {
			return e
		}
	}
	if e = tw.Close(); e != nil {
		return e
	}
	return gz.Close()
}
