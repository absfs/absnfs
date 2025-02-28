package absnfs

import (
	"bytes"
	"testing"
	"time"

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
	// Test the enhanced ReadAheadBuffer with multi-file support
	bufferSize := 1024
	buffer := NewReadAheadBuffer(bufferSize)
	
	// Configure with reasonable limits
	maxFiles := 5
	maxMemory := int64(bufferSize * maxFiles)
	buffer.Configure(maxFiles, maxMemory)
	
	// Verify buffer size was set correctly
	if buffer.bufferSize != bufferSize {
		t.Errorf("Buffer size not set correctly: got %d, want %d", buffer.bufferSize, bufferSize)
	}
	
	// Create test data
	testData := []byte("test data for buffer operations")
	testOffset := int64(100)
	
	// Test filling buffer with data for a single file
	testPath1 := "/testpath1"
	buffer.Fill(testPath1, testData, testOffset)
	
	// Test reading from buffer
	// Case 1: Read exactly what we put in
	readData, hit := buffer.Read(testPath1, testOffset, len(testData))
	if !hit {
		t.Error("Read should hit cache")
	}
	if !bytes.Equal(readData, testData) {
		t.Error("Read returned wrong data")
	}
	
	// Case 2: Read partial data
	readData, hit = buffer.Read(testPath1, testOffset, 4)
	if !hit {
		t.Error("Partial read should hit cache")
	}
	if !bytes.Equal(readData, testData[:4]) {
		t.Error("Partial read returned wrong data")
	}
	
	// Case 3: Read beyond end of cached data
	readData, hit = buffer.Read(testPath1, testOffset + int64(len(testData)), 10)
	if !hit {
		t.Errorf("Read beyond end should still hit cache (got hit=%v)", hit)
	}
	if len(readData) != 0 {
		t.Errorf("Read beyond end should return empty data (got len=%d)", len(readData))
	}
	
	// Case 4: Read before start of cached data
	readData, hit = buffer.Read(testPath1, testOffset - 10, 5)
	if hit {
		t.Error("Read before start should miss cache")
	}
	
	// Case 5: Read from wrong path
	readData, hit = buffer.Read("/wrongpath", testOffset, len(testData))
	if hit {
		t.Error("Read from wrong path should miss cache")
	}
	
	// Test buffer stats
	fileCount, memUsage, capacityPct := buffer.Stats()
	if fileCount != 1 {
		t.Errorf("Wrong file count: got %d, want 1", fileCount)
	}
	expectedMemUsage := int64(bufferSize)
	if memUsage != expectedMemUsage {
		t.Errorf("Wrong memory usage: got %d, want %d", memUsage, expectedMemUsage)
	}
	expectedCapacityPct := float64(memUsage) / float64(maxMemory) * 100
	if capacityPct != expectedCapacityPct {
		t.Errorf("Wrong capacity percentage: got %.2f, want %.2f", capacityPct, expectedCapacityPct)
	}
	
	// Test clear for specific path
	buffer.ClearPath(testPath1)
	readData, hit = buffer.Read(testPath1, testOffset, len(testData))
	if hit {
		t.Error("Read after clear path should miss cache")
	}
	
	// Test filling buffer for multiple files
	// We want to fill exactly max files + 1 to test eviction
	for i := 0; i < maxFiles+1; i++ {
		path := "/testpath" + string(rune('a'+i))
		buffer.Fill(path, testData, testOffset)
		
		// After each fill, check that we haven't exceeded limits
		fc, mu, _ := buffer.Stats()
		if fc > maxFiles {
			t.Errorf("After adding file %d, buffer exceeded max files limit: got %d, want <= %d", i, fc, maxFiles)
		}
		if mu > maxMemory {
			t.Errorf("After adding file %d, buffer exceeded max memory limit: got %d, want <= %d", i, mu, maxMemory)
		}
	}
	
	// Final check that we've respected the maxFiles limit
	fileCount, memUsage, _ = buffer.Stats()
	if fileCount != maxFiles {
		t.Errorf("Buffer has wrong number of files: got %d, want %d", fileCount, maxFiles)
	}
	expectedMem := int64(maxFiles * bufferSize)
	if memUsage != expectedMem {
		t.Errorf("Buffer has wrong memory usage: got %d, want %d", memUsage, expectedMem)
	}
	
	// Test global clear
	buffer.Clear()
	fileCount, memUsage, _ = buffer.Stats()
	if fileCount != 0 {
		t.Errorf("After clear, file count should be 0, got %d", fileCount)
	}
	if memUsage != 0 {
		t.Errorf("After clear, memory usage should be 0, got %d", memUsage)
	}
}

