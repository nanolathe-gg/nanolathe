package aikit

import (
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Role is a bit set describing what a unit definition is for, derived from
// its authored keys alone (never from a stock unit name), so the same brain
// plays any content set that authors the ordinary keys.
type Role uint32

const (
	RoleCommander  Role = 1 << iota
	RoleBuilder         // mobile unit that can construct buildings
	RoleFactory         // immobile unit that produces units
	RoleExtractor       // metal extractor
	RoleEnergy          // energy producer (solar, wind, tidal, fusion)
	RoleMetalMaker      // converts energy to metal
	RoleStorage         // resource storage
	RoleRadar
	RoleSonar
	RoleJammer
	RoleDefense // immobile and armed
	RoleCombat  // mobile and armed, not a builder
	RoleAir
	RoleNaval
	RoleHover
	RoleAmphibious
	RoleScout
	RoleAntiAir
	RoleArtillery
	RoleTransport
	RoleBomber
	RoleFighter
	RoleKamikaze
	RoleMobile
	RoleAssist // mobile builder whose products are all units (nano tower style) or immobile nano
)

// Has reports whether every bit of r2 is set.
func (r Role) Has(r2 Role) bool { return r&r2 == r2 }

// Any reports whether any bit of r2 is set.
func (r Role) Any(r2 Role) bool { return r&r2 != 0 }

// UnitInfo is the immutable per-definition summary a brain reasons with.
// Every number is an integer: costs in metal and energy, rates per second.
type UnitInfo struct {
	Index int32 // position in Table.Units
	Key   string
	Def   *content.UnitDef
	Side  string
	Role  Role

	Metal, Energy int32 // build cost
	BuildTime     int32 // authored build time (work units)
	Value         int32 // metal-equivalent cost: metal + energy/EnergyPerMetal
	HP            int32
	DPS           int32 // summed default damage per second of every active weapon that fires at units (not interceptors)
	AirDPS        int32 // the part of DPS whose weapons can engage aircraft
	WaterDPS      int32 // the part of DPS from water-only weapons (torpedoes): no use against land
	StunDPS       int32 // the part of DPS from paralyzer weapons: stuns, never kills
	Range         int32 // longest range of those weapons, world units
	Speed         int32 // world units per second
	Sight         int32 // world units
	Radar         int32
	BuildPower    int32 // workertime
	BuildRange    int32 // builddistance
	MetalMake     int32 // metal per second ×100 (extraction multiplier ×100 for extractors)
	EnergyMake    int32 // steady energy per second (excludes wind and tidal)
	WindGen       int32 // wind output at full wind: output = WindGen × Obs.WindPermille / 1000
	TidalGen      int32 // tidal output at full tide: output = TidalGen × MapInfo.TidalPermille / 1000
	EnergyUse     int32 // energy per second consumed while active
	MetalStore    int32
	EnergyStore   int32
	FootX, FootZ  int32
	Depth         int32 // build-tree depth from a commander (0 = commander)

	Builds []*UnitInfo // products, authored order, filtered by the session's build rules
}

// EnergyPerMetal is the exchange rate used for Value. It is a planning
// convenience, not a game rule: metal makers convert at roughly this rate.
const EnergyPerMetal = 60

// SurfaceDPS is the damage per second a unit can bring against targets on
// land (everything but water-only weapons).
func (u *UnitInfo) SurfaceDPS() int32 { return u.DPS - u.WaterDPS }

// KillDPS is the part of DPS that destroys (all but paralyzer damage).
func (u *UnitInfo) KillDPS() int32 { return u.DPS - u.StunDPS }

// Strength is the Lanchester-style fighting product of a unit: damage it
// deals per second times the damage it can absorb, scaled down to stay well
// inside int64 when summed over armies.
func (u *UnitInfo) Strength() int64 { return int64(u.DPS) * int64(u.HP) / 16 }

// Table is the catalog summarized for one session's build rules.
type Table struct {
	Units []*UnitInfo
	byKey map[string]*UnitInfo // lookup only; never ranged in a decision path (I1)
	byDef map[*content.UnitDef]*UnitInfo
}

// Lookup returns the info for a canonical key.
func (t *Table) Lookup(key string) *UnitInfo {
	if t == nil {
		return nil
	}
	return t.byKey[content.CanonicalKey(key)]
}

// Of returns the info for a definition pointer.
func (t *Table) Of(def *content.UnitDef) *UnitInfo {
	if t == nil || def == nil {
		return nil
	}
	return t.byDef[def]
}

// BuildTable summarizes cat under rules. The walk is in sorted key order and
// the result is deterministic for a given catalog and rule set.
func BuildTable(cat *content.Catalog, rules construction.Rules) *Table {
	t := &Table{byKey: map[string]*UnitInfo{}, byDef: map[*content.UnitDef]*UnitInfo{}}
	if cat == nil {
		return t
	}
	for _, key := range cat.SortedUnitKeys() {
		def, ok := cat.Unit(key)
		if !ok || def == nil {
			continue
		}
		if _, dup := t.byDef[def]; dup {
			continue
		}
		info := summarize(key, def)
		info.Index = int32(len(t.Units))
		t.Units = append(t.Units, info)
		t.byKey[content.CanonicalKey(key)] = info
		t.byDef[def] = info
	}
	for _, info := range t.Units {
		menu := cat.BuildMenus[content.CanonicalKey(info.Key)]
		for _, product := range construction.BuildProducts(rules, menu) {
			if p := t.byKey[content.CanonicalKey(product)]; p != nil {
				info.Builds = append(info.Builds, p)
			}
		}
		classifyBuilder(info)
		// Factories, towers and the like author a little storage; they are
		// not storage buildings.
		if info.Role.Any(RoleFactory | RoleDefense | RoleRadar | RoleSonar | RoleCommander | RoleBuilder | RoleMobile) {
			info.Role &^= RoleStorage
		}
	}
	t.computeAntiAir()
	t.computeDepth()
	// Anti-air credited from the damage table is known only now: an armed
	// aircraft that is mostly anti-air is a fighter, not a bomber — unless
	// it drops bombs, which makes it a bomber whatever else it carries (the
	// tech-2 bombers also carry an anti-air gun that outweighs their bombs).
	for _, u := range t.Units {
		if u.Role.Has(RoleAir|RoleCombat) && !dropsBombs(u.Def) && u.AirDPS > 0 && u.AirDPS*2 >= u.DPS {
			u.Role = u.Role&^RoleBomber | RoleFighter
		}
	}
	return t
}

// dropsBombs reports whether a definition carries an active dropped weapon
// (a bomb). A dropped weapon never acquires a target on its own [06 §3.2]:
// it falls on what the aircraft is sent to attack, so its carrier is a
// bomber, and a bomb's own damage table never counts as anti-air.
func dropsBombs(def *content.UnitDef) bool {
	if def == nil {
		return false
	}
	for _, w := range [...]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
		if weaponActive(w) && w.Dropped {
			return true
		}
	}
	return false
}

