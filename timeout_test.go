package absnfs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// TestTimeoutConfiguration tests the timeout configuration
func TestTimeoutConfiguration(t *testing.T) {
	t.Run("default timeouts", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		nfs, err := New(fs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}
		defer nfs.Close()

		// Verify default timeouts
		if nfs.options.Timeouts.ReadTimeout != 30*time.Second {
			t.Errorf("Default ReadTimeout should be 30s, got %v", nfs.options.Timeouts.ReadTimeout)
		}
		if nfs.options.Timeouts.WriteTimeout != 60*time.Second {
			t.Errorf("Default WriteTimeout should be 60s, got %v", nfs.options.Timeouts.WriteTimeout)
		}
		if nfs.options.Timeouts.LookupTimeout != 10*time.Second {
			t.Errorf("Default LookupTimeout should be 10s, got %v", nfs.options.Timeouts.LookupTimeout)
		}
		if nfs.options.Timeouts.ReaddirTimeout != 30*time.Second {
			t.Errorf("Default ReaddirTimeout should be 30s, got %v", nfs.options.Timeouts.ReaddirTimeout)
		}
		if nfs.options.Timeouts.CreateTimeout != 15*time.Second {
			t.Errorf("Default CreateTimeout should be 15s, got %v", nfs.options.Timeouts.CreateTimeout)
		}
		if nfs.options.Timeouts.RemoveTimeout != 15*time.Second {
			t.Errorf("Default RemoveTimeout should be 15s, got %v", nfs.options.Timeouts.RemoveTimeout)
		}
		if nfs.options.Timeouts.RenameTimeout != 20*time.Second {
			t.Errorf("Default RenameTimeout should be 20s, got %v", nfs.options.Timeouts.RenameTimeout)
		}
		if nfs.options.Timeouts.HandleTimeout != 5*time.Second {
			t.Errorf("Default HandleTimeout should be 5s, got %v", nfs.options.Timeouts.HandleTimeout)
		}
		if nfs.options.Timeouts.DefaultTimeout != 30*time.Second {
			t.Errorf("Default DefaultTimeout should be 30s, got %v", nfs.options.Timeouts.DefaultTimeout)
		}
	})

	t.Run("custom timeouts", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		customTimeouts := &TimeoutConfig{
			ReadTimeout:    5 * time.Second,
			WriteTimeout:   10 * time.Second,
			LookupTimeout:  2 * time.Second,
			ReaddirTimeout: 8 * time.Second,
			CreateTimeout:  4 * time.Second,
			RemoveTimeout:  3 * time.Second,
			RenameTimeout:  6 * time.Second,
			HandleTimeout:  1 * time.Second,
			DefaultTimeout: 7 * time.Second,
		}

		nfs, err := New(fs, ExportOptions{
			Timeouts: customTimeouts,
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}
		defer nfs.Close()

		// Verify custom timeouts
		if nfs.options.Timeouts.ReadTimeout != 5*time.Second {
			t.Errorf("Custom ReadTimeout should be 5s, got %v", nfs.options.Timeouts.ReadTimeout)
		}
		if nfs.options.Timeouts.WriteTimeout != 10*time.Second {
			t.Errorf("Custom WriteTimeout should be 10s, got %v", nfs.options.Timeouts.WriteTimeout)
		}
		if nfs.options.Timeouts.LookupTimeout != 2*time.Second {
			t.Errorf("Custom LookupTimeout should be 2s, got %v", nfs.options.Timeouts.LookupTimeout)
		}
	})

	t.Run("partial custom timeouts with defaults", func(t *testing.T) {
		fs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		// Only set some timeouts, others should use defaults
		partialTimeouts := &TimeoutConfig{
			ReadTimeout: 5 * time.Second,
			// Other timeouts left as zero
		}

		nfs, err := New(fs, ExportOptions{
			Timeouts: partialTimeouts,
		})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}
		defer nfs.Close()

		// Verify custom timeout is set
		if nfs.options.Timeouts.ReadTimeout != 5*time.Second {
			t.Errorf("Custom ReadTimeout should be 5s, got %v", nfs.options.Timeouts.ReadTimeout)
		}
		// Verify defaults are used for unset timeouts
		if nfs.options.Timeouts.WriteTimeout != 60*time.Second {
			t.Errorf("Default WriteTimeout should be 60s, got %v", nfs.options.Timeouts.WriteTimeout)
		}
		if nfs.options.Timeouts.LookupTimeout != 10*time.Second {
			t.Errorf("Default LookupTimeout should be 10s, got %v", nfs.options.Timeouts.LookupTimeout)
		}
	})
}

