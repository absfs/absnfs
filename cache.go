// cache.go: Attribute and directory caching.
//
// Provides two cache types -- AttrCache (LRU file attributes with TTL)
// and DirCache (LRU directory listings) -- each with configurable size
// limits and eviction.
package absnfs

import (
	"container/list"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// AttrCache provides caching for file attributes and negative lookups
type AttrCache struct {
	mu             sync.RWMutex
	cache          map[string]*CachedAttrs
	ttl            time.Duration
	negativeTTL    time.Duration // TTL for negative cache entries
	maxSize        int           // Maximum number of entries in the cache
	accessList     *list.List    // Doubly-linked list for O(1) LRU tracking
	enableNegative bool          // Enable negative caching
}

// CachedAttrs represents cached file attributes with expiration
// When attrs is nil, this represents a negative cache entry (file not found)
type CachedAttrs struct {
	attrs       *NFSAttrs
	expireAt    time.Time
	listElement *list.Element // Reference to position in LRU list for O(1) access
	isNegative  bool          // True if this is a negative cache entry
}

// NewAttrCache creates a new attribute cache with the specified TTL and maximum size
func NewAttrCache(ttl time.Duration, maxSize int) *AttrCache {
	if maxSize <= 0 {
		maxSize = 10000 // Default size if invalid
	}

	return &AttrCache{
		cache:          make(map[string]*CachedAttrs),
		ttl:            ttl,
		negativeTTL:    5 * time.Second, // Default negative cache TTL
		maxSize:        maxSize,
		accessList:     list.New(),
		enableNegative: false, // Disabled by default
	}
}

// ConfigureNegativeCaching configures negative lookup caching
func (c *AttrCache) ConfigureNegativeCaching(enable bool, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.enableNegative = enable
	if ttl > 0 {
		c.negativeTTL = ttl
	}
}

// Get retrieves cached attributes if they exist and are not expired.
// Returns:
//   - (attrs, true) = positive cache hit (attrs found)
//   - (nil, true)   = negative cache hit (path confirmed non-existent)
//   - (nil, false)  = cache miss (not in cache at all)
func (c *AttrCache) Get(path string, server ...*AbsfsNFS) (*NFSAttrs, bool) {
	var s *AbsfsNFS
	if len(server) > 0 {
		s = server[0]
	}

	c.mu.RLock()
	cached, ok := c.cache[path]
	if ok && time.Now().Before(cached.expireAt) {
		// Handle negative cache entry
		if cached.isNegative {
			c.mu.RUnlock()

			// Update access log (LRU tracking)
			c.mu.Lock()
			if _, stillExists := c.cache[path]; stillExists {
				c.updateAccessLog(path)
			}
			c.mu.Unlock()

			// Record negative cache hit for metrics
			if s != nil {
				s.RecordNegativeCacheHit()

				// Log negative cache hit if debug logging is enabled
				if logger := s.getStructuredLogger(); logger != nil && s.tuning.Load().Log != nil && s.tuning.Load().Log.Level == "debug" {
					logger.Debug("negative cache hit",
						LogField{Key: "path", Value: path})
				}
			}

			// Return (nil, true) to indicate negative cache hit
			return nil, true
		}

		// Copy attributes while holding RLock to prevent data races
		attrs := &NFSAttrs{
			Mode:   cached.attrs.Mode,
			Size:   cached.attrs.Size,
			FileId: cached.attrs.FileId,
			Uid:    cached.attrs.Uid,
			Gid:    cached.attrs.Gid,
		}
		attrs.SetMtime(cached.attrs.Mtime())
		attrs.SetAtime(cached.attrs.Atime())
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
			if logger := s.getStructuredLogger(); logger != nil && s.tuning.Load().Log != nil && s.tuning.Load().Log.Level == "debug" {
				logger.Debug("attribute cache hit",
					LogField{Key: "path", Value: path})
			}
		}

		return attrs, true
	}
	c.mu.RUnlock()

	// Record cache miss for metrics
	if s != nil {
		s.RecordAttrCacheMiss()

		// Log cache miss if debug logging is enabled
		if logger := s.getStructuredLogger(); logger != nil && s.tuning.Load().Log != nil && s.tuning.Load().Log.Level == "debug" {
			logger.Debug("attribute cache miss",
				LogField{Key: "path", Value: path})
		}
	}

	if ok {
		// Expired entry, remove it with re-check after lock upgrade
		c.mu.Lock()
		if entry, exists := c.cache[path]; exists && time.Now().After(entry.expireAt) {
			c.removeFromAccessLog(path)
			delete(c.cache, path)
		}
		c.mu.Unlock()
	}
	return nil, false
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
				lruPath, ok := lruElement.Value.(string)
				if ok {
					delete(c.cache, lruPath)
				}
				c.accessList.Remove(lruElement) // O(1)
			}
		}
	}

	// Deep copy the attributes to prevent modification
	attrsCopy := &NFSAttrs{
		Mode:   attrs.Mode,
		Size:   attrs.Size,
		FileId: attrs.FileId,
		Uid:    attrs.Uid,
		Gid:    attrs.Gid,
	}
	attrsCopy.SetMtime(attrs.Mtime())
	attrsCopy.SetAtime(attrs.Atime())

	// Preserve the listElement reference when updating existing entry
	var listElem *list.Element
	if exists && existing != nil {
		listElem = existing.listElement
	}

	c.cache[path] = &CachedAttrs{
		attrs:       attrsCopy,
		expireAt:    time.Now().Add(c.ttl),
		listElement: listElem,
		isNegative:  false,
	}

	// Update access log to mark this as most recently used - O(1)
	c.updateAccessLog(path)
}