// computeAntiAir credits anti-air fire from the damage table: stock
// anti-air missiles author extra damage against named aircraft rather than
// setting toairweapon, so a weapon with a damage entry naming a flying unit
// is counted as able to engage aircraft at that damage.
func (t *Table) computeAntiAir() {
	for _, u := range t.Units {
		def := u.Def
		for _, w := range [...]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
			// A bomb's damage table may name aircraft, but a dropped weapon
			// cannot engage them (dropsBombs); an interceptor engages only
			// projectiles (firesAtUnits).
			if !firesAtUnits(w) || w.ToAirWeapon || w.Dropped || len(w.Damage) == 0 {
				continue
			}
			var airDmg int64
			for _, key := range w.DamageKeysSorted() {
				target := t.byKey[content.CanonicalKey(key)]
				if target == nil || target.Def == nil || !target.Def.CanFly {
					continue
				}
				if d := int64(w.Damage[key]); d > airDmg {
					airDmg = d
				}
			}
			if airDmg <= int64(w.DamageDefault) || airDmg <= 0 {
				continue
			}
			burst := int64(w.Burst)
			if burst < 1 {
				burst = 1
			}
			reload := int64(w.ReloadTime)
			if reload < 1 {
				reload = 1
			}
			u.AirDPS += int32(airDmg * burst * 30 / reload)
			u.Role |= RoleAntiAir
		}
	}
}

