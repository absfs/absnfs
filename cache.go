package absnfs

import (
	"container/list"
	"sync"
	"time"
)

// AttrCache provides caching for file attributes
type AttrCache struct {
	mu         sync.RWMutex
	cache      map[string]*CachedAttrs
	ttl        time.Duration
	maxSize    int        // Maximum number of entries in the cache
	accessList *list.List // Doubly-linked list for O(1) LRU tracking
}

// CachedAttrs represents cached file attributes with expiration
type CachedAttrs struct {
	attrs       *NFSAttrs
	expireAt    time.Time
	listElement *list.Element // Reference to position in LRU list for O(1) access
}

// NewAttrCache creates a new attribute cache with the specified TTL and maximum size
func NewAttrCache(ttl time.Duration, maxSize int) *AttrCache {
	if maxSize <= 0 {
		maxSize = 10000 // Default size if invalid
	}

	return &AttrCache{
		cache:      make(map[string]*CachedAttrs),
		ttl:        ttl,
		maxSize:    maxSize,
		accessList: list.New(),
	}
}

// Get retrieves cached attributes if they exist and are not expired
func (c *AttrCache) Get(path string, server ...*AbsfsNFS) *NFSAttrs {
	var s *AbsfsNFS
	if len(server) > 0 {
		s = server[0]
	}
	
	c.mu.RLock()
	cached, ok := c.cache[path]
	if ok && time.Now().Before(cached.expireAt) {
		// Copy attributes while holding RLock to prevent data races
		attrs := &NFSAttrs{
			Mode:  cached.attrs.Mode,
			Size:  cached.attrs.Size,
			Mtime: cached.attrs.Mtime,
			Atime: cached.attrs.Atime,
			Uid:   cached.attrs.Uid,
			Gid:   cached.attrs.Gid,
		}
		c.mu.RUnlock()

		// Update access log (LRU tracking)
		c.mu.Lock()
		// Revalidate that entry still exists before updating access log
		// This prevents race condition where entry could be deleted between locks
		if _, stillExists := c.cache[path]; stillExists {
			c.updateAccessLog(path)
		}
		c.mu.Unlock()

		// Record cache hit for metrics
		if s != nil {
			s.RecordAttrCacheHit()

			// Log cache hit if debug logging is enabled
			if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.Level == "debug" {
				s.structuredLogger.Debug("attribute cache hit",
					LogField{Key: "path", Value: path})
			}
		}

		return attrs
	}
	c.mu.RUnlock()

	// Record cache miss for metrics
	if s != nil {
		s.RecordAttrCacheMiss()

		// Log cache miss if debug logging is enabled
		if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.Level == "debug" {
			s.structuredLogger.Debug("attribute cache miss",
				LogField{Key: "path", Value: path})
		}
	}

	if ok {
		// Expired entry, remove it
		c.mu.Lock()
		delete(c.cache, path)
		c.removeFromAccessLog(path)
		c.mu.Unlock()
	}
	return nil
}

// updateAccessLog moves the path to the front of the access list (most recently used)
// This is now O(1) using doubly-linked list operations
func (c *AttrCache) updateAccessLog(path string) {
	cached, ok := c.cache[path]
	if !ok {
		return
	}

	if cached.listElement != nil {
		// Move existing element to front - O(1)
		c.accessList.MoveToFront(cached.listElement)
	} else {
		// Add new element to front - O(1)
		cached.listElement = c.accessList.PushFront(path)
	}
}

// removeFromAccessLog removes a path from the access list
// This is now O(1) using doubly-linked list operations
func (c *AttrCache) removeFromAccessLog(path string) {
	cached, ok := c.cache[path]
	if !ok || cached.listElement == nil {
		return
	}

	// Remove element from list - O(1)
	c.accessList.Remove(cached.listElement)
	cached.listElement = nil
}

// Put adds or updates cached attributes
func (c *AttrCache) Put(path string, attrs *NFSAttrs) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if entry already exists
	existing, exists := c.cache[path]

	// Check if we need to evict entries to make room
	if len(c.cache) >= c.maxSize && !exists {
		// Need to evict the least recently used entry - O(1) using list.Back()
		if c.accessList.Len() > 0 {
			// Get LRU element from back of list - O(1)
			lruElement := c.accessList.Back()
			if lruElement != nil {
				lruPath := lruElement.Value.(string)
				delete(c.cache, lruPath)
				c.accessList.Remove(lruElement) // O(1)
			}
		}
	}

	// Deep copy the attributes to prevent modification
	attrsCopy := &NFSAttrs{
		Mode:  attrs.Mode,
		Size:  attrs.Size,
		Mtime: attrs.Mtime,
		Atime: attrs.Atime,
		Uid:   attrs.Uid,
		Gid:   attrs.Gid,
	}

	// Preserve the listElement reference when updating existing entry
	var listElem *list.Element
	if exists && existing != nil {
		listElem = existing.listElement
	}

	c.cache[path] = &CachedAttrs{
		attrs:       attrsCopy,
		expireAt:    time.Now().Add(c.ttl),
		listElement: listElem,
	}

	// Update access log to mark this as most recently used - O(1)
	c.updateAccessLog(path)
}

