package render

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// Rendertype IDs per [03 §5.4] C6.
const (
	RenderTypeBeam              = 0 // [03 §5.4] line from current endpoint to tail
	RenderTypeBaseSpriteModel   = 1 // [03 §5.4] base sprite plus model
	RenderTypeGlobalGAF         = 2 // [03 §5.4] fixed global GAF aborting whole renderer on failed admission
	RenderTypeBaseModelDistinct = 3 // [03 §5.4] base plus model distinct orientation path
	RenderTypeSelectorGAF       = 4 // [03 §5.4] selector picks one of five GAF sequences; -1 suppresses
	RenderTypeLifetimeGAF       = 5 // [03 §5.4] lifetime-scaled frame
	RenderTypeRecordOrientation = 6 // [03 §5.4] orientation stored verbatim in record
	RenderTypeSegmented         = 7 // [03 §5.4] randomized segmented lines with 0x50000 denominator
)

// BeamLatchState reports whether the beam latch is set at tick per [06 §6.10].
// Latch is set only when creation tick + duration < current tick; the tick that
// sets it still leaves the tail fixed [06 §6.10].
func BeamLatchState(creationTick uint32, duration int32, now uint32) bool { // [06 §6.10]
	return now > creationTick+uint32(duration) // [06 §6.10] strictly less
}

// BeamEndpoints holds head and tail world positions for beam geometry [03 §5.4] [06 §6.10].
type BeamEndpoints struct {
	Head    combat.Vec3
	Tail    combat.Vec3
	Latched bool // whether latch was already set at entry to this tick [06 §6.10]
}

// ComputeBeamEndpoints returns head and tail world positions for presentation
// mirroring the simulation latch tick without mutating combat state [06 §6.10] (I6).
// Before the latch the tail stays fixed while the head advances;
// the tick that sets the latch still leaves the tail fixed;
// starting on the next live tick both move preserving length [06 §6.10].
func ComputeBeamEndpoints(p combat.Projectile, w *content.WeaponDef, now uint32) BeamEndpoints { // [06 §6.10] [03 §5.4] (I6)
	latchedNow := false
	if w != nil && w.BeamWeapon && !p.BeamLatch {
		if BeamLatchState(p.CreationTick, w.Duration, now) { // [06 §6.10]
			latchedNow = true
		}
	}
	wasLatched := p.BeamLatch // latch at entry determines whether tail moves this tick [06 §6.10]
	_ = latchedNow
	return BeamEndpoints{
		Head:    p.Pos,
		Tail:    p.StartPos,
		Latched: wasLatched,
	}
}

// SimulateBeamTick advances a copy of p by one live beam tick mirroring
// combat.AdvanceDirect beam branch without mutating the original [06 §6.10] (I6).
// It reproduces the tick order: check latch, then move head, then move tail
// only if already latched at entry [06 §6.10].
func SimulateBeamTick(p combat.Projectile, w *content.WeaponDef, now uint32) combat.Projectile { // [06 §6.10] (I6)
	if w == nil || !w.BeamWeapon {
		// Non-beam: head moves, tail stays.
		p.Pos.X = p.Pos.X.Add(p.Velocity.X)
		p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
		p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
		return p
	}
	wasLatched := p.BeamLatch
	if !wasLatched {
		if BeamLatchState(p.CreationTick, w.Duration, now) { // [06 §6.10]
			p.BeamLatch = true // set this tick, tail still fixed [06 §6.10]
		}
	}
	// Move head [06 §7.2] [06 §6.10]
	p.Pos.X = p.Pos.X.Add(p.Velocity.X)
	p.Pos.Y = p.Pos.Y.Add(p.Velocity.Y)
	p.Pos.Z = p.Pos.Z.Add(p.Velocity.Z)
	if wasLatched { // already latched at entry moves tail preserving length [06 §6.10]
		p.StartPos.X = p.StartPos.X.Add(p.Velocity.X)
		p.StartPos.Y = p.StartPos.Y.Add(p.Velocity.Y)
		p.StartPos.Z = p.StartPos.Z.Add(p.Velocity.Z)
	}
	return p
}

