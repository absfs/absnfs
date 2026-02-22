package absnfs

import (
	"testing"

	"github.com/absfs/memfs"
)

// newTestFS creates a new memfs filesystem for testing.
func newTestFS(t *testing.T) *memfs.FileSystem {
	t.Helper()
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}
	return fs
}
