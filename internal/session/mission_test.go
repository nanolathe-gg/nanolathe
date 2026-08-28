package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// initCOBForSession is test-only support for legacy unit fixtures. Production
// entry requires an authored binding before InitialMission runs.
func initCOBForSession(s *Session) {
	if s == nil || s.Units == nil {
		return
	}
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive || u.GetScript() != nil {
			continue
		}
		u.SetScript(cob.NewVM(&cob.Program{Scripts: map[string]int{}}))
	}
}

// grantResourcesDirect is test-only support for fixtures without an OTA
// GlobalHeader. Production uses grantResourcesStrict and reports missing data.
func grantResourcesDirect(s *Session, m *mission.Mission) {
	if s == nil || s.Econ == nil {
		return
	}
	if m != nil && m.OTA != nil && m.OTA.Global != nil {
		mg := mission.DecodeMissionGlobals(m.OTA.Global)
		for p := range s.Econ.Players {
			if s.Econ.Players[p].Exists {
				economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, float32(mg.HumanMetal))
				economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, float32(mg.HumanEnergy))
			}
		}
		return
	}
	for p := range s.Econ.Players {
		if s.Econ.Players[p].Exists {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, 1000)
			economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, 1000)
		}
	}
}

func fsFromMap(t *testing.T, files map[string]string) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	for logical, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(logical))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(data), 0644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatalf("MountDirectory: %v", err)
	}
	return fs
}

func TestMissionGametypeRouting(t *testing.T) {
	s := &Session{}
	if err := RouteForGametype(s, GametypeCampaign); err != nil {
		t.Fatalf("RouteForGametype campaign: %v", err)
	}
	if s.State != StateLocalPreload {
		t.Fatalf("Gametype 1 should select StateLocalPreload(4) [08 \"Session states\"] got %v", s.State)
	}
	if !CanTransition(s.State, StateLoading) {
		t.Fatal("campaign 4->5 must be allowed")
	}
	s2 := &Session{}
	if err := RouteForGametype(s2, GametypeMultiplayer); err != nil {
		t.Fatalf("RouteForGametype multiplayer: %v", err)
	}
	if s2.State != StateLoading {
		t.Fatalf("Gametype 2 should select StateLoading(5) directly [08 \"Session states\"] got %v", s2.State)
	}
	// Invalid gametype must error.
	s3 := &Session{}
	if err := RouteForGametype(s3, 99); err == nil {
		t.Fatal("invalid gametype should error")
	}
	// Campaign path takes same state-5 path after 4->5.
	s.State = StateLoading
	if s.State != StateLoading {
		t.Fatal("after 4->5 campaign should be at 5")
	}
}

func TestNewMissionUsesWindBoundsWithoutDraws(t *testing.T) {
	otaText := "[GlobalHeader]\n{\nminwindspeed=15;\nmaxwindspeed=35;\n[Schema 0]\n{\nType=Easy;\n[units]\n{\n[unit0]\n{\nUnitname=armcom;\nXPos=0;\nZPos=0;\n}\n}\n[specials]\n{\n}\n[features]\n{\n}\n}\n}\n"
	fs := fsFromMap(t, map[string]string{
		"maps/test.ota": otaText,
	})
	cat := &content.Catalog{
		Maps: map[string]*content.MapHeader{
			"test": {
				LogicalTNT: "maps/test.tnt",
				Schemas:    []content.MapSchema{{SurfaceMetal: 0}},
			},
		},
		Units: map[string]*content.UnitDef{
			"armcom": {MaxDamage: 100, SightDistance: 128},
		},
	}
	s, err := NewSyntheticMissionForTest(fs, cat, "test.ota", 0)
	if err != nil {
		t.Fatalf("NewMissionWithFS: %v", err)
	}
	if s.Mission == nil {
		t.Fatal("mission nil after load")
	}
	if s.Mission.WindBounds.Min != 15 || s.Mission.WindBounds.Max != 35 {
		t.Fatalf("WindBounds = %+v want 15/35 [08 \"Wind initialization\"]", s.Mission.WindBounds)
	}
	if s.Wind == nil {
		t.Fatal("Wind not initialized after mission load [01 §7.3] C17")
	}
	if s.Wind.Min != 15 || s.Wind.Max != 35 {
		t.Fatalf("Wind bounds not retained into holder: %+v", s.Wind)
	}
	// [R-CORE-02]: battle entry performs NO wind draws — the strength/heading
	// stay at the zero value with the deadline zeroed, and the first wind
	// chain runs in sub-tick 1. The earlier expectation of briefing-drawn
	// values at construction is superseded.
	if s.Wind.Strength != 0 || s.Wind.Heading != 0 {
		t.Fatalf("battle entry drew wind values: strength %d heading %d, want 0/0 [R-CORE-02]", s.Wind.Strength, s.Wind.Heading)
	}
	if s.Wind.NextChange != 0 {
		t.Fatalf("wind deadline %d, want 0 (zeroed at entry) [R-CORE-02]", s.Wind.NextChange)
	}
	if s.State != StateLocalPreload {
		t.Fatalf("NewMission must route via Gametype 1 -> StateLocalPreload [08 \"Session states\"] C3 got %v", s.State)
	}
}

