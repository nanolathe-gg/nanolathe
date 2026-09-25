package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// FileVersion is the schema tag. A file whose Version is unrecognised is
// ignored in memory in favour of defaults. Its original bytes are preserved.
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

// The display-mode pair. `VISUALS`'s `VIDSLDR` and its `RESTORE`/`UNDO`
// buttons are the retail writers; the skirmish and campaign load transitions
// read the pair, comparing it to the presentation window's
// current size and resizing when they differ [07 R-FE-01 §6]
// [07 R-FE-01 §11]. The missing-value defaults are 640 and 480
// [02 R-KEYS-01 §5]. Nanolathe also uses this pair for the stable host window
// size (DESIGN_PRESENTATION_CLIENT §2.1).
const (
	DefaultDisplaymodeWidth  = 640
	DefaultDisplaymodeHeight = 480
	// The logical front-end canvas always runs at 640x480 whatever the pair holds
	// [07 R-FE-02 §2]; modes below that are dropped from the slider's table
	// [07 R-FE-01 §6], so the pair can never name a smaller surface.
	MinDisplaymodeWidth  = 640
	MinDisplaymodeHeight = 480
)

// The visual option values. `ANTI`, `BSHADOWS` and `SHADING` are the three
// two-stage buttons of the `VISUALS` page, bound to bits 1, 4 and 5 of the
// display option word; `BSHADOWS` copies bit 4 into bit 3 and bit 3 into bit 2,
// so the one control drives `FeatureShadows`, `VehicleShadows` and `Shadows`
// together [07 R-FE-01 §6]. Every one of those five defaults to set,
// `DitheredFog` defaults clear and `Gamma` defaults to 12
// [02 R-KEYS-01 §5].
const (
	DefaultAntiAlias      = 1
	DefaultShadows        = 1
	DefaultFeatureShadows = 1
	DefaultVehicleShadows = 1
	DefaultShading        = 1
	DefaultDitheredFog    = 0
	// DefaultGlow is the Enhanced glow layer switch, a Nanolathe option with no
	// retail bit: on until the player turns it off (DESIGN_GPU_RENDERER §19).
	DefaultGlow = 1
	// DefaultGlowStrength and MaxGlowStrength bound the glow layer's strength,
	// a percentage of the tuned halo: 100 is the default look, 0 draws no glow
	// and 200 is the most the preference stores (DESIGN_GPU_RENDERER §19.4).
	DefaultGlowStrength = 100
	MaxGlowStrength     = 200
	// DefaultEffectSwitch is the shared default of the five Enhanced effect
	// switches in the presentation block (DESIGN_GPU_RENDERER §30). Like Glow
	// they have no retail bit and start on.
	DefaultEffectSwitch = 1
	// TrailStrength scales the existing footprint and track darkening only.
	DefaultTrailStrength = 50
	MaxTrailStrength     = 100

	DefaultGamma = 12
	// `VISUALS` `GAMMA` is a kind-4 slider whose maximum is 20; the stored
	// integer is applied as the palette factor 0.5 + g/24 [07 R-FE-01 §6].
	MaxGamma = 20
)

// The interface options page's two non-slider values. `gamespeed` is the
// `GAME` slider's store; `Interface Type` is the `LEFTCLICK` two-stage button's
// [07 R-CAM-01 §7][07 R-CAM-01 §5].
const (
	DefaultGameSpeed = 10 // gamespeed
	// The speed setter clamps the requested speed into 1..20 on signed
	// compares, so 21 — the `GAME` slider's own maximum — stores as 20
	// [07 R-CAM-01 §3].
	MinGameSpeed = 1
	MaxGameSpeed = 20
	// GameSliderMax is the runtime maximum the page opener writes into the
	// `GAME` slider record; the read-out's own floor is 1 [07 R-CAM-01 §7].
	GameSliderMax = 21
	// ScrollSliderMax, TextScrollSliderMax and MaxLinesSliderMax are the other
	// three interface sliders' runtime maxima [07 R-CAM-01 §7].
	ScrollSliderMax     = 65
	TextScrollSliderMax = 20
	MaxLinesSliderMax   = 30
	// MinScrollSpeed is the `SCREEN` read-out's own floor: a value at or below
	// one stores one [07 R-CAM-01 §7].
	MinScrollSpeed = 1

	// DefaultInterfaceType is the absent-value default of `Interface Type`
	// [07 R-CAM-01 §5].
	DefaultInterfaceType = 0
	// DefaultClock is the absent-value default of `clock`. The loader keeps
	// only the stored DWORD's low bit [02 "Settings"][07 R-CAM-01 §6].
	DefaultClock = 0
	// DefaultSwitchAlt is the absent-value default of `SwitchAlt`. The reader
	// keeps only bit 0 [07 R-CAM-01 §4].
	DefaultSwitchAlt = 0
	// InterfaceTypeLeftClick and InterfaceTypeRightClick are the two stages of
	// the `LEFTCLICK` button. They mirror the constants of the same name in
	// `internal/orders`, which is the word's consumer [07 R-CAM-01 §5].
	InterfaceTypeLeftClick  = 0
	InterfaceTypeRightClick = 1
)

