package absnfs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// exportRPC sends one record-marked RPC call over conn and returns the
// accepted reply's result bytes, failing unless the reply is a well-formed,
// record-marked, accepted SUCCESS reply to xid.
func exportRPC(t *testing.T, conn net.Conn, xid, prog, vers, proc uint32, args []byte) []byte {
	t.Helper()
	var call bytes.Buffer
	for _, v := range []uint32{xid, RPC_CALL, 2, prog, vers, proc,
		AUTH_NONE, 0, // credential
		AUTH_NONE, 0, // verifier
	} {
		binary.Write(&call, binary.BigEndian, v)
	}
	call.Write(args)

	rm := NewRecordMarkingConn(conn, conn)
	if err := rm.WriteRecord(call.Bytes()); err != nil {
		t.Fatalf("send: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := rm.ReadRecord()
	if err != nil {
		t.Fatalf("program %d proc %d: no record-marked reply: %v", prog, proc, err)
	}
	r := bytes.NewReader(resp)
	var hdr [6]uint32 // xid, msg type, reply stat, verf flavor, verf len, accept stat
	if err := binary.Read(r, binary.BigEndian, &hdr); err != nil {
		t.Fatalf("program %d proc %d: short reply (%d bytes): %v", prog, proc, len(resp), err)
	}
	if hdr[0] != xid || hdr[1] != RPC_REPLY || hdr[2] != MSG_ACCEPTED || hdr[4] != 0 || hdr[5] != SUCCESS {
		t.Fatalf("program %d proc %d: reply header %v", prog, proc, hdr)
	}
	rest := make([]byte, r.Len())
	r.Read(rest)
	return rest
}

// TestExportServesRecordMarkedNFSAndMountOnOnePort: Export serves what a
// kernel client (mount_nfs) needs without a portmapper: RPC record marking,
// and the MOUNT program on the same port as NFS.
func TestExportServesRecordMarkedNFSAndMountOnOnePort(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.Mkdir("/ws", 0o755); err != nil {
		t.Fatal(err)
	}
	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	if got := nfs.ExportPort(); got != 0 {
		t.Errorf("ExportPort before Export = %d, want 0", got)
	}
	if err := nfs.Export("/", 0); err != nil {
		t.Fatal(err)
	}
	port := nfs.ExportPort()
	if port == 0 {
		t.Fatal("ExportPort after Export(\"/\", 0) = 0, want the bound port")
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// NULL on both programs, over one connection.
	exportRPC(t, conn, 1, NFS_PROGRAM, NFS_V3, NFSPROC3_NULL, nil)
	exportRPC(t, conn, 2, MOUNT_PROGRAM, MOUNT_V3, 0, nil)

	// MNT of the root and of a subdirectory return a file handle.
	for i, p := range []string{"/", "/ws"} {
		var args bytes.Buffer
		xdrEncodeString(&args, p)
		res := exportRPC(t, conn, uint32(10+i), MOUNT_PROGRAM, MOUNT_V3, 1, args.Bytes())
		r := bytes.NewReader(res)
		status, err := xdrDecodeUint32(r)
		if err != nil || status != 0 {
			t.Fatalf("MNT %s: status %d, err %v", p, status, err)
		}
		if n, err := xdrDecodeUint32(r); err != nil || n == 0 {
			t.Fatalf("MNT %s: file handle length %d, err %v", p, n, err)
		}
	}

	// A missing path is MNT3ERR_NOENT.
	var args bytes.Buffer
	xdrEncodeString(&args, "/missing")
	res := exportRPC(t, conn, 20, MOUNT_PROGRAM, MOUNT_V3, 1, args.Bytes())
	if status, _ := xdrDecodeUint32(bytes.NewReader(res)); status != 2 {
		t.Errorf("MNT /missing: status %d, want MNT3ERR_NOENT (2)", status)
	}

	if err := nfs.Unexport(); err != nil {
		t.Fatal(err)
	}
	if got := nfs.ExportPort(); got != 0 {
		t.Errorf("ExportPort after Unexport = %d, want 0", got)
	}
}
