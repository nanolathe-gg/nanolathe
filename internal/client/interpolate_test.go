package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// These tests lock the Enhanced blend of docs/DESIGN_GPU_RENDERER.md §13.5:
// the arithmetic (16.16 fraction, truncation toward zero, shortest arc) and the
// identity rules that decide when a subject is blended at all rather than
// snapped to the current pose.

func wu(n int64) numeric.Fixed { return numeric.Fixed(n) * numeric.FixedOne }

func unitAt(slot pool.Handle, x, z numeric.Fixed, heading uint16) frame.UnitView {
	return frame.UnitView{Slot: slot, DefID: 7, Owner: 1, X: x, Z: z, Heading: heading}
}

func blendOne(t *testing.T, prevUnit, curUnit frame.UnitView, f16 int64) frame.UnitView {
	t.Helper()
	var in interpolator
	prev := &frame.Frame{Tick: 1, Units: []frame.UnitView{prevUnit}}
	cur := &frame.Frame{Tick: 2, Units: []frame.UnitView{curUnit}}
	view := in.blend(prev, cur, f16)
	if len(view.Units) != 1 {
		t.Fatalf("blended units = %d, want 1", len(view.Units))
	}
	if view.Tick != cur.Tick {
		t.Fatalf("blended tick = %d, want the current tick %d", view.Tick, cur.Tick)
	}
	return view.Units[0]
}

// A unit moving ten world units per tick is halfway along at a half fraction.
func TestUnitBlendsHalfwayAtHalfFraction(t *testing.T) {
	got := blendOne(t, unitAt(3, wu(100), wu(50), 0), unitAt(3, wu(110), wu(50), 0), int64(fractionOne)/2)
	if got.X != wu(105) {
		t.Fatalf("blended X = %d, want %d", got.X, wu(105))
	}
	if got.Z != wu(50) {
		t.Fatalf("blended Z = %d, want %d", got.Z, wu(50))
	}
}

// The fraction of zero reproduces the previous pose exactly; that is the
// endpoint the four benchmark fractions start from.
func TestZeroFractionReproducesPreviousPose(t *testing.T) {
	prev := unitAt(3, wu(100), wu(50), 1000)
	prev.Pieces = []frame.PieceView{{Tx: wu(1), RotY: 4000}}
	cur := unitAt(3, wu(110), wu(60), 2000)
	cur.Pieces = []frame.PieceView{{Tx: wu(5), RotY: 9000}}
	got := blendOne(t, prev, cur, 0)
	if got.X != prev.X || got.Z != prev.Z || got.Heading != prev.Heading {
		t.Fatalf("blend at fraction zero = (%d,%d,%d), want the previous pose (%d,%d,%d)", got.X, got.Z, got.Heading, prev.X, prev.Z, prev.Heading)
	}
	if got.Pieces[0].Tx != wu(1) || got.Pieces[0].RotY != 4000 {
		t.Fatalf("piece at fraction zero = (%d,%d), want the previous transform", got.Pieces[0].Tx, got.Pieces[0].RotY)
	}
	// The blended pieces live in the interpolator's own buffer; the committed
	// frame must be untouched [I6].
	if cur.Pieces[0].Tx != wu(5) {
		t.Fatalf("the committed piece was mutated: Tx = %d", cur.Pieces[0].Tx)
	}
}

// Pieces blend with the unit that carries them, and a piece hidden in either
// tick keeps the current tick's transform.
func TestPiecesBlendUnlessHidden(t *testing.T) {
	prev := unitAt(3, 0, 0, 0)
	prev.Pieces = []frame.PieceView{{Tx: wu(0), RotX: 0}, {Tx: wu(0), Hidden: true}}
	cur := unitAt(3, 0, 0, 0)
	cur.Pieces = []frame.PieceView{{Tx: wu(8), RotX: 1000}, {Tx: wu(8)}}
	got := blendOne(t, prev, cur, int64(fractionOne)/4)
	if got.Pieces[0].Tx != wu(2) || got.Pieces[0].RotX != 250 {
		t.Fatalf("blended piece = (%d,%d), want (%d,250)", got.Pieces[0].Tx, got.Pieces[0].RotX, wu(2))
	}
	if got.Pieces[1].Tx != wu(8) {
		t.Fatalf("piece hidden in the previous tick = %d, want the current transform %d", got.Pieces[1].Tx, wu(8))
	}
}

