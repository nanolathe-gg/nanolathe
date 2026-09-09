package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Visibility publication lives INSIDE phase 5 [R-CORE-01 §4.4.1] DET-06:
// after the path scheduler and each player's orders/work, that player's unit
// slice is swept stamping coverage per in-game unit. The stamp is
// dirty-checked — but the dirty check is the visibility service's refresh
// throttle [03 R-VIS-01 §2], not a session-side cache, so a unit that moved in
// phase 2 is re-stamped in the same tick's phase 5 and an unchanged unit
// writes nothing. Bulk wipe-and-rebuild happens ONLY at battle entry
// (publishVisibilityForAll) and in the phase-5 commander spawn/defeat
// branches — never per tick. There is no post-phase-12 visibility pass: the
// phase-5 sweep is the final publisher.

// visStamp records the cell and sight range last handed to the visibility
// service for one unit. It is diagnostic and save-restore state, NOT a
// throttle: the throttle compares against the last actual publication and owns
// inputs this key does not carry [03 R-VIS-01 §2]. Accessed only by handle key
// — never ranged (I1).
type visStamp struct {
	cx, cz int32
	radius int32
}

// raisedHeightWord is the observer record's Y [03 R-VIS-01 §2] "The observer
// record": the unit's world height raised to at least (SeaLevel + 1) << 16 and
// narrowed to the signed high word. The raise happens in the record, so BOTH
// rasters see the raised value — neither branch re-derives it.
func raisedHeightWord(u *units.Unit, seaLevel uint8) int32 {
	if u == nil {
		return 0
	}
	y := u.Y
	if floor := numeric.Fixed(int64(seaLevel)+1) << 16; y < floor {
		y = floor
	}
	return int32(int16(int64(y) >> 16))
}

// heightByteAt derives the observer emitter from the sea-level-clamped world
// height and the immutable model-top extent [03 §3.2, §3.5]. It is the
// terrain-ray branch's stored coverage byte; the sprite-mask branch stores the
// quantized shape index instead [03 R-VIS-01 §2].
func heightByteAt(u *units.Unit, seaLevel uint8) uint8 {
	if u == nil {
		return 0
	}
	h := raisedHeightWord(u, seaLevel)
	if u.Def != nil {
		// The model top reaches the emitter as the LOW BYTE of the definition's
		// reference-height word: a model whose top exceeds 255 whole world units
		// wraps here, before the clamp below, rather than saturating
		// [03 R-VIS-01 §2] "The observer record".
		h += int32(uint8(u.Def.ModelTop))
	}
	if h < 0 {
		h = 0
	} else if h > 255 {
		h = 255
	}
	return uint8(h)
}

// observerTile is the TERRAIN-RAY branch's observer cell [03 R-VIS-01 §2]
// "Terrain-ray branch": one floor of (worldZ_high − emitter/2) after the
// half-height beam shear, converted to the 32-pixel coverage grid. The stored
// tile is the observer cell itself.
func observerTile(u *units.Unit, emitter uint8) (cx, cz int32) {
	if u == nil {
		return 0, 0
	}
	px := int32(int16(int64(u.X) >> 16))
	pz := int32(int16(int64(u.Z)>>16)) - int32(emitter)/2
	return px >> 5, pz >> 5
}

// spriteObserverTile is the SPRITE-MASK (Circular) branch's observer cell
// [03 R-VIS-01 §2] "Sprite-mask branch". The two branches do NOT compute the
// same shear, and the difference is authoritative sight, not displayed fog:
//
//   - the ray branch takes ONE floor of (worldZ_high − emitter/2);
//   - the sprite branch takes TWO independent floors,
//     floorDiv(worldZ, 2^21) − floorDiv(worldY_high, 64), which is not the
//     floor of the combined expression;
//   - the sprite branch does NOT add the model top — only the raised Y enters.
//
// Retail then subtracts the authored vismask frame's own signed offsets to
// reach the raster origin. Nanolathe's raster does that itself from the shape
// anchor (visibility.walkSpriteMask), so the offsets are applied exactly once
// and this function returns the observer cell, not the footprint corner. The
// refresh throttle is unaffected by which of the two it compares: the offsets
// are constant for a shape index, and the index is part of the same key.
func spriteObserverTile(u *units.Unit, seaLevel uint8) (cx, cz int32) {
	if u == nil {
		return 0, 0
	}
	// Arithmetic shifts are the floor divisions of [I3]: >>5 on the signed
	// high word is floorDiv(world, 2^21); >>6 is floorDiv(worldY_high, 64).
	px := int32(int16(int64(u.X) >> 16))
	pz := int32(int16(int64(u.Z) >> 16))
	return px >> 5, (pz >> 5) - (raisedHeightWord(u, seaLevel) >> 6)
}

