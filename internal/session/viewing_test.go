package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// View changes the observer without requesting a fog/mapping refresh, and does
// not transfer selection or command authority [07 R-CAM-01 §6][03 R-VIS-01 §4].
func TestViewingOwnerPreservesCommandsAndVisibilityCaches(t *testing.T) {
	s := &Session{LocalOwner: 0, ViewingOwner: 0, Econ: &economy.Service{},
		Vis:      visibility.New(minimalTerrain(), visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled),
		Snapshot: frame.NewBuffer(), Units: newSessionFixtureWorld(2, nil)}
	for i := 0; i < 2; i++ {
		s.Econ.Players[i] = economy.Player{Exists: true, ControllerState: uint8(i + 1)}
	}
	def := &content.UnitDef{MaxDamage: 10, Script: fixtureCOBProgram()}
	local, err := s.Units.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	viewed, err := s.Units.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.Units.Unit(local).Flags |= 0x10
	s.Units.Unit(viewed).Flags |= 0x10
	// Distinct masks let the reused frame slot expose a stale-observer copy.
	s.Vis.ByteGrid(0)[0] = 1
	s.Vis.ByteGrid(1)[0] = 2
	s.publishSnapshot(1)
	s.publishSnapshot(2)
	mapping, fog := s.Vis.MappingVersion(), s.Vis.FogVersion()
	valid := s.Vis.FogCacheValid()
	if !s.SetViewingOwner(1) {
		t.Fatal("occupied computer view refused")
	}
	if s.LocalOwner != 0 || s.ViewingOwner != 1 || s.humanUnit(local) == nil || s.humanUnit(viewed) != nil {
		t.Fatal("view switch changed command ownership")
	}
	if s.Vis.MappingVersion() != mapping || s.Vis.FogVersion() != fog || s.Vis.FogCacheValid() != valid {
		t.Fatal("view switch refreshed visibility")
	}
	s.publishSnapshot(3)
	cur := s.Snapshot.Current()
	if cur.ViewingPlayer != 1 || cur.Selection.LocalPlayer != 0 || len(cur.Selection.Handles) != 1 || cur.Selection.Handles[0] != local {
		t.Fatal("committed observer and selected command owner were conflated")
	}
	if len(cur.Visibility.Visible) == 0 || cur.Visibility.Visible[0] != 2 {
		t.Fatal("reused frame restored the previous observer's coverage")
	}
	if s.Vis.MappingVersion() != mapping || s.Vis.FogVersion() != fog {
		t.Fatal("publishing a new observer refreshed visibility caches")
	}
	for _, invalid := range []uint8{2, 10, 255} {
		if s.SetViewingOwner(invalid) || s.ViewingOwner != 1 {
			t.Fatal("invalid viewing slot accepted")
		}
	}
	for _, controller := range []uint8{0, 4} {
		s.Econ.Players[0].ControllerState = controller
		if s.SetViewingOwner(0) {
			t.Fatal("invalid controller accepted")
		}
	}
	s.Econ.Players[0].ControllerState = 3
	s.Econ.Players[0].Side = 10
	if s.SetViewingOwner(0) {
		t.Fatal("non-player side accepted")
	}
}

// Sensor timing follows the viewing player's settlement deadline, while local
// trigger/command identity remains unchanged [03 R-SENSOR-01].
func TestViewingOwnerUsesItsSettlementDeadline(t *testing.T) {
	s := visibilityFixture(t, true) // true-local owner 1
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	for player := uint8(0); player < 2; player++ {
		if _, err := s.Units.Create(def, player, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	s.Econ.Players[1].UpdateTime = 3
	s.Econ.Players[0].UpdateTime = 7
	if !s.SetViewingOwner(0) {
		t.Fatal("view switch refused")
	}
	s.tickPlayers(3)
	if len(s.Vis.SensorInputs()) != 0 {
		t.Fatal("sensor ran at true-local deadline")
	}
	s.tickPlayers(7)
	if len(s.Vis.SensorInputs()) == 0 {
		t.Fatal("sensor skipped viewing player's due entry")
	}
	if s.LocalOwner != 1 || s.ViewingOwner != 0 {
		t.Fatal("player phase overwrote the viewing identity")
	}
}

// Saves carry true-local Human Player, and restoration seeds both identities
// from it rather than persisting the temporary viewing choice [08 "Player records"].
func TestViewingOwnerRestoreSeedsBothIdentities(t *testing.T) {
	s, _ := newRestoreCoreFixture(t, 0)
	s.LocalOwner, s.ViewingOwner = 1, 0
	stage := &RetailBattleStage{Session: s, Image: &save.BattleImage{HumanPlayer: 3}}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatal(err)
	}
	if s.LocalOwner != 3 || s.ViewingOwner != 3 {
		t.Fatal("restore did not seed both identities from Human Player")
	}
}
