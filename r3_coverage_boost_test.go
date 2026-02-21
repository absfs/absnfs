package absnfs

import (
	"bytes"
	"encoding/binary"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

// --- helpers ---

// setupHandlerEnv creates a test NFS handler with a memfs backend.
func setupHandlerEnv(t *testing.T, opts ...func(*ExportOptions)) (*Server, *NFSProcedureHandler, *AuthContext) {
	t.Helper()
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("memfs: %v", err)
	}

	config := DefaultRateLimiterConfig()
	eo := ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	}
	for _, fn := range opts {
		fn(&eo)
	}

	nfs, err := New(mfs, eo)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Create test content
	mfs.Mkdir("/dir", 0755)
	f, _ := mfs.Create("/dir/file.txt")
	f.Write([]byte("hello"))
	f.Close()
	mfs.Mkdir("/dir/sub", 0755)

	srv := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}

	handler := &NFSProcedureHandler{server: srv}
	auth := &AuthContext{
		ClientIP:   "127.0.0.1",
		ClientPort: 1023,
		Credential: &RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
	}
	return srv, handler, auth
}

// allocHandle looks up path and allocates a handle for it.
func allocHandle(t *testing.T, srv *Server, path string) uint64 {
	t.Helper()
	node, err := srv.handler.Lookup(path)
	if err != nil {
		t.Fatalf("Lookup(%s): %v", path, err)
	}
	return srv.handler.fileMap.Allocate(node)
}

// readStatus extracts the NFS status from the first 4 bytes of reply data.
func readStatus(t *testing.T, reply *RPCReply) uint32 {
	t.Helper()
	data, ok := reply.Data.([]byte)
	if !ok {
		t.Fatal("reply.Data is not []byte")
	}
	if len(data) < 4 {
		t.Fatal("reply data too short")
	}
	return binary.BigEndian.Uint32(data[0:4])
}

// encodeSattr3 builds a wire-format sattr3 with the given fields set.
func encodeSattr3(setMode bool, mode uint32, setUID bool, uid uint32, setGID bool, gid uint32,
	setSize bool, size uint64, setAtime uint32, atimeSec, atimeNsec uint32,
	setMtime uint32, mtimeSec, mtimeNsec uint32) []byte {
	var buf bytes.Buffer
	boolU32 := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}
	binary.Write(&buf, binary.BigEndian, boolU32(setMode))
	if setMode {
		binary.Write(&buf, binary.BigEndian, mode)
	}
	binary.Write(&buf, binary.BigEndian, boolU32(setUID))
	if setUID {
		binary.Write(&buf, binary.BigEndian, uid)
	}
	binary.Write(&buf, binary.BigEndian, boolU32(setGID))
	if setGID {
		binary.Write(&buf, binary.BigEndian, gid)
	}
	binary.Write(&buf, binary.BigEndian, boolU32(setSize))
	if setSize {
		binary.Write(&buf, binary.BigEndian, size)
	}
	binary.Write(&buf, binary.BigEndian, setAtime)
	if setAtime == 2 {
		binary.Write(&buf, binary.BigEndian, atimeSec)
		binary.Write(&buf, binary.BigEndian, atimeNsec)
	}
	binary.Write(&buf, binary.BigEndian, setMtime)
	if setMtime == 2 {
		binary.Write(&buf, binary.BigEndian, mtimeSec)
		binary.Write(&buf, binary.BigEndian, mtimeNsec)
	}
	return buf.Bytes()
}

// ================================================================
// 1. filehandle.go Allocate – eviction path
// ================================================================

func TestCovBoost_AllocateEviction(t *testing.T) {
	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
		maxHandles:  5, // tiny limit to trigger eviction
	}

	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		name := "/" + string(rune('a'+i)) + ".txt"
		f, _ := fs.Create(name)
		f.Close()
	}

	// Fill up to max
	for i := 0; i < 5; i++ {
		name := "/" + string(rune('a'+i)) + ".txt"
		f, _ := fs.OpenFile(name, 0, 0)
		fm.Allocate(f)
	}
	if fm.Count() != 5 {
		t.Fatalf("expected 5, got %d", fm.Count())
	}

	// Allocate one more – should trigger eviction of oldest
	f, _ := fs.Create("/extra.txt")
	f.Close()
	f2, _ := fs.OpenFile("/extra.txt", 0, 0)
	fm.Allocate(f2)

	// After eviction of maxHandles/10 = 0 (rounds up to 1), count should be 5
	if fm.Count() > 5 {
		t.Errorf("expected count <= 5 after eviction, got %d", fm.Count())
	}
}

func TestCovBoost_AllocateEvictionSmallMax(t *testing.T) {
	// maxHandles=1: evictCount = 1/10 = 0 -> clamped to 1
	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
		maxHandles:  1,
	}

	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	fs.Create("/a.txt")
	fs.Create("/b.txt")

	f1, _ := fs.OpenFile("/a.txt", 0, 0)
	fm.Allocate(f1)
	f2, _ := fs.OpenFile("/b.txt", 0, 0)
	fm.Allocate(f2)

	if fm.Count() != 1 {
		t.Errorf("expected 1 after eviction with maxHandles=1, got %d", fm.Count())
	}
}

// ================================================================
// 2. decodeSattr3 – all field combinations
// ================================================================

func TestCovBoost_DecodeSattr3_AllFields(t *testing.T) {
	now := time.Now()
	sec := uint32(now.Unix())
	nsec := uint32(now.Nanosecond())

	body := encodeSattr3(
		true, 0755,
		true, 1000,
		true, 1000,
		true, 42,
		2, sec, nsec,
		2, sec, nsec,
	)

	s, err := decodeSattr3(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decodeSattr3: %v", err)
	}
	if !s.SetMode || s.Mode != 0755 {
		t.Errorf("mode: set=%v val=%o", s.SetMode, s.Mode)
	}
	if !s.SetUID || s.UID != 1000 {
		t.Errorf("uid: set=%v val=%d", s.SetUID, s.UID)
	}
	if !s.SetGID || s.GID != 1000 {
		t.Errorf("gid: set=%v val=%d", s.SetGID, s.GID)
	}
	if !s.SetSize || s.Size != 42 {
		t.Errorf("size: set=%v val=%d", s.SetSize, s.Size)
	}
	if s.SetAtime != 2 || s.AtimeSec != sec || s.AtimeNsec != nsec {
		t.Errorf("atime: set=%d sec=%d nsec=%d", s.SetAtime, s.AtimeSec, s.AtimeNsec)
	}
	if s.SetMtime != 2 || s.MtimeSec != sec || s.MtimeNsec != nsec {
		t.Errorf("mtime: set=%d sec=%d nsec=%d", s.SetMtime, s.MtimeSec, s.MtimeNsec)
	}
}

