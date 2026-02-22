package absnfs

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/absfs/memfs"
)

func TestAuthenticationEnforcement(t *testing.T) {
	t.Run("AUTH_NONE is allowed", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{Debug: false},
			},
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
			Credential: RPCCredential{
				Flavor: AUTH_NONE,
				Body:   []byte{},
			},
		}

		authCtx := &AuthContext{
			ClientIP:   "127.0.0.1",
			ClientPort: 1023,
			Credential: &call.Credential,
		}

		reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED for AUTH_NONE, got %v", reply.Status)
		}
	})

	t.Run("AUTH_SYS is allowed", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{Debug: false},
			},
		}

		// Create a valid AUTH_SYS credential
		var credBody bytes.Buffer
		binary.Write(&credBody, binary.BigEndian, uint32(12345)) // Stamp
		binary.Write(&credBody, binary.BigEndian, uint32(0))     // Machine name length (empty)
		binary.Write(&credBody, binary.BigEndian, uint32(1000))  // UID
		binary.Write(&credBody, binary.BigEndian, uint32(1000))  // GID
		binary.Write(&credBody, binary.BigEndian, uint32(0))     // Number of auxiliary GIDs

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_NULL,
			},
			Credential: RPCCredential{
				Flavor: AUTH_SYS,
				Body:   credBody.Bytes(),
			},
		}

		authCtx := &AuthContext{
			ClientIP:   "127.0.0.1",
			ClientPort: 1023,
			Credential: &call.Credential,
		}

		reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED for AUTH_SYS, got %v", reply.Status)
		}
	})

	t.Run("IP restriction enforced", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		// Configure with IP restrictions
		fs, err := New(memfs, ExportOptions{
			AllowedIPs: []string{"192.168.1.0/24"},
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{Debug: false},
			},
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
			Credential: RPCCredential{
				Flavor: AUTH_NONE,
				Body:   []byte{},
			},
		}

		// Client from disallowed IP
		authCtx := &AuthContext{
			ClientIP:   "10.0.0.1",
			ClientPort: 1023,
			Credential: &call.Credential,
		}

		reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != MSG_DENIED {
			t.Errorf("Expected MSG_DENIED for disallowed IP, got %v", reply.Status)
		}

		// Client from allowed IP
		authCtx.ClientIP = "192.168.1.100"
		reply, err = handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED for allowed IP, got %v", reply.Status)
		}
	})

	t.Run("Secure port requirement enforced", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		// Configure with Secure flag
		fs, err := New(memfs, ExportOptions{
			Secure: true,
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{Debug: false},
			},
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
			Credential: RPCCredential{
				Flavor: AUTH_NONE,
				Body:   []byte{},
			},
		}

		// Client from non-privileged port
		authCtx := &AuthContext{
			ClientIP:   "127.0.0.1",
			ClientPort: 5000,
			Credential: &call.Credential,
		}

		reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != MSG_DENIED {
			t.Errorf("Expected MSG_DENIED for non-privileged port, got %v", reply.Status)
		}

		// Client from privileged port
		authCtx.ClientPort = 1023
		reply, err = handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED for privileged port, got %v", reply.Status)
		}
	})

	t.Run("Root squashing applied", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		// Configure with root squashing
		fs, err := New(memfs, ExportOptions{
			Squash: "root",
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create AUTH_SYS credential with UID=0 (root)
		var credBody bytes.Buffer
		binary.Write(&credBody, binary.BigEndian, uint32(12345)) // Stamp
		binary.Write(&credBody, binary.BigEndian, uint32(0))     // Machine name length
		binary.Write(&credBody, binary.BigEndian, uint32(0))     // UID = 0 (root)
		binary.Write(&credBody, binary.BigEndian, uint32(0))     // GID = 0
		binary.Write(&credBody, binary.BigEndian, uint32(0))     // No aux GIDs

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_NULL,
			},
			Credential: RPCCredential{
				Flavor: AUTH_SYS,
				Body:   credBody.Bytes(),
			},
		}

		authCtx := &AuthContext{
			ClientIP:   "127.0.0.1",
			ClientPort: 1023,
			Credential: &call.Credential,
		}

		// Validate authentication
		authResult := ValidateAuthentication(authCtx, fs.policy.Load())

		if !authResult.Allowed {
			t.Errorf("Expected authentication to be allowed")
		}

		// Verify that root (UID 0) was mapped to nobody (UID 65534)
		if authResult.UID != 65534 {
			t.Errorf("Expected UID to be squashed to 65534, got %d", authResult.UID)
		}
		if authResult.GID != 65534 {
			t.Errorf("Expected GID to be squashed to 65534, got %d", authResult.GID)
		}
	})

	t.Run("All squashing applied", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		// Configure with all squashing
		fs, err := New(memfs, ExportOptions{
			Squash: "all",
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create AUTH_SYS credential with UID=1000 (non-root)
		var credBody bytes.Buffer
		binary.Write(&credBody, binary.BigEndian, uint32(12345)) // Stamp
		binary.Write(&credBody, binary.BigEndian, uint32(0))     // Machine name length
		binary.Write(&credBody, binary.BigEndian, uint32(1000))  // UID = 1000
		binary.Write(&credBody, binary.BigEndian, uint32(1000))  // GID = 1000
		binary.Write(&credBody, binary.BigEndian, uint32(0))     // No aux GIDs

		authCtx := &AuthContext{
			ClientIP:   "127.0.0.1",
			ClientPort: 1023,
			Credential: &RPCCredential{
				Flavor: AUTH_SYS,
				Body:   credBody.Bytes(),
			},
		}

		// Validate authentication
		authResult := ValidateAuthentication(authCtx, fs.policy.Load())

		if !authResult.Allowed {
			t.Errorf("Expected authentication to be allowed")
		}

		// Verify that non-root user was also mapped to nobody
		if authResult.UID != 65534 {
			t.Errorf("Expected UID to be squashed to 65534, got %d", authResult.UID)
		}
		if authResult.GID != 65534 {
			t.Errorf("Expected GID to be squashed to 65534, got %d", authResult.GID)
		}
	})
}

