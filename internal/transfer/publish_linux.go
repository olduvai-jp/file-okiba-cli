package transfer

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func publishNoReplace(src, dst string) error {
	e := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	if e == nil {
		return nil
	}
	if errors.Is(e, unix.ENOSYS) || errors.Is(e, unix.EINVAL) || errors.Is(e, unix.ENOTSUP) {
		if e = os.Link(src, dst); e != nil {
			return e
		}
		return os.Remove(src)
	}
	return &os.LinkError{Op: "rename", Old: src, New: dst, Err: e}
}
