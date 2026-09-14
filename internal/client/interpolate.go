package client

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Enhanced interpolation. The simulation stays at its 30 Hz authoritative
// cadence while the modern window presents at the display's refresh rate, so a
// presented frame that is not a tick boundary shows a pose blended from the two
// most recent committed ticks (docs/DESIGN_GPU_RENDERER.md §13.5). Everything
// here is presentation: the committed frames are read, never written, no
// simulation RNG is drawn, and the blend never reaches authoritative state
// [I6]. Original (classic) and `--shot` keep committed-tick sampling — they
// never call SetInterpolation.
//
// The arithmetic is integer. The fraction is carried as a 16.16 value so a
// 16.16 world coordinate blends as `prev + ((cur-prev) * f16) >> 16` with
// truncation toward zero, and a 65536-per-circle angle blends along the
// shortest arc [I2][I3].

// fractionOne is the 16.16 unit. tickFraction16 is clamped to
// 0..fractionOne-1, which is the [0, 1) of §13.5 expressed in that domain: the
// clock's carry is a float32 store that can round a remainder just below one up
// to exactly one [01 §4.2], and a fraction of one would show the next tick's
// pose a tick early.
const fractionOne = int32(numeric.FixedOne)

// clampFraction16 narrows a producer's fraction into the 16.16 form the blend
// uses. The comparison order rejects NaN as well as a negative value, and the
// upper clamp keeps the domain half-open.
func clampFraction16(f float32) int32 {
	v := int32(0)
	if f > 0 {
		v = int32(f * float32(fractionOne))
	}
	if v >= fractionOne {
		v = fractionOne - 1
	} else if v < 0 {
		v = 0
	}
	return v
}

// ClampTickFraction narrows a fraction into the half-open [0, 1) the blend
// uses, in the client's own 16.16 domain. A producer calls it so the value it
// keeps and the value the blend uses are the same number; §13.5 puts the clamp
// in the formula, and the doubled speeds are where the unclamped sum passes
// one.
func ClampTickFraction(f float32) float32 {
	return float32(clampFraction16(f)) / float32(fractionOne)
}

// ClampTickFraction16 is the same narrowing in the 16.16 domain the pipeline's
// predicted fractions are carried in, so a producer that knows the next frame's
// fraction exactly — the benchmark's four-draw group — hands over the same
// number SetTickFraction would store (§13.10).
func ClampTickFraction16(f float32) int32 { return clampFraction16(f) }

// SetTickFraction sets the fraction explicitly and makes that value win over
// the Options.TickFraction producer for the rest of the run. The 120 TPS
// benchmark is its caller: it drives the four fractions itself (§13.5), and a
// benchmark client built by the ordinary battle composition also carries the
// live producer, which must not overwrite them.
func (c *Client) SetTickFraction(f float32) {
	if c == nil {
		return
	}
	c.tickFraction16 = clampFraction16(f)
	c.tickFractionSet = true
}

// TickFraction reports the clamped fraction the blend currently uses, in the
// same [0, 1) domain the producer supplies, so a producer can be checked
// against §13.5 without reaching into the client.
func (c *Client) TickFraction() float32 {
	if c == nil {
		return 0
	}
	return float32(c.tickFraction16) / float32(fractionOne)
}

// sampleTickFraction reads the fraction for the frame about to be recorded.
// §13.5 reads it at Draw time, not at the 30 Hz step, because the whole point
// is a position *between* two steps: the producer is the battle's own
// millisecond source, un-floored, and the client only clamps what it returns.
func (c *Client) sampleTickFraction() int64 {
	return int64(c.ResolveTickFraction())
}

// ResolveTickFraction settles this frame's blend fraction from the producer and
// returns it in the client's 16.16 domain. The record/submit pipeline calls it
// before it builds a validity digest, so the number the digest compares and the
// number the recording pass blends with are the same one — a producer read
// twice would be two different wall-clock samples (§13.10). The recorder itself
// calls it too, so a client that never uses the pipeline is unchanged.
func (c *Client) ResolveTickFraction() int32 {
	if c == nil {
		return 0
	}
	if !c.tickFractionSet && c.opts.TickFraction != nil {
		c.tickFraction16 = clampFraction16(c.opts.TickFraction())
	}
	return c.tickFraction16
}

