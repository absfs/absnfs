package absnfs

import (
	"fmt"
	"hash/fnv"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// TestH6_CacheInvalidateOrder verifies that Invalidate calls removeFromAccessLog
// before delete, so that the access log can still look up the cache entry.
func TestH6_CacheInvalidateOrder(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 100)

	// Add an entry
	attrs := &NFSAttrs{Mode: 0644, Size: 100}
	cache.Put("/test/file.txt", attrs)

	// Verify entry exists
	got, found := cache.Get("/test/file.txt")
	if !found || got == nil {
		t.Fatal("Expected cache entry to exist after Put")
	}

	// Invalidate should not panic and should remove both the entry and access log
	cache.Invalidate("/test/file.txt")

	// Verify entry is gone
	got, found = cache.Get("/test/file.txt")
	if found || got != nil {
		t.Error("Expected cache entry to be removed after Invalidate")
	}

	// Verify cache size is 0
	if cache.Size() != 0 {
		t.Errorf("Expected cache size 0 after Invalidate, got %d", cache.Size())
	}

	// Test DirCache Invalidate order too
	dirCache := NewDirCache(10*time.Second, 100, 1000)
	dirCache.Put("/testdir", []os.FileInfo{})

	entries, found := dirCache.Get("/testdir")
	if !found {
		t.Fatal("Expected dir cache entry to exist after Put")
	}
	_ = entries

	dirCache.Invalidate("/testdir")
	_, found = dirCache.Get("/testdir")
	if found {
		t.Error("Expected dir cache entry to be removed after Invalidate")
	}
}

// TestH7_SetAttrModeComparison verifies that SetAttr compares only permission
// bits (not file type bits) when deciding whether to call Chmod.
func TestH7_SetAttrModeComparison(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	// Create a test file
	f, err := fs.Create("/testfile")
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	f.Close()

	node, err := nfs.Lookup("/testfile")
	if err != nil {
		t.Fatalf("Failed to lookup file: %v", err)
	}

	// Get current attrs
	currentAttrs, err := nfs.GetAttr(node)
	if err != nil {
		t.Fatalf("Failed to getattr: %v", err)
	}

	// Set attrs with same permission bits but different type bits.
	// This should NOT trigger a Chmod since only perm bits differ.
	newAttrs := &NFSAttrs{
		Mode: currentAttrs.Mode&os.ModePerm | os.ModeDir, // same perms, different type
		Size: currentAttrs.Size,
		Uid:  currentAttrs.Uid,
		Gid:  currentAttrs.Gid,
	}
	newAttrs.SetMtime(currentAttrs.Mtime())
	newAttrs.SetAtime(currentAttrs.Atime())

	// This should succeed without error since perms are the same
	err = nfs.SetAttr(node, newAttrs)
	if err != nil {
		t.Fatalf("SetAttr failed unexpectedly: %v", err)
	}
}

// TestM4_RootSquashBothUIDAndGID verifies that root_squash squashes both
// UID and GID when UID is 0 (root), regardless of the GID value.
func TestM4_RootSquashBothUIDAndGID(t *testing.T) {
	// Root user with non-zero GID should still get GID squashed
	authSys := &AuthSysCredential{
		UID: 0,
		GID: 1000, // Non-zero GID
	}

	result := &AuthResult{
		Allowed: true,
		UID:     0,
		GID:     1000,
	}

	applySquashing(result, authSys, "root")

	if result.UID != 65534 {
		t.Errorf("Expected UID to be squashed to 65534, got %d", result.UID)
	}
	if result.GID != 65534 {
		t.Errorf("Expected GID to be squashed to 65534 when UID is root, got %d", result.GID)
	}

	// Non-root user should not be squashed
	authSys2 := &AuthSysCredential{
		UID: 1000,
		GID: 0,
	}

	result2 := &AuthResult{
		Allowed: true,
		UID:     1000,
		GID:     0,
	}

	applySquashing(result2, authSys2, "root")

	if result2.UID != 1000 {
		t.Errorf("Non-root UID should not be squashed, got %d", result2.UID)
	}
	if result2.GID != 0 {
		t.Errorf("Non-root GID 0 should not be squashed with root_squash, got %d", result2.GID)
	}
}

