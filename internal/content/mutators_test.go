package content

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// mutatorFixture is an authored catalog shaped for the mutator contracts
// (docs/DESIGN_MODS_MUTATORS.md §6.4–§6.6): two units share one corpse chain
// (the second spells the corpse name in another case with padding, as the
// canonical lookup allows), a third unit's corpse chain is cyclic, and two
// features are reachable from no unit, the way map-authored trees and rocks
// are. Units carry sight and sensor distances, some zero and one negative, and
// link three weapons: one with case-variant damage overrides and a negative
// override, one whose authored default is outside the unsigned 16-bit store,
// and one with no damage at all. Their areas of effect span the direct-hit
// bound: the gun's 16 is a direct hit, the death blast's 400 is a large
// splash, and an unlinked shell's 30 is the smallest stock splash. The units
// carry the production shapes Income distinguishes — a commander-like surplus
// over upkeep, a self-powered unit, a solar-like negative energyuse, wind and
// tidal generators and a negative energymake — and an indestructible metal
// deposit sits among the features. One ground unit and one flying unit
// carry movement values for Unit speed.
func mutatorFixture(t *testing.T) *Catalog {
	t.Helper()
	units := map[string]*UnitDef{
		"armbuilder": {UnitName: "ARMBUILDER", WorkerTime: 300, BuildCostMetal: 150, BuildCostEnergy: 1200, BuildTime: 4000, MaxDamage: 800, HealTime: 20, Corpse: "armbuilder_dead",
			SightDistance: 300, RadarDistance: 1000, SonarDistance: 0, RadarDistanceJam: 200, SonarDistanceJam: 0, Weapon1: "gun", ExplodeAs: "blast",
			EnergyMake: 25, EnergyUse: 1, MetalMake: 0.3, MakesMetal: 1,
			MaxVelocity: 1 << 16, Acceleration: 1000, BrakeRate: 3000, TurnRate: 500, MoveRate1: 2 << 16, MoveRate2: 2 << 16},
		"armtwin": {UnitName: "ARMTWIN", WorkerTime: 0, BuildCostMetal: 0, BuildCostEnergy: 7, BuildTime: 100, MaxDamage: 50, Corpse: " ARMBUILDER_DEAD ",
			SightDistance: -5, SonarDistance: 3, SonarDistanceJam: 7, Weapon1: "gun",
			EnergyMake: 5, EnergyUse: 5, ExtractsMetal: 0.001},
		"corloop": {UnitName: "CORLOOP", WorkerTime: 1, BuildCostMetal: 3, BuildCostEnergy: 5, BuildTime: 10, MaxDamage: 10, Corpse: "loop_a", SightDistance: 1, SelfDestructAs: "dud", EnergyUse: -20,
			CanFly: true, MaxVelocity: 600000, Acceleration: 20000, BrakeRate: 100000, TurnRate: 40000, MoveRate1: 900000},
		"corplain": {UnitName: "CORPLAIN", WorkerTime: 65, BuildCostMetal: 1, BuildCostEnergy: 1, BuildTime: 10, MaxDamage: 10, WindGenerator: 30, TidalGenerator: 1, EnergyMake: -2},
	}
	for key, u := range units {
		u.CanonicalKey = key
		u.Hash = "authored-" + key
	}
	features := map[string]*FeatureDef{
		"armbuilder_dead": {Metal: 90, Energy: 0, Damage: 500, FeatureDead: "armbuilder_heap"},
		"armbuilder_heap": {Metal: 45, Energy: 3, Damage: 100},
		"loop_a":          {Metal: 8, Energy: 1, FeatureDead: "loop_b"},
		"loop_b":          {Metal: 9, Energy: 2, FeatureDead: "loop_a"},
		"tree":            {Energy: 250, FeatureBurnt: "tree_burnt"},
		"tree_burnt":      {Metal: 30, Energy: 30},
		"deposit":         {Metal: 250, Indestructible: true},
	}
	for key, f := range features {
		f.CanonicalKey = key
		f.Hash = "authored-" + key
	}
	if err := LinkFeatureSuccessors(features); err != nil {
		t.Fatal(err)
	}
	weapons := map[string]*WeaponDef{
		"gun":   {ID: 1, DamageDefault: 45, Damage: map[string]int32{"ARMBUILDER": 90, "corloop": -3, "CorLoop": 7}, AreaOfEffect: 16, ReloadTime: 45},
		"blast": {ID: 2, DamageDefault: 70000, AreaOfEffect: 400},
		"dud":   {ID: 3},
		"shell": {ID: 4, DamageDefault: 20, AreaOfEffect: 30, ReloadTime: 5},
	}
	for key, w := range weapons {
		w.CanonicalKey = key
		w.Hash = "authored-" + key
	}
	c := &Catalog{Units: units, Features: features, Weapons: weapons, Hash: "base-catalog-hash"}
	for _, u := range units {
		c.rewireWeaponLink(u.Weapon1, &u.Weapon1Def)
		c.rewireWeaponLink(u.ExplodeAs, &u.ExplodeAsDef)
		c.rewireWeaponLink(u.SelfDestructAs, &u.SelfDestructAsDef)
	}
	return c
}

// unitValue, weaponValue and featureValue return value copies with every
// cross-record pointer cleared, so two catalogs' records compare by their own
// fields.
func unitValue(u *UnitDef) UnitDef {
	cp := *u
	cp.Weapon1Def, cp.Weapon2Def, cp.Weapon3Def, cp.ExplodeAsDef, cp.SelfDestructAsDef = nil, nil, nil, nil, nil
	cp.TransportedExplodeAsDef, cp.TransportedSelfDestructAsDef = nil, nil
	return cp
}

func weaponValue(w *WeaponDef) WeaponDef {
	cp := *w
	if w.Damage != nil {
		cp.Damage = make(map[string]int32, len(w.Damage))
		for k, v := range w.Damage {
			cp.Damage[k] = v
		}
	}
	return cp
}

func featureValue(f *FeatureDef) FeatureDef {
	cp := *f
	cp.FeatureDeadDef, cp.FeatureReclamateDef, cp.FeatureBurntDef = nil, nil, nil
	return cp
}

// TestZeroMutatorsLeaveTheCloneIdentical locks §6.6's identity rule: with no
// mutators nothing is transformed and nothing is rehashed, so every existing
// catalog identity and fingerprint stays where it is. An explicit 1/1 in every
// field is the same identity as the zero value.
func TestZeroMutatorsLeaveTheCloneIdentical(t *testing.T) {
	base := mutatorFixture(t)
	one := Factor{1, 1}
	for _, m := range []Mutators{{}, {BuildSpeed: one, BuildCost: one, Health: one, Damage: one, Sight: one, Radar: one, Income: one, Salvage: one, FireRate: one, UnitSpeed: one, BuilderSpeed: one}} {
		want, got := base.Clone(), base.Clone()
		if err := got.ApplyMutators(m); err != nil {
			t.Fatalf("ApplyMutators(%+v): %v", m, err)
		}
		if got.Hash != base.Hash {
			t.Fatalf("zero set moved the catalog hash: %q -> %q", base.Hash, got.Hash)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("zero set %+v changed the catalog", m)
		}
	}
}

