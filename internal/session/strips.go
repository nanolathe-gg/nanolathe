package session

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/frame"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// The ten effect strips are the phase-11 object family [01 §4.4][R-CORE-01
// §4.4.1] [03 "Strip storage and lifecycle"]. The table holds ten vector
// descriptors, allocated at battle entry and freed at battle exit with every
// object destroyed [R-CORE-01 §4.4.1]. Producers append at the vector end
// chosen by a literal strip index. Strips 2, 5, 6, 7, 9 and 4 have producers;
// strips 0, 1, 3 and 8 have no writer anywhere in retail [R-STRIP-01 §1].
//
// Corrected 2026-08-31: this said "only strips 2, 5, 6, 7, and 9 have
// producers, and strips 0/1/3/4/8 have no writer at all". Strip 4 has exactly
// one — the geothermal steam producer of [05 R-ECO-02 §3], reached from the
// feature stamp — and doc 03's own census row for strip 4 was corrected to say
// so on 2026-08-29. Nothing wrote strip 4 here, so a vent produced no steam.
//
// Every strip object is a container record holding a dynamic vector of
// sub-records (particles or segments) [R-STRIP-01 §2]. The per-tick sweep
// evaluates, per object in insertion order, a removal verdict BEFORE the
// update work; a positive verdict destroys the object (retail calls its
// destructor entry with argument 1 — the argument distinguishes sweep removal
// from battle-exit destruction; Go needs no destructor body, the removal is
// the destruction) and removes it with stable left compaction, while a zero
// verdict runs the update and keeps the object [R-CORE-01 §4.4.1]. A terminal
// condition created during an update is therefore noticed only on the next
// invocation.
//
// Storage shape: retail allocates strip objects from one shared fixed pool
// whose exhaustion silently drops the object [R-STRIP-01 §1]. The pool holds
// 1000 slots for the whole life of the process — it is built once by a static
// constructor, its growth routine has a single caller (that constructor), and
// the take entry has no growth path, so 1000 is a hard capacity and not an
// initial size [03 R-FX-02 §4]. Nanolathe keeps one global live-container
// count across all ten strips and stores the per-strip insertion-order slices
// beneath it; the two observable bounds — 1000 containers overall and at most
// 401 records per strip — are retail's, and the per-strip bound is tested
// after, and independently of, the pool test.
//
// Closed 2026-09-02 [03 R-FX-02 §4]. This carried an open-question marker saying the
// pool's capacity was untraced and that exhaustion was therefore not modelled.
//
// Presentation: these objects are authoritative simulation state swept in
// phase 11, so presentation never reads them. Every live sub-record is copied
// into the committed frame's own strip channel once per tick, at the
// publication boundary and nowhere else — appendStripViews below is the single
// writer [I6]. The client draws each family at its strip's barrier in the
// staged order of [03 §1]; the per-family rules are [R-FX-01 §3],
// [R-FX-02 §2] and [R-FX-02 §3].

const (
	// stripCount is the fixed table size: ten strips, swept in ascending
	// index [R-CORE-01 §4.4.1].
	stripCount = 10

	// stripSteadyCap is the eviction threshold [03 "Strip storage and
	// lifecycle"][R-STRIP-01 §1]: when the pre-insert count exceeds 400 the
	// oldest object is destroyed first, so steady state holds at most 401
	// records per strip.
	stripSteadyCap = 400

	// stripPoolCapacity is the shared slot pool's hard capacity: 1000
	// containers live across all ten strips together, for the whole life of
	// the process [03 R-FX-02 §4]. A producer that finds the pool full
	// constructs nothing, runs no family init and appends nothing — the
	// "silently drops the object" of [R-STRIP-01 §1]. The ten per-strip bounds
	// sum to 4010, so this test is the binding one first.
	stripPoolCapacity = 1000

	// Strip 6 nano emitters spawn five particles per spawn tick, each
	// costing six CRT draws (three for the source box point, three for the
	// target box point) [03 §5.5][R-STRIP-01 §3].
	nanoParticlesPerSpawnTick = 5
	nanoDrawsPerParticle      = 6
)

// stripFamily identifies the researched object family of a strip object.
// The names come from each family's asset bindings [R-STRIP-01 §2].
type stripFamily uint8

const (
	// stripFamilyNano is the strip-6 construction/reclaim nanolathe emitter
	// [R-STRIP-01 §1 strip 6][03 §5.5 "The nanolathe spray"].
	stripFamilyNano stripFamily = iota + 1

	// stripFamilySmoke is the strips-5/9 smoke-puff family (impact smoke,
	// weapon muzzle and trail smoke, burning-feature smoke, the sinking-wreck
	// smoke column) [R-STRIP-01 §1 strips 5/9]. Its removal verdict requires
	// its window to have passed as well as its list to be empty, and its spawn
	// gate carries the window term [R-STRIP-01 §2][03 R-FX-01 §3 addendum].
	stripFamilySmoke

	// stripFamilyVentSteam is the strip-4 geothermal vent plume. It is a
	// SEPARATE class from the strips-5/9 puffer, not a parameterisation of it:
	// it overrides the removal verdict with a constant false and drops the
	// window term from its spawn gate, so a vent steams for the whole battle,
	// and it drifts upward four times as fast [03 R-FX-01 §3 addendum]
	// [05 R-ECO-02 §3]. Everything below the container — the puff record, its
	// wind drift, its animation clock and its own retirement — is shared with
	// the smoke family.
	stripFamilyVentSteam

	// stripFamilySprinkle is the strips-2/7 jittered smoke-sprinkle family
	// (the weapon impact-effect switch case and its strip-7 variant)
	// [R-STRIP-01 §1 strips 2/7].
	stripFamilySprinkle

	// stripFamilyFlame is the strip-5 flame-stream object that lays one
	// animated segment every 10 ticks over a 30-tick window [R-STRIP-01
	// §1 strip 5] — four segments per container, at ticks 0, 10, 20 and 30
	// [03 R-FX-02 §2]. Its one producer is the teleport order handler, which
	// this build does not reach yet; the family's arithmetic below is complete
	// so that producer has nothing left to decide.
	stripFamilyFlame

	// stripFamilyFlameTrail is the strip-7 flame-stream trail: one animated
	// segment per tick over a 6–7 tick flight from source to target, and
	// the only family whose sweep consumes no draws [R-STRIP-01 §1 strip 7]
	// [R-STRIP-01 §3].
	stripFamilyFlameTrail
)

// stripParticle is one sub-record: a particle or segment inside a container
// object [R-STRIP-01 §2]. Field set follows the researched per-family
// records; per I13 the retail strides (32/48/52/60/68 bytes) describe the
// executable's layout, not Go's.
type stripParticle struct {
	// Position and per-tick velocity in 16.16 world units. The smoke
	// family stores no velocity: its motion is the wind drift applied in
	// the update [R-WIND-01].
	x, y, z    numeric.Fixed
	vx, vy, vz numeric.Fixed

	// expiry is the sub-record's own expiry tick; a sub-record is removed
	// once its expiry tick has passed (strictly), with stable in-place
	// compaction [R-STRIP-01 §2]. Zero means no tick deadline (unused
	// today: every wired family sets one).
	expiry uint32

	// Animation state. There is no reduction step between a family's frame
	// word and the bound entry: the word IS an index into that entry
	// [03 R-FX-02 §6]. The smoke puff starts at 0 of `smoke 1`/`smoke 2`
	// [03 R-FX-02 §3], the strip-5 flame segment at
	// `crtRand × (frameCount − 1) / 0x8000` of `flamestream` [03 R-FX-02 §2],
	// the trail segment at 0 of the same entry [03 R-FX-01 §3], and the nano
	// particle and sprinkle puff carry no frame at all — they fill rectangles.
	//
	// Closed 2026-09-02: this carried an open-question marker asking for the mapping,
	// and the flame spawn stored a raw CRT draw here as if one existed.
	frame      int32
	frameDelay int32
	// phase is the modulo animation counter the two families that do not use a
	// countdown share: the sprinkle's colour walk (`phase = (phase + 1) mod
	// spacing`) and the trail segment's frame walk (`mod hold`)
	// [03 R-FX-01 §3][03 R-FX-01 §4]. Per I13 one Go field carries both; the
	// two retail records are laid out differently and neither layout is ours.
	phase int32
	// lastFrame is the smoke puff's own final animation frame. Retail's puff
	// record carries it and the puff dies when its cursor passes it, which is
	// what gives a smoke plume its staggered fade [03 R-STRIP-01 §2].
	// Zero means the family does not use one, or — for a smoke puff — that its
	// container had no frame count yet when the puff spawned.
	lastFrame int32
	// lastFrameDraw is the smoke puff's own CRT draw for lastFrame, retained
	// so the value can be finished later without a second draw.
	//
	// The producer spends the draw at spawn whether or not the bound entry
	// resolved [R-STRIP-01 §3], and a container built before the composer
	// filled the frame-count seam has no count to fold it against. Keeping the
	// draw lets resolveSmokeFrameCounts finish exactly the value retail would
	// have computed, from the same draw, when the seam is filled — instead of
	// discarding it and leaving the puff with no last frame at all.
	// lastFrameDrawn distinguishes a retained draw of zero from no draw.
	lastFrameDraw  int32
	lastFrameDrawn bool

	// color is the palette byte: the nano ramp 0xa1..0xa7 or the sprinkle
	// pair 0x61/0x67 [R-STRIP-01 §2].
	color uint8

	// reservedWord is the unexplained word the nano particle carries,
	// written 0x100 at spawn and read by nothing observed [03 §5.5]
	// [R-STRIP-01 §2].
	reservedWord int32
}

