// Package content compiles retail's authored data into immutable definitions.
// This file implements the weapon class compiler [02 "Weapon record"].
package content

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// WeaponDef is a compiled weapon definition [02 "Weapon record"].
// DefinitionHeader must be the first field per catalog convention [02 §5].
type WeaponDef struct {
	DefinitionHeader
	// Identity [02 "Weapon record"] Record identity: ID read first with default -1
	// selects the record; section name becomes catalog key; name is display string.
	ID   int32  // ID integer default -1 [02 "Weapon record"]
	Name string // display name, 64-byte string [02 "Weapon record"]

	// Unit conversions [02 "Weapon record"] — each truncated after the multiply,
	// never composed differently. Durations <1/30 sec become 0.
	WeaponVelocity     int32   // weaponvelocity *65536/30 truncated [02 "Weapon record"]
	StartVelocity      int32   // startvelocity *65536/30 truncated [02 "Weapon record"]
	WeaponAcceleration int32   // weaponacceleration *65536/900 truncated [02 "Weapon record"]
	ReloadTime         int32   // reloadtime *30 truncated ticks [02 "Weapon record"]
	WeaponTimer        int32   // weapontimer *30 truncated [02 "Weapon record"]
	BurstRate          int32   // burstrate *30 truncated [02 "Weapon record"]
	Duration           int32   // duration *30 truncated [02 "Weapon record"]
	RandomDecay        int32   // randomdecay *30 truncated [02 "Weapon record"]
	SmokeDelay         int32   // smokedelay *30 truncated [02 "Weapon record"]
	FlightTime         int32   // flighttime *30 truncated [02 "Weapon record"]
	HoldTime           int32   // holdtime *30 truncated [02 "Weapon record"]
	ShakeDuration      int32   // shakeduration *30 truncated [02 "Weapon record"]
	TurnRate           int32   // turnrate *1/30 truncated per tick [02 "Weapon record"]
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
	Firestarter       int32   // firestarter integer default 0 [02 "Weapon record"]
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

	// Unknown retains inert parsed keys so a later phase can consume them without re-parsing [02 §5] C14.
	// Keys are OriginalKey preserved case; e.g., aimrate, startfire, ovradjust etc have no reader.
	Unknown map[string]string
}