// The configured per-player unit limit. Retail reads it once at start-up from
// the profile file's `[Preferences]` `UnitLimit` — established as a profile
// value and *not* a registry value, unlike everything else in this file — with
// a missing-value default of 250, then clamps it into 20..500 and keeps it as
// a sixteen-bit configured limit [02 "Unit limit"][08 R-SKIR-01 §6].
//
// Nanolathe raises the default and upper bound by user request; see
// DESIGN_CONTENT_VFS §5. The retail evidence above remains unchanged.
//
// It is stored at the top level rather than inside the Skirmish block for the
// same reason ScrollSpeed is: the Skirmish block mirrors the values retail
// keeps under its Skirmish key, and this is not one of them. Skirmish battle
// entry is nonetheless its only consumer here, because a campaign's limit is
// the map's `maxunits` instead [08 R-SKIR-01 §6].
const (
	DefaultUnitLimit = 1000 // Nanolathe policy; DESIGN_CONTENT_VFS §5.
	MinUnitLimit     = 20   // below 20 becomes 20 [08 R-SKIR-01 §6]
	MaxUnitLimit     = 3276 // ten player slices must fit positive signed 16-bit occupancy IDs.
)

// The audio option values the `SOUND` and `MUSIC` options pages write
// [03 R-AUD-01 §2][03 R-AUD-01 §4]. Retail packs the first five into one
// sound-flags byte and keeps the rest as separate registry values; they stay
// separate fields here for the same reason the display option bits do.
const (
	// SoundModeOff, SoundModeMono and SoundMode3D are the three stages of the
	// `MODE` button, stored in bits 0..2. Every play gate requires a nonzero
	// mode; the value 2 sets the device's 3-D flag and any other value clears
	// it [03 R-AUD-01 §2][03 R-AUD-01 §1].
	SoundModeOff  = 0
	SoundModeMono = 1
	SoundMode3D   = 2
	// MaxSoundMode is the width of the stored field, not the button's stage
	// count: the three-stage `MODE` gadget is its only writer, but the byte
	// holds three bits [03 R-AUD-01 §2].
	MaxSoundMode = 7

	DefaultSoundMode     = 1  // `Sound Mode`
	DefaultRestoreVolume = 0  // `RestoreVolume`, bit 3
	DefaultAckFX         = 1  // `ackfx`, bit 4
	DefaultBuildFX       = 1  // `buildfx`, bit 5
	DefaultSpeechFX      = 1  // `speechfx`, bit 6
	DefaultFXVol         = 27 // `fxvol`
	DefaultMusicVol      = 32 // `musicvol`
	DefaultMixingBuffers = 8  // `MixingBuffers`
	DefaultMusicMode     = 1  // `musicmode` bit 0
	DefaultCDMode        = 4  // `cdmode`, 1..4
	DefaultUnitChat      = 10 // `unitchat`, the voice-level gauge

	// MaxFXVol and MaxMusicVol are the runtime maxima the two page openers
	// write into the `FXVOL` and `MUSICVOL` slider records. Both are 64, so a
	// knob at the end of travel reads 64 and the device level `v << 10`
	// saturates the 16-bit mixer word [03 R-AUD-01 §2].
	MaxFXVol    = 64
	MaxMusicVol = 64
	// MaxUnitChat is the ceiling of both acknowledgement gauges: the stage
	// times five, over three stages [03 R-AUD-01 §2][07 R-CAM-01 §7].
	MaxUnitChat = 10
	// MinCDMode and MaxCDMode bound `cdmode`: 1 Play All, 2 Random, 3 Repeat,
	// 4 Custom [03 R-AUD-01 §4].
	MinCDMode = 1
	MaxCDMode = 4
)

