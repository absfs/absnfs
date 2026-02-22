package absnfs

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func TestNFSNodeOperations(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test file and directory
	f, err := fs.Create("/test.txt")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if _, err := f.Write([]byte("test content")); err != nil {
		f.Close()
		t.Fatalf("Failed to write test content: %v", err)
	}
	f.Close()
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create NFSNode instances
	fileNode := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/test.txt",
		attrs: &NFSAttrs{
			Mode: 0644,
			Size: 12,
			// Mtime: time.Now()
			// Atime: time.Now()
		},
	}

	dirNode := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/testdir",
		attrs: &NFSAttrs{
			Mode: 0755 | os.ModeDir,
			Size: 0,
			// Mtime: time.Now()
			// Atime: time.Now()
		},
	}

	// Test file operations
	t.Run("file operations", func(t *testing.T) {
		// Test Read
		buf := make([]byte, 12)
		n, err := fileNode.Read(buf)
		if err != nil && err != io.EOF {
			t.Errorf("Read failed: %v", err)
		}
		if n != len("test content") {
			t.Errorf("Expected to read %d bytes, got %d", len("test content"), n)
		}
		if string(buf[:n]) != "test content" {
			t.Errorf("Expected 'test content', got '%s'", string(buf[:n]))
		}

		// Test ReadAt
		buf = make([]byte, 7)
		n, err = fileNode.ReadAt(buf, 5)
		if err != nil && err != io.EOF {
			t.Errorf("ReadAt failed: %v", err)
		}
		if string(buf[:n]) != "content" {
			t.Errorf("Expected 'content', got '%s'", string(buf[:n]))
		}

		// Test Write
		n, err = fileNode.Write([]byte("new content"))
		if err != nil {
			t.Errorf("Write failed: %v", err)
		}
		if n != len("new content") {
			t.Errorf("Expected to write %d bytes, got %d", len("new content"), n)
		}

		// Test WriteAt
		n, err = fileNode.WriteAt([]byte("updated"), 0)
		if err != nil {
			t.Errorf("WriteAt failed: %v", err)
		}
		if n != len("updated") {
			t.Errorf("Expected to write %d bytes, got %d", len("updated"), n)
		}

		// Test Seek
		offset, err := fileNode.Seek(5, io.SeekStart)
		if err != nil {
			t.Errorf("Seek failed: %v", err)
		}
		if offset != 5 {
			t.Errorf("Expected offset 5, got %d", offset)
		}

		// Test WriteString
		n, err = fileNode.WriteString("test string")
		if err != nil {
			t.Errorf("WriteString failed: %v", err)
		}
		if n != len("test string") {
			t.Errorf("Expected to write %d bytes, got %d", len("test string"), n)
		}

		// Test Truncate
		if err := fileNode.Truncate(5); err != nil {
			t.Errorf("Truncate failed: %v", err)
		}

		// Test Sync
		if err := fileNode.Sync(); err != nil {
			t.Errorf("Sync failed: %v", err)
		}

		// Test Chmod
		if err := fileNode.Chmod(0600); err != nil {
			t.Errorf("Chmod failed: %v", err)
		}

		// Test Chown
		if err := fileNode.Chown(1000, 1000); err != nil {
			t.Errorf("Chown failed: %v", err)
		}

		// Test Chtimes
		now := time.Now()
		if err := fileNode.Chtimes(now, now); err != nil {
			t.Errorf("Chtimes failed: %v", err)
		}

		// Test Name
		if name := fileNode.Name(); name != "test.txt" {
			t.Errorf("Expected name 'test.txt', got '%s'", name)
		}

		// Test Stat
		info, err := fileNode.Stat()
		if err != nil {
			t.Errorf("Stat failed: %v", err)
		}
		if info.Name() != "test.txt" {
			t.Errorf("Expected stat name 'test.txt', got '%s'", info.Name())
		}
	})

	// Test directory operations
	t.Run("directory operations", func(t *testing.T) {
		// Create some files in the test directory
		f1, err := fs.Create("/testdir/file1.txt")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		if _, err := f1.Write([]byte("file1")); err != nil {
			f1.Close()
			t.Fatalf("Failed to write test content: %v", err)
		}
		f1.Close()

		f2, err := fs.Create("/testdir/file2.txt")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		if _, err := f2.Write([]byte("file2")); err != nil {
			f2.Close()
			t.Fatalf("Failed to write test content: %v", err)
		}
		f2.Close()

		// Test Readdir
		entries, err := dirNode.Readdir(-1)
		if err != nil {
			t.Errorf("Readdir failed: %v", err)
		}
		if len(entries) != 2 {
			t.Errorf("Expected 2 directory entries, got %d", len(entries))
		}

		// Test Readdirnames
		names, err := dirNode.Readdirnames(-1)
		if err != nil {
			t.Errorf("Readdirnames failed: %v", err)
		}
		if len(names) != 2 {
			t.Errorf("Expected 2 directory names, got %d", len(names))
		}

		// Test Name for root directory
		rootNode := &NFSNode{
			SymlinkFileSystem: fs,
			path:              "/",
		}
		if name := rootNode.Name(); name != "/" {
			t.Errorf("Expected root name '/', got '%s'", name)
		}

		// Test Chdir
		if err := dirNode.Chdir(); err != nil {
			t.Errorf("Chdir failed: %v", err)
		}
	})

	// Test error cases
	t.Run("error cases", func(t *testing.T) {
		nonexistentNode := &NFSNode{
			SymlinkFileSystem: fs,
			path:              "/nonexistent",
		}

		// Test operations on non-existent file
		if _, err := nonexistentNode.Read(make([]byte, 10)); err == nil {
			t.Error("Expected error reading non-existent file")
		}
		if _, err := nonexistentNode.ReadAt(make([]byte, 10), 0); err == nil {
			t.Error("Expected error reading non-existent file")
		}
		if _, err := nonexistentNode.Write([]byte("test")); err == nil {
			t.Error("Expected error writing non-existent file")
		}
		if _, err := nonexistentNode.WriteAt([]byte("test"), 0); err == nil {
			t.Error("Expected error writing non-existent file")
		}
		if _, err := nonexistentNode.Seek(0, io.SeekStart); err == nil {
			t.Error("Expected error seeking non-existent file")
		}
		if err := nonexistentNode.Sync(); err == nil {
			t.Error("Expected error syncing non-existent file")
		}
		if _, err := nonexistentNode.Readdir(-1); err == nil {
			t.Error("Expected error reading non-existent directory")
		}
	})
}

