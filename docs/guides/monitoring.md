---
layout: default
title: Monitoring
---

# Monitoring

This guide covers strategies for monitoring ABSNFS servers, tracking performance metrics, identifying issues, and implementing alerting systems. Proper monitoring is essential for maintaining reliable NFS services and optimizing performance.

## Monitoring Overview

A comprehensive monitoring strategy for ABSNFS should cover:

1. **Resource Utilization**: CPU, memory, network, and disk usage
2. **NFS Operations**: Counts, latencies, and error rates for different operations
3. **Cache Performance**: Hit rates, sizes, and invalidations
4. **Client Connections**: Number of clients, geographic distribution, and connection quality
5. **Error Conditions**: Authentication failures, access violations, and system errors

## Built-in Monitoring

ABSNFS provides built-in metrics that can be accessed programmatically:

```go
// Get server metrics
metrics := nfsServer.GetMetrics()

// Log key metrics
log.Printf("Total operations: %d", metrics.TotalOperations)
log.Printf("Read operations: %d", metrics.ReadOperations)
log.Printf("Write operations: %d", metrics.WriteOperations)
log.Printf("Average read latency: %v", metrics.AvgReadLatency)
log.Printf("Average write latency: %v", metrics.AvgWriteLatency)
log.Printf("Cache hit rate: %.2f%%", metrics.CacheHitRate*100)
log.Printf("Active connections: %d", metrics.ActiveConnections)
```

### Available Metrics

The metrics returned by `GetMetrics()` include:

**Operation Counts**:
- `TotalOperations`: Total number of NFS operations processed
- `ReadOperations`: Number of READ operations
- `WriteOperations`: Number of WRITE operations
- `LookupOperations`: Number of LOOKUP operations
- `GetAttrOperations`: Number of GETATTR operations
- `CreateOperations`: Number of CREATE operations
- `RemoveOperations`: Number of REMOVE operations
- `RenameOperations`: Number of RENAME operations
- `MkdirOperations`: Number of MKDIR operations
- `RmdirOperations`: Number of RMDIR operations
- `ReaddirOperations`: Number of READDIR operations
- `AccessOperations`: Number of ACCESS operations

**Latency Metrics**:
- `AvgReadLatency`: Average duration of READ operations
- `AvgWriteLatency`: Average duration of WRITE operations
- `MaxReadLatency`: Maximum observed READ latency
- `MaxWriteLatency`: Maximum observed WRITE latency
- `P95ReadLatency`: 95th percentile READ latency
- `P95WriteLatency`: 95th percentile WRITE latency

**Cache Metrics**:
- `CacheHitRate`: Percentage of attribute lookups served from cache
- `ReadAheadHitRate`: Percentage of READ operations served from read-ahead buffer
- `AttrCacheSize`: Number of entries in the attribute cache
- `AttrCacheCapacity`: Maximum capacity of the attribute cache
- `ReadAheadBufferSize`: Current size of all read-ahead buffers
- `DirCacheHitRate`: Percentage of directory lookups served from cache

**Connection Metrics**:
- `ActiveConnections`: Number of currently active client connections
- `TotalConnections`: Total number of connections since server start
- `RejectedConnections`: Number of connections rejected (due to limits or access control)

**Error Metrics**:
- `ErrorCount`: Total number of errors
- `AuthFailures`: Number of authentication failures
- `AccessViolations`: Number of access violations
- `StaleHandles`: Number of stale file handle errors
- `ResourceErrors`: Number of resource-related errors (out of memory, etc.)

## Implementing a Monitoring Server

You can expose these metrics through an HTTP endpoint for integration with monitoring systems:

