package ebitenapp

import (
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Batch GUI wheel input separately from camera gestures (§16.6 of
// DESIGN_GPU_RENDERER). The callback and Update can run on different threads.
type scrollBatch struct {
	x, y, zoomY        float64
	zoomX              float64 // horizontal notch count; Shift turns a macOS wheel sideways
	panX, panY         float64
	pinches            []input.PinchEvent
	pointerX, pointerY int32
	hasPointer         bool
}

type scrollCollector struct {
	mu                                     sync.Mutex
	active                                 bool
	pending                                scrollBatch
	pointScale, pointOffsetX, pointOffsetY float64
	point                                  [2]float64
	hasPoint                               bool
}

var nativeScroll scrollCollector

// Browser event positions are CSS pixels; use the same centred letterbox as
// Ebitengine's logical pointer (DESIGN_BROWSER_HOST §4 contract 8).
func (c *scrollCollector) viewport(w, h, outsideW, outsideH int) {
	if outsideW <= 0 || outsideH <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pointScale = max(float64(w)/float64(outsideW), float64(h)/float64(outsideH))
	c.pointOffsetX = (float64(outsideW)*c.pointScale - float64(w)) / 2
	c.pointOffsetY = (float64(outsideH)*c.pointScale - float64(h)) / 2
}

// collect atomically pairs each event with its optional browser CSS position.
// Native gestures keep using the ordinary sampled pointer.
func (c *scrollCollector) collect(batch scrollBatch, point *[2]float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return
	}
	c.pending.x += batch.x
	c.pending.y += batch.y
	c.pending.zoomY += batch.zoomY
	c.pending.zoomX += batch.zoomX
	c.pending.panX += batch.panX
	c.pending.panY += batch.panY
	for _, event := range batch.pinches {
		if event.Began && point != nil && c.pointScale > 0 {
			event.X = int32(point[0]*c.pointScale - c.pointOffsetX)
			event.Y = int32(point[1]*c.pointScale - c.pointOffsetY)
			event.Positioned = true
		}
		c.pending.pinches = append(c.pending.pinches, event)
	}
	if point != nil && c.pointScale > 0 {
		c.point, c.hasPoint = *point, true
	}
}

func (c *scrollCollector) setActive(active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active, c.pending = active, scrollBatch{}
	c.hasPoint = false
}

func (c *scrollCollector) add(x, y, wheelX, wheelY float64, precise, momentum bool) {
	var batch scrollBatch
	if precise {
		if !momentum {
			batch.panX, batch.panY = x, y
		}
		// Match Ebitengine 2.10.1's Cocoa conversion for existing GUI controls.
		// Panning above keeps the original device-independent point deltas.
		x *= 0.1
		y *= 0.1
	} else if !momentum {
		batch.zoomX, batch.zoomY = wheelX, wheelY
	}
	batch.x, batch.y = x, y
	c.collect(batch, nil)
}

func (c *scrollCollector) pinch(event input.PinchEvent) {
	c.collect(scrollBatch{pinches: []input.PinchEvent{event}}, nil)
}

func (c *scrollCollector) take(fallbackX, fallbackY float64) scrollBatch {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return scrollBatch{x: fallbackX, y: fallbackY, zoomX: fallbackX, zoomY: fallbackY}
	}
	batch := c.pending
	c.pending = scrollBatch{}
	if c.hasPoint {
		batch.pointerX = int32(c.point[0]*c.pointScale - c.pointOffsetX)
		batch.pointerY = int32(c.point[1]*c.pointScale - c.pointOffsetY)
		batch.hasPointer = true
	}
	// Transfer ownership of the event slice. An empty native batch stays empty:
	// mixing it with Ebiten's separately accumulated wheel would replay events.
	return batch
}
