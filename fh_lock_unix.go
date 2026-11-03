//go:build unix

package absnfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// lockStateDir takes an exclusive lock on the file name, which a second
// server using the same HandleStateDir then fails to take. The lock ends
// when the returned Closer is closed or the process exits.
func lockStateDir(name string) (io.Closer, error) {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("in use by another server")
		}
		return nil, err
	}
	return f, nil
}
