package render

import (
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestShakeRequestVectors locks the magnitude/duration handling [03 §5.6] (C9).
func TestShakeRequestVectors(t *testing.T) {
	var s Shake
	// First request with zeroed duration state runs for half authored duration [03 §5.6].
	s.Request(10, 10)
	if s.Duration() != 5 || s.Remaining() != 5 {
		t.Fatalf("first duration=%d remaining=%d want 5,5", s.Duration(), s.Remaining())
	}
	if s.AmpX() != 10 || s.AmpY() != 10 {
		t.Fatalf("amp %d,%d want 10,10", s.AmpX(), s.AmpY())
	}
	if !s.IsActive() {
		t.Fatal("active want true after duration>0")
	}
	// Second request while active blends against current duration and accumulates amplitude [03 §5.6].
	s.Request(6, 8)
	// new duration = trunc((5+8)/2)=6, remaining=6, amp 16
	if s.Duration() != 6 || s.Remaining() != 6 {
		t.Fatalf("blend duration=%d remaining=%d want 6,6", s.Duration(), s.Remaining())
	}
	if s.AmpX() != 16 || s.AmpY() != 16 {
		t.Fatalf("accumulated amp %d,%d want 16,16", s.AmpX(), s.AmpY())
	}
	// Truncation toward zero: odd sum (5+6)/2=5 not 6
	var s2 Shake
	s2.Request(0, 5)
	if s2.Duration() != 2 {
		t.Fatalf("dur (0+5)/2=%d want 2 trunc", s2.Duration())
	}
	s2.Request(0, 6) // (2+6)/2=4
	if s2.Duration() != 4 {
		t.Fatalf("dur (2+6)/2=%d want 4", s2.Duration())
	}
	// (5+6)/2=5 trunc test via direct durations
	var s3 Shake
	// set up duration 5 manually via first request 10->5 then request 6
	s3.Request(0, 10) // dur 5
	if s3.Duration() != 5 {
		t.Fatalf("setup dur %d want 5", s3.Duration())
	}
	s3.Request(0, 6) // (5+6)/2=5 trunc toward zero
	if s3.Duration() != 5 {
		t.Fatalf("trunc (5+6)/2=%d want 5", s3.Duration())
	}
}

// TestShakeInactiveClearAndStaleDuration locks inactive clearing and stale duration blend [03 §5.6].
func TestShakeInactiveClearAndStaleDuration(t *testing.T) {
	var s Shake
	s.Request(10, 10) // dur=5 amp=10
	// Simulate ticks until inactive
	cam := &camera.Camera{X: 0, Z: 0, MapW: 10000, MapH: 10000, ViewW: 640, ViewH: 480}
	crt := rng.NewCRT(1)
	for s.IsActive() {
		s.Tick(cam, &crt)
		if s.Remaining() == 0 {
			// next tick will deactivate
			s.Tick(cam, &crt)
			break
		}
	}
	if s.IsActive() {
		t.Fatalf("should be inactive after decay, remaining %d", s.Remaining())
	}
	staleDur := s.Duration() // should be 5 left unchanged after inactive
	if staleDur != 5 {
		t.Fatalf("stale duration %d want 5", staleDur)
	}
	ampBefore := s.AmpX() // 10 still? but inactive clearing happens on next request
	// Next request while inactive should clear amplitudes then accumulate
	s.Request(7, 9) // (5+9)/2=7, clear amp then +7
	if s.AmpX() != 7 || s.AmpY() != 7 {
		t.Fatalf("after inactive clear amp %d,%d want 7,7 (was %d)", s.AmpX(), s.AmpY(), ampBefore)
	}
	if s.Duration() != 7 || s.Remaining() != 7 {
		t.Fatalf("after stale blend dur %d rem %d want 7,7", s.Duration(), s.Remaining())
	}
}

// TestShakePreferenceBit locks the 0x10 gate [03 §5.6] (C9).
func TestShakePreferenceBit(t *testing.T) {
	var s Shake
	s.Request(10, 10)
	dur := s.Duration()
	amp := s.AmpX()
	// With prefs bit set, request returns with state untouched [03 §5.6].
	s.RequestWithPrefs(99, 99, 0x10)
	if s.Duration() != dur || s.AmpX() != amp {
		t.Fatalf("prefs 0x10 should leave state untouched dur %d->%d amp %d->%d", dur, s.Duration(), amp, s.AmpX())
	}
	// Without bit, it mutates
	s.RequestWithPrefs(1, 2, 0x00)
	if s.Duration() == dur && s.AmpX() == amp {
		t.Fatalf("prefs 0x00 should mutate")
	}
	// Disabled via SetDisabled also untouched
	var s2 Shake
	s2.Request(5, 6)
	s2.SetDisabled(true)
	dur2 := s2.Duration()
	amp2 := s2.AmpX()
	s2.Request(99, 99)
	if s2.Duration() != dur2 || s2.AmpX() != amp2 {
		t.Fatalf("disabled should leave untouched")
	}
	s2.SetDisabled(false)
	s2.Request(1, 2)
	if s2.Duration() == dur2 {
		t.Fatalf("re-enabled should mutate")
	}
}

// TestShakeDecayLinear locks linear decay envelope sx = amp*remaining/duration [03 §5.6].
func TestShakeDecayLinear(t *testing.T) {
	var s Shake
	s.RequestXY(20, -20, 8) // duration (0+8)/2=4, amp 20,-20
	if s.Duration() != 4 {
		t.Fatalf("dur %d want 4", s.Duration())
	}
	// Check envelope sequence remaining 4,3,2,1
	cases := []struct {
		remaining int32
		wantSX    int32
		wantSY    int32
	}{
		{4, 20, -20},
		{3, 15, -15},
		{2, 10, -10},
		{1, 5, -5},
	}
	for i, c := range cases {
		if s.Remaining() != c.remaining {
			t.Fatalf("step %d remaining %d want %d", i, s.Remaining(), c.remaining)
		}
		sx := s.AmpX() * s.Remaining() / s.Duration()
		sy := s.AmpY() * s.Remaining() / s.Duration()
		if sx != c.wantSX || sy != c.wantSY {
			t.Fatalf("step %d envelope sx=%d sy=%d want %d,%d", i, sx, sy, c.wantSX, c.wantSY)
		}
		cam := &camera.Camera{X: 5000, Z: 5000, MapW: 20000, MapH: 20000, ViewW: 640, ViewH: 480}
		crt := rng.NewCRT(42)
		// Tick to advance remaining for next iteration (but we just checked envelope before tick)
		// Use a fresh shake copy to avoid mutating the main s's remaining for envelope checks?
		// Advance main s
		s.Tick(cam, &crt)
	}
	// After 4 ticks remaining 0, next tick deactivates
	if s.Remaining() != 0 {
		t.Fatalf("remaining %d want 0 after 4 ticks", s.Remaining())
	}
	if !s.IsActive() {
		t.Fatalf("active should still be true until next Tick sees remaining<=0")
	}
	cam := &camera.Camera{X: 5000, Z: 5000, MapW: 20000, MapH: 20000, ViewW: 640, ViewH: 480}
	crt := rng.NewCRT(1)
	s.Tick(cam, &crt) // remaining 0 -> deactivate, no draws
	if s.IsActive() {
		t.Fatalf("should be inactive after remaining 0 tick")
	}
}

// TestShakeDrawCount asserts exactly two CRT draws per active tick [03 §5.6] (I4).
func TestShakeDrawCount(t *testing.T) {
	var s Shake
	cam := &camera.Camera{X: 1000, Z: 1000, MapW: 10000, MapH: 10000, ViewW: 640, ViewH: 480}
	crt := rng.NewCRT(1)
	// Inactive costs zero draws
	before := crt.Draws()
	s.Tick(cam, &crt)
	if crt.Draws() != before {
		t.Fatalf("inactive tick drew %d want 0", crt.Draws()-before)
	}
	s.Request(10, 10) // dur 5
	// 5 active ticks each costs 2 draws =10
	for i := 0; i < 5; i++ {
		before = crt.Draws()
		s.Tick(cam, &crt)
		if crt.Draws()-before != 2 {
			t.Fatalf("tick %d draws %d want 2", i, crt.Draws()-before)
		}
	}
	// After remaining 0, next tick deactivates with 0 draws
	before = crt.Draws()
	s.Tick(cam, &crt)
	if crt.Draws() != before {
		t.Fatalf("deactivating tick draws %d want 0", crt.Draws()-before)
	}
	if s.IsActive() {
		t.Fatalf("should be inactive")
	}
	// Further inactive ticks still 0
	before = crt.Draws()
	s.Tick(cam, &crt)
	if crt.Draws() != before {
		t.Fatalf("post-inactive tick draws %d want 0", crt.Draws()-before)
	}
}

// TestShakeDeterminism locks CRT-seeded determinism [03 §5.6] (I4).
func TestShakeDeterminism(t *testing.T) {
	run := func(seed uint32) (int32, int32, uint64) {
		var s Shake
		s.Request(12, 12) // dur 6
		cam := &camera.Camera{X: 5000, Z: 5000, MapW: 20000, MapH: 20000, ViewW: 640, ViewH: 480}
		crt := rng.NewCRT(seed)
		for i := 0; i < 6; i++ {
			s.Tick(cam, &crt)
		}
		return cam.X, cam.Z, crt.Draws()
	}
	x1, z1, d1 := run(12345)
	x2, z2, d2 := run(12345)
	if x1 != x2 || z1 != z2 {
		t.Fatalf("same seed diverged (%d,%d) vs (%d,%d)", x1, z1, x2, z2)
	}
	if d1 != 12 || d2 != 12 {
		t.Fatalf("draws %d %d want 12", d1, d2)
	}
	x3, z3, _ := run(54321)
	if x1 == x3 && z1 == z3 {
		t.Fatalf("different seeds produced same camera (%d,%d)", x1, z1)
	}
}

// TestShakeClamp verifies map clamping after jitter [03 §5.6] [07 §10].
func TestShakeClamp(t *testing.T) {
	var s Shake
	// Large magnitude to push off-map
	s.Request(1000, 10) // dur 5
	cam := &camera.Camera{X: 0, Z: 0, MapW: 100, MapH: 100, ViewW: 64, ViewH: 64}
	crt := rng.NewCRT(1)
	s.Tick(cam, &crt)
	if cam.X < 0 || cam.X > cam.MapW-cam.ViewW {
		t.Fatalf("clamp failed X=%d range [0,%d]", cam.X, cam.MapW-cam.ViewW)
	}
	if cam.Z < 0 || cam.Z > cam.MapH-cam.ViewH {
		t.Fatalf("clamp failed Z=%d", cam.Z)
	}
	// At max edge, also clamps
	cam.X = 36 // max =36
	cam.Z = 36
	s.Request(1000, 10)
	for i := 0; i < 5; i++ {
		s.Tick(cam, &crt)
		if cam.X < 0 || cam.X > 36 || cam.Z < 0 || cam.Z > 36 {
			t.Fatalf("edge clamp failed tick %d (%d,%d)", i, cam.X, cam.Z)
		}
	}
	// Negative maximum domain (view larger than map) per [07 §10]
	cam2 := &camera.Camera{X: 10, Z: 10, MapW: 50, MapH: 50, ViewW: 100, ViewH: 100}
	// max = -50, clamp order: if cam<0 ->0 else if cam> max ( -50) -> max
	// So positive cam should clamp to -50, negative to 0
	var s2 Shake
	s2.Request(5, 4) // dur 2
	crt2 := rng.NewCRT(2)
	// Start at 10, tick will add jitter then clamp: 10+dx -> if still >-50? Actually 10 > -50 true, so clamp to -50
	s2.Tick(cam2, &crt2)
	// After tick, due to clamp logic, positive camera in negative-max domain clamps to negative max
	if cam2.X != -50 && cam2.X != 0 {
		// Could be 0 if jitter pushed negative
		t.Fatalf("negative-max clamp X=%d want -50 or 0", cam2.X)
	}
}

// TestShakePermanentWalk verifies displacement is permanent (no restore) [03 §5.6].
func TestShakePermanentWalk(t *testing.T) {
	var s Shake
	s.Request(20, 8) // dur 4
	cam := &camera.Camera{X: 5000, Z: 5000, MapW: 20000, MapH: 20000, ViewW: 640, ViewH: 480}
	crt := rng.NewCRT(999)
	startX, startZ := cam.X, cam.Z
	for s.IsActive() {
		s.Tick(cam, &crt)
		if s.Remaining() == 0 {
			// will deactivate next tick
			s.Tick(cam, &crt)
			break
		}
	}
	if cam.X == startX && cam.Z == startZ {
		t.Fatalf("permanent walk: camera unchanged after shake (%d,%d)", cam.X, cam.Z)
	}
	if !s.IsActive() {
		// remain at offset, nothing restores it
		if cam.X == startX && cam.Z == startZ {
			t.Fatalf("shake should leave random walk offset")
		}
	}
}

// TestShakePresentationBoundary ensures shake uses CRT not Sim [03 §5.6] (I4) (I6).
func TestShakePresentationBoundary(t *testing.T) {
	data, err := os.ReadFile("shake.go")
	if err != nil {
		t.Fatalf("read shake.go: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "Global.Sim") || strings.Contains(s, "rng.Global") && strings.Contains(s, ".Sim") {
		t.Fatalf("shake.go must not use simulation RNG (I4) [03 §5.6]")
	}
	if strings.Contains(s, "Simulation") && strings.Contains(s, "Sim") {
		// Allow import of rng but not use of Simulation type for draws
		if strings.Contains(s, "Simulation") && strings.Contains(s, "Uint32n") {
			t.Fatalf("shake.go should not drive Simulation stream")
		}
	}
	if strings.Contains(s, "math/rand") {
		t.Fatalf("shake.go must not import math/rand (I4)")
	}
	if !strings.Contains(s, "214013") && !strings.Contains(s, "Rand()") {
		t.Fatalf("shake.go should contain CRT LCG 214013 or Rand() per I4/C9")
	}
	if strings.Contains(s, "time.Now") || strings.Contains(s, "time.Since") {
		t.Fatalf("shake.go must not use wall-clock (I6)")
	}
}
