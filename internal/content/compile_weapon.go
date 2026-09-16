// The weapon compiler.

package content

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// WeaponDef is a compiled weapon definition [02 "Weapon record"].
// DefinitionHeader must be the first field per catalog convention [02 §5].
type WeaponDef struct {
	DefinitionHeader
	// The definition-side active byte initially equals its catalog slot index.
	// A battle restore may replace it independently of identity; the explicit
	// state keeps hand-authored definitions initialized from ID as well
	// [02 §5] [08 R-SAVE-WEAPON-01].
	activeByte         uint8
	activeByteRestored bool
	// Identity [02 "Weapon record"] Record identity: ID read first with default -1
	// selects the record; section name becomes catalog key; name is display string.
	ID   int32  // ID integer default -1 [02 "Weapon record"]
	Name string // display name, 64-byte string [02 "Weapon record"]

	// Unit conversions [02 "Weapon record"] — each truncated after the multiply,
	// never composed differently. Durations <1/30 sec become 0.
	//
	// Retail additionally stores nine of these as 16-bit words, so the tick
	// count produced by the *30 (or *1/30) multiply is truncated a second
	// time to the field's storage width before it reaches the record — an
	// authored negative value does not survive as a large-magnitude 32-bit
	// negative [01 "Definition parsers"], [02 "Weapon record"]. The wrap
	// itself is Established for all nine — "a 16-bit store then wraps the
	// truncated value modulo 65,536" [06 §7.3] — and only the *extension*
	// each reader applies to the stored word separates the signed from the
	// unsigned form. All nine now name their reader's extension and are
	// wrapped below: the last three (BurstRate, Duration, SmokeDelay) were
	// traced in RWU-19-198 and are zero-extended by every reader
	// [06 R-WPN-05 §12]. Measured: over the 77-file, 198-section stock
	// weapon family every one of the nine compiles to a tick count inside
	// 0..32767, where all three forms agree, so no stock weapon can tell them
	// apart (WU-19-167 census).
	WeaponVelocity     int32   // weaponvelocity *65536/30 truncated [02 "Weapon record"]
	StartVelocity      int32   // startvelocity *65536/30 truncated [02 "Weapon record"]
	WeaponAcceleration int32   // weaponacceleration *65536/900 truncated [02 "Weapon record"]
	ReloadTime         int32   // reloadtime *30 truncated ticks, wrapped to a signed 16-bit store [06 §4.2]
	WeaponTimer        int32   // weapontimer *30 truncated, wrapped to an unsigned 16-bit store [06 §7.3]
	BurstRate          int32   // burstrate *30 truncated, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §6] [06 R-WPN-05 §12]
	Duration           int32   // duration *30 truncated, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §6] [06 R-WPN-05 §12]
	RandomDecay        int32   // randomdecay *30 truncated, wrapped to an unsigned 16-bit store [06 §4.3]
	SmokeDelay         int32   // smokedelay *30 truncated, wrapped to an unsigned 16-bit store [02 R-KEYS-01 §6] [06 R-WPN-05 §12]
	FlightTime         int32   // flighttime *30 truncated, wrapped to an unsigned 16-bit store [06 §6.6]
	HoldTime           int32   // holdtime *30 truncated, wrapped to a signed 16-bit store [07 "in-flight camera move"]
	ShakeDuration      int32   // shakeduration *30 truncated; established as a 32-bit store, no further truncation [02 R-KEYS-01 §2]
	TurnRate           int32   // turnrate *1/30 truncated per tick, wrapped to an unsigned 16-bit store [06 §6.7]
	MinBarrelAngle     float64 // minbarrelangle *pi/180 radians default -11.25 [02 "Weapon record"]

	// Remaining scalar fields [02 "Weapon record"]
	Range             int32   // range integer default 32767 [02 "Weapon record"]
	Coverage          int32   // coverage integer default 0 [02 "Weapon record"]
	AreaOfEffect      int32   // areaofeffect integer default 0 [02 "Weapon record"]
	EdgeEffectiveness float64 // edgeeffectiveness floating default 0 [02 "Weapon record"]
	EnergyPerShot     float64 // energypershot floating default 0 [02 "Weapon record"]
	MetalPerShot      float64 // metalpershot floating default 0 [02 "Weapon record"]
	Burst             int32   // burst integer default 0 [02 "Weapon record"]
	SprayAngle        int32   // sprayangle integer default 0 [02 "Weapon record"]
	Accuracy          int32   // accuracy integer default 0 inert [02 "Weapon record"], [06 §4.1]
	Tolerance         int32   // tolerance integer default 0 inert [02 "Weapon record"], [06 §4.1]
	PitchTolerance    int32   // pitchtolerance integer default 0 inert [02 "Weapon record"], [06 §4.1]
	ShakeMagnitude    int32   // shakemagnitude integer default 0 [02 "Weapon record"]
	Firestarter       int32   // firestarter integer default 0, truncated to the loader's low byte at compile time [02 "Weapon record"] [06 R-WPN-05 §10]
	RenderType        int32   // rendertype integer default 0 [02 "Weapon record"]
	Color             int32   // color integer default 0 [02 "Weapon record"]
	Color2            int32   // color2 integer default 0 [02 "Weapon record"]

	// Behavior flags [02 "Weapon record"] — integer accessor default 0 consumed as bool.
	NoAutoRange  bool // noautorange
	SoundTrigger bool // soundtrigger
	Guidance     bool // guidance
	Tracks       bool // tracks
	LineOfSight  bool // lineofsight
	Ballistic    bool // ballistic
	UnitsOnly    bool // unitsonly
	GroundBounce bool // groundbounce
	WaterWeapon  bool // waterweapon
	ToAirWeapon  bool // toairweapon
	SmokeTrail   bool // smoketrail
	Turret       bool // turret
	SelfProp     bool // selfprop
	Propeller    bool // propeller
	NoExplode    bool // noexplode
	BurnBlow     bool // burnblow
	TwoPhase     bool // twophase
	Cruise       bool // cruise
	CommandFire  bool // commandfire
	Stockpile    bool // stockpile
	Targetable   bool // targetable
	Interceptor  bool // interceptor
	BeamWeapon   bool // beamweapon
	ShellWeapon  bool // shellweapon
	Dropped      bool // dropped
	VLaunch      bool // vlaunch
	Meteor       bool // meteor
	NoRadar      bool // noradar
	Paralyzer    bool // paralyzer
	StartSmoke   bool // startsmoke
	EndSmoke     bool // endsmoke

	// Asset names [02 "Weapon record"] — string 256 default empty.
	Model             string // model
	ExplosionGaf      string // explosiongaf
	ExplosionArt      string // explosionart
	WaterExplosionGaf string // waterexplosiongaf
	WaterExplosionArt string // waterexplosionart
	LavaExplosionGaf  string // lavaexplosiongaf
	LavaExplosionArt  string // lavaexplosionart
	SoundStart        string // soundstart
	SoundHit          string // soundhit
	SoundWater        string // soundwater

	// Damage [02 "Weapon record"] Damage table: default fallback + per-armor map sorted.
	// Note [06 §9.2] lookup is case-insensitive binary search against UnitName — store strings; phase 9 owns lookup.
	DamageDefault int32            // DAMAGE.default integer default 0 [02 "Weapon record"]
	Damage        map[string]int32 // every other DAMAGE key => armor name -> damage, sorted map [02 "Weapon record"]
	// The override map retains spellings; this vector keeps the lower-bound
	// winner ahead of case variants across same-ID parses [06 R-DMG-01 §1].
	damageOrder []string

	// Unknown retains inert parsed keys so a later phase can consume them without re-parsing [02 §5] C14.
	// Keys are OriginalKey preserved case; e.g., aimrate, startfire, ovradjust etc have no reader.
	Unknown map[string]string
}

