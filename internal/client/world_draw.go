package client

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// worldDrawable is one admitted object in the painter pass. The source slice
// order is retained for equal rows; only rows are sorted by their world-Z plot
// row [03 R-RAST-01 §7].
type worldDrawable struct {
	row     int32
	unit    *frame.UnitView
	feature *frame.FeatureView
	screenX int32
	screenY int32
}

// cellPixels is the side of one plot cell in map pixels [03 §2.1].
const cellPixels = 16

// worldWindow is the frame's plot-cell window: the cells the feature passes
// walk, the rows pass A walks, and the bound on which units are bucketed at
// all. It is a pure function of the camera and the map extent, so both the
// short-feature pass and the bucket build derive the same window from the same
// committed camera [03 R-RAST-01 §6].
type worldWindow struct {
	// rowBase and colBase are the window's first cell before any clipping:
	// sixteen rows above and ten columns left of the camera origin. They stay
	// the origin the feature bucket key is measured from even when clipping
	// moves the first drawn cell, because the unit key and the feature key are
	// two different expressions measured from that same origin
	// [03 R-RAST-01 §6][03 R-RAST-01 §7].
	rowBase, colBase int32
	// bucketRows is the unclipped row count and the sole admission bound on a
	// unit: a unit whose row is outside 0..bucketRows-1 is not drawn this
	// frame. Pass B walks all of these rows; only pass A and the feature passes
	// are restricted to the clipped window [03 R-RAST-01 §7].
	bucketRows int32
	// firstRow/firstCol and rowCount/colCount are the window after the two
	// clips of [03 R-RAST-01 §6]: the part below zero is cut off the count,
	// and the map's last row and column are excluded.
	firstRow, firstCol int32
	rowCount, colCount int32
	// rowOffset is the bucket index of firstRow, i.e. how many rows the
	// below-zero reduction removed from the front of the window.
	rowOffset int32
}

// worldWindow derives this frame's plot-cell window from the committed camera.
//
// The row and column counts are the viewport measured in 16-pixel plot cells
// plus a fixed margin — thirty-two rows and twelve columns — sized once when
// the map is loaded, which together with the window origin gives sixteen rows
// of margin above the viewport and sixteen below, ten columns left and two
// right. [03 R-RAST-01 §6] records the origin and the clipping and names the
// two counts only as map-derived globals; the decomposition above is
// Established: the map loader stores the viewport width and height each
// truncated to whole plot cells, and sizes the window's column array at that
// column count plus twelve and its row array at that row count plus
// thirty-two. See the addendum reported for [03 R-RAST-01 §6].
//
// Retail measures the *battle* viewport (the beam rectangle inset at 128,32)
// while Nanolathe composes the world across the whole framebuffer and paints
// the chrome over it, so `cam.EffectiveView()` is 128 wider and 64 taller than
// retail's count. The window origin absorbs the difference exactly: retail's
// anchor formula folds the 128/32 beam offset in as eight columns and two rows
// [03 §5.1.4], which Nanolathe subtracts back out at the projection. Both
// windows therefore admit the same absolute pixel band — 160 map pixels left
// of the visible edge and 17 right, 256 above and 241 below — and neither
// culls a sprite the other would draw.
//
// Presentation zoom is not a retail concept: the effective view is used so
// that zooming out cannot clip a visible unit out of the window [F-P1-008].
//
// The camera origin is negative at the map's west and north edges — the clamp's
// floor is minus the viewport's leading inset, so that world column 0 can reach
// the viewport's left edge [07 §10] — and both cell conversions below are
// written for it. Go's `/` truncates toward zero, which is exactly what
// [03 R-RAST-01 §6-A] establishes for retail's own camera-to-cell divisions
// ("the dividend is biased by fifteen when negative before the arithmetic
// shift"), so a negative origin needs no floorDiv here and must not be given
// one: floor division would move the window origin one cell further out than
// retail's. clipWindowAxis then takes the below-zero part off the count, which
// is the same clip the positive case uses.
func (c *Client) worldWindow() worldWindow {
	var w worldWindow
	if c == nil || c.cam == nil {
		return w
	}
	viewW, viewH := c.cam.EffectiveView()
	w.bucketRows = viewH/cellPixels + 32
	bucketCols := viewW/cellPixels + 12
	w.rowBase = c.cam.Z/cellPixels - 16
	w.colBase = c.cam.X/cellPixels - 10
	var mapRows, mapCols int32
	if c.terrain != nil {
		mapRows, mapCols = c.terrain.CellH, c.terrain.CellW
	}
	w.firstRow, w.rowCount = clipWindowAxis(w.rowBase, w.bucketRows, mapRows)
	w.firstCol, w.colCount = clipWindowAxis(w.colBase, bucketCols, mapCols)
	w.rowOffset = w.firstRow - w.rowBase
	return w
}

