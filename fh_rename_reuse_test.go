package absnfs

import (
	"testing"
)

// TestRenamedLeavesNewSourceHandle replays the interleaving TestHandleStress
// hit about once in a hundred runs: RENAME a -> b commits, a new object is
// made at a and LOOKUP issues its handle, and only then does the RENAME
// move the cached handle of a. It must move the handle of the object that
// moved, not the new one's: that handle was issued for the object now at
// a, and must keep reaching it.
func TestRenamedLeavesNewSourceHandle(t *testing.T) {
	fs := newMemFS(t)
	mustMkdir(t, fs, "/s")
	writeFile(t, fs, "/s/a", 3) // X
	e := newFhEnv(t, fs, ExportOptions{})
	root := e.mount("/")
	s := e.walk(root, "s")
	hx := e.walk(s, "a")

	// RENAME a -> b as RenameWithContext does it, up to the commit.
	src, dst := "s/a", "s/b"
	log := e.nfs.fileMap.log
	op := log.begin(src, dst)
	if err := fs.Rename("/s/a", "/s/b"); err != nil {
		t.Fatal(err)
	}
	epoch := log.commitRename(op, true, false, src, dst)
	e.nfs.invalidate("/s", "/s")
	e.nfs.invalidateTree("/s/a", "/s/b")

	// Before the RENAME updates the cache, a new object Y is made at a and
	// looked up.
	writeFile(t, fs, "/s/a", 5) // Y
	e.nfs.invalidate("/s", "/s/a")
	hy := e.walk(s, "a")

	e.nfs.fileMap.renamed(src, dst, epoch)

	e.size(hy, 5, "the handle issued for the new object at a")
	e.size(hx, 3, "the handle of the renamed object")
}
