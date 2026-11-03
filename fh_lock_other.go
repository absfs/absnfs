//go:build !unix && !windows

package absnfs

import (
	"io"
	"os"
)

// lockStateDir creates the lock file. This platform has no file locks, so
// nothing stops a second server from using the same HandleStateDir.
func lockStateDir(name string) (io.Closer, error) {
	return os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
}