// clipWindowAxis clips one axis of the frame window: the part of the window
// that falls below zero is taken off the count, and the window is then cut so
// that it stops one cell short of the map's last row or column
// [03 R-RAST-01 §6]. A zero map extent means the client has no terrain bound,
// in which case only the below-zero reduction applies.
func clipWindowAxis(base, count, cells int32) (first, n int32) {
	first, n = base, count
	if first < 0 {
		n += first
		first = 0
	}
	if cells > 0 && first+n > cells-1 {
		n = cells - 1 - first
	}
	if n < 0 {
		n = 0
	}
	return first, n
}

// admitsCell reports whether a plot cell is inside the clipped window, which
// is what decides whether its feature is drawn this frame [03 R-RAST-01 §6].
func (w worldWindow) admitsCell(cx, cz int32) bool {
	return cz >= w.firstRow && cz < w.firstRow+w.rowCount &&
		cx >= w.firstCol && cx < w.firstCol+w.colCount
}

// admitsPassARow reports whether a bucket row is one of the rows pass A walks,
// which is the clipped window rather than the whole bucket array
// [03 R-RAST-01 §6][03 R-RAST-01 §7].
func (w worldWindow) admitsPassARow(row int32) bool {
	return row >= w.rowOffset && row < w.rowOffset+w.rowCount
}

// unitBucketRow is the unit's world Z in 16-pixel plot rows relative to the
// camera. It is not a screen coordinate and carries none of the projection's
// half-height shear, so a climbing aircraft keeps the row of the ground it is
// over instead of sorting into an earlier row and painting under the structure
// it just left [03 R-RAST-01 §7].
//
// The division truncates toward zero for a negative camera origin, matching the
// biased shift of [03 R-RAST-01 §6-A].
func unitBucketRow(z numeric.Fixed, camZ int32) int32 {
	return (int32(int64(z)>>16)-camZ)/cellPixels + 16
}

// featureBucketRow is a feature's absolute plot-cell row measured from the
// window's unclipped first row. [03 R-RAST-01 §7] requires both this and
// unitBucketRow as written: the unit row divides one difference while the
// feature row differences two divisions, so the two disagree by one whenever
// the camera's Z is not a multiple of 16. They are deliberately not factored
// into one shared cell index.
func featureBucketRow(cellZ, camZ int32) int32 {
	return cellZ - (camZ/cellPixels - 16)
}

type worldBucket struct {
	row   int32
	items []int
}

// worldBuckets is a reusable row-indexed slice. It deliberately avoids maps in
// the frame path and retains bucket/item capacity between frames [03 §1][I1].
//
// It also carries this frame's carrier-to-children index, because the per-unit
// present of [03 R-RAST-01 §7] runs for the unit "and then each attached child
// that is not carried piece-less" and both unit passes need the same lists.
type worldBuckets struct {
	items        []worldDrawable
	bucket       []worldBucket
	orderedItems []worldDrawable

	// units is the frame's unit slice the child indices point into.
	units []frame.UnitView
	// childHead is indexed by carrier slot and holds the first child's index
	// plus one, so zero means "no children"; childNext is the same encoding
	// indexed by child. touched lists the carrier slots written this frame so
	// the reset costs the number of carriers rather than the slot space.
	childHead    []int32
	childNext    []int32
	childTouched []int32
}

func (b *worldBuckets) reset() {
	for i := range b.bucket {
		b.bucket[i].items = b.bucket[i].items[:0]
	}
	b.bucket = b.bucket[:0]
	b.items = b.items[:0]
	b.orderedItems = b.orderedItems[:0]
	for _, slot := range b.childTouched {
		b.childHead[slot] = 0
	}
	b.childTouched = b.childTouched[:0]
	b.childNext = b.childNext[:0]
	b.units = nil
}

