package transfer

import (
	"golang.org/x/sys/windows"
	"os"
)

func publishNoReplace(src, dst string) error {
	from, e := windows.UTF16PtrFromString(src)
	if e != nil {
		return e
	}
	to, e := windows.UTF16PtrFromString(dst)
	if e != nil {
		return e
	}
	if e = windows.MoveFile(from, to); e != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: e}
	}
	return nil
}
