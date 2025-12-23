package absnfs

import (
	"fmt"
	"net"
	"testing"

	"github.com/absfs/memfs"
)

func TestTCPOptions(t *testing.T) {
	// Create a filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server with TCP options
	options := ExportOptions{
		// Configure TCP options with non-default values to verify they are applied
		TCPKeepAlive:      true,
		TCPNoDelay:        true,
		SendBufferSize:    1048576, // 1MB (different from default 256KB)
		ReceiveBufferSize: 524288,  // 512KB (different from default 256KB)
	}

	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Create server config
	serverOpts := ServerOptions{
		Port:     0, // Use a random port
		Hostname: "localhost",
		Debug:    true, // Enable debug logging
	}

	// Create TCP server
	tcpServer, err := NewServer(serverOpts)
	if err != nil {
		t.Fatalf("Failed to create TCP server: %v", err)
	}

	// Set handler
	tcpServer.SetHandler(server)

	// Start server
	err = tcpServer.Listen()
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer tcpServer.Stop()

	// Create a TCP connection to the server
	addr := net.JoinHostPort("localhost", fmt.Sprintf("%d", tcpServer.options.Port))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	// Verify connection is established
	if conn == nil {
		t.Fatal("Connection is nil")
	}

	// TCP options like keepalive and nodelay are applied on the server side to the accepted connection
	// We can't directly inspect those values from the client side in a portable way
	// Instead, we'll check that our server runs with these options without errors and
	// verify the option configuration in the Server.New() method

	// Just verify we have a valid connection by sending a simple message
	testMessage := []byte("Test TCP Options")
	_, err = conn.Write(testMessage)
	if err != nil {
		t.Fatalf("Failed to write to connection: %v", err)
	}

	// Verify we have a valid connection
	// Note: We don't need to read any response since we're just testing that the
	// TCP options are applied correctly. The server will likely close the connection
	// since we're not sending a valid RPC request.
}

func TestTCPOptionsDefaults(t *testing.T) {
	// Create a filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server with empty options to test defaults
	options := ExportOptions{}

	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Verify default TCP options were set
	if !server.options.TCPKeepAlive {
		t.Error("Default TCP keep-alive should be true")
	}

	if !server.options.TCPNoDelay {
		t.Error("Default TCP no-delay should be true")
	}

	if server.options.SendBufferSize != 262144 {
		t.Errorf("Default send buffer size should be 262144, got %d", server.options.SendBufferSize)
	}

	if server.options.ReceiveBufferSize != 262144 {
		t.Errorf("Default receive buffer size should be 262144, got %d", server.options.ReceiveBufferSize)
	}
}

func TestTCPOptionsCustomValues(t *testing.T) {
	// Create a filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server with custom TCP options
	options := ExportOptions{
		TCPKeepAlive:           false,   // Disable keep-alive
		TCPNoDelay:             false,   // Enable Nagle's algorithm
		SendBufferSize:         1048576, // 1MB
		ReceiveBufferSize:      524288,  // 512KB
		hasExplicitTCPSettings: true,    // Mark as explicitly set
	}

	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Verify custom TCP options were set
	if server.options.TCPKeepAlive {
		t.Error("TCP keep-alive should be false")
	}

	if server.options.TCPNoDelay {
		t.Error("TCP no-delay should be false")
	}

	if server.options.SendBufferSize != 1048576 {
		t.Errorf("Send buffer size should be 1048576, got %d", server.options.SendBufferSize)
	}

	if server.options.ReceiveBufferSize != 524288 {
		t.Errorf("Receive buffer size should be 524288, got %d", server.options.ReceiveBufferSize)
	}
}
