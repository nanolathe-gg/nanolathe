package ebitenapp

import "sync"

// The native scroll monitor keeps total scrolling and active-touch scrolling
// in one batch. Menus consume the total; modern zoom consumes zoomY (§16.6 of
// DESIGN_GPU_RENDERER). The callback and Update can run on different threads.
type scrollBatch struct{ x, y, zoomY float64 }

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

func (c *scrollCollector) add(x, y float64, momentum bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return
	}
	c.pending.x += x
	c.pending.y += y
	if !momentum {
		c.pending.zoomY += y
	}
}

func (c *scrollCollector) take(fallbackX, fallbackY float64) scrollBatch {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return scrollBatch{fallbackX, fallbackY, fallbackY}
	}
	batch := c.pending
	c.pending = scrollBatch{}
	// While active this is the complete native batch, including empty polls.
	// Mixing it with Ebiten's separately accumulated wheel would replay events.
	return batch
}