// TestEachMutatorChangesExactlyItsFields checks every unit, weapon and feature
// record against a pristine clone for each mutator in turn: build speed moves
// only BuildTime, by the inverse factor (P1 revised: the worker quantum is
// workertime/30 in integers [05 R-WORK-01 §1]); build cost moves the two unit
// costs and corpse-chain feature pools (P2); health moves MaxDamage; damage
// moves each weapon's default and overrides; area of effect moves each splash
// weapon's AreaOfEffect; sight moves SightDistance; radar moves the four
// sensor and jamming distances (P3); income moves the production fields;
// salvage moves every destructible feature's pools; fire rate moves each
// weapon's ReloadTime by the inverse factor; unit speed moves the motion
// fields. Per-definition hashes stay the authored identities (§6.6).
func TestEachMutatorChangesExactlyItsFields(t *testing.T) {
	base := mutatorFixture(t)
	pristine := base.Clone()
	k := Factor{2, 1}
	scaleI := func(v int32, limit int64) int32 { return int32(k.scale(int64(v), limit)) }
	chain := map[string]bool{"armbuilder_dead": true, "armbuilder_heap": true, "loop_a": true, "loop_b": true}
	for _, tc := range []struct {
		key     string
		unit    func(*UnitDef)
		weapon  func(*WeaponDef)
		feature func(string, *FeatureDef)
	}{
		{key: "buildSpeed", unit: func(u *UnitDef) { u.BuildTime = int32(k.inverse().scale(int64(u.BuildTime), math.MaxInt32)) }},
		{key: "buildCost", unit: func(u *UnitDef) {
			u.BuildCostMetal, u.BuildCostEnergy = k.scaleCost(u.BuildCostMetal), k.scaleCost(u.BuildCostEnergy)
		}, feature: func(key string, f *FeatureDef) {
			if chain[key] {
				f.Metal, f.Energy = scaleI(f.Metal, math.MaxUint16), scaleI(f.Energy, math.MaxUint16)
			}
		}},
		{key: "health", unit: func(u *UnitDef) { u.MaxDamage = scaleI(u.MaxDamage, math.MaxInt16) }},
		{key: "damage", weapon: func(w *WeaponDef) {
			if stored := int64(uint16(w.DamageDefault)); stored > 0 {
				w.DamageDefault = int32(k.scale(stored, math.MaxInt16))
			}
			for key, v := range w.Damage {
				w.Damage[key] = scaleI(v, math.MaxInt16)
			}
		}},
		{key: "areaOfEffect", weapon: func(w *WeaponDef) {
			if w.AreaOfEffect > 16 {
				w.AreaOfEffect = scaleI(w.AreaOfEffect, math.MaxUint16)
			}
		}},
		{key: "income", unit: func(u *UnitDef) {
			u.EnergyMake = k.scaleSurplus(u.EnergyMake, u.EnergyUse)
			if u.EnergyUse < 0 {
				u.EnergyUse = -k.scaleStoredFloat(-u.EnergyUse)
			}
			u.MetalMake, u.ExtractsMetal = k.scaleStoredFloat(u.MetalMake), k.scaleStoredFloat(u.ExtractsMetal)
			u.WindGenerator, u.TidalGenerator = k.scaleStoredFloat(u.WindGenerator), k.scaleStoredFloat(u.TidalGenerator)
		}},
		{key: "salvage", feature: func(key string, f *FeatureDef) {
			if !f.Indestructible {
				f.Metal, f.Energy = scaleI(f.Metal, math.MaxUint16), scaleI(f.Energy, math.MaxUint16)
			}
		}},
		{key: "fireRate", weapon: func(w *WeaponDef) {
			w.ReloadTime = int32(k.inverse().scale(int64(w.ReloadTime), math.MaxUint16))
		}},
		{key: "unitSpeed", unit: func(u *UnitDef) {
			brake := int64(4)
			if u.CanFly {
				brake = 2
			}
			u.MaxVelocity, u.MoveRate1, u.MoveRate2 = 2*u.MaxVelocity, 2*u.MoveRate1, 2*u.MoveRate2
			u.Acceleration, u.BrakeRate = 4*u.Acceleration, int32(brake*int64(u.BrakeRate))
			if u.TurnRate > 0 {
				u.TurnRate = int32(min(2*int64(u.TurnRate), math.MaxUint16))
			}
		}},
		{key: "sight", unit: func(u *UnitDef) { u.SightDistance = scaleI(u.SightDistance, math.MaxInt16) }},
		{key: "radar", unit: func(u *UnitDef) {
			u.RadarDistance, u.SonarDistance = scaleI(u.RadarDistance, math.MaxInt16), scaleI(u.SonarDistance, math.MaxInt16)
			u.RadarDistanceJam, u.SonarDistanceJam = scaleI(u.RadarDistanceJam, math.MaxInt16), scaleI(u.SonarDistanceJam, math.MaxInt16)
		}},
	} {
		var m Mutators
		if err := m.SetFactor(tc.key, k); err != nil {
			t.Fatal(err)
		}
		got := base.Clone()
		if err := got.ApplyMutators(m); err != nil {
			t.Fatal(err)
		}
		changed := false
		for _, key := range pristine.SortedUnitKeys() {
			want := unitValue(pristine.Units[key])
			if tc.unit != nil {
				tc.unit(&want)
			}
			have := unitValue(got.Units[key])
			if !reflect.DeepEqual(want, have) {
				t.Fatalf("%s: unit %s = %+v, want %+v", tc.key, key, have, want)
			}
			changed = changed || !reflect.DeepEqual(have, unitValue(pristine.Units[key]))
		}
		for key, w := range pristine.Weapons {
			want := weaponValue(w)
			if tc.weapon != nil {
				tc.weapon(&want)
			}
			have := weaponValue(got.Weapons[key])
			if !reflect.DeepEqual(want, have) {
				t.Fatalf("%s: weapon %s = %+v, want %+v", tc.key, key, have, want)
			}
			changed = changed || !reflect.DeepEqual(have, weaponValue(w))
		}
		for key, f := range pristine.Features {
			want := featureValue(f)
			if tc.feature != nil {
				tc.feature(key, &want)
			}
			have := featureValue(got.Features[key])
			if !reflect.DeepEqual(want, have) {
				t.Fatalf("%s: feature %s = %+v, want %+v", tc.key, key, have, want)
			}
			changed = changed || !reflect.DeepEqual(have, featureValue(f))
		}
		if !changed {
			t.Fatalf("%s: the fixture must move a field, or the comparison proves nothing", tc.key)
		}
	}
}

// TestDamageReachesLinkedWeaponsAndStoredDefault: the Damage mutator scales
// the weapon records every unit link points at — primary, death and
// self-destruct — walks each override once, leaves a non-positive override
// alone, and scales the default from its stored unsigned 16-bit value
// [06 R-DMG-01 §1].
func TestDamageReachesLinkedWeaponsAndStoredDefault(t *testing.T) {
	c := mutatorFixture(t).Clone()
	if err := c.ApplyMutators(Mutators{Damage: Factor{2, 1}}); err != nil {
		t.Fatal(err)
	}
	u := c.Units["armbuilder"]
	if u.Weapon1Def != c.Weapons["gun"] || u.ExplodeAsDef != c.Weapons["blast"] || c.Units["corloop"].SelfDestructAsDef != c.Weapons["dud"] {
		t.Fatal("unit weapon links left the clone")
	}
	gun := u.Weapon1Def
	if gun.DamageDefault != 90 || gun.Damage["ARMBUILDER"] != 180 || gun.Damage["CorLoop"] != 14 || gun.Damage["corloop"] != -3 {
		t.Fatalf("gun = default %d overrides %v, want 90 and {180, 14, -3}", gun.DamageDefault, gun.Damage)
	}
	// 70,000 is stored as 70,000 mod 65,536 = 4,464; the mutator starts there.
	if blast := u.ExplodeAsDef; blast.DamageDefault != 8928 {
		t.Fatalf("death weapon default = %d, want twice the stored 4464", blast.DamageDefault)
	}
	if dud := c.Weapons["dud"]; dud.DamageDefault != 0 || dud.Damage != nil {
		t.Fatalf("a weapon with no damage gained some: %+v", dud)
	}
}

