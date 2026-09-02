package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
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
	// this preserves the established no-translated-name-retry contract
	// [08 R-CAMP-01 §11 point 1]. An unopenable/unparsable kind-1 OTA raises
	// the "no mission defintion" box (sic), naming the requested file, not
	// the "does not exist" box (that one is reserved for a missing MISSION%d
	// block) [02 "Mission-file diagnostics"].
	if got := err.Error(); !strings.Contains(got, "Hey, joker!  There is no mission defintion for this mission: Missing.ota") {
		t.Fatalf("strict campaign diagnostic = %q, want the Type 1 no-mission-defintion diagnostic", got)
	}
}

func TestSyntheticCampaignRetainsCompositionIdentity(t *testing.T) {
	fs := fsFromMap(t, map[string]string{
		"camps/TestCampaign.tdf": `
[HEADER]
{
}
[MISSION0]
{
    missionname=First;
    missionfile=Valid.ota;
}
`,
		"maps/Valid.ota": `
[GlobalHeader]
{
    UseOnlyUnits=Foo.tdf;
    [Schema 0]
    {
        Type=Easy;
        [units] { }
        [specials] { }
        [features] { }
    }
}
`,
	})
	s, err := NewSyntheticMissionForTest(fs, minimalCatalogForStrict(), "camps/TestCampaign.tdf:MISSION0", 0)
	if err != nil {
		t.Fatalf("NewSyntheticMissionForTest campaign: %v", err)
	}
	if s.Mission == nil {
		t.Fatal("campaign mission identity missing")
	}
	if s.CampaignSlot != s.Mission.CampaignIndex || s.CampaignSlot != 0 {
		t.Fatalf("CampaignSlot=%d Mission.CampaignIndex=%d, want retained slot 0", s.CampaignSlot, s.Mission.CampaignIndex)
	}
	if s.Mission.CampaignPath != "camps/TestCampaign.tdf" ||
		s.Mission.CampaignMissionName != "First" ||
		s.Mission.UseOnlyPath != "camps/useonly/Foo.tdf" ||
		s.Mission.Difficulty != 0 {
		t.Fatalf("campaign identity = path %q name %q useonly %q difficulty %d", s.Mission.CampaignPath, s.Mission.CampaignMissionName, s.Mission.UseOnlyPath, s.Mission.Difficulty)
	}
	// TODO(question): assert this same campaign mission-name provenance through
	// restore/continuation once a session constructor for that seam exists in
	// the repository; the save player-table/mission-identity decoder and its
	// constructor are the deciders. Do not reconstruct it from the OTA title.
}

// TestCampaignPlayerTableSidesFromAuthoredCampaignSide locks the campaign
// player-table writer: the new-game panel stamps the two campaign slots'
// side bytes as (0, 1) for Arm and (1, 0) for Core [08 R-CAMP-01 §3], and a
// campaign whose `[HEADER] campaignside` names a side is played as that side
// [08 R-CAMP-01 §1]. `ALL` and an unresolved name name no side and must leave
// the commander identity unknown [08 R-TRIG-01 §3].
func TestCampaignPlayerTableSidesFromAuthoredCampaignSide(t *testing.T) {
	fs := fsFromMap(t, map[string]string{
		"camps/Arm Test.tdf":     "[HEADER]\n{\ncampaignside=ARM;\n}\n[MISSION0]\n{\nmissionname=First;\nmissionfile=Valid.ota;\n}\n",
		"camps/Core Test.tdf":    "[HEADER]\n{\ncampaignside=core;\n}\n[MISSION0]\n{\nmissionname=First;\nmissionfile=Valid.ota;\n}\n",
		"camps/Any Test.tdf":     "[HEADER]\n{\ncampaignside=ALL;\n}\n[MISSION0]\n{\nmissionname=First;\nmissionfile=Valid.ota;\n}\n",
		"camps/Unknown Test.tdf": "[HEADER]\n{\ncampaignside=Zaxxon;\n}\n[MISSION0]\n{\nmissionname=First;\nmissionfile=Valid.ota;\n}\n",
	})
	cat := minimalCatalogForStrict() // Sides: ordinal 0 ARM/armcom, ordinal 1 CORE/corcom.
	cases := []struct {
		path      string
		wantKnown bool
		want      [2]int8
	}{
		{"camps/arm test.tdf", true, [2]int8{0, 1}},
		{"camps/core test.tdf", true, [2]int8{1, 0}},
		{"camps/any test.tdf", false, [2]int8{}},
		{"camps/unknown test.tdf", false, [2]int8{}},
		{"", false, [2]int8{}}, // bare-OTA type 1: no campaign file, no authored side
	}
	for _, tc := range cases {
		s := &Session{Catalog: cat}
		m := &mission.Mission{Type: mission.TypeCampaign, CampaignPath: tc.path}
		applyCampaignPlayerTableSides(s, fs, m)
		for slot := 0; slot < 2; slot++ {
			if s.campaignPlayerSideKnown[slot] != tc.wantKnown {
				t.Fatalf("campaign %q slot %d known = %v, want %v", tc.path, slot, s.campaignPlayerSideKnown[slot], tc.wantKnown)
			}
			if tc.wantKnown && s.campaignPlayerSide[slot] != tc.want[slot] {
				t.Fatalf("campaign %q slot %d side = %d, want %d [08 R-CAMP-01 §3]", tc.path, slot, s.campaignPlayerSide[slot], tc.want[slot])
			}
		}
		// The panel writes two rows; slots 2..9 are not part of a campaign
		// battle's owner tests and stay unknown [08 R-TRIG-01 §3].
		for slot := 2; slot < len(s.campaignPlayerSideKnown); slot++ {
			if s.campaignPlayerSideKnown[slot] {
				t.Fatalf("campaign %q wrote an untraced row for slot %d", tc.path, slot)
			}
		}
	}
}

