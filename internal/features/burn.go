package features

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// floorShift16 converts a 16.16 fixed value to its integer part, flooring
// rather than truncating toward zero (I3). Go's arithmetic right shift on a
// signed value is already floor, which is the point.
func floorShift16(v int64) int64 { return v >> 16 }

// footprintCentreWorld is one axis of a stamped footprint's centre in world
// 16.16, the point [05 R-FEAT-01 §11] step 3 fires the burn weapon at:
// `(footprint + 2·cell)·8`, i.e. the cell origin plus half the footprint.
// A zero or negative extent is the 1x1 the placement helper normalises to.
func footprintCentreWorld(cell int, extent int32) numeric.Fixed {
	if extent <= 0 {
		extent = 1
	}
	return world.CellToWorld(int32(cell)).Add(numeric.FixedFromInt(int64(extent) * 8))
}

// The animating selectors of the battle-save feature family: 0 burn, 1 death,
// 2 reclaim [R-SAVE-FEATURE-01]. Selector 2 is the record whose
// reclaim-animation bit is set, which is what promotes the successor in
// [05 R-FEAT-01 §5] step 6.
const (
	featureAnimSelectorBurn    uint8 = 0
	featureAnimSelectorDie     uint8 = 1
	featureAnimSelectorReclaim uint8 = 2
)

// featureAnimEnd records one attached sprite animation that completed on this
// visit. Completions are applied after the walk so the instance map is not
// mutated mid-iteration; the walk order (sorted keys, I1) is carried over, so
// burn ends and die/reclaim ends still settle in one deterministic order.
type featureAnimEnd struct {
	idx int
	// burn distinguishes [05 R-FEAT-01 §10] pass 3c (teardown plus a bare
	// `featureburnt` stamp, explicitly NOT the replacement routine) from the
	// die/reclaim branch's §5 step 6 replacement.
	burn bool
}

// burnTick implements the feature burning phase [05 "Feature burning"] [06 §13.1].
// Smoke emission gated on tick%3==0, animation and countdown every tick,
// animation-driven burn completion, one-shot spread+burnweapon event.

