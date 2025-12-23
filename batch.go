package absnfs

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"time"
)

// BatchType identifies the type of operations in a batch
type BatchType int

const (
	BatchTypeRead BatchType = iota
	BatchTypeWrite
	BatchTypeGetAttr
	BatchTypeSetAttr
	BatchTypeDirRead
)

// BatchRequest represents a single operation in a batch
type BatchRequest struct {
	Type       BatchType         // Type of operation
	FileHandle uint64            // File handle for the operation
	Offset     int64             // Offset for read/write operations
	Length     int               // Length for read/write operations
	Data       []byte            // Data for write operations
	Time       time.Time         // Time the request was added to the batch
	ResultChan chan *BatchResult // Channel to send results back to caller
	Context    context.Context   // Context for cancellation
}

// BatchResult represents the result of a batched operation
type BatchResult struct {
	Data   []byte // Data for read operations
	Error  error  // Error if any occurred
	Status uint32 // NFS status code
}

// Batch represents a group of similar operations that can be processed together
type Batch struct {
	Type      BatchType       // Type of operations in this batch
	Requests  []*BatchRequest // Requests in this batch
	MaxSize   int             // Maximum number of requests in this batch
	ReadyTime time.Time       // Time when this batch should be processed
	mu        sync.Mutex      // Mutex for thread safety
}

// BatchProcessor manages the batching of operations
type BatchProcessor struct {
	enabled   bool                 // Whether batching is enabled
	maxSize   int                  // Maximum batch size
	delay     time.Duration        // Maximum delay before processing a batch
	batches   map[BatchType]*Batch // Active batches by type
	processor *AbsfsNFS            // Reference to the NFS server
	mu        sync.Mutex           // Mutex for thread safety
	ctx       context.Context      // Context for cancellation
	cancel    context.CancelFunc   // Function to cancel the context
	wg        sync.WaitGroup       // Wait group for processing goroutines
}

// NewBatchProcessor creates a new batch processor
func NewBatchProcessor(nfs *AbsfsNFS, maxSize int) *BatchProcessor {
	ctx, cancel := context.WithCancel(context.Background())

	bp := &BatchProcessor{
		enabled:   nfs.options.BatchOperations,
		maxSize:   maxSize,
		delay:     10 * time.Millisecond, // 10ms max delay for batching
		batches:   make(map[BatchType]*Batch),
		processor: nfs,
		ctx:       ctx,
		cancel:    cancel,
	}

	// Initialize empty batches for each type
	bp.batches[BatchTypeRead] = &Batch{Type: BatchTypeRead, MaxSize: maxSize}
	bp.batches[BatchTypeWrite] = &Batch{Type: BatchTypeWrite, MaxSize: maxSize}
	bp.batches[BatchTypeGetAttr] = &Batch{Type: BatchTypeGetAttr, MaxSize: maxSize}
	bp.batches[BatchTypeSetAttr] = &Batch{Type: BatchTypeSetAttr, MaxSize: maxSize}
	bp.batches[BatchTypeDirRead] = &Batch{Type: BatchTypeDirRead, MaxSize: maxSize}

	// Start batch processing goroutine
	bp.wg.Add(1)
	go bp.processBatches()

	return bp
}

// AddRequest adds a request to a batch
// Returns (added, triggered):
//   - added: true if the request was added to a batch, false if caller should handle individually
//   - triggered: true if batch processing was triggered (only meaningful when added=true)
func (bp *BatchProcessor) AddRequest(req *BatchRequest) (added bool, triggered bool) {
	// If batching is disabled, return immediately to process individually
	if !bp.enabled {
		return false, false
	}

	bp.mu.Lock()
	defer bp.mu.Unlock()

	batch, exists := bp.batches[req.Type]
	if !exists {
		// Unknown batch type, return false to process individually
		return false, false
	}

	batch.mu.Lock()

	// If this is the first request in the batch, set the ready time
	if len(batch.Requests) == 0 {
		batch.ReadyTime = time.Now().Add(bp.delay)
	}

	// Add the request to the batch
	batch.Requests = append(batch.Requests, req)

	// If the batch is full, trigger immediate processing
	if len(batch.Requests) >= batch.MaxSize {
		// Create a new empty batch
		bp.batches[req.Type] = &Batch{
			Type:    req.Type,
			MaxSize: batch.MaxSize,
		}

		// Unlock the old batch before passing to goroutine to prevent deadlock
		batch.mu.Unlock()

		// Process the full batch asynchronously with wait group tracking
		bp.wg.Add(1)
		go func(b *Batch) {
			defer bp.wg.Done()
			bp.processBatch(b)
		}(batch)
		return true, true
	}

	batch.mu.Unlock()
	return true, false
}

