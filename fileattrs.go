// fileattrs.go: The single place NFS attributes are built from the filesystem.
//
// Every procedure that reports attributes for an object (LOOKUP, GETATTR,
// SETATTR, READDIR, READDIRPLUS, CREATE, MKDIR, SYMLINK, WRITE, the root)
// gets them from statAttrs or attrsFromInfo, so the file id and owner are
// derived the same way everywhere and no call site can drop them.
package absnfs

import (
	"fmt"
	"os"
)

// FileOwner is a numeric user and group id as reported to NFS clients.
type FileOwner struct {
	UID uint32
	GID uint32
}

// processOwner returns the uid and gid of the serving process. Platforms
// without numeric ids (Windows, where os.Getuid returns -1) report 0.
func processOwner() FileOwner {
	return FileOwner{UID: idOrZero(os.Getuid()), GID: idOrZero(os.Getgid())}
}

func idOrZero(id int) uint32 {
	if id < 0 {
		return 0
	}
	return uint32(id)
}

// ownerOf returns the owner to report for info: the owner the filesystem
// records (see sysOwner), unless ForceOwner is set or there is none, in which
// case the export's configured owner.
func (s *AbsfsNFS) ownerOf(info os.FileInfo) (uid, gid uint32) {
	if !s.forceOwner {
		if uid, gid, ok := sysOwner(info); ok {
			return uid, gid
		}
	}
	return s.owner.UID, s.owner.GID
}

// attrsFromInfo builds the attributes describing the object at p. info must
// describe the object itself, so callers obtain it with Lstat: a symlink is
// reported as a symlink, not as its target.
func (s *AbsfsNFS) attrsFromInfo(p string, info os.FileInfo) *NFSAttrs {
	uid, gid := s.ownerOf(info)
	attrs := &NFSAttrs{
		Mode:   info.Mode(),
		Size:   info.Size(),
		FileId: s.objectID(p),
		Uid:    uid,
		Gid:    gid,
	}
	modTime := info.ModTime()
	attrs.SetMtime(modTime)
	attrs.SetAtime(modTime) // absfs does not expose atime
	attrs.Refresh()
	return attrs
}

// statAttrs reads fresh attributes for the object at p (without following a
// final symlink).
func (s *AbsfsNFS) statAttrs(p string) (*NFSAttrs, error) {
	info, err := s.fs.Lstat(p)
	if err != nil {
		return nil, fmt.Errorf("failed to stat %s: %w", p, err)
	}
	return s.attrsFromInfo(p, info), nil
}
