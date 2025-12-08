package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"testing"
	"time"
)

func TestEncodeFileAttributes(t *testing.T) {
	t.Run("successful encoding", func(t *testing.T) {
		now := time.Now()
		attrs := &NFSAttrs{
			Mode: os.FileMode(0644),
			Size: 1024,
			Uid:  1000,
			Gid:  1000,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode attributes: %v", err)
		}

		// Verify encoded data
		r := bytes.NewReader(buf.Bytes())
		var mode uint32
		if err := binary.Read(r, binary.BigEndian, &mode); err != nil {
			t.Fatalf("Failed to read mode: %v", err)
		}
		if mode != uint32(attrs.Mode) {
			t.Errorf("Expected mode %v, got %v", uint32(attrs.Mode), mode)
		}

		var nlink uint32
		if err := binary.Read(r, binary.BigEndian, &nlink); err != nil {
			t.Fatalf("Failed to read nlink: %v", err)
		}
		if nlink != 1 {
			t.Errorf("Expected nlink 1, got %v", nlink)
		}

		var uid uint32
		if err := binary.Read(r, binary.BigEndian, &uid); err != nil {
			t.Fatalf("Failed to read uid: %v", err)
		}
		if uid != attrs.Uid {
			t.Errorf("Expected uid %v, got %v", attrs.Uid, uid)
		}

		var gid uint32
		if err := binary.Read(r, binary.BigEndian, &gid); err != nil {
			t.Fatalf("Failed to read gid: %v", err)
		}
		if gid != attrs.Gid {
			t.Errorf("Expected gid %v, got %v", attrs.Gid, gid)
		}

		var size uint64
		if err := binary.Read(r, binary.BigEndian, &size); err != nil {
			t.Fatalf("Failed to read size: %v", err)
		}
		if size != uint64(attrs.Size) {
			t.Errorf("Expected size %v, got %v", attrs.Size, size)
		}

		var mtime uint64
		if err := binary.Read(r, binary.BigEndian, &mtime); err != nil {
			t.Fatalf("Failed to read mtime: %v", err)
		}
		if mtime != uint64(attrs.Mtime().Unix()) {
			t.Errorf("Expected mtime %v, got %v", attrs.Mtime().Unix(), mtime)
		}

		var atime uint64
		if err := binary.Read(r, binary.BigEndian, &atime); err != nil {
			t.Fatalf("Failed to read atime: %v", err)
		}
		if atime != uint64(attrs.Atime().Unix()) {
			t.Errorf("Expected atime %v, got %v", attrs.Atime().Unix(), atime)
		}
	})

	t.Run("write errors", func(t *testing.T) {
		now := time.Now()
		attrs := &NFSAttrs{
			Mode: os.FileMode(0644),
			Size: 1024,
			Uid:  1000,
			Gid:  1000,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		// Create a writer that fails after a few writes
		failWriter := &attrFailingWriter{
			failAfter: 2, // Fail after writing mode and nlink
		}

		err := encodeFileAttributes(failWriter, attrs)
		if err == nil {
			t.Error("Expected error from failing writer, got nil")
		}
	})
}

func TestEncodeAttributesResponse(t *testing.T) {
	t.Run("successful encoding", func(t *testing.T) {
		now := time.Now()
		attrs := &NFSAttrs{
			Mode: os.FileMode(0644),
			Size: 1024,
			Uid:  1000,
			Gid:  1000,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		data, err := encodeAttributesResponse(attrs)
		if err != nil {
			t.Fatalf("Failed to encode attributes response: %v", err)
		}

		// Verify encoded data
		r := bytes.NewReader(data)
		var status uint32
		if err := binary.Read(r, binary.BigEndian, &status); err != nil {
			t.Fatalf("Failed to read status: %v", err)
		}
		if status != NFS_OK {
			t.Errorf("Expected status NFS_OK, got %v", status)
		}

		// Verify attributes were encoded
		var mode uint32
		if err := binary.Read(r, binary.BigEndian, &mode); err != nil {
			t.Fatalf("Failed to read mode: %v", err)
		}
		if mode != uint32(attrs.Mode) {
			t.Errorf("Expected mode %v, got %v", uint32(attrs.Mode), mode)
		}
	})
}

func TestEncodeErrorResponse(t *testing.T) {
	t.Run("encode error status", func(t *testing.T) {
		data := encodeErrorResponse(NFSERR_NOENT)

		r := bytes.NewReader(data)
		var status uint32
		if err := binary.Read(r, binary.BigEndian, &status); err != nil {
			t.Fatalf("Failed to read status: %v", err)
		}
		if status != NFSERR_NOENT {
			t.Errorf("Expected status NFSERR_NOENT, got %v", status)
		}
	})
}

// attrFailingWriter is a test helper that fails after a certain number of writes
type attrFailingWriter struct {
	writes    int
	failAfter int
}

func (w *attrFailingWriter) Write(p []byte) (n int, err error) {
	w.writes++
	if w.writes > w.failAfter {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}
