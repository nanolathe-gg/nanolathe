package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// newStripTestSession builds a minimal fixture for the phase-11 strip
// contracts: seeded per-session streams, a clock the test pins by direct
// tick writes, and the battle-entry strip table [R-CORE-01 §4.4.1].
func newStripTestSession(simSeed, crtSeed uint32) (*Session, *rng.CRT) {
	s := &Session{}
	s.SeedSessionRNG(simSeed, crtSeed)
	s.Clock = &clock.State{}
	s.strips = newStripTable()
	return s, s.CrtRNG()
}

// aliveSmokeContainer builds a smoke-family container that survives a sweep
// (one unexpiring particle, window in the future) and carries an identifying
// marker in src[0]. A marker < 0 means terminal: empty list, window passed.
func smokeContainer(marker int64) stripObject {
	o := stripObject{family: stripFamilySmoke, windowEnd: 0xFFFFFFFF}
	if marker < 0 {
		o.windowEnd = 1 // passed at any swept tick >= 2
		return o
	}
	o.src[0] = numeric.FixedFromInt(marker)
	o.particles = []stripParticle{{expiry: 0xFFFFFFFF}}
	return o
}

// TestStripSweepVerdictBeforeUpdate locks the phase-11 ordering contract
// [R-CORE-01 §4.4.1]: the removal verdict is evaluated BEFORE the update
// work, so a terminal object is destroyed without running its update — even
// when that update would have spawned (and drawn). [R-STRIP-01 §2][§3]
func TestStripSweepVerdictBeforeUpdate(t *testing.T) {
	s, crt := newStripTestSession(11, 11)

	// Terminal object: empty sub-record list, non-smoke family, with a
	// spawn gate that would fire this tick (thirty CRT draws if the update
	// ran first).
	s.strips.append(6, stripObject{
		family:        stripFamilyNano,
		windowEnd:     100,
		nextSpawn:     100,
		spawnInterval: 1,
	})

	draws0 := crt.Draws()
	s.phaseObjectSweeps(100)
	if got := crt.Draws() - draws0; got != 0 {
		t.Fatalf("terminal object consumed %d CRT draws; the verdict must run before any update work", got)
	}
	if n := len(s.strips.strips[6]); n != 0 {
		t.Fatalf("terminal object survived the sweep: %d objects left", n)
	}

	// Contrast: the same gate on a live object (non-empty list) fires during
	// the update and spends the family's per-spawn draws.
	s.strips.append(6, stripObject{
		family:        stripFamilyNano,
		windowEnd:     100,
		nextSpawn:     100,
		spawnInterval: 1,
		particles:     []stripParticle{{expiry: 0xFFFFFFFF}},
	})
	draws1 := crt.Draws()
	s.phaseObjectSweeps(100)
	if got := crt.Draws() - draws1; got != nanoParticlesPerSpawnTick*nanoDrawsPerParticle {
		t.Fatalf("live object's gate spent %d draws, want %d", got, nanoParticlesPerSpawnTick*nanoDrawsPerParticle)
	}
	if n := len(s.strips.strips[6]); n != 1 {
		t.Fatalf("live object was removed: %d objects left", n)
	}
}

// TestStripSweepStableLeftCompaction locks stable compaction: objects
// destroyed by a positive verdict slide left without reordering survivors
// [R-CORE-01 §4.4.1][03 "Strip storage and lifecycle"].
func TestStripSweepStableLeftCompaction(t *testing.T) {
	s, crt := newStripTestSession(12, 12)

	// Five smoke containers, markers 0..4; odd markers are terminal (empty
	// list, window passed), even markers carry one live particle.
	for i := 0; i < 5; i++ {
		var o stripObject
		if i%2 == 0 {
			o = smokeContainer(int64(i))
		} else {
			o = smokeContainer(-1)
		}
		s.strips.append(9, o)
	}

	draws0 := crt.Draws()
	s.phaseObjectSweeps(100)
	if got := crt.Draws() - draws0; got != 0 {
		t.Fatalf("compaction sweep consumed %d draws", got)
	}
	strip := s.strips.strips[9]
	if len(strip) != 3 {
		t.Fatalf("survivors = %d, want 3", len(strip))
	}
	for want, o := range strip {
		if got := o.src[0].Int(); got != int64(want*2) {
			t.Fatalf("survivor %d carries marker %d; stable compaction must preserve relative order", want, got)
		}
	}
}