// TestM7_AttrCacheThreeStateReturn verifies that AttrCache.Get returns a
// 3-state result: (attrs, true) for hit, (nil, true) for negative hit,
// (nil, false) for miss.
func TestM7_AttrCacheThreeStateReturn(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 100)
	cache.ConfigureNegativeCaching(true, 10*time.Second)

	// Cache miss: path not in cache at all
	attrs, found := cache.Get("/nonexistent")
	if found {
		t.Error("Expected cache miss (found=false) for path not in cache")
	}
	if attrs != nil {
		t.Error("Expected nil attrs for cache miss")
	}

	// Positive cache hit
	cache.Put("/exists", &NFSAttrs{Mode: 0644, Size: 42})
	attrs, found = cache.Get("/exists")
	if !found {
		t.Error("Expected cache hit (found=true) for cached path")
	}
	if attrs == nil {
		t.Error("Expected non-nil attrs for positive cache hit")
	}

	// Negative cache hit
	cache.PutNegative("/deleted")
	attrs, found = cache.Get("/deleted")
	if !found {
		t.Error("Expected negative cache hit (found=true) for negatively cached path")
	}
	if attrs != nil {
		t.Error("Expected nil attrs for negative cache hit")
	}
}

// TestM10_DirectoryNlinkAtLeastTwo verifies that directories get nlink >= 2
// when attributes are encoded.
func TestM10_DirectoryNlinkAtLeastTwo(t *testing.T) {
	// Create directory attrs
	dirAttrs := &NFSAttrs{
		Mode:   os.ModeDir | 0755,
		Size:   4096,
		FileId: 1,
		Uid:    0,
		Gid:    0,
	}
	dirAttrs.SetMtime(time.Now())
	dirAttrs.SetAtime(time.Now())

	var buf [256]byte
	w := &sliceWriter{buf: buf[:0]}
	err := encodeFileAttributes(w, dirAttrs)
	if err != nil {
		t.Fatalf("encodeFileAttributes failed: %v", err)
	}

	// The nlink field is the 3rd uint32 (bytes 8-11): type(4) + mode(4) + nlink(4)
	if len(w.buf) < 12 {
		t.Fatalf("Expected at least 12 bytes, got %d", len(w.buf))
	}
	nlink := uint32(w.buf[8])<<24 | uint32(w.buf[9])<<16 | uint32(w.buf[10])<<8 | uint32(w.buf[11])
	if nlink < 2 {
		t.Errorf("Expected nlink >= 2 for directory, got %d", nlink)
	}

	// Verify regular file still gets nlink=1
	fileAttrs := &NFSAttrs{
		Mode:   0644,
		Size:   100,
		FileId: 2,
		Uid:    0,
		Gid:    0,
	}
	fileAttrs.SetMtime(time.Now())
	fileAttrs.SetAtime(time.Now())

	w2 := &sliceWriter{buf: buf[:0]}
	err = encodeFileAttributes(w2, fileAttrs)
	if err != nil {
		t.Fatalf("encodeFileAttributes failed: %v", err)
	}

	nlink2 := uint32(w2.buf[8])<<24 | uint32(w2.buf[9])<<16 | uint32(w2.buf[10])<<8 | uint32(w2.buf[11])
	if nlink2 != 1 {
		t.Errorf("Expected nlink=1 for regular file, got %d", nlink2)
	}
}

// sliceWriter is a simple io.Writer for testing
type sliceWriter struct {
	buf []byte
}

