package main

import (
	"fmt"
	"runtime"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
)

// Host counters deliberately describe Go and the battle update, not retail's
// allocator or its nine timing buckets (DESIGN_DEVELOPER_TOOLS §2.2).
type developerHostProfile struct {
	enabled                           bool
	updated                           time.Time
	updates                           uint64
	updateDuration                    time.Duration
	heap, heapObjects, totalAllocated uint64
	gc                                uint32
	goroutines                        int
}

func (b *battleSession) sampleDeveloperHost(start time.Time) {
	p := &b.developer.host
	p.updates++
	p.updateDuration = time.Since(start)
	if !p.updated.IsZero() && time.Since(p.updated) < time.Second {
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	p.heap, p.heapObjects, p.totalAllocated, p.gc = m.HeapAlloc, m.HeapObjects, m.TotalAlloc, m.NumGC
	p.goroutines = runtime.NumGoroutine()
	p.updated = time.Now()
}

func (b *battleSession) drawDeveloperHost(c *client.Client, font *formats.FNT) {
	if b == nil || c == nil || font == nil || !b.developer.host.enabled {
		return
	}
	p := &b.developer.host
	lines := []string{
		"Nanolathe host profile",
		fmt.Sprintf("Battle updates: %d | last %.3f ms", p.updates, float64(p.updateDuration)/float64(time.Millisecond)),
		fmt.Sprintf("Go heap: %.2f MiB | objects: %d", float64(p.heap)/(1024*1024), p.heapObjects),
		fmt.Sprintf("Allocated total: %.2f MiB | GC cycles: %d", float64(p.totalAllocated)/(1024*1024), p.gc),
		fmt.Sprintf("Goroutines: %d | memory sampled each second", p.goroutines),
		"Retail timing buckets / allocator counters: unavailable",
	}
	pitch := int(font.Height) + 3
	width, _ := c.Size()
	x := max(134, width-420)
	c.UIShadeRect(c.PaletteTables(), x-3, 36, 419, pitch*len(lines)+6, -24)
	for i, line := range lines {
		c.UIText(font, line, x, 39+i*pitch, c.GUIColor(15))
	}
}
