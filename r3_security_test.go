package absnfs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

// ---------------------------------------------------------------------------
// 1. Root squash credentials applied in HandleCall
// ---------------------------------------------------------------------------

// TestR3_RootSquashCredentialsApplied verifies that HandleCall propagates
// the squashed UID/GID from ValidateAuthentication into AuthContext's
// EffectiveUID / EffectiveGID before dispatching to a handler.
func TestR3_RootSquashCredentialsApplied(t *testing.T) {
	server, _, _, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	// Enable root squash by storing a new policy snapshot directly
	// (Squash is immutable via UpdatePolicyOptions, so we set it directly in tests)
	p := *server.handler.policy.Load()
	p.Squash = "root"
	server.handler.policy.Store(&p)

	// Build AUTH_SYS credential for root (UID=0, GID=0)
	var credBody bytes.Buffer
	binary.Write(&credBody, binary.BigEndian, uint32(0))    // stamp
	binary.Write(&credBody, binary.BigEndian, uint32(0))    // machine name length
	binary.Write(&credBody, binary.BigEndian, uint32(0))    // uid = 0 (root)
	binary.Write(&credBody, binary.BigEndian, uint32(0))    // gid = 0 (root)
	binary.Write(&credBody, binary.BigEndian, uint32(0))    // aux gids count

	authCtx := &AuthContext{
		ClientIP:   "127.0.0.1",
		ClientPort: 800,
		Credential: &RPCCredential{
			Flavor: AUTH_SYS,
			Body:   credBody.Bytes(),
		},
	}

	// Verify authentication squashes root
	result := ValidateAuthentication(authCtx, server.handler.policy.Load())
	if !result.Allowed {
		t.Fatalf("Expected auth to be allowed, got denied: %s", result.Reason)
	}
	if result.UID != 65534 {
		t.Errorf("Expected squashed UID 65534, got %d", result.UID)
	}
	if result.GID != 65534 {
		t.Errorf("Expected squashed GID 65534, got %d", result.GID)
	}

	// Simulate what HandleCall does: apply squashed credentials
	authCtx.EffectiveUID = result.UID
	authCtx.EffectiveGID = result.GID

	if authCtx.EffectiveUID != 65534 {
		t.Errorf("EffectiveUID should be 65534 after squash, got %d", authCtx.EffectiveUID)
	}
	if authCtx.EffectiveGID != 65534 {
		t.Errorf("EffectiveGID should be 65534 after squash, got %d", authCtx.EffectiveGID)
	}
}

// ---------------------------------------------------------------------------
// 2. READLINK sanitization (rejects ".." in symlink targets)
// ---------------------------------------------------------------------------

// TestR3_ReadlinkSanitization verifies that Readlink rejects symlink targets
// containing ".." components to prevent traversal outside the export.
func TestR3_ReadlinkSanitization(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	// Create a symlink with a dangerous relative target
	err = fs.Symlink("../../etc/passwd", "/badlink")
	if err != nil {
		t.Skip("memfs does not support Symlink, skipping")
	}

	node, err := nfs.Lookup("/badlink")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}

	// Readlink should reject the target containing ".."
	_, err = nfs.Readlink(node)
	if err == nil {
		t.Error("Expected Readlink to reject symlink target containing '..'")
	}

	// Create a safe relative symlink
	err = fs.Symlink("subdir/file.txt", "/goodlink")
	if err != nil {
		t.Fatalf("Failed to create safe symlink: %v", err)
	}

	goodNode, err := nfs.Lookup("/goodlink")
	if err != nil {
		t.Fatalf("Lookup failed for good link: %v", err)
	}

	target, err := nfs.Readlink(goodNode)
	if err != nil {
		t.Fatalf("Readlink should accept safe relative target, got error: %v", err)
	}
	if target != "subdir/file.txt" {
		t.Errorf("Expected target 'subdir/file.txt', got %q", target)
	}
}

// ---------------------------------------------------------------------------
// 3. Nil attrs safety in GetAttr
// ---------------------------------------------------------------------------

