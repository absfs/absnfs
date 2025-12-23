package absnfs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

// Tests for 0% coverage helper functions in nfs_handlers.go

func TestDecodeAndLookupHandle(t *testing.T) {
	server, handler, _, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get a valid handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	t.Run("successful decode and lookup", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		reply := &RPCReply{}

		node, handle := handler.decodeAndLookupHandle(bytes.NewReader(buf.Bytes()), reply)
		if node == nil {
			t.Errorf("Expected node, got nil")
		}
		if handle != rootHandle {
			t.Errorf("Expected handle %d, got %d", rootHandle, handle)
		}
	})

	t.Run("invalid handle data", func(t *testing.T) {
		body := []byte{0x00, 0x00} // Too short
		reply := &RPCReply{}

		node, _ := handler.decodeAndLookupHandle(bytes.NewReader(body), reply)
		if node != nil {
			t.Errorf("Expected nil node for invalid handle data")
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, 999999)
		reply := &RPCReply{}

		node, _ := handler.decodeAndLookupHandle(bytes.NewReader(buf.Bytes()), reply)
		if node != nil {
			t.Errorf("Expected nil node for non-existent handle")
		}
	})
}

func TestEncodePostOpAttr(t *testing.T) {
	attrs := &NFSAttrs{
		Mode: 0644,
		Size: 1024,
		Uid:  1000,
		Gid:  1000,
	}
	attrs.SetMtime(time.Now())
	attrs.SetAtime(time.Now())

	t.Run("encode post_op_attr", func(t *testing.T) {
		var buf bytes.Buffer
		err := encodePostOpAttr(&buf, attrs)
		if err != nil {
			t.Fatalf("encodePostOpAttr failed: %v", err)
		}

		if buf.Len() < 4 {
			t.Errorf("Expected at least 4 bytes, got %d", buf.Len())
		}
	})
}

func TestEncodeNoPostOpAttr(t *testing.T) {
	t.Run("encode empty post_op_attr", func(t *testing.T) {
		var buf bytes.Buffer
		encodeNoPostOpAttr(&buf)

		if buf.Len() != 4 {
			t.Errorf("Expected 4 bytes, got %d", buf.Len())
		}

		data := buf.Bytes()
		if data[0] != 0 || data[1] != 0 || data[2] != 0 || data[3] != 0 {
			t.Errorf("Expected all zeros, got %v", data)
		}
	})
}

// Tests for FileHandleMap.GetOrError (renamed to avoid collision)

func TestFileHandleMapGetOrErrorZeroCoverage(t *testing.T) {
	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
	}

	mfs, _ := memfs.NewFS()
	f, _ := mfs.Create("/test.txt")

	handle := fm.Allocate(f)

	t.Run("get existing handle", func(t *testing.T) {
		file, err := fm.GetOrError(handle)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if file == nil {
			t.Errorf("Expected file, got nil")
		}
	})

	t.Run("get non-existent handle", func(t *testing.T) {
		_, err := fm.GetOrError(999999)
		if err == nil {
			t.Errorf("Expected error for non-existent handle")
		}
		if _, ok := err.(*InvalidFileHandleError); !ok {
			t.Errorf("Expected InvalidFileHandleError, got %T", err)
		}
	})
}

// Tests for DirCache.Resize and DirCache.UpdateTTL

func TestDirCacheResizeZeroCoverage(t *testing.T) {
	cache := NewDirCache(time.Second, 100, 1000)

	// Create mock FileInfo entries using memfs
	mfs, _ := memfs.NewFS()
	mfs.Create("/file1.txt")
	mfs.Create("/file2.txt")
	info1, _ := mfs.Stat("/file1.txt")
	info2, _ := mfs.Stat("/file2.txt")
	entries := []os.FileInfo{info1, info2}

	cache.Put("/dir1", entries)
	cache.Put("/dir2", entries)
	cache.Put("/dir3", entries)

	t.Run("resize to smaller", func(t *testing.T) {
		cache.Resize(2)
		size, _, _ := cache.Stats()
		if size > 2 {
			t.Errorf("Expected max 2 entries after resize, got %d", size)
		}
	})

	t.Run("resize to larger", func(t *testing.T) {
		cache.Resize(1000)
	})

	t.Run("resize with invalid value", func(t *testing.T) {
		cache.Resize(0)
		cache.Resize(-1)
	})
}

