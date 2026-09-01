// Package settings persists the frontend preferences that survive a restart:
// the last skirmish setup, the last map, per-slot side/colour/ally/resources,
// and the campaign difficulty.
//
// Retail keeps exactly this set in the registry under
// HKEY_CURRENT_USER\Software\Cavedog Entertainment\Total Annihilation, with the
// per-slot Player%dController/Side/Color/AllyGroup/Metal/Energy values in the
// nested "Total Annihilation\Skirmish" key. The startup reader loads the whole
// block once and installs a default for every value it does not find; the
// writer rewrites the whole block [02 "Settings"][07 §10].
//
// Nanolathe keeps the value set, the defaults, and the read-once/write-whole
// shape, and swaps the registry for one JSON file. Only the preferences retail
// actually persists are stored here — display mode, audio mixing, and the
// networking identity fields are retail values Nanolathe has no owner for yet
// and are deliberately absent rather than written as invented defaults.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// FileVersion is the schema tag. A file whose Version is unrecognised is
// discarded in favour of the defaults rather than half-read.
const FileVersion = 1

// EnvPath overrides the resolved settings path in full. It exists so a test or
// a throwaway run can point at its own file instead of the user's.
const EnvPath = "NANOLATHE_SETTINGS"

// MaxPlayers matches session.SkirmishMaxPlayers. The package deliberately does
// not import internal/session: settings is a leaf that the frontend converts
// to and from, so the save file cannot drag simulation types into its schema.
const MaxPlayers = 10

// Retail's missing-value defaults [02 "Settings"][07 §10].
const (
	DefaultDifficulty     = 1    // Medium
	DefaultNumPlayers     = 4    // NumSkirmishPlayers
	DefaultCommanderDeath = 1    // commander death ends the game
	DefaultMapping        = 1    // terrain blacked out until explored
	DefaultLineOfSight    = 1    // LOS enabled
	DefaultLOSType        = 1    // elevations affect LOS
	DefaultLocation       = 1    // fixed start positions
	DefaultAllyGroup      = 5    // Player%dAllyGroup
	DefaultMetal          = 1000 // Player%dMetal
	DefaultEnergy         = 1000 // Player%dEnergy
	DefaultScrollSpeed    = 32   // scrollspeed [02 "Settings"] default 32 [07 §10]
	DefaultDamageBars     = 0    // damagebars absent: the bit is cleared [03 R-FX-01 §6]
)

// InterfaceFlagDamageBars is bit 0 of the interface-flags word — the "label
// every unit" bit. The stored `damagebars` value and that bit are the same
// thing: at settings load the bit takes the value's low bit, and the battle
// key command flips the bit and rewrites the whole block immediately
// [07 R-HUD-03 §7][03 R-FX-01 §6].
const InterfaceFlagDamageBars = 1

// Player is one skirmish slot, holding the six Player%d* registry values.
// Controller is retail's raw row value — 0 open, 1 human, 2 computer — not the
// session package's compatibility encoding; the frontend converts at the edge.
type Player struct {
	Controller int `json:"controller"`
	Side       int `json:"side"`
	Color      int `json:"color"`
	AllyGroup  int `json:"allyGroup"`
	Metal      int `json:"metal"`
	Energy     int `json:"energy"`
}

// Skirmish is the SKIRMISH.GUI setup: everything retail stores under the
// Skirmish* value names plus the per-slot rows.
type Skirmish struct {
	Map            string   `json:"map"` // SkirmishMap, basename without extension
	NumPlayers     int      `json:"numPlayers"`
	Difficulty     int      `json:"difficulty"`
	Location       int      `json:"location"`
	CommanderDeath int      `json:"commanderDeath"`
	Mapping        int      `json:"mapping"`
	LineOfSight    int      `json:"lineOfSight"`
	LOSType        int      `json:"losType"`
	Players        []Player `json:"players"`
}

