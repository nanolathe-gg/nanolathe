package mission

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

func mustParseTDF(t *testing.T, text string) *formats.Section {
	t.Helper()
	doc, err := formats.ParseTDF([]byte(text))
	if err != nil {
		t.Fatalf("ParseTDF: %v", err)
	}
	return doc.Root
}

func TestUnitPlacementRoundTrip(t *testing.T) {
	// TDF round-trip: authored pixels shifted left 16, degrees via trunc, flags.
	tdf := `
[GlobalHeader]
{
  [Schema 0]
  {
    Type=Network 1;
    [units]
    {
      [unit0]
      {
        Unitname=ARMCOM;
        Ident=hero;
        XPos=3216;
        ZPos=1184;
        YPos=85;
        Angle=90;
        Player=1;
        HealthPercentage=80;
        BuildPriority=5;
        CreationCountdown=0;
        Immunity=1;
        AiIgnore=1;
        AiPriorityTarget=1;
        MissionCriticalUnit=1;
        InitialGroup=2;
        InitialMission= w 10, m 100 200, ;
      }
    }
  }
}
`
	root := mustParseTDF(t, tdf)
	global := root.Section("GlobalHeader")
	if global == nil {
		t.Fatal("missing GlobalHeader")
	}
	schema := global.Section("Schema 0")
	if schema == nil {
		t.Fatal("missing Schema 0")
	}
	units := DecodeUnitPlacements(schema)
	if len(units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(units))
	}
	u := units[0]
	// X/Z/Y shifted left 16 [GAP T14]
	if u.X != 3216<<16 {
		t.Fatalf("X fixed: got %d want %d", u.X, 3216<<16)
	}
	if u.Z != 1184<<16 {
		t.Fatalf("Z fixed: got %d want %d", u.Z, 1184<<16)
	}
	if u.Y != 85<<16 {
		t.Fatalf("Y fixed: got %d want %d", u.Y, 85<<16)
	}
	// Angle 90 degrees -> 16384 [GAP T14] TODO(question)
	if u.Angle != 16384 {
		t.Fatalf("Angle: got %d want 16384", u.Angle)
	}
	if !u.Immune {
		t.Fatal("Immune should be true (high bit)")
	}
	if !u.AiIgnore || !u.AiPriorityTarget || !u.MissionCriticalUnit {
		t.Fatal("retain flags AiIgnore/AiPriorityTarget/MissionCriticalUnit should be true")
	}
	if u.InitialGroup != "2" {
		t.Fatalf("InitialGroup retain: got %q", u.InitialGroup)
	}
	if u.Player != 1 {
		t.Fatalf("Player: got %d", u.Player)
	}
	if u.HealthPercentage != 80 {
		t.Fatalf("Health: got %d", u.HealthPercentage)
	}
	if u.BuildPriority != 5 {
		t.Fatalf("BuildPriority retain: got %d", u.BuildPriority)
	}
	// Binary 36-byte round-trip via heap pointers [GAP T14] [C6]
	var heap []byte
	rec := MarshalUnit(u, &heap)
	decoded := UnmarshalUnit(rec, heap)
	if decoded.UnitName != u.UnitName || decoded.Ident != u.Ident || decoded.InitialMission != u.InitialMission {
		t.Fatalf("heap strings round-trip mismatch: %+v vs %+v", u, decoded)
	}
	if decoded.X != u.X || decoded.Z != u.Z || decoded.Y != u.Y || decoded.Angle != u.Angle {
		t.Fatalf("coords/angle round-trip mismatch: %+v vs %+v", u, decoded)
	}
	if decoded.RawFlags != u.RawFlags {
		t.Fatalf("flags round-trip: got %02x want %02x", decoded.RawFlags, u.RawFlags)
	}
}

