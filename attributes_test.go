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
			Mode:   os.FileMode(0644),
			Size:   1024,
			FileId: 12345,
			Uid:    1000,
			Gid:    1000,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode attributes: %v", err)
		}

		// RFC 1813 fattr3 structure:
		// ftype3     type       (4 bytes)
		// mode3      mode       (4 bytes)
		// uint32     nlink      (4 bytes)
		// uid3       uid        (4 bytes)
		// gid3       gid        (4 bytes)
		// size3      size       (8 bytes)
		// size3      used       (8 bytes)
		// specdata3  rdev       (8 bytes - two uint32s)
		// uint64     fsid       (8 bytes)
		// fileid3    fileid     (8 bytes)
		// nfstime3   atime      (8 bytes - seconds, nseconds)
		// nfstime3   mtime      (8 bytes - seconds, nseconds)
		// nfstime3   ctime      (8 bytes - seconds, nseconds)

		r := bytes.NewReader(buf.Bytes())

		// ftype - should be NF3REG (1) for regular file
		var ftype uint32
		if err := binary.Read(r, binary.BigEndian, &ftype); err != nil {
			t.Fatalf("Failed to read ftype: %v", err)
		}
		if ftype != NF3REG {
			t.Errorf("Expected ftype NF3REG (1), got %v", ftype)
		}

		// mode - permission bits only (0644 = 420)
		var mode uint32
		if err := binary.Read(r, binary.BigEndian, &mode); err != nil {
			t.Fatalf("Failed to read mode: %v", err)
		}
		if mode != uint32(attrs.Mode.Perm()) {
			t.Errorf("Expected mode %v, got %v", uint32(attrs.Mode.Perm()), mode)
		}

		// nlink
		var nlink uint32
		if err := binary.Read(r, binary.BigEndian, &nlink); err != nil {
			t.Fatalf("Failed to read nlink: %v", err)
		}
		if nlink != 1 {
			t.Errorf("Expected nlink 1, got %v", nlink)
		}

		// uid
		var uid uint32
		if err := binary.Read(r, binary.BigEndian, &uid); err != nil {
			t.Fatalf("Failed to read uid: %v", err)
		}
		if uid != attrs.Uid {
			t.Errorf("Expected uid %v, got %v", attrs.Uid, uid)
		}

		// gid
		var gid uint32
		if err := binary.Read(r, binary.BigEndian, &gid); err != nil {
			t.Fatalf("Failed to read gid: %v", err)
		}
		if gid != attrs.Gid {
			t.Errorf("Expected gid %v, got %v", attrs.Gid, gid)
		}

		// size
		var size uint64
		if err := binary.Read(r, binary.BigEndian, &size); err != nil {
			t.Fatalf("Failed to read size: %v", err)
		}
		if size != uint64(attrs.Size) {
			t.Errorf("Expected size %v, got %v", attrs.Size, size)
		}

		// used (same as size)
		var used uint64
		if err := binary.Read(r, binary.BigEndian, &used); err != nil {
			t.Fatalf("Failed to read used: %v", err)
		}
		if used != uint64(attrs.Size) {
			t.Errorf("Expected used %v, got %v", attrs.Size, used)
		}

		// rdev (specdata1, specdata2)
		var specdata1, specdata2 uint32
		if err := binary.Read(r, binary.BigEndian, &specdata1); err != nil {
			t.Fatalf("Failed to read specdata1: %v", err)
		}
		if err := binary.Read(r, binary.BigEndian, &specdata2); err != nil {
			t.Fatalf("Failed to read specdata2: %v", err)
		}
		if specdata1 != 0 || specdata2 != 0 {
			t.Errorf("Expected rdev (0, 0), got (%v, %v)", specdata1, specdata2)
		}

		// fsid
		var fsid uint64
		if err := binary.Read(r, binary.BigEndian, &fsid); err != nil {
			t.Fatalf("Failed to read fsid: %v", err)
		}
		// fsid is 0 for now
		if fsid != 0 {
			t.Errorf("Expected fsid 0, got %v", fsid)
		}

		// fileid
		var fileid uint64
		if err := binary.Read(r, binary.BigEndian, &fileid); err != nil {
			t.Fatalf("Failed to read fileid: %v", err)
		}
		if fileid != attrs.FileId {
			t.Errorf("Expected fileid %v, got %v", attrs.FileId, fileid)
		}

		// atime (nfstime3: seconds, nseconds)
		var atimeSec, atimeNsec uint32
		if err := binary.Read(r, binary.BigEndian, &atimeSec); err != nil {
			t.Fatalf("Failed to read atime seconds: %v", err)
		}
		if err := binary.Read(r, binary.BigEndian, &atimeNsec); err != nil {
			t.Fatalf("Failed to read atime nseconds: %v", err)
		}
		if atimeSec != uint32(attrs.Atime().Unix()) {
			t.Errorf("Expected atime seconds %v, got %v", attrs.Atime().Unix(), atimeSec)
		}
		if atimeNsec != uint32(attrs.Atime().Nanosecond()) {
			t.Errorf("Expected atime nseconds %v, got %v", attrs.Atime().Nanosecond(), atimeNsec)
		}

		// mtime (nfstime3: seconds, nseconds)
		var mtimeSec, mtimeNsec uint32
		if err := binary.Read(r, binary.BigEndian, &mtimeSec); err != nil {
			t.Fatalf("Failed to read mtime seconds: %v", err)
		}
		if err := binary.Read(r, binary.BigEndian, &mtimeNsec); err != nil {
			t.Fatalf("Failed to read mtime nseconds: %v", err)
		}
		if mtimeSec != uint32(attrs.Mtime().Unix()) {
			t.Errorf("Expected mtime seconds %v, got %v", attrs.Mtime().Unix(), mtimeSec)
		}
		if mtimeNsec != uint32(attrs.Mtime().Nanosecond()) {
			t.Errorf("Expected mtime nseconds %v, got %v", attrs.Mtime().Nanosecond(), mtimeNsec)
		}

		// ctime (nfstime3: seconds, nseconds) - same as mtime
		var ctimeSec, ctimeNsec uint32
		if err := binary.Read(r, binary.BigEndian, &ctimeSec); err != nil {
			t.Fatalf("Failed to read ctime seconds: %v", err)
		}
		if err := binary.Read(r, binary.BigEndian, &ctimeNsec); err != nil {
			t.Fatalf("Failed to read ctime nseconds: %v", err)
		}
		if ctimeSec != uint32(attrs.Mtime().Unix()) {
			t.Errorf("Expected ctime seconds %v, got %v", attrs.Mtime().Unix(), ctimeSec)
		}
		if ctimeNsec != uint32(attrs.Mtime().Nanosecond()) {
			t.Errorf("Expected ctime nseconds %v, got %v", attrs.Mtime().Nanosecond(), ctimeNsec)
		}

		// Verify total size: 4+4+4+4+4+8+8+4+4+8+8+4+4+4+4+4+4 = 84 bytes
		expectedSize := 84
		if buf.Len() != expectedSize {
			t.Errorf("Expected encoded size %d bytes, got %d", expectedSize, buf.Len())
		}
	})

	t.Run("directory type", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeDir | os.FileMode(0755),
			Size: 0,
		}

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode attributes: %v", err)
		}

		r := bytes.NewReader(buf.Bytes())
		var ftype uint32
		if err := binary.Read(r, binary.BigEndian, &ftype); err != nil {
			t.Fatalf("Failed to read ftype: %v", err)
		}
		if ftype != NF3DIR {
			t.Errorf("Expected ftype NF3DIR (2), got %v", ftype)
		}
	})

	t.Run("symlink type", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeSymlink | os.FileMode(0777),
			Size: 0,
		}

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode attributes: %v", err)
		}

		r := bytes.NewReader(buf.Bytes())
		var ftype uint32
		if err := binary.Read(r, binary.BigEndian, &ftype); err != nil {
			t.Fatalf("Failed to read ftype: %v", err)
		}
		if ftype != NF3LNK {
			t.Errorf("Expected ftype NF3LNK (5), got %v", ftype)
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
			failAfter: 2, // Fail after writing ftype and mode
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
			Mode:   os.FileMode(0644),
			Size:   1024,
			FileId: 12345,
			Uid:    1000,
			Gid:    1000,
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

		// ftype should be NF3REG (1) for regular file
		var ftype uint32
		if err := binary.Read(r, binary.BigEndian, &ftype); err != nil {
			t.Fatalf("Failed to read ftype: %v", err)
		}
		if ftype != NF3REG {
			t.Errorf("Expected ftype NF3REG (1), got %v", ftype)
		}

		// mode - permission bits only
		var mode uint32
		if err := binary.Read(r, binary.BigEndian, &mode); err != nil {
			t.Fatalf("Failed to read mode: %v", err)
		}
		if mode != uint32(attrs.Mode.Perm()) {
			t.Errorf("Expected mode %v, got %v", uint32(attrs.Mode.Perm()), mode)
		}

		// Total size: 4 (status) + 84 (fattr3) = 88 bytes
		expectedSize := 88
		if len(data) != expectedSize {
			t.Errorf("Expected encoded size %d bytes, got %d", expectedSize, len(data))
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
