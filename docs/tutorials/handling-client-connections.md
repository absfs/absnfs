---
layout: default
title: Handling Client Connections
---

# Handling Client Connections

This tutorial explores how to effectively manage client connections to your ABSNFS server. You'll learn how to configure connection settings, handle multiple clients, implement access control, and monitor connection activity.

## Prerequisites

- Go 1.21 or later
- Basic understanding of ABSNFS (see [First NFS Server](./first-nfs-server.md) tutorial)
- Familiarity with network programming concepts

## Step 1: Understanding NFS Client Connections

Before diving into implementation, it's important to understand how NFS clients connect to servers:

1. **Portmap/rpcbind Discovery**: Clients first contact the portmap service to find NFS ports
2. **MOUNT Protocol**: Clients use the MOUNT protocol to get the initial file handle
3. **NFS Protocol**: Clients use the NFS protocol for file operations
4. **Connection Types**: NFSv3 can use both TCP and UDP
5. **Connection Lifecycle**: Connections can be persistent (TCP) or stateless (UDP)

ABSNFS handles these details internally, but understanding them helps configure the server appropriately.

## Step 2: Basic Connection Configuration

Let's start with a simple server that includes basic connection settings:

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	// Create a filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}
	
	// Populate with some content
	populateFilesystem(fs)
	
	// Configure export options with connection settings
	options := absnfs.ExportOptions{
		// Connection limits
		MaxConnections: 100,           // Maximum simultaneous connections

		// Timeouts
		IdleTimeout: 5 * time.Minute,  // How long to keep idle connections

		// Security settings
		Secure: true,                  // Enable security features
		AllowedIPs: []string{},        // Empty means allow all (for now)
	}
	
	// Create the NFS server
	server, err := absnfs.New(fs, options)
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}
	
	// Export the filesystem
	mountPath := "/export/test"
	port := 2049
	
	fmt.Printf("Starting NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}
	
	fmt.Printf("NFS server running at %s on port %d\n", mountPath, port)
	fmt.Println("Press Ctrl+C to stop the server")
	
	// Set up signal handling for clean shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	
	fmt.Println("Shutting down server...")
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	
	fmt.Println("Server stopped")
}

func populateFilesystem(fs absfs.FileSystem) {
	// Create some test content
	// (Implementation details omitted for brevity)
}
```

This simple server includes basic connection settings:
- Maximum connections limit
- Idle timeout for connections
- Security enabling

## Step 3: Implementing Connection Logging

Let's enhance our server to log connection activity:

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

// Custom logger that captures connection events
type connectionLogger struct {
	connectCount    int
	disconnectCount int
	activeCount     int
}

func (l *connectionLogger) OnConnect(addr string) {
	l.connectCount++
	l.activeCount++
	log.Printf("Client connected from %s (active: %d, total: %d)", 
		addr, l.activeCount, l.connectCount)
}

func (l *connectionLogger) OnDisconnect(addr string) {
	l.disconnectCount++
	l.activeCount--
	log.Printf("Client disconnected from %s (active: %d, closed: %d)", 
		addr, l.activeCount, l.disconnectCount)
}

func (l *connectionLogger) Log(level, msg string) {
	log.Printf("[%s] %s", level, msg)
}

func main() {
	// Create a filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}
	
	// Populate with some content
	populateFilesystem(fs)
	
	// Create connection logger
	connLogger := &connectionLogger{}
	
	// Configure export options
	options := absnfs.ExportOptions{
		MaxConnections: 100,
		IdleTimeout: 5 * time.Minute,
	}

	// Create the NFS server
	server, err := absnfs.New(fs, options)
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Note: Custom logging can be implemented through Go's standard logging
	// or by integrating with the server's internal logging mechanisms
	
	// Export the filesystem
	mountPath := "/export/test"
	port := 2049
	
	fmt.Printf("Starting NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}
	
	fmt.Printf("NFS server running at %s on port %d\n", mountPath, port)
	fmt.Println("Press Ctrl+C to stop the server")
	
	// Start a goroutine to periodically print connection stats
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		
		for range ticker.C {
			log.Printf("Connection stats: %d active, %d total, %d closed",
				connLogger.activeCount, connLogger.connectCount, connLogger.disconnectCount)
		}
	}()
	
	// Set up signal handling for clean shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	
	fmt.Println("Shutting down server...")
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	
	fmt.Println("Server stopped")
}
```