func TestCovBoost_DecodeSattr3_ServerTime(t *testing.T) {
	body := encodeSattr3(
		false, 0, false, 0, false, 0, false, 0,
		1, 0, 0, // SET_TO_SERVER_TIME
		1, 0, 0, // SET_TO_SERVER_TIME
	)
	s, err := decodeSattr3(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decodeSattr3: %v", err)
	}
	if s.SetAtime != 1 {
		t.Errorf("expected atime=1 (server time), got %d", s.SetAtime)
	}
	if s.SetMtime != 1 {
		t.Errorf("expected mtime=1 (server time), got %d", s.SetMtime)
	}
}

func TestCovBoost_DecodeSattr3_Truncated(t *testing.T) {
	// Empty body should fail
	_, err := decodeSattr3(bytes.NewReader([]byte{}))
	if err == nil {
		t.Error("expected error for empty body")
	}

	// Truncated after mode flag=1 (no mode value)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1)) // setMode=true
	_, err = decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated mode value")
	}
}

// ================================================================
// 3. handleSetattr – guard check, truncation, time setting
// ================================================================

func TestCovBoost_HandleSetattr_GuardCheck(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	// Get current attrs to know the ctime
	node, _ := srv.handler.Lookup("/dir/file.txt")
	attrs, _ := srv.handler.GetAttr(node)
	ctimeSec := uint32(attrs.Mtime().Unix())
	ctimeNsec := uint32(attrs.Mtime().Nanosecond())

	// Build SETATTR with matching guard
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(true, 0644, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(1))        // guardCheck=1
	binary.Write(&buf, binary.BigEndian, ctimeSec)         // guard ctime sec
	binary.Write(&buf, binary.BigEndian, ctimeNsec)        // guard ctime nsec

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Error("expected NFS_OK with matching guard")
	}
}