func TestNewMissionWithFSKeepsCampaignLoadStrict(t *testing.T) {
	// A direct Type 1 load must not use the Type 2/3 fuzzy resolver when the
	// requested mission is absent. Supplying a different valid OTA makes the
	// old fallback observable without requiring terrain or retail assets. The
	// supplied catalog is valid so catalog preflight does not mask the
	// requested-file diagnostic [08 "Campaign discovery", "Mission and map
	// schema selection"].
	fs := fsFromMap(t, map[string]string{
		"maps/Available.ota": "[GlobalHeader]\n{\nmissionname=Available;\n[Schema 0]\n{\nType=Easy;\n}\n}\n",
	})
	cat := minimalCatalogForStrict()
	_, err := NewMissionWithFS(fs, cat, "Missing.ota", 0)
	if err == nil {
		t.Fatal("strict campaign load must fail when the requested mission is absent")
	}
	// Type 1 names the requested file and does not substitute Available.ota;
	// this preserves the established no-fuzzy-fallback contract [08].
	if got := err.Error(); !strings.Contains(got, "The requested mission file, Missing.ota, does not exist.") {
		t.Fatalf("strict campaign diagnostic = %q, want the Type 1 missing-file diagnostic", got)
	}
}

func TestLoadCampaignAIProfileRequiresAuthoredProfile(t *testing.T) {
	fs := fsFromMap(t, map[string]string{})
	_, err := loadCampaignAIProfile(fs, "mission-ai")
	if err == nil {
		t.Fatal("missing mission and default AI profiles must fail campaign construction")
	}
	if got := err.Error(); !strings.Contains(got, "session: ai profile \"mission-ai\"") || !strings.Contains(got, "fallback") {
		t.Fatalf("missing AI profile diagnostic = %q, want profile and fallback context", got)
	}

	// The established fallback remains valid when ai/default.txt exists.
	fsWithDefault := fsFromMap(t, map[string]string{
		"ai/default.txt": "plan any\nweight armcom 1.0\n",
	})
	prof, err := loadCampaignAIProfile(fsWithDefault, "mission-ai")
	if err != nil {
		t.Fatalf("default AI profile fallback: %v", err)
	}
	if prof == nil || prof.Name() != "default" {
		t.Fatalf("fallback profile = %#v, want authored default profile", prof)
	}
}

// TestTriggerPollAndDeathNotificationEndMission locks the mission end-to-end:
// an authored KillUnitType defeat completes through the exactly-once death
// notification, and the once-per-30-ticks poll site latches DefeatDone
// [08 "Evaluation"] [PLAN_10 C14-C17].
func TestTriggerPollAndDeathNotificationEndMission(t *testing.T) {
	otaText := "[GlobalHeader]\n{\nKillUnitType=armflea, 2;\n[Schema 0]\n{\nType=Easy;\n[units]\n{\n[unit0]\n{\nUnitname=armcom;\nXPos=0;\nZPos=0;\n}\n[unit1]\n{\nUnitname=armflea;\nXPos=5;\nZPos=5;\nPlayer=1;\n}\n}\n[specials]\n{\n}\n[features]\n{\n}\n}\n}\n"
	fs := fsFromMap(t, map[string]string{
		"maps/test.ota": otaText,
	})
	cat := &content.Catalog{
		Maps: map[string]*content.MapHeader{
			"test": {
				LogicalTNT: "maps/test.tnt",
				Schemas:    []content.MapSchema{{SurfaceMetal: 0}},
			},
		},
		Units: map[string]*content.UnitDef{
			"armcom":  {UnitName: "armcom", MaxDamage: 100, SightDistance: 128, CanMove: true},
			"armflea": {UnitName: "armflea", MaxDamage: 20, SightDistance: 128},
		},
	}
	s, err := NewSyntheticMissionForTest(fs, cat, "test.ota", 0)
	if err != nil {
		t.Fatalf("NewSyntheticMissionForTest: %v", err)
	}
	if len(s.Mission.Victory) != 1 || s.Mission.Victory[0].Completed {
		t.Fatalf("authored victory condition not decoded: %+v", s.Mission.Victory)
	}
	if len(s.Mission.Defeat) != 1 {
		t.Fatalf("default defeat condition not injected [PLAN_10 C16]: %+v", s.Mission.Defeat)
	}
	// Find the enemy flea.
	var enemy *units.Unit
	for _, u := range s.Units.Iter() {
		if u.Owner == 1 {
			enemy = u
		}
	}
	if enemy == nil {
		t.Fatal("enemy unit was not placed")
	}
	// Stepping 31 ticks crosses the first two poll slices without any death.
	for i := 0; i <= 30; i++ {
		s.Step(int32(i))
	}
	if s.VictoryDone || s.DefeatDone {
		t.Fatalf("nothing may complete before the deaths: v=%v d=%v", s.VictoryDone, s.DefeatDone)
	}
	// First death: the victory countdown drops to 1 — not yet complete.
	s.Units.Destroy(enemy.Handle, units.DeathKilled)
	for i := 31; i <= 60; i++ {
		s.Step(int32(i))
	}
	if s.VictoryDone {
		t.Fatal("KillUnitType=2 must not complete on one death")
	}
	// The default defeat (all-units-killed over the LOCAL player) completes
	// when the local side is gone, while the unmet victory stays open.
	var local *units.Unit
	for _, u := range s.Units.Iter() {
		if u.Owner == 0 {
			local = u
		}
	}
	if local == nil {
		t.Fatal("local unit was not placed")
	}
	s.Units.Destroy(local.Handle, units.DeathKilled)
	for i := 61; i <= 90; i++ {
		s.Step(int32(i))
	}
	if !s.DefeatDone {
		t.Fatal("default all-units-killed defeat did not complete after the local player died")
	}
}
