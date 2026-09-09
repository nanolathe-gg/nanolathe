package mission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
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

// TestLoadVerbatimDiagnostics exercises the six-string catalog of
// [02 "Mission-file diagnostics"], corrected in place by [08 R-CAMP-01 §11]:
// "does not exist" (kind 1, missing MISSION%d block), "campaign does not
// exist" (kind 1, camps/<name>.tdf itself fails to open or parse), "no
// mission defintion" (kind 1, an unopenable/unparsable missionfile OTA),
// "corrupt" (kind 1, a parsed missionfile OTA with no [GlobalHeader]), "Old
// TED" (kind 1, missionfile key absent — see TestOldTEDAbsentVsEmptyValue for
// the absent/empty distinction), and "No GlobalHeader" (kinds 2/3 only, a
// distinct message from kind 1's "corrupt"). Kinds 2/3's own read/parse
// failure is silent and is exercised separately below.
func TestLoadVerbatimDiagnostics(t *testing.T) {
	t.Run("does not exist", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"camps/Empty.tdf": "[HEADER]\n{\n}\n",
		})
		_, err := LoadCampaignWithSink(fs, "camps/Empty.tdf", 0, 0, 1, nil)
		if err == nil {
			t.Fatalf("want does not exist error")
		}
		got := err.Error()
		want := "The requested mission file, MISSION0, does not exist."
		if got != want {
			t.Fatalf("does not exist verbatim: got %q want %q", got, want)
		}
	})
	t.Run("campaign does not exist", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{})
		_, err := LoadCampaignWithSink(fs, "camps/Missing.tdf", 0, 0, 1, nil)
		if err == nil {
			t.Fatalf("want campaign does not exist error")
		}
		got := err.Error()
		want := "The requested campaign file, camps/Missing.tdf, does not exist."
		if got != want {
			t.Fatalf("campaign does not exist verbatim: got %q want %q", got, want)
		}
	})
	t.Run("no mission defintion", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"camps/NoOTA.tdf": "[HEADER]\n{\n}\n[MISSION0]\n{\n    missionfile=Missing.ota;\n    missionname=First;\n}\n",
		})
		_, err := LoadCampaignWithSink(fs, "camps/NoOTA.tdf", 0, 0, 1, nil)
		if err == nil {
			t.Fatalf("want no mission defintion error")
		}
		got := err.Error()
		want := "Hey, joker!  There is no mission defintion for this mission: Missing.ota"
		if got != want {
			t.Fatalf("no mission defintion verbatim: got %q want %q", got, want)
		}
		if !strings.Contains(got, "joker!  There") {
			t.Fatalf("two spaces after joker! not preserved in %q", got)
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"camps/Corrupt.tdf": "[HEADER]\n{\n}\n[MISSION0]\n{\n    missionfile=NoHeader.ota;\n    missionname=First;\n}\n",
			"maps/NoHeader.ota": "[Header]\n{\n}\n",
		})
		_, err := LoadCampaignWithSink(fs, "camps/Corrupt.tdf", 0, 0, 1, nil)
		if err == nil {
			t.Fatalf("want corrupt error")
		}
		got := err.Error()
		want := "Hey, joker!  Mission file NoHeader.ota is corrupt (no header found)."
		if got != want {
			t.Fatalf("corrupt verbatim: got %q want %q", got, want)
		}
		if !strings.Contains(got, "joker!  Mission") {
			t.Fatalf("two spaces after joker! not preserved in %q", got)
		}
	})
	t.Run("old TED", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"camps/Old.tdf": "[HEADER]\n{\n}\n[MISSION0]\n{\n    missionname=First;\n}\n",
		})
		_, err := LoadCampaignWithSink(fs, "camps/Old.tdf", 0, 0, 1, nil)
		if err == nil {
			t.Fatalf("want Old TED error")
		}
		got := err.Error()
		want := "Old TED format no longer supported!"
		if got != want {
			t.Fatalf("Old TED verbatim: got %q want %q", got, want)
		}
	})
	// No GlobalHeader block: kinds 2/3 only — a distinct message from kind 1's
	// "corrupt" above for the same underlying condition (a parsed OTA missing
	// [GlobalHeader]) [02 "Mission-file diagnostics"].
	t.Run("no GlobalHeader", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/NoGlobal.ota": "[Header]\n{\n}\n",
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
	})
	// Kinds 2/3's own read/parse failure emits no message box at all: the
	// loader "retries through the alias probe and returns silently"
	// [02 "Mission-file diagnostics"] [08 R-CAMP-01 §11 point 1]. It still
	// returns a Go error for control flow, but neither the error text nor the
	// sink carries any of the six verbatim strings; the skirmish Start
	// preflight is what tells the player "The terrain for the selected map
	// does not exist."
	t.Run("skirmish miss is silent", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{})
		sink := &CollectSink{}
		_, err := LoadWithType(fs, TypeSkirmish, "Missing.ota", 0, 0, sink)
		if err == nil {
			t.Fatalf("want a Go error even though it is not a message box")
		}
		for _, verbatim := range []string{
			verbatimDoesNotExist, verbatimCorrupt, verbatimOldTED,
			verbatimNoGlobalHeader, verbatimNoMissionDefintion,
		} {
			if strings.Contains(err.Error(), verbatim) {
				t.Fatalf("silent miss leaked a verbatim diagnostic: %q contains %q", err.Error(), verbatim)
			}
		}
		if len(sink.Messages) != 0 {
			t.Fatalf("silent miss must not report to the sink: %v", sink.Messages)
		}
	})
}

