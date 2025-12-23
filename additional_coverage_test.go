package absnfs

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// Tests for sortDurations function
func TestSortDurations(t *testing.T) {
	t.Run("sort empty slice", func(t *testing.T) {
		durations := []time.Duration{}
		sortDurations(durations)
		if len(durations) != 0 {
			t.Errorf("Expected empty slice, got %v", durations)
		}
	})

	t.Run("sort single element", func(t *testing.T) {
		durations := []time.Duration{100 * time.Millisecond}
		sortDurations(durations)
		if durations[0] != 100*time.Millisecond {
			t.Errorf("Expected 100ms, got %v", durations[0])
		}
	})

	t.Run("sort already sorted", func(t *testing.T) {
		durations := []time.Duration{
			10 * time.Millisecond,
			20 * time.Millisecond,
			30 * time.Millisecond,
		}
		sortDurations(durations)
		if durations[0] != 10*time.Millisecond || durations[2] != 30*time.Millisecond {
			t.Errorf("Sort failed: %v", durations)
		}
	})

	t.Run("sort reverse order", func(t *testing.T) {
		durations := []time.Duration{
			300 * time.Millisecond,
			200 * time.Millisecond,
			100 * time.Millisecond,
		}
		sortDurations(durations)
		if durations[0] != 100*time.Millisecond {
			t.Errorf("Expected 100ms first, got %v", durations[0])
		}
		if durations[2] != 300*time.Millisecond {
			t.Errorf("Expected 300ms last, got %v", durations[2])
		}
	})

	t.Run("sort random order", func(t *testing.T) {
		durations := []time.Duration{
			50 * time.Millisecond,
			10 * time.Millisecond,
			80 * time.Millisecond,
			20 * time.Millisecond,
			60 * time.Millisecond,
		}
		sortDurations(durations)
		for i := 1; i < len(durations); i++ {
			if durations[i] < durations[i-1] {
				t.Errorf("Not sorted at index %d: %v < %v", i, durations[i], durations[i-1])
			}
		}
	})
}

// Tests for RecordError with all error types
func TestRecordErrorAllTypes(t *testing.T) {
	createTestCollector := func() *MetricsCollector {
		mfs, _ := memfs.NewFS()
		config := DefaultRateLimiterConfig()
		nfs, _ := New(mfs, ExportOptions{
			EnableRateLimiting: false,
			RateLimitConfig:    &config,
		})
		return NewMetricsCollector(nfs)
	}

	t.Run("record AUTH error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("AUTH")
		metrics := mc.GetMetrics()
		if metrics.AuthFailures != 1 {
			t.Errorf("Expected 1 auth failure, got %d", metrics.AuthFailures)
		}
		if metrics.ErrorCount != 1 {
			t.Errorf("Expected 1 error count, got %d", metrics.ErrorCount)
		}
	})

	t.Run("record ACCESS error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("ACCESS")
		metrics := mc.GetMetrics()
		if metrics.AccessViolations != 1 {
			t.Errorf("Expected 1 access violation, got %d", metrics.AccessViolations)
		}
	})

	t.Run("record STALE error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("STALE")
		metrics := mc.GetMetrics()
		if metrics.StaleHandles != 1 {
			t.Errorf("Expected 1 stale handle, got %d", metrics.StaleHandles)
		}
	})

	t.Run("record RESOURCE error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("RESOURCE")
		metrics := mc.GetMetrics()
		if metrics.ResourceErrors != 1 {
			t.Errorf("Expected 1 resource error, got %d", metrics.ResourceErrors)
		}
	})

	t.Run("record RATELIMIT error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("RATELIMIT")
		metrics := mc.GetMetrics()
		if metrics.RateLimitExceeded != 1 {
			t.Errorf("Expected 1 rate limit exceeded, got %d", metrics.RateLimitExceeded)
		}
	})

	t.Run("record unknown error type", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("UNKNOWN")
		metrics := mc.GetMetrics()
		// Should still increment error count
		if metrics.ErrorCount != 1 {
			t.Errorf("Expected 1 error count, got %d", metrics.ErrorCount)
		}
	})
}

