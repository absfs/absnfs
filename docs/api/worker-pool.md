# WorkerPool

Fixed-size goroutine pool for concurrent NFS request processing.

## Types

### WorkerPool

```go
type WorkerPool struct {
    // (unexported fields)
}
```

Manages a pool of worker goroutines that consume tasks from a buffered channel. The task queue is sized at `2 * maxWorkers`.

### Task

```go
type Task struct {
    Execute    func() interface{} // The function to run
    ResultChan chan interface{}    // Channel to receive the result
    startTime  time.Time          // For latency metrics
}
```

Internal task type passed through the queue.

## Functions

### NewWorkerPool

```go
func NewWorkerPool(maxWorkers int, logger *AbsfsNFS) *WorkerPool
```

Creates a new worker pool. Does not start workers -- call `Start()` to launch them.

```go
pool := absnfs.NewWorkerPool(16, handler)
pool.Start()
defer pool.Stop()
```

### Start

```go
func (p *WorkerPool) Start()
```

Launches `maxWorkers` goroutines. Uses atomic CAS to ensure the pool is only started once. Subsequent calls are no-ops.

### Stop

```go
func (p *WorkerPool) Stop()
```

Gracefully shuts down the pool:
1. Atomically marks the pool as not running.
2. Cancels the context to signal workers.
3. Acquires a write lock to prevent concurrent `Submit` calls, then closes the task channel.
4. Waits for all workers to drain remaining tasks and exit.

### Submit

```go
func (p *WorkerPool) Submit(execute func() interface{}) chan interface{}
```

Submits a task for asynchronous execution. Returns a buffered channel (capacity 1) that will receive the result, or `nil` if:
- The pool is not running.
- The task queue is full after a 50ms timeout.

The method holds a read lock on `closeMu` for the entire check-and-send sequence, preventing a race with `Stop` closing the channel.

### SubmitWait

```go
func (p *WorkerPool) SubmitWait(execute func() interface{}) (interface{}, bool)
```

Submits a task and blocks until the result is available. Returns `(result, true)` on success or `(nil, false)` if submission failed.

### Stats

```go
func (p *WorkerPool) Stats() (maxWorkers int, activeWorkers int, queuedTasks int)
```

Returns the pool's configuration and current state:
- `maxWorkers`: Total worker count.
- `activeWorkers`: Workers currently executing a task.
- `queuedTasks`: Tasks waiting in the channel buffer.

### Resize

```go
func (p *WorkerPool) Resize(maxWorkers int)
```

Changes the number of workers. If `maxWorkers <= 0`, it is clamped to 1. The resize operation:

1. Serializes via a mutex (prevents concurrent resizes).
2. Stops the pool if running, draining the old task queue.
3. Creates a new task channel sized at `2 * maxWorkers`.
4. Restarts the pool with the new worker count.
5. Re-enqueues pending tasks from the old queue. Tasks that don't fit in the new queue are notified with a nil result.

## Concurrency Safety

- `Submit` and `Stop` coordinate via `closeMu` (RWMutex) to prevent sending on a closed channel.
- Workers use a non-blocking send to the result channel, preventing deadlocks if the caller has abandoned the channel.
- Long-running tasks (>100ms) are logged for diagnostic purposes.
