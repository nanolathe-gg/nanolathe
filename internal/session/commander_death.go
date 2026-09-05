package session

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

const (
	deathmatchCandidateLimit = 9999 // [08 R-SKIR-01 §3]
)

// sideForOwner is the slot's side as the runtime PLAYER RECORD carries it.
//
// [08 R-SKIR-01 §2] "Row-to-player conversion" copies colour and side out of
// the setup row into the player's lobby record at battle entry, and the
// placement stamp copies both again; [08 "Player records"] persists the side
// byte with the rest of the record. The setup record is a pre-battle mirror
// that a load does NOT rebuild — "Load restores the five [rule words] into the
// setup record and the map name", and nothing else — so a restored battle's
// setup rows read back as side 0 for every slot while the player records still
// carry the truth. Reading the side off the setup row is therefore correct
// only until the first save/load; the record is the operand.
func (s *Session) sideForOwner(owner int) (int, bool) {
	if s == nil || owner < 0 || owner >= 10 {
		return 0, false
	}
	if s.Econ != nil && owner < len(s.Econ.Players) && s.Econ.Players[owner].Exists {
		return int(s.Econ.Players[owner].Side), true
	}
	// A battle always has a player table; a session without one is an unwired
	// composition, so the setup row is the only thing left to read.
	if owner < len(s.Skirmish.Players) {
		return s.Skirmish.Players[owner].Side, true
	}
	return 0, false
}

// isCommanderForOwner applies the retail identity check: the dead definition
// name must equal the commander's name on the owner's side record. The
// authored Commander bit is not a substitute for that side identity
// [08 R-SKIR-01 §3].
func (s *Session) isCommanderForOwner(u *units.Unit) bool {
	if s == nil || u == nil || u.Def == nil || s.Catalog == nil {
		return false
	}
	side, ok := s.sideForOwner(int(u.Owner))
	if !ok || side < 0 || side >= len(s.Catalog.Sides) || s.Catalog.Sides[side] == nil {
		return false
	}
	name := strings.TrimSpace(s.Catalog.Sides[side].Commander)
	if name == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(u.Def.UnitName), name)
}

// processPendingCommanderDeaths is called only after the unit finalizer has
// filed normal death accounting. The owner sweep therefore observes the
// decremented live count and cannot run twice for one commander [08
// R-SKIR-01 §3].
func (s *Session) processPendingCommanderDeaths(tick uint32) {
	if s == nil || s.Units == nil || s.result.Ended {
		return
	}
	rule := CommanderDeathMode(s.Skirmish.CommanderDeath)
	for owner := 0; owner < len(s.pendingCommanderDeaths); owner++ {
		if !s.pendingCommanderDeaths[owner] {
			continue
		}
		s.pendingCommanderDeaths[owner] = false
		if rule == CommanderDeathContinues {
			continue
		}
		// Rule 2 runs the same owner sweep as rule 1 [08 R-SKIR-01 §3]. It arms
		// nothing here: the respawn rides the one shared countdown, which the
		// defeat predicate arms on the local slot's settlement due once the
		// sweep has driven the live count to zero. The rule word is read again
		// at the due that takes that countdown below zero, and that is where it
		// selects respawn over the end latch [08 R-SKIR-01 §3] "Defeat
		// detection"[08 R-TRIG-01 §6] "Countdown and latch".
		//
		// **Correction.** A private deathmatch countdown used to be armed here,
		// from the commander's death tick rather than from a settlement due,
		// and decremented on a second private deadline. Retail has one
		// countdown and one deadline for every path.
		s.sweepOwnerAfterCommanderDeath(owner, tick)
	}
}

