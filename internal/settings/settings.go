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
// One value here is not a registry value: the per-player unit limit lives in
// the profile file's `[Preferences]` section instead [02 "Unit limit"]. It is
// persisted all the same, and the two stores collapse into the one JSON file
// below.
//
// Nanolathe keeps the value set, the defaults, and the read-once/write-whole
// shape, and swaps the registry for one JSON file. Only the preferences retail
// actually persists are stored here — audio mixing and the networking identity
// fields are retail values Nanolathe has no owner for yet and are deliberately
// absent rather than written as invented defaults.
//
// The display block (`DisplaymodeWidth`/`DisplaymodeHeight`) and the visual
// option values (`Anti-Alias`, `Shadows`, `FeatureShadows`, `VehicleShadows`,
// `Shading`, `Gamma`) are here because the options screen's `VISUALS` page is
// their only writer [07 R-FE-01 §6][07 R-FE-01 §11]; their missing-value
// defaults are the registry loader's [02 R-KEYS-01 §5].
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

// The message-column values. `textlines`/`textscroll` are the SPEEDS page's
// MAXLINES/TXTSCROL controls, `screenchat` is the `ScreenChat` command's
// stored value, and `unitchattext` is the caption priority gauge [02 §3]
// [07 R-FE-01 §11].
const (
	DefaultTextLines    = 10 // textlines: the message ring's line budget
	DefaultTextScroll   = 10 // textscroll: line age limit (textscroll+1)*30 ticks
	DefaultScreenChat   = 1  // screenchat: nonzero draws every message class
	DefaultUnitChatText = 5  // unitchattext: caption gate 10-v < priority
)

// The display block. `VISUALS`'s `VIDSLDR` and its `RESTORE`/`UNDO` buttons are
// the only writers; the skirmish and campaign load transitions are the only
// readers, comparing the pair to the presentation window's current size and
// resizing when they differ [07 R-FE-01 §6][07 R-FE-01 §11]. The missing-value
// defaults are 640 and 480 [02 R-KEYS-01 §5].
const (
	DefaultDisplaymodeWidth  = 640
	DefaultDisplaymodeHeight = 480
	// The front end itself always runs at 640x480 whatever the pair holds
	// [07 R-FE-02 §2]; modes below that are dropped from the slider's table
	// [07 R-FE-01 §6], so the pair can never name a smaller surface.
	MinDisplaymodeWidth  = 640
	MinDisplaymodeHeight = 480
)

// The visual option values. `ANTI`, `BSHADOWS` and `SHADING` are the three
// two-stage buttons of the `VISUALS` page, bound to bits 1, 4 and 5 of the
// display option word; `BSHADOWS` copies bit 4 into bit 3 and bit 3 into bit 2,
// so the one control drives `FeatureShadows`, `VehicleShadows` and `Shadows`
// together [07 R-FE-01 §6]. Every one of the five defaults to set and `Gamma`
// to 12 [02 R-KEYS-01 §5].
const (
	DefaultAntiAlias      = 1
	DefaultShadows        = 1
	DefaultFeatureShadows = 1
	DefaultVehicleShadows = 1
	DefaultShading        = 1
	DefaultGamma          = 12
	// `VISUALS` `GAMMA` is a kind-4 slider whose maximum is 20; the stored
	// integer is applied as the palette factor 0.5 + g/24 [07 R-FE-01 §6].
	MaxGamma = 20
)

// The configured per-player unit limit. Retail reads it once at start-up from
// the profile file's `[Preferences]` `UnitLimit` — established as a profile
// value and *not* a registry value, unlike everything else in this file — with
// a missing-value default of 250, then clamps it into 20..500 and keeps it as
// a sixteen-bit configured limit [02 "Unit limit"][08 R-SKIR-01 §6].
//
// It is stored at the top level rather than inside the Skirmish block for the
// same reason ScrollSpeed is: the Skirmish block mirrors the values retail
// keeps under its Skirmish key, and this is not one of them. Skirmish battle
// entry is nonetheless its only consumer here, because a campaign's limit is
// the map's `maxunits` instead [08 R-SKIR-01 §6].
const (
	DefaultUnitLimit = 250 // missing `UnitLimit` [08 R-SKIR-01 §6]
	MinUnitLimit     = 20  // below 20 becomes 20 [08 R-SKIR-01 §6]
	MaxUnitLimit     = 500 // above 500 becomes 500 [08 R-SKIR-01 §6]
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
	DamageBars int `json:"damagebars"`
	// UnitLimit is the configured per-player unit limit, the profile file's
	// `[Preferences] UnitLimit` [02 "Unit limit"][08 R-SKIR-01 §6]. It sizes
	// the unit pool at skirmish battle entry [05 R-SHARE-01 §7]. No screen
	// edits it — retail's skirmish lobby has no gadget for it — so it reaches
	// the session unchanged from whatever the file holds.
	UnitLimit int `json:"unitLimit"`
	// Display is the `DisplaymodeWidth`/`DisplaymodeHeight` pair and the six
	// visual option values the `VISUALS` page writes [07 R-FE-01 §6].
	Display Display `json:"display"`
	// Messages is the message-column ring configuration: `textlines`,
	// `textscroll`, `screenchat` and `unitchattext` [02 §3][07 R-FE-01 §11].
	Messages Messages `json:"messages"`
	Skirmish Skirmish `json:"skirmish"`
}

