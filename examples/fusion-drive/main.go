// Fusion Drive NFS Server
//
// This example demonstrates a tiered storage system inspired by Apple's Fusion Drive,
// combining fast RAM cache with persistent SSD storage. The system automatically:
//
//   - Caches frequently accessed data in RAM for ultra-fast access
//   - Persists all data to SSD for durability
//   - Uses LRU eviction to manage RAM cache size
//   - Provides real-time statistics on cache hit rates
//
// Architecture:
//
//	┌─────────────────────────────────────────────────────────┐
//	│                    NFS Clients                          │
//	└─────────────────────────────────────────────────────────┘
//	                           │
//	                           ▼
//	┌─────────────────────────────────────────────────────────┐
//	│                    NFS Server                           │
//	└─────────────────────────────────────────────────────────┘
//	                           │
//	                           ▼
//	┌─────────────────────────────────────────────────────────┐
//	│               FusionFS (Tiered Storage)                 │
//	│  ┌─────────────────────────────────────────────────┐    │
//	│  │    Hot Tier: cachefs (RAM-backed LRU cache)     │    │
//	│  │    - Configurable size (default 256MB)          │    │
//	│  │    - LRU eviction policy                        │    │
//	│  │    - Write-through for durability               │    │
//	│  └─────────────────────────────────────────────────┘    │
//	│                         │                               │
//	│                         ▼                               │
//	│  ┌─────────────────────────────────────────────────┐    │
//	│  │    Cold Tier: lockfs(osfs) (SSD-backed)         │    │
//	│  │    - Persistent storage                         │    │
//	│  │    - Thread-safe access                         │    │
//	│  └─────────────────────────────────────────────────┘    │
//	└─────────────────────────────────────────────────────────┘
//
// This demonstrates the absfs composition pattern where multiple filesystem
// wrappers are layered to add capabilities:
//   - osfs: Provides persistent SSD storage
//   - lockfs: Adds thread-safety
//   - cachefs: Adds intelligent RAM caching with eviction
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/absnfs"
	"github.com/absfs/cachefs"
	"github.com/absfs/lockfs"
	"github.com/absfs/memfs"
	"github.com/absfs/osfs"
)

// FusionFS implements a two-tier storage system with RAM cache and SSD backing
type FusionFS struct {
	cache    *cachefs.CacheFS
	coldTier absfs.FileSystem
	cwd      string
}

// FusionConfig holds configuration for the fusion filesystem
type FusionConfig struct {
	// CacheSize is the maximum RAM cache size in bytes (default 256MB)
	CacheSize uint64
	// CacheTTL is how long items stay in cache (0 = forever until evicted)
	CacheTTL time.Duration
	// WriteMode controls cache write behavior
	WriteMode cachefs.WriteMode
	// EvictionPolicy controls how items are evicted from cache
	EvictionPolicy cachefs.EvictionPolicy
}

// DefaultFusionConfig returns sensible defaults for the fusion filesystem
func DefaultFusionConfig() FusionConfig {
	return FusionConfig{
		CacheSize:      256 * 1024 * 1024, // 256MB RAM cache
		CacheTTL:       0,                 // No TTL - use LRU only
		WriteMode:      cachefs.WriteModeWriteThrough,
		EvictionPolicy: cachefs.EvictionLRU,
	}
}

// NewFusionFS creates a new fusion filesystem with RAM cache over SSD storage
func NewFusionFS(coldTier absfs.FileSystem, config FusionConfig) (*FusionFS, error) {
	// Create the cache layer over the cold tier
	cache := cachefs.New(coldTier,
		cachefs.WithMaxBytes(config.CacheSize),
		cachefs.WithEvictionPolicy(config.EvictionPolicy),
		cachefs.WithWriteMode(config.WriteMode),
		cachefs.WithTTL(config.CacheTTL),
		cachefs.WithMetadataCache(true),
		cachefs.WithMetadataMaxEntries(10000),
	)

	return &FusionFS{
		cache:    cache,
		coldTier: coldTier,
		cwd:      "/",
	}, nil
}