// DamageKeysSorted returns the DAMAGE unit-name keys sorted case-insensitively for deterministic iteration (I1).
// Compiled records preserve insertion order within a fold-equal run, which is
// observable to the runtime lower-bound lookup [06 R-DMG-01 §1]. Authored Go
// fixtures without that vector retain the deterministic lexical fallback.
func (w *WeaponDef) DamageKeysSorted() []string {
	if w.Damage == nil {
		return nil
	}
	if w.damageOrder != nil {
		return append([]string(nil), w.damageOrder...)
	}
	return sortedFoldedKeys(w.Damage, CanonicalKey)
}

// UnknownKeysSorted returns inert keys sorted for hash stability (I1).
func (w *WeaponDef) UnknownKeysSorted() []string {
	return sortedFoldedKeys(w.Unknown, CanonicalKey)
}

// knownWeaponKeys is the set of lower-cased weapon-level keys that have a typed reader [02 "Weapon record"].
// Anything not in this set and not the DAMAGE section is retained in Unknown (C14).
var knownWeaponKeys = map[string]struct{}{
	"id": {}, "name": {},
	"weaponvelocity": {}, "startvelocity": {}, "weaponacceleration": {},
	"reloadtime": {}, "weapontimer": {}, "burstrate": {}, "duration": {}, "randomdecay": {}, "smokedelay": {}, "flighttime": {}, "holdtime": {}, "shakeduration": {},
	"turnrate": {}, "minbarrelangle": {},
	"range": {}, "coverage": {}, "areaofeffect": {}, "edgeeffectiveness": {}, "energypershot": {}, "metalpershot": {}, "burst": {}, "sprayangle": {},
	"accuracy": {}, "tolerance": {}, "pitchtolerance": {}, "shakemagnitude": {}, "firestarter": {}, "rendertype": {}, "color": {}, "color2": {},
	"noautorange": {}, "soundtrigger": {}, "guidance": {}, "tracks": {}, "lineofsight": {}, "ballistic": {}, "unitsonly": {}, "groundbounce": {}, "waterweapon": {}, "toairweapon": {},
	"smoketrail": {}, "turret": {}, "selfprop": {}, "propeller": {}, "noexplode": {}, "burnblow": {}, "twophase": {}, "cruise": {}, "commandfire": {}, "stockpile": {},
	"targetable": {}, "interceptor": {}, "beamweapon": {}, "shellweapon": {}, "dropped": {}, "vlaunch": {}, "meteor": {}, "noradar": {}, "paralyzer": {}, "startsmoke": {}, "endsmoke": {},
	"model": {}, "explosiongaf": {}, "explosionart": {}, "waterexplosiongaf": {}, "waterexplosionart": {}, "lavaexplosiongaf": {}, "lavaexplosionart": {}, "soundstart": {}, "soundhit": {}, "soundwater": {},
}

