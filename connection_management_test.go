package absnfs

import (
	"net"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// mockConn is a mock implementation of net.Conn for testing
type mockConn struct {
	net.Conn
	closed bool
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func TestConnectionManagementDefaults(t *testing.T) {
	// Create default options
	options := ExportOptions{}

	// Create NFS server
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create AbsfsNFS: %v", err)
	}
	defer nfs.Close()

	// Verify that connection management options are set to their default values
	if nfs.options.MaxConnections != 100 {
		t.Errorf("Expected MaxConnections to default to 100, got %d", nfs.options.MaxConnections)
	}

	if nfs.options.IdleTimeout != 5*time.Minute {
		t.Errorf("Expected IdleTimeout to default to 5 minutes, got %v", nfs.options.IdleTimeout)
	}
}

func TestConnectionManagementCustomValues(t *testing.T) {
	// Create custom options
	options := ExportOptions{
		MaxConnections: 50,
		IdleTimeout:    2 * time.Minute,
	}

	// Create NFS server
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create AbsfsNFS: %v", err)
	}
	defer nfs.Close()

	// Verify that connection management options are preserved with custom values
	if nfs.options.MaxConnections != 50 {
		t.Errorf("Expected MaxConnections to be 50, got %d", nfs.options.MaxConnections)
	}

	if nfs.options.IdleTimeout != 2*time.Minute {
		t.Errorf("Expected IdleTimeout to be 2 minutes, got %v", nfs.options.IdleTimeout)
	}
}

func TestConnectionTracking(t *testing.T) {
	// Create server with a low connection limit for testing
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{
		MaxConnections: 2, // Set a very low limit for testing
	})
	if err != nil {
		t.Fatalf("Failed to create AbsfsNFS: %v", err)
	}
	defer nfs.Close()

	// Create server
	serverOpts := ServerOptions{
		Name:  "test",
		Port:  0,    // Use random port
		Debug: true, // Enable debug logging
	}
	server, err := NewServer(serverOpts)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	server.SetHandler(nfs)

	// Create mock connections
	conn1 := &mockConn{}
	conn2 := &mockConn{}
	conn3 := &mockConn{}

	// Register connections
	if !server.registerConnection(conn1) {
		t.Errorf("Expected conn1 registration to succeed")
	}

	if !server.registerConnection(conn2) {
		t.Errorf("Expected conn2 registration to succeed")
	}

	// This should fail because we're at the limit
	if server.registerConnection(conn3) {
		t.Errorf("Expected conn3 registration to fail due to connection limit")
	}

	// Unregister one connection
	server.unregisterConnection(conn1)

	// Now we should be able to register conn3
	if !server.registerConnection(conn3) {
		t.Errorf("Expected conn3 registration to succeed after unregistering conn1")
	}

	// Check conn count
	server.connMutex.Lock()
	count := server.connCount
	server.connMutex.Unlock()

	if count != 2 {
		t.Errorf("Expected connection count to be 2, got %d", count)
	}
}

func TestIdleConnectionCleanup(t *testing.T) {
	// Create server with short idle timeout
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{
		IdleTimeout: 10 * time.Millisecond, // Very short timeout for testing
	})
	if err != nil {
		t.Fatalf("Failed to create AbsfsNFS: %v", err)
	}
	defer nfs.Close()

	// Create server
	serverOpts := ServerOptions{
		Name:  "test",
		Port:  0,    // Use random port
		Debug: true, // Enable debug logging
	}
	server, err := NewServer(serverOpts)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	server.SetHandler(nfs)

	// Create a mock connection and register it
	conn := &mockConn{}
	server.registerConnection(conn)

	// Set the connection's activity time to be in the past
	server.connMutex.Lock()
	server.activeConns[conn].lastActivity = time.Now().Add(-100 * time.Millisecond)
	server.connMutex.Unlock()

	// Run idle connection cleanup
	server.cleanupIdleConnections()

	// Verify the connection was closed and removed
	if !conn.closed {
		t.Errorf("Expected idle connection to be closed")
	}

	server.connMutex.Lock()
	count := server.connCount
	server.connMutex.Unlock()

	if count != 0 {
		t.Errorf("Expected connection count to be 0 after cleanup, got %d", count)
	}
}

func TestCloseAllConnections(t *testing.T) {
	// Create server
	serverOpts := ServerOptions{
		Name:  "test",
		Port:  0,
		Debug: true,
	}
	server, err := NewServer(serverOpts)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Create mock connections
	conn1 := &mockConn{}
	conn2 := &mockConn{}
	conn3 := &mockConn{}

	// Register connections directly
	server.connMutex.Lock()
	server.activeConns[conn1] = &connectionState{lastActivity: time.Now()}
	server.activeConns[conn2] = &connectionState{lastActivity: time.Now()}
	server.activeConns[conn3] = &connectionState{lastActivity: time.Now()}
	server.connCount = 3
	server.connMutex.Unlock()

	// Close all connections
	server.closeAllConnections()

	// Verify all connections were closed
	if !conn1.closed || !conn2.closed || !conn3.closed {
		t.Errorf("Expected all connections to be closed")
	}

	// Verify connection count is 0
	server.connMutex.Lock()
	count := server.connCount
	server.connMutex.Unlock()

	if count != 0 {
		t.Errorf("Expected connection count to be 0 after closeAllConnections, got %d", count)
	}
}