// processBatches is a background goroutine that processes batches when they're ready
func (bp *BatchProcessor) processBatches() {
	defer bp.wg.Done()

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-bp.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()

			bp.mu.Lock()
			for typ, batch := range bp.batches {
				batch.mu.Lock()

				// Process batch if it's ready and has requests
				if len(batch.Requests) > 0 && now.After(batch.ReadyTime) {
					// Create a new empty batch
					bp.batches[typ] = &Batch{
						Type:    typ,
						MaxSize: batch.MaxSize,
					}

					// Process the ready batch asynchronously with wait group tracking
					bp.wg.Add(1)
					go func(b *Batch) {
						defer bp.wg.Done()
						bp.processBatch(b)
					}(batch)
				}

				batch.mu.Unlock()
			}
			bp.mu.Unlock()
		}
	}
}

// processBatch handles the actual processing of a batch of operations
func (bp *BatchProcessor) processBatch(batch *Batch) {
	switch batch.Type {
	case BatchTypeRead:
		bp.processReadBatch(batch)
	case BatchTypeWrite:
		bp.processWriteBatch(batch)
	case BatchTypeGetAttr:
		bp.processGetAttrBatch(batch)
	case BatchTypeSetAttr:
		bp.processSetAttrBatch(batch)
	case BatchTypeDirRead:
		bp.processDirReadBatch(batch)
	}
}

// processReadBatch processes a batch of read operations
func (bp *BatchProcessor) processReadBatch(batch *Batch) {
	// Group requests by file handle
	fileGroups := make(map[uint64][]*BatchRequest)
	for _, req := range batch.Requests {
		fileGroups[req.FileHandle] = append(fileGroups[req.FileHandle], req)
	}

	// Process each file's requests as a group
	for fileHandle, requests := range fileGroups {
		// Sort requests by offset (can optimize to read larger contiguous chunks)
		// For simplicity, we're just processing them as grouped but separate reads
		file, ok := bp.processor.fileMap.Get(fileHandle)
		if !ok {
			// File not found, send error to all requests
			for _, req := range requests {
				req.ResultChan <- &BatchResult{
					Error:  os.ErrNotExist,
					Status: NFSERR_NOENT,
				}
				close(req.ResultChan)
			}
			continue
		}

		// Process each read request
		for _, req := range requests {
			// Check if the request has been cancelled
			select {
			case <-req.Context.Done():
				// Request was cancelled
				req.ResultChan <- &BatchResult{
					Error:  req.Context.Err(),
					Status: NFSERR_IO,
				}
				close(req.ResultChan)
				continue
			default:
				// Process the request
			}

			// Perform the actual read operation
			buffer := make([]byte, req.Length)
			bytesRead, err := file.ReadAt(buffer, req.Offset)

			if err != nil && err != io.EOF {
				req.ResultChan <- &BatchResult{
					Error:  err,
					Status: NFSERR_IO,
				}
				close(req.ResultChan)
				continue
			}

			// Send successful result
			req.ResultChan <- &BatchResult{
				Data:   buffer[:bytesRead],
				Status: NFS_OK,
			}
			close(req.ResultChan)
		}
	}
}

// processWriteBatch processes a batch of write operations
func (bp *BatchProcessor) processWriteBatch(batch *Batch) {
	// Group requests by file handle
	fileGroups := make(map[uint64][]*BatchRequest)
	for _, req := range batch.Requests {
		fileGroups[req.FileHandle] = append(fileGroups[req.FileHandle], req)
	}

	// Process each file's requests as a group
	for fileHandle, requests := range fileGroups {
		// Get the file
		file, ok := bp.processor.fileMap.Get(fileHandle)
		if !ok {
			// File not found, send error to all requests
			for _, req := range requests {
				req.ResultChan <- &BatchResult{
					Error:  os.ErrNotExist,
					Status: NFSERR_NOENT,
				}
				close(req.ResultChan)
			}
			continue
		}

		// Check if the server is in read-only mode
		if bp.processor.options.ReadOnly {
			for _, req := range requests {
				req.ResultChan <- &BatchResult{
					Error:  os.ErrPermission,
					Status: NFSERR_ROFS,
				}
				close(req.ResultChan)
			}
			continue
		}

		// Process each write request
		for _, req := range requests {
			// Check if the request has been cancelled
			select {
			case <-req.Context.Done():
				// Request was cancelled
				req.ResultChan <- &BatchResult{
					Error:  req.Context.Err(),
					Status: NFSERR_IO,
				}
				close(req.ResultChan)
				continue
			default:
				// Process the request
			}

			// Perform the actual write operation
			_, err := file.WriteAt(req.Data, req.Offset)

			if err != nil {
				req.ResultChan <- &BatchResult{
					Error:  err,
					Status: NFSERR_IO,
				}
				close(req.ResultChan)
				continue
			}

			// Send successful result
			req.ResultChan <- &BatchResult{
				Data:   nil, // No data for write operations
				Status: NFS_OK,
			}
			close(req.ResultChan)

			// Invalidate attribute cache for written file
			path := ""
			if node, ok := file.(*NFSNode); ok {
				path = node.path
				bp.processor.attrCache.Invalidate(path)
			}
		}
	}
}