// Stats returns the current cache statistics
func (f *FusionFS) Stats() *cachefs.Stats {
	return f.cache.Stats()
}

// Implement absfs.SymlinkFileSystem interface by delegating to cache

func (f *FusionFS) Open(name string) (absfs.File, error) {
	return f.cache.Open(name)
}

func (f *FusionFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
	return f.cache.OpenFile(name, flag, perm)
}

func (f *FusionFS) Create(name string) (absfs.File, error) {
	return f.cache.Create(name)
}

func (f *FusionFS) Mkdir(name string, perm os.FileMode) error {
	return f.cache.Mkdir(name, perm)
}

func (f *FusionFS) MkdirAll(path string, perm os.FileMode) error {
	return f.cache.MkdirAll(path, perm)
}

func (f *FusionFS) Remove(name string) error {
	return f.cache.Remove(name)
}

func (f *FusionFS) RemoveAll(path string) error {
	return f.cache.RemoveAll(path)
}

func (f *FusionFS) Rename(oldpath, newpath string) error {
	return f.cache.Rename(oldpath, newpath)
}

func (f *FusionFS) Stat(name string) (os.FileInfo, error) {
	return f.cache.Stat(name)
}

func (f *FusionFS) Chmod(name string, mode os.FileMode) error {
	return f.cache.Chmod(name, mode)
}

func (f *FusionFS) Chown(name string, uid, gid int) error {
	return f.cache.Chown(name, uid, gid)
}

func (f *FusionFS) Chtimes(name string, atime, mtime time.Time) error {
	return f.cache.Chtimes(name, atime, mtime)
}

func (f *FusionFS) Chdir(dir string) error {
	return f.cache.Chdir(dir)
}

func (f *FusionFS) Getwd() (string, error) {
	return f.cache.Getwd()
}

func (f *FusionFS) TempDir() string {
	return f.cache.TempDir()
}

func (f *FusionFS) Truncate(name string, size int64) error {
	return f.cache.Truncate(name, size)
}

func (f *FusionFS) ReadDir(name string) ([]os.DirEntry, error) {
	return f.cache.ReadDir(name)
}

func (f *FusionFS) ReadFile(name string) ([]byte, error) {
	return f.cache.ReadFile(name)
}

func (f *FusionFS) Sub(dir string) (fs.FS, error) {
	return f.cache.Sub(dir)
}

// SymLinker interface - delegate to cold tier if it supports symlinks
func (f *FusionFS) Lstat(name string) (os.FileInfo, error) {
	if sfs, ok := f.coldTier.(absfs.SymlinkFileSystem); ok {
		return sfs.Lstat(name)
	}
	return f.cache.Stat(name)
}

func (f *FusionFS) Lchown(name string, uid, gid int) error {
	if sfs, ok := f.coldTier.(absfs.SymlinkFileSystem); ok {
		return sfs.Lchown(name, uid, gid)
	}
	return f.cache.Chown(name, uid, gid)
}

func (f *FusionFS) Readlink(name string) (string, error) {
	if sfs, ok := f.coldTier.(absfs.SymlinkFileSystem); ok {
		return sfs.Readlink(name)
	}
	return "", &os.PathError{Op: "readlink", Path: name, Err: syscall.EINVAL}
}

func (f *FusionFS) Symlink(oldname, newname string) error {
	if sfs, ok := f.coldTier.(absfs.SymlinkFileSystem); ok {
		return sfs.Symlink(oldname, newname)
	}
	return &os.PathError{Op: "symlink", Path: newname, Err: syscall.EINVAL}
}

// Close flushes the cache and cleans up resources
func (f *FusionFS) Close() error {
	return f.cache.Close()
}

