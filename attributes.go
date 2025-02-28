package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
)

// encodeFileAttributes writes NFS file attributes to an io.Writer in XDR format
func encodeFileAttributes(w io.Writer, attrs *NFSAttrs) error {
	if err := xdrEncodeUint32(w, uint32(attrs.Mode)); err != nil {
		return err
	}
	if err := xdrEncodeUint32(w, 1); err != nil { // nlink
		return err
	}
	if err := xdrEncodeUint32(w, attrs.Uid); err != nil {
		return err
	}
	if err := xdrEncodeUint32(w, attrs.Gid); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, attrs.Size); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint64(attrs.Mtime.Unix())); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint64(attrs.Atime.Unix())); err != nil {
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