func (s *Service) burnTick(tick uint32) {
	// One smoke flag per call, true when global tick %3==0, shared across every
	// burning instance [05 "Feature burning"].
	smoke := tick%3 == 0

	// Iterate deterministically (I1) over sorted keys.
	keys := s.sortedInstanceKeys()
	// Collect finished animations to settle after iteration to avoid map
	// mutation during the loop.
	var finished []featureAnimEnd
	for _, idx := range keys {
		inst := s.instances[idx]
		if inst == nil {
			continue
		}
		if !inst.IsBurning {
			// [05 R-FEAT-01 §10] pass 3, the "sprite instance, burning bit
			// clear" branch: such an attached record is a die or reclaim
			// animation. One visit advances the main cursor and then the
			// shadow cursor when present; the animation is complete on the
			// visit whose advance clears the main cursor's sequence pointer,
			// and its lifetime in visits is the sum over the sequence's frames
			// of max(delay, 1). No sprite animation consumes a draw on any
			// stream — only the burning branch's smoke does [01 §7.5].
			//
			// Retail attaches an active-list slot only for an EVENT animation:
			// a sprite feature at rest is driven by its catalog record's rest
			// cursor (pass 1 of the same section), which has no instance.
			// Nanolathe attaches an Instance to every stamped anchor, so the
			// resting majority has to be told apart here. The discriminator is
			// the animation-record flag, set only for the animating save
			// selectors; a resting feature never carries it.
			if !inst.IsAnimating {
				continue
			}
			// The visit counter and the animation's length in visits are the
			// same two words the burn cursor uses below, because retail
			// advances every sprite cursor with one routine. The records that
			// reach here are the ones the transition of §5 steps 4-5 attaches
			// (Service.transitionFeatureAt) plus those save restore rebuilds.
			inst.BurnTicks++
			if inst.BurnDuration > 0 && inst.BurnTicks >= inst.BurnDuration {
				finished = append(finished, featureAnimEnd{idx: idx})
			}
			continue
		}
		if smoke {
			// Emit smoke particle at footprint centre jittered by presentation
			// stream, not simulation stream [05 "Feature burning"].
			//
			// This is the burning-feature strip-5 smoke producer
			// [R-STRIP-01 §1 strip 5]: one wind-drifted smoke puff every 3rd
			// tick, reached through the BurnSmoke seam the session binds to
			// its strip table.
			//
			// [05 R-FEAT-01 §10] pass 3a gives the jitter law in full. The
			// puff sits at the footprint centre at terrain height, and the two
			// draws — first the horizontal one, then the vertical one, in that
			// order — are each scaled by the CURRENT BURN FRAME's width and
			// height, with the frame's own offsets recentring the result:
			//
			//	x += (draw·(w/2))/32768 - frame.xoff + w/4
			//	y += 2·(frame.yoff - (draw·(h/2))/32768) - 2·(h/4)
			//
			// taking integer parts with 16-bit truncation. burnSmokeJitter
			// below is exactly those two addends.
			//
			// WHICH AXES the two addends move (Supported inference). The
			// smoke-puff family's sub-record carries a raw 16.16 position
			// triple whose per-tick update is `x += windX·8`, `z += windZ·8`,
			// `y += authoredGravity·16` [03 §5.5 "Smoke-puff family"] — so the
			// family's own `y` is world HEIGHT and its `x` is world X, and
			// pass 3a's `x` and `y` are those two words. The factor of two on
			// the y term corroborates it: the projection shears height by half
			// a row (`screenY = worldZ − worldY/2`, [03 §2.5]), so one sprite
			// row is two world height units, while x is one-to-one and carries
			// no factor. The puff's Z therefore stays at the footprint centre.
			// A trace of the producer site would settle it outright.
			//
			// TODO(question): the strip-5 burning-feature puff's own
			// parameters — the smoke variant and the particle life passed to
			// the container's constructor — are still an open item on doc 03's
			// own list ("The strip-5 burning-feature smoke producer's puff
			// parameters (variant, life)", [03 §5.5][R-STRIP-01 §1]). The same
			// trace would settle a second question this site cannot: whether
			// the container's constructor draw lands here, making the emission
			// cost three CRT draws, or whether the producer appends into a
			// container that already exists, making it the two that
			// [01 §7.5 "6 features | CRT | 2 per fire-effect emission"] counts.
			// The census row names the position jitter specifically and cites
			// §12 (reproduction) rather than §10, so it is read here as
			// scoping itself to the jitter, and the family's constructor draw
			// [R-STRIP-01 §2] is left where the family puts it. Decider:
			// a static trace of the phase-6 producer site.
			//
			// The two draws are taken unconditionally — before any seam is
			// consulted — so a session with no producer bound advances the CRT
			// stream exactly as one with a producer does.
			var drawX, drawY int32
			if crt := s.crt(); crt != nil {
				drawX = crt.Rand()
				drawY = crt.Rand()
			}
			if s.BurnSmoke != nil {
				// The base is the footprint centre at the sampled terrain
				// height. The centre is the same one the burn weapon fires at,
				// `((footprintx + 2x)·8, (footprintz + 2z)·8)`
				// [05 R-FEAT-01 §11 step 3].
				px := footprintCentreWorld(inst.CX, inst.FootprintX)
				pz := footprintCentreWorld(inst.CZ, inst.FootprintZ)
				py := inst.Y
				if s.Terrain != nil {
					py = s.Terrain.CoarseHeightAt(int32(inst.CX), int32(inst.CZ))
				}
				if s.BurnFrameGeometry != nil {
					w, h, xoff, yoff := s.BurnFrameGeometry(inst.Def, inst.BurnTicks)
					dx, dy := burnSmokeJitter(burnFrameGeometry{W: w, H: h, XOff: xoff, YOff: yoff}, drawX, drawY)
					px = px.Add(numeric.FixedFromInt(int64(dx)))
					py = py.Add(numeric.FixedFromInt(int64(dy)))
				}
				s.BurnSmoke([3]numeric.Fixed{px, py, pz})
			}
		}
		// Advance burn animation and shadow when present [05 ...].
		inst.BurnTicks++
		// If burn animation finished, clear cell — releases instance and
		// animations — and spawn featureburnt successor when linked [05 ...].
		if inst.BurnDuration > 0 && inst.BurnTicks >= inst.BurnDuration {
			finished = append(finished, featureAnimEnd{idx: idx, burn: true})
			continue
		}
		// Otherwise if countdown nonzero and not remote-suppressed decrement,
		// when it reaches zero fire burn event exactly once [05 ...].
		if inst.BurnCountdown != 0 && !inst.RemoteSuppressed {
			inst.BurnCountdown--
			if inst.BurnCountdown == 0 {
				s.fireBurnEvent(inst, idx)
			}
		}
	}
	// Settle the animations that ended on this visit, in walk order.
	for _, end := range finished {
		inst := s.instances[end.idx]
		if inst == nil {
			continue
		}
		cx, cz := inst.CX, inst.CZ
		if !end.burn {
			// [05 R-FEAT-01 §10] pass 3's die/reclaim completion runs §5
			// step 6's replacement at the anchor with argument 0. Step 6
			// takes `featuredead` for that argument, EXCEPT that an instance
			// whose reclaim-animation bit is set promotes the successor to
			// `featurereclamate` regardless of the argument — which is the
			// whole point of carrying the bit. Selector 2 is that record.
			// A successor word of 0xFFFF makes the stamp a no-op, i.e. final
			// removal, which the replacement path already models as a nil
			// successor definition.
			//
			// This is the REPLACEMENT, not the transition: going back through
			// the transition entry would meet its own record at step 4 and
			// drop the removal, leaving the finished animation on the cell
			// forever.
			var succ *content.FeatureDef
			if inst.Def != nil {
				if inst.AnimationSelector == featureAnimSelectorReclaim {
					succ = inst.Def.FeatureReclamateDef
				} else {
					succ = inst.Def.FeatureDeadDef
				}
			}
			s.replaceFeatureAt(cx, cz, succ)
			continue
		}
		// Burn completion is pass 3c and is deliberately NOT the replacement
		// routine: teardown at the anchor, then a bare `featureburnt` stamp at
		// the snapped footprint centre carrying no position or orientation.
		def := inst.Def
		s.clearFootprint(cx, cz, def)
		if def != nil && def.FeatureBurntDef != nil {
			s.spawnFeatureAt(cx, cz, def.FeatureBurntDef)
		}
	}
}