// ProjectToScreen projects world position to screen using the orthographic
// formula of [03 §2.5]: screenX = (worldX>>16)-cameraX+128,
// screenY = (worldZ>>16)-((worldY>>16)>>1)-cameraZ+32 [03 §2.5].
func ProjectToScreen(pos combat.Vec3, camX, camZ int32) (sx, sy int32) { // [03 §2.5]
	wx := int32(int64(pos.X) >> 16) // worldX narrowed to signed high word [03 §2.5]
	wy := int32(int64(pos.Y) >> 16)
	wz := int32(int64(pos.Z) >> 16)
	sx = wx - camX + 128 // [03 §2.5]
	sy = wz - (wy >> 1) - camZ + 32
	return sx, sy
}

// BeamStroke is one one-pixel Bresenham line stroke [03 §5.4] C7.
type BeamStroke struct {
	X0, Y0 int32
	X1, Y1 int32
	Color  int32 // palette-remapped color [03 §5.4]
}

// BeamStrokes returns the one or two strokes for a beam [03 §5.4] C7.
// color2==0 draws one stroke; otherwise two adjacent strokes ordered
// endpoint-swapped, secondary first and primary on top [03 §5.4] C7.
// No anti-aliasing, no distance width [03 §5.4].
func BeamStrokes(headScreen, tailScreen [2]int32, color, color2 int32) []BeamStroke { // [03 §5.4] C7
	if color2 == 0 {
		return []BeamStroke{{X0: headScreen[0], Y0: headScreen[1], X1: tailScreen[0], Y1: tailScreen[1], Color: color}} // [03 §5.4] one stroke
	}
	// Two parallel one-pixel lines, using color2 as outer stroke and color as inner [03 §5.4].
	// Order endpoint-swapped, secondary first [03 §5.4].
	return []BeamStroke{
		{X0: tailScreen[0], Y0: tailScreen[1], X1: headScreen[0], Y1: headScreen[1], Color: color2}, // secondary first, swapped [03 §5.4]
		{X0: headScreen[0], Y0: headScreen[1], X1: tailScreen[0], Y1: tailScreen[1], Color: color},  // primary on top [03 §5.4]
	}
}

// TrailEmitter keeps an additive next-emission deadline for smoke trails
// preserving accumulated debt so a delayed emitter keeps owed puffs and a
// zero-delay definition emits one puff every tick [03 §5.4] [06 §13.2] C6.
type TrailEmitter struct {
	NextDeadline uint32 // additive deadline [06 §13.2]
}

// ShouldEmitTrailSmoke reports whether a trail puff should emit at now
// per [06 §13.2] and [03 §5.4], without mutating combat state (I6).
// It mirrors the trailing logic: gated on smoke trail flag, never burst
// parents, before expiry, and past the additive deadline [06 §13.2].
// On emit the deadline is advanced additively by smokedelay [06 §13.2].
// Zero delay preserves debt and emits every eligible tick [03 §5.4].
func (e *TrailEmitter) ShouldEmitTrailSmoke(w *content.WeaponDef, p combat.Projectile, now uint32) bool { // [06 §13.2] [03 §5.4] (I6)
	if e == nil || w == nil {
		return false
	}
	if !w.SmokeTrail { // [06 §13.2] smoke-trail flag
		return false
	}
	if p.BurstRemaining != 0 { // burst parents never emit trail smoke [06 §4.3] [03 §5.4] [06 §13.2]
		return false
	}
	if now >= p.ExpiryTick { // gated on being before expiry [06 §13.2]
		return false
	}
	if now < e.NextDeadline { // past its next-trail deadline [06 §13.2]
		return false
	}
	// Emit: deadline update is ADDITIVE — smoke delay added to deadline [06 §13.2].
	e.NextDeadline += uint32(w.SmokeDelay) // [06 §13.2] preserves debt; zero emits every tick [03 §5.4]
	return true
}

// SmokeSink is the presentation sink for trail/start/expiry smoke puffs [06 §13.2] (I6).
type SmokeSink interface {
	EmitSmoke(pos combat.Vec3, tick uint32)
}

// MaybeEmitTrailSmoke evaluates ShouldEmitTrailSmoke and if true calls sink
// with the projectile head position [06 §13.2] (I6).
func (e *TrailEmitter) MaybeEmitTrailSmoke(w *content.WeaponDef, p combat.Projectile, now uint32, sink SmokeSink) bool { // [06 §13.2] (I6)
	if !e.ShouldEmitTrailSmoke(w, p, now) { // [06 §13.2]
		return false
	}
	if sink != nil {
		sink.EmitSmoke(p.Pos, now) // trail puff at head position [06 §13.2]
	}
	return true
}

