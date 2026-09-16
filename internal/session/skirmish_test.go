package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func prepareFixtureSkirmishCatalog(cat *content.Catalog, cfg *SkirmishConfig) {
	if cat == nil || cfg == nil || len(cat.Units) == 0 {
		return
	}
	keys := make([]string, 0, len(cat.Units))
	for name := range cat.Units {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	commander := ""
	for _, name := range keys {
		def := cat.Units[name]
		if def != nil && def.UnitName != "" {
			commander = def.UnitName
			if def.Commander {
				break
			}
		}
	}
	if commander == "" {
		return
	}
	for i := 0; i < cfg.NumPlayers && i < 10; i++ {
		side := cfg.Players[i].Side
		if side >= 0 && side < len(cat.Sides) && cat.Sides[side] != nil && cat.Sides[side].Commander != "" {
			continue
		}
		cfg.Players[i].Side = 0
	}
	if len(cat.Sides) == 0 {
		cat.Sides = append(cat.Sides, &content.SideDef{Commander: commander})
	} else if cat.Sides[0] == nil || cat.Sides[0].Commander == "" {
		cat.Sides[0] = &content.SideDef{Commander: commander}
	}
}

func fsFromMapSkirmish(t *testing.T, files map[string]string) *vfs.FS {
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

func TestLoadSkirmishAIProfileUsesAuthoredNameAndDefaultFallback(t *testing.T) {
	fs := fsFromMapSkirmish(t, map[string]string{
		"ai/default.txt": "plan any\nweight fallback 0.5\n",
		"ai/mission.txt": "plan any\nweight authored 0.75\n",
	})
	profile, err := loadSkirmishAIProfile(fs, "mission")
	if err != nil {
		t.Fatalf("authored AI profile: %v", err)
	}
	if profile == nil || profile.Name() != "mission" {
		t.Fatalf("authored profile = %#v, want mission", profile)
	}

	profile, err = loadSkirmishAIProfile(fs, "missing")
	if err != nil {
		t.Fatalf("default AI profile fallback: %v", err)
	}
	if profile == nil || profile.Name() != "default" {
		t.Fatalf("fallback profile = %#v, want default", profile)
	}

	fsEmpty := fsFromMapSkirmish(t, map[string]string{})
	_, err = loadSkirmishAIProfile(fsEmpty, "missing")
	if err == nil || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("missing authored/default profile error = %v, want fallback diagnostic", err)
	}
}

func TestSkirmishCommanderRequiresConfiguredSideDefinition(t *testing.T) {
	cat := &content.Catalog{
		Sides: []*content.SideDef{{Commander: "missing"}},
		Units: map[string]*content.UnitDef{},
	}
	if _, err := skirmishCommander(cat, 0, 1); err == nil || !strings.Contains(err.Error(), `commander "missing"`) {
		t.Fatalf("missing configured commander error = %v, want explicit commander diagnostic", err)
	}

	cat.Sides[0].Commander = "armcom"
	if _, err := skirmishCommander(cat, 0, 1); err == nil || !strings.Contains(err.Error(), `commander "armcom"`) {
		t.Fatalf("missing commander definition error = %v, want explicit commander diagnostic", err)
	}
}

func TestSkirmishDefaults(t *testing.T) {
	// C8 NumSkirmishPlayers validation is compiled no-op [P0-05]: both branches store raw
	for _, tc := range []struct {
		n       int
		wantErr bool
	}{
		{2, false}, {10, false}, {4, false}, {1, false}, {11, false}, {0, false}, // 0 defaults to 4 per [02 §3]; 1/11 no-op per P0-05
	} {
		cfg := SkirmishConfig{MapName: "dummy", NumPlayers: tc.n}
		cfg.ApplyDefaults()
		err := cfg.Validate()
		if tc.wantErr && err == nil {
			t.Fatalf("NumPlayers %d should error [GAP T14] C8", tc.n)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("NumPlayers %d should not error: %v", tc.n, err)
		}
	}
	// Do not feed malformed counts through the synthetic constructor: its
	// authored placement fixture has only fixed player slots and cannot model
	// the retail accept-and-store quirk without inventing placement behavior.
	// The direct Validate assertions above are the supported contract [02 §3]
	// [P0-05].
	// Battle entry resolves each eligible slot's commander from the side
	// record's commander name and looks up StartPos<i+1> for every eligible
	// slot; neither has a substitute [08 R-ENTRY-01 §5]. The fixture therefore
	// supplies an authored side catalog and ten start positions.
	var ota strings.Builder
	ota.WriteString("[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n")
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&ota, "[special%d]\n{\nspecialwhat=StartPos%d;\nXPos=%d;\nZPos=%d;\n}\n", i, i+1, i*16, i*16)
	}
	ota.WriteString("}\n}\n}\n")
	fs := fsFromMapSkirmish(t, map[string]string{"maps/dummy.ota": ota.String()})
	for _, good := range []int{2, 10} {
		cfg := SkirmishConfig{MapName: "dummy", NumPlayers: good}
		if _, err := NewSyntheticSkirmishForTest(fs, minimalCatalogForStrict(), cfg); err != nil {
			t.Fatalf("NewSkirmish NumPlayers %d should not error: %v", good, err)
		}
	}
	// Per-slot defaults: 1000 resources, AllyGroup 5, colour slot index, 17-byte nick truncation [GAP T14] C8
	cfg := SkirmishConfig{MapName: "dummy", NumPlayers: 4}
	// Leave slot defaults zero to trigger ApplyDefaults
	cfg.Players[0].Nickname = "12345678901234567890" // 20 chars -> truncate to 16 [02 §3] 17-byte buffer
	cfg.Players[2].Nickname = "short"
	cfg.ApplyDefaults()
	if cfg.Difficulty != SkirmishDefaultDifficulty || cfg.Location != SkirmishDefaultLocation ||
		cfg.CommanderDeath != SkirmishDefaultCommanderDeath || cfg.Mapping != SkirmishDefaultMapping ||
		cfg.LineOfSight != SkirmishDefaultLineOfSight || cfg.LOSType != SkirmishDefaultLOSType {
		t.Fatalf("skirmish scalar defaults want difficulty/location/death/mapping/los/lostype %d/%d/%d/%d/%d/%d got %d/%d/%d/%d/%d/%d",
			SkirmishDefaultDifficulty, SkirmishDefaultLocation, SkirmishDefaultCommanderDeath,
			SkirmishDefaultMapping, SkirmishDefaultLineOfSight, SkirmishDefaultLOSType,
			cfg.Difficulty, cfg.Location, cfg.CommanderDeath, cfg.Mapping, cfg.LineOfSight, cfg.LOSType)
	}
	// Easy/randomized/off are valid menu choices and must survive the second
	// ApplyDefaults performed by the session constructor.
	cfg.Difficulty = 0
	cfg.Location = 0
	cfg.CommanderDeath = 0
	cfg.Mapping = 0
	cfg.LineOfSight = 0
	cfg.LOSType = 0
	cfg.ApplyDefaults()
	if cfg.Difficulty != 0 || cfg.Location != 0 || cfg.CommanderDeath != 0 || cfg.Mapping != 0 || cfg.LineOfSight != 0 || cfg.LOSType != 0 {
		t.Fatalf("explicit zero-valued skirmish rules were replaced by defaults: %+v", cfg)
	}
	for i := 0; i < 4; i++ {
		p := cfg.Players[i]
		if p.Metal != 1000 {
			t.Fatalf("Player%dMetal default 1000 [GAP T14] C8 got %d", i, p.Metal)
		}
		if p.Energy != 1000 {
			t.Fatalf("Player%dEnergy default 1000 C8 got %d", i, p.Energy)
		}
		if p.AllyGroup != 5 {
			t.Fatalf("AllyGroup default 5 C8 slot %d got %d", i, p.AllyGroup)
		}
		if p.Color != i {
			t.Fatalf("colour = slot index [GAP T14] C8 slot %d want %d got %d", i, i, p.Color)
		}
		if p.Controller != 0 {
			t.Fatalf("Controller default 0 human C8 slot %d got %d", i, p.Controller)
		}
	}
	if len(cfg.Players[0].Nickname) != 16 {
		t.Fatalf("nickname buffers 17 bytes (16 payload) [02 §3] C8 want 16 got %d %q", len(cfg.Players[0].Nickname), cfg.Players[0].Nickname)
	}
	if cfg.Players[0].Nickname != "1234567890123456" {
		t.Fatalf("nickname truncation want 1234567890123456 got %q", cfg.Players[0].Nickname)
	}
	// Side defaults slot&1 [02 §3]
	if cfg.Players[1].Side != 1 {
		t.Fatalf("Side default slot&1: slot1 want 1 got %d", cfg.Players[1].Side)
	}
	if cfg.Players[2].Side != 0 {
		t.Fatalf("Side default slot&1: slot2 want 0 got %d", cfg.Players[2].Side)
	}
	// Computer slots per config: controller non-zero indicates computer
	cfg2 := SkirmishConfig{MapName: "dummy", NumPlayers: 2}
	cfg2.Players[1].Controller = 2
	cfg2.ApplyDefaults()
	if cfg2.Players[0].Controller != 0 {
		t.Fatalf("human slot controller 0")
	}
	if cfg2.Players[1].Controller != 2 {
		t.Fatalf("computer slot per config controller 2")
	}
	// Validate computer slots via session economy mapping (fixture)
	s, err := NewSyntheticSkirmishForTest(fs, minimalCatalogForStrict(), cfg2)
	if err != nil {
		t.Fatalf("NewSkirmish computer slots: %v", err)
	}
	if s.Econ.Players[0].ControllerState != 1 {
		t.Fatalf("human economy ControllerState 1 want 1 got %d", s.Econ.Players[0].ControllerState)
	}
	if s.Econ.Players[1].ControllerState != 2 {
		t.Fatalf("computer economy ControllerState 2 want 2 got %d", s.Econ.Players[1].ControllerState)
	}
	if s.Skirmish.MapName != cfg2.MapName || s.Skirmish.NumPlayers != cfg2.NumPlayers ||
		s.Skirmish.Players[1].Controller != cfg2.Players[1].Controller {
		t.Fatalf("session did not retain lobby config: %+v", s.Skirmish)
	}
}

