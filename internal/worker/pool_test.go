package worker

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPool_BasicExecution(t *testing.T) {
	pool := NewWorkerPool(3)
	var count int64

	for i := 0; i < 5; i++ {
		pool.Submit(func() {
			atomic.AddInt64(&count, 1)
		})
	}

	pool.Drain()

	if got := atomic.LoadInt64(&count); got != 5 {
		t.Errorf("expected 5 tasks completed, got %d", got)
	}
}

func TestWorkerPool_ConcurrencyLimit(t *testing.T) {
	maxConcurrent := 2
	pool := NewWorkerPool(maxConcurrent)
	var running int64
	var maxRunning int64

	for i := 0; i < 10; i++ {
		pool.Submit(func() {
			cur := atomic.AddInt64(&running, 1)
			// Track max concurrent
			for {
				old := atomic.LoadInt64(&maxRunning)
				if cur <= old || atomic.CompareAndSwapInt64(&maxRunning, old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt64(&running, -1)
		})
	}

	pool.Drain()

	if got := atomic.LoadInt64(&maxRunning); got > int64(maxConcurrent) {
		t.Errorf("max concurrent was %d, expected <= %d", got, maxConcurrent)
	}
}

func TestWorkerPool_DrainReturns(t *testing.T) {
	pool := NewWorkerPool(2)

	done := make(chan struct{})
	pool.Submit(func() {
		time.Sleep(50 * time.Millisecond)
	})

	go func() {
		pool.Drain()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("Drain did not return in time")
	}
}

func TestWorkerPool_EmptyDrain(t *testing.T) {
	pool := NewWorkerPool(5)
	// Drain with no submissions should return immediately
	done := make(chan struct{})
	go func() {
		pool.Drain()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(1 * time.Second):
		t.Fatal("Drain on empty pool did not return")
	}
}
