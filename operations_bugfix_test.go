package absnfs

import (
	"os"
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

	result := ValidateAuthentication(ctx, ExportOptions{})
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
