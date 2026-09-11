package ebitenapp

import (
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Batch GUI wheel input separately from camera gestures (§16.6 of
// DESIGN_GPU_RENDERER). The callback and Update can run on different threads.
type scrollBatch struct {
	x, y, zoomY float64
	panX, panY  float64
	pinches     []input.PinchEvent
}

type scrollCollector struct {
	mu      sync.Mutex
	active  bool
	pending scrollBatch
}

var nativeScroll scrollCollector

func (c *scrollCollector) setActive(active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active, c.pending = active, scrollBatch{}
}

func (c *scrollCollector) add(x, y float64, precise, momentum bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return
	}
	if precise {
		if !momentum {
			c.pending.panX += x
			c.pending.panY += y
		}
		// Match Ebitengine 2.10.1's Cocoa conversion for existing GUI controls.
		// Panning above keeps the original device-independent point deltas.
		x *= 0.1
		y *= 0.1
	} else if !momentum {
		c.pending.zoomY += y
	}
	c.pending.x += x
	c.pending.y += y
}

func (c *scrollCollector) pinch(event input.PinchEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active {
		c.pending.pinches = append(c.pending.pinches, event)
	}
}

func (c *scrollCollector) take(fallbackX, fallbackY float64) scrollBatch {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return scrollBatch{x: fallbackX, y: fallbackY, zoomY: fallbackY}
	}
	batch := c.pending
	c.pending = scrollBatch{}
	// Transfer ownership of the event slice. An empty native batch stays empty:
	// mixing it with Ebiten's separately accumulated wheel would replay events.
	return batch
}
