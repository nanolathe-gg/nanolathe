package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// campaignVisOTA authors a mission whose [GlobalHeader] carries exactly the two
// visibility keys the OTA loader stores into the single-player option words
// [08 R-SKIR-01 §4]. `keys` is inserted verbatim so a case can omit them and
// exercise the accessor defaults.
func campaignVisOTA(keys string) string {
	return "[GlobalHeader]\n{\n" + keys +
		"[Schema 0]\n{\nType=Easy;\n[units]\n{\n}\n[specials]\n{\n}\n[features]\n{\n}\n}\n}\n"
}

// campaignVisSession builds a campaign session from an authored OTA and binds
// its services, which is where the visibility mode word is written [03 §3.1].
// The synthetic constructor cannot resolve a TNT from the hand-authored
// catalog, so the fixture terrain is installed and the services rebuilt against
// it exactly as the other composition fixtures do.
func campaignVisSession(t *testing.T, ota string) *Session {
	t.Helper()
	fs := fsWithMap(t, ota)
	s, err := NewSyntheticMissionForTest(fs, minimalCatalogForStrict(), "test.ota", 0)
	if err != nil {
		t.Fatalf("NewSyntheticMissionForTest: %v", err)
	}
	if s.Mission == nil || s.Mission.Type != mission.TypeCampaign {
		t.Fatalf("fixture is not a campaign session: %+v", s.Mission)
	}
	if s.World == nil {
		s.World = minimalTerrain()
		s.Features = nil
		s.Vis = nil
		s.Movement = nil
		s.Path = nil
		s.Build = nil
		s.Combat = nil
		if err := createAndBindServicesForTest(t, s); err != nil {
			t.Fatalf("createAndBindServices: %v", err)
		}
	}
	if s.Vis == nil {
		t.Fatal("visibility service not bound")
	}
	return s
}

// TestCampaignVisibilityModeComesFromMissionOTA locks the campaign source of
// the visibility mode word.
//
// Previously every non-skirmish session took a hard-coded `Unmapped + True`
// word. That is the registry default, and a campaign battle never sees it: the
// OTA loader rewrites the single-player option words from the mission's own
// `mapping` and `lineofsight` keys (each defaulting to 0 when absent) every
// time it parses a `[GlobalHeader]`, so the registry `Single*` triple is
// shadowed and reaches nothing [08 R-SKIR-01 §4]. Battle entry then copies the
// low bit of each into mode bits 0 and 1 [08 R-ENTRY-01 §2 step 4]; bit 2 is
// the LOSType option word, which that same loader forces to 1.
//
// Polarity, and the grid fills that pin it, are [03 R-VIS-01 §1]: bit 0 set →
// word grid starts empty (`Unmapped`); bit 0 clear → word grid starts with
// every player bit set (`Mapped`); bit 1 set → current-sight tracking on, byte
// grids start at 0; bit 1 clear → tracking off (`Permanent`), byte grids start
// at 1.
func TestCampaignVisibilityModeComesFromMissionOTA(t *testing.T) {
	cases := []struct {
		name      string
		keys      string
		wantMode  visibility.Mode
		wantWord  uint16
		wantByte  uint8
		wantPoint bool // VisiblePoint at a cell no observer has ever covered
	}{
		{
			// The stock campaign case: every Arm/Core mission OTA authors both
			// keys as 1, so `Unmapped + True` survives this change unaltered.
			name:     "mapping 1 lineofsight 1 is Unmapped plus True",
			keys:     "mapping=1;\nlineofsight=1;\n",
			wantMode: visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay,
			wantWord: 0x0000,
			wantByte: 0,
			// Nothing is published yet and current-sight tracking gates the
			// predicate, so unexplored ground is not visible.
			wantPoint: false,
		},
		{
			// `Mapped + Permanent` publishes nothing at all: both grids keep
			// their all-visible fill for the whole battle [03 R-VIS-01 §1].
			name:      "mapping 0 lineofsight 0 is Mapped plus Permanent",
			keys:      "mapping=0;\nlineofsight=0;\n",
			wantMode:  visibility.ModeTerrainRay,
			wantWord:  0x03FF,
			wantByte:  1,
			wantPoint: true,
		},
		{
			// `Unmapped + Permanent` still rasterizes into the word grid, so
			// the explored region grows from nothing [03 R-VIS-01 §1].
			name:      "mapping 1 lineofsight 0 is Unmapped plus Permanent",
			keys:      "mapping=1;\nlineofsight=0;\n",
			wantMode:  visibility.ModeHistoryEnabled | visibility.ModeTerrainRay,
			wantWord:  0x0000,
			wantByte:  1,
			wantPoint: false,
		},
		{
			// The loader forces the LOSType option word to 1, so bit 2 is set
			// here too: a campaign's line of sight is `Permanent` or `True` and
			// never `Circular`, whatever the mission authors [08 R-SKIR-01 §4].
			name:      "mapping 0 lineofsight 1 is Mapped plus True, never Circular",
			keys:      "mapping=0;\nlineofsight=1;\n",
			wantMode:  visibility.ModeCurrentEnabled | visibility.ModeTerrainRay,
			wantWord:  0x03FF,
			wantByte:  0,
			wantPoint: false,
		},
		{
			// Both keys absent take the accessor default 0 [02 map-global
			// keys], which is the loader's stored value — not the registry 1.
			name:      "absent keys take the key default 0, not the registry default 1",
			keys:      "",
			wantMode:  visibility.ModeTerrainRay,
			wantWord:  0x03FF,
			wantByte:  1,
			wantPoint: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := campaignVisSession(t, campaignVisOTA(tc.keys))
			if got := s.Vis.Mode(); got != tc.wantMode {
				t.Fatalf("mode word = %#03b, want %#03b [03 R-VIS-01 §1][08 R-SKIR-01 §4]", got, tc.wantMode)
			}
			// Bit 2 is the forced LOSType word for every campaign case.
			if !s.Vis.TerrainRay() {
				t.Fatal("campaign mode bit 2 must be the forced terrain ray [08 R-SKIR-01 §4]")
			}
			word := s.Vis.WordMask()
			if len(word) == 0 {
				t.Fatal("word grid not allocated")
			}
			for i, w := range word {
				if w != tc.wantWord {
					t.Fatalf("word grid cell %d = %#04x, want %#04x [03 R-VIS-01 §1]", i, w, tc.wantWord)
				}
			}
			local := visibility.PlayerID(localPlayerForSession(s))
			byteGrid := s.Vis.ByteGrid(local)
			if len(byteGrid) == 0 {
				t.Fatalf("byte grid for player %d not allocated", local)
			}
			for i, b := range byteGrid {
				if b != tc.wantByte {
					t.Fatalf("byte grid cell %d = %d, want %d [03 R-VIS-01 §1]", i, b, tc.wantByte)
				}
			}
			// Map pixel (32, 0, 32) projects to coverage cell (1, 1):
			// u = 32>>5, v = (32 - (0>>1))>>5 [03 §3.2].
			p := func(v int64) numeric.Fixed { return numeric.Fixed(v << 16) }
			if got := s.Vis.VisiblePoint(local, p(32), p(0), p(32)); got != tc.wantPoint {
				t.Fatalf("VisiblePoint at an uncovered cell = %v, want %v [03 §3.2]", got, tc.wantPoint)
			}
		})
	}
}