// Audio is the `SOUND` and `MUSIC` options pages' persisted block
// [03 R-AUD-01 §2][03 R-AUD-01 §4].
//
// `AckFX` and `BuildFX` are stored and displayed but gate no sound in retail —
// a bounded negative over the whole decompiled corpus — so they ride through
// here inert, exactly as they do there.
type Audio struct {
	// SoundMode is bits 0..2 of the sound-flags byte, written by `MODE`.
	SoundMode int `json:"soundMode"`
	// RestoreVolume is bit 3. No traced screen has a gadget for it; it governs
	// whether the player's system mixer levels are persisted across sessions.
	RestoreVolume int `json:"restoreVolume"`
	// AckFX and BuildFX are bits 4 and 5, written only by `RESTORE`/`UNDO`.
	AckFX   int `json:"ackFX"`
	BuildFX int `json:"buildFX"`
	// SpeechFX is bit 6, written by `SPEECH` as `stage != 0`. It is the voice
	// cue resolver's audible gate; captions are unaffected.
	SpeechFX int `json:"speechFX"`
	// FXVol is `fxvol`, the `FXVOL` gauge, 0..64.
	FXVol int `json:"fxVol"`
	// MusicVol is `musicvol`, the `MUSICVOL` gauge, 0..64.
	MusicVol int `json:"musicVol"`
	// MixingBuffers is the device voice limit. No options gadget writes it.
	MixingBuffers int `json:"mixingBuffers"`
	// MusicMode is `musicmode` bit 0, the `NOTRAK` toggle.
	MusicMode int `json:"musicMode"`
	// CDMode is `cdmode`, the `TRACKMODE` button's stage plus one.
	CDMode int `json:"cdMode"`
	// UnitChat is `unitchat`, the acknowledgement **voice** level the `SPEECH`
	// gauge writes as stage times five. Its text twin is
	// Messages.UnitChatText, which the interface page writes [07 R-CAM-01 §7].
	UnitChat int `json:"unitChat"`
}

// DefaultAudio is the block the startup reader installs when nothing is stored
// [03 R-AUD-01 §2].
func DefaultAudio() Audio {
	return Audio{
		SoundMode:     DefaultSoundMode,
		RestoreVolume: DefaultRestoreVolume,
		AckFX:         DefaultAckFX,
		BuildFX:       DefaultBuildFX,
		SpeechFX:      DefaultSpeechFX,
		FXVol:         DefaultFXVol,
		MusicVol:      DefaultMusicVol,
		MixingBuffers: DefaultMixingBuffers,
		MusicMode:     DefaultMusicMode,
		CDMode:        DefaultCDMode,
		UnitChat:      DefaultUnitChat,
	}
}

// Normalize repairs a hand-edited or truncated audio block. Zero is a stored
// choice for every bit and for both gauges — sound off, silence — so only a
// value outside the writer's own range is repaired.
func (a *Audio) Normalize() {
	if a.SoundMode < 0 || a.SoundMode > MaxSoundMode {
		a.SoundMode = DefaultSoundMode
	}
	for _, bit := range []*int{&a.RestoreVolume, &a.AckFX, &a.BuildFX, &a.SpeechFX, &a.MusicMode} {
		if *bit < 0 || *bit > 1 {
			*bit = 1
		}
	}
	if a.FXVol < 0 || a.FXVol > MaxFXVol {
		a.FXVol = DefaultFXVol
	}
	if a.MusicVol < 0 || a.MusicVol > MaxMusicVol {
		a.MusicVol = DefaultMusicVol
	}
	if a.MixingBuffers <= 0 {
		a.MixingBuffers = DefaultMixingBuffers
	}
	if a.CDMode < MinCDMode || a.CDMode > MaxCDMode {
		a.CDMode = DefaultCDMode
	}
	if a.UnitChat < 0 || a.UnitChat > MaxUnitChat {
		a.UnitChat = DefaultUnitChat
	}
}

// SoundEnabled is every play gate's first test: a nonzero sound mode
// [03 R-AUD-01 §2].
func (a Audio) SoundEnabled() bool { return a.SoundMode != SoundModeOff }

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