func main() {
	// Command line flags
	port := flag.Int("port", 2049, "NFS server port")
	dataDir := flag.String("data", "", "Data directory for persistent storage (required)")
	cacheSize := flag.Uint64("cache-size", 256, "RAM cache size in MB")
	statsPort := flag.Int("stats-port", 8080, "HTTP port for statistics endpoint")
	debug := flag.Bool("debug", false, "Enable debug logging")
	demoMode := flag.Bool("demo", false, "Use in-memory storage instead of disk (for testing)")
	flag.Parse()

	// Validate flags
	if *dataDir == "" && !*demoMode {
		log.Fatal("Error: -data flag is required (or use -demo for testing)")
	}

	var coldTier absfs.FileSystem
	var err error

	if *demoMode {
		// Demo mode: use memfs as the cold tier (for testing without disk)
		log.Println("Demo mode: using in-memory storage (data will not persist)")
		coldTier, err = memfs.NewFS()
		if err != nil {
			log.Fatalf("Failed to create memfs: %v", err)
		}
	} else {
		// Production mode: use osfs with the specified data directory
		absPath, err := filepath.Abs(*dataDir)
		if err != nil {
			log.Fatalf("Invalid data path: %v", err)
		}

		// Ensure data directory exists
		if err := os.MkdirAll(absPath, 0755); err != nil {
			log.Fatalf("Failed to create data directory: %v", err)
		}

		// Create osfs and wrap with lockfs for thread safety
		rawFS, err := osfs.NewFS()
		if err != nil {
			log.Fatalf("Failed to create osfs: %v", err)
		}

		// Create a base-path restricted view of the filesystem
		coldTier = &BasePathFS{fs: rawFS, base: absPath}
	}

	// Wrap cold tier with lockfs for thread safety
	lockedColdTier, err := lockfs.NewFS(coldTier)
	if err != nil {
		log.Fatalf("Failed to create lockfs: %v", err)
	}

	// Create the fusion filesystem
	config := DefaultFusionConfig()
	config.CacheSize = *cacheSize * 1024 * 1024 // Convert MB to bytes

	fusionFS, err := NewFusionFS(lockedColdTier, config)
	if err != nil {
		log.Fatalf("Failed to create fusion filesystem: %v", err)
	}
	defer fusionFS.Close()

	// Start statistics HTTP server
	go startStatsServer(fusionFS, *statsPort)

	// Create NFS handler
	nfs, err := absnfs.New(fusionFS, absnfs.ExportOptions{})
	if err != nil {
		log.Fatalf("Failed to create NFS handler: %v", err)
	}

	// Create and start NFS server
	server, err := absnfs.NewServer(absnfs.ServerOptions{
		Port:             *port,
		MountPort:        *port,
		Debug:            *debug,
		UseRecordMarking: true,
	})
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}
	server.SetHandler(nfs)

	if err := server.Listen(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	printBanner(*port, *statsPort, *cacheSize, *dataDir, *demoMode)

	// Start periodic stats reporter
	go periodicStatsReporter(fusionFS, 30*time.Second)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down...")
	server.Stop()
}

