package mission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func fsFromMapLoad(t *testing.T, files map[string]string) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	for logical, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(logical))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatalf("MountDirectory: %v", err)
	}
	return fs
}

func TestLoadVerbatimDiagnostics(t *testing.T) {
	// does not exist: missing OTA via TypeSkirmish with no fuzzy candidate
	t.Run("does not exist", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{})
		_, err := LoadWithType(fs, TypeSkirmish, "Missing.ota", 0, 0, nil)
		if err == nil {
			t.Fatalf("want does not exist error")
		}
		got := err.Error()
		want := "The requested mission file, Missing.ota, does not exist."
		if got != want {
			t.Fatalf("does not exist verbatim: got %q want %q", got, want)
		}
		if !strings.Contains(got, "does not exist") {
			t.Fatalf("substring does not exist not found in %q", got)
		}
	})
	// corrupt (no header found)
	t.Run("corrupt", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/Corrupt.ota": "this is not a TDF header ::: [[[",
		})
		_, err := LoadWithType(fs, TypeSkirmish, "Corrupt.ota", 0, 0, nil)
		if err == nil {
			t.Fatalf("want corrupt error")
		}
		got := err.Error()
		want := "Hey, joker!  Mission file Corrupt.ota is corrupt (no header found)."
		if got != want {
			t.Fatalf("corrupt verbatim: got %q want %q", got, want)
		}
		if !strings.Contains(got, "corrupt (no header found)") {
			t.Fatalf("substring corrupt not found")
		}
		// check two spaces after joker!
		if !strings.Contains(got, "joker!  Mission") {
			t.Fatalf("two spaces after joker! not preserved in %q", got)
		}
	})
	// Old TED format
	t.Run("old TED", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/Old.ota": "TED 1.0\n[GlobalHeader]\n{\n}\n",
		})
		_, err := LoadWithType(fs, TypeSkirmish, "Old.ota", 0, 0, nil)
		if err == nil {
			t.Fatalf("want Old TED error")
		}
		got := err.Error()
		want := "Old TED format no longer supported!"
		if got != want {
			t.Fatalf("Old TED verbatim: got %q want %q", got, want)
		}
	})
	// No GlobalHeader block
	t.Run("no GlobalHeader", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/NoGlobal.ota": "[Header]\n{\n}\n",
			// Provide a valid OTA for fuzzy fallback so this case is NOT fuzzy-resolved to the valid one.
			// To isolate No GlobalHeader, ensure maps contains only this file (no fallback candidate better? but fuzzy would still pick itself).
			// Our implementation will try fuzzy fallback on missing GlobalHeader and could fallback to itself? No, fallback searches other files; with only one file, closest is itself, but we exclude self? Our code checks closest != wanted but with only one file wanted == candidate, so no fallback.
		})
		_, err := LoadWithType(fs, TypeSkirmish, "NoGlobal.ota", 0, 0, nil)
		if err == nil {
			t.Fatalf("want No GlobalHeader error")
		}
		got := err.Error()
		want := "No GlobalHeader block in mission file!"
		if got != want {
			t.Fatalf("No GlobalHeader verbatim: got %q want %q", got, want)
		}
		if !strings.Contains(got, "No GlobalHeader block") {
			t.Fatalf("substring No GlobalHeader not found")
		}
	})
}

