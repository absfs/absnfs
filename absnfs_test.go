package absnfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

func TestNFSAttrs(t *testing.T) {
	t.Run("validity and invalidation", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode:  0644,
			Size:  1234,
			Mtime: time.Now(),
			Atime: time.Now(),
			Uid:   1000,
			Gid:   1000,
		}

		// Test initial state
		if attrs.IsValid() {
			t.Error("Attributes should not be valid before refresh")
		}

		// Test refresh
		attrs.Refresh()
		if !attrs.IsValid() {
			t.Error("Attributes should be valid after refresh")
		}

		// Test expiration
		time.Sleep(3 * time.Second) // Wait longer than validity period
		if attrs.IsValid() {
			t.Error("Attributes should be invalid after expiration")
		}

		// Test explicit invalidation
		attrs.Refresh()
		if !attrs.IsValid() {
			t.Error("Attributes should be valid after second refresh")
		}
		attrs.Invalidate()
		if attrs.IsValid() {
			t.Error("Attributes should be invalid after explicit invalidation")
		}
	})
}

func TestNewAbsNFS(t *testing.T) {
	t.Run("initialization errors", func(t *testing.T) {
		// Test nil filesystem
		nfs, err := New(nil, ExportOptions{})
		if err == nil {
			t.Error("New() with nil filesystem should return error")
		}
		if nfs != nil {
			t.Error("New() with nil filesystem should return nil server")
		}

		// Test with invalid root directory
		invalidFS := &mockFS{statError: os.ErrNotExist}
		nfs, err = New(invalidFS, ExportOptions{})
		if err == nil {
			t.Error("New() with invalid root directory should return error")
		}
		if nfs != nil {
			t.Error("New() with invalid root directory should return nil server")
		}
	})

	t.Run("successful initialization", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		// Test with default options
		nfs, err := New(fs, ExportOptions{})
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}
		if nfs == nil {
			t.Fatal("New() returned nil server")
		}

		// Verify root node initialization
		if nfs.root == nil {
			t.Error("Root node not initialized")
		}
		if nfs.root.path != "/" {
			t.Errorf("Root path = %q, want %q", nfs.root.path, "/")
		}
		if nfs.root.attrs == nil {
			t.Error("Root attributes not initialized")
		}
		if !nfs.root.attrs.Mode.IsDir() {
			t.Error("Root node should be a directory")
		}
		if nfs.root.children == nil {
			t.Error("Root children map not initialized")
		}

		// Verify component initialization
		if nfs.fileMap == nil {
			t.Error("File handle map not initialized")
		}
		if nfs.attrCache == nil {
			t.Error("Attribute cache not initialized")
		}
		if nfs.readBuf == nil {
			t.Error("Read-ahead buffer not initialized")
		}
		if nfs.logger == nil {
			t.Error("Logger not initialized")
		}
	})
}

// setupAbsNFSTest prepares a test environment and returns a cleanup function
func setupAbsNFSTest(t *testing.T) (context.Context, func()) {
	// Capture and limit logging output
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)

	// Set up context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	// Return cleanup function
	return ctx, func() {
		cancel()
		log.SetOutput(io.Discard) // Discard remaining logs
		if t.Failed() && logBuf.Len() > 0 {
			// Only show logs if test failed and there are logs
			t.Logf("Test logs:\n%s", logBuf.String())
		}
	}
}

func setupTestFS(t *testing.T, ctx context.Context) (absfs.FileSystem, *AbsfsNFS) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test files and directories with timeout
	done := make(chan error, 1)
	go func() {
		if err := fs.MkdirAll("/testdir", 0755); err != nil {
			done <- fmt.Errorf("Failed to create test directory: %v", err)
			return
		}

		f, err := fs.Create("/testdir/test.txt")
		if err != nil {
			done <- fmt.Errorf("Failed to create test file: %v", err)
			return
		}
		// Limit test content size
		if _, err := f.Write([]byte("test content")); err != nil {
			f.Close()
			done <- fmt.Errorf("Failed to write test content: %v", err)
			return
		}
		f.Close()
		done <- nil
	}()

	select {
	case <-ctx.Done():
		t.Fatal("Setup timed out")
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	}

	nfs, err := New(fs, ExportOptions{
		ReadOnly: false,
		Secure:   true,
	})
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	return fs, nfs
}

