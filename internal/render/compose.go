// Package render implements the ten-strip frame composer [03 §1].
package render

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// StripObject is the strip lifecycle contract [03 §1] C4.
// The update dispatcher evaluates ShouldRemove BEFORE Update for every object.
type StripObject interface {
	ShouldRemove(tick uint32) bool
	Update(tick uint32)
}

// Strip is a vector descriptor for effect strips [03 §1].
// Each strip holds at most 401 records in steady state; producers append at
// the end and the oldest is destroyed first when the pre-insert count exceeds
// 400 [03 §1] C4.
type Strip struct {
	Objects []StripObject
}

// Append appends obj at the end, evicting the oldest first when the
// pre-insert count exceeds 400 [03 §1] C4. Steady state is at most 401.
func (s *Strip) Append(obj StripObject) {
	if s == nil || obj == nil {
		return
	}
	if len(s.Objects) > 400 { // pre-insert >400 destroys oldest first [03 §1]
		copy(s.Objects[0:], s.Objects[1:])
		s.Objects[len(s.Objects)-1] = nil
		s.Objects = s.Objects[:len(s.Objects)-1]
	}
	s.Objects = append(s.Objects, obj)
}

// Update evaluates removal BEFORE update for every object and stably compacts
// on a positive verdict so survivors keep their order [03 §1] C4.
// A terminal condition created during an Update is noticed only on the next
// invocation.
func (s *Strip) Update(tick uint32) {
	if s == nil || len(s.Objects) == 0 {
		return
	}
	n := len(s.Objects)
	write := 0
	for read := 0; read < n; read++ {
		obj := s.Objects[read]
		if obj == nil {
			continue
		}
		if obj.ShouldRemove(tick) { // removal BEFORE update [03 §1] C4
			continue
		}
		if write != read {
			s.Objects[write] = obj
		}
		obj.Update(tick)
		write++
	}
	for i := write; i < n; i++ {
		s.Objects[i] = nil
	}
	s.Objects = s.Objects[:write]
}

// Draw forwards each stored object to its draw entry if it implements
// Draw() [03 §1]. Skeleton seam for later blitters.
func (s *Strip) Draw() {
	if s == nil {
		return
	}
	for _, obj := range s.Objects {
		if d, ok := obj.(interface{ Draw() }); ok {
			d.Draw()
		}
	}
}

// YBuckets implements per-row screen-Y bucket insertion [03 §1] C3.
// Bucket row is computed from projected screen Y, appended in enumeration
// order. Paint order is Y-sorted rows with in-row enumeration order — there
// is no depth test [03 §1] C3.
type YBuckets struct {
	buckets map[int][]int
}

// Clear resets the bucket map.
func (b *YBuckets) Clear() {
	if b.buckets == nil {
		b.buckets = make(map[int][]int)
		return
	}
	for k := range b.buckets {
		delete(b.buckets, k)
	}
}

// Insert appends id to the bucket for row in enumeration order [03 §1] C3.
func (b *YBuckets) Insert(row, id int) {
	if b.buckets == nil {
		b.buckets = make(map[int][]int)
	}
	b.buckets[row] = append(b.buckets[row], id)
}

// Rows returns the sorted row keys (Y-sorted) [03 §1] C3.
func (b *YBuckets) Rows() []int {
	if b.buckets == nil || len(b.buckets) == 0 {
		return nil
	}
	rows := make([]int, 0, len(b.buckets))
	for r := range b.buckets {
		rows = append(rows, r)
	}
	sort.Ints(rows)
	return rows
}

// Ordered returns ids in paint order: Y-sorted rows with in-row enumeration
// order [03 §1] C3. No depth test is performed.
func (b *YBuckets) Ordered() []int {
	if b.buckets == nil || len(b.buckets) == 0 {
		return nil
	}
	rows := b.Rows()
	var out []int
	for _, r := range rows {
		out = append(out, b.buckets[r]...)
	}
	return out
}

// FixedEffectCap is the fixed pool capacity [03 §1] C5 (I5).
const FixedEffectCap = 300 // 0x54-byte records, 300 entries [03 §1] (I5)