func TestTypeDispatchMatrix(t *testing.T) {
	validOTA := `
[GlobalHeader]
{
    missionname=Test;
    minwindspeed=100;
    maxwindspeed=3000;
    useonlyunits=Foo.tdf;
    [Schema 0] { Type=Network 1; [specials] { [special0] { specialwhat=StartPos1; XPos=0; ZPos=0; } } }
    [Schema 1] { Type=Easy; [specials] { [special0] { specialwhat=StartPos1; } } [units] { [unit0] { Unitname=ARMCOM; XPos=100; ZPos=200; } } [features] { [feature0] { Featurename=Rock; XPos=10; ZPos=10; } } }
}
`
	campaignTDF := `
[HEADER]
{
}
[MISSION0]
{
    missionname=First;
    missionfile=Valid.ota;
}
`
	// Type 1 via campaign
	t.Run("type 1 campaign", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/Valid.ota":         validOTA,
			"camps/TestCampaign.tdf": campaignTDF,
		})
		m, err := LoadCampaign(fs, "camps/TestCampaign.tdf", 0, 0, 1)
		if err != nil {
			t.Fatalf("Type 1 campaign load: %v", err)
		}
		if m.Type != TypeCampaign {
			t.Fatalf("want TypeCampaign got %d", m.Type)
		}
		if m.OTA == nil || m.OTA.Global == nil {
			t.Fatalf("OTA not loaded")
		}
		if m.UseOnlyPath != "camps/useonly/Foo.tdf" {
			t.Fatalf("UseOnly routing C8: got %q want camps/useonly/Foo.tdf", m.UseOnlyPath)
		}
		if m.WindBounds.Min != 100 || m.WindBounds.Max != 3000 {
			t.Fatalf("wind retention C5: got %v", m.WindBounds)
		}
		if len(m.Units) != 1 {
			t.Fatalf("units not decoded via placement after schema C4")
		}
	})
	// Type 2 direct
	t.Run("type 2 skirmish", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/Skirm.ota": `
[GlobalHeader]
{
    [Schema 0] { Type=Network 1; [specials] { [special0] { specialwhat=StartPos1; } [special1] { specialwhat=StartPos2; } } }
    [Schema 1] { Type=Network 2; [specials] { [special0] { specialwhat=StartPos1; } } }
}
`,
		})
		m, err := LoadWithType(fs, TypeSkirmish, "Skirm.ota", 0, 2, nil)
		if err != nil {
			t.Fatalf("Type 2 skirmish: %v", err)
		}
		if m.Type != TypeSkirmish {
			t.Fatalf("want TypeSkirmish")
		}
		// With playerCount 2, Network 1 has 2 StartPos -> exact match
		if m.Schema.Name != "Schema 0" {
			t.Fatalf("schema selection before placement C4: got %q", m.Schema.Name)
		}
	})
	// Type 3 saved OTA, same as skirmish with fuzzy fallback
	t.Run("type 3 saved", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/SavedMap.ota": `
[GlobalHeader]
{
    [Schema 0] { Type=Network 1; [specials] { [special0] { specialwhat=StartPos1; } } }
}
`,
		})
		m, err := LoadWithType(fs, TypeSaved, "SavedMap.ota", 0, 1, nil)
		if err != nil {
			t.Fatalf("Type 3: %v", err)
		}
		if m.Type != TypeSaved {
			t.Fatalf("want TypeSaved")
		}
	})
}

func TestFuzzySearchFallback(t *testing.T) {
	// Fixture: two valid OTAs; request a typo that should fuzzy to the closest.
	alphaOTA := `
[GlobalHeader]
{
    missionname=Alpha;
    [Schema 0] { Type=Network 1; [specials] { [special0] { specialwhat=StartPos1; } } }
}
`
	betaOTA := `
[GlobalHeader]
{
    missionname=Beta;
    [Schema 0] { Type=Network 1; [specials] { [special0] { specialwhat=StartPos1; } } }
}
`
	fs := fsFromMapLoad(t, map[string]string{
		"maps/Alpha.ota": alphaOTA,
		"maps/Beta.ota":  betaOTA,
	})
	// Request Alpa.ota (missing 'h') – closest is Alpha.ota (distance 1 vs distance to Beta larger)
	m, err := LoadWithType(fs, TypeSkirmish, "Alpa.ota", 0, 1, nil)
	if err != nil {
		t.Fatalf("fuzzy fallback: %v", err)
	}
	if !strings.EqualFold(m.TerrainKey, "Alpha") {
		t.Fatalf("fuzzy fallback expected Alpha terrain key, got %q", m.TerrainKey)
	}
	if m.OTA == nil || !strings.EqualFold(m.OTA.MissionName, "Alpha") {
		t.Fatalf("fuzzy fallback OTA not Alpha: %+v", m.OTA)
	}
	// Also test that fuzzy fallback is attempted when parsing misses due to corrupt?
	// Create a corrupt file for the requested name, but valid fallback exists – should still fallback to closest valid?
	// Our implementation tries fuzzy on corrupt/no header as well.
}

func TestOrderingSchemaBeforePlacement(t *testing.T) {
	ota := `
[GlobalHeader]
{
    missionname=OrderTest;
    minwindspeed=50;
    maxwindspeed=150;
    [Schema 0]
    {
        Type=Network 1;
        [specials] { [special0] { specialwhat=StartPos1; } }
        [units] { [unit0] { Unitname=ARMCOM; XPos=10; ZPos=20; } }
        [features] { [feature0] { Featurename=Tree; XPos=5; ZPos=5; } }
    }
}
`
	fs := fsFromMapLoad(t, map[string]string{
		"maps/Order.ota": ota,
	})
	m, err := LoadWithType(fs, TypeSkirmish, "Order.ota", 0, 1, nil)
	if err != nil {
		t.Fatalf("ordering load: %v", err)
	}
	order := m.Order()
	// Expected order: schema, units, specials, features, wind, useonly, then the
	// common tail's trigger objects [C4][C5] [08 "Mission type dispatch"].
	wantOrder := []string{"schema", "units", "specials", "features", "wind", "useonly", "triggers"}
	if len(order) != len(wantOrder) {
		t.Fatalf("order length: got %v want %v", order, wantOrder)
	}
	for i, w := range wantOrder {
		if order[i] != w {
			t.Fatalf("order[%d]: got %q want %q (full %v)", i, order[i], w, order)
		}
	}
	// Verify schema selection happened before placement: units come from Schema 0
	if m.Schema.Name != "Schema 0" {
		t.Fatalf("schema before placement: want Schema 0 got %q", m.Schema.Name)
	}
	if len(m.Units) == 0 || m.Units[0].UnitName != "ARMCOM" {
		t.Fatalf("placement after schema: units %+v", m.Units)
	}
	// Verify wind retained after placement without RNG: wind bounds are from GlobalHeader
	if m.WindBounds.Min != 50 || m.WindBounds.Max != 150 {
		t.Fatalf("wind retention C5: got %+v", m.WindBounds)
	}
}

func TestDiagnosticsSink(t *testing.T) {
	fs := fsFromMapLoad(t, map[string]string{
		"maps/NoGlobal2.ota": "[Header]\n{\n}\n",
	})
	sink := &CollectSink{}
	_, err := LoadWithType(fs, TypeSkirmish, "NoGlobal2.ota", 0, 1, sink)
	if err == nil {
		t.Fatalf("want error for sink")
	}
	if len(sink.Messages) == 0 {
		t.Fatalf("sink should collect verbatim diagnostic per ORCH §7")
	}
	if sink.Messages[0] != "No GlobalHeader block in mission file!" {
		t.Fatalf("sink verbatim: got %q want %q", sink.Messages[0], "No GlobalHeader block in mission file!")
	}
	// ensure not logged via other means – just sink
}

func TestWindRetentionNoRNGAndUseOnly(t *testing.T) {
	ota := `
[GlobalHeader]
{
    minwindspeed=100;
    maxwindspeed=3000;
    useonlyunits=The Pass.tdf;
    [Schema 0] { Type=Network 1; [specials] { [special0] { specialwhat=StartPos1; } } }
}
`
	fs := fsFromMapLoad(t, map[string]string{
		"maps/Wind.ota": ota,
	})
	m, err := LoadWithType(fs, TypeSkirmish, "Wind.ota", 0, 1, nil)
	if err != nil {
		t.Fatalf("wind/useonly: %v", err)
	}
	if m.WindBounds.Min != 100 || m.WindBounds.Max != 3000 {
		t.Fatalf("wind bounds retained C5: got %+v", m.WindBounds)
	}
	if m.UseOnlyPath != "camps/useonly/The Pass.tdf" {
		t.Fatalf("UseOnlyUnits routing C8: got %q", m.UseOnlyPath)
	}
	// Verify no RNG draws: just ensure load is deterministic and doesn't panic on missing rng.
	// Call load again and compare results deterministically.
	m2, err := LoadWithType(fs, TypeSkirmish, "Wind.ota", 0, 1, nil)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if m2.WindBounds != m.WindBounds || m2.UseOnlyPath != m.UseOnlyPath {
		t.Fatalf("deterministic second load mismatch")
	}
}

func TestImmunityFlagRetention(t *testing.T) {
	// C7: only high bit is consumed; other flags retained but not acted on.
	ota := `
[GlobalHeader]
{
    [Schema 0]
    {
        Type=Network 1;
        [specials] { [special0] { specialwhat=StartPos1; } }
        [units]
        {
            [unit0]
            {
                Unitname=ARMCOM;
                Immunity=1;
                AiIgnore=1;
                AiPriorityTarget=1;
                MissionCriticalUnit=1;
                BuildPriority=5;
                InitialGroup=2;
            }
        }
    }
}
`
	fs := fsFromMapLoad(t, map[string]string{
		"maps/Flag.ota": ota,
	})
	m, err := LoadWithType(fs, TypeSkirmish, "Flag.ota", 0, 1, nil)
	if err != nil {
		t.Fatalf("flag retention: %v", err)
	}
	if len(m.Units) != 1 {
		t.Fatalf("want 1 unit")
	}
	u := m.Units[0]
	if !u.Immune {
		t.Fatalf("Immune high bit should be true C7")
	}
	if !u.AiIgnore || !u.AiPriorityTarget || !u.MissionCriticalUnit {
		t.Fatalf("other flags retained C7: %+v", u)
	}
	if u.BuildPriority != 5 || u.InitialGroup != "2" {
		t.Fatalf("BuildPriority/InitialGroup retained C7: %+v", u)
	}
	if u.RawFlags&0x80 == 0 {
		t.Fatalf("RawFlags high bit not set")
	}
	// Ensure only immunity matters for IsImmune
	if !IsImmuneFromFlags(0x80) || IsImmuneFromFlags(0x20) || IsImmuneFromFlags(0x40) {
		t.Fatalf("IsImmuneFromFlags only high bit C7")
	}
}
