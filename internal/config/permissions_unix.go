//go:build !windows

package config

import "os"

func RestrictFile(path string) error { return os.Chmod(path, 0600) }