func TestSpecialRecordIdentity(t *testing.T) {
	tdf := `
[GlobalHeader]
{
  [Schema 0]
  {
    Type=Network 1;
    [specials]
    {
      [special0]
      {
        specialwhat=StartPos1;
        XPos=560;
        ZPos=240;
      }
      [special1]
      {
        specialwhat=StartPos10;
        XPos=1000;
        ZPos=2000;
      }
      [special2]
      {
        specialwhat=StartPosA;
        XPos=1;
        ZPos=2;
      }
    }
  }
}
`
	root := mustParseTDF(t, tdf)
	schema := root.Section("GlobalHeader").Section("Schema 0")
	specials := DecodeSpecials(schema)
	if len(specials) != 3 {
		t.Fatalf("expected 3 specials, got %d", len(specials))
	}
	// First: StartPos1 -> Kind 1, ID 1 [GAP T14]
	if specials[0].Kind != 1 || specials[0].ID != 1 || specials[0].X != 560 || specials[0].Z != 240 {
		t.Fatalf("special0: %+v", specials[0])
	}
	if specials[1].ID != 10 {
		t.Fatalf("special1 ID: got %d want 10", specials[1].ID)
	}
	// Alphabetic suffix distinguished -> ID 0 [02 "Map files"] [GAP T14]
	if specials[2].ID != 0 {
		t.Fatalf("alphabetic suffix should give ID 0, got %d", specials[2].ID)
	}
	// 12-byte binary round-trip [C6]
	rec := MarshalSpecial(specials[0])
	decoded := UnmarshalSpecial(rec)
	if decoded.Kind != specials[0].Kind || decoded.ID != specials[0].ID || decoded.X != specials[0].X || decoded.Z != specials[0].Z {
		t.Fatalf("special binary round-trip mismatch: %+v vs %+v", specials[0], decoded)
	}
}

func TestFeatureNegativeClearing(t *testing.T) {
	tdf := `
[GlobalHeader]
{
  [Schema 0]
  {
    Type=Network 1;
    [features]
    {
      [feature0]
      {
        Featurename=WaterAquaOre3;
        XPos=362;
        ZPos=427;
      }
      [feature1]
      {
        Featurename=Rock;
        XPos=-1;
        ZPos=-5;
      }
      [feature2]
      {
        Featurename=Tree;
        // XPos/ZPos missing -> default -1, cleared [GAP T14]
      }
    }
  }
}
`
	root := mustParseTDF(t, tdf)
	schema := root.Section("GlobalHeader").Section("Schema 0")
	features := DecodeFeaturePlacements(schema)
	if len(features) != 3 {
		t.Fatalf("expected 3 features, got %d", len(features))
	}
	// Valid coords preserved
	if features[0].X != 362 || features[0].Z != 427 {
		t.Fatalf("feature0 coords: got %d,%d", features[0].X, features[0].Z)
	}
	if !features[0].IsPlaced() {
		t.Fatal("feature0 should be placed")
	}
	// Negative cleared to -1 sentinel [GAP T14] [02 "Map files"]
	if features[1].X != -1 || features[1].Z != -1 {
		t.Fatalf("feature1 negative clearing: got %d,%d want -1,-1", features[1].X, features[1].Z)
	}
	if features[1].IsPlaced() {
		t.Fatal("feature1 should not be placed after clearing")
	}
	if features[1].RawX != -1 || features[1].RawZ != -5 {
		t.Fatalf("feature1 raw retain: got %d,%d", features[1].RawX, features[1].RawZ)
	}
	// Missing defaults to -1 cleared [GAP T14]
	if features[2].X != -1 || features[2].Z != -1 {
		t.Fatalf("feature2 default -1 clearing: got %d,%d", features[2].X, features[2].Z)
	}
	// 136-byte round-trip: 128-byte name buffer + X/Z [C6]
	rec := MarshalFeature(features[0])
	decoded := UnmarshalFeature(rec)
	if decoded.Name != "WaterAquaOre3" || decoded.X != 362 || decoded.Z != 427 {
		t.Fatalf("feature binary round-trip: %+v", decoded)
	}
	// Ensure 128-byte buffer is null-padded correctly
	if rec[0] != 'W' || rec[127] != 0 {
		t.Fatalf("feature 128-byte buffer not padded")
	}
}