func TestCovBoost_HandleSetattr_GuardMismatch(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	// Guard with wrong ctime
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(1))        // guardCheck=1
	binary.Write(&buf, binary.BigEndian, uint32(99999))    // wrong sec
	binary.Write(&buf, binary.BigEndian, uint32(0))

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFSERR_NOT_SYNC {
		t.Errorf("expected NFSERR_NOT_SYNC, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSetattr_Truncate(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	// Set size to 0 (truncate)
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, true, 0, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(0)) // no guard

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSetattr_ClientTime(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	now := time.Now()
	sec := uint32(now.Unix())
	nsec := uint32(now.Nanosecond())

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0,
		2, sec, nsec, // SET_TO_CLIENT_TIME for atime
		2, sec, nsec, // SET_TO_CLIENT_TIME for mtime
	))
	binary.Write(&buf, binary.BigEndian, uint32(0)) // no guard

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSetattr_ServerTime(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0,
		1, 0, 0, // SET_TO_SERVER_TIME for atime
		1, 0, 0, // SET_TO_SERVER_TIME for mtime
	))
	binary.Write(&buf, binary.BigEndian, uint32(0))

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSetattr_InvalidModeBit(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	// Mode with bit 0x8000 set
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(true, 0x8000|0644, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(0))

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFSERR_INVAL {
		t.Errorf("expected NFSERR_INVAL for mode with 0x8000, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSetattr_UIDGIDAsRoot(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	// Auth as root
	auth.EffectiveUID = 0
	auth.EffectiveGID = 0

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(false, 0, true, 500, true, 500, false, 0, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(0))

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSetattr_ReadOnly(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t, func(o *ExportOptions) {
		o.ReadOnly = true
	})
	fh := allocHandle(t, srv, "/dir/file.txt")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(true, 0644, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(0))

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFSERR_ROFS {
		t.Errorf("expected NFSERR_ROFS, got %d", readStatus(t, result))
	}
}

// ================================================================
// 4. handleCreate – EXCLUSIVE mode, error paths
// ================================================================

func TestCovBoost_HandleCreate_Exclusive(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "newfile.txt")
	binary.Write(&buf, binary.BigEndian, uint32(2)) // createHow=EXCLUSIVE
	buf.Write(make([]byte, 8))                      // 8-byte verifier

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK for EXCLUSIVE create, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleCreate_ExclusiveExistingFile(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	// file.txt already exists in /dir
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "file.txt")
	binary.Write(&buf, binary.BigEndian, uint32(2)) // createHow=EXCLUSIVE
	buf.Write(make([]byte, 8))                      // verifier

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	// EXCLUSIVE create of existing file should succeed (idempotent)
	st := readStatus(t, result)
	if st != NFS_OK {
		t.Errorf("expected NFS_OK for EXCLUSIVE create of existing file, got %d", st)
	}
}

func TestCovBoost_HandleCreate_GUARDED(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	// GUARDED create (createHow=1) of new file should succeed
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "guarded_new.txt")
	binary.Write(&buf, binary.BigEndian, uint32(1)) // createHow=GUARDED
	buf.Write(encodeSattr3(true, 0644, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK for GUARDED create of new file, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleCreate_InvalidFilename(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "..") // invalid
	binary.Write(&buf, binary.BigEndian, uint32(0))
	buf.Write(encodeSattr3(true, 0644, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) == NFS_OK {
		t.Error("expected error for '..' filename")
	}
}

func TestCovBoost_HandleCreate_WithUIDGID(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")
	auth.EffectiveUID = 0 // root

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "owned.txt")
	binary.Write(&buf, binary.BigEndian, uint32(0)) // UNCHECKED
	buf.Write(encodeSattr3(true, 0644, true, 500, true, 500, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleCreate_ReadOnly(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t, func(o *ExportOptions) {
		o.ReadOnly = true
	})
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "nope.txt")
	binary.Write(&buf, binary.BigEndian, uint32(0))
	buf.Write(encodeSattr3(true, 0644, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) != NFSERR_ROFS {
		t.Errorf("expected NFSERR_ROFS, got %d", readStatus(t, result))
	}
}

// ================================================================
// 5. handleWrite – error paths, bounds check
// ================================================================

func TestCovBoost_HandleWrite_OverflowCheck(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	// offset + count overflow
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	binary.Write(&buf, binary.BigEndian, uint64(0xFFFFFFFFFFFFFFFF)) // max offset
	binary.Write(&buf, binary.BigEndian, uint32(1))                  // count=1 -> overflow
	binary.Write(&buf, binary.BigEndian, uint32(0))                  // stable
	binary.Write(&buf, binary.BigEndian, uint32(1))                  // dataLen
	buf.Write([]byte{0})

	reply := &RPCReply{}
	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	if readStatus(t, result) != NFSERR_INVAL {
		t.Errorf("expected NFSERR_INVAL for overflow, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleWrite_DataLenMismatch(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	binary.Write(&buf, binary.BigEndian, uint64(0)) // offset
	binary.Write(&buf, binary.BigEndian, uint32(5)) // count
	binary.Write(&buf, binary.BigEndian, uint32(0)) // stable
	binary.Write(&buf, binary.BigEndian, uint32(3)) // dataLen != count

	reply := &RPCReply{}
	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	if readStatus(t, result) != GARBAGE_ARGS {
		t.Errorf("expected GARBAGE_ARGS, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleWrite_ExceedsMaxWriteSize(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t, func(o *ExportOptions) {
		o.TransferSize = 16 // tiny
	})
	fh := allocHandle(t, srv, "/dir/file.txt")

	data := make([]byte, 32)
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	binary.Write(&buf, binary.BigEndian, uint64(0))          // offset
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))  // count
	binary.Write(&buf, binary.BigEndian, uint32(0))          // stable
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))  // dataLen
	buf.Write(data)

	reply := &RPCReply{}
	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	if readStatus(t, result) != NFSERR_INVAL {
		t.Errorf("expected NFSERR_INVAL for exceeding max, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleWrite_StaleHandle(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)

	data := []byte("hi")
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, 99999) // non-existent handle
	binary.Write(&buf, binary.BigEndian, uint64(0))
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	buf.Write(data)

	_ = srv // keep reference
	reply := &RPCReply{}
	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	if readStatus(t, result) != NFSERR_STALE {
		t.Errorf("expected NFSERR_STALE, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleWrite_Success(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	data := []byte("new data")
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	binary.Write(&buf, binary.BigEndian, uint64(0))
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	binary.Write(&buf, binary.BigEndian, uint32(2)) // FILE_SYNC
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	buf.Write(data)

	reply := &RPCReply{}
	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleWrite_ReadOnly(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t, func(o *ExportOptions) {
		o.ReadOnly = true
	})
	fh := allocHandle(t, srv, "/dir/file.txt")

	data := []byte("x")
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	binary.Write(&buf, binary.BigEndian, uint64(0))
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	buf.Write(data)

	reply := &RPCReply{}
	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	if readStatus(t, result) != NFSERR_ROFS {
		t.Errorf("expected NFSERR_ROFS, got %d", readStatus(t, result))
	}
}

// ================================================================
// 6. WriteWithContext – chtimes path, error paths
// ================================================================

func TestCovBoost_WriteWithContext_NilData(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	node, _ := srv.handler.Lookup("/dir/file.txt")
	_, err := srv.handler.WriteWithContext(nil, node, 0, nil)
	if err == nil {
		t.Error("expected error for nil data")
	}
}

func TestCovBoost_WriteWithContext_NegativeOffset(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	node, _ := srv.handler.Lookup("/dir/file.txt")
	_, err := srv.handler.WriteWithContext(nil, node, -1, []byte("x"))
	if err == nil {
		t.Error("expected error for negative offset")
	}
}

func TestCovBoost_WriteWithContext_Success(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	node, _ := srv.handler.Lookup("/dir/file.txt")
	n, err := srv.handler.Write(node, 0, []byte("overwritten"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 11 {
		t.Errorf("expected 11 bytes written, got %d", n)
	}
}

func TestCovBoost_WriteWithContext_ReadOnly(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t, func(o *ExportOptions) {
		o.ReadOnly = true
	})
	node, _ := srv.handler.Lookup("/dir/file.txt")
	_, err := srv.handler.Write(node, 0, []byte("x"))
	if err == nil {
		t.Error("expected error for read-only write")
	}
}

// ================================================================
// 7. handleSymlink – validation paths, error paths
// ================================================================

func TestCovBoost_HandleSymlink_EmptyTarget(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "mylink")
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	xdrEncodeString(&buf, "") // empty target

	reply := &RPCReply{}
	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSymlink: %v", err)
	}
	if readStatus(t, result) != NFSERR_INVAL {
		t.Errorf("expected NFSERR_INVAL for empty target, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSymlink_AbsoluteTarget(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "mylink")
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	xdrEncodeString(&buf, "/etc/passwd") // absolute path

	reply := &RPCReply{}
	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSymlink: %v", err)
	}
	if readStatus(t, result) != NFSERR_ACCES {
		t.Errorf("expected NFSERR_ACCES for absolute target, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSymlink_DotDotTarget(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "mylink")
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	xdrEncodeString(&buf, "foo/../../../etc") // contains ..

	reply := &RPCReply{}
	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSymlink: %v", err)
	}
	if readStatus(t, result) != NFSERR_ACCES {
		t.Errorf("expected NFSERR_ACCES for .. in target, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSymlink_Success(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "goodlink")
	buf.Write(encodeSattr3(true, 0777, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	xdrEncodeString(&buf, "file.txt")

	reply := &RPCReply{}
	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSymlink: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSymlink_ReadOnly(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t, func(o *ExportOptions) {
		o.ReadOnly = true
	})
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "link")
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	xdrEncodeString(&buf, "target")

	reply := &RPCReply{}
	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSymlink: %v", err)
	}
	if readStatus(t, result) != NFSERR_ROFS {
		t.Errorf("expected NFSERR_ROFS, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleSymlink_InvalidName(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "..")
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	xdrEncodeString(&buf, "target")

	reply := &RPCReply{}
	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSymlink: %v", err)
	}
	if readStatus(t, result) == NFS_OK {
		t.Error("expected error for '..' filename")
	}
}

func TestCovBoost_HandleSymlink_WithUIDGID(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")
	auth.EffectiveUID = 0 // root

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "ownedlink")
	buf.Write(encodeSattr3(false, 0, true, 500, true, 500, false, 0, 0, 0, 0, 0, 0, 0))
	xdrEncodeString(&buf, "file.txt")

	reply := &RPCReply{}
	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSymlink: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

// ================================================================
// 8. HandleCall – timeout, auth denial, program dispatch
// ================================================================

func TestCovBoost_HandleCall_AuthDenied(t *testing.T) {
	srv, handler, _ := setupHandlerEnv(t, func(o *ExportOptions) {
		o.Secure = true
	})
	_ = srv

	// Use unprivileged port with Secure=true
	auth := &AuthContext{
		ClientIP:   "127.0.0.1",
		ClientPort: 50000, // unprivileged
		Credential: &RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
	}

	call := &RPCCall{
		Header: RPCMsgHeader{
			Xid:        1,
			MsgType:    RPC_CALL,
			RPCVersion: 2,
			Program:    NFS_PROGRAM,
			Version:    NFS_V3,
			Procedure:  NFSPROC3_NULL,
		},
		Credential: RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
		Verifier:   RPCVerifier{Flavor: 0, Body: []byte{}},
	}

	reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), auth)
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if reply.Status != MSG_DENIED {
		t.Errorf("expected MSG_DENIED, got %d", reply.Status)
	}
}

func TestCovBoost_HandleCall_MountProgram(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Xid:        2,
			MsgType:    RPC_CALL,
			RPCVersion: 2,
			Program:    MOUNT_PROGRAM,
			Version:    MOUNT_V3,
			Procedure:  0, // NULL
		},
		Credential: RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
		Verifier:   RPCVerifier{Flavor: 0, Body: []byte{}},
	}

	reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), auth)
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if reply.AcceptStatus != SUCCESS {
		t.Errorf("expected SUCCESS, got %d", reply.AcceptStatus)
	}
}

func TestCovBoost_HandleCall_UnknownProgram(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Xid:        3,
			MsgType:    RPC_CALL,
			RPCVersion: 2,
			Program:    999999,
			Version:    1,
			Procedure:  0,
		},
		Credential: RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
		Verifier:   RPCVerifier{Flavor: 0, Body: []byte{}},
	}

	reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), auth)
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if reply.AcceptStatus != PROG_UNAVAIL {
		t.Errorf("expected PROG_UNAVAIL, got %d", reply.AcceptStatus)
	}
}