// TestStripAppendEvictsOldestAbove400 locks the eviction rule: when the
// pre-insert count exceeds 400 the oldest object is destroyed first, so
// steady state holds at most 401 records and same-strip order among
// survivors equals insertion order [03 "Strip storage and lifecycle"]
// [R-STRIP-01 §1].
func TestStripAppendEvictsOldestAbove400(t *testing.T) {
	s, _ := newStripTestSession(13, 13)

	for i := 0; i <= 400; i++ {
		s.strips.append(2, smokeContainer(int64(i)))
	}
	if n := len(s.strips.strips[2]); n != 401 {
		t.Fatalf("count = %d at the cap boundary, want 401", n)
	}
	if first := s.strips.strips[2][0].src[0].Int(); first != 0 {
		t.Fatalf("oldest object evicted too early: first marker %d, want 0", first)
	}

	// Pre-insert count 401 exceeds 400: the marker-0 object dies first.
	s.strips.append(2, smokeContainer(401))
	strip := s.strips.strips[2]
	if len(strip) != 401 {
		t.Fatalf("count = %d after the eviction append, want 401", len(strip))
	}
	if last := strip[len(strip)-1].src[0].Int(); last != 401 {
		t.Fatalf("last marker %d, want 401 (appended at the vector end)", last)
	}
	for i, o := range strip {
		if o.src[0].Int() != int64(i+1) {
			t.Fatalf("marker at %d is %d; survivors must keep insertion order", i, o.src[0].Int())
		}
	}
}

// TestNanoEmitterProducerAndSweep locks the wired strip-6 producer and its
// sweep behavior [R-STRIP-01 §1 strip 6][R-STRIP-01 §3][03 §5.5]: thirty CRT
// draws at the producer (five particles, six coordinate draws each), a
// second thirty-draw spawn on the next tick's gate — ten particles over two
// ticks — plus the colour ramp, the 0x100 word, trunc(distance/4) lifetimes,
// and the four-world-units-per-tick travel.
func TestNanoEmitterProducerAndSweep(t *testing.T) {
	s, crt := newStripTestSession(14, 14)
	ref := rng.NewCRT(14)
	for i := 0; i < 30; i++ {
		ref.Rand() // walk the reference to the post-producer state
	}

	s.Clock.GlobalTick = 100
	src := [3]numeric.Fixed{numeric.FixedFromInt(0), numeric.FixedFromInt(0), numeric.FixedFromInt(0)}
	dst := [3]numeric.Fixed{numeric.FixedFromInt(8), numeric.FixedFromInt(0), numeric.FixedFromInt(0)}

	draws0 := crt.Draws()
	s.appendStripNanoEmitter(src, dst)
	if got := crt.Draws() - draws0; got != nanoParticlesPerSpawnTick*nanoDrawsPerParticle {
		t.Fatalf("producer spent %d draws, want %d (five particles, six draws each)", got, nanoParticlesPerSpawnTick*nanoDrawsPerParticle)
	}
	if crt.State != ref.State {
		t.Fatal("CRT state diverged from the reference stream after the producer spawn")
	}

	strip := s.strips.strips[6]
	if len(strip) != 1 {
		t.Fatalf("strip 6 holds %d objects, want 1", len(strip))
	}
	o := strip[0]
	if o.family != stripFamilyNano || o.windowEnd != 101 || o.nextSpawn != 101 {
		t.Fatalf("emitter window/next-spawn = %d/%d, want 101/101 (window closes one tick after creation)", o.windowEnd, o.nextSpawn)
	}
	if len(o.particles) != nanoParticlesPerSpawnTick {
		t.Fatalf("%d particles after the producer spawn, want 5", len(o.particles))
	}
	for i, p := range o.particles {
		if want := uint8(0xa0 | uint8(1+i%7)); p.color != want {
			t.Fatalf("particle %d color %#x, want %#x (ramp starts at the spawn index)", i, p.color, want)
		}
		if p.reservedWord != 0x100 {
			t.Fatalf("particle %d reserved word %#x, want 0x100", i, p.reservedWord)
		}
		// distance 8 → lifetime trunc(8/4) = 2 ticks; travel = 4 wu/tick.
		if p.expiry != 102 {
			t.Fatalf("particle %d expiry %d, want 102 (spawn tick + trunc(distance/4))", i, p.expiry)
		}
		if p.vx != numeric.FixedFromInt(4) || p.vy != 0 || p.vz != 0 {
			t.Fatalf("particle %d velocity (%d,%d,%d) wu/tick, want 4 toward the landing point", i, p.vx.Int(), p.vy.Int(), p.vz.Int())
		}
		if p.x != src[0] {
			t.Fatalf("particle %d spawn point %d, want the source point (zero-extent boxes)", i, p.x.Int())
		}
	}

	// Same tick's sweep: the gate is armed for tick 101, so phase 11 at 100
	// consumes no draws [R-STRIP-01 §2 spawn gate].
	draws1 := crt.Draws()
	s.phaseObjectSweeps(100)
	if got := crt.Draws() - draws1; got != 0 {
		t.Fatalf("pre-gate sweep spent %d draws, want 0", got)
	}

	// Gate tick: five more particles, thirty more draws, identical stream
	// order.
	s.phaseObjectSweeps(101)
	if got := crt.Draws() - draws1; got != nanoParticlesPerSpawnTick*nanoDrawsPerParticle {
		t.Fatalf("gate spawn spent %d draws, want %d", got, nanoParticlesPerSpawnTick*nanoDrawsPerParticle)
	}
	for i := 0; i < 30; i++ {
		ref.Rand() // walk the reference over the gate spawn's draws
	}
	if crt.State != ref.State {
		t.Fatal("CRT state diverged from the reference stream after the gate spawn")
	}
	strip = s.strips.strips[6]
	if len(strip) != 1 || len(strip[0].particles) != 2*nanoParticlesPerSpawnTick {
		t.Fatalf("ten particles over two ticks: got %d objects / %d particles", len(strip), len(strip[0].particles))
	}

	// Particles expire once their expiry tick has passed, then the emptied
	// container dies on the next invocation — without spending a draw. The
	// second batch (spawned at 101) expires at 103 and is dropped at 104.
	s.phaseObjectSweeps(103)
	strip = s.strips.strips[6]
	if len(strip) != 1 || len(strip[0].particles) != nanoParticlesPerSpawnTick {
		t.Fatalf("first batch must drop at 103: %d objects / %d particles left", len(strip), len(strip[0].particles))
	}
	s.phaseObjectSweeps(104)
	strip = s.strips.strips[6]
	if len(strip) != 1 || len(strip[0].particles) != 0 {
		t.Fatalf("expired particles must drop: %d objects / %d particles left", len(strip), len(strip[0].particles))
	}
	draws2 := crt.Draws()
	s.phaseObjectSweeps(105) // verdict: list empty → destroyed
	if got := crt.Draws() - draws2; got != 0 {
		t.Fatalf("removal sweep spent %d draws", got)
	}
	if len(s.strips.strips[6]) != 0 {
		t.Fatal("emptied container must die on the next invocation")
	}
}