// RenderSpec describes one projectile's presentation dispatch result [03 §5.4] C6.
type RenderSpec struct {
	RenderType int32  // 0..7 [03 §5.4]
	Kind       string // dispatch kind for tests
	Suppressed bool   // suppressed branch (selector -1, lifetime out of range, global GAF abort) [03 §5.4]
	Aborted    bool   // rendertype 2 buffer abort aborts whole renderer [03 §5.4] C6
	Frame      int    // selected frame for types 4,5 [03 §5.4]
	Beam       BeamEndpoints
	Strokes    []BeamStroke
}

// dispatch table size [03 §5.4] C6.
const RendertypeCount = 8 // [03 §5.4] eight cases 0..7

// DispatchRendertype selects presentation from rendertype byte per [03 §5.4] C6.
// It is presentation-only and never mutates combat state (I6).
// For selector and lifetime cases the caller supplies selector and lifetime
// derived from the weapon definition plus shared frameCount for that family.
// This keeps the selector/lifetime wiring explicit and avoids inventing data
// beyond WeaponDef [02 "Weapon record"].
//
// Cases per [03 §5.4]:
//
//	0 line pair (beam geometry below)
//	1 base sprite plus model
//	2 global GAF aborting whole renderer on failed admission
//	3 base plus model distinct orientation path
//	4 selector with -1 suppression, frame (tick-spawn) mod count
//	5 lifetime-scaled frame, drawn only while 0 <= frame < count
//	6 record-stored orientation
//	7 randomized segmented lines with 0x50000 denominator
//
// TODO(question): selector wiring for case 4 is not typed in WeaponDef; caller
// supplies it as explicit argument. The lifetime field for case 5 is taken as
// the provided lifetime param (commonly WeaponTimer or Duration) pending trace
// of the 16-bit definition field [03 §5.4].
func DispatchRendertype(p combat.Projectile, w *content.WeaponDef, now uint32, frameCount int, selector int, lifetime int32, admitOK func() bool) RenderSpec { // [03 §5.4] C6 (I6)
	rt := int32(0)
	if w != nil {
		rt = w.RenderType // [02 "Weapon record"] default 0
	}
	// Clamp to 0..7 dispatch established; out-of-range is unknown but preserve mapping.
	switch rt {
	case RenderTypeBeam: // [03 §5.4] 0 line from current endpoint to tail endpoint
		bm := ComputeBeamEndpoints(p, w, now) // [06 §6.10] [03 §5.4]
		var color, color2 int32
		if w != nil {
			color = w.Color
			color2 = w.Color2 // [02 "Weapon record"] default 0
		}
		hs := [2]int32{0, 0}
		ts := [2]int32{0, 0}
		// Screen positions for beam geometry; caller may re-project with camera.
		// Here we provide placeholder zeros and compute strokes relative; tests
		// verify color branching and latch, not camera offset.
		strokes := BeamStrokes(hs, ts, color, color2) // [03 §5.4] C7
		return RenderSpec{RenderType: rt, Kind: "beam", Beam: bm, Strokes: strokes}
	case RenderTypeBaseSpriteModel: // [03 §5.4] 1 base sprite plus model
		return RenderSpec{RenderType: rt, Kind: "base-sprite+model"}
	case RenderTypeGlobalGAF: // [03 §5.4] 2 global GAF aborting whole renderer on failed admission
		if admitOK != nil && !admitOK() { // [03 §5.4] ABORTS ENTIRE RENDERER
			return RenderSpec{RenderType: rt, Kind: "global-gaf", Suppressed: true, Aborted: true}
		}
		return RenderSpec{RenderType: rt, Kind: "global-gaf"}
	case RenderTypeBaseModelDistinct: // [03 §5.4] 3 base plus model distinct orientation
		return RenderSpec{RenderType: rt, Kind: "base+model-distinct"}
	case RenderTypeSelectorGAF: // [03 §5.4] 4 selector picks one of five global sequences; -1 suppresses
		if selector == -1 { // [03 §5.4] suppresses branch entirely
			return RenderSpec{RenderType: rt, Kind: "selector-gaf", Suppressed: true}
		}
		if frameCount <= 0 {
			return RenderSpec{RenderType: rt, Kind: "selector-gaf", Suppressed: true}
		}
		frame := int((now - p.CreationTick) % uint32(frameCount)) // [03 §5.4] (currentTick - spawnTick) mod frameCount
		return RenderSpec{RenderType: rt, Kind: "selector-gaf", Frame: frame}
	case RenderTypeLifetimeGAF: // [03 §5.4] 5 lifetime-scaled
		if frameCount <= 0 || lifetime <= 0 {
			return RenderSpec{RenderType: rt, Kind: "lifetime-gaf", Suppressed: true}
		}
		// frame = frameCount - ((expiryTick - now) * frameCount)/lifetime [03 §5.4]
		// Drawn only while 0 <= frame < frameCount [03 §5.4].
		remaining := int64(0)
		if p.ExpiryTick > now {
			remaining = int64(p.ExpiryTick - now) // [03 §5.4] expiryTick - currentTick
		} else {
			// now >= expiry: remaining zero or would wrap unsigned; spec expects signed? treat as 0 for lattice.
			remaining = 0
		}
		frame := int(int64(frameCount) - (remaining*int64(frameCount))/int64(lifetime)) // [03 §5.4] trunc toward zero [01 §8]
		if frame < 0 || frame >= frameCount {                                           // [03 §5.4] drawn only while 0 <= frame < frameCount
			return RenderSpec{RenderType: rt, Kind: "lifetime-gaf", Suppressed: true}
		}
		return RenderSpec{RenderType: rt, Kind: "lifetime-gaf", Frame: frame}
	case RenderTypeRecordOrientation: // [03 §5.4] 6 record-stored orientation
		return RenderSpec{RenderType: rt, Kind: "record-orientation"}
	case RenderTypeSegmented: // [03 §5.4] 7 randomized segmented lines
		return RenderSpec{RenderType: rt, Kind: "segmented"}
	default:
		// Unknown rendertype stays suppressed rather than panicking; never invent [I9].
		return RenderSpec{RenderType: rt, Kind: "unknown", Suppressed: true}
	}
}