func weaponActive(w *content.WeaponDef) bool {
	return w != nil && !content.IsWeaponInactive(w)
}

// firesAtUnits reports whether an active weapon ever takes a unit target. An
// interceptor (a stock anti-nuke) never does: when its slot re-acquires it
// scans projectiles and aims at the winning one's position [06 §3.2], so it
// adds nothing to what a unit's fire does to other units — and its authored
// range (tens of thousands of world units, the coverage it intercepts over)
// is not a reach into the enemy base.
func firesAtUnits(w *content.WeaponDef) bool {
	return weaponActive(w) && !w.Interceptor
}

// intercepts reports whether a definition carries an active interceptor.
func intercepts(def *content.UnitDef) bool {
	for _, w := range [...]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
		if weaponActive(w) && w.Interceptor {
			return true
		}
	}
	return false
}

func summarize(key string, def *content.UnitDef) *UnitInfo {
	u := &UnitInfo{Key: key, Def: def, Side: def.Side}
	u.Metal = int32(def.BuildCostMetal)
	u.Energy = int32(def.BuildCostEnergy)
	u.BuildTime = def.BuildTime
	u.Value = u.Metal + u.Energy/EnergyPerMetal
	u.HP = def.MaxDamage
	u.Speed = int32((int64(def.MaxVelocity) * 30) >> 16)
	u.Sight = def.SightDistance
	u.Radar = def.RadarDistance
	u.BuildPower = def.WorkerTime
	u.BuildRange = def.BuildDistance
	u.MetalMake = int32(def.MetalMake * 100)
	if def.ExtractsMetal > 0 {
		u.MetalMake = int32(def.ExtractsMetal * 100000) // per footprint metal unit, ×1e5
	}
	// Net energy: authored production minus upkeep. A negative energyuse is
	// how solar collectors author their output.
	u.EnergyMake = int32(def.EnergyMake - def.EnergyUse)
	if u.EnergyMake < 0 {
		u.EnergyMake = 0
	}
	u.WindGen = int32(def.WindGenerator)
	u.TidalGen = int32(def.TidalGenerator)
	u.EnergyUse = int32(def.EnergyUse)
	if u.EnergyUse < 0 {
		u.EnergyUse = 0
	}
	u.MetalStore = int32(def.MetalStorage)
	u.EnergyStore = int32(def.EnergyStorage)
	u.FootX, u.FootZ = def.FootprintX, def.FootprintZ

	mobile := def.BMCode != 0 && def.CanMove
	if mobile {
		u.Role |= RoleMobile
	}
	for _, w := range [...]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
		if !firesAtUnits(w) {
			continue
		}
		dmg := int64(w.DamageDefault)
		// A manually fired super-weapon (the commander's disintegrator
		// authors a damage in the thousands) is not sustained fire.
		if dmg <= 0 || dmg >= 4000 {
			continue
		}
		burst := int64(w.Burst)
		if burst < 1 {
			burst = 1
		}
		reload := int64(w.ReloadTime)
		if reload < 1 {
			reload = 1
		}
		if w.Dropped && reload < 90 {
			reload = 90 // bombs fall in passes, not as sustained fire
		}
		dps := int32(dmg * burst * 30 / reload)
		u.DPS += dps
		if w.WaterWeapon {
			u.WaterDPS += dps
		}
		if w.Paralyzer {
			u.StunDPS += dps
		}
		if w.ToAirWeapon {
			u.AirDPS += dps
			u.Role |= RoleAntiAir
		}
		if w.Range > u.Range && w.Range < 32767 {
			u.Range = w.Range
		}
		if w.Ballistic && w.Range >= 700 {
			u.Role |= RoleArtillery
		}
	}
	switch {
	case def.Commander:
		u.Role |= RoleCommander
	case def.ExtractsMetal > 0:
		u.Role |= RoleExtractor
	}
	if (u.EnergyMake > 0 || u.WindGen > 0 || u.TidalGen > 0) && !def.Commander && !mobile && def.RadarDistance == 0 && def.SonarDistance == 0 && u.DPS == 0 {
		u.Role |= RoleEnergy
	}
	if def.MakesMetal > 0 || (def.MetalMake > 0 && def.EnergyUse > 0 && !mobile && !def.Commander) {
		u.Role |= RoleMetalMaker
	}
	if (def.MetalStorage > 0 || def.EnergyStorage > 0) && !def.Commander && !mobile && u.Role&(RoleEnergy|RoleExtractor|RoleMetalMaker) == 0 {
		u.Role |= RoleStorage
	}
	if def.RadarDistance > 0 && !def.Commander {
		u.Role |= RoleRadar
	}
	if def.SonarDistance > 0 && !def.Commander {
		u.Role |= RoleSonar
	}
	if def.RadarDistanceJam > 0 || def.SonarDistanceJam > 0 {
		u.Role |= RoleJammer
	}
	if def.CanFly {
		u.Role |= RoleAir
	}
	if def.CanHover {
		u.Role |= RoleHover
	}
	if def.Amphibious {
		u.Role |= RoleAmphibious
	}
	if mobile && !def.CanFly && !def.CanHover && def.MinWaterDepth > 0 {
		u.Role |= RoleNaval
	}
	if def.TransportCapacity > 0 {
		u.Role |= RoleTransport
	}
	if def.Kamikaze {
		u.Role |= RoleKamikaze
	}
	armed := u.DPS > 0
	if armed && !mobile && def.BMCode == 0 {
		u.Role |= RoleDefense
	}
	if armed && mobile && !def.Commander && !def.Builder {
		u.Role |= RoleCombat
		if def.CanFly {
			if u.AirDPS > 0 && u.AirDPS*2 >= u.DPS && !dropsBombs(def) {
				u.Role |= RoleFighter
			} else {
				u.Role |= RoleBomber
			}
		}
	}
	if mobile && !def.Builder && !def.Commander && u.Role&(RoleTransport) == 0 {
		// A scout is fast and cheap for its speed class, or an unarmed
		// mobile whose only purpose can be looking — not a mobile anti-nuke,
		// whose interceptor is its purpose.
		if (!armed && !intercepts(def)) || (u.Metal <= 70 && u.Speed >= 60) {
			u.Role |= RoleScout
		}
	}
	return u
}

