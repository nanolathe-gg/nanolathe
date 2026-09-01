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
// census gives strips 0/1/3/4/8 no writer anywhere [R-STRIP-01 §1]. The full
// production producer set — strip-6 nano submissions, the strips-5/9 smoke
// sites (impact, weapon-fire start, burning-feature, sinking-wreck
// profiles), and the strips-2/7 sprinkle producers (the emit-sfx thrust and
// sub-bubble cases) — must leave those five strips empty. Nothing may invent
// events for the writerless strips.
func TestStripProducerCensusKeepsWriterlessStripsEmpty(t *testing.T) {
	s, _ := newStripTestSession(16, 16)
	s.Clock.GlobalTick = 10

	point := [3]numeric.Fixed{numeric.FixedFromInt(3), numeric.FixedFromInt(0), numeric.FixedFromInt(3)}
	farPoint := [3]numeric.Fixed{numeric.FixedFromInt(103), numeric.FixedFromInt(0), numeric.FixedFromInt(3)}
	for i := 0; i < 3; i++ {
		s.appendStripNanoEmitter(point, farPoint)
	}
	s.appendStripSmokePuffer(9, point, SmokePuffInit{SpawnInterval: 15, Lifetime: 900}) // sinking-wreck smoke column profile
	s.appendStripSmokePuffer(5, point, SmokePuffInit{SpawnInterval: 3, Lifetime: 30})   // burning-feature smoke profile
	s.appendStripSmokePuffer(9, point, SmokePuffTrail)                                  // impact / weapon-fire smoke profile
	s.appendStripSprinkle(2, point, 16, 1)                                              // emit-sfx thrust pair (type 2)
	s.appendStripSprinkle(2, point, 8, 1)                                               // emit-sfx thrust pair (type 3)
	s.appendStripSprinkle(7, point, 8, 0)                                               // emit-sfx sub-bubbles (0x103)

	for tick := uint32(10); tick <= 15; tick++ {
		s.phaseObjectSweeps(tick)
	}

	for _, strip := range []int{0, 1, 3, 4, 8} {
		if n := len(s.strips.strips[strip]); n != 0 {
			t.Fatalf("strip %d holds %d objects; the census gives it no producer [R-STRIP-01 §1]", strip, n)
		}
	}
	for _, strip := range []int{2, 5, 6, 7, 9} {
		if n := len(s.strips.strips[strip]); n == 0 {
			t.Fatalf("strip %d received no objects from the wired producer set", strip)
		}
	}
}