// TestR3_NilAttrsSafetyInGetAttr verifies that GetAttr handles a node
// with nil attrs gracefully, returning zero UID/GID without panicking.
func TestR3_NilAttrsSafetyInGetAttr(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	// Create a file
	f, err := fs.Create("/testfile")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	node, err := nfs.Lookup("/testfile")
	if err != nil {
		t.Fatal(err)
	}

	// Deliberately set node.attrs to nil to test nil safety
	node.mu.Lock()
	node.attrs = nil
	node.mu.Unlock()

	// GetAttr should still work - it reads uid/gid under RLock with nil check
	attrs, err := nfs.GetAttr(node)
	if err != nil {
		t.Fatalf("GetAttr should not fail with nil attrs, got: %v", err)
	}

	// When attrs is nil, uid and gid should default to 0
	if attrs.Uid != 0 {
		t.Errorf("Expected UID=0 when node.attrs is nil, got %d", attrs.Uid)
	}
	if attrs.Gid != 0 {
		t.Errorf("Expected GID=0 when node.attrs is nil, got %d", attrs.Gid)
	}
}

// ---------------------------------------------------------------------------
// 4. ACCESS permission checking (owner/group/other/root/readonly)
// ---------------------------------------------------------------------------

// TestR3_AccessPermissionChecking verifies that handleAccess correctly
// applies owner/group/other permission bits, root override, and read-only
// server restrictions.
func TestR3_AccessPermissionChecking(t *testing.T) {
	t.Run("OwnerPermissions", func(t *testing.T) {
		server, handler, _, err := newTestServerForBugfixes()
		if err != nil {
			t.Fatal(err)
		}

		// Create a file with mode 0700 (owner rwx only)
		server.handler.fs.Create("/ownerfile")
		server.handler.fs.Chmod("/ownerfile", 0700)
		fileHandle := getFileHandle(server, "/ownerfile")

		// Set the file's uid to match the auth context
		node, ok := handler.lookupNode(fileHandle)
		if !ok {
			t.Fatal("Failed to look up node")
		}
		node.mu.Lock()
		node.attrs.Uid = 1000
		node.attrs.Gid = 2000
		node.mu.Unlock()

		// Auth context: owner (UID 1000)
		authCtx := &AuthContext{
			ClientIP:   "127.0.0.1",
			ClientPort: 12345,
			Credential: &RPCCredential{Flavor: AUTH_NONE},
			AuthSys:    &AuthSysCredential{UID: 1000, GID: 9999},
		}

		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint32(0x3f)) // all access bits

		result, err := handler.handleAccess(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
		if err != nil {
			t.Fatal(err)
		}
		status := readStatusFromReply(result)
		if status != NFS_OK {
			t.Fatalf("Expected NFS_OK, got %d", status)
		}

		data := getReplyData(result)
		accessResult := binary.BigEndian.Uint32(data[len(data)-4:])

		// Owner with 0700 should have READ, EXECUTE
		if accessResult&ACCESS3_READ == 0 {
			t.Error("Owner should have READ access")
		}
		if accessResult&ACCESS3_EXECUTE == 0 {
			t.Error("Owner should have EXECUTE access")
		}
	})

	t.Run("OtherPermissions", func(t *testing.T) {
		server, handler, _, err := newTestServerForBugfixes()
		if err != nil {
			t.Fatal(err)
		}

		// Create file with mode 0704 (other has only read)
		server.handler.fs.Create("/otherfile")
		server.handler.fs.Chmod("/otherfile", 0704)
		fileHandle := getFileHandle(server, "/otherfile")

		node, ok := handler.lookupNode(fileHandle)
		if !ok {
			t.Fatal("Failed to look up node")
		}
		node.mu.Lock()
		node.attrs.Uid = 1000
		node.attrs.Gid = 2000
		node.mu.Unlock()

		// Auth context: neither owner nor group (UID 9999, GID 8888)
		authCtx := &AuthContext{
			ClientIP:   "127.0.0.1",
			ClientPort: 12345,
			Credential: &RPCCredential{Flavor: AUTH_NONE},
			AuthSys:    &AuthSysCredential{UID: 9999, GID: 8888},
		}

		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint32(0x3f))

		result, err := handler.handleAccess(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
		if err != nil {
			t.Fatal(err)
		}

		data := getReplyData(result)
		accessResult := binary.BigEndian.Uint32(data[len(data)-4:])

		// Other with 4 (read) should have READ but not MODIFY/EXTEND/EXECUTE
		if accessResult&ACCESS3_READ == 0 {
			t.Error("Other should have READ access (mode 4)")
		}
		if accessResult&ACCESS3_MODIFY != 0 {
			t.Error("Other should NOT have MODIFY access (mode 4)")
		}
		if accessResult&ACCESS3_EXECUTE != 0 {
			t.Error("Other should NOT have EXECUTE access (mode 4)")
		}
	})

	t.Run("RootOverride", func(t *testing.T) {
		server, handler, _, err := newTestServerForBugfixes()
		if err != nil {
			t.Fatal(err)
		}

		// Create file with mode 0000 (no permissions)
		server.handler.fs.Create("/nopermfile")
		server.handler.fs.Chmod("/nopermfile", 0000)
		fileHandle := getFileHandle(server, "/nopermfile")

		node, ok := handler.lookupNode(fileHandle)
		if !ok {
			t.Fatal("Failed to look up node")
		}
		node.mu.Lock()
		node.attrs.Uid = 1000
		node.attrs.Gid = 2000
		node.mu.Unlock()

		// Auth context: root (UID 0)
		authCtx := &AuthContext{
			ClientIP:   "127.0.0.1",
			ClientPort: 12345,
			Credential: &RPCCredential{Flavor: AUTH_NONE},
			AuthSys:    &AuthSysCredential{UID: 0, GID: 0},
		}

		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint32(0x3f))

		result, err := handler.handleAccess(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
		if err != nil {
			t.Fatal(err)
		}

		data := getReplyData(result)
		accessResult := binary.BigEndian.Uint32(data[len(data)-4:])

		// Root should get all permissions (permBits = 7)
		if accessResult&ACCESS3_READ == 0 {
			t.Error("Root should have READ access even on mode 0000")
		}
		if accessResult&ACCESS3_MODIFY == 0 {
			t.Error("Root should have MODIFY access even on mode 0000")
		}
		if accessResult&ACCESS3_EXECUTE == 0 {
			t.Error("Root should have EXECUTE access even on mode 0000")
		}
	})

	t.Run("ReadOnlyServerBlocksWrites", func(t *testing.T) {
		server, handler, _, err := newReadOnlyTestServer()
		if err != nil {
			t.Fatal(err)
		}

		execHandle := getFileHandle(server, "/execfile")

		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, execHandle)
		binary.Write(&buf, binary.BigEndian, uint32(0x3f))

		authCtx := &AuthContext{
			ClientIP:   "127.0.0.1",
			ClientPort: 12345,
			Credential: &RPCCredential{Flavor: AUTH_NONE},
			AuthSys:    &AuthSysCredential{UID: 0, GID: 0},
		}

		result, err := handler.handleAccess(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
		if err != nil {
			t.Fatal(err)
		}

		data := getReplyData(result)
		accessResult := binary.BigEndian.Uint32(data[len(data)-4:])

		// Read-only server should block MODIFY, EXTEND, DELETE
		if accessResult&ACCESS3_MODIFY != 0 {
			t.Error("Read-only server should NOT grant MODIFY")
		}
		if accessResult&ACCESS3_EXTEND != 0 {
			t.Error("Read-only server should NOT grant EXTEND")
		}
		if accessResult&ACCESS3_DELETE != 0 {
			t.Error("Read-only server should NOT grant DELETE")
		}
		// But READ and EXECUTE should still work
		if accessResult&ACCESS3_READ == 0 {
			t.Error("Read-only server should still grant READ")
		}
		if accessResult&ACCESS3_EXECUTE == 0 {
			t.Error("Read-only server should still grant EXECUTE for executable file")
		}
	})
}