// SetInterpolation selects the Enhanced blended view. Only the modern window
// path and the 120 TPS benchmark enable it (docs/DESIGN_GPU_RENDERER.md §13.5).
func (c *Client) SetInterpolation(enabled bool) {
	if c == nil {
		return
	}
	c.interpolation = enabled
}

// presentationFrame is the frame the recorder reads. It is the committed frame
// itself unless Enhanced interpolation is on and a previous committed tick
// exists; a nil previous is a snap (docs/DESIGN_GPU_RENDERER.md §13.5).
func (c *Client) presentationFrame() *frame.Frame {
	if c == nil {
		return nil
	}
	cur := c.buffer.Current()
	if cur == nil || !c.interpolation {
		return cur
	}
	prev := c.buffer.Previous()
	if prev == nil {
		return cur
	}
	fraction := c.sampleTickFraction()
	key := pausedBlendInputs{prev, cur, prev.Tick, cur.Tick, fraction}
	if c.presentationPaused && c.interp.pausedValid && c.interp.pausedInputs == key {
		return &c.interp.view
	}
	blended := c.interp.blend(prev, cur, fraction)
	c.interp.pausedInputs, c.interp.pausedValid = key, c.presentationPaused
	// The effect strip buckets are keyed by frame pointer and committed tick,
	// and they hold copies of the views. The blended frame keeps both across the
	// several presented frames of one tick, so the classification has to be
	// redone for every blend or every effect would hold its first blended
	// position for the whole tick.
	c.stripsValid = false
	return blended
}

// sampleCameraOrigin records the camera origin at the 30 Hz step. The camera
// moves in that step — the scroll pass and the follow glide both write it
// [07 §10][07 R-CAM-01 §12] — so Enhanced blends it with the same fraction as
// the world it frames, or the world would slide under a camera that jumps
// (§13.5). Presentation state only; nothing reads it back into the session
// [I6].
func (c *Client) sampleCameraOrigin() {
	if c == nil || c.cam == nil {
		c.camSamples = 0
		return
	}
	c.camPrevView, c.camCurView = c.camCurView, c.cam.PresentationView()
	if c.camSamples < 2 {
		c.camSamples++
	}
}

// SetCameraFraction records how far the window is through the current Update,
// which is the camera's own fraction and not the tick fraction (§13.5).
//
// The camera advances on the window's Update grid; the simulation's scaled
// units are not phase-aligned with it [01 §4.1]. Blending the origin by the
// tick fraction would therefore snap it backwards whenever a tick fired
// mid-update — 0.9 of the way along, then 0.05 of the way along the same
// unchanged pair of samples. The window adapter timestamps each Update and
// supplies (now − lastUpdate) × 30 here; the value is platform time and reaches
// neither the client's clock nor the simulation [I6].
func (c *Client) SetCameraFraction(f float32) {
	if c == nil {
		return
	}
	c.cameraFraction16 = clampFraction16(f)
	c.cameraFractionSet = true
}

// blendedCameraView interpolates the affine projection, not the origin and
// scale independently: lerp(origin*zoom)/lerp(zoom) keeps an anchored world
// point fixed for every displayed fraction (DESIGN_GPU_RENDERER §16.5).
func (c *Client) blendedCameraView() camera.PresentationView {
	prev, cur := c.camPrevView, c.camCurView
	f := float64(c.cameraFraction16) / float64(fractionOne)
	factor := prev.Factor + (cur.Factor-prev.Factor)*f
	axis := func(a, b float64, viewport int32) float64 {
		if prev.Factor == cur.Factor && viewport > 0 && math.Abs(b-a) > float64(viewport) {
			return b
		}
		return (a*prev.Factor + (b*cur.Factor-a*prev.Factor)*f) / factor
	}
	w, h := c.cam.EffectiveView()
	return camera.PresentationView{X: axis(prev.X, cur.X, w), Z: axis(prev.Z, cur.Z, h), Factor: factor}
}

// presentationCameraView is shared by recording, paused reuse and strategic
// picking. During a record it returns the exact view installed for that record.
func (c *Client) presentationCameraView() camera.PresentationView {
	if c.camBlending {
		return c.camDrawView
	}
	if c.hasCameraBlend() {
		return c.blendedCameraView()
	}
	if c.cam == nil {
		return camera.PresentationView{Factor: 1}
	}
	return camera.PresentationView{X: float64(c.cam.X), Z: float64(c.cam.Z), Factor: c.cam.EffectiveZoom().Float()}
}

