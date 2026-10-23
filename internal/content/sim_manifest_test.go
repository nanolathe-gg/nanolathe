package content

import (
	"bytes"
	"crypto/sha256"
	"reflect"
	"slices"
	"sort"
	"testing"
)

// The manifest encoding is a published protocol layout (DESIGN_MULTIPLAYER
// §8.7): entry count, then {Family u8, Key text, Ordinal u32, Presence u8,
// SemanticDigest} with the fallback key only on a fallback entry, shortest
// varints throughout, and the digest is SHA-256 of the bare domain followed by
// that encoding.
func TestSimulationManifestEncodingIsPositional(t *testing.T) {
	entries := []SimulationInput{
		{Family: SimulationFamilyCatalog, Key: "a", Ordinal: 300, Presence: SimulationInputPresent, SemanticDigest: [32]byte{0x11}},
		{Family: SimulationFamilyAI, Key: "ai/x.txt", Presence: SimulationInputFallback, FallbackKey: "ai/default.txt", SemanticDigest: [32]byte{0x22}},
	}
	want := []byte{0x02, 0x01, 0x01, 'a', 0xAC, 0x02, 0x01}
	want = append(want, entries[0].SemanticDigest[:]...)
	want = append(want, 0x06, 0x08)
	want = append(want, "ai/x.txt"...)
	want = append(want, 0x00, 0x02)
	want = append(want, entries[1].SemanticDigest[:]...)
	want = append(want, 0x0E)
	want = append(want, "ai/default.txt"...)
	got := encodeSimulationManifest(entries)
	if !bytes.Equal(got, want) {
		t.Fatalf("encoding\n got %x\nwant %x", got, want)
	}
	if simulationManifestDigest(entries) != sha256.Sum256(append([]byte("nanolathe/sim-content/1"), want...)) {
		t.Fatal("digest is not SHA-256 of the domain and the encoding")
	}
	// Any one field changes the identity.
	for i, mutate := range []func(*SimulationInput){
		func(e *SimulationInput) { e.Family = SimulationFamilyCOB },
		func(e *SimulationInput) { e.Key = "b" },
		func(e *SimulationInput) { e.Ordinal = 301 },
		func(e *SimulationInput) { e.Presence = SimulationInputAbsent },
		func(e *SimulationInput) { e.SemanticDigest[31] = 1 },
	} {
		changed := slices.Clone(entries)
		mutate(&changed[0])
		if simulationManifestDigest(changed) == simulationManifestDigest(entries) {
			t.Fatalf("mutation %d left the digest unchanged", i)
		}
	}
}

func TestSimulationManifestOrderAndLimits(t *testing.T) {
	entries := []SimulationInput{
		{Family: SimulationFamilyModel, Key: "b", Presence: SimulationInputPresent},
		{Family: SimulationFamilyCatalog, Key: "z", Ordinal: 2, Presence: SimulationInputPresent},
		{Family: SimulationFamilyCatalog, Key: "y", Ordinal: 1, Presence: SimulationInputPresent},
		{Family: SimulationFamilyModel, Key: "a", Presence: SimulationInputPresent},
	}
	sortSimulationInputs(entries)
	var order []string
	for _, e := range entries {
		order = append(order, e.Key)
	}
	if !slices.Equal(order, []string{"y", "z", "a", "b"}) {
		t.Fatalf("manifest order = %v, want family, ordinal, key", order)
	}
	if err := validateSimulationInputs(entries); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]SimulationInput{
		"empty key":          {Family: 1, Presence: 1},
		"long key":           {Family: 1, Presence: 1, Key: string(make([]byte, 1025))},
		"nul key":            {Family: 1, Presence: 1, Key: "a\x00"},
		"fallback without":   {Family: 1, Presence: 2, Key: "a"},
		"fallback on absent": {Family: 1, Presence: 0, Key: "a", FallbackKey: "b"},
		"family":             {Family: 8, Presence: 1, Key: "a"},
		"presence":           {Family: 1, Presence: 3, Key: "a"},
	} {
		if err := validateSimulationInputs([]SimulationInput{bad}); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	dup := []SimulationInput{{Family: 1, Presence: 1, Key: "a"}, {Family: 1, Presence: 1, Key: "a"}}
	if err := validateSimulationInputs(dup); err == nil {
		t.Fatal("duplicate entry accepted")
	}
}