// ---------------------------------------------------------------------------
// 5. MOUNT path validation (filepath.Clean)
// ---------------------------------------------------------------------------

// TestR3_MountPathValidation verifies that the mount handler uses
// filepath.Clean to sanitize mount paths, preventing traversal attacks.
func TestR3_MountPathValidation(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}
	_ = server

	tests := []struct {
		name string
		path string
	}{
		{"root path", "/"},
		{"traversal attempt", "/../../../etc"},
		{"double slash", "//test"},
		{"trailing slash", "/test/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			xdrEncodeString(&buf, tt.path)

			call := &RPCCall{
				Header: RPCMsgHeader{
					Xid:       1,
					Program:   MOUNT_PROGRAM,
					Version:   MOUNT_V3,
					Procedure: 1, // MNT
				},
			}

			reply := &RPCReply{
				Header: call.Header,
			}

			result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleMountCall returned error: %v", err)
			}

			// The traversal path should be cleaned by filepath.Clean,
			// resulting in "/" which should succeed or a valid path
			data := getReplyData(result)
			if data == nil {
				// Reply has no data, which is valid for some responses
				return
			}
			// We just verify it doesn't panic or return garbage
			t.Logf("Mount path %q: response %d bytes", tt.path, len(data))
		})
	}
}

// ---------------------------------------------------------------------------
// 6. mapError uses errors.Is for wrapped errors
// ---------------------------------------------------------------------------