// beginCameraBlend installs a temporary recording camera. Integer projection
// sites use its whole origin and step; the world boundary carries the remaining
// subpixel transform to the GPU. The complete camera is restored afterwards.
func (c *Client) beginCameraBlend() bool {
	if c == nil || c.cam == nil || c.camSamples < 2 || !c.cameraFractionSet {
		return false
	}
	c.camSave = *c.cam
	c.camDrawView = c.blendedCameraView()
	c.camBlending = true
	// Floor so the residual only shifts the recording toward the leading
	// edge; integer clipping cannot expose an unrecorded strip there.
	c.cam.X, c.cam.Z = int32(math.Floor(c.camDrawView.X)), int32(math.Floor(c.camDrawView.Z))
	c.cam.Zoom = camera.Zoom(math.Round(c.camDrawView.Factor * float64(camera.ZoomUnit)))
	// The record step follows the precise factor even just above native.
	c.cam.Scale = camera.ViewScaleNative
	if c.camDrawView.Factor > 1 {
		c.cam.Scale = camera.ViewScaleDetail
	}
	return true
}

func (c *Client) endCameraBlend(applied bool) {
	if !applied || c == nil || c.cam == nil {
		return
	}
	*c.cam = c.camSave
	c.camBlending = false
}

// pausedBlendInputs identifies the frozen committed pose blend.
type pausedBlendInputs struct {
	previous, current         *frame.Frame
	previousTick, currentTick uint32
	fraction                  int64
}

// hasCameraBlend keeps the paused raster key on the recorder's exact admission
// rule. All previous-frame reads stay in this file [I6].
func (c *Client) hasCameraBlend() bool {
	return c.interpolation && c.buffer != nil && c.buffer.Previous() != nil &&
		c.cam != nil && c.camSamples >= 2 && c.cameraFractionSet
}

// interpolator owns the blended view and retained buffers. Unchanged paused
// views reuse their blend; running views rebuild it on every presentation.
type interpolator struct {
	pausedInputs pausedBlendInputs
	pausedValid  bool
	view         frame.Frame
	units        []frame.UnitView
	pieces       [][]frame.PieceView
	projectiles  []frame.ProjectileView
	effects      []frame.EffectView
	// unitAt is the previous tick's pool-slot lookup. projAt and effectAt are
	// presentation-identity lookups, used only on the frame path and never
	// ranged [I1].
	unitAt   []int32
	projAt   map[uint64]int32
	effectAt map[effectKey]int32
}

// effectKey is the effect identity of §13.5: the presentation identity when it
// is nonzero, and otherwise the record identity, its admission sequence and its
// start tick together.
type effectKey struct {
	presentation uint64
	sequence     uint64
	id           uint32
	start        uint32
}

func keyForEffect(e frame.EffectView) effectKey {
	if e.PresentationID != 0 {
		return effectKey{presentation: e.PresentationID}
	}
	return effectKey{sequence: e.EventSeq, id: e.ID, start: e.StartTick}
}

// snapDistance is the horizontal displacement above which a unit is treated as
// a different subject rather than one that moved: 64 world units in one tick,
// well above any authored movement rate. It is a presentation constant of
// §13.5, not a retail datum.
const snapDistance = numeric.Fixed(64) * numeric.FixedOne

// blend builds the view of §13.5: a copy of the current frame whose units,
// projectiles and effects are blended against the previous committed tick.
// Every other field — fog, visibility, selection, orders, events, the HUD
// readouts and Tick — is the current tick's.
func (in *interpolator) blend(prev, cur *frame.Frame, f16 int64) *frame.Frame {
	in.view = *cur
	in.view.Units = in.blendUnits(prev, cur, f16)
	in.view.Projectiles = in.blendProjectiles(prev, cur, f16)
	in.view.Effects = in.blendEffects(prev, cur, f16)
	return &in.view
}

