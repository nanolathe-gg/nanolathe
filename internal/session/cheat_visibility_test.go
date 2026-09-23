package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// The +LOS cheat toggles mode bit 1 (current-sight tracking) from whatever the
// skirmish setup record put there; it is not a one-way reveal. A Permanent
// start (LineOfSight 0) therefore turns enforcement ON with the first press,
// and a True/Circular start turns it OFF. Bits 0 and 2 are untouched and the
// setup record is never rewritten [07 R-CAM-01 §6][03 R-VIS-01 §1].
func TestLOSCheatTogglesFromSkirmishStart(t *testing.T) {
	los := HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}}
	for _, tc := range []struct {
		name                 string
		mapping, lineOfSight int
		wantStart            visibility.Mode
	}{
		{"permanent mapped", 0, 0, visibility.ModeTerrainRay},
		{"true unmapped", 1, 1, visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{Catalog: minimalCatalogForStrict(), World: minimalTerrain(), Mission: &mission.Mission{Type: mission.TypeSkirmish}}
			w, _ := newSlicedWorld(s.Catalog)
			s.Units = w
			s.Econ = economyForTest()
			s.Econ.Players[0].Exists = true
			s.Econ.Players[0].ControllerState = 1
			s.Skirmish = SkirmishConfig{Mapping: tc.mapping, LineOfSight: tc.lineOfSight, LOSType: 1}
			setup := s.Skirmish
			if err := createAndBindServicesForTest(t, s); err != nil {
				t.Fatalf("bind: %v", err)
			}
			start := visibilityModeForSession(s)
			if start != tc.wantStart {
				t.Fatalf("battle-entry mode = %#x, want %#x", start, tc.wantStart)
			}
			s.Vis.SetMode(start)
			s.applyHumanCommand(los, 0)
			if got := s.Vis.Mode(); got != start^visibility.ModeCurrentEnabled {
				t.Fatalf("first +LOS mode = %#x, want %#x", got, start^visibility.ModeCurrentEnabled)
			}
			if tc.lineOfSight == 0 && !s.Vis.CurrentEnabled() {
				t.Fatal("+LOS from a Permanent start must enable current-sight tracking")
			}
			s.applyHumanCommand(los, 0)
			if got := s.Vis.Mode(); got != start {
				t.Fatalf("second +LOS mode = %#x, want start %#x", got, start)
			}
			if s.Skirmish != setup {
				t.Fatalf("+LOS rewrote the setup record: %+v", s.Skirmish)
			}
		})
	}
}

// Campaign has no cheat route outside developer mode, so the mask-2 visibility
// cheats are inert there [07 R-CAM-01 §6][08 R-OOS-01 §5].
func TestLOSCheatInertInCampaign(t *testing.T) {
	s := &Session{Catalog: minimalCatalogForStrict(), World: minimalTerrain(), Mission: &mission.Mission{Type: mission.TypeCampaign}}
	w, _ := newSlicedWorld(s.Catalog)
	s.Units = w
	s.Econ = economyForTest()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	before := s.Vis.Mode()
	s.applyHumanCommand(HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}}, 0)
	if s.Vis.Mode() != before {
		t.Fatalf("campaign +LOS changed mode %#x -> %#x", before, s.Vis.Mode())
	}
}
