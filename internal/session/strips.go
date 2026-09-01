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
// whose exhaustion silently drops the object [R-STRIP-01 §1]. The pool's
// capacity is not established, so Nanolathe stores per-strip insertion-order
// slices bounded by the researched eviction rule instead; the observable
// bound (at most 401 records per strip) is identical.
// TODO(question): the shared strip-object pool's capacity — a traced pool
// size would let Nanolathe reproduce exhaustion-driven silent drops.
//
// Presentation: these objects are authoritative simulation state swept in
// phase 11. The committed frame boundary for presentation remains the
// render.EffectService admission path fed by frame events; strip objects are
// not yet mirrored into the committed frame.
// TODO(question): strip objects' committed-frame publication — the researched
// families map onto per-sub-record draws (GAF frames or two-by-two fills with
// the nano ramp 0xa1..0xa7 and sprinkle colors 0x61/0x67 [R-STRIP-01 §2]);
// publishing them requires a frame view of the strip table, which the frame
// package does not carry yet.

const (
	// stripCount is the fixed table size: ten strips, swept in ascending
	// index [R-CORE-01 §4.4.1].
	stripCount = 10

	// stripSteadyCap is the eviction threshold [03 "Strip storage and
	// lifecycle"][R-STRIP-01 §1]: when the pre-insert count exceeds 400 the
	// oldest object is destroyed first, so steady state holds at most 401
	// records per strip.
	stripSteadyCap = 400

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
	// burning-feature smoke, the sinking-wreck smoke column) [R-STRIP-01
	// §1 strips 5/9]. It is the one family whose removal verdict
	// additionally requires its window to have passed [R-STRIP-01 §2].
	stripFamilySmoke

	// stripFamilySprinkle is the strips-2/7 jittered smoke-sprinkle family
	// (the weapon impact-effect switch case and its strip-7 variant)
	// [R-STRIP-01 §1 strips 2/7].
	stripFamilySprinkle

	// stripFamilyFlame is the strip-5 flame-stream object that lays one
	// animated segment every 10 ticks over a 30-tick window [R-STRIP-01
	// §1 strip 5].
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

	// Animation state. The start-frame value is retained as drawn; its
	// reduction against the bound GAF entry's frame count is not
	// established. TODO(question): the start-frame mapping to a GAF entry
	// frame index — the committed contract is the draw itself [R-STRIP-01
	// §3].
	frame      int32
	frameDelay int32
	// lastFrame is the smoke puff's own final animation frame. Retail's puff
	// record carries it and the puff dies when its cursor passes it, which is
	// what gives a smoke plume its staggered fade [03 R-STRIP-01 §2].
	// Zero means the family does not use one.
	lastFrame int32

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
	// [R-STRIP-01 §2]. For the smoke family it also participates in the
	// removal verdict [R-STRIP-01 §2].
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

	// particleLife is the producer-supplied sub-record lifetime in ticks
	// for families whose per-site constant is not established (sprinkle,
	// smoke, flame).
	// TODO(question): the per-site lifetimes of the sprinkle, smoke, and
	// flame-stream producers [R-STRIP-01 §1 names the sites but not the
	// constants]; the sinking-wreck smoke column's 900-tick life is the one
	// established value.
	particleLife int32

	// frameDelayParam is the authored animation frame delay for the smoke
	// family: each animation-frame advance redraws the next frame's delay
	// as half to full of this value, one CRT draw per advance [R-STRIP-01
	// §3]. Zero (unknown) leaves the animation clock idle and consumes no
	// draws.
	frameDelayParam int32

	// frameCountBase is the bound GAF entry's frame count less one, which the
	// smoke family's spawn draws its puff's last frame from. Retail's container
	// stores it at init; this build receives it through the session's
	// entry-frame-count seam, because the entry is presentation asset data the
	// simulation does not otherwise carry [03 R-STRIP-01 §2][03 R-FX-01 §3].
	// Zero leaves the puff without a last frame, which is the pre-seam
	// behaviour and keeps a session with no resolver running.
	frameCountBase int32

	// smokeSelector picks which of the two bound smoke entries the family
	// blits: 0 is `smoke 1` and 1 is `smoke 2` [06 R-WFX-01 §1][06 R-WFX-01 §5].
	smokeSelector uint8

	particles []stripParticle
}