func (s *Session) sweepOwnerAfterCommanderDeath(owner int, tick uint32) {
	if s == nil || s.Units == nil || owner < 0 || owner >= 10 {
		return
	}
	if s.Units.LiveCountForPlayer(owner) == 0 {
		return
	}
	controlled := false
	if s.Econ != nil {
		p := &s.Econ.Players[owner]
		controlled = p.Exists && !p.IsObserver && (p.ControllerState == 1 || p.ControllerState == 2)
	}
	// IterSliced is player/slot ordered; this loop only mutates death marks,
	// leaving finalization to the normal phase-2 sweep [01 §6.2][08 §3].
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive || u.Dying || int(u.Owner) != owner {
			continue
		}
		if !controlled {
			// [08 R-SKIR-01 §3] splits the sweep by the owner record: a unit
			// whose owner is inactive or not human/computer "is destroyed
			// silently (death kind 3, dying bit set, kill record filed)",
			// where the controlled branch below instead "receives 30000
			// damage from itself with damage kind 3". Both ends carry kind 3;
			// only this one carries no packet, so it writes the kind byte
			// directly and leaves the attacker-side snapshot alone — the
			// snapshot is the damage intake's, and no damage is applied here.
			// The finalizer reads the kind byte for the credit switch, where
			// cause 3 is the loss-only partial path [06 §12.1]; before this
			// the silent branch reached it with no cause at all.
			u.LastDamageCause = uint8(combat.CauseSelfDestruct)
			s.Units.Destroy(u.Handle, units.DeathSelfDestruct)
			continue
		}
		// The ordinary damage path is intentional: the sweep gives controlled
		// units 30000 self-damage so armour/death effects retain their normal
		// accounting [08 R-SKIR-01 §3].
		if s.Combat != nil {
			s.Combat.ApplySelfDestructDamage(s.Units, u.Handle, tick)
		}
	}
}