This enhanced server:
- Implements a custom logger that tracks connections
- Records connection and disconnection events
- Provides periodic connection statistics

## Step 4: Implementing IP-Based Access Control

Now let's add IP-based access control:

```go
// Configure export options with IP restrictions
options := absnfs.ExportOptions{
	MaxConnections: 100,
	IdleTimeout: 5 * time.Minute,
	Secure: true,
	
	// IP-based access control
	AllowedIPs: []string{
		"127.0.0.1",         // Local loopback
		"192.168.1.0/24",    // Local network
		"10.0.0.5",          // Specific IP
	},
}
```

With this configuration, only clients from the specified IP addresses or ranges will be able to connect. All other connection attempts will be rejected.

## Step 5: Implementing Graceful Connection Handling

Let's enhance our server to handle connections more gracefully, including:
1. Graceful shutdown with connection draining
2. Connection rate limiting
3. Handling connection errors

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
	"golang.org/x/time/rate"
)

// ConnectionManager tracks and manages client connections
type ConnectionManager struct {
	mu             sync.Mutex
	connections    map[string]time.Time  // client addr -> connect time
	totalConnected int
	totalClosed    int
	limiter        *rate.Limiter
}

// NewConnectionManager creates a new connection manager
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		connections: make(map[string]time.Time),
		// Allow burst of 10 connections, then rate limit to 2 per second
		limiter: rate.NewLimiter(2, 10),
	}
}

// OnConnect is called when a client connects
func (cm *ConnectionManager) OnConnect(addr string) {
	// Apply rate limiting
	if !cm.limiter.Allow() {
		log.Printf("Connection rate limit exceeded, rejecting %s", addr)
		return
	}
	
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	cm.connections[addr] = time.Now()
	cm.totalConnected++
	
	log.Printf("Client connected from %s (active: %d, total: %d)", 
		addr, len(cm.connections), cm.totalConnected)
}

// OnDisconnect is called when a client disconnects
func (cm *ConnectionManager) OnDisconnect(addr string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	delete(cm.connections, addr)
	cm.totalClosed++
	
	log.Printf("Client disconnected from %s (active: %d, closed: %d)", 
		addr, len(cm.connections), cm.totalClosed)
}

// Log handles general logging
func (cm *ConnectionManager) Log(level, msg string) {
	log.Printf("[%s] %s", level, msg)
}

// Stats returns current connection statistics
func (cm *ConnectionManager) Stats() (active, total, closed int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	return len(cm.connections), cm.totalConnected, cm.totalClosed
}

// WaitForDrain waits for all connections to close or timeout
func (cm *ConnectionManager) WaitForDrain(ctx context.Context, timeout time.Duration) bool {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	
	// Check every 100ms
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			// Context canceled or timed out
			return false
		case <-ticker.C:
			// Check if all connections are closed
			cm.mu.Lock()
			activeCount := len(cm.connections)
			cm.mu.Unlock()
			
			if activeCount == 0 {
				return true
			}
			
			log.Printf("Waiting for %d connections to close...", activeCount)
		}
	}
}

func main() {
	// Create a filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}
	
	// Populate with some content
	populateFilesystem(fs)
	
	// Create connection manager
	connManager := NewConnectionManager()
	
	// Configure export options
	options := absnfs.ExportOptions{
		MaxConnections: 100,
		IdleTimeout: 5 * time.Minute,

		// IP-based access control (if needed)
		Secure: true,
		AllowedIPs: []string{
			"127.0.0.1",
			"192.168.0.0/16",
		},
	}

	// Create the NFS server
	server, err := absnfs.New(fs, options)
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Note: Connection tracking is done through the connection manager
	// Custom logging can be integrated as needed
	
	// Export the filesystem
	mountPath := "/export/test"
	port := 2049
	
	fmt.Printf("Starting NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}
	
	fmt.Printf("NFS server running at %s on port %d\n", mountPath, port)
	fmt.Println("Press Ctrl+C to stop the server")
	
	// Start a goroutine to periodically print connection stats
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		
		for range ticker.C {
			active, total, closed := connManager.Stats()
			log.Printf("Connection stats: %d active, %d total, %d closed", 
				active, total, closed)
		}
	}()
	
	// Set up signal handling for clean shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	
	fmt.Println("Shutdown signal received. Waiting for connections to drain...")
	
	// Give connections time to drain gracefully
	ctx := context.Background()
	allClosed := connManager.WaitForDrain(ctx, 30*time.Second)
	
	if !allClosed {
		fmt.Println("Timeout waiting for connections to close. Forcing shutdown.")
	} else {
		fmt.Println("All connections closed gracefully.")
	}
	
	// Unexport regardless
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	
	fmt.Println("Server stopped")
}
```

This enhanced server:
- Implements a connection manager that tracks all connections
- Applies rate limiting to prevent connection flooding
- Supports graceful shutdown by waiting for connections to drain
- Provides detailed connection statistics

## Step 6: Implementing Client Interaction Monitoring

Let's enhance our server to monitor client interactions with the filesystem:

```go
// FileOperationMonitor tracks file operations
type FileOperationMonitor struct {
	mu sync.Mutex
	operationCounts map[string]int // operation -> count
	clientActivity map[string]map[string]int // client -> operation -> count
}

