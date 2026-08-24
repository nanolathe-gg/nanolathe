// Package mission inventories mission-global keys and optional-media fallback [P1-02].
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	Key      string      // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	VA       string      // string VA in vocabulary, empty when none [P1-02 §2.1]
	Offset   string      // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Type     string      // int/float/string [P1-02 §2.1]
	Default  string      // default literal at call site [P1-02 §2.1]
	Clamp    string      // domain/clamp note [P1-02 §2.1]
	Consumer string      // consumer site [P1-02 §2.1]
	Class    GlobalClass // authoritative/presentation/inert [P1-02 §2.1]
	Fatal    FatalKind   // fatal vs degrade for missing [P1-02 §2.2]
}

// MissionGlobalCensus inventories every mission-global key [P1-02 §2.1].
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// GameTime blob [P1-02 §1]. Each row lists key, VA, offset, accessor type,
// default, clamp/domain, consumer, and authoritative vs presentation vs inert.
// Fatal vs degrade for optional media is in §2.2 [P1-02 §2.2].
var MissionGlobalCensus = []CensusEntry{
	// Authoritative — change simulation setup [P1-02 §2.1].
	{Key: "numplayers", VA: "", Offset: "", Type: "int", Default: "2", Clamp: "2..10 raw stored then validated via StartPos [P1-02 §4]", Consumer: "[analysis omitted] placement, [analysis omitted] HOT striping, [analysis omitted]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "HumanMetal", VA: "", Offset: "", Type: "float", Default: "1000", Clamp: "none (float)", Consumer: "[analysis omitted] spawnCredits → player[layout omitted]/0x98", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "HumanEnergy", VA: "", Offset: "", Type: "float", Default: "1000", Clamp: "none", Consumer: "[analysis omitted]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "ComputerMetal", VA: "", Offset: "", Type: "float", Default: "1000", Clamp: "none", Consumer: "[analysis omitted]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "ComputerEnergy", VA: "", Offset: "", Type: "float", Default: "1000", Clamp: "none", Consumer: "[analysis omitted]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "SurfaceMetal", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "0..255 → byte trunc at cell+7", Consumer: "[analysis omitted] RNG(255) branch, [analysis omitted] seed", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "maxunits", VA: "", Offset: "mission+? GlobalHeader", Type: "int", Default: "250", Clamp: "0..250 then allocator per-def limit", Consumer: "[analysis omitted] summary, [analysis omitted] allocator per-def", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "UseOnlyUnits", VA: "", Offset: "path", Type: "string", Default: "empty", Clamp: "—", Consumer: "camps\\useonly via [analysis omitted] resolver [P1-02 §2.1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "aiprofile", VA: "", Offset: "", Type: "string", Default: "\"default\" via [analysis omitted]", Clamp: "100-char fallback", Consumer: "[analysis omitted] ai\\<profile>.txt → strategic weights", Class: GlobalAuthoritative, Fatal: FatalKindDegrade},
	{Key: "minwindspeed", VA: "", Offset: "", Type: "int", Default: "100 canonical fallback", Clamp: "—", Consumer: "[analysis omitted] rand()%(max-min+1)+min briefing CRT", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "maxwindspeed", VA: "", Offset: "", Type: "int", Default: "2000", Clamp: "—", Consumer: "[analysis omitted] + [analysis omitted] wind scalar", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "gravity", VA: "", Offset: "", Type: "int", Default: "0x1FDB 8155 if absent canonical", Clamp: "—", Consumer: "[analysis omitted] vy drift, projectile gravity", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "tidalstrength", VA: "", Offset: "", Type: "float", Default: "0.5 sentinel -1.0", Clamp: "—", Consumer: "tidalGenerator economy", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "lavaworld", VA: "", Offset: "", Type: "int", Default: "0 bool", Clamp: "nonzero→lava", Consumer: "[analysis omitted] 0xFFFD flood, [analysis omitted] splash", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "nosealeveltrigger", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "—", Consumer: "[analysis omitted] water test gate", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "waterdoesdamage", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "—", Consumer: "[analysis omitted] acid water flag", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "waterdamage", VA: "", Offset: "", Type: "int", Default: "0", Clamp: "amount", Consumer: "water damage tick", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "killmul", VA: "", Offset: "", Type: "float", Default: "1.0 TODO(question) 0 vs 1.0", Clamp: "—", Consumer: "[analysis omitted] score int(kills*killmul)", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "timemul", VA: "", Offset: "", Type: "float", Default: "1.0 TODO(question)", Clamp: "—", Consumer: "[analysis omitted] int(ticks/1800*timemul)", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "mapping", VA: "", Offset: "mapping LOS mode", Type: "int", Default: "1 via registry", Clamp: "1 default per registry", Consumer: "[analysis omitted] MapFlags&1 full vs byte grid [P1-02 §2.1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "lineofsight", VA: "SingleLineOfSight", Offset: "LOS mode", Type: "int", Default: "1", Clamp: "—", Consumer: "visibility word vs ray [P1-02 §2.1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "LOSType", VA: "SingleLOSType", Offset: "LOS type enum", Type: "int", Default: "—", Clamp: "sprite vs ray", Consumer: "anims/vismasks.gaf vs ray", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "commanderDeath", VA: "SingleCommanderDeath", Offset: "commanderDeath", Type: "int", Default: "1 registry [analysis omitted]", Clamp: "—", Consumer: "CommanderKilled defeat trigger injection [P1-02 §2.1] TODO(question)", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorWeapon", VA: "", Offset: "", Type: "string", Default: "empty→disable", Clamp: "empty predicate", Consumer: "meteor scheduler + gamedata\\METEOR.TDF [Default] merge [P1-02 §2.1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorRadius", VA: "", Offset: "meteor", Type: "int", Default: "from METEOR.TDF [Default]", Clamp: "—", Consumer: "meteor scheduler", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorDensity", VA: "", Offset: "meteor", Type: "float", Default: "from METEOR.TDF", Clamp: "—", Consumer: "meteor scheduler", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorDuration", VA: "", Offset: "meteor", Type: "int", Default: "from METEOR.TDF", Clamp: "—", Consumer: "meteor scheduler", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "MeteorInterval", VA: "", Offset: "meteor", Type: "int", Default: "from METEOR.TDF", Clamp: "—", Consumer: "meteor scheduler", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	// Presentation — briefing/panorama only [P1-02 §2.1].
	{Key: "Planet", VA: "", Offset: "", Type: "string", Default: "empty 15-value enum Green…Crystal", Clamp: "enum via [analysis omitted] triple", Consumer: "[analysis omitted] planet→brief/pan/rotate [P1-02 §2.1]", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "brief", VA: "", Offset: "", Type: "string", Default: "empty", Clamp: "—", Consumer: "[analysis omitted] briefing text camps\\briefs", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "narration", VA: "", Offset: "narration", Type: "string", Default: "empty", Clamp: "—", Consumer: "camps\\briefs speech alias", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "missionhint", VA: "", Offset: "hint", Type: "string", Default: "empty", Clamp: "—", Consumer: "camps\\hints WAV [P1-02 §2.1]", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "glamour", VA: "", Offset: "glamour", Type: "string", Default: "empty", Clamp: "—", Consumer: "panorama/glam art", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "glamoursound", VA: "", Offset: "glamourSound", Type: "string", Default: "empty", Clamp: "—", Consumer: "audio alias", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	{Key: "missiondescription", VA: "", Offset: "", Type: "string", Default: "\"No description available\" [analysis omitted]", Clamp: "menu description", Consumer: "menu description", Class: GlobalPresentation, Fatal: FatalKindDegrade},
	// Inert — parsed but no tick reader [P1-02 §2.1] bounded negative.
	{Key: "memory", VA: "", Offset: "", Type: "string", Default: "empty", Clamp: "menu-only, no tick read", Consumer: "menu display only [P1-02 §2.1]", Class: GlobalInert, Fatal: FatalKindNotFatal},
	{Key: "nomovie", VA: "", Offset: "bool", Type: "int", Default: "0", Clamp: "suppresses briefing movie [analysis omitted]", Consumer: "presentation only [P1-02 §2.1]", Class: GlobalInert, Fatal: FatalKindNotFatal},
	// Timers/display — sibling deadlines [P1-02 §2.1] TODO(question) consumers.
	{Key: "UpdateTime", VA: "save key UpdateTime", Offset: "", Type: "int32 tick", Default: "seed globalTick via [analysis omitted]", Clamp: "absolute tick ADD 30", Consumer: "[analysis omitted] deadline gate CMP tick>=[layout omitted]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "WinLoseTime", VA: "save key WinLoseTime", Offset: "", Type: "int32", Default: "seed globalTick", Clamp: "—", Consumer: "[analysis omitted]/[analysis omitted] TODO(question) [P1-02 §2.1]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "DisplayTimer", VA: "save key DisplayTimer", Offset: "", Type: "int32", Default: "seed globalTick", Clamp: "—", Consumer: "scattered reads TODO(question)", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "GlobalTick", VA: "", Offset: "", Type: "int32", Default: "persisted blob", Clamp: "tick executor inc", Consumer: "globalTick before phase1 [P0-09]", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
	{Key: "EndCountdown", VA: "", Offset: "", Type: "i16/byte", Default: "-1/0 at [analysis omitted], reinit", Clamp: "not persisted", Consumer: "settlement gate + front-end latch", Class: GlobalAuthoritative, Fatal: FatalKindNotFatal},
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Green planet … Crystal plus Lunar special-case rewrite via 0x37EF2.
var PlanetNames = [15]string{
	"Green planet", "Red planet", "Lava", "Metal", "Ice",
	"Lush", "Archipelago", "Slate", "Lunar", "Water World",
	"Wet Desert", "Acid", "Crystal", "Desert", "Urban",
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
var PlanetBriefKeys = [15]string{
	"Greenbrief", "Redbrief", "Lavabrief", "Metalbrief", "Icebrief",
	"Lushbrief", "Archipelagobrief", "Slatebrief", "Lunarbrief", "Waterbrief",
	"WetDesertbrief", "Acidbrief", "Crystalbrief", "Desertbrief", "Urbanbrief",
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	HumanMetal         float64 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	HumanEnergy        float64 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	ComputerMetal      float64 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	ComputerEnergy     float64 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	SurfaceMetal       int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	MaxUnits           int32   // maxunits 250 clamp [P1-02 §2.1] vs per-def limit
	UseOnlyUnitsPath   string  // UseOnlyUnits → camps\useonly [P1-02 §2.1]
	AIProfile          string  // aiprofile fallback "default" [P1-02 §2.1]
	Planet             string  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Brief              string  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Narration          string  // presentation [P1-02 §2.1]
	MissionHint        string  // presentation [P1-02 §2.1]
	Glamour            string  // presentation [P1-02 §2.1]
	GlamourSound       string  // presentation [P1-02 §2.1]
	MinWind            int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	MaxWind            int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Gravity            int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TidalStrength      float64 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	LavaWorld          int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	NoSeaLevelTrigger  int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	WaterDoesDamage    int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	WaterDamage        int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	KillMul            float64 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TimeMul            float64 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Memory             string  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	NoMovie            int32   // nomovie inert [P1-02 §2.1]
	MissionDescription string  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Mapping            int32   // mapping LOS mode [P1-02 §2.1]
	LineOfSight        int32   // lineofsight [P1-02 §2.1]
	LOSType            int32   // LOSType enum [P1-02 §2.1]
	CommanderDeath     int32   // commanderDeath TODO(question) [P1-02 §8]
	MeteorWeapon       string  // MeteorWeapon empty disables [P1-02 §2.1]
	MeteorRadius       int32   // MeteorRadius [P1-02 §2.1]
	MeteorDensity      float64
	MeteorDuration     float64
	MeteorInterval     float64
	UpdateTime         int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	WinLoseTime        int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	DisplayTimer       int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	v, _ := global.StringValue("aiprofile", "default")   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
		mg.MissionDescription = "No description available" // TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	mg.UpdateTime = global.IntValue("UpdateTime", 0)     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	mg.WinLoseTime = global.IntValue("WinLoseTime", 0)   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	mg.DisplayTimer = global.IntValue("DisplayTimer", 0) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return mg
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P1-02 §2.1] or -1 when unknown → presentation leaves empty but game still loads
// degraded not fatal [P1-02 §2.2].
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