func TestDirCacheUpdateTTLZeroCoverage(t *testing.T) {
	cache := NewDirCache(time.Second, 100, 1000)

	t.Run("update TTL", func(t *testing.T) {
		cache.UpdateTTL(5 * time.Second)
	})

	t.Run("update TTL with invalid value", func(t *testing.T) {
		cache.UpdateTTL(0)
		cache.UpdateTTL(-time.Second)
	})
}

// Tests for NewNFSAttrs

func TestNewNFSAttrsZeroCoverage(t *testing.T) {
	now := time.Now()
	atime := now.Add(-time.Hour)

	t.Run("create new attrs", func(t *testing.T) {
		attrs := NewNFSAttrs(0755|os.ModeDir, 4096, now, atime, 1000, 1000)

		if attrs.Mode != 0755|os.ModeDir {
			t.Errorf("Expected mode %v, got %v", 0755|os.ModeDir, attrs.Mode)
		}
		if attrs.Size != 4096 {
			t.Errorf("Expected size 4096, got %d", attrs.Size)
		}
		if attrs.Uid != 1000 {
			t.Errorf("Expected uid 1000, got %d", attrs.Uid)
		}
		if attrs.Gid != 1000 {
			t.Errorf("Expected gid 1000, got %d", attrs.Gid)
		}
		if !attrs.Mtime().Equal(now) {
			t.Errorf("Expected mtime %v, got %v", now, attrs.Mtime())
		}
		if !attrs.Atime().Equal(atime) {
			t.Errorf("Expected atime %v, got %v", atime, attrs.Atime())
		}
	})
}

// Tests for NFSNode.ReadDir

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

// Tests for batch processing functions

func TestBatchProcessorGetAttrZeroCoverage(t *testing.T) {
	mfs, _ := memfs.NewFS()
	config := DefaultRateLimiterConfig()
	nfs, _ := New(mfs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})

	f, _ := mfs.Create("/testfile.txt")
	f.Write([]byte("test content"))
	f.Close()

	node, _ := nfs.Lookup("/testfile.txt")
	handle := nfs.fileMap.Allocate(node)

	bp := nfs.batchProc

	t.Run("batch get attr", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resultChan := make(chan *BatchResult, 1)
		req := &BatchRequest{
			Type:       BatchTypeGetAttr,
			FileHandle: handle,
			Context:    ctx,
			ResultChan: resultChan,
		}

		batch := &Batch{
			Type:     BatchTypeGetAttr,
			Requests: []*BatchRequest{req},
		}

		bp.processGetAttrBatch(batch)

		select {
		case result := <-resultChan:
			if result.Status != NFS_OK {
				t.Errorf("Expected NFS_OK, got %d", result.Status)
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for result")
		}
	})

	t.Run("batch get attr with cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		resultChan := make(chan *BatchResult, 1)
		req := &BatchRequest{
			Type:       BatchTypeGetAttr,
			FileHandle: handle,
			Context:    ctx,
			ResultChan: resultChan,
		}

		batch := &Batch{
			Type:     BatchTypeGetAttr,
			Requests: []*BatchRequest{req},
		}

		bp.processGetAttrBatch(batch)

		select {
		case result := <-resultChan:
			if result.Error == nil {
				t.Errorf("Expected error for cancelled context")
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for result")
		}
	})

	t.Run("batch get attr with invalid handle", func(t *testing.T) {
		ctx := context.Background()
		resultChan := make(chan *BatchResult, 1)
		req := &BatchRequest{
			Type:       BatchTypeGetAttr,
			FileHandle: 999999,
			Context:    ctx,
			ResultChan: resultChan,
		}

		batch := &Batch{
			Type:     BatchTypeGetAttr,
			Requests: []*BatchRequest{req},
		}

		bp.processGetAttrBatch(batch)

		select {
		case result := <-resultChan:
			if result.Status != NFSERR_NOENT {
				t.Errorf("Expected NFSERR_NOENT, got %d", result.Status)
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for result")
		}
	})
}