// stripObject is one container record [R-STRIP-01 §2]. Retail's descriptor is
// a tag byte, a zeroed word, and begin/end pointers [R-CORE-01 §4.4.1]; the
// per-family parameters below carry the researched init arguments.
type stripObject struct {
	family stripFamily

	// windowEnd bounds the object's spawn window; the spawn gate compares
	// the next-spawn tick against both this value and the global tick
	// [R-STRIP-01 §2].
	//
	// It does NOT bound the vent-steam family. That class's spawn predicate
	// has no window term and its removal verdict is a constant false, so the
	// value its producer stores here is only the capacity hint the spawn uses
	// to size its sub-record vector — never a lifetime [03 R-FX-01 §3
	// addendum]. Every other family, the strips-5/9 smoke puffer included, is
	// bounded by it.
	windowEnd uint32

	// nextSpawn/spawnInterval drive the spawn gate: at most one spawn per
	// update, interval ticks apart [R-STRIP-01 §2].
	nextSpawn     uint32
	spawnInterval int32

	// src/dst are the producer's source and target points. For nano
	// emitters the producer narrows its source and target boxes per axis to
	// the 4/11..7/11 span and stores origin plus extent [03 §5.5]; the
	// degenerate point boxes narrow to themselves.
	src, dst             [3]numeric.Fixed
	srcExtent, dstExtent [3]numeric.Fixed

	// colorSel is the sprinkle family's init flag [R-STRIP-01 §1 strips
	// 2/7]: the spawn selects between the palette pair 0x61/0x67 by it —
	// nonzero selects 0x61, zero selects 0x67.
	colorSel uint8

	// particleLife is the sub-record's tick deadline in ticks from its spawn,
	// for the two families that have one [03 R-FX-02 §6]:
	//
	//   - the sprinkle puff's `spacing × 6` (96 at spacing 16, 48 at 8)
	//     [03 R-FX-01 §3] — which is only one of its two exits; it also dies
	//     the tick the terrain under it reaches sea level, see
	//     advanceParticles [03 R-WATER-01 §1];
	//   - the trail segment's flight length, 6 or 7 [03 R-FX-01 §3].
	//
	// The two puff classes have NO tick deadline at all: a puff retires when
	// its own frame cursor reaches its own drawn last frame, and the container's
	// lifetime is the producer's literal, stored in windowEnd
	// [06 R-WFX-01 §5][03 R-FX-01 §3 addendum §B]. The strip-5 flame segment's
	// deadline is neither: it is `floor(spanUnits / 5)` derived from the
	// container's own two points, see flameSegLife [03 R-FX-02 §2].
	//
	// Closed 2026-09-02: this carried an open-question marker saying the per-site
	// lifetimes were untraced.
	particleLife int32

	// phaseModulus is the modulus of the per-sub-record phase counter for the
	// two families that use one: the sprinkle's `spacing` and the trail
	// segment's `hold` (1 at every researched site) [03 R-FX-01 §3]
	// [03 R-FX-01 §4]. Zero means the producer set none, which leaves the
	// counter idle — retail's own modulo would fault on it.
	phaseModulus int32

	// frameDelayParam is the authored animation frame delay for the smoke
	// family: each animation-frame advance redraws the next frame's delay
	// as half to full of this value, one CRT draw per advance [R-STRIP-01
	// §3]. Zero (unknown) leaves the animation clock idle and consumes no
	// draws.
	frameDelayParam int32

	// frameCountBase is the bound GAF entry's frame count less one. Three
	// families need it and all three mean the same quantity:
	//
	//   - the smoke families draw each puff's last frame against it
	//     [06 R-WFX-01 §5];
	//   - the strip-5 flame segment draws its START frame as
	//     `crtRand × frameCountBase / 0x8000` and then advances
	//     `mod frameCountBase`, so the entry's last frame is never shown
	//     [03 R-FX-02 §2];
	//   - the trail segment's `lastFrame` IS frameCountBase and its wrap is the
	//     same modulus [03 R-FX-01 §3].
	//
	// Retail's container stores it at init; this build receives it through the
	// session's entry-frame-count seam, because the entry is presentation asset
	// data the simulation does not otherwise carry [03 R-STRIP-01 §2]
	// [03 R-FX-01 §3]. Zero leaves the puff without a last frame and the flame
	// families without a wrap point, which is the pre-seam behaviour and keeps
	// a session with no resolver running.
	frameCountBase int32

	// smokeSelector picks which of the two bound smoke entries the family
	// blits: 0 is `smoke 1` and 1 is `smoke 2` [06 R-WFX-01 §1][06 R-WFX-01 §5].
	smokeSelector uint8

	particles []stripParticle
}

// isPuffFamily reports whether the family is one of the two smoke-puff classes.
// They share the whole sub-record: the wind drift, the animation clock, the
// drawn last frame, and the retirement compare against it. They differ only in
// the container's removal verdict, its spawn gate, its vertical scale, and
// whether the blitted entry is selectable [03 R-FX-01 §3 addendum].
func (f stripFamily) isPuffFamily() bool {
	return f == stripFamilySmoke || f == stripFamilyVentSteam
}

// stripTable is the ten-descriptor table [R-CORE-01 §4.4.1].
type stripTable struct {
	strips [stripCount][]stripObject

	// live is the shared slot pool's occupancy: one count across all ten
	// strips, capped at stripPoolCapacity [03 R-FX-02 §4]. Retail's pool is a
	// LIFO free list of fixed slots [03 R-FX-02 §1]; nothing here depends on
	// WHICH slot a container got, only on how many are out, so the count is
	// the whole of the pool Nanolathe needs.
	live int
}

// newStripTable allocates the empty table. Battle entry calls this; battle
// exit (and a fresh battle entry replacing the table) destroys every object
// by dropping the table [R-CORE-01 §4.4.1].
func newStripTable() *stripTable {
	return &stripTable{}
}

// release destroys every object and clears the table (battle exit)
// [R-CORE-01 §4.4.1].
func (t *stripTable) release() {
	if t == nil {
		return
	}
	for i := range t.strips {
		t.strips[i] = nil
	}
	// Battle exit walks all ten strips and destroys every object with the
	// slot-returning flag, so a new battle always starts with all 1000 slots
	// free; nothing leaks across battles [03 R-FX-02 §4].
	t.live = 0
}

func (t *stripTable) anyObjects() bool {
	if t == nil {
		return false
	}
	for i := range t.strips {
		if len(t.strips[i]) != 0 {
			return true
		}
	}
	return false
}

// poolFull is the shared pool's take entry seen from the producer: it reports
// whether all 1000 slots are out [03 R-FX-02 §4]. A producer must consult it
// BEFORE it runs the family init, because that is where retail's take sits —
// the draws inside an init or a spawn are not spent by a dropped object, while
// the draws a call site makes before calling the producer are spent regardless
// (the burning-feature emission's two jitter draws, the debris fire particle's
// four) [03 R-FX-02 §4][05 R-FEAT-01 §16][03 R-FX-01 §3].
func (t *stripTable) poolFull() bool {
	return t == nil || t.live >= stripPoolCapacity
}

// append inserts one object at the vector end of the given strip, destroying
// the oldest object first when the pre-insert count exceeds the steady cap
// [03 "Strip storage and lifecycle"][R-STRIP-01 §1]. The 401-record eviction
// is tested after, and independently of, the pool test above [03 R-FX-02 §4];
// the evicted object is destroyed with the slot-returning flag, so it gives
// its slot back. Out-of-range strip indices cannot occur: every call site uses
// a literal strip index.
func (t *stripTable) append(strip int, o stripObject) {
	if t == nil || strip < 0 || strip >= stripCount {
		return
	}
	if len(t.strips[strip]) > stripSteadyCap {
		// Destroy the oldest object and slide the survivors left; same-strip
		// order among survivors equals insertion order [R-STRIP-01 §1].
		t.strips[strip] = t.strips[strip][1:]
		t.releaseSlot()
	}
	t.strips[strip] = append(t.strips[strip], o)
	t.live++
}

// releaseSlot is the pool's return entry: every path that destroys a strip
// object — the phase-11 sweep's removal, the 401-cap eviction, and the
// battle-exit teardown — returns its slot [03 R-FX-02 §4].
func (t *stripTable) releaseSlot() {
	if t != nil && t.live > 0 {
		t.live--
	}
}

// sweep is the phase-11 dispatcher: strips in ascending index, objects in
// insertion order, removal verdict evaluated BEFORE the update work
// [R-CORE-01 §4.4.1][R-STRIP-01 §2]. The dispatcher itself consumes no random
// draws; object-internal draws all come from the CRT presentation stream
// [R-STRIP-01 §3], and a sweep over only empty strips changes no globals
// [R-CORE-01 §4.4.1].
func (t *stripTable) sweep(tick uint32, s *Session) {
	if t == nil || s == nil {
		return
	}
	crt := s.CrtRNG()
	gravityWord := int32(0)
	if s.World != nil {
		gravityWord = s.World.AuthoredGravity
	}
	for strip := 0; strip < stripCount; strip++ {
		t.sweepStrip(strip, tick, crt, s.Wind, gravityWord, s.World)
	}
}