// TestAreaOfEffectKeepsTheDirectHitBound: a projectile meeting a unit with
// areaofeffect at or below 16 damages that unit alone [06 §9.1], so the Area
// of effect mutator leaves such a weapon alone at every factor, never scales a
// splash weapon into that class, and saturates at the unsigned 16-bit store.
func TestAreaOfEffectKeepsTheDirectHitBound(t *testing.T) {
	for _, tc := range []struct {
		f          Factor
		area, want int32
	}{
		{Factor{4, 1}, 0, 0},         // no area stays none
		{Factor{4, 1}, 16, 16},       // the bound itself is a direct hit
		{Factor{1, 4}, 8, 8},         // a direct hit does not shrink either
		{Factor{4, 1}, 17, 68},       // the first splash value scales
		{Factor{1, 4}, 30, 17},       // 8 would be a direct hit: floor at 17
		{Factor{1, 2}, 36, 18},       // above the floor, plain rounding
		{Factor{3, 2}, 950, 1425},    // the stock commander blast
		{Factor{4, 1}, 16384, 65535}, // saturates at the 16-bit store
	} {
		if got := tc.f.scaleArea(tc.area); got != tc.want {
			t.Errorf("area %d × %s = %d, want %d", tc.area, tc.f, got, tc.want)
		}
	}
	c := mutatorFixture(t).Clone()
	if err := c.ApplyMutators(Mutators{AreaOfEffect: Factor{2, 1}}); err != nil {
		t.Fatal(err)
	}
	if blast := c.Units["armbuilder"].ExplodeAsDef; blast != c.Weapons["blast"] || blast.AreaOfEffect != 800 {
		t.Fatal("the death weapon's area must double through the unit's link")
	}
	if c.Weapons["gun"].AreaOfEffect != 16 {
		t.Fatal("a direct-hit weapon gained splash")
	}
}

// TestIncomeScalesProductionBeyondUpkeep locks Income's shapes on the
// fixture: a surplus over upkeep scales and the upkeep does not (25 made for 1
// used becomes 1 + k·24), a self-powered unit (5 for 5) is untouched, the
// solar-style negative energyuse is production and scales with its sign kept
// [05 R-ECO-01 §2], the metal, extraction, wind and tidal producers scale,
// and makesmetal, positive upkeep and a negative energymake are left alone.
// Each scaled value starts from its single-precision store and is rounded
// once to single [02 R-KEYS-01 §5]: 0.3 at ×0.75 is the single nearest
// 0.22500001, where one double multiply narrowed afterwards would give the
// single below it, nearest 0.22499999.
func TestIncomeScalesProductionBeyondUpkeep(t *testing.T) {
	for _, tc := range []struct {
		k              Factor
		surplus, solar float64
		metal          float32
		wind, tidal    float64
	}{
		{Factor{2, 1}, 49, -40, 0.6, 60, 2},
		{Factor{3, 4}, 19, -15, 0.22500001, 22.5, 0.75},
		{Factor{1, 4}, 7, -5, 0.075, 7.5, 0.25},
	} {
		c := mutatorFixture(t).Clone()
		if err := c.ApplyMutators(Mutators{Income: tc.k}); err != nil {
			t.Fatal(err)
		}
		com, twin, solar, gen := c.Units["armbuilder"], c.Units["armtwin"], c.Units["corloop"], c.Units["corplain"]
		if com.EnergyMake != tc.surplus || com.EnergyUse != 1 || com.MakesMetal != 1 {
			t.Errorf("×%s: surplus unit make %v use %v makesmetal %d, want %v, 1 and 1", tc.k, com.EnergyMake, com.EnergyUse, com.MakesMetal, tc.surplus)
		}
		if got := float32(com.MetalMake); got != tc.metal || float64(got) != com.MetalMake {
			t.Errorf("×%s: metalmake 0.3 = %v, want exactly the single %v", tc.k, com.MetalMake, tc.metal)
		}
		if twin.EnergyMake != 5 || twin.EnergyUse != 5 {
			t.Errorf("×%s: self-powered unit = make %v use %v, want 5 and 5", tc.k, twin.EnergyMake, twin.EnergyUse)
		}
		if want := float64(float32(float64(float32(0.001)) * tc.k.float())); twin.ExtractsMetal != want {
			t.Errorf("×%s: extractsmetal = %v, want %v", tc.k, twin.ExtractsMetal, want)
		}
		if solar.EnergyUse != tc.solar || solar.EnergyMake != 0 {
			t.Errorf("×%s: solar energyuse %v make %v, want %v and 0", tc.k, solar.EnergyUse, solar.EnergyMake, tc.solar)
		}
		if gen.WindGenerator != tc.wind || gen.TidalGenerator != tc.tidal || gen.EnergyMake != -2 {
			t.Errorf("×%s: wind %v tidal %v make %v, want %v, %v and an untouched -2", tc.k, gen.WindGenerator, gen.TidalGenerator, gen.EnergyMake, tc.wind, tc.tidal)
		}
	}
	for _, tc := range []struct {
		name             string
		made, used, want float64
	}{
		{"no upkeep is plain scaling", 1200, 0, 4800},
		{"negative upkeep is not upkeep", 3, -20, 12},
		{"make below upkeep", 2, 5, 2},
		{"NaN stays", math.NaN(), 0, math.NaN()},
		{"single ceiling", math.MaxFloat32, 0, math.MaxFloat32},
	} {
		got := (Factor{4, 1}).scaleSurplus(tc.made, tc.used)
		if got != tc.want && !(math.IsNaN(got) && math.IsNaN(tc.want)) {
			t.Errorf("%s: surplus ×4 of make %v use %v = %v, want %v", tc.name, tc.made, tc.used, got, tc.want)
		}
	}
}