func TestBatchProcessorSetAttrZeroCoverage(t *testing.T) {
	mfs, _ := memfs.NewFS()
	config := DefaultRateLimiterConfig()
	nfs, _ := New(mfs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})

	f, _ := mfs.Create("/testfile.txt")
	f.Write([]byte("test content"))
	f.Close()

	node, _ := nfs.Lookup("/testfile.txt")
	handle := nfs.fileMap.Allocate(node)

	bp := nfs.batchProc

	t.Run("batch set attr", func(t *testing.T) {
		ctx := context.Background()
		resultChan := make(chan *BatchResult, 1)
		req := &BatchRequest{
			Type:       BatchTypeSetAttr,
			FileHandle: handle,
			Context:    ctx,
			ResultChan: resultChan,
		}

		batch := &Batch{
			Type:     BatchTypeSetAttr,
			Requests: []*BatchRequest{req},
		}

		bp.processSetAttrBatch(batch)

		select {
		case result := <-resultChan:
			if result.Status != NFS_OK {
				t.Errorf("Expected NFS_OK, got %d", result.Status)
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for result")
		}
	})

	t.Run("batch set attr with cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		resultChan := make(chan *BatchResult, 1)
		req := &BatchRequest{
			Type:       BatchTypeSetAttr,
			FileHandle: handle,
			Context:    ctx,
			ResultChan: resultChan,
		}

		batch := &Batch{
			Type:     BatchTypeSetAttr,
			Requests: []*BatchRequest{req},
		}

		bp.processSetAttrBatch(batch)

		select {
		case result := <-resultChan:
			if result.Error == nil {
				t.Errorf("Expected error for cancelled context")
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for result")
		}
	})

	t.Run("batch set attr with invalid handle", func(t *testing.T) {
		ctx := context.Background()
		resultChan := make(chan *BatchResult, 1)
		req := &BatchRequest{
			Type:       BatchTypeSetAttr,
			FileHandle: 999999,
			Context:    ctx,
			ResultChan: resultChan,
		}

		batch := &Batch{
			Type:     BatchTypeSetAttr,
			Requests: []*BatchRequest{req},
		}

		bp.processSetAttrBatch(batch)

		select {
		case result := <-resultChan:
			if result.Status != NFSERR_NOENT {
				t.Errorf("Expected NFSERR_NOENT, got %d", result.Status)
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for result")
		}
	})
}

func TestBatchProcessorDirReadZeroCoverage(t *testing.T) {
	mfs, _ := memfs.NewFS()
	config := DefaultRateLimiterConfig()
	nfs, _ := New(mfs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
		EnableDirCache:     true,
	})

	mfs.Mkdir("/testdir", 0755)
	f, _ := mfs.Create("/testdir/file1.txt")
	f.Close()

	node, _ := nfs.Lookup("/testdir")
	handle := nfs.fileMap.Allocate(node)

	bp := nfs.batchProc

	t.Run("batch dir read", func(t *testing.T) {
		ctx := context.Background()
		resultChan := make(chan *BatchResult, 1)
		req := &BatchRequest{
			Type:       BatchTypeDirRead,
			FileHandle: handle,
			Context:    ctx,
			ResultChan: resultChan,
		}

		batch := &Batch{
			Type:     BatchTypeDirRead,
			Requests: []*BatchRequest{req},
		}

		bp.processDirReadBatch(batch)

		select {
		case result := <-resultChan:
			if result.Status != NFS_OK {
				t.Errorf("Expected NFS_OK, got %d (error: %v)", result.Status, result.Error)
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for result")
		}
	})

	t.Run("batch dir read with cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		resultChan := make(chan *BatchResult, 1)
		req := &BatchRequest{
			Type:       BatchTypeDirRead,
			FileHandle: handle,
			Context:    ctx,
			ResultChan: resultChan,
		}

		batch := &Batch{
			Type:     BatchTypeDirRead,
			Requests: []*BatchRequest{req},
		}

		bp.processDirReadBatch(batch)

		select {
		case result := <-resultChan:
			if result.Error == nil {
				t.Errorf("Expected error for cancelled context")
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for result")
		}
	})

	t.Run("batch dir read with invalid handle", func(t *testing.T) {
		ctx := context.Background()
		resultChan := make(chan *BatchResult, 1)
		req := &BatchRequest{
			Type:       BatchTypeDirRead,
			FileHandle: 999999,
			Context:    ctx,
			ResultChan: resultChan,
		}

		batch := &Batch{
			Type:     BatchTypeDirRead,
			Requests: []*BatchRequest{req},
		}

		bp.processDirReadBatch(batch)

		select {
		case result := <-resultChan:
			if result.Status != NFSERR_NOENT {
				t.Errorf("Expected NFSERR_NOENT, got %d", result.Status)
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for result")
		}
	})
}

func TestBatchGetAttrZeroCoverage(t *testing.T) {
	t.Run("BatchGetAttr with batching disabled", func(t *testing.T) {
		// When batching is disabled, BatchGetAttr returns nil, 0, nil
		// to signal the caller should handle the operation directly
		mfs, _ := memfs.NewFS()
		config := DefaultRateLimiterConfig()
		nfs, _ := New(mfs, ExportOptions{
			EnableRateLimiting: false,
			RateLimitConfig:    &config,
			BatchOperations:    false, // Explicitly disable
		})

		f, _ := mfs.Create("/testfile.txt")
		f.Write([]byte("test content"))
		f.Close()

		node, _ := nfs.Lookup("/testfile.txt")
		handle := nfs.fileMap.Allocate(node)

		ctx := context.Background()
		data, status, err := nfs.batchProc.BatchGetAttr(ctx, handle)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if status != 0 {
			t.Errorf("Expected status 0 (not processed), got %d", status)
		}
		if data != nil {
			t.Errorf("Expected nil data (not processed), got %v", data)
		}
	})

	t.Run("BatchGetAttr with batching enabled and timeout", func(t *testing.T) {
		// When batching is enabled, test the timeout path
		mfs, _ := memfs.NewFS()
		config := DefaultRateLimiterConfig()
		nfs, _ := New(mfs, ExportOptions{
			EnableRateLimiting: false,
			RateLimitConfig:    &config,
			BatchOperations:    true,
			MaxBatchSize:       100, // Large batch size so it doesn't trigger immediately
		})

		f, _ := mfs.Create("/testfile.txt")
		f.Write([]byte("test content"))
		f.Close()

		node, _ := nfs.Lookup("/testfile.txt")
		handle := nfs.fileMap.Allocate(node)

		// Use a very short timeout to trigger the context.Done() path
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()
		time.Sleep(1 * time.Millisecond) // Ensure context expires

		_, status, err := nfs.batchProc.BatchGetAttr(ctx, handle)
		// Should get timeout error
		if err == nil || status != NFSERR_IO {
			// If the batch was processed before timeout (unlikely but possible)
			// that's also acceptable
			if status != NFS_OK && status != NFSERR_IO {
				t.Errorf("Expected NFS_OK or NFSERR_IO, got %d", status)
			}
		}
	})

	t.Run("BatchGetAttr with batching enabled and wait", func(t *testing.T) {
		// When batching is enabled, the request should be batched and processed
		mfs, _ := memfs.NewFS()
		config := DefaultRateLimiterConfig()
		nfs, _ := New(mfs, ExportOptions{
			EnableRateLimiting: false,
			RateLimitConfig:    &config,
			BatchOperations:    true,
			MaxBatchSize:       100, // Large batch size so it doesn't trigger immediately
		})

		f, _ := mfs.Create("/testfile.txt")
		f.Write([]byte("test content"))
		f.Close()

		node, _ := nfs.Lookup("/testfile.txt")
		handle := nfs.fileMap.Allocate(node)

		// Use a longer timeout to allow batch processing
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		data, status, err := nfs.batchProc.BatchGetAttr(ctx, handle)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
		if data == nil {
			t.Errorf("Expected data, got nil")
		}
	})
}

// Tests for metrics functions with 0% coverage

func createTestNFSForMetrics() *AbsfsNFS {
	mfs, _ := memfs.NewFS()
	config := DefaultRateLimiterConfig()
	nfs, _ := New(mfs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	return nfs
}

func TestMetricsRecordConnectionZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record connection", func(t *testing.T) {
		mc.RecordConnection()
		metrics := mc.GetMetrics()
		if metrics.ActiveConnections != 1 {
			t.Errorf("Expected 1 active connection, got %d", metrics.ActiveConnections)
		}
		if metrics.TotalConnections != 1 {
			t.Errorf("Expected 1 total connection, got %d", metrics.TotalConnections)
		}
	})
}

func TestMetricsRecordConnectionClosedZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record connection closed", func(t *testing.T) {
		mc.RecordConnection()
		mc.RecordConnectionClosed()
		metrics := mc.GetMetrics()
		if metrics.ActiveConnections != 0 {
			t.Errorf("Expected 0 active connections, got %d", metrics.ActiveConnections)
		}
	})
}

func TestMetricsRecordRejectedConnectionZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record rejected connection", func(t *testing.T) {
		mc.RecordRejectedConnection()
		metrics := mc.GetMetrics()
		if metrics.RejectedConnections != 1 {
			t.Errorf("Expected 1 rejected connection, got %d", metrics.RejectedConnections)
		}
	})
}

func TestMetricsRecordRateLimitExceededZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record rate limit exceeded", func(t *testing.T) {
		mc.RecordRateLimitExceeded()
		metrics := mc.GetMetrics()
		if metrics.RateLimitExceeded != 1 {
			t.Errorf("Expected 1 rate limit exceeded, got %d", metrics.RateLimitExceeded)
		}
	})
}

func TestMetricsRecordTLSZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record TLS handshake", func(t *testing.T) {
		mc.RecordTLSHandshake()
		metrics := mc.GetMetrics()
		if metrics.TLSHandshakes != 1 {
			t.Errorf("Expected 1 TLS handshake, got %d", metrics.TLSHandshakes)
		}
	})

	t.Run("record TLS handshake failure", func(t *testing.T) {
		mc.RecordTLSHandshakeFailure()
		metrics := mc.GetMetrics()
		if metrics.TLSHandshakeFailures != 1 {
			t.Errorf("Expected 1 TLS handshake failure, got %d", metrics.TLSHandshakeFailures)
		}
	})

	t.Run("record TLS client cert", func(t *testing.T) {
		mc.RecordTLSClientCert(true)
		mc.RecordTLSClientCert(false)
	})

	t.Run("record TLS session reused", func(t *testing.T) {
		mc.RecordTLSSessionReused()
	})

	t.Run("record TLS version", func(t *testing.T) {
		mc.RecordTLSVersion(0x0304) // TLS 1.3 version constant
	})
}

