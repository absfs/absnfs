//go:build !unix

package absnfs

import "os"

// sysOwner reports no owner on platforms without unix ownership (Windows),
// so the export's configured owner is used for every file.
func sysOwner(info os.FileInfo) (uid, gid uint32, ok bool) {
	return 0, 0, false
}

// sysSameFile knows no host file identity here (os.SameFile covers
// Windows files that are not wrapped).
func sysSameFile(a, b any) (same, known bool) {
	return false, false
}