// TestSalvageScalesEveryDestructibleFeature: Salvage reaches every feature
// that is not indestructible — corpse chains and map features alike — and a
// corpse-chain feature takes Build cost and Salvage as one product rounded
// once: 45 at 0.5·0.5 is 11, where two roundings would give 12. An
// indestructible metal deposit is left alone at every factor, because the
// deposit pass keeps only the low byte of its metal [05 R-FEAT-01 §7].
func TestSalvageScalesEveryDestructibleFeature(t *testing.T) {
	c := mutatorFixture(t).Clone()
	if err := c.ApplyMutators(Mutators{BuildCost: Factor{1, 2}, Salvage: Factor{1, 2}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key           string
		metal, energy int32
	}{
		{"armbuilder_dead", 23, 0}, // 90/4 = 22.5 rounds half up
		{"armbuilder_heap", 11, 1}, // one rounding of 45/4 and 3/4
		{"loop_a", 2, 1},           // the minimum of one
		{"loop_b", 2, 1},
		{"tree", 0, 125},       // map-only: Salvage alone
		{"tree_burnt", 15, 15}, // FeatureBurnt is not the corpse chain
		{"deposit", 250, 0},    // indestructible
	} {
		f := c.Features[tc.key]
		if f.Metal != tc.metal || f.Energy != tc.energy {
			t.Errorf("%s = metal %d energy %d, want %d/%d", tc.key, f.Metal, f.Energy, tc.metal, tc.energy)
		}
	}
	c = mutatorFixture(t).Clone()
	if err := c.ApplyMutators(Mutators{Salvage: Factor{4, 1}}); err != nil {
		t.Fatal(err)
	}
	if d, tree := c.Features["deposit"], c.Features["tree"]; d.Metal != 250 || tree.Energy != 1000 {
		t.Fatalf("salvage ×4: deposit %d tree %d, want an untouched 250 and 1000", d.Metal, tree.Energy)
	}
	if got := (Factor{4, 1}).times(Factor{4, 1}); got != (Factor{16, 1}) || (Factor{}).times(Factor{1, 4}) != (Factor{1, 4}) {
		t.Fatal("factor products must be exact and treat the zero value as the identity")
	}
}

// TestFireRateDividesReloadTime: Fire rate k scales each weapon's stored
// reload by 1/k with the Build speed rounding, never creates a zero reload
// (a stockpile weapon would build free rounds [06 R-WPN-05 §2]), leaves a zero
// reload alone and saturates at the unsigned 16-bit store [06 §4.2].
func TestFireRateDividesReloadTime(t *testing.T) {
	for _, tc := range []struct {
		k                Factor
		gun, shell, dead int32
	}{
		{Factor{2, 1}, 23, 3, 0},   // 22.5 and 2.5 round half up
		{Factor{4, 1}, 11, 1, 0},   // 5/4 rounds down to one
		{Factor{1, 4}, 180, 20, 0}, // slower
	} {
		c := mutatorFixture(t).Clone()
		if err := c.ApplyMutators(Mutators{FireRate: tc.k}); err != nil {
			t.Fatal(err)
		}
		if gun, shell, blast := c.Weapons["gun"], c.Weapons["shell"], c.Weapons["blast"]; gun.ReloadTime != tc.gun || shell.ReloadTime != tc.shell || blast.ReloadTime != tc.dead {
			t.Errorf("fire rate ×%s: reloads %d/%d/%d, want %d/%d/%d", tc.k, gun.ReloadTime, shell.ReloadTime, blast.ReloadTime, tc.gun, tc.shell, tc.dead)
		}
		if c.Units["armbuilder"].Weapon1Def != c.Weapons["gun"] {
			t.Fatal("unit weapon links left the clone")
		}
	}
	c := &Catalog{Weapons: map[string]*WeaponDef{"slow": {ReloadTime: 20000}, "one": {ReloadTime: 1}}}
	if err := c.ApplyMutators(Mutators{FireRate: Factor{1, 4}}); err != nil {
		t.Fatal(err)
	}
	if c.Weapons["slow"].ReloadTime != math.MaxUint16 || c.Weapons["one"].ReloadTime != 4 {
		t.Fatalf("reloads = %d/%d, want the 16-bit ceiling and 4", c.Weapons["slow"].ReloadTime, c.Weapons["one"].ReloadTime)
	}
}

// TestUnitSpeedKeepsThePathShape: Unit speed k scales velocity, turn rate and
// the movement-rate thresholds by k and acceleration by k², so the ground
// mover's turning distance |err|·speed/turnrate and braking distance
// speed²/(2·brakerate) are unchanged [04 R-MOV-01 §4]; brakerate scales by k²
// on the ground and by k for a unit that flies, whose integrator compares
// speed with brakerate directly [04 §10.1]. Turn rate saturates at its
// unsigned 16-bit store, and ×0.25 rounds k² = 1/16 once, half up.
func TestUnitSpeedKeepsThePathShape(t *testing.T) {
	for _, tc := range []struct {
		k                                   Factor
		groundV, groundA, groundB, groundW  int32
		airV, airA, airB, airW, airMoveRate int32
	}{
		{Factor{2, 1}, 131072, 4000, 12000, 1000, 1200000, 80000, 200000, 65535, 1800000},
		{Factor{1, 4}, 16384, 63, 188, 125, 150000, 1250, 25000, 10000, 225000},
		{Factor{3, 2}, 98304, 2250, 6750, 750, 900000, 45000, 150000, 60000, 1350000},
	} {
		c := mutatorFixture(t).Clone()
		if err := c.ApplyMutators(Mutators{UnitSpeed: tc.k}); err != nil {
			t.Fatal(err)
		}
		g, a := c.Units["armbuilder"], c.Units["corloop"]
		if g.MaxVelocity != tc.groundV || g.Acceleration != tc.groundA || g.BrakeRate != tc.groundB || g.TurnRate != tc.groundW || g.MoveRate1 != 2*tc.groundV {
			t.Errorf("×%s ground: v %d a %d b %d w %d moverate %d, want %d %d %d %d %d", tc.k, g.MaxVelocity, g.Acceleration, g.BrakeRate, g.TurnRate, g.MoveRate1, tc.groundV, tc.groundA, tc.groundB, tc.groundW, 2*tc.groundV)
		}
		if a.MaxVelocity != tc.airV || a.Acceleration != tc.airA || a.BrakeRate != tc.airB || a.TurnRate != tc.airW || a.MoveRate1 != tc.airMoveRate || a.MoveRate2 != 0 {
			t.Errorf("×%s air: v %d a %d b %d w %d moverate %d/%d, want %d %d %d %d %d/0", tc.k, a.MaxVelocity, a.Acceleration, a.BrakeRate, a.TurnRate, a.MoveRate1, a.MoveRate2, tc.airV, tc.airA, tc.airB, tc.airW, tc.airMoveRate)
		}
	}
	// The ground braking distance is the same at every exact step.
	stop := func(u *UnitDef) int64 {
		v := int64(u.MaxVelocity)
		return ((v * v) >> 16 << 16) / (2 * int64(u.BrakeRate))
	}
	base := mutatorFixture(t).Units["armbuilder"]
	for _, k := range []Factor{{2, 1}, {4, 1}, {1, 2}} {
		c := mutatorFixture(t).Clone()
		if err := c.ApplyMutators(Mutators{UnitSpeed: k}); err != nil {
			t.Fatal(err)
		}
		if got, want := stop(c.Units["armbuilder"]), stop(base); got != want {
			t.Errorf("×%s: braking distance %d, want the unscaled %d", k, got, want)
		}
	}
}

// TestCorpseChainFeaturesAreScaledOnce follows P2's reach: the corpse and its
// FeatureDead successors are scaled, a feature reachable from two units or
// around a cycle is scaled once, and a feature no corpse chain reaches — a
// map-authored tree and its burnt successor — is left alone.
func TestCorpseChainFeaturesAreScaledOnce(t *testing.T) {
	c := mutatorFixture(t).Clone()
	if err := c.ApplyMutators(Mutators{BuildCost: Factor{1, 2}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key           string
		metal, energy int32
	}{
		{"armbuilder_dead", 45, 0}, // reachable from two units, halved once
		{"armbuilder_heap", 23, 2}, // successor: 45/2 and 3/2 round half up
		{"loop_a", 4, 1},           // 1/2 rounds up to the minimum of one
		{"loop_b", 5, 1},           // the cycle ends after one visit
		{"tree", 0, 250},           // map-only
		{"tree_burnt", 30, 30},     // FeatureBurnt is not the corpse chain
	} {
		f := c.Features[tc.key]
		if f.Metal != tc.metal || f.Energy != tc.energy {
			t.Errorf("%s = metal %d energy %d, want %d/%d", tc.key, f.Metal, f.Energy, tc.metal, tc.energy)
		}
	}
	// The clone's successor pointers still reach the clone's scaled records.
	if dead := c.Features["armbuilder_dead"]; dead.FeatureDeadDef != c.Features["armbuilder_heap"] {
		t.Fatal("successor pointer left the clone")
	}
}

// TestMutatorArithmeticBoundaries locks §6.4's arithmetic at its edges: round
// half up in integers, the minimum of one, saturation at each field's store
// maximum, and v <= 0 unchanged.
func TestMutatorArithmeticBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		f        Factor
		v, limit int64
		want     int64
	}{
		{"zero stays zero", Factor{4, 1}, 0, math.MaxUint16, 0},
		{"negative stays", Factor{4, 1}, -5, math.MaxUint16, -5},
		{"minimum one", Factor{1, 4}, 1, math.MaxUint16, 1},
		{"half rounds up", Factor{1, 2}, 3, math.MaxUint16, 2},
		{"quarter rounds down", Factor{1, 4}, 5, math.MaxUint16, 1},
		{"quarter half rounds up", Factor{1, 4}, 6, math.MaxUint16, 2},
		{"three quarters", Factor{3, 4}, 7, math.MaxUint16, 5},
		{"three halves half up", Factor{3, 2}, 3, math.MaxUint16, 5},
		{"exact product", Factor{4, 1}, 16383, math.MaxUint16, 65532},
		{"saturates", Factor{4, 1}, 16384, math.MaxUint16, math.MaxUint16},
		{"saturated input stays", Factor{2, 1}, math.MaxUint16, math.MaxUint16, math.MaxUint16},
		{"cost ceiling", Factor{4, 1}, 1 << 31, math.MaxInt32, math.MaxInt32},
		{"distance ceiling", Factor{2, 1}, 16384, math.MaxInt16, math.MaxInt16},
		{"16-bit exact", Factor{4, 1}, 8191, math.MaxInt16, 32764},
		{"16-bit saturates", Factor{4, 1}, 8192, math.MaxInt16, math.MaxInt16},
	} {
		if got := tc.f.scale(tc.v, tc.limit); got != tc.want {
			t.Errorf("%s: %s × %d = %d, want %d", tc.name, tc.f, tc.v, got, tc.want)
		}
	}
	// Build speed k divides buildtime: max(1, (v·Den + ⌊Num/2⌋) div Num).
	for _, tc := range []struct {
		f       Factor
		v, want int64
	}{
		{Factor{2, 1}, 3, 2},               // 1.5 rounds up
		{Factor{2, 1}, 1, 1},               // 0.5 rounds up to the minimum
		{Factor{4, 1}, 1, 1},               // 0.25 floors at the minimum
		{Factor{3, 2}, 4, 3},               // 2.67 rounds up
		{Factor{3, 2}, 5, 3},               // 3.33 rounds down
		{Factor{1, 4}, 1 << 29, 1<<31 - 1}, // saturates at the 32-bit store
	} {
		if got := tc.f.inverse().scale(tc.v, math.MaxInt32); got != tc.want {
			t.Errorf("build speed %s on buildtime %d = %d, want %d", tc.f, tc.v, got, tc.want)
		}
	}

	// The same boundaries through the catalog, one per stored field type.
	c := &Catalog{Units: map[string]*UnitDef{
		"a": {UnitName: "A", WorkerTime: 20000, BuildTime: 5, BuildCostMetal: -3, BuildCostEnergy: float32(1 << 31), Corpse: "wreck",
			MaxDamage: 1 << 30, SightDistance: 10000, RadarDistance: 9000, SonarDistance: -2, RadarDistanceJam: 8192, SonarDistanceJam: 1},
		"b": {UnitName: "B", WorkerTime: 50, BuildTime: -7, BuildCostMetal: 0, BuildCostEnergy: 1, MaxDamage: -9, SightDistance: 0},
		"c": {UnitName: "C", MaxDamage: 8191},
	}, Features: map[string]*FeatureDef{
		"wreck": {Metal: 65535, Energy: 1},
	}, Weapons: map[string]*WeaponDef{
		"big":   {DamageDefault: 20000, Damage: map[string]int32{"A": 600000000, "B": 0, "C": 8191}},
		"wrapd": {DamageDefault: -5}, // stored as 65,531, above the packet's reach
	}}
	four := Factor{4, 1}
	if err := c.ApplyMutators(Mutators{BuildSpeed: four, BuildCost: four, Health: four, Damage: four, Sight: four, Radar: four}); err != nil {
		t.Fatal(err)
	}
	a, b, wreck, big, wrapd := c.Units["a"], c.Units["b"], c.Features["wreck"], c.Weapons["big"], c.Weapons["wrapd"]
	if a.BuildTime != 1 || b.BuildTime != -7 || a.WorkerTime != 20000 || b.WorkerTime != 50 {
		t.Errorf("buildtime = %d/%d workertime = %d/%d, want 5/4 rounded to 1, an untouched negative and untouched workertimes", a.BuildTime, b.BuildTime, a.WorkerTime, b.WorkerTime)
	}
	if math.Float32bits(a.BuildCostMetal) != math.Float32bits(-3) || b.BuildCostMetal != 0 || b.BuildCostEnergy != 4 {
		t.Errorf("costs = %v/%v/%v, want -3, 0, 4", a.BuildCostMetal, b.BuildCostMetal, b.BuildCostEnergy)
	}
	if a.BuildCostEnergy != float32(math.MaxInt32) {
		t.Errorf("cost ceiling = %v, want the single-float store of the 32-bit maximum", a.BuildCostEnergy)
	}
	if wreck.Metal != math.MaxUint16 || wreck.Energy != 4 {
		t.Errorf("wreck = %d/%d, want saturation at the sixteen-bit pool and 4", wreck.Metal, wreck.Energy)
	}
	if a.MaxDamage != math.MaxInt16 || b.MaxDamage != -9 || c.Units["c"].MaxDamage != 32764 {
		t.Errorf("maxdamage = %d/%d/%d, want the signed 16-bit live-health ceiling, an untouched negative and 32764", a.MaxDamage, b.MaxDamage, c.Units["c"].MaxDamage)
	}
	if a.SightDistance != math.MaxInt16 || b.SightDistance != 0 {
		t.Errorf("sight = %d/%d, want the signed 16-bit ceiling and zero", a.SightDistance, b.SightDistance)
	}
	if a.RadarDistance != math.MaxInt16 || a.SonarDistance != -2 || a.RadarDistanceJam != math.MaxInt16 || a.SonarDistanceJam != 4 {
		t.Errorf("sensors = %d/%d/%d/%d, want 32767, -2, 32767, 4", a.RadarDistance, a.SonarDistance, a.RadarDistanceJam, a.SonarDistanceJam)
	}
	if big.DamageDefault != math.MaxInt16 || big.Damage["A"] != math.MaxInt16 || big.Damage["B"] != 0 || big.Damage["C"] != 32764 {
		t.Errorf("damage = default %d overrides %v, want the signed 16-bit packet ceiling for both, zero and 32764", big.DamageDefault, big.Damage)
	}
	if wrapd.DamageDefault != math.MaxInt16 {
		t.Errorf("a default stored as 65,531 = %d after ×4, want the packet ceiling", wrapd.DamageDefault)
	}
}

