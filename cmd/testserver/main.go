// Example NFS server using absnfs with an in-memory filesystem.
//
// This demonstrates how to create an NFS server that exposes an absfs
// filesystem to standard NFS clients (macOS, Linux).
//
// Usage:
//
//	# Without portmapper (non-privileged port):
//	go run ./cmd/testserver -port 12049
//	sudo mount_nfs -o resvport,nolocks,vers=3,tcp,port=12049,mountport=12049 localhost:/ /mnt/test
//
//	# With portmapper (requires root):
//	sudo go run ./cmd/testserver -portmapper
//	sudo mount -t nfs localhost:/ /mnt/test
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	port := flag.Int("port", 2049, "NFS port to listen on")
	usePortmapper := flag.Bool("portmapper", false, "Start with portmapper service (requires root for port 111)")
	debug := flag.Bool("debug", true, "Enable debug logging")
	flag.Parse()

	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create memfs: %v", err)
	}

	// Create some test content
	if err := fs.Mkdir("/test", 0755); err != nil {
		log.Fatalf("Failed to create test directory: %v", err)
	}
	f, err := fs.Create("/test/hello.txt")
	if err != nil {
		log.Fatalf("Failed to create test file: %v", err)
	}
	f.Write([]byte("Hello from NFS!\n"))
	f.Close()

	f, err = fs.Create("/README.txt")
	if err != nil {
		log.Fatalf("Failed to create README: %v", err)
	}
	f.Write([]byte("Welcome to the absnfs example server.\n\nThis is an in-memory filesystem exported via NFSv3.\n"))
	f.Close()

	// Create NFS handler
	nfs, err := absnfs.New(fs, absnfs.ExportOptions{})
	if err != nil {
		log.Fatalf("Failed to create NFS: %v", err)
	}

	// Create server
	server, err := absnfs.NewServer(absnfs.ServerOptions{
		Port:             *port,
		MountPort:        *port, // Use same port for mount
		Debug:            *debug,
		UseRecordMarking: true,
	})
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}
	server.SetHandler(nfs)

	// Start server
	if *usePortmapper {
		// Start with portmapper (requires root for port 111)
		if err := server.StartWithPortmapper(); err != nil {
			log.Fatalf("Failed to start server with portmapper: %v", err)
		}
		fmt.Println("NFS server started with portmapper")
		fmt.Println("Port 111: Portmapper")
		fmt.Printf("Port %d: NFS + Mount\n", *port)
	} else {
		// Start without portmapper
		if err := server.Listen(); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
		fmt.Printf("NFS server started on port %d\n", *port)
	}

	fmt.Println()
	fmt.Println("Test commands:")
	if *usePortmapper {
		fmt.Println("  rpcinfo -p localhost")
		fmt.Println("  showmount -e localhost")
		fmt.Println("  sudo mount -t nfs localhost:/ /mnt/test")
	} else {
		fmt.Printf("  sudo mount_nfs -o resvport,nolocks,vers=3,tcp,port=%d,mountport=%d localhost:/ /mnt/test\n", *port, *port)
	}
	fmt.Println()
	fmt.Println("Press Ctrl+C to stop")

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down...")
	if err := server.Stop(); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
}