// processGetAttrBatch processes a batch of getattr operations
func (bp *BatchProcessor) processGetAttrBatch(batch *Batch) {
	for _, req := range batch.Requests {
		// Check if the request has been cancelled
		select {
		case <-req.Context.Done():
			// Request was cancelled
			req.ResultChan <- &BatchResult{
				Error:  req.Context.Err(),
				Status: NFSERR_IO,
			}
			close(req.ResultChan)
			continue
		default:
			// Process the request
		}

		// Get the file
		file, ok := bp.processor.fileMap.Get(req.FileHandle)
		if !ok {
			req.ResultChan <- &BatchResult{
				Error:  os.ErrNotExist,
				Status: NFSERR_NOENT,
			}
			close(req.ResultChan)
			continue
		}

		// Get file attributes
		var attrs *NFSAttrs
		path := ""

		if node, ok := file.(*NFSNode); ok {
			path = node.path
			// Try to get attributes from cache first
			attrs = bp.processor.attrCache.Get(path, bp.processor)
		}

		// If not in cache or not an NFSNode, get attributes directly
		if attrs == nil {
			info, err := file.Stat()
			if err != nil {
				req.ResultChan <- &BatchResult{
					Error:  err,
					Status: NFSERR_IO,
				}
				close(req.ResultChan)
				continue
			}

			// Create attributes
			modTime := info.ModTime()
			attrs = &NFSAttrs{
				Mode: info.Mode(),
				Size: info.Size(),
				Uid:  0, // Default uid
				Gid:  0, // Default gid
			}
			attrs.SetMtime(modTime)
			attrs.SetAtime(modTime) // Use ModTime as Atime since absfs doesn't expose Atime

			// Cache the attributes if path is available
			if path != "" {
				bp.processor.attrCache.Put(path, attrs)
			}
		}

		// Encode attributes into a buffer for the result
		var buf bytes.Buffer
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			req.ResultChan <- &BatchResult{
				Error:  err,
				Status: NFSERR_IO,
			}
			close(req.ResultChan)
			continue
		}

		// Send successful result
		req.ResultChan <- &BatchResult{
			Data:   buf.Bytes(),
			Status: NFS_OK,
		}
		close(req.ResultChan)
	}
}

// processSetAttrBatch processes a batch of setattr operations
// This is a simplified implementation, as setattr is more complex in practice
func (bp *BatchProcessor) processSetAttrBatch(batch *Batch) {
	for _, req := range batch.Requests {
		// Check if the request has been cancelled
		select {
		case <-req.Context.Done():
			// Request was cancelled
			req.ResultChan <- &BatchResult{
				Error:  req.Context.Err(),
				Status: NFSERR_IO,
			}
			close(req.ResultChan)
			continue
		default:
			// Process the request
		}

		// Get the file
		file, ok := bp.processor.fileMap.Get(req.FileHandle)
		if !ok {
			req.ResultChan <- &BatchResult{
				Error:  os.ErrNotExist,
				Status: NFSERR_NOENT,
			}
			close(req.ResultChan)
			continue
		}

		// In a real implementation, we would parse req.Data to get the attributes to set
		// and then apply them to the file. For now, we'll just invalidate the cache.

		// Invalidate attribute cache for this file
		if node, ok := file.(*NFSNode); ok {
			bp.processor.attrCache.Invalidate(node.path)
		}

		// Send successful result
		req.ResultChan <- &BatchResult{
			Status: NFS_OK,
		}
		close(req.ResultChan)
	}
}

// processDirReadBatch processes a batch of directory read operations
func (bp *BatchProcessor) processDirReadBatch(batch *Batch) {
	for _, req := range batch.Requests {
		// Check if the request has been cancelled
		select {
		case <-req.Context.Done():
			// Request was cancelled
			req.ResultChan <- &BatchResult{
				Error:  req.Context.Err(),
				Status: NFSERR_IO,
			}
			close(req.ResultChan)
			continue
		default:
			// Process the request
		}

		// Get the directory
		dir, ok := bp.processor.fileMap.Get(req.FileHandle)
		if !ok {
			req.ResultChan <- &BatchResult{
				Error:  os.ErrNotExist,
				Status: NFSERR_NOENT,
			}
			close(req.ResultChan)
			continue
		}

		// Verify this is a directory
		info, err := dir.Stat()
		if err != nil {
			req.ResultChan <- &BatchResult{
				Error:  err,
				Status: NFSERR_IO,
			}
			close(req.ResultChan)
			continue
		}

		if !info.IsDir() {
			req.ResultChan <- &BatchResult{
				Error:  os.ErrInvalid,
				Status: NFSERR_NOTDIR,
			}
			close(req.ResultChan)
			continue
		}

		// In a real implementation, we would read the directory entries
		// and format them according to the NFS protocol. For this example,
		// we'll just return a successful status.

		// Send successful result
		req.ResultChan <- &BatchResult{
			Status: NFS_OK,
		}
		close(req.ResultChan)
	}
}

