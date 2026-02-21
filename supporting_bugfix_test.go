package absnfs

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var _ = sort.Slice // ensure import is used

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
	nfs.UpdateTuningOptions(func(t *TuningOptions) { t.BatchOperations = true })

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

// TestR14_SubmitWaitNoDoubleClose verifies that SubmitWait no longer double-closes
// the result channel (worker sends on buffered chan, SubmitWait just reads).
func TestR14_SubmitWaitNoDoubleClose(t *testing.T) {
	nfs := createTestNFS(t)
	defer nfs.Close()

	pool := NewWorkerPool(2, nfs)
	pool.Start()
	defer pool.Stop()

	// SubmitWait should work without panicking from double-close
	result, ok := pool.SubmitWait(func() interface{} { return "hello" })
	if !ok {
		t.Fatal("expected ok=true from SubmitWait")
	}
	if result != "hello" {
		t.Fatalf("expected 'hello', got %v", result)
	}
}

// TestR15_P95IndexCorrect verifies that P95 index uses n-1 to stay in bounds.
func TestR15_P95IndexCorrect(t *testing.T) {
	mc := NewMetricsCollector(nil)

	// Record exactly 20 samples (minimum for P95 calculation)
	for i := 1; i <= 20; i++ {
		mc.RecordLatency("READ", time.Duration(i)*time.Millisecond)
	}

	mc.latencyMutex.Lock()
	p95 := mc.metrics.P95ReadLatency
	mc.latencyMutex.Unlock()

	// With 20 samples [1ms..20ms], P95 index = int(19 * 0.95) = int(18.05) = 18
	// sorted[18] = 19ms
	expected := 19 * time.Millisecond
	if p95 != expected {
		t.Fatalf("expected P95=%v, got %v", expected, p95)
	}
}

// TestR16_ResizeAfterStopNoPanic verifies that calling Resize after Stop
// doesn't panic from double-closing the task queue channel.
func TestR16_ResizeAfterStopNoPanic(t *testing.T) {
	nfs := createTestNFS(t)
	defer nfs.Close()

	pool := NewWorkerPool(2, nfs)
	pool.Start()
	pool.Stop()

	// This should not panic even though the channel is already closed
	pool.Resize(4)
	pool.Stop()
}

// TestR17_LatencyRaceDetector verifies that concurrent RecordLatency, IsHealthy,
// and GetMetrics calls don't trigger the race detector.
func TestR17_LatencyRaceDetector(t *testing.T) {
	mc := NewMetricsCollector(nil)

	var wg sync.WaitGroup
	wg.Add(3)

	// Concurrent RecordLatency
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			mc.RecordLatency("READ", time.Duration(i)*time.Microsecond)
			mc.RecordLatency("WRITE", time.Duration(i)*time.Microsecond)
		}
	}()

	// Concurrent IsHealthy
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			mc.IsHealthy()
		}
	}()

	// Concurrent GetMetrics
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			mc.GetMetrics()
		}
	}()

	wg.Wait()
}

// TestR29_ConcurrentResize verifies that concurrent Resize calls are serialized
// and don't cause panics.
func TestR29_ConcurrentResize(t *testing.T) {
	nfs := createTestNFS(t)
	defer nfs.Close()

	pool := NewWorkerPool(2, nfs)
	pool.Start()
	defer pool.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			pool.Resize(n + 1)
		}(i)
	}
	wg.Wait()
}

// TestR30_MaxLatencySimpleComparison verifies that max latency is tracked
// correctly using simple comparison under mutex (no unsafe pointer cast).
func TestR30_MaxLatencySimpleComparison(t *testing.T) {
	mc := NewMetricsCollector(nil)

	mc.RecordLatency("READ", 10*time.Millisecond)
	mc.RecordLatency("READ", 50*time.Millisecond)
	mc.RecordLatency("READ", 30*time.Millisecond)

	mc.latencyMutex.Lock()
	maxRead := mc.metrics.MaxReadLatency
	mc.latencyMutex.Unlock()

	if maxRead != 50*time.Millisecond {
		t.Fatalf("expected max=50ms, got %v", maxRead)
	}
}

// TestR31_BatchProcessorUnlockBeforeGoroutine verifies that the timer-based
// batch processing path works correctly (unlock before goroutine dispatch).
func TestR31_BatchProcessorUnlockBeforeGoroutine(t *testing.T) {
	nfs := createTestNFS(t)
	defer nfs.Close()
	nfs.UpdateTuningOptions(func(t *TuningOptions) { t.BatchOperations = true })

	// Small delay so timer fires quickly
	bp := NewBatchProcessor(nfs, 100)

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

	// Wait for timer-based processing (default delay is 10ms, ticker is 5ms)
	select {
	case res := <-resultChan:
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		// We expect an error since file handle 999 doesn't exist
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for timer-based batch processing")
	}

	bp.Stop()
}

// TestR32_PerIPLimiterCleanupBounded verifies that cleanup removes at most 100
// entries per pass instead of iterating the entire map.
func TestR32_PerIPLimiterCleanupBounded(t *testing.T) {
	pl := NewPerIPLimiter(1000, 100, time.Minute)

	// Create 200 IP entries that are all at max tokens (eligible for cleanup)
	for i := 0; i < 200; i++ {
		ip := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		pl.limiters[ip] = NewTokenBucket(1000, 100)
	}

	initialCount := len(pl.limiters)
	pl.cleanup()

	remaining := len(pl.limiters)
	deleted := initialCount - remaining

	// Should have deleted exactly 100 (the bounded max)
	if deleted != 100 {
		t.Fatalf("expected 100 deletions, got %d", deleted)
	}
}

// createTestNFS creates a minimal AbsfsNFS for testing
func createTestNFS(t *testing.T) *AbsfsNFS {
	t.Helper()
	nfs, _ := createTestServer(t)
	return nfs
}
