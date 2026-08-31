package features

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

// floorShift16 converts a 16.16 fixed value to its integer part, flooring
// rather than truncating toward zero (I3). Go's arithmetic right shift on a
// signed value is already floor, which is the point.
func floorShift16(v int64) int64 { return v >> 16 }

// burnTick implements the feature burning phase [05 "Feature burning"] [06 §13.1].
// Smoke emission gated on tick%3==0, animation and countdown every tick,
// animation-driven burn completion, one-shot spread+burnweapon event.

func (s *Service) burnTick(tick uint32) {
	// One smoke flag per call, true when global tick %3==0, shared across every
	// burning instance [05 "Feature burning"].
	smoke := tick%3 == 0

	// Iterate deterministically (I1) over sorted keys.
	keys := s.sortedInstanceKeys()
	// Collect finished burns to clear after iteration to avoid map mutation during loop.
	var finished []int
	for _, idx := range keys {
		inst := s.instances[idx]
		if inst == nil {
			continue
		}
		if !inst.IsBurning {
			// TODO(question): The complete interpretation of AnimationState and
			// its binding to the death/reclaim animation sequences is unknown;
			// tracing that animation-state/sequence binding would settle how
			// selectors 1/2 advance and when they complete.
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
			// TODO(question): the two draws' jitter law and the puff's spawn
			// offset are untraced. The append itself is blocked on file
			// ownership, not research: features has no session-side port to
			// the strip table, and adding one means a new field on
			// features.Service (outside the strip-producer unit's ownership).
			// The session-side helper (Session.appendStripSmokePuffer(5, …))
			// is ready for that port.
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
			finished = append(finished, idx)
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
	// Process finished burn animations through the established burnt successor
	// path. Restored death/reclaim selectors remain attached records: their
	// animation-state/sequence consumer is not established here.
	for _, idx := range finished {
		inst := s.instances[idx]
		if inst == nil {
			continue
		}
		cx, cz := inst.CX, inst.CZ
		def := inst.Def
		s.clearFootprint(cx, cz, def)
		if def != nil && def.FeatureBurntDef != nil {
			s.spawnFeatureAt(cx, cz, def.FeatureBurntDef)
		}
	}
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
	// 2. Wind embers exactly five steps [05 "Feature burning"].
	// Probe walks in 16.16 tile space from origin adding twice each wind component
	// per step, tests tile each step with same legality chain and draw rule.
	// Zero wind collapses all five probes onto origin where they are skipped,
	// so no draws happen at all [05 ...].
	if s.Wind != nil && sim != nil {
		dx := s.Wind.DirX
		dz := s.Wind.DirZ
		if dx != 0 || dz != 0 {
			// The probe walks in 16.16 TILE space from the origin, adding twice
			// each wind component per step, and tests the tile it lands on with
			// the same legality chain and draw rule as the neighbourhood pass
			// [05 "Feature burning"]. A tile is two cells on a side [03 §2.1],
			// so the origin in tile space is the cell index halved.
			//
			// Zero wind collapses all five probes onto the origin tile, where
			// they are skipped and no draws happen at all; the guard above
			// keeps that exact by not walking.
			//
			// TODO(question): [05 "Feature burning"] describes both spread
			// passes in tiles, while the 7x7 neighbourhood above is implemented
			// over cells. Whether the legality chain reads one cell per tile or
			// all four is not established; this tests the tile's origin cell.
			// The step addition below also assumes the wind components are
			// already in 16.16 tile units — if retail scales them differently
			// the trajectory bends, though the draw count (five probes, zero
			// at still air) does not change.
			tileFX := int64(cx) * 65536 / 2
			tileFZ := int64(cz) * 65536 / 2
			for step := 0; step < 5; step++ {
				tileFX += int64(dx) * 2
				tileFZ += int64(dz) * 2
				// Tile space back to a cell index: floor the 16.16 tile
				// coordinate, then scale by two. Flooring, not truncation —
				// the two disagree west and north of the origin (I3).
				px := int(floorShift16(tileFX)) * 2
				pz := int(floorShift16(tileFZ)) * 2
				if px < 0 || px >= w || pz < 0 || pz >= h {
					continue
				}
				tIdx := pz*w + px
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
				s.igniteAt(px, pz, def)
			}
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
