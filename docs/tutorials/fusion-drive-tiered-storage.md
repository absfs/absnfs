# Fusion Drive: RAM + SSD Tiered Storage

This tutorial demonstrates how to build a tiered storage system inspired by Apple's Fusion Drive, combining fast RAM cache with persistent SSD storage. The system automatically promotes frequently accessed data to RAM while ensuring durability through write-through to SSD.

## What We'll Build

A two-tier storage NFS server that:

- **Hot Tier (RAM)**: Caches frequently accessed data for ultra-fast reads
- **Cold Tier (SSD)**: Stores all data persistently
- **Automatic Promotion**: Data is cached on read using LRU eviction
- **Write-Through**: All writes go to both cache and SSD for durability
- **Real-Time Statistics**: HTTP endpoint showing cache hit rates

```
┌─────────────────────────────────────────────────────┐
│                    NFS Clients                       │
└─────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────┐
│               FusionFS (Tiered Storage)             │
│  ┌─────────────────────────────────────────────┐   │
│  │    Hot Tier: cachefs (RAM-backed LRU)       │   │
│  │    - Configurable size (default 256MB)      │   │
│  │    - LRU eviction when full                 │   │
│  └─────────────────────────────────────────────┘   │
│                         │                           │
│                         ▼                           │
│  ┌─────────────────────────────────────────────┐   │
│  │    Cold Tier: lockfs(osfs) (SSD-backed)     │   │
│  │    - Persistent storage                     │   │
│  │    - Thread-safe access                     │   │
│  └─────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
```

## Prerequisites

```bash
go get github.com/absfs/absnfs
go get github.com/absfs/cachefs
go get github.com/absfs/lockfs
go get github.com/absfs/osfs
go get github.com/absfs/memfs
```

## The Composition Pattern

The key insight is layering filesystem wrappers to add capabilities:

```go
// Start with raw SSD storage
rawFS, _ := osfs.NewFS()

// Restrict to a specific directory
basedFS := &BasePathFS{fs: rawFS, base: "/data/fusion"}

// Add thread safety
lockedFS, _ := lockfs.NewFS(basedFS)

// Add intelligent caching
cachedFS := cachefs.New(lockedFS,
    cachefs.WithMaxBytes(256 * 1024 * 1024),  // 256MB cache
    cachefs.WithEvictionPolicy(cachefs.EvictionLRU),
    cachefs.WithWriteMode(cachefs.WriteModeWriteThrough),
)

// Export via NFS
nfs, _ := absnfs.New(cachedFS, absnfs.ExportOptions{})
```

Each layer adds a specific capability:
- **osfs**: Persistent SSD storage
- **BasePathFS**: Path restriction (jail)
- **lockfs**: Thread safety with RWMutex
- **cachefs**: LRU RAM cache with eviction

## Understanding cachefs

The `cachefs` package provides sophisticated caching with multiple options:

### Write Modes

```go
// Write-Through: Safest, writes to both cache and backing store
cachefs.WithWriteMode(cachefs.WriteModeWriteThrough)

// Write-Back: Faster writes, but data loss possible on crash
cachefs.WithWriteMode(cachefs.WriteModeWriteBack)

// Write-Around: Bypasses cache on writes, only caches reads
cachefs.WithWriteMode(cachefs.WriteModeWriteAround)
```

### Eviction Policies

```go
// LRU: Evict least recently used (good for temporal locality)
cachefs.WithEvictionPolicy(cachefs.EvictionLRU)

// LFU: Evict least frequently used (good for working sets)
cachefs.WithEvictionPolicy(cachefs.EvictionLFU)

// TTL: Evict based on time-to-live
cachefs.WithEvictionPolicy(cachefs.EvictionTTL)

// Hybrid: Combines LRU/LFU with TTL
cachefs.WithEvictionPolicy(cachefs.EvictionHybrid)
```

### Cache Statistics

```go
stats := cache.Stats()
fmt.Printf("Hit Rate: %.1f%%\n", stats.HitRate() * 100)
fmt.Printf("Hits: %d, Misses: %d\n", stats.Hits(), stats.Misses())
fmt.Printf("Evictions: %d\n", stats.Evictions())
fmt.Printf("Cache Size: %d bytes\n", stats.BytesUsed())
```

## Complete Example

See the full implementation in [`examples/fusion-drive/main.go`](../../examples/fusion-drive/main.go).

### Running the Server

```bash
# Build the example
cd examples/fusion-drive
go build -o fusion-drive

# Run with persistent storage
./fusion-drive \
    -data /path/to/storage \
    -cache-size 512 \
    -port 2049 \
    -stats-port 8080

# Or run in demo mode (in-memory, for testing)
./fusion-drive -demo -port 2049
```

### Command Line Options

| Flag | Description | Default |
|------|-------------|---------|
| `-data` | Data directory for persistent storage | (required) |
| `-cache-size` | RAM cache size in MB | 256 |
| `-port` | NFS server port | 2049 |
| `-stats-port` | HTTP statistics endpoint port | 8080 |
| `-demo` | Use in-memory storage (for testing) | false |
| `-debug` | Enable debug logging | false |