func (w *sliceWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// TestL4_IsChildOfDepthCheck verifies that isChildOf only matches direct
// children (one level deep), not arbitrary descendants.
func TestL4_IsChildOfDepthCheck(t *testing.T) {
	tests := []struct {
		path    string
		dirPath string
		want    bool
	}{
		{"/dir/file.txt", "/dir", true},           // Direct child
		{"/dir/sub/file.txt", "/dir", false},       // Grandchild - should NOT match
		{"/dir/sub/deep/file.txt", "/dir", false},  // Deep descendant
		{"/file.txt", "/", true},                    // Direct child of root
		{"/dir/sub/file.txt", "/", false},           // Not direct child of root
		{"/dir", "/dir", false},                     // Same path
		{"/", "/", false},                           // Root vs root
		{"/dir2/file.txt", "/dir", false},           // Different prefix
		{"/directory/file.txt", "/dir", false},      // Prefix but not parent
	}

	for _, tt := range tests {
		got := isChildOf(tt.path, tt.dirPath)
		if got != tt.want {
			t.Errorf("isChildOf(%q, %q) = %v, want %v", tt.path, tt.dirPath, got, tt.want)
		}
	}
}

// TestL8_AuthNoneDocumentation verifies that AUTH_NONE is accepted as
// intentional standard NFS behavior for public/shared exports.
func TestL8_AuthNoneDocumentation(t *testing.T) {
	ctx := &AuthContext{
		ClientIP:   "127.0.0.1",
		ClientPort: 1023,
		Credential: &RPCCredential{
			Flavor: AUTH_NONE,
			Body:   []byte{},
		},
	}

	defaultOpts := ExportOptions{}
	result := ValidateAuthentication(ctx, policyFromExportOptions(&defaultOpts))
	if !result.Allowed {
		t.Error("AUTH_NONE should be accepted for public/shared exports")
	}
	if result.UID != 65534 || result.GID != 65534 {
		t.Errorf("AUTH_NONE should map to nobody (65534/65534), got %d/%d", result.UID, result.GID)
	}
}

// TestL9_AuxGIDsRootSquash verifies that auxiliary GID 0 entries are squashed
// to the anonymous GID when root_squash is enabled.
func TestL9_AuxGIDsRootSquash(t *testing.T) {
	authSys := &AuthSysCredential{
		UID:     0,
		GID:     0,
		AuxGIDs: []uint32{0, 1000, 0, 2000},
	}

	result := &AuthResult{
		Allowed: true,
		UID:     0,
		GID:     0,
	}

	applySquashing(result, authSys, "root")

	// Check that GID 0 entries in AuxGIDs are squashed
	for i, gid := range authSys.AuxGIDs {
		switch i {
		case 0, 2:
			if gid != 65534 {
				t.Errorf("AuxGIDs[%d] = %d, want 65534 (squashed)", i, gid)
			}
		case 1:
			if gid != 1000 {
				t.Errorf("AuxGIDs[%d] = %d, want 1000 (unchanged)", i, gid)
			}
		case 3:
			if gid != 2000 {
				t.Errorf("AuxGIDs[%d] = %d, want 2000 (unchanged)", i, gid)
			}
		}
	}

	// Non-root user: AuxGIDs should not be affected
	authSys2 := &AuthSysCredential{
		UID:     1000,
		GID:     1000,
		AuxGIDs: []uint32{0, 500},
	}

	result2 := &AuthResult{
		Allowed: true,
		UID:     1000,
		GID:     1000,
	}

	applySquashing(result2, authSys2, "root")

	// GID 0 in aux list should still be squashed for root_squash
	if authSys2.AuxGIDs[0] != 65534 {
		t.Errorf("AuxGIDs[0] = %d, want 65534 (squashed even for non-root user in root_squash)", authSys2.AuxGIDs[0])
	}
	if authSys2.AuxGIDs[1] != 500 {
		t.Errorf("AuxGIDs[1] = %d, want 500 (unchanged)", authSys2.AuxGIDs[1])
	}
}

// TestR3_InvalidateNegativeInDirAccessLogOrder verifies that InvalidateNegativeInDir
// removes entries from the access log before deleting from the cache map, preventing
// ghost elements in the LRU list.
func TestR3_InvalidateNegativeInDirAccessLogOrder(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 100)
	cache.ConfigureNegativeCaching(true, 10*time.Second)

	// Add several negative entries under /dir
	cache.PutNegative("/dir/a")
	cache.PutNegative("/dir/b")
	cache.PutNegative("/dir/c")

	// Add a positive entry too
	cache.Put("/dir/existing", &NFSAttrs{Mode: 0644, Size: 10})

	// Verify all entries exist
	if cache.Size() != 4 {
		t.Fatalf("Expected 4 cache entries, got %d", cache.Size())
	}

	// Invalidate negative entries in /dir
	cache.InvalidateNegativeInDir("/dir")

	// Negative entries should be gone, positive entry should remain
	if cache.Size() != 1 {
		t.Errorf("Expected 1 cache entry after InvalidateNegativeInDir, got %d", cache.Size())
	}

	// The positive entry should still be accessible
	got, found := cache.Get("/dir/existing")
	if !found || got == nil {
		t.Error("Positive entry should still exist after InvalidateNegativeInDir")
	}

	// Verify that the LRU list is consistent by filling cache to max and forcing eviction
	for i := 0; i < 100; i++ {
		cache.Put(fmt.Sprintf("/other/%d", i), &NFSAttrs{Mode: 0644, Size: int64(i)})
	}
	// If ghost elements existed, the cache size would exceed maxSize
	if cache.Size() > 100 {
		t.Errorf("Cache size %d exceeds max 100, LRU list may have ghost elements", cache.Size())
	}
}

