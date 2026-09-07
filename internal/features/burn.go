package features

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
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

// burnFrameGeometry is the geometry of the burn animation's CURRENT frame,
// which is what [05 R-FEAT-01 §10] pass 3a scales the smoke jitter by: the
// frame's width and height and its two authored offsets. The names are the
// GAF frame fields [fmt gaf]; the values reach this package through the
// BurnFrameGeometry seam.
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
// the section states. The addends are returned rather than applied so the
// arithmetic can be locked on its own; emitBurnSmoke applies them to world X
// and world height [05 R-FEAT-01 §16].
func burnSmokeJitter(frame burnFrameGeometry, drawX, drawY int32) (dx, dy int32) {
	dx = int32(int16(drawX*(frame.W/2)/32768 - frame.XOff + frame.W/4))
	dy = int32(int16(2*(frame.YOff-drawY*(frame.H/2)/32768) - 2*(frame.H/4)))
	return dx, dy
}

// fireBurnEvent is the burn event of [05 R-FEAT-01 §11], run once per burn on
// the visit the spark countdown reaches zero: the neighbourhood pass, the wind
// pass, then the burn weapon.
func (s *Service) fireBurnEvent(inst *Instance) {
	if inst == nil || s.Terrain == nil {
		return
	}
	cx, cz := inst.CX, inst.CZ
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	sim := s.sim()
	// 1. Neighbourhood: the 7x7 window around the origin, row-major ascending,
	// skipping the origin tile before any legality test, so at most 48
	// candidates and at most 48 draws [05 R-FEAT-01 §11 step 1].
	for dz := -3; dz <= 3; dz++ {
		for dx := -3; dx <= 3; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			s.spreadTo(cx+dx, cz+dz, sim)
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
	// first": with any wind slower than half a tile per probe the repeated
	// tiles are skipped and draws happen only when the tile changes, so the
	// five probes make between zero and five draws depending on wind speed,
	// and a wind fast enough to jump a tile never tests the skipped one. An
	// off-map probe still counts as visited: §11 puts the rejection in the
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
			s.spreadTo(int(px), int(pz), sim)
		}
	}
	// 3. Burn weapon: if `burnweapon` resolved, fire it at the footprint
	// centre `((footprintx + 2x)·8, (footprintz + 2z)·8)` at the bilinear
	// terrain height, owned by the dummy feature unit of [05 R-FEAT-01 §2],
	// through the ordinary weapon request — unconditional on the spread
	// results [05 R-FEAT-01 §11 step 3]. That request builds the impact as a
	// synthetic projectile-shaped record with a NULL shooter and a zeroed side
	// byte and pushes it through the ordinary area enumeration, so the damage
	// awards no veterancy and no kill credit [06 §13.1]; the seam the session
	// binds resolves the weapon name and hands that record to the shared
	// splash entry. The weapon itself is a seam because the projectile and
	// damage subsystem is combat's, which this package cannot import.
	if inst.Def != nil && inst.Def.BurnWeapon != "" && s.BurnWeapon != nil {
		x := footprintCentreWorld(cx, inst.FootprintX)
		z := footprintCentreWorld(cz, inst.FootprintZ)
		// The four-corner bilinear query of [03 §2.3], raw — on the map's last
		// row or column it is retail's −1 sentinel, and the request carries it.
		y := s.Terrain.HeightAt(x, z)
		s.BurnWeapon(inst.Def.BurnWeapon, [3]numeric.Fixed{x, y, z})
	}
}

// spreadTo applies the burn event's candidate chain to one tile, in the
// section's order [05 R-FEAT-01 §11] step 1: the cell must exist, hold a word
// below the sentinel band (a fringe cell's 0xFFFE is rejected here, so only
// anchor cells can catch fire), have no instance, and its definition must be
// `flamable`; only then one simulation draw `boundedDraw(100)`, and ignition
// when the draw is below the CANDIDATE's own `spreadchance` (signed compare of
// the byte) — never the burning feature's. Every cheap rejection precedes the
// draw, so the stream advances only for candidates that survive them.
func (s *Service) spreadTo(tx, tz int, sim *rng.Simulation) {
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	if tx < 0 || tx >= w || tz < 0 || tz >= h {
		return
	}
	idx := tz*w + tx
	cell := s.Terrain.Plot[idx]
	feat := cell.Feature()
	if feat >= 0xFFFB { // empty, fringe, void or the reserved band [GAP T14]
		return
	}
	def, ok := s.Terrain.FeatureDefAt(feat)
	if !ok || def == nil {
		return
	}
	if s.cellHasInstance(idx, def) {
		return
	}
	if !def.Flamable { // flammable flag [02 "Feature record"]
		return
	}
	if sim == nil {
		return
	}
	roll := sim.Uint32n(100)
	if int32(roll) >= def.SpreadChance {
		return
	}
	s.igniteAt(tx, tz, def)
}