// burnFrameGeometry is the geometry of the burn animation's CURRENT frame,
// which is what [05 R-FEAT-01 §10] pass 3a scales the smoke jitter by: the
// frame's width and height and its two authored offsets. The names are the
// GAF frame fields [fmt gaf]; the values reach this package through a seam
// that does not exist yet (see the smoke site in burnTick).
type burnFrameGeometry struct {
	W, H       int32
	XOff, YOff int32
}

// burnSmokeJitter returns the two addends [05 R-FEAT-01 §10] pass 3a applies
// to the burning feature's smoke position, given the two CRT draws in the
// order the site consumes them — horizontal first, vertical second, each in
// 0..32767 [01 §7.2]:
//
//	x += (draw·(w/2))/32768 - frame.xoff + w/4
//	y += 2·(frame.yoff - (draw·(h/2))/32768) - 2·(h/4)
//
// Every division is an integer part and the result is truncated to 16 bits, as
// the section states. The addends are returned rather than applied because the
// space the base position lives in is the open question recorded at the call
// site; the addends themselves are pure frame geometry and are unambiguous.
func burnSmokeJitter(frame burnFrameGeometry, drawX, drawY int32) (dx, dy int32) {
	dx = int32(int16(drawX*(frame.W/2)/32768 - frame.XOff + frame.W/4))
	dy = int32(int16(2*(frame.YOff-drawY*(frame.H/2)/32768) - 2*(frame.H/4)))
	return dx, dy
}

