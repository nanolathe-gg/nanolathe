package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
)

// BenchmarkClassifyEffectStrips locks the per-frame strip staging at zero
// steady-state allocations once the buckets have their capacity. The composer
// visits ten barriers plus the fixed pool per frame, and each barrier used to
// allocate its own filtered slice [03 R-STRIP-01 §1–§3].
func BenchmarkClassifyEffectStrips(b *testing.B) {
	effects := make([]frame.EffectView, 0, 256)
	for i := range 256 {
		effects = append(effects, frame.EffectView{ID: uint32(i), Strip: int8(i % 11)})
	}
	c := &Client{}
	cur := &frame.Frame{Effects: effects}

	// Warm the buckets so the benchmark measures the steady state, not the
	// first frame's growth.
	for tick := range uint32(4) {
		cur.Tick = tick
		for strip := int8(0); strip < effectStripCount; strip++ {
			c.effectViewsForStrip(cur, strip)
		}
		c.unstrippedEffectViews(cur)
	}

	b.ReportAllocs()
	b.ResetTimer()
	total := 0
	for i := 0; i < b.N; i++ {
		cur.Tick = uint32(i) + 100
		for strip := int8(0); strip < effectStripCount; strip++ {
			total += len(c.effectViewsForStrip(cur, strip))
		}
		total += len(c.unstrippedEffectViews(cur))
	}
	_ = total
}