```go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	// Create a filesystem
	fs, _ := memfs.NewFS()

	// Create NFS server
	nfsServer, _ := absnfs.New(fs, absnfs.ExportOptions{})

	// Export the filesystem
	nfsServer.Export("/export/test", 2049)

	// Set up HTTP metrics server
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics := nfsServer.GetMetrics()
		
		// Convert to JSON
		jsonData, err := json.MarshalIndent(metrics, "", "  ")
		if err != nil {
			http.Error(w, "Error generating metrics", http.StatusInternalServerError)
			return
		}
		
		// Set content type and write response
		w.Header().Set("Content-Type", "application/json")
		w.Write(jsonData)
	})

	// Add a health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// Check if server is healthy
		if nfsServer.IsHealthy() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Unhealthy"))
		}
	})

	// Add a Prometheus metrics endpoint if using Prometheus
	http.HandleFunc("/prometheus", func(w http.ResponseWriter, r *http.Request) {
		metrics := nfsServer.GetMetrics()

		// Generate Prometheus format metrics
		w.Header().Set("Content-Type", "text/plain")

		// Counter metrics
		fmt.Fprintf(w, "# HELP absnfs_operations_total Total number of NFS operations\n")
		fmt.Fprintf(w, "# TYPE absnfs_operations_total counter\n")
		fmt.Fprintf(w, "absnfs_operations_total %d\n", metrics.TotalOperations)

		fmt.Fprintf(w, "# HELP absnfs_read_operations_total Total number of READ operations\n")
		fmt.Fprintf(w, "# TYPE absnfs_read_operations_total counter\n")
		fmt.Fprintf(w, "absnfs_read_operations_total %d\n", metrics.ReadOperations)

		// More metrics in Prometheus format...
	})

	// Start monitoring server on a separate port
	go func() {
		log.Println("Starting monitoring server on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatalf("Failed to start monitoring server: %v", err)
		}
	}()

	// Keep the main server running
	select {}
}
```

## Integrating with Monitoring Systems

### Prometheus Integration

To integrate with Prometheus, you can use a custom collector:

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NFSCollector implements the prometheus.Collector interface
type NFSCollector struct {
	nfsServer *absnfs.AbsfsNFS

	// Define metrics
	totalOps          prometheus.Counter
	readOps           prometheus.Counter
	writeOps          prometheus.Counter
	readLatency       prometheus.Histogram
	writeLatency      prometheus.Histogram
	cacheHitRate      prometheus.Gauge
	activeConnections prometheus.Gauge
	errorCount        prometheus.Counter
}

// NewNFSCollector creates a new collector
func NewNFSCollector(nfsServer *absnfs.AbsfsNFS) *NFSCollector {
	return &NFSCollector{
		nfsServer: nfsServer,

		totalOps: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "absnfs_operations_total",
			Help: "Total number of NFS operations",
		}),

		readOps: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "absnfs_read_operations_total",
			Help: "Total number of READ operations",
		}),

		writeOps: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "absnfs_write_operations_total",
			Help: "Total number of WRITE operations",
		}),

		readLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "absnfs_read_latency_seconds",
			Help:    "READ operation latency in seconds",
			Buckets: prometheus.DefBuckets,
		}),

		writeLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "absnfs_write_latency_seconds",
			Help:    "WRITE operation latency in seconds",
			Buckets: prometheus.DefBuckets,
		}),

		cacheHitRate: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "absnfs_cache_hit_rate",
			Help: "Cache hit rate percentage",
		}),

		activeConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "absnfs_active_connections",
			Help: "Number of active client connections",
		}),

		errorCount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "absnfs_errors_total",
			Help: "Total number of errors",
		}),
	}
}

// Describe implements prometheus.Collector
func (c *NFSCollector) Describe(ch chan<- *prometheus.Desc) {
	c.totalOps.Describe(ch)
	c.readOps.Describe(ch)
	c.writeOps.Describe(ch)
	c.readLatency.Describe(ch)
	c.writeLatency.Describe(ch)
	c.cacheHitRate.Describe(ch)
	c.activeConnections.Describe(ch)
	c.errorCount.Describe(ch)
}

// Collect implements prometheus.Collector
func (c *NFSCollector) Collect(ch chan<- prometheus.Metric) {
	// Get current metrics from NFS server
	metrics := c.nfsServer.GetMetrics()

	// Update counters using Add() method
	// Note: In a real implementation, you'd track deltas between calls
	// For simplicity, this example shows the pattern
	c.totalOps.Add(float64(metrics.TotalOperations))
	c.readOps.Add(float64(metrics.ReadOperations))
	c.writeOps.Add(float64(metrics.WriteOperations))

	// Update gauges
	c.cacheHitRate.Set(float64(metrics.CacheHitRate))
	c.activeConnections.Set(float64(metrics.ActiveConnections))
	c.errorCount.Add(float64(metrics.ErrorCount))

	// Send to channel
	c.totalOps.Collect(ch)
	c.readOps.Collect(ch)
	c.writeOps.Collect(ch)
	c.readLatency.Collect(ch)
	c.writeLatency.Collect(ch)
	c.cacheHitRate.Collect(ch)
	c.activeConnections.Collect(ch)
	c.errorCount.Collect(ch)
}

