// cache.go: Attribute and directory caching.
//
// Provides two cache types -- AttrCache (LRU file attributes with TTL)
// and DirCache (LRU directory listings) -- each with configurable size
// limits and eviction.
//
// Coherence. Every procedure that changes the filesystem invalidates what
// it changed (invalidate, invalidateTree), so the server's own changes are
// visible to the next request. A reader that consulted the filesystem
// before such an invalidation must not store its result after it: readers
// take the cache's generation before reading and store with putAt, which
// drops the result when anything was invalidated in between. Changes made
// to the filesystem by anything other than this server are not seen until
// the entry's TTL has passed; the TTL in force when an entry is read
// applies, so lowering it also bounds entries stored under the old one.
package absnfs

import (
	"container/list"
	"os"
	"strings"
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
	gen            uint64        // Incremented by every invalidation (see putAt)
}

// CachedAttrs represents cached file attributes with expiration
// When attrs is nil, this represents a negative cache entry (file not found)
type CachedAttrs struct {
	attrs       *NFSAttrs
	storedAt    time.Time
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

// expiry returns when e stops being served under the TTL in force now.
// The caller holds c.mu.
func (c *AttrCache) expiry(e *CachedAttrs) time.Time {
	if e.isNegative {
		return e.storedAt.Add(c.negativeTTL)
	}
	return e.storedAt.Add(c.ttl)
}

// Get retrieves cached attributes if they exist and are not expired. The
// attributes returned are a copy, valid (IsValid) until the entry expires.
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
	live, negative := false, false
	var attrs *NFSAttrs
	if ok {
		expiry := c.expiry(cached)
		live = time.Now().Before(expiry)
		negative = cached.isNegative
		if live && !negative {
			// Copy while holding the lock: the entry may be replaced.
			cp := *cached.attrs
			cp.validUntil = expiry
			attrs = &cp
		}
	}
	c.mu.RUnlock()

	if live {
		// Update access log (LRU tracking) if the entry is still there
		c.mu.Lock()
		if _, stillExists := c.cache[path]; stillExists {
			c.updateAccessLog(path)
		}
		c.mu.Unlock()

		if s != nil {
			if negative {
				s.RecordNegativeCacheHit()
			} else {
				s.RecordAttrCacheHit()
			}
			s.logCacheEvent("attribute cache hit", path, negative)
		}
		return attrs, true
	}

	// Record cache miss for metrics
	if s != nil {
		s.RecordAttrCacheMiss()
		s.logCacheEvent("attribute cache miss", path, false)
	}

	if ok {
		// Expired entry, remove it with re-check after lock upgrade
		c.mu.Lock()
		if entry, exists := c.cache[path]; exists && !time.Now().Before(c.expiry(entry)) {
			c.removeFromAccessLog(path)
			delete(c.cache, path)
		}
		c.mu.Unlock()
	}
	return nil, false
}

// logCacheEvent logs an attribute cache hit or miss at debug level.
func (s *AbsfsNFS) logCacheEvent(msg, path string, negative bool) {
	logger := s.getStructuredLogger()
	if logger == nil {
		return
	}
	if tuning := s.tuning.Load(); tuning.Log == nil || tuning.Log.Level != "debug" {
		return
	}
	if negative {
		msg = "negative cache hit"
	}
	logger.Debug(msg, LogField{Key: "path", Value: path})
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

// evictLRU removes the least recently used entry. The caller holds c.mu.
func (c *AttrCache) evictLRU() {
	lru := c.accessList.Back()
	if lru == nil {
		return
	}
	if lruPath, ok := lru.Value.(string); ok {
		delete(c.cache, lruPath)
	}
	c.accessList.Remove(lru)
}

// store adds or replaces the entry for path. nil attrs records that path
// does not exist. The caller holds c.mu.
func (c *AttrCache) store(path string, attrs *NFSAttrs) {
	existing, exists := c.cache[path]
	if !exists && len(c.cache) >= c.maxSize {
		c.evictLRU()
	}

	entry := &CachedAttrs{storedAt: time.Now(), isNegative: attrs == nil}
	if attrs != nil {
		cp := *attrs // copy, so the caller cannot modify the cached value
		entry.attrs = &cp
	}
	if exists {
		entry.listElement = existing.listElement
	}
	c.cache[path] = entry
	c.updateAccessLog(path)
}

// generation returns the invalidation count. A reader takes it before
// consulting the filesystem and passes it to putAt.
func (c *AttrCache) generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gen
}

// Put adds or updates cached attributes
func (c *AttrCache) Put(path string, attrs *NFSAttrs) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store(path, attrs)
}

// putAt caches attributes read from the filesystem after generation
// returned gen, unless something was invalidated since: the attributes may
// have been read before that change.
func (c *AttrCache) putAt(path string, attrs *NFSAttrs, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen == gen {
		c.store(path, attrs)
	}
}

// PutNegative adds a negative cache entry (file not found)
func (c *AttrCache) PutNegative(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enableNegative {
		c.store(path, nil)
	}
}

// putNegativeAt is PutNegative for a miss observed after generation
// returned gen (see putAt).
func (c *AttrCache) putNegativeAt(path string, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enableNegative && c.gen == gen {
		c.store(path, nil)
	}
}