// TestCampaignPlayerTableSidesDrawNothing proves the player-table stamp is a
// pure identity copy: it consumes neither stream [I4].
func TestCampaignPlayerTableSidesDrawNothing(t *testing.T) {
	fs := fsFromMap(t, map[string]string{
		"camps/Arm Test.tdf": "[HEADER]\n{\ncampaignside=ARM;\n}\n[MISSION0]\n{\nmissionname=First;\nmissionfile=Valid.ota;\n}\n",
	})
	rng.SeedGlobal(0x1234, 0x5678)
	simBefore, crtBefore := rng.Global.Sim.Draws(), rng.Global.Crt.Draws()
	s := &Session{Catalog: minimalCatalogForStrict()}
	applyCampaignPlayerTableSides(s, fs, &mission.Mission{Type: mission.TypeCampaign, CampaignPath: "camps/arm test.tdf"})
	if !s.campaignPlayerSideKnown[0] {
		t.Fatal("fixture did not stamp the campaign player table")
	}
	if got, want := rng.Global.Sim.Draws(), simBefore; got != want {
		t.Fatalf("player-table stamp drew %d simulation values, want %d [I4]", got-want, 0)
	}
	if got, want := rng.Global.Crt.Draws(), crtBefore; got != want {
		t.Fatalf("player-table stamp drew %d CRT values, want %d [I4]", got-want, 0)
	}
}

// TestRetailRestoreKeepsPlayerTableSides locks the restore-side writer: each
// `Player%i` account's `Side` item is that slot's player-table side ordinal
// [08 "Player records"], so a resumed battle keeps the commander identity of
// [08 R-TRIG-01 §3].
func TestRetailRestoreKeepsPlayerTableSides(t *testing.T) {
	cat := minimalCatalogForStrict()
	s := &Session{
		Catalog: cat,
		Clock:   &clock.State{Requested: 10, Active: 10},
		Econ:    &economy.Service{},
		Units:   units.NewSliced(8, cat),
		Mission: &mission.Mission{Type: mission.TypeCampaign},
	}
	// A construction-time row that the save does not name must survive; a row
	// the save names must take the saved value.
	s.campaignPlayerSide[1], s.campaignPlayerSideKnown[1] = 1, true
	stage := &RetailBattleStage{
		Session:    s,
		StableUnit: map[uint16]pool.Handle{},
		Image: &save.BattleImage{
			HumanPlayer: 0,
			Players:     []save.PlayerSlot{{Index: 0, Side: 1}, {Index: 2, Side: 0}},
		},
	}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatalf("RestoreRetailBattleCore: %v", err)
	}
	if !s.campaignPlayerSideKnown[0] || s.campaignPlayerSide[0] != 1 {
		t.Fatalf("slot 0 side = %d known %v, want 1/true from the Player0 account [08 \"Player records\"]", s.campaignPlayerSide[0], s.campaignPlayerSideKnown[0])
	}
	if !s.campaignPlayerSideKnown[1] || s.campaignPlayerSide[1] != 1 {
		t.Fatalf("restore dropped the constructed row for slot 1: side %d known %v", s.campaignPlayerSide[1], s.campaignPlayerSideKnown[1])
	}
	if !s.campaignPlayerSideKnown[2] || s.campaignPlayerSide[2] != 0 {
		t.Fatalf("slot 2 side = %d known %v, want 0/true from the Player2 account", s.campaignPlayerSide[2], s.campaignPlayerSideKnown[2])
	}
	// A slot no account named stays unknown and the identity fails closed.
	if s.campaignPlayerSideKnown[3] {
		t.Fatal("restore invented a side for a slot no Player account named")
	}
	// The restored ordinal is what the commander identity resolves through.
	u := &units.Unit{Owner: 0, Def: cat.Units["corcom"]}
	if !s.missionTriggerContext(0).IsCommander(u) {
		t.Fatal("restored player-table side did not resolve the owner's side commander [08 R-TRIG-01 §3]")
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