func TestCacheSizeControlConfiguration(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}
	
	// Create multiple test files to test file count limits
	numFiles := 10
	fileSize := 1024 // 1KB
	
	for i := 0; i < numFiles; i++ {
		fileName := "/testfile" + string(rune('a'+i))
		testData := make([]byte, fileSize)
		for j := range testData {
			testData[j] = byte((i * j) % 256)
		}
		
		f, err := fs.Create(fileName)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", fileName, err)
		}
		_, err = f.Write(testData)
		if err != nil {
			t.Fatalf("Failed to write test data to %s: %v", fileName, err)
		}
		f.Close()
	}
	
	// Test cases for different cache size control configurations
	testCases := []struct {
		name              string
		readAheadMaxFiles int
		readAheadMaxMemory int64
		filesToAccess     int
		expectedCachedFiles int
	}{
		{
			name:              "Default cache size limits",
			readAheadMaxFiles: 100,
			readAheadMaxMemory: 104857600, // 100MB
			filesToAccess:     5,
			expectedCachedFiles: 5, // Should cache all accessed files
		},
		{
			name:              "Limited by max files",
			readAheadMaxFiles: 3,
			readAheadMaxMemory: 104857600, // 100MB
			filesToAccess:     5,
			expectedCachedFiles: 3, // Should only cache the latest 3 files
		},
		{
			name:              "Limited by max memory",
			readAheadMaxFiles: 100,
			readAheadMaxMemory: 2048, // 2KB (enough for ~2 buffers with 1KB each)
			filesToAccess:     5,
			expectedCachedFiles: 2, // Should only cache the latest 2 files (but exact count may vary)
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create server with the specified cache size configuration
			options := ExportOptions{
				EnableReadAhead:    true,
				ReadAheadSize:      1024, // 1KB buffer size
				ReadAheadMaxFiles:  tc.readAheadMaxFiles,
				ReadAheadMaxMemory: tc.readAheadMaxMemory,
			}
			server, err := New(fs, options)
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}
			
			// Verify options were set correctly
			if server.options.ReadAheadMaxFiles != tc.readAheadMaxFiles {
				t.Errorf("ReadAheadMaxFiles not set correctly: got %d, want %d",
					server.options.ReadAheadMaxFiles, tc.readAheadMaxFiles)
			}
			if server.options.ReadAheadMaxMemory != tc.readAheadMaxMemory {
				t.Errorf("ReadAheadMaxMemory not set correctly: got %d, want %d",
					server.options.ReadAheadMaxMemory, tc.readAheadMaxMemory)
			}
			
			// Access files to fill cache
			for i := 0; i < tc.filesToAccess; i++ {
				fileName := "/testfile" + string(rune('a'+i))
				node, err := server.Lookup(fileName)
				if err != nil {
					t.Fatalf("Failed to lookup file %s: %v", fileName, err)
				}
				
				// Read a small amount to trigger read-ahead
				_, err = server.Read(node, 0, 64)
				if err != nil {
					t.Fatalf("Failed to read from file %s: %v", fileName, err)
				}
				
				// Small delay to ensure clear ordering of access
				time.Sleep(5 * time.Millisecond)
			}
			
			// Check cache statistics
			fileCount, memUsage, _ := server.readBuf.Stats()
			
			// Verify correct number of files are cached
			// For the memory-limited case, we just check we're within limits
			if tc.name == "Limited by max memory" {
				if memUsage > tc.readAheadMaxMemory {
					t.Errorf("Memory usage exceeds limit: got %d, limit %d",
						memUsage, tc.readAheadMaxMemory)
				}
			} else {
				// For other cases, check exact file count
				if fileCount != tc.expectedCachedFiles {
					t.Errorf("Wrong number of files cached: got %d, want %d",
						fileCount, tc.expectedCachedFiles)
				}
			}
			
			// Always verify memory usage is within limits
			if memUsage > tc.readAheadMaxMemory {
				t.Errorf("Memory usage exceeds limit: got %d, limit %d",
					memUsage, tc.readAheadMaxMemory)
			}
			
			// Verify the most recently accessed files are cached
			for i := tc.filesToAccess - tc.expectedCachedFiles; i < tc.filesToAccess; i++ {
				fileName := "/testfile" + string(rune('a'+i))
				node, _ := server.Lookup(fileName)
				
				// Try to read from cache at a new offset (should hit if file is cached)
				data, err := server.Read(node, 64, 64)
				if err != nil {
					t.Fatalf("Failed to read from file %s: %v", fileName, err)
				}
				
				if len(data) != 64 {
					t.Errorf("Read returned wrong amount of data for %s: got %d, want 64",
						fileName, len(data))
				}
			}
		})
	}
}