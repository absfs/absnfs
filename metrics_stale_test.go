package absnfs

import (
	"bytes"
	"testing"
)

// TestStaleRepliesCounted: every NFS3ERR_STALE reply is counted in the
// StaleHandles metric, and no other status is.
func TestStaleRepliesCounted(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{})
	e.writeFile("/f", "x")
	fh := e.handle("/f")
	root := e.handle("/")
	h := e.h
	call := func(proc uint32, args []byte) uint32 {
		t.Helper()
		c := &RPCCall{Header: RPCMsgHeader{Program: NFS_PROGRAM, Version: NFS_V3, Procedure: proc}}
		reply, err := h.handleNFSCall(c, bytes.NewReader(args), &RPCReply{}, e.auth)
		if err != nil {
			t.Fatal(err)
		}
		return readStatusFromReply(reply)
	}
	stale := func() uint64 { return e.nfs.GetMetrics().StaleHandles }

	if st := call(NFSPROC3_LOOKUP, xdrArgs(root, "missing")); st != NFSERR_NOENT || stale() != 0 {
		t.Fatalf("LOOKUP of a missing name: status %d, StaleHandles %d; want NOENT and 0", st, stale())
	}
	if st := call(NFSPROC3_GETATTR, xdrArgs(legacyHandle(5))); st != NFSERR_STALE || stale() != 1 {
		t.Errorf("GETATTR with a handle never issued: status %d, StaleHandles %d; want STALE and 1", st, stale())
	}
	if st := call(NFSPROC3_REMOVE, xdrArgs(root, "f")); st != NFS_OK {
		t.Fatalf("REMOVE: status %d", st)
	}
	if st := call(NFSPROC3_READ, xdrArgs(fh, uint64(0), uint32(10))); st != NFSERR_STALE || stale() != 2 {
		t.Errorf("READ of a removed file: status %d, StaleHandles %d; want STALE and 2", st, stale())
	}
	if st := call(NFSPROC3_SETATTR, append(append(xdrArgs(fh), noSattr...), xdrArgs(uint32(0))...)); st != NFSERR_STALE || stale() != 3 {
		t.Errorf("SETATTR of a removed file: status %d, StaleHandles %d; want STALE and 3", st, stale())
	}
}