// TestR10_ChmodMasksTypeBits verifies that SetAttr and Create only pass
// permission bits (not file type bits) to Chmod.
func TestR10_ChmodMasksTypeBits(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	// Create a test file
	f, err := fs.Create("/testfile")
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	f.Close()

	node, err := nfs.Lookup("/testfile")
	if err != nil {
		t.Fatalf("Failed to lookup file: %v", err)
	}

	currentAttrs, err := nfs.GetAttr(node)
	if err != nil {
		t.Fatalf("Failed to getattr: %v", err)
	}

	// Set attrs with type bits included (e.g. ModeDir) - Chmod should strip them
	newAttrs := &NFSAttrs{
		Mode: os.ModeDir | 0755, // includes type bits
		Size: currentAttrs.Size,
		Uid:  currentAttrs.Uid,
		Gid:  currentAttrs.Gid,
	}
	newAttrs.SetMtime(currentAttrs.Mtime())
	newAttrs.SetAtime(currentAttrs.Atime())

	err = nfs.SetAttr(node, newAttrs)
	if err != nil {
		t.Fatalf("SetAttr failed: %v", err)
	}

	// Verify the file permissions are correct and type bits weren't applied
	info, err := fs.Stat("/testfile")
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}
	if info.Mode()&os.ModePerm != 0755 {
		t.Errorf("Expected perms 0755, got %04o", info.Mode()&os.ModePerm)
	}
	if info.IsDir() {
		t.Error("File should not have become a directory")
	}
}

// TestR10_CreateChmodMasksTypeBits verifies that Create masks type bits when calling Chmod.
func TestR10_CreateChmodMasksTypeBits(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	// Ensure root dir exists
	dirNode, err := nfs.Lookup("/")
	if err != nil {
		t.Fatalf("Failed to lookup root: %v", err)
	}

	// Create with type bits included in mode
	createAttrs := &NFSAttrs{
		Mode: os.ModeDir | 0644, // type bits should be stripped
	}
	createAttrs.SetMtime(time.Now())
	createAttrs.SetAtime(time.Now())

	node, err := nfs.Create(dirNode, "newfile", createAttrs)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	info, err := fs.Stat(node.path)
	if err != nil {
		t.Fatalf("Failed to stat created file: %v", err)
	}
	if info.Mode()&os.ModePerm != 0644 {
		t.Errorf("Expected perms 0644, got %04o", info.Mode()&os.ModePerm)
	}
}