// fireBurnEvent runs the three passes in order [05 "Feature burning"].
func (s *Service) fireBurnEvent(inst *Instance, idx int) {
	if inst == nil || s.Terrain == nil {
		return
	}
	cx, cz := inst.CX, inst.CZ
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	sim := s.sim()
	// 1. Neighbourhood spread exactly 48 candidates in 7x7 window around origin
	// row-major ascending skipping origin tile before any legality test [05 ...].
	for dz := -3; dz <= 3; dz++ {
		for dx := -3; dx <= 3; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			tx := cx + dx
			tz := cz + dz
			if tx < 0 || tx >= w || tz < 0 || tz >= h {
				continue
			}
			targetIdx := tz*w + tx
			cell := s.Terrain.Plot[targetIdx]
			// Candidate skipped when off-map, empty, already has instance
			// attached, or its definition not flammable [05 ...].
			if cell.IsEmpty() {
				continue
			}
			if cell.Occupied() {
				continue
			}
			// Also skip if live instance already at that cell (attached).
			if _, ok := s.instances[targetIdx]; ok {
				continue
			}
			feat := cell.Feature()
			if feat >= 0xFFFB { // sentinel band [GAP T14]
				continue
			}
			def, ok := s.Terrain.FeatureDefAt(feat)
			if !ok || def == nil || !def.Flamable { // flammable flag [02 "Feature record"]
				continue
			}
			// Only after every cheap rejection does it draw simulationRandom(100)
			// and ignite when draw is below candidate's own spreadchance — never
			// the burning feature's [05 "Feature burning"] [06 §13.1].
			if sim == nil {
				continue
			}
			roll := sim.Uint32n(100) // [05 "Feature burning"] [06 §13.1]
			if int32(roll) >= def.SpreadChance {
				continue
			}
			// Ignite candidate.
			s.igniteAt(tx, tz, def)
		}
	}
	// 2. Wind embers, exactly five probes [05 R-FEAT-01 §11 step 2]:
	//
	//	pos := (x << 16, z << 16)
	//	five times: pos += 2·wind per axis; tile := (pos.x >> 16, pos.z >> 16)
	//	            if tile == previous: skip
	//	            else previous := tile; apply step 1's legality chain and draw
	//
	// The coordinate is the anchor CELL shifted left 16, not a halved tile
	// coordinate, and the wind vector is the global 16.16 one doubled in 64-bit
	// [R-WIND-01]. The shift back is arithmetic, so it floors west and north of
	// the origin rather than truncating (I3).
	//
	// The skip rule is "same tile as the PREVIOUS probe, the origin for the
	// first" — §11's correction to the earlier "zero wind collapses all five
	// probes onto the origin tile, where they are skipped". That earlier
	// reading gave the right draw count only at still air; the general rule
	// makes the five probes draw between zero and five times depending on wind
	// speed, and a wind fast enough to jump a tile never tests the skipped one.
	// An off-map probe still counts as visited: §11 puts the rejection in the
	// cell lookup, after the same-tile test.
	if s.Wind != nil && sim != nil {
		dx := s.Wind.DirX
		dz := s.Wind.DirZ
		posX := int64(cx) << 16
		posZ := int64(cz) << 16
		prevX, prevZ := int64(cx), int64(cz)
		for step := 0; step < 5; step++ {
			posX += int64(dx) * 2
			posZ += int64(dz) * 2
			px := floorShift16(posX)
			pz := floorShift16(posZ)
			if px == prevX && pz == prevZ {
				continue // same tile as the previous probe [05 R-FEAT-01 §11]
			}
			prevX, prevZ = px, pz
			if px < 0 || int(px) >= w || pz < 0 || int(pz) >= h {
				continue // the cell lookup rejects an off-map probe [05 R-FEAT-01 §11]
			}
			tIdx := int(pz)*w + int(px)
			cell := s.Terrain.Plot[tIdx]
			if cell.IsEmpty() {
				continue
			}
			if cell.Occupied() {
				continue
			}
			if _, ok := s.instances[tIdx]; ok {
				continue
			}
			feat := cell.Feature()
			if feat >= 0xFFFB {
				continue
			}
			def, ok := s.Terrain.FeatureDefAt(feat)
			if !ok || def == nil || !def.Flamable {
				continue
			}
			roll := sim.Uint32n(100)
			if int32(roll) >= def.SpreadChance {
				continue
			}
			s.igniteAt(int(px), int(pz), def)
		}
	}
	// 3. Burn weapon after both spread passes regardless of results, if definition
	// names burnweapon fires ordinary weapon request at footprint centre at
	// sampled terrain height [05 "Feature burning"] [06 §13.1].
	if inst.Def != nil && inst.Def.BurnWeapon != "" {
		ev := BurnWeaponEvent{
			Weapon: inst.Def.BurnWeapon,
			CX:     cx,
			CZ:     cz,
			X:      inst.X,
			Y:      inst.Y,
			Z:      inst.Z,
		}
		// Sample terrain height at footprint centre for Y if Y is zero? Use coarse.
		if s.Terrain != nil {
			ev.Y = s.Terrain.CoarseHeightAt(int32(cx), int32(cz))
		}
		s.BurnWeaponsEmitted = append(s.BurnWeaponsEmitted, ev)
		// In retail routes through ordinary projectile and area-damage subsystem [06 §13.1].
	}
}

