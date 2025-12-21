//go:build integration

package absnfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// TestIntegrationMountAndAccess tests the full NFS mount workflow
func TestIntegrationMountAndAccess(t *testing.T) {
	// Use a high port to avoid requiring root for NFS server
	nfsPort := 12049
	mountPort := 12049

	// Create safe mount handler
	safeMount, err := NewSafeTestMount(nfsPort, mountPort)
	if err != nil {
		t.Fatalf("Failed to create SafeTestMount: %v", err)
	}
	defer safeMount.MustCleanup()

	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test content
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	f, err := fs.Create("/testdir/hello.txt")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	testContent := "Hello from NFS integration test!\n"
	f.Write([]byte(testContent))
	f.Close()

	// Create NFS handler
	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Create and start NFS server
	server, err := NewServer(ServerOptions{
		Port:             nfsPort,
		MountPort:        mountPort,
		Debug:            true,
		UseRecordMarking: true,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	server.SetHandler(nfs)

	if err := server.Listen(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	// Give server time to start
	time.Sleep(200 * time.Millisecond)

	// Prepare mount point (creates /mnt/absnfs-TIMESTAMP-PID)
	if err := safeMount.Prepare(); err != nil {
		t.Fatalf("Failed to prepare mount point: %v", err)
	}

	t.Logf("Mount point: %s", safeMount.MountPoint)
	t.Logf("NFS Port: %d", nfsPort)

	// Mount the NFS share
	// Note: On macOS, mount_nfs requires the server to respond to portmapper queries
	// unless we use specific mount options
	mountOpts := fmt.Sprintf("resvport,nolocks,vers=3,tcp,port=%d,mountport=%d", nfsPort, mountPort)
	t.Logf("Mount options: %s", mountOpts)

	if err := safeMount.Mount(); err != nil {
		// Mount might fail if not running as root or portmapper isn't available
		t.Skipf("Mount failed (may require root or portmapper): %v", err)
	}

	// Verify mount is active
	if err := safeMount.VerifyMounted(); err != nil {
		t.Fatalf("Mount verification failed: %v", err)
	}

	t.Log("NFS share mounted successfully")

	// Test: List files in mounted directory
	t.Run("list files", func(t *testing.T) {
		entries, err := os.ReadDir(safeMount.MountPoint)
		if err != nil {
			t.Fatalf("Failed to read directory: %v", err)
		}

		found := false
		for _, entry := range entries {
			t.Logf("Found: %s (dir=%v)", entry.Name(), entry.IsDir())
			if entry.Name() == "testdir" && entry.IsDir() {
				found = true
			}
		}

		if !found {
			t.Error("Expected to find 'testdir' directory")
		}
	})

	// Test: Read file content
	t.Run("read file", func(t *testing.T) {
		safePath, err := safeMount.SafePath("testdir/hello.txt")
		if err != nil {
			t.Fatalf("Failed to get safe path: %v", err)
		}

		content, err := os.ReadFile(safePath)
		if err != nil {
			t.Fatalf("Failed to read file: %v", err)
		}

		if string(content) != testContent {
			t.Errorf("Content mismatch: expected %q, got %q", testContent, string(content))
		}
	})

	// Test: Create new file
	t.Run("create file", func(t *testing.T) {
		safePath, err := safeMount.SafePath("newfile.txt")
		if err != nil {
			t.Fatalf("Failed to get safe path: %v", err)
		}

		newContent := "New file created via NFS!\n"
		if err := os.WriteFile(safePath, []byte(newContent), 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}

		// Verify the file was created in the underlying filesystem
		content, err := os.ReadFile(safePath)
		if err != nil {
			t.Fatalf("Failed to read new file: %v", err)
		}

		if string(content) != newContent {
			t.Errorf("New file content mismatch: expected %q, got %q", newContent, string(content))
		}
	})

	// Test: Create directory
	t.Run("create directory", func(t *testing.T) {
		safePath, err := safeMount.SafePath("newdir")
		if err != nil {
			t.Fatalf("Failed to get safe path: %v", err)
		}

		if err := os.Mkdir(safePath, 0755); err != nil {
			t.Fatalf("Failed to create directory: %v", err)
		}

		info, err := os.Stat(safePath)
		if err != nil {
			t.Fatalf("Failed to stat new directory: %v", err)
		}

		if !info.IsDir() {
			t.Error("Expected directory, got file")
		}
	})

	// Test: Delete file
	t.Run("delete file", func(t *testing.T) {
		safePath, err := safeMount.SafePath("newfile.txt")
		if err != nil {
			t.Fatalf("Failed to get safe path: %v", err)
		}

		if err := os.Remove(safePath); err != nil {
			t.Fatalf("Failed to remove file: %v", err)
		}

		if _, err := os.Stat(safePath); !os.IsNotExist(err) {
			t.Error("File still exists after removal")
		}
	})

	// Unmount
	if err := safeMount.Unmount(); err != nil {
		t.Errorf("Unmount failed: %v", err)
	}
}

// TestIntegrationWithPortmapper tests NFS with the portmapper service
func TestIntegrationWithPortmapper(t *testing.T) {
	// This test requires root privileges for port 111
	if os.Geteuid() != 0 {
		t.Skip("Skipping portmapper test: requires root privileges")
	}

	nfsPort := 2049
	mountPort := 2049

	// Create safe mount handler
	safeMount, err := NewSafeTestMount(nfsPort, mountPort)
	if err != nil {
		t.Fatalf("Failed to create SafeTestMount: %v", err)
	}
	defer safeMount.MustCleanup()

	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test content
	f, err := fs.Create("/testfile.txt")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Write([]byte("Test content"))
	f.Close()

	// Create NFS handler
	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Create server
	server, err := NewServer(ServerOptions{
		Port:  nfsPort,
		Debug: true,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	server.SetHandler(nfs)

	// Start with portmapper
	if err := server.StartWithPortmapper(); err != nil {
		t.Fatalf("Failed to start server with portmapper: %v", err)
	}
	defer server.Stop()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Test rpcinfo
	t.Run("rpcinfo", func(t *testing.T) {
		cmd := exec.Command("rpcinfo", "-p", "localhost")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rpcinfo failed: %v\n%s", err, output)
		}
		t.Logf("rpcinfo output:\n%s", output)
	})

	// Test showmount
	t.Run("showmount", func(t *testing.T) {
		cmd := exec.Command("showmount", "-e", "localhost")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("showmount failed: %v\n%s", err, output)
		}
		t.Logf("showmount output:\n%s", output)
	})

	// Prepare and mount
	if err := safeMount.Prepare(); err != nil {
		t.Fatalf("Failed to prepare mount point: %v", err)
	}

	if err := safeMount.Mount(); err != nil {
		t.Fatalf("Mount failed: %v", err)
	}

	// Verify content
	content, err := os.ReadFile(filepath.Join(safeMount.MountPoint, "testfile.txt"))
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(content) != "Test content" {
		t.Errorf("Content mismatch: expected 'Test content', got %q", string(content))
	}

	// Unmount
	if err := safeMount.Unmount(); err != nil {
		t.Errorf("Unmount failed: %v", err)
	}
}