// classifyBuilder runs after products are linked: a builder role needs a
// building among its products, a factory needs a mobile product.
func classifyBuilder(u *UnitInfo) {
	if len(u.Builds) == 0 || u.Def == nil || !u.Def.Builder {
		return
	}
	buildsBuilding, buildsMobile := false, false
	for _, p := range u.Builds {
		if p.Def.BMCode == 0 {
			buildsBuilding = true
		} else {
			buildsMobile = true
		}
	}
	mobile := u.Role.Has(RoleMobile)
	switch {
	case mobile && buildsBuilding:
		u.Role |= RoleBuilder
	case !mobile && buildsMobile:
		u.Role |= RoleFactory
	}
}

// computeDepth labels each definition with its distance from a commander in
// the build graph: a breadth-first walk in table order, so the answer is the
// same on every run.
func (t *Table) computeDepth() {
	for _, u := range t.Units {
		u.Depth = -1
	}
	var frontier []*UnitInfo
	for _, u := range t.Units {
		if u.Role.Has(RoleCommander) {
			u.Depth = 0
			frontier = append(frontier, u)
		}
	}
	for len(frontier) > 0 {
		var next []*UnitInfo
		for _, u := range frontier {
			for _, p := range u.Builds {
				if p.Depth < 0 {
					p.Depth = u.Depth + 1
					next = append(next, p)
				}
			}
		}
		frontier = next
	}
}

// Buildable reports whether builder can produce p.
func (u *UnitInfo) Buildable(p *UnitInfo) bool {
	if u == nil || p == nil {
		return false
	}
	for _, b := range u.Builds {
		if b == p {
			return true
		}
	}
	return false
}