// NewFileOperationMonitor creates a new operation monitor
func NewFileOperationMonitor() *FileOperationMonitor {
	return &FileOperationMonitor{
		operationCounts: make(map[string]int),
		clientActivity: make(map[string]map[string]int),
	}
}

// RecordOperation records a file operation
func (m *FileOperationMonitor) RecordOperation(client, operation, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Increment overall operation count
	m.operationCounts[operation]++
	
	// Increment client-specific operation count
	if _, ok := m.clientActivity[client]; !ok {
		m.clientActivity[client] = make(map[string]int)
	}
	m.clientActivity[client][operation]++
	
	// Log the operation
	log.Printf("Client %s: %s %s", client, operation, path)
}

// GetStats returns operation statistics
func (m *FileOperationMonitor) GetStats() (map[string]int, map[string]map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Create copies to avoid race conditions
	opCounts := make(map[string]int)
	for k, v := range m.operationCounts {
		opCounts[k] = v
	}
	
	clientAct := make(map[string]map[string]int)
	for client, ops := range m.clientActivity {
		clientAct[client] = make(map[string]int)
		for op, count := range ops {
			clientAct[client][op] = count
		}
	}
	
	return opCounts, clientAct
}
```

This monitor could be integrated into the server to track and analyze client behavior.

## Step 7: Testing with Multiple Clients

To test our server with multiple clients, we can:

1. Run the server on a machine accessible on the network
2. Set up multiple client machines to connect simultaneously
3. Monitor the connections and operations

On each client, mount the NFS share:

```bash
# Linux
sudo mount -t nfs server_ip:/export/test /mnt/nfs

# macOS
sudo mount -t nfs -o resvport server_ip:/export/test /mnt/nfs

# Windows
mount -o anon \\server_ip\export\test Z:
```

Then perform various operations to test the server:

```bash
# Create files
touch /mnt/nfs/client1_file.txt
echo "Hello from client 1" > /mnt/nfs/client1_file.txt

# Read files
cat /mnt/nfs/client1_file.txt

# List directories
ls -la /mnt/nfs

# Create directories
mkdir -p /mnt/nfs/client1_dir

# Remove files
rm /mnt/nfs/client1_file.txt
```

## Step 8: Implementing Client Identification

We can enhance client identification by adding username tracking:

```go
// Connection with user information
type ClientConnection struct {
	Address  string
	User     string
	ConnectTime time.Time
	LastActivity time.Time
}

// Enhanced connection manager
type EnhancedConnectionManager struct {
	mu sync.Mutex
	connections map[string]*ClientConnection
	// ... other fields ...
}