// hasEventRecordAt reports whether the anchor carries an EVENT animation
// record — burning, dying or reclaiming. That is what retail means by "the cell
// has an instance" [05 R-FEAT-01 §8][05 R-FEAT-01 §9]: a sprite feature at rest
// owns no slot there, while this build attaches an Instance to every stamped
// anchor, so every "no instance" test in the impact and ignition paths reads
// through here.
func (s *Service) hasEventRecordAt(idx int) bool {
	inst := s.instances[idx]
	return inst != nil && (inst.IsBurning || inst.IsAnimating)
}

// igniteAt performs ignition per [05 "Feature burning"] [06 §13.1].
// Requires burn animation sequence, refuses when cell already has instance,
// takes slot from burning-feature free list (silent no-op if none free).
func (s *Service) igniteAt(cx, cz int, def *content.FeatureDef) bool {
	if def == nil || s.Terrain == nil {
		return false
	}
	// Ignition requires definition to name burn animation sequence [05 ...].
	if def.SeqNameBurn == "" {
		return false
	}
	idx := cz*int(s.Terrain.CellW) + cx
	if idx < 0 || idx >= len(s.Terrain.Plot) {
		return false
	}
	cell := s.Terrain.Plot[idx]
	// "The cell must have no instance" [05 R-FEAT-01 §9 step 1] — and in retail
	// a sprite feature AT REST has none: instances are popped for event
	// animations only, the rest cursor living on the catalog record instead
	// [05 R-FEAT-01 §10] pass 1. This build attaches an Instance to every
	// stamped anchor, so the faithful translation of "no instance" is "no event
	// record": a resting feature ignites, one already burning, dying or
	// reclaiming refuses (which is also §5's "a second ignition refuses").
	//
	// Reading it as "no Instance object" is why nothing could ever catch fire
	// once the impact path was wired: every stamped tree owns one.
	existing := s.instances[idx]
	if s.hasEventRecordAt(idx) {
		return false
	}
	if existing == nil && cell.Occupied() {
		return false // an attachment this service does not track
	}
	// Pools 0x100/0x800/0xD silent fail, successors 0xFFFF [P1-10][P1-15]; burning anim slots 0x800 [P1-10][P1-15].
	if len(s.instances) >= FeatureAnimSlots {
		return false // anim pool 0x800 silent fail [P1-10][P1-15]
	}
	// On success: bind the cell to the slot, start the burn animation and, when
	// named, the burn shadow, mark the instance burning, record the tile, play
	// the burn sound, and draw the countdown [05 "Feature burning"] [P1-10].
	sim := s.sim()
	if sim == nil {
		return false
	}
	// The countdown, one simulation draw [05 R-FEAT-01 §9 step 4]:
	//
	//	half      = sparkTicks >> 1
	//	countdown = uint8(boundedDraw(half) + half)
	//
	// CORRECTION. This site read `def.SparkTime / 30` first, recovering an
	// "authored" seconds value and halving that, on the older text's claim that
	// the shipped spark time of 5 yields a countdown of 2 or 3. [05 R-FEAT-01
	// §9] corrects exactly that: the parser multiplies the authored seconds by
	// thirty and truncates, so the compiled field ALREADY holds ticks and the
	// formula halves the ticks. The shipped 5 stores 150, and the countdown is
	// 75..149 visits — two and a half to five seconds, not two or three visits.
	// [06 §13.1] carries the same correction. The old reading fired the spread
	// and burn-weapon event about thirty times too early.
	//
	// The bound's own semantics decide whether a bound below two advances the
	// stream (a half of 0 or 1 draws nothing and returns 0, [01 §7.3]), and that
	// decision belongs to the stream, not to this call site. Retail stores the
	// result as a BYTE, so a spark time whose ticks reach 512 wraps; the
	// shipped corpus tops out at 150 ticks.
	half := def.SparkTime >> 1
	countdown := int32(uint8(int32(sim.Uint32n(uint32(half))) + half))

	// The burn ends when the burn ANIMATION finishes, not on a tick budget:
	// "if the burn animation has finished, clear the cell" [05 "Feature
	// burning"] [P1-10] forced non-looping finite 46-282 visits one-shot after sparktime countdown then inert.
	// Shipped seqnameburn lifetimes forced non-looping with *(handle+2)=0 [P1-10].
	var duration int32
	if s.BurnAnimationTicks != nil {
		duration = s.BurnAnimationTicks(def)
	}
	// If hook gives 0 (no length known), default to finite 46-282 range midpoint 100 to satisfy forced finite [P1-10][P1-15].
	if duration == 0 {
		duration = 100 // within 46-282 [P1-10], forced non-looping [P1-15]
	}
	// A resting instance is CONVERTED rather than replaced: retail pops a slot
	// because the resting feature owns none, so the one record this build
	// already has is the same record retail ends up with.
	inst := existing
	if inst == nil {
		inst = &Instance{
			Def:        def,
			Terrain:    s.Terrain,
			CX:         cx,
			CZ:         cz,
			FootprintX: def.FootprintX,
			FootprintZ: def.FootprintZ,
		}
	}
	inst.IsBurning = true
	inst.IsAnimating = false
	inst.AnimationSelector = featureAnimSelectorBurn
	inst.BurnCountdown = countdown
	inst.BurnDuration = duration
	inst.BurnTicks = 0
	inst.Status = 0
	if inst.FootprintX <= 0 {
		inst.FootprintX = 1
	}
	if inst.FootprintZ <= 0 {
		inst.FootprintZ = 1
	}
	if existing == nil {
		// World position of the cell: X and Z are the cell origin, Y is the
		// terrain height there [03 §2.1]. These were all three assigned a
		// HEIGHT, which put every burning instance on the diagonal at
		// height-scale coordinates. A CONVERTED record keeps the position the
		// stamp gave it — ignition does not move a feature.
		inst.X = world.CellToWorld(int32(cx))
		inst.Z = world.CellToWorld(int32(cz))
		inst.Y = s.Terrain.CoarseHeightAt(int32(cx), int32(cz))
	}
	s.instances[idx] = inst
	s.Terrain.Plot[idx].SetOccupied(true) // mark instance attached [05 ...]
	// Record tile, play burn sound at tile's world position [05 ...] — presentation only.
	return true
}

