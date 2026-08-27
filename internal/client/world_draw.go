package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
)

// worldDrawable is one admitted object in the painter pass. The source slice
// order is retained for equal rows; only rows are sorted [03 §1].
type worldDrawable struct {
	row     int32
	unit    *frame.UnitView
	feature *frame.FeatureView
	screenX int32
	screenY int32
}

type worldBucket struct {
	row   int32
	items []int
}

// worldBuckets is a reusable row-indexed slice. It deliberately avoids maps in
// the frame path and retains bucket/item capacity between frames [03 §1][I1].
type worldBuckets struct {
	items        []worldDrawable
	bucket       []worldBucket
	orderedItems []worldDrawable
}

func (b *worldBuckets) reset() {
	for i := range b.bucket {
		b.bucket[i].items = b.bucket[i].items[:0]
	}
	b.bucket = b.bucket[:0]
	b.items = b.items[:0]
	b.orderedItems = b.orderedItems[:0]
}

func (b *worldBuckets) add(v worldDrawable) {
	idx := len(b.items)
	b.items = append(b.items, v)
	for i := range b.bucket {
		if b.bucket[i].row == v.row {
			b.bucket[i].items = append(b.bucket[i].items, idx)
			return
		}
	}
	if len(b.bucket) < cap(b.bucket) {
		b.bucket = b.bucket[:len(b.bucket)+1]
		last := &b.bucket[len(b.bucket)-1]
		last.row = v.row
		last.items = append(last.items[:0], idx)
		return
	}
	b.bucket = append(b.bucket, worldBucket{row: v.row})
	b.bucket[len(b.bucket)-1].items = append(b.bucket[len(b.bucket)-1].items, idx)
}

func (b *worldBuckets) ordered() []worldDrawable {
	// Insertion sort is allocation-free and the bucket count is bounded by the
	// distinct rows admitted for this frame. Existing bucket order is not a
	// contract; equal-row item order remains source enumeration order [03 §1].
	for i := 1; i < len(b.bucket); i++ {
		v := b.bucket[i]
		j := i
		for j > 0 && v.row < b.bucket[j-1].row {
			b.bucket[j] = b.bucket[j-1]
			j--
		}
		b.bucket[j] = v
	}
	for _, row := range b.bucket {
		for _, idx := range row.items {
			b.orderedItems = append(b.orderedItems, b.items[idx])
		}
	}
	return b.orderedItems
}

// drawCommittedFrame is the sole production frame ordering. It reads one
// immutable frame and writes the indexed surface; cursor conversion happens
// outside this sequence [03 §1][I6].
func (c *Client) drawCommittedFrame(cur *frame.Frame, ok bool, mode int) {
	if c == nil || len(c.indexed) != c.width*c.height {
		return
	}
	// The committed tick drives every presentation animator that reads it
	// directly (nanoframe pulse, nanolathe particles) [03 §5.2][03 §5.5].
	if cur != nil {
		c.frameTick = cur.Tick
	}
	// A missing terrain source is an empty indexed surface. No synthetic art is
	// emitted when the frontend has no world attachment [I9].
	for i := range c.indexed {
		c.indexed[i] = 0
	}
	// Consume the committed tick's shake before any camera-dependent draw so
	// terrain, world, fog, selection, and cursor share one camera. The event's
	// authored request remains the post-projectile presentation event [03 §5.6].
	c.consumeShake(cur)

	// Terrain/static preparation, radar preparation, and viewport clipping are
	// unconditional. Radar and clip have no concrete frame input yet.
	c.drawTerrainPrep()
	// TODO(T23): strip slots 0-2 have no published live producer; preserve the
	// established positions without claiming that an empty strip was drawn.

	// The first feature traversal owns the never-seen admission. Features with
	// height >= 10 are deferred to the screen-Y pass [03 §5.1.3].
	c.drawFeaturePass(cur, ok, false)
	// TODO(T23): strip slots 3-4 have no published live producer.

	// Units and deferred tall features share one stable screen-Y painter pass.
	c.drawWorldPass(cur, ok)
	// TODO(T23): strip slot 5 has no published live producer.

	if mode != 0 {
		// TODO(T23): strip slot 6 has no published live producer.
		c.drawProjectiles(cur)
		c.drawEffects(cur)
		// The shake request was consumed before projection so this same frame has
		// one camera for projectile and subsequent presentation passes [03 §5.6].
		// TODO(T23): strip slot 7 and auxiliary traversal have no published live
		// producer; retain the established position without inventing a route.
		// Auxiliary unit traversal has no published auxiliary draw records yet;
		// leave this established slot empty rather than inventing a route [03 §1].
	}
	// Strip slot 8 is unconditional; no published producer exists [03 §1].

	// Key overlays and labels have no concrete authored client route yet. Strip
	// 9 remains mode-gated; unknown producers remain unresolved [03 §1][I9].
	if mode != 0 {
		// TODO(T23): strip slot 9 has no published live producer.
		c.drawFog(cur)
	}
	c.drawSelectionStage()
	c.drawInterface(cur)
}

