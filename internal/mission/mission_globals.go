// This file: mission-global keys and the optional-media fallback [P1-02].
//
// The census models the retail mission-state block: a per-mission singleton of
// typed slots plus an extended string region read back by tick-time consumers,
// a global game-type integer, and the save blob's GameTime region that carries
// the global tick. Slot placement was recovered from the mission-load and
// battle-setup paths; key-string identities come from the out-of-tree key
// vocabulary notes. The raw slot and address trail lives only in
// $HOME/ta-decompile/notes/cleanroom-scrub-trail.md [P1-02 §1].

package mission

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// GlobalClass distinguishes authoritative vs presentation vs inert [P1-02 §2.1].
type GlobalClass int

const (
	GlobalAuthoritative GlobalClass = iota // changes simulation setup [P1-02 §2.1]
	GlobalPresentation                     // briefing/panorama art only [P1-02 §2.1]
	GlobalInert                            // parsed but no tick reader [P1-02 §2.1]
)

// FatalKind distinguishes fatal vs degrade for missing/optional media [P1-02 §2.2].
type FatalKind int

const (
	FatalKindFatal    FatalKind = iota // load aborts [P1-02 §2.2]
	FatalKindDegrade                   // visual absent but game continues [P1-02 §2.2]
	FatalKindNotFatal                  // empty fallback, no abort [P1-02 §2.2]
)

// CensusEntry is one row of the mission-global key census [P1-02 §2.1].
type CensusEntry struct {
	Key      string      // TDF key as authored, matched case-insensitively [P1-02 §2.1]
	VA       string      // retail key-string vocabulary slot; provenance only, empty when unset [P1-02 §2.1]
	Offset   string      // mission-singleton slot label; the raw slot map is kept out of tree [P1-02 §2.1]
	Type     string      // int/float/string [P1-02 §2.1]
	Default  string      // default literal at call site [P1-02 §2.1]
	Clamp    string      // domain/clamp note [P1-02 §2.1]
	Consumer string      // consumer site [P1-02 §2.1]
	Class    GlobalClass // authoritative/presentation/inert [P1-02 §2.1]
	Fatal    FatalKind   // fatal vs degrade for missing [P1-02 §2.2]
}

