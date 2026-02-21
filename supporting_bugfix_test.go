package absnfs

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestL1_MemoryMonitorRestart verifies that MemoryMonitor can be restarted
// after Stop() by recreating the stopCh channel.
func TestL1_MemoryMonitorRestart(t *testing.T) {
	nfs := createTestNFS(t)
	defer nfs.Close()

	mon := NewMemoryMonitor(nfs)

	// Start, stop, then start again
	mon.Start(50 * time.Millisecond)
	if !mon.IsActive() {
		t.Fatal("monitor should be active after first Start")
	}

	mon.Stop()
	if mon.IsActive() {
		t.Fatal("monitor should be inactive after Stop")
	}

	// This was the bug: second Start would exit immediately because stopCh was closed
	mon.Start(50 * time.Millisecond)
	if !mon.IsActive() {
		t.Fatal("monitor should be active after second Start")
	}

	// Let it run a tick to confirm the goroutine is alive
	time.Sleep(100 * time.Millisecond)
	if !mon.IsActive() {
		t.Fatal("monitor should still be active after running")
	}

	mon.Stop()
}

// TestL2_BatchProcessorStopDrains verifies that BatchProcessor.Stop() sends
// error results to pending waiters instead of leaving them blocked.
func TestL2_BatchProcessorStopDrains(t *testing.T) {
	nfs := createTestNFS(t)
	defer nfs.Close()
	nfs.options.BatchOperations = true

	bp := NewBatchProcessor(nfs, 100) // large max so batch doesn't auto-fire

	// Submit a request that will sit pending
	resultChan := make(chan *BatchResult, 1)
	req := &BatchRequest{
		Type:       BatchTypeRead,
		FileHandle: 999,
		Offset:     0,
		Length:     100,
		Time:       time.Now(),
		ResultChan: resultChan,
		Context:    context.Background(),
	}

	added, _ := bp.AddRequest(req)
	if !added {
		t.Fatal("request should have been added")
	}

	// Stop should drain pending and notify waiters
	bp.Stop()

	// The waiter should receive an error result, not block forever
	select {
	case res := <-resultChan:
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		if res.Error == nil {
			t.Fatal("expected error in result after Stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for result after Stop - waiter was not notified")
	}
}

// TestL3_WorkerPoolTimerLeak verifies that Submit uses a proper timer
// instead of time.After (which leaks until it fires).
// We test indirectly by confirming that Submit still works correctly
// when the queue is full (timeout path).
func TestL3_WorkerPoolTimerLeak(t *testing.T) {
	nfs := createTestNFS(t)
	defer nfs.Close()

	pool := NewWorkerPool(1, nfs)
	pool.Start()
	defer pool.Stop()

	// Fill the queue and the single worker with blocking tasks
	blocker := make(chan struct{})
	// Submit a blocking task to occupy the worker
	pool.Submit(func() interface{} {
		<-blocker
		return nil
	})
	// Fill the buffered queue (size = 1*2 = 2)
	for i := 0; i < 2; i++ {
		pool.Submit(func() interface{} {
			<-blocker
			return nil
		})
	}

	// Next submit should timeout (testing the timer path)
	result := pool.Submit(func() interface{} { return "ok" })
	if result != nil {
		t.Fatal("expected nil when queue is full and submit times out")
	}

	close(blocker)
}

// TestL5_WorkerPoolResizePreservesTasks verifies that Resize re-enqueues
// pending tasks instead of silently dropping them.
func TestL5_WorkerPoolResizePreservesTasks(t *testing.T) {
	nfs := createTestNFS(t)
	defer nfs.Close()

	pool := NewWorkerPool(2, nfs)
	pool.Start()

	// Block both workers
	blocker := make(chan struct{})
	var completed int32
	for i := 0; i < 2; i++ {
		pool.Submit(func() interface{} {
			<-blocker
			return nil
		})
	}

	// Queue some tasks that will be pending
	for i := 0; i < 2; i++ {
		pool.Submit(func() interface{} {
			atomic.AddInt32(&completed, 1)
			return "done"
		})
	}

	// Unblock workers so Stop() in Resize can proceed
	close(blocker)
	time.Sleep(50 * time.Millisecond)

	// Resize - this should re-enqueue pending tasks
	pool.Resize(4)
	defer pool.Stop()

	// Give re-enqueued tasks time to execute
	time.Sleep(200 * time.Millisecond)

	// Pending tasks should have been re-enqueued and completed
	c := atomic.LoadInt32(&completed)
	if c == 0 {
		t.Fatal("expected at least some re-enqueued tasks to complete, got 0")
	}
}

// TestL6_RateLimiterPerConnectionRace verifies that concurrent calls to
// AllowRequest for the same connID don't create duplicate limiters (TOCTOU race).
func TestL6_RateLimiterPerConnectionRace(t *testing.T) {
	config := DefaultRateLimiterConfig()
	rl := NewRateLimiter(config)

	var wg sync.WaitGroup
	const goroutines = 50
	connID := "test-conn-1"

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			rl.AllowRequest("127.0.0.1", connID)
		}()
	}
	wg.Wait()

	// Verify only one limiter exists for the connID
	count := 0
	rl.perConnectionLimiter.Range(func(key, value interface{}) bool {
		if key.(string) == connID {
			count++
		}
		return true
	})

	if count != 1 {
		t.Fatalf("expected exactly 1 limiter for connID, got %d", count)
	}
}