// A heading that crosses zero sweeps the short way, not three quarters of the
// circle backwards.
func TestHeadingBlendsAlongTheShortArc(t *testing.T) {
	// The signed difference between 65036 and 500 is +1000, so the midpoint is
	// 500 past 65036 — which is zero. Blending the unsigned values instead would
	// land near half a circle away.
	got := blendOne(t, unitAt(3, 0, 0, 65036), unitAt(3, 0, 0, 500), int64(fractionOne)/2)
	if got.Heading != 0 {
		t.Fatalf("blended heading = %d, want 0", got.Heading)
	}
	// The same arc backwards.
	got = blendOne(t, unitAt(3, 0, 0, 500), unitAt(3, 0, 0, 65036), int64(fractionOne)/2)
	if got.Heading != 0 {
		t.Fatalf("blended heading backwards = %d, want 0", got.Heading)
	}
}

// A pool slot reused by a different definition is a different subject, so it
// takes the current pose rather than sliding out of the dead unit's position
// [I5].
func TestReusedSlotSnaps(t *testing.T) {
	prev := unitAt(3, wu(100), wu(50), 0)
	cur := unitAt(3, wu(104), wu(50), 0)
	cur.DefID = 8
	got := blendOne(t, prev, cur, int64(fractionOne)/2)
	if got.X != wu(104) {
		t.Fatalf("blended X = %d, want the current pose %d", got.X, wu(104))
	}
}

// Beyond the presentation snap bound the subject is treated as teleported.
func TestLongDisplacementSnaps(t *testing.T) {
	got := blendOne(t, unitAt(3, wu(100), wu(50), 0), unitAt(3, wu(200), wu(50), 0), int64(fractionOne)/2)
	if got.X != wu(200) {
		t.Fatalf("blended X = %d, want the current pose %d", got.X, wu(200))
	}
	// Just inside the bound still blends.
	got = blendOne(t, unitAt(3, wu(100), wu(50), 0), unitAt(3, wu(160), wu(50), 0), int64(fractionOne)/2)
	if got.X != wu(130) {
		t.Fatalf("blended X inside the bound = %d, want %d", got.X, wu(130))
	}
}

// Projectiles match on handle plus weapon, shooter and creation tick; effects
// match on their presentation identity. A mismatch snaps.
func TestProjectileAndEffectIdentity(t *testing.T) {
	var in interpolator
	prev := &frame.Frame{Tick: 1,
		Projectiles: []frame.ProjectileView{
			{Handle: 2, WeaponID: 5, Shooter: 9, CreationTick: 1, X: wu(10), Yaw: 100},
			{Handle: 3, WeaponID: 5, Shooter: 9, CreationTick: 1, X: wu(10)},
		},
		Effects: []frame.EffectView{{PresentationID: 40, X: wu(0)}},
	}
	cur := &frame.Frame{Tick: 2,
		Projectiles: []frame.ProjectileView{
			{Handle: 2, WeaponID: 5, Shooter: 9, CreationTick: 1, X: wu(20), Yaw: 300},
			{Handle: 3, WeaponID: 5, Shooter: 9, CreationTick: 2, X: wu(20)},
		},
		Effects: []frame.EffectView{{PresentationID: 40, X: wu(8)}, {PresentationID: 41, X: wu(8)}},
	}
	view := in.blend(prev, cur, int64(fractionOne)/2)
	if view.Projectiles[0].X != wu(15) || view.Projectiles[0].Yaw != 200 {
		t.Fatalf("blended projectile = (%d,%d), want (%d,200)", view.Projectiles[0].X, view.Projectiles[0].Yaw, wu(15))
	}
	if view.Projectiles[1].X != wu(20) {
		t.Fatalf("a reused projectile handle blended: X = %d, want %d", view.Projectiles[1].X, wu(20))
	}
	if view.Effects[0].X != wu(4) {
		t.Fatalf("blended effect = %d, want %d", view.Effects[0].X, wu(4))
	}
	if view.Effects[1].X != wu(8) {
		t.Fatalf("a new effect blended: X = %d, want the current position %d", view.Effects[1].X, wu(8))
	}
}

