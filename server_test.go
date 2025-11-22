package absnfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// testTimeout is the maximum duration for any single test
const testTimeout = 5 * time.Second
const testConnTimeout = 100 * time.Millisecond
const testBindDelay = 50 * time.Millisecond
const testReadTimeout = 6 * time.Second
const testStopTimeout = 2 * time.Second

// maxConnections limits the number of concurrent connections in tests
const maxConnections = 3

// setupTest prepares a test environment and returns a cleanup function
func setupTest(t *testing.T) (context.Context, func()) {
	// Capture and limit logging output
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)

	// Set up context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)

	// Return context and cleanup function
	return ctx, func() {
		cancel()
		log.SetOutput(io.Discard) // Discard remaining logs
		if t.Failed() && logBuf.Len() > 0 {
			// Only show logs if test failed and there are logs
			t.Logf("Test logs:\n%s", logBuf.String())
		}
	}
}

// startServer starts a server with proper error handling and cleanup
func startServer(t *testing.T, ctx context.Context, nfs *AbsfsNFS) (*Server, func()) {
	server, err := NewServer(ServerOptions{
		Name:     "test",
		Port:     0, // Use random port
		Hostname: "localhost",
		Debug:    false, // Disable debug logging during tests
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	server.SetHandler(nfs)

	// Start server with timeout
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Listen()
	}()

	select {
	case <-ctx.Done():
		server.Stop()
		t.Fatal("Server start timed out")
	case err := <-errChan:
		if err != nil {
			server.Stop()
			t.Fatalf("Failed to start server: %v", err)
		}
	}

	// Return cleanup function
	return server, func() {
		if err := server.Stop(); err != nil {
			t.Logf("Failed to stop server: %v", err)
		}
	}
}

func TestServerStartStop(t *testing.T) {
	ctx, cleanup := setupTest(t)
	defer cleanup()

	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{
		ReadOnly: false,
		Secure:   true,
	})
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	server, cleanup := startServer(t, ctx, nfs)
	defer cleanup()

	// Test server stop
	stopChan := make(chan error, 1)
	go func() {
		stopChan <- server.Stop()
	}()

	select {
	case <-ctx.Done():
		t.Fatal("Server stop timed out")
	case err := <-stopChan:
		if err != nil {
			t.Fatalf("Failed to stop server: %v", err)
		}
	}
}

