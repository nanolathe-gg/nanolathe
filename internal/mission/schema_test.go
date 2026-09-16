package mission

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

func mustLoadOTA(t *testing.T, src string) *formats.OTA {
	t.Helper()
	ota, err := formats.LoadOTA([]byte(src))
	if err != nil {
		t.Fatalf("LoadOTA: %v", err)
	}
	return ota
}

func TestCampaignPermutations(t *testing.T) {
	// All three difficulty literals present; each difficulty's try order selects the first in its permutation.
	src := `
[GlobalHeader]
{
    [Schema 0] { Type=Easy; }
    [Schema 1] { Type=Medium; }
    [Schema 2] { Type=Hard; }
}
`
	ota := mustLoadOTA(t, src)

	// difficulty 0: Easy,Medium,Hard -> Easy
	s, err := SelectCampaignSchema(ota, 0)
	if err != nil {
		t.Fatalf("difficulty 0: %v", err)
	}
	if s.Name != "Schema 0" {
		t.Fatalf("difficulty 0: want Schema 0 got %q", s.Name)
	}

	// difficulty 1: Medium,Easy,Hard -> Medium
	s, err = SelectCampaignSchema(ota, 1)
	if err != nil {
		t.Fatalf("difficulty 1: %v", err)
	}
	if s.Name != "Schema 1" {
		t.Fatalf("difficulty 1: want Schema 1 got %q", s.Name)
	}

	// difficulty 2: Hard,Medium,Easy -> Hard
	s, err = SelectCampaignSchema(ota, 2)
	if err != nil {
		t.Fatalf("difficulty 2: %v", err)
	}
	if s.Name != "Schema 2" {
		t.Fatalf("difficulty 2: want Schema 2 got %q", s.Name)
	}
}

func TestCampaignFallbackWithinPermutation(t *testing.T) {
	// Difficulty 0 prefers Easy; if Easy missing, falls to Medium.
	src := `
[GlobalHeader]
{
    [Schema 0] { Type=Medium; }
    [Schema 1] { Type=Hard; }
}
`
	ota := mustLoadOTA(t, src)
	s, err := SelectCampaignSchema(ota, 0)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if s.Name != "Schema 0" {
		t.Fatalf("fallback difficulty 0 missing Easy: want Schema 0 (Medium) got %q", s.Name)
	}

	// Difficulty 1 prefers Medium; if Medium missing, falls to Easy.
	src2 := `
[GlobalHeader]
{
    [Schema 0] { Type=Easy; }
    [Schema 1] { Type=Hard; }
}
`
	ota2 := mustLoadOTA(t, src2)
	s, err = SelectCampaignSchema(ota2, 1)
	if err != nil {
		t.Fatalf("fallback 2: %v", err)
	}
	if s.Name != "Schema 0" {
		t.Fatalf("fallback difficulty 1 missing Medium: want Schema 0 (Easy) got %q", s.Name)
	}

	// Difficulty 2 prefers Hard; if Hard missing, falls to Medium.
	src3 := `
[GlobalHeader]
{
    [Schema 0] { Type=Easy; }
    [Schema 1] { Type=Medium; }
}
`
	ota3 := mustLoadOTA(t, src3)
	s, err = SelectCampaignSchema(ota3, 2)
	if err != nil {
		t.Fatalf("fallback 3: %v", err)
	}
	if s.Name != "Schema 1" {
		t.Fatalf("fallback difficulty 2 missing Hard: want Schema 1 (Medium) got %q", s.Name)
	}
}

func TestCampaignInvalidDifficulty(t *testing.T) {
	src := `
[GlobalHeader]
{
    [Schema 0] { Type=Easy; }
    [Schema 1] { Type=Medium; }
    [Schema 2] { Type=Hard; }
}
`
	ota := mustLoadOTA(t, src)
	for _, d := range []int{-1, 3, 99} {
		if _, err := SelectCampaignSchema(ota, d); err == nil {
			t.Fatalf("difficulty %d: want failure", d)
		} else if err.Error() != "No suitable schema type..." {
			t.Fatalf("difficulty %d: want verbatim No suitable schema type... got %q", d, err.Error())
		}
		if _, err := SelectSchemaForType(ota, TypeCampaign, d, 2); err == nil {
			t.Fatalf("SelectSchemaForType difficulty %d: want failure", d)
		} else if err.Error() != "No suitable schema type..." {
			t.Fatalf("SelectSchemaForType difficulty %d: verbatim mismatch got %q", d, err.Error())
		}
	}
}

