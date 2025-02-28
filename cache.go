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
func (c *AttrCache) Get(path string) *NFSAttrs {
	c.mu.RLock()
	cached, ok := c.cache[path]
	if ok && time.Now().Before(cached.expireAt) {
		c.mu.RUnlock()
		
		// Update access log (LRU tracking)
		c.mu.Lock()
		c.updateAccessLog(path)
		c.mu.Unlock()
		
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

// ReadAheadBuffer implements a simple read-ahead buffer
type ReadAheadBuffer struct {
	mu      sync.RWMutex
	data    []byte
	dataLen int
	offset  int64
	path    string
}

// NewReadAheadBuffer creates a new read-ahead buffer
func NewReadAheadBuffer(size int) *ReadAheadBuffer {
	return &ReadAheadBuffer{
		data: make([]byte, size),
	}
}

// Fill fills the buffer with data from the given offset
func (b *ReadAheadBuffer) Fill(path string, data []byte, offset int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.path = path
	b.offset = offset
	// Only copy up to buffer capacity
	b.dataLen = len(data)
	if b.dataLen > len(b.data) {
		b.dataLen = len(b.data)
	}
	copy(b.data[:b.dataLen], data)
}

// Read attempts to read from the buffer
func (b *ReadAheadBuffer) Read(path string, offset int64, count int) ([]byte, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if path != b.path {
		return nil, false
	}

	// Return empty slice for reads beyond EOF
	if offset >= b.offset+int64(b.dataLen) {
		return []byte{}, true
	}

	// Return nil for reads before buffer start
	if offset < b.offset {
		return nil, false
	}

	start := int(offset - b.offset)
	if start >= b.dataLen {
		return []byte{}, true
	}

	end := start + count
	if end > b.dataLen {
		end = b.dataLen
	}

	// Return exact number of bytes available
	result := make([]byte, end-start)
	copy(result, b.data[start:end])
	return result, true
}

// Clear clears the buffer
func (b *ReadAheadBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.path = ""
	b.offset = 0
}