// observerCell derives the observer cell with the raster the mode word's bit 2
// selects [03 R-VIS-01 §2]. Both publishers — the battle-entry bulk publish and
// the per-tick phase-5 sweep — must ask through here; applying the ray shear in
// Circular mode moves what units can see.
func observerCell(s *Session, u *units.Unit, emitter uint8) (cx, cz int32) {
	if s != nil && s.Vis != nil && !s.Vis.TerrainRay() {
		return spriteObserverTile(u, seaLevelFor(s))
	}
	return observerTile(u, emitter)
}

func radiusFor(u *units.Unit) int32 {
	if u != nil && u.Def != nil && u.Def.SightDistance > 0 {
		return int32(u.Def.SightDistance)
	}
	return 32
}

// publishOne hands one unit's observer record to the throttled refresh. Every
// publisher goes through it — battle entry, capture, construction complete,
// death, and the per-tick phase-5 sweep — because the decision to recompute is
// the visibility service's, not the session's [03 R-VIS-01 §2].
//
// The session must NOT pre-filter on its own key. The throttle's inputs differ
// from anything the caller can see from one sweep to the next: the terrain-ray
// branch refreshes when the emitter moved more than five from the LAST
// PUBLISHED byte, so six one-unit climbs must refresh even though no single
// sweep changed the cell; the sprite branch keys on the quantized shape index,
// not the raw sight radius; and both key on the owner, which a capture changes
// without moving the unit.
func publishOne(s *Session, u *units.Unit) {
	if s == nil || s.Vis == nil || u == nil || !u.Alive {
		return
	}
	hb := heightByteAt(u, seaLevelFor(s))
	cx, cz := observerCell(s, u, hb)
	r := radiusFor(u)
	s.Vis.Refresh(visibility.ObserverID(u.Handle), visibility.Observer{
		Owner: visibility.PlayerID(u.Owner), CX: cx, CZ: cz,
		HeightByte: hb, Radius: r,
	})
	if s.visStamps == nil {
		s.visStamps = make(map[int]visStamp)
	}
	s.visStamps[int(u.Handle)] = visStamp{cx: cx, cz: cz, radius: r}
}

// unpublishOne retires the stored footprint. Reconstructing from the unit's
// current position is incorrect after movement or an owner transfer [03 §3.2].
func unpublishOne(s *Session, u *units.Unit) {
	if s == nil || s.Vis == nil || u == nil {
		return
	}
	s.Vis.RetireObserver(visibility.ObserverID(u.Handle))
	if s.visStatus != nil {
		delete(s.visStatus, int(u.Handle))
	}
	if s.visStamps != nil {
		delete(s.visStamps, int(u.Handle))
	}
}

// publishVisibilityForAll is the battle-entry bulk wipe-and-rebuild
// [R-CORE-01 §4.4.1]: every live unit's coverage is published before the
// first frame. IterSliced order is player-ascending then slot-ascending
// [01 §6.2].
func publishVisibilityForAll(s *Session) {
	if s == nil || s.Vis == nil || s.Units == nil {
		return
	}
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive {
			publishOne(s, u)
		}
	}
}

// stampPlayerSlice is the phase-5 per-player stamp sweep [R-CORE-01 §4.4.1]:
// walk player's unit slice slots ascending and re-stamp coverage for each
// in-game unit whose stamp cell or sight range changed. Unchanged units write
// nothing. Called after strategic refresh and before settlement in phase 5.
func stampPlayerSlice(s *Session, player int) {
	if s == nil || s.Vis == nil || s.Units == nil {
		return
	}
	start, end, ok := s.Units.SliceForPlayer(player)
	if !ok {
		return
	}
	for slot := start; slot <= end; slot++ {
		if slot < 0 || slot >= s.Units.TotalRecords() {
			continue
		}
		u := s.Units.Unit(pool.Handle(slot))
		if u == nil || !u.Alive || int(u.Owner) != player {
			continue
		}
		// The throttle lives in visibility.Refresh, which compares against the
		// LAST PUBLICATION rather than against the previous sweep. Skipping the
		// call here on an unchanged cell hid the terrain-ray height test
		// entirely [03 R-VIS-01 §2] "Terrain-ray branch"; see publishOne.
		publishOne(s, u)
	}
}

