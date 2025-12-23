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
		authResult := ValidateAuthentication(authCtx, fs.options)

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
		authResult := ValidateAuthentication(authCtx, fs.options)

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