func (in *interpolator) blendUnits(prev, cur *frame.Frame, f16 int64) []frame.UnitView {
	in.unitAt = resetIndex(in.unitAt, maxUnitSlot(prev.Units))
	for i := range prev.Units {
		if slot := int(prev.Units[i].Slot); slot < len(in.unitAt) {
			in.unitAt[slot] = int32(i) + 1
		}
	}
	in.units = growSlice(in.units, len(cur.Units))
	for i := range cur.Units {
		u := cur.Units[i]
		p := in.previousUnit(prev, u)
		if p != nil {
			u.X = lerpFixed(p.X, u.X, f16)
			u.Y = lerpFixed(p.Y, u.Y, f16)
			u.Z = lerpFixed(p.Z, u.Z, f16)
			u.Heading = lerpAngle(p.Heading, u.Heading, f16)
			u.Pitch = lerpAngle(p.Pitch, u.Pitch, f16)
			u.Bank = lerpAngle(p.Bank, u.Bank, f16)
			u.Pieces = in.blendPieces(i, p.Pieces, u.Pieces, f16)
		}
		in.units[i] = u
	}
	return in.units
}

// previousUnit is the unit continuity rule of §13.5. InstanceID must agree
// across the two ticks, so a missing ID on only one side snaps. Both zero is
// the fixture exception. Every matching identity still requires the same slot,
// definition, owner, carrier, mode mirror, piece count, and a horizontal step
// within the snap bound. Pool slots carry no generation [01 §6.1].
func (in *interpolator) previousUnit(prev *frame.Frame, u frame.UnitView) *frame.UnitView {
	slot := int(u.Slot)
	if slot >= len(in.unitAt) || in.unitAt[slot] == 0 {
		return nil
	}
	p := &prev.Units[in.unitAt[slot]-1]
	if p.InstanceID != u.InstanceID {
		return nil
	}
	if p.DefID != u.DefID || p.Owner != u.Owner || p.Carrier != u.Carrier || p.MoverMode != u.MoverMode {
		return nil
	}
	if len(p.Pieces) != len(u.Pieces) {
		return nil
	}
	dx, dz := u.X-p.X, u.Z-p.Z
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	// The per-axis test comes first so the squared distance below cannot
	// overflow on a map-crossing teleport.
	if dx > snapDistance || dz > snapDistance {
		return nil
	}
	if int64(dx)*int64(dx)+int64(dz)*int64(dz) > int64(snapDistance)*int64(snapDistance) {
		return nil
	}
	return p
}

// blendPieces blends one unit's COB piece transforms into this unit index's
// retained buffer. A piece hidden in either tick is drawn as the current tick
// says (§13.5), so its transform is copied rather than blended; every other
// piece field is the current tick's either way.
func (in *interpolator) blendPieces(unit int, prev, cur []frame.PieceView, f16 int64) []frame.PieceView {
	for len(in.pieces) <= unit {
		in.pieces = append(in.pieces, nil)
	}
	buf := growSlice(in.pieces[unit], len(cur))
	in.pieces[unit] = buf
	for j := range cur {
		p := cur[j]
		q := prev[j]
		if !p.Hidden && !q.Hidden {
			p.Tx = lerpFixed(q.Tx, p.Tx, f16)
			p.Ty = lerpFixed(q.Ty, p.Ty, f16)
			p.Tz = lerpFixed(q.Tz, p.Tz, f16)
			p.RotX = lerpAngle(q.RotX, p.RotX, f16)
			p.RotY = lerpAngle(q.RotY, p.RotY, f16)
			p.RotZ = lerpAngle(q.RotZ, p.RotZ, f16)
		}
		buf[j] = p
	}
	return buf
}

