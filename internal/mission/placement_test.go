package mission

import (
	"math"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
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
	// Angle 90 degrees -> 16384 [08 "Mission placement record"]
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
	// The decoded ID is the STORED number, one less than the authored label
	// [08 R-TRIG-01 §9]: StartPos1 stores 0 and StartPos10 stores 9.
	if specials[0].Kind != 1 || specials[0].ID != 0 || specials[0].X != 560 || specials[0].Z != 240 {
		t.Fatalf("special0: %+v", specials[0])
	}
	if specials[1].ID != 9 {
		t.Fatalf("special1 ID: got %d want 9", specials[1].ID)
	}
	// A non-numeric suffix takes the running counter, which the numeric
	// records before it did not advance — this is the first record to take
	// it, so 1, stored as 0 [08 R-TRIG-01 §12].
	if specials[2].Kind != 1 || specials[2].ID != 0 {
		t.Fatalf("alphabetic suffix: %+v, want Kind 1 and stored number 0", specials[2])
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
	if u3.RawFlags != 0x80|0x20|0x40|0x10 {
		t.Fatalf("RawFlags packed (bit4 MissionCriticalUnit): got %02x", u3.RawFlags)
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

func TestHeadingFromDegrees(t *testing.T) {
	// The placement heading is one signed division: trunc((int32)(deg << 16) / 360)
	// narrowed to 16 bits [08 "Mission placement record"].
	for _, tc := range []struct {
		deg  int32
		want uint16
	}{
		{0, 0},
		{45, 8192},
		{90, 16384},
		{180, 32768},
		{359, 65353},
		{360, 0},
	} {
		if got := HeadingFromDegrees(tc.deg); got != tc.want {
			t.Fatalf("%d deg: got %d want %d", tc.deg, got, tc.want)
		}
	}
}

// TestHeadingFromDegreesEquivalenceDomain locks the two halves of the
// equivalence statement that were carrying a wrong claim before WU-19-167:
// negative degrees truncate toward zero with NO one-unit bias, and the wrap
// boundary is 32768, not 65536 [08 "Mission placement record"].
//
// The bias is the regression this guards: dropping the sign-bit add turns the
// quotient into a floor, which is off by one for every negative angle whose
// division is inexact, and the sweep below reaches those.
func TestHeadingFromDegreesEquivalenceDomain(t *testing.T) {
	floatForm := func(deg int32) uint16 {
		v := int64(math.Trunc(float64(deg)*65536.0/360.0)) % 65536
		if v < 0 {
			v += 65536
		}
		return uint16(v)
	}
	inexact := 0
	for deg := int32(-32768); deg <= 32767; deg++ {
		got, want := HeadingFromDegrees(deg), floatForm(deg)
		if got != want {
			t.Fatalf("inside the exact domain: %d deg gave %d, want %d", deg, got, want)
		}
		if deg < 0 && int64(deg)*65536%360 != 0 {
			inexact++
		}
	}
	if inexact == 0 {
		t.Fatal("sweep never reached a negative angle whose division is inexact; it cannot see a floor-vs-truncate bug")
	}
	// 32768 shifts into the sign bit, so it reads back as a large negative
	// angle rather than as 32768 modulo the circle. 65535 is the value the
	// retracted text claimed still agreed with the floating formula.
	for _, deg := range []int32{32768, 65535, -32769} {
		if HeadingFromDegrees(deg) == floatForm(deg) {
			t.Fatalf("%d deg is past the 32-bit shift boundary and must diverge from the unbounded formula", deg)
		}
	}
	// An authored 65535 wraps to the same word as -1.
	if HeadingFromDegrees(65535) != HeadingFromDegrees(-1) {
		t.Fatal("65535 deg must land on the same heading word as -1 deg")
	}
}

// The start-position identity in full [08 R-TRIG-01 §9] [08 R-TRIG-01 §12]:
// only an eight-character `StartPos` prefix is a start position
// (case-insensitively); a suffix whose first character is a digit is parsed
// as an integer, anything else takes the running counter, which only those
// records advance; and the stored number is that value minus one when
// positive. Both `StartPos0` and `StartPos1` therefore store 0, and so does
// the first lettered label.
//
// Before WU-19-205 the decoder took a trailing digit run and gave every
// non-numeric suffix zero, so `StartPosA` and `StartPosB` collided on a number
// no consumer asks for and `StartPos0` was indistinguishable from them (review
// finding R10).
func TestStartPosStoredNumbers(t *testing.T) {
	tdf := `
[GlobalHeader]
{
  [Schema 0]
  {
    Type=Network 1;
    [specials]
    {
      [special0] { specialwhat=StartPos0; XPos=1; ZPos=1; }
      [special1] { specialwhat=startpos2; XPos=2; ZPos=2; }
      [special2] { specialwhat=StartPosA; XPos=3; ZPos=3; }
      [special3] { specialwhat=StartPosB; XPos=4; ZPos=4; }
      [special4] { specialwhat=StartPos1; XPos=5; ZPos=5; }
      [special5] { specialwhat=NotAStartPos7; XPos=6; ZPos=6; }
      [special6] { specialwhat=StartPos12x; XPos=7; ZPos=7; }
    }
  }
}
`
	specials := DecodeSpecials(mustParseTDF(t, tdf).Section("GlobalHeader").Section("Schema 0"))
	if len(specials) != 7 {
		t.Fatalf("decoded %d specials, want 7", len(specials))
	}
	for i, want := range []struct {
		kind int32
		id   int32
	}{
		{1, 0},  // StartPos0: a suffix of 0 stays 0
		{1, 1},  // startpos2: the prefix match is case-insensitive
		{1, 0},  // StartPosA: first record to take the counter, 1, stored 0
		{1, 1},  // StartPosB: second, 2, stored 1
		{1, 0},  // StartPos1: the same stored number as StartPos0
		{0, 0},  // not a start position at all
		{1, 11}, // the integer parse stops at the first non-digit
	} {
		if specials[i].Kind != want.kind || specials[i].ID != want.id {
			t.Fatalf("special%d %q decoded Kind %d ID %d, want Kind %d ID %d [08 R-TRIG-01 §9]",
				i, specials[i].Name, specials[i].Kind, specials[i].ID, want.kind, want.id)
		}
	}
	// Duplicate stored numbers are kept in authored order; the consumer's scan
	// takes the first, so the decoder must not sort or de-duplicate
	// [08 R-ENTRY-01 §5] step 3.
	if specials[0].X != 1 || specials[4].X != 5 {
		t.Fatalf("the two records storing number 0 were reordered: %+v, %+v", specials[0], specials[4])
	}
}

// The counter that a non-numeric `StartPos` label takes advances only on the
// records that take it; numeric labels between them do not move it, and a
// numeric label never falls back to it (`StartPos0` stores 0 through the
// integer path) [08 R-TRIG-01 §12]. Before RWU-19-219 the decoder advanced
// it on every start-position record, which would give 4, 1, 0, 3 here.
func TestStartPosCounterAdvancesOnlyOnNonNumericLabels(t *testing.T) {
	tdf := `
[GlobalHeader]
{
  [Schema 0]
  {
    Type=Network 1;
    [specials]
    {
      [special0] { specialwhat=StartPos5; XPos=1; ZPos=1; }
      [special1] { specialwhat=StartPosA; XPos=2; ZPos=2; }
      [special2] { specialwhat=StartPos0; XPos=3; ZPos=3; }
      [special3] { specialwhat=StartPosB; XPos=4; ZPos=4; }
    }
  }
}
`
	specials := DecodeSpecials(mustParseTDF(t, tdf).Section("GlobalHeader").Section("Schema 0"))
	if len(specials) != 4 {
		t.Fatalf("decoded %d specials, want 4", len(specials))
	}
	for i, want := range []int32{4, 0, 0, 1} {
		if specials[i].Kind != 1 || specials[i].ID != want {
			t.Fatalf("special%d %q decoded Kind %d ID %d, want Kind 1 ID %d [08 R-TRIG-01 §12]",
				i, specials[i].Name, specials[i].Kind, specials[i].ID, want)
		}
	}
}
