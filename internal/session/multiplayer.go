package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// NewPlaytestSkirmish composes an online battle under Modern gameplay
// (DESIGN_MULTIPLAYER §16.4, §16.6): a skirmish of 2..10 humans on static
// lobby teams, or Survival with 2..3 human survivors and the attacker row,
// either joined by computer rows the room host added. The rows are the human
// seats first, in relay-slot order, then the computers, each hosted by seat 0
// (§6.6), then Survival's attacker; a skirmish seats at most ten rows and no
// more than the map's start positions (OnlineMapCapacity), and Survival at
// most three survivors. The local seat selects presentation only and must be
// a human row. Admission runs before world allocation; broader configurations
// — watcher rows, a computer another seat hosts, cheats, watching, Deathmatch,
// another rule set — remain explicitly refused. The name is the first slice's;
// code and documents cite it.
//
// Every client runs every computer, which borrows its host's perspective as
// retail's hosted computer borrows its machine's sensor picture (§6.2, §6.6).
// A computer has no result row and is never removed online: it plays until it
// is destroyed or the battle ends, and counts as an opponent in the victory
// sweep [08 R-SKIR-01 §3].
//
// Teams are fixed at entry: alliances come from the ally groups and the
// shared-victory bits from the configuration, and online seat commands
// cannot change either (§6.7). Teammates, computers included, share current
// sight and radar through the visibility service's vision teams, as
// Survival's survivors do.
//
// The host's unit restrictions (field 12) are admitted since the first online
// lobby (§16.6): admission requires them to be the set the frozen catalog was
// restricted with, so every seat composes from the same restricted clone and
// records the same set (DESIGN_MODS_MUTATORS §15.5).
func NewPlaytestSkirmish(inputs *content.SimulationInputs, config EffectiveMatchConfig, localSeat uint8, progress content.Progress) (*Session, error) {
	admitted, err := admitMatch(config, inputs)
	if err != nil {
		return nil, err
	}
	r := admitted.request
	refuse := func() (*Session, error) {
		return nil, matchAdmissionError(ErrMatchConfigurationRejected, inputs, "playtest", "a Modern online skirmish of 2..10 human seats or online Survival of 2..3 human survivors, joined by computers the host added up to ten rows, the map's start positions or three survivors, the local seat a human, with no cheats, watchers or Deathmatch (DESIGN_MULTIPLAYER §16.6)")
	}
	if r.RuleName != string(gameplay.Modern) || r.CommanderDeath == 2 || r.CheatsAllowed || r.WatchingAllowed {
		return refuse()
	}
	// The human rows lead, then the host's computers; Survival's attacker is
	// the last row, which configuration validation already requires.
	humans, computers := 0, 0
	for i, seat := range r.Seats {
		switch {
		case seat.Role == MatchRoleHuman && i == humans:
			humans++
		case seat.Role == MatchRoleComputer && i == humans+computers && seat.HostSeat == 0:
			computers++
		case seat.Role == MatchRoleSurvivalAttacker && r.SessionKind == MatchOnlineSurvival:
		default:
			return refuse()
		}
	}
	players := humans + computers
	switch r.SessionKind {
	case MatchOnlineSkirmish:
		if humans < matchMinSeats || players > SkirmishMaxPlayers || len(r.Seats) != players {
			return refuse()
		}
		// The lobby's own cap (§16.6); a map without a network schema that
		// names start positions has none, as the lobby reads it.
		if capacity, err := OnlineMapCapacity(inputs.Catalog(), r.MapName); err == nil && players > capacity {
			return refuse()
		}
	case MatchOnlineSurvival:
		if humans < matchMinSeats || players > OnlineSurvivalMaxSurvivors || len(r.Seats) != players+1 {
			return refuse()
		}
	default:
		return refuse()
	}
	if int(localSeat) >= humans {
		return refuse()
	}
	cfg, options := matchSkirmishSetup(r)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	options.Progress = progress
	return composeSkirmish(skirmishEntry{cfg: cfg, features: r.Community, mission: admitted.mission, inputs: inputs, online: &config, localSeat: localSeat}, options, nil)
}