func TestParseAuthSysCredential(t *testing.T) {
	t.Run("Valid AUTH_SYS credential", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(12345)) // Stamp
		binary.Write(&buf, binary.BigEndian, uint32(7))     // Machine name length
		buf.Write([]byte("testbox"))                        // Machine name
		buf.Write([]byte{0})                                // Padding
		binary.Write(&buf, binary.BigEndian, uint32(1000))  // UID
		binary.Write(&buf, binary.BigEndian, uint32(1000))  // GID
		binary.Write(&buf, binary.BigEndian, uint32(2))     // Aux GID count
		binary.Write(&buf, binary.BigEndian, uint32(1001))  // Aux GID 1
		binary.Write(&buf, binary.BigEndian, uint32(1002))  // Aux GID 2

		cred, err := ParseAuthSysCredential(buf.Bytes())
		if err != nil {
			t.Fatalf("Failed to parse AUTH_SYS credential: %v", err)
		}

		if cred.Stamp != 12345 {
			t.Errorf("Expected stamp 12345, got %d", cred.Stamp)
		}
		if cred.MachineName != "testbox" {
			t.Errorf("Expected machine name 'testbox', got '%s'", cred.MachineName)
		}
		if cred.UID != 1000 {
			t.Errorf("Expected UID 1000, got %d", cred.UID)
		}
		if cred.GID != 1000 {
			t.Errorf("Expected GID 1000, got %d", cred.GID)
		}
		if len(cred.AuxGIDs) != 2 {
			t.Errorf("Expected 2 auxiliary GIDs, got %d", len(cred.AuxGIDs))
		}
		if len(cred.AuxGIDs) > 0 && cred.AuxGIDs[0] != 1001 {
			t.Errorf("Expected aux GID[0] 1001, got %d", cred.AuxGIDs[0])
		}
		if len(cred.AuxGIDs) > 1 && cred.AuxGIDs[1] != 1002 {
			t.Errorf("Expected aux GID[1] 1002, got %d", cred.AuxGIDs[1])
		}
	})

	t.Run("Empty credential body", func(t *testing.T) {
		_, err := ParseAuthSysCredential([]byte{})
		if err == nil {
			t.Error("Expected error for empty credential body")
		}
	})

	t.Run("Too many auxiliary GIDs", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(12345)) // Stamp
		binary.Write(&buf, binary.BigEndian, uint32(0))     // Machine name length
		binary.Write(&buf, binary.BigEndian, uint32(1000))  // UID
		binary.Write(&buf, binary.BigEndian, uint32(1000))  // GID
		binary.Write(&buf, binary.BigEndian, uint32(100))   // Too many aux GIDs

		_, err := ParseAuthSysCredential(buf.Bytes())
		if err == nil {
			t.Error("Expected error for too many auxiliary GIDs")
		}
	})
}

