//go:build unix

package absnfs

import (
	"os"
	"syscall"
)

// sysOwner returns the owner recorded by the operating system when info comes
// from a real file (osfs and anything wrapping it that passes Sys through).
func sysOwner(info os.FileInfo) (uid, gid uint32, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, 0, false
	}
	return st.Uid, st.Gid, true
}

// sysSameFile compares the device and inode numbers of two host files.
// known is false unless both are host files.
func sysSameFile(a, b any) (same, known bool) {
	x, ok := a.(*syscall.Stat_t)
	if !ok || x == nil {
		return false, false
	}
	y, ok := b.(*syscall.Stat_t)
	if !ok || y == nil {
		return false, false
	}
	return x.Dev == y.Dev && x.Ino == y.Ino, true
}