// MissionGlobalCensus inventories every mission-global key [P1-02 §2.1].
//
// Rows describe the retail mission-state model: a per-mission singleton of
// typed slots plus an extended string region, a global game-type integer, and
// the save blob's GameTime region [P1-02 §1]. Each row lists key, key-string
// vocabulary slot, singleton slot label, accessor type, default, clamp/domain,
// consumer, and authoritative vs presentation vs inert. Defaults are the
// accessor defaults of the map-global key table [02 map-global keys]; world
// consumption applies its own terrain-side fallbacks [03 §2.2]. Fatal vs
// degrade for optional media is in §2.2 [P1-02 §2.2].
var MissionGlobalCensus = []CensusEntry{
	// Authoritative — change simulation setup [P1-02 §2.1].
	{Key: "numplayers", VA: "", Offset: "presentation string slot", Type: "string", Default: "empty", Clamp: "80-byte string slot; recommended counts, comma list [fmt ota]", Consumer: "presentation only — schema selection uses lobby occupancy, no numeric consumer [08 mission globals]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	// CORRECTION: these four rows previously read Offset "" and named the
	// spawn-credit pass alone, filing them with the [GlobalHeader] keys around
	// them. That placement is wrong, and wrong the same way SurfaceMetal below
	// was: the executable reads all four **with the chosen schema current**
	// [02 R-MAP-01 §5], and all 635 schemas of the reference install author
	// them, none of the 275 [GlobalHeader] blocks. Decoding them from the
	// global section therefore yields the accessor default of zero for every
	// stock mission — Arm campaign mission 2 authors HumanMetal=1000 in each of
	// its three schemas and opened with an empty treasury. Battle setup must
	// resolve them through Mission.StartingResources, which reads the selected
	// schema and falls back to a GlobalHeader-authored word only when the
	// schema does not author the key.
	{Key: "HumanMetal", VA: "", Offset: "per-schema key, NOT [GlobalHeader]", Type: "int", Default: "0", Clamp: "—", Consumer: "battle setup must read the SELECTED SCHEMA's word [02 R-MAP-01 §5] via Mission.StartingResources: the surviving grant writes the human slots' live stock and their storage bonus [08 R-ENTRY-01 §8 step 5][05 R-ECO-01 §4]. The GlobalHeader decode below is retained for a mission file that does author one there, and is the accessor default otherwise", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "HumanEnergy", VA: "", Offset: "per-schema key, NOT [GlobalHeader]", Type: "int", Default: "0", Clamp: "—", Consumer: "as HumanMetal, for energy [02 R-MAP-01 §5]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "ComputerMetal", VA: "", Offset: "per-schema key, NOT [GlobalHeader]", Type: "int", Default: "0", Clamp: "—", Consumer: "as HumanMetal, for the computer slots [02 R-MAP-01 §5]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "ComputerEnergy", VA: "", Offset: "per-schema key, NOT [GlobalHeader]", Type: "int", Default: "0", Clamp: "—", Consumer: "as HumanMetal, for the computer slots' energy [02 R-MAP-01 §5]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	// CORRECTION: this row previously read Offset "" and a Consumer that named
	// only the seeding and the extractor-helper draw, filing SurfaceMetal with
	// the [GlobalHeader] keys around it. That placement is wrong. The key is
	// authored per schema: across the reference install's 275 map .ota files it
	// occurs 635 times and every occurrence is inside a [Schema N] section,
	// none in [GlobalHeader] [08 R-AI-03 §4-A]. Decoding it from the global
	// section therefore yields the accessor default of zero for every map in
	// the corpus, and a battle-setup consumer that trusted this row got a zero
	// that disagreed with the terrain's own seed — Nanolathe defect PT3-14.
	{Key: "SurfaceMetal", VA: "", Offset: "per-schema key, NOT [GlobalHeader]", Type: "int", Default: "0", Clamp: "0..255 → byte trunc at cell+7", Consumer: "battle setup must read the SELECTED SCHEMA's word [08 R-AI-03 §4-A]: uniform per-cell metal-byte seeding, the AI scatter helper's acceptance limit surfaceMetal*footZ*footX*2, and the extractor-helper selector draw with bound 255 [03][08 placement helpers]. The GlobalHeader decode below is retained for a mission file that does author one there, and is the accessor default otherwise", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "maxunits", VA: "", Offset: "unit-limit word", Type: "int", Default: "200", Clamp: "—", Consumer: "read into the active unit-limit word used during play; the save Summary persists the low 16 bits [02 unit limit][08 Summary]. The 250 figure is the totala.ini [Preferences] UnitLimit profile default — a different mechanism [02 §5 R-CONTENT-03]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "UseOnlyUnits", VA: "", Offset: "path", Type: "string", Default: "empty", Clamp: "—", Consumer: "resolves into the campaign useonly area [08 restriction-flag disposition]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "aiprofile", VA: "", Offset: "", Type: "string", Default: "empty", Clamp: "—", Consumer: "resource slot loads ai\\<profile>.txt with fallback to ai\\default.txt, feeding strategic weights [08 planner]", Class: GlobalAuthoritative, Fatal: FatalKindDegrade},
	{Key: "minwindspeed", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "authored nonnegative value overrides the terrain value for canonical maps only [03 §2.2]", Consumer: "briefing wind draw rand()%(max-min+1)+min on the CRT stream [08 wind draws]; canonical TNT hard-codes 100 when the key is absent [03 §2.2]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "maxwindspeed", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "authored nonnegative value overrides the terrain value for canonical maps only [03 §2.2]", Consumer: "briefing wind draw; wind-generator scalar [03]; canonical TNT hard-codes 2000 when the key is absent [03 §2.2]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "gravity", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "authored nonnegative value overrides the terrain value for canonical maps only [03 §2.2]", Consumer: "vertical drift and projectile gravity [03]; the 0x1FDB (8155) constant is the world-init fallback when neither TNT nor OTA supplies gravity [03 §2.2]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "tidalstrength", VA: "", Offset: "", Type: "float", Default: "0.0", Clamp: "—", Consumer: "tidalGenerator economy; world consumption falls back to 0.5 when the key is absent [03 §2.2]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "lavaworld", VA: "", Offset: "", Type: "int", Default: "0 bool", Clamp: "nonzero→lava", Consumer: "world-init lava flood sweep; lava splash effects [03]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "nosealeveltrigger", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "—", Consumer: "water test gate", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "waterdoesdamage", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "—", Consumer: "acid-water flag", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "waterdamage", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "amount", Consumer: "water damage tick", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "killmul", VA: "", Offset: "", Type: "float", Default: "0.0", Clamp: "—", Consumer: "score: int(kills*killmul) [02 map-global keys][08 mission globals]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "timemul", VA: "", Offset: "", Type: "float", Default: "0.0", Clamp: "retail campaign maps may author the signed sentinel -1 [fmt ota]", Consumer: "score: int(ticks/60*timemul), the tick divide UNSIGNED integer and not 1800 [08 R-CAMP-01 §11][02 map-global keys][08 mission globals]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "mapping", VA: "", Offset: "mapping option word", Type: "int", Default: "0", Clamp: "—", Consumer: "the OTA loader stores it into the single-player mapping option word; campaign battle entry copies its low bit into visibility mode bit 0 — Unmapped when set [08 R-SKIR-01 §4][03 R-VIS-01 §1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "lineofsight", VA: "SingleLineOfSight", Offset: "line-of-sight option word", Type: "int", Default: "0", Clamp: "—", Consumer: "stored into the single-player line-of-sight option word; campaign battle entry copies its low bit into visibility mode bit 1 — current-sight tracking on when set, Permanent when clear [08 R-SKIR-01 §4][03 R-VIS-01 §1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "LOSType", VA: "SingleLOSType", Offset: "LOS type enum", Type: "int", Default: "—", Clamp: "sprite vs ray", Consumer: "none: the OTA loader forces the LOSType option word to 1 whenever it parses a GlobalHeader, so campaign mode bit 2 is always the terrain ray and never reads an authored key — this decode is an inert carry [08 R-SKIR-01 §4]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "commanderDeath", VA: "SingleCommanderDeath", Offset: "commanderDeath", Type: "int", Default: "1 registry default", Clamp: "—", Consumer: "lobby-value rule for the skirmish defeat gate and respawn path; no mission-level key, no injected trigger [08 mission globals]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorWeapon", VA: "", Offset: "selected-schema storm record", Type: "string", Default: "empty (decode)", Clamp: "original empty predicate", Consumer: "meteor scheduler reads the selected schema, never GlobalHeader; it chooses enablement from that original value before the METEOR.TDF [Default] whole-record replacement [02 meteor merge][06 §6.5]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorRadius", VA: "", Offset: "selected-schema storm record", Type: "int", Default: "0 (decode)", Clamp: "zero selects the whole Default record", Consumer: "meteor scheduler; one zero numeric selects METEOR.TDF [Default] for all five values [02 meteor merge][06 §6.5]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorDensity", VA: "", Offset: "selected-schema storm record", Type: "float32", Default: "0.0 (decode)", Clamp: "zero selects the whole Default record", Consumer: "meteor scheduler source single store, then working-precision spacing conversion [06 §6.5][01 R-DET-01 §1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorDuration", VA: "", Offset: "selected-schema storm record", Type: "float32", Default: "0.0 (decode)", Clamp: "zero selects the whole Default record", Consumer: "meteor scheduler source single store, then working-precision duration conversion [06 §6.5][01 R-DET-01 §1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorInterval", VA: "", Offset: "selected-schema storm record", Type: "float32", Default: "0.0 (decode)", Clamp: "zero selects the whole Default record", Consumer: "meteor scheduler source single store, then working-precision interval conversion [06 §6.5][01 R-DET-01 §1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	// Presentation — briefing/panorama only [P1-02 §2.1].
	{Key: "Planet", VA: "", Offset: "fixed-size string slot", Type: "string", Default: "empty 15-value enum Green…Crystal", Clamp: "enum via the planet-table triple", Consumer: "planet selects parallel brief/pan/rotate tables [P0-05]", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "brief", VA: "", Offset: "briefing text", Type: "string", Default: "empty", Clamp: "—", Consumer: "briefing text from camps\\briefs", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "narration", VA: "", Offset: "narration", Type: "string", Default: "empty", Clamp: "—", Consumer: "camps\\briefs speech alias", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "missionhint", VA: "", Offset: "hint", Type: "string", Default: "empty", Clamp: "—", Consumer: "camps\\hints WAV [P1-02 §2.1]", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "glamour", VA: "", Offset: "glamour", Type: "string", Default: "empty", Clamp: "—", Consumer: "panorama/glam art", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "glamoursound", VA: "", Offset: "glamourSound", Type: "string", Default: "empty", Clamp: "—", Consumer: "audio alias", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "missiondescription", VA: "", Offset: "fixed-size string slot", Type: "string", Default: "\"No description available\" fallback", Clamp: "menu description", Consumer: "menu description [02 map-global keys]", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	// Inert — parsed but no tick reader [P1-02 §2.1] bounded negative.
	{Key: "memory", VA: "", Offset: "fixed-size string slot", Type: "string", Default: "empty", Clamp: "menu-only, no tick read", Consumer: "menu display only [P1-02 §2.1]", Class: GlobalInert, Fatal: FatalKindNotFatal},
	{Key: "nomovie", VA: "", Offset: "bool", Type: "int", Default: "0", Clamp: "suppresses briefing movie", Consumer: "presentation only [P1-02 §2.1]", Class: GlobalInert, Fatal: FatalKindNotFatal},
	// Timers/display — sibling deadlines [P1-02 §2.1]. Every consumer below is
	// now named: the settlement deadline, the persisted-only WinLoseTime, the
	// HUD refresh deadline and the scheduler's globalTick [08 "Player records"].
	{Key: "UpdateTime", VA: "save key UpdateTime", Offset: "per-player Players-box slot [08 save]", Type: "int32 tick", Default: "0 (decode); battle init seeds the current tick [08 player records]", Clamp: "absolute tick, advanced by 30 when due", Consumer: "economy settlement deadline vs the global tick [05 settlement]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "WinLoseTime", VA: "save key WinLoseTime", Offset: "per-player Players-box slot", Type: "int32", Default: "0 (decode); battle init seeds the current tick [08 player records]", Clamp: "—", Consumer: "persisted verbatim; no other reader (closed) [08 save]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "DisplayTimer", VA: "save key DisplayTimer", Offset: "per-player Players-box slot", Type: "int32", Default: "0 (decode); battle init seeds the current tick [08 player records]", Clamp: "—", Consumer: "HUD resource-rate refresh deadline [08 save]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "GlobalTick", VA: "save Players/GameTime box key", Offset: "scheduler-block globalTick word [08 save]", Type: "int32", Default: "persisted blob", Clamp: "tick executor inc", Consumer: "globalTick before phase1 [P0-09]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "EndCountdown", VA: "runtime countdown, not a save key", Offset: "countdown + end-latch word", Type: "i16/byte", Default: "armed at four, decremented on the ~30-tick cadence; latch written at end", Clamp: "not persisted", Consumer: "settlement gate + front-end latch", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
}

// PlanetNames is the 15-value planet enum behind the parallel planet tables [P1-02 §2.1].
// Green planet … Crystal, plus the Lunar special-case briefing rewrite when the
// display flag is set [P0-05].
var PlanetNames = [15]string{
	"Green planet", "Red planet", "Lava", "Metal", "Ice",
	"Lush", "Archipelago", "Slate", "Lunar", "Water World",
	"Wet Desert", "Acid", "Crystal", "Desert", "Urban",
}

// PlanetBriefKeys is the briefing-key parallel table, Greenbrief…Crystalbrief [P1-02 §2.1].
var PlanetBriefKeys = [15]string{
	"Greenbrief", "Redbrief", "Lavabrief", "Metalbrief", "Icebrief",
	"Lushbrief", "Archipelagobrief", "Slatebrief", "Lunarbrief", "Waterbrief",
	"WetDesertbrief", "Acidbrief", "Crystalbrief", "Desertbrief", "Urbanbrief",
}

// PlanetPanKeys is the panorama GAF key table, GreenPan…CrystalPan [P1-02 §2.1].
var PlanetPanKeys = [15]string{
	"GreenPan", "RedPan", "LavaPan", "MetalPan", "IcePan",
	"LushPan", "ArchipelagoPan", "SlatePan", "LunarPan", "WaterPan",
	"WetDesertPan", "AcidPan", "CrystalPan", "DesertPan", "UrbanPan",
}

// PlanetRotateKeys is GreenRotate… [P1-02 §2.1].
var PlanetRotateKeys = [15]string{
	"GreenRotate", "RedRotate", "LavaRotate", "MetalRotate", "IceRotate",
	"LushRotate", "ArchipelagoRotate", "SlateRotate", "LunarRotate", "WaterRotate",
	"WetDesertRotate", "AcidRotate", "CrystalRotate", "DesertRotate", "UrbanRotate",
}

// IsAuthoritative reports whether key is authoritative (simulation) [P1-02 §2.1].
func IsAuthoritative(key string) bool {
	for _, e := range MissionGlobalCensus {
		if strings.EqualFold(e.Key, key) && e.Class == GlobalAuthoritative {
			return true
		}
	}
	return false
}

// IsPresentation reports whether key is presentation-only [P1-02 §2.1].
func IsPresentation(key string) bool {
	for _, e := range MissionGlobalCensus {
		if strings.EqualFold(e.Key, key) && e.Class == GlobalPresentation {
			return true
		}
	}
	return false
}

// IsInert reports whether key is inert (no tick reader) [P1-02 §2.1].
func IsInert(key string) bool {
	for _, e := range MissionGlobalCensus {
		if strings.EqualFold(e.Key, key) && e.Class == GlobalInert {
			return true
		}
	}
	return false
}

// MissionGlobals is the decoded authoritative mission-global block for battle
// setup [P1-02 §2.1]. Presentation/inert fields are retained for menu but not
// simulation. Field defaults are the accessor defaults of the map-global key
// table [02 map-global keys]; terrain-side fallbacks (canonical TNT hard-codes,
// the 0x1FDB gravity and 0.5 tidal constants) apply at world consumption, not
// here [03 §2.2].
type MissionGlobals struct {
	NumPlayers         string  // numplayers string slot, empty; presentation only [02 map-global keys][08 mission globals]
	HumanMetal         int32   // starting human metal, integer accessor, default 0 [02 map-global keys]
	HumanEnergy        int32   // starting human energy, integer accessor, default 0 [02 map-global keys]
	ComputerMetal      int32   // starting computer metal, integer accessor, default 0 [02 map-global keys]
	ComputerEnergy     int32   // starting computer energy, integer accessor, default 0 [02 map-global keys]
	SurfaceMetal       int32   // [GlobalHeader] SurfaceMetal only, default 0. NOT the battle-setup word: the key is authored per schema and no reference map authors it here, so this field is the accessor default on the whole corpus. Battle setup reads the selected schema's word [08 R-AI-03 §4-A][P1-02 §2.1][P1-15]
	MaxUnits           int32   // maxunits default 200 into the active unit-limit word [02 map-global keys][02 unit limit]
	UseOnlyUnitsPath   string  // UseOnlyUnits → camps\useonly [P1-02 §2.1]
	AIProfile          string  // aiprofile, empty; ai\default.txt fallback happens at profile load [02 map-global keys][08 planner]
	Planet             string  // planet enum string, 15 values Green…Crystal [P1-02 §2.1]
	Brief              string  // briefing text, presentation [P1-02 §2.1]
	Narration          string  // presentation [P1-02 §2.1]
	MissionHint        string  // presentation [P1-02 §2.1]
	Glamour            string  // presentation [P1-02 §2.1]
	GlamourSound       string  // presentation [P1-02 §2.1]
	MinWind            int32   // minwindspeed accessor default 0; canonical TNT hard-codes 100 [02 map-global keys][03 §2.2]
	MaxWind            int32   // maxwindspeed accessor default 0; canonical TNT hard-codes 2000 [02 map-global keys][03 §2.2]
	Gravity            int32   // gravity accessor default 0; 0x1FDB is the world-init fallback [02 map-global keys][03 §2.2]
	TidalStrength      float64 // tidalstrength accessor default 0.0; 0.5 is the world-init fallback [02 map-global keys][03 §2.2]
	LavaWorld          int32   // lavaworld flag [P1-02 §2.1]
	NoSeaLevelTrigger  int32   // no-sea-level trigger [P1-02 §2.1]
	WaterDoesDamage    int32   // acid-water flag [P1-02 §2.1]
	WaterDamage        int32   // water damage amount [P1-02 §2.1]
	KillMul            float64 // score kill multiplier, default 0.0 [02 map-global keys][08 mission globals]
	TimeMul            float64 // score time multiplier, default 0.0 [02 map-global keys][08 mission globals]
	Memory             string  // memory requirement, inert [P1-02 §2.1]
	NoMovie            int32   // nomovie inert [P1-02 §2.1]
	MissionDescription string  // menu description, fallback "No description available" [02 map-global keys]
	Mapping            int32   // mapping, default 0 [02 map-global keys]; its low bit IS the campaign visibility mode word's bit 0 [08 R-SKIR-01 §4][03 R-VIS-01 §1]
	LineOfSight        int32   // lineofsight, default 0 [02 map-global keys]; its low bit IS the campaign visibility mode word's bit 1 [08 R-SKIR-01 §4][03 R-VIS-01 §1]
	LOSType            int32   // authored LOSType/SingleLOSType if the OTA carries one; inert — the OTA loader forces the LOSType option word to 1, so mode bit 2 never reads this key [08 R-SKIR-01 §4]
	MeteorWeapon       string  // GlobalHeader carry only; scheduler reads selected schema [06 §6.5]
	MeteorRadius       int32   // GlobalHeader carry only; selected schema owns the storm record [06 §6.5]
	MeteorDensity      float32 // GlobalHeader carry only; source single store when selected schema installs [06 §6.5]
	MeteorDuration     float32 // GlobalHeader carry only; source single store when selected schema installs [06 §6.5]
	MeteorInterval     float32 // GlobalHeader carry only; source single store when selected schema installs [06 §6.5]
	UpdateTime         int32   // economy settlement deadline, per-player save slot, default 0 [08 player records]
	WinLoseTime        int32   // save WinLoseTime slot, default 0 [08 player records]
	DisplayTimer       int32   // HUD resource-rate refresh deadline, default 0 [08 player records]
}

// DecodeMissionGlobals decodes mission-global keys from GlobalHeader via typed
// accessors [02 map-global keys] [P1-02 §2.1]. Absent keys decode to the
// accessor defaults of the map-global key table; the terrain-side fallbacks
// (canonical TNT wind 100/2000 and gravity 0, the 0x1FDB gravity and 0.5 tidal
// constants when neither source supplies) are world-consumption concerns [03 §2.2].
func DecodeMissionGlobals(global *formats.Section) *MissionGlobals {
	if global == nil {
		// A missing GlobalHeader is fatal upstream [02 mission-file diagnostics];
		// this nil path mirrors decoding an empty section: accessor defaults only.
		return DecodeMissionGlobals(&formats.Section{Name: "GlobalHeader"})
	}
	mg := &MissionGlobals{}
	mg.NumPlayers, _ = global.StringValue("numplayers", "") // string slot, presentation only [02 map-global keys][08 mission globals]
	// Starting resources use the integer accessor, default 0 [02 map-global keys].
	mg.HumanMetal = global.IntValue("HumanMetal", 0)
	mg.HumanEnergy = global.IntValue("HumanEnergy", 0)
	mg.ComputerMetal = global.IntValue("ComputerMetal", 0)
	mg.ComputerEnergy = global.IntValue("ComputerEnergy", 0)
	// The global section is not where maps author this key; the selected schema
	// is [08 R-AI-03 §4-A]. The read stays for a mission file that does author
	// one here, but no consumer may treat its default as the battle word.
	mg.SurfaceMetal = global.IntValue("SurfaceMetal", 0)  // 0 [02 map-global keys] → byte cell+7
	mg.MaxUnits = global.IntValue("maxunits", 200)        // 200 [02 map-global keys]; 250 is the totala.ini UnitLimit profile default, a different mechanism [02 §5 R-CONTENT-03]
	mg.UseOnlyUnitsPath = DecodeUseOnlyUnits(global)      // path building [P1-02 §2.1] C8
	mg.AIProfile, _ = global.StringValue("aiprofile", "") // empty default; ai\default.txt fallback happens at profile load [02 map-global keys][08 planner]
	mg.Planet, _ = global.StringValue("Planet", "")       // 15-enum Green…Crystal [P1-02 §2.1]
	mg.Brief, _ = global.StringValue("brief", "")
	mg.Narration, _ = global.StringValue("narration", "")
	mg.MissionHint, _ = global.StringValue("missionhint", "")
	mg.Glamour, _ = global.StringValue("glamour", "")
	mg.GlamourSound, _ = global.StringValue("glamoursound", "")
	mg.MinWind = global.IntValue("minwindspeed", 0)          // 0 [02 map-global keys]; canonical TNT hard-codes 100 [03 §2.2]
	mg.MaxWind = global.IntValue("maxwindspeed", 0)          // 0 [02 map-global keys]; canonical TNT hard-codes 2000 [03 §2.2]
	mg.Gravity = global.IntValue("gravity", 0)               // 0 [02 map-global keys]; 0x1FDB fallback when neither TNT nor OTA supplies [03 §2.2]
	mg.TidalStrength = global.FloatValue("tidalstrength", 0) // 0.0 [02 map-global keys]; 0.5 fallback at world consumption [03 §2.2]
	mg.LavaWorld = global.IntValue("lavaworld", 0)           // bool [P1-02 §2.1]
	mg.NoSeaLevelTrigger = global.IntValue("nosealeveltrigger", 0)
	mg.WaterDoesDamage = global.IntValue("waterdoesdamage", 0)
	mg.WaterDamage = global.IntValue("waterdamage", 0)
	mg.KillMul = global.FloatValue("killmul", 0) // 0.0 [02 map-global keys][08 mission globals]
	mg.TimeMul = global.FloatValue("timemul", 0) // 0.0 [02 map-global keys][08 mission globals]
	mg.Memory, _ = global.StringValue("memory", "")
	mg.NoMovie = global.IntValue("nomovie", 0) // inert [P1-02 §2.1]
	// The string accessor copies the caller's default only on an ABSENT key and
	// does a bounded copy of the stored text on a PRESENT one, reporting which
	// path it took [02 §4 "Accessor table"] — a string field is exactly the one
	// kind that distinguishes an authored empty value from a missing key. So an
	// authored-empty `missiondescription` stays empty and does not fall back to
	// the literal, which is the missing-key default alone
	// [02 R-MAP-01 §3 row 13].
	mg.MissionDescription, _ = global.StringValue("missiondescription", "No description available")
	mg.Mapping = global.IntValue("mapping", 0)         // 0 [02 map-global keys]; the skirmish lobby triple is a separate mechanism [08 R-SKIR-01 §4]
	mg.LineOfSight = global.IntValue("lineofsight", 0) // 0 [02 map-global keys]
	mg.LOSType, _ = func() (int32, bool) {             // SingleLOSType/MultiLOSType enum
		if _, ok := global.RawValue("SingleLOSType"); ok {
			return global.IntValue("SingleLOSType", 0), true
		}
		if _, ok := global.RawValue("LOSType"); ok {
			return global.IntValue("LOSType", 0), true
		}
		return 0, false
	}()
	// CORRECTION: this note used to name the registry triple `SingleMapping`/
	// `SingleLineOfSight`/`SingleLOSType` (each defaulting to 1) as the source
	// of a campaign session's mode bit 2, and pointed at `internal/settings` as
	// the place holding it. The registry triple is read once at start-up and
	// then shadowed: every time the OTA loader parses a `[GlobalHeader]` it
	// rewrites the single-player option words from `mapping` and `lineofsight`
	// and *forces* the companion words — LOSType to 1, commander death to 0 —
	// so for a campaign battle the `Single*` values reach nothing
	// [08 R-SKIR-01 §4]. The mode word's bits 0 and 1 are the low bits of
	// Mapping and LineOfSight above; bit 2 is the forced 1, not this key.
	//
	// The map-global key table still carries no LOSType row, and this decode
	// stays a lossless carry of an authored key for diagnostics only: the field
	// has no session reader, and the mode word must not be built from it
	// [03 R-VIS-01 §1][08 R-ENTRY-01 §2 step 4].
	// No commanderDeath decode: there is no mission-level key — the rule is a
	// lobby value, and no defeat trigger is injected for it [08 mission globals].
	mg.MeteorWeapon, _ = global.StringValue("MeteorWeapon", "") // empty disables [02 meteor merge]
	mg.MeteorRadius = global.IntValue("MeteorRadius", 0)        // 0 [02 map-global keys]; METEOR.TDF substitution happens at storm resolution [02 meteor merge][06 §6.5]
	mg.MeteorDensity = float32(global.FloatValue("MeteorDensity", 0))
	mg.MeteorDuration = float32(global.FloatValue("MeteorDuration", 0))
	mg.MeteorInterval = float32(global.FloatValue("MeteorInterval", 0))
	mg.UpdateTime = global.IntValue("UpdateTime", 0)     // i32 0 [08 player records]; battle init seeds the current tick
	mg.WinLoseTime = global.IntValue("WinLoseTime", 0)   // i32 0 [08 player records]
	mg.DisplayTimer = global.IntValue("DisplayTimer", 0) // i32 0 [08 player records]
	return mg
}

// PlanetIndex returns the planet-table index for the planet string, matched
// case-insensitively [P1-02 §2.1], or -1 when unknown → presentation leaves
// empty but game still loads degraded not fatal [P1-02 §2.2].
func PlanetIndex(planet string) int {
	planet = strings.TrimSpace(planet)
	for i, name := range PlanetNames {
		if strings.EqualFold(name, planet) {
			return i
		}
	}
	return -1 // unknown → no pan/rotate fetched, no abort [P1-02 §2.2]
}

// ResolvePlanetMedia resolves brief/pan/rotate GAF keys for planet enum [P1-02 §2.1]
// via the planet-table triple. Returns empty when planet unknown (degrade) [P1-02 §2.2].
func ResolvePlanetMedia(planet string) (briefKey, panKey, rotateKey string, ok bool) {
	idx := PlanetIndex(planet)
	if idx < 0 {
		return "", "", "", false // unknown planet → no GAF, degraded not fatal [P1-02 §2.2]
	}
	return PlanetBriefKeys[idx], PlanetPanKeys[idx], PlanetRotateKeys[idx], true
}

// MediaFatal reports whether missing media for key is fatal [P1-02 §2.2].
func MediaFatal(key string) FatalKind {
	lower := strings.ToLower(strings.TrimSpace(key))
	switch lower {
	case "tnt", "version", "idversion":
		return FatalKindFatal // mandatory TNT version 0x2000/0x1020 fatal [P1-02 §2.2][fmt tnt]
	case "moveinfo", "moveinfo.tdf":
		return FatalKindFatal // Can't load MOVEINFO.TDF fatal [P1-02 §2.2]
	case "translate.tdf":
		return FatalKindNotFatal // empty fallback not fatal byte-exact [P1-02 §2.2][02 §3]
	case "gamedata.tdf":
		return FatalKindNotFatal // SC2 not fatal, no file anywhere [P1-02 §2.2][SPEC_CONFLICTS SC2]
	case "panorama", "brief", "pan", "rotate", "glamour":
		return FatalKindDegrade // optional media leaves visual absent [P1-02 §2.2] G1 §4
	case "sound", "soundcategory":
		return FatalKindDegrade // SC7 diverge muted not fatal [P1-02 §2.2][SPEC_CONFLICTS SC7]
	default:
		// Check global census entry
		for _, e := range MissionGlobalCensus {
			if strings.EqualFold(e.Key, key) {
				return e.Fatal
			}
		}
		return FatalKindDegrade
	}
}