// ModSelection is the settings key `mod`: one mod's id and, optionally, its
// version; an empty version selects the newest installed one
// (docs/DESIGN_MODS_MUTATORS.md §4.3).
type ModSelection struct {
	ID      string `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
}

// Settings is the whole persisted block.
type Settings struct {
	// Gameplay is the selected rule set: `modern`, `strict-3.1`, or the name
	// of a set this build registered. Normalize keeps a name this build can
	// select and falls back to Modern otherwise, so a file written by a build
	// that linked a set the running one does not — or a hand-edited typo —
	// starts under the default instead of failing to load
	// (docs/DESIGN_GAMEPLAY_RULES.md §8).
	Gameplay         gameplay.Mode       `json:"gameplay"`
	GameplayFeatures community.Overrides `json:"gameplayFeatures,omitempty"`
	BuilderOptions   BuilderOptions      `json:"builderOptions"`
	// ContentProfile is the selected content profile: a shipped profile's
	// name, the path of a user-authored profile JSON file, or empty to detect
	// the profile from the mounted content set's own markers. It is a
	// load-time content fact, not a gameplay rule set
	// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
	ContentProfile string `json:"contentProfile,omitempty"`
	// Mutators is the selected mutator set, each key mapped to its canonical
	// factor spelling, e.g. {"buildSpeed": "2"} (docs/DESIGN_MODS_MUTATORS.md
	// §6.6). It is kept verbatim: this package does not import content, so a
	// reader parses it with content.ParseMutators and reports an invalid entry
	// there. A command-line --mutator wins over it.
	Mutators map[string]string `json:"mutators,omitempty"`
	// Mod is the saved mod choice (docs/DESIGN_MODS_MUTATORS.md §4.3). It is
	// only stored and round-tripped here; the mod library resolves it. The
	// zero value selects no mod and is omitted from the file.
	Mod     ModSelection `json:"mod,omitzero"`
	Version int          `json:"version"`
	// Fullscreen is Nanolathe's desktop presentation preference, independent of
	// retail display options. Absent in older settings files means windowed.
	Fullscreen bool `json:"fullscreen"`
	// Presentation selects Nanolathe's executor and presentation cap, separate
	// from the retail display options (DESIGN_GPU_RENDERER §13.5, §14.6).
	Presentation Presentation `json:"presentation"`
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
	// Audio is the `SOUND` and `MUSIC` pages' block [03 R-AUD-01 §2].
	Audio Audio `json:"audio"`
	// GameSpeed is `gamespeed`, the interface page's `GAME` slider
	// [07 R-CAM-01 §7].
	GameSpeed int `json:"gameSpeed"`
	// InterfaceType is the `Interface Type` word the interface page's
	// `LEFTCLICK` button writes [07 R-CAM-01 §5].
	InterfaceType int `json:"interfaceType"`
	// SwitchAlt selects the digit-key mux. Only bit 0 is retained: clear maps
	// plain digits to build pages, set maps Alt+digits to build pages
	// [07 R-CAM-01 §4].
	SwitchAlt int `json:"switchAlt"`
	// Clock is the persisted stand-alone battle-clock switch. Only bit 0 is
	// retained and the `Clock` chat command rewrites it immediately
	// [02 "Settings"][07 R-CAM-01 §6].
	Clock int `json:"clock"`
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

// Presentation holds Nanolathe's host presentation preferences
// (DESIGN_GPU_RENDERER §13.5, §14.6). FPS zero follows the display refresh;
// positive values cap modern presentation without changing the simulation.
type Presentation struct {
	// Negative snap radii select the content table's default; zero disables.
	MexSnapRadius        int        `json:"mexSnapRadius"`
	WreckSnapRadius      int        `json:"wreckSnapRadius"`
	BuildRotateKey       string     `json:"buildRotateKey"`
	ClickSnapOverrideKey string     `json:"clickSnapOverrideKey"`
	BuildRotationOverlay int        `json:"buildRotationOverlay"`
	NanoframePreview     int        `json:"nanoframePreview"`
	QueuedOrderDrag      int        `json:"queuedOrderDrag"`
	StrategicIconConfig  string     `json:"strategicIconConfig"`
	TeamColorNanolathe   int        `json:"teamColorNanolathe"`
	PlayerStreamColors   [10]string `json:"playerStreamColors"`
	PlayerFrameColors    [10]string `json:"playerFrameColors"`

	CommunityCounters int `json:"communityCounters"`
	ReloadBars        int `json:"reloadBars"`
	VeteranLabels     int `json:"veteranLabels"`
	GroupNumbers      int `json:"groupNumbers"`
	AlliedResources   int `json:"alliedResources"`
	WeatherReport     int `json:"weatherReport"`

	Renderer string `json:"renderer"`
	FPS      int    `json:"fps"`
	// ExpandedSidebar uses spare modern UI height for more authored controls.
	// This is a Nanolathe presentation preference (interface design §3.3).
	ExpandedSidebar int `json:"expandedSidebar"`
	// CommunitySelection enables the community patch's Ctrl+B/F idle-unit
	// cycles and Ctrl+S on-screen mobile-weapon selection. It is host input
	// policy, independent of gameplay and renderer selection.
	CommunitySelection int `json:"communitySelection"`
	// DoubleClickSelection enables the community patch's on-screen same-type
	// selection for a native left- or right-double-click record.
	DoubleClickSelection int `json:"doubleClickSelection"`
	// The five Enhanced effect switches (DESIGN_GPU_RENDERER §30). They are
	// Nanolathe options with no retail bit, read only by the modern recorder
	// and executor, and stored as integers for the same reason the display
	// bits are: a stored 0 is "off" and is kept, so only a negative value is
	// repaired. A file that omits a key keeps the default, because the loader
	// decodes over the defaults [02 "Settings"].
	//
	// Water is the coastal water surface, wakes and hover dust, building foam,
	// water motion and the screen-space reflections.
	Water int `json:"water"`
	// Lighting is the battle lighting of models and smoke.
	Lighting int `json:"lighting"`
	// Finish is the metallic glint and the metal/paint material finishes.
	Finish int `json:"finish"`
	// Distortion is the blast rings, the burning-vegetation heat shimmer and
	// the fresh-wreck shimmer with its cooling emission colour.
	Distortion int `json:"distortion"`
	// Marks is the scorch marks and the trail layer of footprints and tracks.
	Marks int `json:"marks"`
	// TrailStrength is a percentage of the trail layer's tuned peak opacity.
	// Zero hides trails while leaving the Marks switch's scorch layer intact.
	TrailStrength int `json:"trailStrength"`
}

// DefaultPresentation selects the modern executor with a 60 FPS cap and every
// Enhanced effect on.
func DefaultPresentation() Presentation {
	return Presentation{
		Renderer: "modern", FPS: 60, ExpandedSidebar: 1, GroupNumbers: 1,
		MexSnapRadius: -1, WreckSnapRadius: -1, BuildRotateKey: "/", ClickSnapOverrideKey: "alt", BuildRotationOverlay: 1,
		Water: DefaultEffectSwitch, Lighting: DefaultEffectSwitch, Finish: DefaultEffectSwitch,
		Distortion: DefaultEffectSwitch, Marks: DefaultEffectSwitch,
		TrailStrength: DefaultTrailStrength,
	}
}

// Normalize repairs unsupported values while preserving the CLI's display
// refresh choice (zero) and arbitrary positive presentation caps. The effect
// switches are booleans, so only a negative value is repaired.
func (p *Presentation) Normalize() {
	if p.NanoframePreview < 0 || p.NanoframePreview > 2 {
		p.NanoframePreview = 0
	}
	if p.MexSnapRadius < 0 {
		p.MexSnapRadius = -1
	}
	if p.WreckSnapRadius < 0 {
		p.WreckSnapRadius = -1
	}
	if p.BuildRotateKey == "" {
		p.BuildRotateKey = "/"
	}
	if p.ClickSnapOverrideKey == "" {
		p.ClickSnapOverrideKey = "alt"
	}

	if p.Renderer != "classic" && p.Renderer != "modern" {
		p.Renderer = DefaultPresentation().Renderer
	}
	if p.FPS < 0 {
		p.FPS = DefaultPresentation().FPS
	}
	if p.ExpandedSidebar < 0 {
		p.ExpandedSidebar = DefaultPresentation().ExpandedSidebar
	}
	for _, value := range []*int{&p.CommunitySelection, &p.DoubleClickSelection, &p.CommunityCounters, &p.ReloadBars, &p.VeteranLabels, &p.GroupNumbers, &p.AlliedResources, &p.WeatherReport, &p.BuildRotationOverlay, &p.QueuedOrderDrag, &p.TeamColorNanolathe} {
		if *value < 0 {
			*value = 0
		} else {
			*value &= 1
		}
	}
	for _, value := range []*int{&p.Water, &p.Lighting, &p.Finish, &p.Distortion, &p.Marks} {
		if *value < 0 {
			*value = DefaultEffectSwitch
		}
	}
	if p.TrailStrength < 0 {
		p.TrailStrength = DefaultTrailStrength
	} else if p.TrailStrength > MaxTrailStrength {
		p.TrailStrength = MaxTrailStrength
	}
}

// Display is the `VISUALS` page's persisted block. The two size values are
// retail's `DisplaymodeWidth`/`DisplaymodeHeight`; the six option values are
// display-option-word bits, kept as separate registry values because that is
// how retail stores them
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
	// DitheredFog is bit 6. Its only options-page writer is front-end
	// RESTORE; the Dither command toggles it in battle.
	DitheredFog int `json:"ditheredFog"`
	// Glow is the Enhanced glow layer (bloom) of docs/DESIGN_GPU_RENDERER.md
	// §19: a Nanolathe option the modern executor alone reads. It has no
	// retail bit and is persisted only here; a file that omits it keeps the
	// default, because the loader decodes over the defaults.
	Glow int `json:"glow"`
	// GlowStrength is the glow layer's strength as a percentage of the tuned
	// halo, 0..MaxGlowStrength, applied while Glow is on. It is a separate key
	// rather than a meaning of `glow` because `glow` is a stored on/off value
	// its options button and chat command write as 0 or 1.
	GlowStrength int `json:"glowStrength"`
	// Gamma retains the signed command integer; the slider writes 0..20.
	// Load alone maps 10 to 12 [07 R-FE-01 §11][07 R-CAM-01 §6].
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
		DitheredFog:    DefaultDitheredFog,
		Glow:           DefaultGlow,
		GlowStrength:   DefaultGlowStrength,
		Gamma:          DefaultGamma,
	}
}

// Normalize repairs a truncated or hand-edited display block. Zero is not a
// stored choice for either size — the slider's table drops everything below
// 640x480 [07 R-FE-01 §6] — so a zero or sub-minimum size takes the default.
// The five button values are booleans in a DWORD, so only a negative value is
// repaired; a stored 0 is "off" and is kept. The DitheredFog loader takes its
// DWORD's low bit. Gamma retains command values outside the slider range.
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
	d.DitheredFog &= 1
	if d.Glow < 0 {
		d.Glow = DefaultGlow
	}
	if d.GlowStrength < 0 {
		d.GlowStrength = DefaultGlowStrength
	}
	d.GlowStrength = min(d.GlowStrength, MaxGlowStrength)
	d.Gamma = int(int32(d.Gamma)) // retain the signed DWORD [07 R-FE-01 §11]
}

// DitheredFogEnabled reports the persisted display word's bit-6 source.
// Retail reads only the stored DWORD's low bit [07 R-FE-01 §11].
func (d Display) DitheredFogEnabled() bool { return d.DitheredFog&1 != 0 }

// DamageBarsEnabled is the loader's rule for the value: present → bit 0 of the
// interface-flags word takes the value's low bit; absent → the bit is cleared
// and the default is written back with the rest of the block
// [03 R-FX-01 §6][07 R-HUD-03 §7].
func (s Settings) DamageBarsEnabled() bool { return s.DamageBars&InterfaceFlagDamageBars != 0 }

// SwitchAltEnabled reports the persisted digit-key mux bit.
func (s Settings) SwitchAltEnabled() bool { return s.SwitchAlt&1 != 0 }

// ClockEnabled reports the persisted stand-alone battle-clock bit.
func (s Settings) ClockEnabled() bool { return s.Clock&1 != 0 }

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
// An unreadable block is preserved and the error reported. This host recovery
// policy protects unrelated preferences while the in-memory toggle remains live.
func StoreDamageBars(on bool) error {
	s, loadErr := Load()
	if loadErr != nil {
		return loadErr
	}
	s.SetDamageBarsEnabled(on)
	if err := s.Save(); err != nil {
		return err
	}
	return nil
}

// Defaults returns the block the startup reader installs when nothing is
// stored [02 "Settings"][07 §10].
//
// The six skirmish rule scalars are set here rather than in Normalize because
// zero is a legitimate stored choice for every one of them — Easy, randomized
// start positions, commander death continues, all terrain visible, LOS off,
// elevation ignored — so a loader may not treat a zero as an absent value.
func Defaults() Settings {
	s := Settings{Version: FileVersion, Difficulty: DefaultDifficulty, ScrollSpeed: DefaultScrollSpeed, DamageBars: DefaultDamageBars, UnitLimit: DefaultUnitLimit,
		GameSpeed: DefaultGameSpeed, InterfaceType: DefaultInterfaceType, SwitchAlt: DefaultSwitchAlt, Clock: DefaultClock}
	s.Gameplay = gameplay.Modern
	s.BuilderOptions = DefaultBuilderOptions()
	s.Display = DefaultDisplay()
	s.Presentation = DefaultPresentation()
	s.Audio = DefaultAudio()
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
	s.Gameplay = s.Gameplay.Normalize()
	s.BuilderOptions.Normalize()
	// A stored profile selector is kept verbatim apart from surrounding
	// space: it may be a shipped name or a host path, and this package owns
	// neither vocabulary. An unknown selector is rejected where it is
	// resolved, at the mount boundary, so a typo names itself there.
	s.ContentProfile = strings.TrimSpace(s.ContentProfile)
	// The mutator entries are kept verbatim for the reader that parses them;
	// only an empty set is folded to absent.
	if len(s.Mutators) == 0 {
		s.Mutators = nil
	}
	if s.Version == 0 {
		s.Version = FileVersion
	}
	if s.Difficulty < 0 || s.Difficulty > 2 {
		s.Difficulty = DefaultDifficulty
	}
	if s.ScrollSpeed < 0 || s.ScrollSpeed > 255 {
		s.ScrollSpeed = DefaultScrollSpeed
	}
	// Nanolathe's configured range is wider than retail's startup clamp
	// [08 R-SKIR-01 §6]; see DESIGN_CONTENT_VFS §5. Zero means absent.
	if s.UnitLimit == 0 {
		s.UnitLimit = DefaultUnitLimit
	}
	if s.UnitLimit < MinUnitLimit {
		s.UnitLimit = MinUnitLimit
	}
	if s.UnitLimit > MaxUnitLimit {
		s.UnitLimit = MaxUnitLimit
	}
	// The stored game speed is the speed setter's own clamped range, not the
	// slider's maximum: the setter pulls 21 down to 20 before it stores
	// [07 R-CAM-01 §3][07 R-CAM-01 §7].
	if s.GameSpeed < MinGameSpeed || s.GameSpeed > MaxGameSpeed {
		s.GameSpeed = DefaultGameSpeed
	}
	// Its only consumer reads bit 0 [07 R-CAM-01 §4]. Keep the stored
	// representation semantic too.
	s.SwitchAlt &= 1
	// The `clock` DWORD likewise supplies only one option bit [02 "Settings"]
	// [07 R-CAM-01 §6].
	s.Clock &= 1
	s.Display.Normalize()
	s.Presentation.Normalize()
	s.Audio.Normalize()
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
	// This is a load-only substitution; write-all preserves +Gamma 10 until
	// the next startup [07 R-FE-01 §11].
	if s.Display.Gamma == 10 {
		s.Display.Gamma = DefaultGamma
	}
	// Startup clamps the stored interface word to the two stages of LEFTCLICK.
	// The typed +IFace command may write any integer during a running battle,
	// and the settings writer preserves that raw value until the next load
	// [02 "Settings"][07 R-CAM-01 §5][07 R-CAM-01 §6].
	if s.InterfaceType > InterfaceTypeRightClick {
		s.InterfaceType = InterfaceTypeRightClick
	}
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
// half-written file where the next start expects settings. As a host recovery
// policy, an existing unreadable or unsupported file is never replaced; return
// its error so the caller can report the failure while keeping in-memory state.
func (s Settings) SaveTo(path string) error {
	if _, err := LoadFrom(path); err != nil {
		return fmt.Errorf("settings: preserve existing file: %w", err)
	}
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