// ================================================================
// 9. handleMkdir – error paths
// ================================================================

func TestCovBoost_HandleMkdir_InvalidFilename(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "..")
	buf.Write(encodeSattr3(true, 0755, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMkdir: %v", err)
	}
	if readStatus(t, result) == NFS_OK {
		t.Error("expected error for '..' directory name")
	}
}

func TestCovBoost_HandleMkdir_DuplicateDir(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	// "sub" already exists
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "sub")
	buf.Write(encodeSattr3(true, 0755, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMkdir: %v", err)
	}
	st := readStatus(t, result)
	if st == NFS_OK {
		t.Error("expected error for duplicate dir")
	}
}

func TestCovBoost_HandleMkdir_WithUIDGID(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")
	auth.EffectiveUID = 0

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "owneddir")
	buf.Write(encodeSattr3(true, 0755, true, 500, true, 500, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMkdir: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleMkdir_ReadOnly(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t, func(o *ExportOptions) {
		o.ReadOnly = true
	})
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "newdir")
	buf.Write(encodeSattr3(true, 0755, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMkdir: %v", err)
	}
	if readStatus(t, result) != NFSERR_ROFS {
		t.Errorf("expected NFSERR_ROFS, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleMkdir_InvalidMode(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "badmode")
	buf.Write(encodeSattr3(true, 0170755, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0)) // type bits set

	reply := &RPCReply{}
	result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMkdir: %v", err)
	}
	if readStatus(t, result) == NFS_OK {
		t.Error("expected error for mode with type bits")
	}
}

func TestCovBoost_HandleMkdir_StaleHandle(t *testing.T) {
	_, handler, auth := setupHandlerEnv(t)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, 99999)
	xdrEncodeString(&buf, "newdir")
	buf.Write(encodeSattr3(true, 0755, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMkdir: %v", err)
	}
	if readStatus(t, result) != NFSERR_STALE {
		t.Errorf("expected NFSERR_STALE, got %d", readStatus(t, result))
	}
}

// ================================================================
// 10. portmapper handleSet/handleUnset – loopback check, success
// ================================================================

func TestCovBoost_PortmapperHandleSet_Loopback(t *testing.T) {
	pm := NewPortmapper()

	// Localhost address should succeed
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))

	result := pm.handleSet(bytes.NewReader(args.Bytes()), addr)
	// Should return TRUE (4 bytes: 0x00000001)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 1 {
		t.Error("expected true from handleSet with loopback addr")
	}

	// Verify it was registered
	port := pm.GetPort(NFS_PROGRAM, NFS_V3, IPPROTO_TCP)
	if port != 2049 {
		t.Errorf("expected port 2049, got %d", port)
	}
}

func TestCovBoost_PortmapperHandleSet_NonLoopback(t *testing.T) {
	pm := NewPortmapper()

	// Non-loopback address should be rejected
	addr := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))

	result := pm.handleSet(bytes.NewReader(args.Bytes()), addr)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false from handleSet with non-loopback addr")
	}
}

func TestCovBoost_PortmapperHandleSet_NilAddr(t *testing.T) {
	pm := NewPortmapper()

	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))

	result := pm.handleSet(bytes.NewReader(args.Bytes()), nil)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 1 {
		t.Error("expected true from handleSet with nil addr")
	}
}

func TestCovBoost_PortmapperHandleUnset_Loopback(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)

	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))

	result := pm.handleUnset(bytes.NewReader(args.Bytes()), addr)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 1 {
		t.Error("expected true from handleUnset with loopback addr")
	}
}

func TestCovBoost_PortmapperHandleUnset_NonLoopback(t *testing.T) {
	pm := NewPortmapper()

	addr := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))

	result := pm.handleUnset(bytes.NewReader(args.Bytes()), addr)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false from handleUnset with non-loopback addr")
	}
}

func TestCovBoost_PortmapperHandleSet_TruncatedArgs(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}

	// Only send program (truncated)
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))

	result := pm.handleSet(bytes.NewReader(args.Bytes()), addr)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false for truncated args")
	}
}

func TestCovBoost_PortmapperHandleUnset_TruncatedArgs(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}

	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))

	result := pm.handleUnset(bytes.NewReader(args.Bytes()), addr)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false for truncated args")
	}
}

// ================================================================
// 11. portmapper skipAuth – padding consumption
// ================================================================

func TestCovBoost_PortmapperSkipAuth_WithBody(t *testing.T) {
	pm := NewPortmapper()

	// AUTH_SYS flavor=1, body of 5 bytes (needs 3 padding to align to 4)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1)) // flavor
	binary.Write(&buf, binary.BigEndian, uint32(5)) // length
	buf.Write([]byte{1, 2, 3, 4, 5})               // body
	buf.Write([]byte{0, 0, 0})                      // padding to 8

	err := pm.skipAuth(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_EmptyBody(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // flavor
	binary.Write(&buf, binary.BigEndian, uint32(0)) // length

	err := pm.skipAuth(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_ExcessiveLength(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0))          // flavor
	binary.Write(&buf, binary.BigEndian, uint32(0xFFFFFFFF)) // excessive length

	err := pm.skipAuth(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for excessive auth length")
	}
}

func TestCovBoost_PortmapperSkipAuth_4ByteAligned(t *testing.T) {
	pm := NewPortmapper()

	// Body exactly 4 bytes: no padding needed
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1)) // flavor
	binary.Write(&buf, binary.BigEndian, uint32(4)) // length
	buf.Write([]byte{1, 2, 3, 4})                   // body (aligned)

	err := pm.skipAuth(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_Truncated(t *testing.T) {
	pm := NewPortmapper()

	// Only flavor, no length
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))

	err := pm.skipAuth(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated auth")
	}
}

// ================================================================
// 12. portmapper handleCall – version mismatch, program mismatch
// ================================================================