func printBanner(port, statsPort int, cacheSize uint64, dataDir string, demoMode bool) {
	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════════════════════════╗")
	fmt.Println("║           Fusion Drive NFS Server                             ║")
	fmt.Println("║   RAM + SSD Tiered Storage (like Apple's Fusion Drive)        ║")
	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║                                                               ║")
	fmt.Printf("║  NFS Server Port:     %d                                    ║\n", port)
	fmt.Printf("║  Statistics Port:     %d (http://localhost:%d/stats)     ║\n", statsPort, statsPort)
	fmt.Printf("║  RAM Cache Size:      %d MB                                  ║\n", cacheSize)
	if demoMode {
		fmt.Println("║  Storage Mode:        Demo (in-memory, non-persistent)       ║")
	} else {
		fmt.Printf("║  Data Directory:      %-38s ║\n", truncateString(dataDir, 38))
	}
	fmt.Println("║                                                               ║")
	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Architecture:                                                ║")
	fmt.Println("║    ┌─────────────────────────────────────────┐                ║")
	fmt.Println("║    │  Hot Tier: RAM Cache (LRU eviction)     │                ║")
	fmt.Println("║    │  - Frequently accessed data             │                ║")
	fmt.Println("║    │  - Automatic promotion on read          │                ║")
	fmt.Println("║    └────────────────┬────────────────────────┘                ║")
	fmt.Println("║                     │                                         ║")
	fmt.Println("║                     ▼                                         ║")
	fmt.Println("║    ┌─────────────────────────────────────────┐                ║")
	fmt.Println("║    │  Cold Tier: SSD Storage (persistent)    │                ║")
	fmt.Println("║    │  - All data stored durably              │                ║")
	fmt.Println("║    │  - Write-through for safety             │                ║")
	fmt.Println("║    └─────────────────────────────────────────┘                ║")
	fmt.Println("║                                                               ║")
	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Mount Commands:                                              ║")
	fmt.Println("║                                                               ║")
	fmt.Println("║  macOS:                                                       ║")
	fmt.Printf("║    sudo mount_nfs -o resvport,nolocks,vers=3,tcp,\\           ║\n")
	fmt.Printf("║      port=%d,mountport=%d localhost:/ /Volumes/fusion        ║\n", port, port)
	fmt.Println("║                                                               ║")
	fmt.Println("║  Linux:                                                       ║")
	fmt.Printf("║    sudo mount -t nfs -o vers=3,tcp,port=%d,\\                 ║\n", port)
	fmt.Printf("║      mountport=%d,nolock localhost:/ /mnt/fusion             ║\n", port)
	fmt.Println("║                                                               ║")
	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
	fmt.Println("║  Press Ctrl+C to stop                                         ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func startStatsServer(fusionFS *FusionFS, port int) {
	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := fusionFS.Stats()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "cache": {
    "hits": %d,
    "misses": %d,
    "hit_rate": %.2f,
    "evictions": %d,
    "bytes_used": %d,
    "entries": %d
  }
}`,
			stats.Hits(),
			stats.Misses(),
			stats.HitRate()*100,
			stats.Evictions(),
			stats.BytesUsed(),
			stats.Entries(),
		)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		stats := fusionFS.Stats()
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>Fusion Drive Statistics</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 40px; background: #1a1a2e; color: #eee; }
        h1 { color: #00d4ff; }
        .stats { background: #16213e; padding: 20px; border-radius: 10px; max-width: 500px; }
        .stat { margin: 15px 0; display: flex; justify-content: space-between; }
        .label { color: #a0a0a0; }
        .value { color: #00d4ff; font-weight: bold; font-size: 1.2em; }
        .hit-rate { font-size: 2em; color: #00ff88; }
        .progress { background: #0f3460; border-radius: 5px; height: 20px; margin-top: 10px; }
        .progress-bar { background: linear-gradient(90deg, #00d4ff, #00ff88); height: 100%%; border-radius: 5px; transition: width 0.5s; }
        .refresh { color: #666; font-size: 0.8em; margin-top: 20px; }
    </style>
    <script>
        setTimeout(function(){ location.reload(); }, 5000);
    </script>
</head>
<body>
    <h1>Fusion Drive Statistics</h1>
    <div class="stats">
        <div class="stat">
            <span class="label">Cache Hit Rate</span>
            <span class="value hit-rate">%.1f%%</span>
        </div>
        <div class="progress">
            <div class="progress-bar" style="width: %.1f%%"></div>
        </div>
        <div class="stat">
            <span class="label">Cache Hits</span>
            <span class="value">%d</span>
        </div>
        <div class="stat">
            <span class="label">Cache Misses</span>
            <span class="value">%d</span>
        </div>
        <div class="stat">
            <span class="label">Evictions</span>
            <span class="value">%d</span>
        </div>
        <div class="stat">
            <span class="label">Cache Size</span>
            <span class="value">%s</span>
        </div>
        <div class="stat">
            <span class="label">Cached Entries</span>
            <span class="value">%d</span>
        </div>
        <p class="refresh">Auto-refreshes every 5 seconds</p>
    </div>
</body>
</html>`,
			stats.HitRate()*100,
			stats.HitRate()*100,
			stats.Hits(),
			stats.Misses(),
			stats.Evictions(),
			formatBytes(stats.BytesUsed()),
			stats.Entries(),
		)
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Statistics server listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Printf("Statistics server error: %v", err)
	}
}

