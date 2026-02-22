// rpc_transport.go: Record marking for TCP-based ONC RPC.
//
// Implements RFC 1831 section 10 record fragment framing via
// RecordMarkingConn, which wraps a TCP connection to handle
// multi-fragment RPC messages. Provides ReadRecord and WriteRecord
// methods for reading and writing complete RPC messages as a
// sequence of length-prefixed fragments.
package absnfs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

// Record Marking constants (RFC 1831 Section 10)
const (
	// LastFragmentFlag is set in the fragment header to indicate the last fragment
	LastFragmentFlag = 0x80000000

	// MaxFragmentSize is the maximum size of a single RPC fragment
	MaxFragmentSize = 0x7FFFFFFF // 2GB - 1, limited by 31-bit length field

	// DefaultMaxFragmentSize is the default maximum fragment size for writes
	DefaultMaxFragmentSize = 1 << 20 // 1MB

	// DefaultMaxRecordSize is the maximum total size of a reassembled record (all fragments combined).
	// This prevents unbounded memory growth from many small fragments.
	DefaultMaxRecordSize = 1 << 20 // 1MB
)

// RecordMarkingReader wraps a reader to handle RPC record marking.
// RPC over TCP uses "record marking" where each message is preceded
// by a 4-byte header containing the fragment length and last-fragment flag.
type RecordMarkingReader struct {
	r             io.Reader
	fragmentBuf   *bytes.Buffer
	lastFragment  bool
	complete      bool // true when we've read a complete record
	MaxRecordSize int  // maximum total record size across all fragments (0 = DefaultMaxRecordSize)
}

// NewRecordMarkingReader creates a new record marking reader
func NewRecordMarkingReader(r io.Reader) *RecordMarkingReader {
	return &RecordMarkingReader{
		r:             r,
		fragmentBuf:   new(bytes.Buffer),
		MaxRecordSize: DefaultMaxRecordSize,
	}
}

// ReadRecord reads a complete RPC record (all fragments) from the underlying reader.
// It returns a copy of the complete record data that is safe for the caller to retain.
func (rm *RecordMarkingReader) ReadRecord() ([]byte, error) {
	rm.fragmentBuf.Reset()
	rm.complete = false

	maxSize := rm.MaxRecordSize
	if maxSize <= 0 {
		maxSize = DefaultMaxRecordSize
	}

	for !rm.complete {
		// Read the 4-byte fragment header
		var header uint32
		if err := binary.Read(rm.r, binary.BigEndian, &header); err != nil {
			return nil, fmt.Errorf("failed to read fragment header: %w", err)
		}

		// Extract last fragment flag and length
		rm.lastFragment = (header & LastFragmentFlag) != 0
		fragmentLen := header & ^uint32(LastFragmentFlag)

		// Validate fragment length
		if fragmentLen > MaxFragmentSize {
			return nil, fmt.Errorf("fragment length %d exceeds maximum %d", fragmentLen, MaxFragmentSize)
		}

		// Check total accumulated size before reading more data
		if rm.fragmentBuf.Len()+int(fragmentLen) > maxSize {
			return nil, fmt.Errorf("record size %d exceeds maximum %d", rm.fragmentBuf.Len()+int(fragmentLen), maxSize)
		}

		// Read the fragment data
		if fragmentLen > 0 {
			fragment := make([]byte, fragmentLen)
			if _, err := io.ReadFull(rm.r, fragment); err != nil {
				return nil, fmt.Errorf("failed to read fragment data: %w", err)
			}
			rm.fragmentBuf.Write(fragment)
		}

		if rm.lastFragment {
			rm.complete = true
		}
	}

	// Return a copy of the buffer to avoid exposing the internal buffer slice
	data := rm.fragmentBuf.Bytes()
	result := make([]byte, len(data))
	copy(result, data)
	return result, nil
}

// RecordMarkingWriter wraps a writer to add RPC record marking.
type RecordMarkingWriter struct {
	w           io.Writer
	maxFragment int
	mu          sync.Mutex
}

// NewRecordMarkingWriter creates a new record marking writer
func NewRecordMarkingWriter(w io.Writer) *RecordMarkingWriter {
	return &RecordMarkingWriter{
		w:           w,
		maxFragment: DefaultMaxFragmentSize,
	}
}

// NewRecordMarkingWriterWithSize creates a new record marking writer with custom max fragment size
func NewRecordMarkingWriterWithSize(w io.Writer, maxFragment int) *RecordMarkingWriter {
	if maxFragment <= 0 || maxFragment > MaxFragmentSize {
		maxFragment = DefaultMaxFragmentSize
	}
	return &RecordMarkingWriter{
		w:           w,
		maxFragment: maxFragment,
	}
}

// WriteRecord writes a complete RPC record with record marking.
// For records larger than maxFragment, multiple fragments are sent.
// An empty data slice emits a single last-fragment header with length 0.
func (rm *RecordMarkingWriter) WriteRecord(data []byte) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Handle empty data: emit a last-fragment header with length 0
	if len(data) == 0 {
		header := uint32(0) | LastFragmentFlag
		if err := binary.Write(rm.w, binary.BigEndian, header); err != nil {
			return fmt.Errorf("failed to write fragment header: %w", err)
		}
		return nil
	}

	remaining := len(data)
	offset := 0

	for remaining > 0 {
		// Determine fragment size
		fragmentLen := remaining
		if fragmentLen > rm.maxFragment {
			fragmentLen = rm.maxFragment
		}

		// Build fragment header
		header := uint32(fragmentLen)
		if remaining == fragmentLen {
			// This is the last fragment
			header |= LastFragmentFlag
		}

		// Write fragment header
		if err := binary.Write(rm.w, binary.BigEndian, header); err != nil {
			return fmt.Errorf("failed to write fragment header: %w", err)
		}

		// Write fragment data
		if _, err := rm.w.Write(data[offset : offset+fragmentLen]); err != nil {
			return fmt.Errorf("failed to write fragment data: %w", err)
		}

		offset += fragmentLen
		remaining -= fragmentLen
	}

	return nil
}

// RecordMarkingConn wraps a connection to provide record marking semantics
type RecordMarkingConn struct {
	reader *RecordMarkingReader
	writer *RecordMarkingWriter
}

// NewRecordMarkingConn creates a new record marking connection wrapper
func NewRecordMarkingConn(r io.Reader, w io.Writer) *RecordMarkingConn {
	return &RecordMarkingConn{
		reader: NewRecordMarkingReader(r),
		writer: NewRecordMarkingWriter(w),
	}
}

// ReadRecord reads a complete RPC record
func (c *RecordMarkingConn) ReadRecord() ([]byte, error) {
	return c.reader.ReadRecord()
}

// WriteRecord writes a complete RPC record
func (c *RecordMarkingConn) WriteRecord(data []byte) error {
	return c.writer.WriteRecord(data)
}