func (t *stripTable) sweepStrip(strip int, tick uint32, crt *rng.CRT, wind *world.Wind, gravityWord int32, ter *world.Terrain) {
	objs := t.strips[strip]
	write := 0
	for read := range objs {
		o := objs[read]
		if o.removalVerdict(tick) {
			// Positive verdict: destroy (destructor argument 1) and remove
			// with stable left compaction; the survivor order is unchanged
			// [R-CORE-01 §4.4.1][03 "Strip storage and lifecycle"]. The
			// destructor's flag returns the slot to the shared pool
			// [03 R-FX-02 §4].
			t.releaseSlot()
			continue
		}
		o.update(tick, crt, wind, gravityWord, ter)
		objs[write] = o
		write++
	}
	for i := write; i < len(objs); i++ {
		objs[i] = stripObject{}
	}
	t.strips[strip] = objs[:write]
}

// removalVerdict is the removal-verdict virtual, evaluated before the update
// work [R-STRIP-01 §2]: the verdict is "the internal list is empty", and for
// the smoke puffer "the internal list is empty AND the window has passed".
//
// Corrected twice, and the second correction is the one that stands
// [03 R-FX-01 §3 addendum]:
//
//   - 2026-09-01 (morning) this became a constant false for every smoke
//     container, on the strength of a retail capture of a vent still steaming
//     eighteen seconds in. The capture is real and the reading of the class it
//     came from is right.
//   - It was applied to the wrong containers. The vent and the strips-5/9
//     puffer are two classes with two vtables, not one class with two call
//     sites. Only the VENT's class overrides this virtual with a constant
//     false; the puffer's verdict keeps both terms. Giving every muzzle,
//     impact, trail and emit-sfx container the vent's verdict made each one an
//     immortal one-puff-per-tick emitter — a strip saturated at its 401-object
//     bound, ~14.8k live puffs, and a carpet of smoke over every place a shot
//     had ever landed.
//
// Retail's own comparison is `deadline < tick` on unsigned words.
func (o *stripObject) removalVerdict(tick uint32) bool {
	if o.family == stripFamilyVentSteam {
		// The vent's class returns a constant false: it is never removed by
		// the sweep, only by the producer's 401-object eviction.
		return false
	}
	if len(o.particles) != 0 {
		return false
	}
	if o.family == stripFamilySmoke {
		return tick > o.windowEnd
	}
	return true
}

// update is the update virtual [R-STRIP-01 §2]: advance each sub-record's
// position by its velocity (the smoke family by its wind drift instead),
// advance the animation state, remove expired sub-records with stable
// in-place compaction, then run the spawn gate. Object-internal CRT draws
// happen in the animation and spawn steps [R-STRIP-01 §3].
func (o *stripObject) update(tick uint32, crt *rng.CRT, wind *world.Wind, gravityWord int32, ter *world.Terrain) {
	o.advanceParticles(tick, crt, wind, gravityWord, ter)
	o.expireParticles(tick)
	o.spawnGate(tick, crt)
}

// advanceParticles advances positions and animation state per family
// [R-STRIP-01 §2].
func (o *stripObject) advanceParticles(tick uint32, crt *rng.CRT, wind *world.Wind, gravityWord int32, ter *world.Terrain) {
	switch o.family {
	case stripFamilyNano:
		// Position advances by velocity; the colour nibble advances one
		// step up the green ramp every tick, wrapping seven back to one,
		// never reaching 0xa0 [03 §5.5].
		for i := range o.particles {
			p := &o.particles[i]
			p.x = p.x.Add(p.vx)
			p.y = p.y.Add(p.vy)
			p.z = p.z.Add(p.vz)
			n := p.color & 0x0F
			n++
			if n > 7 {
				n = 1
			}
			p.color = 0xa0 | n
		}
	case stripFamilySmoke, stripFamilyVentSteam:
		// Neither puff family carries a velocity: each tick adds the published
		// wind words multiplied by 8 to the RAW 16.16 X and Z words — the
		// first word to X, the second to Z [R-WIND-01] — and the map's
		// authored gravity word to the raw Y word, which is what makes a puff
		// rise [03 R-FX-01 §3].
		//
		// Corrected 2026-08-31, twice over. The drift was added in WHOLE world
		// units, sixty-five thousand times too far: with a wind word of forty a
		// puff crossed twenty cells a tick and left the map inside a second.
		// And the vertical term carried an open-question marker saying the scale
		// was "a game gravity global whose value is untraced" and kept at zero; the
		// global is the map's authored `gravity` key, the same word the
		// projectile conversion divides by 900.
		//
		// Corrected again 2026-09-01: the two classes shift that word by a
		// DIFFERENT amount. The strips-5/9 puffer scales it by 4 and the vent
		// by 16, so a vent's steam climbs four times as fast as a shot's smoke
		// [03 R-FX-01 §3 addendum]. The single shift of 16 came from reading
		// the vent's update and assuming one class.
		rise := int64(4)
		if o.family == stripFamilyVentSteam {
			rise = 16
		}
		for i := range o.particles {
			p := &o.particles[i]
			if wind != nil {
				p.x = numeric.Fixed(p.x.Raw() + int64(wind.DirX)*8)
				p.z = numeric.Fixed(p.z.Raw() + int64(wind.DirZ)*8)
			}
			p.y = numeric.Fixed(p.y.Raw() + int64(gravityWord)*rise)
			// Animation clock: countdown to zero advances the frame and
			// redraws the next delay as half to full of the authored
			// delay — one CRT draw per advance [R-STRIP-01 §3]. An
			// unknown authored delay (0) leaves the clock idle.
			if p.frameDelay > 0 {
				p.frameDelay--
				if p.frameDelay == 0 && o.frameDelayParam > 0 {
					p.frame++
					p.frameDelay = halfToFullDelay(o.frameDelayParam, crt)
				}
			}
			// "it is removed when its frame index REACHES its last frame"
			// [06 R-WFX-01 §5], tested on EVERY visit and not only on the
			// visits that advanced the cursor — the class's update runs the
			// signed compare once per sub-record per tick, after the position
			// and animation work [03 R-FX-01 §3 addendum]. expiry is the
			// tick-deadline form the other families use; setting it to the
			// tick just passed retires this puff on the same compaction pass.
			//
			// The lastFrame > 0 guard is ours, not retail's: zero means the
			// container has no bound entry yet, a state retail never reaches
			// because its entry pointer is resolved in the constructor. Ours
			// is filled by the composer through a seam, so zero reads as "not
			// decidable yet" rather than "retire now" — see
			// resolveSmokeFrameCounts.
			if p.lastFrame > 0 && p.frame >= p.lastFrame {
				p.expiry = expiredAt(tick)
			}
		}
	case stripFamilySprinkle:
		// `pos += step`, then the colour walk: `phase = (phase + 1) mod
		// spacing` and, when it wraps, `colour += dir` with the seven-entry
		// ramp 0x61..0x67 wrapping at both ends — 0x61 climbing past 0x67 and
		// 0x67 descending past 0x61. No draws [03 R-FX-01 §3].
		//
		// Closed 2026-09-02: this carried an open-question marker calling the counter
		// arithmetic untraced, and the family had no walk at all.
		dir := int32(-1) // colour flag 0 (the strip-7 sub-bubble) descends
		if o.colorSel != 0 {
			dir = 1 // flag 1 (the strip-2 emit-sfx wakes) climbs
		}
		for i := range o.particles {
			p := &o.particles[i]
			p.x = p.x.Add(p.vx)
			p.y = p.y.Add(p.vy)
			p.z = p.z.Add(p.vz)
			if o.phaseModulus > 0 {
				p.phase = (p.phase + 1) % o.phaseModulus
				if p.phase == 0 {
					p.color = sprinkleRampStep(p.color, dir)
				}
			}
			// The sprinkle is a WAKE: a puff survives only while its tick
			// deadline holds AND the bilinear terrain height under it is
			// strictly below the sea-level byte; it dies the tick it drifts
			// onto land at or above the water plane, and off-map (the −1
			// sentinel) counts as water [03 R-WATER-01 §1][R-TERR-01 §4].
			// Both exits return the same "erase" verdict, so this is written
			// as the same tick deadline the compaction pass already reads.
			//
			// The keep/erase sense here is the one thing [03 R-WATER-01 §1]
			// exists to correct: [03 R-FX-01 §3] had it inverted, and a clone
			// that keeps the inverted rule draws wakes on land and never on
			// water. A session with no terrain (headless fixtures) cannot make
			// the test and leaves the tick deadline as the only exit.
			if ter != nil && ter.HeightAt(p.x, p.z) >= ter.SeaLevelWorld() {
				p.expiry = expiredAt(tick)
			}
		}
	case stripFamilyFlame:
		for i := range o.particles {
			p := &o.particles[i]
			p.x = p.x.Add(p.vx)
			p.y = p.y.Add(p.vy)
			p.z = p.z.Add(p.vz)
			// `frame = (frame + 1) mod (frameCount − 1)`, every tick: the
			// entry's last frame is never shown [03 R-FX-02 §2]. An unresolved
			// entry (frameCountBase 0) has no modulus — retail's own modulo
			// would divide by zero — so the cursor stands still.
			//
			// Closed 2026-09-02: this carried an open-question marker and advanced the
			// cursor without any wrap at all.
			if o.frameCountBase > 0 {
				p.frame = (p.frame + 1) % o.frameCountBase
			}
		}
	case stripFamilyFlameTrail:
		// `pos += step`; `phase = (phase + 1) mod hold`; when the phase wraps,
		// `frame = (frame + 1) mod (frameCount − 1)` — the entry's last frame
		// is never shown, and with the hold of 1 that every researched site
		// passes the frame advances every tick. This family spends no draws
		// [03 R-FX-01 §3][R-STRIP-01 §3].
		//
		// Closed 2026-09-02: this carried an open-question marker calling the counter
		// untraced, and the segments never animated.
		for i := range o.particles {
			p := &o.particles[i]
			p.x = p.x.Add(p.vx)
			p.y = p.y.Add(p.vy)
			p.z = p.z.Add(p.vz)
			if o.phaseModulus <= 0 {
				continue
			}
			p.phase = (p.phase + 1) % o.phaseModulus
			if p.phase == 0 && o.frameCountBase > 0 {
				p.frame = (p.frame + 1) % o.frameCountBase
			}
		}
	}
}

