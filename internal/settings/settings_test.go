package settings

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// Nanolathe preferences migrate independently of the retail display block
// (DESIGN_GPU_RENDERER §13.5, §14.6).
func TestPresentationPreferencesLoadAndRoundTrip(t *testing.T) {
	one := DefaultEffectSwitch
	// The five effect switches default on, so a value the case does not name is
	// the default. want builds a block from the two named fields plus overrides.
	want := func(renderer string, fps int, effects ...int) Presentation {
		p := DefaultPresentation()
		p.Renderer, p.FPS = renderer, fps
		fields := []*int{&p.Water, &p.Lighting, &p.Finish, &p.Distortion, &p.Marks}
		for i, v := range effects {
			*fields[i] = v
		}
		return p
	}
	if got := Defaults().Presentation; got != want("modern", 60) {
		t.Fatalf("default presentation = %+v", got)
	}
	for _, tc := range []struct {
		name string
		body string
		want Presentation
	}{
		{"older file", `{"version":1}`, want("modern", 60)},
		{"missing fps", `{"version":1,"presentation":{"renderer":"classic"}}`, want("classic", 60)},
		{"display refresh", `{"version":1,"presentation":{"renderer":"modern","fps":0}}`, want("modern", 0)},
		{"arbitrary CLI cap", `{"version":1,"presentation":{"renderer":"classic","fps":90}}`, want("classic", 90)},
		{"invalid preferences", `{"version":1,"presentation":{"renderer":"unknown","fps":-1}}`, want("modern", 60)},
		// A file that omits the effect keys entirely still decodes to the
		// defaults, and a stored 0 is "off" and survives the round trip.
		{"effects off", `{"version":1,"presentation":{"renderer":"modern","fps":60,"water":0,"lighting":0,"finish":0,"distortion":0,"marks":0}}`,
			want("modern", 60, 0, 0, 0, 0, 0)},
		{"one effect off", `{"version":1,"presentation":{"renderer":"modern","fps":60,"distortion":0}}`,
			want("modern", 60, one, one, one, 0)},
		{"negative effect repaired", `{"version":1,"presentation":{"renderer":"modern","fps":60,"marks":-3}}`,
			want("modern", 60)},
		{"trail strength zero", `{"version":1,"presentation":{"trailStrength":0}}`,
			func() Presentation { p := want("modern", 60); p.TrailStrength = 0; return p }()},
		{"trail strength capped", `{"version":1,"presentation":{"trailStrength":250}}`,
			func() Presentation { p := want("modern", 60); p.TrailStrength = MaxTrailStrength; return p }()},
		{"negative trail strength repaired", `{"version":1,"presentation":{"trailStrength":-1}}`,
			want("modern", 60)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			if s.Presentation != tc.want {
				t.Fatalf("loaded presentation = %+v, want %+v", s.Presentation, tc.want)
			}
			if err := s.SaveTo(path); err != nil {
				t.Fatal(err)
			}
			s, err = LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			if s.Presentation != tc.want {
				t.Fatalf("round-trip presentation = %+v, want %+v", s.Presentation, tc.want)
			}
		})
	}
}

func TestLegacyTeamNanosprayPreferenceIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"presentation":{"teamNanospray":1,"teamColorNanolathe":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Presentation.TeamColorNanolathe != 1 {
		t.Fatal("Community team colour setting was lost")
	}
	if err := s.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"teamNanospray"`)) {
		t.Fatalf("legacy setting survived save: %s", data)
	}
}

// TestDefaultsMatchRetail locks the missing-value block the startup reader
// installs [02 "Settings"][07 §10].
func TestDefaultsMatchRetail(t *testing.T) {
	s := Defaults()
	if s.Difficulty != 1 {
		t.Errorf("Difficulty = %d, want 1", s.Difficulty)
	}
	if s.SwitchAlt != DefaultSwitchAlt || s.SwitchAltEnabled() {
		t.Errorf("SwitchAlt = %d (enabled %t), want clear default", s.SwitchAlt, s.SwitchAltEnabled())
	}
	sk := s.Skirmish
	if sk.NumPlayers != 4 {
		t.Errorf("NumPlayers = %d, want 4", sk.NumPlayers)
	}
	for _, c := range []struct {
		name string
		got  int
	}{
		{"Difficulty", sk.Difficulty},
		{"Location", sk.Location},
		{"CommanderDeath", sk.CommanderDeath},
		{"Mapping", sk.Mapping},
		{"LineOfSight", sk.LineOfSight},
		{"LOSType", sk.LOSType},
	} {
		if c.got != 1 {
			t.Errorf("Skirmish.%s = %d, want 1", c.name, c.got)
		}
	}
	if len(sk.Players) != MaxPlayers {
		t.Fatalf("len(Players) = %d, want %d", len(sk.Players), MaxPlayers)
	}
	for i, p := range sk.Players {
		want := Player{Side: i & 1, Color: i, AllyGroup: 5, Metal: 1000, Energy: 1000}
		if p != want {
			t.Errorf("Players[%d] = %+v, want %+v", i, p, want)
		}
	}
	wantMessages := Messages{TextLines: 10, TextScroll: 10, ScreenChat: 1, UnitChatText: 5}
	if s.Messages != wantMessages {
		t.Errorf("Messages = %+v, want %+v [02 §3]", s.Messages, wantMessages)
	}
}

func TestTypedCommandValuesSaveRawAndNormalizeAtLoad(t *testing.T) {
	s := Defaults()
	s.ScrollSpeed = 0
	s.InterfaceType = 7
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored Settings
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ScrollSpeed != 0 || stored.InterfaceType != 7 {
		t.Fatalf("Save rewrote typed-command settings to scroll=%d interface=%d", stored.ScrollSpeed, stored.InterfaceType)
	}
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ScrollSpeed != 0 || loaded.InterfaceType != InterfaceTypeRightClick {
		t.Fatalf("Load normalized typed-command settings to scroll=%d interface=%d", loaded.ScrollSpeed, loaded.InterfaceType)
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")

	// A file that was never written yields the defaults and no error.
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom(missing): %v", err)
	}
	if got.Skirmish.NumPlayers != DefaultNumPlayers {
		t.Fatalf("missing file did not yield defaults: %+v", got.Skirmish)
	}

	want := Defaults()
	want.Difficulty = 2
	want.Skirmish.Map = "Comet Catcher"
	want.Skirmish.NumPlayers = 3
	want.Skirmish.Mapping = 0
	want.Messages = Messages{TextLines: 20, TextScroll: 0, ScreenChat: 0, UnitChatText: 10}
	want.SwitchAlt = 7 // only the stored low bit survives SaveTo.
	want.Skirmish.Players[0] = Player{Controller: 1, Side: 1, Color: 4, AllyGroup: 0, Metal: 2500, Energy: 500}
	want.Skirmish.Players[1] = Player{Controller: 2, Side: 0, Color: 0, AllyGroup: 1, Metal: 200, Energy: 200}
	if err := want.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	got, err = LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.Difficulty != want.Difficulty || got.Skirmish.Map != want.Skirmish.Map ||
		got.Skirmish.NumPlayers != want.Skirmish.NumPlayers || got.Skirmish.Mapping != want.Skirmish.Mapping {
		t.Errorf("scalars round-tripped as %+v, want %+v", got, want)
	}
	// TextScroll and ScreenChat are stored zeros here, not absent values, so
	// they must survive round-tripping rather than being repaired back to
	// their defaults.
	if got.Messages != want.Messages {
		t.Errorf("Messages round-tripped as %+v, want %+v", got.Messages, want.Messages)
	}
	if got.SwitchAlt != 1 || !got.SwitchAltEnabled() {
		t.Errorf("SwitchAlt round-tripped as %d (enabled %t), want normalized set bit", got.SwitchAlt, got.SwitchAltEnabled())
	}
	// The zeros in these two rows are stored choices, not absent values, so
	// they must survive the normalization the loader runs.
	for i := 0; i < 2; i++ {
		if got.Skirmish.Players[i] != want.Skirmish.Players[i] {
			t.Errorf("Players[%d] = %+v, want %+v", i, got.Skirmish.Players[i], want.Skirmish.Players[i])
		}
	}
}

func TestLoadRejectsUnknownVersionAndGarbage(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ name, body string }{
		{"garbage.json", "not json at all"},
		{"version.json", `{"version":99,"difficulty":0}`},
	} {
		path := filepath.Join(dir, c.name)
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := LoadFrom(path)
		if err == nil {
			t.Errorf("%s: want an error", c.name)
		}
		if got.Difficulty != DefaultDifficulty || got.Skirmish.NumPlayers != DefaultNumPlayers {
			t.Errorf("%s: want defaults alongside the error, got %+v", c.name, got)
		}
	}
}

// TestNormalizeFillsShortPlayerArray covers the hand-edited file: rows that are
// simply absent get retail's per-slot defaults, and the count is clamped.
func TestNormalizeFillsShortPlayerArray(t *testing.T) {
	s := Skirmish{NumPlayers: 42, Players: []Player{{Controller: 1, Color: 7}}}
	s.Normalize()
	if s.NumPlayers != MaxPlayers {
		t.Errorf("NumPlayers = %d, want %d", s.NumPlayers, MaxPlayers)
	}
	if len(s.Players) != MaxPlayers {
		t.Fatalf("len(Players) = %d, want %d", len(s.Players), MaxPlayers)
	}
	if s.Players[0] != (Player{Controller: 1, Color: 7}) {
		t.Errorf("present row was rewritten: %+v", s.Players[0])
	}
	if s.Players[3] != DefaultPlayer(3) {
		t.Errorf("Players[3] = %+v, want %+v", s.Players[3], DefaultPlayer(3))
	}
}

func TestPathHonoursOverrideAndXDG(t *testing.T) {
	t.Setenv(EnvPath, "/tmp/explicit.json")
	if got, err := Path(); err != nil || got != "/tmp/explicit.json" {
		t.Fatalf("Path() = %q, %v; want the override", got, err)
	}
	t.Setenv(EnvPath, "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	want := filepath.Join("/tmp/xdg", "nanolathe", "settings.json")
	if got, err := Path(); err != nil || got != want {
		t.Fatalf("Path() = %q, %v; want %q", got, err, want)
	}
}

// TestLoadFromDistinguishesAbsentFromZero locks the decode contract: a field
// the file omits keeps its retail default, and a field the file stores as zero
// stays zero.
//
// Several skirmish rules default to 1 while 0 is an equally valid stored
// choice, so Normalize cannot separate the two cases. LoadFrom decodes over
// Defaults() to make the separation before Normalize runs; decoding over a
// zero value silently turned every omitted rule off [02 "Settings"][07 §10].
func TestLoadFromDistinguishesAbsentFromZero(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return path
	}

	t.Run("absent fields keep retail defaults", func(t *testing.T) {
		got, err := LoadFrom(write(t, `{"version":`+strconv.Itoa(FileVersion)+`}`))
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		for _, tc := range []struct {
			name string
			got  int
			want int
		}{
			{"CommanderDeath", got.Skirmish.CommanderDeath, DefaultCommanderDeath},
			{"Mapping", got.Skirmish.Mapping, DefaultMapping},
			{"LineOfSight", got.Skirmish.LineOfSight, DefaultLineOfSight},
			{"LOSType", got.Skirmish.LOSType, DefaultLOSType},
			{"Location", got.Skirmish.Location, DefaultLocation},
			{"Difficulty", got.Difficulty, DefaultDifficulty},
			{"NumPlayers", got.Skirmish.NumPlayers, DefaultNumPlayers},
			{"ScrollSpeed", got.ScrollSpeed, DefaultScrollSpeed},
			{"TextLines", got.Messages.TextLines, DefaultTextLines},
			{"TextScroll", got.Messages.TextScroll, DefaultTextScroll},
			{"ScreenChat", got.Messages.ScreenChat, DefaultScreenChat},
			{"UnitChatText", got.Messages.UnitChatText, DefaultUnitChatText},
		} {
			if tc.got != tc.want {
				t.Errorf("absent %s = %d, want the default %d", tc.name, tc.got, tc.want)
			}
		}
	})

	t.Run("explicit zero is retained", func(t *testing.T) {
		body := `{"version":` + strconv.Itoa(FileVersion) + `,"skirmish":{` +
			`"commanderDeath":0,"mapping":0,"lineOfSight":0,"losType":0,"location":0},` +
			`"messages":{"textLines":0,"textScroll":0,"screenChat":0,"unitChatText":0}}`
		got, err := LoadFrom(write(t, body))
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		for _, tc := range []struct {
			name string
			got  int
		}{
			{"CommanderDeath", got.Skirmish.CommanderDeath},
			{"Mapping", got.Skirmish.Mapping},
			{"LineOfSight", got.Skirmish.LineOfSight},
			{"LOSType", got.Skirmish.LOSType},
			{"Location", got.Skirmish.Location},
			{"TextLines", got.Messages.TextLines},
			{"TextScroll", got.Messages.TextScroll},
			{"ScreenChat", got.Messages.ScreenChat},
			{"UnitChatText", got.Messages.UnitChatText},
		} {
			if tc.got != 0 {
				t.Errorf("stored %s = %d, want the stored 0", tc.name, tc.got)
			}
		}
	})

	t.Run("switch alt keeps only bit zero", func(t *testing.T) {
		for _, tc := range []struct {
			stored int
			want   int
		}{
			{stored: 0, want: 0},
			{stored: 6, want: 0},
			{stored: 7, want: 1},
			{stored: -1, want: 1},
		} {
			body := `{"version":` + strconv.Itoa(FileVersion) + `,"switchAlt":` + strconv.Itoa(tc.stored) + `}`
			got, err := LoadFrom(write(t, body))
			if err != nil {
				t.Fatalf("LoadFrom(%d): %v", tc.stored, err)
			}
			if got.SwitchAlt != tc.want {
				t.Errorf("SwitchAlt %d loaded as %d, want %d", tc.stored, got.SwitchAlt, tc.want)
			}
		}
	})

	t.Run("missing player rows get per-slot defaults", func(t *testing.T) {
		body := `{"version":` + strconv.Itoa(FileVersion) +
			`,"skirmish":{"players":[{"controller":1,"allyGroup":0}]}}`
		got, err := LoadFrom(write(t, body))
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
		if len(got.Skirmish.Players) != MaxPlayers {
			t.Fatalf("player rows = %d, want %d", len(got.Skirmish.Players), MaxPlayers)
		}
		// The stored row keeps its explicit zero ally group and inherits the
		// per-value defaults it omitted.
		if got.Skirmish.Players[0].AllyGroup != 0 {
			t.Errorf("stored ally group = %d, want the stored 0", got.Skirmish.Players[0].AllyGroup)
		}
		if got.Skirmish.Players[0].Metal != DefaultMetal {
			t.Errorf("omitted metal = %d, want %d", got.Skirmish.Players[0].Metal, DefaultMetal)
		}
		// Rows past the end of the stored array are the default rows.
		if got.Skirmish.Players[7] != DefaultPlayer(7) {
			t.Errorf("row 7 = %+v, want %+v", got.Skirmish.Players[7], DefaultPlayer(7))
		}
	})

	t.Run("unsupported version yields defaults and an error", func(t *testing.T) {
		for _, body := range []string{`{"version":99}`, `{}`} {
			got, err := LoadFrom(write(t, body))
			if err == nil {
				t.Errorf("%s: expected an error", body)
			}
			if !reflect.DeepEqual(got, Defaults()) {
				t.Errorf("%s: settings = %+v, want Defaults()", body, got)
			}
		}
	})
}

func TestSavePreservesUnrecognizedSettings(t *testing.T) {
	for _, body := range []string{`{"version":99,"futurePreference":"keep me"}`, `{"version":1,"difficulty":`, `{"version":1,"difficulty":"invalid"}`} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadFrom(path)
		if err == nil {
			t.Fatal("expected an unsupported or corrupt settings error")
		}
		if err := loaded.SaveTo(path); err == nil {
			t.Fatal("saving defaults over unrecognized settings succeeded")
		}
		t.Setenv(EnvPath, path)
		if err := StoreDamageBars(true); err == nil {
			t.Fatal("damage bar toggle overwrote unrecognized settings")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != body {
			t.Fatalf("original settings changed: %q, %v", got, err)
		}
	}
}

// The glow strength is a percentage beside the glow switch: a file that omits
// it keeps the default, a stored 0 is kept (no glow), and a hand-edited value
// is repaired into 0..MaxGlowStrength (DESIGN_GPU_RENDERER §19.4).
func TestGlowStrengthLoadsAndClamps(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"absent", `{}`, DefaultGlowStrength},
		{"zero kept", `{"glowStrength":0}`, 0},
		{"half", `{"glowStrength":50}`, 50},
		{"capped", `{"glowStrength":350}`, MaxGlowStrength},
		{"negative repaired", `{"glowStrength":-4}`, DefaultGlowStrength},
	} {
		path := filepath.Join(t.TempDir(), "settings.json")
		body := `{"version":` + strconv.Itoa(FileVersion) + `,"display":` + tc.body + `}`
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := LoadFrom(path)
		if err != nil {
			t.Fatalf("%s: LoadFrom: %v", tc.name, err)
		}
		if got.Display.GlowStrength != tc.want || got.Display.Glow != DefaultGlow {
			t.Errorf("%s: glowStrength %d glow %d, want %d and the default switch", tc.name, got.Display.GlowStrength, got.Display.Glow, tc.want)
		}
	}
}

// The `mutators` and `mod` keys are stored and round-tripped verbatim
// (docs/DESIGN_MODS_MUTATORS.md §4.3, §6.6). This package does not validate
// mutator entries — it does not import content — so an entry its reader will
// refuse still survives a save; an empty set and an unselected mod are
// omitted from the file.
func TestMutatorsAndModRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := Defaults()
	want.Mutators = map[string]string{"buildSpeed": "2", "buildCost": "0.5", "notAMutator": "9"}
	want.Mod = ModSelection{ID: "prota", Version: "4.6"}
	if err := want.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Mutators, want.Mutators) || got.Mod != want.Mod {
		t.Fatalf("round trip = %v %+v, want %v %+v", got.Mutators, got.Mod, want.Mutators, want.Mod)
	}

	empty := Defaults()
	empty.Mutators = map[string]string{}
	if err := empty.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"mutators"`)) || bytes.Contains(data, []byte(`"mod"`)) {
		t.Fatalf("an empty set and an unselected mod must be omitted:\n%s", data)
	}
	got, err = LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mutators != nil || got.Mod != (ModSelection{}) {
		t.Fatalf("absent keys loaded as %v %+v", got.Mutators, got.Mod)
	}
}