func main() {
	// Create NFS server
	fs, _ := memfs.NewFS()
	nfsServer, _ := absnfs.New(fs, absnfs.ExportOptions{})
	nfsServer.Export("/export/test", 2049)

	// Create and register collector
	collector := NewNFSCollector(nfsServer)
	prometheus.MustRegister(collector)

	// Expose the Prometheus metrics endpoint
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(":8080", nil))
}
```

### Grafana Dashboard

Here's a sample Grafana dashboard configuration for ABSNFS monitoring:

```json
{
  "dashboard": {
    "id": null,
    "title": "ABSNFS Monitoring Dashboard",
    "timezone": "browser",
    "panels": [
      {
        "id": 1,
        "title": "Operation Rate",
        "type": "graph",
        "datasource": "Prometheus",
        "targets": [
          {
            "expr": "rate(absnfs_operations_total[1m])",
            "legendFormat": "Total Operations",
            "refId": "A"
          },
          {
            "expr": "rate(absnfs_read_operations_total[1m])",
            "legendFormat": "Read Operations",
            "refId": "B"
          },
          {
            "expr": "rate(absnfs_write_operations_total[1m])",
            "legendFormat": "Write Operations",
            "refId": "C"
          }
        ]
      },
      {
        "id": 2,
        "title": "Operation Latency",
        "type": "graph",
        "datasource": "Prometheus",
        "targets": [
          {
            "expr": "histogram_quantile(0.95, rate(absnfs_read_latency_seconds_bucket[5m]))",
            "legendFormat": "Read P95",
            "refId": "A"
          },
          {
            "expr": "histogram_quantile(0.95, rate(absnfs_write_latency_seconds_bucket[5m]))",
            "legendFormat": "Write P95",
            "refId": "B"
          }
        ]
      },
      {
        "id": 3,
        "title": "Cache Hit Rate",
        "type": "gauge",
        "datasource": "Prometheus",
        "targets": [
          {
            "expr": "absnfs_cache_hit_rate * 100",
            "refId": "A"
          }
        ],
        "options": {
          "minValue": 0,
          "maxValue": 100,
          "thresholds": [
            { "value": 0, "color": "red" },
            { "value": 50, "color": "yellow" },
            { "value": 80, "color": "green" }
          ]
        }
      },
      {
        "id": 4,
        "title": "Active Connections",
        "type": "stat",
        "datasource": "Prometheus",
        "targets": [
          {
            "expr": "absnfs_active_connections",
            "refId": "A"
          }
        ]
      },
      {
        "id": 5,
        "title": "Error Rate",
        "type": "graph",
        "datasource": "Prometheus",
        "targets": [
          {
            "expr": "rate(absnfs_errors_total[1m])",
            "legendFormat": "Errors",
            "refId": "A"
          }
        ]
      }
    ]
  }
}
```

## OS-Level Monitoring

In addition to application-level metrics, monitor these OS-level resources:

### Memory Monitoring

```bash
# Using vmstat
vmstat 5

# Using free
free -m

# Process-specific memory
ps -o pid,user,%mem,rss,command -p $(pgrep nfs-server)
```

### CPU Monitoring

```bash
# Using top
top -p $(pgrep nfs-server)

# Using mpstat
mpstat -P ALL 5

# Process-specific CPU
pidstat -p $(pgrep nfs-server) 5
```

### Network Monitoring

```bash
# Using netstat
netstat -an | grep 2049

# Using ss
ss -tan state established '( dport = :2049 or sport = :2049 )'

# Using iftop
iftop -i eth0
```

### Disk I/O Monitoring

```bash
# Using iostat
iostat -xz 5

# Using iotop
iotop -p $(pgrep nfs-server)
```

## NFS-Specific Monitoring

For NFS-specific monitoring, you can use:

```bash
# NFS statistics
nfsstat -s

# RPC statistics
rpcinfo -p localhost

