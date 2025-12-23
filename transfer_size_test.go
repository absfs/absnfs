package absnfs

import (
	"testing"

	"github.com/absfs/memfs"
)

func TestTransferSize(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test file with some content
	testData := make([]byte, 1024*1024) // 1MB of data
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	f, err := fs.Create("/testfile")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	_, err = f.Write(testData)
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}
	f.Close()

	// Test cases for different transfer sizes
	testCases := []struct {
		name         string
		transferSize int
		readSize     int64
		expected     int
	}{
		{"Small transfer size", 1024, 2048, 1024},        // TransferSize limits read
		{"Medium transfer size", 4096, 2048, 2048},       // Read request is smaller than limit
		{"Large transfer size", 1024 * 1024, 2048, 2048}, // Read request is much smaller than limit
		{"Zero transfer size", 0, 2048, 2048},            // Default 64K used when 0
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create server with the specified transfer size
			options := ExportOptions{
				TransferSize: tc.transferSize,
			}
			server, err := New(fs, options)
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}

			// Verify transfer size was set correctly
			if tc.transferSize == 0 && server.options.TransferSize != 65536 {
				t.Errorf("Default transfer size not set correctly: got %d, want %d",
					server.options.TransferSize, 65536)
			} else if tc.transferSize > 0 && server.options.TransferSize != tc.transferSize {
				t.Errorf("Transfer size not set correctly: got %d, want %d",
					server.options.TransferSize, tc.transferSize)
			}

			// Get the root node
			node, err := server.Lookup("/testfile")
			if err != nil {
				t.Fatalf("Failed to lookup test file: %v", err)
			}

			// Perform read operation
			data, err := server.Read(node, 0, tc.readSize)
			if err != nil {
				t.Fatalf("Read operation failed: %v", err)
			}

			// Check that read size respects transfer size limit
			if len(data) != tc.expected {
				t.Errorf("Read data length incorrect: got %d, want %d", len(data), tc.expected)
			}

			// Verify data content
			for i := 0; i < len(data); i++ {
				if data[i] != testData[i] {
					t.Errorf("Data mismatch at index %d: got %d, want %d", i, data[i], testData[i])
					break
				}
			}
		})
	}
}

func TestWriteTransferSize(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create empty test file
	f, err := fs.Create("/writefile")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	// Test cases for different transfer sizes
	testCases := []struct {
		name         string
		transferSize int
		writeSize    int
		expected     int64
	}{
		{"Small transfer size", 1024, 2048, 1024},        // TransferSize limits write
		{"Medium transfer size", 4096, 2048, 2048},       // Write request is smaller than limit
		{"Large transfer size", 1024 * 1024, 2048, 2048}, // Write request is much smaller than limit
		{"Zero transfer size", 0, 2048, 2048},            // Default 64K used when 0
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create server with the specified transfer size
			options := ExportOptions{
				TransferSize: tc.transferSize,
			}
			server, err := New(fs, options)
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}

			// Get the root node
			node, err := server.Lookup("/writefile")
			if err != nil {
				t.Fatalf("Failed to lookup test file: %v", err)
			}

			// Create write data
			writeData := make([]byte, tc.writeSize)
			for i := range writeData {
				writeData[i] = byte(i % 256)
			}

			// Perform write operation
			written, err := server.Write(node, 0, writeData)
			if err != nil {
				t.Fatalf("Write operation failed: %v", err)
			}

			// Check that write size respects transfer size limit
			if written != tc.expected {
				t.Errorf("Write length incorrect: got %d, want %d", written, tc.expected)
			}

			// Verify file content
			readFile, err := fs.Open("/writefile")
			if err != nil {
				t.Fatalf("Failed to open file for verification: %v", err)
			}
			readData := make([]byte, tc.writeSize)
			n, err := readFile.Read(readData)
			if err != nil {
				t.Fatalf("Failed to read file for verification: %v", err)
			}
			readFile.Close()

			// Check that the correct amount was written
			if int64(n) != tc.expected {
				t.Errorf("File size incorrect: got %d, want %d", n, tc.expected)
			}

			// Verify data was written correctly
			for i := 0; i < int(tc.expected); i++ {
				if readData[i] != writeData[i] {
					t.Errorf("Data mismatch at index %d: got %d, want %d", i, readData[i], writeData[i])
					break
				}
			}
		})
	}
}
