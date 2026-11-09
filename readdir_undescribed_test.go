package absnfs

import (
	"bytes"
	"os"
	"syscall"
	"testing"
)

// TestReaddirKeepsUndescribedEntry: a name the backend lists but cannot
// Lstat stays in READDIR and READDIRPLUS (absfs/absnfs#92), the latter
// sending it without attributes or handle, and LOOKUP and GETATTR of it
// fail with the backend's error. A name that is gone is still left out.
func TestReaddirKeepsUndescribedEntry(t *testing.T) {
	for _, dirCache := range []bool{false, true} {
		mfs := newMemFS(t)
		mustMkdir(t, mfs, "/d")
		writeFile(t, mfs, "/d/good", 4)
		writeFile(t, mfs, "/d/bad", 5)
		hook := &faHookFS{SymlinkFileSystem: mfs, lstatErr: map[string]error{}}
		e := newFhEnv(t, hook, ExportOptions{EnableDirCache: dirCache})
		root := e.mount("/")
		d := e.walk(root, "d")
		bad := e.walk(d, "bad") // a handle from before the failure
		e.nfs.attrCache.Clear()
		hook.lstatErr["/d/bad"] = syscall.EIO

		r, err := e.h.handleReaddir(bytes.NewReader(buildReaddirRequest(d, 0, 4096)), &RPCReply{}, e.auth)
		if err != nil {
			t.Fatal(err)
		}
		f := newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("dircache %v: READDIR status %d", dirCache, st)
		}
		f.postOpAttr()
		f.skip(8)
		names := map[string]uint64{}
		for _, en := range f.entries(false) {
			names[en.Name] = en.FileId
		}
		if len(names) != 2 || names["bad"] != e.nfs.objectID("/d/bad") || names["good"] == 0 {
			t.Errorf("dircache %v: READDIR lists %v, want good and bad (fileid %d)", dirCache, names, e.nfs.objectID("/d/bad"))
		}

		r, err = e.h.handleReaddirplus(bytes.NewReader(buildReaddirplusRequest(d, 0, 4096, 8192)), &RPCReply{}, e.auth)
		if err != nil {
			t.Fatal(err)
		}
		f = newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("dircache %v: READDIRPLUS status %d", dirCache, st)
		}
		f.postOpAttr()
		f.skip(8)
		seen := 0
		for _, en := range f.entries(true) {
			seen++
			switch en.Name {
			case "bad":
				if en.Attr != nil || en.Handle != nil || en.FileId != e.nfs.objectID("/d/bad") {
					t.Errorf("dircache %v: READDIRPLUS sent bad with attrs %v handle %x fileid %d", dirCache, en.Attr, en.Handle, en.FileId)
				}
			case "good":
				if en.Attr == nil || en.Attr.Size != 4 || en.Handle == nil {
					t.Errorf("dircache %v: READDIRPLUS sent good with attrs %v handle %x", dirCache, en.Attr, en.Handle)
				}
			}
		}
		if eof := f.u32(); seen != 2 || eof != 1 {
			t.Errorf("dircache %v: READDIRPLUS sent %d entries, eof %d", dirCache, seen, eof)
		}

		if _, st := e.lookup(d, "bad"); st != NFSERR_IO {
			t.Errorf("dircache %v: LOOKUP bad: status %d, want NFSERR_IO", dirCache, st)
		}
		if st, _ := e.getattr(bad); st != NFSERR_IO {
			t.Errorf("dircache %v: GETATTR bad: status %d, want NFSERR_IO", dirCache, st)
		}

		// Gone is gone: a name Lstat says does not exist is not listed.
		hook.lstatErr["/d/bad"] = os.ErrNotExist
		e.nfs.attrCache.Clear()
		nodes, err := e.nfs.ReadDir(&NFSNode{path: "/d", attrs: &NFSAttrs{Mode: os.ModeDir | 0o755}})
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) != 1 || nodes[0].path != "/d/good" {
			t.Errorf("dircache %v: a vanished entry is listed: %d nodes", dirCache, len(nodes))
		}
	}
}