func TestCampaignCaseInsensitive(t *testing.T) {
	src := `
[GlobalHeader]
{
    [Schema 0] { Type=eAsY; }
    [Schema 1] { Type=MEDIUM; }
    [Schema 2] { Type=hard; }
}
`
	ota := mustLoadOTA(t, src)
	s, err := SelectCampaignSchema(ota, 0)
	if err != nil || s.Name != "Schema 0" {
		t.Fatalf("case-insensitive 0: %v %q", err, s.Name)
	}
	s, err = SelectCampaignSchema(ota, 1)
	if err != nil || s.Name != "Schema 1" {
		t.Fatalf("case-insensitive 1: %v %q", err, s.Name)
	}
	s, err = SelectCampaignSchema(ota, 2)
	if err != nil || s.Name != "Schema 2" {
		t.Fatalf("case-insensitive 2: %v %q", err, s.Name)
	}
	// Network case-insensitive
	src2 := `
[GlobalHeader]
{
    [Schema 0]
    {
        Type=network 1;
        [specials] { [special0] { specialwhat=StartPos1; } }
    }
    [Schema 1]
    {
        Type=NETWORK 2;
        [specials] { [special0] { specialwhat=StartPos1; } [special1] { specialwhat=StartPos2; } }
    }
}
`
	ota2 := mustLoadOTA(t, src2)
	s, err = SelectNetworkSchema(ota2, 1)
	if err != nil || s.Name != "Schema 0" {
		t.Fatalf("network case-insensitive 1: %v %q", err, s.Name)
	}
	s, err = SelectNetworkSchema(ota2, 2)
	if err != nil || s.Name != "Schema 1" {
		t.Fatalf("network case-insensitive 2: %v %q", err, s.Name)
	}
}

func TestNetworkExactMatch(t *testing.T) {
	src := `
[GlobalHeader]
{
    [Schema 0]
    {
        Type=Network 1;
        [specials]
        {
            [special0] { specialwhat=StartPos1; }
            [special1] { specialwhat=StartPos2; }
        }
    }
    [Schema 1]
    {
        Type=Network 2;
        [specials]
        {
            [special0] { specialwhat=StartPos1; }
            [special1] { specialwhat=StartPos2; }
            [special2] { specialwhat=StartPos3; }
            [special3] { specialwhat=StartPos4; }
        }
    }
}
`
	ota := mustLoadOTA(t, src)
	// playerCount 2 -> Network 1 exact
	s, err := SelectNetworkSchema(ota, 2)
	if err != nil {
		t.Fatalf("exact 2: %v", err)
	}
	if s.Name != "Schema 0" || s.StartPositions != 2 {
		t.Fatalf("exact 2: want Schema 0 count 2 got %q %d", s.Name, s.StartPositions)
	}
	// playerCount 4 -> Network 2 exact
	s, err = SelectNetworkSchema(ota, 4)
	if err != nil {
		t.Fatalf("exact 4: %v", err)
	}
	if s.Name != "Schema 1" || s.StartPositions != 4 {
		t.Fatalf("exact 4: want Schema 1 count 4 got %q %d", s.Name, s.StartPositions)
	}
}

