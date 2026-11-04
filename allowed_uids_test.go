package absnfs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/absfs/memfs"
)

// authSysBody encodes an AUTH_SYS credential for uid and gid.
func authSysBody(uid, gid uint32) []byte {
	var b bytes.Buffer
	xdrEncodeUint32(&b, 1) // stamp
	xdrEncodeString(&b, "client")
	xdrEncodeUint32(&b, uid)
	xdrEncodeUint32(&b, gid)
	xdrEncodeUint32(&b, 0) // no auxiliary gids
	return b.Bytes()
}

// TestAllowedUIDs: with AllowedUIDs set, only AUTH_SYS requests carrying a
// listed uid (as sent, before squashing) are served.
func TestAllowedUIDs(t *testing.T) {
	sys := func(uid uint32) *AuthContext {
		return &AuthContext{
			ClientIP:   "127.0.0.1",
			Credential: &RPCCredential{Flavor: AUTH_SYS, Body: authSysBody(uid, 20)},
		}
	}
	none := &AuthContext{ClientIP: "127.0.0.1", Credential: &RPCCredential{Flavor: AUTH_NONE}}
	cases := []struct {
		name   string
		policy PolicyOptions
		ctx    *AuthContext
		want   bool
	}{
		{"listed uid", PolicyOptions{AllowedUIDs: []uint32{501, 1000}}, sys(1000), true},
		{"unlisted uid", PolicyOptions{AllowedUIDs: []uint32{501, 1000}}, sys(1001), false},
		{"root not listed, though squashed", PolicyOptions{AllowedUIDs: []uint32{501}, Squash: "root"}, sys(0), false},
		{"AUTH_NONE has no uid", PolicyOptions{AllowedUIDs: []uint32{65534}}, none, false},
		{"no list: any uid", PolicyOptions{}, sys(4242), true},
		{"no list: AUTH_NONE", PolicyOptions{}, none, true},
	}
	for _, c := range cases {
		res := ValidateAuthentication(c.ctx, &c.policy)
		if res.Allowed != c.want {
			t.Errorf("%s: allowed %v (%s), want %v", c.name, res.Allowed, res.Reason, c.want)
		}
	}
	gss := &AuthContext{ClientIP: "127.0.0.1", Credential: &RPCCredential{Flavor: 6}}
	if res := ValidateAuthentication(gss, &PolicyOptions{}); res.Allowed || res.Reason == "" {
		t.Errorf("RPCSEC_GSS: allowed %v (%q), want refused with a reason", res.Allowed, res.Reason)
	}
	// An unknown squash mode fails closed: a listed uid is served as nobody.
	res := ValidateAuthentication(sys(1000), &PolicyOptions{AllowedUIDs: []uint32{1000}, Squash: "bogus"})
	if !res.Allowed || res.UID != 65534 || res.GID != 65534 {
		t.Errorf("unknown squash mode: allowed %v as %d:%d, want nobody", res.Allowed, res.UID, res.GID)
	}

	// Through HandleCall: an unlisted uid is denied (AUTH_ERROR) before an
	// NFS procedure runs. NULL and the MOUNT protocol, which reach no file,
	// are answered for any uid: macOS mount_nfs sends MNT as uid 0.
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	allowed := []uint32{1000}
	nfs, err := New(mfs, ExportOptions{AllowedUIDs: allowed})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()
	allowed[0] = 1001 // the export keeps its own copy
	h := &NFSProcedureHandler{server: &Server{handler: nfs}}
	rpc := func(prog, vers, proc uint32, args []byte, flavor uint32, uid uint32) uint32 {
		t.Helper()
		cred := RPCCredential{Flavor: flavor}
		if flavor == AUTH_SYS {
			cred.Body = authSysBody(uid, 20)
		}
		c := &RPCCall{Header: RPCMsgHeader{Program: prog, Version: vers, Procedure: proc}, Credential: cred}
		reply, err := h.HandleCall(c, bytes.NewReader(args), &AuthContext{ClientIP: "127.0.0.1", Credential: &c.Credential})
		if err != nil {
			t.Fatal(err)
		}
		return reply.Status
	}
	getattr := xdrArgs(legacyHandle(1))
	call := func(uid uint32) uint32 { return rpc(NFS_PROGRAM, NFS_V3, NFSPROC3_GETATTR, getattr, AUTH_SYS, uid) }
	if st := call(1000); st != MSG_ACCEPTED {
		t.Errorf("GETATTR as uid 1000 (listed): reply status %d, want MSG_ACCEPTED", st)
	}
	if st := call(1001); st != MSG_DENIED {
		t.Errorf("GETATTR as uid 1001 (not listed): reply status %d, want MSG_DENIED", st)
	}
	if st := rpc(NFS_PROGRAM, NFS_V3, NFSPROC3_GETATTR, getattr, AUTH_NONE, 0); st != MSG_DENIED {
		t.Errorf("GETATTR with AUTH_NONE: reply status %d, want MSG_DENIED", st)
	}
	for name, st := range map[string]uint32{
		"NFS NULL, AUTH_NONE": rpc(NFS_PROGRAM, NFS_V3, NFSPROC3_NULL, nil, AUTH_NONE, 0),
		"NFS NULL, uid 1001":  rpc(NFS_PROGRAM, NFS_V3, NFSPROC3_NULL, nil, AUTH_SYS, 1001),
		"MNT as uid 0":        rpc(MOUNT_PROGRAM, MOUNT_V3, 1, xdrArgs("/"), AUTH_SYS, 0),
	} {
		if st != MSG_ACCEPTED {
			t.Errorf("%s: reply status %d, want MSG_ACCEPTED", name, st)
		}
	}

	// GetExportOptions reports a copy; UpdateExportOptions replaces it.
	opts := nfs.GetExportOptions()
	if len(opts.AllowedUIDs) != 1 || opts.AllowedUIDs[0] != 1000 {
		t.Fatalf("GetExportOptions().AllowedUIDs = %v, want [1000]", opts.AllowedUIDs)
	}
	opts.AllowedUIDs[0] = 7
	if st := call(1000); st != MSG_ACCEPTED {
		t.Error("changing GetExportOptions' slice changed the export")
	}
	opts.AllowedUIDs = []uint32{1001}
	if err := nfs.UpdateExportOptions(opts); err != nil {
		t.Fatal(err)
	}
	if call(1000) != MSG_DENIED || call(1001) != MSG_ACCEPTED {
		t.Error("UpdateExportOptions did not replace AllowedUIDs")
	}
	opts.AllowedUIDs = nil
	if err := nfs.UpdateExportOptions(opts); err != nil {
		t.Fatal(err)
	}
	if call(4242) != MSG_ACCEPTED {
		t.Error("an empty AllowedUIDs still limits uids")
	}
}