func (s *Session) respawnLocalCommander() bool {
	if s == nil || s.Units == nil || s.World == nil || s.Catalog == nil {
		return false
	}
	owner := int(s.LocalOwner)
	if owner < 0 || owner >= 10 {
		return false
	}
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive && !u.Dying && int(u.Owner) == owner && s.isCommanderForOwner(u) {
			return true // a duplicate death event cannot create a second commander
		}
	}
	// The respawn creates "the side's commander" for the local slot, and the
	// side is the player record's for the same reason the identity test above
	// reads it there [08 R-SKIR-01 §3][08 R-SKIR-01 §2].
	side, ok := s.sideForOwner(owner)
	if !ok {
		return false
	}
	def, err := skirmishCommander(s.Catalog, side, owner)
	if err != nil {
		return false
	}
	rules, err := world.PlacementRulesForUnit(s.Catalog, def)
	if err != nil {
		return false
	}
	extent, err := world.NewFootprintExtent(3, 3)
	if err != nil {
		return false
	}
	sim := s.SimRNG()
	mapW := int64(s.World.CellW) * 16
	mapH := int64(s.World.CellH) * 16
	// The candidate rectangle removes one tenth of each map dimension from
	// both sides.  Keep the integer division before subtraction: the draw
	// bounds and the coordinate offsets use the same truncated inset
	// [01 §7.1][08 R-SKIR-01 §3].
	insetW, insetH := mapW/10, mapH/10
	boundW, boundH := mapW-2*insetW, mapH-2*insetH
	for attempt := 0; attempt < deathmatchCandidateLimit; attempt++ {
		s.deathmatchAttempts++
		// Candidate draws are deliberately adjacent and in X then Z order. The
		// simulation helper's sub-two rule preserves the retail zero-bound
		// behavior [01 §7.1][08 R-SKIR-01 §3].
		var rx, rz uint32
		if sim != nil {
			if boundW > 0 {
				rx = sim.Uint32n(uint32(boundW))
			}
			if boundH > 0 {
				rz = sim.Uint32n(uint32(boundH))
			}
		}
		xPixels := int64(rx) + insetW
		zPixels := int64(rz) + insetH
		x := numeric.Fixed(xPixels << 16)
		z := numeric.Fixed(zPixels << 16)
		anchor := world.NewFootprintAnchor(world.WorldToCell(x), world.WorldToCell(z))
		rect, err := world.NewFootprintRect(anchor, extent)
		if err != nil {
			continue
		}
		// The side-commander's placement test owns terrain, feature, and map
		// bounds admission. Mobile queries cover all nine cells [04 §6.1].
		if _, err = s.World.CheckPlacement(world.PlacementQuery{Rect: rect, Rules: rules, Mobile: true}); err != nil {
			continue
		}
		if s.Movement != nil && s.Movement.Grid != nil && s.Movement.Grid.FootprintOccupied(movement.Cell{X: anchor.CellX(), Z: anchor.CellZ()}, 3, 3, 0) {
			continue
		}
		y := numeric.Fixed(0)
		if h := s.World.HeightAt(x, z); h != -1 {
			y = h
		}
		// The height gate is a lava/water-sentinel test, not a generic
		// sea-level test.  The map-global lava flag is the established reader
		// for this candidate rejection; ordinary maps may have a non-zero
		// terrain sea level without enabling it [08 R-SKIR-01 §3].
		if s.respawnRejectsSubmerged() && y <= s.World.SeaLevelWorld() {
			continue
		}
		h, err := s.Units.Create(def, uint8(owner), x, y, z)
		if err != nil {
			continue
		}
		if u := s.Units.Unit(h); u != nil {
			if s.Econ != nil {
				p := &s.Econ.Players[owner]
				// The respawn's resource write is established in full
				// [08 R-SKIR-01 §3] "Defeat detection": the storage-bonus
				// setter is applied with the **multiplayer lobby record's**
				// metal and energy shorts × 100, and the same two products
				// are added to the new unit's stored metal/energy (× 0.5 at
				// difficulty 0 and × 0.7 at difficulty 1 when the owner is
				// a computer). Those shorts are written only by the multiplayer
				// lobby, which no single-player session has, so both products
				// are zero on every path this engine can reach: the setter's own
				// 200 floor [08 R-SKIR-01 §5] is the whole effect, and there
				// is no per-unit addition left to make.
				//
				// **Correction.** This used to multiply the *skirmish setup
				// row's* starting metal and energy by 100 whenever the mission
				// was absent or not a skirmish. That row is not the lobby short
				// — it is the [200, 10000] starting-resource ladder that
				// battle entry installs unscaled [08 R-SKIR-01 §5] — and
				// the gate was inverted besides: skirmish is precisely the mode
				// retail names as reaching rule 2 with zero products. The branch
				// installed a capacity two orders of magnitude too large.
				p.InstallStorageBonus(0, 0)
				economy.RebuildCapacity(s.Econ, s.Units)
			}
			if s.Movement != nil {
				s.Movement.EnsureUnit(u)
			}
			// Respawn rebuilds the observer table after the new unit has its
			// movement/occupancy identity; this also repairs any stale owner
			// visibility left by the death path [08 R-SKIR-01 §3][R-ENTRY-01 §7].
			publishVisibilityForAll(s)
		}
		return true
	}
	return false
}

func (s *Session) respawnRejectsSubmerged() bool {
	if s == nil || s.Mission == nil || s.Mission.OTA == nil || s.Mission.OTA.Global == nil {
		return false
	}
	return mission.DecodeMissionGlobals(s.Mission.OTA.Global).LavaWorld != 0
}

// DeathmatchStatus is a compact diagnostic view used by tests and future
// front-end consumers; presentation does not own or mutate this state [I6].
// The countdown it reports is the one shared countdown of [08 R-TRIG-01 §6];
// deathmatch owns no second one.
func (s *Session) DeathmatchStatus() (active bool, countdown int16, attempts uint16, exhausted bool) {
	if s == nil {
		return false, 0, 0, false
	}
	return s.deathmatchActive, s.Latch.Countdown, s.deathmatchAttempts, s.deathmatchExhausted
}

// NotifyDeathFinalized lets non-pump composition seams deliver the same
// owner-level transition as phase-2 finalization. It is intentionally narrow:
// callers provide only the already-filed owner slot [08 R-SKIR-01 §3].
func (s *Session) NotifyDeathFinalized(owner int, tick uint32) {
	if s == nil || owner < 0 || owner >= len(s.pendingCommanderDeaths) {
		return
	}
	if s.pendingCommanderDeaths[owner] {
		s.processPendingCommanderDeaths(tick)
	}
}