// sprinkleRampStep walks one sprinkle puff's palette byte one step along the
// seven-entry ramp 0x61..0x67, wrapping past either end [03 R-FX-01 §3]:
// `colour += dir`, then `> 0x67 → 0x61` and `< 0x61 → 0x67`.
func sprinkleRampStep(color uint8, dir int32) uint8 {
	next := int32(color) + dir
	if next > int32(sprinkleRampTop) {
		next = int32(sprinkleRampBottom)
	}
	if next < int32(sprinkleRampBottom) {
		next = int32(sprinkleRampTop)
	}
	return uint8(next)
}

// expiredAt returns a deadline the current compaction pass already treats as
// passed, which is how the two animation-driven retirements (the puff's frame
// cursor, the sprinkle's shoreline) reach expireParticles' `tick > expiry`
// test without a second removal path.
func expiredAt(tick uint32) uint32 {
	if tick > 1 {
		return tick - 1
	}
	return 1
}

// expireParticles removes sub-records whose expiry tick has passed, with
// stable in-place compaction [R-STRIP-01 §2].
func (o *stripObject) expireParticles(tick uint32) {
	write := 0
	for read := range o.particles {
		p := o.particles[read]
		if p.expiry != 0 && tick > p.expiry {
			continue
		}
		o.particles[write] = p
		write++
	}
	for i := write; i < len(o.particles); i++ {
		o.particles[i] = stripParticle{}
	}
	o.particles = o.particles[:write]
}

// readyToSpawn is the per-family "is it time to spawn" virtual the update
// consults before it calls the spawn [R-STRIP-01 §2].
//
// nextSpawn == 0 means the producer armed no gate: retail's family
// constructors always write the first spawn tick, so the zero value is our
// explicit unarmed sentinel, never a gate at tick 0.
//
// The VENT's class — and only that class — reduces the predicate to "the stored
// next-spawn tick is at or before the global tick", with no window term. That
// is what makes a vent emit for the whole battle [03 R-FX-01 §3 addendum].
// The strips-5/9 smoke puffer keeps both terms like every other family, which
// is what makes a weapon-side container with a zero window spawn its
// constructor's one puff and then nothing more.
func (o *stripObject) readyToSpawn(tick uint32) bool {
	if o.nextSpawn == 0 || o.nextSpawn > tick {
		return false
	}
	if o.family == stripFamilyVentSteam {
		return true
	}
	return o.nextSpawn <= o.windowEnd
}

// spawnGate may spawn new sub-records: the next-spawn tick is compared
// against both the object's window end and the global tick [R-STRIP-01 §2].
// At most one spawn fires per update. nextSpawn == 0 means the producer
// armed no gate: retail's family constructors always write the first spawn
// tick, so the zero value is our explicit unarmed sentinel, never a gate at
// tick 0.
func (o *stripObject) spawnGate(tick uint32, crt *rng.CRT) {
	if crt == nil || !o.readyToSpawn(tick) {
		return
	}
	o.spawnOnce(tick, crt)
	if o.spawnInterval > 0 {
		o.nextSpawn += uint32(o.spawnInterval)
	} else {
		// No researched producer uses a non-positive interval; a zero
		// interval must not re-fire on every later tick.
		o.nextSpawn = ^uint32(0)
	}
}

// spawnOnce spends the family's researched per-spawn draws and appends the
// sub-records [R-STRIP-01 §3].
func (o *stripObject) spawnOnce(tick uint32, crt *rng.CRT) {
	switch o.family {
	case stripFamilyNano:
		// Five particles per spawn tick, six CRT draws per particle: three
		// to pick a point in the source box, three in the target box, each
		// as origin + rand()×extent/0x8000 [03 §5.5][R-STRIP-01 §3].
		for i := 0; i < nanoParticlesPerSpawnTick; i++ {
			sx := o.src[0].Add(scaleExtent(o.srcExtent[0], crt))
			sy := o.src[1].Add(scaleExtent(o.srcExtent[1], crt))
			sz := o.src[2].Add(scaleExtent(o.srcExtent[2], crt))
			tx := o.dst[0].Add(scaleExtent(o.dstExtent[0], crt))
			ty := o.dst[1].Add(scaleExtent(o.dstExtent[1], crt))
			tz := o.dst[2].Add(scaleExtent(o.dstExtent[2], crt))
			life := nanoLifetimeTicks(sx, sy, sz, tx, ty, tz)
			if life <= 0 {
				// A zero-length hop is discarded before the particle is
				// written [03 §5.5].
				continue
			}
			p := stripParticle{
				x: sx, y: sy, z: sz,
				expiry:       tick + uint32(life),
				color:        0xa0 | uint8(1+i%7),
				reservedWord: 0x100,
			}
			// The particle travels from the source point to the landing
			// point over its lifetime [03 §5.5 "The nanolathe spray"];
			// Fixed.Div truncates toward zero [I3].
			p.vx = divByTicks(tx.Sub(sx), life)
			p.vy = divByTicks(ty.Sub(sy), life)
			p.vz = divByTicks(tz.Sub(sz), life)
			o.particles = append(o.particles, p)
		}
	case stripFamilySmoke, stripFamilyVentSteam:
		// One puff per spawn; exactly one CRT draw [R-STRIP-01 §3]. The two
		// puff classes' spawns are identical apart from the entry the vent
		// hardwires — see drawIdentity.
		//
		// Corrected 2026-08-31: that draw is the puff's LAST FRAME, not a
		// random start frame — `crtRand·(frameCount − 1 − 2)/0x8000 + 2` — and
		// the cursor starts at 0. Reading it as a start frame left every puff
		// without an end: the cursor had nothing to run past, so nothing
		// retired it, and a strip filled to its 401-record bound with immortal
		// smoke. The staggered last frame is what makes a plume thin out
		// instead of standing still.
		p := stripParticle{
			x: o.src[0], y: o.src[1], z: o.src[2],
			frame:      0,
			frameDelay: o.frameDelayParam,
		}
		// The producer spends the draw whether or not the entry resolved, so it
		// is taken unconditionally and retained: with no frame count bound yet
		// the puff's last frame is finished later from this same draw, never
		// from a second one.
		p.lastFrameDraw, p.lastFrameDrawn = crt.Rand(), true
		p.lastFrame = smokeLastFrame(o.frameCountBase, p.lastFrameDraw)
		// No tick deadline: a puff's only exit is its frame cursor reaching its
		// own drawn last frame [06 R-WFX-01 §5][03 R-FX-02 §6]. The container's
		// lifetime — the producer's literal — lives in windowEnd and bounds the
		// container, not the puff.
		o.particles = append(o.particles, p)
	case stripFamilySprinkle:
		// One jittered puff per spawn; exactly three CRT draws of
		// rand×7/0x8000 − 3 per axis [R-STRIP-01 §1 strip 2][R-STRIP-01
		// §3]. The puff carries both ramp ends (0x61, 0x67), the current
		// colour — `flag ? 0x61 : 0x67` at spawn — and the direction word; the
		// draw writes that current colour RAW into a two-by-two rectangle, and
		// the `smoke 1` entry the puff also carries is never drawn
		// [03 R-FX-01 §3][03 R-FX-02 §6]. The two ramp ends are constants
		// (sprinkleRampBottom/Top) and the direction follows colorSel, so the
		// only per-puff word is the current colour.
		//
		// Closed 2026-09-02: this carried an open-question marker calling the drawn
		// byte's place in the sub-record unestablished.
		color := sprinkleRampTop
		if o.colorSel != 0 {
			color = sprinkleRampBottom
		}
		p := stripParticle{
			x:     o.src[0].Add(numeric.FixedFromInt(crtJitter7(crt))),
			y:     o.src[1].Add(numeric.FixedFromInt(crtJitter7(crt))),
			z:     o.src[2].Add(numeric.FixedFromInt(crtJitter7(crt))),
			color: color,
		}
		if o.particleLife > 0 {
			p.expiry = tick + uint32(o.particleLife)
		}
		// Half a world unit per tick along A→B, the reciprocal truncated
		// [03 R-FX-01 §3]. The step is the container's, taken from the two
		// unjittered points, and every puff copies it.
		p.vx, p.vy, p.vz = sprinkleStep(o.src, o.dst)
		o.particles = append(o.particles, p)
	case stripFamilyFlame:
		// One animated segment per spawn; exactly one CRT draw for the random
		// start frame [R-STRIP-01 §1 strip 5][R-STRIP-01 §3]. The segment
		// starts at a fresh copy of A, targets B, and crosses the whole span in
		// segLife ticks at five world units per tick [03 R-FX-02 §2].
		//
		// Closed 2026-09-02, two markers. The start frame stored the raw CRT
		// draw as if the frame word needed no reduction; it is
		// `crtRand × (frameCount − 1) / 0x8000`, an index into `flamestream`
		// [03 R-FX-02 §2][03 R-FX-02 §6]. And the travel law is not untraced:
		// it is `(B − A) / segLife` per axis, signed truncating.
		segLife := flameSegLife(o.src, o.dst)
		p := stripParticle{
			x: o.src[0], y: o.src[1], z: o.src[2],
			frame: flameStartFrame(o.frameCountBase, crt.Rand()),
			vx:    divByTicks(o.dst[0].Sub(o.src[0]), segLife),
			vy:    divByTicks(o.dst[1].Sub(o.src[1]), segLife),
			vz:    divByTicks(o.dst[2].Sub(o.src[2]), segLife),
		}
		if segLife > 0 {
			p.expiry = tick + uint32(segLife)
		}
		o.particles = append(o.particles, p)
	case stripFamilyFlameTrail:
		// One animated segment per tick; the strip-7 trail spends no draws
		// [R-STRIP-01 §1 strip 7][R-STRIP-01 §3]. The segment flies from
		// source to target over the flight length.
		// Every segment starts at a fresh copy of the source, not at the
		// previous segment, and expires with the container's deadline
		// [03 R-FX-01 §3].
		p := stripParticle{x: o.src[0], y: o.src[1], z: o.src[2]}
		if o.particleLife > 0 {
			p.expiry = o.windowEnd
			// `step = ((B − A) · trunc(65536 / lifetime)) >> 16` — a truncated
			// reciprocal times the span, which lands slightly SHORT of
			// `(B − A) / lifetime`: the factor is 10922/65536 at lifetime 6 and
			// 9362/65536 at 7, not 1/6 and 1/7 [03 R-FX-01 §3]. Corrected
			// 2026-09-02; this used the exact quotient and overshot the target
			// by a fraction of a unit per tick.
			p.vx = trailStep(o.dst[0].Sub(o.src[0]), o.particleLife)
			p.vy = trailStep(o.dst[1].Sub(o.src[1]), o.particleLife)
			p.vz = trailStep(o.dst[2].Sub(o.src[2]), o.particleLife)
		}
		o.particles = append(o.particles, p)
	}
}

