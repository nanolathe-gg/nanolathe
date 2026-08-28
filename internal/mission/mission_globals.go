// Package mission inventories mission-global keys and optional-media fallback [P1-02].
//
// The census models the retail mission-state block: a per-mission singleton of
// typed slots plus an extended string region read back by tick-time consumers,
// a global game-type integer, and the save blob's GameTime region that carries
// the global tick. Slot placement was recovered from the mission-load and
// battle-setup paths; key-string identities come from the out-of-tree key
// vocabulary notes. The raw slot and address trail lives only in
// /tmp/ta-decompile/notes/cleanroom-scrub-trail.md [P1-02 §1].
package mission

import (
	"strings"

	"github.com/nanolathe/nanolathe/formats"
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
// consumer, and authoritative vs presentation vs inert. Fatal vs degrade for
// optional media is in §2.2 [P1-02 §2.2].
var MissionGlobalCensus = []CensusEntry{
	// Authoritative — change simulation setup [P1-02 §2.1].
	{Key: "numplayers", VA: "", Offset: "presentation string slot", Type: "int", Default: "2", Clamp: "2..10 raw stored then validated via StartPos [P1-02 §4]", Consumer: "presentation only — schema selection uses lobby occupancy, no numeric consumer [08 mission globals]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "HumanMetal", VA: "", Offset: "", Type: "float", Default: "1000", Clamp: "none (float)", Consumer: "spawn-credit pass seeds each side's starting resource stores", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "HumanEnergy", VA: "", Offset: "", Type: "float", Default: "1000", Clamp: "none", Consumer: "spawn-credit pass, as HumanMetal", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "ComputerMetal", VA: "", Offset: "", Type: "float", Default: "1000", Clamp: "none", Consumer: "spawn-credit pass (computer side)", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "ComputerEnergy", VA: "", Offset: "", Type: "float", Default: "1000", Clamp: "none", Consumer: "spawn-credit pass (computer side)", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "SurfaceMetal", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "0..255 → byte trunc at cell+7", Consumer: "uniform per-cell metal-byte seeding; extractor helper draws once with bound 255 and compares against this value [03][08 placement helpers]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "maxunits", VA: "", Offset: "unit-limit word", Type: "int", Default: "250", Clamp: "0..250 then allocator per-def limit", Consumer: "save Summary key; unit allocator enforces per-definition limits [08 mission globals]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "UseOnlyUnits", VA: "", Offset: "path", Type: "string", Default: "empty", Clamp: "—", Consumer: "resolves into the campaign useonly area [08 restriction-flag disposition]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "aiprofile", VA: "", Offset: "", Type: "string", Default: "\"default\" fallback", Clamp: "100-char fallback", Consumer: "loads ai\\<profile>.txt with fallback to ai\\default.txt, feeding strategic weights [08 planner]", Class: GlobalAuthoritative, Fatal: FatalKindDegrade},
	{Key: "minwindspeed", VA: "", Offset: "", Type: "int", Default: "100 canonical fallback", Clamp: "—", Consumer: "briefing wind draw rand()%(max-min+1)+min on the CRT stream [08 wind draws]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "maxwindspeed", VA: "", Offset: "", Type: "int", Default: "2000", Clamp: "—", Consumer: "briefing wind draw; wind-generator scalar [03]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "gravity", VA: "", Offset: "", Type: "int", Default: "0x1FDB (8155) canonical fallback", Clamp: "—", Consumer: "vertical drift and projectile gravity [03]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "tidalstrength", VA: "", Offset: "", Type: "float", Default: "0.5 sentinel -1.0", Clamp: "—", Consumer: "tidalGenerator economy", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "lavaworld", VA: "", Offset: "", Type: "int", Default: "0 bool", Clamp: "nonzero→lava", Consumer: "world-init lava flood sweep; lava splash effects [03]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "nosealeveltrigger", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "—", Consumer: "water test gate", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "waterdoesdamage", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "—", Consumer: "acid-water flag", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "waterdamage", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "amount", Consumer: "water damage tick", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "killmul", VA: "", Offset: "", Type: "float", Default: "1.0 TODO(question) 0 vs 1.0", Clamp: "—", Consumer: "score: int(kills*killmul)", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "timemul", VA: "", Offset: "", Type: "float", Default: "1.0 TODO(question)", Clamp: "—", Consumer: "score: int(ticks/1800*timemul)", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "mapping", VA: "", Offset: "mapping LOS mode", Type: "int", Default: "1 via registry", Clamp: "1 default per registry", Consumer: "MapFlags bit 0 selects full vs byte-grid LOS mode [P1-02 §2.1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "lineofsight", VA: "SingleLineOfSight", Offset: "LOS mode", Type: "int", Default: "1", Clamp: "—", Consumer: "visibility word vs ray [P1-02 §2.1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "LOSType", VA: "SingleLOSType", Offset: "LOS type enum", Type: "int", Default: "—", Clamp: "sprite vs ray", Consumer: "anims/vismasks.gaf vs ray", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "commanderDeath", VA: "SingleCommanderDeath", Offset: "commanderDeath", Type: "int", Default: "1 registry default", Clamp: "—", Consumer: "lobby-value rule for the skirmish defeat gate and respawn path; no mission-level key, no injected trigger [08 mission globals]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorWeapon", VA: "", Offset: "meteor storm record", Type: "string", Default: "empty→disable", Clamp: "empty predicate", Consumer: "meteor scheduler + gamedata\\METEOR.TDF [Default] merge [P1-02 §2.1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorRadius", VA: "", Offset: "meteor storm record", Type: "int", Default: "from METEOR.TDF [Default]", Clamp: "—", Consumer: "meteor scheduler", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorDensity", VA: "", Offset: "meteor storm record", Type: "float", Default: "from METEOR.TDF", Clamp: "—", Consumer: "meteor scheduler", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorDuration", VA: "", Offset: "meteor storm record", Type: "int", Default: "from METEOR.TDF", Clamp: "—", Consumer: "meteor scheduler", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorInterval", VA: "", Offset: "meteor storm record", Type: "int", Default: "from METEOR.TDF", Clamp: "—", Consumer: "meteor scheduler", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	// Presentation — briefing/panorama only [P1-02 §2.1].
	{Key: "Planet", VA: "", Offset: "fixed-size string slot", Type: "string", Default: "empty 15-value enum Green…Crystal", Clamp: "enum via the planet-table triple", Consumer: "planet selects parallel brief/pan/rotate tables [P0-05]", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "brief", VA: "", Offset: "briefing text", Type: "string", Default: "empty", Clamp: "—", Consumer: "briefing text from camps\\briefs", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "narration", VA: "", Offset: "narration", Type: "string", Default: "empty", Clamp: "—", Consumer: "camps\\briefs speech alias", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "missionhint", VA: "", Offset: "hint", Type: "string", Default: "empty", Clamp: "—", Consumer: "camps\\hints WAV [P1-02 §2.1]", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "glamour", VA: "", Offset: "glamour", Type: "string", Default: "empty", Clamp: "—", Consumer: "panorama/glam art", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "glamoursound", VA: "", Offset: "glamourSound", Type: "string", Default: "empty", Clamp: "—", Consumer: "audio alias", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "missiondescription", VA: "", Offset: "fixed-size string slot", Type: "string", Default: "\"No description available\" fallback", Clamp: "menu description", Consumer: "menu description", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	// Inert — parsed but no tick reader [P1-02 §2.1] bounded negative.
	{Key: "memory", VA: "", Offset: "fixed-size string slot", Type: "string", Default: "empty", Clamp: "menu-only, no tick read", Consumer: "menu display only [P1-02 §2.1]", Class: GlobalInert, Fatal: FatalKindNotFatal},
	{Key: "nomovie", VA: "", Offset: "bool", Type: "int", Default: "0", Clamp: "suppresses briefing movie", Consumer: "presentation only [P1-02 §2.1]", Class: GlobalInert, Fatal: FatalKindNotFatal},
	// Timers/display — sibling deadlines [P1-02 §2.1] TODO(question) consumers.
	{Key: "UpdateTime", VA: "save key UpdateTime", Offset: "per-player Players-box slot [08 save]", Type: "int32 tick", Default: "seed globalTick at battle init", Clamp: "absolute tick, advanced by 30 when due", Consumer: "economy settlement deadline vs the global tick [05 settlement]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "WinLoseTime", VA: "save key WinLoseTime", Offset: "per-player Players-box slot", Type: "int32", Default: "seed globalTick", Clamp: "—", Consumer: "persisted verbatim; no other reader (closed) [08 save]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "DisplayTimer", VA: "save key DisplayTimer", Offset: "per-player Players-box slot", Type: "int32", Default: "seed globalTick", Clamp: "—", Consumer: "HUD resource-rate refresh deadline [08 save]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
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
// simulation.
type MissionGlobals struct {
	NumPlayers         int32   // numplayers int 2 default [P1-02 §2.1]
	HumanMetal         float64 // starting human metal, default 1000 [P1-02 §2.1]
	HumanEnergy        float64 // starting human energy, default 1000 [P1-02 §2.1]
	ComputerMetal      float64 // starting computer metal, default 1000 [P1-02 §2.1]
	ComputerEnergy     float64 // starting computer energy, default 1000 [P1-02 §2.1]
	SurfaceMetal       int32   // surface metal seeding, default 0; per-cell metal byte [P1-02 §2.1][P1-15]
	MaxUnits           int32   // maxunits 250 clamp [P1-02 §2.1] vs per-def limit
	UseOnlyUnitsPath   string  // UseOnlyUnits → camps\useonly [P1-02 §2.1]
	AIProfile          string  // aiprofile fallback "default" [P1-02 §2.1]
	Planet             string  // planet enum string, 15 values Green…Crystal [P1-02 §2.1]
	Brief              string  // briefing text, presentation [P1-02 §2.1]
	Narration          string  // presentation [P1-02 §2.1]
	MissionHint        string  // presentation [P1-02 §2.1]
	Glamour            string  // presentation [P1-02 §2.1]
	GlamourSound       string  // presentation [P1-02 §2.1]
	MinWind            int32   // minimum wind, canonical 100 [P1-02 §2.1]
	MaxWind            int32   // maximum wind, canonical 2000 [P1-02 §2.1]
	Gravity            int32   // gravity, fallback 0x1FDB (8155) [P1-02 §2.1]
	TidalStrength      float64 // tidal strength, default 0.5, read sentinel -1.0 [P1-02 §2.1]
	LavaWorld          int32   // lavaworld flag [P1-02 §2.1]
	NoSeaLevelTrigger  int32   // no-sea-level trigger [P1-02 §2.1]
	WaterDoesDamage    int32   // acid-water flag [P1-02 §2.1]
	WaterDamage        int32   // water damage amount [P1-02 §2.1]
	KillMul            float64 // score kill multiplier, TODO(question) 0 vs 1.0 default [P1-02 §8]
	TimeMul            float64 // score time multiplier, TODO(question) default [P1-02 §8]
	Memory             string  // memory requirement, inert [P1-02 §2.1]
	NoMovie            int32   // nomovie inert [P1-02 §2.1]
	MissionDescription string  // menu description, fallback "No description available" [P1-02 §2.1]
	Mapping            int32   // mapping LOS mode [P1-02 §2.1]
	LineOfSight        int32   // lineofsight [P1-02 §2.1]
	LOSType            int32   // LOSType enum [P1-02 §2.1]
	CommanderDeath     int32   // commanderDeath TODO(question) [P1-02 §8]
	MeteorWeapon       string  // MeteorWeapon empty disables [P1-02 §2.1]
	MeteorRadius       int32   // MeteorRadius [P1-02 §2.1]
	MeteorDensity      float64
	MeteorDuration     float64
	MeteorInterval     float64
	UpdateTime         int32 // economy settlement deadline, per-player save slot [08] TODO(question)
	WinLoseTime        int32 // save WinLoseTime slot TODO(question)
	DisplayTimer       int32 // HUD resource-rate refresh deadline [08 save]
}

// DecodeMissionGlobals decodes mission-global keys from GlobalHeader via typed
// accessors [02 §4] [P1-02 §2.1]. Defaults are per census [P1-02 §4].
func DecodeMissionGlobals(global *formats.Section) *MissionGlobals {
	if global == nil {
		return &MissionGlobals{
			MinWind: 100, MaxWind: 2000, Gravity: 0x1FDB,
			TidalStrength: 0.5, KillMul: 1.0, TimeMul: 1.0,
			AIProfile: "default", MaxUnits: 250,
			MissionDescription: "No description available",
		}
	}
	mg := &MissionGlobals{}
	mg.NumPlayers = global.IntValue("numplayers", 2)      // 2 [P1-02 §2.1]
	mg.HumanMetal = global.FloatValue("HumanMetal", 1000) // 1000 stock [P1-02 §2.1]
	mg.HumanEnergy = global.FloatValue("HumanEnergy", 1000)
	mg.ComputerMetal = global.FloatValue("ComputerMetal", 1000)
	mg.ComputerEnergy = global.FloatValue("ComputerEnergy", 1000)
	mg.SurfaceMetal = global.IntValue("SurfaceMetal", 0) // 0 [P1-02 §2.1] → byte cell+7
	mg.MaxUnits = global.IntValue("maxunits", 250)       // 250 before clamp [P1-02 §2.1]
	mg.UseOnlyUnitsPath = DecodeUseOnlyUnits(global)     // path building [P1-02 §2.1] C8
	v, _ := global.StringValue("aiprofile", "default")   // fallback "default" [P1-02 §2.1]
	if strings.TrimSpace(v) == "" {
		v = "default"
	}
	mg.AIProfile = v
	mg.Planet, _ = global.StringValue("Planet", "") // 15-enum Green…Crystal [P1-02 §2.1]
	mg.Brief, _ = global.StringValue("brief", "")
	mg.Narration, _ = global.StringValue("narration", "")
	mg.MissionHint, _ = global.StringValue("missionhint", "")
	mg.Glamour, _ = global.StringValue("glamour", "")
	mg.GlamourSound, _ = global.StringValue("glamoursound", "")
	mg.MinWind = global.IntValue("minwindspeed", 100)  // canonical 100 [P1-02 §2.1]
	mg.MaxWind = global.IntValue("maxwindspeed", 2000) // 2000 [P1-02 §2.1]
	// Gravity fallback 0x1FDB when neither OTA nor TNT supplies [P1-02 §8]
	mg.Gravity = global.IntValue("gravity", 0x1FDB)
	// TidalStrength default 0.5 sentinel -1.0 at read [P1-02 §2.1][P1-02 §4]
	if _, ok := global.RawValue("tidalstrength"); ok {
		mg.TidalStrength = global.FloatValue("tidalstrength", 0.5)
	} else {
		mg.TidalStrength = 0.5 // 0x3F000000 default when not authored
	}
	mg.LavaWorld = global.IntValue("lavaworld", 0) // bool [P1-02 §2.1]
	mg.NoSeaLevelTrigger = global.IntValue("nosealeveltrigger", 0)
	mg.WaterDoesDamage = global.IntValue("waterdoesdamage", 0)
	mg.WaterDamage = global.IntValue("waterdamage", 0)
	// killmul/timemul default TODO(question) 0 vs 1.0 [P1-02 §8]; use 1.0 for non-zero score.
	if _, ok := global.RawValue("killmul"); ok {
		mg.KillMul = global.FloatValue("killmul", 1.0)
	} else {
		mg.KillMul = 1.0 // TODO(question) [P1-02 §8]
	}
	if _, ok := global.RawValue("timemul"); ok {
		mg.TimeMul = global.FloatValue("timemul", 1.0)
	} else {
		mg.TimeMul = 1.0 // TODO(question)
	}
	mg.Memory, _ = global.StringValue("memory", "")
	mg.NoMovie = global.IntValue("nomovie", 0)                                                      // inert [P1-02 §2.1]
	mg.MissionDescription, _ = global.StringValue("missiondescription", "No description available") // fallback [P1-02 §2.1]
	if strings.TrimSpace(mg.MissionDescription) == "" {
		mg.MissionDescription = "No description available" // canonical substitute [P1-02 §2.1]
	}
	mg.Mapping = global.IntValue("mapping", 1) // 1 default per registry [P1-02 §2.1]
	mg.LineOfSight = global.IntValue("lineofsight", 1)
	mg.LOSType, _ = func() (int32, bool) { // SingleLOSType/MultiLOSType enum
		if _, ok := global.RawValue("SingleLOSType"); ok {
			return global.IntValue("SingleLOSType", 0), true
		}
		if _, ok := global.RawValue("LOSType"); ok {
			return global.IntValue("LOSType", 0), true
		}
		return 0, false
	}()
	mg.CommanderDeath = global.IntValue("SingleCommanderDeath", 1) // TODO(question) trigger mapping [P1-02 §8]
	if _, ok := global.RawValue("SingleCommanderDeath"); !ok {
		if v, ok := global.RawValue("commanderDeath"); ok && strings.TrimSpace(v) != "" {
			mg.CommanderDeath = global.IntValue("commanderDeath", 1)
		}
	}
	mg.MeteorWeapon, _ = global.StringValue("MeteorWeapon", "") // empty disables [P1-02 §2.1]
	mg.MeteorRadius = global.IntValue("MeteorRadius", 0)
	mg.MeteorDensity = global.FloatValue("MeteorDensity", 0)
	mg.MeteorDuration = global.FloatValue("MeteorDuration", 0)
	mg.MeteorInterval = global.FloatValue("MeteorInterval", 0)
	mg.UpdateTime = global.IntValue("UpdateTime", 0)     // per-player save slot TODO(question)
	mg.WinLoseTime = global.IntValue("WinLoseTime", 0)   // save WinLoseTime slot
	mg.DisplayTimer = global.IntValue("DisplayTimer", 0) // save DisplayTimer slot
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

// IsOptionalMediaNotFatal reports true when missing media degrades rather than aborts [P1-02 §2.2].
func IsOptionalMediaNotFatal(logical string) bool {
	return MediaFatal(logical) != FatalKindFatal
}