// Invalidate removes an entry from the cache
func (c *AttrCache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.cache, path)
	c.removeFromAccessLog(path)
}

// Clear removes all entries from the cache
func (c *AttrCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*CachedAttrs)
	c.accessList = list.New()
}

// Size returns the current number of entries in the cache
func (c *AttrCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return len(c.cache)
}

// MaxSize returns the maximum size of the cache
func (c *AttrCache) MaxSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return c.maxSize
}

// Stats returns the current size and capacity of the cache
func (c *AttrCache) Stats() (int, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return len(c.cache), c.maxSize
}

// FileBuffer represents a read-ahead buffer for a specific file
type FileBuffer struct {
	data        []byte
	dataLen     int
	offset      int64
	path        string
	lastUse     time.Time
	listElement *list.Element // Reference to position in LRU list for O(1) access
}

// ReadAheadBuffer implements a multi-file read-ahead buffer with memory management
type ReadAheadBuffer struct {
	mu           sync.RWMutex
	buffers      map[string]*FileBuffer
	bufferSize   int
	accessList   *list.List // Doubly-linked list for O(1) LRU tracking
	maxFiles     int        // Maximum number of files that can have buffers
	maxMemory    int64      // Maximum total memory for all buffers
	currentUsage int64      // Current memory usage
}

// NewReadAheadBuffer creates a new read-ahead buffer with specified size and limits
func NewReadAheadBuffer(size int) *ReadAheadBuffer {
	return &ReadAheadBuffer{
		buffers:    make(map[string]*FileBuffer),
		bufferSize: size,
		maxFiles:   100,       // Default, will be updated in Configure
		maxMemory:  104857600, // Default 100MB, will be updated in Configure
		accessList: list.New(),
	}
}

// Configure sets the configuration options for the read-ahead buffer
func (b *ReadAheadBuffer) Configure(maxFiles int, maxMemory int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	b.maxFiles = maxFiles
	b.maxMemory = maxMemory
	
	// If current usage exceeds new limits, evict buffers
	b.enforceMemoryLimits()
}

// Size returns the current memory usage of all read-ahead buffers
func (b *ReadAheadBuffer) Size() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	return b.currentUsage
}

// Stats returns the number of files and memory usage of the read-ahead buffer
func (b *ReadAheadBuffer) Stats() (int, int64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	return len(b.buffers), b.currentUsage
}

// enforceMemoryLimits ensures that memory usage stays within configured limits
// Must be called with lock held
func (b *ReadAheadBuffer) enforceMemoryLimits() {
	// We need to free up at least one slot for a new buffer
	// Enforce file count limit
	for (len(b.buffers) >= b.maxFiles || b.currentUsage+int64(b.bufferSize) > b.maxMemory) && b.accessList.Len() > 0 {
		// If we're at or above limits, we need to evict the LRU buffer
		if b.accessList.Len() == 0 {
			break // No buffers to evict
		}

		// Get least recently used buffer from back of list - O(1)
		lruElement := b.accessList.Back()
		if lruElement != nil {
			lruPath := lruElement.Value.(string)
			b.evictBuffer(lruPath)
		}
	}
}

// evictBuffer removes a buffer for the specified path
// Must be called with lock held
func (b *ReadAheadBuffer) evictBuffer(path string) {
	buffer, exists := b.buffers[path]
	if !exists {
		return
	}

	// Update memory usage
	b.currentUsage -= int64(cap(buffer.data))

	// Remove from buffers map
	delete(b.buffers, path)

	// Remove from access list - O(1)
	if buffer.listElement != nil {
		b.accessList.Remove(buffer.listElement)
		buffer.listElement = nil
	}
}

// updateAccessOrder moves a path to the front of the access order list
// Must be called with lock held
// This is now O(1) using doubly-linked list operations
func (b *ReadAheadBuffer) updateAccessOrder(path string) {
	buffer, ok := b.buffers[path]
	if !ok {
		return
	}

	if buffer.listElement != nil {
		// Move existing element to front - O(1)
		b.accessList.MoveToFront(buffer.listElement)
	} else {
		// Add new element to front - O(1)
		buffer.listElement = b.accessList.PushFront(path)
	}
}