// TestStripEmptyTableSweepTouchesNothing: a sweep over only empty strips
// consumes no draws and changes no globals [R-CORE-01 §4.4.1][R-STRIP-01
// §3]; the simulation stream is never touched by phase 11.
func TestStripEmptyTableSweepTouchesNothing(t *testing.T) {
	s, crt := newStripTestSession(15, 15)
	sim0, crt0 := s.SimRNG().Draws(), crt.Draws()
	simState0, crtState0 := s.SimRNG().State, crt.State
	for tick := uint32(1); tick <= 5; tick++ {
		s.phaseObjectSweeps(tick)
	}
	if s.SimRNG().Draws() != sim0 || s.SimRNG().State != simState0 {
		t.Fatal("phase 11 touched the simulation stream")
	}
	if crt.Draws() != crt0 || crt.State != crtState0 {
		t.Fatal("empty-table sweep consumed CRT draws")
	}
}

// TestStripProducerCensusKeepsWriterlessStripsEmpty: the complete producer
// census gives strips 0/1/3/4/8 no writer anywhere [R-STRIP-01 §1], and the
// in-session producer set today writes only strip 6 (nano submissions) plus
// the smoke scaffolding for strips 5/9. Strips 2 and 7 have retail producers
// but none wired in-session. Nothing may invent events for the writerless
// strips.
func TestStripProducerCensusKeepsWriterlessStripsEmpty(t *testing.T) {
	s, _ := newStripTestSession(16, 16)
	s.Clock.GlobalTick = 10

	point := [3]numeric.Fixed{numeric.FixedFromInt(3), numeric.FixedFromInt(0), numeric.FixedFromInt(3)}
	farPoint := [3]numeric.Fixed{numeric.FixedFromInt(103), numeric.FixedFromInt(0), numeric.FixedFromInt(3)}
	for i := 0; i < 3; i++ {
		s.appendStripNanoEmitter(point, farPoint)
	}
	s.appendStripSmokePuffer(9, point, 900, 0) // sinking-wreck smoke column profile
	s.appendStripSmokePuffer(5, point, 30, 0)  // burning-feature smoke profile

	for tick := uint32(10); tick <= 15; tick++ {
		s.phaseObjectSweeps(tick)
	}

	for _, strip := range []int{0, 1, 3, 4, 8} {
		if n := len(s.strips.strips[strip]); n != 0 {
			t.Fatalf("strip %d holds %d objects; the census gives it no producer [R-STRIP-01 §1]", strip, n)
		}
	}
	for _, strip := range []int{2, 7} {
		if n := len(s.strips.strips[strip]); n != 0 {
			t.Fatalf("strip %d holds %d objects; its producers are not wired in-session", strip, n)
		}
	}
	if len(s.strips.strips[6]) == 0 {
		t.Fatal("strip 6 received no nano emitters")
	}
}

