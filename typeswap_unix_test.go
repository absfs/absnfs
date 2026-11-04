//go:build unix

package absnfs

import (
	"path/filepath"
	"syscall"
	"testing"
)

// TestSpecialFilesNotReadOrTruncated: a FIFO is neither read, written nor
// truncated (NFS3ERR_INVAL, as Linux knfsd answers): opening one would
// block the server's worker.
func TestSpecialFilesNotReadOrTruncated(t *testing.T) {
	s := newSwapEnv(t)
	if err := syscall.Mkfifo(filepath.Join(s.native, "export", "fifo"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	fh := s.handle(s.export + "/fifo")
	sizeZero := encodeSattr3(false, 0, false, 0, false, 0, true, 0, 0, 0, 0, 0, 0, 0)
	data := []byte("data")
	write := append(xdrArgs(fh, uint64(0), uint32(len(data)), uint32(2), uint32(len(data))), data...)
	for name, c := range map[string]struct {
		proc uint32
		args []byte
	}{
		"READ":         {NFSPROC3_READ, xdrArgs(fh, uint64(0), uint32(100))},
		"WRITE":        {NFSPROC3_WRITE, write},
		"SETATTR size": {NFSPROC3_SETATTR, setattrRequest(fh, sizeZero)},
	} {
		if got := s.call(c.proc, c.args); got != NFSERR_INVAL {
			t.Errorf("%s on a FIFO: status %d, want NFS3ERR_INVAL", name, got)
		}
	}
}