// Messages is the message-column ring configuration. All four values are
// legitimate to store as zero — a zero `textlines` disables ring storage
// outright [07 R-FE-01 §11] — so their defaults are installed by Defaults()
// rather than treated as absent by Normalize.
type Messages struct {
	// TextLines is `textlines`, the message ring's line budget
	// [07 R-HUD-03 §14.3].
	TextLines int `json:"textLines"`
	// TextScroll is `textscroll`; a line's age limit is
	// (textscroll+1)*30 simulation ticks [07 R-HUD-03 §14.3].
	TextScroll int `json:"textScroll"`
	// ScreenChat is `screenchat`: nonzero draws every message class, zero
	// retains only classes 1, 4 and 8 [07 R-HUD-03 §14.4].
	ScreenChat int `json:"screenChat"`
	// UnitChatText is `unitchattext`, the caption priority gauge: a status
	// caption is admitted when `10 - unitchattext < priority`
	// [07 R-HUD-03 §14.1].
	UnitChatText int `json:"unitChatText"`
}

// DefaultMessages is the block the startup reader installs when nothing is
// stored [02 §3].
func DefaultMessages() Messages {
	return Messages{
		TextLines:    DefaultTextLines,
		TextScroll:   DefaultTextScroll,
		ScreenChat:   DefaultScreenChat,
		UnitChatText: DefaultUnitChatText,
	}
}

// Normalize repairs a negative stored value, the one thing decoding cannot
// rule out on its own; zero is a legitimate choice for every field here and
// is left alone.
func (m *Messages) Normalize() {
	if m.TextLines < 0 {
		m.TextLines = DefaultTextLines
	}
	if m.TextScroll < 0 {
		m.TextScroll = DefaultTextScroll
	}
	if m.ScreenChat < 0 {
		m.ScreenChat = DefaultScreenChat
	}
	if m.UnitChatText < 0 {
		m.UnitChatText = DefaultUnitChatText
	}
}

// Display is the `VISUALS` page's persisted block. The two size values are
// retail's `DisplaymodeWidth`/`DisplaymodeHeight`; the five option values are
// the display-option-word bits the page's three two-stage buttons drive, kept
// as separate registry values because that is how retail stores them
// [07 R-FE-01 §6][02 R-KEYS-01 §5].
type Display struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	// AntiAlias is `Anti-Alias`, bit 1 of the display option word.
	AntiAlias int `json:"antiAlias"`
	// Shadows / FeatureShadows / VehicleShadows are bits 2, 4 and 3. One
	// control writes all three, but they stay separate values so a file
	// written by a build that later separates them is not silently collapsed.
	Shadows        int `json:"shadows"`
	FeatureShadows int `json:"featureShadows"`
	VehicleShadows int `json:"vehicleShadows"`
	// Shading is bit 5.
	Shading int `json:"shading"`
	// Gamma is the 0..20 slider integer, applied as the palette factor
	// 0.5 + g/24 [07 R-FE-01 §6].
	Gamma int `json:"gamma"`
}

// DefaultDisplay is the block the startup reader installs when nothing is
// stored [02 R-KEYS-01 §5].
func DefaultDisplay() Display {
	return Display{
		Width:          DefaultDisplaymodeWidth,
		Height:         DefaultDisplaymodeHeight,
		AntiAlias:      DefaultAntiAlias,
		Shadows:        DefaultShadows,
		FeatureShadows: DefaultFeatureShadows,
		VehicleShadows: DefaultVehicleShadows,
		Shading:        DefaultShading,
		Gamma:          DefaultGamma,
	}
}

// Normalize repairs a truncated or hand-edited display block. Zero is not a
// stored choice for either size — the slider's table drops everything below
// 640x480 [07 R-FE-01 §6] — so a zero or sub-minimum size takes the default.
// The five option values are booleans in a DWORD, so only a negative value is
// repaired; a stored 0 is "off" and is kept. Gamma is clamped into the
// slider's own 0..20 range.
func (d *Display) Normalize() {
	if d.Width < MinDisplaymodeWidth {
		d.Width = DefaultDisplaymodeWidth
	}
	if d.Height < MinDisplaymodeHeight {
		d.Height = DefaultDisplaymodeHeight
	}
	for _, value := range []*int{&d.AntiAlias, &d.Shadows, &d.FeatureShadows, &d.VehicleShadows, &d.Shading} {
		if *value < 0 {
			*value = 1
		}
	}
	if d.Gamma < 0 || d.Gamma > MaxGamma {
		d.Gamma = DefaultGamma
	}
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
	s := Settings{Version: FileVersion, Difficulty: DefaultDifficulty, ScrollSpeed: DefaultScrollSpeed, DamageBars: DefaultDamageBars, UnitLimit: DefaultUnitLimit}
	s.Display = DefaultDisplay()
	s.Messages = DefaultMessages()
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
	// The unit limit's clamp is retail's own start-up clamp, not a schema
	// repair: an absent value installs 250, and a stored value outside
	// 20..500 is pulled to the nearer bound [08 R-SKIR-01 §6]. Zero cannot be
	// a stored choice because the legal range starts at 20.
	if s.UnitLimit == 0 {
		s.UnitLimit = DefaultUnitLimit
	}
	if s.UnitLimit < MinUnitLimit {
		s.UnitLimit = MinUnitLimit
	}
	if s.UnitLimit > MaxUnitLimit {
		s.UnitLimit = MaxUnitLimit
	}
	s.Display.Normalize()
	s.Messages.Normalize()
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
