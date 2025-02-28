---
layout: default
title: Simple NFS Server Example
---

# Simple NFS Server Example

This example demonstrates how to create a basic NFS server exporting an in-memory filesystem. It's a complete, working example that you can run to see ABSNFS in action.

## Complete Example

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
	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Create some test content
	createTestContent(fs)

	// Create NFS server with default options
	server, err := absnfs.New(fs, absnfs.ExportOptions{})
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Export the filesystem
	mountPath := "/export/memfs"
	port := 2049

	fmt.Printf("Starting NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	// Display mount commands for different platforms
	fmt.Println("\nNFS server is running!")
	fmt.Println("\nTo mount on Linux:")
	fmt.Printf("  sudo mkdir -p /mnt/nfs\n")
	fmt.Printf("  sudo mount -t nfs localhost:%s /mnt/nfs\n", mountPath)
	
	fmt.Println("\nTo mount on macOS:")
	fmt.Printf("  sudo mkdir -p /mnt/nfs\n")
	fmt.Printf("  sudo mount -t nfs -o resvport localhost:%s /mnt/nfs\n", mountPath)
	
	fmt.Println("\nTo mount on Windows:")
	fmt.Printf("  mount -o anon \\\\localhost%s Z:\n", mountPath)
	
	fmt.Println("\nPress Ctrl+C to stop the server")

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\nShutting down NFS server...")
	
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	
	fmt.Println("NFS server stopped")
}

// Helper function to create test content
func createTestContent(fs absfs.FileSystem) {
	// Create directories
	dirs := []string{
		"/docs",
		"/images",
		"/data",
	}
	
	for _, dir := range dirs {
		err := fs.Mkdir(dir, 0755)
		if err != nil {
			log.Printf("Warning: couldn't create directory %s: %v", dir, err)
		}
	}
	
	// Create README.txt
	readme, err := fs.Create("/README.txt")
	if err != nil {
		log.Printf("Warning: couldn't create README: %v", err)
		return
	}
	
	readmeContent := `Welcome to the ABSNFS Example Server

This is a simple example of an NFS server created with ABSNFS.
The filesystem is entirely in-memory and will be reset when the server restarts.

Feel free to explore and modify files as needed.

Key directories:
- /docs - Documentation files
- /images - Sample images
- /data - Data files

Created: ` + time.Now().Format(time.RFC1123)
	
	_, err = readme.Write([]byte(readmeContent))
	if err != nil {
		log.Printf("Warning: couldn't write README content: %v", err)
	}
	readme.Close()
	
	// Create a sample document
	docFile, err := fs.Create("/docs/sample.txt")
	if err != nil {
		log.Printf("Warning: couldn't create sample document: %v", err)
		return
	}
	
	docContent := `This is a sample document.
It demonstrates that files can be created and read through the NFS interface.
Try creating your own files or modifying this one!`
	
	_, err = docFile.Write([]byte(docContent))
	if err != nil {
		log.Printf("Warning: couldn't write sample document content: %v", err)
	}
	docFile.Close()
}
```

## Key Components

Let's break down the key components of this example:

### Creating the Filesystem

```go
// Create an in-memory filesystem
fs, err := memfs.NewFS()
if err != nil {
    log.Fatalf("Failed to create filesystem: %v", err)
}

// Create some test content
createTestContent(fs)
```

This creates an in-memory filesystem using `memfs` from the ABSFS ecosystem. The `createTestContent` function adds some sample directories and files to make the example more interesting.

### Creating the NFS Server

```go
// Create NFS server with default options
server, err := absnfs.New(fs, absnfs.ExportOptions{})
if err != nil {
    log.Fatalf("Failed to create NFS server: %v", err)
}
```

This creates an NFS server that will export the in-memory filesystem. We're using default export options here, which provides reasonable defaults for most scenarios.

### Exporting the Filesystem

```go
// Export the filesystem
mountPath := "/export/memfs"
port := 2049

fmt.Printf("Starting NFS server on port %d...\n", port)
if err := server.Export(mountPath, port); err != nil {
    log.Fatalf("Failed to export filesystem: %v", err)
}
```

This makes the filesystem available to NFS clients. The `mountPath` is the path that clients will use when mounting the share, and `port` is the network port the server will listen on.

### Graceful Shutdown

```go
// Set up graceful shutdown
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

// Wait for shutdown signal
<-sigChan
fmt.Println("\nShutting down NFS server...")

if err := server.Unexport(); err != nil {
    log.Printf("Error during shutdown: %v", err)
}
```

This sets up signal handling to catch Ctrl+C and properly shut down the NFS server when the program is terminated.

## Running the Example

To run this example:

1. Save the code to a file named `main.go`
2. Initialize a Go module if needed: `go mod init example`
3. Install dependencies: `go get github.com/absfs/absnfs github.com/absfs/memfs`
4. Build the program: `go build`
5. Run the program with appropriate privileges: `sudo ./example`

Note that you typically need root privileges to bind to port 2049, which is the default NFS port.

## Testing the NFS Share

Once the server is running, you can mount the NFS share from another terminal or machine using the commands shown in the program output.

After mounting, you can interact with the filesystem like any other mounted filesystem:

```bash
# List the contents
ls -la /mnt/nfs

# Read the README
cat /mnt/nfs/README.txt

# Create a new file
echo "This is a test file created by the client" > /mnt/nfs/test.txt

# Verify the file was created
cat /mnt/nfs/test.txt
```

## Next Steps

This example provides a simple starting point. You might want to explore:

1. Customizing export options for security or performance
2. Using different types of filesystems
3. Implementing more complex filesystem structures
4. Adding authentication or access controls

For more advanced examples, see:
- [Read-Only Export](./read-only-export.md)
- [Custom Export Options](./custom-export-options.md)
- [Multi-Export Server](./multi-export-server.md)