// FixedEffectPool is the fixed effect pool [03 §1] C5.
// It holds up to 300 fixed-size records; appends at or above the cap allocate
// nothing [03 §1]. Full integration lives in effects.go (WU-13-2).
type FixedEffectPool struct {
	records  []EffectRecord
	gravity  numeric.Fixed                          // default per-tick gravity when record.Gravity is zero [03 §2.2]
	heightAt func(x, z numeric.Fixed) numeric.Fixed // terrain height query; nil skips terrain/water contact [03 §1]
	seaLevel numeric.Fixed                          // sea level in world units byte*65536 [03 §2.2]
}

// Composer option bits for skeleton gating [03 §1] C1.
// Exact bit values are presentation state; labels are gated on OptionsByte
// and owner==local slot, two optional overlays under option bits and
// render-mode [03 §1].
const (
	OptLabels   = 0x02 // unit labels [03 §1] C1
	OptOverlayA = 0x04 // first optional overlay [03 §1] C1
	OptOverlayB = 0x08 // second optional overlay [03 §1] C1
)

// ComposerHooks are draw-callback seams for the skeleton [03 §1].
// Actual blitters are later units' callbacks (C1–C4).
type ComposerHooks struct {
	PreWorld      func()                       // pre-world presentation (shake/camera) [03 §5.6]
	Terrain       func()                       // terrain/static prep [03 §1] step1
	Minimap       func()                       // minimap/radar prep [03 §1] step1
	Clip          func()                       // viewport clip [03 §1] step1
	Barrier       func(pass int)               // barrier closed with pass number 0..9 [03 §1]
	DrawStrip     func(idx int)                // strip draw dispatcher [03 §1]
	BucketBuild   func()                       // screen-Y bucket build [03 §1] step3
	FeaturePass   func()                       // feature pass owning unexplored marker [03 §1] step3
	UnitTraversal func(kind string)            // intervening/aux unit traversals [03 §1] steps5,7
	Projectiles   func()                       // projectile pool between strips 6 and 7 [03 §1] C2
	Effects       func()                       // fixed effect pool between strips 6 and 7 [03 §1] C2
	KeyOverlay    func()                       // key-controlled overlay [03 §1] step9
	UnitLabel     func(unit snapshot.UnitView) // unit labels gated on options byte and owner==local [03 §1] step9
	OverlayA      func()                       // first optional overlay [03 §1] step9
	OverlayB      func()                       // second optional overlay [03 §1] step9
	Fog           func()                       // fog presentation [03 §1] step10 C2
	Selection     func()                       // selection rectangle before present [03 §1] step10
	Interface     func()                       // interface/diagnostics [03 §1] step10
	Shake         func()                       // shake consumption seam [03 §5.6]
}

// IndexedCompositor is the narrow service boundary consumed by a client
// frame. The frame owns no simulation or asset pointers; implementations read
// immutable snapshots and emit indexed pixels through their own backend [03
// §1], [I6].
type IndexedCompositor interface {
	Compose(*snapshot.Frame, float32, int)
}

// Composer is the ten-strip frame composer [03 §1] C1–C4.
type Composer struct {
	Strips  [10]Strip
	Effects FixedEffectPool
	Fog     *visibility.FogCache

	// Gating state [03 §1] C1.
	OptionsByte uint8 // options byte for labels/overlays [03 §1]
	LocalSlot   uint8 // local player slot for label owner==local [03 §1]
	KeyDown     bool  // key-controlled overlay predicate [03 §1]

	// Camera for screen-Y projection [03 §2.5] C3.
	Cam *camera.Camera

	// Hooks are callback seams; actual blitters are later units [03 §1].
	Hooks ComposerHooks

	// ShakeHook is the shake seam [03 §5.6] — composer calls it for shake
	// rather than editing shake.go. Invoked once per frame after world strips
	// and before fog/selection [03 §1][03 §5.6].
	ShakeHook func()

	// Barriers records barrier pass numbers closed in order each Frame [03 §1].
	Barriers []int

	// Buckets holds per-row screen-Y buckets [03 §1] C3.
	Buckets YBuckets

	// internal alpha/camera state not needed for ordering
}

// barrier closes a strip with its pass number [03 §1].
func (c *Composer) barrier(pass int) {
	c.Barriers = append(c.Barriers, pass)
	if c.Hooks.Barrier != nil {
		c.Hooks.Barrier(pass)
	}
}