// cellHasInstance is retail's "the cell has an instance attached" read against
// this build's records [05 R-FEAT-01 §2][05 R-FEAT-01 §3][05 R-FEAT-01 §15].
// In retail the anchor's instance-attached bit is set by the stamp for every
// 3D definition and by ignition and the die/reclaim transitions for a sprite
// definition; a sprite feature AT REST owns no slot. Nanolathe keeps a
// convenience Instance for every stamped anchor, so membership in the
// instance map is NOT that predicate — reading it as one is what kept every
// resting tree from ever catching fire from a neighbour. The predicate is: a
// 3D definition (always attached), a sprite carrying an event record, or an
// attachment this service does not track (the bit set on a cell it holds no
// record for), which is exactly what ignition refuses on.
func (s *Service) cellHasInstance(idx int, def *content.FeatureDef) bool {
	if !isSpriteDef(def) {
		return true // flag bit 0 clear: the stamp popped a slot [05 R-FEAT-01 §3 step 4]
	}
	if s.hasEventRecordAt(idx) {
		return true
	}
	return s.instances[idx] == nil && s.Terrain.Plot[idx].Occupied()
}

// EventSequence reports the animation sequence a live EVENT record is running
// on this instance, its shadow twin, and the visit index of the frame its
// cursor is on.
//
// It exists because the presentation boundary has to tell the two feature draw
// cases apart. [03 R-RAST-01 §6] gives them: a cell with a live instance blits
// the INSTANCE's shadow cursor frame and then its normal cursor frame, while a
// cell with none blits `seqnameshad`/`seqname` — the definition's rest cursor.
// This build attaches an Instance to every stamped anchor, so "live instance"
// in the retail sense is an event record with a running cursor, and the
// sequence is the one its selector names: burn, death or reclaim
// [05 R-FEAT-01 §10] pass 3.
//
// The visit is the cursor's own frame expressed as that frame's first visit
// under the max(delay, 1) cadence, so a consumer that walks the cadence lands
// on exactly the frame the simulation is on. A resting feature, a 3D
// definition and a record whose cursor holds no sequence all report false,
// which leaves the rest cursor in charge.
func (i *Instance) EventSequence() (name, shadow string, visit int32, ok bool) {
	if i == nil || i.Def == nil || !(i.IsBurning || i.IsAnimating) || !i.cursor.running() {
		return "", "", 0, false
	}
	name = EventSequenceName(i.Def, i.AnimationSelector)
	if name == "" {
		return "", "", 0, false
	}
	return name, eventShadowName(i.Def, i.AnimationSelector), i.cursor.visitIndex(), true
}

// CursorFrame is the live event cursor's frame index — the byte the animating
// save record carries at offset 8 [08 R-SAVE-FEATURE-01].
func (i *Instance) CursorFrame() int32 {
	if i == nil {
		return 0
	}
	return i.cursor.frame
}

// CursorDelay is the live event cursor's per-frame delay countdown
// [05 R-FEAT-01 §10] pass 1. It is not persisted: a reload restarts it at
// frame 0's word (see RestoreAt).
func (i *Instance) CursorDelay() int32 {
	if i == nil {
		return 0
	}
	return i.cursor.delay
}

// hasEventRecordAt reports whether the anchor carries an EVENT animation
// record — burning, dying or reclaiming. That is what retail means by "the cell
// has an instance" for a SPRITE definition [05 R-FEAT-01 §8][05 R-FEAT-01 §9]:
// a sprite feature at rest owns no slot there, while this build attaches an
// Instance to every stamped anchor, so every "no instance" test in the impact
// and ignition paths reads through here (or through cellHasInstance, which
// adds the 3D arm).
func (s *Service) hasEventRecordAt(idx int) bool {
	inst := s.instances[idx]
	return inst != nil && (inst.IsBurning || inst.IsAnimating)
}

