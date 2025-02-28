---
layout: default
title: Read-Only Export Example
---

# Read-Only Export Example

This example demonstrates how to create an NFS server that exports a filesystem in read-only mode. This is useful for sharing reference data, documentation, or any content that should not be modified by clients.

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

	// Create NFS server with read-only option
	options := absnfs.ExportOptions{
		ReadOnly: true, // This makes the filesystem read-only
	}
	
	server, err := absnfs.New(fs, options)
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Export the filesystem
	mountPath := "/export/readonly"
	port := 2049

	fmt.Printf("Starting read-only NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	// Display mount commands for different platforms
	fmt.Println("\nRead-only NFS server is running!")
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
	
	readmeContent := `Welcome to the Read-Only NFS Example

This is an example of a read-only NFS server created with ABSNFS.
You can browse the content, but you cannot modify it.

This filesystem contains:
- Documentation in /docs
- Example images in /images
- Reference data in /data

Created: ` + time.Now().Format(time.RFC1123)
	
	_, err = readme.Write([]byte(readmeContent))
	if err != nil {
		log.Printf("Warning: couldn't write README content: %v", err)
	}
	readme.Close()
	
	// Create a documentation file
	docFile, err := fs.Create("/docs/about-readonly.txt")
	if err != nil {
		log.Printf("Warning: couldn't create documentation file: %v", err)
		return
	}
	
	docContent := `About Read-Only NFS Exports

Read-only exports are useful for:
1. Sharing reference documentation
2. Distributing software or data
3. Providing access to critical files that should not be modified
4. Creating data archives

When a filesystem is exported as read-only:
- Clients cannot create new files
- Clients cannot modify existing files
- Clients cannot delete files or directories
- Clients can only read files and list directories

This provides a simple but effective way to protect data integrity.`
	
	_, err = docFile.Write([]byte(docContent))
	if err != nil {
		log.Printf("Warning: couldn't write documentation content: %v", err)
	}
	docFile.Close()
	
	// Create a data file
	dataFile, err := fs.Create("/data/sample.csv")
	if err != nil {
		log.Printf("Warning: couldn't create data file: %v", err)
		return
	}
	
	dataContent := `id,name,value
1,Item 1,10.5
2,Item 2,20.75
3,Item 3,15.25
4,Item 4,30.0
5,Item 5,25.5`
	
	_, err = dataFile.Write([]byte(dataContent))
	if err != nil {
		log.Printf("Warning: couldn't write data content: %v", err)
	}
	dataFile.Close()
}
```

## Key Components

Let's break down the key components of this example:

### Read-Only Configuration

The most important part of this example is setting the `ReadOnly` option to `true`:

```go
options := absnfs.ExportOptions{
    ReadOnly: true, // This makes the filesystem read-only
}

server, err := absnfs.New(fs, options)
```

This single option changes the behavior of the NFS server to:
- Reject all write operations
- Reject all file creation operations
- Reject all delete operations
- Reject all rename operations
- Allow all read operations

### Creating Content Before Exporting

Since the filesystem will be read-only, we need to create all content before exporting it:

```go
// Create an in-memory filesystem
fs, err := memfs.NewFS()
if err != nil {
    log.Fatalf("Failed to create filesystem: %v", err)
}

// Create some test content
createTestContent(fs)

// Then create the NFS server and export
// ...
```

The `createTestContent` function populates the filesystem with sample content including:
- A README file
- Documentation files
- Data files
- Directory structure

### Testing the Read-Only Export

Once the server is running, you can mount it on a client and verify that it's read-only:

```bash
# Mount the filesystem
sudo mount -t nfs localhost:/export/readonly /mnt/nfs

# Try to read a file (should succeed)
cat /mnt/nfs/README.txt

# Try to create a file (should fail)
touch /mnt/nfs/test.txt

# Try to modify a file (should fail)
echo "test" > /mnt/nfs/README.txt

# Try to delete a file (should fail)
rm /mnt/nfs/README.txt
```

All write operations should fail with a "Read-only file system" error.

## Variations

### Read-Only with Additional Security

To create a more secure read-only export, you can combine the read-only option with IP restrictions:

```go
options := absnfs.ExportOptions{
    ReadOnly: true,
    Secure: true,
    AllowedIPs: []string{"192.168.1.0/24", "10.0.0.5"},
}
```

This will only allow connections from the specified IP addresses or ranges.

### Temporary Read-Only Mode

You might want to temporarily switch a filesystem to read-only mode:

```go
// Start in read-write mode
options := absnfs.ExportOptions{
    ReadOnly: false,
}
server, err := absnfs.New(fs, options)

// Later, switch to read-only
newOptions := server.GetExportOptions()
newOptions.ReadOnly = true
if err := server.UpdateExportOptions(newOptions); err != nil {
    log.Printf("Failed to update options: %v", err)
}
```

### Read-Only with Attribute Caching

For better performance with read-only data, you can increase the attribute cache timeout:

```go
options := absnfs.ExportOptions{
    ReadOnly: true,
    AttrCacheTimeout: 60 * time.Second, // Cache attributes for 1 minute
}
```

## Use Cases

Read-only exports are ideal for:

1. **Documentation Servers**: Sharing documentation that shouldn't be modified
2. **Software Distribution**: Distributing software packages
3. **Reference Data**: Providing access to reference data like lookup tables
4. **Media Libraries**: Sharing media files like images, videos, or music
5. **Archival Data**: Providing access to archived data that should not change

## Best Practices

When using read-only exports:

1. **Verify Content Before Exporting**: Make sure all content is correct before exporting
2. **Consider Security**: Combine read-only with other security options for sensitive data
3. **Document for Users**: Let users know the filesystem is read-only so they understand why changes fail
4. **Monitor Access**: Even with read-only access, monitor usage patterns
5. **Regular Updates**: For content that changes, set up a process to update the content and re-export

## Next Steps

Now that you understand read-only exports, you may want to explore:

1. [Custom Export Options](./custom-export-options.md): More detailed configuration options
2. [OS Filesystem Export](./os-filesystem-export.md): Exporting a directory from your local filesystem
3. [Secure Server Configuration](./secure-server.md): Additional security configurations