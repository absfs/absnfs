---
layout: default
title: Creating a Read-Only Documentation Server
---

# Creating a Read-Only Documentation Server

This tutorial guides you through creating a read-only NFS server specifically designed for sharing documentation. This is a perfect use case for ABSNFS, as documentation typically needs to be accessible to many users but modified only by authorized personnel.

## Use Case

A read-only documentation server is useful for:

- Sharing company documentation with employees
- Distributing reference materials to teams
- Providing user manuals for products
- Creating a centralized knowledge base
- Sharing technical documentation for software projects

## Prerequisites

- Go 1.21 or later
- Basic understanding of Go programming
- Understanding of NFS concepts (see [First NFS Server](./first-nfs-server.md))
- Source documentation files to share

## Step 1: Set Up the Project

Create a new directory for your project and initialize a Go module:

```bash
mkdir docs-nfs-server
cd docs-nfs-server
go mod init docs-nfs-server
```

Install the required dependencies:

```bash
go get github.com/absfs/absnfs
go get github.com/absfs/memfs
go get github.com/absfs/osfs
```

## Step 2: Create the Basic Server Structure

Create a file named `main.go` with the following content:

```go
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/absfs"
	"github.com/absfs/osfs"
)

// Command-line flags
var (
	docsDir   = flag.String("docs", "./docs", "Path to documentation directory")
	mountPath = flag.String("mount", "/export/docs", "NFS mount path")
	port      = flag.Int("port", 2049, "NFS server port")
	allowedIPs = flag.String("allow", "", "Comma-separated list of allowed IP addresses/ranges")
)

func main() {
	// Parse command-line flags
	flag.Parse()

	// Validate docs directory
	info, err := os.Stat(*docsDir)
	if err != nil {
		log.Fatalf("Error accessing docs directory: %v", err)
	}
	if !info.IsDir() {
		log.Fatalf("Docs path is not a directory: %s", *docsDir)
	}

	// Create the documentation filesystem
	log.Printf("Using documentation from: %s", *docsDir)
	fs, err := osfs.NewFS(*docsDir)
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Configure NFS server options
	options := absnfs.ExportOptions{
		// Set read-only mode for security
		ReadOnly: true,
		
		// Enable security features
		Secure: true,
		
		// Parse allowed IPs
		AllowedIPs: parseAllowedIPs(*allowedIPs),
		
		// Performance settings optimized for documentation
		EnableReadAhead: true,
		ReadAheadSize: 262144, // 256KB
		
		// Longer attribute caching for mostly-static content
		AttrCacheTimeout: 30 * time.Second,
	}

	// Create NFS server
	server, err := absnfs.New(fs, options)
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Export the filesystem
	log.Printf("Starting NFS server on port %d...", *port)
	if err := server.Export(*mountPath, *port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	// Print mount instructions
	printMountInstructions(*mountPath, *port)

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutdown signal received...")

	// Unexport the filesystem
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	log.Println("Server stopped")
}

// Parse comma-separated list of allowed IPs
func parseAllowedIPs(ips string) []string {
	if ips == "" {
		return nil // Allow all
	}
	
	// Split by comma
	return strings.Split(ips, ",")
}

// Print mount instructions for different platforms
func printMountInstructions(mountPath string, port int) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}
	
	fmt.Println("\nDocumentation server is running!")
	fmt.Println("\nMount commands:")
	fmt.Printf("  Linux:   sudo mount -t nfs %s:%s /mnt/docs\n", hostname, mountPath)
	fmt.Printf("  macOS:   sudo mount -t nfs -o resvport %s:%s /mnt/docs\n", hostname, mountPath)
	fmt.Printf("  Windows: mount -o anon \\\\%s%s Z:\n", hostname, strings.ReplaceAll(mountPath, "/", "\\"))
	
	if port != 2049 {
		fmt.Println("\nNote: You're using a non-standard port. Use these commands instead:")
		fmt.Printf("  Linux/macOS: sudo mount -t nfs %s:%d:%s /mnt/docs\n", hostname, port, mountPath)
		fmt.Printf("  Windows:     mount -o anon \\\\%s@%d%s Z:\n", hostname, port, strings.ReplaceAll(mountPath, "/", "\\"))
	}
	
	fmt.Println("\nPress Ctrl+C to stop the server")
}
```

Make sure to add the necessary imports:

```go
import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	
	"github.com/absfs/absnfs"
	"github.com/absfs/absfs"
	"github.com/absfs/osfs"
)
```

## Step 3: Add Documentation Organization Helper

To help users navigate the documentation, let's add a function that creates an index file if one doesn't exist:

```go
// Ensure documentation has an index
func ensureDocumentationIndex(fs absfs.FileSystem) error {
	// Check if index.html exists
	_, err := fs.Stat("/index.html")
	if err == nil {
		// Index already exists
		return nil
	}
	
	// List all top-level files and directories
	rootDir, err := fs.Open("/")
	if err != nil {
		return err
	}
	defer rootDir.Close()
	
	entries, err := rootDir.Readdir(-1)
	if err != nil {
		return err
	}
	
	// Create a simple index.html
	index, err := fs.Create("/index.html")
	if err != nil {
		return err
	}
	defer index.Close()
	
	// Write HTML header
	indexContent := `<!DOCTYPE html>
<html>
<head>
    <title>Documentation Index</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; line-height: 1.6; }
        h1 { color: #333; }
        ul { list-style-type: none; padding: 0; }
        li { margin-bottom: 10px; }
        li a { text-decoration: none; color: #0066cc; }
        li a:hover { text-decoration: underline; }
        .directory { font-weight: bold; }
        .file { }
    </style>
</head>
<body>
    <h1>Documentation Index</h1>
    <p>This is an automatically generated index of available documentation.</p>
    <ul>
`
	
	// Add entries
	for _, entry := range entries {
		name := entry.Name()
		
		// Skip hidden files and the index itself
		if strings.HasPrefix(name, ".") || name == "index.html" {
			continue
		}
		
		// Create a link
		class := "file"
		if entry.IsDir() {
			class = "directory"
		}
		
		linkLine := fmt.Sprintf("        <li class=\"%s\"><a href=\"%s\">%s</a></li>\n", 
			class, name, name)
		indexContent += linkLine
	}
	
	// Close HTML
	indexContent += `    </ul>
    <hr>
    <p><em>Generated on ` + time.Now().Format("2006-01-02 15:04:05") + `</em></p>
</body>
</html>`
	
	// Write the content
	_, err = index.Write([]byte(indexContent))
	if err != nil {
		return err
	}
	
	log.Println("Created documentation index at /index.html")
	return nil
}
```

Update the `main()` function to call this helper:

```go
// Create the documentation filesystem
log.Printf("Using documentation from: %s", *docsDir)
fs, err := osfs.NewFS(*docsDir)
if err != nil {
    log.Fatalf("Failed to create filesystem: %v", err)
}

// Ensure there's an index
if err := ensureDocumentationIndex(fs); err != nil {
    log.Printf("Warning: Failed to create documentation index: %v", err)
}
```

## Step 4: Add Access Logging

Let's add access logging to track usage of the documentation:

```go
// Custom logger that tracks document access
type DocumentationLogger struct {
	accessLogs map[string]int
	mu         sync.Mutex
}

// Create a new documentation logger
func NewDocumentationLogger() *DocumentationLogger {
	return &DocumentationLogger{
		accessLogs: make(map[string]int),
	}
}

// Log a document access
func (l *DocumentationLogger) LogAccess(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	
	l.accessLogs[path]++
}

// Get access statistics
func (l *DocumentationLogger) GetStats() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	
	// Create a copy of the stats
	stats := make(map[string]int)
	for path, count := range l.accessLogs {
		stats[path] = count
	}
	
	return stats
}

// Implement logger interface for NFS server
func (l *DocumentationLogger) Log(level, msg string) {
	log.Printf("[%s] %s", level, msg)
	
	// Extract path from READ operations
	if strings.Contains(msg, "READ") {
		parts := strings.Split(msg, " ")
		for i, part := range parts {
			if part == "path:" && i < len(parts)-1 {
				l.LogAccess(parts[i+1])
				break
			}
		}
	}
}

// OnConnect is called when a client connects
func (l *DocumentationLogger) OnConnect(addr string) {
	log.Printf("Client connected: %s", addr)
}

// OnDisconnect is called when a client disconnects
func (l *DocumentationLogger) OnDisconnect(addr string) {
	log.Printf("Client disconnected: %s", addr)
}
```

Add a function to periodically print access statistics:

```go
// Print access statistics periodically
func runStatsReporter(logger *DocumentationLogger) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		stats := logger.GetStats()
		
		// Skip if no access has been logged
		if len(stats) == 0 {
			continue
		}
		
		log.Println("Documentation access statistics:")
		
		// Convert to a sortable slice
		type docStat struct {
			path  string
			count int
		}
		
		var statsList []docStat
		for path, count := range stats {
			statsList = append(statsList, docStat{path, count})
		}
		
		// Sort by access count (descending)
		sort.Slice(statsList, func(i, j int) bool {
			return statsList[i].count > statsList[j].count
		})
		
		// Print top 10 (or fewer if there aren't that many)
		limit := 10
		if len(statsList) < limit {
			limit = len(statsList)
		}
		
		for i := 0; i < limit; i++ {
			log.Printf("  %s: %d accesses", statsList[i].path, statsList[i].count)
		}
	}
}
```

Update the `main()` function to use the logger:

```go
// Create logger
logger := NewDocumentationLogger()

// Create NFS server
server, err := absnfs.New(fs, options)
if err != nil {
    log.Fatalf("Failed to create NFS server: %v", err)
}

// Set the logger
server.SetLogger(logger)

// Start stats reporter
go runStatsReporter(logger)
```

## Step 5: Add HTTP Preview Server

To make the documentation even more accessible, let's add an HTTP server that serves the same content:

```go
// Start HTTP preview server
func startHTTPServer(fs absfs.FileSystem, httpPort int) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Get the path
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		
		// Try to open the file
		file, err := fs.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		
		// Get file info
		info, err := file.Stat()
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		
		// Handle directory
		if info.IsDir() {
			// Redirect if not ending in /
			if !strings.HasSuffix(r.URL.Path, "/") {
				http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
				return
			}
			
			// Try to serve index.html in the directory
			indexPath := path
			if !strings.HasSuffix(indexPath, "/") {
				indexPath += "/"
			}
			indexPath += "index.html"
			
			indexFile, err := fs.Open(indexPath)
			if err == nil {
				defer indexFile.Close()
				// Serve the index file
				http.ServeContent(w, r, "index.html", info.ModTime(), indexFile.(io.ReadSeeker))
				return
			}
			
			// Generate directory listing
			entries, err := file.Readdir(-1)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, "<html><head><title>Directory: %s</title></head><body>", path)
			fmt.Fprintf(w, "<h1>Directory: %s</h1><ul>", path)
			
			// Add parent directory link if not root
			if path != "/" {
				fmt.Fprintf(w, "<li><a href=\"%s\">..</a></li>", filepath.Dir(path))
			}
			
			// Add entries
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				fmt.Fprintf(w, "<li><a href=\"%s%s\">%s</a></li>", r.URL.Path, name, name)
			}
			
			fmt.Fprintf(w, "</ul></body></html>")
			return
		}
		
		// Serve the file
		http.ServeContent(w, r, info.Name(), info.ModTime(), file.(io.ReadSeeker))
	})
	
	// Start HTTP server
	go func() {
		log.Printf("Starting HTTP preview server on port %d", httpPort)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", httpPort), nil); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()
}
```

Add a new command-line flag and call the HTTP server function:

```go
// Command-line flags
var (
	docsDir    = flag.String("docs", "./docs", "Path to documentation directory")
	mountPath  = flag.String("mount", "/export/docs", "NFS mount path")
	port       = flag.Int("port", 2049, "NFS server port")
	allowedIPs = flag.String("allow", "", "Comma-separated list of allowed IP addresses/ranges")
	httpPort   = flag.Int("http", 8080, "HTTP preview server port (0 to disable)")
)

// In main()
// Start HTTP preview server if enabled
if *httpPort > 0 {
    startHTTPServer(fs, *httpPort)
    fmt.Printf("\nHTTP preview available at: http://localhost:%d/\n", *httpPort)
}
```

## Step 6: Add Document Search Functionality

Let's add a simple search function to the HTTP server:

```go
// Add to HTTP handler
http.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
    query := r.URL.Query().Get("q")
    if query == "" {
        // Show search form
        w.Header().Set("Content-Type", "text/html")
        fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>Documentation Search</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; line-height: 1.6; }
        h1 { color: #333; }
        .search-form { margin: 20px 0; }
        input[type="text"] { padding: 8px; width: 300px; }
        input[type="submit"] { padding: 8px 16px; background: #0066cc; color: white; border: none; }
    </style>
</head>
<body>
    <h1>Documentation Search</h1>
    <form class="search-form" action="/search" method="GET">
        <input type="text" name="q" placeholder="Enter search term...">
        <input type="submit" value="Search">
    </form>
    <p><a href="/">Back to Index</a></p>
</body>
</html>`)
        return
    }
    
    // Perform search
    results, err := searchDocumentation(fs, query)
    if err != nil {
        http.Error(w, "Search error: "+err.Error(), http.StatusInternalServerError)
        return
    }
    
    // Show results
    w.Header().Set("Content-Type", "text/html")
    fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>Search Results: %s</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; line-height: 1.6; }
        h1 { color: #333; }
        .search-form { margin: 20px 0; }
        input[type="text"] { padding: 8px; width: 300px; }
        input[type="submit"] { padding: 8px 16px; background: #0066cc; color: white; border: none; }
        .result { margin-bottom: 20px; }
        .result h3 { margin-bottom: 5px; }
        .result p { margin-top: 5px; color: #666; }
        .highlight { background-color: #FFFFCC; }
    </style>
</head>
<body>
    <h1>Search Results: %s</h1>
    <form class="search-form" action="/search" method="GET">
        <input type="text" name="q" value="%s" placeholder="Enter search term...">
        <input type="submit" value="Search">
    </form>
    `, query, query, html.EscapeString(query))
    
    if len(results) == 0 {
        fmt.Fprintf(w, "<p>No results found for <strong>%s</strong></p>", html.EscapeString(query))
    } else {
        fmt.Fprintf(w, "<p>Found %d results for <strong>%s</strong></p>", len(results), html.EscapeString(query))
        
        for _, result := range results {
            fmt.Fprintf(w, `<div class="result">
    <h3><a href="%s">%s</a></h3>
    <p>%s</p>
</div>`, result.Path, filepath.Base(result.Path), html.EscapeString(result.Preview))
        }
    }
    
    fmt.Fprintf(w, `<p><a href="/">Back to Index</a></p>
</body>
</html>`)
})
```

Add the search implementation:

```go
// Search result structure
type SearchResult struct {
    Path    string
    Preview string
}

// Search documentation for a query
func searchDocumentation(fs absfs.FileSystem, query string) ([]SearchResult, error) {
    query = strings.ToLower(query)
    var results []SearchResult
    
    // Walk the filesystem and search for the query
    err := walkFilesystem(fs, "/", func(path string, info os.FileInfo) error {
        if info.IsDir() {
            return nil // Skip directories
        }
        
        // Skip non-text files based on extension
        ext := strings.ToLower(filepath.Ext(path))
        textExts := []string{".txt", ".md", ".html", ".htm", ".xml", ".json", ".csv", ".go", ".js", ".css"}
        isTextFile := false
        for _, textExt := range textExts {
            if ext == textExt {
                isTextFile = true
                break
            }
        }
        
        if !isTextFile {
            return nil
        }
        
        // Open and read the file
        file, err := fs.Open(path)
        if err != nil {
            return nil
        }
        defer file.Close()
        
        // Read file content
        content, err := io.ReadAll(file)
        if err != nil {
            return nil
        }
        
        // Check if query is in content
        contentStr := strings.ToLower(string(content))
        if strings.Contains(contentStr, query) {
            // Find a snippet around the query
            index := strings.Index(contentStr, query)
            start := index - 50
            if start < 0 {
                start = 0
            }
            end := index + len(query) + 50
            if end > len(contentStr) {
                end = len(contentStr)
            }
            
            // Create preview
            preview := "..." + string(content[start:end]) + "..."
            
            // Add to results
            results = append(results, SearchResult{
                Path:    path,
                Preview: preview,
            })
        }
        
        return nil
    })
    
    return results, err
}

// Walk filesystem recursively
func walkFilesystem(fs absfs.FileSystem, path string, fn func(path string, info os.FileInfo) error) error {
    entries, err := fs.ReadDir(path)
    if err != nil {
        return err
    }
    
    for _, entry := range entries {
        entryPath := filepath.Join(path, entry.Name())
        
        // Call function for this entry
        if err := fn(entryPath, entry); err != nil {
            return err
        }
        
        // Recurse if directory
        if entry.IsDir() {
            if err := walkFilesystem(fs, entryPath, fn); err != nil {
                return err
            }
        }
    }
    
    return nil
}
```

## Step 7: Build and Run the Server

Compile and run your documentation server:

```bash
go build
./docs-nfs-server --docs=/path/to/your/docs
```

You can customize the behavior with flags:

```bash
./docs-nfs-server --docs=/path/to/your/docs --port=2050 --http=8080 --allow=192.168.1.0/24
```

## Step 8: Mount the Documentation

On client machines, mount the NFS share:

### Linux

```bash
sudo mkdir -p /mnt/docs
sudo mount -t nfs server_hostname:/export/docs /mnt/docs
```

### macOS

```bash
sudo mkdir -p /mnt/docs
sudo mount -t nfs -o resvport server_hostname:/export/docs /mnt/docs
```

### Windows

```
mount -o anon \\server_hostname\export\docs Z:
```

Alternatively, users can access the documentation through the HTTP preview server at `http://server_hostname:8080/`.

## Complete Example

Here's the complete example with all features combined:

```go
package main

import (
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/absfs"
	"github.com/absfs/osfs"
)

// Command-line flags
var (
	docsDir    = flag.String("docs", "./docs", "Path to documentation directory")
	mountPath  = flag.String("mount", "/export/docs", "NFS mount path")
	port       = flag.Int("port", 2049, "NFS server port")
	allowedIPs = flag.String("allow", "", "Comma-separated list of allowed IP addresses/ranges")
	httpPort   = flag.Int("http", 8080, "HTTP preview server port (0 to disable)")
)

// Custom logger that tracks document access
type DocumentationLogger struct {
	accessLogs map[string]int
	mu         sync.Mutex
}

// Create a new documentation logger
func NewDocumentationLogger() *DocumentationLogger {
	return &DocumentationLogger{
		accessLogs: make(map[string]int),
	}
}

// Log a document access
func (l *DocumentationLogger) LogAccess(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	
	l.accessLogs[path]++
}

// Get access statistics
func (l *DocumentationLogger) GetStats() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	
	// Create a copy of the stats
	stats := make(map[string]int)
	for path, count := range l.accessLogs {
		stats[path] = count
	}
	
	return stats
}

// Implement logger interface for NFS server
func (l *DocumentationLogger) Log(level, msg string) {
	log.Printf("[%s] %s", level, msg)
	
	// Extract path from READ operations
	if strings.Contains(msg, "READ") {
		parts := strings.Split(msg, " ")
		for i, part := range parts {
			if part == "path:" && i < len(parts)-1 {
				l.LogAccess(parts[i+1])
				break
			}
		}
	}
}

// OnConnect is called when a client connects
func (l *DocumentationLogger) OnConnect(addr string) {
	log.Printf("Client connected: %s", addr)
}

// OnDisconnect is called when a client disconnects
func (l *DocumentationLogger) OnDisconnect(addr string) {
	log.Printf("Client disconnected: %s", addr)
}

// Search result structure
type SearchResult struct {
	Path    string
	Preview string
}

func main() {
	// Parse command-line flags
	flag.Parse()

	// Validate docs directory
	info, err := os.Stat(*docsDir)
	if err != nil {
		log.Fatalf("Error accessing docs directory: %v", err)
	}
	if !info.IsDir() {
		log.Fatalf("Docs path is not a directory: %s", *docsDir)
	}

	// Create the documentation filesystem
	log.Printf("Using documentation from: %s", *docsDir)
	fs, err := osfs.NewFS(*docsDir)
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Ensure there's an index
	if err := ensureDocumentationIndex(fs); err != nil {
		log.Printf("Warning: Failed to create documentation index: %v", err)
	}

	// Create logger
	logger := NewDocumentationLogger()

	// Configure NFS server options
	options := absnfs.ExportOptions{
		// Set read-only mode for security
		ReadOnly: true,
		
		// Enable security features
		Secure: true,
		
		// Parse allowed IPs
		AllowedIPs: parseAllowedIPs(*allowedIPs),
		
		// Performance settings optimized for documentation
		EnableReadAhead: true,
		ReadAheadSize: 262144, // 256KB
		
		// Longer attribute caching for mostly-static content
		AttrCacheTimeout: 30 * time.Second,
	}

	// Create NFS server
	server, err := absnfs.New(fs, options)
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Set the logger
	server.SetLogger(logger)

	// Start stats reporter
	go runStatsReporter(logger)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP preview server if enabled
	if *httpPort > 0 {
		startHTTPServer(fs, *httpPort)
		fmt.Printf("\nHTTP preview available at: http://localhost:%d/\n", *httpPort)
	}

	// Export the filesystem
	log.Printf("Starting NFS server on port %d...", *port)
	if err := server.Export(*mountPath, *port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	// Print mount instructions
	printMountInstructions(*mountPath, *port)

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutdown signal received...")

	// Unexport the filesystem
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	log.Println("Server stopped")
}

// Parse comma-separated list of allowed IPs
func parseAllowedIPs(ips string) []string {
	if ips == "" {
		return nil // Allow all
	}
	
	// Split by comma
	return strings.Split(ips, ",")
}

// Print mount instructions for different platforms
func printMountInstructions(mountPath string, port int) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}
	
	fmt.Println("\nDocumentation server is running!")
	fmt.Println("\nMount commands:")
	fmt.Printf("  Linux:   sudo mount -t nfs %s:%s /mnt/docs\n", hostname, mountPath)
	fmt.Printf("  macOS:   sudo mount -t nfs -o resvport %s:%s /mnt/docs\n", hostname, mountPath)
	fmt.Printf("  Windows: mount -o anon \\\\%s%s Z:\n", hostname, strings.ReplaceAll(mountPath, "/", "\\"))
	
	if port != 2049 {
		fmt.Println("\nNote: You're using a non-standard port. Use these commands instead:")
		fmt.Printf("  Linux/macOS: sudo mount -t nfs %s:%d:%s /mnt/docs\n", hostname, port, mountPath)
		fmt.Printf("  Windows:     mount -o anon \\\\%s@%d%s Z:\n", hostname, port, strings.ReplaceAll(mountPath, "/", "\\"))
	}
	
	fmt.Println("\nPress Ctrl+C to stop the server")
}

// Ensure documentation has an index
func ensureDocumentationIndex(fs absfs.FileSystem) error {
	// Check if index.html exists
	_, err := fs.Stat("/index.html")
	if err == nil {
		// Index already exists
		return nil
	}
	
	// List all top-level files and directories
	rootDir, err := fs.Open("/")
	if err != nil {
		return err
	}
	defer rootDir.Close()
	
	entries, err := rootDir.Readdir(-1)
	if err != nil {
		return err
	}
	
	// Create a simple index.html
	index, err := fs.Create("/index.html")
	if err != nil {
		return err
	}
	defer index.Close()
	
	// Write HTML header
	indexContent := `<!DOCTYPE html>
<html>
<head>
    <title>Documentation Index</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; line-height: 1.6; }
        h1 { color: #333; }
        ul { list-style-type: none; padding: 0; }
        li { margin-bottom: 10px; }
        li a { text-decoration: none; color: #0066cc; }
        li a:hover { text-decoration: underline; }
        .directory { font-weight: bold; }
        .file { }
        .search-box { margin: 20px 0; padding: 10px; background: #f5f5f5; border-radius: 5px; }
        .search-box input[type="text"] { padding: 8px; width: 300px; }
        .search-box input[type="submit"] { padding: 8px 16px; background: #0066cc; color: white; border: none; }
    </style>
</head>
<body>
    <h1>Documentation Index</h1>
    <div class="search-box">
        <form action="/search" method="GET">
            <input type="text" name="q" placeholder="Search documentation...">
            <input type="submit" value="Search">
        </form>
    </div>
    <p>This is an automatically generated index of available documentation.</p>
    <ul>
`
	
	// Add entries
	for _, entry := range entries {
		name := entry.Name()
		
		// Skip hidden files and the index itself
		if strings.HasPrefix(name, ".") || name == "index.html" {
			continue
		}
		
		// Create a link
		class := "file"
		if entry.IsDir() {
			class = "directory"
		}
		
		linkLine := fmt.Sprintf("        <li class=\"%s\"><a href=\"%s\">%s</a></li>\n", 
			class, name, name)
		indexContent += linkLine
	}
	
	// Close HTML
	indexContent += `    </ul>
    <hr>
    <p><em>Generated on ` + time.Now().Format("2006-01-02 15:04:05") + `</em></p>
</body>
</html>`
	
	// Write the content
	_, err = index.Write([]byte(indexContent))
	if err != nil {
		return err
	}
	
	log.Println("Created documentation index at /index.html")
	return nil
}

// Print access statistics periodically
func runStatsReporter(logger *DocumentationLogger) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		stats := logger.GetStats()
		
		// Skip if no access has been logged
		if len(stats) == 0 {
			continue
		}
		
		log.Println("Documentation access statistics:")
		
		// Convert to a sortable slice
		type docStat struct {
			path  string
			count int
		}
		
		var statsList []docStat
		for path, count := range stats {
			statsList = append(statsList, docStat{path, count})
		}
		
		// Sort by access count (descending)
		sort.Slice(statsList, func(i, j int) bool {
			return statsList[i].count > statsList[j].count
		})
		
		// Print top 10 (or fewer if there aren't that many)
		limit := 10
		if len(statsList) < limit {
			limit = len(statsList)
		}
		
		for i := 0; i < limit; i++ {
			log.Printf("  %s: %d accesses", statsList[i].path, statsList[i].count)
		}
	}
}

// Start HTTP preview server
func startHTTPServer(fs absfs.FileSystem, httpPort int) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Get the path
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		
		// Try to open the file
		file, err := fs.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		
		// Get file info
		info, err := file.Stat()
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		
		// Handle directory
		if info.IsDir() {
			// Redirect if not ending in /
			if !strings.HasSuffix(r.URL.Path, "/") {
				http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
				return
			}
			
			// Try to serve index.html in the directory
			indexPath := path
			if !strings.HasSuffix(indexPath, "/") {
				indexPath += "/"
			}
			indexPath += "index.html"
			
			indexFile, err := fs.Open(indexPath)
			if err == nil {
				defer indexFile.Close()
				// Serve the index file
				http.ServeContent(w, r, "index.html", info.ModTime(), indexFile.(io.ReadSeeker))
				return
			}
			
			// Generate directory listing
			entries, err := file.Readdir(-1)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, "<html><head><title>Directory: %s</title></head><body>", path)
			fmt.Fprintf(w, "<h1>Directory: %s</h1><ul>", path)
			
			// Add parent directory link if not root
			if path != "/" {
				fmt.Fprintf(w, "<li><a href=\"%s\">..</a></li>", filepath.Dir(path))
			}
			
			// Add entries
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				fmt.Fprintf(w, "<li><a href=\"%s%s\">%s</a></li>", r.URL.Path, name, name)
			}
			
			fmt.Fprintf(w, "</ul></body></html>")
			return
		}
		
		// Serve the file
		http.ServeContent(w, r, info.Name(), info.ModTime(), file.(io.ReadSeeker))
	})
	
	// Add search handler
	http.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			// Show search form
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>Documentation Search</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; line-height: 1.6; }
        h1 { color: #333; }
        .search-form { margin: 20px 0; }
        input[type="text"] { padding: 8px; width: 300px; }
        input[type="submit"] { padding: 8px 16px; background: #0066cc; color: white; border: none; }
    </style>
</head>
<body>
    <h1>Documentation Search</h1>
    <form class="search-form" action="/search" method="GET">
        <input type="text" name="q" placeholder="Enter search term...">
        <input type="submit" value="Search">
    </form>
    <p><a href="/">Back to Index</a></p>
</body>
</html>`)
			return
		}
		
		// Perform search
		results, err := searchDocumentation(fs, query)
		if err != nil {
			http.Error(w, "Search error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		
		// Show results
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>Search Results: %s</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; line-height: 1.6; }
        h1 { color: #333; }
        .search-form { margin: 20px 0; }
        input[type="text"] { padding: 8px; width: 300px; }
        input[type="submit"] { padding: 8px 16px; background: #0066cc; color: white; border: none; }
        .result { margin-bottom: 20px; }
        .result h3 { margin-bottom: 5px; }
        .result p { margin-top: 5px; color: #666; }
        .highlight { background-color: #FFFFCC; }
    </style>
</head>
<body>
    <h1>Search Results: %s</h1>
    <form class="search-form" action="/search" method="GET">
        <input type="text" name="q" value="%s" placeholder="Enter search term...">
        <input type="submit" value="Search">
    </form>
    `, query, query, html.EscapeString(query))
		
		if len(results) == 0 {
			fmt.Fprintf(w, "<p>No results found for <strong>%s</strong></p>", html.EscapeString(query))
		} else {
			fmt.Fprintf(w, "<p>Found %d results for <strong>%s</strong></p>", len(results), html.EscapeString(query))
			
			for _, result := range results {
				fmt.Fprintf(w, `<div class="result">
    <h3><a href="%s">%s</a></h3>
    <p>%s</p>
</div>`, result.Path, filepath.Base(result.Path), html.EscapeString(result.Preview))
			}
		}
		
		fmt.Fprintf(w, `<p><a href="/">Back to Index</a></p>
</body>
</html>`)
	})
	
	// Start HTTP server
	go func() {
		log.Printf("Starting HTTP preview server on port %d", httpPort)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", httpPort), nil); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()
}

// Search documentation for a query
func searchDocumentation(fs absfs.FileSystem, query string) ([]SearchResult, error) {
	query = strings.ToLower(query)
	var results []SearchResult
	
	// Walk the filesystem and search for the query
	err := walkFilesystem(fs, "/", func(path string, info os.FileInfo) error {
		if info.IsDir() {
			return nil // Skip directories
		}
		
		// Skip non-text files based on extension
		ext := strings.ToLower(filepath.Ext(path))
		textExts := []string{".txt", ".md", ".html", ".htm", ".xml", ".json", ".csv", ".go", ".js", ".css"}
		isTextFile := false
		for _, textExt := range textExts {
			if ext == textExt {
				isTextFile = true
				break
			}
		}
		
		if !isTextFile {
			return nil
		}
		
		// Open and read the file
		file, err := fs.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		
		// Read file content
		content, err := io.ReadAll(file)
		if err != nil {
			return nil
		}
		
		// Check if query is in content
		contentStr := strings.ToLower(string(content))
		if strings.Contains(contentStr, query) {
			// Find a snippet around the query
			index := strings.Index(contentStr, query)
			start := index - 50
			if start < 0 {
				start = 0
			}
			end := index + len(query) + 50
			if end > len(contentStr) {
				end = len(contentStr)
			}
			
			// Create preview
			preview := "..." + string(content[start:end]) + "..."
			
			// Add to results
			results = append(results, SearchResult{
				Path:    path,
				Preview: preview,
			})
		}
		
		return nil
	})
	
	return results, err
}

// Walk filesystem recursively
func walkFilesystem(fs absfs.FileSystem, path string, fn func(path string, info os.FileInfo) error) error {
	entries, err := fs.ReadDir(path)
	if err != nil {
		return err
	}
	
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		
		// Call function for this entry
		if err := fn(entryPath, entry); err != nil {
			return err
		}
		
		// Recurse if directory
		if entry.IsDir() {
			if err := walkFilesystem(fs, entryPath, fn); err != nil {
				return err
			}
		}
	}
	
	return nil
}
```

## Use Cases and Enhancements

Once you have the basic read-only documentation server working, consider these enhancements:

1. **Documentation Conversion**: Automatically convert Markdown to HTML
2. **Syntax Highlighting**: Add syntax highlighting for code blocks
3. **Full-Text Search**: Implement more advanced search functionality
4. **Versioning**: Add support for different documentation versions
5. **Analytics**: Track popular documentation pages
6. **Access Control**: Add basic authentication for sensitive documentation
7. **Automated Updates**: Automatically update documentation from a Git repository

## Benefits of Using ABSNFS for Documentation

Using ABSNFS for documentation provides several advantages:

1. **Universal Access**: Documents are available via both NFS and HTTP
2. **Read-Only Security**: Prevents accidental modifications
3. **Standard Tools**: Users can use familiar file browsers or web browsers
4. **Performance**: Efficient caching for fast access
5. **Search Capability**: Built-in search functionality
6. **Usage Analytics**: Track which documents are most accessed
7. **Simple Deployment**: Easy to deploy and maintain

## Conclusion

You've created a powerful read-only documentation server using ABSNFS. This server provides both NFS and HTTP access to documentation, with features like search, access tracking, and automatic indexing.

By leveraging the strength of NFS for file access and providing a web interface, you've created a versatile documentation platform that can serve various use cases, from internal company documentation to public reference materials.

## Next Steps

- Enhance security by implementing custom authentication for restricted documentation
- Create a multi-export server to manage multiple document repositories
- Optimize performance for large documentation collections