# Server exports
showmount -e localhost
```

## Log Monitoring

ABSNFS includes built-in logging that you can configure using the standard Go logger. You can set a custom logger on the server instance:

```go
import (
    "log"
    "os"
)

// Create a custom logger
logger := log.New(os.Stdout, "[absnfs] ", log.LstdFlags|log.Lshortfile)

// Create NFS server
nfsServer, _ := absnfs.New(fs, absnfs.ExportOptions{})

// The logger is automatically initialized, but you can replace it if needed
// by accessing the internal logger field (if exported in your version)
```

### Log Analysis

Monitor the server's standard output/error for operational messages and use standard log analysis tools to extract insights from your application logs.

## Alerting

Set up alerts for critical conditions:

### Prometheus Alerting Rules

```yaml
groups:
- name: absnfs
  rules:
  - alert: HighErrorRate
    expr: rate(absnfs_errors_total[5m]) > 10
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High error rate on NFS server"
      description: "NFS server has a high error rate ({{ $value }} errors/s)"

  - alert: LowCacheHitRate
    expr: absnfs_cache_hit_rate < 0.5
    for: 15m
    labels:
      severity: warning
    annotations:
      summary: "Low cache hit rate"
      description: "Cache hit rate is below 50% ({{ $value }})"

  - alert: HighLatency
    expr: histogram_quantile(0.95, rate(absnfs_read_latency_seconds_bucket[5m])) > 0.1
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High read latency"
      description: "95th percentile read latency is above 100ms ({{ $value }}s)"

  - alert: TooManyConnections
    expr: absnfs_active_connections > 100
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "Too many active connections"
      description: "There are {{ $value }} active connections"
```

### Email Alerts

For simple setups, you can implement email alerts directly:

```go
func monitorAndAlert(nfsServer *absnfs.AbsfsNFS, alertThresholds AlertThresholds) {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()

    var lastErrorCount uint64

    for range ticker.C {
        metrics := nfsServer.GetMetrics()

        // Check error rate
        errRate := float64(metrics.ErrorCount-lastErrorCount) / 60.0
        if errRate > alertThresholds.ErrorRate {
            sendAlert("High error rate", fmt.Sprintf("Error rate: %.2f/s", errRate))
        }

        // Check latency (if P95 metrics are available)
        if metrics.MaxReadLatency > alertThresholds.ReadLatency {
            sendAlert("High read latency", fmt.Sprintf("Max read latency: %v", metrics.MaxReadLatency))
        }

        // Check cache hit rate
        if metrics.CacheHitRate < alertThresholds.MinCacheHitRate {
            sendAlert("Low cache hit rate", fmt.Sprintf("Cache hit rate: %.2f%%", metrics.CacheHitRate*100))
        }

        // Update last values
        lastErrorCount = metrics.ErrorCount
    }
}

func sendAlert(subject, message string) {
    // Send email, Slack message, etc.
    log.Printf("ALERT: %s - %s", subject, message)

    // Example: Send to alerting system
    // alertingClient.Send(subject, message)
}
```

## Visualizing Performance Data

Beyond Grafana, you can visualize performance data using:

### Custom Web Dashboards

```html
<!DOCTYPE html>
<html>
<head>
    <title>ABSNFS Dashboard</title>
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
    <style>
        .chart-container { width: 48%; display: inline-block; }
    </style>
</head>
<body>
    <h1>ABSNFS Performance Dashboard</h1>
    
    <div class="chart-container">
        <canvas id="operationsChart"></canvas>
    </div>
    
    <div class="chart-container">
        <canvas id="latencyChart"></canvas>
    </div>
    
    <div class="chart-container">
        <canvas id="cacheChart"></canvas>
    </div>
    
    <div class="chart-container">
        <canvas id="connectionsChart"></canvas>
    </div>
    
    <script>
        // Fetch metrics and update charts
        function updateCharts() {
            fetch('/metrics')
                .then(response => response.json())
                .then(data => {
                    // Update operation chart
                    operationsChart.data.datasets[0].data.push(data.ReadOperations - lastReadOps);
                    operationsChart.data.datasets[1].data.push(data.WriteOperations - lastWriteOps);
                    operationsChart.update();
                    
                    // Update other charts...
                    
                    // Save last values
                    lastReadOps = data.ReadOperations;
                    lastWriteOps = data.WriteOperations;
                });
        }
        
        // Create charts
        const operationsChart = new Chart(
            document.getElementById('operationsChart'),
            {
                type: 'line',
                data: {
                    labels: [],
                    datasets: [
                        {
                            label: 'Read Ops',
                            data: [],
                            borderColor: 'blue',
                        },
                        {
                            label: 'Write Ops',
                            data: [],
                            borderColor: 'green',
                        }
                    ]
                }
            }
        );
        
        // Initialize other charts...
        
        // Global variables for tracking deltas
        let lastReadOps = 0;
        let lastWriteOps = 0;
        
        // Update every 5 seconds
        setInterval(updateCharts, 5000);
        updateCharts(); // Initial update
    </script>