// indexChildren builds the carrier-to-children lists for one committed frame.
// A child links to the front of its carrier's list, so walking the list yields
// the most recently attached child first — retail's cargo list is that LIFO
// order [04 R-UNIT-06 §3]. Units are enumerated in ascending slot, so the
// order this reproduces is descending slot, which is the attach order for the
// dominant case of one carrier and one product.
//
// A piece-less carry (a negative hang piece) is excluded: the per-unit present
// draws only children that follow a real piece [03 R-RAST-01 §7]
// [04 R-UNIT-06 §3].
func (b *worldBuckets) indexChildren(units []frame.UnitView) {
	b.units = units
	if len(units) == 0 {
		return
	}
	if cap(b.childNext) < len(units) {
		b.childNext = make([]int32, len(units))
	}
	b.childNext = b.childNext[:len(units)]
	for i := range b.childNext {
		b.childNext[i] = 0
	}
	maxSlot := 0
	for i := range units {
		if s := int(units[i].Slot); s > maxSlot {
			maxSlot = s
		}
	}
	if len(b.childHead) < maxSlot+1 {
		grown := make([]int32, maxSlot+1)
		copy(grown, b.childHead)
		b.childHead = grown
	}
	for i := range units {
		u := &units[i]
		if !isCarried(*u) || u.CarriedPiece < 0 {
			continue
		}
		carrier := int(u.Carrier)
		if carrier >= len(b.childHead) {
			continue
		}
		if b.childHead[carrier] == 0 {
			b.childTouched = append(b.childTouched, int32(carrier))
		}
		b.childNext[i] = b.childHead[carrier]
		b.childHead[carrier] = int32(i) + 1
	}
}

// firstChild returns the index of a carrier's first attached child, or -1.
func (b *worldBuckets) firstChild(carrier pool.Handle) int {
	slot := int(carrier)
	if slot <= 0 || slot >= len(b.childHead) {
		return -1
	}
	return int(b.childHead[slot]) - 1
}