func TestServerHandleConnection(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Test with debug logging enabled
	t.Run("debug logging", func(t *testing.T) {
		server, err := NewServer(ServerOptions{Debug: true})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		client, srv := net.Pipe()
		defer client.Close()
		defer srv.Close()

		done := make(chan struct{})
		go func() {
			srv.Write([]byte("invalid"))
			close(done)
		}()

		procHandler := &NFSProcedureHandler{server: server}
		server.handleConnection(client, procHandler)
		<-done
	})

	// Test standard server
	server, err := NewServer(ServerOptions{})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	server.SetHandler(nfs)

	// Test invalid message
	t.Run("invalid message", func(t *testing.T) {
		client, srv := net.Pipe()
		defer client.Close()
		defer srv.Close()

		done := make(chan struct{})
		go func() {
			srv.Write([]byte("invalid"))
			close(done)
		}()

		procHandler := &NFSProcedureHandler{server: server}
		server.handleConnection(client, procHandler)
		<-done
	})

	// Test write timeout
	t.Run("write timeout", func(t *testing.T) {
		client, srv := net.Pipe()
		defer client.Close()
		defer srv.Close()

		done := make(chan struct{})
		go func() {
			// Write valid RPC call header
			header := RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_NULL,
			}
			call := &RPCCall{
				Header: header,
				Credential: RPCCredential{
					Flavor: 0, // AUTH_NONE
					Body:   []byte{},
				},
				Verifier: RPCVerifier{
					Flavor: 0, // AUTH_NONE
					Body:   []byte{},
				},
			}
			if err := xdrEncodeUint32(srv, call.Header.Xid); err != nil {
				t.Fatalf("Failed to encode XID: %v", err)
			}
			if err := xdrEncodeUint32(srv, call.Header.MsgType); err != nil {
				t.Fatalf("Failed to encode message type: %v", err)
			}
			if err := xdrEncodeUint32(srv, call.Header.RPCVersion); err != nil {
				t.Fatalf("Failed to encode RPC version: %v", err)
			}
			if err := xdrEncodeUint32(srv, call.Header.Program); err != nil {
				t.Fatalf("Failed to encode program: %v", err)
			}
			if err := xdrEncodeUint32(srv, call.Header.Version); err != nil {
				t.Fatalf("Failed to encode version: %v", err)
			}
			if err := xdrEncodeUint32(srv, call.Header.Procedure); err != nil {
				t.Fatalf("Failed to encode procedure: %v", err)
			}
			if err := xdrEncodeUint32(srv, call.Credential.Flavor); err != nil {
				t.Fatalf("Failed to encode credential flavor: %v", err)
			}
			if err := xdrEncodeUint32(srv, 0); err != nil { // credential body length
				t.Fatalf("Failed to encode credential length: %v", err)
			}
			if err := xdrEncodeUint32(srv, call.Verifier.Flavor); err != nil {
				t.Fatalf("Failed to encode verifier flavor: %v", err)
			}
			if err := xdrEncodeUint32(srv, 0); err != nil { // verifier body length
				t.Fatalf("Failed to encode verifier length: %v", err)
			}
			// Block on read to trigger write timeout
			buf := make([]byte, 1024)
			srv.Read(buf)
			close(done)
		}()

		procHandler := &NFSProcedureHandler{server: server}
		server.handleConnection(client, procHandler)
		<-done
	})

	// Test read timeout
	t.Run("read timeout", func(t *testing.T) {
		client, srv := net.Pipe()
		defer client.Close()
		defer srv.Close()

		done := make(chan struct{})
		go func() {
			// Do nothing, let read timeout
			time.Sleep(testReadTimeout)
			close(done)
		}()

		procHandler := &NFSProcedureHandler{server: server}
		server.handleConnection(client, procHandler)
		<-done
	})

	// Test concurrent connections
	t.Run("concurrent connections", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				client, srv := net.Pipe()
				defer client.Close()
				defer srv.Close()

				done := make(chan struct{})
				go func() {
					// Write valid RPC call header
					header := RPCMsgHeader{
						Xid:        1,
						MsgType:    RPC_CALL,
						RPCVersion: 2,
						Program:    NFS_PROGRAM,
						Version:    NFS_V3,
						Procedure:  NFSPROC3_NULL,
					}
					call := &RPCCall{
						Header: header,
						Credential: RPCCredential{
							Flavor: 0, // AUTH_NONE
							Body:   []byte{},
						},
						Verifier: RPCVerifier{
							Flavor: 0, // AUTH_NONE
							Body:   []byte{},
						},
					}
					if err := xdrEncodeUint32(srv, call.Header.Xid); err != nil {
						t.Fatalf("Failed to encode XID: %v", err)
					}
					if err := xdrEncodeUint32(srv, call.Header.MsgType); err != nil {
						t.Fatalf("Failed to encode message type: %v", err)
					}
					if err := xdrEncodeUint32(srv, call.Header.RPCVersion); err != nil {
						t.Fatalf("Failed to encode RPC version: %v", err)
					}
					if err := xdrEncodeUint32(srv, call.Header.Program); err != nil {
						t.Fatalf("Failed to encode program: %v", err)
					}
					if err := xdrEncodeUint32(srv, call.Header.Version); err != nil {
						t.Fatalf("Failed to encode version: %v", err)
					}
					if err := xdrEncodeUint32(srv, call.Header.Procedure); err != nil {
						t.Fatalf("Failed to encode procedure: %v", err)
					}
					if err := xdrEncodeUint32(srv, call.Credential.Flavor); err != nil {
						t.Fatalf("Failed to encode credential flavor: %v", err)
					}
					if err := xdrEncodeUint32(srv, 0); err != nil { // credential body length
						t.Fatalf("Failed to encode credential length: %v", err)
					}
					if err := xdrEncodeUint32(srv, call.Verifier.Flavor); err != nil {
						t.Fatalf("Failed to encode verifier flavor: %v", err)
					}
					if err := xdrEncodeUint32(srv, 0); err != nil { // verifier body length
						t.Fatalf("Failed to encode verifier length: %v", err)
					}
					close(done)
				}()

				procHandler := &NFSProcedureHandler{server: server}
				server.handleConnection(client, procHandler)
				<-done
			}()
		}
		wg.Wait()
	})

	// Test stop timeout
	t.Run("stop timeout", func(t *testing.T) {
		server, err := NewServer(ServerOptions{})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		// Start a long-running connection
		client, srv := net.Pipe()
		defer client.Close()
		defer srv.Close()

		server.wg.Add(1)
		go func() {
			defer server.wg.Done()
			// Sleep longer than stop timeout
			time.Sleep(testReadTimeout)
		}()

		// Stop should timeout
		if err := server.Stop(); err == nil {
			t.Error("Expected timeout error on stop")
		}
	})

	// Test accept error handling
	t.Run("accept errors", func(t *testing.T) {
		server, err := NewServer(ServerOptions{Debug: true})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		// Create a listener that always errors
		errListener := &errorListener{err: fmt.Errorf("test error")}
		server.listener = errListener

		done := make(chan struct{})
		go func() {
			procHandler := &NFSProcedureHandler{server: server}
			server.acceptLoop(procHandler)
			close(done)
		}()

		// Let it run for a bit to accumulate errors
		time.Sleep(testBindDelay * 10)
		server.cancel()
		<-done

		if server.acceptErrs < 3 {
			t.Error("Expected accept errors to be counted")
		}
	})
}