// drawStrip forwards the strip's objects to their draw entries [03 §1].
func (c *Composer) drawStrip(idx int) {
	if idx < 0 || idx >= len(c.Strips) {
		return
	}
	c.Strips[idx].Draw()
}

// projectRow computes the bucket row from projected screen Y [03 §1] C3 [03 §2.5].
// Row is projected screen Y, appended in enumeration order; paint is Y-sorted
// rows with in-row enumeration order and no depth test [03 §1] C3.
func (c *Composer) projectRow(uv snapshot.UnitView, alpha float32) int {
	if c.Cam != nil {
		// Interpolate world position for presentation [03 §2.4] C12 (sanctioned divergence I6 islerp).
		// For bucket row we use current position; alpha interpolation is presentation-only.
		// Use camera WorldToScreen shear: screenY = (worldZ>>16) - ((worldY>>16)>>1) - cameraZ + OriginY [03 §2.5].
		_, sy := c.Cam.WorldToScreen(uv.X, uv.Y, uv.Z)
		return int(sy)
	}
	// Fallback deterministic row when no camera: use Z pixel plus truncated Y shear approximation.
	wz := int32(int64(uv.Z) >> 16)
	wy := int32(int64(uv.Y) >> 16)
	// numeric.Fixed is 16.16 int64; shear is wy>>1 [03 §2.5]
	_ = numeric.Fixed(0) // ensure numeric import used (world units are Fixed [I2])
	_ = alpha
	return int(wz - (wy >> 1))
}

