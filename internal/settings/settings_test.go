package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

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