// stepSensorPhase runs the sensor status and cloak deadline walks inside the
// viewing player's due settlement block, after the settlement gates. The
// service additionally requires more than one active player; skipped passes
// retain their previous status bits [03 R-SENSOR-01][03 R-VIS-01 §4].
func (s *Session) stepSensorPhase(tick uint32) {
	if s.Vis == nil || s.Units == nil {
		return
	}
	if s.visStatus == nil {
		s.visStatus = make(map[int]uint32)
	}
	// SensorTick owns the active-player gate and drops its prior callback
	// snapshot when that gate skips [R-VIS-01 §4] — so this pass calls it
	// unconditionally.
	active := s.activePlayerCount()
	// Scratch, not state: the status row is written before it is read for
	// every handle staged below, and both staging slices are truncated here.
	// The status row is sized to the whole pool up front so the pointers
	// handed to the sensor service stay valid for the length of the pass.
	records := s.Units.TotalRecords()
	if cap(s.sensorStatusScratch) < records {
		s.sensorStatusScratch = make([]uint32, records)
	}
	s.sensorStatusScratch = s.sensorStatusScratch[:records]
	s.sensorHolders = s.sensorHolders[:0]
	sensorUnits := s.sensorUnitScratch[:0]
	// Pass 4's candidate set: the per-side PRIMARY candidate lists of [06 §3.1],
	// folded into one membership bit per player slot so the sensor pass can read
	// "is this unit on my owner's list" without a second lookup structure. The
	// lists keep their own thirty-tick rebuild cadence — the combat service owns
	// it, this is a read of what the last rebuild left [03 R-VIS-01 §4] pass 4.
	// A slot with no registry contributes no bits, so its units never breach,
	// which is the fail-closed direction (WU-19-210).
	primaryOf := s.primaryCandidateMasks()
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		h := int(u.Handle)
		if h < 0 || h >= records {
			continue
		}
		s.sensorStatusScratch[h] = s.visStatus[h]
		sp := &s.sensorStatusScratch[h]
		// The proximity breach's `tick + 90` goes into THE shared
		// reveal/cloak-suppression word, the one the work handlers and the
		// projectile fill also write and the cloak debit gate alone reads
		// [03 R-VIS-01 §6 "Writer census of the shared deadline"]. It used to
		// land in a session-side map that nothing read, so the breach's
		// durable half never reached the gate (WU-19-92).
		dp := &u.RevealDeadline
		s.sensorHolders = append(s.sensorHolders, int32(h))
		// Hidden is the INSTANCE cloak bit — the seen probe's only gate besides
		// the seen bit itself [R-VIS-01 §4] pass 5, [R-VIS-01 §6]. It used to
		// OR in the definition's init_cloaked flag as well; that flag is
		// consumed once, by the constructor, which seeds the cloak-REQUESTED
		// bit from it, and the instance bit read here is set only when the
		// settlement's transition service pays the cloak debit
		// [03 R-VIS-01 §6][05 R-ECO-01 §9] (RWU-19-26).
		// Definition stealth is a different input entirely: it is the contact
		// callback's third reject, so it suppresses radar and sonar detection
		// outright but never line of sight [R-VIS-01 §5]. The two must not be
		// folded together, and neither is reconstructed from presentation bits
		// in Unit.Flags.
		// Unit.Hidden is that instance bit; Unit.IsCloaked is the cloak
		// REQUEST, which this pass must not read (WU-19-92).
		hidden := u.Hidden
		stealth := false
		var rd, sd, rj, sj, mc, modelTop int32
		onOffable := false
		// The proximity pass's definition gate is the DERIVED can-cloak flag,
		// `cloakcost > 0` — the same derivation `Cloak_On`/`Cloak_Off` gate on
		// [04 R-ORD-01 §2] — and never `init_cloaked` and never the instance
		// cloak bit: pass 4 "does not test whether the unit is currently
		// cloaked" [03 R-VIS-01 §4] pass 4.
		canCloak := false
		if u.Def != nil {
			stealth = u.Def.Stealth
			onOffable = u.Def.OnOffable
			rd = u.Def.RadarDistance
			sd = u.Def.SonarDistance
			rj = u.Def.RadarDistanceJam
			sj = u.Def.SonarDistanceJam
			mc = u.Def.MinCloakDistance
			modelTop = u.Def.ModelTop
			canCloak = u.Def.CloakCost > 0
		}
		var mask uint16
		if h >= 0 && h < len(primaryOf) {
			mask = primaryOf[h]
		}
		sensorUnits = append(sensorUnits, visibility.SensorUnit{
			ID:                    uint16(u.Handle),
			Owner:                 visibility.PlayerID(u.Owner),
			Status:                sp,
			X:                     u.X,
			Z:                     u.Z,
			Y:                     u.Y,
			Alive:                 true,
			Dying:                 u.Dying,
			Hidden:                hidden,
			Stealth:               stealth,
			Active:                u.Activated,
			OnOffable:             onOffable,
			RadarDistance:         rd,
			SonarDistance:         sd,
			RadarJam:              rj,
			SonarJam:              sj,
			MinCloakDistance:      mc,
			ModelTop:              modelTop,
			DecloakDeadline:       dp,
			CanCloak:              canCloak,
			OwnerLocallySimulated: s.ownerLocallySimulated(u.Owner),
			PrimaryCandidateOf:    mask,
		})
	}
	// No alliance row reaches the phase any more. Pass 1's allied disjunct
	// cannot fire in retail [R-VIS-01 §7], and pass 4 — the one pass that used
	// to consult a row, to separate hostiles from friends in a live-unit scan —
	// now reads the per-side primary candidate lists, where hostility was
	// settled once at the registry rebuild against the alliance rows economy
	// owns [06 §3.1] (WU-19-210).
	//
	// Defeated/observing friendliness is relative to the viewing player,
	// independently of the true-local command owner [03 R-VIS-01 §4].
	viewer := int(s.ViewingOwner)
	defeated := false
	if s.Econ != nil && viewer >= 0 && viewer < len(s.Econ.Players) {
		p := &s.Econ.Players[viewer]
		// Defeat is derived from the viewer's two unit counters, not from a
		// flag: the defeat predicate for session kinds 2 and 3 is the local
		// live unit count reaching zero [08 R-SKIR-01 §3] "Defeat detection",
		// which is this predicate once the row has created a unit.
		defeated = p.IsObserver || s.ownerEliminated(viewer)
	}
	s.Vis.SetViewerDefeated(defeated)
	s.Vis.SensorTick(tick, active, sensorUnits)
	for _, h := range s.sensorHolders {
		s.visStatus[int(h)] = s.sensorStatusScratch[h]
	}
	s.sensorUnitScratch = sensorUnits[:0]
}

