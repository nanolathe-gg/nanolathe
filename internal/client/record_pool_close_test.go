package client

import (
	"runtime"
	"testing"
)

// TestClientCloseStopsRecordPool pins the pool's teardown. A client that is
// dropped without it strands one worker goroutine per hardware thread, and each
// worker holds a clone of the whole client and that clone's scratch arena, so
// the leak is unbounded across battles rather than one goroutine
// (docs/DESIGN_GPU_RENDERER.md §13.9).
func TestClientCloseStopsRecordPool(t *testing.T) {
	const participants = 5 // the recording goroutine plus four workers
	c := newTestClient(t)
	before := runtime.NumGoroutine()
	c.recordPool = newRecordPool(participants)
	pool := c.recordPool
	if got := len(pool.workers); got != participants-1 {
		t.Fatalf("pool started %d workers, want %d", got, participants-1)
	}
	if got := runtime.NumGoroutine(); got < before+participants-1 {
		t.Fatalf("goroutine count %d did not grow by the %d workers from %d", got, participants-1, before)
	}

	// close waits for every worker to leave its loop, so the count is settled
	// the moment it returns — no polling, no sleep.
	c.Close()
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutine count %d after Close, want no more than the %d before the pool started", got, before)
	}
	if c.recordPool != nil {
		t.Fatal("Close left the pool attached to the client")
	}
	if pool.workers != nil || pool.wake != nil {
		t.Fatal("Close kept the worker clones and their arenas alive")
	}

	// Teardown is idempotent, and a client that never recorded a frame has no
	// pool to stop.
	c.Close()
	pool.close()
	newTestClient(t).Close()
	var nilClient *Client
	nilClient.Close()
}