// TestApplyMutatorsRefusesBeforeWriting: a factor off the step list and a cost
// the unit compiler could not have stored are both refused, and a refused call
// writes nothing — not the hash, not another mutator's field.
func TestApplyMutatorsRefusesBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    Mutators
		edit func(*Catalog)
	}{
		{"off-list factor", Mutators{BuildSpeed: Factor{5, 1}}, nil},
		{"off-list radar", Mutators{Health: Factor{2, 1}, Radar: Factor{5, 1}}, nil},
		{"unreduced factor", Mutators{Damage: Factor{2, 2}}, nil},
		{"zero denominator", Mutators{BuildCost: Factor{1, 0}}, nil},
		{"fractional cost", Mutators{BuildSpeed: Factor{2, 1}, BuildCost: Factor{2, 1}, Sight: Factor{2, 1}}, func(c *Catalog) { c.Units["corplain"].BuildCostMetal = 1.5 }},
	} {
		c := mutatorFixture(t).Clone()
		if tc.edit != nil {
			tc.edit(c)
		}
		want := c.Clone()
		if err := c.ApplyMutators(tc.m); err == nil {
			t.Fatalf("%s: accepted", tc.name)
		}
		if !reflect.DeepEqual(want, c) {
			t.Fatalf("%s: a refused call changed the catalog", tc.name)
		}
	}
}

