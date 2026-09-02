package session

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// Visibility publication lives INSIDE phase 5 [R-CORE-01 §4.4.1] DET-06:
// after the path scheduler and each player's orders/work, that player's unit
// slice is swept stamping coverage per in-game unit. The stamp is
// dirty-checked — a unit's coverage is re-rasterized only when its stored
// stamp cell or sight range changed (a unit that moved in phase 2 is
// re-stamped in the same tick's phase 5; an unchanged unit writes nothing).
// Bulk wipe-and-rebuild happens ONLY at battle entry
// (publishVisibilityForAll) and in the phase-5 commander spawn/defeat
// branches — never per tick. There is no post-phase-12 visibility pass: the
// phase-5 sweep is the final publisher.

// visStamp is the dirty-check key of a unit's last-published stamp
// [R-CORE-01 §4.4.1]: stamp cell (CX, CZ) and sight range. Accessed only by
// handle key — never ranged (I1).
type visStamp struct {
	cx, cz int32
	radius int32
}

// heightByteAt derives the observer emitter from the sea-level-clamped world
// height and the immutable model-top extent [03 §3.2, §3.5].
func heightByteAt(u *units.Unit, seaLevel uint8) uint8 {
	if u == nil {
		return 0
	}
	y := u.Y
	floor := numeric.Fixed(int64(seaLevel)+1) << 16
	if y < floor {
		y = floor
	}
	h := int32(int16(int64(y) >> 16))
	if u.Def != nil {
		h += u.Def.ModelTop
	}
	if h < 0 {
		h = 0
	} else if h > 255 {
		h = 255
	}
	return uint8(h)
}

// observerTile applies the half-height beam shear before converting to the
// 32-pixel coverage grid [03 §3.2, §3.5].
func observerTile(u *units.Unit, emitter uint8) (cx, cz int32) {
	if u == nil {
		return 0, 0
	}
	px := int32(int16(int64(u.X) >> 16))
	pz := int32(int16(int64(u.Z)>>16)) - int32(emitter)/2
	return px >> 5, pz >> 5
}

func radiusFor(u *units.Unit) int32 {
	if u != nil && u.Def != nil && u.Def.SightDistance > 0 {
		return int32(u.Def.SightDistance)
	}
	return 32
}

// publishOne synchronously refreshes one unit's stored observer footprint and
// records its dirty-check key. Event-driven callers (battle entry, capture,
// construction complete, death) publish immediately; the per-tick phase-5
// sweep uses stampPlayerSlice's dirty check instead.
func publishOne(s *Session, u *units.Unit) {
	if s == nil || s.Vis == nil || u == nil || !u.Alive {
		return
	}
	hb := heightByteAt(u, seaLevelFor(s))
	cx, cz := observerTile(u, hb)
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
// nothing. Called after that player's orders/work inside phase 5.
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
		hb := heightByteAt(u, seaLevelFor(s))
		cx, cz := observerTile(u, hb)
		r := radiusFor(u)
		if st, seen := s.visStamps[int(u.Handle)]; seen &&
			st.cx == cx && st.cz == cz && st.radius == r {
			continue // unchanged unit writes nothing [R-CORE-01 §4.4.1]
		}
		s.Vis.Refresh(visibility.ObserverID(u.Handle), visibility.Observer{
			Owner: visibility.PlayerID(u.Owner), CX: cx, CZ: cz,
			HeightByte: hb, Radius: r,
		})
		if s.visStamps == nil {
			s.visStamps = make(map[int]visStamp)
		}
		s.visStamps[int(u.Handle)] = visStamp{cx: cx, cz: cz, radius: r}
	}
}

