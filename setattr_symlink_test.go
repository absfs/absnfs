package absnfs

import (
	"os"
	"path/filepath"
	"testing"
)

// setattrRequest encodes SETATTR3args: handle, sattr3 and no guard.
func setattrRequest(fh []byte, sattr []byte) []byte {
	return append(append(xdrArgs(fh), sattr...), xdrArgs(uint32(0))...)
}

// TestSetattrOnSymlink: SETATTR on a symbolic link changes the link, never
// its target. Its owner is set with Lchown; its mode, size and times, which
// absfs can set only through the link, are refused with NFS3ERR_INVAL
// before anything changes.
func TestSetattrOnSymlink(t *testing.T) {
	s := newSwapEnv(t)
	canary := filepath.Join(s.outside, "canary")
	link := filepath.Join(s.native, "export", "link")
	if err := os.Symlink(canary, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(s.outside, "missing"), filepath.Join(s.native, "export", "dangling")); err != nil {
		t.Fatal(err)
	}
	fh := s.handle(s.export + "/link")

	for name, sattr := range map[string][]byte{
		"mode":              encodeSattr3(true, 0o600, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0),
		"size":              encodeSattr3(false, 0, false, 0, false, 0, true, 0, 0, 0, 0, 0, 0, 0),
		"atime server time": encodeSattr3(false, 0, false, 0, false, 0, false, 0, 1, 0, 0, 0, 0, 0),
		"mtime client time": encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 2, 12345, 0),
		"mode and owner":    encodeSattr3(true, 0o600, true, uint32(os.Getuid()), false, 0, false, 0, 0, 0, 0, 0, 0, 0),
	} {
		if got := s.call(NFSPROC3_SETATTR, setattrRequest(fh, sattr)); got != NFSERR_INVAL {
			t.Errorf("SETATTR %s on a symlink: status %d, want NFS3ERR_INVAL", name, got)
		}
		s.checkOutside("SETATTR " + name + " on a symlink")
	}

	// The owner is applied to the link. A group of the test user other
	// than the target's shows Lchown was used, not Chown.
	target, err := os.Stat(canary)
	if err != nil {
		t.Fatal(err)
	}
	targetGID := gidOf(target)
	if targetGID < 0 {
		t.Skip("the host records no owners (Windows)")
	}
	other := os.Getgid()
	groups, _ := os.Getgroups()
	for _, g := range groups {
		if g != targetGID {
			other = g
			break
		}
	}
	owner := encodeSattr3(false, 0, true, uint32(os.Getuid()), true, uint32(other), false, 0, 0, 0, 0, 0, 0, 0)
	if got := s.call(NFSPROC3_SETATTR, setattrRequest(fh, owner)); got != NFS_OK {
		t.Fatalf("SETATTR owner on a symlink: status %d, want NFS_OK", got)
	}
	s.checkOutside("SETATTR owner on a symlink")
	if after, err := os.Stat(canary); err != nil || gidOf(after) != targetGID {
		t.Errorf("SETATTR owner on a symlink changed its target's group to %d (was %d), %v", gidOf(after), targetGID, err)
	}
	if li, err := os.Lstat(link); err != nil || gidOf(li) != other {
		t.Errorf("link's group is %d after SETATTR, want %d (%v)", gidOf(li), other, err)
	}

	// A dangling link takes an owner too.
	dfh := s.handle(s.export + "/dangling")
	if got := s.call(NFSPROC3_SETATTR, setattrRequest(dfh, owner)); got != NFS_OK {
		t.Errorf("SETATTR owner on a dangling symlink: status %d, want NFS_OK", got)
	}
	if _, err := os.Lstat(filepath.Join(s.outside, "missing")); !os.IsNotExist(err) {
		t.Errorf("SETATTR on a dangling symlink made its target: %v", err)
	}

	// Through the Go API, a mode or times differing from the link's are
	// refused too.
	node, err := s.nfs.Lookup(s.export + "/link")
	if err != nil {
		t.Fatal(err)
	}
	attrs := *node.attrs
	attrs.Mode = os.ModeSymlink | 0o600
	if err := s.nfs.SetAttr(node, &attrs); mapError(err) != NFSERR_INVAL {
		t.Errorf("SetAttr mode on a symlink: %v, want NFS3ERR_INVAL", err)
	}
	attrs = *node.attrs
	attrs.SetMtime(attrs.Mtime().Add(-1e9))
	if err := s.nfs.SetAttr(node, &attrs); mapError(err) != NFSERR_INVAL {
		t.Errorf("SetAttr times on a symlink: %v, want NFS3ERR_INVAL", err)
	}
	attrs = *node.attrs
	if err := s.nfs.SetAttr(node, &attrs); err != nil {
		t.Errorf("SetAttr changing nothing on a symlink: %v", err)
	}
	s.checkOutside("SetAttr on a symlink")
}

// gidOf returns the group the host records for info, or -1 if none.
func gidOf(info os.FileInfo) int {
	if info == nil {
		return -1
	}
	_, gid, ok := sysOwner(info)
	if !ok {
		return -1
	}
	return int(gid)
}