// The `modernAI` block is stored and round-tripped verbatim, like the
// mutators: this package does not know the brain's keys, so an entry the
// desktop command will refuse still survives a save. A value may be written
// as a string or a number; a value of another type fails the parse rather
// than being dropped, and an empty block is omitted from the file.
func TestModernAIBlockRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	file := `{"version": 1, "modernAI": {"all": {"style": "eco", "jitter": 0},
		"difficulty": {"hard": {"w_army": 120}}, "players": {"2": {"style": "tower", "notAKey": "1"}}}}`
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	want := ModernAI{
		All:        AIParams{"style": "eco", "jitter": "0"},
		Difficulty: map[string]AIParams{"hard": {"w_army": "120"}},
		Players:    map[string]AIParams{"2": {"style": "tower", "notAKey": "1"}},
	}
	if !reflect.DeepEqual(got.ModernAI, want) {
		t.Fatalf("loaded %+v, want %+v", got.ModernAI, want)
	}
	if err := got.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	again, err := LoadFrom(path)
	if err != nil || !reflect.DeepEqual(again.ModernAI, want) {
		t.Fatalf("round trip %+v (%v), want %+v", again.ModernAI, err, want)
	}

	if err := os.WriteFile(path, []byte(`{"version": 1, "modernAI": {"all": {"jitter": false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(path); err == nil {
		t.Fatal("a boolean value loaded")
	}

	empty := Defaults()
	empty.ModernAI = ModernAI{All: AIParams{}, Players: map[string]AIParams{}}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := empty.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"modernAI"`)) {
		t.Fatalf("an empty block must be omitted:\n%s", data)
	}
}