func TestNetworkZeroCountAcceptance(t *testing.T) {
	src := `
[GlobalHeader]
{
    [Schema 0]
    {
        Type=Network 1;
        [specials] { [special0] { specialwhat=StartPos1; } }
    }
    [Schema 1]
    {
        Type=Network 2;
        [specials] { [special0] { specialwhat=StartPos1; } [special1] { specialwhat=StartPos2; } }
    }
}
`
	ota := mustLoadOTA(t, src)
	s, err := SelectNetworkSchema(ota, 0)
	if err != nil {
		t.Fatalf("zero: %v", err)
	}
	if s.Name != "Schema 1" {
		t.Fatalf("zero: want Schema 1 (last accepted candidate) got %q", s.Name)
	}
	// Also through the typed entry point the load path uses.
	s, err = SelectSchemaForType(ota, TypeSkirmish, -1, 0)
	if err != nil {
		t.Fatalf("typed zero: %v", err)
	}
	if s.Name != "Schema 1" {
		t.Fatalf("typed zero: want Schema 1 got %q", s.Name)
	}
}

func TestNetworkLargestFallback(t *testing.T) {
	// No exact match: playerCount 5, candidates have 2 and 4 -> fallback largest 4 (Schema 1)
	src := `
[GlobalHeader]
{
    [Schema 0]
    {
        Type=Network 1;
        [specials]
        {
            [special0] { specialwhat=StartPos1; }
            [special1] { specialwhat=StartPos2; }
        }
    }
    [Schema 1]
    {
        Type=Network 2;
        [specials]
        {
            [special0] { specialwhat=StartPos1; }
            [special1] { specialwhat=StartPos2; }
            [special2] { specialwhat=StartPos3; }
            [special3] { specialwhat=StartPos4; }
        }
    }
    [Schema 2]
    {
        Type=Network 3;
        [specials]
        {
            [special0] { specialwhat=StartPos1; }
            [special1] { specialwhat=StartPos2; }
            [special2] { specialwhat=StartPos3; }
        }
    }
}
`
	ota := mustLoadOTA(t, src)
	s, err := SelectNetworkSchema(ota, 5)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if s.Name != "Schema 1" || s.StartPositions != 4 {
		t.Fatalf("fallback: want Schema 1 count 4 got %q %d", s.Name, s.StartPositions)
	}
	// Another case: playerCount 10 with largest 4 still
	s, err = SelectNetworkSchema(ota, 10)
	if err != nil {
		t.Fatalf("fallback 10: %v", err)
	}
	if s.Name != "Schema 1" {
		t.Fatalf("fallback 10: want Schema 1 got %q", s.Name)
	}
	// Order matters: if Network 2 (count 4) is largest but Network 3 has 3, best remains 4
}

func TestNetworkFallbackPrefersLargestNotFirst(t *testing.T) {
	// Ensure fallback picks largest overall, not first largest-seen immediate.
	// Schemas: N1=2, N2=6, N3=3 ; playerCount 5 no exact -> largest 6 (N2) not 2
	src := `
[GlobalHeader]
{
    [Schema 0]
    {
        Type=Network 1;
        [specials] { [special0] { specialwhat=StartPos1; } [special1] { specialwhat=StartPos2; } }
    }
    [Schema 1]
    {
        Type=Network 2;
        [specials]
        {
            [special0] { specialwhat=StartPos1; } [special1] { specialwhat=StartPos2; } [special2] { specialwhat=StartPos3; }
            [special3] { specialwhat=StartPos4; } [special4] { specialwhat=StartPos5; } [special5] { specialwhat=StartPos6; }
        }
    }
    [Schema 2]
    {
        Type=Network 3;
        [specials] { [special0] { specialwhat=StartPos1; } [special1] { specialwhat=StartPos2; } [special2] { specialwhat=StartPos3; } }
    }
}
`
	ota := mustLoadOTA(t, src)
	s, err := SelectNetworkSchema(ota, 5)
	if err != nil {
		t.Fatalf("largest fallback: %v", err)
	}
	if s.Name != "Schema 1" || s.StartPositions != 6 {
		t.Fatalf("largest fallback: want Schema 1 count 6 got %q %d", s.Name, s.StartPositions)
	}
}

