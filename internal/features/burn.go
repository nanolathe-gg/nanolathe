package features

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

// floorShift16 converts a 16.16 fixed value to its integer part, flooring
// rather than truncating toward zero (I3). Go's arithmetic right shift on a
// signed value is already floor, which is the point.
func floorShift16(v int64) int64 { return v >> 16 }

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
			// advances every sprite cursor with one routine.
			//
			// Unimplemented seam, not research: nothing attaches a die or
			// reclaim animation at runtime yet. §5 steps 4-5 (a transition
			// with a named `seqnamedie`/`seqnamereclamate` attaches an
			// instance instead of replacing at once) lives in the transition
			// entry in service.go, and the sequence-length source for those
			// two sequences has no seam the way the burn sequence has
			// Service.BurnAnimationTicks. Until both land, only a save-restored
			// selector-1/2 record reaches this branch and it retires only when
			// a length is known. See PLAN 19 §2.4.
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
			// This is also the burning-feature strip-5 smoke producer
			// [R-STRIP-01 §1 strip 5]: one wind-drifted smoke puff every 3rd
			// tick, with exactly two CRT jitter draws at the call site — the
			// draws below are those two, and the puff would append a strip-5
			// smoke container via the session's appendStripSmokePuffer.
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
			// below returns exactly those two addends; the site cannot call it
			// with real geometry yet, because two things it needs are absent
			// and neither of them is research. The burn cursor's current GAF
			// frame geometry does not reach this service
			// (there is no seam for it the way Service.BurnAnimationTicks is a
			// seam for the sequence length), and features has no session-side
			// port to the strip table, so the container cannot be appended —
			// the session helper Session.appendStripSmokePuffer(5, …) is ready
			// for that port and only the Service field and its wiring are
			// missing. See PLAN 19 §2.4.
			//
			// TODO(question): the strip-5 burning-feature puff's own
			// parameters — the smoke variant and the particle life passed to
			// the container's constructor — are still an open item on doc 03's
			// own list ("The strip-5 burning-feature smoke producer's puff
			// parameters (variant, life)", [03 §5.5][R-STRIP-01 §1]). Decider:
			// a static trace of the phase-6 producer site, which would name
			// both arguments and settle whether the jittered pair above is the
			// container's world position or a sprite-space offset applied to
			// it — §10 pass 3a states the arithmetic but not its space.
			//
			// The two draws below are the right two draws in the right place,
			// so the CRT stream stays correct while the puff is missing
			// [01 §7.5 "6 features | CRT | 2 per fire-effect emission"].
			if crt := s.crt(); crt != nil {
				_ = crt.Rand()
				_ = crt.Rand()
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
			cause := CauseDead
			if inst.AnimationSelector == featureAnimSelectorReclaim {
				cause = CauseReclaim
			}
			s.RemoveFeatureAt(cx, cz, cause)
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
	// Refuse when cell already has instance attached [05 ...].
	if cell.Occupied() {
		return false
	}
	if _, ok := s.instances[idx]; ok {
		return false
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
	// countdown = simulationRandom(sparktime/2) + (sparktime/2), one draw [P1-10] 48+5wind etc.
	// Written literally: the stream's own bound semantics decide whether a bound below two advances it (PLAN_03 C-rng),
	// and that decision belongs to the stream, not to this call site. Shipped spark time is 5, giving a countdown of 2 or 3.
	//
	// The countdown counts VISITS, so the formula consumes the AUTHORED
	// sparktime; the compiled field is ×30 truncated ticks [02 "Feature
	// record"] (I8), so the authored value is recovered once at this
	// documented boundary. One-shot after sparktime countdown then inert [P1-10].
	authored := def.SparkTime / 30
	half := authored / 2
	countdown := int32(sim.Uint32n(uint32(half))) + half

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
	inst := &Instance{
		Def:           def,
		Terrain:       s.Terrain,
		CX:            cx,
		CZ:            cz,
		IsBurning:     true,
		BurnCountdown: countdown,
		BurnDuration:  duration,
		BurnTicks:     0,
		FootprintX:    def.FootprintX,
		FootprintZ:    def.FootprintZ,
		Status:        0,
	}
	if inst.FootprintX <= 0 {
		inst.FootprintX = 1
	}
	if inst.FootprintZ <= 0 {
		inst.FootprintZ = 1
	}
	// World position of the cell: X and Z are the cell origin, Y is the terrain
	// height there [03 §2.1]. These were all three assigned a HEIGHT, which put
	// every burning instance on the diagonal at height-scale coordinates.
	inst.X = world.CellToWorld(int32(cx))
	inst.Z = world.CellToWorld(int32(cz))
	inst.Y = s.Terrain.CoarseHeightAt(int32(cx), int32(cz))
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
	if isCandidate && !cell.Occupied() {
		if _, attached := s.instances[idx]; !attached {
			// Ignites, and the impact deals no blast damage [05 "Feature burning"].
			return s.igniteAt(cx, cz, def)
		}
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
	// If burning instance attached and definition filename-based, immune [05 ...].
	if inst, ok := s.instances[idx]; ok && inst != nil && inst.IsBurning {
		// Need to check filename-based: def.Filename != "" covers shipped ignitable [05 ...].
		if inst.Def != nil && inst.Def.Filename != "" {
			return false // ignored [05 "Feature burning"]
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