// scaleExtent draws one CRT value and returns rand()×extent/0x8000 in 16.16
// [03 §5.5]. The draw is consumed even at zero extent — the count is the
// determinism contract [R-STRIP-01 §3].
func scaleExtent(extent numeric.Fixed, crt *rng.CRT) numeric.Fixed {
	return numeric.Fixed(extent.Raw() * int64(crt.Rand()) / 0x8000)
}

// crtJitter7 draws one CRT value for one sprinkle jitter axis:
// rand×7/0x8000 − 3, signed truncating [R-STRIP-01 §1 strip 2].
func crtJitter7(crt *rng.CRT) int64 {
	return int64(crt.Rand())*7/0x8000 - 3
}

// halfToFullDelay draws one CRT value for a smoke animation-frame advance:
// the next frame's delay as half to full of the authored delay
// [R-STRIP-01 §3].
func halfToFullDelay(authored int32, crt *rng.CRT) int32 {
	half := authored / 2 // signed truncating [I3]
	return half + int32(int64(crt.Rand())*int64(half)/0x8000)
}

// nanoLifetimeTicks is trunc(distance/4) taken from the floating-point
// distance, as a signed count [03 §5.5]. The float64 distance is the I2
// allowlisted presentation temporary and is never stored.
func nanoLifetimeTicks(ax, ay, az, bx, by, bz numeric.Fixed) int32 {
	const fractionOne = 65536.0
	dx := float64(bx.Raw()-ax.Raw()) / fractionOne
	dy := float64(by.Raw()-ay.Raw()) / fractionOne
	dz := float64(bz.Raw()-az.Raw()) / fractionOne
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	return int32(int64(dist) / 4) // truncate toward zero twice: __ftol then the integer divide [I3]
}

// sprinkleStep is the sprinkle family's per-tick step, one half world unit
// along A→B [03 R-FX-01 §3][03 R-FX-02 §6]:
//
//	len  = trunc(sqrt(dx² + dy² + dz²))          over the RAW 16.16 deltas
//	step = ((B − A) · trunc(0x80000000 / len)) >> 16   per axis
//
// so len is the span expressed in 16.16 units and the reciprocal is truncated
// before it multiplies. The sqrt is retail's own floating-point one, taken over
// raw deltas and narrowed immediately; nothing float is stored [I2].
//
// Retail's edge case is a fault: `A == B` gives len 0 and an integer divide
// exception (the sub-bubble type reaches it when the piece sits exactly at sea
// level, a piece whose vertices 0 and 1 coincide reaches it for types 2–5).
// Nanolathe cannot crash the battle there, so a degenerate pair yields a zero
// step — the one divergence in this family, and the state every wired site is
// in today, because the emit-sfx sink has no second effect vertex to pass yet
// (its own open second-vertex question in composition.go).
func sprinkleStep(a, b [3]numeric.Fixed) (sx, sy, sz numeric.Fixed) {
	dx := b[0].Raw() - a[0].Raw()
	dy := b[1].Raw() - a[1].Raw()
	dz := b[2].Raw() - a[2].Raw()
	length := int64(math.Sqrt(float64(dx)*float64(dx) + float64(dy)*float64(dy) + float64(dz)*float64(dz)))
	if length <= 0 {
		return 0, 0, 0
	}
	recip := int64(0x80000000) / length // truncating [I3]
	step := func(d int64) numeric.Fixed {
		return numeric.Fixed((d * recip) >> 16) // arithmetic shift, as retail's [I3]
	}
	return step(dx), step(dy), step(dz)
}

// flameSegLife is the strip-5 flame segment's life in ticks [03 R-FX-02 §2]:
//
//	n       = trunc(sqrt(dx² + dy² + dz²))   over the RAW 16.16 deltas
//	segLife = floor(n / (5 × 65536))         whole world units of span, / 5
//
// so a segment crosses the whole span in segLife ticks at five world units per
// tick. The sqrt is the same allowlisted float transient as sprinkleStep's.
//
// Retail's edge case is again a fault: a span shorter than five world units
// gives segLife 0 and the per-axis division raises an integer divide exception
// (teleport destinations closer than that to a unit reach it). Nanolathe
// returns 0 and divByTicks yields a zero step rather than crashing.
func flameSegLife(a, b [3]numeric.Fixed) int32 {
	dx := b[0].Raw() - a[0].Raw()
	dy := b[1].Raw() - a[1].Raw()
	dz := b[2].Raw() - a[2].Raw()
	n := int64(math.Sqrt(float64(dx)*float64(dx) + float64(dy)*float64(dy) + float64(dz)*float64(dz)))
	return int32(n / (5 * 65536)) // n is non-negative, so the divide floors [I3]
}

// flameStartFrame folds one CRT draw into the flame segment's start frame:
// `crtRand × (frameCount − 1) / 0x8000`, in 0 … frameCount − 2 of the bound
// `flamestream` entry [03 R-FX-02 §2][03 R-FX-02 §6]. There is no reduction
// step beyond this one — the word IS the entry's frame index. A container whose
// entry has not resolved (frameCountBase 0) starts at frame 0 and still spends
// the draw, exactly as the producer does.
func flameStartFrame(frameCountBase int32, draw int32) int32 {
	if frameCountBase <= 0 {
		return 0
	}
	return int32(int64(draw) * int64(frameCountBase) / 0x8000)
}

// trailStep is the flame-stream trail's per-axis step,
// `((B − A) · trunc(65536 / lifetime)) >> 16` [03 R-FX-01 §3]. The truncated
// reciprocal is what makes it fall slightly short of the exact quotient.
func trailStep(delta numeric.Fixed, lifetime int32) numeric.Fixed {
	if lifetime <= 0 {
		return 0
	}
	recip := int64(65536) / int64(lifetime) // truncating [I3]
	return numeric.Fixed((delta.Raw() * recip) >> 16)
}

// narrowBox narrows one producer box per axis to the span between its 4/11
// and 7/11 interpolants, returning origin plus extent [03 §5.5].
func narrowBox(min, max [3]numeric.Fixed) (origin, extent [3]numeric.Fixed) {
	for i := range min {
		delta := max[i].Sub(min[i])
		lo := min[i].Add(divByTicks(delta.Mul(numeric.FixedFromInt(4)), 11))
		hi := min[i].Add(divByTicks(delta.Mul(numeric.FixedFromInt(7)), 11))
		origin[i] = lo
		extent[i] = hi.Sub(lo)
	}
	return origin, extent
}

