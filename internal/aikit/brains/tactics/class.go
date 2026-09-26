package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Unit kinds by how the army can use them. They are derived once per
// definition from authored keys (movement flags, water depths, weapon
// flags), never from a unit name, so any content set that authors the
// ordinary keys is operated the same way.
const (
	ukOther    uint8 = iota
	ukGround         // walks on land; wades only shallow water
	ukHover          // hovers over land and water
	ukAmphib         // walks on land and along the sea floor
	ukNaval          // ships and submarines: water deep enough for its draught
	ukFighter        // aircraft armed mainly against aircraft
	ukBomber         // aircraft that drop bombs in passes
	ukGunship        // aircraft with direct fire at the ground
	ukTorpedo        // aircraft whose only weapons are torpedoes
	ukAirScout       // unarmed aircraft (scouts, radar planes)
)

var kindNames = [...]string{"other", "ground", "hover", "amphib", "naval", "fighter", "bomber", "gunship", "torpedo", "airscout"}

// uclass is the army's static view of one unit definition.
type uclass struct {
	kind uint8
	// surfDPS is the damage per second of weapons that can hit a target on
	// land or on the water surface (torpedoes and anti-air-only weapons
	// excluded). A submarine has none: it cannot shell a coast.
	surfDPS int32
	// torp is set when the unit carries a torpedo (it can hit submarines
	// and ships, nothing on land).
	torp bool
}

func (c *uclass) air() bool { return c.kind >= ukFighter }

// anyTerrain reports a surface unit that reaches both land and water.
func (c *uclass) anyTerrain() bool { return c.kind == ukHover || c.kind == ukAmphib }

// classify fills the per-definition table. sea is the map's sea level: a
// walker whose authored maximum water depth reaches it can cross any water
// on this map (the sea floor is never lower than height zero).
func classify(t *aikit.Table, sea int32) []uclass {
	out := make([]uclass, len(t.Units))
	for i, u := range t.Units {
		out[i] = classOf(u, sea)
	}
	return out
}

func classOf(u *aikit.UnitInfo, sea int32) uclass {
	var c uclass
	d := u.Def
	if d == nil || !u.Role.Has(aikit.RoleMobile) {
		return c
	}
	var dropped, direct, airOnly, torpOnly bool
	torpOnly = true
	for _, w := range [...]*content.WeaponDef{d.Weapon1Def, d.Weapon2Def, d.Weapon3Def} {
		if w == nil || content.IsWeaponInactive(w) || w.Interceptor {
			continue // an anti-missile interceptor never fires at units
		}
		dmg := int64(w.DamageDefault)
		if dmg <= 0 || dmg >= 4000 {
			continue // manual super-weapons are not sustained fire
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
			reload = 90
		}
		dps := int32(dmg * burst * 30 / reload)
		switch {
		case w.WaterWeapon:
			c.torp = true
		case w.ToAirWeapon:
			airOnly = true
			torpOnly = false
		default:
			c.surfDPS += dps
			torpOnly = false
			if w.Dropped {
				dropped = true
			} else {
				direct = true
			}
		}
	}
	armed := u.DPS > 0 || u.AirDPS > 0
	switch {
	case d.CanFly:
		switch {
		case u.Role.Any(aikit.RoleBuilder | aikit.RoleTransport):
			c.kind = ukOther
		case !armed:
			c.kind = ukAirScout
		case dropped:
			c.kind = ukBomber
		case u.AirDPS > 0 && u.AirDPS >= u.DPS:
			c.kind = ukFighter
		case c.torp && torpOnly:
			c.kind = ukTorpedo
		case direct:
			c.kind = ukGunship
		case airOnly:
			c.kind = ukFighter
		}
	case u.Role.Has(aikit.RoleNaval):
		c.kind = ukNaval
	case d.CanHover || (d.MaxWaterDepth >= 10000 && d.MinWaterDepth <= 0):
		c.kind = ukHover
	case d.Amphibious || (sea > 0 && d.MaxWaterDepth >= sea):
		c.kind = ukAmphib
	default:
		c.kind = ukGround
	}
	return c
}

// cls returns the class of a definition.
func (a *Army) cls(info *aikit.UnitInfo) *uclass {
	return &a.classes[info.Index]
}

// Loss accounting buckets (Report only).
const (
	bkGround = iota
	bkHover
	bkNaval
	bkAir
	bkBuilder
	bkCommander
	bkBuilding
	numBuckets
)

var bucketNames = [numBuckets]string{"ground", "hover", "naval", "air", "builder", "commander", "building"}

func (a *Army) bucket(info *aikit.UnitInfo) int {
	r := info.Role
	switch {
	case r.Has(aikit.RoleCommander):
		return bkCommander
	case !r.Has(aikit.RoleMobile):
		return bkBuilding
	case r.Has(aikit.RoleBuilder):
		return bkBuilder
	}
	switch a.cls(info).kind {
	case ukNaval:
		return bkNaval
	case ukHover, ukAmphib:
		return bkHover
	case ukGround, ukOther:
		if r.Has(aikit.RoleAir) {
			return bkAir
		}
		return bkGround
	}
	return bkAir
}
