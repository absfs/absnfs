// worker_pool.go: Concurrent request processing via goroutine pool.
//
// Contains WorkerPool which manages a fixed set of worker goroutines
// for handling NFS requests, with task queuing and graceful shutdown.
package absnfs

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// WorkerPool manages a pool of worker goroutines for handling concurrent operations
type WorkerPool struct {
	// Number of workers in the pool
	maxWorkers int
	// Channel for passing tasks to workers
	taskQueue chan Task
	// Context for cancellation
	ctx context.Context
	// Cancel function to stop all workers
	cancel context.CancelFunc
	// Wait group for tracking active workers
	wg sync.WaitGroup
	// Is the pool running?
	running int32
	// Number of active tasks
	activeWorkers int32
	// Logger for worker pool events
	logger *AbsfsNFS
	// R29: Mutex to serialize Resize calls
	resizeMu sync.Mutex
	// closeMu prevents Submit from sending on a closed taskQueue.
	// Submit holds RLock; Stop holds Lock before closing the channel.
	closeMu sync.RWMutex
}

// Task represents a unit of work to be processed by a worker
type Task struct {
	// The function to execute
	Execute func() interface{}
	// Channel to receive the result
	ResultChan chan interface{}
	// Start time of the task for metrics
	startTime time.Time
}

// NewWorkerPool creates a new worker pool with the specified number of workers
func NewWorkerPool(maxWorkers int, logger *AbsfsNFS) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		maxWorkers: maxWorkers,
		taskQueue:  make(chan Task, maxWorkers*2), // Buffer size is 2x number of workers
		ctx:        ctx,
		cancel:     cancel,
		logger:     logger,
	}
}

// Start launches the worker pool
func (p *WorkerPool) Start() {
	// Use atomic CAS to ensure we only start once
	if !atomic.CompareAndSwapInt32(&p.running, 0, 1) {
		return // Already running
	}

	// Launch worker goroutines
	p.wg.Add(p.maxWorkers)
	for i := 0; i < p.maxWorkers; i++ {
		go p.worker(i)
	}

	p.logger.logger.Printf("Worker pool started with %d workers", p.maxWorkers)
}

// worker runs in a goroutine and processes tasks from the queue
func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			// Worker pool is shutting down
			return
		case task, ok := <-p.taskQueue:
			if !ok {
				// Task queue has been closed
				return
			}

			// Execute the task
			atomic.AddInt32(&p.activeWorkers, 1)
			result := task.Execute()
			atomic.AddInt32(&p.activeWorkers, -1)

			// R14/R33: Non-blocking send to avoid deadlock if receiver is gone
			if task.ResultChan != nil {
				select {
				case task.ResultChan <- result:
				default:
				}
			}

			// Calculate and log task duration if we have a valid start time
			if !task.startTime.IsZero() {
				duration := time.Since(task.startTime)
				// Only log long-running tasks
				if duration > 100*time.Millisecond {
					p.logger.logger.Printf("Task completed in %v", duration)
				}
			}
		}
	}
}

// Submit adds a task to the worker pool
// Returns a channel that will receive the result, or nil if the task was rejected
func (p *WorkerPool) Submit(execute func() interface{}) chan interface{} {
	// Hold closeMu.RLock for the entire check+send sequence so that
	// Stop cannot close taskQueue between our running check and the send.
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()

	if atomic.LoadInt32(&p.running) == 0 {
		return nil // Not running
	}

	// Create a channel for the result
	resultChan := make(chan interface{}, 1)

	// Create a task
	task := Task{
		Execute:    execute,
		ResultChan: resultChan,
		startTime:  time.Now(),
	}

	// Try to submit the task to the queue with timeout
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case p.taskQueue <- task:
		// Task submitted successfully
		return resultChan
	case <-timer.C:
		// Task queue is full, close the result channel
		close(resultChan)
		return nil
	}
}

// SubmitWait adds a task to the worker pool and waits for the result
// Returns the result and a boolean indicating if the task was successfully processed
func (p *WorkerPool) SubmitWait(execute func() interface{}) (interface{}, bool) {
	resultChan := p.Submit(execute)
	if resultChan == nil {
		return nil, false
	}

	// R14: Wait for the result without closing - the worker owns the channel lifetime
	result, ok := <-resultChan
	return result, ok
}

// Stop shuts down the worker pool gracefully
func (p *WorkerPool) Stop() {
	// Use atomic to ensure we only stop once
	if !atomic.CompareAndSwapInt32(&p.running, 1, 0) {
		return // Not running
	}

	// Signal all workers to stop
	p.cancel()

	// Close the task queue under closeMu so that no Submit is mid-send.
	p.closeMu.Lock()
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Channel was already closed, ignore the panic
			}
		}()
		close(p.taskQueue)
	}()
	p.closeMu.Unlock()

	// Wait for all workers to finish
	p.wg.Wait()

	p.logger.logger.Printf("Worker pool stopped")
}

// Stats returns statistics about the worker pool
func (p *WorkerPool) Stats() (maxWorkers int, activeWorkers int, queuedTasks int) {
	p.resizeMu.Lock()
	maxWorkers = p.maxWorkers
	p.resizeMu.Unlock()
	activeWorkers = int(atomic.LoadInt32(&p.activeWorkers))
	queuedTasks = len(p.taskQueue)
	return
}

// Resize changes the number of workers in the pool
// This operation requires stopping and restarting the worker pool
func (p *WorkerPool) Resize(maxWorkers int) {
	// R29: Serialize Resize calls to prevent concurrent access
	p.resizeMu.Lock()
	defer p.resizeMu.Unlock()

	// Ensure valid worker count
	if maxWorkers <= 0 {
		maxWorkers = 1
	}

	// Only resize if the worker count changes
	if p.maxWorkers == maxWorkers {
		return
	}

	// Check if the pool is running
	wasRunning := atomic.LoadInt32(&p.running) == 1

	// Save reference to old queue before stopping
	oldQueue := p.taskQueue

	// Stop the pool if it's running
	// This will close the old queue and wait for all workers to finish
	if wasRunning {
		p.Stop()
	}

	// Drain remaining tasks from old queue and notify callers.
	// Stop() already closed oldQueue. If wasRunning was false, the queue
	// may still be closed from a prior Stop() call, so use recover().
	var pendingTasks []Task
	if oldQueue != nil {
		if !wasRunning {
			func() {
				defer func() { recover() }()
				close(oldQueue)
			}()
		}
		for task := range oldQueue {
			pendingTasks = append(pendingTasks, task)
		}
	}

	// Update the max workers
	p.maxWorkers = maxWorkers
	// Create a new task queue with appropriate size
	p.taskQueue = make(chan Task, maxWorkers*2)
	// Create a new context
	p.ctx, p.cancel = context.WithCancel(context.Background())
	// Reset active workers count
	atomic.StoreInt32(&p.activeWorkers, 0)

	// Restart the pool if it was running
	if wasRunning {
		p.Start()

		// Re-enqueue pending tasks from the old queue
		for _, task := range pendingTasks {
			select {
			case p.taskQueue <- task:
			default:
				// Queue full, notify caller of failure
				if task.ResultChan != nil {
					task.ResultChan <- nil
				}
			}
		}
	} else {
		// Pool wasn't running, notify callers of dropped tasks
		for _, task := range pendingTasks {
			if task.ResultChan != nil {
				task.ResultChan <- nil
			}
		}
	}
}