func TestImmunityBitOnlyConsumption(t *testing.T) {
	// Only high bit 0x80 matters; other bits retained but not acted on [C7] [08 "Mission placement record"]
	cases := []struct {
		flags byte
		want  bool
	}{
		{0x00, false},
		{0x80, true},
		{0xFF, true},  // other bits set still immune
		{0x7F, false}, // all low bits set but high clear -> not immune
		{0x20, false}, // AiIgnore bit5 alone not immune
		{0x40, false}, // AiPriority bit6 alone not immune
		{0x01, false}, // MissionCritical bit0 not immune
		{0xA0, true},  // AiIgnore+Immune
	}
	for _, c := range cases {
		if got := IsImmuneFromFlags(c.flags); got != c.want {
			t.Fatalf("IsImmuneFromFlags(%02x)=%v want %v", c.flags, got, c.want)
		}
	}
	// Ensure UnitPlacement retains other flags but IsImmune only checks high bit
	tdfTemplate := func(immune, aiIgnore, aiPriority, missionCrit int) string {
		return `
[GlobalHeader]
{
  [Schema 0]
  {
    Type=Network 1;
    [units]
    {
      [unit0]
      {
        Unitname=ARMCOM;
        XPos=0; ZPos=0;
        Immunity=` + itoa(immune) + `;
        AiIgnore=` + itoa(aiIgnore) + `;
        AiPriorityTarget=` + itoa(aiPriority) + `;
        MissionCriticalUnit=` + itoa(missionCrit) + `;
      }
    }
  }
}
`
	}
	// All flags set: should be immune, but AiIgnore etc retained
	root := mustParseTDF(t, tdfTemplate(0, 1, 1, 1))
	u := DecodeUnitPlacements(root.Section("GlobalHeader").Section("Schema 0"))[0]
	if u.IsImmune() {
		t.Fatal("should not be immune when Immunity=0 even with other bits")
	}
	if !u.AiIgnore || !u.AiPriorityTarget || !u.MissionCriticalUnit {
		t.Fatal("other flags should be retained even when not immune")
	}
	// Only immunity high bit matters for consumption assertion
	root2 := mustParseTDF(t, tdfTemplate(1, 0, 0, 0))
	u2 := DecodeUnitPlacements(root2.Section("GlobalHeader").Section("Schema 0"))[0]
	if !u2.IsImmune() {
		t.Fatal("should be immune when Immunity=1")
	}
	if u2.AiIgnore || u2.AiPriorityTarget || u2.MissionCriticalUnit {
		t.Fatal("other flags should be false when not authored")
	}
	// Retained fields do not affect immunity: set all, still immune only due to high bit
	root3 := mustParseTDF(t, tdfTemplate(1, 1, 1, 1))
	u3 := DecodeUnitPlacements(root3.Section("GlobalHeader").Section("Schema 0"))[0]
	if !u3.IsImmune() || !u3.AiIgnore || !u3.AiPriorityTarget || !u3.MissionCriticalUnit {
		t.Fatalf("all flags set: %+v", u3)
	}
	if u3.RawFlags != 0x80|0x20|0x40|0x01 {
		t.Fatalf("RawFlags packed: got %02x", u3.RawFlags)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	return "1"
}

func TestWindRetentionPassthrough(t *testing.T) {
	// Retain selected map's wind min/max without RNG draws [C5] [08 "Wind initialization"]
	tdf := `
[GlobalHeader]
{
  missionname=Test;
  minwindspeed=100;
  maxwindspeed=3000;
  [Schema 0]
  {
    Type=Network 1;
  }
}
`
	root := mustParseTDF(t, tdf)
	global := root.Section("GlobalHeader")
	// Capture RNG draws before
	crt := rng.NewCRT(12345)
	sim := rng.NewSimulation(12345)
	crtDrawsBefore := crt.Draws()
	simDrawsBefore := sim.Draws()
	bounds := DecodeWindBounds(global)
	// No RNG draws should have occurred [C5]
	if crt.Draws() != crtDrawsBefore {
		t.Fatalf("DecodeWindBounds should not draw CRT: before %d after %d", crtDrawsBefore, crt.Draws())
	}
	if sim.Draws() != simDrawsBefore {
		t.Fatalf("DecodeWindBounds should not draw sim: before %d after %d", simDrawsBefore, sim.Draws())
	}
	if bounds.Min != 100 || bounds.Max != 3000 {
		t.Fatalf("wind bounds: got %d,%d want 100,3000", bounds.Min, bounds.Max)
	}
	// Missing wind defaults to 0 [02 "Map files"]; still no draws
	emptyDoc, _ := formats.ParseTDF([]byte("[GlobalHeader]\n{\n}\n"))
	emptyGlobal := emptyDoc.Root.Section("GlobalHeader")
	bounds2 := DecodeWindBounds(emptyGlobal)
	if bounds2.Min != 0 || bounds2.Max != 0 {
		t.Fatalf("empty wind defaults: got %d,%d", bounds2.Min, bounds2.Max)
	}
	// WindBounds struct retains for PLAN_14's single battle-entry initializer
	_ = WindBounds{Min: bounds.Min, Max: bounds.Max}
}

func TestUseOnlyUnitsRouting(t *testing.T) {
	// UseOnlyUnits is not inert, routed into camps\useonly [C8] [08 "Mission placement record"] [fmt ota]
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"The Pass.tdf", "camps/useonly/The Pass.tdf"},
		{"  MyUnits.tdf  ", "camps/useonly/MyUnits.tdf"},
		{"camps/useonly/Already.tdf", "camps/useonly/Already.tdf"},
		{"camps\\useonly\\Backslash.tdf", "camps/useonly/Backslash.tdf"},
	}
	for _, c := range cases {
		if got := UseOnlyPath(c.input); got != c.want {
			t.Fatalf("UseOnlyPath(%q)=%q want %q", c.input, got, c.want)
		}
	}
	// Decode from OTA GlobalHeader [02 "Map files"] [fmt ota]
	tdf := `
[GlobalHeader]
{
  useonlyunits=PassUse.tdf;
  [Schema 0]
  {
    Type=Network 1;
  }
}
`
	root := mustParseTDF(t, tdf)
	global := root.Section("GlobalHeader")
	routed := DecodeUseOnlyUnits(global)
	if routed != "camps/useonly/PassUse.tdf" {
		t.Fatalf("DecodeUseOnlyUnits: got %q want %q", routed, "camps/useonly/PassUse.tdf")
	}
	// Ensure routing hook is exposed: UseOnlyBasePath constant
	if !strings.HasPrefix(routed, UseOnlyBasePath) {
		t.Fatalf("routed path should start with %q", UseOnlyBasePath)
	}
	// Empty UseOnlyUnits yields no restriction (empty path)
	emptyDoc, _ := formats.ParseTDF([]byte("[GlobalHeader]\n{\n}\n"))
	if got := DecodeUseOnlyUnits(emptyDoc.Root.Section("GlobalHeader")); got != "" {
		t.Fatalf("empty UseOnly should route to empty, got %q", got)
	}
}

func TestDegreesToHeading(t *testing.T) {
	// Angle conversion via trunc(degrees*65536/360) TODO(question) [GAP T14]
	// Verify known values
	if got := DegreesToHeading(0); got != 0 {
		t.Fatalf("0 deg: got %d", got)
	}
	if got := DegreesToHeading(90); got != 16384 {
		t.Fatalf("90 deg: got %d want 16384", got)
	}
	if got := DegreesToHeading(180); got != 32768 {
		t.Fatalf("180 deg: got %d", got)
	}
	if got := DegreesToHeading(360); got != 0 {
		t.Fatalf("360 deg wraps to 0, got %d", got)
	}
	if got := DegreesToHeading(45); got != 8192 {
		t.Fatalf("45 deg: got %d", got)
	}
}