</body>
</html>
```

## Performance Analysis and Troubleshooting

### Identifying Performance Bottlenecks

1. **High Read Latency**:
   - Check read-ahead buffer size and hit rate
   - Look for disk I/O contention
   - Check network bandwidth and latency
   - Look for large file transfers impacting other operations

2. **High Write Latency**:
   - Check disk I/O performance
   - Look for network congestion
   - Check for concurrent writers to the same files
   - Verify sufficient memory for write buffering

3. **Low Cache Hit Rate**:
   - Check if access patterns are random (reducing effectiveness of caching)
   - Consider increasing cache sizes
   - Look for frequent file modifications causing cache invalidations
   - Check if cache timeouts are too short

4. **High CPU Usage**:
   - Look for excessive request rate from clients
   - Check for computationally expensive operations (e.g., directory listings for very large directories)
   - Consider adding more CPU cores or optimizing code

5. **High Memory Usage**:
   - Check cache sizes and adjust if necessary
   - Look for memory leaks (steadily increasing usage)
   - Verify buffer sizes are appropriate for workload

### Troubleshooting Common Issues

#### Authentication Failures

```
metrics.AuthFailures is high
```

Check:
- Client IP restrictions
- User mapping configuration
- Authentication logs for specific error messages

#### Stale File Handles

```
metrics.StaleHandles is high
```

Check:
- Files being deleted while clients have them open
- File handle cache configuration
- Client behavior after server restart

#### Connection Limits

```
metrics.RejectedConnections is high
```

Check:
- `MaxConnections` setting
- Client connection patterns (too many reconnects?)
- Consider increasing limit or implementing connection pooling

#### Resource Exhaustion

```
metrics.ResourceErrors is high
```

Check:
- Memory usage
- File descriptor limits
- Disk space
- Consider resource limits and graceful degradation

## Best Practices for Monitoring

1. **Establish Baselines**: Measure normal performance before looking for anomalies
2. **Multi-Level Monitoring**: Monitor at application, OS, and network levels
3. **Trend Analysis**: Look for patterns over time, not just instantaneous values
4. **Correlation**: Correlate metrics across different subsystems
5. **Proactive Alerts**: Set up alerts to detect issues before they become critical
6. **Regular Review**: Regularly review and refine monitoring strategies
7. **Documentation**: Document normal ranges and troubleshooting procedures

## Monitoring Checklist

Use this checklist to ensure comprehensive monitoring:

- [ ] Application-level metrics (operations, latency, cache, connections)
- [ ] OS-level metrics (CPU, memory, disk, network)
- [ ] Log monitoring and analysis
- [ ] Alerting for critical conditions
- [ ] Performance visualization
- [ ] Historical trend analysis
- [ ] Client experience monitoring
- [ ] Regular review of monitoring data

## Conclusion

Effective monitoring is essential for maintaining reliable and high-performance NFS services. By implementing comprehensive monitoring at multiple levels, setting up appropriate alerts, and regularly analyzing performance data, you can ensure your ABSNFS servers meet your performance and reliability goals.

With the monitoring approaches described in this guide, you'll be able to:
1. Detect and address issues before they impact users
2. Optimize performance by identifying bottlenecks
3. Plan capacity based on usage trends
4. Validate the effectiveness of configuration changes
5. Provide reliable NFS services to your users

## Next Steps

- [Client Compatibility](./client-compatibility.md): Ensure compatibility with different NFS clients
- [Performance Tuning](./performance-tuning.md): Optimize your NFS server based on monitoring data
- [Using with Different Filesystems](./using-with-different-filesystems.md): Monitor performance across different storage backends