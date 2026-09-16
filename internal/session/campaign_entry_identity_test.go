package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

// Player account projection and restored commander recognition must agree for
// both frontend choices [08 R-CAMP-01 §3][08 "Player records"].
func TestCampaignSelectedSidePersistsCommanderIdentity(t *testing.T) {
	for _, side := range []int{0, 1} {
		cat := minimalCatalogForStrict()
		s := &Session{Catalog: cat, Clock: &clock.State{}, Econ: &economy.Service{}, Mission: &mission.Mission{Type: mission.TypeCampaign}}
		for i := 0; i < 2; i++ {
			s.Econ.Players[i].Exists = true
			s.Econ.Players[i].Logo = uint8(i)
		}
		stampCampaignPlayerSides(s, side)
		projection, err := ProjectRetailSession(s, RetailSaveInputs{Mapping: []byte{0}})
		if err != nil {
			t.Fatal(err)
		}
		if len(projection.Players) != 2 || projection.Players[0].Side != uint8(side) || projection.Players[1].Side != uint8(1-side) {
			t.Fatalf("projected sides: %+v", projection.Players)
		}
		restored := &Session{Catalog: cat, Clock: &clock.State{}, Econ: &economy.Service{}, Mission: s.Mission, Units: units.NewSliced(8, cat)}
		err = RestoreRetailBattleCore(&RetailBattleStage{Session: restored, Image: &save.BattleImage{Players: projection.Players}})
		if err != nil {
			t.Fatal(err)
		}
		for owner, want := range []int{side, 1 - side} {
			if projection.Players[owner].Logo != uint8(owner) || restored.Econ.Players[owner].Logo != uint8(owner) {
				t.Fatalf("owner %d colour changed with selected side %d", owner, side)
			}
			got, known := restored.SideForOwner(owner)
			if !known || got != want || int(restored.Econ.Players[owner].Side) != want {
				t.Fatalf("owner %d side %d known %v", owner, got, known)
			}
			def := cat.Units[cat.Sides[want].Commander]
			if !restored.missionTriggerContext(0).IsCommander(&units.Unit{Owner: uint8(owner), Def: def}) {
				t.Fatalf("owner %d lost commander identity", owner)
			}
		}
	}
}

// Fresh campaign colours belong to player slots, not the selected faction or
// the presence of a campaign wrapper [08 R-CAMP-01 §3][08 R-SKIR-01 §8].
func TestFreshCampaignPlayerColors(t *testing.T) {
	f := loadRetailFixture(t)
	for _, tc := range []struct {
		name    string
		path    string
		options MissionEntryOptions
		side    int
		known   bool
	}{
		{"selected ARM", "camps/Arm Campaign.tdf:MISSION0", MissionEntryOptions{SelectedSide: 0, SelectedSideSet: true}, 0, true},
		{"selected CORE", "camps/Core Campaign.tdf:MISSION0", MissionEntryOptions{SelectedSide: 1, SelectedSideSet: true}, 1, true},
		{"authored ARM", "camps/Arm Campaign.tdf:MISSION0", MissionEntryOptions{}, 0, true},
		{"authored CORE", "camps/Core Campaign.tdf:MISSION0", MissionEntryOptions{}, 1, true},
		{"bare mission", "AC01.ota", MissionEntryOptions{}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewMissionWithEntryOptions(f.fs, f.cat, tc.path, 0, 7, 7, tc.options, nil)
			if err != nil {
				t.Fatal(err)
			}
			s.publishSnapshot(0)
			for owner := 0; owner < 2; owner++ {
				if got := s.Econ.Players[owner].Logo; got != uint8(owner) {
					t.Fatalf("owner %d live colour = %d, want %d", owner, got, owner)
				}
				if got := s.Snapshot.Current().Players[owner].Logo; got != uint8(owner) {
					t.Fatalf("owner %d committed colour = %d, want %d", owner, got, owner)
				}
				wantSide := tc.side
				if owner == 1 {
					wantSide = 1 - wantSide
				}
				if side, known := s.SideForOwner(owner); known != tc.known || known && side != wantSide {
					t.Fatalf("owner %d side = %d/%v, want %d/%v", owner, side, known, wantSide, tc.known)
				}
			}
		})
	}
}

func TestCampaignLocalPreloadPreservesEntryPrime(t *testing.T) {
	s := &Session{State: StateLocalPreload, Clock: &clock.State{}, Econ: &economy.Service{}, battleEntryTailDone: true, EnemyOwner: 1}
	s.Econ.Players[0].UpdateTime = 30
	s.Econ.Players[0].WinLoseTime = 17
	s.Econ.Players[0].EndGameCountdown = 4
	handleLocalPreload(s)
	if s.State != StateLoading || s.Econ.Players[0].UpdateTime != 30 || s.Econ.Players[0].WinLoseTime != 17 || s.Econ.Players[0].EndGameCountdown != 4 {
		t.Fatalf("preload overwrote primed player: %+v", s.Econ.Players[0])
	}
	fresh := &Session{State: StateLocalPreload, Clock: &clock.State{GlobalTick: 9}, Econ: &economy.Service{}}
	handleLocalPreload(fresh)
	if !fresh.Econ.Players[0].Exists || fresh.Econ.Players[1].ControllerState != 2 || fresh.Econ.Players[0].UpdateTime != 9 || fresh.EnemyOwner != 1 {
		t.Fatal("uncomposed local preload skipped setup")
	}
}

func TestCampaignResultStartUsesSelectedAuthoredIndex(t *testing.T) {
	c := NewPostBattleController(frame.ResultView{Ended: true, Kind: "defeat"}, PostBattleConfig{Kind: PostBattleCampaign, MissionIndex: 2, HasNext: true})
	c.state = PostBattleEndMission
	if !c.SelectMission(7) || !c.Handle(PostBattleControlStart, 0) {
		t.Fatal("selection was refused")
	}
	index, ok := c.SelectedMission()
	if !ok || index != 7 {
		t.Fatalf("Start selected %d/%v", index, ok)
	}
}
