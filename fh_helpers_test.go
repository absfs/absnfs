package absnfs

import "encoding/binary"

// legacyHandle is an 8-byte handle in the format before version 1 handles,
// a big-endian number. No server issues it any more: it is always stale.
func legacyHandle(v uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, v)
}