// DamageKeysSorted returns the DAMAGE armor keys sorted case-insensitively for deterministic iteration (I1).
// The map itself is unordered; this helper provides the sorted view for hashing and tests [02 "Weapon record"].
func (w *WeaponDef) DamageKeysSorted() []string {
	if w.Damage == nil {
		return nil
	}
	keys := make([]string, 0, len(w.Damage))
	for k := range w.Damage {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		li, lj := strings.ToLower(keys[i]), strings.ToLower(keys[j])
		if li != lj {
			return li < lj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// UnknownKeysSorted returns inert keys sorted for hash stability (I1).
func (w *WeaponDef) UnknownKeysSorted() []string {
	if w.Unknown == nil {
		return nil
	}
	keys := make([]string, 0, len(w.Unknown))
	for k := range w.Unknown {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		li, lj := strings.ToLower(keys[i]), strings.ToLower(keys[j])
		if li != lj {
			return li < lj
		}
		return keys[i] < keys[j]
	})
	return keys
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

// compileWeaponSection compiles a single top-level weapon section into a WeaponDef.
// It reads ID first with default -1 to select the record [02 "Weapon record"] C2, then
// applies conversions exactly as tabulated each truncated after multiply [02 "Weapon record"] C3.
func compileWeaponSection(section *formats.Section, sectionName string, prov Provenance) *WeaponDef {
	// C2 weapon identity: read ID first, default -1, use to select record; section name is catalog key; name is display string [02 "Weapon record"]
	id := section.IntValue("ID", -1)
	displayName, _ := section.StringValue("name", "")
	// Conversions exactly as tabulated, each truncated after multiply, never composed differently [02 "Weapon record"] C3
	// Velocities *65536/30 truncated — 16.16 per tick
	weaponVelocity := int32(section.FloatValue("weaponvelocity", 0) * 65536.0 / 30.0)
	startVelocity := int32(section.FloatValue("startvelocity", 0) * 65536.0 / 30.0)
	// Acceleration *65536/900 truncated — 16.16 per tick^2
	weaponAcceleration := int32(section.FloatValue("weaponacceleration", 0) * 65536.0 / 900.0)
	// Durations *30 truncated — whole ticks; <1/30 becomes 0 [02 "Weapon record"]
	reloadTime := int32(section.FloatValue("reloadtime", 0) * 30.0)
	weaponTimer := int32(section.FloatValue("weapontimer", 0) * 30.0)
	burstRate := int32(section.FloatValue("burstrate", 0) * 30.0)
	duration := int32(section.FloatValue("duration", 0) * 30.0)
	randomDecay := int32(section.FloatValue("randomdecay", 0) * 30.0)
	smokeDelay := int32(section.FloatValue("smokedelay", 0) * 30.0)
	flightTime := int32(section.FloatValue("flighttime", 0) * 30.0)
	holdTime := int32(section.FloatValue("holdtime", 0) * 30.0)
	shakeDuration := int32(section.FloatValue("shakeduration", 0) * 30.0)
	// turnrate *1/30 truncated per tick
	turnRate := int32(section.FloatValue("turnrate", 0) * (1.0 / 30.0))
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
	fireStarter := section.IntValue("firestarter", 0)
	renderType := section.IntValue("rendertype", 0)
	color := section.IntValue("color", 0)
	color2 := section.IntValue("color2", 0)

	// Behavior flags — integer accessor default 0 consumed as bool [02 "Weapon record"]
	noAutoRange := section.BoolValue("noautorange", false)
	soundTrigger := section.BoolValue("soundtrigger", false)
	guidance := section.BoolValue("guidance", false)
	tracks := section.BoolValue("tracks", false)
	lineOfSight := section.BoolValue("lineofsight", false)
	ballistic := section.BoolValue("ballistic", false)
	unitsOnly := section.BoolValue("unitsonly", false)
	groundBounce := section.BoolValue("groundbounce", false)
	waterWeapon := section.BoolValue("waterweapon", false)
	toAirWeapon := section.BoolValue("toairweapon", false)
	smokeTrail := section.BoolValue("smoketrail", false)
	turret := section.BoolValue("turret", false)
	selfProp := section.BoolValue("selfprop", false)
	propeller := section.BoolValue("propeller", false)
	noExplode := section.BoolValue("noexplode", false)
	burnBlow := section.BoolValue("burnblow", false)
	twoPhase := section.BoolValue("twophase", false)
	cruise := section.BoolValue("cruise", false)
	commandFire := section.BoolValue("commandfire", false)
	stockpile := section.BoolValue("stockpile", false)
	targetable := section.BoolValue("targetable", false)
	interceptor := section.BoolValue("interceptor", false)
	beamWeapon := section.BoolValue("beamweapon", false)
	shellWeapon := section.BoolValue("shellweapon", false)
	dropped := section.BoolValue("dropped", false)
	vLaunch := section.BoolValue("vlaunch", false)
	meteor := section.BoolValue("meteor", false)
	noRadar := section.BoolValue("noradar", false)
	paralyzer := section.BoolValue("paralyzer", false)
	startSmoke := section.BoolValue("startsmoke", false)
	endSmoke := section.BoolValue("endsmoke", false)

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

	// Damage table [02 "Weapon record"] C4
	// Its default key is read with integer accessor default 0 and becomes fallback.
	// Every other key enumerated: key is armor-class name (case preserved) and value is damage,
	// interned into per-weapon sorted map. No DAMAGE section => fallback 0.
	damageDefault := int32(0)
	damageMap := make(map[string]int32)
	if dmgSection := section.Section("DAMAGE"); dmgSection != nil {
		damageDefault = dmgSection.IntValue("default", 0)
		for _, item := range dmgSection.Items {
			if item.Kind != formats.Assignment {
				continue
			}
			if strings.EqualFold(item.Key, "default") {
				continue
			}
			// Use typed accessor for integer conversion [02 §4]; enumeration preserves case.
			val := dmgSection.IntValue(item.Key, 0)
			// Case preserved, sorted later [02 "Weapon record"] C4
			damageMap[item.OriginalKey] = val
		}
	}

	// Unknown inert keys retained per C14 [02 §5]
	unknown := make(map[string]string)
	for _, item := range section.Items {
		if item.Kind != formats.Assignment {
			continue
		}
		fold := strings.ToLower(item.Key)
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

// CompileWeapons compiles weapons from the VFS. Discovery is weapons/*.tdf (77) AND gamedata/weapons.tdf
// — one weapon per top-level section across all files [PLAN 02 Discovery]. It returns a map keyed by
// CanonicalKey(section name) [02 §5]. The helper uses only typed accessors from formats/tdf_typed.go [02 §4].
//
// Record identity is by ID [02 "Weapon record"]: each section fills the record
// selected by its authored ID, so two sections sharing an ID produce ONE
// record whose catalog name is the later section's. Measured on the reference
// install: ID 36 is shared by EARTHQUAKE (gamedata/weapons.tdf) and cormine2
// (weapons/cormine2_weapon.tdf); every unit link references CORMINE2 and none
// references EARTHQUAKE.
//
// Determinism ON-04: winner for duplicate IDs is the smallest canonical key
// (lexicographically, I1), not last-wins-by-canonical-order nor file order.
// Duplicates are recorded for diagnostics via CompileWeaponsWithDuplicates.
// TODO(question) R-P0-01 duplicate winner policy: smallest canonical key wins is a supported inference
// from [02 §5] deterministic catalog requirement (I1); last-wins would be nondeterministic
// under map iteration and is NOT acceptable per ON-04.
func CompileWeapons(fs vfs.FSOps) (map[string]*WeaponDef, error) {
	m, _, err := CompileWeaponsWithDuplicates(fs)
	return m, err
}

// CompileWeaponsWithDuplicates is the stable implementation that also returns duplicate diagnostics ON-04.
// It collects all weapon sections first, then selects winners deterministically as smallest canonical key
// per ID, recording all colliding keys for diagnostics.
func CompileWeaponsWithDuplicates(fs vfs.FSOps) (map[string]*WeaponDef, []WeaponDuplicate, error) {
	if fs == nil {
		return nil, nil, fmt.Errorf("content: nil VFS")
	}
	var allDefs []*WeaponDef

	processFileCollect := func(data []byte, prov Provenance) error {
		doc, err := formats.ParseTDF(data)
		if err != nil {
			return err
		}
		for _, section := range doc.Root.Sections() {
			name := strings.TrimSpace(section.OriginalName)
			if name == "" {
				continue
			}
			wd := compileWeaponSection(section, name, prov)
			allDefs = append(allDefs, wd)
		}
		return nil
	}

	if data, err := fs.ReadFileLimit("gamedata/weapons.tdf", 1<<20); err == nil {
		prov := Provenance{LogicalPath: "gamedata/weapons.tdf"}
		if info, statErr := fs.Stat("gamedata/weapons.tdf"); statErr == nil {
			prov = ProvenanceFrom(info)
		}
		if err := processFileCollect(data, prov); err != nil {
			return nil, nil, fmt.Errorf("content: gamedata/weapons.tdf: %w", err)
		}
	}

	entries, err := fs.ReadDir("weapons")
	if err != nil {
		// If gamedata already contributed weapons, allow missing weapons dir as empty.
		if len(allDefs) == 0 {
			return nil, nil, fmt.Errorf("content: weapons: %w", err)
		}
	} else {
		// ReadDir already sorts by Path [vfs.ReadDir], so iteration is stable (I1).
		for _, e := range entries {
			if e.IsDir {
				continue
			}
			// Filter by extension — directory also holds .bat, .pl, .txt, .xls junk [PLAN Discovery]
			if !strings.HasSuffix(strings.ToLower(e.Path), ".tdf") {
				continue
			}
			data, err := fs.ReadFileLimit(e.Path, 1<<20)
			if err != nil {
				continue
			}
			prov := Provenance{
				LogicalPath: e.Path,
				ProviderID:  e.Source.SourcePath,
				MountOrder:  e.Source.MountOrder,
			}
			// Fallback to Stat provenance if ReadDir entry lacks it (should not happen).
			if prov.ProviderID == "" {
				if info, serr := fs.Stat(e.Path); serr == nil {
					prov = ProvenanceFrom(info)
				}
			}
			if err := processFileCollect(data, prov); err != nil {
				return nil, nil, fmt.Errorf("content: %s: %w", e.Path, err)
			}
		}
	}
	// Deterministic winner selection: smallest canonical key wins per ID ON-04.
	// Group by ID, keep smallest key as winner, record all colliding keys for diagnostics.
	// ID < 0 (no ID) stays distinct per TODO(question) in processFileCollect comment.
	sort.SliceStable(allDefs, func(i, j int) bool { return allDefs[i].CanonicalKey < allDefs[j].CanonicalKey })
	result := make(map[string]*WeaponDef, len(allDefs))
	byID := make(map[int32]*WeaponDef)
	// dupKeys tracks all keys per ID in sorted winner-first order for diagnostics
	dupKeys := make(map[int32][]string)
	for _, wd := range allDefs {
		if wd.ID >= 0 {
			if _, exists := byID[wd.ID]; !exists {
				byID[wd.ID] = wd
				result[wd.CanonicalKey] = wd
			}
			// Record key for duplicate diagnostics (always append in sorted order)
			dupKeys[wd.ID] = append(dupKeys[wd.ID], wd.CanonicalKey)
		} else {
			// TODO(question): ID-less sections stay distinct records per earlier comment
			if _, exists := result[wd.CanonicalKey]; !exists {
				result[wd.CanonicalKey] = wd
			} else {
				// Same canonical key duplicate with ID <0: keep first winner, record duplicate key
				// For ID -1 we don't group by ID, but we can still note duplicate canonical
				dupKeys[wd.ID] = append(dupKeys[wd.ID], wd.CanonicalKey)
			}
		}
	}
	// Build WeaponDuplicate slice for IDs where len >1, sorted by ID for determinism
	var duplicates []WeaponDuplicate
	for id, keys := range dupKeys {
		if len(keys) <= 1 {
			continue
		}
		// keys already in sorted order because allDefs sorted and we appended in that order
		// but ensure sorted ascending for diagnostics
		sort.Strings(keys)
		// Ensure winner (smallest) first; after sort it is
		dup := WeaponDuplicate{ID: id, Keys: append([]string(nil), keys...), Winner: keys[0]}
		duplicates = append(duplicates, dup)
	}
	sort.Slice(duplicates, func(i, j int) bool { return duplicates[i].ID < duplicates[j].ID })
	return result, duplicates, nil
}

// compileWeapons is an unexported alias for Catalog integration [02 §5] C1 two-stage discover → parse → link.
func compileWeapons(fs vfs.FSOps) (map[string]*WeaponDef, error) {
	return CompileWeapons(fs)
}

// WeaponByID selects the weapon with the given ID. After CompileWeapons there
// is exactly one record per nonnegative ID (same-ID sections merge into one
// record whose name is the later section's [02 "Weapon record"]), so the scan
// is deterministic regardless of order; it iterates sorted canonical keys (I1)
// and returns the first match.
func WeaponByID(weapons map[string]*WeaponDef, id int32) (*WeaponDef, bool) {
	if weapons == nil {
		return nil, false
	}
	// Deterministic iteration: sorted keys (I1)
	keys := make([]string, 0, len(weapons))
	for k := range weapons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if weapons[k].ID == id {
			return weapons[k], true
		}
	}
	return nil, false
}

// CompileWeaponsSorted returns weapons sorted by canonical key for hash-stable iteration (I1) [02 §5] C12.
func CompileWeaponsSorted(fs vfs.FSOps) ([]*WeaponDef, error) {
	m, err := CompileWeapons(fs)
	if err != nil {
		return nil, err
	}
	out := make([]*WeaponDef, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CanonicalKey < out[j].CanonicalKey })
	return out, nil
}