// Tests for isAuthError
func TestIsAuthErrorCoverage(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if isAuthError(nil) {
			t.Error("nil should not be an auth error")
		}
	})

	t.Run("os.ErrPermission", func(t *testing.T) {
		if !isAuthError(os.ErrPermission) {
			t.Error("os.ErrPermission should be an auth error")
		}
	})

	t.Run("permission denied message", func(t *testing.T) {
		err := errors.New("permission denied")
		if !isAuthError(err) {
			t.Error("'permission denied' should be an auth error")
		}
	})

	t.Run("EACCES message", func(t *testing.T) {
		err := errors.New("EACCES: permission denied")
		if !isAuthError(err) {
			t.Error("EACCES should be an auth error")
		}
	})

	t.Run("EPERM message", func(t *testing.T) {
		err := errors.New("EPERM: operation not permitted")
		if !isAuthError(err) {
			t.Error("EPERM should be an auth error")
		}
	})

	t.Run("authentication message", func(t *testing.T) {
		err := errors.New("authentication failed")
		if !isAuthError(err) {
			t.Error("'authentication' should be an auth error")
		}
	})

	t.Run("unauthorized message", func(t *testing.T) {
		err := errors.New("unauthorized access")
		if !isAuthError(err) {
			t.Error("'unauthorized' should be an auth error")
		}
	})

	t.Run("non-auth error", func(t *testing.T) {
		err := errors.New("file not found")
		if isAuthError(err) {
			t.Error("'file not found' should not be an auth error")
		}
	})
}

// Tests for isStaleFileHandle
func TestIsStaleFileHandleCoverage(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if isStaleFileHandle(nil) {
			t.Error("nil should not be stale file handle")
		}
	})

	t.Run("stale message", func(t *testing.T) {
		err := errors.New("stale file handle")
		if !isStaleFileHandle(err) {
			t.Error("'stale' should be stale file handle")
		}
	})

	t.Run("ESTALE message", func(t *testing.T) {
		err := errors.New("ESTALE")
		if !isStaleFileHandle(err) {
			t.Error("ESTALE should be stale file handle")
		}
	})

	t.Run("no such file message", func(t *testing.T) {
		err := errors.New("no such file or directory")
		if !isStaleFileHandle(err) {
			t.Error("'no such file or directory' should be stale file handle")
		}
	})

	t.Run("file handle message", func(t *testing.T) {
		err := errors.New("invalid file handle")
		if !isStaleFileHandle(err) {
			t.Error("'file handle' should be stale file handle")
		}
	})

	t.Run("non-stale error", func(t *testing.T) {
		err := errors.New("timeout occurred")
		if isStaleFileHandle(err) {
			t.Error("'timeout' should not be stale file handle")
		}
	})
}

// Tests for validateFilename edge cases
func TestValidateFilenameCoverage(t *testing.T) {
	t.Run("valid filename", func(t *testing.T) {
		if status := validateFilename("myfile.txt"); status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("empty name", func(t *testing.T) {
		if status := validateFilename(""); status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL, got %d", status)
		}
	})

	t.Run("name too long", func(t *testing.T) {
		longName := make([]byte, 256)
		for i := range longName {
			longName[i] = 'a'
		}
		if status := validateFilename(string(longName)); status != NFSERR_NAMETOOLONG {
			t.Errorf("Expected NFSERR_NAMETOOLONG, got %d", status)
		}
	})

	t.Run("name with null byte", func(t *testing.T) {
		if status := validateFilename("file\x00name"); status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for null byte, got %d", status)
		}
	})

	t.Run("name with forward slash", func(t *testing.T) {
		if status := validateFilename("path/file"); status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for forward slash, got %d", status)
		}
	})

	t.Run("name with backslash", func(t *testing.T) {
		if status := validateFilename("path\\file"); status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for backslash, got %d", status)
		}
	})

	t.Run("dot name", func(t *testing.T) {
		if status := validateFilename("."); status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for dot, got %d", status)
		}
	})

	t.Run("dotdot name", func(t *testing.T) {
		if status := validateFilename(".."); status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for dotdot, got %d", status)
		}
	})

	t.Run("valid hidden file", func(t *testing.T) {
		if status := validateFilename(".hidden"); status != NFS_OK {
			t.Errorf("Expected NFS_OK for hidden file, got %d", status)
		}
	})

	t.Run("max length name", func(t *testing.T) {
		maxName := make([]byte, 255)
		for i := range maxName {
			maxName[i] = 'x'
		}
		if status := validateFilename(string(maxName)); status != NFS_OK {
			t.Errorf("Expected NFS_OK for max length name, got %d", status)
		}
	})
}