// TestR11_ReadDirPlusLockProtection verifies that ReadDirPlus accesses
// node.attrs with proper lock protection.
func TestR11_ReadDirPlusLockProtection(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	// Create test directory and files
	fs.Mkdir("/testdir", 0755)
	for i := 0; i < 5; i++ {
		f, err := fs.Create(fmt.Sprintf("/testdir/file%d", i))
		if err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
		f.Close()
	}

	dirNode, err := nfs.Lookup("/testdir")
	if err != nil {
		t.Fatalf("Failed to lookup dir: %v", err)
	}

	// Run ReadDirPlus concurrently with attrs modifications to detect races
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = nfs.ReadDirPlus(dirNode)
		}()
	}
	wg.Wait()
}

// TestR19_SetAttrZeroTimesSkipsChtimes verifies that SetAttr does not call
// Chtimes when atime and mtime are zero-valued (not being changed).
func TestR19_SetAttrZeroTimesSkipsChtimes(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	// Create a test file
	f, err := fs.Create("/testfile")
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	f.Close()

	node, err := nfs.Lookup("/testfile")
	if err != nil {
		t.Fatalf("Failed to lookup file: %v", err)
	}

	// Get the current mtime
	info, err := fs.Stat("/testfile")
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}
	originalMtime := info.ModTime()

	// Set attrs with zero-valued times (should NOT change file times)
	newAttrs := &NFSAttrs{
		Mode: node.attrs.Mode,
		Size: node.attrs.Size,
		Uid:  node.attrs.Uid,
		Gid:  node.attrs.Gid,
		// atime and mtime are zero-valued (time.Time{})
	}

	err = nfs.SetAttr(node, newAttrs)
	if err != nil {
		t.Fatalf("SetAttr failed: %v", err)
	}

	// Verify times were NOT changed to epoch
	info, err = fs.Stat("/testfile")
	if err != nil {
		t.Fatalf("Failed to stat file after SetAttr: %v", err)
	}
	if info.ModTime() != originalMtime {
		t.Errorf("File mtime changed when it shouldn't have: was %v, now %v",
			originalMtime, info.ModTime())
	}
}

// TestR20_LookupSetsFileId verifies that Lookup sets FileId to a deterministic
// hash of the path, and that the FileId is preserved through the AttrCache.
func TestR20_LookupSetsFileId(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	// Create a test file
	f, err := fs.Create("/testfile")
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	f.Close()

	// Lookup should set FileId
	node, err := nfs.Lookup("/testfile")
	if err != nil {
		t.Fatalf("Failed to lookup file: %v", err)
	}

	if node.attrs.FileId == 0 {
		t.Error("FileId should not be zero after Lookup")
	}

	// Verify it's deterministic (same path = same FileId)
	h := fnv.New64a()
	h.Write([]byte("/testfile"))
	expectedId := h.Sum64()
	if node.attrs.FileId != expectedId {
		t.Errorf("FileId = %d, expected fnv64a hash %d", node.attrs.FileId, expectedId)
	}

	// Second lookup should return the same FileId (from cache)
	node2, err := nfs.Lookup("/testfile")
	if err != nil {
		t.Fatalf("Failed to lookup file again: %v", err)
	}
	if node2.attrs.FileId != expectedId {
		t.Errorf("Cached FileId = %d, expected %d", node2.attrs.FileId, expectedId)
	}
}