### Statistics Dashboard

Access real-time cache statistics at `http://localhost:8080`:

- **Hit Rate**: Percentage of reads served from RAM cache
- **Hits/Misses**: Raw counts of cache operations
- **Evictions**: How many items were evicted due to cache pressure
- **Cache Size**: Current RAM usage

JSON endpoint available at `http://localhost:8080/stats`:

```json
{
  "cache": {
    "hits": 1523,
    "misses": 47,
    "hit_rate": 97.01,
    "evictions": 12,
    "bytes_used": 134217728,
    "entries": 423
  }
}
```

## Mounting the Export

### macOS

```bash
# Create mount point
sudo mkdir -p /Volumes/fusion

# Mount the NFS share
sudo mount_nfs -o resvport,nolocks,vers=3,tcp,port=2049,mountport=2049 \
    localhost:/ /Volumes/fusion

# Verify mount
ls /Volumes/fusion

# Unmount when done
sudo umount /Volumes/fusion
```

### Linux

```bash
# Create mount point
sudo mkdir -p /mnt/fusion

# Mount the NFS share
sudo mount -t nfs -o vers=3,tcp,port=2049,mountport=2049,nolock \
    localhost:/ /mnt/fusion

# Verify mount
ls /mnt/fusion

# Unmount when done
sudo umount /mnt/fusion
```

### Windows

```powershell
# Enable NFS Client (if not already)
Enable-WindowsOptionalFeature -FeatureName ServicesForNFS-ClientOnly -Online

# Mount the share
mount -o anon,nolock,vers=3,port=2049,mountport=2049 \\localhost\ F:

# Unmount
umount F:
```

## How It Works

### Read Path (Cache Hit)

1. Client requests file via NFS
2. cachefs checks RAM cache
3. **Cache hit**: Return data immediately (microseconds)
4. Update LRU position

### Read Path (Cache Miss)

1. Client requests file via NFS
2. cachefs checks RAM cache
3. **Cache miss**: Read from SSD via osfs
4. Store data in RAM cache
5. If cache full, evict LRU entry
6. Return data to client

### Write Path (Write-Through)

1. Client writes file via NFS
2. cachefs writes to SSD immediately
3. cachefs updates RAM cache
4. Acknowledge write to client

This ensures durability: even if the server crashes, data is safe on SSD.

## Performance Characteristics

| Operation | Hot (RAM) | Cold (SSD) |
|-----------|-----------|------------|
| Sequential Read | 10+ GB/s | 500 MB/s |
| Random Read | 1M+ IOPS | 100K IOPS |
| Write | N/A (write-through) | 400 MB/s |

The cache dramatically improves read performance for working sets that fit in RAM.

## Use Cases

### Development Workspace
```bash
./fusion-drive \
    -data ~/workspace \
    -cache-size 1024 \
    -port 12049
```
Your IDE and build tools get RAM-speed access to frequently used files.

### Media Server
```bash
./fusion-drive \
    -data /media/library \
    -cache-size 4096 \
    -port 2049
```
Stream media files with intelligent caching of popular content.

### Database Backing Store
```bash
./fusion-drive \
    -data /var/db/data \
    -cache-size 8192 \
    -port 2049
```
Database hot pages stay in RAM, cold data on SSD.

## Advanced Configurations

### High-Performance (Large Cache)
```go
config := FusionConfig{
    CacheSize:      4 * 1024 * 1024 * 1024, // 4GB
    EvictionPolicy: cachefs.EvictionLFU,     // Favor frequently used
    WriteMode:      cachefs.WriteModeWriteBack, // Faster writes
}
```

### Maximum Durability
```go
config := FusionConfig{
    CacheSize:      256 * 1024 * 1024,      // 256MB
    EvictionPolicy: cachefs.EvictionLRU,
    WriteMode:      cachefs.WriteModeWriteThrough, // Always sync
}
```

### Time-Sensitive Data
```go
config := FusionConfig{
    CacheSize:      512 * 1024 * 1024,
    CacheTTL:       5 * time.Minute,        // Expire after 5 min
    EvictionPolicy: cachefs.EvictionTTL,
}
```

## Comparison to Apple's Fusion Drive

| Feature | Apple Fusion Drive | This Implementation |
|---------|-------------------|---------------------|
| Fast Tier | SSD (128GB+) | RAM (configurable) |
| Slow Tier | HDD (1TB+) | SSD (persistent) |
| Promotion | Block-level | File-level |
| Eviction | Proprietary | LRU/LFU/TTL |
| Persistence | Both tiers | Cold tier only |

Apple's Fusion Drive combines SSD + HDD with block-level tiering. Our implementation uses RAM + SSD with file-level caching, providing even faster hot-tier access at the cost of volatility.

## Next Steps

- Add metrics export to Prometheus using `metricsfs`
- Implement custom eviction policies based on file type
- Add compression for cold tier using `compressfs`
- Set up monitoring dashboards

## Complete Code

The full working example is available at:

```
examples/fusion-drive/main.go
```

Build and run:

```bash
cd examples/fusion-drive
go build
./fusion-drive -data /tmp/fusion-data -cache-size 512
```
