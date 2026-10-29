package transfer

import (
	"golang.org/x/sys/unix"
	"os"
)

func publishNoReplace(src, dst string) error {
	if e := unix.RenamexNp(src, dst, unix.RENAME_EXCL); e != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: e}
	}
	return nil
}