// Tests for validateMode
func TestValidateModeCoverage(t *testing.T) {
	t.Run("valid file mode 0644", func(t *testing.T) {
		if status := validateMode(0644, false); status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("valid directory mode 0755", func(t *testing.T) {
		if status := validateMode(0755, true); status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("mode with file type bits", func(t *testing.T) {
		// 0100644 = regular file + 0644 permissions
		if status := validateMode(0100644, false); status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for file type bits, got %d", status)
		}
	})

	t.Run("mode with invalid bits", func(t *testing.T) {
		// 01777 includes sticky bit which is invalid
		if status := validateMode(01777, false); status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for invalid bits, got %d", status)
		}
	})

	t.Run("zero mode", func(t *testing.T) {
		if status := validateMode(0, false); status != NFS_OK {
			t.Errorf("Expected NFS_OK for zero mode, got %d", status)
		}
	})
}

// Tests for MetricsCollector cache hit/miss recording
func TestMetricsCacheRecording(t *testing.T) {
	createTestNFS := func() *AbsfsNFS {
		mfs, _ := memfs.NewFS()
		config := DefaultRateLimiterConfig()
		nfs, _ := New(mfs, ExportOptions{
			EnableRateLimiting: false,
			RateLimitConfig:    &config,
		})
		return nfs
	}

	t.Run("record attr cache hit", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordAttrCacheHit()
	})

	t.Run("record attr cache miss", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordAttrCacheMiss()
	})

	t.Run("record read ahead hit", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordReadAheadHit()
	})

	t.Run("record read ahead miss", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordReadAheadMiss()
	})

	t.Run("record dir cache hit", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordDirCacheHit()
	})

	t.Run("record dir cache miss", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordDirCacheMiss()
	})

	t.Run("record negative cache hit", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordNegativeCacheHit()
	})

	t.Run("record negative cache miss", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordNegativeCacheMiss()
	})
}

// Tests for GetMetrics and IsHealthy on AbsfsNFS
func TestAbsfsNFSMetricsAPI(t *testing.T) {
	createTestNFS := func() *AbsfsNFS {
		mfs, _ := memfs.NewFS()
		config := DefaultRateLimiterConfig()
		nfs, _ := New(mfs, ExportOptions{
			EnableRateLimiting: false,
			RateLimitConfig:    &config,
		})
		return nfs
	}

	t.Run("get metrics", func(t *testing.T) {
		nfs := createTestNFS()
		metrics := nfs.GetMetrics()
		// Metrics is returned by value, just ensure it doesn't panic
		_ = metrics
	})

	t.Run("is healthy", func(t *testing.T) {
		nfs := createTestNFS()
		healthy := nfs.IsHealthy()
		if !healthy {
			t.Error("Expected healthy status")
		}
	})

	t.Run("get metrics nil collector", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.metrics = nil
		metrics := nfs.GetMetrics()
		// When collector is nil, GetMetrics returns zero value
		// Just ensure it doesn't panic
		_ = metrics
	})

	t.Run("is healthy nil collector", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.metrics = nil
		// Should not panic
		healthy := nfs.IsHealthy()
		if !healthy {
			t.Error("Expected healthy when no metrics collector")
		}
	})
}

// Tests for encodeFileAttributes with all file types
func TestEncodeFileAttributesAllTypes(t *testing.T) {
	now := time.Now()

	t.Run("block device", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeDevice | os.FileMode(0644),
			Size: 0,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode block device: %v", err)
		}
		if buf.Len() != 84 {
			t.Errorf("Expected 84 bytes, got %d", buf.Len())
		}
	})

	t.Run("character device", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeDevice | os.ModeCharDevice | os.FileMode(0644),
			Size: 0,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode char device: %v", err)
		}
	})

	t.Run("socket", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeSocket | os.FileMode(0644),
			Size: 0,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode socket: %v", err)
		}
	})

	t.Run("named pipe", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeNamedPipe | os.FileMode(0644),
			Size: 0,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode named pipe: %v", err)
		}
	})
}

// Tests for encodeWccAttr
func TestEncodeWccAttrCoverage(t *testing.T) {
	now := time.Now()

	t.Run("successful encoding", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.FileMode(0644),
			Size: 1024,
		}
		attrs.SetMtime(now)

		var buf bytes.Buffer
		err := encodeWccAttr(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode wcc attr: %v", err)
		}
		// Size: 8 (size) + 4+4 (mtime) + 4+4 (ctime) = 24 bytes
		if buf.Len() != 24 {
			t.Errorf("Expected 24 bytes, got %d", buf.Len())
		}
	})

	t.Run("error on write", func(t *testing.T) {
		attrs := &NFSAttrs{
			Size: 1024,
		}
		attrs.SetMtime(now)

		fw := &wccFailingWriter{failAfter: 0}
		err := encodeWccAttr(fw, attrs)
		if err == nil {
			t.Error("Expected error from failing writer")
		}
	})
}