// Ignite is the weapon-driven impact entry on a feature cell
// [05 "Feature burning"] [06 §13.1]. It is the whole impact path, not only the
// ignition half: a cell that is an ignition candidate with no instance attached
// ignites and takes NO blast damage; every other case accumulates weaponDamage
// instead. It reports whether the cell ignited.
//
// weaponDamage is a parameter because the alternative is fabricating one. The
// non-ignition branch used to compute a literal 10 and discard it, so feature
// health was never reduced by any weapon that does not start fires.
func (s *Service) Ignite(cx, cz int, weaponFirestarter, weaponDamage int32) bool {
	if s.Terrain == nil {
		return false
	}
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	if cx < 0 || cx >= w || cz < 0 || cz >= h {
		return false
	}
	idx := cz*w + cx
	cell := s.Terrain.Plot[idx]
	// Rejects empty cell and indestructible definition [05 "Feature burning"].
	if cell.IsEmpty() {
		return false
	}
	feat := cell.Feature()
	if feat >= 0xFFFB {
		// Could be fringe; resolve via signed offset [SPEC_CONFLICTS SC6].
		if res, ok := world.ResolveFeature(s.Terrain.Plot, w, h, cx, cz); ok {
			feat = res
		} else {
			return false
		}
	}
	def, ok := s.Terrain.FeatureDefAt(feat)
	if !ok || def == nil {
		return false
	}
	if def.Indestructible {
		return false
	}
	// In multiplayer, client without authority bit sends request instead of acting [05 ...].
	// Not modeled.

	// Ignition candidate when flammable && weapon firestarter nonzero [05 ...].
	// There is no probability roll against firestarter — only nonzero test [05 ...][06 §13.1].
	isCandidate := def.Flamable && weaponFirestarter != 0
	if isCandidate && !s.hasEventRecordAt(idx) {
		// Step 5 of [05 R-FEAT-01 §8]: the entry ignites and RETURNS — the
		// impact deals no blast damage — and it returns whether or not the
		// ignition itself succeeded, so a flammable definition naming no
		// `seqnameburn` simply absorbs the hit. Only "an instance is attached"
		// falls through to the accumulators.
		return s.igniteAt(cx, cz, def)
	}
	// Every other case accumulates damage: a cell with no attached instance
	// accrues against the definition's hit points, and an attached
	// object-based instance accrues on the instance. DamageFeature owns both,
	// including the rule that a burning instance is immune to further blast
	// [05 "Feature burning"].
	s.DamageFeature(cx, cz, weaponDamage)
	return false
}