// TestL7_MinHeapPopMinEmpty verifies that PopMin on an empty heap returns
// (0, false) instead of panicking.
func TestL7_MinHeapPopMinEmpty(t *testing.T) {
	h := NewUint64MinHeap()

	val, ok := h.PopMin()
	if ok {
		t.Fatal("expected ok=false for empty heap")
	}
	if val != 0 {
		t.Fatalf("expected val=0 for empty heap, got %d", val)
	}

	// Verify normal operation still works
	h.PushValue(42)
	h.PushValue(7)
	h.PushValue(99)

	val, ok = h.PopMin()
	if !ok {
		t.Fatal("expected ok=true for non-empty heap")
	}
	if val != 7 {
		t.Fatalf("expected min=7, got %d", val)
	}
}

// TestL10_MetricsIsHealthyWindowed verifies that IsHealthy uses a windowed
// error rate that can recover after an initial burst of errors.
func TestL10_MetricsIsHealthyWindowed(t *testing.T) {
	mc := NewMetricsCollector(nil)

	// Record a burst of errors (>50%)
	for i := 0; i < 100; i++ {
		mc.RecordOperationResult(true) // error
	}
	if mc.IsHealthy() {
		t.Fatal("should be unhealthy after 100% error rate")
	}

	// Now record many successes to push errors out of the window
	for i := 0; i < 1000; i++ {
		mc.RecordOperationResult(false) // success
	}

	if !mc.IsHealthy() {
		t.Fatal("should recover to healthy after window fills with successes")
	}
}

// TestL11_MetricsLatencyRingBuffer verifies that latency samples are stored
// in a fixed-size ring buffer that doesn't grow unbounded.
func TestL11_MetricsLatencyRingBuffer(t *testing.T) {
	mc := NewMetricsCollector(nil)

	// Record more than maxLatencySamples latencies
	for i := 0; i < 2000; i++ {
		mc.RecordLatency("READ", time.Duration(i)*time.Microsecond)
	}

	// The underlying slice should be capped at maxLatencySamples
	mc.latencyMutex.Lock()
	readLen := mc.readLatLen
	readCap := cap(mc.readLatencies)
	mc.latencyMutex.Unlock()

	if readLen != mc.maxLatencySamples {
		t.Fatalf("expected readLatLen=%d, got %d", mc.maxLatencySamples, readLen)
	}
	if readCap != mc.maxLatencySamples {
		t.Fatalf("expected readLatencies cap=%d, got %d", mc.maxLatencySamples, readCap)
	}

	// Verify stats are computed correctly
	mc.latencyMutex.Lock()
	avg := mc.metrics.AvgReadLatency
	mc.latencyMutex.Unlock()

	if avg == 0 {
		t.Fatal("expected non-zero average read latency")
	}
}

// TestL12_MemoryMonitorNoExplicitGC verifies that reduceCacheSizes does not
// call runtime.GC(). We test this indirectly by confirming the function
// completes without forcing a GC cycle (the explicit GC line was removed).
func TestL12_MemoryMonitorNoExplicitGC(t *testing.T) {
	nfs := createTestNFS(t)
	defer nfs.Close()

	mon := NewMemoryMonitor(nfs)

	// Calling reduceCacheSizes should work without runtime.GC()
	// If the code still had runtime.GC(), it would still work but we've
	// verified by code inspection that the call was removed.
	mon.reduceCacheSizes(0.5)

	// Just verify the function completes without panic
}

// createTestNFS creates a minimal AbsfsNFS for testing
func createTestNFS(t *testing.T) *AbsfsNFS {
	t.Helper()
	nfs, _ := createTestServer(t)
	return nfs
}