// wccFailingWriter fails after specified number of writes (for wcc tests)
type wccFailingWriter struct {
	writes    int
	failAfter int
}

func (w *wccFailingWriter) Write(p []byte) (n int, err error) {
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed")
	}
	w.writes++
	return len(p), nil
}

// Tests for NewDirCache with edge cases
func TestNewDirCacheCoverage(t *testing.T) {
	t.Run("default values for zero inputs", func(t *testing.T) {
		cache := NewDirCache(0, 0, 0)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		// Defaults should be applied
		if cache.maxEntries <= 0 {
			t.Error("Expected positive maxEntries default")
		}
		if cache.maxDirSize <= 0 {
			t.Error("Expected positive maxDirSize default")
		}
		if cache.timeout <= 0 {
			t.Error("Expected positive timeout default")
		}
	})

	t.Run("negative values trigger defaults", func(t *testing.T) {
		cache := NewDirCache(-1*time.Second, -1, -1)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		if cache.maxEntries <= 0 {
			t.Error("Expected positive maxEntries after negative input")
		}
	})

	t.Run("custom values preserved", func(t *testing.T) {
		cache := NewDirCache(30*time.Second, 500, 5000)
		if cache.timeout != 30*time.Second {
			t.Errorf("Expected 30s timeout, got %v", cache.timeout)
		}
		if cache.maxEntries != 500 {
			t.Errorf("Expected 500 maxEntries, got %d", cache.maxEntries)
		}
		if cache.maxDirSize != 5000 {
			t.Errorf("Expected 5000 maxDirSize, got %d", cache.maxDirSize)
		}
	})
}

// Tests for NewAttrCache with edge cases
func TestNewAttrCacheCoverage(t *testing.T) {
	t.Run("zero size uses default", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 0)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		if cache.maxSize <= 0 {
			t.Error("Expected positive maxSize default")
		}
	})

	t.Run("negative size uses default", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, -100)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		if cache.maxSize <= 0 {
			t.Error("Expected positive maxSize after negative input")
		}
	})

	t.Run("positive values", func(t *testing.T) {
		cache := NewAttrCache(10*time.Second, 500)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		if cache.maxSize != 500 {
			t.Errorf("Expected maxSize 500, got %d", cache.maxSize)
		}
	})
}

// Tests for AttrCache removeFromAccessLog
func TestAttrCacheRemoveFromAccessLog(t *testing.T) {
	t.Run("remove non-existent path", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 100)
		// Should not panic
		cache.removeFromAccessLog("/nonexistent")
	})

	t.Run("remove existing path", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 100)
		attrs := &NFSAttrs{
			Mode: os.FileMode(0644),
			Size: 100,
		}
		cache.Put("/test/file", attrs)
		cache.removeFromAccessLog("/test/file")
		// Verify the element was removed from list
		cache.mu.RLock()
		cached, ok := cache.cache["/test/file"]
		cache.mu.RUnlock()
		if ok && cached.listElement != nil {
			t.Error("Expected listElement to be nil after removeFromAccessLog")
		}
	})
}

// Tests for DirCache removeFromAccessList
func TestDirCacheRemoveFromAccessList(t *testing.T) {
	t.Run("remove non-existent path", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 1000)
		// Should not panic
		cache.removeFromAccessList("/nonexistent")
	})

	t.Run("remove existing path", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 1000)
		cache.Put("/test/dir", []os.FileInfo{})
		cache.removeFromAccessList("/test/dir")
		// Verify the element was removed from list
		cache.mu.RLock()
		cached, ok := cache.entries["/test/dir"]
		cache.mu.RUnlock()
		if ok && cached.listElement != nil {
			t.Error("Expected listElement to be nil after removeFromAccessList")
		}
	})
}

// Tests for DirCache Put with oversized directory
func TestDirCachePutOversized(t *testing.T) {
	t.Run("reject directory exceeding maxDirSize", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 10) // maxDirSize = 10

		// Create more entries than allowed
		entries := make([]os.FileInfo, 20)
		cache.Put("/large/dir", entries)

		// Should not be cached
		_, found := cache.Get("/large/dir")
		if found {
			t.Error("Expected oversized directory to not be cached")
		}
	})
}