// igniteAt is `ignite(x, z, remote = 0)` of [05 R-FEAT-01 §9]. It reports
// whether the ignition happened.
func (s *Service) igniteAt(cx, cz int, def *content.FeatureDef) bool {
	if def == nil || s.Terrain == nil {
		return false
	}
	// Step 1: the definition must have a RESOLVED `seqnameburn` — a name that
	// resolves in the battle's content metadata — else the ignition returns
	// silently. A 3D definition never has one, so 3D features never burn.
	delays := s.sequenceDelays(def, featureAnimSelectorBurn)
	if delays == nil {
		return false
	}
	idx := cz*int(s.Terrain.CellW) + cx
	if idx < 0 || idx >= len(s.Terrain.Plot) {
		return false
	}
	cell := s.Terrain.Plot[idx]
	// "The cell must have no instance" — and in retail a sprite feature AT
	// REST has none: instances are popped for event animations only, the rest
	// cursor living on the catalog record instead [05 R-FEAT-01 §10] pass 1.
	// This build attaches an Instance to every stamped anchor, so the faithful
	// translation of "no instance" is "no event record": a resting feature
	// ignites, one already burning, dying or reclaiming refuses (which is
	// also §5's "a second ignition refuses").
	existing := s.instances[idx]
	if s.hasEventRecordAt(idx) {
		return false
	}
	if existing == nil && cell.Occupied() {
		return false // an attachment this service does not track
	}
	// Step 2: pop a free slot; an empty pool is a silent return, no broadcast.
	// A resting anchor holds no slot, so the record this attaches needs one.
	if s.arenaOccupants() >= FeatureAnimSlots {
		return false
	}
	sim := s.sim()
	if sim == nil {
		return false
	}
	// Step 4, the countdown, one simulation draw:
	//
	//	half      = sparkTicks >> 1
	//	countdown = uint8(boundedDraw(half) + half)
	//
	// The compiled field ALREADY holds ticks — the parser multiplies the
	// authored seconds by thirty and truncates — so the formula halves the
	// ticks: the shipped 5 stores 150 and the countdown is 75..149 visits.
	// The bound's own semantics decide whether a bound below two advances the
	// stream (a half of 0 or 1 draws nothing and returns 0, [01 §7.3]). Retail
	// stores the result as a BYTE, so a spark time whose ticks reach 512
	// wraps; the shipped corpus tops out at 150 ticks.
	half := def.SparkTime >> 1
	countdown := int32(uint8(int32(sim.Uint32n(uint32(half))) + half))

	// Step 3: bind. A resting instance is CONVERTED rather than replaced:
	// retail pops a slot because the resting feature owns none, so the one
	// record this build already has is the same record retail ends up with.
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
	inst.RemoteSuppressed = false // remote = 0: this instance runs its burn event
	inst.BurnCountdown = countdown
	inst.cursor.start(delays) // the burn cursor at frame 0
	inst.Status = 0
	if inst.FootprintX <= 0 {
		inst.FootprintX = 1
	}
	if inst.FootprintZ <= 0 {
		inst.FootprintZ = 1
	}
	if existing == nil {
		// A record built for a cell the service held none for takes the
		// stamp's null-position placement; a CONVERTED record keeps the
		// position the stamp gave it — ignition does not move a feature.
		inst.X = footprintCentreWorld(cx, inst.FootprintX)
		inst.Z = footprintCentreWorld(cz, inst.FootprintZ)
		inst.Y = s.Terrain.HeightAt(inst.X, inst.Z)
	}
	s.setInstance(idx, inst)
	s.attachEventRecord(inst)
	s.Terrain.Plot[idx].SetOccupied(true) // the anchor's instance-attached bit
	// Step 6, the `treeburn` sound at the tile corner, is presentation's.
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
//
// weaponFirestarter is tested here with a plain nonzero compare, not a byte
// mask: content.WeaponDef.Firestarter is already truncated to the loader's
// low byte at compile time, so every caller — this one included — already
// sees the byte retail would test [06 R-WPN-05 §10].
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
	if isCandidate && !s.cellHasInstance(idx, def) {
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
	// Both accumulating branches take the weapon's `[DAMAGE] default` as the
	// 16-bit word retail stores it in [02 "Weapon record"]; the compiled int32
	// is that word sign-extended, so uint16(damage) is the stored word exactly.
	// The definition's `damage` is likewise a stored 16-bit word [02 "Feature
	// record"], and every compare below is unsigned against it.
	hit := uint16(damage)
	threshold := uint16(def.Damage)

	// Step 7 of [05 R-FEAT-01 §8]: a 3D definition always carries an instance,
	// and the hit accrues on the instance's own accumulator with 16-bit wrap;
	// the death transition fires when `damage <= accumulator`, unsigned. It is
	// an ACCUMULATOR, not a countdown: the stamp zeroes it, so `damage = 0`
	// dies on the first hit of any strength, a negative default is added as its
	// wrapped word, and a sum that wraps past 65535 keeps only the low 16 bits
	// and may fall back below the threshold. The save carries this word at
	// 0x06..0x07 and restores it after the stamp [08 R-SAVE-FEATURE-01], which
	// is why a partly damaged wreck reloads partly damaged.
	if inst, ok := s.instances[idx]; ok && inst != nil {
		if inst.Def != nil && inst.Def.Object != "" {
			inst.DamageAccumulator += hit
			if threshold <= inst.DamageAccumulator {
				s.RemoveFeatureAt(cx, cz, CauseDead)
				return true
			}
			return false
		}
	}
	// Step 6 of [05 R-FEAT-01 §8]: no instance, so the running sum lives in
	// the anchor cell's word [02 "Terrain file"] (attachment repurposes that
	// word as the slot index, so the two are never live together). The sum is
	// formed in 32 bits from the two zero-extended 16-bit words and compared
	// unsigned against the definition's 16-bit `damage`: only a sum strictly
	// below the threshold is stored back (truncated to the word), so a sum
	// that exceeds 16 bits always dies — unlike the instance branch, this one
	// never wraps — and a negative default counts as its unsigned word.
	sum := uint32(hit) + uint32(cell.AnchorWord())
	if sum >= uint32(threshold) {
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
	s.Terrain.Plot[anchorIdx].SetAnchorWord(uint16(sum))
	return false
}