func TestCovBoost_PortmapperHandleCall_VersionMismatch(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))                 // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(99))                // invalid version
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // procedure
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len

	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall: %v", err)
	}
	// Check for PROG_MISMATCH in the reply
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_WrongProgram(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))        // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL)) // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))        // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(999999))   // wrong program
	binary.Write(&buf, binary.BigEndian, uint32(2))        // version
	binary.Write(&buf, binary.BigEndian, uint32(0))        // procedure
	binary.Write(&buf, binary.BigEndian, uint32(0))        // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))        // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))        // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))        // verf len

	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_UnknownProcV2(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))                 // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // version 2
	binary.Write(&buf, binary.BigEndian, uint32(99))                // unknown procedure
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len

	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_UnknownProcV3(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))                 // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(3))                 // version 3
	binary.Write(&buf, binary.BigEndian, uint32(99))                // unknown procedure
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len

	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_NotRPCCall(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1)) // XID
	binary.Write(&buf, binary.BigEndian, uint32(1)) // msg type = REPLY (not CALL)

	_, err := pm.handleCall(buf.Bytes(), nil)
	if err == nil {
		t.Error("expected error for non-RPC-call message")
	}
}

func TestCovBoost_PortmapperHandleCall_SetViaWire(t *testing.T) {
	pm := NewPortmapper()

	// Build a SET call through handleCall with loopback addr
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(42))                // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // version
	binary.Write(&buf, binary.BigEndian, uint32(PMAPPROC_SET))      // procedure
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len
	// SET args
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&buf, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&buf, binary.BigEndian, uint32(2049))

	result, err := pm.handleCall(buf.Bytes(), addr)
	if err != nil {
		t.Fatalf("handleCall SET: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_UnsetViaWire(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)

	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(43))                // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // version
	binary.Write(&buf, binary.BigEndian, uint32(PMAPPROC_UNSET))    // procedure
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len
	// UNSET args
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&buf, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&buf, binary.BigEndian, uint32(2049))

	result, err := pm.handleCall(buf.Bytes(), addr)
	if err != nil {
		t.Fatalf("handleCall UNSET: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ================================================================
// 13. handleMountCall – path validation, export, version checks
// ================================================================

func TestCovBoost_HandleMountCall_MNT(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	var buf bytes.Buffer
	xdrEncodeString(&buf, "/")

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 1, // MNT
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	data := result.Data.([]byte)
	status := binary.BigEndian.Uint32(data[0:4])
	if status != 0 {
		t.Errorf("expected MNT3_OK (0), got %d", status)
	}
}

func TestCovBoost_HandleMountCall_MNTNonexistent(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	var buf bytes.Buffer
	xdrEncodeString(&buf, "/nonexistent/deep/path")

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 1,
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	data := result.Data.([]byte)
	status := binary.BigEndian.Uint32(data[0:4])
	if status != 2 { // MNT3ERR_NOENT
		t.Errorf("expected MNT3ERR_NOENT (2), got %d", status)
	}
}

func TestCovBoost_HandleMountCall_EXPORT(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 5, // EXPORT
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.Data == nil {
		t.Error("expected data for EXPORT")
	}
}

func TestCovBoost_HandleMountCall_DUMP(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 2, // DUMP
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.Data == nil {
		t.Error("expected data for DUMP")
	}
}

func TestCovBoost_HandleMountCall_UMNTALL(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 4, // UMNTALL
		},
	}

	reply := &RPCReply{}
	_, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall UMNTALL: %v", err)
	}
}

func TestCovBoost_HandleMountCall_VersionMismatch(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   99, // bad version
			Procedure: 0,
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.AcceptStatus != PROG_MISMATCH {
		t.Errorf("expected PROG_MISMATCH, got %d", result.AcceptStatus)
	}
}

func TestCovBoost_HandleMountCall_UnknownProc(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 99,
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.AcceptStatus != PROC_UNAVAIL {
		t.Errorf("expected PROC_UNAVAIL, got %d", result.AcceptStatus)
	}
}

func TestCovBoost_HandleMountCall_V1(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   1, // v1
			Procedure: 0,
		},
	}

	reply := &RPCReply{}
	_, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall v1 NULL: %v", err)
	}
}

// ================================================================
// 14. validateFilename additional cases
// ================================================================

func TestCovBoost_ValidateFilename_NullByte(t *testing.T) {
	if validateFilename("foo\x00bar") != NFSERR_INVAL {
		t.Error("expected NFSERR_INVAL for null byte")
	}
}

func TestCovBoost_ValidateFilename_Backslash(t *testing.T) {
	if validateFilename("foo\\bar") != NFSERR_INVAL {
		t.Error("expected NFSERR_INVAL for backslash")
	}
}

func TestCovBoost_ValidateFilename_TooLong(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	if validateFilename(string(long)) != NFSERR_NAMETOOLONG {
		t.Error("expected NFSERR_NAMETOOLONG")
	}
}

func TestCovBoost_ValidateFilename_Dot(t *testing.T) {
	if validateFilename(".") != NFSERR_INVAL {
		t.Error("expected NFSERR_INVAL for '.'")
	}
}

// ================================================================
// 15. handleNFSCall – version mismatch, unknown proc
// ================================================================

func TestCovBoost_HandleNFSCall_VersionMismatch(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   NFS_PROGRAM,
			Version:   2, // not V3
			Procedure: NFSPROC3_NULL,
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleNFSCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleNFSCall: %v", err)
	}
	if result.AcceptStatus != PROG_MISMATCH {
		t.Errorf("expected PROG_MISMATCH, got %d", result.AcceptStatus)
	}
}

func TestCovBoost_HandleNFSCall_UnknownProc(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   NFS_PROGRAM,
			Version:   NFS_V3,
			Procedure: 9999,
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleNFSCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleNFSCall: %v", err)
	}
	if result.AcceptStatus != PROC_UNAVAIL {
		t.Errorf("expected PROC_UNAVAIL, got %d", result.AcceptStatus)
	}
}

// ================================================================
// 16. CreateWithContext – error paths
// ================================================================

func TestCovBoost_CreateWithContext_NilDir(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	_, err := srv.handler.Create(nil, "test", &NFSAttrs{Mode: 0644})
	if err == nil {
		t.Error("expected error for nil dir")
	}
}

func TestCovBoost_CreateWithContext_EmptyName(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	dir, _ := srv.handler.Lookup("/dir")
	_, err := srv.handler.Create(dir, "", &NFSAttrs{Mode: 0644})
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestCovBoost_CreateWithContext_NilAttrs(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	dir, _ := srv.handler.Lookup("/dir")
	_, err := srv.handler.Create(dir, "test", nil)
	if err == nil {
		t.Error("expected error for nil attrs")
	}
}

func TestCovBoost_CreateWithContext_ReadOnly(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t, func(o *ExportOptions) {
		o.ReadOnly = true
	})
	dir, _ := srv.handler.Lookup("/dir")
	_, err := srv.handler.Create(dir, "test", &NFSAttrs{Mode: 0644})
	if err == nil {
		t.Error("expected error for read-only create")
	}
}