// Frame stages the frame through exactly ten fixed-order strips each closed
// by a barrier with its pass number, in the [03 §1] order:
//
//	1 terrain/minimap/clip unconditionally;
//	2 strips 0–2 unconditional;
//	3 screen-Y bucket build + feature pass owning unexplored marker;
//	4 strips 3–4 unconditional;
//	5 intervening unit traversals;
//	6 strip 5 unconditional;
//	7 strip 6, projectile pool, fixed effect pool, strip 7, auxiliary traversal — all gated on nonzero render-mode;
//	8 strip 8 always outside that gate;
//	9 key-controlled overlay + unit labels gated on options byte and owner==local slot + strip 9 + two optional overlays;
//	10 fog under render-mode after world drawing before selection/interface [03 §1] C1 C2.
func (c *Composer) Frame(f *snapshot.Frame, alpha float32, mode int) {
	if c == nil {
		return
	}
	c.Barriers = c.Barriers[:0]

	// 1 Terrain/static preparation, minimap/radar preparation, and viewport clip, unconditionally [03 §1].
	if c.Hooks.PreWorld != nil {
		c.Hooks.PreWorld()
	}
	if c.Hooks.Terrain != nil {
		c.Hooks.Terrain()
	}
	if c.Hooks.Minimap != nil {
		c.Hooks.Minimap()
	}
	if c.Hooks.Clip != nil {
		c.Hooks.Clip()
	}

	// 2 Strips 0, 1, and 2, unconditionally [03 §1].
	for i := 0; i <= 2; i++ {
		if c.Hooks.DrawStrip != nil {
			c.Hooks.DrawStrip(i)
		}
		c.drawStrip(i)
		c.barrier(i)
	}

	// 3 Screen-Y bucket build, then the feature pass, which owns the unexplored-marker logic [03 §1].
	if c.Hooks.BucketBuild != nil {
		c.Hooks.BucketBuild()
	}
	c.Buckets.Clear()
	if f != nil {
		for _, uv := range f.Units {
			row := c.projectRow(uv, alpha)
			c.Buckets.Insert(row, int(uv.Slot))
		}
	}
	if c.Hooks.FeaturePass != nil {
		c.Hooks.FeaturePass()
	}

	// 4 Strips 3 and 4, unconditionally [03 §1].
	for i := 3; i <= 4; i++ {
		if c.Hooks.DrawStrip != nil {
			c.Hooks.DrawStrip(i)
		}
		c.drawStrip(i)
		c.barrier(i)
	}

	// 5 Intervening unit traversals under mixed internal predicates [03 §1].
	if c.Hooks.UnitTraversal != nil {
		c.Hooks.UnitTraversal("mid")
	}

	// 6 Strip 5, unconditionally [03 §1].
	if c.Hooks.DrawStrip != nil {
		c.Hooks.DrawStrip(5)
	}
	c.drawStrip(5)
	c.barrier(5)

	// 7 Strip 6, the projectile pool, the fixed effect pool, strip 7, and the
	// remaining unit auxiliary-draw traversal — all gated on the composer’s
	// render-mode argument being nonzero [03 §1].
	if mode != 0 {
		if c.Hooks.DrawStrip != nil {
			c.Hooks.DrawStrip(6)
		}
		c.drawStrip(6)
		c.barrier(6)

		// Projectiles and effects sit between strips 6 and 7 and are not strip
		// objects [03 §1] C2.
		if c.Hooks.Projectiles != nil {
			c.Hooks.Projectiles()
		}
		if c.Hooks.Effects != nil {
			c.Hooks.Effects()
		}

		if c.Hooks.DrawStrip != nil {
			c.Hooks.DrawStrip(7)
		}
		c.drawStrip(7)
		c.barrier(7)

		if c.Hooks.UnitTraversal != nil {
			c.Hooks.UnitTraversal("aux")
		}
	}

	// 8 Strip 8 draws always, outside the render-mode gate [03 §1].
	if c.Hooks.DrawStrip != nil {
		c.Hooks.DrawStrip(8)
	}
	c.drawStrip(8)
	c.barrier(8)

	// Shake hook — expose a hook the composer calls for shake rather than editing shake.go [03 §5.6].
	// Consumption is once per tick after the projectile phase [03 §5.6]; for the
	// composer skeleton we invoke it once per frame after world strips. This is
	// presentation-only and uses the CRT stream (I4).
	if c.ShakeHook != nil {
		c.ShakeHook()
	}
	if c.Hooks.Shake != nil {
		c.Hooks.Shake()
	}

	// 9 A key-controlled overlay under its key predicate; then unit labels
	// (each gated on an options byte and on the labeled owner equaling the
	// local player slot) followed by strip 9 under the render-mode argument;
	// then two optional mode overlays under option bits and the same argument [03 §1].
	if c.KeyDown {
		if c.Hooks.KeyOverlay != nil {
			c.Hooks.KeyOverlay()
		}
	}
	// Unit labels gated on options byte and owner==local slot [03 §1].
	if c.OptionsByte&OptLabels != 0 && f != nil {
		for _, uv := range f.Units {
			if uv.Owner == c.LocalSlot {
				if c.Hooks.UnitLabel != nil {
					c.Hooks.UnitLabel(uv)
				}
			}
		}
	}
	if mode != 0 {
		if c.Hooks.DrawStrip != nil {
			c.Hooks.DrawStrip(9)
		}
		c.drawStrip(9)
		c.barrier(9)

		if c.OptionsByte&OptOverlayA != 0 {
			if c.Hooks.OverlayA != nil {
				c.Hooks.OverlayA()
			}
		}
		if c.OptionsByte&OptOverlayB != 0 {
			if c.Hooks.OverlayB != nil {
				c.Hooks.OverlayB()
			}
		}
	}

	// 10 Fog presentation under the render-mode argument, after all ten strips,
	// projectiles, and effects but before selection/interface work [03 §1].
	// Fog covers world drawing but never selection or interface [03 §1] C2.
	if mode != 0 {
		if c.Hooks.Fog != nil {
			c.Hooks.Fog()
		}
	}

	// Selection rectangle, interface, diagnostics, and present prep close the
	// frame under local predicates [03 §1]. Fog never covers these [03 §1] C2.
	if c.Hooks.Selection != nil {
		c.Hooks.Selection()
	}
	if c.Hooks.Interface != nil {
		c.Hooks.Interface()
	}
}

// Compose is the canonical compositor entry point. Frame is retained as the
// historical name for existing callers; both paths execute the same ordered
// ten-strip composer so a client cannot accidentally select a second draw
// order [03 §1].
func (c *Composer) Compose(f *snapshot.Frame, alpha float32, mode int) {
	c.Frame(f, alpha, mode)
}

// Update evaluates removal BEFORE update for every strip and stably compacts
// on a positive verdict [03 §1] C4, then ticks the fixed effect pool whose
// emptied records compact within the same call [03 §1] C5.
func (c *Composer) Update(tick uint32) {
	if c == nil {
		return
	}
	for i := range c.Strips {
		c.Strips[i].Update(tick)
	}
	c.Effects.Update(tick)
}