// TestLookupTimeout tests the Lookup operation timeout
func TestLookupTimeout(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test file
	f, err := fs.Create("/testfile")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	nfs, err := New(fs, ExportOptions{
		Timeouts: &TimeoutConfig{
			LookupTimeout: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	// Test with already expired context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	_, err = nfs.LookupWithContext(ctx, "/testfile")
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout error, got: %v", err)
	}

	// Verify timeout metrics
	if nfs.metrics != nil {
		metrics := nfs.metrics.GetMetrics()
		if metrics.LookupTimeouts == 0 {
			t.Error("Expected LookupTimeouts to be incremented")
		}
	}
}

// TestReadTimeout tests the Read operation timeout
func TestReadTimeout(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test file with content
	testContent := []byte("Hello, Timeout World!")
	f, err := fs.Create("/testfile")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if _, err := f.Write(testContent); err != nil {
		t.Fatalf("Failed to write test content: %v", err)
	}
	f.Close()

	nfs, err := New(fs, ExportOptions{
		Timeouts: &TimeoutConfig{
			ReadTimeout: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	node, err := nfs.Lookup("/testfile")
	if err != nil {
		t.Fatalf("Failed to lookup file: %v", err)
	}

	// Test with already expired context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	_, err = nfs.ReadWithContext(ctx, node, 0, int64(len(testContent)))
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout error, got: %v", err)
	}

	// Verify timeout metrics
	if nfs.metrics != nil {
		metrics := nfs.metrics.GetMetrics()
		if metrics.ReadTimeouts == 0 {
			t.Error("Expected ReadTimeouts to be incremented")
		}
	}
}

// TestWriteTimeout tests the Write operation timeout
func TestWriteTimeout(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test file
	f, err := fs.Create("/testfile")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	nfs, err := New(fs, ExportOptions{
		Timeouts: &TimeoutConfig{
			WriteTimeout: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	node, err := nfs.Lookup("/testfile")
	if err != nil {
		t.Fatalf("Failed to lookup file: %v", err)
	}

	// Test with already expired context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	testData := []byte("Test data")
	_, err = nfs.WriteWithContext(ctx, node, 0, testData)
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout error, got: %v", err)
	}

	// Verify timeout metrics
	if nfs.metrics != nil {
		metrics := nfs.metrics.GetMetrics()
		if metrics.WriteTimeouts == 0 {
			t.Error("Expected WriteTimeouts to be incremented")
		}
	}
}

// TestCreateTimeout tests the Create operation timeout
func TestCreateTimeout(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directory
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	nfs, err := New(fs, ExportOptions{
		Timeouts: &TimeoutConfig{
			CreateTimeout: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	dirNode, err := nfs.Lookup("/testdir")
	if err != nil {
		t.Fatalf("Failed to lookup directory: %v", err)
	}

	// Test with already expired context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	attrs := &NFSAttrs{
		Mode: 0644,
		Uid:  1000,
		Gid:  1000,
		Size: 0,
		// Mtime: time.Now()
		// Atime: time.Now()
	}

	_, err = nfs.CreateWithContext(ctx, dirNode, "testfile", attrs)
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout error, got: %v", err)
	}

	// Verify timeout metrics
	if nfs.metrics != nil {
		metrics := nfs.metrics.GetMetrics()
		if metrics.CreateTimeouts == 0 {
			t.Error("Expected CreateTimeouts to be incremented")
		}
	}
}

// TestRemoveTimeout tests the Remove operation timeout
func TestRemoveTimeout(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directory and file
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	f, err := fs.Create("/testdir/testfile")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	nfs, err := New(fs, ExportOptions{
		Timeouts: &TimeoutConfig{
			RemoveTimeout: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	dirNode, err := nfs.Lookup("/testdir")
	if err != nil {
		t.Fatalf("Failed to lookup directory: %v", err)
	}

	// Test with already expired context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	err = nfs.RemoveWithContext(ctx, dirNode, "testfile")
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout error, got: %v", err)
	}

	// Verify timeout metrics
	if nfs.metrics != nil {
		metrics := nfs.metrics.GetMetrics()
		if metrics.RemoveTimeouts == 0 {
			t.Error("Expected RemoveTimeouts to be incremented")
		}
	}
}

// TestRenameTimeout tests the Rename operation timeout
func TestRenameTimeout(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directory and file
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	f, err := fs.Create("/testdir/oldfile")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	nfs, err := New(fs, ExportOptions{
		Timeouts: &TimeoutConfig{
			RenameTimeout: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	dirNode, err := nfs.Lookup("/testdir")
	if err != nil {
		t.Fatalf("Failed to lookup directory: %v", err)
	}

	// Test with already expired context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	err = nfs.RenameWithContext(ctx, dirNode, "oldfile", dirNode, "newfile")
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout error, got: %v", err)
	}

	// Verify timeout metrics
	if nfs.metrics != nil {
		metrics := nfs.metrics.GetMetrics()
		if metrics.RenameTimeouts == 0 {
			t.Error("Expected RenameTimeouts to be incremented")
		}
	}
}

// TestReaddirTimeout tests the ReadDir operation timeout
func TestReaddirTimeout(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directory with files
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	for i := 0; i < 3; i++ {
		f, err := fs.Create("/testdir/file")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		f.Close()
	}

	nfs, err := New(fs, ExportOptions{
		Timeouts: &TimeoutConfig{
			ReaddirTimeout: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	dirNode, err := nfs.Lookup("/testdir")
	if err != nil {
		t.Fatalf("Failed to lookup directory: %v", err)
	}

	// Test with already expired context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	_, err = nfs.ReadDirWithContext(ctx, dirNode)
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout error, got: %v", err)
	}

	// Verify timeout metrics
	if nfs.metrics != nil {
		metrics := nfs.metrics.GetMetrics()
		if metrics.ReaddirTimeouts == 0 {
			t.Error("Expected ReaddirTimeouts to be incremented")
		}
	}
}

// TestTimeoutMetricsTracking tests that timeout metrics are properly tracked
func TestTimeoutMetricsTracking(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test structure
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	f, err := fs.Create("/testdir/testfile")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Write([]byte("test data"))
	f.Close()

	nfs, err := New(fs, ExportOptions{
		Timeouts: &TimeoutConfig{
			ReadTimeout:    100 * time.Millisecond,
			WriteTimeout:   100 * time.Millisecond,
			LookupTimeout:  100 * time.Millisecond,
			ReaddirTimeout: 100 * time.Millisecond,
			CreateTimeout:  100 * time.Millisecond,
			RemoveTimeout:  100 * time.Millisecond,
			RenameTimeout:  100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	// Create an expired context
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	dirNode, _ := nfs.Lookup("/testdir")
	node, _ := nfs.Lookup("/testdir/testfile")

	// Trigger various timeouts
	nfs.LookupWithContext(ctx, "/testdir/testfile")
	if node != nil {
		nfs.ReadWithContext(ctx, node, 0, 10)
		nfs.WriteWithContext(ctx, node, 0, []byte("data"))
	}
	if dirNode != nil {
		now := time.Now()
		attrs := &NFSAttrs{Mode: 0644, Uid: 1000, Gid: 1000, Size: 0}
		attrs.SetMtime(now)
		attrs.SetAtime(now)
		nfs.CreateWithContext(ctx, dirNode, "newfile", attrs)
		nfs.RemoveWithContext(ctx, dirNode, "testfile")
		nfs.RenameWithContext(ctx, dirNode, "testfile", dirNode, "renamed")
		nfs.ReadDirWithContext(ctx, dirNode)
	}

	// Get metrics
	metrics := nfs.metrics.GetMetrics()

	// Verify TotalTimeouts is greater than 0
	if metrics.TotalTimeouts == 0 {
		t.Error("Expected TotalTimeouts to be greater than 0")
	}

	// Verify individual timeout counters
	if metrics.LookupTimeouts == 0 {
		t.Error("Expected LookupTimeouts to be incremented")
	}
}

// TestTimeoutErrorMapping tests that timeout errors are properly mapped to NFSERR_DELAY
func TestTimeoutErrorMapping(t *testing.T) {
	// Test ErrTimeout mapping
	status := mapError(ErrTimeout)
	if status != NFSERR_DELAY {
		t.Errorf("Expected NFSERR_DELAY for ErrTimeout, got %d", status)
	}

	// Test context.DeadlineExceeded mapping
	status = mapError(context.DeadlineExceeded)
	if status != NFSERR_DELAY {
		t.Errorf("Expected NFSERR_DELAY for context.DeadlineExceeded, got %d", status)
	}

	// Test nil error
	status = mapError(nil)
	if status != NFS_OK {
		t.Errorf("Expected NFS_OK for nil error, got %d", status)
	}

	// Test other errors
	status = mapError(os.ErrNotExist)
	if status != NFSERR_NOENT {
		t.Errorf("Expected NFSERR_NOENT for ErrNotExist, got %d", status)
	}
}

// TestDefaultTimeoutFallback tests that operations use defaults when not specified
func TestDefaultTimeoutFallback(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	nfs, err := New(fs, ExportOptions{
		Timeouts: &TimeoutConfig{
			DefaultTimeout: 5 * time.Second,
			// Leave other timeouts as zero
		},
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}
	defer nfs.Close()

	// Verify that zero timeouts were filled with defaults
	if nfs.options.Timeouts.ReadTimeout == 0 {
		t.Error("ReadTimeout should have been set to default")
	}
	if nfs.options.Timeouts.WriteTimeout == 0 {
		t.Error("WriteTimeout should have been set to default")
	}
}