// TestMutatedCatalogIdentity locks §6.6: a non-zero set moves Catalog.Hash to
// a value determined by the base hash and the canonical set, so equal sets
// agree however they were assembled and different sets disagree. The digest
// carries the identity tag, which moved to "mutators/3" when Sight's
// transform changed meaning.
func TestMutatedCatalogIdentity(t *testing.T) {
	base := mutatorFixture(t)
	apply := func(m Mutators) string {
		c := base.Clone()
		if err := c.ApplyMutators(m); err != nil {
			t.Fatal(err)
		}
		return c.Hash
	}
	all := Mutators{BuildSpeed: Factor{2, 1}, BuildCost: Factor{1, 2}, Health: Factor{3, 2}, Damage: Factor{3, 4}, Sight: Factor{3, 1}, Radar: Factor{1, 4}}
	parsed, err := ParseMutators(map[string]string{"radar": "0.25", "sight": "3", "damage": "0.75", "health": "1.5", "buildSpeed": "2", "buildCost": "0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed != all || apply(all) != apply(parsed) || apply(all) == base.Hash {
		t.Fatal("equal sets must share one mutated identity distinct from the base")
	}
	seen := map[string]string{}
	for _, info := range MutatorCatalog() {
		var m Mutators
		if err := m.SetFactor(info.Key, Factor{2, 1}); err != nil {
			t.Fatal(err)
		}
		hash := apply(m)
		if other, dup := seen[hash]; dup {
			t.Fatalf("%s and %s share an identity", info.Key, other)
		}
		seen[hash] = info.Key
	}
	if want := HashDefinition([]byte("mutators/3\n" + all.String() + "\n")); all.Digest() != want {
		t.Fatalf("digest %s is not the mutators/3 hash of the canonical set", all.Digest())
	}
	if all.Digest() == HashDefinition([]byte("mutators/1\n"+all.String()+"\n")) || all.Digest() == (Mutators{}).Digest() {
		t.Fatal("digest must carry the current identity tag and follow the set")
	}
	// MutatedHash names the identity ApplyMutators gives, and the zero set
	// leaves the base, so a load can compute a save's recorded identity.
	if MutatedHash(base.Hash, all) != apply(all) || MutatedHash(base.Hash, Mutators{}) != base.Hash {
		t.Fatal("MutatedHash must be the identity ApplyMutators gives")
	}
}

// TestParseMutatorsRoundTrip walks every key through every step via Map,
// String and ParseMutators, and checks the canonical spellings and the error
// cases.
func TestParseMutatorsRoundTrip(t *testing.T) {
	for _, info := range MutatorCatalog() {
		for _, step := range info.Steps {
			var m Mutators
			if err := m.SetFactor(info.Key, step); err != nil {
				t.Fatal(err)
			}
			got, err := ParseMutators(m.Map())
			if err != nil {
				t.Fatalf("ParseMutators(%v): %v", m.Map(), err)
			}
			if got != m || got.String() != m.String() || got.IsZero() != step.IsIdentity() {
				t.Fatalf("round trip %q -> %q", m.String(), got.String())
			}
		}
	}
	m := Mutators{BuildSpeed: Factor{2, 1}, BuildCost: Factor{1, 2}, Radar: Factor{3, 2}}
	if got := m.String(); got != "buildCost=0.5,buildSpeed=2,radar=1.5" {
		t.Fatalf("String = %q", got)
	}
	if got := strings.Join(m.Describe(), "; "); got != "Build speed ×2; Build cost ×0.5; Radar ×1.5" {
		t.Fatalf("Describe = %q", got)
	}
	if got := strings.Join(m.DescribeASCII(), "; "); got != "Build speed x2; Build cost x0.5; Radar x1.5" {
		t.Fatalf("DescribeASCII = %q", got)
	}
	zero := Mutators{}
	if zero.String() != "" || len(zero.Describe()) != 0 || len(zero.DescribeASCII()) != 0 || len(zero.Map()) != 0 {
		t.Fatal("the zero set must print, describe and map to nothing")
	}
	for _, bad := range []map[string]string{
		{"buildspeed": "2"},
		{"speed": "2"},
		{"Health": "2"},
		{"buildSpeed": "5"},
		{"sight": "1/2"},
		{"sight": ".5"},
		{"sight": "2.0"},
		{"radar": "x2"},
		{"damage": "×2"},
		{"health": "2/1"},
		{"buildCost": ""},
		{"builderSpeed": "4"},
		{"builderSpeed": "0.5"},
		{"builderSpeed": "20.0"},
		{"health": "20"},
		{"unitSpeed": "5"},
	} {
		if _, err := ParseMutators(bad); err == nil {
			t.Errorf("ParseMutators(%v) accepted", bad)
		} else {
			for key := range bad {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("error %q does not name %q", err, key)
				}
			}
		}
	}
}

// TestMutatorCatalogDescribesTheClosedSet locks the descriptor API a front end
// builds its rows from: the design table's order and grouping, ASCII-only
// player text for the retail fonts, and Factor/SetFactor over every key.
func TestMutatorCatalogDescribesTheClosedSet(t *testing.T) {
	infos := MutatorCatalog()
	var keys, groups []string
	for _, info := range infos {
		keys = append(keys, info.Key)
		groups = append(groups, info.Group)
		for _, text := range []string{info.Label, info.Group, info.Description} {
			if text == "" {
				t.Fatalf("%s has an empty descriptor field: %+v", info.Key, info)
			}
			for i := 0; i < len(text); i++ {
				if text[i] >= 0x80 {
					t.Fatalf("%s descriptor %q is not ASCII", info.Key, text)
				}
			}
		}
		if !strings.HasSuffix(info.Description, ".") {
			t.Fatalf("%s description %q is not one sentence", info.Key, info.Description)
		}
	}
	if got := strings.Join(keys, ","); got != "buildSpeed,buildCost,income,salvage,smartMakers,health,damage,areaOfEffect,fireRate,unitSpeed,builderSpeed,sight,radar" {
		t.Fatalf("catalog order = %s", got)
	}
	if got := strings.Join(groups, ","); got != "Economy,Economy,Economy,Economy,Economy,Combat,Combat,Combat,Combat,Movement,Builders,Vision,Vision" {
		t.Fatalf("catalog groups = %s", got)
	}
	// Every mutator shares the step list but Builder speed, which has its
	// own, and the Smart makers switch (docs/DESIGN_MODS_MUTATORS.md §6.4).
	for _, info := range infos {
		want := MutatorSteps
		switch info.Key {
		case "builderSpeed":
			want = []Factor{{1, 1}, {2, 1}, {3, 1}, {5, 1}, {8, 1}, {20, 1}}
		case "smartMakers":
			want = []Factor{{1, 1}, {2, 1}}
		}
		if !reflect.DeepEqual(info.Steps, want) {
			t.Fatalf("%s steps = %v, want %v", info.Key, info.Steps, want)
		}
		if info.IsSwitch() != (info.Key == "smartMakers") {
			t.Fatalf("%s IsSwitch = %v", info.Key, info.IsSwitch())
		}
	}
	infos[0].Label = "changed"
	infos[0].Steps[0] = Factor{9, 1}
	if MutatorCatalog()[0].Label == "changed" || MutatorSteps[0] != (Factor{1, 4}) {
		t.Fatal("MutatorCatalog must return a copy")
	}
	infos[10].Steps[1] = Factor{9, 1}
	infos[4].Steps[1] = Factor{9, 1}
	if BuilderSpeedSteps[1] != (Factor{2, 1}) || SwitchSteps[1] != (Factor{2, 1}) {
		t.Fatal("MutatorCatalog must copy Builder speed's own list")
	}

	var m Mutators
	for _, info := range MutatorCatalog() {
		if f, ok := m.Factor(info.Key); !ok || f != (Factor{}) {
			t.Fatalf("Factor(%s) on the zero set = %+v, %v", info.Key, f, ok)
		}
		// The step below the top, which for a two-step switch is its identity,
		// so a switch takes its on step instead.
		step := info.Steps[len(info.Steps)-2]
		if info.IsSwitch() {
			step = info.Steps[1]
		}
		if err := m.SetFactor(info.Key, step); err != nil {
			t.Fatal(err)
		}
		if f, _ := m.Factor(info.Key); f != step || !strings.Contains(m.String(), info.Key+"="+info.Spell(step)) {
			t.Fatalf("SetFactor(%s) did not store %s: %q", info.Key, step, m.String())
		}
	}
	for _, info := range MutatorCatalog() {
		if err := m.SetFactor(info.Key, Factor{1, 1}); err != nil {
			t.Fatal(err)
		}
		if f, _ := m.Factor(info.Key); f != (Factor{}) {
			t.Fatalf("SetFactor(%s, 1/1) stored %+v, want the zero value", info.Key, f)
		}
	}
	if m != (Mutators{}) {
		t.Fatalf("a set edited back to 1 must equal the zero set: %+v", m)
	}
	if _, ok := m.Factor("speed"); ok {
		t.Fatal("Factor accepted an unknown key")
	}
	if err := m.SetFactor("speed", Factor{2, 1}); err == nil {
		t.Fatal("SetFactor accepted an unknown key")
	}
	if err := m.SetFactor("health", Factor{5, 1}); err == nil || m != (Mutators{}) {
		t.Fatal("SetFactor accepted a factor off the step list")
	}
	if err := m.SetFactor("builderSpeed", Factor{4, 1}); err == nil || m != (Mutators{}) {
		t.Fatal("SetFactor accepted a Builder speed factor off its own list")
	}
	if err := m.SetFactor("builderSpeed", Factor{1, 2}); err == nil || m != (Mutators{}) {
		t.Fatal("SetFactor accepted a shared step for Builder speed")
	}
	if err := (Mutators{BuilderSpeed: Factor{4, 1}}).validate(); err == nil || !strings.Contains(err.Error(), "builderSpeed") {
		t.Fatalf("validate = %v, want a builderSpeed refusal", err)
	}
	if err := (Mutators{BuilderSpeed: Factor{20, 1}, UnitSpeed: Factor{4, 1}}).validate(); err != nil {
		t.Fatal(err)
	}
}