// Fill fills the buffer for a file with data from the given offset
func (b *ReadAheadBuffer) Fill(path string, data []byte, offset int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	buffer, exists := b.buffers[path]
	if !exists {
		// Make sure we have room by enforcing limits before adding a new buffer
		// This ensures we never exceed our maximum limits
		if len(b.buffers) >= b.maxFiles || b.currentUsage+int64(b.bufferSize) > b.maxMemory {
			// Need to evict at least one buffer
			b.enforceMemoryLimits()
		}
		
		// Create new buffer
		buffer = &FileBuffer{
			data: make([]byte, b.bufferSize),
			path: path,
		}
		b.buffers[path] = buffer
		b.currentUsage += int64(b.bufferSize)
	}

	buffer.offset = offset
	buffer.lastUse = time.Now()
	
	// Only copy up to buffer capacity
	buffer.dataLen = len(data)
	if buffer.dataLen > len(buffer.data) {
		buffer.dataLen = len(buffer.data)
	}
	copy(buffer.data[:buffer.dataLen], data)
	
	// Update access order
	b.updateAccessOrder(path)
}

// Read attempts to read from the buffer for a file
func (b *ReadAheadBuffer) Read(path string, offset int64, count int, server ...*AbsfsNFS) ([]byte, bool) {
	var s *AbsfsNFS
	if len(server) > 0 {
		s = server[0]
	}
	
	b.mu.RLock()
	buffer, exists := b.buffers[path]
	if !exists {
		b.mu.RUnlock()

		// Record cache miss in metrics
		if s != nil {
			s.RecordReadAheadMiss()

			// Log cache miss if debug logging is enabled
			if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.Level == "debug" {
				s.structuredLogger.Debug("read-ahead cache miss",
					LogField{Key: "path", Value: path},
					LogField{Key: "offset", Value: offset},
					LogField{Key: "count", Value: count})
			}
		}

		return nil, false
	}
	
	// Check if buffer has the requested data
	// Special case: handle reads that are exactly at the end of the buffer
	if offset == buffer.offset+int64(buffer.dataLen) {
		b.mu.RUnlock()
		
		// Record cache hit in metrics
		if s != nil {
			s.RecordReadAheadHit()
		}
		
		return []byte{}, true // Empty result indicates EOF
	}

	if offset < buffer.offset || offset > buffer.offset+int64(buffer.dataLen) {
		b.mu.RUnlock()

		// Record cache miss in metrics
		if s != nil {
			s.RecordReadAheadMiss()

			// Log cache miss if debug logging is enabled
			if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.Level == "debug" {
				s.structuredLogger.Debug("read-ahead cache miss: out of range",
					LogField{Key: "path", Value: path},
					LogField{Key: "offset", Value: offset},
					LogField{Key: "buffer_offset", Value: buffer.offset},
					LogField{Key: "buffer_len", Value: buffer.dataLen})
			}
		}

		return nil, false
	}
	
	// Calculate start and end positions in buffer
	start := int(offset - buffer.offset)
	if start >= buffer.dataLen {
		b.mu.RUnlock()
		
		// Record cache hit in metrics
		if s != nil {
			s.RecordReadAheadHit()
		}
		
		return []byte{}, true
	}
	
	end := start + count
	if end > buffer.dataLen {
		end = buffer.dataLen
	}
	
	// Copy data from buffer
	result := make([]byte, end-start)
	copy(result, buffer.data[start:end])
	b.mu.RUnlock()
	
	// Update access time and order (requires write lock)
	b.mu.Lock()
	if buff, ok := b.buffers[path]; ok {
		buff.lastUse = time.Now()
		b.updateAccessOrder(path)
	}
	b.mu.Unlock()
	
	// Record cache hit in metrics
	if s != nil {
		s.RecordReadAheadHit()

		// Log cache hit if debug logging is enabled
		if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.Level == "debug" {
			s.structuredLogger.Debug("read-ahead cache hit",
				LogField{Key: "path", Value: path},
				LogField{Key: "offset", Value: offset},
				LogField{Key: "bytes_read", Value: len(result)})
		}
	}

	return result, true
}

// Clear clears all buffers
func (b *ReadAheadBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buffers = make(map[string]*FileBuffer)
	b.accessList = list.New()
	b.currentUsage = 0
}

// ClearPath clears the buffer for a specific path
func (b *ReadAheadBuffer) ClearPath(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	b.evictBuffer(path)
}