func TestVerbatimDiagnostics(t *testing.T) {
	// Missing GlobalHeader -> Very bad news! No MSG!
	doc, err := formats.ParseTDF([]byte(`[NotGlobal] { }`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ota := &formats.OTA{Document: doc, Global: nil}
	if _, err := SelectCampaignSchema(ota, 0); err == nil || err.Error() != "Very bad news! No MSG!" {
		t.Fatalf("missing Global campaign: want verbatim Very bad news! No MSG! got %v", err)
	}
	if _, err := SelectNetworkSchema(ota, 2); err == nil || err.Error() != "Very bad news! No MSG!" {
		t.Fatalf("missing Global network: want verbatim got %v", err)
	}
	if _, err := SelectSchemaForType(ota, TypeCampaign, 0, 2); err == nil || err.Error() != "Very bad news! No MSG!" {
		t.Fatalf("missing Global typed: want verbatim got %v", err)
	}
	// No suitable schema type...
	src := `
[GlobalHeader]
{
    [Schema 0] { Type=Network 1; [specials] { [special0] { specialwhat=StartPos1; } } }
}
`
	ota2 := mustLoadOTA(t, src)
	// Campaign with no Easy/Medium/Hard
	if _, err := SelectCampaignSchema(ota2, 0); err == nil || err.Error() != "No suitable schema type..." {
		t.Fatalf("no suitable campaign: want verbatim got %v", err)
	}
	// Actually with no specials, count 0, playerCount 2 -> fallback largest 0 would be returned, so need case where no Network schemas at all
	src4 := `
[GlobalHeader]
{
    [Schema 0] { Type=Easy; }
}
`
	ota4 := mustLoadOTA(t, src4)
	if _, err := SelectNetworkSchema(ota4, 2); err == nil || err.Error() != "No suitable schema type..." {
		t.Fatalf("no suitable network: want verbatim got %v", err)
	}
	// Nil OTA
	if _, err := SelectSchemaForType(nil, TypeCampaign, 0, 2); err == nil || err.Error() != "Very bad news! No MSG!" {
		t.Fatalf("nil OTA: want Very bad news! got %v", err)
	}
}

func TestSchemaBeforePlacementOrdering(t *testing.T) {
	// C4: selection happens BEFORE placement records are instantiated.
	// Reflect in API: SelectSchemaForType returns Schema name; placement builder consumes it later.
	// Verify that selecting a schema does not require units/features to be present,
	// and that the returned name can be used to locate the schema section for later
	// placement building.
	src := `
[GlobalHeader]
{
    [Schema 0]
    {
        Type=Network 2;
        SurfaceMetal=5;
        [specials] { [special0] { specialwhat=StartPos1; } [special1] { specialwhat=StartPos2; } }
        [units] { [unit0] { Unitname=ARMCOM; XPos=100; ZPos=200; } }
    }
    [Schema 1]
    {
        Type=Network 1;
        SurfaceMetal=3;
        [specials] { [special0] { specialwhat=StartPos1; } }
    }
}
`
	ota := mustLoadOTA(t, src)
	s, err := SelectNetworkSchema(ota, 2)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if s.Name != "Schema 0" {
		t.Fatalf("want Schema 0 got %q", s.Name)
	}
	// Simulate placement builder consuming the name: locate section by OriginalName
	var chosen *formats.Section
	for _, sec := range ota.Global.Sections() {
		if sec.OriginalName == s.Name {
			chosen = sec
			break
		}
	}
	if chosen == nil {
		t.Fatalf("placement builder could not find schema %q", s.Name)
	}
	if v, _ := chosen.FirstValue("surfacemetal"); v != "5" {
		t.Fatalf("placement builder found wrong schema: surfacemetal %q want 5", v)
	}
	// Ensure selection did not mutate OTA placements (still present)
	if units := chosen.Section("units"); units == nil {
		t.Fatalf("units section missing after selection")
	}
}

func TestSelectSchemaForTypeDispatch(t *testing.T) {
	// Campaign map through the campaign type
	camp := `
[GlobalHeader]
{
    [Schema 0] { Type=Easy; }
    [Schema 1] { Type=Medium; }
    [Schema 2] { Type=Hard; }
}
`
	otaCamp := mustLoadOTA(t, camp)
	s, err := SelectSchemaForType(otaCamp, TypeCampaign, 1, 0)
	if err != nil || s.Name != "Schema 1" {
		t.Fatalf("campaign type: %v %q", err, s.Name)
	}
	// Network map through a skirmish type (difficulty is not consulted)
	net := `
[GlobalHeader]
{
    [Schema 0]
    {
        Type=Network 1;
        [specials] { [special0] { specialwhat=StartPos1; } [special1] { specialwhat=StartPos2; } }
    }
    [Schema 1]
    {
        Type=Network 2;
        [specials] { [special0] { specialwhat=StartPos1; } [special1] { specialwhat=StartPos2; } [special2] { specialwhat=StartPos3; } [special3] { specialwhat=StartPos4; } }
    }
}
`
	otaNet := mustLoadOTA(t, net)
	s, err = SelectSchemaForType(otaNet, TypeSkirmish, 0, 2) // difficulty 0 is ignored; Network 1 is selected
	if err != nil || s.Name != "Schema 0" {
		t.Fatalf("skirmish type: %v %q", err, s.Name)
	}
	// Ensure case-insensitive candidate comparison
	campCI := `
[GlobalHeader]
{
    [Schema 0] { Type=easy; }
}
`
	otaCI := mustLoadOTA(t, campCI)
	s, err = SelectSchemaForType(otaCI, TypeCampaign, 0, 0)
	if err != nil || s.Name != "Schema 0" {
		t.Fatalf("case-insensitive: %v %q", err, s.Name)
	}
}

// TestStartingResourcesComeFromSelectedSchema locks [02 R-MAP-01 §5]: the four
// starting-resource words are read with the chosen schema current, so the
// difficulty the campaign selected decides the treasury. Reading them from the
// [GlobalHeader] returned zero for every stock mission — Arm campaign mission 2
// authors HumanMetal=1000 per schema and started with nothing.
func TestStartingResourcesComeFromSelectedSchema(t *testing.T) {
	src := `
[GlobalHeader]
{
    HumanMetal=7;
    HumanEnergy=7;
    ComputerMetal=7;
    ComputerEnergy=7;
    [Schema 0] { Type=Easy;   HumanMetal=1000; HumanEnergy=1100; ComputerMetal=100; ComputerEnergy=200; }
    [Schema 1] { Type=Medium; HumanMetal=500;  HumanEnergy=600;  ComputerMetal=300; ComputerEnergy=400; }
}
`
	ota := mustLoadOTA(t, src)
	for _, tc := range []struct {
		difficulty int
		want       StartingResources
	}{
		{0, StartingResources{HumanMetal: 1000, HumanEnergy: 1100, ComputerMetal: 100, ComputerEnergy: 200}},
		{1, StartingResources{HumanMetal: 500, HumanEnergy: 600, ComputerMetal: 300, ComputerEnergy: 400}},
	} {
		schema, err := SelectCampaignSchema(ota, tc.difficulty)
		if err != nil {
			t.Fatalf("SelectCampaignSchema(%d): %v", tc.difficulty, err)
		}
		m := &Mission{OTA: ota, Schema: schema}
		if got := m.StartingResources(); got != tc.want {
			t.Fatalf("difficulty %d selected %q and read %+v, want %+v [02 R-MAP-01 §5]",
				tc.difficulty, schema.Name, got, tc.want)
		}
	}
}

// A schema that does not author a key falls back to a GlobalHeader-authored
// one; a key absent from both is the accessor default of zero.
func TestStartingResourcesFallBackToGlobalHeader(t *testing.T) {
	src := `
[GlobalHeader]
{
    HumanMetal=250;
    [Schema 0] { Type=Easy; HumanEnergy=900; }
}
`
	ota := mustLoadOTA(t, src)
	schema, err := SelectCampaignSchema(ota, 0)
	if err != nil {
		t.Fatalf("SelectCampaignSchema: %v", err)
	}
	m := &Mission{OTA: ota, Schema: schema}
	want := StartingResources{HumanMetal: 250, HumanEnergy: 900}
	if got := m.StartingResources(); got != want {
		t.Fatalf("mixed placement read %+v, want %+v", got, want)
	}
}