// Settings is the whole persisted block.
type Settings struct {
	Version int `json:"version"`
	// Difficulty is retail's top-level "Difficulty" value, the campaign and
	// mission setting. It is separate from Skirmish.Difficulty, which retail
	// keeps as its own "SkirmishDifficulty" value.
	Difficulty  int `json:"difficulty"`
	ScrollSpeed int `json:"scrollSpeed"` // scrollspeed [02 "Settings"] [07 §10] C2
	// DamageBars is the stored `damagebars` value. Only its low bit is read:
	// it becomes bit 0 of the interface-flags word [03 R-FX-01 §6].
	DamageBars int      `json:"damagebars"`
	Skirmish   Skirmish `json:"skirmish"`
}

// DamageBarsEnabled is the loader's rule for the value: present → bit 0 of the
// interface-flags word takes the value's low bit; absent → the bit is cleared
// and the default is written back with the rest of the block
// [03 R-FX-01 §6][07 R-HUD-03 §7].
func (s Settings) DamageBarsEnabled() bool { return s.DamageBars&InterfaceFlagDamageBars != 0 }

// SetDamageBarsEnabled writes the bit back into the stored value.
func (s *Settings) SetDamageBarsEnabled(on bool) {
	if on {
		s.DamageBars |= InterfaceFlagDamageBars
		return
	}
	s.DamageBars &^= InterfaceFlagDamageBars
}

// StoreDamageBars persists one damagebars state. Retail's key command flips
// the bit and immediately writes every setting back, so this reads the whole
// block, replaces the bit and rewrites the whole block [07 R-HUD-03 §7].
// A block that could not be read is reported and the write still proceeds from
// the defaults, because losing the rest of the preferences must not silently
// swallow the toggle the player just pressed.
func StoreDamageBars(on bool) error {
	s, loadErr := Load()
	s.SetDamageBarsEnabled(on)
	if err := s.Save(); err != nil {
		return err
	}
	return loadErr
}

// Defaults returns the block the startup reader installs when nothing is
// stored [02 "Settings"][07 §10].
//
// The six skirmish rule scalars are set here rather than in Normalize because
// zero is a legitimate stored choice for every one of them — Easy, randomized
// start positions, commander death continues, all terrain visible, LOS off,
// elevation ignored — so a loader may not treat a zero as an absent value.
func Defaults() Settings {
	s := Settings{Version: FileVersion, Difficulty: DefaultDifficulty, ScrollSpeed: DefaultScrollSpeed, DamageBars: DefaultDamageBars}
	s.Skirmish = Skirmish{
		NumPlayers:     DefaultNumPlayers,
		Difficulty:     DefaultDifficulty,
		Location:       DefaultLocation,
		CommanderDeath: DefaultCommanderDeath,
		Mapping:        DefaultMapping,
		LineOfSight:    DefaultLineOfSight,
		LOSType:        DefaultLOSType,
	}
	s.Skirmish.Normalize()
	return s
}

// DefaultPlayer is the row installed for slot i when none of its Player%d*
// values are stored: open row, colour the slot index, side slot&1, ally group
// 5, 1000 metal and 1000 energy [02 "Settings"].
func DefaultPlayer(i int) Player {
	return Player{
		Controller: 0,
		Side:       i & 1,
		Color:      i,
		AllyGroup:  DefaultAllyGroup,
		Metal:      DefaultMetal,
		Energy:     DefaultEnergy,
	}
}

// Normalize fills absent values with retail's defaults and clamps the slot
// count, so a hand-edited or truncated file still yields a usable setup. It
// mirrors the reader's per-value "if the read failed, install the default"
// arms rather than rejecting the file [02 "Settings"].
//
// A slot that is present in the file is taken verbatim, including zeros: the
// writer always emits all ten rows, so a zero is a stored choice — an ally
// group of 0 or a colour of 0 on slot three — and not an absent value. Only
// rows past the end of the array are defaulted.
func (s *Skirmish) Normalize() {
	if s.NumPlayers == 0 {
		s.NumPlayers = DefaultNumPlayers
	}
	if s.NumPlayers < 0 {
		s.NumPlayers = 0
	}
	if s.NumPlayers > MaxPlayers {
		s.NumPlayers = MaxPlayers
	}
	if len(s.Players) > MaxPlayers {
		s.Players = s.Players[:MaxPlayers]
	}
	for i := len(s.Players); i < MaxPlayers; i++ {
		s.Players = append(s.Players, DefaultPlayer(i))
	}
	for i := range s.Players {
		if s.Players[i].Controller < 0 || s.Players[i].Controller > 2 {
			s.Players[i].Controller = 0
		}
	}
}