// PutNegative adds a negative cache entry (file not found)
func (c *AttrCache) PutNegative(path string) {
	// Only store negative entries if enabled
	c.mu.RLock()
	enabled := c.enableNegative
	negativeTTL := c.negativeTTL
	c.mu.RUnlock()

	if !enabled {
		return
	}

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
				lruPath, ok := lruElement.Value.(string)
				if ok {
					delete(c.cache, lruPath)
				}
				c.accessList.Remove(lruElement) // O(1)
			}
		}
	}

	// Preserve the listElement reference when updating existing entry
	var listElem *list.Element
	if exists && existing != nil {
		listElem = existing.listElement
	}

	c.cache[path] = &CachedAttrs{
		attrs:       nil, // No attributes for negative entry
		expireAt:    time.Now().Add(negativeTTL),
		listElement: listElem,
		isNegative:  true,
	}

	// Update access log to mark this as most recently used - O(1)
	c.updateAccessLog(path)
}

// Invalidate removes an entry from the cache
func (c *AttrCache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.removeFromAccessLog(path)
	delete(c.cache, path)
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

// NegativeStats returns the count of negative cache entries
func (c *AttrCache) NegativeStats() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	count := 0
	for _, cached := range c.cache {
		if cached.isNegative {
			count++
		}
	}
	return count
}

// InvalidateNegativeInDir invalidates all negative cache entries in a directory
// This is called when a file is created in the directory
func (c *AttrCache) InvalidateNegativeInDir(dirPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Find all negative entries that are children of this directory
	toDelete := make([]string, 0)
	for path, cached := range c.cache {
		if cached.isNegative && isChildOf(path, dirPath) {
			toDelete = append(toDelete, path)
		}
	}

	// Delete the negative entries
	// Remove from access log BEFORE deleting from cache, because
	// removeFromAccessLog looks up the entry in c.cache to find the list element.
	for _, path := range toDelete {
		c.removeFromAccessLog(path)
		delete(c.cache, path)
	}
}

