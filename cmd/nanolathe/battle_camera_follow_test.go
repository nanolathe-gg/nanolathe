package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// publishFollowedUnit commits a new frame carrying one unit at the tracked
// handle, at map-pixel position (x, y, z). It is the test's stand-in for a
// completed simulation sub-tick's publication [01 §4.4].
func publishFollowedUnit(t *testing.T, buf *frame.Buffer, tick uint32, tracked pool.Handle, x, y, z int32) {
	t.Helper()
	f := buf.BeginWrite()
	f.Units = append(f.Units[:0], frame.UnitView{
		Slot: tracked,
		X:    numeric.Fixed(int64(x) << 16),
		Y:    numeric.Fixed(int64(y) << 16),
		Z:    numeric.Fixed(int64(z) << 16),
	})
	if err := buf.Publish(tick); err != nil {
		t.Fatalf("publish tick %d: %v", tick, err)
	}
}

// followTestFixture builds a minimal battleSession wired for the follow path
// only: a camera, a session with just a snapshot buffer, and a tracked handle
// [07 R-CAM-01 §12]. The map is oversized so the per-axis clamp never engages
// and the arithmetic under test is DesiredOrigin/FollowTo alone.
func followTestFixture(tracked pool.Handle) (*battleSession, *camera.Camera, *frame.Buffer) {
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 1 << 20, MapH: 1 << 20}
	cam.SetTracked(tracked)
	buf := frame.NewBuffer()
	b := &battleSession{sess: &session.Session{Snapshot: buf}, cam: cam}
	return b, cam, buf
}

// Every completed sub-tick advances follow once; a zero-tick host frame
// advances nothing. Intermediate catch-up publications must not be dropped
// [01 §4.4][07 R-CAM-01 §12].
func TestFollowCameraUsesThisFramesPublishedPosition(t *testing.T) {
	const tracked = pool.Handle(7)
	b, cam, buf := followTestFixture(tracked)

	// ticksThisFrame is deliberately irregular: 1,1,2,0,1,2,0,1,1,2 — a stand-in
	// for the sub-tick budget's catch-up behavior across host frames whose
	// wall-clock delta is not perfectly uniform.
	ticksThisFrame := []int{1, 1, 2, 0, 1, 2, 0, 1, 1, 2}
	const velocity = int32(4) // map pixels per sub-tick
	pos := int32(1000)
	var tick uint32
	for i, n := range ticksThisFrame {
		// Latch before the controller runs this frame's input and ticks.
		b.stepFollowCamera()

		wantX, wantZ := cam.X, cam.Z
		for j := 0; j < n; j++ {
			tick++
			pos += velocity
			publishFollowedUnit(t, buf, tick, tracked, pos, 0, 500)
			b.applyPublishedCamera(buf.Current())
			want := cam.DesiredOrigin(camera.TargetPoint{X: pos, Y: 0, Z: 500})
			wantX = stepAxisForTest(wantX, want.X)
			wantZ = stepAxisForTest(wantZ, want.Z)
		}

		if cam.X != wantX || cam.Z != wantZ {
			t.Fatalf("frame %d (ticks=%d): camera=(%d,%d), want (%d,%d) stepped toward the just-published position %d — the follow step is reading a stale publication again", i, n, cam.X, cam.Z, wantX, wantZ, pos)
		}
	}
}

// stepAxisForTest reproduces the phase-10 bounded half-step so the test can
// predict FollowTo's result without depending on an unexported helper
// [01 §4.4].
func stepAxisForTest(current, desired int32) int32 {
	delta := int64(desired) - int64(current)
	switch {
	case delta > 320:
		return current + 320
	case delta < -320:
		return current - 320
	default:
		return int32(int64(current) + delta/2)
	}
}

// TestFollowedUnitScreenPositionSettlesToAConstantOffset drives a follow
// across many presentation frames with a unit moving at a constant per-tick
// velocity (the ordinary case: one sub-tick per host frame) and checks that
// the unit's screen-space offset from the camera origin converges and then
// holds constant — the followed unit sits still on screen while the terrain
// scrolls under it, rather than shaking [07 R-CAM-01 §12].
func TestFollowedUnitScreenPositionSettlesToAConstantOffset(t *testing.T) {
	const tracked = pool.Handle(3)
	b, cam, buf := followTestFixture(tracked)

	const velocity = int32(6)
	pos := int32(2000)
	var tick uint32
	var offsets []int32
	const frames = 40
	for i := 0; i < frames; i++ {
		b.stepFollowCamera()
		tick++
		pos += velocity
		publishFollowedUnit(t, buf, tick, tracked, pos, 0, 300)
		b.applyPublishedCamera(buf.Current())
		offsets = append(offsets, pos-cam.X)
	}

	// The last several frames must agree exactly: once the half-step gap
	// reaches its fixed point relative to a constant velocity, the screen
	// offset stops changing at all. A build that reads a stale publication
	// instead lets a variable per-frame lag leak into this value and it never
	// settles.
	settled := offsets[frames-1]
	for i := frames - 5; i < frames; i++ {
		if offsets[i] != settled {
			t.Fatalf("screen offset at frame %d = %d, want the settled value %d (offsets tail: %v)", i, offsets[i], settled, offsets[frames-5:])
		}
	}
}

// The real controller must consume the automatic repick in its first tick,
// follow again in its second tick, and do nothing on a zero-tick pump.
// [04 R-MOV-03 §1][07 R-CAM-01 §12]
func TestBigBrotherFollowsEveryControllerTick(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(80, 80))
	b.sess.State = session.StateBattle
	first := placeUnit(b, "armcons", numeric.Fixed(400<<16), numeric.Fixed(500<<16))
	second := placeUnit(b, "armfav", numeric.Fixed(900<<16), numeric.Fixed(800<<16))
	first.Flags |= units.SelectedStatus
	b.cam.SetTracked(first.Handle)
	b.cam.MapW, b.cam.MapH = 1<<20, 1<<20
	c := NewBattleController(b, &scriptedMillisSource{samples: []uint32{0, 67, 67}})
	b.dispatchLocalCommand("+BigBrother")
	c.Step(BattleInputFrame{}, nil)
	c.Step(BattleInputFrame{}, nil)
	if b.sess.Clock.GlobalTick != 2 || b.cam.Tracked() != second.Handle {
		t.Fatalf("tick=%d tracked=%v, want tick 2 and %v", b.sess.Clock.GlobalTick, b.cam.Tracked(), second.Handle)
	}
	want := b.cam.DesiredOrigin(camera.TargetPoint{X: 900, Z: 800})
	x := stepAxisForTest(stepAxisForTest(0, want.X), want.X)
	z := stepAxisForTest(stepAxisForTest(0, want.Z), want.Z)
	if b.cam.X != x || b.cam.Z != z {
		t.Fatalf("origin=(%d,%d), want (%d,%d)", b.cam.X, b.cam.Z, x, z)
	}
	b.cam.StoreBookmark(0)
	c.Step(BattleInputFrame{PressedKeys: []input.Key{input.KeyT, input.KeyF5}}, nil)
	if b.cam.Tracked() != 0 {
		t.Fatal("deferred follow undid the later bookmark recall")
	}
	if b.cam.X != x || b.cam.Z != z {
		t.Fatal("zero-tick pump advanced camera")
	}
}
