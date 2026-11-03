//go:build !unix

package absnfs

import "os"

// sysOwner reports no owner on platforms without unix ownership (Windows),
// so the export's configured owner is used for every file.
func sysOwner(info os.FileInfo) (uid, gid uint32, ok bool) {
	return 0, 0, false
}