// TestSmokeFamilySweepDrawsAndWindow locks the smoke family's committed
// behavior: one CRT draw per spawned puff (start frame), one draw per
// animation-frame advance with the next delay drawn as half to full of the
// authored delay [R-STRIP-01 §3], and the published wind words applied ×8 to
// the X and Z terms each tick [R-WIND-01]. The container is assembled
// directly so the gate, animation, and drift mechanics are pinned
// independently of any producer site's parameters.
func TestSmokeFamilySweepDrawsAndWindow(t *testing.T) {
	s, crt := newStripTestSession(17, 17)
	ref := rng.NewCRT(17)
	// Corrected 2026-08-31: the spawn's single draw is the puff's LAST FRAME,
	// not a start frame, and the cursor starts at 0 [03 R-STRIP-01 §2]. This
	// fixture binds no entry frame count, so the container has none to draw
	// against and the value is discarded — but the draw is still spent, because
	// the producer spends it whether or not the entry resolved.
	_ = ref.Rand()

	s.Clock.GlobalTick = 50
	pos := [3]numeric.Fixed{numeric.FixedFromInt(100), numeric.FixedFromInt(0), numeric.FixedFromInt(200)}
	o := stripObject{family: stripFamilySmoke, src: pos, frameDelayParam: 8, windowEnd: 50, nextSpawn: 50, spawnInterval: 3}
	s.strips.append(9, o)

	draws0 := crt.Draws()
	s.phaseObjectSweeps(50)
	if got := crt.Draws() - draws0; got != 1 {
		t.Fatalf("smoke spawn spent %d draws, want 1 (the puff's last frame)", got)
	}
	live := &s.strips.strips[9][0]
	p := live.particles[len(live.particles)-1]
	if p.frame != 0 {
		t.Fatalf("puff cursor starts at %d, want 0", p.frame)
	}
	if live.particles[0].x != pos[0] || live.particles[0].z != pos[2] {
		t.Fatalf("puff drifted without wind: (%d,%d)", live.particles[0].x.Int(), live.particles[0].z.Int())
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
	p = live.particles[len(live.particles)-1]
	advance := ref.Rand()
	wantDelay := 4 + int32(int64(advance)*int64(4)/0x8000)
	if p.frameDelay != wantDelay || p.frame != 1 {
		t.Fatalf("after the advance: frame %d delay %d, want frame 1 delay %d (half-to-full of 8)",
			p.frame, p.frameDelay, wantDelay)
	}
	// Corrected 2026-08-31: the ×8 scales the RAW 16.16 word, not the whole
	// world unit [R-WIND-01][03 R-FX-01 §3]. Eight ticks of a wind word of
	// three therefore move the puff 192 raw units — three thousandths of a
	// world unit — and its whole-unit position does not change at all. This
	// assertion previously read the drift as whole units and so locked a puff
	// that crossed the map in seconds.
	if wantX, wantZ := int64(100)<<16|0, int64(200)<<16; p.x.Raw() != wantX+3*8*8 || p.z.Raw() != wantZ+(-2)*8*8 {
		t.Fatalf("wind drift raw (%d,%d), want (%d,%d)", p.x.Raw(), p.z.Raw(), wantX+3*8*8, wantZ+(-2)*8*8)
	}
	if dx, dz := p.x.Raw()-(int64(100)<<16), p.z.Raw()-(int64(200)<<16); dx >= 65536 || dx <= -65536 || dz >= 65536 || dz <= -65536 {
		t.Fatalf("eight ticks of wind moved the puff a whole world unit: raw deltas (%d,%d)", dx, dz)
	}
}

// TestSmokeProducerSpawnsFirstPuff locks the producer-side contract shared by
// every wired smoke site [R-STRIP-01 §1 strips 5/9][R-STRIP-01 §2][R-STRIP-01
// §3]: the family's constructor spawns its first puff immediately, spending
// exactly one CRT draw — the puff's LAST frame — and carries the site's
// animation hold into the puff.
func TestSmokeProducerSpawnsFirstPuff(t *testing.T) {
	s, crt := newStripTestSession(21, 21)
	ref := rng.NewCRT(21)
	_ = ref.Rand() // the spawn's single draw is the puff's last frame [03 R-STRIP-01 §2]

	s.Clock.GlobalTick = 9
	pos := [3]numeric.Fixed{numeric.FixedFromInt(5), numeric.FixedFromInt(0), numeric.FixedFromInt(6)}
	draws0 := crt.Draws()
	s.appendStripSmokePuffer(9, pos, SmokePuffTrail)
	if got := crt.Draws() - draws0; got != 1 {
		t.Fatalf("producer spent %d draws, want 1 (the puff's last frame)", got)
	}

	strip := s.strips.strips[9]
	if len(strip) != 1 || len(strip[0].particles) != 1 {
		t.Fatalf("producer left %d containers / %d puffs, want 1/1", len(strip), len(strip[0].particles))
	}
	p := strip[0].particles[0]
	if p.frame != 0 {
		t.Fatalf("puff cursor starts at %d, want 0", p.frame)
	}
	if p.x != pos[0] || p.z != pos[2] {
		t.Fatal("first puff did not spawn at the site's position")
	}
	// The trail producer's lifetime is 0, so its spawn window closes
	// immediately: the gate is armed for the next tick but can never fire past
	// a window that has already passed, and the container dies with its one
	// particle [06 R-WFX-01 §5].
	if strip[0].windowEnd != 0 {
		t.Fatalf("window end %d, want the lifetime-0 window closed at once", strip[0].windowEnd)
	}
	if strip[0].nextSpawn <= strip[0].windowEnd {
		t.Fatalf("gate %d is inside a window ending %d; a lifetime-0 emitter must spawn exactly once",
			strip[0].nextSpawn, strip[0].windowEnd)
	}
	if p.expiry != 0 {
		t.Fatalf("puff tick deadline %d, want none — this family expires on its frame cursor", p.expiry)
	}
	if p.frameDelay != smokeDefaultFrameDelay {
		t.Fatalf("puff hold %d, want the frameHold-0 default of 7", p.frameDelay)
	}
}

// TestSprinkleProducerTwoPuffsAndLifetime locks the strips-2/7 sprinkle
// producer [R-STRIP-01 §1 strips 2/7][R-STRIP-01 §2][R-STRIP-01 §3]: three
// CRT draws at the producer (per-axis jitter), a second three-draw spawn from
// the phase-11 gate on the next tick and then no more (the spawn window
// closes one tick after creation), each puff living spacing×6 ticks, and the
// palette entry selected by the producer's init flag.
func TestSprinkleProducerTwoPuffsAndLifetime(t *testing.T) {
	s, crt := newStripTestSession(22, 22)
	ref := rng.NewCRT(22)
	j0 := [3]int64{
		int64(ref.Rand())*7/0x8000 - 3,
		int64(ref.Rand())*7/0x8000 - 3,
		int64(ref.Rand())*7/0x8000 - 3,
	}

	s.Clock.GlobalTick = 40
	pos := [3]numeric.Fixed{numeric.FixedFromInt(10), numeric.FixedFromInt(20), numeric.FixedFromInt(30)}
	draws0 := crt.Draws()
	s.appendStripSprinkle(2, pos, 16, 1)
	if got := crt.Draws() - draws0; got != 3 {
		t.Fatalf("producer spent %d draws, want 3 (per-axis jitter)", got)
	}

	o := &s.strips.strips[2][0]
	if o.family != stripFamilySprinkle || o.windowEnd != 41 || o.nextSpawn != 41 || o.spawnInterval != 1 {
		t.Fatalf("window/gate = %d/%d/%d, want 41/41/1 (window closes one tick after creation)",
			o.windowEnd, o.nextSpawn, o.spawnInterval)
	}
	if len(o.particles) != 1 {
		t.Fatalf("%d puffs after the producer, want 1", len(o.particles))
	}
	p := o.particles[0]
	if p.color != 0x61 {
		t.Fatalf("colorSel=1 puff color %#x, want 0x61", p.color)
	}
	if p.x.Int() != 10+j0[0] || p.y.Int() != 20+j0[1] || p.z.Int() != 30+j0[2] {
		t.Fatalf("jittered spawn (%d,%d,%d), want (%d,%d,%d)",
			p.x.Int(), p.y.Int(), p.z.Int(), 10+j0[0], 20+j0[1], 30+j0[2])
	}
	if p.expiry != 40+16*6 {
		t.Fatalf("puff expiry %d, want %d (spacing×6 lifetime)", p.expiry, 40+16*6)
	}

	// The creation tick's sweep must not fire the gate; the next tick's
	// spawns the second puff (three more draws) and advances past the closed
	// window so no third spawn ever fires.
	draws1 := crt.Draws()
	s.phaseObjectSweeps(40)
	if got := crt.Draws() - draws1; got != 0 {
		t.Fatalf("creation-tick sweep spent %d draws, want 0", got)
	}
	s.phaseObjectSweeps(41)
	if got := crt.Draws() - draws1; got != 3 {
		t.Fatalf("gate spawn spent %d draws, want 3", got)
	}
	if len(o.particles) != 2 {
		t.Fatalf("%d puffs after the gate, want 2", len(o.particles))
	}
	s.phaseObjectSweeps(42)
	if got := crt.Draws() - draws1; got != 3 {
		t.Fatalf("closed-window sweep spent %d draws, want 3 (no third spawn)", got)
	}

	// colorSel zero selects the other palette entry (the strip-7 variant's
	// selection).
	s.appendStripSprinkle(7, pos, 8, 0)
	if p := s.strips.strips[7][0].particles[0]; p.color != 0x67 {
		t.Fatalf("colorSel=0 puff color %#x, want 0x67", p.color)
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

// TestGeothermalSteamCadenceAndWindow locks the strip-4 producer of
// [05 R-ECO-02 §3] as [06-adjacent research] closed it: spawn interval 5,
// frame hold 7, container lifetime 150 ticks, one puff from the constructor.
//
// The three literals were recorded as "which of the family's init parameters
// each binds to" Unknown until 2026-08-31, and the family was recorded as the
// flame class rather than the smoke-puff class. Both are now traced, and the
// cadence is the whole visible behaviour of a geothermal vent — the definition
// itself is a one-pixel placement marker with no artwork.
func TestGeothermalSteamCadenceAndWindow(t *testing.T) {
	s, _ := newStripTestSession(21, 21)
	pos := [3]numeric.Fixed{numeric.FixedFromInt(104), numeric.FixedFromInt(40), numeric.FixedFromInt(152)}

	s.Clock.GlobalTick = 0
	s.appendStripGeothermalSteam(pos)

	objs := s.strips.strips[stripGeothermalSteam]
	if len(objs) != 1 {
		t.Fatalf("strip 4 holds %d objects after the producer, want 1", len(objs))
	}
	o := objs[0]
	if o.family != stripFamilySmoke {
		t.Fatalf("the geothermal producer built family %d; it is the smoke-puff family, not the flame class", o.family)
	}
	if o.spawnInterval != geothermalSteamInterval || o.windowEnd != geothermalSteamLifetime {
		t.Fatalf("interval %d window %d, want interval 5 and a 150-tick window", o.spawnInterval, o.windowEnd)
	}
	if o.frameDelayParam != smokeDefaultFrameDelay {
		t.Fatalf("frame hold %d; the producer passes 0 and the constructor defaults it to 7", o.frameDelayParam)
	}
	if len(o.particles) != 1 {
		t.Fatalf("the constructor spawned %d puffs, want exactly one", len(o.particles))
	}
	if o.src != pos {
		t.Fatalf("the emitter sits at %v, want the stamp's centre/height point %v", o.src, pos)
	}

	// Sweeping across the window must spawn on the fifth tick and every fifth
	// tick after it, and must stop once the next-spawn tick passes the window.
	// Puffs expire on their own animation clock, so the count is read as
	// "spawns seen", tracked by watching it rise.
	spawns := 1
	last := 1
	for tick := uint32(1); tick <= 400; tick++ {
		s.Clock.GlobalTick = tick
		s.phaseObjectSweeps(tick)
		cur := s.strips.strips[stripGeothermalSteam]
		if len(cur) == 0 {
			if tick <= geothermalSteamLifetime {
				t.Fatalf("the emitter was destroyed at tick %d, inside its own %d-tick window", tick, geothermalSteamLifetime)
			}
			break
		}
		if n := len(cur[0].particles); n > last {
			spawns += n - last
		}
		last = len(cur[0].particles)
	}

	// The window admits next-spawn ticks 5, 10, ... 150: thirty gate spawns
	// plus the constructor's own.
	const wantSpawns = 1 + int(geothermalSteamLifetime)/int(geothermalSteamInterval)
	if spawns != wantSpawns {
		t.Fatalf("the emitter produced %d puffs over its window, want %d (one at construction plus one every %d ticks through tick %d)",
			spawns, wantSpawns, geothermalSteamInterval, geothermalSteamLifetime)
	}
}

// TestEveryStripFamilyMirrorsItsOwnDrawForm locks the per-sub-record draw of
// [03 R-STRIP-01 §2]: each mirrored particle carries either the GAF entry its
// family blits or the colour of the two-by-two rectangle its family fills, and
// never both.
//
// Strip 6 is excluded on purpose: the nanolathe spray already has a
// presentation path, and mirroring its particles beside it would draw the same
// spray twice.
func TestEveryStripFamilyMirrorsItsOwnDrawForm(t *testing.T) {
	s, _ := newStripTestSession(22, 22)
	s.Clock.GlobalTick = 0
	s.appendStripGeothermalSteam([3]numeric.Fixed{numeric.FixedFromInt(104), 0, numeric.FixedFromInt(152)})
	s.appendStripSmokePuffer(9, [3]numeric.Fixed{}, SmokePuffTrail)
	s.appendStripSprinkle(2, [3]numeric.Fixed{}, 8, 0)
	s.appendStripSprinkle(7, [3]numeric.Fixed{}, 16, 1)
	s.appendStripNanoEmitter(
		[3]numeric.Fixed{0, 0, 0},
		[3]numeric.Fixed{numeric.FixedFromInt(40), 0, numeric.FixedFromInt(40)},
	)

	views := s.appendStripParticleViews(nil, 0)
	if len(views) == 0 {
		t.Fatal("no strip particle reached the committed frame")
	}
	byStrip := map[int8]int{}
	for _, v := range views {
		byStrip[v.Strip]++
		if v.Graphic != "" && v.StripFill != 0 {
			t.Fatalf("strip %d view carries both a GAF entry (%q) and a fill colour (%#x); a family does one or the other", v.Strip, v.Graphic, v.StripFill)
		}
		if v.Graphic == "" && v.StripFill == 0 {
			t.Fatalf("strip %d view carries neither an entry nor a fill colour, so nothing can draw it", v.Strip)
		}
		switch v.Strip {
		case 4, 9:
			if v.Graphic != smokePuffEntry {
				t.Fatalf("strip %d smoke view names %q, want the smoke-puff entry", v.Strip, v.Graphic)
			}
		case 2, 7:
			// The sprinkle pair is 0x61/0x67 [R-STRIP-01 §1 strips 2/7].
			if v.StripFill != 0x61 && v.StripFill != 0x67 {
				t.Fatalf("strip %d sprinkle fill %#x is off the authored pair", v.Strip, v.StripFill)
			}
		default:
			t.Fatalf("strip %d was mirrored unexpectedly", v.Strip)
		}
	}
	for _, strip := range []int8{2, 4, 7, 9} {
		if byStrip[strip] == 0 {
			t.Fatalf("strip %d produced particles but mirrored none", strip)
		}
	}
	if byStrip[stripNanolathe] != 0 {
		t.Fatalf("strip 6 was mirrored (%d views); the nanolathe spray already has its own presentation path and would draw twice", byStrip[stripNanolathe])
	}
}

// TestSmokeDriftIsRawFixedPoint locks the correction that made the steam stay
// where it was produced: the wind words scale the RAW 16.16 position, not whole
// world units, and the map's authored gravity word lifts the puff by sixteen
// raw units a tick [R-WIND-01][03 R-FX-01 §3].
//
// The old form multiplied by 65536 too much: a wind word of forty moved a puff
// three hundred and twenty world units — twenty cells — every tick.
func TestSmokeDriftIsRawFixedPoint(t *testing.T) {
	s, _ := newStripTestSession(23, 23)
	s.Clock.GlobalTick = 0
	s.appendStripGeothermalSteam([3]numeric.Fixed{numeric.FixedFromInt(100), numeric.FixedFromInt(50), numeric.FixedFromInt(200)})

	const windX, windZ, gravity = int32(40), int32(-24), int32(112)
	wind := &world.Wind{DirX: windX, DirZ: windZ}
	o := &s.strips.strips[stripGeothermalSteam][0]
	before := o.particles[0]
	o.advanceParticles(1, s.CrtRNG(), wind, gravity)
	after := o.particles[0]

	if got := after.x.Raw() - before.x.Raw(); got != int64(windX)*8 {
		t.Fatalf("X drifted %d raw units, want the wind word times eight (%d)", got, int64(windX)*8)
	}
	if got := after.z.Raw() - before.z.Raw(); got != int64(windZ)*8 {
		t.Fatalf("Z drifted %d raw units, want the wind word times eight (%d)", got, int64(windZ)*8)
	}
	if got := after.y.Raw() - before.y.Raw(); got != int64(gravity)*16 {
		t.Fatalf("Y rose %d raw units, want the authored gravity word times sixteen (%d)", got, int64(gravity)*16)
	}
	// A tick of drift must stay sub-world-unit; the whole point of the
	// correction is that a puff does not travel.
	if after.x.Raw()>>16 != before.x.Raw()>>16 {
		t.Fatalf("one tick of wind moved the puff a whole world unit on X")
	}
}

// TestSmokePuffRetiresAtItsOwnLastFrame locks the correction that stopped a
// strip filling with immortal smoke [03 R-STRIP-01 §2].
//
// The producer's one CRT draw is the puff's LAST FRAME —
// `crtRand·(frameCount − 1 − 2)/0x8000 + 2` — and the cursor starts at 0. It
// had been read as a random START frame, which left the puff with no end at
// all: nothing retired it, and strip 9 filled to its 401-record bound with
// smoke that never faded. The staggered last frame is what thins a plume out.
func TestSmokePuffRetiresAtItsOwnLastFrame(t *testing.T) {
	s, _ := newStripTestSession(31, 31)
	// The bound entry's frame count reaches the strips through the composer's
	// seam; `smoke 1` holds twelve frames in the stock bank.
	s.SetEffectEntryFrameCount(func(bank, entry string) (int, bool) {
		if entry != smokePuffEntry {
			return 0, false
		}
		return 12, true
	})
	s.Clock.GlobalTick = 0
	s.appendStripSmokePuffer(9, [3]numeric.Fixed{}, SmokePuffTrail)

	obj := &s.strips.strips[9][0]
	if obj.frameCountBase != 11 {
		t.Fatalf("container frame-count base %d, want the entry's twelve frames less one", obj.frameCountBase)
	}
	p := obj.particles[0]
	if p.frame != 0 {
		t.Fatalf("puff cursor starts at %d, want 0", p.frame)
	}
	// lastFrame = crtRand·(11 − 2)/0x8000 + 2, so it lands in 2..10 — inside
	// the entry, and never at frame 0 or 1.
	if p.lastFrame < 2 || p.lastFrame > 10 {
		t.Fatalf("puff last frame %d, want 2..10 for a twelve-frame entry", p.lastFrame)
	}

	// Sweeping long enough must retire it. Twelve frames at a hold of at most
	// seven is well under 200 ticks.
	for tick := uint32(1); tick <= 400; tick++ {
		s.Clock.GlobalTick = tick
		s.phaseObjectSweeps(tick)
		if len(s.strips.strips[9]) == 0 {
			return
		}
	}
	live := 0
	if len(s.strips.strips[9]) > 0 {
		live = len(s.strips.strips[9][0].particles)
	}
	t.Fatalf("after 400 ticks the smoke container is still alive with %d puffs; a puff must die when its cursor passes its last frame", live)
}

// TestSmokeProducersCarryTheirOwnParameters locks the per-producer table of
// [06 R-WFX-01 §5]. Every weapon-side smoke site used to pass the same
// `(lifetime 0, hold 7)` pair, because the producer's signature had nowhere to
// put the other three arguments; the four producers are supposed to look
// different from each other, and this is what makes them.
func TestSmokeProducersCarryTheirOwnParameters(t *testing.T) {
	// `startsmoke` at the muzzle: four frames at hold 30 — a slow puff.
	if SmokePuffStart.FrameCap != 3 || SmokePuffStart.FrameHold != 30 || SmokePuffStart.SpawnInterval != 1 || SmokePuffStart.Lifetime != 0 {
		t.Fatalf("startsmoke init %+v, want frameCap 3, interval 1, hold 30, lifetime 0", SmokePuffStart)
	}
	// The land dust of an above-sea explosion: one at spawn and one every
	// seven ticks while the fifteen-tick window stands — three particles.
	if SmokePuffLandDust.SpawnInterval != 7 || SmokePuffLandDust.Lifetime != 15 || SmokePuffLandDust.FrameCap != 0 {
		t.Fatalf("land dust init %+v, want interval 7, lifetime 15, no frame cap", SmokePuffLandDust)
	}
	// The trail puff, the timer-expiry puff and `endsmoke` share one row.
	if SmokePuffTrail.FrameCap != 0 || SmokePuffTrail.SpawnInterval != 1 || SmokePuffTrail.FrameHold != 0 || SmokePuffTrail.Lifetime != 0 {
		t.Fatalf("trail init %+v, want frameCap 0, interval 1, hold 0, lifetime 0", SmokePuffTrail)
	}
	// The black emit-sfx puff is the trail's shape on the second entry.
	if SmokePuffBlack.Selector != 1 {
		t.Fatalf("black puff selector %d, want the second smoke entry", SmokePuffBlack.Selector)
	}

	s, _ := newStripTestSession(41, 41)
	s.SetEffectEntryFrameCount(func(bank, entry string) (int, bool) {
		if entry != smokePuffEntry {
			return 0, false
		}
		return 12, true
	})
	s.Clock.GlobalTick = 100

	// The frame-limit field is min(frameCount − 1, frameCap) when the cap is
	// nonzero, so `startsmoke` bounds its puffs to four frames where the trail
	// puff plays all twelve.
	s.appendStripSmokePuffer(9, [3]numeric.Fixed{}, SmokePuffStart)
	if got := s.strips.strips[9][0].frameCountBase; got != 3 {
		t.Fatalf("startsmoke frame limit %d, want the cap of 3", got)
	}
	if got := s.strips.strips[9][0].frameDelayParam; got != 30 {
		t.Fatalf("startsmoke hold %d, want 30", got)
	}
	s.appendStripSmokePuffer(9, [3]numeric.Fixed{}, SmokePuffTrail)
	if got := s.strips.strips[9][1].frameCountBase; got != 11 {
		t.Fatalf("trail frame limit %d, want the entry's twelve frames less one", got)
	}

	// The land dust really does spawn three particles: one at construction and
	// one every seven ticks while its fifteen-tick window stands.
	s2, _ := newStripTestSession(42, 42)
	s2.SetEffectEntryFrameCount(func(bank, entry string) (int, bool) { return 12, true })
	s2.Clock.GlobalTick = 0
	s2.appendStripSmokePuffer(9, [3]numeric.Fixed{}, SmokePuffLandDust)
	spawns, last := 1, 1
	for tick := uint32(1); tick <= 40; tick++ {
		s2.Clock.GlobalTick = tick
		s2.phaseObjectSweeps(tick)
		cur := s2.strips.strips[9]
		if len(cur) == 0 {
			break
		}
		if n := len(cur[0].particles); n > last {
			spawns += n - last
		}
		last = len(cur[0].particles)
	}
	if spawns != 3 {
		t.Fatalf("land dust produced %d particles over its window, want 3", spawns)
	}
}