// TestFactorSpellingAndCycling covers the UI helpers: canonical spelling,
// labels, and cycling through the step list with clamping at both ends.
func TestFactorSpellingAndCycling(t *testing.T) {
	if (Factor{}).String() != "1" || (Factor{}).Label() != "×1" || (Factor{1, 2}).Label() != "×0.5" || (Factor{3, 2}).String() != "1.5" || (Factor{1, 4}).String() != "0.25" {
		t.Fatal("identity and decimal spellings")
	}
	f := MutatorSteps[0]
	for i := 1; i < len(MutatorSteps); i++ {
		f = f.NextIn(MutatorSteps)
		if f != MutatorSteps[i] {
			t.Fatalf("NextIn walk reached %s at %d, want %s", f, i, MutatorSteps[i])
		}
	}
	if f.NextIn(MutatorSteps) != f {
		t.Fatal("NextIn must clamp at the last step")
	}
	for i := len(MutatorSteps) - 2; i >= 0; i-- {
		f = f.PrevIn(MutatorSteps)
		if f != MutatorSteps[i] {
			t.Fatalf("PrevIn walk reached %s at %d, want %s", f, i, MutatorSteps[i])
		}
	}
	if f.PrevIn(MutatorSteps) != f {
		t.Fatal("PrevIn must clamp at the first step")
	}
	if (Factor{}).NextIn(MutatorSteps) != (Factor{3, 2}) || (Factor{}).PrevIn(MutatorSteps) != (Factor{3, 4}) {
		t.Fatal("the zero value cycles as 1/1")
	}
	for _, step := range MutatorSteps {
		parsed, err := parseFactorIn(step.String(), MutatorSteps)
		if err != nil || parsed != step {
			t.Fatalf("parseFactorIn(%q) = %+v, %v", step.String(), parsed, err)
		}
	}
	// Builder speed cycles on its own list, from the identity at its foot.
	builder := BuilderSpeedSteps
	f = Factor{}
	for _, want := range []string{"2", "3", "5", "8", "20", "20"} {
		if f = f.NextIn(builder); f.String() != want {
			t.Fatalf("NextIn walk reached %s, want %s", f, want)
		}
	}
	for _, want := range []string{"8", "5", "3", "2", "1", "1"} {
		if f = f.PrevIn(builder); f.String() != want {
			t.Fatalf("PrevIn walk reached %s, want %s", f, want)
		}
	}
	if (Factor{}).PrevIn(builder) != (Factor{1, 1}) || (Factor{4, 1}).NextIn(builder) != (Factor{5, 1}) || (Factor{4, 1}).PrevIn(builder) != (Factor{3, 1}) {
		t.Fatal("an off-list factor moves to the neighbouring steps")
	}
	if (Factor{20, 1}).String() != "20" || (Factor{20, 1}).Label() != "×20" {
		t.Fatal("Builder speed's largest step spells as a whole number")
	}
	if _, err := parseFactorIn("20", MutatorSteps); err == nil {
		t.Fatal("the shared list accepted Builder speed's 20")
	}
}

// builderFixture marks the mutator fixture's units for Builder speed: the
// ground armbuilder and the flying corloop are mobile builders (bmcode 1),
// armtwin is a builder the allocator gives no mover, as a factory or a nano
// tower is, with a turn rate to show it is left alone, and corplain, a
// non-builder, carries motion values to show it is untouched.
func builderFixture(t *testing.T) *Catalog {
	t.Helper()
	c := mutatorFixture(t)
	c.Units["armbuilder"].Builder, c.Units["armbuilder"].BMCode = true, 1
	c.Units["corloop"].Builder, c.Units["corloop"].BMCode = true, 1
	c.Units["armtwin"].Builder, c.Units["armtwin"].TurnRate = true, 700
	c.Units["corplain"].BMCode, c.Units["corplain"].MaxVelocity, c.Units["corplain"].Acceleration = 1, 1<<16, 900
	c.Units["corplain"].BrakeRate, c.Units["corplain"].TurnRate = 2000, 400
	return c
}

// TestBuilderSpeedScalesBuilderAgility locks the Builder speed transform
// (docs/DESIGN_MODS_MUTATORS.md "Builder speed"): a ground mobile builder
// turns and accelerates and brakes k× as fast and keeps its top speed; a
// flying one gains turn rate only, because the flight integrator reads
// acceleration and brakerate as speed terms [04 §10.1]; a builder without a
// mover gains nothing but the recorded factor; a non-builder is untouched.
// Turn rate saturates at its unsigned 16-bit store.
func TestBuilderSpeedScalesBuilderAgility(t *testing.T) {
	base := builderFixture(t)
	c := base.Clone()
	if err := c.ApplyMutators(Mutators{BuilderSpeed: Factor{5, 1}}); err != nil {
		t.Fatal(err)
	}
	g, a, fixed := c.Units["armbuilder"], c.Units["corloop"], c.Units["armtwin"]
	if g.TurnRate != 2500 || g.Acceleration != 5000 || g.BrakeRate != 15000 || g.MaxVelocity != 1<<16 || g.MoveRate1 != 2<<16 || g.MoveRate2 != 2<<16 {
		t.Errorf("ground builder: w %d a %d b %d v %d moverates %d/%d", g.TurnRate, g.Acceleration, g.BrakeRate, g.MaxVelocity, g.MoveRate1, g.MoveRate2)
	}
	if a.TurnRate != 65535 || a.Acceleration != 20000 || a.BrakeRate != 100000 || a.MaxVelocity != 600000 || a.MoveRate1 != 900000 {
		t.Errorf("flying builder: w %d a %d b %d v %d moverate %d", a.TurnRate, a.Acceleration, a.BrakeRate, a.MaxVelocity, a.MoveRate1)
	}
	if fixed.TurnRate != 700 {
		t.Errorf("builder without a mover: turn rate %d", fixed.TurnRate)
	}
	for _, key := range []string{"armbuilder", "corloop", "armtwin"} {
		if c.Units[key].BuilderSpeed != (Factor{5, 1}) {
			t.Errorf("%s records %+v, want 5/1", key, c.Units[key].BuilderSpeed)
		}
	}
	if !reflect.DeepEqual(unitValue(c.Units["corplain"]), unitValue(base.Units["corplain"])) {
		t.Error("a non-builder changed")
	}
	if c.Hash == base.Hash {
		t.Error("Builder speed did not move the catalog identity")
	}
	// Builders keep their factor and agility only on the clone.
	if base.Units["armbuilder"].BuilderSpeed != (Factor{}) || base.Units["armbuilder"].TurnRate != 500 {
		t.Error("the transform wrote through to the base catalog")
	}
}

