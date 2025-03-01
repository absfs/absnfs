package absnfs

import (
	"sync"
	"time"
)

// AttrCache provides caching for file attributes
type AttrCache struct {
	mu        sync.RWMutex
	cache     map[string]*CachedAttrs
	ttl       time.Duration
	maxSize   int           // Maximum number of entries in the cache
	accessLog []string      // Keep track of least recently used entries
}

// CachedAttrs represents cached file attributes with expiration
type CachedAttrs struct {
	attrs    *NFSAttrs
	expireAt time.Time
}

// NewAttrCache creates a new attribute cache with the specified TTL and maximum size
func NewAttrCache(ttl time.Duration, maxSize int) *AttrCache {
	if maxSize <= 0 {
		maxSize = 10000 // Default size if invalid
	}
	
	return &AttrCache{
		cache:     make(map[string]*CachedAttrs),
		ttl:       ttl,
		maxSize:   maxSize,
		accessLog: make([]string, 0, maxSize),
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
		c.mu.RUnlock()
		
		// Update access log (LRU tracking)
		c.mu.Lock()
		c.updateAccessLog(path)
		c.mu.Unlock()
		
		// Record cache hit for metrics
		if s != nil {
			s.RecordAttrCacheHit()
		}
		
		// Return a copy to prevent modification of cached data
		attrs := &NFSAttrs{
			Mode:  cached.attrs.Mode,
			Size:  cached.attrs.Size,
			Mtime: cached.attrs.Mtime,
			Atime: cached.attrs.Atime,
			Uid:   cached.attrs.Uid,
			Gid:   cached.attrs.Gid,
		}
		return attrs
	}
	c.mu.RUnlock()

	// Record cache miss for metrics
	if s != nil {
		s.RecordAttrCacheMiss()
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

// updateAccessLog moves the path to the front of the access log (most recently used)
func (c *AttrCache) updateAccessLog(path string) {
	// Remove from current position if it exists
	c.removeFromAccessLog(path)
	
	// Add to the front (most recently used)
	c.accessLog = append([]string{path}, c.accessLog...)
}

// removeFromAccessLog removes a path from the access log
func (c *AttrCache) removeFromAccessLog(path string) {
	for i, p := range c.accessLog {
		if p == path {
			// Remove the item from the slice
			c.accessLog = append(c.accessLog[:i], c.accessLog[i+1:]...)
			break
		}
	}
}

// Put adds or updates cached attributes
func (c *AttrCache) Put(path string, attrs *NFSAttrs) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we need to evict entries to make room
	if len(c.cache) >= c.maxSize && c.cache[path] == nil {
		// Need to evict the least recently used entry
		if len(c.accessLog) > 0 {
			lruPath := c.accessLog[len(c.accessLog)-1]
			delete(c.cache, lruPath)
			c.accessLog = c.accessLog[:len(c.accessLog)-1]
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
	c.cache[path] = &CachedAttrs{
		attrs:    attrsCopy,
		expireAt: time.Now().Add(c.ttl),
	}
	
	// Update access log to mark this as most recently used
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
	c.accessLog = make([]string, 0, c.maxSize)
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
	data    []byte
	dataLen int
	offset  int64
	path    string
	lastUse time.Time
}

// ReadAheadBuffer implements a multi-file read-ahead buffer with memory management
type ReadAheadBuffer struct {
	mu           sync.RWMutex
	buffers      map[string]*FileBuffer
	bufferSize   int
	accessOrder  []string        // LRU tracking
	maxFiles     int             // Maximum number of files that can have buffers
	maxMemory    int64           // Maximum total memory for all buffers
	currentUsage int64           // Current memory usage
}

// NewReadAheadBuffer creates a new read-ahead buffer with specified size and limits
func NewReadAheadBuffer(size int) *ReadAheadBuffer {
	return &ReadAheadBuffer{
		buffers:    make(map[string]*FileBuffer),
		bufferSize: size,
		maxFiles:   100,          // Default, will be updated in Configure
		maxMemory:  104857600,    // Default 100MB, will be updated in Configure
		accessOrder: make([]string, 0, 100),
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
	for (len(b.buffers) >= b.maxFiles || b.currentUsage+int64(b.bufferSize) > b.maxMemory) && len(b.accessOrder) > 0 {
		// If we're at or above limits, we need to evict the LRU buffer
		if len(b.accessOrder) == 0 {
			break // No buffers to evict
		}
		
		// Remove least recently used buffer
		lruPath := b.accessOrder[len(b.accessOrder)-1]
		b.evictBuffer(lruPath)
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
	
	// Remove from access order
	for i, p := range b.accessOrder {
		if p == path {
			b.accessOrder = append(b.accessOrder[:i], b.accessOrder[i+1:]...)
			break
		}
	}
}

// updateAccessOrder moves a path to the front of the access order list
// Must be called with lock held
func (b *ReadAheadBuffer) updateAccessOrder(path string) {
	// Remove from current position if exists
	for i, p := range b.accessOrder {
		if p == path {
			b.accessOrder = append(b.accessOrder[:i], b.accessOrder[i+1:]...)
			break
		}
	}
	
	// Add to front (most recently used)
	b.accessOrder = append([]string{path}, b.accessOrder...)
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
	}
	
	return result, true
}

// Clear clears all buffers
func (b *ReadAheadBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buffers = make(map[string]*FileBuffer)
	b.accessOrder = make([]string, 0, b.maxFiles)
	b.currentUsage = 0
}

// ClearPath clears the buffer for a specific path
func (b *ReadAheadBuffer) ClearPath(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	b.evictBuffer(path)
}