// Stop stops the batch processor
func (bp *BatchProcessor) Stop() {
	bp.cancel()
	bp.wg.Wait()
}

// BatchRead submits a read request to be batched
// Returns the read data, NFS status code, and error (error is last per Go convention)
func (bp *BatchProcessor) BatchRead(ctx context.Context, fileHandle uint64, offset int64, length int) ([]byte, uint32, error) {
	// If batching is disabled, return immediately to process individually
	if !bp.enabled {
		return nil, 0, nil
	}

	// Create result channel
	resultChan := make(chan *BatchResult, 1)

	// Create request
	req := &BatchRequest{
		Type:       BatchTypeRead,
		FileHandle: fileHandle,
		Offset:     offset,
		Length:     length,
		Time:       time.Now(),
		ResultChan: resultChan,
		Context:    ctx,
	}

	// Add to batch
	added, _ := bp.AddRequest(req)

	// If not added to batch, caller should handle individually
	if !added {
		close(resultChan)
		return nil, 0, nil
	}

	// Request was added to batch - wait for result (even if triggered immediately)
	select {
	case <-ctx.Done():
		return nil, NFSERR_IO, ctx.Err()
	case res, ok := <-resultChan:
		if !ok {
			return nil, NFSERR_IO, os.ErrInvalid
		}
		return res.Data, res.Status, res.Error
	}
}

// BatchWrite submits a write request to be batched
// Returns NFS status code and error (error is last per Go convention)
func (bp *BatchProcessor) BatchWrite(ctx context.Context, fileHandle uint64, offset int64, data []byte) (uint32, error) {
	// If batching is disabled, return immediately to process individually
	if !bp.enabled {
		return 0, nil
	}

	// Create result channel
	resultChan := make(chan *BatchResult, 1)

	// Create request
	req := &BatchRequest{
		Type:       BatchTypeWrite,
		FileHandle: fileHandle,
		Offset:     offset,
		Length:     len(data),
		Data:       data,
		Time:       time.Now(),
		ResultChan: resultChan,
		Context:    ctx,
	}

	// Add to batch
	added, _ := bp.AddRequest(req)

	// If not added to batch, caller should handle individually
	if !added {
		close(resultChan)
		return 0, nil
	}

	// Request was added to batch - wait for result (even if triggered immediately)
	select {
	case <-ctx.Done():
		return NFSERR_IO, ctx.Err()
	case res, ok := <-resultChan:
		if !ok {
			return NFSERR_IO, os.ErrInvalid
		}
		return res.Status, res.Error
	}
}

// BatchGetAttr submits a getattr request to be batched
// Returns the attributes, NFS status code, and error (error is last per Go convention)
func (bp *BatchProcessor) BatchGetAttr(ctx context.Context, fileHandle uint64) ([]byte, uint32, error) {
	// If batching is disabled, return immediately to process individually
	if !bp.enabled {
		return nil, 0, nil
	}

	// Create result channel
	resultChan := make(chan *BatchResult, 1)

	// Create request
	req := &BatchRequest{
		Type:       BatchTypeGetAttr,
		FileHandle: fileHandle,
		Time:       time.Now(),
		ResultChan: resultChan,
		Context:    ctx,
	}

	// Add to batch
	added, _ := bp.AddRequest(req)

	// If not added to batch, caller should handle individually
	if !added {
		close(resultChan)
		return nil, 0, nil
	}

	// Request was added to batch - wait for result (even if triggered immediately)
	select {
	case <-ctx.Done():
		return nil, NFSERR_IO, ctx.Err()
	case res, ok := <-resultChan:
		if !ok {
			return nil, NFSERR_IO, os.ErrInvalid
		}
		return res.Data, res.Status, res.Error
	}
}

// GetStats returns statistics about the batch processor
func (bp *BatchProcessor) GetStats() (enabled bool, batchesByType map[BatchType]int) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	batchesByType = make(map[BatchType]int)

	for typ, batch := range bp.batches {
		batch.mu.Lock()
		batchesByType[typ] = len(batch.Requests)
		batch.mu.Unlock()
	}

	return bp.enabled, batchesByType
}