// setOnlineVisionTeams applies the configuration's online seat relations to
// a freshly composed battle, once at entry, before any coverage is published
// or any tick runs: each computer's borrowed perspective, its host's
// countdown attribution, and the online skirmish's vision teams.
//
// A computer borrows its host seat's perspective (DESIGN_MULTIPLAYER §6.2,
// §6.6), as retail's hosted computer reads the sensor picture of the machine
// that runs it [03 R-VIS-01 §4]: its sensor status and its Permanent LOS
// history bit are its host's, and the host's sensor pass is the one that runs
// pass 4 for its units. The Survival attacker is a scenario row, not a
// computer seat, and borrows nothing (DESIGN_SURVIVAL §4.1).
//
// Under the Modern seat policy (SeatRules.ComputersStopWithHost false) a
// computer outlives its defeated host, and the host's pass, which it reads,
// stays an ordinary viewer's while the computer lives rather than marking
// every unit friendly [03 R-VIS-01 §4] pass 1: the computer keeps normal
// sight, and the defeated human watches with normal fog until its computers
// are gone (DESIGN_MULTIPLAYER §6.6). Binding projects that answer onto the
// visibility service.
//
// Each online skirmish team of two or more rows is one side for sight and
// radar (§6.7 "Allied sight"): every member's coverage reaches every member's
// grids, and the sensor pass treats teammates as its own side, the
// vision-team mechanism Survival's survivors use (DESIGN_SURVIVAL §4.3). A
// computer joins its team as a human does. It is Nanolathe's online policy,
// not retail's, which never merges an ally's current sight [03 §3.2]; teams
// are fixed for the battle, so the coverage reference counts stay balanced. A
// vision team also stamps explored history for every member, since a cell
// any member's perspective sees must not draw as unexplored for it; each
// player keeps its own history bit. A battle with no computer row makes no
// call it did not make before, so a human-only room composes exactly as it
// did.
func (s *Session) setOnlineVisionTeams(r *MatchConfigRequest) {
	if s == nil || r == nil {
		return
	}
	for i := range r.Seats {
		seat := &r.Seats[i]
		if seat.Role != MatchRoleComputer || int(seat.HostSeat) >= len(r.Seats) {
			continue
		}
		if s.Vis != nil {
			s.Vis.SetPerspectiveHost(visibility.PlayerID(i), visibility.PlayerID(seat.HostSeat))
		}
		if s.onlineResults != nil && i < len(s.onlineResults.hosted) {
			s.onlineResults.hosted[i] = int8(seat.HostSeat)
		}
	}
	if s.Vis == nil || r.SessionKind != MatchOnlineSkirmish {
		return
	}
	for group := uint8(0); group < SkirmishDefaultAllyGroup; group++ {
		var team []visibility.PlayerID
		for i := range r.Seats {
			role := r.Seats[i].Role
			if (role == MatchRoleHuman || role == MatchRoleComputer) && r.Seats[i].AllyGroup == group {
				team = append(team, visibility.PlayerID(i))
			}
		}
		if len(team) >= 2 {
			s.Vis.SetVisionTeam(team)
		}
	}
}

func (s *Session) sensorStatus(viewer uint8, u *units.Unit) uint32 {
	if u == nil {
		return 0
	}
	if s.Vis == nil {
		return u.Flags
	}
	return s.Vis.StatusForPerspective(visibility.PlayerID(viewer), uint16(u.Handle), u.AllocationSerial, visibility.PlayerID(u.Owner), u.Flags)
}