// TestBuilderSpeedComposesWithUnitSpeed: with Unit speed each motion field is
// scaled once by the exact product, formed in 64 bits rather than as a Factor
// (×4² × 20 is 320, which an 8-bit numerator would wrap to 64), then
// saturated (docs/DESIGN_MODS_MUTATORS.md §6.4).
func TestBuilderSpeedComposesWithUnitSpeed(t *testing.T) {
	for _, tc := range []struct {
		unit, builder              Factor
		gw, ga, gb, gv             int32
		aw, aa, ab, av, plainAccel int32
	}{
		// ground: turn ku·kb, accel ku²·kb, brake ku²·kb; air: turn ku·kb,
		// accel ku², brake ku. corplain is not a builder: accel ku².
		{Factor{2, 1}, Factor{20, 1}, 20000, 80000, 240000, 131072, 65535, 80000, 200000, 1200000, 3600},
		{Factor{4, 1}, Factor{20, 1}, 40000, 320000, 960000, 262144, 65535, 320000, 400000, 2400000, 14400},
		{Factor{1, 4}, Factor{2, 1}, 250, 125, 375, 16384, 20000, 1250, 25000, 150000, 56},
		{Factor{3, 2}, Factor{3, 1}, 2250, 6750, 20250, 98304, 65535, 45000, 150000, 900000, 2025},
	} {
		c := builderFixture(t).Clone()
		if err := c.ApplyMutators(Mutators{UnitSpeed: tc.unit, BuilderSpeed: tc.builder}); err != nil {
			t.Fatal(err)
		}
		g, a, p := c.Units["armbuilder"], c.Units["corloop"], c.Units["corplain"]
		if g.TurnRate != tc.gw || g.Acceleration != tc.ga || g.BrakeRate != tc.gb || g.MaxVelocity != tc.gv {
			t.Errorf("×%s ×%s ground: w %d a %d b %d v %d, want %d %d %d %d", tc.unit, tc.builder, g.TurnRate, g.Acceleration, g.BrakeRate, g.MaxVelocity, tc.gw, tc.ga, tc.gb, tc.gv)
		}
		if a.TurnRate != tc.aw || a.Acceleration != tc.aa || a.BrakeRate != tc.ab || a.MaxVelocity != tc.av {
			t.Errorf("×%s ×%s air: w %d a %d b %d v %d, want %d %d %d %d", tc.unit, tc.builder, a.TurnRate, a.Acceleration, a.BrakeRate, a.MaxVelocity, tc.aw, tc.aa, tc.ab, tc.av)
		}
		if p.Acceleration != tc.plainAccel {
			t.Errorf("×%s ×%s non-builder accel %d, want %d", tc.unit, tc.builder, p.Acceleration, tc.plainAccel)
		}
	}
	// The 16.16 stores saturate rather than wrap.
	c := builderFixture(t)
	c.Units["armbuilder"].Acceleration, c.Units["armbuilder"].BrakeRate = 1<<30, -7
	if err := c.ApplyMutators(Mutators{UnitSpeed: Factor{4, 1}, BuilderSpeed: Factor{20, 1}}); err != nil {
		t.Fatal(err)
	}
	if g := c.Units["armbuilder"]; g.Acceleration != math.MaxInt32 || g.BrakeRate != -7 {
		t.Fatalf("saturation: accel %d brake %d, want %d and the negative value kept", g.Acceleration, g.BrakeRate, int32(math.MaxInt32))
	}
}

// TestSmartMakersIsASwitch locks the switch's spellings
// (docs/DESIGN_MODS_MUTATORS.md "Smart makers"): it parses only "on" and
// "off", prints "smartMakers=on" in the canonical string, is named alone in
// the player lists, shows On and Off as its value, cycles between its two
// steps, and refuses any multiplier spelling or factor.
func TestSmartMakersIsASwitch(t *testing.T) {
	on, err := ParseMutators(map[string]string{"smartMakers": "on", "buildSpeed": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if on.SmartMakers != SwitchOn() || on.String() != "buildSpeed=2,smartMakers=on" || on.Map()["smartMakers"] != "on" {
		t.Fatalf("on parsed to %+v, %q", on.SmartMakers, on.String())
	}
	if got := strings.Join(on.Describe(), "; "); got != "Build speed ×2; Smart makers" {
		t.Fatalf("Describe = %q", got)
	}
	if got := strings.Join(on.DescribeASCII(), "; "); got != "Build speed x2; Smart makers" {
		t.Fatalf("DescribeASCII = %q", got)
	}
	off, err := ParseMutators(map[string]string{"smartMakers": "off"})
	if err != nil || !off.IsZero() || off.String() != "" {
		t.Fatalf("off parsed to %+v, %v", off, err)
	}
	for _, bad := range []string{"On", "ON", "1", "2", "true", "x2", ""} {
		if _, err := ParseMutators(map[string]string{"smartMakers": bad}); err == nil || !strings.Contains(err.Error(), "off, on") {
			t.Errorf("smartMakers=%q: %v", bad, err)
		}
	}
	if _, err := ParseMutators(map[string]string{"health": "on"}); err == nil {
		t.Error("a scaled mutator accepted the switch's spelling")
	}
	var m Mutators
	if err := m.SetFactor("smartMakers", Factor{3, 1}); err == nil || !m.IsZero() {
		t.Fatal("SetFactor accepted a switch step off its list")
	}
	var info MutatorInfo
	for _, i := range MutatorCatalog() {
		if i.Key == "smartMakers" {
			info = i
		}
	}
	if info.Value(Factor{}) != "Off" || info.Value(SwitchOn()) != "On" || info.ValueASCII(SwitchOn()) != "On" || info.Spell(Factor{}) != "off" {
		t.Fatalf("switch values %q %q %q %q", info.Value(Factor{}), info.Value(SwitchOn()), info.ValueASCII(SwitchOn()), info.Spell(Factor{}))
	}
	if (Factor{}).NextIn(info.Steps) != SwitchOn() || SwitchOn().NextIn(info.Steps) != SwitchOn() || SwitchOn().PrevIn(info.Steps) != (Factor{1, 1}) {
		t.Fatal("the switch must cycle between off and on")
	}
}

// TestSmartMakersMarksOnlySwitchableMakers: the switch changes no authored
// field and marks exactly the definitions the economy's maker arm runs and a
// player can switch — a building with a makesmetal byte, no extractsmetal
// ahead of it, positive upkeep and onoffable [05 R-ECO-01 §2]
// [04 R-ORD-01 §2] — and it moves the catalog identity only when on.
func TestSmartMakersMarksOnlySwitchableMakers(t *testing.T) {
	maker := func(name string, makes int32, use float64) *UnitDef {
		return &UnitDef{UnitName: name, CanonicalKey: name, Hash: "authored-" + name, MakesMetal: makes, EnergyUse: use, OnOffable: true}
	}
	units := map[string]*UnitDef{
		"maker":     maker("maker", 1, 60),
		"moho":      maker("moho", 16, 800),
		"byte":      maker("byte", 256, 60), // the 8-bit store wraps 256 to zero
		"extractor": maker("extractor", 1, 60),
		"fixed":     maker("fixed", 1, 60),
		"mobile":    maker("mobile", 1, 60),
		"solar":     maker("solar", 1, -20),
		"radar":     maker("radar", 0, 20),
	}
	units["extractor"].ExtractsMetal = 0.002
	units["fixed"].OnOffable = false
	units["mobile"].BMCode = 1
	base := &Catalog{Units: units, Hash: "base"}
	off := base.Clone()
	if err := off.ApplyMutators(Mutators{}); err != nil || !reflect.DeepEqual(off, base) {
		t.Fatalf("the zero set changed the catalog: %v", err)
	}
	c := base.Clone()
	if err := c.ApplyMutators(Mutators{SmartMakers: SwitchOn()}); err != nil {
		t.Fatal(err)
	}
	for key, u := range c.Units {
		want := key == "maker" || key == "moho"
		if u.SmartMaker != want {
			t.Errorf("%s SmartMaker = %v, want %v", key, u.SmartMaker, want)
		}
		got := unitValue(u)
		got.SmartMaker = false
		if !reflect.DeepEqual(got, unitValue(base.Units[key])) {
			t.Errorf("%s changed beyond its mark", key)
		}
	}
	if c.Hash == base.Hash || base.Units["maker"].SmartMaker {
		t.Fatal("the switch must move the clone's identity and leave the base unmarked")
	}
}