// ================================================================
// 17. Symlink operation – error paths
// ================================================================

func TestCovBoost_Symlink_NilDir(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	_, err := srv.handler.Symlink(nil, "link", "target", &NFSAttrs{Mode: os.ModeSymlink | 0777})
	if err == nil {
		t.Error("expected error for nil dir")
	}
}

func TestCovBoost_Symlink_EmptyName(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	dir, _ := srv.handler.Lookup("/dir")
	_, err := srv.handler.Symlink(dir, "", "target", &NFSAttrs{Mode: os.ModeSymlink | 0777})
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestCovBoost_Symlink_EmptyTarget(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	dir, _ := srv.handler.Lookup("/dir")
	_, err := srv.handler.Symlink(dir, "link", "", &NFSAttrs{Mode: os.ModeSymlink | 0777})
	if err == nil {
		t.Error("expected error for empty target")
	}
}

func TestCovBoost_Symlink_NilAttrs(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t)
	dir, _ := srv.handler.Lookup("/dir")
	_, err := srv.handler.Symlink(dir, "link", "target", nil)
	if err == nil {
		t.Error("expected error for nil attrs")
	}
}

func TestCovBoost_Symlink_ReadOnly(t *testing.T) {
	srv, _, _ := setupHandlerEnv(t, func(o *ExportOptions) {
		o.ReadOnly = true
	})
	dir, _ := srv.handler.Lookup("/dir")
	_, err := srv.handler.Symlink(dir, "link", "target", &NFSAttrs{Mode: os.ModeSymlink | 0777})
	if err == nil {
		t.Error("expected error for read-only symlink")
	}
}

// ================================================================
// 18. xdrEncodeFileHandle – coverage for the encode path
// ================================================================

func TestCovBoost_XdrEncodeFileHandle(t *testing.T) {
	var buf bytes.Buffer
	err := xdrEncodeFileHandle(&buf, 12345)
	if err != nil {
		t.Fatalf("xdrEncodeFileHandle: %v", err)
	}
	if buf.Len() != 12 { // 4 (length) + 8 (handle)
		t.Errorf("expected 12 bytes, got %d", buf.Len())
	}

	// Verify we can decode it back
	handle, err := xdrDecodeFileHandle(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("xdrDecodeFileHandle: %v", err)
	}
	if handle != 12345 {
		t.Errorf("expected 12345, got %d", handle)
	}
}

func TestCovBoost_XdrEncodeFileHandle_ErrorWriter(t *testing.T) {
	// Write to a writer that fails (limitedWriter defined in coverage_boost_test.go)
	w := &limitedWriter{buf: make([]byte, 2), limit: 2}
	err := xdrEncodeFileHandle(w, 1)
	if err == nil {
		t.Error("expected error for limited writer")
	}
}

// ================================================================
// 19. handleSetattr with stale handle
// ================================================================

func TestCovBoost_HandleSetattr_StaleHandle(t *testing.T) {
	_, handler, auth := setupHandlerEnv(t)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, 99999) // nonexistent
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(0)) // no guard

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFSERR_STALE {
		t.Errorf("expected NFSERR_STALE, got %d", readStatus(t, result))
	}
}

// ================================================================
// 20. handleCreate with stale handle
// ================================================================

func TestCovBoost_HandleCreate_StaleHandle(t *testing.T) {
	_, handler, auth := setupHandlerEnv(t)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, 99999)
	xdrEncodeString(&buf, "newfile")
	binary.Write(&buf, binary.BigEndian, uint32(0))
	buf.Write(encodeSattr3(true, 0644, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) != NFSERR_STALE {
		t.Errorf("expected NFSERR_STALE, got %d", readStatus(t, result))
	}
}

// ================================================================
// 21. handleSymlink with stale handle
// ================================================================

func TestCovBoost_HandleSymlink_StaleHandle(t *testing.T) {
	_, handler, auth := setupHandlerEnv(t)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, 99999)
	xdrEncodeString(&buf, "link")
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	xdrEncodeString(&buf, "target")

	reply := &RPCReply{}
	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSymlink: %v", err)
	}
	if readStatus(t, result) != NFSERR_STALE {
		t.Errorf("expected NFSERR_STALE, got %d", readStatus(t, result))
	}
}

// ================================================================
// 22. handleSetattr with truncated body for various fields
// ================================================================

func TestCovBoost_HandleSetattr_TruncatedGuard(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	// sattr3 with no fields set, then guard=1 but no ctime bytes
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(1)) // guard=1
	// No ctime sec/nsec -> should fail

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != GARBAGE_ARGS {
		t.Errorf("expected GARBAGE_ARGS, got %d", readStatus(t, result))
	}
}

// ================================================================
// 23. handleCreate - truncated exclusive verifier
// ================================================================

func TestCovBoost_HandleCreate_TruncatedExclusiveVerf(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "newfile2.txt")
	binary.Write(&buf, binary.BigEndian, uint32(2)) // EXCLUSIVE
	buf.Write([]byte{1, 2, 3})                      // Only 3 bytes, need 8

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) != GARBAGE_ARGS {
		t.Errorf("expected GARBAGE_ARGS for truncated verifier, got %d", readStatus(t, result))
	}
}

// ================================================================
// 24. handleWrite - truncated body at various points
// ================================================================

func TestCovBoost_HandleWrite_TruncatedOffset(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	// No offset -> should fail

	reply := &RPCReply{}
	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	if readStatus(t, result) != GARBAGE_ARGS {
		t.Errorf("expected GARBAGE_ARGS, got %d", readStatus(t, result))
	}
}

func TestCovBoost_HandleWrite_TruncatedData(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	binary.Write(&buf, binary.BigEndian, uint64(0))  // offset
	binary.Write(&buf, binary.BigEndian, uint32(10))  // count
	binary.Write(&buf, binary.BigEndian, uint32(0))   // stable
	binary.Write(&buf, binary.BigEndian, uint32(10))  // dataLen
	buf.Write([]byte{1, 2, 3})                        // only 3 bytes, need 10

	reply := &RPCReply{}
	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	if readStatus(t, result) != GARBAGE_ARGS {
		t.Errorf("expected GARBAGE_ARGS, got %d", readStatus(t, result))
	}
}

// ================================================================
// 25. Additional portmapper handleSet/Unset truncation variants
// ================================================================

func TestCovBoost_PortmapperHandleSet_TruncatedVers(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}

	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	// missing prot and port

	result := pm.handleSet(bytes.NewReader(args.Bytes()), addr)
	if binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false for truncated args missing prot")
	}
}

func TestCovBoost_PortmapperHandleUnset_TruncatedVers(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}

	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))

	result := pm.handleUnset(bytes.NewReader(args.Bytes()), addr)
	if binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false for truncated args missing prot")
	}
}

