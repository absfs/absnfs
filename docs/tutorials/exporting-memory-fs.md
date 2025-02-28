---
layout: default
title: Exporting a Memory Filesystem
---

# Exporting a Memory Filesystem

This tutorial guides you through the process of creating and exporting an in-memory filesystem using ABSNFS. In-memory filesystems are useful for temporary storage, testing, or high-performance applications where persistence isn't required.

## Prerequisites

- Go 1.21 or later
- Basic understanding of Go programming
- Familiarity with NFS concepts

## Step 1: Set Up the Project

First, create a new directory for your project and initialize a Go module:

```bash
mkdir memfs-nfs
cd memfs-nfs
go mod init memfs-nfs
```

Install the required dependencies:

```bash
go get github.com/absfs/absnfs
go get github.com/absfs/memfs
```

## Step 2: Create the Basic Server

Create a file named `main.go` with the following content:

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Create NFS server with default options
	server, err := absnfs.New(fs, absnfs.ExportOptions{})
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Export the filesystem
	mountPath := "/export/memfs"
	port := 2049

	fmt.Printf("Starting NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	fmt.Printf("Memory filesystem exported at %s on port %d\n", mountPath, port)
	fmt.Println("Press Ctrl+C to stop the server")

	// Wait for interrupt signal
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

This creates a basic NFS server that exports an empty in-memory filesystem.

## Step 3: Add Content to the Filesystem

Let's enhance our code to create some initial content in the filesystem:

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

func main() {
	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Populate the filesystem with content
	if err := setupFilesystem(fs); err != nil {
		log.Fatalf("Failed to setup filesystem: %v", err)
	}

	// Create NFS server with default options
	server, err := absnfs.New(fs, absnfs.ExportOptions{})
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Export the filesystem
	mountPath := "/export/memfs"
	port := 2049

	fmt.Printf("Starting NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	fmt.Printf("Memory filesystem exported at %s on port %d\n", mountPath, port)
	fmt.Println("Press Ctrl+C to stop the server")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("Shutting down server...")
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	fmt.Println("Server stopped")
}

// setupFilesystem creates initial content in the filesystem
func setupFilesystem(fs absfs.FileSystem) error {
	// Create some directories
	directories := []string{
		"/docs",
		"/images",
		"/data",
		"/tmp",
	}

	for _, dir := range directories {
		if err := fs.Mkdir(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Create README file
	readme, err := fs.Create("/README.txt")
	if err != nil {
		return fmt.Errorf("failed to create README: %w", err)
	}
	defer readme.Close()

	readmeContent := `Welcome to the Memory Filesystem Example

This is an in-memory filesystem exported via NFS using ABSNFS.
Any changes you make will be lost when the server is restarted.

Directories:
- /docs - Documentation files
- /images - Image files
- /data - Data files
- /tmp - Temporary files (feel free to use this)

Created on: ` + time.Now().Format(time.RFC1123)

	if _, err := readme.Write([]byte(readmeContent)); err != nil {
		return fmt.Errorf("failed to write README content: %w", err)
	}

	// Create a sample document
	docFile, err := fs.Create("/docs/getting-started.txt")
	if err != nil {
		return fmt.Errorf("failed to create document: %w", err)
	}
	defer docFile.Close()

	docContent := `Getting Started with Memory Filesystems
=====================================

Memory filesystems are useful for:
1. Temporary data storage
2. High-performance read/write operations
3. Testing and development
4. Situations where persistence is not required

Benefits include:
- Very fast access times
- No disk I/O overhead
- Automatic cleanup when the program exits

Note that all data is lost when the server stops!`

	if _, err := docFile.Write([]byte(docContent)); err != nil {
		return fmt.Errorf("failed to write document content: %w", err)
	}

	// Create a sample data file
	dataFile, err := fs.Create("/data/sample.csv")
	if err != nil {
		return fmt.Errorf("failed to create data file: %w", err)
	}
	defer dataFile.Close()

	dataContent := `id,name,value
1,Item 1,10.50
2,Item 2,25.75
3,Item 3,15.20
4,Item 4,32.80
5,Item 5,8.95`

	if _, err := dataFile.Write([]byte(dataContent)); err != nil {
		return fmt.Errorf("failed to write data content: %w", err)
	}

	return nil
}
```

This enhanced version creates several directories and populates them with sample files.

## Step 4: Configure the NFS Server

Let's update our code to include more configuration options:

```go
// Create NFS server with custom options
options := absnfs.ExportOptions{
    // Enable read-ahead buffering for better sequential read performance
    EnableReadAhead: true,
    ReadAheadSize: 262144, // 256KB
    
    // Cache file attributes for improved performance
    AttrCacheTimeout: 10 * time.Second,
    
    // Use a reasonable transfer size
    TransferSize: 131072, // 128KB
}

server, err := absnfs.New(fs, options)
if err != nil {
    log.Fatalf("Failed to create NFS server: %v", err)
}
```

## Step 5: Build and Run the Server

Build and run your NFS server (you'll need root privileges to bind to port 2049):

```bash
go build
sudo ./memfs-nfs
```

You should see output indicating that the NFS server is running.

## Step 6: Mount the Filesystem

Now you can mount the in-memory filesystem from a client. Open another terminal and run:

### Linux

```bash
sudo mkdir -p /mnt/memfs
sudo mount -t nfs localhost:/export/memfs /mnt/memfs
```

### macOS

```bash
sudo mkdir -p /mnt/memfs
sudo mount -t nfs -o resvport localhost:/export/memfs /mnt/memfs
```

### Windows

On Windows, you can use the "Map Network Drive" feature or the command line:

```
mount -o anon \\localhost\export\memfs Z:
```

## Step 7: Interact with the Filesystem

Now you can interact with the in-memory filesystem as if it were a local directory:

```bash
# List the contents
ls -la /mnt/memfs

# Read the README
cat /mnt/memfs/README.txt

# Create a new file
echo "This is a test file created by the client" > /mnt/memfs/test.txt

# Create files in the tmp directory
cp /etc/hosts /mnt/memfs/tmp/
```

Any changes you make will be stored in memory and will be available to all clients that mount the filesystem.

## Step 8: Test Performance

You can test the performance of your in-memory filesystem using standard tools:

```bash
# Test write performance
dd if=/dev/zero of=/mnt/memfs/tmp/testfile bs=1M count=100 oflag=direct

# Test read performance
dd if=/mnt/memfs/tmp/testfile of=/dev/null bs=1M iflag=direct
```

Since this is an in-memory filesystem, you should see very high performance compared to disk-based filesystems.

## Step 9: Add Monitoring

Let's enhance our server to include basic monitoring:

```go
// Add after exporting the filesystem
go monitorServer(fs)

// ...

// monitorServer periodically prints statistics about the filesystem
func monitorServer(fs absfs.FileSystem) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Count files and directories
			fileCount, dirCount := countFilesAndDirs(fs, "/")
			
			fmt.Printf("[%s] Server status: %d files, %d directories\n", 
				time.Now().Format("15:04:05"), fileCount, dirCount)
		}
	}
}

func countFilesAndDirs(fs absfs.FileSystem, path string) (files int, dirs int) {
	// This is a simplified implementation
	// A real implementation would recursively walk the filesystem
	
	// Open the directory
	dir, err := fs.Open(path)
	if err != nil {
		return 0, 0
	}
	defer dir.Close()
	
	// Read directory entries
	entries, err := dir.Readdir(-1)
	if err != nil {
		return 0, 0
	}
	
	// Count files and directories
	for _, entry := range entries {
		if entry.IsDir() {
			dirs++
			// Recursively count in subdirectories
			subFiles, subDirs := countFilesAndDirs(fs, path+"/"+entry.Name())
			files += subFiles
			dirs += subDirs
		} else {
			files++
		}
	}
	
	return files, dirs
}
```

This adds a simple monitoring routine that prints filesystem statistics every 30 seconds.

## Step 10: Unmount and Cleanup

When you're done testing, unmount the filesystem:

### Linux/macOS

```bash
sudo umount /mnt/memfs
```

### Windows

```
umount Z:
```

Then stop the server by pressing Ctrl+C in the terminal where it's running.

## Complete Example

Here's the complete example with all features combined:

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

func main() {
	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Populate the filesystem with content
	if err := setupFilesystem(fs); err != nil {
		log.Fatalf("Failed to setup filesystem: %v", err)
	}

	// Create NFS server with custom options
	options := absnfs.ExportOptions{
		// Enable read-ahead buffering for better sequential read performance
		EnableReadAhead: true,
		ReadAheadSize: 262144, // 256KB
		
		// Cache file attributes for improved performance
		AttrCacheTimeout: 10 * time.Second,
		
		// Use a reasonable transfer size
		TransferSize: 131072, // 128KB
	}

	server, err := absnfs.New(fs, options)
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Export the filesystem
	mountPath := "/export/memfs"
	port := 2049

	fmt.Printf("Starting NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	fmt.Printf("Memory filesystem exported at %s on port %d\n", mountPath, port)
	fmt.Println("\nMount commands:")
	fmt.Println("  Linux:   sudo mount -t nfs localhost:/export/memfs /mnt/memfs")
	fmt.Println("  macOS:   sudo mount -t nfs -o resvport localhost:/export/memfs /mnt/memfs")
	fmt.Println("  Windows: mount -o anon \\\\localhost\\export\\memfs Z:")
	fmt.Println("\nPress Ctrl+C to stop the server")

	// Start monitoring
	go monitorServer(fs)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("Shutting down server...")
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	fmt.Println("Server stopped")
}

// setupFilesystem creates initial content in the filesystem
func setupFilesystem(fs absfs.FileSystem) error {
	// Create some directories
	directories := []string{
		"/docs",
		"/images",
		"/data",
		"/tmp",
	}

	for _, dir := range directories {
		if err := fs.Mkdir(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Create README file
	readme, err := fs.Create("/README.txt")
	if err != nil {
		return fmt.Errorf("failed to create README: %w", err)
	}
	defer readme.Close()

	readmeContent := `Welcome to the Memory Filesystem Example

This is an in-memory filesystem exported via NFS using ABSNFS.
Any changes you make will be lost when the server is restarted.

Directories:
- /docs - Documentation files
- /images - Image files
- /data - Data files
- /tmp - Temporary files (feel free to use this)

Created on: ` + time.Now().Format(time.RFC1123)

	if _, err := readme.Write([]byte(readmeContent)); err != nil {
		return fmt.Errorf("failed to write README content: %w", err)
	}

	// Create a sample document
	docFile, err := fs.Create("/docs/getting-started.txt")
	if err != nil {
		return fmt.Errorf("failed to create document: %w", err)
	}
	defer docFile.Close()

	docContent := `Getting Started with Memory Filesystems
=====================================

Memory filesystems are useful for:
1. Temporary data storage
2. High-performance read/write operations
3. Testing and development
4. Situations where persistence is not required

Benefits include:
- Very fast access times
- No disk I/O overhead
- Automatic cleanup when the program exits

Note that all data is lost when the server stops!`

	if _, err := docFile.Write([]byte(docContent)); err != nil {
		return fmt.Errorf("failed to write document content: %w", err)
	}

	// Create a sample data file
	dataFile, err := fs.Create("/data/sample.csv")
	if err != nil {
		return fmt.Errorf("failed to create data file: %w", err)
	}
	defer dataFile.Close()

	dataContent := `id,name,value
1,Item 1,10.50
2,Item 2,25.75
3,Item 3,15.20
4,Item 4,32.80
5,Item 5,8.95`

	if _, err := dataFile.Write([]byte(dataContent)); err != nil {
		return fmt.Errorf("failed to write data content: %w", err)
	}

	return nil
}

// monitorServer periodically prints statistics about the filesystem
func monitorServer(fs absfs.FileSystem) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Count files and directories
			fileCount, dirCount := countFilesAndDirs(fs, "/")
			
			fmt.Printf("[%s] Server status: %d files, %d directories\n", 
				time.Now().Format("15:04:05"), fileCount, dirCount)
		}
	}
}

func countFilesAndDirs(fs absfs.FileSystem, path string) (files int, dirs int) {
	// Open the directory
	dir, err := fs.Open(path)
	if err != nil {
		return 0, 0
	}
	defer dir.Close()
	
	// Read directory entries
	entries, err := dir.Readdir(-1)
	if err != nil {
		return 0, 0
	}
	
	// Count files and directories
	for _, entry := range entries {
		if entry.IsDir() {
			dirs++
			// Recursively count in subdirectories
			subFiles, subDirs := countFilesAndDirs(fs, path+"/"+entry.Name())
			files += subFiles
			dirs += subDirs
		} else {
			files++
		}
	}
	
	return files, dirs
}
```

## Use Cases for Memory Filesystems

In-memory filesystems are particularly useful for:

1. **Temporary Storage**: Data that doesn't need to persist beyond the program's lifetime
2. **Cache Storage**: High-speed cache for frequently accessed data
3. **Build Systems**: Temporary workspace for build artifacts
4. **Testing**: Clean, isolated environment for tests
5. **Data Processing**: Fast intermediate storage for data pipelines
6. **Development**: Rapid development without waiting for disk I/O

## Next Steps

Now that you've created a basic in-memory NFS server, you can:

1. Explore other export options to fine-tune performance
2. Implement persistence by periodically saving to disk
3. Add authentication to restrict access
4. Implement monitoring and metrics collection
5. Create a more sophisticated directory structure for your use case

For more details, see:
- [Configuration Guide](../guides/configuration.md)
- [Security Guide](../guides/security.md)
- [Performance Tuning](../guides/performance-tuning.md)