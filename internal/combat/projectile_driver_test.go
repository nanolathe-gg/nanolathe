package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// driverWeapon is a complete projectile fixture definition for the live
// driver tests. Its values only give the test a one-tick direct flight; the
// assertions lock ordering and contact decisions rather than content tuning.
func driverWeapon(unitsOnly bool) *content.WeaponDef {
	return &content.WeaponDef{
		ID:             91,
		LineOfSight:    true,
		Range:          100,
		WeaponVelocity: int32(numeric.FixedFromInt(1)),
		AreaOfEffect:   64,
		DamageDefault:  20,
		UnitsOnly:      unitsOnly,
	}
}

func driverCatalog(w *content.WeaponDef) *content.Catalog {
	c := &content.Catalog{Weapons: map[string]*content.WeaponDef{"driver": w}}
	c.RebuildWeaponIndex()
	return c
}

// TestTickProjectilesNonBounceGroundContact covers the live ladder rather
// than its helper: a direct projectile moves below the signed terrain minimum
// and, when GroundBounce is clear, central impact emits effects and applies
// its area packet [06 §8.1][06 §9.1][06 §13.2].
func TestTickProjectilesNonBounceGroundContact(t *testing.T) {
	var svc Service
	bindFixtureControlBytes(&svc)
	w, terrain := newContactFixture(t)
	const cx, cz = 8, 8
	terrain.PlotAt(cx, cz).SetMinHeight(20)
	terrain.PlotAt(cx, cz).SetMaxHeight(20)
	targetHandle, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx+1), numeric.FixedFromInt(20), cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	target := w.Unit(targetHandle)
	stampGroundRect(terrain, cx+1, cz, 1, 1, targetHandle)

	weapon := driverWeapon(false)
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	p := &svc.Records[int(h)-1]
	p.WeaponID = weapon.ID
	p.ShooterSide = 0
	p.Pos = Vec3{X: cellCentre(cx), Y: numeric.FixedFromInt(21), Z: cellCentre(cz)}
	p.Velocity.Y = numeric.FixedFromInt(-2)
	p.ExpiryTick = 10
	var events []Event
	svc.Events = func(ev Event) { events = append(events, ev) }

	svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
	if svc.Count() != 0 {
		t.Fatalf("non-bouncing terrain contact left %d projectile records live", svc.Count())
	}
	if target.Health >= target.MaxHealth {
		t.Fatalf("terrain impact did not apply area damage: health=%d max=%d", target.Health, target.MaxHealth)
	}
	foundImpact, foundExplosion := false, false
	for _, ev := range events {
		foundImpact = foundImpact || ev.Kind == EventProjectileImpact
		foundExplosion = foundExplosion || ev.Kind == EventExplosion
	}
	if !foundImpact || !foundExplosion {
		t.Fatalf("terrain impact events: impact=%v explosion=%v events=%#v", foundImpact, foundExplosion, events)
	}
}

// TestTickProjectilesUnitsOnlyMissSkipsGroundContact locks the ladder's
// units-only return through TickProjectiles: after both local occupancy words
// miss, a below-ground projectile remains live and produces no terrain impact
// [06 §8.1].
func TestTickProjectilesUnitsOnlyMissSkipsGroundContact(t *testing.T) {
	var svc Service
	w, terrain := newContactFixture(t)
	const cx, cz = 11, 11
	terrain.PlotAt(cx, cz).SetMinHeight(20)
	terrain.PlotAt(cx, cz).SetMaxHeight(20)
	weapon := driverWeapon(true)
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	p := &svc.Records[int(h)-1]
	p.WeaponID = weapon.ID
	p.ShooterSide = 0
	p.Pos = Vec3{X: cellCentre(cx), Y: numeric.FixedFromInt(21), Z: cellCentre(cz)}
	p.Velocity.Y = numeric.FixedFromInt(-2)
	p.ExpiryTick = 10
	var events []Event
	svc.Events = func(ev Event) { events = append(events, ev) }

	svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
	if svc.Count() != 1 || !svc.Alive(h) {
		t.Fatalf("units-only terrain miss retired projectile: count=%d alive=%v", svc.Count(), svc.Alive(h))
	}
	if len(events) != 0 {
		t.Fatalf("units-only terrain miss emitted impact events: %#v", events)
	}
}

// TestTickProjectilesProximityImpactSweepsLiveVictims enters through the
// driver's projectile-link contact. The interceptor explodes before later
// cell contacts, and its sweep resolves each currently-live victim through
// the same central impact path [06 §8.1][06 §11.2].
func TestTickProjectilesProximityImpactSweepsLiveVictims(t *testing.T) {
	var svc Service
	interceptor := driverWeapon(false)
	interceptor.Interceptor = true
	incoming := driverWeapon(false)
	incoming.ID = 92
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{
		"interceptor": interceptor,
		"incoming":    incoming,
	}}
	cat.RebuildWeaponIndex()
	hInterceptor, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve interceptor")
	}
	hIncoming, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve incoming projectile")
	}
	pos := Vec3{X: numeric.FixedFromInt(40), Y: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(40)}
	pInterceptor := &svc.Records[int(hInterceptor)-1]
	pInterceptor.WeaponID = interceptor.ID
	pInterceptor.TargetProjectile = hIncoming
	pInterceptor.Pos = pos
	pInterceptor.ExpiryTick = 10
	pIncoming := &svc.Records[int(hIncoming)-1]
	pIncoming.WeaponID = incoming.ID
	pIncoming.Pos = pos
	pIncoming.ExpiryTick = 10
	var impacts []Event
	svc.Events = func(ev Event) {
		if ev.Kind == EventProjectileImpact {
			impacts = append(impacts, ev)
		}
	}

	svc.TickProjectiles(1, nil, nil, nil, nil, nil, nil, cat, nil, nil)
	if svc.Count() != 0 {
		t.Fatalf("interceptor sweep left %d projectile records live", svc.Count())
	}
	if len(impacts) != 2 {
		t.Fatalf("central proximity impact plus live victim sweep emitted %d impacts, want 2", len(impacts))
	}
}