func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func periodicStatsReporter(fusionFS *FusionFS, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		stats := fusionFS.Stats()
		log.Printf("[Cache Stats] Hit Rate: %.1f%% | Hits: %d | Misses: %d | Size: %s | Entries: %d | Evictions: %d",
			stats.HitRate()*100,
			stats.Hits(),
			stats.Misses(),
			formatBytes(stats.BytesUsed()),
			stats.Entries(),
			stats.Evictions(),
		)
	}
}

// BasePathFS restricts a filesystem to a base path (same as composed-workspace example)
type BasePathFS struct {
	fs   absfs.FileSystem
	base string
}

func (b *BasePathFS) resolvePath(name string) string {
	clean := filepath.Clean(name)
	if clean == "." || clean == "/" {
		return b.base
	}
	if len(clean) > 0 && clean[0] == '/' {
		clean = clean[1:]
	}
	return filepath.Join(b.base, clean)
}

func (b *BasePathFS) Open(name string) (absfs.File, error) {
	return b.fs.Open(b.resolvePath(name))
}

func (b *BasePathFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
	return b.fs.OpenFile(b.resolvePath(name), flag, perm)
}

func (b *BasePathFS) Create(name string) (absfs.File, error) {
	return b.fs.Create(b.resolvePath(name))
}

func (b *BasePathFS) Mkdir(name string, perm os.FileMode) error {
	return b.fs.Mkdir(b.resolvePath(name), perm)
}

func (b *BasePathFS) MkdirAll(path string, perm os.FileMode) error {
	return b.fs.MkdirAll(b.resolvePath(path), perm)
}

func (b *BasePathFS) Remove(name string) error {
	return b.fs.Remove(b.resolvePath(name))
}

func (b *BasePathFS) RemoveAll(path string) error {
	return b.fs.RemoveAll(b.resolvePath(path))
}

func (b *BasePathFS) Rename(oldpath, newpath string) error {
	return b.fs.Rename(b.resolvePath(oldpath), b.resolvePath(newpath))
}

func (b *BasePathFS) Stat(name string) (os.FileInfo, error) {
	return b.fs.Stat(b.resolvePath(name))
}

func (b *BasePathFS) Chmod(name string, mode os.FileMode) error {
	return b.fs.Chmod(b.resolvePath(name), mode)
}

func (b *BasePathFS) Chown(name string, uid, gid int) error {
	return b.fs.Chown(b.resolvePath(name), uid, gid)
}

func (b *BasePathFS) Chtimes(name string, atime, mtime time.Time) error {
	return b.fs.Chtimes(b.resolvePath(name), atime, mtime)
}

func (b *BasePathFS) Chdir(dir string) error {
	return b.fs.Chdir(b.resolvePath(dir))
}

func (b *BasePathFS) Getwd() (string, error) {
	return "/", nil
}

func (b *BasePathFS) TempDir() string {
	return "/tmp"
}

func (b *BasePathFS) Truncate(name string, size int64) error {
	return b.fs.Truncate(b.resolvePath(name), size)
}

func (b *BasePathFS) ReadDir(name string) ([]os.DirEntry, error) {
	return b.fs.ReadDir(b.resolvePath(name))
}

func (b *BasePathFS) ReadFile(name string) ([]byte, error) {
	return b.fs.ReadFile(b.resolvePath(name))
}

func (b *BasePathFS) Sub(dir string) (fs.FS, error) {
	return b.fs.Sub(b.resolvePath(dir))
}