// Tests for rate limiter functions with 0% coverage

func TestTokenBucketAllowNZeroCoverage(t *testing.T) {
	tb := NewTokenBucket(10, 10)

	t.Run("allow multiple tokens", func(t *testing.T) {
		if !tb.AllowN(5) {
			t.Errorf("Expected to allow 5 tokens")
		}
	})

	t.Run("deny when not enough tokens", func(t *testing.T) {
		tb.AllowN(5)
		if tb.AllowN(10) {
			t.Errorf("Expected to deny 10 tokens when only ~0 available")
		}
	})
}

// Tests for TLS config functions with 0% coverage

func TestTLSConfigGetClientAuthStringZeroCoverage(t *testing.T) {
	config := DefaultTLSConfig()

	t.Run("get client auth string", func(t *testing.T) {
		result := config.GetClientAuthString()
		if result == "" {
			t.Errorf("Expected non-empty string")
		}
	})
}

func TestTLSConfigCloneZeroCoverage(t *testing.T) {
	config := DefaultTLSConfig()

	t.Run("clone config", func(t *testing.T) {
		cloned := config.Clone()
		if cloned == nil {
			t.Errorf("Expected cloned config, got nil")
		}
	})
}

// Tests for NoopLogger methods (renamed to avoid collision)

func TestNoopLoggerZeroCoverage(t *testing.T) {
	logger := NewNoopLogger()

	t.Run("debug", func(t *testing.T) {
		logger.Debug("test message", LogField{Key: "key", Value: "value"})
	})

	t.Run("info", func(t *testing.T) {
		logger.Info("test message", LogField{Key: "key", Value: "value"})
	})

	t.Run("warn", func(t *testing.T) {
		logger.Warn("test message", LogField{Key: "key", Value: "value"})
	})

	t.Run("error", func(t *testing.T) {
		logger.Error("test message", LogField{Key: "key", Value: "value"})
	})
}

// Tests for metrics_api.go isResourceError function

func TestIsResourceErrorZeroCoverage(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if isResourceError(nil) {
			t.Errorf("nil should not be a resource error")
		}
	})

	t.Run("non-resource errors", func(t *testing.T) {
		if isResourceError(fmt.Errorf("file not found")) {
			t.Errorf("'file not found' should not be a resource error")
		}
		if isResourceError(fmt.Errorf("permission denied")) {
			t.Errorf("'permission denied' should not be a resource error")
		}
	})

	t.Run("resource errors", func(t *testing.T) {
		if !isResourceError(fmt.Errorf("no space left on device")) {
			t.Errorf("'no space left' should be a resource error")
		}
		if !isResourceError(fmt.Errorf("quota exceeded")) {
			t.Errorf("'quota exceeded' should be a resource error")
		}
		if !isResourceError(fmt.Errorf("too many open files")) {
			t.Errorf("'too many open files' should be a resource error")
		}
		if !isResourceError(fmt.Errorf("ENOSPC")) {
			t.Errorf("ENOSPC should be a resource error")
		}
		if !isResourceError(fmt.Errorf("resource limit reached")) {
			t.Errorf("'resource limit' should be a resource error")
		}
	})
}
