package absnfs

import (
	"bytes"
	"io"
	"os"
)

// NFSv3 file types (ftype3)
const (
	NF3REG  = 1 // Regular file
	NF3DIR  = 2 // Directory
	NF3BLK  = 3 // Block device
	NF3CHR  = 4 // Character device
	NF3LNK  = 5 // Symbolic link
	NF3SOCK = 6 // Socket
	NF3FIFO = 7 // Named pipe (FIFO)
)

// encodeFileAttributes writes NFSv3 fattr3 structure to an io.Writer in XDR format
// Per RFC 1813, fattr3 contains:
//   ftype3     type       - file type
//   mode3      mode       - protection mode bits
//   uint32     nlink      - number of hard links
//   uid3       uid        - owner user id
//   gid3       gid        - owner group id
//   size3      size       - file size in bytes
//   size3      used       - disk space used
//   specdata3  rdev       - device info (specdata1, specdata2)
//   uint64     fsid       - filesystem id
//   fileid3    fileid     - file id (inode)
//   nfstime3   atime      - access time (seconds, nseconds)
//   nfstime3   mtime      - modify time (seconds, nseconds)
//   nfstime3   ctime      - change time (seconds, nseconds)
func encodeFileAttributes(w io.Writer, attrs *NFSAttrs) error {
	// Determine file type from mode
	var ftype uint32 = NF3REG
	mode := attrs.Mode
	switch mode & os.ModeType {
	case os.ModeDir:
		ftype = NF3DIR
	case os.ModeSymlink:
		ftype = NF3LNK
	case os.ModeDevice:
		ftype = NF3BLK
	case os.ModeDevice | os.ModeCharDevice:
		ftype = NF3CHR
	case os.ModeSocket:
		ftype = NF3SOCK
	case os.ModeNamedPipe:
		ftype = NF3FIFO
	default:
		ftype = NF3REG
	}

	// type - file type
	if err := xdrEncodeUint32(w, ftype); err != nil {
		return err
	}
	// mode - permission bits only (strip type bits)
	if err := xdrEncodeUint32(w, uint32(mode.Perm())); err != nil {
		return err
	}
	// nlink - number of hard links (always 1 for now)
	if err := xdrEncodeUint32(w, 1); err != nil {
		return err
	}
	// uid - owner user id
	if err := xdrEncodeUint32(w, attrs.Uid); err != nil {
		return err
	}
	// gid - owner group id
	if err := xdrEncodeUint32(w, attrs.Gid); err != nil {
		return err
	}
	// size - file size in bytes (uint64)
	if err := xdrEncodeUint64(w, uint64(attrs.Size)); err != nil {
		return err
	}
	// used - disk space used (uint64) - same as size for now
	if err := xdrEncodeUint64(w, uint64(attrs.Size)); err != nil {
		return err
	}
	// rdev - specdata3 (specdata1, specdata2) - not used for regular files
	if err := xdrEncodeUint32(w, 0); err != nil { // specdata1
		return err
	}
	if err := xdrEncodeUint32(w, 0); err != nil { // specdata2
		return err
	}
	// fsid - filesystem id (uint64)
	if err := xdrEncodeUint64(w, 0); err != nil {
		return err
	}
	// fileid - file id/inode (uint64)
	if err := xdrEncodeUint64(w, attrs.FileId); err != nil {
		return err
	}
	// atime - access time (nfstime3: seconds, nseconds)
	atime := attrs.Atime()
	if err := xdrEncodeUint32(w, uint32(atime.Unix())); err != nil {
		return err
	}
	if err := xdrEncodeUint32(w, uint32(atime.Nanosecond())); err != nil {
		return err
	}
	// mtime - modify time (nfstime3: seconds, nseconds)
	mtime := attrs.Mtime()
	if err := xdrEncodeUint32(w, uint32(mtime.Unix())); err != nil {
		return err
	}
	if err := xdrEncodeUint32(w, uint32(mtime.Nanosecond())); err != nil {
		return err
	}
	// ctime - change time (nfstime3: seconds, nseconds) - use mtime
	if err := xdrEncodeUint32(w, uint32(mtime.Unix())); err != nil {
		return err
	}
	if err := xdrEncodeUint32(w, uint32(mtime.Nanosecond())); err != nil {
		return err
	}
	return nil
}

// encodeWccAttr writes NFSv3 wcc_attr structure to an io.Writer in XDR format
// Per RFC 1813, wcc_attr contains:
//
//	size3     size  - file size in bytes
//	nfstime3  mtime - modify time (seconds, nseconds)
//	nfstime3  ctime - change time (seconds, nseconds)
//
// Total size: 24 bytes (8 + 8 + 8)
func encodeWccAttr(w io.Writer, attrs *NFSAttrs) error {
	// size (uint64)
	if err := xdrEncodeUint64(w, uint64(attrs.Size)); err != nil {
		return err
	}
	// mtime (nfstime3: seconds, nseconds)
	mtime := attrs.Mtime()
	if err := xdrEncodeUint32(w, uint32(mtime.Unix())); err != nil {
		return err
	}
	if err := xdrEncodeUint32(w, uint32(mtime.Nanosecond())); err != nil {
		return err
	}
	// ctime (nfstime3: seconds, nseconds) - use mtime
	if err := xdrEncodeUint32(w, uint32(mtime.Unix())); err != nil {
		return err
	}
	if err := xdrEncodeUint32(w, uint32(mtime.Nanosecond())); err != nil {
		return err
	}
	return nil
}

// encodeAttributesResponse writes a successful NFS response with file attributes
func encodeAttributesResponse(attrs *NFSAttrs) ([]byte, error) {
	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeErrorResponse writes an NFS error response with the given error code
func encodeErrorResponse(errorCode uint32) []byte {
	var buf bytes.Buffer
	xdrEncodeUint32(&buf, errorCode)
	return buf.Bytes()
}