// compileWeaponSectionWithPrior compiles a single top-level weapon section
// into a WeaponDef. It reads ID first with default -1 to select the record
// [02 "Weapon record"] C2, then applies conversions exactly as tabulated, each
// truncated after multiply [02 "Weapon record"] C3.
//
// A nil prior starts a fresh record. Re-parsing an existing ID passes that
// slot's record so its damage overrides survive: scalar fields replace
// authored-or-default on every parse, while DAMAGE overrides append to the
// existing table [02 R-CONTENT-02][06 R-DMG-01 §1].
func compileWeaponSectionWithPrior(section *formats.Section, sectionName string, prov Provenance, prior *WeaponDef) *WeaponDef {
	// C2 weapon identity: read ID first, default -1, use to select record; section name is catalog key; name is display string [02 "Weapon record"]
	id := section.IntValue("ID", -1)
	displayName, _ := section.StringValue("name", "")
	displayName = boundedString(displayName, 63)
	// Conversions exactly as tabulated, each truncated after multiply, never composed differently [02 "Weapon record"] C3
	// Velocities *65536/30 truncated — 16.16 per tick
	weaponVelocity := numeric.TruncateFloat64ToLow32(section.FloatValue("weaponvelocity", 0) * (65536.0 / 30.0))
	startVelocity := numeric.TruncateFloat64ToLow32(section.FloatValue("startvelocity", 0) * (65536.0 / 30.0))
	// Acceleration *65536/900 truncated — 16.16 per tick^2
	weaponAcceleration := numeric.TruncateFloat64ToLow32(section.FloatValue("weaponacceleration", 0) * (65536.0 / 900.0))
	// Durations *30 truncated — whole ticks; <1/30 becomes 0 [02 "Weapon record"].
	//
	// Nine of these keys are additionally 16-bit stores in retail
	// [01 "Definition parsers"], so the *30-truncated tick count is wrapped a
	// second time to the field's storage width: "a 16-bit store then wraps
	// the truncated value modulo 65,536" is Established for the family as a
	// whole [06 §7.3]. What is established per key is the *extension* the
	// reader applies to that stored word, and six keys name theirs — wrapped
	// here with `int32(int16(...))` (sign-extended back) or
	// `int32(uint16(...))` (zero-extended back).
	//
	// The remaining three (burstrate, duration, smokedelay) used to be left as
	// a plain *30 truncation because no reader extension was written up. It
	// now is: every reader of the three words zero-extends them and compares
	// unsigned — the burst scheduler's due test, its ">4" refresh gate and its
	// deadline advance, the beam latch, and the trail-smoke deadline
	// [06 R-WPN-05 §12] [02 R-KEYS-01 §6] — so they take the same unsigned wrap
	// as weapontimer. The three arms disagree only outside 0..32767, and a
	// census of the stock weapon family (77 files, 198 sections) finds every
	// one of the nine keys inside that range in every section, so nothing
	// shipped distinguishes them (WU-19-167); third-party content can.
	reloadTime := int32(int16(numeric.TruncateFloat64ToLow32(section.FloatValue("reloadtime", 0) * 30.0)))    // signed 16-bit store [06 §4.2]
	weaponTimer := int32(uint16(numeric.TruncateFloat64ToLow32(section.FloatValue("weapontimer", 0) * 30.0))) // unsigned 16-bit store [06 §7.3]
	burstRate := int32(uint16(numeric.TruncateFloat64ToLow32(section.FloatValue("burstrate", 0) * 30.0)))     // unsigned 16-bit store, zero-extended by all three burst-scheduler loads [06 R-WPN-05 §12]
	duration := int32(uint16(numeric.TruncateFloat64ToLow32(section.FloatValue("duration", 0) * 30.0)))       // unsigned 16-bit store, zero-extended by the beam latch [06 R-WPN-05 §12]
	randomDecay := int32(uint16(numeric.TruncateFloat64ToLow32(section.FloatValue("randomdecay", 0) * 30.0))) // unsigned 16-bit store [06 §4.3] ("an unsigned 16-bit shift")
	smokeDelay := int32(uint16(numeric.TruncateFloat64ToLow32(section.FloatValue("smokedelay", 0) * 30.0)))   // unsigned 16-bit store, zero-extended by the trail-smoke deadline [06 R-WPN-05 §12]
	flightTime := int32(uint16(numeric.TruncateFloat64ToLow32(section.FloatValue("flighttime", 0) * 30.0)))   // unsigned 16-bit store [06 §6.6] ("(uint16)flighttime")
	holdTime := int32(int16(numeric.TruncateFloat64ToLow32(section.FloatValue("holdtime", 0) * 30.0)))        // signed 16-bit store [07 "in-flight camera move"] ("the count is signed")
	shakeDuration := numeric.TruncateFloat64ToLow32(section.FloatValue("shakeduration", 0) * 30.0)            // established 32-bit store [02 R-KEYS-01 §2], no further truncation
	// turnrate *1/30 truncated per tick, wrapped to an unsigned 16-bit store [06 §6.7] ("zero-extended from its 16-bit store")
	turnRate := int32(uint16(numeric.TruncateFloat64ToLow32(section.FloatValue("turnrate", 0) * (1.0 / 30.0))))
	// minbarrelangle *pi/180 radians default -11.25 — composed exactly as
	// tabulated, value times the pi/180 constant [02 "Weapon record"] C3.
	minBarrelAngle := section.FloatValue("minbarrelangle", -11.25) * (math.Pi / 180.0)

	// Remaining scalars [02 "Weapon record"]
	rng := section.IntValue("range", 32767)
	coverage := section.IntValue("coverage", 0)
	areaOfEffect := section.IntValue("areaofeffect", 0)
	edgeEffectiveness := section.FloatValue("edgeeffectiveness", 0)
	energyPerShot := section.FloatValue("energypershot", 0)
	metalPerShot := section.FloatValue("metalpershot", 0)
	burst := section.IntValue("burst", 0)
	sprayAngle := section.IntValue("sprayangle", 0)
	accuracy := section.IntValue("accuracy", 0)
	tolerance := section.IntValue("tolerance", 0)
	pitchTolerance := section.IntValue("pitchtolerance", 0)
	shakeMagnitude := section.IntValue("shakemagnitude", 0)
	// firestarter is stored as the loader's low byte, not the full authored
	// integer: the loader stores only the low 8 bits of the authored value
	// ([02 "Weapon record"] lists the field as 8-bit), and the sole reader is
	// a byte-width nonzero test in the feature-damage helper of the area
	// sweep's feature phase [06 R-WPN-05 §10]. Truncating here means every
	// reader of WeaponDef.Firestarter — combat's feature-ignite call included
	// — already sees the byte retail would test, with no truncation left for
	// callers to remember. The field keeps its int32 type to avoid churn.
	fireStarter := int32(uint8(section.IntValue("firestarter", 0)))
	renderType := section.IntValue("rendertype", 0)
	color := section.IntValue("color", 0)
	color2 := section.IntValue("color2", 0)

	// Behavior flags — integer accessor default 0 consumed as bool [02 "Weapon record"]
	noAutoRange := storedFlag(section, "noautorange", false)
	soundTrigger := storedFlag(section, "soundtrigger", false)
	guidance := storedFlag(section, "guidance", false)
	tracks := storedFlag(section, "tracks", false)
	lineOfSight := storedFlag(section, "lineofsight", false)
	ballistic := storedFlag(section, "ballistic", false)
	unitsOnly := storedFlag(section, "unitsonly", false)
	groundBounce := storedFlag(section, "groundbounce", false)
	waterWeapon := storedFlag(section, "waterweapon", false)
	toAirWeapon := storedFlag(section, "toairweapon", false)
	smokeTrail := storedFlag(section, "smoketrail", false)
	turret := storedFlag(section, "turret", false)
	selfProp := storedFlag(section, "selfprop", false)
	propeller := storedFlag(section, "propeller", false)
	noExplode := storedFlag(section, "noexplode", false)
	burnBlow := storedFlag(section, "burnblow", false)
	twoPhase := storedFlag(section, "twophase", false)
	cruise := storedFlag(section, "cruise", false)
	commandFire := storedFlag(section, "commandfire", false)
	stockpile := storedFlag(section, "stockpile", false)
	targetable := storedFlag(section, "targetable", false)
	interceptor := storedFlag(section, "interceptor", false)
	beamWeapon := storedFlag(section, "beamweapon", false)
	shellWeapon := storedFlag(section, "shellweapon", false)
	dropped := storedFlag(section, "dropped", false)
	vLaunch := storedFlag(section, "vlaunch", false)
	meteor := storedFlag(section, "meteor", false)
	noRadar := storedFlag(section, "noradar", false)
	paralyzer := storedFlag(section, "paralyzer", false)
	startSmoke := storedFlag(section, "startsmoke", false)
	endSmoke := storedFlag(section, "endsmoke", false)

	// Asset names — string 256 default empty [02 "Weapon record"]
	model, _ := section.StringValue("model", "")
	explosionGaf, _ := section.StringValue("explosiongaf", "")
	explosionArt, _ := section.StringValue("explosionart", "")
	waterExplosionGaf, _ := section.StringValue("waterexplosiongaf", "")
	waterExplosionArt, _ := section.StringValue("waterexplosionart", "")
	lavaExplosionGaf, _ := section.StringValue("lavaexplosiongaf", "")
	lavaExplosionArt, _ := section.StringValue("lavaexplosionart", "")
	soundStart, _ := section.StringValue("soundstart", "")
	soundHit, _ := section.StringValue("soundhit", "")
	soundWater, _ := section.StringValue("soundwater", "")
	model = boundedString(model, 255)
	explosionGaf = boundedString(explosionGaf, 255)
	explosionArt = boundedString(explosionArt, 255)
	waterExplosionGaf = boundedString(waterExplosionGaf, 255)
	waterExplosionArt = boundedString(waterExplosionArt, 255)
	lavaExplosionGaf = boundedString(lavaExplosionGaf, 255)
	lavaExplosionArt = boundedString(lavaExplosionArt, 255)
	soundStart = boundedString(soundStart, 255)
	soundHit = boundedString(soundHit, 255)
	soundWater = boundedString(soundWater, 255)

	// Damage table [02 "Weapon record"] C4
	// Its default key is read with integer accessor default 0 and becomes fallback.
	// Every other key names a unit definition (case preserved) and its damage,
	// interned into per-weapon sorted map. No DAMAGE section => fallback 0.
	damageDefault := int32(0)
	damageMap := make(map[string]int32)
	var damageOrder []string
	if prior != nil {
		damageOrder = prior.DamageKeysSorted()
		for _, key := range damageOrder {
			damageMap[key] = prior.Damage[key]
		}
	}
	if dmgSection := section.Section("DAMAGE"); dmgSection != nil {
		damageDefault = dmgSection.IntValue("default", 0)
		for _, item := range dmgSection.ResolvedAssignments() {
			if CanonicalKey(item.Key) == "default" {
				continue
			}
			// Use typed accessor for integer conversion [02 §4]; enumeration preserves case.
			val := dmgSection.IntValue(item.Key, 0)
			// Insertion is at the case-insensitive lower bound. A repeated
			// spelling needs only its newest entry in this map projection; the
			// retained order reproduces the first matching lookup [06 R-DMG-01 §1].
			key := item.OriginalKey
			for i, old := range damageOrder {
				if old == key {
					damageOrder = append(damageOrder[:i], damageOrder[i+1:]...)
					break
				}
			}
			lo := sort.Search(len(damageOrder), func(i int) bool { return CanonicalKey(damageOrder[i]) >= CanonicalKey(key) })
			damageOrder = append(damageOrder, "")
			copy(damageOrder[lo+1:], damageOrder[lo:])
			damageOrder[lo] = key
			damageMap[key] = val
		}
	}

	// Unknown inert keys retained per C14 [02 §5]
	unknown := make(map[string]string)
	for _, item := range section.Items {
		if item.Kind != formats.Assignment {
			continue
		}
		fold := CanonicalKey(item.Key)
		if _, ok := knownWeaponKeys[fold]; ok {
			continue
		}
		// DAMAGE is a nested section, not a key — skip; already handled
		unknown[item.OriginalKey] = item.Value
	}
	if len(unknown) == 0 {
		unknown = nil
	}
	if len(damageMap) == 0 {
		// Keep empty map as nil for clarity, but hash must still be stable; nil vs empty treated same.
		// Preserve nil to avoid empty map iteration differences.
		damageMap = nil
	}

	wd := &WeaponDef{
		DefinitionHeader: DefinitionHeader{
			CanonicalKey: CanonicalKey(sectionName),
			Provenance:   prov,
		},
		ID:                 id,
		Name:               displayName,
		WeaponVelocity:     weaponVelocity,
		StartVelocity:      startVelocity,
		WeaponAcceleration: weaponAcceleration,
		ReloadTime:         reloadTime,
		WeaponTimer:        weaponTimer,
		BurstRate:          burstRate,
		Duration:           duration,
		RandomDecay:        randomDecay,
		SmokeDelay:         smokeDelay,
		FlightTime:         flightTime,
		HoldTime:           holdTime,
		ShakeDuration:      shakeDuration,
		TurnRate:           turnRate,
		MinBarrelAngle:     minBarrelAngle,
		Range:              rng,
		Coverage:           coverage,
		AreaOfEffect:       areaOfEffect,
		EdgeEffectiveness:  edgeEffectiveness,
		EnergyPerShot:      energyPerShot,
		MetalPerShot:       metalPerShot,
		Burst:              burst,
		SprayAngle:         sprayAngle,
		Accuracy:           accuracy,
		Tolerance:          tolerance,
		PitchTolerance:     pitchTolerance,
		ShakeMagnitude:     shakeMagnitude,
		Firestarter:        fireStarter,
		RenderType:         renderType,
		Color:              color,
		Color2:             color2,
		NoAutoRange:        noAutoRange,
		SoundTrigger:       soundTrigger,
		Guidance:           guidance,
		Tracks:             tracks,
		LineOfSight:        lineOfSight,
		Ballistic:          ballistic,
		UnitsOnly:          unitsOnly,
		GroundBounce:       groundBounce,
		WaterWeapon:        waterWeapon,
		ToAirWeapon:        toAirWeapon,
		SmokeTrail:         smokeTrail,
		Turret:             turret,
		SelfProp:           selfProp,
		Propeller:          propeller,
		NoExplode:          noExplode,
		BurnBlow:           burnBlow,
		TwoPhase:           twoPhase,
		Cruise:             cruise,
		CommandFire:        commandFire,
		Stockpile:          stockpile,
		Targetable:         targetable,
		Interceptor:        interceptor,
		BeamWeapon:         beamWeapon,
		ShellWeapon:        shellWeapon,
		Dropped:            dropped,
		VLaunch:            vLaunch,
		Meteor:             meteor,
		NoRadar:            noRadar,
		Paralyzer:          paralyzer,
		StartSmoke:         startSmoke,
		EndSmoke:           endSmoke,
		Model:              model,
		ExplosionGaf:       explosionGaf,
		ExplosionArt:       explosionArt,
		WaterExplosionGaf:  waterExplosionGaf,
		WaterExplosionArt:  waterExplosionArt,
		LavaExplosionGaf:   lavaExplosionGaf,
		LavaExplosionArt:   lavaExplosionArt,
		SoundStart:         soundStart,
		SoundHit:           soundHit,
		SoundWater:         soundWater,
		DamageDefault:      damageDefault,
		Damage:             damageMap,
		damageOrder:        damageOrder,
		Unknown:            unknown,
	}

	// Hash over canonical bytes including defaults, independent of map iteration (I1) [02 §5] C12.
	// Never range a map directly — sort keys.
	var b strings.Builder
	// Fixed order canonical representation (split to avoid format/arg mismatch).
	fmt.Fprintf(&b, "%s|%d|%s|", wd.CanonicalKey, wd.ID, wd.Name)
	fmt.Fprintf(&b, "%d|%d|%d|", wd.WeaponVelocity, wd.StartVelocity, wd.WeaponAcceleration)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|%d|%d|%d|", wd.ReloadTime, wd.WeaponTimer, wd.BurstRate, wd.Duration, wd.RandomDecay, wd.SmokeDelay, wd.FlightTime, wd.HoldTime, wd.ShakeDuration)
	fmt.Fprintf(&b, "%d|%.10f|", wd.TurnRate, wd.MinBarrelAngle)
	fmt.Fprintf(&b, "%d|%d|%d|%.10f|%.10f|%.10f|", wd.Range, wd.Coverage, wd.AreaOfEffect, wd.EdgeEffectiveness, wd.EnergyPerShot, wd.MetalPerShot)
	fmt.Fprintf(&b, "%d|%d|%d|%d|%d|%d|%d|%d|%d|%d|", wd.Burst, wd.SprayAngle, wd.Accuracy, wd.Tolerance, wd.PitchTolerance, wd.ShakeMagnitude, wd.Firestarter, wd.RenderType, wd.Color, wd.Color2)
	// Flags as 0/1 in fixed order
	flags := []bool{wd.NoAutoRange, wd.SoundTrigger, wd.Guidance, wd.Tracks, wd.LineOfSight, wd.Ballistic, wd.UnitsOnly, wd.GroundBounce, wd.WaterWeapon, wd.ToAirWeapon, wd.SmokeTrail, wd.Turret, wd.SelfProp, wd.Propeller, wd.NoExplode, wd.BurnBlow, wd.TwoPhase, wd.Cruise, wd.CommandFire, wd.Stockpile, wd.Targetable, wd.Interceptor, wd.BeamWeapon, wd.ShellWeapon, wd.Dropped, wd.VLaunch, wd.Meteor, wd.NoRadar, wd.Paralyzer, wd.StartSmoke, wd.EndSmoke}
	for _, f := range flags {
		if f {
			b.WriteString("1|")
		} else {
			b.WriteString("0|")
		}
	}
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|", wd.Model, wd.ExplosionGaf, wd.ExplosionArt, wd.WaterExplosionGaf, wd.WaterExplosionArt, wd.LavaExplosionGaf, wd.LavaExplosionArt, wd.SoundStart, wd.SoundHit, wd.SoundWater)
	fmt.Fprintf(&b, "dmgDefault:%d|", wd.DamageDefault)
	for _, k := range wd.DamageKeysSorted() {
		fmt.Fprintf(&b, "%s=%d|", k, wd.Damage[k])
	}
	b.WriteString("unknown|")
	for _, k := range wd.UnknownKeysSorted() {
		fmt.Fprintf(&b, "%s=%s|", k, wd.Unknown[k])
	}
	wd.Hash = HashDefinition([]byte(b.String()))
	return wd
}

