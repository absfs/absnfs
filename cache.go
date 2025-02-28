package absnfs

import (
	"sync"
	"time"
)

// AttrCache provides caching for file attributes
type AttrCache struct {
	mu    sync.RWMutex
	cache map[string]*CachedAttrs
	ttl   time.Duration
}

// CachedAttrs represents cached file attributes with expiration
type CachedAttrs struct {
	attrs    *NFSAttrs
	expireAt time.Time
}

// NewAttrCache creates a new attribute cache with the specified TTL
func NewAttrCache(ttl time.Duration) *AttrCache {
	return &AttrCache{
		cache: make(map[string]*CachedAttrs),
		ttl:   ttl,
	}
}

// Get retrieves cached attributes if they exist and are not expired
func (c *AttrCache) Get(path string) *NFSAttrs {
	c.mu.RLock()
	cached, ok := c.cache[path]
	if ok && time.Now().Before(cached.expireAt) {
		// Return a copy to prevent modification of cached data
		attrs := &NFSAttrs{
			Mode:  cached.attrs.Mode,
			Size:  cached.attrs.Size,
			Mtime: cached.attrs.Mtime,
			Atime: cached.attrs.Atime,
			Uid:   cached.attrs.Uid,
			Gid:   cached.attrs.Gid,
		}
		c.mu.RUnlock()
		return attrs
	}
	c.mu.RUnlock()

	if ok {
		// Expired entry, remove it
		c.mu.Lock()
		delete(c.cache, path)
		c.mu.Unlock()
	}
	return nil
}

// Put adds or updates cached attributes
func (c *AttrCache) Put(path string, attrs *NFSAttrs) {
	c.mu.Lock()
	defer c.mu.Unlock()

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
}

// Invalidate removes an entry from the cache
func (c *AttrCache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.cache, path)
}

// Clear removes all entries from the cache
func (c *AttrCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*CachedAttrs)
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