// ================================================================
// 26. handleSetattr - large size overflow
// ================================================================

func TestCovBoost_HandleSetattr_SizeOverflow(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	// Set size to > MaxInt64
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(false, 0, false, 0, false, 0, true, 0x8000000000000000, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(0))

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFSERR_INVAL {
		t.Errorf("expected NFSERR_INVAL for size overflow, got %d", readStatus(t, result))
	}
}

// ================================================================
// 27. handleCreate - invalid mode
// ================================================================

func TestCovBoost_HandleCreate_InvalidMode(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "badmode.txt")
	binary.Write(&buf, binary.BigEndian, uint32(0)) // UNCHECKED
	buf.Write(encodeSattr3(true, 0170644, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) == NFS_OK {
		t.Error("expected error for invalid mode with type bits")
	}
}

// ================================================================
// 28. decodeSattr3 – more truncation variants for inner fields
// ================================================================

func TestCovBoost_DecodeSattr3_TruncatedUIDValue(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(1)) // setUID=true
	// No UID value
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated UID value")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedGIDValue(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setUID=false
	binary.Write(&buf, binary.BigEndian, uint32(1)) // setGID=true
	// No GID value
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated GID value")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedSizeValue(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setUID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setGID=false
	binary.Write(&buf, binary.BigEndian, uint32(1)) // setSize=true
	// No size value
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated size value")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedAtimeValue(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setUID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setGID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setSize=false
	binary.Write(&buf, binary.BigEndian, uint32(2)) // setAtime=SET_TO_CLIENT_TIME
	// No atime sec/nsec
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated atime value")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedMtimeValue(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setUID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setGID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setSize=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setAtime=don't set
	binary.Write(&buf, binary.BigEndian, uint32(2)) // setMtime=SET_TO_CLIENT_TIME
	// No mtime sec/nsec
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated mtime value")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedAtimeNsec(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0))   // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0))   // setUID=false
	binary.Write(&buf, binary.BigEndian, uint32(0))   // setGID=false
	binary.Write(&buf, binary.BigEndian, uint32(0))   // setSize=false
	binary.Write(&buf, binary.BigEndian, uint32(2))   // setAtime=SET_TO_CLIENT_TIME
	binary.Write(&buf, binary.BigEndian, uint32(100)) // atime sec
	// No atime nsec
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated atime nsec")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedMtimeNsec(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0))   // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0))   // setUID=false
	binary.Write(&buf, binary.BigEndian, uint32(0))   // setGID=false
	binary.Write(&buf, binary.BigEndian, uint32(0))   // setSize=false
	binary.Write(&buf, binary.BigEndian, uint32(0))   // setAtime
	binary.Write(&buf, binary.BigEndian, uint32(2))   // setMtime=SET_TO_CLIENT_TIME
	binary.Write(&buf, binary.BigEndian, uint32(100)) // mtime sec
	// No mtime nsec
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated mtime nsec")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedUIDFlag(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	// No UID flag
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated UID flag")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedGIDFlag(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setUID=false
	// No GID flag
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated GID flag")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedSizeFlag(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setUID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setGID=false
	// No size flag
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated size flag")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedAtimeFlag(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setUID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setGID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setSize=false
	// No atime flag
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated atime flag")
	}
}

func TestCovBoost_DecodeSattr3_TruncatedMtimeFlag(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setMode=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setUID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setGID=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setSize=false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // setAtime
	// No mtime flag
	_, err := decodeSattr3(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("expected error for truncated mtime flag")
	}
}

// ================================================================
// 29. HandleCall – RPC error conversion path
// ================================================================

func TestCovBoost_HandleCall_NFSVersionMismatch(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	// NFS program but wrong version triggers PROG_MISMATCH via handleNFSCall
	call := &RPCCall{
		Header: RPCMsgHeader{
			Xid:        10,
			MsgType:    RPC_CALL,
			RPCVersion: 2,
			Program:    NFS_PROGRAM,
			Version:    1, // wrong version
			Procedure:  NFSPROC3_NULL,
		},
		Credential: RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
		Verifier:   RPCVerifier{Flavor: 0, Body: []byte{}},
	}

	reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), auth)
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if reply.AcceptStatus != PROG_MISMATCH {
		t.Errorf("expected PROG_MISMATCH, got %d", reply.AcceptStatus)
	}
}

// ================================================================
// 30. handleWrite with debug enabled
// ================================================================

func TestCovBoost_HandleWrite_SuccessDebug(t *testing.T) {
	srv, _, auth := setupHandlerEnv(t)
	srv.options.Debug = true
	srv.logger = log.New(os.Stderr, "[test] ", log.LstdFlags)
	handler := &NFSProcedureHandler{server: srv}
	fh := allocHandle(t, srv, "/dir/file.txt")

	data := []byte("debug write test")
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	binary.Write(&buf, binary.BigEndian, uint64(0))
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	binary.Write(&buf, binary.BigEndian, uint32(2)) // FILE_SYNC
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	buf.Write(data)

	reply := &RPCReply{}
	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleWrite: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

// ================================================================
// 31. handleSetattr with all sattr fields + no guard (full path)
// ================================================================

func TestCovBoost_HandleSetattr_AllFields(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")

	now := time.Now()
	sec := uint32(now.Unix())
	nsec := uint32(now.Nanosecond())

	// Set mode, size=3, client atime, client mtime, no uid/gid
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(true, 0600, false, 0, false, 0, true, 3,
		2, sec, nsec,
		2, sec, nsec,
	))
	binary.Write(&buf, binary.BigEndian, uint32(0)) // no guard

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

// ================================================================
// 32. handleMkdir success path (basic)
// ================================================================

func TestCovBoost_HandleMkdir_Success(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "newsubdir")
	buf.Write(encodeSattr3(true, 0755, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMkdir: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

// ================================================================
// 33. handleCreate - UNCHECKED with sattr (covers decodeSattr3 in create)
// ================================================================

func TestCovBoost_HandleCreate_UncheckedWithSattr(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")

	now := time.Now()
	sec := uint32(now.Unix())
	nsec := uint32(now.Nanosecond())

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "sattr_file.txt")
	binary.Write(&buf, binary.BigEndian, uint32(0)) // UNCHECKED
	// sattr3 with all fields
	buf.Write(encodeSattr3(true, 0600, true, 1000, true, 1000, false, 0,
		2, sec, nsec,
		2, sec, nsec,
	))

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

// ================================================================
// 34. handleMountCall - UMNT procedure
// ================================================================

func TestCovBoost_HandleMountCall_UMNT(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	var buf bytes.Buffer
	xdrEncodeString(&buf, "/")

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 3, // UMNT
		},
	}

	reply := &RPCReply{}
	_, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall UMNT: %v", err)
	}
}

