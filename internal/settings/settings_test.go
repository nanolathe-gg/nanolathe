package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestDefaultsMatchRetail(t *testing.T) {
	s := Defaults()
	if s.Difficulty != 1 {
		t.Errorf("Difficulty = %d, want 1", s.Difficulty)
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