// TestSmokeFamilySweepDrawsAndWindow locks the smoke family's committed
// behavior: one CRT draw per spawned puff (start frame), one draw per
// animation-frame advance with the next delay drawn as half to full of the
// authored delay [R-STRIP-01 §3], and the published wind words applied ×8 to
// the X and Z terms each tick [R-WIND-01].
func TestSmokeFamilySweepDrawsAndWindow(t *testing.T) {
	s, crt := newStripTestSession(17, 17)
	ref := rng.NewCRT(17)
	startFrame := ref.Rand() // the spawn's single start-frame draw

	s.Clock.GlobalTick = 50
	pos := [3]numeric.Fixed{numeric.FixedFromInt(100), numeric.FixedFromInt(0), numeric.FixedFromInt(200)}
	s.appendStripSmokePuffer(9, pos, 60, 8)
	o := &s.strips.strips[9][0]
	o.nextSpawn = 50
	o.windowEnd = 50 // one spawn: the gate dies once nextSpawn (53) passes the window
	o.spawnInterval = 3

	draws0 := crt.Draws()
	s.phaseObjectSweeps(50)
	if got := crt.Draws() - draws0; got != 1 {
		t.Fatalf("smoke spawn spent %d draws, want 1 (start frame)", got)
	}
	p := o.particles[len(o.particles)-1]
	if p.frame != startFrame {
		t.Fatal("start-frame draw diverged from the reference stream")
	}
	if o.particles[0].x != pos[0] || o.particles[0].z != pos[2] {
		t.Fatalf("puff drifted without wind: (%d,%d)", o.particles[0].x.Int(), o.particles[0].z.Int())
	}

	// Wind drift: ×8 per tick on the published words [R-WIND-01]; the spawn
	// tick does not drift (spawning is the update's last step).
	s.Wind = &world.Wind{DirX: 3, DirZ: -2}
	draws1 := crt.Draws()
	for tick := uint32(51); tick <= 58; tick++ {
		s.phaseObjectSweeps(tick)
	}
	// Countdown 8 reaches zero on tick 58: the frame advances and the next
	// delay is drawn as half to full of the authored delay — one draw.
	if got := crt.Draws() - draws1; got != 1 {
		t.Fatalf("animation advance spent %d draws, want 1", got)
	}
	p = o.particles[len(o.particles)-1]
	advance := ref.Rand()
	wantDelay := 4 + int32(int64(advance)*int64(4)/0x8000)
	if p.frameDelay != wantDelay || p.frame != startFrame+1 {
		t.Fatalf("after the advance: frame %d delay %d, want frame %d delay %d (half-to-full of 8)",
			p.frame, p.frameDelay, startFrame+1, wantDelay)
	}
	if wantX, wantZ := int64(100)+3*8*8, int64(200)+(-2)*8*8; p.x.Int() != wantX || p.z.Int() != wantZ {
		t.Fatalf("wind drift (%d,%d), want (%d,%d)", p.x.Int(), p.z.Int(), wantX, wantZ)
	}
}

// TestSmokeWindowPassedVerdict locks the smoke family's verdict: the
// container dies only when its list is empty AND its window has passed
// [R-STRIP-01 §2].
func TestSmokeWindowPassedVerdict(t *testing.T) {
	s, _ := newStripTestSession(18, 18)

	s.strips.append(5, stripObject{family: stripFamilySmoke, windowEnd: 200}) // empty, window not passed
	s.strips.append(5, stripObject{family: stripFamilySmoke, windowEnd: 5})   // empty, window passed
	busy := stripObject{family: stripFamilySmoke, windowEnd: 5}               // window passed, list not empty
	busy.particles = []stripParticle{{expiry: 0xFFFFFFFF}}
	s.strips.append(5, busy)

	s.phaseObjectSweeps(100)
	strip := s.strips.strips[5]
	if len(strip) != 2 {
		t.Fatalf("%d containers survived, want 2", len(strip))
	}
	if strip[0].windowEnd != 200 || len(strip[1].particles) != 1 {
		t.Fatal("wrong survivors: the verdict must require an empty list AND a passed window")
	}
}