// The retained buffers are reused: a second blend of the same shape allocates
// nothing, which is what lets the modern path record every presented frame.
func TestSteadyStateBlendAllocatesNothing(t *testing.T) {
	var in interpolator
	prev := &frame.Frame{Tick: 1, Units: []frame.UnitView{{Slot: 1, Pieces: []frame.PieceView{{}, {}}}},
		Projectiles: []frame.ProjectileView{{Handle: 1}}, Effects: []frame.EffectView{{PresentationID: 1}}}
	cur := &frame.Frame{Tick: 2, Units: []frame.UnitView{{Slot: 1, Pieces: []frame.PieceView{{}, {}}}},
		Projectiles: []frame.ProjectileView{{Handle: 1}}, Effects: []frame.EffectView{{PresentationID: 1}}}
	in.blend(prev, cur, int64(fractionOne)/2)
	if n := testing.AllocsPerRun(20, func() { in.blend(prev, cur, int64(fractionOne)/2) }); n != 0 {
		t.Fatalf("steady-state blend allocated %v times per frame, want 0", n)
	}
}

// The fraction setter clamps into [0, 1) in the 16.16 domain: the clock's
// float32 carry can round to exactly one [01 §4.2], and a fraction of one would
// show the next tick's pose a tick early.
func TestTickFractionClamps(t *testing.T) {
	var c Client
	c.SetTickFraction(-0.5)
	if c.tickFraction16 != 0 {
		t.Fatalf("negative fraction = %d, want 0", c.tickFraction16)
	}
	c.SetTickFraction(1)
	if c.tickFraction16 != fractionOne-1 {
		t.Fatalf("fraction one = %d, want %d", c.tickFraction16, fractionOne-1)
	}
	c.SetTickFraction(0.5)
	if c.tickFraction16 != fractionOne/2 {
		t.Fatalf("half fraction = %d, want %d", c.tickFraction16, fractionOne/2)
	}
}

// Without SetInterpolation the recorder reads the committed frame itself, which
// is what keeps `--shot` and classic on committed-tick sampling [I6].
func TestPresentationFrameIsCommittedUnlessInterpolating(t *testing.T) {
	buf := frame.NewBuffer()
	for tick := uint32(1); tick <= 2; tick++ {
		f := buf.BeginWrite()
		f.Units = append(f.Units, unitAt(1, wu(int64(tick)*10), 0, 0))
		if err := buf.Publish(tick); err != nil {
			t.Fatalf("publish %d: %v", tick, err)
		}
	}
	c := &Client{buffer: buf}
	c.SetTickFraction(0.5)
	if got := c.presentationFrame(); got != buf.Current() {
		t.Fatal("the recorder read a blended frame with interpolation off")
	}
	c.SetInterpolation(true)
	got := c.presentationFrame()
	if got == buf.Current() {
		t.Fatal("the recorder read the committed frame with interpolation on")
	}
	if got.Units[0].X != wu(15) {
		t.Fatalf("blended X = %d, want %d", got.Units[0].X, wu(15))
	}
}