// Invalidate removes an entry from the cache
func (c *AttrCache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.gen++
	c.removeFromAccessLog(path)
	delete(c.cache, path)
}

// invalidateTree removes the entries for each of roots and everything
// below them, in one pass over the cache.
func (c *AttrCache) invalidateTree(roots ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.gen++
	prefixes := treePrefixes(roots)
	for p := range c.cache {
		if inAnyTree(p, roots, prefixes) {
			c.removeFromAccessLog(p)
			delete(c.cache, p)
		}
	}
}

// treePrefixes returns the prefix of the paths below each root. Cached
// paths are slash-separated (sanitizePath).
func treePrefixes(roots []string) []string {
	prefixes := make([]string, len(roots))
	for i, root := range roots {
		prefixes[i] = strings.TrimSuffix(root, "/") + "/"
	}
	return prefixes
}

// inAnyTree reports whether p is one of roots or lies below one of them.
func inAnyTree(p string, roots, prefixes []string) bool {
	for i, root := range roots {
		if p == root || strings.HasPrefix(p, prefixes[i]) {
			return true
		}
	}
	return false
}

// Clear removes all entries from the cache
func (c *AttrCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.gen++
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

// InvalidateNegativeInDir invalidates all negative cache entries in a
// directory (its direct children). The server itself invalidates only the
// names a procedure changes.
func (c *AttrCache) InvalidateNegativeInDir(dirPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.gen++
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
		c.evictLRU()
	}
}

// UpdateTTL changes the time-to-live for cached attributes. It applies to
// every entry, including those already cached.
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
	gen        uint64 // Incremented by every invalidation (see putAt)
	hits       uint64
	misses     uint64
}

// CachedDirEntry represents cached directory entries with expiration
type CachedDirEntry struct {
	entries     []os.FileInfo
	storedAt    time.Time
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

// expired reports whether e is past the timeout in force now. The caller
// holds c.mu.
func (c *DirCache) expired(e *CachedDirEntry) bool {
	return !time.Now().Before(e.storedAt.Add(c.timeout))
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
	if c.expired(cached) {
		c.mu.RUnlock()
		atomic.AddUint64(&c.misses, 1)

		// Remove expired entry with re-check after lock upgrade
		c.mu.Lock()
		if entry, exists := c.entries[path]; exists && c.expired(entry) {
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
	c.store(path, entries)
}

// generation returns the invalidation count (see AttrCache.generation).
func (c *DirCache) generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gen
}

// putAt caches a listing read after generation returned gen, unless
// something was invalidated since (see AttrCache.putAt).
func (c *DirCache) putAt(path string, entries []os.FileInfo, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen == gen {
		c.store(path, entries)
	}
}

// store adds or replaces the listing for path. The caller holds c.mu.
func (c *DirCache) store(path string, entries []os.FileInfo) {
	// Don't cache directories that exceed the maximum size
	if len(entries) > c.maxDirSize {
		return
	}

	// Check if entry already exists
	existing, exists := c.entries[path]

	// Check if we need to evict entries to make room
	if len(c.entries) >= c.maxEntries && !exists {
		c.evictLRU()
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
		storedAt:    time.Now(),
		listElement: listElem,
	}

	// Update access log to mark this as most recently used
	c.updateAccessLog(path)
}

// evictLRU removes the least recently used listing. The caller holds c.mu.
func (c *DirCache) evictLRU() {
	lru := c.accessList.Back()
	if lru == nil {
		return
	}
	if lruPath, ok := lru.Value.(string); ok {
		delete(c.entries, lruPath)
	}
	c.accessList.Remove(lru)
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

	c.gen++
	c.removeFromAccessList(path)
	delete(c.entries, path)
}

// invalidateTree removes the listings of each of roots and of every
// directory below them.
func (c *DirCache) invalidateTree(roots ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.gen++
	prefixes := treePrefixes(roots)
	for p := range c.entries {
		if inAnyTree(p, roots, prefixes) {
			c.removeFromAccessList(p)
			delete(c.entries, p)
		}
	}
}

// Clear removes all entries from the cache
func (c *DirCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.gen++
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
		c.evictLRU()
	}
}

// UpdateTTL changes the timeout for directory cache entries. It applies
// to every entry, including those already cached.
func (c *DirCache) UpdateTTL(newTimeout time.Duration) {
	if newTimeout <= 0 {
		newTimeout = 10 * time.Second // Default timeout if invalid
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.timeout = newTimeout
}

// invalidate drops what the caches hold for the objects at paths: their
// attributes or a cached miss, and their listing if they are directories.
// Procedures call it for every object they change, after changing it.
func (s *AbsfsNFS) invalidate(paths ...string) {
	for _, p := range paths {
		s.attrCache.Invalidate(p)
		if s.dirCache != nil {
			s.dirCache.Invalidate(p)
		}
	}
}

// invalidateTree drops what the caches hold for each of roots and
// everything below them, for a directory that moved or was replaced.
func (s *AbsfsNFS) invalidateTree(roots ...string) {
	s.attrCache.invalidateTree(roots...)
	if s.dirCache != nil {
		s.dirCache.invalidateTree(roots...)
	}
}