func (c *Client) drawFeaturePass(cur *frame.Frame, ok, deferred bool) {
	if !ok || cur == nil || c.cam == nil {
		return
	}
	for i := range cur.Features {
		f := &cur.Features[i]
		if (f.Height >= 10) != deferred || fogUnexploredFeature(cur.Fog, *f) {
			continue
		}
		c.drawFeature(f)
	}
}

func (c *Client) drawWorldPass(cur *frame.Frame, ok bool) {
	if !ok || cur == nil || c.cam == nil {
		return
	}
	b := &c.worldBuckets
	b.reset()
	viewer := cur.Selection.LocalPlayer
	for i := range cur.Units {
		u := &cur.Units[i]
		if u.Owner != viewer {
			if fogUnexploredUnit(cur.Fog, *u) || !unitVisibleForFrame(cur, *u, viewer) {
				continue
			}
		}
		sx, sy := c.cam.WorldToScreen(u.X, u.Y, u.Z)
		sx -= camera.OriginX
		sy -= camera.OriginY
		b.add(worldDrawable{row: sy, unit: u, screenX: sx, screenY: sy})
	}
	for i := range cur.Features {
		f := &cur.Features[i]
		if f.Height < 10 || fogUnexploredFeature(cur.Fog, *f) {
			continue
		}
		sx, sy := c.featureScreenPos(*f)
		b.add(worldDrawable{row: sy, feature: f, screenX: sx, screenY: sy})
	}
	for _, d := range b.ordered() {
		if d.unit != nil {
			u := *d.unit
			if u.Model != "" && c.drawUnitModel(u, d.screenX, d.screenY) {
				c.selectionChrome = append(c.selectionChrome, selectionChrome{view: u, screenX: d.screenX, screenY: d.screenY})
			}
			continue
		}
		if d.feature != nil {
			c.drawFeature(d.feature)
		}
	}
}

func (c *Client) drawFeature(f *frame.FeatureView) {
	if c == nil || f == nil {
		return
	}
	sx, sy := c.featureScreenPos(*f)
	is3DO := f.Model != "" && f.Filename == "" || (f.Filename == "" && f.SeqName == "")
	if is3DO && f.Model != "" {
		_ = c.drawFeatureModel(*f)
		return
	}
	shadowFrame := c.featureFrameFor(*f, true)
	normalFrame := c.featureFrameFor(*f, false)
	if shadowFrame != nil {
		c.blitGAFFrame(shadowFrame, int(sx)-int(shadowFrame.XOffset), int(sy)-int(shadowFrame.YOffset), true, f.ShadTrans)
	}
	if normalFrame != nil {
		c.blitGAFFrame(normalFrame, int(sx)-int(normalFrame.XOffset), int(sy)-int(normalFrame.YOffset), false, f.AnimTrans)
	}
}
