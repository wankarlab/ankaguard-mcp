package hft

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Task represents a unit of work
type Task interface {
	Execute(ctx context.Context) error
	ID() string
}

// Result represents the result of a task execution
type Result struct {
	TaskID    string
	Success   bool
	Error     error
	Duration  time.Duration
	Timestamp time.Time
}

// WorkerPool manages a pool of goroutines for concurrent task execution
type WorkerPool struct {
	workers    int
	taskQueue  chan Task
	resultChan chan Result
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	// Metrics
	tasksSubmitted uint64
	tasksCompleted uint64
	tasksSucceeded uint64
	tasksFailed    uint64
	totalLatencyUs uint64

	// State
	running atomic.Bool
}

// WorkerPoolConfig holds configuration for the worker pool
type WorkerPoolConfig struct {
	Workers      int
	QueueSize    int
	ResultBuffer int
}

// DefaultWorkerPoolConfig returns default configuration
func DefaultWorkerPoolConfig() WorkerPoolConfig {
	return WorkerPoolConfig{
		Workers:      100,   // 100 concurrent workers
		QueueSize:    10000, // 10k task buffer
		ResultBuffer: 1000,  // 1k result buffer
	}
}

// NewWorkerPool creates a new worker pool
func NewWorkerPool(config WorkerPoolConfig) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())

	pool := &WorkerPool{
		workers:    config.Workers,
		taskQueue:  make(chan Task, config.QueueSize),
		resultChan: make(chan Result, config.ResultBuffer),
		ctx:        ctx,
		cancel:     cancel,
	}

	pool.running.Store(false)

	return pool
}

// Start starts the worker pool
func (p *WorkerPool) Start() error {
	if p.running.Load() {
		return fmt.Errorf("worker pool already running")
	}

	p.running.Store(true)

	// Start workers
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	return nil
}

// Stop stops the worker pool gracefully
func (p *WorkerPool) Stop() error {
	if !p.running.Load() {
		return fmt.Errorf("worker pool not running")
	}

	p.running.Store(false)

	// Close task queue to signal workers to stop
	close(p.taskQueue)

	// Wait for all workers to finish
	p.wg.Wait()

	// Close result channel
	close(p.resultChan)

	// Cancel context
	p.cancel()

	return nil
}

// Submit submits a task to the worker pool
func (p *WorkerPool) Submit(task Task) error {
	if !p.running.Load() {
		return fmt.Errorf("worker pool not running")
	}

	select {
	case p.taskQueue <- task:
		atomic.AddUint64(&p.tasksSubmitted, 1)
		return nil
	case <-p.ctx.Done():
		return fmt.Errorf("worker pool stopped")
	default:
		return fmt.Errorf("task queue full")
	}
}

// SubmitAsync submits a task and returns a channel for the result
func (p *WorkerPool) SubmitAsync(task Task) (<-chan Result, error) {
	if err := p.Submit(task); err != nil {
		return nil, err
	}

	// Create result channel for this specific task
	resultChan := make(chan Result, 1)

	// Start goroutine to wait for result
	go func() {
		for result := range p.resultChan {
			if result.TaskID == task.ID() {
				resultChan <- result
				close(resultChan)
				return
			}
		}
	}()

	return resultChan, nil
}

// Results returns the result channel
func (p *WorkerPool) Results() <-chan Result {
	return p.resultChan
}

// worker is the worker goroutine
func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()

	for task := range p.taskQueue {
		start := time.Now()

		// Execute task with timeout
		ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
		err := task.Execute(ctx)
		cancel()

		duration := time.Since(start)

		// Update metrics
		atomic.AddUint64(&p.tasksCompleted, 1)
		atomic.AddUint64(&p.totalLatencyUs, uint64(duration.Microseconds()))

		success := err == nil
		if success {
			atomic.AddUint64(&p.tasksSucceeded, 1)
		} else {
			atomic.AddUint64(&p.tasksFailed, 1)
		}

		// Send result
		result := Result{
			TaskID:    task.ID(),
			Success:   success,
			Error:     err,
			Duration:  duration,
			Timestamp: time.Now(),
		}

		select {
		case p.resultChan <- result:
		default:
			// Result channel full, drop result
		}
	}
}

// Stats returns current pool statistics
func (p *WorkerPool) Stats() WorkerPoolStats {
	submitted := atomic.LoadUint64(&p.tasksSubmitted)
	completed := atomic.LoadUint64(&p.tasksCompleted)
	succeeded := atomic.LoadUint64(&p.tasksSucceeded)
	failed := atomic.LoadUint64(&p.tasksFailed)
	totalLatency := atomic.LoadUint64(&p.totalLatencyUs)

	avgLatencyUs := uint64(0)
	if completed > 0 {
		avgLatencyUs = totalLatency / completed
	}

	return WorkerPoolStats{
		Workers:        p.workers,
		QueueSize:      len(p.taskQueue),
		QueueCapacity:  cap(p.taskQueue),
		TasksSubmitted: submitted,
		TasksCompleted: completed,
		TasksSucceeded: succeeded,
		TasksFailed:    failed,
		TasksPending:   submitted - completed,
		AvgLatencyUs:   avgLatencyUs,
		Running:        p.running.Load(),
	}
}

// WorkerPoolStats holds worker pool statistics
type WorkerPoolStats struct {
	Workers        int
	QueueSize      int
	QueueCapacity  int
	TasksSubmitted uint64
	TasksCompleted uint64
	TasksSucceeded uint64
	TasksFailed    uint64
	TasksPending   uint64
	AvgLatencyUs   uint64
	Running        bool
}

// OrderTask wraps an order execution as a task
type OrderTask struct {
	id       string
	order    *PooledOrder
	executor func(context.Context, *PooledOrder) error
}

// NewOrderTask creates a new order task
func NewOrderTask(id string, order *PooledOrder, executor func(context.Context, *PooledOrder) error) *OrderTask {
	return &OrderTask{
		id:       id,
		order:    order,
		executor: executor,
	}
}

// Execute executes the order task
func (t *OrderTask) Execute(ctx context.Context) error {
	return t.executor(ctx, t.order)
}

// ID returns the task ID
func (t *OrderTask) ID() string {
	return t.id
}