// errorListener implements net.Listener and always returns an error on Accept
type errorListener struct {
	err error
}

func (l *errorListener) Accept() (net.Conn, error) {
	return nil, l.err
}

func (l *errorListener) Close() error {
	return nil
}

func (l *errorListener) Addr() net.Addr {
	return nil
}

func TestServerPortBinding(t *testing.T) {
	_, cleanup := setupTest(t)
	defer cleanup()

	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Test binding to specific port
	t.Run("specific port", func(t *testing.T) {
		// Use a random high port to avoid conflicts
		listener, err := net.Listen("tcp", "localhost:0")
		if err != nil {
			t.Fatalf("Failed to get random port: %v", err)
		}
		tcpAddr, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			t.Fatalf("Failed to get TCP address from listener")
		}
		port := tcpAddr.Port
		listener.Close()

		// First server should bind successfully
		server1, err := NewServer(ServerOptions{
			Port:     port,
			Hostname: "localhost",
		})
		if err != nil {
			t.Fatalf("Failed to create first server: %v", err)
		}
		server1.SetHandler(nfs)

		if err := server1.Listen(); err != nil {
			t.Fatalf("Failed to start first server: %v", err)
		}
		// Give the server time to fully bind to the port
		time.Sleep(testBindDelay)

		// Second server should fail to bind to same port
		server2, err := NewServer(ServerOptions{
			Port:     port,
			Hostname: "localhost",
		})
		if err != nil {
			t.Fatalf("Failed to create second server: %v", err)
		}
		server2.SetHandler(nfs)

		err = server2.Listen()
		server1.Stop() // Stop first server after attempting to bind second server
		if err == nil {
			server2.Stop()
			t.Error("Expected error binding to in-use port")
		} else if !strings.Contains(err.Error(), "address already in use") {
			t.Errorf("Expected 'address already in use' error, got: %v", err)
		}
	})

	// Test automatic port selection
	t.Run("automatic port", func(t *testing.T) {
		server, err := NewServer(ServerOptions{
			Port:     0, // Let system choose port
			Hostname: "localhost",
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		if err := server.Listen(); err != nil {
			t.Fatalf("Failed to start server: %v", err)
		}
		defer server.Stop()

		// Verify port was assigned
		if server.options.Port == 0 {
			t.Error("Port was not assigned")
		}

		// Try connecting to assigned port
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", server.options.Port), testConnTimeout)
		if err != nil {
			t.Errorf("Failed to connect to assigned port: %v", err)
		}
		if conn != nil {
			conn.Close()
		}
	})

	// Test network error handling
	t.Run("network errors", func(t *testing.T) {
		server, err := NewServer(ServerOptions{
			Port:     0,
			Hostname: "localhost",
			Debug:    true,
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		if err := server.Listen(); err != nil {
			t.Fatalf("Failed to start server: %v", err)
		}
		defer server.Stop()

		// Test connection reset
		conn, err := net.Dial("tcp", server.listener.Addr().String())
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}

		// Force connection reset
		tcpConn, ok := conn.(*net.TCPConn)
		if !ok {
			t.Fatalf("Failed to get TCP connection")
		}
		tcpConn.SetLinger(0)
		tcpConn.Close()

		// Let server process the reset
		time.Sleep(testBindDelay)

		// Test connection timeout
		conn, err = net.Dial("tcp", server.listener.Addr().String())
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		// Write partial data and stop
		conn.Write([]byte{0x00})
		time.Sleep(testReadTimeout) // Longer than read timeout
	})
}

func TestServerErrorHandling(t *testing.T) {
	ctx, cleanup := setupTest(t)
	defer cleanup()

	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{
		ReadOnly: false,
		Secure:   true,
	})
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Test starting server without handler
	server, err := NewServer(ServerOptions{
		Name:     "test",
		Port:     0,
		Hostname: "localhost",
		Debug:    false, // Disable debug logging during tests
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	if err := server.Listen(); err == nil {
		server.Stop()
		t.Error("Expected error when starting server without handler")
	}

	// Test invalid port - should fail on Listen() with "no handler set"
	// Test invalid port
	invalidServer, err := NewServer(ServerOptions{
		Name:     "test",
		Port:     -1, // This should fail in NewServer
		Hostname: "localhost",
		Debug:    false,
	})
	if err == nil {
		t.Error("Expected error creating server with invalid port")
		if invalidServer != nil {
			invalidServer.Stop()
		}
	} else if !strings.Contains(err.Error(), "invalid port") {
		t.Errorf("Expected 'invalid port' error, got: %v", err)
	}

	// Test with valid port but no handler
	invalidServer, err = NewServer(ServerOptions{
		Name:     "test",
		Port:     0, // Use random port
		Hostname: "localhost",
		Debug:    false,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// First error should be "no handler set"
	if err := invalidServer.Listen(); err == nil {
		invalidServer.Stop()
		t.Error("Expected error when starting server without handler")
	} else if err.Error() != "no handler set" {
		t.Errorf("Expected 'no handler set' error, got: %v", err)
	}

	// Set handler and try again - should succeed with random port
	invalidServer.SetHandler(nfs)
	if err := invalidServer.Listen(); err != nil {
		t.Errorf("Failed to listen with random port: %v", err)
	}
	invalidServer.Stop()

	t.Run("shutdown with active connections", func(t *testing.T) {
		server, serverCleanup := startServer(t, ctx, nfs)
		defer serverCleanup()

		// Create some connections
		var conns []net.Conn
		for i := 0; i < maxConnections; i++ {
			conn, err := net.DialTimeout("tcp", server.listener.Addr().String(), testConnTimeout)
			if err != nil {
				t.Fatalf("Failed to create connection %d: %v", i, err)
			}
			conns = append(conns, conn)
		}

		// Close all connections first
		for _, conn := range conns {
			conn.Close()
		}

		// Stop server after connections are closed
		stopChan := make(chan error, 1)
		go func() {
			stopChan <- server.Stop()
		}()

		// Wait for server to stop with timeout
		select {
		case err := <-stopChan:
			if err != nil {
				t.Errorf("Failed to stop server: %v", err)
			}
		case <-time.After(testStopTimeout):
			t.Error("Server stop timed out")
		}
	})
}

// TestAllowedIPsEnforcement tests that the AllowedIPs security control is properly enforced
func TestAllowedIPsEnforcement(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	t.Run("individual IP allowed", func(t *testing.T) {
		nfs, err := New(fs, ExportOptions{
			AllowedIPs: []string{"127.0.0.1"},
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		server, err := NewServer(ServerOptions{
			Port:     0,
			Hostname: "localhost",
			Debug:    true,
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		// Test that 127.0.0.1 is allowed
		if !server.isIPAllowed("127.0.0.1") {
			t.Error("Expected 127.0.0.1 to be allowed")
		}

		// Test that other IPs are blocked
		if server.isIPAllowed("192.168.1.100") {
			t.Error("Expected 192.168.1.100 to be blocked")
		}
		if server.isIPAllowed("10.0.0.1") {
			t.Error("Expected 10.0.0.1 to be blocked")
		}
	})

	t.Run("CIDR notation", func(t *testing.T) {
		nfs, err := New(fs, ExportOptions{
			AllowedIPs: []string{"192.168.1.0/24", "10.0.0.0/8"},
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		server, err := NewServer(ServerOptions{
			Port:     0,
			Hostname: "localhost",
			Debug:    true,
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		// Test IPs in the 192.168.1.0/24 subnet
		if !server.isIPAllowed("192.168.1.1") {
			t.Error("Expected 192.168.1.1 to be allowed")
		}
		if !server.isIPAllowed("192.168.1.100") {
			t.Error("Expected 192.168.1.100 to be allowed")
		}
		if !server.isIPAllowed("192.168.1.254") {
			t.Error("Expected 192.168.1.254 to be allowed")
		}

		// Test IPs in the 10.0.0.0/8 subnet
		if !server.isIPAllowed("10.0.0.1") {
			t.Error("Expected 10.0.0.1 to be allowed")
		}
		if !server.isIPAllowed("10.255.255.255") {
			t.Error("Expected 10.255.255.255 to be allowed")
		}

		// Test IPs outside allowed subnets
		if server.isIPAllowed("192.168.2.1") {
			t.Error("Expected 192.168.2.1 to be blocked")
		}
		if server.isIPAllowed("172.16.0.1") {
			t.Error("Expected 172.16.0.1 to be blocked")
		}
		if server.isIPAllowed("127.0.0.1") {
			t.Error("Expected 127.0.0.1 to be blocked")
		}
	})

	t.Run("empty AllowedIPs allows all", func(t *testing.T) {
		nfs, err := New(fs, ExportOptions{
			AllowedIPs: []string{},
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		server, err := NewServer(ServerOptions{
			Port:     0,
			Hostname: "localhost",
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		// All IPs should be allowed when AllowedIPs is empty
		if !server.isIPAllowed("127.0.0.1") {
			t.Error("Expected 127.0.0.1 to be allowed when AllowedIPs is empty")
		}
		if !server.isIPAllowed("192.168.1.1") {
			t.Error("Expected 192.168.1.1 to be allowed when AllowedIPs is empty")
		}
		if !server.isIPAllowed("10.0.0.1") {
			t.Error("Expected 10.0.0.1 to be allowed when AllowedIPs is empty")
		}
	})

	t.Run("nil AllowedIPs allows all", func(t *testing.T) {
		nfs, err := New(fs, ExportOptions{
			AllowedIPs: nil,
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		server, err := NewServer(ServerOptions{
			Port:     0,
			Hostname: "localhost",
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		// All IPs should be allowed when AllowedIPs is nil
		if !server.isIPAllowed("127.0.0.1") {
			t.Error("Expected 127.0.0.1 to be allowed when AllowedIPs is nil")
		}
		if !server.isIPAllowed("192.168.1.1") {
			t.Error("Expected 192.168.1.1 to be allowed when AllowedIPs is nil")
		}
	})

	t.Run("invalid IP address rejected", func(t *testing.T) {
		nfs, err := New(fs, ExportOptions{
			AllowedIPs: []string{"127.0.0.1"},
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		server, err := NewServer(ServerOptions{
			Port:     0,
			Hostname: "localhost",
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		// Invalid IP addresses should be rejected
		if server.isIPAllowed("invalid") {
			t.Error("Expected invalid IP to be rejected")
		}
		if server.isIPAllowed("999.999.999.999") {
			t.Error("Expected malformed IP to be rejected")
		}
	})

	t.Run("invalid CIDR notation handled", func(t *testing.T) {
		nfs, err := New(fs, ExportOptions{
			AllowedIPs: []string{"127.0.0.1", "invalid/24", "192.168.1.0/24"},
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		server, err := NewServer(ServerOptions{
			Port:     0,
			Hostname: "localhost",
			Debug:    true,
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		// Valid entries should still work
		if !server.isIPAllowed("127.0.0.1") {
			t.Error("Expected 127.0.0.1 to be allowed")
		}
		if !server.isIPAllowed("192.168.1.50") {
			t.Error("Expected 192.168.1.50 to be allowed")
		}

		// IPs not in valid entries should be blocked
		if server.isIPAllowed("10.0.0.1") {
			t.Error("Expected 10.0.0.1 to be blocked")
		}
	})

	t.Run("mixed IPs and CIDR notation", func(t *testing.T) {
		nfs, err := New(fs, ExportOptions{
			AllowedIPs: []string{"127.0.0.1", "192.168.1.0/24", "10.5.5.5"},
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		server, err := NewServer(ServerOptions{
			Port:     0,
			Hostname: "localhost",
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		server.SetHandler(nfs)

		// Individual IPs should be allowed
		if !server.isIPAllowed("127.0.0.1") {
			t.Error("Expected 127.0.0.1 to be allowed")
		}
		if !server.isIPAllowed("10.5.5.5") {
			t.Error("Expected 10.5.5.5 to be allowed")
		}

		// IPs in CIDR range should be allowed
		if !server.isIPAllowed("192.168.1.100") {
			t.Error("Expected 192.168.1.100 to be allowed")
		}

		// Other IPs should be blocked
		if server.isIPAllowed("10.5.5.6") {
			t.Error("Expected 10.5.5.6 to be blocked")
		}
		if server.isIPAllowed("192.168.2.1") {
			t.Error("Expected 192.168.2.1 to be blocked")
		}
	})

	t.Run("no handler allows all", func(t *testing.T) {
		server, err := NewServer(ServerOptions{
			Port:     0,
			Hostname: "localhost",
		})
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}

		// Without a handler, all IPs should be allowed
		if !server.isIPAllowed("127.0.0.1") {
			t.Error("Expected 127.0.0.1 to be allowed when no handler")
		}
		if !server.isIPAllowed("192.168.1.1") {
			t.Error("Expected 192.168.1.1 to be allowed when no handler")
		}
	})
}