// ================================================================
// 35. handleMountCall - MNT with a subdirectory path
// ================================================================

func TestCovBoost_HandleMountCall_MNTSubdir(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	var buf bytes.Buffer
	xdrEncodeString(&buf, "/dir")

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 1, // MNT
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	data := result.Data.([]byte)
	status := binary.BigEndian.Uint32(data[0:4])
	if status != 0 {
		t.Errorf("expected MNT3_OK (0), got %d", status)
	}
}

// ================================================================
// 36. Portmapper handleCall with SET/UNSET via v3 (rpcbind)
// ================================================================

func TestCovBoost_PortmapperHandleCall_RpcbSetV3(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(50))                // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(3))                 // rpcbind v3
	binary.Write(&buf, binary.BigEndian, uint32(1))                 // RPCBPROC_SET
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len
	// rpcb args: prog, vers, netid, addr, owner
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	xdrEncodeString(&buf, "tcp")
	xdrEncodeString(&buf, "0.0.0.0.8.1")
	xdrEncodeString(&buf, "")

	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall RPCB_SET: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_RpcbUnsetV3(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(51))                // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(3))                 // rpcbind v3
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPCBPROC_UNSET
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len
	// rpcb args
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	xdrEncodeString(&buf, "tcp")
	xdrEncodeString(&buf, "")
	xdrEncodeString(&buf, "")

	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall RPCB_UNSET: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_RpcbDumpV3(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(52))                // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(3))                 // rpcbind v3
	binary.Write(&buf, binary.BigEndian, uint32(4))                 // RPCBPROC_DUMP
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len

	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall RPCB_DUMP: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_NullV3(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(53))                // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(3))                 // rpcbind v3
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // NULL
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf flavor
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len

	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall NULL v3: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ================================================================
// 37. Portmapper skipAuth with AUTH_SYS body (1-byte body)
// ================================================================

func TestCovBoost_PortmapperSkipAuth_OneByteBody(t *testing.T) {
	pm := NewPortmapper()

	// Body of 1 byte needs 3 bytes padding
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1)) // flavor
	binary.Write(&buf, binary.BigEndian, uint32(1)) // length
	buf.Write([]byte{42})                           // body
	buf.Write([]byte{0, 0, 0})                      // padding to 4

	err := pm.skipAuth(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_TwoByteBody(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1)) // flavor
	binary.Write(&buf, binary.BigEndian, uint32(2)) // length
	buf.Write([]byte{1, 2})                         // body
	buf.Write([]byte{0, 0})                         // padding to 4

	err := pm.skipAuth(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_ThreeByteBody(t *testing.T) {
	pm := NewPortmapper()

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1)) // flavor
	binary.Write(&buf, binary.BigEndian, uint32(3)) // length
	buf.Write([]byte{1, 2, 3})                      // body
	buf.Write([]byte{0})                             // padding to 4

	err := pm.skipAuth(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

// ================================================================
// 38. handleSetattr - UID/GID as non-root (silently ignored)
// ================================================================

func TestCovBoost_HandleSetattr_UIDGIDNonRoot(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	fh := allocHandle(t, srv, "/dir/file.txt")
	auth.EffectiveUID = 1000 // non-root

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	buf.Write(encodeSattr3(false, 0, true, 500, true, 500, false, 0, 0, 0, 0, 0, 0, 0))
	binary.Write(&buf, binary.BigEndian, uint32(0))

	reply := &RPCReply{}
	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSetattr: %v", err)
	}
	// Should succeed but UID/GID change is silently ignored for non-root
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

// ================================================================
// 39. handleCreate - non-root UID/GID (silently ignored)
// ================================================================

func TestCovBoost_HandleCreate_UIDGIDNonRoot(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")
	auth.EffectiveUID = 1000 // non-root

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "nonroot.txt")
	binary.Write(&buf, binary.BigEndian, uint32(0)) // UNCHECKED
	buf.Write(encodeSattr3(true, 0644, true, 500, true, 500, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleCreate: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

// ================================================================
// 40. handleSymlink - non-root UID/GID (silently ignored)
// ================================================================

func TestCovBoost_HandleSymlink_UIDGIDNonRoot(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")
	auth.EffectiveUID = 1000 // non-root

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "nonrootlink")
	buf.Write(encodeSattr3(false, 0, true, 500, true, 500, false, 0, 0, 0, 0, 0, 0, 0))
	xdrEncodeString(&buf, "file.txt")

	reply := &RPCReply{}
	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleSymlink: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

// ================================================================
// 41. handleMkdir - non-root UID/GID
// ================================================================

func TestCovBoost_HandleMkdir_UIDGIDNonRoot(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	dirH := allocHandle(t, srv, "/dir")
	auth.EffectiveUID = 1000 // non-root

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dirH)
	xdrEncodeString(&buf, "nonrootdir")
	buf.Write(encodeSattr3(true, 0755, true, 500, true, 500, false, 0, 0, 0, 0, 0, 0, 0))

	reply := &RPCReply{}
	result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMkdir: %v", err)
	}
	if readStatus(t, result) != NFS_OK {
		t.Errorf("expected NFS_OK, got %d", readStatus(t, result))
	}
}

// ================================================================
// 42. handleMountCall - MNT with truncated body (GARBAGE_ARGS)
// ================================================================

func TestCovBoost_HandleMountCall_MNTGarbageArgs(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 1, // MNT
		},
	}

	// Empty body - can't decode mount path
	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.AcceptStatus != GARBAGE_ARGS {
		t.Errorf("expected GARBAGE_ARGS, got %d", result.AcceptStatus)
	}
}

// ================================================================
// 43. Portmapper handleCall truncated header
// ================================================================

func TestCovBoost_PortmapperHandleCall_TruncatedHeader(t *testing.T) {
	pm := NewPortmapper()

	// Only XID, no message type
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))

	_, err := pm.handleCall(buf.Bytes(), nil)
	if err == nil {
		t.Error("expected error for truncated header")
	}
}

func TestCovBoost_PortmapperHandleCall_EmptyData(t *testing.T) {
	pm := NewPortmapper()
	_, err := pm.handleCall([]byte{}, nil)
	if err == nil {
		t.Error("expected error for empty data")
	}
}

// ================================================================
// 44. Portmapper handleCall with v4 (same as v3 path)
// ================================================================

func TestCovBoost_PortmapperHandleCall_V4(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(54))                // XID
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))          // msg type
	binary.Write(&buf, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram)) // program
	binary.Write(&buf, binary.BigEndian, uint32(4))                 // v4
	binary.Write(&buf, binary.BigEndian, uint32(3))                 // GETADDR
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // auth len
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // verf len
	// GETADDR args
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	xdrEncodeString(&buf, "tcp")
	xdrEncodeString(&buf, "")
	xdrEncodeString(&buf, "")

	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall GETADDR v4: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}
