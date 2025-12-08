package absnfs

import (
	"os"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func TestOperationsAdvanced(t *testing.T) {
	// Test Read and Write operations
	t.Run("read and write operations", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		nfs, err := New(fs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create test file with content
		testContent := []byte("Hello, NFS World!")
		f, err := fs.Create("/testfile")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		if _, err := f.Write(testContent); err != nil {
			t.Fatalf("Failed to write test content: %v", err)
		}
		f.Close()

		// Get file node
		node, err := nfs.Lookup("/testfile")
		if err != nil {
			t.Fatalf("Failed to lookup file: %v", err)
		}

		// Test basic read
		data, err := nfs.Read(node, 0, int64(len(testContent)))
		if err != nil {
			t.Errorf("Read failed: %v", err)
		}
		if string(data) != string(testContent) {
			t.Errorf("Read returned wrong data: got %s, want %s", string(data), string(testContent))
		}

		// Test partial read
		data, err = nfs.Read(node, 7, 3)
		if err != nil {
			t.Errorf("Partial read failed: %v", err)
		}
		if string(data) != "NFS" {
			t.Errorf("Partial read returned wrong data: got %s, want NFS", string(data))
		}

		// Test read beyond EOF
		data, err = nfs.Read(node, int64(len(testContent)), 10)
		if err != nil {
			t.Errorf("Read beyond EOF failed: %v", err)
		}
		if len(data) != 0 {
			t.Errorf("Read beyond EOF returned data: got %d bytes, want 0", len(data))
		}

		// Test read with negative offset
		if _, err := nfs.Read(node, -1, 10); err == nil {
			t.Error("Expected error for negative offset")
		}

		// Test read with negative count
		if _, err := nfs.Read(node, 0, -1); err == nil {
			t.Error("Expected error for negative count")
		}

		// Test read with nil node
		if _, err := nfs.Read(nil, 0, 10); err == nil {
			t.Error("Expected error for nil node")
		}

		// Test write
		newContent := []byte("Updated content")
		n, err := nfs.Write(node, 0, newContent)
		if err != nil {
			t.Errorf("Write failed: %v", err)
		}
		if n != int64(len(newContent)) {
			t.Errorf("Write returned wrong length: got %d, want %d", n, len(newContent))
		}

		// Verify write by reading
		data, err = nfs.Read(node, 0, int64(len(newContent)))
		if err != nil {
			t.Errorf("Read after write failed: %v", err)
		}
		if string(data) != string(newContent) {
			t.Errorf("Read after write returned wrong data: got %s, want %s", string(data), string(newContent))
		}

		// Test write with negative offset
		if _, err := nfs.Write(node, -1, newContent); err == nil {
			t.Error("Expected error for negative offset")
		}

		// Test write with nil data
		if _, err := nfs.Write(node, 0, nil); err == nil {
			t.Error("Expected error for nil data")
		}

		// Test write with nil node
		if _, err := nfs.Write(nil, 0, newContent); err == nil {
			t.Error("Expected error for nil node")
		}

		// Test write in read-only mode
		readOnlyNFS, err := New(fs, ExportOptions{ReadOnly: true})
		if err != nil {
			t.Fatalf("Failed to create read-only NFS: %v", err)
		}
		readOnlyNode, err := readOnlyNFS.Lookup("/testfile")
		if err != nil {
			t.Fatalf("Failed to lookup file in read-only mode: %v", err)
		}
		if _, err := readOnlyNFS.Write(readOnlyNode, 0, newContent); err != os.ErrPermission {
			t.Errorf("Expected permission error for write in read-only mode, got: %v", err)
		}
	})

	// Test GetAttr and SetAttr operations
	t.Run("attribute operations", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		nfs, err := New(fs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create test file
		f, err := fs.Create("/attrfile")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		f.Close()

		// Get file node
		node, err := nfs.Lookup("/attrfile")
		if err != nil {
			t.Fatalf("Failed to lookup file: %v", err)
		}

		// Test GetAttr
		attrs, err := nfs.GetAttr(node)
		if err != nil {
			t.Errorf("GetAttr failed: %v", err)
		}
		if attrs == nil {
			t.Error("GetAttr returned nil attributes")
		}

		// Test GetAttr with nil node
		if _, err := nfs.GetAttr(nil); err == nil {
			t.Error("Expected error for GetAttr with nil node")
		}

		// Test GetAttr with non-existent file
		nonExistentNode := &NFSNode{
			FileSystem: fs,
			path:       "/nonexistent",
			attrs:      &NFSAttrs{},
		}
		if _, err := nfs.GetAttr(nonExistentNode); err == nil {
			t.Error("Expected error for GetAttr with non-existent file")
		}

		// Test SetAttr
		newAttrs := &NFSAttrs{
			Mode: 0644,
			Uid:  1000,
			Gid:  1000,
			Size: 0,
		}
		newAttrs.SetMtime(attrs.Mtime())
		newAttrs.SetAtime(attrs.Atime())
		if err := nfs.SetAttr(node, newAttrs); err != nil {
			t.Errorf("SetAttr failed: %v", err)
		}

		// Verify attributes were set
		updatedAttrs, err := nfs.GetAttr(node)
		if err != nil {
			t.Errorf("GetAttr after SetAttr failed: %v", err)
		}
		if updatedAttrs.Mode != newAttrs.Mode {
			t.Errorf("SetAttr didn't update mode: got %o, want %o", updatedAttrs.Mode, newAttrs.Mode)
		}
		if updatedAttrs.Uid != newAttrs.Uid {
			t.Errorf("SetAttr didn't update uid: got %d, want %d", updatedAttrs.Uid, newAttrs.Uid)
		}
		if updatedAttrs.Gid != newAttrs.Gid {
			t.Errorf("SetAttr didn't update gid: got %d, want %d", updatedAttrs.Gid, newAttrs.Gid)
		}

		// Test SetAttr with nil node
		if err := nfs.SetAttr(nil, newAttrs); err == nil {
			t.Error("Expected error for SetAttr with nil node")
		}

		// Test SetAttr with nil attributes
		if err := nfs.SetAttr(node, nil); err == nil {
			t.Error("Expected error for SetAttr with nil attributes")
		}

		// Test SetAttr with non-existent file
		if err := nfs.SetAttr(nonExistentNode, newAttrs); err == nil {
			t.Error("Expected error for SetAttr with non-existent file")
		}
	})

	// Test Create, Remove, and Rename operations
	t.Run("file operations", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		nfs, err := New(fs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create test directory
		if err := fs.Mkdir("/testdir", 0755); err != nil {
			t.Fatalf("Failed to create test directory: %v", err)
		}
		dirNode, err := nfs.Lookup("/testdir")
		if err != nil {
			t.Fatalf("Failed to lookup directory: %v", err)
		}

		// Test Create
		now := time.Now()
		attrs := &NFSAttrs{
			Mode: 0644,
			Uid:  1000,
			Gid:  1000,
			Size: 0,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		// Test successful file creation
		node, err := nfs.Create(dirNode, "testfile", attrs)
		if err != nil {
			t.Errorf("Create failed: %v", err)
		}
		if node == nil {
			t.Error("Create returned nil node")
		}

		// Verify file exists
		if _, err := fs.Stat("/testdir/testfile"); err != nil {
			t.Errorf("Created file not found: %v", err)
		}

		// Test Create with nil directory
		if _, err := nfs.Create(nil, "testfile2", attrs); err == nil {
			t.Error("Expected error for Create with nil directory")
		}

		// Test Create with empty name
		if _, err := nfs.Create(dirNode, "", attrs); err == nil {
			t.Error("Expected error for Create with empty name")
		}

		// Test Create with nil attributes
		if _, err := nfs.Create(dirNode, "testfile3", nil); err == nil {
			t.Error("Expected error for Create with nil attributes")
		}

		// Test Create in read-only mode
		readOnlyNFS, err := New(fs, ExportOptions{ReadOnly: true})
		if err != nil {
			t.Fatalf("Failed to create read-only NFS: %v", err)
		}
		readOnlyDirNode, err := readOnlyNFS.Lookup("/testdir")
		if err != nil {
			t.Fatalf("Failed to lookup directory in read-only mode: %v", err)
		}
		if _, err := readOnlyNFS.Create(readOnlyDirNode, "testfile4", attrs); err != os.ErrPermission {
			t.Errorf("Expected permission error for Create in read-only mode, got: %v", err)
		}

		// Test Remove
		// Test successful file removal
		if err := nfs.Remove(dirNode, "testfile"); err != nil {
			t.Errorf("Remove failed: %v", err)
		}

		// Verify file was removed
		if _, err := fs.Stat("/testdir/testfile"); err == nil {
			t.Error("File still exists after Remove")
		}

		// Test Remove with nil directory
		if err := nfs.Remove(nil, "testfile"); err == nil {
			t.Error("Expected error for Remove with nil directory")
		}

		// Test Remove with empty name
		if err := nfs.Remove(dirNode, ""); err == nil {
			t.Error("Expected error for Remove with empty name")
		}

		// Test Remove non-existent file
		if err := nfs.Remove(dirNode, "nonexistent"); err == nil {
			t.Error("Expected error for Remove of non-existent file")
		}

		// Test Remove in read-only mode
		if err := readOnlyNFS.Remove(readOnlyDirNode, "testfile"); err != os.ErrPermission {
			t.Errorf("Expected permission error for Remove in read-only mode, got: %v", err)
		}

		// Test Rename
		// Create source and target directories
		if err := fs.Mkdir("/srcdir", 0755); err != nil {
			t.Fatalf("Failed to create source directory: %v", err)
		}
		if err := fs.Mkdir("/dstdir", 0755); err != nil {
			t.Fatalf("Failed to create destination directory: %v", err)
		}

		srcDirNode, err := nfs.Lookup("/srcdir")
		if err != nil {
			t.Fatalf("Failed to lookup source directory: %v", err)
		}
		dstDirNode, err := nfs.Lookup("/dstdir")
		if err != nil {
			t.Fatalf("Failed to lookup destination directory: %v", err)
		}

		// Create a test file for renaming
		_, err = nfs.Create(srcDirNode, "renamefile", attrs)
		if err != nil {
			t.Fatalf("Failed to create file for rename test: %v", err)
		}

		// Test successful rename
		if err := nfs.Rename(srcDirNode, "renamefile", dstDirNode, "renamedfile"); err != nil {
			t.Errorf("Rename failed: %v", err)
		}

		// Verify file was renamed
		if _, err := fs.Stat("/dstdir/renamedfile"); err != nil {
			t.Error("Renamed file not found in destination")
		}
		if _, err := fs.Stat("/srcdir/renamefile"); err == nil {
			t.Error("Original file still exists after rename")
		}

		// Test Rename with nil source directory
		if err := nfs.Rename(nil, "file1", dstDirNode, "file2"); err == nil {
			t.Error("Expected error for Rename with nil source directory")
		}

		// Test Rename with nil destination directory
		if err := nfs.Rename(srcDirNode, "file1", nil, "file2"); err == nil {
			t.Error("Expected error for Rename with nil destination directory")
		}

		// Test Rename with empty names
		if err := nfs.Rename(srcDirNode, "", dstDirNode, "file2"); err == nil {
			t.Error("Expected error for Rename with empty source name")
		}
		if err := nfs.Rename(srcDirNode, "file1", dstDirNode, ""); err == nil {
			t.Error("Expected error for Rename with empty destination name")
		}

		// Test Rename in read-only mode
		readOnlySrcDirNode, err := readOnlyNFS.Lookup("/srcdir")
		if err != nil {
			t.Fatalf("Failed to lookup source directory in read-only mode: %v", err)
		}
		readOnlyDstDirNode, err := readOnlyNFS.Lookup("/dstdir")
		if err != nil {
			t.Fatalf("Failed to lookup destination directory in read-only mode: %v", err)
		}
		if err := readOnlyNFS.Rename(readOnlySrcDirNode, "file1", readOnlyDstDirNode, "file2"); err != os.ErrPermission {
			t.Errorf("Expected permission error for Rename in read-only mode, got: %v", err)
		}
	})

	// Test ReadDir operations
	t.Run("readdir operations", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		nfs, err := New(fs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create test directory structure
		if err := fs.Mkdir("/testdir", 0755); err != nil {
			t.Fatalf("Failed to create test directory: %v", err)
		}

		// Create multiple files
		fileNames := []string{"file1", "file2", "file3", ".hidden"}
		for _, name := range fileNames {
			f, err := fs.Create("/testdir/" + name)
			if err != nil {
				t.Fatalf("Failed to create test file %s: %v", name, err)
			}
			f.Close()
		}

		// Create subdirectory
		if err := fs.Mkdir("/testdir/subdir", 0755); err != nil {
			t.Fatalf("Failed to create subdirectory: %v", err)
		}

		// Get directory node
		dirNode, err := nfs.Lookup("/testdir")
		if err != nil {
			t.Fatalf("Failed to lookup directory: %v", err)
		}

		// Test successful ReadDir
		entries, err := nfs.ReadDir(dirNode)
		if err != nil {
			t.Errorf("ReadDir failed: %v", err)
		}

		// Should return all entries including hidden files but excluding . and ..
		expectedCount := len(fileNames) + 1 // +1 for subdir
		if len(entries) != expectedCount {
			t.Errorf("ReadDir returned wrong number of entries: got %d, want %d", len(entries), expectedCount)
		}

		// Verify all expected entries are present
		foundNames := make(map[string]bool)
		for _, entry := range entries {
			foundNames[entry.path[len("/testdir/"):]] = true
		}

		for _, name := range fileNames {
			if !foundNames[name] {
				t.Errorf("ReadDir missing entry: %s", name)
			}
		}
		if !foundNames["subdir"] {
			t.Errorf("ReadDir missing subdirectory entry")
		}

		// Test ReadDir with nil directory
		if _, err := nfs.ReadDir(nil); err == nil {
			t.Error("Expected error for ReadDir with nil directory")
		}

		// Test ReadDir with non-existent directory
		nonExistentDir := &NFSNode{
			FileSystem: fs,
			path:       "/nonexistent",
			attrs: &NFSAttrs{
				Mode: os.ModeDir,
			},
		}
		if _, err := nfs.ReadDir(nonExistentDir); err == nil {
			t.Error("Expected error for ReadDir with non-existent directory")
		}

		// Test ReadDir with file instead of directory
		fileNode, err := nfs.Lookup("/testdir/file1")
		if err != nil {
			t.Fatalf("Failed to lookup test file: %v", err)
		}
		if _, err := nfs.ReadDir(fileNode); err == nil {
			t.Error("Expected error for ReadDir with file node")
		}
	})

	// Test cache operations
	t.Run("cache operations", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		nfs, err := New(fs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create test file
		f, err := fs.Create("/cachefile")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		f.Close()

		// Get file node
		node, err := nfs.Lookup("/cachefile")
		if err != nil {
			t.Fatalf("Failed to lookup file: %v", err)
		}

		// Test attribute caching
		// First GetAttr should cache the attributes
		attrs1, err := nfs.GetAttr(node)
		if err != nil {
			t.Errorf("First GetAttr failed: %v", err)
		}

		// Second GetAttr should return cached attributes
		attrs2, err := nfs.GetAttr(node)
		if err != nil {
			t.Errorf("Second GetAttr failed: %v", err)
		}

		// Attributes should be the same
		if attrs1.Mode != attrs2.Mode || attrs1.Size != attrs2.Size {
			t.Error("Cached attributes don't match original attributes")
		}

		// Modify file to invalidate cache
		now := time.Now()
		newAttrs := &NFSAttrs{
			Mode: 0644,
			Uid:  1000,
			Gid:  1000,
			Size: 0,
		}
		newAttrs.SetMtime(now)
		newAttrs.SetAtime(now)
		if err := nfs.SetAttr(node, newAttrs); err != nil {
			t.Errorf("SetAttr failed: %v", err)
		}

		// GetAttr should now return new attributes
		attrs3, err := nfs.GetAttr(node)
		if err != nil {
			t.Errorf("GetAttr after SetAttr failed: %v", err)
		}
		if attrs3.Mode != newAttrs.Mode || attrs3.Uid != newAttrs.Uid {
			t.Error("Attributes not updated after cache invalidation")
		}

		// Test read buffer caching
		testData := []byte("Hello, Cache World!")
		if _, err := nfs.Write(node, 0, testData); err != nil {
			t.Errorf("Write failed: %v", err)
		}

		// First read should cache the data
		data1, err := nfs.Read(node, 0, int64(len(testData)))
		if err != nil {
			t.Errorf("First read failed: %v", err)
		}
		if string(data1) != string(testData) {
			t.Errorf("First read returned wrong data: got %s, want %s", string(data1), string(testData))
		}

		// Second read should use cache
		data2, err := nfs.Read(node, 0, int64(len(testData)))
		if err != nil {
			t.Errorf("Second read failed: %v", err)
		}
		if string(data2) != string(testData) {
			t.Errorf("Cached read returned wrong data: got %s, want %s", string(data2), string(testData))
		}

		// Write should invalidate read cache
		newData := []byte("Updated Cache Data")
		if _, err := nfs.Write(node, 0, newData); err != nil {
			t.Errorf("Write failed: %v", err)
		}

		// Read after write should return new data
		data3, err := nfs.Read(node, 0, int64(len(newData)))
		if err != nil {
			t.Errorf("Read after write failed: %v", err)
		}
		if string(data3) != string(newData) {
			t.Errorf("Read after cache invalidation returned wrong data: got %s, want %s", string(data3), string(newData))
		}
	})

	// Test Lookup and file handle operations
	t.Run("lookup and filehandle operations", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		nfs, err := New(fs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create test directory structure
		if err := fs.Mkdir("/testdir", 0755); err != nil {
			t.Fatalf("Failed to create test directory: %v", err)
		}
		if err := fs.Mkdir("/testdir/subdir", 0755); err != nil {
			t.Fatalf("Failed to create subdirectory: %v", err)
		}
		f, err := fs.Create("/testdir/testfile")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		f.Close()

		// Test successful Lookup of directory
		dirNode, err := nfs.Lookup("/testdir")
		if err != nil {
			t.Errorf("Lookup directory failed: %v", err)
		}
		if dirNode == nil {
			t.Error("Lookup directory returned nil node")
		}
		if !dirNode.attrs.Mode.IsDir() {
			t.Error("Lookup directory returned non-directory node")
		}

		// Test successful Lookup of file
		fileNode, err := nfs.Lookup("/testdir/testfile")
		if err != nil {
			t.Errorf("Lookup file failed: %v", err)
		}
		if fileNode == nil {
			t.Error("Lookup file returned nil node")
		}
		if fileNode.attrs.Mode.IsDir() {
			t.Error("Lookup file returned directory node")
		}

		// Test Lookup with empty path
		if _, err := nfs.Lookup(""); err == nil {
			t.Error("Expected error for Lookup with empty path")
		}

		// Test Lookup of non-existent file
		if _, err := nfs.Lookup("/nonexistent"); err == nil {
			t.Error("Expected error for Lookup of non-existent file")
		}

		// Test Lookup with invalid path
		if _, err := nfs.Lookup("///invalid///path"); err == nil {
			t.Error("Expected error for Lookup with invalid path")
		}

		// Test Lookup of symlink (if supported)
		if err := fs.Symlink("/testdir/testfile", "/testdir/symlink"); err == nil {
			// Only test if filesystem supports symlinks
			if _, err := nfs.Lookup("/testdir/symlink"); err != nil {
				t.Errorf("Lookup symlink failed: %v", err)
			}
		}

		// Test path traversal
		subdirNode, err := nfs.Lookup("/testdir/subdir")
		if err != nil {
			t.Errorf("Lookup subdirectory failed: %v", err)
		}
		if subdirNode == nil {
			t.Error("Lookup subdirectory returned nil node")
		}
		if !subdirNode.attrs.Mode.IsDir() {
			t.Error("Lookup subdirectory returned non-directory node")
		}

		// Test attribute caching in Lookup
		// First Lookup should cache attributes
		node1, err := nfs.Lookup("/testdir/testfile")
		if err != nil {
			t.Errorf("First Lookup failed: %v", err)
		}

		// Second Lookup should use cached attributes
		node2, err := nfs.Lookup("/testdir/testfile")
		if err != nil {
			t.Errorf("Second Lookup failed: %v", err)
		}

		// Attributes should match
		if node1.attrs.Mode != node2.attrs.Mode || node1.attrs.Size != node2.attrs.Size {
			t.Error("Cached attributes don't match between Lookups")
		}

		// Test cache invalidation
		// First get current attributes
		origAttrs, err := nfs.GetAttr(node1)
		if err != nil {
			t.Errorf("GetAttr failed: %v", err)
		}

		// Modify file attributes with different values
		now := time.Now()
		newAttrs := &NFSAttrs{
			Mode: 0644,
			Uid:  1000,
			Gid:  1000,
			Size: origAttrs.Size,
		}
		newAttrs.SetMtime(now)
		newAttrs.SetAtime(now)

		// Apply new attributes
		if err := nfs.SetAttr(node1, newAttrs); err != nil {
			t.Errorf("SetAttr failed: %v", err)
		}

		// Get fresh attributes directly from filesystem
		info, err := fs.Stat("/testdir/testfile")
		if err != nil {
			t.Errorf("Stat failed: %v", err)
		}

		// Verify the changes were applied
		if info.Mode()&0777 != newAttrs.Mode&0777 {
			t.Errorf("File mode not updated: got %o, want %o", info.Mode()&0777, newAttrs.Mode&0777)
		}

		// Test file handle allocation
		handleFS, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create handle test filesystem: %v", err)
		}
		handleNFS, err := New(handleFS, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create handle test NFS: %v", err)
		}

		// Test sequential handle allocation
		f1, err := handleFS.Create("/file1")
		if err != nil {
			t.Fatalf("Failed to create first test file: %v", err)
		}
		handle1 := handleNFS.fileMap.Allocate(f1)
		if handle1 != 1 {
			t.Errorf("First handle should be 1, got %d", handle1)
		}

		f2, err := handleFS.Create("/file2")
		if err != nil {
			t.Fatalf("Failed to create second test file: %v", err)
		}
		handle2 := handleNFS.fileMap.Allocate(f2)
		if handle2 != 2 {
			t.Errorf("Second handle should be 2, got %d", handle2)
		}

		// Test handle reuse after release
		handleNFS.fileMap.Release(handle1) // Release first handle
		f3, err := handleFS.Create("/file3")
		if err != nil {
			t.Fatalf("Failed to create third test file: %v", err)
		}
		handle3 := handleNFS.fileMap.Allocate(f3)
		if handle3 != 1 {
			t.Errorf("Released handle 1 should be reused, got %d", handle3)
		}

		// Release remaining handles
		handleNFS.fileMap.Release(handle2)
		handleNFS.fileMap.Release(handle3)

		// Test handle allocation after all releases
		f4, err := handleFS.Create("/file4")
		if err != nil {
			t.Fatalf("Failed to create fourth test file: %v", err)
		}
		handle4 := handleNFS.fileMap.Allocate(f4)
		if handle4 != 1 {
			t.Errorf("After all releases, next handle should be 1, got %d", handle4)
		}
		handleNFS.fileMap.Release(handle4)

		// Clean up test files
		handleFS.Remove("/file1")
		handleFS.Remove("/file2")
		handleFS.Remove("/file3")
	})

	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Test ReadDirPlus
	t.Run("readdirplus operations", func(t *testing.T) {
		// Create test directory with files
		if err := fs.Mkdir("/testdir", 0755); err != nil {
			t.Fatalf("Failed to create test directory: %v", err)
		}
		for i := 1; i <= 3; i++ {
			f, err := fs.Create("/testdir/file" + string(rune('0'+i)))
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}
			f.Close()
		}

		// Get directory node
		dirNode, err := nfs.Lookup("/testdir")
		if err != nil {
			t.Fatalf("Failed to lookup directory: %v", err)
		}

		// Test successful ReadDirPlus
		entries, err := nfs.ReadDirPlus(dirNode)
		if err != nil {
			t.Errorf("ReadDirPlus failed: %v", err)
		}
		if len(entries) != 3 {
			t.Errorf("Expected 3 entries, got %d", len(entries))
		}

		// Test ReadDirPlus on non-existent directory
		nonExistentDir := &NFSNode{
			FileSystem: fs,
			path:       "/nonexistent",
			attrs: &NFSAttrs{
				Mode: os.ModeDir,
			},
		}
		if _, err := nfs.ReadDirPlus(nonExistentDir); err == nil {
			t.Error("Expected error reading non-existent directory")
		}

		// Test ReadDirPlus with nil directory
		if _, err := nfs.ReadDirPlus(nil); err == nil {
			t.Error("Expected error with nil directory")
		}

		// Test ReadDirPlus with invalid file type
		f, err := fs.Create("/testfile")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		f.Close()
		fileNode, err := nfs.Lookup("/testfile")
		if err != nil {
			t.Fatalf("Failed to lookup file: %v", err)
		}
		if _, err := nfs.ReadDirPlus(fileNode); err == nil {
			t.Error("Expected error reading directory entries from file")
		}
	})

	// Test Export/Unexport
	t.Run("export operations", func(t *testing.T) {
		// Test Export with empty path
		if err := nfs.Export("", 0); err == nil {
			t.Error("Expected error with empty mount path")
		}

		// Test Export with valid path but random port
		err = nfs.Export("/mnt/test", 0)
		if err != nil {
			t.Errorf("Export failed: %v", err)
		}

		// Test Unexport
		if err := nfs.Unexport(); err != nil {
			t.Errorf("Unexport failed: %v", err)
		}
	})

	// Test error mapping
	t.Run("error mapping", func(t *testing.T) {
		// Test nil error
		if status := mapError(nil); status != NFS_OK {
			t.Errorf("Expected NFS_OK for nil error, got %d", status)
		}

		// Test not exist error
		if status := mapError(os.ErrNotExist); status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT for ErrNotExist, got %d", status)
		}

		// Test permission error
		if status := mapError(os.ErrPermission); status != NFSERR_PERM {
			t.Errorf("Expected NFSERR_PERM for ErrPermission, got %d", status)
		}

		// Test file exists error
		if status := mapError(os.ErrExist); status != NFSERR_EXIST {
			t.Errorf("Expected NFSERR_EXIST for ErrExist, got %d", status)
		}

		// Test invalid argument error
		if status := mapError(os.ErrInvalid); status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for ErrInvalid, got %d", status)
		}

		// Test other error
		if status := mapError(os.ErrClosed); status != NFSERR_IO {
			t.Errorf("Expected NFSERR_IO for other error, got %d", status)
		}
	})

	// Test file attribute conversion
	t.Run("file attribute conversion", func(t *testing.T) {
		// Create test file with specific attributes
		f, err := fs.Create("/attrtest")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		f.Close()

		// Get file info
		info, err := fs.Stat("/attrtest")
		if err != nil {
			t.Fatalf("Failed to stat file: %v", err)
		}

		// Convert to NFS attributes
		attrs := toFileAttribute(info)

		// Verify conversion
		if attrs.Type != uint32(info.Mode()>>16) {
			t.Errorf("Wrong file type: got %d", attrs.Type)
		}
		if attrs.Mode != uint32(info.Mode()&0xFFFF) {
			t.Errorf("Wrong file mode: got %d", attrs.Mode)
		}
		if attrs.Size != uint64(info.Size()) {
			t.Errorf("Wrong file size: got %d", attrs.Size)
		}

		expectedTime := uint32(info.ModTime().Unix())
		if attrs.Mtime != expectedTime {
			t.Errorf("Wrong mtime: got %d, want %d", attrs.Mtime, expectedTime)
		}
		if attrs.Atime != expectedTime {
			t.Errorf("Wrong atime: got %d, want %d", attrs.Atime, expectedTime)
		}
		if attrs.Ctime != expectedTime {
			t.Errorf("Wrong ctime: got %d, want %d", attrs.Ctime, expectedTime)
		}
	})

	// Test helper functions
	t.Run("helper functions", func(t *testing.T) {
		// Test min function
		if min(5, 3) != 3 {
			t.Error("min(5, 3) should return 3")
		}
		if min(2, 7) != 2 {
			t.Error("min(2, 7) should return 2")
		}
		if min(4, 4) != 4 {
			t.Error("min(4, 4) should return 4")
		}
	})

	// Test path traversal security
	t.Run("path traversal security", func(t *testing.T) {
		// Test sanitizePath directly
		t.Run("sanitizePath function", func(t *testing.T) {
			// Test valid paths
			validTests := []struct {
				base string
				name string
				want string
			}{
				{"/export", "file.txt", "/export/file.txt"},
				{"/export/dir", "subfile.txt", "/export/dir/subfile.txt"},
				{"/mnt", "data", "/mnt/data"},
			}

			for _, tt := range validTests {
				got, err := sanitizePath(tt.base, tt.name)
				if err != nil {
					t.Errorf("sanitizePath(%q, %q) unexpected error: %v", tt.base, tt.name, err)
				}
				if got != tt.want {
					t.Errorf("sanitizePath(%q, %q) = %q, want %q", tt.base, tt.name, got, tt.want)
				}
			}

			// Test path traversal attacks
			traversalTests := []struct {
				base string
				name string
			}{
				{"/export", "../etc/passwd"},
				{"/export", "../../etc/passwd"},
				{"/export/dir", "../../../etc/passwd"},
				{"/export", ".."},
				{"/export", "."},
				{"/export", "file/../../../etc/passwd"},
				{"/export", "file/../../etc/passwd"},
				{"/export", "foo/../bar"},
			}

			for _, tt := range traversalTests {
				got, err := sanitizePath(tt.base, tt.name)
				if err == nil {
					t.Errorf("sanitizePath(%q, %q) expected error for path traversal, got path: %q", tt.base, tt.name, got)
				}
			}

			// Test invalid paths with separators
			separatorTests := []string{
				"file/subfile",
				"dir/file.txt",
				"../file",
				"./file",
				"file\\subfile",
				"dir\\file.txt",
			}

			for _, name := range separatorTests {
				_, err := sanitizePath("/export", name)
				if err == nil {
					t.Errorf("sanitizePath(/export, %q) expected error for path with separators", name)
				}
			}

			// Test empty name
			if _, err := sanitizePath("/export", ""); err == nil {
				t.Error("sanitizePath with empty name should return error")
			}
		})

		// Test Create operation against path traversal
		t.Run("Create path traversal protection", func(t *testing.T) {
			fs, err := memfs.NewFS()
			if err != nil {
				t.Fatalf("Failed to create memfs: %v", err)
			}

			nfs, err := New(fs, ExportOptions{})
			if err != nil {
				t.Fatalf("Failed to create NFS: %v", err)
			}

			// Create test directories
			if err := fs.Mkdir("/export", 0755); err != nil {
				t.Fatalf("Failed to create export directory: %v", err)
			}
			if err := fs.Mkdir("/secret", 0755); err != nil {
				t.Fatalf("Failed to create secret directory: %v", err)
			}

			dirNode, err := nfs.Lookup("/export")
			if err != nil {
				t.Fatalf("Failed to lookup export directory: %v", err)
			}

			now := time.Now()
			attrs := &NFSAttrs{
				Mode: 0644,
				Uid:  1000,
				Gid:  1000,
				Size: 0,
			}
			attrs.SetMtime(now)
			attrs.SetAtime(now)

			// Try to create file with path traversal
			maliciousNames := []string{
				"../secret/evil.txt",
				"../../root/evil.txt",
				"..",
				".",
				"file/../../../etc/passwd",
			}

			for _, name := range maliciousNames {
				_, err := nfs.Create(dirNode, name, attrs)
				if err == nil {
					t.Errorf("Create with malicious name %q should have failed", name)
					// Clean up if it somehow succeeded
					nfs.Remove(dirNode, name)
				}
			}

			// Verify no files were created outside export
			secretNode, err := nfs.Lookup("/secret")
			if err != nil {
				t.Fatalf("Failed to lookup secret directory: %v", err)
			}
			entries, err := nfs.ReadDir(secretNode)
			if err != nil {
				t.Fatalf("Failed to read secret directory: %v", err)
			}
			if len(entries) > 0 {
				t.Errorf("Files were created in secret directory: %v", entries)
			}
		})

		// Test Remove operation against path traversal
		t.Run("Remove path traversal protection", func(t *testing.T) {
			fs, err := memfs.NewFS()
			if err != nil {
				t.Fatalf("Failed to create memfs: %v", err)
			}

			nfs, err := New(fs, ExportOptions{})
			if err != nil {
				t.Fatalf("Failed to create NFS: %v", err)
			}

			// Create test directories and files
			if err := fs.Mkdir("/export", 0755); err != nil {
				t.Fatalf("Failed to create export directory: %v", err)
			}
			if err := fs.Mkdir("/secret", 0755); err != nil {
				t.Fatalf("Failed to create secret directory: %v", err)
			}
			f, err := fs.Create("/secret/important.txt")
			if err != nil {
				t.Fatalf("Failed to create secret file: %v", err)
			}
			f.Close()

			dirNode, err := nfs.Lookup("/export")
			if err != nil {
				t.Fatalf("Failed to lookup export directory: %v", err)
			}

			// Try to remove file with path traversal
			maliciousNames := []string{
				"../secret/important.txt",
				"../../secret/important.txt",
				"..",
			}

			for _, name := range maliciousNames {
				err := nfs.Remove(dirNode, name)
				if err == nil {
					t.Errorf("Remove with malicious name %q should have failed", name)
				}
			}

			// Verify secret file still exists
			if _, err := fs.Stat("/secret/important.txt"); err != nil {
				t.Error("Secret file was removed by path traversal attack")
			}
		})

		// Test Rename operation against path traversal
		t.Run("Rename path traversal protection", func(t *testing.T) {
			fs, err := memfs.NewFS()
			if err != nil {
				t.Fatalf("Failed to create memfs: %v", err)
			}

			nfs, err := New(fs, ExportOptions{})
			if err != nil {
				t.Fatalf("Failed to create NFS: %v", err)
			}

			// Create test directories and files
			if err := fs.Mkdir("/export", 0755); err != nil {
				t.Fatalf("Failed to create export directory: %v", err)
			}
			if err := fs.Mkdir("/secret", 0755); err != nil {
				t.Fatalf("Failed to create secret directory: %v", err)
			}
			f, err := fs.Create("/export/testfile.txt")
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}
			f.Close()

			dirNode, err := nfs.Lookup("/export")
			if err != nil {
				t.Fatalf("Failed to lookup export directory: %v", err)
			}

			// Try to rename with path traversal in old name
			err = nfs.Rename(dirNode, "../secret/important.txt", dirNode, "stolen.txt")
			if err == nil {
				t.Error("Rename with traversal in old name should have failed")
			}

			// Try to rename with path traversal in new name
			err = nfs.Rename(dirNode, "testfile.txt", dirNode, "../secret/evil.txt")
			if err == nil {
				t.Error("Rename with traversal in new name should have failed")
			}

			// Verify no files were created in secret directory
			secretNode, err := nfs.Lookup("/secret")
			if err != nil {
				t.Fatalf("Failed to lookup secret directory: %v", err)
			}
			entries, err := nfs.ReadDir(secretNode)
			if err != nil {
				t.Fatalf("Failed to read secret directory: %v", err)
			}
			if len(entries) > 0 {
				t.Errorf("Files were created in secret directory: %v", entries)
			}

			// Verify original file still exists
			if _, err := fs.Stat("/export/testfile.txt"); err != nil {
				t.Error("Original file was affected by failed rename")
			}
		})
	})
}