// stripTable is the ten-descriptor table [R-CORE-01 §4.4.1].
type stripTable struct {
	strips [stripCount][]stripObject
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

// append inserts one object at the vector end of the given strip, destroying
// the oldest object first when the pre-insert count exceeds the steady cap
// [03 "Strip storage and lifecycle"][R-STRIP-01 §1]. Out-of-range strip
// indices cannot occur: every call site uses a literal strip index.
func (t *stripTable) append(strip int, o stripObject) {
	if t == nil || strip < 0 || strip >= stripCount {
		return
	}
	if len(t.strips[strip]) > stripSteadyCap {
		// Destroy the oldest object and slide the survivors left; same-strip
		// order among survivors equals insertion order [R-STRIP-01 §1].
		t.strips[strip] = t.strips[strip][1:]
	}
	t.strips[strip] = append(t.strips[strip], o)
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
		t.sweepStrip(strip, tick, crt, s.Wind, gravityWord)
	}
}

func (t *stripTable) sweepStrip(strip int, tick uint32, crt *rng.CRT, wind *world.Wind, gravityWord int32) {
	objs := t.strips[strip]
	write := 0
	for read := range objs {
		o := objs[read]
		if o.removalVerdict(tick) {
			// Positive verdict: destroy (destructor argument 1) and remove
			// with stable left compaction; the survivor order is unchanged
			// [R-CORE-01 §4.4.1][03 "Strip storage and lifecycle"].
			continue
		}
		o.update(tick, crt, wind, gravityWord)
		objs[write] = o
		write++
	}
	for i := write; i < len(objs); i++ {
		objs[i] = stripObject{}
	}
	t.strips[strip] = objs[:write]
}