// DamageFeature applies weapon damage to a feature cell per [05 "Feature burning"] [06 §13.1].
// If burning flame-based instance exists, further blast is ignored [05 "Feature burning"].
func (s *Service) DamageFeature(cx, cz int, damage int32) bool {
	if s.Terrain == nil {
		return false
	}
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	if cx < 0 || cx >= w || cz < 0 || cz >= h {
		return false
	}
	idx := cz*w + cx
	cell := s.Terrain.Plot[idx]
	if cell.IsEmpty() {
		return false
	}
	// Resolve feature index if fringe [SPEC_CONFLICTS SC6].
	featIdx := cell.Feature()
	if featIdx == world.PlotFeatureFringe {
		if res, ok := world.ResolveFeature(s.Terrain.Plot, w, h, cx, cz); ok {
			featIdx = res
		} else {
			return false
		}
		// Use anchor coordinates? For damage, anchor cell holds instance.
	}
	// Step 8 of the damage entry [05 R-FEAT-01 §8]: a SPRITE definition that
	// already carries an instance — burning, dying or reclaiming — has no
	// branch at all, so the impact is discarded. The two accumulating branches
	// below are the no-instance case (step 6, the anchor word) and the 3D case
	// (step 7, the instance's own accumulator), and a sprite with a live record
	// is neither. This is the same "every further cause is inert" rule the
	// same-tick precedence of [05 R-FEAT-01 §5] states from the other side, and
	// it subsumes the older filename-scoped burning guard that stood here:
	// "burning filename-based features ... are immune to further blast-damage
	// accumulation" [06 §13.1] is one consequence of it, not the whole rule.
	// Without this a tree playing its death animation kept accruing damage into
	// the anchor word the animation record had already taken over.
	if inst, ok := s.instances[idx]; ok && inst != nil {
		if inst.Def != nil && inst.Def.Object == "" && (inst.IsBurning || inst.IsAnimating) {
			return false // discarded [05 R-FEAT-01 §8 step 8]
		}
	}
	def, ok := s.Terrain.FeatureDefAt(featIdx)
	if !ok || def == nil {
		return false
	}
	if def.Indestructible {
		return false
	}
	// If cell has instance attached and object-based, damage accumulates on instance.
	if inst, ok := s.instances[idx]; ok && inst != nil {
		if inst.Def != nil && inst.Def.Object != "" {
			inst.Health -= damage
			if inst.Health <= 0 {
				s.RemoveFeatureAt(cx, cz, CauseDead)
				return true
			}
			return false
		}
	}
	// Otherwise damage accumulates on cell's AnchorWord [02 "Terrain file"].
	// Attachment clears accumulator role, so two never simultaneous.
	acc := int32(cell.AnchorWord()) + damage
	if acc >= def.Damage {
		s.RemoveFeatureAt(cx, cz, CauseDead)
		return true
	}
	// Store accumulated damage back to anchor word [02 "Terrain file"].
	// Need to find anchor cell if fringe: resolve anchor index?
	anchorCX, anchorCZ := cx, cz
	if cell.IsFringe() {
		// Resolve anchor via offsets
		dx := int(cell.AnchorDXSigned())
		dz := int(cell.AnchorDZSigned())
		anchorCX = cx + dx
		anchorCZ = cz + dz
	}
	if anchorCX < 0 || anchorCX >= w || anchorCZ < 0 || anchorCZ >= h {
		return false
	}
	anchorIdx := anchorCZ*w + anchorCX
	if anchorIdx < 0 || anchorIdx >= len(s.Terrain.Plot) {
		return false
	}
	s.Terrain.Plot[anchorIdx].SetAnchorWord(uint16(acc))
	return false
}