// mntStatus sends MNT of p as AUTH_SYS uid through HandleCall and returns
// the MNT3 status (fhs_status).
func mntStatus(t *testing.T, h *NFSProcedureHandler, p string, uid uint32) uint32 {
	t.Helper()
	c := &RPCCall{Header: RPCMsgHeader{Program: MOUNT_PROGRAM, Version: MOUNT_V3, Procedure: 1},
		Credential: RPCCredential{Flavor: AUTH_SYS, Body: authSysBody(uid, 20)}}
	reply, err := h.HandleCall(c, bytes.NewReader(xdrArgs(p)), &AuthContext{ClientIP: "127.0.0.1", Credential: &c.Credential})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Status != MSG_ACCEPTED || reply.AcceptStatus != SUCCESS {
		t.Fatalf("MNT %s as uid %d: reply %d/%d", p, uid, reply.Status, reply.AcceptStatus)
	}
	return readStatusFromReply(reply)
}

// TestMountWithAllowedUIDs: with AllowedUIDs set, a uid not on the list may
// mount the export's root (macOS mount_nfs sends MNT as uid 0) but no other
// path, so MNT tells it nothing about which paths exist; a listed uid
// mounts any directory.
func TestMountWithAllowedUIDs(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	if err := mfs.Mkdir("/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	nfs, err := New(mfs, ExportOptions{AllowedUIDs: []uint32{1000}})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()
	h := &NFSProcedureHandler{server: &Server{handler: nfs}}
	handles := nfs.fileMap.Count()
	for _, c := range []struct {
		path string
		uid  uint32
		want uint32
	}{
		{"/", 0, 0},
		{"/", 4242, 0},
		{"/sub", 4242, 13},     // MNT3ERR_ACCES, whether it exists or not
		{"/missing", 4242, 13}, // the same
		{"/sub", 0, 13},
		{"/sub", 1000, 0},
		{"/missing", 1000, 2}, // MNT3ERR_NOENT
	} {
		if got := mntStatus(t, h, c.path, c.uid); got != c.want {
			t.Errorf("MNT %s as uid %d: status %d, want %d", c.path, c.uid, got, c.want)
		}
	}
	if got := nfs.fileMap.Count() - handles; got > 2 {
		t.Errorf("MNT issued %d handles, want at most 2 (the root and /sub)", got)
	}
}

// TestMountPathComponents: MNT walks no symbolic link and mounts only a
// directory: a path through a link (which the host would follow out of the
// export) answers MNT3ERR_NOENT whether its target exists or not, and a
// link or file as the last component MNT3ERR_NOTDIR.
func TestMountPathComponents(t *testing.T) {
	s := newSwapEnv(t)
	if err := os.Symlink(s.outside, filepath.Join(s.native, "export", "out")); err != nil {
		t.Fatal(err)
	}
	s.writeFile(s.export+"/file", "x")
	s.mkdir(s.export + "/dir")
	h := &NFSProcedureHandler{server: &Server{handler: s.nfs}}
	for _, c := range []struct {
		path string
		want uint32
	}{
		{s.export + "/dir", 0},
		{s.export + "/out/sub", 2},
		{s.export + "/out/nope", 2},
		{s.export + "/out", 20},
		{s.export + "/file", 20},
		{s.export + "/missing", 2},
	} {
		if got := mntStatus(t, h, c.path, 0); got != c.want {
			t.Errorf("MNT %s: status %d, want %d", c.path, got, c.want)
		}
	}
	s.checkOutside("MNT")
}
