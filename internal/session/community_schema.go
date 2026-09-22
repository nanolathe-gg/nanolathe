package session

import (
	"fmt"
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// communitySchemaState is the session-owned remainder of the Community
// schema-unit entry pass. It is populated only when the resolved feature table
// enables CP-UD-3; Strict therefore carries the zero value and never changes
// commander allocation or later tick work (DESIGN_COMMUNITY_PATCH §4.5).
type communitySchemaState struct {
	active             bool
	mission            *mission.Mission
	playerByStart      [10]int8
	neutralOwner       int8
	deferredPlacements []int
	nextDeferred       int
	diagnostics        []string
}

// configureCommunitySchemaStarts applies the skirmish-only start assignment
// rules before the state snapshots them. When neutral placements exist under
// random starts, the highest-position human and highest-position computer are
// exchanged if the computer is lower, making the final active position the
// designated neutral computer exactly as the source does.
func (s *Session) configureCommunitySchemaStarts(cfg SkirmishConfig, m *mission.Mission, eligible []int, assignment map[int]int) {
	var playerByStart [10]int8
	for i := range playerByStart {
		playerByStart[i] = -1
	}
	if s == nil || !s.Community.SchemaUnits || m == nil || len(m.Units) == 0 {
		s.initCommunitySchema(m, playerByStart, -1)
		return
	}
	hasNeutral := false
	for _, placement := range m.Units {
		if placement.Player == 11 {
			hasNeutral = true
			break
		}
	}
	if hasNeutral && cfg.Location == 0 {
		lastHuman, lastComputer := -1, -1
		humanPosition, computerPosition := -1, -1
		for _, player := range eligible {
			position, ok := assignment[player]
			if !ok {
				continue
			}
			switch {
			case cfg.Players[player].IsHuman() && position > humanPosition:
				lastHuman, humanPosition = player, position
			case cfg.Players[player].IsComputer() && position > computerPosition:
				lastComputer, computerPosition = player, position
			}
		}
		if lastHuman >= 0 && lastComputer >= 0 && computerPosition < humanPosition {
			assignment[lastHuman], assignment[lastComputer] = assignment[lastComputer], assignment[lastHuman]
		}
	}
	for _, player := range eligible {
		position, ok := assignment[player]
		if !ok || position < 0 || position >= len(playerByStart) {
			continue
		}
		playerByStart[position] = int8(player)
	}
	neutralOwner := int8(-1)
	if hasNeutral {
		lastPosition := len(eligible) - 1
		if lastPosition >= 0 && lastPosition < len(playerByStart) {
			candidate := playerByStart[lastPosition]
			if candidate >= 0 && cfg.Players[candidate].IsComputer() {
				neutralOwner = candidate
			}
		}
	}
	s.initCommunitySchema(m, playerByStart, neutralOwner)
}

func (st *communitySchemaState) reset() {
	*st = communitySchemaState{neutralOwner: -1}
	for i := range st.playerByStart {
		st.playerByStart[i] = -1
	}
}

// initCommunitySchema records the already-selected start-position ownership
// and builds the one deferred queue. Equal countdowns retain authored order by
// approved Nanolathe policy (DESIGN_COMMUNITY_PATCH §11 Q11).
func (s *Session) initCommunitySchema(m *mission.Mission, playerByStart [10]int8, neutralOwner int8) {
	if s == nil {
		return
	}
	s.communitySchema.reset()
	if !s.Community.SchemaUnits || m == nil || len(m.Units) == 0 {
		return
	}
	st := &s.communitySchema
	st.active = true
	st.mission = m
	st.playerByStart = playerByStart
	st.neutralOwner = neutralOwner
	for idx := range m.Units {
		placement := &m.Units[idx]
		if placement.CreationCountdown > 0 && placement.UnitName != "" {
			st.deferredPlacements = append(st.deferredPlacements, idx)
		}
	}
	// TODO(question): the source uses an unstable countdown sort and never
	// runs deferred InitialMission text despite author documentation showing
	// one. Maintainer intent would settle both discrepancies. Approved Q11
	// keeps authored order for equal countdowns and runs no deferred script.
	sort.SliceStable(st.deferredPlacements, func(i, j int) bool {
		left := m.Units[st.deferredPlacements[i]].CreationCountdown
		right := m.Units[st.deferredPlacements[j]].CreationCountdown
		return left < right
	})
}

// communitySchemaDefinition performs CP-UD-3's byte-exact linear name scan.
// Catalog.Unit is intentionally not used: the ordinary catalog lookup folds
// ASCII case, while this extension's match is case-sensitive and the first
// record in definition order wins.
func communitySchemaDefinition(cat *content.Catalog, name string) *content.UnitDef {
	if cat == nil {
		return nil
	}
	for _, def := range cat.UnitRecords() {
		if def != nil && def.UnitName == name {
			return def
		}
	}
	return nil
}

// communitySchemaOwner resolves the placement's Player field as a start
// position, not a player number. Player 11 addresses the designated neutral
// computer player. Other values outside 1..10 have no recipient.
func (st *communitySchemaState) communitySchemaOwner(placement mission.UnitPlacement) int8 {
	if st == nil {
		return -1
	}
	if placement.Player == 11 {
		return st.neutralOwner
	}
	position := placement.Player - 1
	if position < 0 || position >= int32(len(st.playerByStart)) {
		return -1
	}
	return st.playerByStart[position]
}

// spawnCommunitySchemaUnit makes one attempt through the normal unit
// allocator. It reads only the CP-UD-3 field set: name, X/Y/Z and Player are
// consumed by this path; health, angle, immunity and editor fields remain
// inert. InitialMission is handled by the initial-pass caller alone.
func (s *Session) spawnCommunitySchemaUnit(placementIdx int, owner int8) *units.Unit {
	if s == nil || s.communitySchema.mission == nil || s.Units == nil || owner < 0 || owner >= 10 {
		return nil
	}
	placements := s.communitySchema.mission.Units
	if placementIdx < 0 || placementIdx >= len(placements) {
		return nil
	}
	placement := placements[placementIdx]
	s.communitySchema.diagnostics = append(s.communitySchema.diagnostics,
		fmt.Sprintf("community schema placement %d attempted unit %q for owner %d", placementIdx, placement.UnitName, owner))
	def := communitySchemaDefinition(s.Catalog, placement.UnitName)
	if def == nil {
		return nil
	}
	x := numeric.Fixed(placement.X)
	y := numeric.Fixed(placement.Y)
	z := numeric.Fixed(placement.Z)
	if s.World != nil {
		cell := s.World.PlotAt(world.WorldToCell(x), world.WorldToCell(z))
		if cell != nil {
			y = numeric.Fixed(int64(cell.Height()) << 16)
		}
	}
	h, err := s.Units.Create(def, uint8(owner), x, y, z)
	if err != nil {
		return nil
	}
	u := s.Units.Unit(h)
	if u != nil && s.Movement != nil {
		s.Movement.EnsureUnit(u)
	}
	return u
}

// spawnInitialCommunitySchema runs the initial (countdown <= 0) pass for the
// player whose commander site is being processed. attempted is raised by a
// matching placement before definition lookup or allocation, so even a typo
// suppresses that player's ordinary commander. The sparse array is local to
// this player pass, matching the source's identifier-reference boundary.
func (s *Session) spawnInitialCommunitySchema(player, startPosition int) (attempted bool) {
	if s == nil || !s.Community.SchemaUnits || !s.communitySchema.active || s.communitySchema.mission == nil {
		return false
	}
	placements := s.communitySchema.mission.Units
	createdSparse := make([]*units.Unit, len(placements))
	for idx, placement := range placements {
		if placement.CreationCountdown > 0 {
			continue
		}
		matches := false
		if int8(player) == s.communitySchema.neutralOwner {
			matches = placement.Player == 11
		} else {
			matches = placement.Player == int32(startPosition+1)
		}
		if !matches {
			continue
		}
		attempted = true
		createdSparse[idx] = s.spawnCommunitySchemaUnit(idx, int8(player))
	}
	if attempted {
		mission.RunInitialMissionsForCreated(placements, createdSparse, s.Units, s.Catalog)
	}
	return attempted
}

// stepCommunitySchema drains every due deferred placement at the approved
// end-of-tick extension boundary (Community design §11 Q13). CreationCountdown is authored in seconds and becomes due
// when it is at or before the tick divided by 30. The sorted cursor makes each
// placement an exactly-once attempt and preserves authored order on ties.
func (s *Session) stepCommunitySchema(tick uint32) {
	if s == nil || !s.Community.SchemaUnits {
		return
	}
	st := &s.communitySchema
	if !st.active || st.mission == nil {
		return
	}
	seconds := tick / 30
	for st.nextDeferred < len(st.deferredPlacements) {
		idx := st.deferredPlacements[st.nextDeferred]
		if idx < 0 || idx >= len(st.mission.Units) {
			st.nextDeferred++
			continue
		}
		placement := st.mission.Units[idx]
		if uint32(placement.CreationCountdown) > seconds {
			break
		}
		st.nextDeferred++
		owner := st.communitySchemaOwner(placement)
		if owner < 0 {
			continue
		}
		// The designated neutral computer accepts only Player 11. A deferred
		// ordinary-position entry that resolves to that player is dropped by
		// the extension contract.
		if placement.Player < 11 && owner == st.neutralOwner {
			continue
		}
		s.spawnCommunitySchemaUnit(idx, owner)
	}
}