// Every field of the definitions the catalog family digests is classified,
// so a field added later cannot silently stay out of the content identity.
// "canonical" fields reach the unit digest through writeUnitCanonical,
// "hash" fields through a weapon or feature's compiled Hash (preparation never
// changes them), "explicit" fields are encoded by the family encoder, "cob" is
// the COB family's, "diagnostic" fields are provenance, never identity, and
// "derived" fields are pure functions of canonical fields already digested.
func TestSimulationDigestClassifiesEveryDefinitionField(t *testing.T) {
	check := func(t *testing.T, v any, classes map[string][]string) map[string]string {
		t.Helper()
		classOf := make(map[string]string)
		for class, names := range classes {
			for _, name := range names {
				if prev, dup := classOf[name]; dup {
					t.Fatalf("%s classified twice (%s, %s)", name, prev, class)
				}
				classOf[name] = class
			}
		}
		typ := reflect.TypeOf(v)
		var missing []string
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			if _, ok := classOf[name]; !ok {
				missing = append(missing, name)
			}
			delete(classOf, name)
		}
		sort.Strings(missing)
		if len(missing) != 0 {
			t.Fatalf("%s fields not classified for the simulation-content digest: %v", typ.Name(), missing)
		}
		if len(classOf) != 0 {
			t.Fatalf("%s classification names fields it does not have: %v", typ.Name(), classOf)
		}
		out := make(map[string]string)
		for class, names := range classes {
			for _, name := range names {
				out[name] = class
			}
		}
		return out
	}

	unitClasses := check(t, UnitDef{}, map[string][]string{
		"canonical": {"DefinitionHeader", "extensionKeys", "DiscoveryOnly", "UnitDefID", "UnitName", "Name", "Description", "Side", "ObjectName", "Category", "SoundCategory", "Corpse", "MovementClass", "MobilityDomain", "Weapon1", "Weapon2", "Weapon3", "ExplodeAs", "SelfDestructAs", "YardMap", "DefaultMissionType", "BadTargetCategoryWPRI", "BadTargetCategoryWSEC", "BadTargetCategoryWSPE", "NoChaseCategory", "AIWeight",
			"BadTargetCategoryWPRIMask", "BadTargetCategoryWSECMask", "BadTargetCategoryWSPEMask", "NoChaseCategoryMask", "UnitMask",
			"BuildCostEnergy", "BuildCostMetal", "EnergyMake", "EnergyUse", "MetalMake", "ExtractsMetal", "WindGenerator", "TidalGenerator", "EnergyStorage", "MetalStorage", "MakesMetal", "BuildTime", "WorkerTime", "HealTime", "CloakCost", "CloakCostMoving",
			"MaxVelocity", "BrakeRate", "Acceleration", "BankScale", "PitchScale", "DamageModifier", "MoveRate1", "MoveRate2", "TurnRate", "Waterline", "MinWaterDepth", "MaxWaterDepth", "MaxSlope", "BadSlope", "MaxWaterSlope", "BadWaterSlope", "CruiseAlt", "TransportSize", "TransportCapacity", "BuildAngle", "BuildDistance", "SortBias", "ManeuverLeashLength", "AttackRunLength", "KamikazeDistance", "FootprintX", "FootprintZ",
			"MaxDamage", "SightDistance", "RadarDistance", "SonarDistance", "RadarDistanceJam", "SonarDistanceJam", "MinCloakDistance",
			"StandingMoveOrder", "StandingFireOrder", "InitCloaked", "Downloadable", "Builder", "Stealth", "BMCode", "ZBuffer", "IsAirBase", "IsTargetingUpgrade", "Teleporter", "HideDamage", "ShootMe", "ArmoredState", "ActivateWhenBuilt", "CanFly", "CanHover", "Upright", "Floater", "Amphibious", "IsFeature", "NoShadow", "ImmuneToParalyzer", "HoverAttack", "AntiWeapons", "Digger", "OnOffable", "MobileStandOrders", "FireStandOrders", "CanStop", "CanAttack", "CanGuard", "CanPatrol", "CanMove", "CanLoad", "CanReclamate", "CanResurrect", "CanCapture", "CanDGun", "Kamikaze", "NoRestrict", "ShowPlayerName", "Commander", "CantBeTransported", "Wacky",
			"SelfDestructCountdown", "SelfDestructCountdownPresent", "Unknown"},
		"explicit": {"ModelTop", "ModelTopFixed", "BuildPageCount", "HasPageZeroGUI", "UnitLimit", "LimitEnabled", "Limit", "Weapon1Def", "Weapon2Def", "Weapon3Def", "ExplodeAsDef", "SelfDestructAsDef", "TransportedExplodeAsDef", "TransportedSelfDestructAsDef",
			"Rotations", "VeterancyThresholds", "VeterancyAccuracyBuffRate", "TransportedExplodeAs", "TransportedSelfDestructAs", "PreviewPieces", "PreviewPiecesS", "PreviewPiecesE", "PreviewPiecesN", "PreviewPiecesW", "PreviewFaceOpponent", "PreviewObject3D",
			"BuilderSpeed", "SmartMaker"},
		"cob":        {"Script"},
		"diagnostic": {"DiscoveryProvenance", "ScriptProvenance"},
		// Derived only from the canonical Category string, which already
		// reaches both identities; recording it again would move stock hashes.
		"derived": {"NanolatheInfector"},
	})
	check(t, WeaponDef{}, map[string][]string{
		"hash": {"DefinitionHeader", "extensionKeys", "Name", "WeaponVelocity", "StartVelocity", "WeaponAcceleration", "WeaponTimer", "BurstRate", "Duration", "RandomDecay", "SmokeDelay", "FlightTime", "HoldTime", "ShakeDuration", "TurnRate", "MinBarrelAngle",
			"Range", "Coverage", "EdgeEffectiveness", "EnergyPerShot", "MetalPerShot", "Burst", "SprayAngle", "Accuracy", "Tolerance", "PitchTolerance", "ShakeMagnitude", "Firestarter", "RenderType", "Color", "Color2",
			"NoAutoRange", "SoundTrigger", "Guidance", "Tracks", "LineOfSight", "Ballistic", "UnitsOnly", "GroundBounce", "WaterWeapon", "ToAirWeapon", "SmokeTrail", "Turret", "SelfProp", "Propeller", "NoExplode", "BurnBlow", "TwoPhase", "Cruise", "CommandFire", "Stockpile", "Targetable", "Interceptor", "BeamWeapon", "ShellWeapon", "Dropped", "VLaunch", "Meteor", "NoRadar", "Paralyzer", "StartSmoke", "EndSmoke",
			"NotToAir", "ToAirOnly", "NotToUnderwater", "SurfaceFire", "NoOverWater", "NoOverLand", "NoMapWeaponAlert", "ReloadBar",
			"Model", "ExplosionGaf", "ExplosionArt", "WaterExplosionGaf", "WaterExplosionArt", "LavaExplosionGaf", "LavaExplosionArt", "SoundStart", "SoundHit", "SoundWater", "damageOrder", "Unknown"},
		"explicit": {"ID", "ReloadTime", "AreaOfEffect", "DamageDefault", "Damage", "activeByte", "activeByteRestored"},
		// Derived only from damageOrder, which already reaches the hash; a
		// lower-cased projection of the same keys carries no further identity.
		"derived": {"damageFold"},
	})
	check(t, FeatureDef{}, map[string][]string{
		"hash": {"DefinitionHeader", "Description", "FootprintX", "FootprintZ", "Height", "Object", "Filename", "SeqName", "SeqNameShad", "SeqNameBurn", "SeqNameBurnShad", "SeqNameDie", "SeqNameDieShad", "SeqNameReclamate", "SeqNameReclamateShad",
			"Damage", "SpreadChance", "Reproduce", "ReproduceArea", "SparkTime", "BurnWeapon", "Animating", "AnimTrans", "ShadTrans",
			"Flamable", "Geothermal", "Blocking", "Reclaimable", "Autoreclaimable", "Indestructible", "NoDisplayInfo", "NoDrawUnderGray", "FeatureDead", "FeatureReclamate", "FeatureBurnt", "Unknown"},
		"explicit": {"Metal", "Energy", "FeatureDeadDef", "FeatureReclamateDef", "FeatureBurntDef"},
	})

	// The unit classification is checked against the encoder itself: changing
	// any exported canonical or explicit field changes the unit's digest.
	base := UnitDef{UnitName: "u", ObjectName: "m", Limit: -1}
	baseDigest := unitSemanticDigest(&base)
	typ := reflect.TypeOf(base)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		class := unitClasses[field.Name]
		if !field.IsExported() || (class != "canonical" && class != "explicit") || field.Name == "DefinitionHeader" {
			continue
		}
		changed := base
		value := reflect.ValueOf(&changed).Elem().Field(i)
		switch value.Kind() {
		case reflect.Bool:
			value.SetBool(!value.Bool())
		case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
			value.SetInt(value.Int() + 3)
		case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			value.SetUint(value.Uint() + 3)
		case reflect.Float32, reflect.Float64:
			value.SetFloat(value.Float() + 3)
		case reflect.String:
			value.SetString(value.String() + "x")
		case reflect.Slice:
			value.Set(reflect.Append(value, reflect.Zero(value.Type().Elem())))
		case reflect.Map:
			value.Set(reflect.MakeMap(value.Type()))
			value.SetMapIndex(reflect.ValueOf("k"), reflect.ValueOf("v"))
		case reflect.Pointer:
			value.Set(reflect.ValueOf(&WeaponDef{DefinitionHeader: DefinitionHeader{CanonicalKey: "w"}, ID: 4}))
		case reflect.Struct:
			if value.Type() == reflect.TypeOf(Factor{}) {
				value.Set(reflect.ValueOf(Factor{Num: 5, Den: 1}))
			} else {
				value.Set(reflect.ValueOf(MaskForID(5)))
			}
		default:
			t.Fatalf("no perturbation for %s (%s)", field.Name, value.Kind())
		}
		if unitSemanticDigest(&changed) == baseDigest {
			t.Errorf("unit field %s is classified %s but does not reach the digest", field.Name, class)
		}
	}
}
