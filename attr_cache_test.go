package absnfs

import (
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func TestAttrCacheConfiguration(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Test cases for different attr cache configurations
	testCases := []struct {
		name            string
		cacheTimeout    time.Duration
		cacheSize       int
		expectedTimeout time.Duration
		expectedSize    int
	}{
		{
			name:            "Default values",
			cacheTimeout:    0,
			cacheSize:       0,
			expectedTimeout: 5 * time.Second,
			expectedSize:    10000,
		},
		{
			name:            "Custom timeout",
			cacheTimeout:    30 * time.Second,
			cacheSize:       0,
			expectedTimeout: 30 * time.Second,
			expectedSize:    10000,
		},
		{
			name:            "Custom size",
			cacheTimeout:    0,
			cacheSize:       5000,
			expectedTimeout: 5 * time.Second,
			expectedSize:    5000,
		},
		{
			name:            "Custom timeout and size",
			cacheTimeout:    15 * time.Second,
			cacheSize:       2000,
			expectedTimeout: 15 * time.Second,
			expectedSize:    2000,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create server with the specified attr cache configuration
			options := ExportOptions{
				AttrCacheTimeout: tc.cacheTimeout,
				AttrCacheSize:    tc.cacheSize,
			}
			server, err := New(fs, options)
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}

			// Verify options were set correctly in the server options
			if server.options.AttrCacheTimeout != tc.expectedTimeout {
				t.Errorf("AttrCacheTimeout not set correctly: got %v, want %v",
					server.options.AttrCacheTimeout, tc.expectedTimeout)
			}
			if server.options.AttrCacheSize != tc.expectedSize {
				t.Errorf("AttrCacheSize not set correctly: got %d, want %d",
					server.options.AttrCacheSize, tc.expectedSize)
			}

			// Verify the attribute cache was created with the correct values
			if server.attrCache.MaxSize() != tc.expectedSize {
				t.Errorf("AttrCache MaxSize not set correctly: got %d, want %d",
					server.attrCache.MaxSize(), tc.expectedSize)
			}
		})
	}
}

func TestAttrCacheSizeLimit(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create some test files
	numFiles := 100
	for i := 0; i < numFiles; i++ {
		filePath := "/file" + string(rune('0'+i%10)) + string(rune('0'+(i/10)%10))
		f, err := fs.Create(filePath)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", filePath, err)
		}
		f.Close()
	}

	// Create server with small cache size
	smallCacheSize := 10
	server, err := New(fs, ExportOptions{
		AttrCacheSize: smallCacheSize,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Access more files than the cache can hold to trigger eviction
	for i := 0; i < numFiles; i++ {
		filePath := "/file" + string(rune('0'+i%10)) + string(rune('0'+(i/10)%10))
		node, err := server.Lookup(filePath)
		if err != nil {
			t.Fatalf("Failed to lookup file %s: %v", filePath, err)
		}

		// Get attributes to cache them
		_, err = server.GetAttr(node)
		if err != nil {
			t.Fatalf("Failed to get attributes for file %s: %v", filePath, err)
		}
	}

	// Check that cache size doesn't exceed the limit
	if size := server.attrCache.Size(); size > smallCacheSize {
		t.Errorf("Cache size exceeded limit: got %d, limit is %d", size, smallCacheSize)
	}

	// Access the most recently used files to ensure they're still in cache
	for i := numFiles - smallCacheSize; i < numFiles; i++ {
		filePath := "/file" + string(rune('0'+i%10)) + string(rune('0'+(i/10)%10))
		node, err := server.Lookup(filePath)
		if err != nil {
			t.Fatalf("Failed to lookup file %s: %v", filePath, err)
		}

		// Get attributes - this should hit cache
		attrs, err := server.GetAttr(node)
		if err != nil {
			t.Fatalf("Failed to get attributes for file %s: %v", filePath, err)
		}
		
		// Basic verification that we got valid attributes
		if attrs == nil || !attrs.Mode.IsRegular() {
			t.Errorf("Invalid or missing cached attributes for %s", filePath)
		}
	}
}

func TestAttrCacheTimeout(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test file
	testFile := "/timeout-test"
	f, err := fs.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	// Create server with short cache timeout
	shortTimeout := 100 * time.Millisecond
	server, err := New(fs, ExportOptions{
		AttrCacheTimeout: shortTimeout,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Get the file node
	node, err := server.Lookup(testFile)
	if err != nil {
		t.Fatalf("Failed to lookup test file: %v", err)
	}

	// Get attributes to cache them
	attrs1, err := server.GetAttr(node)
	if err != nil {
		t.Fatalf("Failed to get attributes: %v", err)
	}

	// Get attributes again immediately - should be cached
	attrs2, err := server.GetAttr(node)
	if err != nil {
		t.Fatalf("Failed to get attributes from cache: %v", err)
	}

	// Verify attributes match
	if attrs1.Size != attrs2.Size || attrs1.Mode != attrs2.Mode {
		t.Error("Cached attributes don't match original attributes")
	}

	// Wait for the cache to expire
	time.Sleep(shortTimeout + 50*time.Millisecond)

	// Modify the file
	err = fs.Chmod(testFile, 0777) // Use a different mode to make sure we see the change
	if err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Get attributes again - should be refreshed from fs after cache expiry
	attrs3, err := server.GetAttr(node)
	if err != nil {
		t.Fatalf("Failed to get fresh attributes: %v", err)
	}

	// The mode should reflect the changes
	if attrs3.Mode != 0777 {
		t.Errorf("Attributes were not refreshed after cache expiry: got %o, want %o", 
			attrs3.Mode, 0777)
	}
}