// Normalize applies Skirmish.Normalize and the top-level defaults.
func (s *Settings) Normalize() {
	if s.Version == 0 {
		s.Version = FileVersion
	}
	if s.Difficulty < 0 || s.Difficulty > 2 {
		s.Difficulty = DefaultDifficulty
	}
	if s.ScrollSpeed < 0 || s.ScrollSpeed > 255 {
		s.ScrollSpeed = DefaultScrollSpeed
	}
	if s.ScrollSpeed == 0 {
		s.ScrollSpeed = DefaultScrollSpeed
	}
	s.Skirmish.Normalize()
}

// Path is the settings file location: $NANOLATHE_SETTINGS when set, otherwise
// $XDG_CONFIG_HOME/nanolathe/settings.json, otherwise ~/.config/nanolathe/settings.json.
// The XDG layout is used on every platform so the file lives in one documented
// place regardless of host conventions.
func Path() (string, error) {
	if p := os.Getenv(EnvPath); p != "" {
		return p, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("settings: locate home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "nanolathe", "settings.json"), nil
}

// Load reads Path(). A missing file is not an error: it yields the defaults.
// A file that cannot be parsed also yields the defaults, together with the
// error, so the caller can report it and still start.
func Load() (Settings, error) {
	path, err := Path()
	if err != nil {
		return Defaults(), err
	}
	return LoadFrom(path)
}

// LoadFrom reads one settings file.
func LoadFrom(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Defaults(), nil
		}
		return Defaults(), fmt.Errorf("settings: read %s: %w", path, err)
	}
	// The version is read on its own, because the decode below starts from the
	// defaults and would otherwise supply a valid version to a file that
	// carries none.
	var wire struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return Defaults(), fmt.Errorf("settings: parse %s: %w", path, err)
	}
	if wire.Version == nil || *wire.Version != FileVersion {
		stored := 0
		if wire.Version != nil {
			stored = *wire.Version
		}
		return Defaults(), fmt.Errorf("settings: %s: unsupported version %d", path, stored)
	}

	// Decode over the defaults rather than over a zero value. Several stored
	// values are legitimately zero — LOS off, mapping off, commander death
	// off, ally group 0 — so Normalize cannot tell an absent field from a
	// stored zero and must leave both alone. Starting from the defaults makes
	// the distinction for it: a field the file omits keeps its retail default,
	// and a field the file stores as 0 overwrites that default with 0.
	//
	// Without this, a truncated or hand-edited file silently turned five
	// skirmish rules whose default is 1 — commander death, mapping, line of
	// sight, LOS type, start locations — into 0
	// [02 "Settings"][07 §10].
	s := Defaults()
	if err := json.Unmarshal(data, &s); err != nil {
		return Defaults(), fmt.Errorf("settings: parse %s: %w", path, err)
	}
	s.Normalize()
	return s, nil
}

// Save writes the block to Path().
func (s Settings) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	return s.SaveTo(path)
}

// SaveTo writes the whole block, the way the retail writer rewrites every value
// rather than tracking which one changed. The write goes to a sibling temp
// file and is renamed over the target, so an interrupted save cannot leave a
// half-written file where the next start expects settings.
func (s Settings) SaveTo(path string) error {
	s.Normalize()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: encode: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("settings: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("settings: create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("settings: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("settings: close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("settings: chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("settings: rename onto %s: %w", path, err)
	}
	return nil
}