// The camera moves in the 30 Hz step, so the blend has to carry it too — and it
// must be put back before anything else reads it (§13.5) [I6].
func TestCameraBlendAppliesAndRestores(t *testing.T) {
	buf := frame.NewBuffer()
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096, Scale: 1}
	c, err := New(Options{Buffer: buf})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	c.SetCamera(cam)
	c.SetInterpolation(true)
	// The two fractions are deliberately different: the camera follows the
	// window's Update grid, the world follows the tick (§13.5).
	c.SetTickFraction(0.25)
	c.SetCameraFraction(0.5)
	publish := func(tick uint32) {
		f := buf.BeginWrite()
		f.Units = append(f.Units, unitAt(1, wu(int64(tick)*10), 0, 0))
		if err := buf.Publish(tick); err != nil {
			t.Fatalf("publish %d: %v", tick, err)
		}
	}

	// Step is where the origin sample is taken, after the injected step has
	// moved the camera. One sample is not enough to blend between.
	publish(1)
	c.Step(1.0 / 30)
	if c.beginCameraBlend() {
		t.Fatal("the camera blended from a single sample")
	}
	cam.X, cam.Z = 100, 40
	publish(2)
	c.Step(1.0 / 30)

	applied := c.beginCameraBlend()
	if !applied {
		t.Fatal("two samples did not blend")
	}
	// Half of the step, from the camera fraction — a quarter, from the tick
	// fraction, would be (25,10). Blending the camera by the tick fraction
	// snaps it backwards whenever a tick fires mid-update (§13.5).
	if cam.X != 50 || cam.Z != 20 {
		t.Fatalf("blended origin = (%d,%d), want the camera fraction's halfway origin (50,20)", cam.X, cam.Z)
	}
	c.endCameraBlend(applied)
	if cam.X != 100 || cam.Z != 40 {
		t.Fatalf("restored origin = (%d,%d), want the stepped origin (100,40)", cam.X, cam.Z)
	}

	// The real recording path restores it too: nothing outside the record may
	// observe the blend.
	c.RecordFrame()
	if cam.X != 100 || cam.Z != 40 {
		t.Fatalf("origin after RecordFrame = (%d,%d), want the stepped origin (100,40)", cam.X, cam.Z)
	}
}

// A client that never presents through the window adapter — the 120 TPS
// benchmark, a test harness — supplies no camera fraction, and its camera is
// left alone however the world blends (§13.5).
func TestCameraDoesNotBlendWithoutACameraFraction(t *testing.T) {
	buf := frame.NewBuffer()
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096, Scale: 1}
	c, err := New(Options{Buffer: buf})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	c.SetCamera(cam)
	c.SetInterpolation(true)
	c.SetTickFraction(0.5)
	for tick := uint32(1); tick <= 2; tick++ {
		f := buf.BeginWrite()
		f.Units = append(f.Units, unitAt(1, wu(int64(tick)*10), 0, 0))
		if err := buf.Publish(tick); err != nil {
			t.Fatalf("publish %d: %v", tick, err)
		}
		if tick == 2 {
			cam.X, cam.Z = 100, 40
		}
		c.Step(1.0 / 30)
	}
	if c.beginCameraBlend() {
		t.Fatal("the camera blended with no camera fraction supplied")
	}
	if cam.X != 100 || cam.Z != 40 {
		t.Fatalf("origin = (%d,%d), want the stepped origin (100,40)", cam.X, cam.Z)
	}
}

// A jump larger than the viewport is a minimap click or a bookmark recall, not
// motion: that axis snaps to the current origin (§13.5).
func TestCameraBlendSnapsOnAViewportSizedJump(t *testing.T) {
	if got := lerpOrigin(0, 100, int64(fractionOne)/2, 640); got != 50 {
		t.Fatalf("blend inside the viewport = %d, want 50", got)
	}
	if got := lerpOrigin(0, 641, int64(fractionOne)/2, 640); got != 641 {
		t.Fatalf("blend across a viewport-sized jump = %d, want the current origin 641", got)
	}
	if got := lerpOrigin(1000, 300, int64(fractionOne)/2, 640); got != 300 {
		t.Fatalf("backwards jump = %d, want the current origin 300", got)
	}
	// Truncation toward zero, not rounding [I3].
	if got := lerpOrigin(0, 3, int64(fractionOne)/2, 640); got != 1 {
		t.Fatalf("truncated blend = %d, want 1", got)
	}
}