// OnConnect with user information
func (cm *EnhancedConnectionManager) OnConnect(addr string, credentials map[string]interface{}) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	// Extract user information from credentials
	var user string
	if uid, ok := credentials["uid"].(int); ok {
		user = fmt.Sprintf("uid:%d", uid)
	} else {
		user = "unknown"
	}
	
	// Create connection record
	cm.connections[addr] = &ClientConnection{
		Address: addr,
		User: user,
		ConnectTime: time.Now(),
		LastActivity: time.Now(),
	}
	
	log.Printf("Client connected: %s as %s", addr, user)
}
```

## Complete Example

Putting it all together, here's a complete example of a server with comprehensive client connection handling:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
	"golang.org/x/time/rate"
)

// ConnectionManager tracks and manages client connections
type ConnectionManager struct {
	mu             sync.Mutex
	connections    map[string]*ClientConnection
	operationCounts map[string]int
	totalConnected int
	totalClosed    int
	limiter        *rate.Limiter
}

// ClientConnection represents a client connection with activity tracking
type ClientConnection struct {
	Address     string
	User        string
	ConnectTime time.Time
	LastActivity time.Time
	Operations  map[string]int
}

// NewConnectionManager creates a new connection manager
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		connections: make(map[string]*ClientConnection),
		operationCounts: make(map[string]int),
		limiter: rate.NewLimiter(2, 10), // 2 per second, burst of 10
	}
}

// OnConnect is called when a client connects
func (cm *ConnectionManager) OnConnect(addr string) {
	// Apply rate limiting
	if !cm.limiter.Allow() {
		log.Printf("Connection rate limit exceeded, rejecting %s", addr)
		return
	}
	
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	now := time.Now()
	cm.connections[addr] = &ClientConnection{
		Address: addr,
		User: "unknown", // Will update if auth info available
		ConnectTime: now,
		LastActivity: now,
		Operations: make(map[string]int),
	}
	cm.totalConnected++
	
	log.Printf("Client connected from %s (active: %d, total: %d)", 
		addr, len(cm.connections), cm.totalConnected)
}

// OnDisconnect is called when a client disconnects
func (cm *ConnectionManager) OnDisconnect(addr string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	if conn, ok := cm.connections[addr]; ok {
		duration := time.Since(conn.ConnectTime)
		log.Printf("Client %s disconnected after %v", addr, duration)
		delete(cm.connections, addr)
	}
	cm.totalClosed++
}

// RecordOperation records a file operation from a client
func (cm *ConnectionManager) RecordOperation(addr, operation, path string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	// Update global operation counts
	cm.operationCounts[operation]++
	
	// Update client-specific operation counts
	if conn, ok := cm.connections[addr]; ok {
		conn.LastActivity = time.Now()
		conn.Operations[operation]++
	}
	
	// We could log each operation, but that might be too verbose
	// Uncomment if detailed logging is needed
	// log.Printf("Client %s: %s %s", addr, operation, path)
}

// Log implements the Logger interface
func (cm *ConnectionManager) Log(level, msg string) {
	log.Printf("[%s] %s", level, msg)
}

// Stats returns current connection statistics
func (cm *ConnectionManager) Stats() map[string]interface{} {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	// Basic stats
	stats := map[string]interface{}{
		"active_connections": len(cm.connections),
		"total_connected": cm.totalConnected,
		"total_closed": cm.totalClosed,
		"operations": cm.operationCounts,
	}
	
	// Client details (limited to avoid too much output)
	clients := make(map[string]interface{})
	for addr, conn := range cm.connections {
		clients[addr] = map[string]interface{}{
			"connect_time": conn.ConnectTime,
			"last_activity": conn.LastActivity,
			"idle_time": time.Since(conn.LastActivity).String(),
			"total_operations": len(conn.Operations),
		}
	}
	stats["clients"] = clients
	
	return stats
}

// WaitForDrain waits for all connections to close or timeout
func (cm *ConnectionManager) WaitForDrain(ctx context.Context, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			cm.mu.Lock()
			count := len(cm.connections)
			cm.mu.Unlock()
			
			if count == 0 {
				return true
			}
			
			log.Printf("Waiting for %d connections to close...", count)
		}
	}
}

// Start the HTTP monitoring server
func (cm *ConnectionManager) StartMonitoringServer(addr string) {
	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := cm.Stats()
		jsonData, err := json.MarshalIndent(stats, "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.Write(jsonData)
	})
	
	go func() {
		log.Printf("Starting monitoring server on %s", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("Monitoring server error: %v", err)
		}
	}()
}

func main() {
	// Create a filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}
	
	// Create some example content
	createExampleContent(fs)
	
	// Create connection manager
	connManager := NewConnectionManager()
	
	// Start the monitoring HTTP server
	connManager.StartMonitoringServer(":8080")
	
	// Configure export options
	options := absnfs.ExportOptions{
		MaxConnections: 100,
		IdleTimeout: 5 * time.Minute,

		// IP-based access control
		Secure: true,
		AllowedIPs: []string{
			"127.0.0.1",
			"192.168.0.0/16",
		},

		// Performance settings
		EnableReadAhead: true,
		ReadAheadSize: 262144, // 256KB
		AttrCacheTimeout: 10 * time.Second,
	}

	// Create the NFS server
	server, err := absnfs.New(fs, options)
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Note: Connection tracking is managed by the connection manager
	
	// Export the filesystem
	mountPath := "/export/test"
	port := 2049
	
	fmt.Printf("Starting NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}
	
	fmt.Printf("NFS server running at %s on port %d\n", mountPath, port)
	fmt.Printf("Monitoring interface available at http://localhost:8080/stats\n")
	fmt.Println("Press Ctrl+C to stop the server")
	
	// Set up signal handling for clean shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	
	fmt.Println("Shutdown signal received. Waiting for connections to drain...")
	
	// Give connections time to drain gracefully
	ctx := context.Background()
	allClosed := connManager.WaitForDrain(ctx, 30*time.Second)
	
	if !allClosed {
		fmt.Println("Timeout waiting for connections to close. Forcing shutdown.")
	} else {
		fmt.Println("All connections closed gracefully.")
	}
	
	// Unexport regardless
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	
	fmt.Println("Server stopped")
}

func createExampleContent(fs absfs.FileSystem) {
	// Create directories
	dirs := []string{"/docs", "/data", "/images", "/tmp"}
	for _, dir := range dirs {
		fs.Mkdir(dir, 0755)
	}
	
	// Create a README file
	readme, _ := fs.Create("/README.txt")
	readme.Write([]byte("This is a test NFS server with connection management.\n"))
	readme.Close()
	
	// Create some example files
	for i := 1; i <= 5; i++ {
		filename := fmt.Sprintf("/data/file%d.txt", i)
		file, _ := fs.Create(filename)
		file.Write([]byte(fmt.Sprintf("This is test file %d\n", i)))
		file.Close()
	}
}
```