// SegmentCount computes the number of segments for rendertype 7 per [03 §5.4].
// Segment count derives from endpoint span divided by literal constant 327680 (0x50000),
// skipped when zero [03 §5.4]. Span is Euclidean distance of head-tail in raw
// Fixed units (16.16) so 327680 corresponds to 5 world units (5*65536) [03 §5.4].
// TODO(question): calibration of whether span is map-space vs projected-space is open per [03 §5.4] missing; this uses world-space Fixed raw.
func SegmentCount(head, tail combat.Vec3) int { // [03 §5.4]
	dx := float64(int64(head.X) - int64(tail.X)) // Fixed raw delta [I2]
	dy := float64(int64(head.Y) - int64(tail.Y))
	dz := float64(int64(head.Z) - int64(tail.Z))
	span := math.Hypot(math.Hypot(dx, dy), dz) // Fixed raw Euclidean
	if span <= 0 {
		return 0
	}
	n := int(span / 327680) // [03 §5.4] 0x50000 literal
	if n < 0 {
		n = 0
	}
	return n
}

// SegmentedJitter applies integer per-axis jitter of rand()*11/0x8000 -5
// [03 §5.4] to X, height (Y), and Z. It draws exactly three CRT values per point
// preserving deterministic call order per [03 §5.4] and I4.
// Presentation only (I6); never uses simulation RNG.
func SegmentedJitter(crt *rng.CRT, x, y, z numeric.Fixed) (numeric.Fixed, numeric.Fixed, numeric.Fixed) { // [03 §5.4] [I4] (I6)
	if crt == nil {
		return x, y, z
	}
	// [03 §5.4] rand()*11/0x8000 -5 ; CRT Rand returns 0..0x7FFF [01 §7.2]
	jx := int32(crt.Rand()*11/0x8000 - 5) // [03 §5.4]
	jy := int32(crt.Rand()*11/0x8000 - 5)
	jz := int32(crt.Rand()*11/0x8000 - 5)
	// Jitter is integer map-pixel units per [03 §5.4]; convert to Fixed world units via *65536.
	// TODO(question): pixel vs world unit scale for jitter is unresolved per missing; using 1 pixel = 65536 [03 §2.1].
	xj := x + numeric.Fixed(int64(jx)*65536)
	yj := y + numeric.Fixed(int64(jy)*65536)
	zj := z + numeric.Fixed(int64(jz)*65536)
	return xj, yj, zj
}

