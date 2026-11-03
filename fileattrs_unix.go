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
