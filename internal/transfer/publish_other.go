//go:build !linux && !darwin && !windows

package transfer

import "os"

func publishNoReplace(src, dst string) error {
	if e := os.Link(src, dst); e != nil {
		return e
	}
	return os.Remove(src)
}