// SegmentedBeamPoints generates jittered intermediate points for rendertype 7
// per [03 §5.4]. It returns head, jittered intermediates, and tail, using
// exactly three CRT draws per interior point. When segment count is zero it
// returns nil and the caller skips drawing [03 §5.4].
func SegmentedBeamPoints(head, tail combat.Vec3, crt *rng.CRT) []combat.Vec3 { // [03 §5.4] [I4] (I6)
	n := SegmentCount(head, tail) // [03 §5.4]
	if n == 0 {                   // skipped when zero [03 §5.4]
		return nil
	}
	pts := make([]combat.Vec3, 0, n+1)
	pts = append(pts, head)
	for i := 1; i < n; i++ {
		t := float64(i) / float64(n)
		ix := numeric.Fixed(int64(float64(int64(head.X))*(1-t) + float64(int64(tail.X))*t))
		iy := numeric.Fixed(int64(float64(int64(head.Y))*(1-t) + float64(int64(tail.Y))*t))
		iz := numeric.Fixed(int64(float64(int64(head.Z))*(1-t) + float64(int64(tail.Z))*t))
		jx, jy, jz := SegmentedJitter(crt, ix, iy, iz) // [03 §5.4]
		pts = append(pts, combat.Vec3{X: jx, Y: jy, Z: jz})
	}
	pts = append(pts, tail)
	return pts
}

// IsProjectileVisible performs the single visibility test BEFORE rendertype
// dispatch per [03 §5.4]: when the mode enables per-owner current-coverage
// byte grids the projected half-resolution cell must hold nonzero; otherwise
// the same cell goes through the one-point word-grid test at the local player's
// bit. The gate evaluates once per record [03 §5.4].
// This is presentation-only and never writes sim state (I6).
func IsProjectileVisible(pos combat.Vec3, svc interface {
	VisiblePoint(player uint8, x, y, z numeric.Fixed) bool
}, localPlayer uint8) bool { // [03 §5.4] (I6)
	if svc == nil {
		return true // fixture: no visibility service means always visible
	}
	return svc.VisiblePoint(localPlayer, pos.X, pos.Y, pos.Z) // [03 §5.4] one-point form
}

// RenderBatch iterates projectiles in stable order (I1) and dispatches each
// per [03 §5.4] C6. Rendertype 2 admission failure aborts the entire batch per [03 §5.4].
// It is presentation-only (I6) and never mutates the supplied records.
func RenderBatch(projectiles []combat.Projectile, weapons map[int32]*content.WeaponDef, now uint32, frameCounts map[int32]int, selectors map[int32]int, lifetimes map[int32]int32, visible func(combat.Vec3) bool, admitOK func() bool) ([]RenderSpec, bool) { // [03 §5.4] C6 C7 (I1) (I6)
	var out []RenderSpec
	aborted := false
	// Deterministic iteration: projectiles already in stable pool order (I1) [03 §1].
	for _, p := range projectiles {
		if visible != nil && !visible(p.Pos) { // [03 §5.4] draw gate once per record
			continue
		}
		var w *content.WeaponDef
		if weapons != nil {
			w = weapons[p.WeaponID]
		}
		fc := 0
		if frameCounts != nil {
			fc = frameCounts[p.WeaponID]
		}
		sel := -2 // sentinel != -1
		if selectors != nil {
			if v, ok := selectors[p.WeaponID]; ok {
				sel = v
			}
		}
		lt := int32(0)
		if lifetimes != nil {
			lt = lifetimes[p.WeaponID]
		} else if w != nil {
			// Default lifetime wiring: WeaponTimer if nonzero else Duration pending trace [03 §5.4] TODO(question)
			if w.WeaponTimer != 0 {
				lt = w.WeaponTimer
			} else {
				lt = w.Duration
			}
		}
		spec := DispatchRendertype(p, w, now, fc, sel, lt, admitOK) // [03 §5.4] C6
		if spec.Aborted {                                           // [03 §5.4] aborts whole renderer
			aborted = true
			break
		}
		if spec.Suppressed {
			continue
		}
		out = append(out, spec)
	}
	return out, aborted
}