// isChildOf checks if path is a direct child of dirPath (one level deep only)
func isChildOf(path, dirPath string) bool {
	// Normalize paths
	var remainder string
	if dirPath == "/" {
		// For root, the remainder is everything after "/"
		if path == "/" || len(path) < 2 {
			return false
		}
		remainder = path[1:]
	} else {
		// Check if path starts with dirPath followed by a separator
		if len(path) <= len(dirPath)+1 {
			return false
		}

		// Path must start with dirPath
		if path[:len(dirPath)] != dirPath {
			return false
		}

		// Must be followed by a separator
		if path[len(dirPath)] != '/' {
			return false
		}

		remainder = path[len(dirPath)+1:]
	}

	// Direct child: remainder must not contain any more separators
	for i := 0; i < len(remainder); i++ {
		if remainder[i] == '/' {
			return false
		}
	}

	return len(remainder) > 0
}

// Resize changes the maximum size of the attribute cache
// If the new size is smaller than current entries, LRU entries will be evicted
func (c *AttrCache) Resize(newSize int) {
	if newSize <= 0 {
		newSize = 10000 // Default size if invalid
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Only resize if the size actually changed
	if c.maxSize == newSize {
		return
	}

	c.maxSize = newSize

	// If the new size is smaller than current entries, evict LRU entries
	for len(c.cache) > c.maxSize && c.accessList.Len() > 0 {
		lruElement := c.accessList.Back()
		if lruElement == nil {
			break
		}
		lruPath, ok := lruElement.Value.(string)
		if ok {
			delete(c.cache, lruPath)
		}
		c.accessList.Remove(lruElement)
	}
}

// UpdateTTL changes the time-to-live for cached attributes
// This affects new entries and does not retroactively change existing entries
func (c *AttrCache) UpdateTTL(newTTL time.Duration) {
	if newTTL <= 0 {
		newTTL = 5 * time.Second // Default TTL if invalid
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.ttl = newTTL
}

// DirCache provides caching for directory entries
type DirCache struct {
	entries    map[string]*CachedDirEntry
	accessList *list.List
	mu         sync.RWMutex
	timeout    time.Duration
	maxEntries int
	maxDirSize int
	hits       uint64
	misses     uint64
}

// CachedDirEntry represents cached directory entries with expiration
type CachedDirEntry struct {
	entries     []os.FileInfo
	validUntil  time.Time
	listElement *list.Element
}

// NewDirCache creates a new directory cache with the specified timeout and limits
func NewDirCache(timeout time.Duration, maxEntries int, maxDirSize int) *DirCache {
	if maxEntries <= 0 {
		maxEntries = 1000 // Default: 1000 directories
	}
	if maxDirSize <= 0 {
		maxDirSize = 10000 // Default: 10000 entries per directory
	}
	if timeout <= 0 {
		timeout = 10 * time.Second // Default: 10 seconds
	}

	return &DirCache{
		entries:    make(map[string]*CachedDirEntry),
		accessList: list.New(),
		timeout:    timeout,
		maxEntries: maxEntries,
		maxDirSize: maxDirSize,
	}
}

// Get retrieves cached directory entries if they exist and are not expired
func (c *DirCache) Get(path string) ([]os.FileInfo, bool) {
	c.mu.RLock()
	cached, ok := c.entries[path]
	if !ok {
		c.mu.RUnlock()
		atomic.AddUint64(&c.misses, 1)
		return nil, false
	}

	// Check if expired
	if time.Now().After(cached.validUntil) {
		c.mu.RUnlock()
		atomic.AddUint64(&c.misses, 1)

		// Remove expired entry with re-check after lock upgrade
		c.mu.Lock()
		if entry, exists := c.entries[path]; exists && time.Now().After(entry.validUntil) {
			c.removeFromAccessList(path)
			delete(c.entries, path)
		}
		c.mu.Unlock()

		return nil, false
	}

	// Make a copy of the entries to prevent modification
	entries := make([]os.FileInfo, len(cached.entries))
	copy(entries, cached.entries)
	c.mu.RUnlock()

	// Update access log (LRU tracking)
	c.mu.Lock()
	// Revalidate that entry still exists before updating access log
	if _, stillExists := c.entries[path]; stillExists {
		c.updateAccessLog(path)
	}
	c.mu.Unlock()

	atomic.AddUint64(&c.hits, 1)
	return entries, true
}

// Put adds or updates cached directory entries
func (c *DirCache) Put(path string, entries []os.FileInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Don't cache directories that exceed the maximum size
	if len(entries) > c.maxDirSize {
		return
	}

	// Check if entry already exists
	existing, exists := c.entries[path]

	// Check if we need to evict entries to make room
	if len(c.entries) >= c.maxEntries && !exists {
		// Need to evict the least recently used entry
		if c.accessList.Len() > 0 {
			lruElement := c.accessList.Back()
			if lruElement != nil {
				lruPath, ok := lruElement.Value.(string)
				if ok {
					delete(c.entries, lruPath)
				}
				c.accessList.Remove(lruElement)
			}
		}
	}

	// Make a copy of the entries to prevent external modification
	entriesCopy := make([]os.FileInfo, len(entries))
	copy(entriesCopy, entries)

	// Preserve the listElement reference when updating existing entry
	var listElem *list.Element
	if exists && existing != nil {
		listElem = existing.listElement
	}

	c.entries[path] = &CachedDirEntry{
		entries:     entriesCopy,
		validUntil:  time.Now().Add(c.timeout),
		listElement: listElem,
	}

	// Update access log to mark this as most recently used
	c.updateAccessLog(path)
}

// updateAccessLog moves the path to the front of the access list (most recently used)
func (c *DirCache) updateAccessLog(path string) {
	cached, ok := c.entries[path]
	if !ok {
		return
	}

	if cached.listElement != nil {
		c.accessList.MoveToFront(cached.listElement)
	} else {
		cached.listElement = c.accessList.PushFront(path)
	}
}

// removeFromAccessList removes a path from the access list
func (c *DirCache) removeFromAccessList(path string) {
	cached, ok := c.entries[path]
	if !ok || cached.listElement == nil {
		return
	}

	c.accessList.Remove(cached.listElement)
	cached.listElement = nil
}

// Invalidate removes an entry from the cache
func (c *DirCache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.removeFromAccessList(path)
	delete(c.entries, path)
}

// Clear removes all entries from the cache
func (c *DirCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*CachedDirEntry)
	c.accessList = list.New()
}

// Size returns the current number of entries in the cache
func (c *DirCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}

// Stats returns entries count, hits, and misses
func (c *DirCache) Stats() (int, int64, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries), int64(atomic.LoadUint64(&c.hits)), int64(atomic.LoadUint64(&c.misses))
}

// Resize changes the maximum number of entries in the directory cache
// If the new size is smaller than current entries, LRU entries will be evicted
func (c *DirCache) Resize(newMaxEntries int) {
	if newMaxEntries <= 0 {
		newMaxEntries = 1000 // Default size if invalid
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Only resize if the size actually changed
	if c.maxEntries == newMaxEntries {
		return
	}

	c.maxEntries = newMaxEntries

	// If the new size is smaller than current entries, evict LRU entries
	for len(c.entries) > c.maxEntries && c.accessList.Len() > 0 {
		lruElement := c.accessList.Back()
		if lruElement == nil {
			break
		}
		lruPath, ok := lruElement.Value.(string)
		if ok {
			delete(c.entries, lruPath)
		}
		c.accessList.Remove(lruElement)
	}
}

// UpdateTTL changes the timeout duration for directory cache entries
// This affects new entries and does not retroactively change existing entries
func (c *DirCache) UpdateTTL(newTimeout time.Duration) {
	if newTimeout <= 0 {
		newTimeout = 10 * time.Second // Default timeout if invalid
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.timeout = newTimeout
}