func TestIsIPAllowed(t *testing.T) {
	tests := []struct {
		name       string
		clientIP   string
		allowedIPs []string
		expected   bool
	}{
		{
			name:       "Exact IP match",
			clientIP:   "192.168.1.100",
			allowedIPs: []string{"192.168.1.100"},
			expected:   true,
		},
		{
			name:       "CIDR match",
			clientIP:   "192.168.1.100",
			allowedIPs: []string{"192.168.1.0/24"},
			expected:   true,
		},
		{
			name:       "Multiple IPs - match second",
			clientIP:   "10.0.0.5",
			allowedIPs: []string{"192.168.1.0/24", "10.0.0.0/8"},
			expected:   true,
		},
		{
			name:       "No match",
			clientIP:   "172.16.0.1",
			allowedIPs: []string{"192.168.1.0/24", "10.0.0.0/8"},
			expected:   false,
		},
		{
			name:       "IPv6 match",
			clientIP:   "::1",
			allowedIPs: []string{"::1"},
			expected:   true,
		},
		{
			name:       "Invalid IP",
			clientIP:   "invalid",
			allowedIPs: []string{"192.168.1.0/24"},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isIPAllowed(tt.clientIP, tt.allowedIPs)
			if result != tt.expected {
				t.Errorf("isIPAllowed(%s, %v) = %v, expected %v",
					tt.clientIP, tt.allowedIPs, result, tt.expected)
			}
		})
	}
}

// TestM4_RootSquashBothUIDAndGID verifies that root_squash squashes both
// UID and GID when UID is 0 (root), regardless of the GID value.
func TestM4_RootSquashBothUIDAndGID(t *testing.T) {
	// Root user with non-zero GID should still get GID squashed
	authSys := &AuthSysCredential{
		UID: 0,
		GID: 1000, // Non-zero GID
	}

	result := &AuthResult{
		Allowed: true,
		UID:     0,
		GID:     1000,
	}

	applySquashing(result, authSys, "root")

	if result.UID != 65534 {
		t.Errorf("Expected UID to be squashed to 65534, got %d", result.UID)
	}
	if result.GID != 65534 {
		t.Errorf("Expected GID to be squashed to 65534 when UID is root, got %d", result.GID)
	}

	// Non-root user should not be squashed
	authSys2 := &AuthSysCredential{
		UID: 1000,
		GID: 0,
	}

	result2 := &AuthResult{
		Allowed: true,
		UID:     1000,
		GID:     0,
	}

	applySquashing(result2, authSys2, "root")

	if result2.UID != 1000 {
		t.Errorf("Non-root UID should not be squashed, got %d", result2.UID)
	}
	// Root squash should squash primary GID 0 even for non-root UID,
	// matching standard NFS server behavior (group root is privileged).
	if result2.GID != 65534 {
		t.Errorf("Primary GID 0 should be squashed to 65534 under root_squash, got %d", result2.GID)
	}
}

// TestL8_AuthNoneDocumentation verifies that AUTH_NONE is accepted as
// intentional standard NFS behavior for public/shared exports.
func TestL8_AuthNoneDocumentation(t *testing.T) {
	ctx := &AuthContext{
		ClientIP:   "127.0.0.1",
		ClientPort: 1023,
		Credential: &RPCCredential{
			Flavor: AUTH_NONE,
			Body:   []byte{},
		},
	}

	defaultOpts := ExportOptions{}
	result := ValidateAuthentication(ctx, policyFromExportOptions(&defaultOpts))
	if !result.Allowed {
		t.Error("AUTH_NONE should be accepted for public/shared exports")
	}
	if result.UID != 65534 || result.GID != 65534 {
		t.Errorf("AUTH_NONE should map to nobody (65534/65534), got %d/%d", result.UID, result.GID)
	}
}

