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
)

// RecordMarkingReader wraps a reader to handle RPC record marking.
// RPC over TCP uses "record marking" where each message is preceded
// by a 4-byte header containing the fragment length and last-fragment flag.
type RecordMarkingReader struct {
	r            io.Reader
	fragmentBuf  *bytes.Buffer
	lastFragment bool
	complete     bool // true when we've read a complete record
}

// NewRecordMarkingReader creates a new record marking reader
func NewRecordMarkingReader(r io.Reader) *RecordMarkingReader {
	return &RecordMarkingReader{
		r:           r,
		fragmentBuf: new(bytes.Buffer),
	}
}

// ReadRecord reads a complete RPC record (all fragments) from the underlying reader.
// It returns the complete record data.
func (rm *RecordMarkingReader) ReadRecord() ([]byte, error) {
	rm.fragmentBuf.Reset()
	rm.complete = false

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

	return rm.fragmentBuf.Bytes(), nil
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
func (rm *RecordMarkingWriter) WriteRecord(data []byte) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

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