// divByTicks divides a 16.16 span by an integer tick count, truncating toward
// zero [I3]; a non-positive count yields zero.
func divByTicks(delta numeric.Fixed, ticks int32) numeric.Fixed {
	if ticks <= 0 {
		return 0
	}
	q, _ := delta.Div(numeric.FixedFromInt(int64(ticks)))
	return q
}

// appendStripNanoEmitter is the strip-6 producer for in-session nano
// submissions [R-STRIP-01 §1 strip 6][05 "Established — the record
// constructor and allocator epilogue"][03 §5.5]. One emitter is created per
// accepted submission; its spawn window closes one tick after creation, so
// each accepted work step contributes ten particles over two ticks — the
// first five spawn immediately as part of construction, spending thirty CRT
// draws at the producer [03 §5.5]. Eviction runs at insert; exhaustion is
// not modelled (see the storage note above).
func (s *Session) appendStripNanoEmitter(srcPoint, dstPoint [3]numeric.Fixed) {
	s.appendStripNanoEmitterBox(srcPoint, dstPoint, dstPoint)
}

// appendStripNanoEmitterBox retains the authored target footprint for feature
// reclaim/resurrection spray. Unit/build callers use the degenerate wrapper
// above until their model-box adapter supplies extents [05 R-WORK-01 §8].
func (s *Session) appendStripNanoEmitterBox(srcPoint, dstMin, dstMax [3]numeric.Fixed) {
	if s == nil || s.strips == nil {
		return
	}
	if s.strips.poolFull() {
		// A full pool drops the object before the family init runs, so the
		// emitter's thirty spawn draws are NOT spent [03 R-FX-02 §4].
		return
	}
	crt := s.CrtRNG()
	if crt == nil {
		return
	}
	tick := uint32(0)
	if s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	srcOrigin, srcExtent := narrowBox(srcPoint, srcPoint)
	dstOrigin, dstExtent := narrowBox(dstMin, dstMax)
	o := stripObject{
		family:        stripFamilyNano,
		windowEnd:     tick + 1,
		nextSpawn:     tick + 1,
		spawnInterval: 1,
		src:           srcOrigin, srcExtent: srcExtent,
		dst: dstOrigin, dstExtent: dstExtent,
	}
	// The family's constructor spawns the first five particles immediately
	// [03 §5.5 "ten particles over two ticks"]; the phase-11 gate fires the
	// second spawn on the next tick.
	o.spawnOnce(tick, crt)
	s.strips.append(6, o)
}

// SmokePuffInit is the smoke emitter's researched init, exactly as
// [06 R-WFX-01 §5] gives it: `(point, frameCap, spawnInterval, frameHold,
// lifetime, smokeSelector)`. Naming the arguments is what keeps the four
// producers' parameter rows readable at their call sites, where the older
// signature took only a window and a delay and every site passed the same
// two values.
type SmokePuffInit struct {
	// FrameCap bounds the emitter's frame-limit field: the limit is
	// `min(frameCount − 1, FrameCap)` when FrameCap is nonzero and
	// `frameCount − 1` otherwise. `startsmoke` is the one producer that caps
	// it, at 3, which is what makes its puff a four-frame one.
	FrameCap int32
	// SpawnInterval is the gate's period in ticks.
	SpawnInterval int32
	// FrameHold is the particle's per-frame countdown; 0 means 7.
	FrameHold int32
	// Lifetime bounds the emitter's spawn window: the init stores
	// `tick + Lifetime` as the container's deadline. Zero closes the window
	// immediately, so the container spawns once and dies with its particle —
	// the spawn gate needs the next-spawn tick to be within the window and the
	// removal verdict needs the window to have passed [R-STRIP-01 §2].
	//
	// This holds because the strips-5/9 puffer keeps both of those terms. The
	// geothermal vent is a different class that drops them, and briefly giving
	// this one the vent's virtuals turned all four producers below into
	// immortal emitters [03 R-FX-01 §3 addendum].
	Lifetime int32
	// Selector picks the bound smoke entry: 0 is `smoke 1` and 1 is `smoke 2`
	// [06 R-WFX-01 §1].
	Selector uint8
}

// The four researched weapon-side smoke producers [06 R-WFX-01 §5]. Every one
// of them goes to strip 9, and the parameters are what make them look
// different from each other: the trail puff plays all twelve frames at hold 7
// and dies, `startsmoke` plays four frames at hold 30 — a slow puff hanging at
// the muzzle — and the land dust of an above-sea explosion spawns three
// particles seven ticks apart.
var (
	// SmokePuffTrail backs the trail puff, the timer-expiry puff, `endsmoke`
	// at impact, and COB emit-sfx 0x102's white twin.
	SmokePuffTrail = SmokePuffInit{FrameCap: 0, SpawnInterval: 1, FrameHold: 0, Lifetime: 0}
	// SmokePuffStart is `startsmoke` at the muzzle point.
	SmokePuffStart = SmokePuffInit{FrameCap: 3, SpawnInterval: 1, FrameHold: 30, Lifetime: 0}
	// SmokePuffLandDust is the dust every above-sea explosion raises.
	SmokePuffLandDust = SmokePuffInit{FrameCap: 0, SpawnInterval: 7, FrameHold: 0, Lifetime: 15}
	// SmokePuffBlack is emit-sfx 0x102, the same shape as the trail puff on
	// the second smoke entry.
	SmokePuffBlack = SmokePuffInit{FrameCap: 0, SpawnInterval: 1, FrameHold: 0, Lifetime: 0, Selector: 1}
)

// appendStripSmokePuffer creates a smoke-puff container on one strip
// [R-STRIP-01 §1 strips 5/9][06 R-WFX-01 §5]. The family's constructor spawns
// its first puff immediately [R-STRIP-01 §2], spending exactly one CRT draw —
// the particle's LAST frame, not a start frame [R-STRIP-01 §3 as corrected].
//
// Corrected 2026-08-31: every call site used to pass the same `(life 0,
// frameDelay 7)` pair, because the older signature had nowhere to put the
// other three arguments and [R-STRIP-01 §1] carried the per-site parameters as
// an open question. [06 R-WFX-01 §5] itemises all four producers, so the sites
// now differ from each other the way retail's do.
func (s *Session) appendStripSmokePuffer(strip int, pos [3]numeric.Fixed, init SmokePuffInit) {
	if s == nil || s.strips == nil {
		return
	}
	if s.strips.poolFull() {
		// The puff's one spawn draw lives inside the init the producer never
		// calls, so a dropped container spends nothing [03 R-FX-02 §4].
		return
	}
	tick := uint32(0)
	if s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	hold := init.FrameHold
	if hold == 0 {
		hold = smokeDefaultFrameDelay // "frameHold 0 means 7"
	}
	o := stripObject{
		family:          stripFamilySmoke,
		src:             pos,
		frameDelayParam: hold,
		frameCountBase:  s.smokeFrameLimit(init),
		smokeSelector:   init.Selector,
	}
	// The window is armed unconditionally, exactly as the class's init does it
	// — `deadline = tick + life`, life included when it is zero. A zero life
	// therefore leaves the deadline AT the creation tick, which is what makes
	// the gate refuse the second spawn (nextSpawn = tick + interval is already
	// past it) and the verdict retire the container on the first tick after
	// its one puff is gone [03 R-FX-01 §3 addendum].
	o.windowEnd = tick + uint32(init.Lifetime)
	if init.SpawnInterval > 0 {
		o.spawnInterval = init.SpawnInterval
		o.nextSpawn = tick + uint32(init.SpawnInterval)
	}
	if crt := s.CrtRNG(); crt != nil {
		o.spawnOnce(tick, crt)
	}
	s.strips.append(strip, o)
}

// smokeFrameLimit is the emitter's frame-limit field: `min(frameCount − 1,
// FrameCap)` when FrameCap is nonzero, `frameCount − 1` otherwise
// [06 R-WFX-01 §5]. Each particle's last frame is drawn against it.
func (s *Session) smokeFrameLimit(init SmokePuffInit) int32 {
	base := s.smokeEntryFrameCountBase(init.Selector)
	if init.FrameCap > 0 && (base == 0 || init.FrameCap < base) {
		return init.FrameCap
	}
	return base
}

// smokeDefaultFrameDelay is the smoke family's animation frame hold when a site
// passes zero: "hold, or 7 when 0" in the family's three-argument init
// [03 R-FX-02 §3], stated the same way by the census — "the smoke family's
// animation delay defaults to 7 when a producer passes zero"
// [R-STRIP-01 §1] — and indexed as the rule for this site [03 R-FX-02 §6].
// Each animation-frame advance consumes one CRT draw [R-STRIP-01 §3].
//
// Closed 2026-09-02: this carried an open-question marker asking for the default to be
// written up in the research docs, which it now is.
const smokeDefaultFrameDelay = 7