func (in *interpolator) blendProjectiles(prev, cur *frame.Frame, f16 int64) []frame.ProjectileView {
	if in.projAt == nil {
		in.projAt = make(map[uint64]int32, len(prev.Projectiles))
	} else {
		clear(in.projAt)
	}
	for i := range prev.Projectiles {
		if id := prev.Projectiles[i].PresentationID; id != 0 {
			in.projAt[id] = int32(i) + 1
		}
	}
	in.projectiles = growSlice(in.projectiles, len(cur.Projectiles))
	for i := range cur.Projectiles {
		v := cur.Projectiles[i]
		// A nonzero admission identity stays with a projectile when the packed
		// pool compacts. A zero fixture identity never borrows history: a handle
		// is a packed-array position, not a subject identity [06 §5.1][§5.2].
		if prior := in.projAt[v.PresentationID]; v.PresentationID != 0 && prior != 0 {
			p := &prev.Projectiles[prior-1]
			if p.WeaponID == v.WeaponID && p.Shooter == v.Shooter && p.CreationTick == v.CreationTick {
				v.X = lerpFixed(p.X, v.X, f16)
				v.Y = lerpFixed(p.Y, v.Y, f16)
				v.Z = lerpFixed(p.Z, v.Z, f16)
				v.StartX = lerpFixed(p.StartX, v.StartX, f16)
				v.StartY = lerpFixed(p.StartY, v.StartY, f16)
				v.StartZ = lerpFixed(p.StartZ, v.StartZ, f16)
				v.TailX = lerpFixed(p.TailX, v.TailX, f16)
				v.TailY = lerpFixed(p.TailY, v.TailY, f16)
				v.TailZ = lerpFixed(p.TailZ, v.TailZ, f16)
				v.Yaw = lerpAngle(p.Yaw, v.Yaw, f16)
				v.Pitch = lerpAngle(p.Pitch, v.Pitch, f16)
				v.Roll = lerpAngle(p.Roll, v.Roll, f16)
				v.PropellerRoll = lerpAngle(p.PropellerRoll, v.PropellerRoll, f16)
				v.MeteorPitch = lerpAngle(p.MeteorPitch, v.MeteorPitch, f16)
			}
		}
		in.projectiles[i] = v
	}
	return in.projectiles
}

func (in *interpolator) blendEffects(prev, cur *frame.Frame, f16 int64) []frame.EffectView {
	if in.effectAt == nil {
		in.effectAt = make(map[effectKey]int32, len(prev.Effects))
	}
	clear(in.effectAt)
	for i := range prev.Effects {
		in.effectAt[keyForEffect(prev.Effects[i])] = int32(i) + 1
	}
	in.effects = growSlice(in.effects, len(cur.Effects))
	for i := range cur.Effects {
		e := cur.Effects[i]
		// Only the position blends: sprite and animation cursors, the flash
		// tables and the strip assignment are the current tick's (§13.5).
		if at := in.effectAt[keyForEffect(e)]; at != 0 {
			p := &prev.Effects[at-1]
			e.X = lerpFixed(p.X, e.X, f16)
			e.Y = lerpFixed(p.Y, e.Y, f16)
			e.Z = lerpFixed(p.Z, e.Z, f16)
		}
		in.effects[i] = e
	}
	return in.effects
}

// lerpFixed blends two 16.16 world values: `prev + ((cur-prev) * f16) >> 16`
// with truncation toward zero, which is what a `__ftol`-shaped narrowing does
// [I3]. Integer division in Go truncates toward zero, so it is the shift for a
// positive delta and the truncating form for a negative one.
func lerpFixed(prev, cur numeric.Fixed, f16 int64) numeric.Fixed {
	return prev + numeric.Fixed((int64(cur-prev)*f16)/int64(fractionOne))
}

// lerpAngle blends two 65536-per-circle angles along the shortest arc: the
// difference is read as a signed 16-bit value, scaled by the fraction and added
// back, so a heading crossing zero sweeps the short way (§13.5) [I2].
func lerpAngle(prev, cur uint16, f16 int64) uint16 {
	d := int64(int16(cur - prev))
	return prev + uint16((d*f16)/int64(fractionOne))
}

func maxUnitSlot(units []frame.UnitView) int {
	n := 0
	for i := range units {
		if s := int(units[i].Slot); s >= n {
			n = s + 1
		}
	}
	return n
}

// resetIndex returns a zeroed lookup table of n entries, reusing the retained
// backing array whenever it is large enough.
func resetIndex(dst []int32, n int) []int32 {
	if cap(dst) < n {
		return make([]int32, n)
	}
	dst = dst[:n]
	clear(dst)
	return dst
}

// growSlice returns a slice of exactly n elements over the retained backing
// array, growing it only when the frame is larger than any seen before.
func growSlice[T any](dst []T, n int) []T {
	if cap(dst) < n {
		return make([]T, n)
	}
	return dst[:n]
}