// TestOldTEDAbsentVsEmptyValue is the WU-19-14 regression: Old TED fires only
// when the missionfile key is absent from the MISSION%d block; an authored
// empty value is present and instead builds Maps\.OTA, which fails to open
// and raises "no mission defintion" [08 R-CAMP-01 §11 point 2].
func TestOldTEDAbsentVsEmptyValue(t *testing.T) {
	t.Run("absent key", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"camps/Absent.tdf": "[HEADER]\n{\n}\n[MISSION0]\n{\n    missionname=First;\n}\n",
		})
		_, err := LoadCampaignWithSink(fs, "camps/Absent.tdf", 0, 0, 1, nil)
		if err == nil || err.Error() != verbatimOldTED {
			t.Fatalf("want Old TED for an absent missionfile key, got %v", err)
		}
	})
	t.Run("empty value", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"camps/Empty.tdf": "[HEADER]\n{\n}\n[MISSION0]\n{\n    missionfile=;\n    missionname=First;\n}\n",
		})
		_, err := LoadCampaignWithSink(fs, "camps/Empty.tdf", 0, 0, 1, nil)
		if err == nil {
			t.Fatalf("want an error for an unresolvable empty missionfile")
		}
		if err.Error() == verbatimOldTED {
			t.Fatalf("an empty missionfile value must not raise Old TED, got %v", err)
		}
		want := "Hey, joker!  There is no mission defintion for this mission: "
		if err.Error() != want {
			t.Fatalf("empty missionfile: got %q want %q", err.Error(), want)
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

// TestTranslatedNameFallback exercises kinds 2/3's only fallback
// [08 R-CAMP-01 §11 point 1]: on a read/parse miss, the requested name is
// looked up once as a translated map display name in the translation table
// (case-insensitive equality on the translated text, first entry in
// source-sorted order); a hit retries with the source string as both display
// name and file name; no table, no entry, or a second miss fails silently.
// There is no directory scan and no edit-distance metric — the deleted
// Levenshtein search was invented. resolveOTAWithFallback is exercised
// directly (rather than through LoadWithType) because no production caller
// in this codebase yet plumbs a non-English "current language" this far —
// see the defaultLanguage comment in load.go.
func TestTranslatedNameFallback(t *testing.T) {
	alphaOTA := `
[GlobalHeader]
{
    missionname=Alpha;
    [Schema 0] { Type=Network 1; [specials] { [special0] { specialwhat=StartPos1; } } }
}
`
	t.Run("hit retries with the source name", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/Alpha.ota":         alphaOTA,
			"gamedata/translate.tdf": "[Alpha]\n{\n    German=Anfang;\n}\n",
		})
		ota, terrKey, err := resolveOTAWithFallback(fs, "German", "Anfang", nil)
		if err != nil {
			t.Fatalf("translated-name retry: %v", err)
		}
		if !strings.EqualFold(terrKey, "Alpha") {
			t.Fatalf("want terrain key Alpha, got %q", terrKey)
		}
		if ota == nil || !strings.EqualFold(ota.MissionName, "Alpha") {
			t.Fatalf("want OTA Alpha, got %+v", ota)
		}
	})
	t.Run("no table loaded fails silently", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/Alpha.ota": alphaOTA,
			// gamedata/translate.tdf is absent: "no table loaded" per §11.
		})
		sink := &CollectSink{}
		_, _, err := resolveOTAWithFallback(fs, "German", "Anfang", sink)
		if err == nil {
			t.Fatalf("want failure with no translation table")
		}
		if len(sink.Messages) != 0 {
			t.Fatalf("silent failure must not report to the sink: %v", sink.Messages)
		}
	})
	t.Run("malformed table reaches the mission loader", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"maps/Alpha.ota":         alphaOTA,
			"gamedata/translate.tdf": "[Alpha]{ German=Anfang",
		})
		sink := &CollectSink{}
		_, _, err := resolveOTAWithFallback(fs, "German", "Anfang", sink)
		if err == nil {
			t.Fatal("malformed translation table was accepted")
		}
		for _, want := range []string{"Parse error in .TDF File!", "Data field - ';' not found", "from file gamedata/translate.tdf"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("mission translation error %q does not contain %q", err, want)
			}
		}
		if len(sink.Messages) != 1 || sink.Messages[0] != err.Error() {
			t.Fatalf("mission sink = %v, want the returned parse diagnostic", sink.Messages)
		}
	})
	t.Run("second miss fails silently", func(t *testing.T) {
		fs := fsFromMapLoad(t, map[string]string{
			"gamedata/translate.tdf": "[Alpha]\n{\n    German=Anfang;\n}\n",
			// maps/Alpha.ota is deliberately absent: the retried source name
			// misses too, so nothing further is tried.
		})
		sink := &CollectSink{}
		_, _, err := resolveOTAWithFallback(fs, "German", "Anfang", sink)
		if err == nil {
			t.Fatalf("want failure on a second miss")
		}
		if len(sink.Messages) != 0 {
			t.Fatalf("silent failure must not report to the sink: %v", sink.Messages)
		}
	})
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