// CompileWeaponsWithDuplicates compiles weapons from the VFS and returns the
// collision diagnostics alongside the catalog. The weapon family is exactly
// Weapons/*.tdf — retail never parses gamedata/weapons.tdf, which is inert
// [02 §5 R-CONTENT-02] — one weapon per top-level section across all files.
// It returns a map keyed by CanonicalKey(section name) [02 §5] and uses only
// typed accessors from formats/tdf_typed.go [02 §4].
//
// Record identity is by ID [02 "Weapon record"]: the parser reads ID first
// with default -1 and uses it to select the record it fills, then stores the
// section name as the record's catalog name. Measured stock: the parsed
// family has unique IDs — ID 36 is [cormine2] (weapons/cormine2_weapon.tdf)
// alone and [earthquake] is weapons/earthquake.tdf at ID 227
// [02 §5 R-CONTENT-02]. Determinism comes from deterministic discovery order
// (I1).
//
// Discovery is the union enumeration of Weapons/*.tdf:
// provider mount precedence first, then host directory order within one
// provider — which Nanolathe realizes as first-provider-wins dedup in mount
// order followed by sorted logical paths, the documented lexical divergence
// [SPEC_CONFLICTS SC3] — each file's sections in document order. Every
// top-level section feeds the record parser in that order [02 §5 R-CONTENT-02].
//
// A later section with an already-seen ID replaces the earlier record whole,
// catalog name included (the parser stores authored-or-default for every
// field and copies the section name over the catalog name) [02 §5
// R-CONTENT-02]. Sections without an authored ID (default -1) are inert: they
// all write into the one scratch slot just before record 0, the last one wins
// it, and the runtime name scan never reaches that slot — they appear only in
// diagnostics here [02 §5 R-CONTENT-02].
func CompileWeaponsWithDuplicates(fs vfs.FSOps) (map[string]*WeaponDef, []WeaponDuplicate, error) {
	if fs == nil {
		return nil, nil, fmt.Errorf("content: nil VFS")
	}
	// Record table substitute: slot (ID) -> record, replacing scalars while
	// preserving each slot's accumulated damage overrides [02 R-CONTENT-02].
	slots := make(map[int32]*WeaponDef)
	// keysPerID preserves discovery order per ID for the collision diagnostics.
	keysPerID := make(map[int32][]string)
	// ID-less sections share the unreachable scratch slot before record 0;
	// their names never enter the catalog [02 §5 R-CONTENT-02].
	var scratchKeys []string

	processFile := func(data []byte, path string, prov Provenance) error {
		doc, err := formats.ParseTDF(data)
		if err != nil {
			return formats.WithTDFContext(fs, err, path)
		}
		for _, section := range doc.Root.Sections() {
			name := trimTDFSemantic(section.OriginalName)
			if name == "" {
				continue
			}
			wd := compileWeaponSectionWithPrior(section, name, prov, slots[section.IntValue("ID", -1)])
			if wd.ID >= 0 {
				keysPerID[wd.ID] = append(keysPerID[wd.ID], wd.CanonicalKey)
				// The later section owns the scalar fields and catalog name;
				// its DAMAGE table includes retained overrides [02 R-CONTENT-02].
				slots[wd.ID] = wd
			} else {
				scratchKeys = append(scratchKeys, wd.CanonicalKey)
			}
		}
		return nil
	}

	entries, err := discoverArchiveContent(fs, "weapons", ".tdf")
	if err != nil {
		return nil, nil, fmt.Errorf("content: weapons: %w", err)
	}
	// ReadDir resolves duplicate logical paths first-provider-wins in mount
	// order and then sorts by Path [vfs.ReadDir] — provider mount precedence,
	// then host directory order with the lexical divergence [SPEC_CONFLICTS
	// SC3]. This iteration is the discovery order (I1).
	for _, entry := range entries {
		e := entry.info
		data, err := readContentEntry(fs, entry)
		if err != nil {
			return nil, nil, err
		}
		prov := ProvenanceFrom(e)
		if err := processFile(data, e.Path, prov); err != nil {
			return nil, nil, err
		}
	}

	// Collision diagnostics: every name that shared a slot, in discovery
	// order, the surviving record's name last. ID -1 is the shared scratch
	// slot of the ID-less sections — its "winner" wins a slot name lookup can
	// never reach [02 §5 R-CONTENT-02].
	var duplicates []WeaponDuplicate
	for id, keys := range keysPerID {
		if len(keys) <= 1 {
			continue
		}
		duplicates = append(duplicates, WeaponDuplicate{ID: id, Keys: append([]string(nil), keys...), Winner: keys[len(keys)-1]})
	}
	if len(scratchKeys) > 1 {
		duplicates = append(duplicates, WeaponDuplicate{ID: -1, Keys: append([]string(nil), scratchKeys...), Winner: scratchKeys[len(scratchKeys)-1]})
	}
	sort.Slice(duplicates, func(i, j int) bool { return duplicates[i].ID < duplicates[j].ID })

	// The name-keyed catalog is the runtime name resolution itself: walk the
	// record table from slot 0 upward and keep the first record per catalog
	// name — exactly the first match the case-insensitive linear scan returns
	// [02 §5 R-CONTENT-02]. Slots are visited in ascending ID order (I1).
	// Representation limit: two distinct IDs sharing one section name leave
	// the higher-ID record unreachable through this name-keyed map. Retail's
	// table holds both records and its name scan returns the lower slot; the
	// resolution here matches, but no name-keyed view can also address the
	// shadowed record by ID. No stock instance is known (IDs are unique; a
	// name shared across IDs is unmeasured).
	ids := make([]int32, 0, len(slots))
	for id := range slots {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := make(map[string]*WeaponDef, len(ids))
	for _, id := range ids {
		wd := slots[id]
		if _, exists := result[wd.CanonicalKey]; !exists {
			result[wd.CanonicalKey] = wd
		}
	}
	return result, duplicates, nil
}

// WeaponByID selects the record occupying the given slot. A compiled map has
// exactly one record per nonnegative ID — same-ID sections replaced each other
// whole, and ID-less sections never enter the map [02 §5 R-CONTENT-02] — so
// the sole match is returned. For a hand-built map that still carries
// colliding IDs, the last match in sorted key order stands in for
// discovery-order last-wins, matching buildWeaponIndex's fallback (I1).
func WeaponByID(weapons map[string]*WeaponDef, id int32) (*WeaponDef, bool) {
	if weapons == nil {
		return nil, false
	}
	// Deterministic iteration: sorted keys (I1).
	keys := make([]string, 0, len(weapons))
	for k := range weapons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var found *WeaponDef
	var ok bool
	for _, k := range keys {
		if weapons[k].ID == id {
			found = weapons[k]
			ok = true
		}
	}
	return found, ok
}

// ActiveByte returns the definition-side byte used as the active gate and
// copied by retail saves. ID continues to select catalog identity after load
// [08 R-SAVE-WEAPON-01]. A nil definition represents an absent inactive link.
func (w *WeaponDef) ActiveByte() uint8 {
	if w == nil {
		return 0
	}
	if w.activeByteRestored {
		return w.activeByte
	}
	return uint8(w.ID)
}

// RestoreActiveByte applies a saved definition byte to a battle-local copy.
// Callers must isolate the catalog first; this does not change weapon identity
// or the independently persisted slot-enabled bit [08 R-SAVE-WEAPON-01].
func (w *WeaponDef) RestoreActiveByte(value uint8) {
	if w != nil {
		w.activeByte = value
		w.activeByteRestored = true
	}
}