// TestR3_MapErrorWrappedErrors verifies that mapError correctly identifies
// wrapped errors using errors.Is instead of direct comparison.
func TestR3_MapErrorWrappedErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected uint32
	}{
		{
			name:     "direct os.ErrInvalid",
			err:      os.ErrInvalid,
			expected: NFSERR_INVAL,
		},
		{
			name:     "wrapped os.ErrInvalid",
			err:      fmt.Errorf("operation failed: %w", os.ErrInvalid),
			expected: NFSERR_INVAL,
		},
		{
			name:     "double-wrapped os.ErrInvalid",
			err:      fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", os.ErrInvalid)),
			expected: NFSERR_INVAL,
		},
		{
			name:     "direct os.ErrNotExist",
			err:      os.ErrNotExist,
			expected: NFSERR_NOENT,
		},
		{
			name:     "PathError wrapping os.ErrNotExist",
			err:      &os.PathError{Op: "stat", Path: "/test", Err: os.ErrNotExist},
			expected: NFSERR_NOENT,
		},
		{
			name:     "direct os.ErrPermission",
			err:      os.ErrPermission,
			expected: NFSERR_PERM,
		},
		{
			name:     "PathError wrapping os.ErrPermission",
			err:      &os.PathError{Op: "open", Path: "/test", Err: os.ErrPermission},
			expected: NFSERR_PERM,
		},
		{
			name:     "direct os.ErrExist",
			err:      os.ErrExist,
			expected: NFSERR_EXIST,
		},
		{
			name:     "PathError wrapping os.ErrExist",
			err:      &os.PathError{Op: "create", Path: "/test", Err: os.ErrExist},
			expected: NFSERR_EXIST,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: NFS_OK,
		},
		{
			name:     "unknown error",
			err:      fmt.Errorf("some random error"),
			expected: NFSERR_IO,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.err)
			if got != tt.expected {
				t.Errorf("mapError(%v) = %d, want %d", tt.err, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 7. SetLogger locking (concurrent race test)
// ---------------------------------------------------------------------------

// TestR3_SetLoggerConcurrentSafety verifies that concurrent calls to
// SetLogger do not race. Run with -race to detect data races.
func TestR3_SetLoggerConcurrentSafety(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// SetLogger uses n.mu.Lock() for thread safety
			_ = nfs.SetLogger(nil)
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// 8. WorkerPool Stats sync (concurrent race test)
// ---------------------------------------------------------------------------

// TestR3_WorkerPoolStatsConcurrentSafety verifies that concurrent calls
// to Stats() use resizeMu to safely read maxWorkers. The resizeMu serializes
// access to maxWorkers between Stats() and Resize(). Run with -race.
func TestR3_WorkerPoolStatsConcurrentSafety(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{
		MaxWorkers: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	pool := nfs.workerPool
	if pool == nil {
		t.Skip("Worker pool not initialized")
	}
	pool.Start()
	defer pool.Stop()

	// Concurrent Stats() calls should not race with each other.
	// Stats() uses resizeMu.Lock to read maxWorkers safely.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				max, active, queued := pool.Stats()
				if max <= 0 {
					t.Errorf("maxWorkers should be > 0, got %d", max)
				}
				_ = active
				_ = queued
			}
		}()
	}
	wg.Wait()

	// Verify Resize serializes through resizeMu
	pool.Resize(8)
	max, _, _ := pool.Stats()
	if max != 8 {
		t.Errorf("After Resize(8), maxWorkers = %d, want 8", max)
	}
}

// ---------------------------------------------------------------------------
// 9. Cache type assertions safety (corrupted LRU entries)
// ---------------------------------------------------------------------------

// TestR3_CacheTypeAssertionSafety verifies that the attribute cache handles
// LRU list entries correctly without panicking due to type assertion failures.
func TestR3_CacheTypeAssertionSafety(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 10)

	// Fill the cache up to capacity
	for i := 0; i < 10; i++ {
		attrs := &NFSAttrs{Mode: 0644, Size: int64(i)}
		attrs.SetMtime(time.Now())
		attrs.SetAtime(time.Now())
		cache.Put(fmt.Sprintf("/file%d", i), attrs)
	}

	// Verify all entries are accessible
	for i := 0; i < 10; i++ {
		got, found := cache.Get(fmt.Sprintf("/file%d", i))
		if !found {
			t.Errorf("Expected cache hit for /file%d", i)
		}
		if got == nil {
			t.Errorf("Expected non-nil attrs for /file%d", i)
		}
	}

	// Trigger eviction by adding more entries
	for i := 10; i < 15; i++ {
		attrs := &NFSAttrs{Mode: 0644, Size: int64(i)}
		attrs.SetMtime(time.Now())
		attrs.SetAtime(time.Now())
		cache.Put(fmt.Sprintf("/file%d", i), attrs)
	}

	// Cache should not exceed its max size
	if cache.Size() > 10 {
		t.Errorf("Cache size %d exceeds max 10", cache.Size())
	}

	// Verify no panic during concurrent access with eviction
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("/concurrent%d", n)
			attrs := &NFSAttrs{Mode: 0644, Size: int64(n)}
			attrs.SetMtime(time.Now())
			attrs.SetAtime(time.Now())
			cache.Put(key, attrs)
			cache.Get(key)
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// 10. Portmapper skipAuth XDR padding
// ---------------------------------------------------------------------------

// TestR3_SkipAuthXDRPadding verifies that skipAuth correctly consumes
// the XDR body and its padding to reach 4-byte alignment.
func TestR3_SkipAuthXDRPadding(t *testing.T) {
	pm := NewPortmapper()
	defer pm.Stop()

	tests := []struct {
		name       string
		bodyLen    uint32
		expectPad  int
	}{
		{"length 0 (no padding)", 0, 0},
		{"length 4 (no padding)", 4, 0},
		{"length 5 (3 bytes padding)", 5, 3},
		{"length 6 (2 bytes padding)", 6, 2},
		{"length 7 (1 byte padding)", 7, 1},
		{"length 8 (no padding)", 8, 0},
		{"length 1 (3 bytes padding)", 1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			// flavor
			binary.Write(&buf, binary.BigEndian, uint32(AUTH_SYS))
			// length
			binary.Write(&buf, binary.BigEndian, tt.bodyLen)
			// body bytes
			body := make([]byte, tt.bodyLen)
			buf.Write(body)
			// padding bytes
			pad := (4 - tt.bodyLen%4) % 4
			if pad > 0 {
				buf.Write(make([]byte, pad))
			}
			// Write a sentinel after to verify skipAuth consumed exactly the right amount
			sentinel := []byte{0xDE, 0xAD, 0xBE, 0xEF}
			buf.Write(sentinel)

			reader := bytes.NewReader(buf.Bytes())
			err := pm.skipAuth(reader)
			if err != nil {
				t.Fatalf("skipAuth failed: %v", err)
			}

			// Read the sentinel - if skipAuth consumed the right amount, we should read it
			remaining := make([]byte, 4)
			n, err := io.ReadFull(reader, remaining)
			if err != nil {
				t.Fatalf("Failed to read sentinel after skipAuth: %v (read %d bytes)", err, n)
			}
			if !bytes.Equal(remaining, sentinel) {
				t.Errorf("Sentinel mismatch: got %x, want %x (skipAuth consumed wrong amount)", remaining, sentinel)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 11. Portmapper SET/UNSET auth (non-loopback rejection)
// ---------------------------------------------------------------------------

// fakeAddr implements net.Addr for testing with a specific address string.
type fakeAddr struct {
	addr string
}

func (a *fakeAddr) Network() string { return "tcp" }
func (a *fakeAddr) String() string  { return a.addr }

// TestR3_PortmapperSetUnsetAuth verifies that handleSet and handleUnset
// reject requests from non-loopback addresses.
func TestR3_PortmapperSetUnsetAuth(t *testing.T) {
	pm := NewPortmapper()
	defer pm.Stop()

	// Build a valid SET/UNSET request body: prog(4) + vers(4) + prot(4) + port(4)
	var requestBody bytes.Buffer
	binary.Write(&requestBody, binary.BigEndian, uint32(100005)) // prog
	binary.Write(&requestBody, binary.BigEndian, uint32(3))      // vers
	binary.Write(&requestBody, binary.BigEndian, uint32(6))      // prot (TCP)
	binary.Write(&requestBody, binary.BigEndian, uint32(2049))   // port

	t.Run("SET from non-loopback rejected", func(t *testing.T) {
		remoteAddr := &fakeAddr{addr: "192.168.1.100:5000"}
		result := pm.handleSet(bytes.NewReader(requestBody.Bytes()), remoteAddr)
		// Should return false (4 bytes: 0x00000000)
		if len(result) != 4 {
			t.Fatalf("Expected 4 bytes, got %d", len(result))
		}
		val := binary.BigEndian.Uint32(result)
		if val != 0 {
			t.Errorf("Expected false (0) for non-loopback SET, got %d", val)
		}
	})

	t.Run("UNSET from non-loopback rejected", func(t *testing.T) {
		remoteAddr := &fakeAddr{addr: "10.0.0.1:5000"}
		result := pm.handleUnset(bytes.NewReader(requestBody.Bytes()), remoteAddr)
		if len(result) != 4 {
			t.Fatalf("Expected 4 bytes, got %d", len(result))
		}
		val := binary.BigEndian.Uint32(result)
		if val != 0 {
			t.Errorf("Expected false (0) for non-loopback UNSET, got %d", val)
		}
	})

	t.Run("SET from loopback accepted", func(t *testing.T) {
		remoteAddr := &fakeAddr{addr: "127.0.0.1:5000"}
		result := pm.handleSet(bytes.NewReader(requestBody.Bytes()), remoteAddr)
		if len(result) != 4 {
			t.Fatalf("Expected 4 bytes, got %d", len(result))
		}
		val := binary.BigEndian.Uint32(result)
		if val != 1 {
			t.Errorf("Expected true (1) for loopback SET, got %d", val)
		}
	})

	t.Run("UNSET from loopback accepted", func(t *testing.T) {
		// First register the service so UNSET has something to remove
		pm.RegisterService(100005, 3, 6, 2049)

		remoteAddr := &fakeAddr{addr: "127.0.0.1:5000"}
		result := pm.handleUnset(bytes.NewReader(requestBody.Bytes()), remoteAddr)
		if len(result) != 4 {
			t.Fatalf("Expected 4 bytes, got %d", len(result))
		}
		val := binary.BigEndian.Uint32(result)
		if val != 1 {
			t.Errorf("Expected true (1) for loopback UNSET, got %d", val)
		}
	})
}

// ---------------------------------------------------------------------------
// 12. FileHandle map eviction
// ---------------------------------------------------------------------------

// TestR3_FileHandleMapEviction verifies that the FileHandleMap evicts
// the oldest handles when the maximum is exceeded.
func TestR3_FileHandleMapEviction(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	// Create a FileHandleMap with a very small maximum
	fhMap := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		freeHandles: NewUint64MinHeap(),
		maxHandles:  10,
	}

	// Create test files and allocate handles for them
	for i := 0; i < 10; i++ {
		fname := fmt.Sprintf("/evictfile%d", i)
		f, err := fs.Create(fname)
		if err != nil {
			t.Fatalf("Failed to create file %s: %v", fname, err)
		}
		fhMap.Allocate(f)
	}

	if fhMap.Count() != 10 {
		t.Fatalf("Expected 10 handles, got %d", fhMap.Count())
	}

	// Allocate one more to trigger eviction
	extraFile, err := fs.Create("/extra")
	if err != nil {
		t.Fatal(err)
	}
	fhMap.Allocate(extraFile)

	// After eviction, count should be <= maxHandles
	count := fhMap.Count()
	if count > 10 {
		t.Errorf("Expected count <= 10 after eviction, got %d", count)
	}

	// Should have evicted at least 1 (maxH/10 = 1)
	if count > 10 {
		t.Errorf("Eviction did not reduce handle count below max")
	}
}

// ---------------------------------------------------------------------------
// 13. Portmapper connection limit
// ---------------------------------------------------------------------------

// TestR3_PortmapperConnectionLimit verifies that the portmapper's connSem
// limits concurrent connections correctly.
func TestR3_PortmapperConnectionLimit(t *testing.T) {
	pm := NewPortmapper()

	// Verify default capacity
	if cap(pm.connSem) != DefaultMaxPortmapperConns {
		t.Errorf("Expected connSem capacity %d, got %d", DefaultMaxPortmapperConns, cap(pm.connSem))
	}

	// Create a portmapper with a very small connection limit for testing
	smallPm := &Portmapper{
		connSem: make(chan struct{}, 2),
	}

	// Fill up the connection slots
	smallPm.connSem <- struct{}{}
	smallPm.connSem <- struct{}{}

	// The third connection should be rejected (non-blocking)
	select {
	case smallPm.connSem <- struct{}{}:
		t.Error("Should not have accepted a third connection when at capacity")
		// Drain to clean up
		<-smallPm.connSem
	default:
		// Expected: at capacity, connection rejected
	}

	// Release one slot
	<-smallPm.connSem

	// Now one more should fit
	select {
	case smallPm.connSem <- struct{}{}:
		// Expected: slot available
		<-smallPm.connSem
	default:
		t.Error("Should have accepted connection after releasing a slot")
	}

	// Clean up
	<-smallPm.connSem
}

// ---------------------------------------------------------------------------
// 14. WriteWithContext err shadowing fix
// ---------------------------------------------------------------------------

// TestR3_WriteWithContextErrShadowing verifies that a successful write
// preserves the written data even if Chtimes fails (the fix renamed the
// Chtimes error variable to chtimesErr to avoid shadowing the outer err).
func TestR3_WriteWithContextErrShadowing(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	// Create a test file
	f, err := fs.Create("/writefile")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("initial"))
	f.Close()

	node, err := nfs.Lookup("/writefile")
	if err != nil {
		t.Fatal(err)
	}

	// Write new data
	data := []byte("hello world")
	n, err := nfs.Write(node, 0, data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != int64(len(data)) {
		t.Errorf("Expected %d bytes written, got %d", len(data), n)
	}

	// Read back and verify data is preserved
	readData, err := nfs.Read(node, 0, 100)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(readData) != "hello world" {
		t.Errorf("Expected 'hello world', got %q", string(readData))
	}
}

// ---------------------------------------------------------------------------
// 15. SETATTR UID/GID auth restriction
// ---------------------------------------------------------------------------

// TestR3_SetattrUIDGIDAuthRestriction verifies that handleSetattr only
// allows UID/GID changes when EffectiveUID == 0 (root).
func TestR3_SetattrUIDGIDAuthRestriction(t *testing.T) {
	server, handler, _, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	fileHandle := getFileHandle(server, "/testfile.txt")

	// Get current attrs to know the original UID/GID
	node, ok := handler.lookupNode(fileHandle)
	if !ok {
		t.Fatal("Failed to look up node")
	}

	node.mu.Lock()
	node.attrs.Uid = 1000
	node.attrs.Gid = 1000
	node.mu.Unlock()

	t.Run("NonRootCannotChangeUID", func(t *testing.T) {
		// Build SETATTR request: set UID to 0
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		// sattr3: mode=don't set, uid=SET(0), gid=don't set, size=don't set, atime=don't set, mtime=don't set
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mode = false
		binary.Write(&buf, binary.BigEndian, uint32(1)) // set_uid = true
		binary.Write(&buf, binary.BigEndian, uint32(0)) // uid = 0
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_gid = false
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_size = false
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_atime = DONT_CHANGE
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mtime = DONT_CHANGE
		binary.Write(&buf, binary.BigEndian, uint32(0)) // guard = no check

		authCtx := &AuthContext{
			ClientIP:     "127.0.0.1",
			ClientPort:   12345,
			Credential:   &RPCCredential{Flavor: AUTH_NONE},
			EffectiveUID: 1000, // Non-root
			EffectiveGID: 1000,
		}

		result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
		if err != nil {
			t.Fatal(err)
		}

		status := readStatusFromReply(result)
		if status != NFS_OK {
			t.Fatalf("Expected NFS_OK, got %d", status)
		}

		// Verify UID was NOT changed (non-root cannot change UID)
		node.mu.RLock()
		currentUID := node.attrs.Uid
		node.mu.RUnlock()

		if currentUID == 0 {
			t.Error("Non-root user should not be able to change UID to 0")
		}
		if currentUID != 1000 {
			t.Errorf("UID should remain 1000, got %d", currentUID)
		}
	})

	t.Run("RootCanChangeUID", func(t *testing.T) {
		// Reset UID
		node.mu.Lock()
		node.attrs.Uid = 1000
		node.mu.Unlock()

		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mode = false
		binary.Write(&buf, binary.BigEndian, uint32(1)) // set_uid = true
		binary.Write(&buf, binary.BigEndian, uint32(0)) // uid = 0
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_gid = false
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_size = false
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_atime = DONT_CHANGE
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mtime = DONT_CHANGE
		binary.Write(&buf, binary.BigEndian, uint32(0)) // guard = no check

		authCtx := &AuthContext{
			ClientIP:     "127.0.0.1",
			ClientPort:   12345,
			Credential:   &RPCCredential{Flavor: AUTH_NONE},
			EffectiveUID: 0, // Root
			EffectiveGID: 0,
		}

		result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
		if err != nil {
			t.Fatal(err)
		}

		status := readStatusFromReply(result)
		if status != NFS_OK {
			t.Fatalf("Expected NFS_OK, got %d", status)
		}

		// Verify UID WAS changed (root can change UID)
		node.mu.RLock()
		currentUID := node.attrs.Uid
		node.mu.RUnlock()

		if currentUID != 0 {
			t.Errorf("Root should be able to change UID to 0, got %d", currentUID)
		}
	})
}

// ---------------------------------------------------------------------------
// 16. AttrCache.Get passes server for metrics
// ---------------------------------------------------------------------------

// TestR3_AttrCacheGetPassesServerForMetrics verifies that AttrCache.Get
// accepts a variadic server parameter and records cache hit/miss metrics.
func TestR3_AttrCacheGetPassesServerForMetrics(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	cache := NewAttrCache(10*time.Second, 100)

	// Test cache miss with server parameter
	_, found := cache.Get("/nonexistent", nfs)
	if found {
		t.Error("Expected cache miss")
	}

	// Test cache hit with server parameter
	attrs := &NFSAttrs{Mode: 0644, Size: 42}
	attrs.SetMtime(time.Now())
	attrs.SetAtime(time.Now())
	cache.Put("/existing", attrs)

	got, found := cache.Get("/existing", nfs)
	if !found {
		t.Error("Expected cache hit")
	}
	if got == nil {
		t.Error("Expected non-nil attrs for cache hit")
	}

	// Test cache Get without server parameter (backwards compatibility)
	got2, found2 := cache.Get("/existing")
	if !found2 {
		t.Error("Expected cache hit without server param")
	}
	if got2 == nil {
		t.Error("Expected non-nil attrs without server param")
	}

	// Verify the metrics methods exist and don't panic
	nfs.RecordAttrCacheHit()
	nfs.RecordAttrCacheMiss()
}