func TestLookup(t *testing.T) {
	ctx, cleanup := setupAbsNFSTest(t)
	defer cleanup()

	_, nfs := setupTestFS(t, ctx)

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"root", "/", false},
		{"existing dir", "/testdir", false},
		{"existing file", "/testdir/test.txt", false},
		{"non-existent", "/nonexistent", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan error, 1)
			var node *NFSNode
			go func() {
				var err error
				node, err = nfs.Lookup(tt.path)
				if (err != nil) != tt.wantErr {
					done <- fmt.Errorf("Lookup() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if !tt.wantErr && node == nil {
					done <- fmt.Errorf("Lookup() returned nil node for existing path")
					return
				}
				done <- nil
			}()

			select {
			case <-ctx.Done():
				t.Fatal("Lookup operation timed out")
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestReadWrite(t *testing.T) {
	ctx, cleanup := setupAbsNFSTest(t)
	defer cleanup()

	_, nfs := setupTestFS(t, ctx)

	done := make(chan error, 1)
	go func() {
		// Test writing to a new file
		dir, err := nfs.Lookup("/testdir")
		if err != nil {
			done <- fmt.Errorf("Failed to lookup directory: %v", err)
			return
		}

		attrs := &NFSAttrs{
			Mode:  0644,
			Mtime: time.Now(),
			Atime: time.Now(),
		}

		node, err := nfs.Create(dir, "write_test.txt", attrs)
		if err != nil {
			done <- fmt.Errorf("Failed to create test file: %v", err)
			return
		}

		// Limit test data size
		testData := []byte("test write data")
		written, err := nfs.Write(node, 0, testData)
		if err != nil {
			done <- fmt.Errorf("Write failed: %v", err)
			return
		}
		if written != int64(len(testData)) {
			done <- fmt.Errorf("Write() wrote %d bytes, want %d", written, len(testData))
			return
		}

		// Test reading the written data
		read, err := nfs.Read(node, 0, int64(len(testData)))
		if err != nil {
			done <- fmt.Errorf("Read failed: %v", err)
			return
		}
		if string(read) != string(testData) {
			done <- fmt.Errorf("Read() = %q, want %q", string(read), string(testData))
			return
		}
		done <- nil
	}()

	select {
	case <-ctx.Done():
		t.Fatal("Read/Write operations timed out")
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	}
}

func TestReadDir(t *testing.T) {
	ctx, cleanup := setupAbsNFSTest(t)
	defer cleanup()

	_, nfs := setupTestFS(t, ctx)

	done := make(chan error, 1)
	go func() {
		dir, err := nfs.Lookup("/testdir")
		if err != nil {
			done <- fmt.Errorf("Failed to lookup directory: %v", err)
			return
		}

		entries, err := nfs.ReadDir(dir)
		if err != nil {
			done <- fmt.Errorf("ReadDir failed: %v", err)
			return
		}

		if len(entries) != 1 {
			done <- fmt.Errorf("ReadDir() returned %d entries, want 1", len(entries))
			return
		}

		if entries[0].path != "/testdir/test.txt" {
			done <- fmt.Errorf("ReadDir() entry path = %q, want %q", entries[0].path, "/testdir/test.txt")
			return
		}
		done <- nil
	}()

	select {
	case <-ctx.Done():
		t.Fatal("ReadDir operation timed out")
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	}
}

func TestRemove(t *testing.T) {
	ctx, cleanup := setupAbsNFSTest(t)
	defer cleanup()

	_, nfs := setupTestFS(t, ctx)

	done := make(chan error, 1)
	go func() {
		dir, err := nfs.Lookup("/testdir")
		if err != nil {
			done <- fmt.Errorf("Failed to lookup directory: %v", err)
			return
		}

		if err := nfs.Remove(dir, "test.txt"); err != nil {
			done <- fmt.Errorf("Remove failed: %v", err)
			return
		}

		// Verify file is gone
		if _, err := nfs.Lookup("/testdir/test.txt"); err == nil {
			done <- fmt.Errorf("File still exists after Remove()")
			return
		}
		done <- nil
	}()

	select {
	case <-ctx.Done():
		t.Fatal("Remove operation timed out")
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	}
}

func TestRename(t *testing.T) {
	ctx, cleanup := setupAbsNFSTest(t)
	defer cleanup()

	_, nfs := setupTestFS(t, ctx)

	done := make(chan error, 1)
	go func() {
		oldDir, err := nfs.Lookup("/testdir")
		if err != nil {
			done <- fmt.Errorf("Failed to lookup source directory: %v", err)
			return
		}

		// Create a new directory for rename target
		fs := oldDir.FileSystem
		if err := fs.MkdirAll("/newdir", 0755); err != nil {
			done <- fmt.Errorf("Failed to create target directory: %v", err)
			return
		}

		newDir, err := nfs.Lookup("/newdir")
		if err != nil {
			done <- fmt.Errorf("Failed to lookup target directory: %v", err)
			return
		}

		if err := nfs.Rename(oldDir, "test.txt", newDir, "renamed.txt"); err != nil {
			done <- fmt.Errorf("Rename failed: %v", err)
			return
		}

		// Verify file was moved
		if _, err := nfs.Lookup("/testdir/test.txt"); err == nil {
			done <- fmt.Errorf("Source file still exists after Rename()")
			return
		}

		if _, err := nfs.Lookup("/newdir/renamed.txt"); err != nil {
			done <- fmt.Errorf("Target file doesn't exist after Rename()")
			return
		}
		done <- nil
	}()

	select {
	case <-ctx.Done():
		t.Fatal("Rename operation timed out")
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	}
}

func TestReadOnlyMode(t *testing.T) {
	ctx, cleanup := setupAbsNFSTest(t)
	defer cleanup()

	done := make(chan error, 1)
	go func() {
		fs, err := memfs.NewFS()
		if err != nil {
			done <- fmt.Errorf("Failed to create memfs: %v", err)
			return
		}

		nfs, err := New(fs, ExportOptions{
			ReadOnly: true,
			Secure:   true,
		})
		if err != nil {
			done <- fmt.Errorf("Failed to create NFS server: %v", err)
			return
		}

		dir, err := nfs.Lookup("/")
		if err != nil {
			done <- fmt.Errorf("Failed to lookup root directory: %v", err)
			return
		}

		// Attempt to create a file in read-only mode
		_, err = nfs.Create(dir, "test.txt", &NFSAttrs{Mode: 0644})
		if err != os.ErrPermission {
			done <- fmt.Errorf("Create() in read-only mode = %v, want %v", err, os.ErrPermission)
			return
		}
		done <- nil
	}()

	select {
	case <-ctx.Done():
		t.Fatal("Read-only mode operation timed out")
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	}
}

// mockFS implements a minimal absfs.FileSystem for testing
type mockFS struct {
	statError error
}

func (m *mockFS) Stat(path string) (os.FileInfo, error)        { return nil, m.statError }
func (m *mockFS) Lstat(path string) (os.FileInfo, error)       { return nil, m.statError }
func (m *mockFS) Create(path string) (absfs.File, error)       { return nil, m.statError }
func (m *mockFS) Mkdir(path string, perm os.FileMode) error    { return m.statError }
func (m *mockFS) MkdirAll(path string, perm os.FileMode) error { return m.statError }
func (m *mockFS) Open(path string) (absfs.File, error)         { return nil, m.statError }
func (m *mockFS) OpenFile(path string, flag int, perm os.FileMode) (absfs.File, error) {
	return nil, m.statError
}
func (m *mockFS) Remove(path string) error               { return m.statError }
func (m *mockFS) RemoveAll(path string) error            { return m.statError }
func (m *mockFS) Rename(oldpath, newpath string) error   { return m.statError }
func (m *mockFS) Truncate(path string, size int64) error { return m.statError }
func (m *mockFS) Chdir(path string) error                { return m.statError }
func (m *mockFS) Chmod(path string, mode os.FileMode) error {
	return m.statError
}
func (m *mockFS) Chown(path string, uid, gid int) error { return m.statError }
func (m *mockFS) Chtimes(path string, atime time.Time, mtime time.Time) error {
	return m.statError
}
func (m *mockFS) Symlink(oldname, newname string) error { return m.statError }
func (m *mockFS) Readlink(path string) (string, error)  { return "", m.statError }
func (m *mockFS) Getwd() (string, error)                { return "", m.statError }
func (m *mockFS) ListSeparator() uint8                  { return '/' }
func (m *mockFS) Separator() uint8                      { return '/' }
func (m *mockFS) TempDir() string                       { return "/tmp" }