// removalVerdict is the removal-verdict virtual, evaluated before the update
// work [R-STRIP-01 §2]: the verdict is "the internal list is empty"; the
// smoke family additionally requires its window to have passed.
func (o *stripObject) removalVerdict(tick uint32) bool {
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
func (o *stripObject) update(tick uint32, crt *rng.CRT, wind *world.Wind, gravityWord int32) {
	o.advanceParticles(tick, crt, wind, gravityWord)
	o.expireParticles(tick)
	o.spawnGate(tick, crt)
}

// advanceParticles advances positions and animation state per family
// [R-STRIP-01 §2].
func (o *stripObject) advanceParticles(tick uint32, crt *rng.CRT, wind *world.Wind, gravityWord int32) {
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
	case stripFamilySmoke:
		// The smoke family carries no velocity: each tick adds the published
		// wind words multiplied by 8 to the RAW 16.16 X and Z words — the
		// first word to X, the second to Z [R-WIND-01] — and the map's
		// authored gravity word multiplied by 16 to the raw Y word, which is
		// what makes a puff rise [03 R-FX-01 §3].
		//
		// Corrected 2026-08-31, twice over. The drift was added in WHOLE world
		// units, sixty-five thousand times too far: with a wind word of forty a
		// puff crossed twenty cells a tick and left the map inside a second.
		// And the vertical term was a TODO(question) saying the scale was "a
		// game gravity global whose value is untraced" and kept at zero; the
		// global is the map's authored `gravity` key, the same word the
		// projectile conversion divides by 900, and the shift is four.
		for i := range o.particles {
			p := &o.particles[i]
			if wind != nil {
				p.x = numeric.Fixed(p.x.Raw() + int64(wind.DirX)*8)
				p.z = numeric.Fixed(p.z.Raw() + int64(wind.DirZ)*8)
			}
			p.y = numeric.Fixed(p.y.Raw() + int64(gravityWord)*16)
			// Animation clock: countdown to zero advances the frame and
			// redraws the next delay as half to full of the authored
			// delay — one CRT draw per advance [R-STRIP-01 §3]. An
			// unknown authored delay (0) leaves the clock idle.
			if p.frameDelay > 0 {
				p.frameDelay--
				if p.frameDelay == 0 && o.frameDelayParam > 0 {
					p.frame++
					p.frameDelay = halfToFullDelay(o.frameDelayParam, crt)
					// "it is removed when its frame index REACHES its last
					// frame" [06 R-WFX-01 §5]. expiry is the tick-deadline form
					// the other families use; setting it to the tick just
					// passed retires this puff on the same compaction pass.
					if p.lastFrame > 0 && p.frame >= p.lastFrame {
						p.expiry = 1
						if tick > 1 {
							p.expiry = tick - 1
						}
					}
				}
			}
		}
	case stripFamilySprinkle:
		// Position advances by velocity (zero until traced — see the spawn
		// note below).
		for i := range o.particles {
			p := &o.particles[i]
			p.x = p.x.Add(p.vx)
			p.y = p.y.Add(p.vy)
			p.z = p.z.Add(p.vz)
		}
		// TODO(question): the sprinkle's animation-frame walk (its spawn
		// and expiry are established; the frame advance consumes no draws
		// [R-STRIP-01 §3] but its counter arithmetic is untraced).
	case stripFamilyFlame:
		for i := range o.particles {
			p := &o.particles[i]
			p.x = p.x.Add(p.vx)
			p.y = p.y.Add(p.vy)
			p.z = p.z.Add(p.vz)
			p.frame++
			// TODO(question): the flame segment's frame-cursor wrap point
			// against the flame-stream GAF entry's frame count.
		}
	case stripFamilyFlameTrail:
		for i := range o.particles {
			p := &o.particles[i]
			p.x = p.x.Add(p.vx)
			p.y = p.y.Add(p.vy)
			p.z = p.z.Add(p.vz)
		}
		// TODO(question): the trail segment's animation counter (it draws
		// no start-frame RNG; its per-tick frame advance is untraced)
		// [R-STRIP-01 §3].
	}
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

// spawnGate may spawn new sub-records: the next-spawn tick is compared
// against both the object's window end and the global tick [R-STRIP-01 §2].
// At most one spawn fires per update. nextSpawn == 0 means the producer
// armed no gate: retail's family constructors always write the first spawn
// tick, so the zero value is our explicit unarmed sentinel, never a gate at
// tick 0.
func (o *stripObject) spawnGate(tick uint32, crt *rng.CRT) {
	if crt == nil || o.nextSpawn == 0 || o.nextSpawn > tick || o.nextSpawn > o.windowEnd {
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
	case stripFamilySmoke:
		// One puff per spawn; exactly one CRT draw [R-STRIP-01 §3].
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
		if o.frameCountBase > 2 {
			p.lastFrame = int32(int64(crt.Rand())*int64(o.frameCountBase-2)/0x8000) + 2
		} else {
			// No frame count bound yet: the draw is still spent, because the
			// producer spends it whether or not the entry resolved.
			_ = crt.Rand()
		}
		if o.particleLife > 0 {
			p.expiry = tick + uint32(o.particleLife)
		}
		o.particles = append(o.particles, p)
	case stripFamilySprinkle:
		// One jittered puff per spawn; exactly three CRT draws of
		// rand×7/0x8000 − 3 per axis [R-STRIP-01 §1 strip 2][R-STRIP-01
		// §3]. The palette pair is 0x61/0x67, selected by the producer's
		// init flag: nonzero selects 0x61, zero selects 0x67 [R-STRIP-01
		// §1 strips 2/7]. TODO(question): the drawn byte's offset inside
		// the sprinkle sub-record — the pair-plus-selection reading is
		// supported inference, not yet a committed field map.
		color := uint8(0x67)
		if o.colorSel != 0 {
			color = 0x61
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
		// TODO(question): the sprinkle puff's per-tick velocity — retail's
		// sub-record advances by a vector derived from the producer's two
		// points (the piece origin and its second effect vertex); the
		// second vertex's derivation is untraced, so the placeholder keeps
		// velocity at zero.
		o.particles = append(o.particles, p)
	case stripFamilyFlame:
		// One animated segment per spawn; exactly one CRT draw for the
		// random start frame [R-STRIP-01 §1 strip 5][R-STRIP-01 §3].
		// TODO(question): the flame segment's travel law between the
		// source and target points is untraced; the placeholder keeps
		// velocity at zero.
		p := stripParticle{
			x: o.src[0], y: o.src[1], z: o.src[2],
			frame: int32(crt.Rand()),
		}
		if o.particleLife > 0 {
			p.expiry = tick + uint32(o.particleLife)
		}
		o.particles = append(o.particles, p)
	case stripFamilyFlameTrail:
		// One animated segment per tick; the strip-7 trail spends no draws
		// [R-STRIP-01 §1 strip 7][R-STRIP-01 §3]. The segment flies from
		// source to target over the flight length.
		p := stripParticle{x: o.src[0], y: o.src[1], z: o.src[2]}
		if o.particleLife > 0 {
			p.expiry = o.windowEnd
			p.vx = divByTicks(o.dst[0].Sub(o.src[0]), o.particleLife)
			p.vy = divByTicks(o.dst[1].Sub(o.src[1]), o.particleLife)
			p.vz = divByTicks(o.dst[2].Sub(o.src[2]), o.particleLife)
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
	// Lifetime bounds the emitter's spawn window. Zero closes the window
	// immediately, so the container spawns once and dies with its particle.
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
	if init.Lifetime > 0 {
		o.windowEnd = tick + uint32(init.Lifetime)
	}
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

// smokeDefaultFrameDelay is the smoke family's animation frame delay when a
// site passes zero: the family constructor defaults the authored delay to 7.
// Each animation-frame advance consumes one CRT draw [R-STRIP-01 §3].
// TODO(question): promote the constructor's default-delay value into the
// committed family contract — it is read off the family constructor directly
// and is not yet stated in the research doc.
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
func (s *Session) appendStripSprinkle(strip int, pos [3]numeric.Fixed, spacing int32, colorSel uint8) {
	if s == nil || s.strips == nil {
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
		colorSel:      colorSel,
		src:           pos,
		// dst stays at the spawn point: the container's second point (the
		// piece's second effect vertex) is untraced — see the spawn
		// TODO(question) on the sprinkle family above.
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
// fifth tick while the next-spawn tick is still within the window, so a vent
// produces thirty-one puffs over five seconds and then stops. It is reached
// only from the feature stamp, so a vent steams when the map places it (and
// again if a save reload or a successor re-stamps it) and not otherwise —
// there is no perpetual plume in retail. Nothing removes an earlier object but
// its own lifetime, so repeated stamps accumulate up to the 401-record bound.
func (s *Session) appendStripGeothermalSteam(pos [3]numeric.Fixed) {
	if s == nil || s.strips == nil {
		return
	}
	tick := uint32(0)
	if s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	o := stripObject{
		family:          stripFamilySmoke,
		src:             pos,
		frameDelayParam: smokeDefaultFrameDelay,
		frameCountBase:  s.smokeEntryFrameCountBase(0),
		windowEnd:       tick + geothermalSteamLifetime,
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

	// geothermalSteamInterval and geothermalSteamLifetime are the producer's
	// first and third init literals [05 R-ECO-02 §3].
	geothermalSteamInterval int32  = 5
	geothermalSteamLifetime uint32 = 150
)

// appendStripParticleViews mirrors every live strip sub-record into the
// committed frame, one view per particle, rebuilt from scratch at every
// publication.
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
// The per-sub-record draw of [03 R-STRIP-01 §2] blits "either a GAF frame or a
// two-by-two filled rectangle", so each view carries one or the other: the
// flame families name the flame-stream entry, the smoke family names one of the
// two smoke entries, and the sprinkle and nano families carry a fill colour.
//
// Strip 6 is deliberately NOT mirrored. The nanolathe spray already has a
// presentation path of its own — the strip-6 stroke gate the composer runs on
// published nanolathe events — and mirroring the particles beside it would draw
// the same spray twice. Folding the two into one is the remaining half of this
// seam and wants the event path retired first.
func (s *Session) appendStripParticleViews(out []frame.EffectView, tick uint32) []frame.EffectView {
	if s == nil || s.strips == nil {
		return out
	}
	id := stripViewIDBase
	for strip := 0; strip < stripCount; strip++ {
		if strip == stripNanolathe {
			continue
		}
		for _, o := range s.strips.strips[strip] {
			graphic, fill := o.drawIdentity()
			if graphic == "" && fill == 0 {
				continue
			}
			for i := range o.particles {
				p := &o.particles[i]
				view := frame.EffectView{
					ID:        id,
					Kind:      frame.KindSmokeStart.String(),
					Strip:     int8(strip),
					StartTick: tick,
					X:         p.x,
					Y:         p.y,
					Z:         p.z,
				}
				if graphic != "" {
					view.Graphic = graphic
					view.SeqA = p.frame
				} else {
					// The sprinkle family's own colour walks its ramp as the
					// puff ages [R-STRIP-01 §1 strips 2/7]; the family default
					// stands in only for a sub-record that carries none.
					view.StripFill = fill
					if p.color != 0 {
						view.StripFill = p.color
					}
				}
				out = append(out, view)
				id++
			}
		}
	}
	return out
}

// drawIdentity is one family's per-sub-record draw form [03 R-STRIP-01 §2]:
// either the GAF entry it blits, or the fill colour of its two-by-two
// rectangle. A family with neither is not mirrored.
func (o *stripObject) drawIdentity() (graphic string, fill uint8) {
	switch o.family {
	case stripFamilySmoke:
		// "the smoke family blits one of two smoke GAF entries selected by an
		// init flag" [03 R-STRIP-01 §2][06 R-WFX-01 §1].
		return smokeEntryForSelector(o.smokeSelector), 0
	case stripFamilyFlame, stripFamilyFlameTrail:
		// "the flame families blit the flame-stream GAF entry".
		return flameStreamEntry, 0
	case stripFamilySprinkle:
		// The sprinkle fills rectangles rather than blitting its carried entry:
		// [R-FX-01 §3] records the puff's `smoke 1` entry as "carried, NEVER
		// drawn — this family fills rectangles".
		return "", sprinkleDefaultColor
	case stripFamilyNano:
		return "", nanoRampBase
	}
	return "", 0
}

const (
	// stripNanolathe is the strip whose spray already has its own presentation
	// path; see appendStripParticleViews.
	stripNanolathe = 6

	// flameStreamEntry is the shared effect bank entry both flame families blit
	// [03 R-FX-01 §3].
	flameStreamEntry = "flamestream"

	// sprinkleDefaultColor is the sprinkle pair's zero-selector colour
	// [R-STRIP-01 §1 strips 2/7]; a live sub-record carries its own walked
	// value and overrides it.
	sprinkleDefaultColor uint8 = 0x67

	// nanoRampBase is the first entry of the nano ramp 0xa1..0xa7
	// [R-STRIP-01 §2].
	nanoRampBase uint8 = 0xa1
)

// stripViewIDBase keeps the mirrored views' synthetic identities clear of the
// effect pool's own allocator, which counts up from one. These views are
// rebuilt every frame and are never matched by identity across frames.
const stripViewIDBase uint32 = 1 << 24

// smokeEntryForSelector maps the init flag to the bound entry
// [06 R-WFX-01 §1]: selector 0 is `smoke 1` and selector 1 is `smoke 2`.
//
// TODO(question): the stock shared bank holds no `smoke 2` entry, so selector
// 1 binds nothing there and the engine's startup slot binder has whatever a
// failed entry lookup leaves it. Which of the two the black emit-sfx puff
// actually draws in a stock install is unresolved; a trace of the startup
// binder's miss path would settle it, and until then a selector-1 emitter
// resolves no frame count and its particles fall back to the unbounded form.
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
	if s == nil || s.effectFrameCount == nil {
		return 0
	}
	n, ok := s.effectFrameCount("", smokeEntryForSelector(selector))
	if !ok || n <= 0 {
		return 0
	}
	return int32(n - 1)
}

// SetEffectEntryFrameCount installs the resolver the strip families ask for an
// effect entry's frame count. It is the same shape as the effect-timing
// resolver next door and is filled by the same composer, from the same bank
// cache, so the frame count a puff's last frame is drawn against and the frames
// the draw pass can actually blit can never come apart.
func (s *Session) SetEffectEntryFrameCount(resolve func(bank, entry string) (int, bool)) {
	if s == nil {
		return
	}
	s.effectFrameCount = resolve
}