func TestSkirmishWindSinglePath(t *testing.T) {
	// Single battle-entry wind initializer per [01 §7.3]; [R-CORE-02]
	// supersedes the draw-count reading: battle entry consumes NO wind draws
	// (briefing speed/direction are front-end display state) and zeroes the
	// deadline, identically via skirmish and mission. The skirmish slot
	// shuffle draws from its disposable setup CRT, never from the retained
	// session CRT or rng.Global [DET-01], so neither retained stream moves.
	// The Network 1 schema authors one StartPos per lobby slot. It carried only
	// StartPos1 until WU-19-178, when a `StartPos` miss became fatal on the
	// kind-2 path as [08 R-ENTRY-01 §5] step 4 states: with two players and one
	// authored start position, retail refuses the battle rather than placing
	// slot 1 at a random point, so this two-player fixture authors two.
	otaText := "[GlobalHeader]\n{\nminwindspeed=15;\nmaxwindspeed=35;\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=16;\nZPos=16;\n}\n}\n}\n[Schema 0]\n{\nType=Easy;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\n}\n}\n}\n}\n"
	// Use separate FS instances to avoid catalog contamination
	fsMission := fsFromMapSkirmish(t, map[string]string{"maps/wind.ota": otaText})
	fsSkirmish := fsFromMapSkirmish(t, map[string]string{"maps/wind.ota": otaText})
	cat := &content.Catalog{
		Maps: map[string]*content.MapHeader{
			"wind": {Name: "wind", LogicalTNT: "maps/wind.tnt", Schemas: []content.MapSchema{{Type: "Network 1", StartPosCount: 2}}},
		},
		Units: map[string]*content.UnitDef{"armcom": {UnitName: "armcom", MaxDamage: 100}},
	}
	seed := uint32(0x1234)
	// Mission path: no battle-entry wind draws.
	rng.SeedGlobal(99, seed)
	before := rng.Global.Crt.Draws()
	sM, err := NewSyntheticMissionForTest(fsMission, cat, "wind.ota", 0)
	if err != nil {
		t.Fatalf("NewSyntheticMissionForTest: %v", err)
	}
	missionDraws := rng.Global.Crt.Draws() - before
	if missionDraws != 0 {
		t.Fatalf("mission battle entry consumed %d global CRT draws, want 0 [R-CORE-02][DET-01]", missionDraws)
	}
	// Skirmish path: the shuffle draws its disposable setup CRT; the retained
	// session and global streams are untouched and wind still draws nothing.
	rng.SeedGlobal(99, seed)
	before2 := rng.Global.Crt.Draws()
	cfg := SkirmishConfig{MapName: "wind", NumPlayers: 2}
	sS, err := NewSyntheticSkirmishForTest(fsSkirmish, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishWithProgress: %v", err)
	}
	skirmishDraws := rng.Global.Crt.Draws() - before2
	if skirmishDraws != 0 {
		t.Fatalf("skirmish battle entry consumed %d global CRT draws, want 0 [R-CORE-02][DET-01]", skirmishDraws)
	}
	// Both entries zero the deadline and start wind at the zero value; the
	// first chain runs in sub-tick 1 [R-CORE-02].
	if sM.Wind == nil || sS.Wind == nil {
		t.Fatalf("wind holders nil")
	}
	if sM.Wind.NextChange != 0 || sS.Wind.NextChange != 0 {
		t.Fatalf("battle entry must zero the wind deadline: mission %d skirmish %d [R-CORE-02]", sM.Wind.NextChange, sS.Wind.NextChange)
	}
	if sM.Wind.Strength != 0 || sS.Wind.Strength != 0 {
		t.Fatalf("battle entry must not draw wind values: mission %d skirmish %d [R-CORE-02]", sM.Wind.Strength, sS.Wind.Strength)
	}
	// Determinism: two skirmish constructions with the same seeds produce the
	// identical session-stream state and wind state.
	sS2, err := NewSyntheticSkirmishForTest(fsFromMapSkirmish(t, map[string]string{"maps/wind.ota": otaText}), cat, cfg)
	if err != nil {
		t.Fatalf("second skirmish: %v", err)
	}
	if sS.Wind.NextChange != sS2.Wind.NextChange || sS.CrtRNG().Draws() != sS2.CrtRNG().Draws() {
		t.Fatalf("second skirmish same seeds must give identical wind/session-stream state")
	}
	_ = sM
	_ = fsMission
	_ = fsSkirmish
}