// A skirmish row's AI is "classic" or absent, and absence is the Modern AI:
// "classic" in any case reads as Classic, while "modern", an unknown word and
// no word at all read as Modern, and a save writes the word only for a
// Classic row (user decision 2026-09-25).
func TestSkirmishRowAIRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	file := `{"version": 1, "skirmish": {"numPlayers": 4, "players": [
		{"controller": 1}, {"controller": 2, "ai": "Modern"}, {"controller": 2, "ai": " Classic "},
		{"controller": 2, "ai": "genius"}, {"controller": 2}]}}`
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"", "", PlayerAIClassic, "", ""} {
		if got.Skirmish.Players[i].AI != want {
			t.Fatalf("row %d AI %q, want %q", i, got.Skirmish.Players[i].AI, want)
		}
	}
	if err := got.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), `"ai"`); n != 1 || !strings.Contains(string(data), `"ai": "classic"`) {
		t.Fatalf("the saved file names a row AI %d times, want the Classic row once:\n%s", n, data)
	}
}

// A file written before the per-row choice existed names no row AI, so every
// computer row loads on the Modern AI; a file written by the first per-row
// encoding stored "modern" for a Modern row and nothing for a Classic one,
// and every row of it loads Modern too (user decision 2026-09-25: old rows
// switch to Modern).
func TestOldSettingsRowsLoadModern(t *testing.T) {
	for name, file := range map[string]string{
		"before the choice":       `{"version": 1, "gameplay": "strict-3.1", "skirmish": {"numPlayers": 3, "players": [{"controller": 1}, {"controller": 2}, {"controller": 2}]}}`,
		"the first row encoding":  `{"version": 1, "gameplay": "strict-3.1", "skirmish": {"numPlayers": 3, "players": [{"controller": 1}, {"controller": 2, "ai": "modern"}, {"controller": 2}]}}`,
		"no skirmish rows at all": `{"version": 1, "gameplay": "strict-3.1"}`,
	} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := LoadFrom(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Gameplay != gameplay.Strict31 {
			t.Fatalf("%s: gameplay %q", name, got.Gameplay)
		}
		for i, p := range got.Skirmish.Players {
			if p.AI != "" {
				t.Fatalf("%s: row %d AI %q, want the Modern default", name, i, p.AI)
			}
		}
	}
}

// The retired modern-ai selection loads as Modern with every row on the
// Modern AI, Classic rows included, which is the game it selected; saving
// it back writes neither the word nor a Classic row.
func TestTheRetiredModernAISelectionLoadsAsModernWithModernRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	file := `{"version": 1, "gameplay": "modern-ai", "skirmish": {"numPlayers": 3, "players": [
		{"controller": 1}, {"controller": 2, "ai": "classic"}, {"controller": 2}]}}`
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gameplay != gameplay.Modern {
		t.Fatalf("gameplay %q, want modern", got.Gameplay)
	}
	for i, p := range got.Skirmish.Players {
		if p.AI != "" {
			t.Fatalf("row %d AI %q, want the Modern AI", i, p.AI)
		}
	}
	if err := got.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "modern-ai") || strings.Contains(string(data), `"ai"`) {
		t.Fatalf("the saved file keeps the retired selection:\n%s", data)
	}
}