// TestSprinkleSpawnDrawsAndBounds locks the sprinkle family's per-spawn
// contract: exactly three CRT draws, jitter rand×7/0x8000 − 3 per axis
// [R-STRIP-01 §1 strip 2][R-STRIP-01 §3].
func TestSprinkleSpawnDrawsAndBounds(t *testing.T) {
	s, crt := newStripTestSession(19, 19)
	ref := rng.NewCRT(19)

	s.Clock.GlobalTick = 7
	jitter := [3]int64{
		int64(ref.Rand())*7/0x8000 - 3,
		int64(ref.Rand())*7/0x8000 - 3,
		int64(ref.Rand())*7/0x8000 - 3,
	}
	s.strips.append(2, stripObject{
		family:        stripFamilySprinkle,
		windowEnd:     100,
		nextSpawn:     7,
		spawnInterval: 16,
		// The family's constructor spawns its first puff immediately, so a
		// live container always holds a sub-record; without one the removal
		// verdict fires before the gate.
		particles: []stripParticle{{expiry: 0xFFFFFFFF}},
		src: [3]numeric.Fixed{
			numeric.FixedFromInt(10), numeric.FixedFromInt(20), numeric.FixedFromInt(30),
		},
	})

	draws0 := crt.Draws()
	s.phaseObjectSweeps(7)
	if got := crt.Draws() - draws0; got != 3 {
		t.Fatalf("sprinkle spawn spent %d draws, want 3 (per-axis jitter)", got)
	}
	particles := s.strips.strips[2][0].particles
	p := particles[len(particles)-1] // the spawned puff, after the creation marker
	if p.x.Int() != 10+jitter[0] || p.y.Int() != 20+jitter[1] || p.z.Int() != 30+jitter[2] {
		t.Fatalf("jittered spawn (%d,%d,%d), want (%d,%d,%d)",
			p.x.Int(), p.y.Int(), p.z.Int(),
			10+jitter[0], 20+jitter[1], 30+jitter[2])
	}
	for _, j := range jitter {
		if j < -3 || j > 3 {
			t.Fatalf("jitter %d outside the researched [-3,3] band", j)
		}
	}
}

// TestFlameTrailSweepSpendsNoDraws locks the strip-7 trail family: one
// segment per gated tick with no CRT draws, flying from source to target
// over the flight length [R-STRIP-01 §1 strip 7][R-STRIP-01 §3].
func TestFlameTrailSweepSpendsNoDraws(t *testing.T) {
	s, crt := newStripTestSession(20, 20)

	s.Clock.GlobalTick = 30
	s.strips.append(7, stripObject{
		family:        stripFamilyFlameTrail,
		windowEnd:     36,
		nextSpawn:     30,
		spawnInterval: 1,
		particleLife:  6,
		// The trail lays its first segment at creation, so a live container
		// holds a sub-record; without one the removal verdict fires before
		// the gate.
		particles: []stripParticle{{expiry: 0xFFFFFFFF}},
		src: [3]numeric.Fixed{
			numeric.FixedFromInt(0), numeric.FixedFromInt(0), numeric.FixedFromInt(0),
		},
		dst: [3]numeric.Fixed{
			numeric.FixedFromInt(12), numeric.FixedFromInt(0), numeric.FixedFromInt(0),
		},
	})

	draws0 := crt.Draws()
	s.phaseObjectSweeps(30)
	if got := crt.Draws() - draws0; got != 0 {
		t.Fatalf("trail spawn spent %d draws, want 0 [R-STRIP-01 §3]", got)
	}
	particles := s.strips.strips[7][0].particles
	p := particles[len(particles)-1] // the spawned segment, after the creation marker
	if p.vx != numeric.FixedFromInt(2) {
		t.Fatalf("trail velocity %d wu/tick, want 2 (12 wu over the 6-tick flight)", p.vx.Int())
	}
	if p.expiry != 36 {
		t.Fatalf("trail expiry %d, want the window end 36", p.expiry)
	}
}