// TestR3_WriteWithContextErrShadowing verifies that a successful write
// preserves the written data even if Chtimes fails (the fix renamed the
// Chtimes error variable to chtimesErr to avoid shadowing the outer err).
func TestR3_WriteWithContextErrShadowing(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	// Create a test file
	f, err := fs.Create("/writefile")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("initial"))
	f.Close()

	node, err := nfs.Lookup("/writefile")
	if err != nil {
		t.Fatal(err)
	}

	// Write new data
	data := []byte("hello world")
	n, err := nfs.Write(node, 0, data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != int64(len(data)) {
		t.Errorf("Expected %d bytes written, got %d", len(data), n)
	}

	// Read back and verify data is preserved
	readData, err := nfs.Read(node, 0, 100)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(readData) != "hello world" {
		t.Errorf("Expected 'hello world', got %q", string(readData))
	}
}

func TestNFSNodeReadDirZeroCoverage(t *testing.T) {
	mfs, _ := memfs.NewFS()
	mfs.Mkdir("/testdir", 0755)
	mfs.Create("/testdir/file1.txt")
	mfs.Create("/testdir/file2.txt")

	node := &NFSNode{
		SymlinkFileSystem: mfs,
		path:              "/testdir",
		attrs:             &NFSAttrs{Mode: os.ModeDir | 0755},
	}

	t.Run("read directory entries", func(t *testing.T) {
		entries, err := node.ReadDir(-1)
		if err != nil {
			t.Fatalf("ReadDir failed: %v", err)
		}
		if len(entries) < 2 {
			t.Errorf("Expected at least 2 entries, got %d", len(entries))
		}
	})

	t.Run("read limited entries", func(t *testing.T) {
		entries, err := node.ReadDir(1)
		if err != nil {
			t.Fatalf("ReadDir failed: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("Expected 1 entry, got %d", len(entries))
		}
	})
}