// primaryCandidateMasks folds the per-side PRIMARY candidate lists of [06 §3.1]
// into one membership word per unit handle: bit s is set when that unit was on
// player slot s's primary list at that side's last registry rebuild.
//
// It is a READ of the combat service's registry, never a rebuild: the lists keep
// their own thirty-tick cadence, taken from the strategic refresh's one gate
// [06 §3.1], and the sensor phase must see them exactly as stale as an
// acquisition attempt does — that staleness is the contract, because pass 4's
// breach test is "an enemy I could see up to a second ago is within
// mincloakdistance" [03 R-VIS-01 §4] pass 4.
//
// The row is indexed by handle over the pool's fixed record count, never a map
// (I1). With no combat service there are no lists and no unit ever breaches.
//
// The row is a reused buffer cleared on entry, not state: the sensor pass reads
// it within the tick that builds it and never across ticks.
func (s *Session) primaryCandidateMasks() []uint16 {
	if s == nil || s.Combat == nil || s.Units == nil {
		return nil
	}
	n := s.Units.TotalRecords()
	if n <= 0 {
		return nil
	}
	if cap(s.primaryMaskScratch) < n {
		s.primaryMaskScratch = make([]uint16, n)
	}
	masks := s.primaryMaskScratch[:n]
	for i := range masks {
		masks[i] = 0
	}
	for slot := 0; slot < 10; slot++ {
		for _, h := range s.Combat.PrimaryTargets(uint8(slot)) { // a slice, in registry order (I1)
			if int(h) > 0 && int(h) < n {
				masks[int(h)] |= 1 << uint(slot)
			}
		}
	}
	return masks
}

// ownerLocallySimulated reports whether a player slot's record is active with
// controller type 1 or 2 — the locally simulated human and computer
// controllers, as against 3 (remote) and 0 (empty). It is pass 4's source gate
// [03 R-VIS-01 §4] pass 4, the same predicate the death latch and the paralyzer
// gate read on a victim's owner [06 R-WPN-02 §2].
func (s *Session) ownerLocallySimulated(owner uint8) bool {
	if s == nil || s.Econ == nil || int(owner) >= len(s.Econ.Players) {
		return false
	}
	p := &s.Econ.Players[owner]
	return p.Exists && (p.ControllerState == 1 || p.ControllerState == 2)
}
