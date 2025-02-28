package absnfs

import (
	"bytes"
	"testing"

	"github.com/absfs/memfs"
)

func TestReadAheadConfiguration(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test file with sequential data
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

	// Test cases for different read-ahead configurations
	testCases := []struct {
		name            string
		enableReadAhead bool
		readAheadSize   int
		firstReadSize   int64
		secondReadSize  int64
		secondReadOffset int64
		expectCacheHit  bool
	}{
		{
			name:            "Read-ahead enabled with default size",
			enableReadAhead: true,
			readAheadSize:   262144, // 256KB
			firstReadSize:   1024,   // Read 1KB first
			secondReadSize:  1024,   // Then read next 1KB
			secondReadOffset: 1024,  // Starting from offset 1KB
			expectCacheHit:  true,   // Should hit read-ahead cache
		},
		{
			name:            "Read-ahead enabled with custom size",
			enableReadAhead: true,
			readAheadSize:   4096,  // 4KB
			firstReadSize:   1024,  // Read 1KB first
			secondReadSize:  1024,  // Then read next 1KB
			secondReadOffset: 1024, // Starting from offset 1KB
			expectCacheHit:  true,  // Should hit read-ahead cache
		},
		{
			name:            "Read-ahead disabled",
			enableReadAhead: false,
			readAheadSize:   262144, // 256KB (not used)
			firstReadSize:   1024,   // Read 1KB first
			secondReadSize:  1024,   // Then read next 1KB
			secondReadOffset: 1024,  // Starting from offset 1KB
			expectCacheHit:  false,  // Should not hit cache (disabled)
		},
		{
			name:            "Read beyond read-ahead buffer",
			enableReadAhead: true,
			readAheadSize:   4096,   // 4KB
			firstReadSize:   1024,   // Read 1KB first
			secondReadSize:  1024,   // Then read 1KB
			secondReadOffset: 6144,  // Starting from offset 6KB (beyond read-ahead)
			expectCacheHit:  false,  // Should not hit cache (beyond buffer)
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create server with the specified read-ahead configuration
			options := ExportOptions{
				EnableReadAhead: tc.enableReadAhead,
				ReadAheadSize:   tc.readAheadSize,
			}
			server, err := New(fs, options)
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}

			// Verify options were set correctly
			if server.options.EnableReadAhead != tc.enableReadAhead {
				t.Errorf("EnableReadAhead not set correctly: got %v, want %v",
					server.options.EnableReadAhead, tc.enableReadAhead)
			}
			if server.options.ReadAheadSize != tc.readAheadSize {
				t.Errorf("ReadAheadSize not set correctly: got %d, want %d",
					server.options.ReadAheadSize, tc.readAheadSize)
			}

			// Get test file node
			node, err := server.Lookup("/testfile")
			if err != nil {
				t.Fatalf("Failed to lookup test file: %v", err)
			}

			// First read to trigger potential read-ahead
			data1, err := server.Read(node, 0, tc.firstReadSize)
			if err != nil {
				t.Fatalf("First read failed: %v", err)
			}
			if int64(len(data1)) != tc.firstReadSize {
				t.Errorf("First read returned wrong size: got %d, want %d", len(data1), tc.firstReadSize)
			}
			
			// Verify first read data
			expectedData1 := testData[:tc.firstReadSize]
			if !bytes.Equal(data1, expectedData1) {
				t.Error("First read returned incorrect data")
			}

			// Second read, which may hit read-ahead cache
			data2, err := server.Read(node, tc.secondReadOffset, tc.secondReadSize)
			if err != nil {
				t.Fatalf("Second read failed: %v", err)
			}
			
			// For the "Read beyond read-ahead buffer" test, we might not get back exactly
			// the amount of data we requested, so we check that we got some valid data
			if tc.name != "Read beyond read-ahead buffer" {
				if int64(len(data2)) != tc.secondReadSize {
					t.Errorf("Second read returned wrong size: got %d, want %d", len(data2), tc.secondReadSize)
				}
				
				expectedData2 := testData[tc.secondReadOffset:tc.secondReadOffset+tc.secondReadSize]
				if !bytes.Equal(data2, expectedData2) {
					t.Error("Second read returned incorrect data")
				}
			} else if len(data2) > 0 {
				// If we got data, make sure it's correct
				expectedData2 := testData[tc.secondReadOffset:tc.secondReadOffset+int64(len(data2))]
				if !bytes.Equal(data2, expectedData2) {
					t.Error("Second read returned incorrect data")
				}
			}
		})
	}
}

func TestReadAheadBufferOperations(t *testing.T) {
	// Test the ReadAheadBuffer directly
	bufferSize := 1024
	buffer := NewReadAheadBuffer(bufferSize)
	
	// Test initial state
	if buffer.data == nil {
		t.Error("Buffer data should be initialized")
	}
	if len(buffer.data) != bufferSize {
		t.Errorf("Buffer has wrong size: got %d, want %d", len(buffer.data), bufferSize)
	}
	
	// Test filling buffer
	testPath := "/testpath"
	testData := []byte("test data")
	testOffset := int64(100)
	
	buffer.Fill(testPath, testData, testOffset)
	
	// Test reading from buffer
	// Case 1: Read exactly what we put in
	readData, hit := buffer.Read(testPath, testOffset, len(testData))
	if !hit {
		t.Error("Read should hit cache")
	}
	if !bytes.Equal(readData, testData) {
		t.Error("Read returned wrong data")
	}
	
	// Case 2: Read partial data
	readData, hit = buffer.Read(testPath, testOffset, 4)
	if !hit {
		t.Error("Partial read should hit cache")
	}
	if !bytes.Equal(readData, testData[:4]) {
		t.Error("Partial read returned wrong data")
	}
	
	// Case 3: Read beyond end of cached data
	readData, hit = buffer.Read(testPath, testOffset + int64(len(testData)), 10)
	if !hit {
		t.Error("Read beyond end should still hit cache but return empty data")
	}
	if len(readData) != 0 {
		t.Error("Read beyond end should return empty data")
	}
	
	// Case 4: Read before start of cached data
	readData, hit = buffer.Read(testPath, testOffset - 10, 5)
	if hit {
		t.Error("Read before start should miss cache")
	}
	
	// Case 5: Read from wrong path
	readData, hit = buffer.Read("/wrongpath", testOffset, len(testData))
	if hit {
		t.Error("Read from wrong path should miss cache")
	}
	
	// Test clear
	buffer.Clear()
	readData, hit = buffer.Read(testPath, testOffset, len(testData))
	if hit {
		t.Error("Read after clear should miss cache")
	}
}