// nextChild returns the index of the next child in a carrier's list, or -1.
func (b *worldBuckets) nextChild(child int) int {
	if child < 0 || child >= len(b.childNext) {
		return -1
	}
	return int(b.childNext[child]) - 1
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

// ordered returns this frame's drawables in ascending bucket row, stable
// within a row. It is idempotent: the two unit passes are two walks over one
// bucket build, so both call it and both see the same sequence
// [03 R-RAST-01 §7].
func (b *worldBuckets) ordered() []worldDrawable {
	b.orderedItems = b.orderedItems[:0]
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
func (c *Client) drawCommittedFrame(cur *frame.Frame, ok bool) {
	if c == nil || len(c.indexed) != c.width*c.height {
		return
	}
	// The committed tick drives every presentation animator that reads it
	// directly (nanoframe pulse, nanolathe particles) [03 §5.2][03 §5.5].
	if cur != nil {
		c.frameTick = cur.Tick
	}
	// A missing terrain source is an empty indexed surface. No synthetic art is
	// emitted when the frontend has no world attachment [I9]. The clear is the
	// first recorded command of the frame, so replaying the list zeroes the
	// surface before any draw and the whole frame executes once (WU-1.8) [C-G1].
	c.emitClear()
	// Terrain/static preparation, radar preparation, and viewport clipping are
	// unconditional. Radar and clip have no concrete frame input yet.
	c.drawTerrainPrep()
	// The Enhanced trail layer lies on the terrain under every strip
	// (DESIGN_GPU_RENDERER §15). Marks are placed from the committed tick, not
	// the blended view, so the layer never moves with the blend fraction.
	c.placeTrails(c.buffer.Current())
	c.drawTrails()
	// Strips 0 and 1 are unconditional but producerless; strip 2 is the first
	// published effect barrier [03 §1][03 R-STRIP-01 §2].
	//
	// Closed 2026-09-02 [03 R-FX-02 §5]. These barriers carried T23
	// platform-residual markers waiting on a manual retail observation. The closure is static and
	// exhaustive instead — every code reference to each of the thirteen producer
	// routines was enumerated with the strip literal read from each call site's
	// own argument pushes, and independently every instruction that touches the
	// strip-table root word — and it agrees with the earlier census by a
	// different method: strips 0, 1, 3 and 8 have NO producer anywhere in the
	// image and hold no object in any retail session. The composer's walk over
	// them is a no-op in retail, so these calls are kept as empty passes to hold
	// the barrier order of [03 §1]. What must never happen is attaching a
	// producer to any of the four.
	c.drawStripSlot(cur, 0)
	c.drawStripSlot(cur, 1)
	c.drawStripSlot(cur, 2)

	// The first feature traversal owns the never-seen admission. Features with
	// height >= 10 are deferred to pass A [03 R-RAST-01 §6].
	c.drawFeaturePass(cur, ok)
	// Strip 3 is the second of the four producerless barriers; see the note at
	// strips 0/1 [03 R-FX-02 §5].
	c.drawStripSlot(cur, 3)
	c.drawStripSlot(cur, 4)

	// Pass A: the grounded units of each window row, interleaved with that
	// row's deferred tall features [03 R-RAST-01 §7].
	c.drawWorldPass(cur, ok)
	c.drawStripSlot(cur, 5)

	// Strip 6 advances and paints nanolathe once, then consumes its committed
	// effect records at the same barrier [03 §1][03 R-STRIP-01 §2]. The
	// strip-6 objects themselves are the spray drawNanolathe paints, so the
	// publication leaves them out of the committed strip channel and this
	// barrier has no strip-object pass of its own.
	c.drawEffects(cur)
	c.drawProjectiles(cur)
	c.drawFixedEffects(cur)
	c.drawStripSlot(cur, 7)
	// Pass B: the units whose mode mirror is not 1 — airborne aircraft,
	// attached/parked cargo, save-installed — at the end of strip 7, after the
	// projectile pool and the fixed effect pool [03 R-RAST-01 §7][03 §1].
	c.drawWorldPassB(cur, ok)
	// Auxiliary unit traversal has no published auxiliary draw records yet;
	// leave this established slot empty rather than inventing a route [03 §1].
	// Strip slot 8 is unconditional and is the last of the four producerless
	// barriers; see the note at strips 0/1 [03 §1][03 R-FX-02 §5].
	c.drawStripSlot(cur, 8)

	// The key-controlled overlay has no concrete authored client route yet.
	// The unit labels follow it and precede strip 9: the health bar and the
	// control-group digit, each gated on the option byte and on the labelled
	// owner equalling the local player slot [03 §1][03 R-FX-01 §6].
	c.drawUnitLabels(cur, ok)
	c.drawStripSlot(cur, 9)
	c.drawFog(cur)
	c.drawSelectionStage()
	c.drawInterface(cur)
}

// drawFeaturePass is the first feature traversal: every short feature
// (authored height below 10) whose plot cell is inside the frame window. A
// tall feature is deferred here and drawn in pass A, interleaved with that
// row's grounded units [03 R-RAST-01 §6].
//
// The window is the only admission test either feature pass applies to a tree,
// rock or wreck. Retail's gate is "the definition does not carry
// nodrawundergray, or the plot cell's placer nibble equals the local player's
// slot, or the two-corner LOS predicate passes", and it reads neither the
// explored mask nor the fog grids: fog and LOS are not applied at raster time
// — the fog overlay is composed after strip 9 and darkens features, units,
// shadows and projectiles alike [03 R-RAST-01 §6][03 §5.1.5]. Gating the draw
// on the anchor cell's fog tile instead was the PT3-10 defect: it culled the
// whole sprite on a per-tile edge that does not line up with the 32-pixel fog
// blocks, so a tree vanished while the ground it stands on was lit and
// reappeared whole the moment its own tile flipped.
func (c *Client) drawFeaturePass(cur *frame.Frame, ok bool) {
	if !ok || cur == nil || c.cam == nil {
		return
	}
	win := c.worldWindow()
	for i := range cur.Features {
		f := &cur.Features[i]
		if f.Height >= 10 {
			continue
		}
		if !win.admitsCell(f.CX, f.CZ) {
			continue
		}
		if !featureVisibleForFrame(cur, *f) {
			continue
		}
		c.drawFeature(f)
	}
}

// featureVisibleForFrame applies the feature draw gate after window admission.
// Definitions without nodrawundergray draw unconditionally. Flagged
// definitions draw only for a local-player placer selector or when either of
// the two footprint corners is visible in the committed visibility mask; the
// fog/explored channels are deliberately not consulted [03 R-RAST-01 §6]
// [03 §5.1.5].
func featureVisibleForFrame(cur *frame.Frame, f frame.FeatureView) bool {
	if cur == nil {
		return false
	}
	if !f.NoDrawUnderGray {
		return true
	}
	viewer := cur.Selection.LocalPlayer
	if viewer >= 10 {
		return false
	}
	if f.OwnerKnown && f.Owner < 10 && f.Owner == viewer {
		return true
	}
	// The feature predicate samples the anchor cell origin, then one opposite
	// corner displaced by the authored footprint offsets [03 §3.2]. CellToWorld
	// preserves the established cell-origin conversion, including its signed
	// 16.16 representation [03 §2.1].
	minX := world.CellToWorld(f.CX)
	minZ := world.CellToWorld(f.CZ)
	maxX := world.CellToWorld(f.CX + int32(f.FootX))
	maxZ := world.CellToWorld(f.CZ + int32(f.FootZ))
	return SnapshotPointVisible(cur.Visibility, minX, f.Y, minZ, viewer) ||
		SnapshotPointVisible(cur.Visibility, maxX, f.Y, maxZ, viewer)
}

// drawWorldPass builds this frame's world-Z row buckets and then runs pass A:
// for each window row in order, the grounded units of that row followed by the
// row's deferred tall features [03 R-RAST-01 §7].
//
// The bucket key is the unit's world Z in plot rows relative to the camera,
// never its projected screen position: the projection subtracts a half-height
// shear, so keying on it made an ascending aircraft's row fall and sorted it
// under the structure it had just left. The screen position each drawable is
// blitted at is unchanged — only the sort key is [03 R-RAST-01 §7].
//
// Pass B (drawWorldPassB) walks the same buckets after the projectile and
// effect strips; both calls come from drawCommittedFrame, in that order.
func (c *Client) drawWorldPass(cur *frame.Frame, ok bool) {
	b := &c.worldBuckets
	b.reset()
	if !ok || cur == nil || c.cam == nil {
		return
	}
	win := c.worldWindow()
	camZ := c.cam.Z
	viewer := cur.Selection.LocalPlayer
	// The carrier lists are built over every published unit, not only the
	// bucketed ones: a child rides its carrier's admission and is presented
	// with it even when its own row falls outside the window
	// [03 R-RAST-01 §7].
	b.indexChildren(cur.Units)
	// The bucket build walks the units the viewer may see, in slot order, and
	// appends each to its row. Appends are stable, so in-row draw order is
	// ascending unit slot in both passes [03 R-RAST-01 §7].
	for i := range cur.Units {
		u := &cur.Units[i]
		if u.Owner != viewer {
			if fogUnexploredUnit(cur.Fog, *u) || !unitVisibleForFrame(cur, *u, viewer) {
				continue
			}
		}
		row := unitBucketRow(u.Z, camZ)
		if row < 0 || row >= win.bucketRows {
			continue
		}
		sx, sy := c.cam.WorldToScreen(u.X, u.Y, u.Z)
		sx -= camera.OriginX
		sy -= camera.OriginY
		b.add(worldDrawable{row: row, unit: u, screenX: sx, screenY: sy})
	}
	// The deferred tall features take the same gate as pass 1: the window, and
	// nothing that reads fog or LOS [03 R-RAST-01 §6].
	for i := range cur.Features {
		f := &cur.Features[i]
		if f.Height < 10 {
			continue
		}
		if !win.admitsCell(f.CX, f.CZ) {
			continue
		}
		if !featureVisibleForFrame(cur, *f) {
			continue
		}
		sx, sy := c.featureScreenPos(*f)
		b.add(worldDrawable{row: featureBucketRow(f.CZ, camZ), feature: f, screenX: sx, screenY: sy})
	}
	for _, d := range b.ordered() {
		if !win.admitsPassARow(d.row) {
			continue
		}
		if d.unit != nil {
			// Pass A is the grounded movers only. Structures and airborne
			// units are pass B's [03 R-RAST-01 §7][04 R-MOV-01 §8].
			if d.unit.MoverMode == moverModeGrounded {
				c.presentUnit(d)
			}
			continue
		}
		if d.feature != nil {
			c.drawFeature(d.feature)
		}
	}
}

// drawWorldPassB is the second unit pass: every bucketed unit whose committed
// mode mirror is not 1 — airborne aircraft (2), units attached to a carrier or
// parked on a pad (0), and the save-installed mode (3). It runs after the
// projectile and effect strips, so those units paint over every feature, every
// mode-1 unit and every projectile whatever their Z row
// [03 R-RAST-01 §7][03 §1].
//
// Structures are NOT in this pass. That reading was retracted on 2026-08-30:
// the pass predicate is the unit record's own flags-word mode mirror, never a
// mover, and a structure's mirror is 1 for its whole life — so a structure is
// painted in Z-row order among the grounded units of pass A, and does not
// change pass when it completes. The sentence that used to close this comment,
// "That is the retail order and not a defect to correct", rested on the
// inverted partition and does not survive it; the nanolathe spray of strip 6,
// drawn between the two passes, therefore paints over the unit being built.
// See the 2026-08-30 correction under [03 R-RAST-01 §7].
//
// Unlike pass A this walks the whole bucket array, not the clipped window: a
// unit's only admission test was the bucket build's row bound.
func (c *Client) drawWorldPassB(cur *frame.Frame, ok bool) {
	if !ok || cur == nil || c.cam == nil {
		return
	}
	for _, d := range c.worldBuckets.ordered() {
		if d.unit == nil || d.unit.MoverMode == moverModeGrounded {
			continue
		}
		c.presentUnit(d)
	}
}

// moverModeGrounded is the committed mover mode of a unit on the ground or on
// the water surface, and the selector that splits the two unit passes
// [04 R-MOV-01 §8][03 R-RAST-01 §7].
const moverModeGrounded uint8 = 1

// isCarried reports whether this unit record holds a carrier link. It is the
// first test the per-unit present makes, and the whole of it: a unit with a
// carrier is presented by that carrier and never on its own
// [03 R-RAST-01 §7-A].
//
// The hang piece is deliberately not part of the test. The present's check is
// the carrier link alone, while the carrier's children walk is the one that
// skips a piece-less carry, so a unit carried on the no-piece sentinel is drawn
// by neither and appears nowhere — retail's behavior, not an omission here
// [03 R-RAST-01 §7][04 R-UNIT-06 §3][04 R-FAC-02 §1].
//
// A record naming itself as its carrier is not a link, the same self-reference
// indexChildren rejects.
func isCarried(v frame.UnitView) bool {
	return v.Carrier != 0 && v.Carrier != v.Slot
}

// presentUnit runs the per-unit steps both passes share, in order: the
// selected-unit footprint quad when the unit's selected bit is set, and then
// the model present, which carries the unit's attached children with it
// [03 R-RAST-01 §7].
//
// **A unit that has a carrier is not presented here.** The per-unit present is
// a no-op for a carried unit: it tests the unit record's carrier link first and
// returns before any of its own work, so a carried unit reaches the screen only
// through the children step of its CARRIER's present, inside the carrier's
// staging image and under the key test [03 R-RAST-01 §7-A][R-REN-03A §4]. The
// bucket build applies no carrier filter, so cargo is still bucketed and still
// walked by pass B; what the pass finds is a present that does nothing.
//
// This is the play-test defect the unit fixes. Cargo hangs at the carrier's
// attach piece, so it lands in the same 16-pixel plot row as its carrier, where
// in-row order is ascending unit slot. An Atlas built before the unit it lifts
// therefore had its cargo painted a second time, straight to the framebuffer,
// after the carrier's staging blit — which is exactly the per-pixel occlusion
// [R-REN-03A §4] exists to produce, undone one blit later. The reverse slot
// order hid the bug rather than fixing it.
//
// The selected-unit footprint quad is NOT inside the present and is still drawn
// for a carried unit: both passes draw the quad and then call the present, so
// the carrier check gates only the model [03 R-RAST-01 §7].
func (c *Client) presentUnit(d worldDrawable) {
	u := *d.unit
	// The selected-unit footprint quad occupies this unit's own depth slot and
	// precedes its model present, so the model draws over it; both passes run
	// before the fog composite, so the quad is never fog-clipped
	// [03 R-WATER-01 §1].
	if u.Flags&hud.SelectionFlag != 0 {
		c.drawSelectionQuad(u)
	}
	if isCarried(u) {
		return
	}
	if u.Model == "" {
		// A carrier with no model of its own still presents its children.
		for _, child := range c.attachedChildren(u.Slot) {
			c.drawChildModel(child)
		}
		return
	}
	if c.composeCarrier(u, d.screenX, d.screenY, c.attachedChildren(u.Slot)) {
		c.selectionChrome = append(c.selectionChrome, selectionChrome{view: u, screenX: d.screenX, screenY: d.screenY})
	}
}

// attachedChildren collects the children step of the per-unit present: after a
// unit's own body and live pieces, each attached child that follows a real
// carrier piece is presented too [03 R-RAST-01 §7][04 R-UNIT-06 §3].
//
// This is what puts a factory's nanoframe on its build plate. A product hangs
// from the QueryBuildInfo piece [04 R-FAC-02 §2] and keeps the grounded mode
// mirror [04 R-FAC-02 §1], so it is bucketed in pass A on its own world-Z row
// — and the plate sits a few world units in front of or behind the factory's
// own origin, which is often a different 16-pixel row. On a stock Kbot Lab it
// is one row earlier, so the product was painted first and the factory body
// then painted straight over it: the play-test report of a nanoframe sitting
// under the build plate. Its own bucket entry is left alone; this is the
// second, later present retail also performs.
//
// The children are returned rather than drawn because the carrier composites
// them into its own staging image when it has a key plane [R-REN-03A §4]; see
// composeCarrier. A child with no model is dropped here, as it was when this
// function drew them itself.
func (c *Client) attachedChildren(carrier pool.Handle) []frame.UnitView {
	if c == nil {
		return nil
	}
	b := &c.worldBuckets
	var out []frame.UnitView
	for i := b.firstChild(carrier); i >= 0; i = b.nextChild(i) {
		if i >= len(b.units) {
			break
		}
		child := b.units[i]
		if child.Model == "" {
			continue
		}
		out = append(out, child)
	}
	return out
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
	// Both passes draw the frame's 2x variant in the detail view; at the native
	// scale viewFrame is the identity. The variant carries doubled authored
	// offsets, so the subtraction below needs no scale term of its own
	// (DESIGN_GPU_RENDERER §14.2, §14.3).
	var shadowFrame *formats.GAFFrame
	if c.featureShadows && (!f.RuntimeLive || f.ShadowEnabled) && (!f.RuntimeLive || f.EventSeqName != "") {
		shadowFrame = c.viewFrame(c.featureFrameFor(*f, true))
	}
	var normalFrame *formats.GAFFrame
	if !f.RuntimeLive || f.EventSeqName != "" {
		normalFrame = c.viewFrame(c.featureFrameFor(*f, false))
	}
	// Record the shadow then the normal frame through the committed-frame draw
	// list, in the same order the direct blits ran. drawFeature subtracts the
	// frame's XOffset/YOffset here, so the recorded X/Y are the final top-left
	// (not the anchored path); the sink selects the keyed or ALP-tinted primitive
	// without changing that geometry [03 R-RAST-01 §6][03 §5.3.1].
	if shadowFrame != nil {
		// Runtime sprite cursors use the opaque primitive. The authored
		// shadtrans selector applies to the static definition cursor only
		// [03 R-RAST-01 §6][03 §5.3.1]. RuntimeLive is a record bit, not a
		// proxy based on EventSeqName.
		shadowTrans := f.ShadTrans && !f.RuntimeLive
		c.emitSprite(drawlist.Sprite{
			Frame: shadowFrame,
			X:     sx - int32(shadowFrame.XOffset),
			Y:     sy - int32(shadowFrame.YOffset),
			Kind:  drawlist.BlitFeatureShadow,
			Trans: shadowTrans,
		})
	}
	if normalFrame != nil {
		// A live event cursor is opaque for both its shadow and body. The authored
		// animtrans selector applies only to the static definition cursor
		// [03 R-RAST-01 §6].
		normalTrans := f.AnimTrans && !f.RuntimeLive
		c.emitSprite(drawlist.Sprite{
			Frame: normalFrame,
			X:     sx - int32(normalFrame.XOffset),
			Y:     sy - int32(normalFrame.YOffset),
			Kind:  drawlist.BlitFeatureNormal,
			Trans: normalTrans,
		})
	}
}
