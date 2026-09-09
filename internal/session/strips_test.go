package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
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
// It is deliberately NOT the smoke family: that class overrides the removal
// verdict with a constant false, so a smoke container is never a compaction
// subject [03 R-FX-01 §3 addendum]. The flame family's verdict is the ordinary
// "the sub-record list is empty".
func terminalOrLiveContainer(marker int64) stripObject {
	o := stripObject{family: stripFamilyFlame, windowEnd: 0xFFFFFFFF}
	if marker < 0 {
		return o // empty list: terminal
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

	// Five containers, markers 0..4; odd markers are terminal (empty list),
	// even markers carry one live particle.
	for i := 0; i < 5; i++ {
		var o stripObject
		if i%2 == 0 {
			o = terminalOrLiveContainer(int64(i))
		} else {
			o = terminalOrLiveContainer(-1)
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
		s.strips.append(2, terminalOrLiveContainer(int64(i)))
	}
	if n := len(s.strips.strips[2]); n != 401 {
		t.Fatalf("count = %d at the cap boundary, want 401", n)
	}
	if first := s.strips.strips[2][0].src[0].Int(); first != 0 {
		t.Fatalf("oldest object evicted too early: first marker %d, want 0", first)
	}

	// Pre-insert count 401 exceeds 400: the marker-0 object dies first.
	s.strips.append(2, terminalOrLiveContainer(401))
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
	s.Snapshot = frame.NewBuffer()
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
	s.publishSnapshot(104)
	if got := len(s.Snapshot.Current().Strips); got != 0 {
		t.Fatalf("expired nano particles remained in the committed frame: %d views", got)
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
	s.appendStripSprinkle(2, point, point, 16, 1)                                       // emit-sfx thrust pair (type 2)
	s.appendStripSprinkle(2, point, point, 8, 1)                                        // emit-sfx thrust pair (type 3)
	s.appendStripSprinkle(7, point, point, 8, 0)                                        // emit-sfx sub-bubbles (0x103)

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

// TestSmokeFamilySweepDrawsAndDrift locks the smoke family's committed
// behavior: one CRT draw per spawned puff (its last frame), one draw per
// animation-frame advance with the next delay drawn as half to full of the
// authored delay [R-STRIP-01 §3], and the published wind words applied ×8 to
// the X and Z terms each tick [R-WIND-01]. The container is assembled
// directly so the gate, animation, and drift mechanics are pinned
// independently of any producer site's parameters.
func TestSmokeFamilySweepDrawsAndDrift(t *testing.T) {
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
	// The interval is wide enough that the eight ticks under test hold no
	// second spawn: the gate is perpetual for this family
	// [03 R-FX-01 §3 addendum], so isolating the animation draw means spacing
	// the spawns, not waiting for a window to close.
	o := stripObject{family: stripFamilySmoke, src: pos, frameDelayParam: 8, windowEnd: 50, nextSpawn: 50, spawnInterval: 100}
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
	if wantX, wantZ := int64(100)<<16, int64(200)<<16; p.x.Raw() != wantX+3*8*8 || p.z.Raw() != wantZ+(-2)*8*8 {
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
	// The trail producer passes lifetime 0, so the init stores the creation
	// tick itself as the deadline. That is what makes it a one-shot: the gate
	// is armed for tick 10 but the window closed at tick 9, so the second
	// spawn never fires [03 R-FX-01 §3 addendum].
	//
	// Corrected 2026-09-01: this briefly asserted the opposite — no deadline
	// and a puff every tick forever — after the vent's virtuals were applied
	// to this class. The two assertions below pull against each other on
	// purpose: the gate IS armed, and it still must not fire.
	if strip[0].windowEnd != 9 {
		t.Fatalf("stored deadline %d, want the creation tick 9 for a lifetime-0 producer", strip[0].windowEnd)
	}
	if strip[0].nextSpawn != 10 || strip[0].spawnInterval != 1 {
		t.Fatalf("gate armed at %d interval %d, want tick 10 and interval 1", strip[0].nextSpawn, strip[0].spawnInterval)
	}
	s.Clock.GlobalTick = 10
	s.phaseObjectSweeps(10)
	if n := len(s.strips.strips[9][0].particles); n != 1 {
		t.Fatalf("the trail emitter holds %d puffs one tick on, want its single one; the window closed at creation", n)
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
	s.appendStripSprinkle(2, pos, pos, 16, 1)
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
	s.appendStripSprinkle(7, pos, pos, 8, 0)
	if p := s.strips.strips[7][0].particles[0]; p.color != 0x67 {
		t.Fatalf("colorSel=0 puff color %#x, want 0x67", p.color)
	}
}

// TestPuffContainerVerdictsDivergeByClass locks the one difference that makes
// the two smoke-puff classes two classes [03 R-FX-01 §3 addendum].
//
// It asserts the pair that pulls against itself, because either half alone
// passes on a broken build:
//
//   - the strips-5/9 puffer's verdict keeps BOTH terms — empty list AND the
//     stored deadline passed — so a weapon-side container really does die;
//   - the vent's class returns a constant false, so an identically-shaped
//     container on strip 4 survives the same sweep.
//
// The history is worth keeping: this file first asserted only the first half,
// then (wrongly) only the second. Asserting only the second gave every muzzle
// and impact site an immortal emitter; asserting only the first stopped a
// vent's plume after five seconds, against a retail capture of one still
// steaming eighteen seconds in. Both readings were right about their own class.
func TestPuffContainerVerdictsDivergeByClass(t *testing.T) {
	s, _ := newStripTestSession(18, 18)

	s.strips.append(5, stripObject{family: stripFamilySmoke, windowEnd: 200}) // empty, deadline ahead
	s.strips.append(5, stripObject{family: stripFamilySmoke, windowEnd: 5})   // empty, deadline long passed
	busy := stripObject{family: stripFamilySmoke, windowEnd: 5}
	busy.particles = []stripParticle{{expiry: 0xFFFFFFFF}}
	s.strips.append(5, busy)

	// The vent's class, given the very same closed window and empty list.
	s.strips.append(4, stripObject{family: stripFamilyVentSteam, windowEnd: 5})

	s.phaseObjectSweeps(100)

	strip := s.strips.strips[5]
	if len(strip) != 2 {
		t.Fatalf("%d puffer containers survived, want 2: the one whose window is still open and the one still holding a puff", len(strip))
	}
	if strip[0].windowEnd != 200 || len(strip[1].particles) != 1 {
		t.Fatal("the sweep removed the wrong container, or reordered the survivors")
	}
	if n := len(s.strips.strips[4]); n != 1 {
		t.Fatalf("%d vent containers survived, want 1; the vent's verdict is a constant false and its window is not a lifetime", n)
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
	// The step is `((B − A) · trunc(65536 / lifetime)) >> 16`, NOT the exact
	// quotient: the truncated reciprocal (10922/65536 at lifetime 6) lands the
	// segment slightly short of its target [03 R-FX-01 §3]. The assertion is a
	// pair — the exact value, and the relationship that says why it is not the
	// round 2 world units this test used to demand.
	wantStep := numeric.Fixed((numeric.FixedFromInt(12).Raw() * (65536 / 6)) >> 16)
	if p.vx != wantStep {
		t.Fatalf("trail velocity %d raw, want %d (truncated reciprocal over the 6-tick flight)", p.vx.Raw(), wantStep.Raw())
	}
	if p.vx >= numeric.FixedFromInt(2) {
		t.Fatalf("trail velocity %d raw reached the exact quotient; the truncated reciprocal must land short", p.vx.Raw())
	}
	if p.expiry != 36 {
		t.Fatalf("trail expiry %d, want the window end 36", p.expiry)
	}
}

// TestGeothermalSteamCadence locks the strip-4 producer of [05 R-ECO-02 §3]:
// spawn interval 5, frame hold 7, one puff from the constructor, and a cadence
// that does not stop.
//
// The three literals were recorded as "which of the family's init parameters
// each binds to" Unknown until 2026-08-31, and the family was recorded as the
// flame class rather than the smoke-puff class. The third literal was then read
// as a container lifetime, which it is not [03 R-FX-01 §3 addendum]. The
// cadence is the whole visible behaviour of a geothermal vent on a map like
// Great Divide, whose vent definition is a one-pixel placement marker with no
// artwork of its own.
func TestGeothermalSteamCadence(t *testing.T) {
	s, _ := newStripTestSession(21, 21)
	pos := [3]numeric.Fixed{numeric.FixedFromInt(104), numeric.FixedFromInt(40), numeric.FixedFromInt(152)}

	s.Clock.GlobalTick = 0
	s.appendStripGeothermalSteam(pos)

	objs := s.strips.strips[stripGeothermalSteam]
	if len(objs) != 1 {
		t.Fatalf("strip 4 holds %d objects after the producer, want 1", len(objs))
	}
	o := objs[0]
	if o.family != stripFamilyVentSteam {
		t.Fatalf("the geothermal producer built family %d; it is the vent's own class — not the flame class it was first recorded as, and not the strips-5/9 puffer it was briefly merged into", o.family)
	}
	if o.spawnInterval != geothermalSteamInterval || o.windowEnd != geothermalSteamCapacityHorizon {
		t.Fatalf("interval %d horizon %d, want interval 5 and a 150-tick capacity horizon", o.spawnInterval, o.windowEnd)
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

	// The gate must fire on the fifth tick and every fifth tick after it, past
	// the capacity horizon and for as long as the sweep runs. Puffs expire on
	// their own animation clock, so the count is read as "spawns seen", tracked
	// by watching it rise.
	spawns := 1
	last := 1
	for tick := uint32(1); tick <= 400; tick++ {
		s.Clock.GlobalTick = tick
		s.phaseObjectSweeps(tick)
		cur := s.strips.strips[stripGeothermalSteam]
		if len(cur) == 0 {
			t.Fatalf("the emitter was destroyed at tick %d; the vent class's removal verdict is a constant false [03 R-FX-01 §3 addendum]", tick)
		}
		if n := len(cur[0].particles); n > last {
			spawns += n - last
		}
		last = len(cur[0].particles)
	}

	// Next-spawn ticks 5, 10, ... 400: eighty gate spawns plus the
	// constructor's own. The horizon at 150 must not have stopped it.
	const wantSpawns = 1 + 400/int(geothermalSteamInterval)
	if spawns != wantSpawns {
		t.Fatalf("the emitter produced %d puffs over 400 ticks, want %d (one at construction plus one every %d ticks); a plume that stops at the %d-tick horizon is the corrected defect",
			spawns, wantSpawns, geothermalSteamInterval, geothermalSteamCapacityHorizon)
	}
}

// TestEveryStripFamilyMirrorsItsOwnDrawForm locks the per-sub-record draw of
// [03 R-STRIP-01 §2]: each mirrored particle carries either the GAF entry its
// family blits or the colour of the two-by-two rectangle its family fills, and
// never both.
func TestEveryStripFamilyMirrorsItsOwnDrawForm(t *testing.T) {
	s, _ := newStripTestSession(22, 22)
	s.Clock.GlobalTick = 0
	s.appendStripGeothermalSteam([3]numeric.Fixed{numeric.FixedFromInt(104), 0, numeric.FixedFromInt(152)})
	s.appendStripSmokePuffer(9, [3]numeric.Fixed{}, SmokePuffTrail)
	s.appendStripSprinkle(2, [3]numeric.Fixed{}, [3]numeric.Fixed{}, 8, 0)
	s.appendStripSprinkle(7, [3]numeric.Fixed{}, [3]numeric.Fixed{}, 16, 1)
	s.appendStripNanoEmitter(
		[3]numeric.Fixed{0, 0, 0},
		[3]numeric.Fixed{numeric.FixedFromInt(40), 0, numeric.FixedFromInt(40)},
	)

	views := s.appendStripViews(nil)
	if len(views) == 0 {
		t.Fatal("no strip particle reached the committed frame")
	}
	byStrip := map[int8]int{}
	last := int8(-1)
	for _, v := range views {
		// The committed order is the composer's walk order: strips ascending
		// [03 §1]. The client finds one barrier's run by a boundary search and
		// would silently miss records published out of order.
		if v.Strip < last {
			t.Fatalf("strip %d was published after strip %d; the committed order is strips ascending [03 §1]", v.Strip, last)
		}
		last = v.Strip
		byStrip[v.Strip]++
		if v.Entry != "" && v.Fill != 0 {
			t.Fatalf("strip %d view carries both a GAF entry (%q) and a fill colour (%#x); a family does one or the other", v.Strip, v.Entry, v.Fill)
		}
		if v.Entry == "" && v.Fill == 0 {
			t.Fatalf("strip %d view carries neither an entry nor a fill colour, so nothing can draw it", v.Strip)
		}
		if v.Entry != "" && v.Bank == "" {
			t.Fatalf("strip %d view names entry %q with no bank; the identity is a pair [06 R-WFX-01 §1]", v.Strip, v.Entry)
		}
		switch v.Strip {
		case 4, 9:
			if v.Entry != smokePuffEntry {
				t.Fatalf("strip %d smoke view names %q, want the smoke-puff entry", v.Strip, v.Entry)
			}
			want := frame.StripFamilySmokePuff
			if v.Strip == 4 {
				want = frame.StripFamilyVentSteam
			}
			if v.Family != want {
				t.Fatalf("strip %d smoke view carries family %d, want %d; the family selects the draw [03 R-FX-01 §3]", v.Strip, v.Family, want)
			}
		case 2, 7:
			// The sprinkle pair is 0x61/0x67 [R-STRIP-01 §1 strips 2/7].
			if v.Fill != 0x61 && v.Fill != 0x67 {
				t.Fatalf("strip %d sprinkle fill %#x is off the authored pair", v.Strip, v.Fill)
			}
			if v.Family != frame.StripFamilySprinkle {
				t.Fatalf("strip %d sprinkle view carries family %d", v.Strip, v.Family)
			}
		case 6:
			if v.Family != frame.StripFamilyNano {
				t.Fatalf("strip 6 view carries family %d, want nanolathe", v.Family)
			}
			if v.Fill < 0xa1 || v.Fill > 0xa7 {
				t.Fatalf("strip 6 nano fill %#x is outside the authored ramp", v.Fill)
			}
		default:
			t.Fatalf("strip %d was mirrored unexpectedly", v.Strip)
		}
	}
	for _, strip := range []int8{2, 4, 6, 7, 9} {
		if byStrip[strip] == 0 {
			t.Fatalf("strip %d produced particles but mirrored none", strip)
		}
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
	o.advanceParticles(1, s.CrtRNG(), wind, gravity, nil)
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

	// Sweeping long enough must retire that FIRST puff. Twelve frames at a hold
	// of at most seven is well under 200 ticks. The container itself outlives
	// it — the class's removal verdict is a constant false
	// [03 R-FX-01 §3 addendum] — so the assertion is on the sub-record, not on
	// the container.
	for tick := uint32(1); tick <= 400; tick++ {
		s.Clock.GlobalTick = tick
		s.phaseObjectSweeps(tick)
		obj = &s.strips.strips[9][0]
		gone := true
		for i := range obj.particles {
			if obj.particles[i].lastFrame == p.lastFrame && obj.particles[i].frame < p.lastFrame {
				gone = false
			}
		}
		if gone {
			return
		}
	}
	t.Fatalf("after 400 ticks the constructor's puff has still not reached its last frame %d", p.lastFrame)
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
	// The land dust of an above-sea explosion. The third literal is the
	// producer's stored deadline, and for THIS class it closes the gate: only
	// the vent's class ignores it [03 R-FX-01 §3 addendum].
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

	// The land dust's fifteen-tick window bounds its cadence: one puff at
	// construction, then the gate fires at tick 7 and tick 14 and refuses tick
	// 21, because by then the next-spawn tick has passed the deadline. Three
	// particles, and then a container that retires once they have.
	//
	// Corrected 2026-09-01: this briefly expected six, on the reading that the
	// deadline never closes the gate. That is true only of the vent's class.
	s2, _ := newStripTestSession(42, 42)
	s2.SetEffectEntryFrameCount(func(bank, entry string) (int, bool) { return 12, true })
	s2.Clock.GlobalTick = 0
	s2.appendStripSmokePuffer(9, [3]numeric.Fixed{}, SmokePuffLandDust)
	spawns, last, gone := 1, 1, uint32(0)
	for tick := uint32(1); tick <= 400; tick++ {
		s2.Clock.GlobalTick = tick
		s2.phaseObjectSweeps(tick)
		cur := s2.strips.strips[9]
		if len(cur) == 0 {
			gone = tick
			break
		}
		if n := len(cur[0].particles); n > last {
			spawns += n - last
		}
		last = len(cur[0].particles)
	}
	if spawns != 3 {
		t.Fatalf("land dust produced %d particles, want 3 (one at construction plus the gate at ticks 7 and 14)", spawns)
	}
	// The other half of the pair: the container must actually go, or an
	// explosion leaves a permanent emitter behind.
	if gone == 0 {
		t.Fatal("the land-dust container was still alive 400 ticks on; its window closed at tick 15")
	}
}

// TestSteamCreatedBeforeTheSeamThinsOutButKeepsEmitting locks both halves of
// the geothermal plume against a retail capture.
//
// The PUFFS must retire. The ordering that stopped them from retiring is
// unavoidable: the steam producer of [05 R-ECO-02 §3] runs from the feature
// stamp, so a vent's container is built while the map's terrain features are
// populated — inside the authoritative session constructor, long before any
// composer exists to fill the frame-count seam. The container resolved a count
// of zero, its puffs got no last frame, and a smoke puff's ONLY retirement is
// its cursor reaching that frame [06 R-WFX-01 §5]. The seam now finishes what
// it finds, taking no draw to do it: each puff retained its own spawn draw, so
// the value finished is the one retail computes from the same draw.
//
// The CONTAINER must not retire, and neither must its spawning. The smoke class
// overrides the removal verdict with a constant false and its spawn predicate
// is a bare `nextSpawn <= tick` with no window term
// [03 R-FX-01 §3 addendum], which is why a retail vent is still steaming
// eighteen seconds into a capture. Asserting only that the puffs thin out would
// pass on the build that stopped the whole plume after five seconds.
func TestSteamCreatedBeforeTheSeamThinsOutButKeepsEmitting(t *testing.T) {
	s, _ := newStripTestSession(57, 57)
	s.Clock.GlobalTick = 0

	// The container is built with no resolver installed, exactly as a vent's is.
	s.appendStripGeothermalSteam([3]numeric.Fixed{})
	obj := &s.strips.strips[stripGeothermalSteam][0]
	if obj.frameCountBase != 0 {
		t.Fatalf("fixture is not exercising the defect: the container resolved %d without a seam", obj.frameCountBase)
	}
	if len(obj.particles) != 1 || obj.particles[0].lastFrame != 0 {
		t.Fatalf("the constructor's own puff is %+v, want one puff with no last frame yet", obj.particles)
	}
	if !obj.particles[0].lastFrameDrawn {
		t.Fatal("the producer spends its draw whether or not the entry resolved; it must be retained")
	}

	// Filling the seam finishes the container and the puff it already spawned.
	s.SetEffectEntryFrameCount(func(bank, entry string) (int, bool) {
		if entry != smokePuffEntry {
			return 0, false
		}
		return 12, true
	})
	obj = &s.strips.strips[stripGeothermalSteam][0]
	if obj.frameCountBase != 11 {
		t.Fatalf("container frame-count base %d after the seam was filled, want 11", obj.frameCountBase)
	}
	if lf := obj.particles[0].lastFrame; lf < 2 || lf > 10 {
		t.Fatalf("the already-spawned puff's last frame is %d, want 2..10 for a twelve-frame entry", lf)
	}

	// Individual puffs must retire, so the plume settles at a handful rather
	// than growing without bound, and the container must still be emitting
	// long after the 150-tick capacity horizon has passed.
	peak := 0
	for tick := uint32(1); tick <= 1800; tick++ {
		s.Clock.GlobalTick = tick
		s.phaseObjectSweeps(tick)
		if len(s.strips.strips[stripGeothermalSteam]) == 0 {
			t.Fatalf("the vent's container was removed at tick %d; retail never removes one [03 R-FX-01 §3 addendum]", tick)
		}
		if n := len(s.strips.strips[stripGeothermalSteam][0].particles); n > peak {
			peak = n
		}
	}
	live := len(s.strips.strips[stripGeothermalSteam][0].particles)
	if live == 0 {
		t.Fatal("the vent stopped emitting; retail's spawn predicate has no window term [03 R-FX-01 §3 addendum]")
	}
	// One puff every five ticks over 1800 ticks is 360 spawns. A plume that
	// retires nothing would hold every one of them.
	if peak > 40 {
		t.Fatalf("the plume peaked at %d puffs; puffs are not retiring at their last frame [06 R-WFX-01 §5]", peak)
	}
}

// TestWeaponSmokeDrainsAfterTheShootingStops is the test the smoke-container
// regression got past, written from the play test that found it.
//
// A firefight creates weapon-side smoke containers at a high rate: two per shot
// at the muzzle and the impact, plus one per trail tick along each projectile's
// flight. Every one of them carries lifetime 0, so each must spawn its single
// puff and go. When they stopped going, strip 9 saturated at its 401-object
// bound within twenty seconds of contact and held roughly fifteen thousand live
// puffs for the rest of the battle — a carpet of smoke over every place a shot
// had ever landed, and about thirteen milliseconds a frame of blitting on a
// thirty-three millisecond budget.
//
// The assertion is the shape of the defect rather than a puff census: after the
// shooting stops, the strip must drain to nothing.
func TestWeaponSmokeDrainsAfterTheShootingStops(t *testing.T) {
	s, _ := newStripTestSession(23, 23)
	s.SetEffectEntryFrameCount(func(bank, entry string) (int, bool) { return 12, true })
	pos := [3]numeric.Fixed{numeric.FixedFromInt(64), numeric.FixedFromInt(0), numeric.FixedFromInt(64)}

	peak := 0
	for tick := uint32(1); tick <= 600; tick++ {
		s.Clock.GlobalTick = tick
		if tick <= 300 && tick%5 == 0 {
			// Ten shooters, one shot every five ticks: muzzle, trail, impact.
			for i := 0; i < 10; i++ {
				s.appendStripSmokePuffer(9, pos, SmokePuffStart)
				s.appendStripSmokePuffer(9, pos, SmokePuffTrail)
				s.appendStripSmokePuffer(9, pos, SmokePuffTrail)
			}
		}
		s.phaseObjectSweeps(tick)
		if n := len(s.strips.strips[9]); n > peak {
			peak = n
		}
	}

	// While the shooting lasts the strip must stay clear of the eviction bound.
	// Eighteen hundred containers are created here; the live count is bounded
	// by how long ONE puff's animation runs, not by how many shots have been
	// fired, so it settles near six a tick times a puff's few dozen ticks. The
	// regression pegged it at exactly 401 and left it there.
	if peak >= 401 {
		t.Fatalf("strip 9 peaked at %d containers during the firefight; it reached the eviction bound, so containers are not retiring", peak)
	}
	// And once it stops, nothing is left behind.
	if n := len(s.strips.strips[9]); n != 0 {
		t.Fatalf("%d smoke containers survived 300 ticks after the last shot; every weapon-side producer passes lifetime 0 and must leave nothing", n)
	}
}

// TestPuffFamiliesRiseAtDifferentRates locks the second difference between the
// two puff classes [03 R-FX-01 §3 addendum]: both add the published wind words
// multiplied by 8 to the raw X and Z words, but the strips-5/9 puffer scales
// the map's authored gravity word by 4 where the vent scales it by 16. A vent's
// steam climbs four times as fast as a shot's smoke.
//
// The single scale of 16 was read off the vent's update on the assumption that
// there was one class; it made every weapon-side puff rise like steam.
func TestPuffFamiliesRiseAtDifferentRates(t *testing.T) {
	const gravity = 100

	rise := func(family stripFamily, strip int) int64 {
		s, _ := newStripTestSession(24, 24)
		o := stripObject{family: family, windowEnd: 0xFFFFFFFF}
		o.particles = []stripParticle{{lastFrame: 0, frameDelay: 0}}
		s.strips.append(strip, o)
		before := s.strips.strips[strip][0].particles[0].y
		s.strips.sweepStrip(strip, 1, s.CrtRNG(), nil, gravity, nil)
		return s.strips.strips[strip][0].particles[0].y.Raw() - before.Raw()
	}

	if got := rise(stripFamilySmoke, 9); got != gravity*4 {
		t.Fatalf("a weapon-side puff rose by %d raw words a tick, want the gravity word times 4", got)
	}
	if got := rise(stripFamilyVentSteam, 4); got != gravity*16 {
		t.Fatalf("a vent puff rose by %d raw words a tick, want the gravity word times 16", got)
	}
}

// TestStripViewsArePublishedEveryTickAndAreByteStable locks the publication
// boundary itself [I6]: the committed strip channel is rebuilt from the live
// table at every publication, and two identical sessions publish identical
// bytes at every tick.
//
// Both halves matter. A channel published only when it changes would leave a
// stale frame's records on screen after the sweep retired them; a channel that
// differed between two identical runs would mean the walk order or the mirror
// itself had picked up a nondeterministic source [I1].
func TestStripViewsArePublishedEveryTickAndAreByteStable(t *testing.T) {
	const ticks = 12

	run := func() []string {
		s, _ := newStripTestSession(31, 31)
		s.Snapshot = frame.NewBuffer()
		s.Clock.GlobalTick = 0
		// One producer of each mirrored family, on four different strips.
		s.appendStripGeothermalSteam([3]numeric.Fixed{numeric.FixedFromInt(104), 0, numeric.FixedFromInt(152)})
		s.appendStripSmokePuffer(9, [3]numeric.Fixed{numeric.FixedFromInt(40), 0, numeric.FixedFromInt(40)}, SmokePuffLandDust)
		s.appendStripSprinkle(2, [3]numeric.Fixed{numeric.FixedFromInt(8), 0, numeric.FixedFromInt(8)}, [3]numeric.Fixed{numeric.FixedFromInt(8), 0, numeric.FixedFromInt(8)}, 8, 0)
		s.appendStripSprinkle(7, [3]numeric.Fixed{numeric.FixedFromInt(9), 0, numeric.FixedFromInt(9)}, [3]numeric.Fixed{numeric.FixedFromInt(9), 0, numeric.FixedFromInt(9)}, 16, 1)

		out := make([]string, 0, ticks)
		for tick := uint32(1); tick <= ticks; tick++ {
			s.Clock.GlobalTick = tick
			s.strips.sweep(tick, s)
			s.publishSnapshot(tick)
			published := s.Snapshot.Current()
			if published == nil {
				t.Fatalf("tick %d published no frame", tick)
			}
			if len(published.Strips) == 0 {
				t.Fatalf("tick %d published no strip records while the table held live objects", tick)
			}
			line := ""
			for _, v := range published.Strips {
				line += fmt.Sprintf("%d/%d/%s/%s/%d/%02x/%d,%d,%d|",
					v.Strip, v.Family, v.Bank, v.Entry, v.Frame, v.Fill,
					v.X.Raw(), v.Y.Raw(), v.Z.Raw())
			}
			out = append(out, line)
		}
		return out
	}

	first, second := run(), run()
	if len(first) != ticks || len(second) != ticks {
		t.Fatalf("runs published %d and %d ticks", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("tick %d differs between two identical runs:\n%s\n%s", i+1, first[i], second[i])
		}
	}
}

// TestFlameSegmentStartFrameIsScaled locks the strip-5 flame segment's start
// frame [03 R-FX-02 §2][03 R-FX-02 §6]: `crtRand × (frameCount − 1) / 0x8000`,
// an index into `flamestream`, in 0 … frameCount − 2.
//
// The spawn used to store the raw CRT draw here, on the belief that the
// mapping from a drawn word to a GAF frame index was untraced. There is no
// mapping step: the word IS the index, and a raw draw is a five-digit frame
// number in a twenty-frame entry — a cursor no wrap can ever bring back into
// range.
func TestFlameSegmentStartFrameIsScaled(t *testing.T) {
	const frameCountBase = 19 // stock `flamestream` holds twenty frames

	_, crt := newStripTestSession(30, 30)
	ref := rng.NewCRT(30)
	draw := ref.Rand()

	o := &stripObject{
		family:         stripFamilyFlame,
		frameCountBase: frameCountBase,
		dst:            [3]numeric.Fixed{numeric.FixedFromInt(100), 0, 0},
	}
	o.spawnOnce(7, crt)

	p := o.particles[0]
	want := int32(int64(draw) * frameCountBase / 0x8000)
	if p.frame != want {
		t.Fatalf("start frame %d, want %d (crtRand × (frameCount − 1) / 0x8000)", p.frame, want)
	}
	if p.frame == draw {
		t.Fatalf("start frame is the raw CRT draw %d; it must be scaled into the entry", draw)
	}
	if p.frame < 0 || p.frame > frameCountBase-1 {
		t.Fatalf("start frame %d is outside 0…frameCount−2", p.frame)
	}
}

// TestFlameSegmentWrapsBelowLastFrame locks the two flame families' frame wrap
// [03 R-FX-02 §2][03 R-FX-01 §3]: `frame = (frame + 1) mod (frameCount − 1)`,
// every tick for the strip-5 segment, so the entry's LAST frame is never shown
// and the cursor returns to where it started after frameCount − 1 ticks.
func TestFlameSegmentWrapsBelowLastFrame(t *testing.T) {
	const frameCountBase = 19

	o := &stripObject{
		family:         stripFamilyFlame,
		frameCountBase: frameCountBase,
		particles:      []stripParticle{{frame: frameCountBase - 1, expiry: 0xFFFFFFFF}},
	}
	start := o.particles[0].frame
	for tick := uint32(1); tick <= frameCountBase; tick++ {
		o.advanceParticles(tick, nil, nil, 0, nil)
		if got := o.particles[0].frame; got < 0 || got >= frameCountBase {
			t.Fatalf("tick %d left the cursor at %d; the last frame (%d) is never shown", tick, got, frameCountBase)
		}
	}
	if got := o.particles[0].frame; got != start {
		t.Fatalf("cursor at %d after a full %d-tick cycle, want the start %d", got, frameCountBase, start)
	}
}

// TestFlameSegmentTravelAndLife locks the strip-5 segment's span arithmetic
// [03 R-FX-02 §2]: `segLife = floor(spanUnits / 5)` ticks and `step =
// (B − A) / segLife` per axis, so a segment crosses its whole span at five
// world units per tick.
func TestFlameSegmentTravelAndLife(t *testing.T) {
	_, crt := newStripTestSession(33, 33)

	o := &stripObject{
		family: stripFamilyFlame,
		dst:    [3]numeric.Fixed{numeric.FixedFromInt(100), 0, 0},
	}
	o.spawnOnce(40, crt)

	p := o.particles[0]
	if p.expiry != 40+20 {
		t.Fatalf("segment expiry %d, want %d (a 100-unit span / 5 = 20 ticks)", p.expiry, 40+20)
	}
	if p.vx != numeric.FixedFromInt(5) {
		t.Fatalf("segment step %d raw, want five world units a tick", p.vx.Raw())
	}
	// The relationship that makes the two halves one rule: step × segLife is
	// the span the producer asked for.
	if got := p.vx.Raw() * 20; got != numeric.FixedFromInt(100).Raw() {
		t.Fatalf("step × segLife = %d raw, want the 100-unit span", got)
	}
}

// TestFlameSegmentDegenerateTeleportIsBoundedNotImmortal locks the TODO(T23)
// placeholder in spawnOnce's stripFamilyFlame case: retail's segment life is
// `floor(spanUnits/5)`, and a span under five world units — the from==to
// teleport here is the limit of that, span zero — makes retail divide by
// zero [03 R-FX-02 §2 "flame segment travel law"]. There is no retail
// behavior to clone, so Nanolathe substitutes the family's one-tick minimum
// life instead of leaving the segment's expiry at 0 (which expireParticles
// reads as no deadline at all).
//
// The one-tick life bounds the object, but it does not reach the family's
// four-segments-per-container cadence: the container's OWN removal verdict
// is already established as "the segment list is empty", with no window
// term [R-STRIP-01 §2][03 R-FX-02 §2 "container verdict"], and a one-tick
// segment expires long before the next scheduled lay ten ticks later. That
// is not a new defect this placeholder introduces — the same gap empties an
// ordinary (non-degenerate) container whose real span comes in under fifty
// world units, so its segLife (1..9) is also short of the ten-tick spawn
// interval; a from==to teleport is simply the most extreme case of it. So
// the degenerate container here lays and draws exactly its one segment, and
// is destroyed promptly afterward rather than sitting immortal — "bounded
// and drawn-once", not "reaches its fourth segment".
func TestFlameSegmentDegenerateTeleportIsBoundedNotImmortal(t *testing.T) {
	s, crt := newStripTestSession(35, 35)
	s.Clock.GlobalTick = 0

	point := [3]numeric.Fixed{numeric.FixedFromInt(50), numeric.FixedFromInt(10), numeric.FixedFromInt(-4)}
	o := stripObject{
		family:        stripFamilyFlame,
		src:           point,
		dst:           point, // from == to: the degenerate teleport
		windowEnd:     uint32(flameContainerLifetime),
		spawnInterval: flameSegmentInterval,
		nextSpawn:     uint32(flameSegmentInterval),
	}
	draws0 := crt.Draws()
	o.spawnOnce(0, crt) // the constructor's own first segment
	s.strips.append(stripTeleportFlame, o)

	if got := len(s.strips.strips[stripTeleportFlame][0].particles); got != 1 {
		t.Fatalf("constructor laid %d segments, want exactly 1", got)
	}
	if got := crt.Draws() - draws0; got != 1 {
		t.Fatalf("constructor spent %d CRT draws, want 1 (the same one draw any segment costs) [I4]", got)
	}

	const observeTicks = uint32(flameContainerLifetime) + 5
	destroyedAt := uint32(0)
	for tick := uint32(1); tick <= observeTicks; tick++ {
		s.Clock.GlobalTick = tick
		s.phaseObjectSweeps(tick)
		if len(s.strips.strips[stripTeleportFlame]) == 0 {
			destroyedAt = tick
			break
		}
	}
	if destroyedAt == 0 {
		t.Fatalf("degenerate container is still alive %d ticks after spawn; the T23 placeholder must bound it, not leave it immortal", observeTicks)
	}
	// The one-tick segment expires at tick 1 (removed once tick > 1) and the
	// container's own verdict fires the tick after that: destroyed at tick 3,
	// long before the 10-tick cadence would lay a second segment.
	if destroyedAt != 3 {
		t.Fatalf("degenerate container destroyed at tick %d, want tick 3 (one-tick segment, removed the tick after its expiry passes)", destroyedAt)
	}
	if got := crt.Draws() - draws0; got != 1 {
		t.Fatalf("container's whole life spent %d CRT draws, want 1 — it never reaches a second scheduled lay", got)
	}
}

// TestStripPoolCapIsGlobalAndPrecedesTheStripCap locks [03 R-FX-02 §4]: one
// live-container count across all TEN strips, capped at 1000, tested BEFORE the
// per-strip 401 rule; a producer at the cap drops its object silently, spending
// none of the draws that sit inside the family init.
//
// The ordering is the half that is easy to get backwards: a strip already at
// its own 401 bound must NOT evict its oldest object to make room when the
// shared pool is what is full. Retail's take runs first and returns null, and
// the producer then constructs nothing at all.
func TestStripPoolCapIsGlobalAndPrecedesTheStripCap(t *testing.T) {
	s, crt := newStripTestSession(34, 34)
	s.Clock.GlobalTick = 3

	marker := func(i int) [3]numeric.Fixed {
		return [3]numeric.Fixed{numeric.FixedFromInt(int64(i)), 0, 0}
	}
	// Saturate two smoke strips at their own 401 bound and part-fill a third,
	// which is 1000 live containers across the table.
	n := 0
	for i := 0; i < stripSteadyCap+1; i++ {
		s.appendStripSmokePuffer(9, marker(n), SmokePuffTrail)
		n++
	}
	for i := 0; i < stripSteadyCap+1; i++ {
		s.appendStripSmokePuffer(5, marker(n), SmokePuffTrail)
		n++
	}
	for s.strips.live < stripPoolCapacity {
		s.appendStripSprinkle(2, marker(n), marker(n), 16, 1)
		n++
	}

	if s.strips.live != stripPoolCapacity {
		t.Fatalf("pool holds %d live containers, want the capacity %d", s.strips.live, stripPoolCapacity)
	}
	if got := len(s.strips.strips[9]); got != stripSteadyCap+1 {
		t.Fatalf("strip 9 holds %d objects, want the per-strip bound %d", got, stripSteadyCap+1)
	}

	// A producer aimed at the saturated strip 9 must now drop: no draw, no
	// append, and — the ordering assertion — no eviction of the oldest object,
	// which a 401-first implementation would have performed.
	oldest := s.strips.strips[9][0].src[0]
	draws0 := crt.Draws()
	s.appendStripSmokePuffer(9, marker(n), SmokePuffTrail)
	if got := crt.Draws() - draws0; got != 0 {
		t.Fatalf("a dropped container spent %d draws; the puff's draw lives inside the init the producer never calls", got)
	}
	if got := len(s.strips.strips[9]); got != stripSteadyCap+1 {
		t.Fatalf("strip 9 holds %d objects after a dropped producer, want %d", got, stripSteadyCap+1)
	}
	if s.strips.strips[9][0].src[0] != oldest {
		t.Fatalf("the full pool evicted strip 9's oldest object; the 1000-container test runs BEFORE the 401 rule")
	}
	// A producer aimed at an unsaturated strip is dropped just the same: the
	// count is one pool across all ten strips, not a per-strip budget.
	before7 := len(s.strips.strips[7])
	s.appendStripSprinkle(7, marker(n), marker(n), 8, 0)
	if got := len(s.strips.strips[7]); got != before7 {
		t.Fatalf("strip 7 accepted an object at the global cap (%d → %d)", before7, got)
	}

	// Every destruction path returns its slot. Emptying one strip through the
	// sweep must free exactly that many.
	freed := len(s.strips.strips[9])
	for i := range s.strips.strips[9] {
		s.strips.strips[9][i].particles = nil
		s.strips.strips[9][i].windowEnd = 0
	}
	s.phaseObjectSweeps(100)
	if got := len(s.strips.strips[9]); got != 0 {
		t.Fatalf("strip 9 still holds %d objects after a terminal sweep", got)
	}
	if got := s.strips.live; got != stripPoolCapacity-freed {
		t.Fatalf("pool holds %d live after freeing %d, want %d", got, freed, stripPoolCapacity-freed)
	}
	// Battle exit returns every slot [03 R-FX-02 §4].
	s.strips.release()
	if s.strips.live != 0 {
		t.Fatalf("battle exit left %d slots out", s.strips.live)
	}
}

// TestSprinkleWakeDiesOnLand locks [03 R-WATER-01 §1]: a sprinkle puff survives
// only while its tick deadline holds AND the bilinear terrain height under it
// is strictly below the sea-level byte. The family is a WAKE — it lives on
// water and dies on the shore.
//
// The sense is the whole point: [03 R-FX-01 §3] had the branch inverted, and a
// clone that keeps the inverted rule draws wakes on land and never on water.
// The test asserts both halves against the same terrain, moving only the sea
// level, so a build that keeps neither test nor a reversed one can pass.
func TestSprinkleWakeDiesOnLand(t *testing.T) {
	s, _ := newStripTestSession(35, 35)
	ter := minimalTerrain() // a uniform terrain of height 10
	ter.SeaLevel = 20       // …entirely under water
	s.World = ter
	s.Clock.GlobalTick = 5

	pos := [3]numeric.Fixed{numeric.FixedFromInt(80), numeric.FixedFromInt(0), numeric.FixedFromInt(80)}
	s.appendStripSprinkle(2, pos, pos, 16, 1)

	// Over water the puffs outlive the container's spawn window; their only
	// remaining exit is the spacing×6 deadline, which is 96 ticks away.
	for tick := uint32(5); tick <= 12; tick++ {
		s.phaseObjectSweeps(tick)
	}
	if len(s.strips.strips[2]) != 1 {
		t.Fatalf("the wake container died over water (%d objects)", len(s.strips.strips[2]))
	}
	if got := len(s.strips.strips[2][0].particles); got != 2 {
		t.Fatalf("%d puffs alive over water, want the container's two", got)
	}

	// The shore: the same ground now reads at or above sea level.
	ter.SeaLevel = 5
	live := s.strips.live
	s.phaseObjectSweeps(13)
	if got := len(s.strips.strips[2][0].particles); got != 0 {
		t.Fatalf("%d puffs survived the tick the terrain reached sea level", got)
	}
	// The emptied container is terminal, so the next sweep's verdict removes it
	// and returns its slot.
	s.phaseObjectSweeps(14)
	if got := len(s.strips.strips[2]); got != 0 {
		t.Fatalf("%d wake containers survived their last puff", got)
	}
	if got := s.strips.live; got != live-1 {
		t.Fatalf("pool holds %d live after the sweep removed one container, want %d", got, live-1)
	}
}

// TestSprinkleColourWalksItsRamp locks the sprinkle's animation counter
// [03 R-FX-01 §3][03 R-FX-02 §6]: `phase = (phase + 1) mod spacing` and, on the
// wrap, `colour += dir` over the seven-entry ramp 0x61..0x67 — flag 1 climbing
// from the bottom, flag 0 descending from the top, each wrapping to the other
// end.
func TestSprinkleColourWalksItsRamp(t *testing.T) {
	const spacing = 4

	step := func(colorSel uint8, ticks int) uint8 {
		o := &stripObject{
			family:       stripFamilySprinkle,
			phaseModulus: spacing,
			colorSel:     colorSel,
			particles:    []stripParticle{{color: sprinkleRampTop, expiry: 0xFFFFFFFF}},
		}
		if colorSel != 0 {
			o.particles[0].color = sprinkleRampBottom
		}
		for tick := 1; tick <= ticks; tick++ {
			o.advanceParticles(uint32(tick), nil, nil, 0, nil)
		}
		return o.particles[0].color
	}

	// One step per `spacing` ticks, in opposite directions.
	if got := step(1, spacing); got != sprinkleRampBottom+1 {
		t.Fatalf("flag-1 colour %#x after one wrap, want %#x", got, sprinkleRampBottom+1)
	}
	if got := step(0, spacing); got != sprinkleRampTop-1 {
		t.Fatalf("flag-0 colour %#x after one wrap, want %#x", got, sprinkleRampTop-1)
	}
	// Short of the wrap nothing moves.
	if got := step(1, spacing-1); got != sprinkleRampBottom {
		t.Fatalf("flag-1 colour %#x before the wrap, want %#x", got, sprinkleRampBottom)
	}
	// Six wraps carry each end past the other and back onto the ramp.
	for _, sel := range []uint8{0, 1} {
		for wraps := 1; wraps <= 8; wraps++ {
			got := step(sel, spacing*wraps)
			if got < sprinkleRampBottom || got > sprinkleRampTop {
				t.Fatalf("flag-%d colour %#x after %d wraps left the ramp 0x61..0x67", sel, got, wraps)
			}
		}
	}
	// The wrap point itself: flag 1 climbs off the top back to the bottom.
	if got := step(1, spacing*7); got != sprinkleRampBottom {
		t.Fatalf("flag-1 colour %#x after seven wraps, want the ramp bottom %#x", got, sprinkleRampBottom)
	}
	if got := step(0, spacing*7); got != sprinkleRampTop {
		t.Fatalf("flag-0 colour %#x after seven wraps, want the ramp top %#x", got, sprinkleRampTop)
	}
}

// TestSprinkleStepIsHalfAWorldUnit locks the sprinkle's per-tick velocity
// [03 R-FX-01 §3][03 R-FX-02 §6]: `len = trunc(sqrt(Σd²))` over the raw 16.16
// deltas and `step = ((B − A) · trunc(0x80000000 / len)) >> 16` per axis — half
// a world unit per tick along A→B, whatever the span.
func TestSprinkleStepIsHalfAWorldUnit(t *testing.T) {
	half := numeric.FixedFromInt(1).Raw() / 2

	// The step is the SAME half-unit whatever the span — this family crosses
	// no fixed number of ticks. A span that divides the reciprocal exactly
	// gives half on the nose; every other span lands a hair under it, because
	// the reciprocal is truncated before it multiplies. Never over.
	for _, span := range []int64{1, 2, 17, 400} {
		a := [3]numeric.Fixed{}
		b := [3]numeric.Fixed{numeric.FixedFromInt(span), 0, 0}
		sx, sy, sz := sprinkleStep(a, b)
		if sy != 0 || sz != 0 {
			t.Fatalf("span %d moved off its axis (%d,%d)", span, sy.Raw(), sz.Raw())
		}
		if sx.Raw() <= 0 || sx.Raw() > half {
			t.Fatalf("span %d stepped %d raw, want (0, half a world unit = %d]", span, sx.Raw(), half)
		}
		if (span == 1 || span == 2) && sx.Raw() != half {
			t.Fatalf("span %d stepped %d raw; a span that divides the reciprocal exactly must give half (%d)", span, sx.Raw(), half)
		}
	}

	// Direction, not distance: a 3–4 span splits the same half unit between the
	// two axes in the span's own ratio.
	sx, _, sz := sprinkleStep([3]numeric.Fixed{}, [3]numeric.Fixed{numeric.FixedFromInt(3), 0, numeric.FixedFromInt(4)})
	if sx.Raw()*4 != sz.Raw()*3 {
		t.Fatalf("a 3–4 span stepped (%d,%d), which is not in the span's ratio", sx.Raw(), sz.Raw())
	}
	if sx.Raw() >= sz.Raw() || sz.Raw() > half {
		t.Fatalf("a 3–4 span stepped (%d,%d); the longer axis must move further and stay within half a unit", sx.Raw(), sz.Raw())
	}
	// A degenerate pair is retail's divide fault; Nanolathe yields a zero step
	// rather than crashing the battle (see sprinkleStep).
	if sx, sy, sz := sprinkleStep([3]numeric.Fixed{}, [3]numeric.Fixed{}); sx|sy|sz != 0 {
		t.Fatalf("a degenerate pair produced a step (%d,%d,%d)", sx.Raw(), sy.Raw(), sz.Raw())
	}
}

// simArtCompositionFS authors the two animation banks a battle's authoritative
// phases read, with no retail bytes: the default effect bank whose smoke entry
// gives a puff its last frame [03 R-STRIP-01 §2], and one feature bank whose
// burn and death sequences give the feature phase its geometry and its
// lifetimes [05 R-FEAT-01 §10].
func simArtCompositionFS(t *testing.T) *vfs.FS {
	t.Helper()
	frameOf := func(w, h int, xoff, yoff int16, dur uint32) formats.GAFWriteFrame {
		p := make([]byte, w*h)
		for i := range p {
			p[i] = 1
		}
		return formats.GAFWriteFrame{Width: uint16(w), Height: uint16(h), XOffset: xoff, YOffset: yoff, Duration: dur, Pixels: p}
	}
	smoke := make([]formats.GAFWriteFrame, 12)
	for i := range smoke {
		smoke[i] = frameOf(4, 4, 0, 0, 2)
	}
	fx, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: smokePuffEntry, Frames: smoke}})
	if err != nil {
		t.Fatalf("encode effect bank: %v", err)
	}
	trees, err := formats.EncodeGAF([]formats.GAFWriteEntry{
		{Name: "treeburn", Frames: []formats.GAFWriteFrame{
			frameOf(20, 12, 7, 5, 3), frameOf(5, 7, 2, 3, 0), frameOf(16, 24, -3, 9, 2),
		}},
		{Name: "treedie", Frames: []formats.GAFWriteFrame{frameOf(8, 8, 1, 1, 4)}},
		{Name: "treedieshad", Frames: []formats.GAFWriteFrame{frameOf(8, 8, 1, 1, 4)}},
	})
	if err != nil {
		t.Fatalf("encode feature bank: %v", err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "anims"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "anims", "fx.gaf"), fx, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "anims", "trees.gaf"), trees, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

// TestCompositionInstallsContentAnimationMetadata locks the seam this file's
// smoke families and the feature phase both depend on: the authored animation
// metadata is compiled by content from the battle's own VFS and installed by
// composition, with no client in the process.
//
// It used to arrive only when the graphical shell attached its client, so a
// headless battle retired no smoke puff by animation and took the immediate
// replacement at every feature death — an authoritative difference between two
// runs of one simulation, not a rendering one [05 R-FEAT-01 §10]
// [03 R-STRIP-01 §2].
func TestCompositionInstallsContentAnimationMetadata(t *testing.T) {
	fs := simArtCompositionFS(t)
	cat := minimalCatalogForStrict()
	cat.Features = map[string]*content.FeatureDef{
		"tree1": {Filename: "trees", SeqNameBurn: "treeburn", SeqNameDie: "treedie", SeqNameDieShad: "treedieshad"},
	}
	w, err := newSlicedWorldWithCOB(cat, fs)
	if err != nil {
		t.Fatalf("unit pool: %v", err)
	}
	s := &Session{
		Catalog: cat,
		World:   minimalTerrain(),
		Mission: syntheticMission(),
		Units:   w,
		Clock:   &clock.State{},
		Econ:    &economy.Service{},
	}
	s.SeedSessionRNG(31, 31)
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}

	// The effect length the smoke families draw a last frame against.
	if got := s.effectEntryFrameCountBase(smokePuffEntry); got != 11 {
		t.Fatalf("smoke entry frame-count base %d, want the fixture's twelve frames less one", got)
	}
	// The two feature seams, bound against the same table.
	def := cat.Features["tree1"]
	if s.Features == nil || s.Features.SequenceFrames == nil || s.Features.ShadowSequenceResolved == nil || s.Features.BurnFrameGeometry == nil {
		t.Fatal("composition left a feature art seam unbound")
	}
	if got := s.Features.SequenceFrames(def, 1); len(got) != 1 || got[0] != 4 {
		t.Fatalf("death sequence delays %v, want the entry's single frame word [4]", got)
	}
	// The reclaim sequence is unauthored, so it reports no sequence and the
	// transition keeps its immediate replacement. Nothing is invented for it.
	if got := s.Features.SequenceFrames(def, 2); got != nil {
		t.Fatalf("unauthored reclaim sequence reported delays %v, want none", got)
	}
	if !s.Features.ShadowSequenceResolved(def, def.SeqNameDieShad) {
		t.Fatal("authored event shadow did not resolve through composition")
	}
	if s.Features.ShadowSequenceResolved(def, "missing-shadow") {
		t.Fatal("missing event shadow resolved through composition")
	}
	gw, gh, gx, gy := s.Features.BurnFrameGeometry(def, 0)
	if gw != 20 || gh != 12 || gx != 7 || gy != 5 {
		t.Fatalf("burn frame geometry (%d,%d,%d,%d), want the first frame's (20,12,7,5)", gw, gh, gx, gy)
	}
	// Visit 3 has crossed the first frame's three holds into the second, whose
	// authored delay of zero still occupies one visit [05 R-FEAT-01 §10].
	if gw, gh, _, _ := s.Features.BurnFrameGeometry(def, 3); gw != 5 || gh != 7 {
		t.Fatalf("burn frame geometry at visit 3 is %dx%d, want the second frame's 5x7", gw, gh)
	}

	// And the end the whole seam exists for: a puff built after composition
	// carries a last frame, so something retires it.
	s.appendStripSmokePuffer(9, [3]numeric.Fixed{}, SmokePuffTrail)
	obj := &s.strips.strips[9][0]
	if obj.frameCountBase != 11 {
		t.Fatalf("container frame-count base %d, want 11", obj.frameCountBase)
	}
	if p := obj.particles[0]; p.lastFrame < 2 || p.lastFrame > 10 {
		t.Fatalf("puff last frame %d, want 2..10 for a twelve-frame entry", p.lastFrame)
	}
}

// The low-word store precedes each integer divisor [01 R-DET-01 §1][03 R-FX-02 §2].
func TestStripSpanNarrowsBeforeDividing(t *testing.T) {
	a := [3]numeric.Fixed{}
	b := [3]numeric.Fixed{numeric.FixedFromRaw(1 << 32), 0, 0}
	if got := flameSegLife(a, b); got != 0 {
		t.Fatalf("flame life = %d", got)
	}
	if x, y, z := sprinkleStep(a, b); x|y|z != 0 {
		t.Fatalf("zero stored span did not take existing degenerate fallback: %d/%d/%d", x, y, z)
	}
	if got := nanoLifetimeTicks(0, 0, 0, numeric.FixedFromInt(1<<32), 0, 0); got != 0 {
		t.Fatalf("nano lifetime = %d", got)
	}
}
