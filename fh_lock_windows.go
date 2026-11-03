//go:build windows

package absnfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION, which package syscall
// does not name.
const errSharingViolation = syscall.Errno(32)

// lockStateDir opens the file name without sharing, which a second server
// using the same HandleStateDir then fails to do. The lock ends when the
// returned Closer is closed or the process exits.
func lockStateDir(name string) (io.Closer, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, errSharingViolation) {
			return nil, fmt.Errorf("in use by another server")
		}
		return nil, err
	}
	return os.NewFile(uintptr(h), name), nil
}
