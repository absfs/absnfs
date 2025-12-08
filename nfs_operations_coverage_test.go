package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// TestNFSOperationsErrorPaths targets specific error paths in nfs_operations.go
func TestNFSOperationsErrorPaths(t *testing.T) {
	server, err := newTestServer()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	handler := &NFSProcedureHandler{server: server}

	// Set up real test handles
	_, err = server.handler.Lookup("/")
	if err != nil {
		t.Fatalf("Failed to lookup root directory: %v", err)
	}

	// Get file handle
	fileNode, err := server.handler.Lookup("/testfile.txt")
	if err != nil {
		t.Fatalf("Failed to lookup test file: %v", err)
	}
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("binary.Read errors", func(t *testing.T) {
		// Test with invalid reader that always returns error
		badReader := &badReader{}
		
		testCases := []struct {
			name      string
			procedure uint32
		}{
			{"GETATTR binary.Read error", NFSPROC3_GETATTR},
			{"SETATTR binary.Read error", NFSPROC3_SETATTR},
			{"LOOKUP binary.Read error", NFSPROC3_LOOKUP},
			{"READ binary.Read error", NFSPROC3_READ},
			{"WRITE binary.Read error", NFSPROC3_WRITE},
			{"CREATE binary.Read error", NFSPROC3_CREATE},
			{"MKDIR binary.Read error", NFSPROC3_MKDIR},
			{"READDIR binary.Read error", NFSPROC3_READDIR},
			{"READDIRPLUS binary.Read error", NFSPROC3_READDIRPLUS},
			{"FSSTAT binary.Read error", NFSPROC3_FSSTAT},
			{"FSINFO binary.Read error", NFSPROC3_FSINFO},
			{"PATHCONF binary.Read error", NFSPROC3_PATHCONF},
			{"ACCESS binary.Read error", NFSPROC3_ACCESS},
			{"COMMIT binary.Read error", NFSPROC3_COMMIT},
			{"REMOVE binary.Read error", NFSPROC3_REMOVE},
			{"RMDIR binary.Read error", NFSPROC3_RMDIR},
			{"RENAME binary.Read error", NFSPROC3_RENAME},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				call := &RPCCall{
					Header: RPCMsgHeader{
						Version:   NFS_V3,
						Procedure: tc.procedure,
					},
				}

				reply := &RPCReply{}
				authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
				result, err := handler.handleNFSCall(call, badReader, reply, authCtx)
				if err != nil {
					t.Fatalf("handleNFSCall should not return error for bad reader: %v", err)
				}
				
				// Should get GARBAGE_ARGS in the reply
				if data, ok := result.Data.([]byte); ok {
					var status uint32
					buf := bytes.NewBuffer(data)
					binary.Read(buf, binary.BigEndian, &status)
					if status != GARBAGE_ARGS {
						t.Errorf("Expected GARBAGE_ARGS, got %d", status)
					}
				} else {
					t.Errorf("Expected []byte data in reply, got %T", result.Data)
				}
			})
		}
	})

	t.Run("invalid file handles", func(t *testing.T) {
		invalidHandle := uint64(999999) // Non-existent handle
		
		testCases := []struct {
			name      string
			procedure uint32
			setupBuf  func() *bytes.Buffer
		}{
			{
				"GETATTR invalid handle", 
				NFSPROC3_GETATTR,
				func() *bytes.Buffer {
					var buf bytes.Buffer
					binary.Write(&buf, binary.BigEndian, invalidHandle)
					return &buf
				},
			},
			{
				"SETATTR invalid handle", 
				NFSPROC3_SETATTR,
				func() *bytes.Buffer {
					var buf bytes.Buffer
					binary.Write(&buf, binary.BigEndian, invalidHandle)
					// Add setmode flag
					binary.Write(&buf, binary.BigEndian, uint32(1))
					binary.Write(&buf, binary.BigEndian, uint32(0644))
					// Add setuid flag
					binary.Write(&buf, binary.BigEndian, uint32(0))
					// Add setgid flag
					binary.Write(&buf, binary.BigEndian, uint32(0))
					return &buf
				},
			},
			{
				"READ invalid handle", 
				NFSPROC3_READ,
				func() *bytes.Buffer {
					var buf bytes.Buffer
					binary.Write(&buf, binary.BigEndian, invalidHandle)
					binary.Write(&buf, binary.BigEndian, uint64(0))  // offset
					binary.Write(&buf, binary.BigEndian, uint32(10)) // count
					return &buf
				},
			},
			{
				"WRITE invalid handle", 
				NFSPROC3_WRITE,
				func() *bytes.Buffer {
					var buf bytes.Buffer
					binary.Write(&buf, binary.BigEndian, invalidHandle)
					binary.Write(&buf, binary.BigEndian, uint64(0))   // offset
					binary.Write(&buf, binary.BigEndian, uint32(5))   // count
					binary.Write(&buf, binary.BigEndian, uint32(1))   // stable
					binary.Write(&buf, binary.BigEndian, uint32(5))   // data length
					buf.Write([]byte("hello"))                        // data
					return &buf
				},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				call := &RPCCall{
					Header: RPCMsgHeader{
						Version:   NFS_V3,
						Procedure: tc.procedure,
					},
				}

				buf := tc.setupBuf()
				reply := &RPCReply{}
				authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
				result, err := handler.handleNFSCall(call, buf, reply, authCtx)
				if err != nil {
					t.Fatalf("handleNFSCall should not return error: %v", err)
				}
				
				// Should get NFSERR_NOENT in the reply
				if data, ok := result.Data.([]byte); ok {
					var status uint32
					resBuf := bytes.NewBuffer(data)
					binary.Read(resBuf, binary.BigEndian, &status)
					if status != NFSERR_NOENT {
						t.Errorf("Expected NFSERR_NOENT, got %d", status)
					}
				} else {
					t.Errorf("Expected []byte data in reply, got %T", result.Data)
				}
			})
		}
	})

	t.Run("error paths in handlers", func(t *testing.T) {
		// Test LOOKUP with non-directory handle
		t.Run("LOOKUP with file handle", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_LOOKUP,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, fileHandle) // Use file handle instead of dir handle
			xdrEncodeString(&buf, "some_name")

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, &buf, reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed: %v", err)
			}

			// Should get NFSERR_NOTDIR in the reply
			if data, ok := result.Data.([]byte); ok {
				var status uint32
				resBuf := bytes.NewBuffer(data)
				binary.Read(resBuf, binary.BigEndian, &status)
				if status != NFSERR_NOTDIR {
					t.Errorf("Expected NFSERR_NOTDIR, got %d", status)
				}
			} else {
				t.Errorf("Expected []byte data in reply, got %T", result.Data)
			}
		})

		// Test WRITE with read-only mode
		t.Run("WRITE with read-only mode", func(t *testing.T) {
			// Create a new AbsfsNFS with read-only option
			readOnlyFS, _ := New(server.handler.fs, ExportOptions{ReadOnly: true})
			
			// Create a new server and set the read-only handler
			readOnlyServer, _ := NewServer(ServerOptions{})
			readOnlyServer.SetHandler(readOnlyFS)
			readOnlyHandler := &NFSProcedureHandler{server: readOnlyServer}

			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_WRITE,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, fileHandle)
			binary.Write(&buf, binary.BigEndian, uint64(0))    // offset
			binary.Write(&buf, binary.BigEndian, uint32(5))    // count
			binary.Write(&buf, binary.BigEndian, uint32(1))    // stable
			binary.Write(&buf, binary.BigEndian, uint32(5))    // data length
			buf.Write([]byte("hello"))                         // data

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := readOnlyHandler.handleNFSCall(call, &buf, reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed: %v", err)
			}

			// Should get ACCESS_DENIED in the reply
			if data, ok := result.Data.([]byte); ok {
				var status uint32
				resBuf := bytes.NewBuffer(data)
				binary.Read(resBuf, binary.BigEndian, &status)
				if status != ACCESS_DENIED {
					t.Errorf("Expected ACCESS_DENIED, got %d", status)
				}
			} else {
				t.Errorf("Expected []byte data in reply, got %T", result.Data)
			}
		})
		
		// Test SETATTR with invalid mode
		t.Run("SETATTR with invalid mode", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_SETATTR,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, fileHandle)
			// Set mode flag
			binary.Write(&buf, binary.BigEndian, uint32(1))
			// Set invalid mode (S_IFMT bit set)
			binary.Write(&buf, binary.BigEndian, uint32(0x8000))
			// Don't set uid
			binary.Write(&buf, binary.BigEndian, uint32(0))
			// Don't set gid
			binary.Write(&buf, binary.BigEndian, uint32(0))

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, &buf, reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed: %v", err)
			}

			// Should get NFSERR_INVAL in the reply
			if data, ok := result.Data.([]byte); ok {
				var status uint32
				resBuf := bytes.NewBuffer(data)
				binary.Read(resBuf, binary.BigEndian, &status)
				if status != NFSERR_INVAL {
					t.Errorf("Expected NFSERR_INVAL, got %d", status)
				}
			} else {
				t.Errorf("Expected []byte data in reply, got %T", result.Data)
			}
		})
		
		// Test invalid program version
		t.Run("Invalid version", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   2, // Use version 2 instead of NFS_V3
					Procedure: NFSPROC3_NULL,
				},
			}

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, &bytes.Buffer{}, reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed: %v", err)
			}

			if result.Status != PROG_MISMATCH {
				t.Errorf("Expected PROG_MISMATCH status, got %d", result.Status)
			}
		})
		
		// Test READ with count mismatch
		t.Run("WRITE count mismatch", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_WRITE,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, fileHandle)
			binary.Write(&buf, binary.BigEndian, uint64(0))    // offset
			binary.Write(&buf, binary.BigEndian, uint32(10))   // count - intentionally different from data length
			binary.Write(&buf, binary.BigEndian, uint32(1))    // stable
			binary.Write(&buf, binary.BigEndian, uint32(5))    // data length
			buf.Write([]byte("hello"))                         // data

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, &buf, reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed: %v", err)
			}

			// Should get GARBAGE_ARGS in the reply
			if data, ok := result.Data.([]byte); ok {
				var status uint32
				resBuf := bytes.NewBuffer(data)
				binary.Read(resBuf, binary.BigEndian, &status)
				if status != GARBAGE_ARGS {
					t.Errorf("Expected GARBAGE_ARGS, got %d", status)
				}
			} else {
				t.Errorf("Expected []byte data in reply, got %T", result.Data)
			}
		})
	})
}

// Test write errors in file attribute encoding
func TestFileAttributeEncodeErrors(t *testing.T) {
	badWriter := &badWriter{}
	attrs := &NFSAttrs{
		Mode:  0644,
		Uid:   1000,
		Gid:   1000,
		Size:  1024,
		// Mtime: time.Now()
		// Atime: time.Now()
	}
	
	err := encodeFileAttributes(badWriter, attrs)
	if err == nil {
		t.Error("Expected error when writing to bad writer")
	}
}

// Helper types for testing error cases
type badReader struct{}

func (r *badReader) Read(p []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

type badWriter struct{}

func (w *badWriter) Write(p []byte) (n int, err error) {
	return 0, io.ErrShortWrite
}