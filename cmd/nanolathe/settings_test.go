package main

import (
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/settings"
)

// TestSettingsRoundTripThroughShell locks the conversion in both directions:
// the retail row array, the six rule scalars, and the map name have to come
// back out of a saved block exactly as they went in, because that is the whole
// point of the file — the skirmish screen reopens on the last setup played.
func TestSettingsRoundTripThroughShell(t *testing.T) {
	maps := []string{"Anteer Straight", "Comet Catcher", "Painted Desert"}

	src := &gameShell{maps: maps}
	src.setup = newSkirmishMenuConfig(maps[0])
	src.setup.MapName = "Painted Desert"
	src.setup.NumPlayers = 3
	src.setup.Difficulty = 2
	src.setup.Location = 0
	src.setup.CommanderDeath = 0
	src.setup.Mapping = 0
	src.setup.LineOfSight = 1
	src.setup.LOSType = 0
	src.missionDifficultyValue = 0
	src.retailControllers = [session.SkirmishMaxPlayers]int{1, 2, 2}
	src.retailControllersSet = true
	src.setup.Players[0] = session.SkirmishPlayer{Side: 1, Color: 4, AllyGroup: 0, Metal: 2500, Energy: 700}
	src.setup.Players[1] = session.SkirmishPlayer{Side: 0, Color: 0, AllyGroup: 1, Metal: 200, Energy: 10000, Controller: 1}
	src.setup.Players[2] = session.SkirmishPlayer{Side: 1, Color: 9, AllyGroup: 1, Metal: 1000, Energy: 1000, Controller: 1}
	// No screen edits the message-column block, so it has to ride through
	// captureSettings/applySettings unchanged, the same as UnitLimit.
	src.messages = settings.Messages{TextLines: 20, TextScroll: 15, ScreenChat: 0, UnitChatText: 8}
	src.switchAlt = true

	blob := src.captureSettings()

	dst := &gameShell{maps: maps}
	dst.setup = newSkirmishMenuConfig(maps[0])
	dst.applySettings(blob)

	if dst.setup.MapName != "Painted Desert" || dst.mapIdx != 2 {
		t.Errorf("map = %q idx %d, want %q idx 2", dst.setup.MapName, dst.mapIdx, "Painted Desert")
	}
	if dst.missionDifficultyValue != 0 {
		t.Errorf("mission difficulty = %d, want 0", dst.missionDifficultyValue)
	}
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"NumPlayers", dst.setup.NumPlayers, 3},
		{"Difficulty", dst.setup.Difficulty, 2},
		{"Location", dst.setup.Location, 0},
		{"CommanderDeath", dst.setup.CommanderDeath, 0},
		{"Mapping", dst.setup.Mapping, 0},
		{"LineOfSight", dst.setup.LineOfSight, 1},
		{"LOSType", dst.setup.LOSType, 0},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if dst.retailControllers != src.retailControllers {
		t.Errorf("controllers = %v, want %v", dst.retailControllers, src.retailControllers)
	}
	if dst.messages != src.messages {
		t.Errorf("messages = %+v, want %+v", dst.messages, src.messages)
	}
	if !dst.switchAlt || blob.SwitchAlt != 1 {
		t.Errorf("SwitchAlt shell round trip: source %t, captured %d, destination %t; want set", src.switchAlt, blob.SwitchAlt, dst.switchAlt)
	}
	for i := 0; i < 3; i++ {
		a, b := src.setup.Players[i], dst.setup.Players[i]
		if a.Side != b.Side || a.Color != b.Color || a.AllyGroup != b.AllyGroup || a.Metal != b.Metal || a.Energy != b.Energy {
			t.Errorf("Players[%d] = %+v, want %+v", i, b, a)
		}
	}
	// The row values that skirmishConfigForStart actually reads must survive
	// too: row 0 is the human, rows 1 and 2 are computers.
	cfg := dst.skirmishConfigForStart(dst.setup.MapName)
	if cfg.NumPlayers != 3 {
		t.Fatalf("start config NumPlayers = %d, want 3", cfg.NumPlayers)
	}
	if cfg.Players[0].Controller != session.SkirmishDefaultController {
		t.Errorf("slot 0 controller = %d, want human", cfg.Players[0].Controller)
	}
	if cfg.Players[1].Controller == session.SkirmishDefaultController {
		t.Errorf("slot 1 controller = %d, want computer", cfg.Players[1].Controller)
	}
}

// TestBattleSwitchAltCapturesShellOrAttachedSettings keeps the bit at the
// battle-install seam. A live shell is authoritative for a menu-launched
// battle; direct --map composition has no shell and consumes the one loaded
// settings block. routeDigit then reads only this cached boolean.
func TestBattleSwitchAltCapturesShellOrAttachedSettings(t *testing.T) {
	fromShell := &battleSession{shell: &gameShell{switchAlt: false}}
	fromShell.applySwitchAltSetting(settings.Settings{SwitchAlt: 1})
	if fromShell.switchAlt {
		t.Fatal("shell-backed battle ignored its live clear SwitchAlt value")
	}

	direct := &battleSession{}
	direct.applySwitchAltSetting(settings.Settings{SwitchAlt: 7})
	if !direct.switchAlt {
		t.Fatal("direct battle did not capture the loaded SwitchAlt low bit")
	}
}

// TestApplySettingsRecoversFromAllRowsOpen guards the one stored state that
// would deadlock the screen: a block whose every row is Open leaves Start
// permanently refused, so the shell rebuilds the human row instead.
func TestApplySettingsRecoversFromAllRowsOpen(t *testing.T) {
	shell := &gameShell{maps: []string{"Anteer Straight"}}
	shell.setup = newSkirmishMenuConfig("Anteer Straight")

	blob := settings.Defaults()
	for i := range blob.Skirmish.Players {
		blob.Skirmish.Players[i].Controller = 0
	}
	shell.applySettings(blob)

	if shell.retailControllers[0] != 1 {
		t.Fatalf("controllers = %v, want a human in row 0", shell.retailControllers)
	}
}

// TestSaveSettingsIsInertUntilAttached keeps the screenshot path and the tests
// from writing to the user's settings file.
func TestSaveSettingsIsInertUntilAttached(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	t.Setenv(settings.EnvPath, path)

	shell := &gameShell{maps: []string{"Anteer Straight"}}
	shell.setup = newSkirmishMenuConfig("Anteer Straight")
	shell.saveSettings()
	if _, err := settings.LoadFrom(path); err != nil {
		t.Fatalf("LoadFrom after inert save: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("saveSettings wrote %s without attachSettings", path)
	}

	shell.settingsWritable = true
	shell.saveSettings()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saveSettings did not write %s: %v", path, err)
	}
}