// stepSensorPhase runs the multi-player sensor state (radar/sonar/jam/cloak
// deadlines, SensorTick) once per tick [03 §3.4] P0-11. [R-SENSOR-01] closes
// the DET-06 seam question: this is not a tick phase of its own — it executes
// inside phase 5's per-player pass, in the LOCAL viewing player's iteration,
// after that player's stamp sweep (and, in retail, after the per-tick minimap
// contacts pass and 30-tick victory/defeat block, immediately before the
// mapped-minimap rebuild; nanolathe's residual deltas are recorded at
// tickPlayers). Callers: tickPlayers calls it unconditionally — SensorTick
// owns the player-count gate. When that gate skips, retail leaves the three
// status bits exactly as unit construction wrote them [R-VIS-01 §4] "Gate",
// so this pass writes nothing either.
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
	type holder struct {
		statusPtr *uint32
		deadPtr   *uint32
		handle    int
	}
	var holders []holder
	var sensorUnits []visibility.SensorUnit
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		h := int(u.Handle)
		stVal := s.visStatus[h]
		sp := new(uint32)
		*sp = stVal
		// The proximity breach's `tick + 90` goes into THE shared
		// reveal/cloak-suppression word, the one the work handlers and the
		// projectile fill also write and the cloak debit gate alone reads
		// [03 R-VIS-01 §6 "Writer census of the shared deadline"]. It used to
		// land in a session-side map that nothing read, so the breach's
		// durable half never reached the gate (WU-19-92).
		dp := &u.RevealDeadline
		holders = append(holders, holder{statusPtr: sp, deadPtr: dp, handle: h})
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
		if u.Def != nil {
			stealth = u.Def.Stealth
			onOffable = u.Def.OnOffable
			rd = u.Def.RadarDistance
			sd = u.Def.SonarDistance
			rj = u.Def.RadarDistanceJam
			sj = u.Def.SonarDistanceJam
			mc = u.Def.MinCloakDistance
			modelTop = u.Def.ModelTop
		}
		sensorUnits = append(sensorUnits, visibility.SensorUnit{
			ID:               uint16(u.Handle),
			Owner:            visibility.PlayerID(u.Owner),
			Status:           sp,
			X:                u.X,
			Z:                u.Z,
			Y:                u.Y,
			Alive:            true,
			Hidden:           hidden,
			Stealth:          stealth,
			Active:           u.Activated,
			OnOffable:        onOffable,
			RadarDistance:    rd,
			SonarDistance:    sd,
			RadarJam:         rj,
			SonarJam:         sj,
			MinCloakDistance: mc,
			ModelTop:         modelTop,
			DecloakDeadline:  dp,
		})
	}
	// The alliance row. AllyGroup 5 is the UNASSIGNED sentinel, not a team:
	// every slot carries it in a default lobby, so equality alone would make
	// every player everyone's ally [08 "Skirmish configuration"] — the same
	// reading teamForOwner already applies. The sensor phase consumes this row
	// for the proximity scan only; pass 1's allied disjunct cannot fire in
	// retail [R-VIS-01 §7].
	allied := func(a, b visibility.PlayerID) bool {
		if a == b {
			return true
		}
		if s.Skirmish.NumPlayers > 0 && int(a) < 10 && int(b) < 10 {
			ga := s.Skirmish.Players[a].AllyGroup
			gb := s.Skirmish.Players[b].AllyGroup
			if ga == SkirmishDefaultAllyGroup || gb == SkirmishDefaultAllyGroup {
				return false
			}
			return ga == gb
		}
		return false
	}
	// The friendly pass's third disjunct: a defeated or observing viewer marks
	// every live unit friendly [R-VIS-01 §4] pass 1. Retail keeps the local
	// player's own slot and the viewing slot as two separate globals and reads
	// the viewing one here; Nanolathe's two coincide today because composition
	// binds the visibility service's viewing slot from LocalOwner. They diverge
	// only in an observer session, which phase 5 does not reach at all — the
	// caller's local-player scan rejects observer records.
	viewer := int(s.LocalOwner)
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
	s.Vis.SensorTick(tick, active, allied, sensorUnits)
	for _, h := range holders {
		s.visStatus[h.handle] = *h.statusPtr
	}
}