// TestAllyGroupZeroSurvivesRepeatedDefaulting pins ally group 0 as a real
// group. [08 R-SKIR-01 §1] "The setup record" gives the field's range as
// `0..4, or the unassigned sentinel 5`, [08 R-SKIR-01 §2] allies rows with the
// same group `and group ≠ 5`, and the registry mirror installs 5 only when
// `Player%dAllyGroup` is ABSENT. So 0 means "team 0", not "no value".
//
// **Correction (WU-19-116).** ApplyDefaults and Normalize rewrote every stored
// 0 to the sentinel on every call, not only on the first. internal/settings
// writes all ten rows with no `omitempty`, so a stored `allyGroup: 0` is a
// choice; the second ApplyDefaults inside the shell's skirmishConfigForStart
// then promoted a player's group 0 to "allied with nobody" behind their back.
func TestAllyGroupZeroSurvivesRepeatedDefaulting(t *testing.T) {
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 4}
	cfg.ApplyDefaults()
	// Absent rows take the miss default on the first application, and every row
	// takes it, so raising the row count later cannot reintroduce a bare zero.
	for i := 0; i < SkirmishMaxPlayers; i++ {
		if got := cfg.Players[i].AllyGroup; got != SkirmishDefaultAllyGroup {
			t.Fatalf("row %d ally group = %d after the first ApplyDefaults, want the miss default %d", i, got, SkirmishDefaultAllyGroup)
		}
	}

	// The stored choices the lobby's Allies gadget can produce, 0..5.
	cfg.Players[0].AllyGroup = 0
	cfg.Players[1].AllyGroup = 0
	cfg.Players[2].AllyGroup = 4
	cfg.Players[3].AllyGroup = 5
	cfg.ApplyDefaults()
	cfg.ApplyDefaults()
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	for i, want := range []int{0, 0, 4, 5} {
		if got := cfg.Players[i].AllyGroup; got != want {
			t.Fatalf("row %d ally group = %d after re-defaulting, want the stored %d", i, got, want)
		}
	}

	// The alliance predicate is what the rewrite corrupted: two group-0 rows are
	// allies, and neither is allied with group 4 or with the sentinel row
	// [08 R-SKIR-01 §2].
	for i := 0; i < 4; i++ {
		cfg.Players[i].Controller = SkirmishControllerHuman
	}
	if !skirmishPlayersAllied(cfg, 0, 1) || !skirmishPlayersAllied(cfg, 1, 0) {
		t.Fatal("two rows stored in group 0 must be allies [08 R-SKIR-01 §2]")
	}
	if skirmishPlayersAllied(cfg, 0, 2) || skirmishPlayersAllied(cfg, 0, 3) {
		t.Fatal("group 0 must not ally with group 4 or with the unassigned sentinel")
	}
	if skirmishPlayersAllied(cfg, 3, 0) || !skirmishPlayersAllied(cfg, 3, 3) {
		t.Fatal("a group-5 row is allied with nobody but itself [08 R-SKIR-01 §2]")
	}
}