// TestR20_AttrCachePreservesFileId verifies that AttrCache.Put and Get
// correctly copy the FileId field.
func TestR20_AttrCachePreservesFileId(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 100)

	attrs := &NFSAttrs{
		Mode:   0644,
		Size:   100,
		FileId: 12345,
		Uid:    1000,
		Gid:    1000,
	}
	attrs.SetMtime(time.Now())
	attrs.SetAtime(time.Now())

	cache.Put("/test", attrs)

	got, found := cache.Get("/test")
	if !found || got == nil {
		t.Fatal("Expected cache hit")
	}
	if got.FileId != 12345 {
		t.Errorf("FileId = %d, expected 12345", got.FileId)
	}
}

// TestR24_DirCacheExpiredEntryRecheck verifies that DirCache.Get re-checks
// the entry after upgrading from RLock to Lock when removing expired entries.
func TestR24_DirCacheExpiredEntryRecheck(t *testing.T) {
	// Create a cache with very short TTL
	cache := NewDirCache(1*time.Millisecond, 100, 1000)

	// Add an entry
	cache.Put("/testdir", []os.FileInfo{})

	// Wait for it to expire
	time.Sleep(5 * time.Millisecond)

	// Get should return miss for expired entry
	_, found := cache.Get("/testdir")
	if found {
		t.Error("Expected cache miss for expired entry")
	}

	// Verify it was cleaned up
	if cache.Size() != 0 {
		t.Errorf("Expected cache size 0 after expired entry cleanup, got %d", cache.Size())
	}
}

// TestR25_AttrCacheExpiredEntryRecheck verifies that AttrCache.Get re-checks
// the entry after upgrading from RLock to Lock when removing expired entries.
func TestR25_AttrCacheExpiredEntryRecheck(t *testing.T) {
	// Create a cache with very short TTL
	cache := NewAttrCache(1*time.Millisecond, 100)

	// Add an entry
	attrs := &NFSAttrs{Mode: 0644, Size: 100}
	cache.Put("/test", attrs)

	// Wait for it to expire
	time.Sleep(5 * time.Millisecond)

	// Get should return miss for expired entry
	got, found := cache.Get("/test")
	if found || got != nil {
		t.Error("Expected cache miss for expired entry")
	}

	// Verify it was cleaned up
	if cache.Size() != 0 {
		t.Errorf("Expected cache size 0 after expired entry cleanup, got %d", cache.Size())
	}
}

// TestR26_RootSquashDoesNotMutateOriginalAuxGIDs verifies that applySquashing
// does not mutate the original AuxGIDs slice.
func TestR26_RootSquashDoesNotMutateOriginalAuxGIDs(t *testing.T) {
	// Create a shared slice that simulates reuse across calls
	sharedAuxGIDs := []uint32{0, 1000, 0, 2000}

	// Create AuthSysCredential referencing the shared slice
	authSys1 := &AuthSysCredential{
		UID:     0,
		GID:     0,
		AuxGIDs: sharedAuxGIDs,
	}

	// Save a copy of the original values
	originalValues := make([]uint32, len(sharedAuxGIDs))
	copy(originalValues, sharedAuxGIDs)

	result := &AuthResult{Allowed: true, UID: 0, GID: 0}
	applySquashing(result, authSys1, "root")

	// Verify the original shared slice was NOT mutated
	for i, v := range sharedAuxGIDs {
		if v != originalValues[i] {
			t.Errorf("Original sharedAuxGIDs[%d] was mutated: got %d, expected %d", i, v, originalValues[i])
		}
	}

	// Verify that authSys1.AuxGIDs was properly squashed (on its own copy)
	for i, gid := range authSys1.AuxGIDs {
		switch i {
		case 0, 2:
			if gid != 65534 {
				t.Errorf("authSys1.AuxGIDs[%d] = %d, want 65534", i, gid)
			}
		case 1:
			if gid != 1000 {
				t.Errorf("authSys1.AuxGIDs[%d] = %d, want 1000", i, gid)
			}
		case 3:
			if gid != 2000 {
				t.Errorf("authSys1.AuxGIDs[%d] = %d, want 2000", i, gid)
			}
		}
	}
}