// PrepareGrantedBattle finishes ordinary entry dispatch without a wall-clock
// budget or a zero-tick pump. The local relay calls this before its ready barrier.
func (s *Session) PrepareGrantedBattle() error {
	if s == nil || s.onlineResults == nil {
		return fmt.Errorf("nanolathe: granted entry refused: logical path session, providers searched [session], expected a play-test multiplayer battle")
	}
	for i := 0; i < 10 && (s.State != StateBattle || s.IsPendingBattle()); i++ {
		s.Advance()
	}
	if s.State != StateBattle || s.IsPendingBattle() {
		return fmt.Errorf("nanolathe: granted entry failed: logical path session, providers searched [entry dispatch], expected a ready battle")
	}
	return nil
}

// StepGranted runs exactly the next sealed tick, including its executor tail.
// Socket arrival time and host update cadence never choose simulation work.
func (s *Session) StepGranted(tick uint32) error {
	if s == nil || s.onlineResults == nil || s.Clock == nil || s.State != StateBattle || s.IsPendingBattle() || tick == 0 || tick != s.Clock.GlobalTick+1 {
		return fmt.Errorf("nanolathe: granted tick refused: logical path tick %d, providers searched [session], expected the next tick of a ready online battle", tick)
	}
	s.ExecuteStep(StepPlan{run: true, ticks: 1})
	return nil
}

// OnlineBattleEnded reports the shared end boundary; a local result may become
// terminal earlier, while the other seat must still finish its own countdown.
func (s *Session) OnlineBattleEnded() bool {
	return s != nil && s.onlineResults != nil && s.onlineBattleEnded()
}

// Knowledge is checked before a queued build can cancel or replace anything.
// A stale builder remains the ordinary no-op; it discloses no site result.
func (s *Session) onlineBuildSiteKnown(seat uint8, p MobileBuildPayload) bool {
	if s.commandActor(seat, p.Builder) == nil {
		return true
	}
	if s.Catalog == nil || s.Build == nil || s.World == nil || s.Vis == nil {
		return false
	}
	def, ok := s.Catalog.Unit(p.Product)
	if !ok || def == nil {
		return false
	}
	geometry, err := s.Build.StructureGeometry(def, units.StructureFacing(p.Facing))
	if err != nil {
		return false
	}
	extent, err := world.NewFootprintExtent(geometry.FootprintX, geometry.FootprintZ)
	if err != nil {
		return false
	}
	placement, err := world.SnapMobilePlacement(p.Position.X, p.Position.Y, p.Position.Z, extent)
	if err != nil {
		return false
	}
	rect := placement.Rect()
	if !s.World.KnownPlacementSite(rect, &sessionPlacementViewer{vis: s.Vis, local: seat, player: seat}) {
		return false
	}
	height := s.World.SiteHeight(rect.MinX(), rect.MinZ(), geometry.Yard, int(geometry.FootprintX), int(geometry.FootprintZ), def.Waterline)
	return p.Position.Y == numeric.Fixed(int64(height)<<16)
}

// Union admission keeps effect lifetimes/RNG shared; each client's detached
// publication still uses its own sight (DESIGN_MULTIPLAYER §6.3). This is an
// online presentation policy, not a change to the shared pool's admission.
func (s *Session) filterOnlineVisuals(f *frame.Frame) {
	if s.onlineResults == nil {
		return
	}
	visible := func(x, y, z numeric.Fixed) bool {
		return s.Vis != nil && s.Vis.VisiblePoint(visibility.PlayerID(s.ViewingOwner), x, y, z)
	}
	n := 0
	for i := range f.Effects {
		v := &f.Effects[i]
		if visible(v.X, v.Y, v.Z) {
			// Swap preserves distinct reusable duration buffers in discarded slots.
			f.Effects[n], f.Effects[i] = f.Effects[i], f.Effects[n]
			n++
		}
	}
	f.Effects = f.Effects[:n]
	n = 0
	for i := range f.Debris {
		v := &f.Debris[i]
		if visible(v.X, v.Y, v.Z) {
			f.Debris[n], f.Debris[i] = f.Debris[i], f.Debris[n]
			n++
		}
	}
	f.Debris = f.Debris[:n]
}