This comprehensive example:
1. Implements a sophisticated connection manager
2. Tracks client connections and operations
3. Provides rate limiting to prevent overload
4. Includes an HTTP monitoring interface
5. Supports graceful shutdown with connection draining
6. Provides detailed connection information

Note: Some of the logging functions shown (SetLogger, OnConnect, OnDisconnect) are conceptual examples. The actual implementation would need to integrate with the NFS server's internal mechanisms or use Go's standard logging.

## Testing the Monitoring Interface

With the above implementation, you can access the monitoring interface at http://localhost:8080/stats to see real-time connection and operation statistics.

The JSON output will look something like:

```json
{
  "active_connections": 3,
  "total_connected": 5,
  "total_closed": 2,
  "operations": {
    "GETATTR": 127,
    "LOOKUP": 45,
    "READ": 78,
    "READDIR": 14,
    "WRITE": 23
  },
  "clients": {
    "192.168.1.5:12345": {
      "connect_time": "2023-06-15T14:30:22.123456Z",
      "last_activity": "2023-06-15T14:35:12.654321Z",
      "idle_time": "2m30s",
      "total_operations": 3
    },
    "192.168.1.10:54321": {
      "connect_time": "2023-06-15T14:32:10.123456Z",
      "last_activity": "2023-06-15T14:35:40.654321Z",
      "idle_time": "2m2s",
      "total_operations": 7
    }
  }
}
```

## Performance Considerations

When handling many client connections, consider these performance optimizations:

1. **Connection Pooling**: Reuse network connections when possible
2. **Buffer Management**: Allocate and reuse buffers efficiently
3. **Concurrency Control**: Limit the number of concurrent operations
4. **Idle Timeouts**: Close inactive connections to free resources
5. **Memory Management**: Monitor memory usage and implement limits

## Security Considerations

For secure connection handling:

1. **IP Filtering**: Restrict connections to trusted networks
2. **Rate Limiting**: Prevent connection flooding attacks
3. **Monitoring**: Detect and respond to unusual connection patterns
4. **Timeouts**: Implement appropriate timeouts to prevent resource exhaustion
5. **Access Logging**: Log all connection attempts for audit purposes

## Conclusion

Effective client connection management is crucial for building a robust NFS server. By implementing proper connection tracking, monitoring, and security measures, you can ensure your server performs well under load and resists potential attacks.

In this tutorial, you've learned how to:
1. Configure basic connection settings
2. Implement connection logging and tracking
3. Add IP-based access control
4. Implement connection rate limiting
5. Support graceful shutdown with connection draining
6. Monitor client operations
7. Expose a monitoring interface

These techniques will help you build a production-quality NFS server that can handle many concurrent clients while maintaining performance and security.

## Next Steps

- [Custom Authentication](./custom-authentication.md): Learn how to implement advanced authentication
- [Multi-Export Server](./multi-export-server.md): Serve multiple filesystems from one server
- [High Performance](./high-performance.md): Optimize your server for maximum performance