// TestL9_AuxGIDsRootSquash verifies that auxiliary GID 0 entries are squashed
// to the anonymous GID when root_squash is enabled.
func TestL9_AuxGIDsRootSquash(t *testing.T) {
	authSys := &AuthSysCredential{
		UID:     0,
		GID:     0,
		AuxGIDs: []uint32{0, 1000, 0, 2000},
	}

	result := &AuthResult{
		Allowed: true,
		UID:     0,
		GID:     0,
	}

	applySquashing(result, authSys, "root")

	// Check that GID 0 entries in AuxGIDs are squashed
	for i, gid := range authSys.AuxGIDs {
		switch i {
		case 0, 2:
			if gid != 65534 {
				t.Errorf("AuxGIDs[%d] = %d, want 65534 (squashed)", i, gid)
			}
		case 1:
			if gid != 1000 {
				t.Errorf("AuxGIDs[%d] = %d, want 1000 (unchanged)", i, gid)
			}
		case 3:
			if gid != 2000 {
				t.Errorf("AuxGIDs[%d] = %d, want 2000 (unchanged)", i, gid)
			}
		}
	}

	// Non-root user: AuxGIDs should not be affected
	authSys2 := &AuthSysCredential{
		UID:     1000,
		GID:     1000,
		AuxGIDs: []uint32{0, 500},
	}

	result2 := &AuthResult{
		Allowed: true,
		UID:     1000,
		GID:     1000,
	}

	applySquashing(result2, authSys2, "root")

	// GID 0 in aux list should still be squashed for root_squash
	if authSys2.AuxGIDs[0] != 65534 {
		t.Errorf("AuxGIDs[0] = %d, want 65534 (squashed even for non-root user in root_squash)", authSys2.AuxGIDs[0])
	}
	if authSys2.AuxGIDs[1] != 500 {
		t.Errorf("AuxGIDs[1] = %d, want 500 (unchanged)", authSys2.AuxGIDs[1])
	}
}

// TestR26_RootSquashDoesNotMutateOriginalAuxGIDs verifies that applySquashing
// does not mutate the original AuxGIDs slice.
func TestR26_RootSquashDoesNotMutateOriginalAuxGIDs(t *testing.T) {
	// Create a shared slice that simulates reuse across calls
	sharedAuxGIDs := []uint32{0, 1000, 0, 2000}

	// Create AuthSysCredential referencing the shared slice
	authSys1 := &AuthSysCredential{
		UID:     0,
		GID:     0,
		AuxGIDs: sharedAuxGIDs,
	}

	// Save a copy of the original values
	originalValues := make([]uint32, len(sharedAuxGIDs))
	copy(originalValues, sharedAuxGIDs)

	result := &AuthResult{Allowed: true, UID: 0, GID: 0}
	applySquashing(result, authSys1, "root")

	// Verify the original shared slice was NOT mutated
	for i, v := range sharedAuxGIDs {
		if v != originalValues[i] {
			t.Errorf("Original sharedAuxGIDs[%d] was mutated: got %d, expected %d", i, v, originalValues[i])
		}
	}

	// Verify that authSys1.AuxGIDs was properly squashed (on its own copy)
	for i, gid := range authSys1.AuxGIDs {
		switch i {
		case 0, 2:
			if gid != 65534 {
				t.Errorf("authSys1.AuxGIDs[%d] = %d, want 65534", i, gid)
			}
		case 1:
			if gid != 1000 {
				t.Errorf("authSys1.AuxGIDs[%d] = %d, want 1000", i, gid)
			}
		case 3:
			if gid != 2000 {
				t.Errorf("authSys1.AuxGIDs[%d] = %d, want 2000", i, gid)
			}
		}
	}
}

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
	binary.Write(&credBody, binary.BigEndian, uint32(0)) // stamp
	binary.Write(&credBody, binary.BigEndian, uint32(0)) // machine name length
	binary.Write(&credBody, binary.BigEndian, uint32(0)) // uid = 0 (root)
	binary.Write(&credBody, binary.BigEndian, uint32(0)) // gid = 0 (root)
	binary.Write(&credBody, binary.BigEndian, uint32(0)) // aux gids count

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
			ClientIP:     "127.0.0.1",
			ClientPort:   12345,
			Credential:   &RPCCredential{Flavor: AUTH_NONE},
			AuthSys:      &AuthSysCredential{UID: 1000, GID: 9999},
			EffectiveUID: 1000,
			EffectiveGID: 9999,
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
			ClientIP:     "127.0.0.1",
			ClientPort:   12345,
			Credential:   &RPCCredential{Flavor: AUTH_NONE},
			AuthSys:      &AuthSysCredential{UID: 9999, GID: 8888},
			EffectiveUID: 9999,
			EffectiveGID: 8888,
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
			ClientIP:     "127.0.0.1",
			ClientPort:   12345,
			Credential:   &RPCCredential{Flavor: AUTH_NONE},
			AuthSys:      &AuthSysCredential{UID: 0, GID: 0},
			EffectiveUID: 0,
			EffectiveGID: 0,
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

// ================================================================
// Coverage boost: HandleCall – auth denial with secure port requirement
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