// appendStripSprinkle creates a strips-2/7 smoke-sprinkle container
// [R-STRIP-01 §1 strips 2/7]. Retail's constructor closes the spawn window
// one tick after creation and spawns the first puff immediately, so each
// container holds two puffs: one at the producer and one from the phase-11
// gate on the next tick, after which the closed window stops the gate. Each
// puff lives spacing×6 ticks (16-tick spacing → 96, 8-tick → 48). The
// producer spends exactly three CRT draws per spawn (per-axis jitter)
// [R-STRIP-01 §3]. colorSel selects the palette entry: nonzero → 0x61, zero
// → 0x67 [R-STRIP-01 §1 strips 2/7].
//
// The producer takes two points, A and B [03 R-FX-01 §3]: the container's
// second point is not a copy of the spawn point but the emit-sfx producer's
// own — the piece's other transformed vertex, or the surface point the
// sub-bubble case substitutes — and is what the sprinkle's per-tick velocity
// is built from: `step = ((B − A) · trunc(0x80000000 / len)) >> 16`, half a
// world unit per tick along A→B [03 R-FX-02 §6]. This folds what was two
// producers (a one-point form here and a two-point `appendStripSprinkleVector`
// in composition.go) into the one the family has always had; the one-point
// form had no production caller once the emit-sfx sink started passing its
// own second vertex, only test callers passing the same point twice.
//
// A == B is retail's integer-divide fault; sprinkleStep yields a zero step
// here instead — the same bounds-check divergence a caller that still passes
// one point twice observes.
func (s *Session) appendStripSprinkle(strip int, a, b [3]numeric.Fixed, spacing int32, colorSel uint8) {
	if s == nil || s.strips == nil {
		return
	}
	if s.strips.poolFull() {
		// The three jitter draws sit inside the family's spawn, so a dropped
		// container spends none of them [03 R-FX-02 §4].
		return
	}
	tick := uint32(0)
	if s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	o := stripObject{
		family:        stripFamilySprinkle,
		windowEnd:     tick + 1,
		nextSpawn:     tick + 1,
		spawnInterval: 1,
		particleLife:  spacing * 6,
		phaseModulus:  spacing,
		colorSel:      colorSel,
		src:           a,
		dst:           b,
	}
	if crt := s.CrtRNG(); crt != nil {
		o.spawnOnce(tick, crt)
	}
	s.strips.append(strip, o)
}

// The geothermal steam producer [05 R-ECO-02 §3][R-STRIP-01 §1 strip 4].
//
// A geothermal vent's whole visible product is this emitter. The vent itself
// has no artwork: `geotherm.gaf` holds one entry of one frame, one pixel by one
// pixel [05 "Feature catalog and placement"], so the definition is a placement
// marker for the geothermal-plant build test and the steam is what a player
// actually sees.
//
// Established parameters, all three closed 2026-08-31 (they were recorded as
// "which of the family's init parameters each of the three literals binds to"
// is Unknown, and the family was recorded as the flame class; it is the
// smoke-puff class):
//
//   - spawn interval 5 ticks,
//   - animation frame hold 0, which the family constructor defaults to 7,
//   - container lifetime 150 ticks.
//
// The container spawns one puff from its own constructor and then one every
// fifth tick, for as long as the battle lasts. It is reached only from the
// feature stamp — a single call site in the whole image, verified by an
// exhaustive scan for every call, jump and absolute reference to the producer —
// so a vent starts steaming when the map places it (and again if a save reload
// or a successor re-stamps it), and never stops.
//
// Corrected 2026-09-01 [03 R-FX-01 §3 addendum]. This said "thirty-one puffs
// over five seconds and then stops … there is no perpetual plume in retail",
// reading the third literal as a container lifetime. It is not: the class's
// removal verdict is a constant false and its spawn predicate is a bare
// `nextSpawn <= tick`, and the only reader of the stored deadline is the
// spawn's capacity reservation. A retail capture settles it — a vent's plume
// is still there eighteen seconds in, anchored and undiminished.
//
// Nothing removes an earlier object at all, so repeated stamps accumulate up to
// the 401-record bound, at which point the producer evicts the oldest.
func (s *Session) appendStripGeothermalSteam(pos [3]numeric.Fixed) {
	if s == nil || s.strips == nil {
		return
	}
	if s.strips.poolFull() {
		// The vent's own first puff is spawned by the init, so its draw is not
		// spent when the pool drops the container [03 R-FX-02 §4].
		return
	}
	tick := uint32(0)
	if s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	o := stripObject{
		family:          stripFamilyVentSteam,
		src:             pos,
		frameDelayParam: smokeDefaultFrameDelay,
		frameCountBase:  s.smokeEntryFrameCountBase(0),
		windowEnd:       tick + geothermalSteamCapacityHorizon,
		spawnInterval:   geothermalSteamInterval,
		nextSpawn:       tick + uint32(geothermalSteamInterval),
	}
	if crt := s.CrtRNG(); crt != nil {
		o.spawnOnce(tick, crt) // the constructor's own first puff
	}
	s.strips.append(stripGeothermalSteam, o)
}

const (
	// stripGeothermalSteam is the literal strip index the producer passes
	// [05 R-ECO-02 §3][R-STRIP-01 §1 strip 4].
	stripGeothermalSteam = 4

	// geothermalSteamInterval and geothermalSteamCapacityHorizon are the
	// producer's first and third init literals [05 R-ECO-02 §3]. The third is
	// stored as `tick + 150` and read by nothing but the spawn's vector
	// reservation; it is not a lifetime [03 R-FX-01 §3 addendum].
	geothermalSteamInterval        int32  = 5
	geothermalSteamCapacityHorizon uint32 = 150

	// The strip-5 flame container's two site literals: the teleport handler
	// builds a 30-tick container that lays one segment every 10 ticks — four
	// segments in all [03 R-FX-02 §2][03 R-FX-02 §6]. The segment life is not a
	// literal: it is floor(spanUnits/5), see flameSegLife. No producer reaches
	// this family yet (the teleport order handler is unwired), so these name
	// the site's parameters for the one that will.
	flameContainerLifetime int32 = 30
	flameSegmentInterval   int32 = 10

	// trailSegmentHold is the flame-stream trail's phase modulus. Every
	// researched site passes 1, so the frame advances every tick
	// [03 R-FX-01 §3][03 R-FX-02 §6].
	trailSegmentHold int32 = 1
)

// appendStripViews mirrors every live strip sub-record into the committed
// frame, one view per sub-record, rebuilt from scratch at every publication.
//
// Strip objects are authoritative simulation state with no committed-frame
// representation of their own, and they cannot reach the frame through the
// presentation event buffer: the strip sweep is phase 11, the effect pool is
// advanced in phase 4, and the buffer is reset at publication — so an event
// emitted from the sweep is wiped before any effect phase reads it. A per-frame
// copy at the publication boundary is the shape that fits: it samples committed
// state, mutates nothing, and needs no lifetime bookkeeping of its own because
// the strip object already owns each sub-record's life [I6].
//
// The walk order IS the composer's: strips ascending 0..9, objects in insertion
// order, sub-records in vector order [03 §1][R-FX-02 §1][I1]. The client
// consumes one contiguous run per barrier, so this order is a contract and not
// an implementation detail.
//
// Each view carries its family, its art identity as a (bank, entry) pair, its
// own animation cursor and — for the one family that fills rather than blits —
// its own palette byte. The per-family draw rules are the client's; nothing
// here decides how a record is painted [03 R-FX-01 §3].
//
// Corrected: these records used to be mirrored as frame.EffectView values on
// the committed Effects slice, with an entry name and no bank, no family, and
// no way for the draw to tell one family from another. Half of [R-FX-01 §3]'s
// per-family contract cannot be expressed that way — the two puff classes blit
// with NO coverage gate while the flame and sprinkle families gate, and the
// sprinkle's colour byte must reach the framebuffer unremapped — so the mirror
// has its own committed channel and its own draw.
//
// Strip 6 is deliberately NOT mirrored. The nanolathe spray already has a
// presentation path of its own — the strip-6 stroke gate the composer runs on
// published nanolathe events — and mirroring the particles beside it would draw
// the same spray twice. Folding the two into one is the remaining half of this
// seam and wants the event path retired first.
func (s *Session) appendStripViews(out []frame.StripView) []frame.StripView {
	if s == nil || s.strips == nil {
		return out
	}
	for strip := 0; strip < stripCount; strip++ {
		if strip == stripNanolathe {
			continue
		}
		for _, o := range s.strips.strips[strip] {
			family, bank, entry, fill := o.drawIdentity()
			if entry == "" && fill == 0 {
				continue
			}
			for i := range o.particles {
				p := &o.particles[i]
				view := frame.StripView{
					Strip:  int8(strip),
					Family: family,
					Bank:   bank,
					Entry:  entry,
					Frame:  p.frame,
					X:      p.x,
					Y:      p.y,
					Z:      p.z,
				}
				if entry == "" {
					// The sprinkle family's own colour walks its ramp as the
					// puff ages [R-STRIP-01 §1 strips 2/7][R-FX-01 §3]; the
					// family default stands in only for a sub-record that
					// carries none.
					view.Fill = fill
					if p.color != 0 {
						view.Fill = p.color
					}
				}
				out = append(out, view)
			}
		}
	}
	return out
}

