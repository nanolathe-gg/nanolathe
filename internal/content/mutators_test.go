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
// splash, and an unlinked shell's 30 is the smallest stock splash.
func mutatorFixture(t *testing.T) *Catalog {
	t.Helper()
	units := map[string]*UnitDef{
		"armbuilder": {UnitName: "ARMBUILDER", WorkerTime: 300, BuildCostMetal: 150, BuildCostEnergy: 1200, BuildTime: 4000, MaxDamage: 800, HealTime: 20, Corpse: "armbuilder_dead",
			SightDistance: 300, RadarDistance: 1000, SonarDistance: 0, RadarDistanceJam: 200, SonarDistanceJam: 0, Weapon1: "gun", ExplodeAs: "blast"},
		"armtwin": {UnitName: "ARMTWIN", WorkerTime: 0, BuildCostMetal: 0, BuildCostEnergy: 7, BuildTime: 100, MaxDamage: 50, Corpse: " ARMBUILDER_DEAD ",
			SightDistance: -5, SonarDistance: 3, SonarDistanceJam: 7, Weapon1: "gun"},
		"corloop":  {UnitName: "CORLOOP", WorkerTime: 1, BuildCostMetal: 3, BuildCostEnergy: 5, BuildTime: 10, MaxDamage: 10, Corpse: "loop_a", SightDistance: 1, SelfDestructAs: "dud"},
		"corplain": {UnitName: "CORPLAIN", WorkerTime: 65, BuildCostMetal: 1, BuildCostEnergy: 1, BuildTime: 10, MaxDamage: 10},
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
	}
	for key, f := range features {
		f.CanonicalKey = key
		f.Hash = "authored-" + key
	}
	if err := LinkFeatureSuccessors(features); err != nil {
		t.Fatal(err)
	}
	weapons := map[string]*WeaponDef{
		"gun":   {ID: 1, DamageDefault: 45, Damage: map[string]int32{"ARMBUILDER": 90, "corloop": -3, "CorLoop": 7}, AreaOfEffect: 16},
		"blast": {ID: 2, DamageDefault: 70000, AreaOfEffect: 400},
		"dud":   {ID: 3},
		"shell": {ID: 4, DamageDefault: 20, AreaOfEffect: 30},
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
	for _, m := range []Mutators{{}, {BuildSpeed: one, BuildCost: one, Health: one, Damage: one, Sight: one, Radar: one}} {
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
// sensor and jamming distances (P3). Per-definition hashes stay
// the authored identities (§6.6).
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
// carries the identity tag, which moved to "mutators/2" when Build speed's
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
	if want := HashDefinition([]byte("mutators/2\n" + all.String() + "\n")); all.Digest() != want {
		t.Fatalf("digest %s is not the mutators/2 hash of the canonical set", all.Digest())
	}
	if all.Digest() == HashDefinition([]byte("mutators/1\n"+all.String()+"\n")) || all.Digest() == (Mutators{}).Digest() {
		t.Fatal("digest must carry the current identity tag and follow the set")
	}
}

// TestParseMutatorsRoundTrip walks every key through every step via Map,
// String and ParseMutators, and checks the canonical spellings and the error
// cases.
func TestParseMutatorsRoundTrip(t *testing.T) {
	for _, info := range MutatorCatalog() {
		for _, step := range MutatorSteps {
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
	if got := strings.Join(keys, ","); got != "buildSpeed,buildCost,health,damage,areaOfEffect,sight,radar" {
		t.Fatalf("catalog order = %s", got)
	}
	if got := strings.Join(groups, ","); got != "Economy,Economy,Combat,Combat,Combat,Vision,Vision" {
		t.Fatalf("catalog groups = %s", got)
	}
	infos[0].Label = "changed"
	if MutatorCatalog()[0].Label == "changed" {
		t.Fatal("MutatorCatalog must return a copy")
	}

	var m Mutators
	for _, info := range MutatorCatalog() {
		if f, ok := m.Factor(info.Key); !ok || f != (Factor{}) {
			t.Fatalf("Factor(%s) on the zero set = %+v, %v", info.Key, f, ok)
		}
		if err := m.SetFactor(info.Key, Factor{3, 4}); err != nil {
			t.Fatal(err)
		}
		if f, _ := m.Factor(info.Key); f != (Factor{3, 4}) || !strings.Contains(m.String(), info.Key+"=0.75") {
			t.Fatalf("SetFactor(%s) did not store 3/4: %q", info.Key, m.String())
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
}

// TestFactorSpellingAndCycling covers the UI helpers: canonical spelling,
// labels, and cycling through the step list with clamping at both ends.
func TestFactorSpellingAndCycling(t *testing.T) {
	if (Factor{}).String() != "1" || (Factor{}).Label() != "×1" || (Factor{1, 2}).Label() != "×0.5" || (Factor{3, 2}).String() != "1.5" || (Factor{1, 4}).String() != "0.25" {
		t.Fatal("identity and decimal spellings")
	}
	f := MutatorSteps[0]
	for i := 1; i < len(MutatorSteps); i++ {
		f = f.Next()
		if f != MutatorSteps[i] {
			t.Fatalf("Next walk reached %s at %d, want %s", f, i, MutatorSteps[i])
		}
	}
	if f.Next() != f {
		t.Fatal("Next must clamp at the last step")
	}
	for i := len(MutatorSteps) - 2; i >= 0; i-- {
		f = f.Prev()
		if f != MutatorSteps[i] {
			t.Fatalf("Prev walk reached %s at %d, want %s", f, i, MutatorSteps[i])
		}
	}
	if f.Prev() != f {
		t.Fatal("Prev must clamp at the first step")
	}
	if (Factor{}).Next() != (Factor{3, 2}) || (Factor{}).Prev() != (Factor{3, 4}) {
		t.Fatal("the zero value cycles as 1/1")
	}
	for _, step := range MutatorSteps {
		parsed, err := ParseFactor(step.String())
		if err != nil || parsed != step {
			t.Fatalf("ParseFactor(%q) = %+v, %v", step.String(), parsed, err)
		}
	}
}