// TestSkirmishVisibilityModeStillComesFromTheSetupRecord is the regression lock
// on the other half of the battle-entry table: a kind-2 session reads the setup
// record's three fields and never the map's OTA keys [03 R-VIS-01 §1][08
// R-ENTRY-01 §2 step 4]. The fixture map authors `mapping=1; lineofsight=1;`,
// which the campaign rule would turn into `0b111`, while the setup record asks
// for something else in every row.
func TestSkirmishVisibilityModeStillComesFromTheSetupRecord(t *testing.T) {
	// A kind-2 load selects from the `Network N` schema family [08 "Schema
	// choice"], so this fixture authors one beside the same two OTA keys.
	fs := fsWithMap(t, "[GlobalHeader]\n{\nmapping=1;\nlineofsight=1;\n"+
		"[Schema 0]\n{\nType=Network 1;\n[units]\n{\n}\n[specials]\n{\n}\n[features]\n{\n}\n}\n}\n")
	m, err := mission.LoadWithType(fs, mission.TypeSkirmish, "test", 0, 2, nil)
	if err != nil {
		t.Fatalf("skirmish map load: %v", err)
	}
	if m.Type != mission.TypeSkirmish {
		t.Fatalf("fixture mission type = %d, want skirmish", m.Type)
	}
	cases := []struct {
		mapping, los, losType int
		want                  visibility.Mode
	}{
		{0, 0, 0, 0},
		{1, 0, 0, visibility.ModeHistoryEnabled},
		{0, 1, 0, visibility.ModeCurrentEnabled},
		{0, 0, 1, visibility.ModeTerrainRay},
		{1, 1, 1, visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("mapping%d_los%d_type%d", tc.mapping, tc.los, tc.losType), func(t *testing.T) {
			s := &Session{
				Mission: m,
				Skirmish: SkirmishConfig{
					MapName: "test", NumPlayers: 2,
					Mapping: tc.mapping, LineOfSight: tc.los, LOSType: tc.losType,
				},
			}
			if got := visibilityModeForSession(s); got != tc.want {
				t.Fatalf("skirmish mode word = %#03b, want %#03b [03 R-VIS-01 §1]", got, tc.want)
			}
		})
	}
}

// TestVisibilityModeWithoutAParsedGlobalHeaderKeepsTheRegistryDefault covers
// the one session that never met the OTA loader's writer. Nothing shadowed the
// start-up registry read, so the option words still hold `SingleMapping` /
// `SingleLineOfSight` / `SingleLOSType`, each defaulting to 1 and stored back
// on a miss [03 R-VIS-01 §1][03 R-TERR-01 §8].
func TestVisibilityModeWithoutAParsedGlobalHeaderKeepsTheRegistryDefault(t *testing.T) {
	want := visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay
	if got := visibilityModeForSession(nil); got != want {
		t.Fatalf("nil session mode = %#03b, want %#03b", got, want)
	}
	s := &Session{Mission: &mission.Mission{Type: mission.TypeCampaign}}
	if got := visibilityModeForSession(s); got != want {
		t.Fatalf("campaign without a parsed OTA = %#03b, want the registry default %#03b [03 R-TERR-01 §8]", got, want)
	}
}