// drawIdentity is one family's per-sub-record draw form [03 R-STRIP-01 §2]:
// the frame family that selects the draw, the (bank, entry) pair it blits, and
// the fill colour of its two-by-two rectangle when it fills instead. A family
// with neither an entry nor a fill is not mirrored.
func (o *stripObject) drawIdentity() (family frame.StripFamily, bank, entry string, fill uint8) {
	switch o.family {
	case stripFamilySmoke:
		// "the smoke family blits one of two smoke GAF entries selected by an
		// init flag" [03 R-STRIP-01 §2][06 R-WFX-01 §1].
		return frame.StripFamilySmokePuff, effectBank, smokeEntryForSelector(o.smokeSelector), 0
	case stripFamilyVentSteam:
		// The vent's class takes no selector: its init binds the first smoke
		// entry directly [03 R-FX-01 §3 addendum].
		return frame.StripFamilyVentSteam, effectBank, smokePuffEntry, 0
	case stripFamilyFlame:
		// "the flame families blit the flame-stream GAF entry".
		return frame.StripFamilyFlame, effectBank, flameStreamEntry, 0
	case stripFamilyFlameTrail:
		return frame.StripFamilyFlameTrail, effectBank, flameStreamEntry, 0
	case stripFamilySprinkle:
		// The sprinkle fills rectangles rather than blitting its carried entry:
		// [R-FX-01 §3] records the puff's `smoke 1` entry as "carried, NEVER
		// drawn — this family fills rectangles".
		return frame.StripFamilySprinkle, "", "", sprinkleDefaultColor
	case stripFamilyNano:
		return frame.StripFamilyNano, "", "", nanoRampBase
	}
	return frame.StripFamilyNone, "", "", 0
}

const (
	// stripNanolathe is the strip whose spray already has its own presentation
	// path; see appendStripViews.
	stripNanolathe = 6

	// effectBank is the bank half of every strip family's identity pair. The
	// entries the strip families blit are rows of the engine's own fixed
	// effect-slot table, which is bound from `fx` at startup [06 R-WFX-01 §1]
	// [03 R-FX-01 §3]; the reference install's `anims/fx.gaf` holds all of
	// them (I14).
	effectBank = "fx"

	// flameStreamEntry is the shared effect bank entry both flame families blit
	// [03 R-FX-01 §3].
	flameStreamEntry = "flamestream"

	// sprinkleRampBottom and sprinkleRampTop are the seven-entry palette ramp
	// the sprinkle puff walks, 0x61..0x67 [03 R-FX-01 §3]. Colour flag 1 spawns
	// at the bottom and climbs, flag 0 spawns at the top and descends; either
	// wraps to the other end.
	sprinkleRampBottom uint8 = 0x61
	sprinkleRampTop    uint8 = 0x67

	// sprinkleDefaultColor is the sprinkle pair's zero-selector colour
	// [R-STRIP-01 §1 strips 2/7]; a live sub-record carries its own walked
	// value and overrides it.
	sprinkleDefaultColor = sprinkleRampTop

	// nanoRampBase is the first entry of the nano ramp 0xa1..0xa7
	// [R-STRIP-01 §2].
	nanoRampBase uint8 = 0xa1
)

// smokeEntryForSelector maps the init flag to the bound entry
// [06 R-WFX-01 §1]: selector 0 is `smoke 1` and selector 1 is `smoke 2`.
//
// Corrected: this carried an open-question marker saying "the stock shared bank holds
// no `smoke 2` entry, so selector 1 binds nothing there", and asked which entry
// the black emit-sfx puff actually draws. The premise is wrong on both the
// research and the assets. [03 R-FX-01 §2]'s bank census lists `smoke 2` among
// the `fx` bindings and [06 R-WFX-01 §1] itemizes it, and a census of the
// reference install's own `anims/fx.gaf` (I14) reads both entries: `smoke 1`
// with twelve frames and `smoke 2` with sixteen. Selector 1 binds normally and
// resolves a frame count like any other entry; there is no miss path here.
func smokeEntryForSelector(selector uint8) string {
	if selector == 1 {
		return smokeBlackEntry
	}
	return smokePuffEntry
}

// smokeBlackEntry is the family's second bound entry [06 R-WFX-01 §1].
const smokeBlackEntry = "smoke 2"

// smokePuffEntry is the shared effect bank entry the smoke-puff family blits
// [R-FX-01 §3]. The bank holds a separate `steam` entry, which this producer
// does NOT select: the geothermal container's init reads the same default
// smoke entry pointer every other three-argument smoke producer reads.
const smokePuffEntry = "smoke 1"

// smokeEntryFrameCountBase is the selected smoke entry's frame count less one
// [03 R-STRIP-01 §2][06 R-WFX-01 §5].
//
// Retail's container reads it from the entry pointer it was initialised with;
// the entry is presentation asset data, so this build receives it through a
// seam the composer fills. A session with no resolver — every headless run —
// gets zero, and its puffs then carry no last frame, which is exactly the
// behaviour that stood before the seam existed.
func (s *Session) smokeEntryFrameCountBase(selector uint8) int32 {
	return s.effectEntryFrameCountBase(smokeEntryForSelector(selector))
}

// effectEntryFrameCountBase is the same seam for any effect-bank entry: the two
// flame families need `flamestream`'s frame count less one for their start
// frame and their wrap modulus [03 R-FX-02 §2][03 R-FX-01 §3], which is the
// same quantity the smoke families draw their last frame against.
func (s *Session) effectEntryFrameCountBase(entry string) int32 {
	if s == nil || s.effectFrameCount == nil {
		return 0
	}
	n, ok := s.effectFrameCount("", entry)
	if !ok || n <= 0 {
		return 0
	}
	return int32(n - 1)
}

// smokeLastFrame folds one retained CRT draw against a bound entry's frame
// count into a smoke puff's own final animation frame:
// `crtRand·(frameCount − 3)/0x8000 + 2`, expressed here against the container's
// stored `frameCount − 1` [03 R-FX-01 §3][06 R-WFX-01 §5]. A container with no
// bound count yields 0, which means "not finished yet" and is finished by
// resolveSmokeFrameCounts.
func smokeLastFrame(frameCountBase int32, draw int32) int32 {
	if frameCountBase <= 2 {
		return 0
	}
	return int32(int64(draw)*int64(frameCountBase-2)/0x8000) + 2
}

// resolveSmokeFrameCounts finishes every live smoke container that was built
// before the frame-count seam was filled, and every puff those containers had
// already spawned.
//
// The ordering this repairs is real and unavoidable: the geothermal steam
// producer runs from the feature stamp, so a vent's container is created while
// the map's terrain features are populated — inside the authoritative session
// constructor, well before any composer exists to fill the seam. Those
// containers therefore resolved a frame count of zero, their puffs got no last
// frame, and nothing retired them: the smoke family's ONLY retirement is its
// cursor reaching its last frame, so every vent's steam was immortal. Thirty-one
// puffs piled at the vent and then slid downwind forever, which is what the
// play test saw.
//
// No draw is taken here. Each puff already spent its own draw at spawn and
// retained it, so the value finished now is exactly the one retail computes.
func (s *Session) resolveSmokeFrameCounts() {
	if s == nil || s.strips == nil {
		return
	}
	for strip := 0; strip < stripCount; strip++ {
		for i := range s.strips.strips[strip] {
			o := &s.strips.strips[strip][i]
			if !o.family.isPuffFamily() || o.frameCountBase > 0 {
				continue
			}
			o.frameCountBase = s.smokeEntryFrameCountBase(o.smokeSelector)
			if o.frameCountBase <= 2 {
				continue
			}
			for j := range o.particles {
				p := &o.particles[j]
				if p.lastFrame != 0 || !p.lastFrameDrawn {
					continue
				}
				p.lastFrame = smokeLastFrame(o.frameCountBase, p.lastFrameDraw)
			}
		}
	}
}

// SetEffectEntryFrameCount installs the resolver the strip families ask for an
// effect entry's frame count. It is the same shape as the effect-timing
// resolver next door and is filled by the same composer, from the same bank
// cache, so the frame count a puff's last frame is drawn against and the frames
// the draw pass can actually blit can never come apart.
//
// Installing it also finishes the containers that already exist — see
// resolveSmokeFrameCounts for why any exist at all.
func (s *Session) SetEffectEntryFrameCount(resolve func(bank, entry string) (int, bool)) {
	if s == nil {
		return
	}
	s.effectFrameCount = resolve
	s.resolveSmokeFrameCounts()
}

// SetFeatureSequenceResolver installs the resolver the FEATURE phase asks for
// a definition's animation sequence — the burn frame's geometry and the
// die/reclaim/burn lifetime of [05 R-FEAT-01 §10]. It is the same shape as the
// two effect resolvers above and is filled by the same composer, from the same
// process's asset cache, so the frames the draw pass blits and the frames the
// simulation counts can never come apart.
//
// Unlike the effect resolvers this one feeds AUTHORITATIVE state: the visit a
// feature's death animation ends on is when its successor is stamped. That is
// sound because the answer depends on nothing but the asset bytes, and it is
// why the resolver must be installed before the battle composes rather than at
// the first draw.
func (s *Session) SetFeatureSequenceResolver(resolve func(filename, sequence string, visit int32) (w, h, xoff, yoff, visits int32, ok bool)) {
	if s == nil {
		return
	}
	s.featureSequence = resolve
}
