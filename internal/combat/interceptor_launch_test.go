package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// WU-19-234 wired the two halves of the interceptor launch that were missing:
// the automatic scan at the autonomous scan's per-slot position, and the
// fire-time rescan on the vertical-launch executor. These tests lock the gates
// that decide whether a shot happens at all, which is where the whole feature
// silently switched off before.

// interceptorTestCatalog builds a catalog whose weapon index answers for the
// two records these tests use.
func interceptorTestCatalog(t *testing.T, defs ...*content.WeaponDef) *content.Catalog {
	t.Helper()
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{}}
	for i, d := range defs {
		if d.CanonicalKey == "" {
			d.CanonicalKey = content.CanonicalKey(string(rune('a' + i)))
		}
		cat.Weapons[d.CanonicalKey] = d
	}
	cat.RebuildWeaponIndex()
	return cat
}

// TestInterceptorScanRequiresAmmunition locks the first gate of [06 §11.2]: the
// automatic scan "requires a nonzero slot ammunition byte" before it walks the
// projectile prefix at all. With the byte at zero an anti-nuke silo that has
// not finished a round must acquire nothing, so it never reaches the fire path
// and never spends a shot it does not have.
func TestInterceptorScanRequiresAmmunition(t *testing.T) {
	incoming := weaponForStockpile(300, 10, 0, 0, true, false, true, 0, 0, 0)
	interceptor := weaponForStockpile(301, 10, 0, 100, true, true, false, 0, 0, 0)
	cat := interceptorTestCatalog(t, incoming, interceptor)

	var svc Service
	h, _ := svc.Reserve()
	svc.Records[int(h)-1] = Projectile{
		WeaponID:    incoming.ID,
		Pos:         Vec3{X: fixedI(20), Z: fixedI(20)},
		TargetPos:   Vec3{X: fixedI(5), Z: fixedI(5)}, // the scan metric
		ShooterSide: 2,
	}
	u := &units.Unit{Owner: 1, X: fixedI(5), Z: fixedI(5)}

	empty := &units.Slot{Weapon: interceptor, Ammo: 0}
	if _, _, ok := interceptorScanCandidate(&svc, u, empty, cat); ok {
		t.Fatal("the scan ran with an empty stockpile; a nonzero ammunition byte is its first gate [06 §11.2]")
	}
	armed := &units.Slot{Weapon: interceptor, Ammo: 1}
	got, storePos, ok := interceptorScanCandidate(&svc, u, armed, cat)
	if !ok || got != h {
		t.Fatalf("the scan found %d (ok=%v) with one round in stock, want the incoming record %d [06 §11.2]", got, ok, h)
	}
	// The two positions play different roles and must not be conflated: the
	// scan matched on the STORED AIM POINT (5,5) and the slot store is the
	// candidate's CURRENT position (20,20) [06 §11.2].
	if storePos.X.Raw() != fixedI(20).Raw() || storePos.Z.Raw() != fixedI(20).Raw() {
		t.Fatalf("the slot store is (%d,%d), want the candidate's current position (20,20) rather than its aim point [06 §11.2]",
			storePos.X.Int(), storePos.Z.Int())
	}
}

// TestInterceptorScanIgnoresNonInterceptorWeapons keeps the scan off every
// other weapon: it is "chosen by the slot weapon's interceptor flag"
// [06 §11.2], so a stockpiled nuclear missile must go through ordinary
// acquisition and not through this one.
func TestInterceptorScanIgnoresNonInterceptorWeapons(t *testing.T) {
	incoming := weaponForStockpile(300, 10, 0, 0, true, false, true, 0, 0, 0)
	nuke := weaponForStockpile(302, 10, 0, 100, true, false, false, 0, 0, 0)
	cat := interceptorTestCatalog(t, incoming, nuke)

	var svc Service
	h, _ := svc.Reserve()
	svc.Records[int(h)-1] = Projectile{WeaponID: incoming.ID, TargetPos: Vec3{}, ShooterSide: 2}
	u := &units.Unit{Owner: 1}
	slot := &units.Slot{Weapon: nuke, Ammo: 1}
	if _, _, ok := interceptorScanCandidate(&svc, u, slot, cat); ok {
		t.Fatal("a weapon without the interceptor flag ran the interceptor scan [06 §11.2]")
	}
}

// TestInterceptorRescanPortIsNilForOrdinaryVerticalLaunch is the regression
// that disarmed every nuke silo in the game the first time this was wired.
//
// TryFire reads a ZERO return from InterceptorRescan as "the rescan found no
// candidate", which leaves the shot pending and makes no record [06 §11.2]. A
// nuclear missile is `vlaunch` and not `interceptor`: bind it a closure that
// answers zero and it can never fire. The gate has to be the nil port, not a
// test inside the closure.
func TestInterceptorRescanPortIsNilForOrdinaryVerticalLaunch(t *testing.T) {
	interceptor := weaponForStockpile(301, 10, 0, 100, true, true, false, 0, 0, 0)
	nuke := weaponForStockpile(302, 10, 0, 0, true, false, false, 0, 0, 0)
	cat := interceptorTestCatalog(t, interceptor, nuke)
	var svc Service
	u := &units.Unit{Owner: 1}

	if port := interceptorRescanPort(&svc, u, nuke, cat); port != nil {
		t.Fatal("an ordinary vertical launch was given a rescan port; a zero return from it refuses the shot [06 §11.2]")
	}
	if port := interceptorRescanPort(&svc, u, interceptor, cat); port == nil {
		t.Fatal("an interceptor weapon was given no rescan port; the vertical-launch executor performs it before firing [06 §4.4][06 §11.2]")
	}
}

// TestVerticalLaunchStoresTheRescanLink locks the two things the executor does
// with the rescan's answer: a zero leaves the shot pending with no record
// [06 §11.2], and a handle becomes the new projectile's matched-projectile
// link, written after the family dispatch because the common initializer clears
// it first [06 §6.6][06 §4.1].
func TestVerticalLaunchStoresTheRescanLink(t *testing.T) {
	interceptor := weaponForStockpile(301, 10, 0, 100, true, true, false, 0, 0, 0)
	interceptor.WeaponVelocity = 1 << 16

	newSpawner := func(link pool.Handle, calls *int) (*Service, FirePorts, *Slot) {
		svc := &Service{}
		slot := &Slot{Weapon: interceptor, Ammo: 1, MuzzlePiece: -1}
		ports := FirePorts{
			ShooterSide: 1,
			Origin:      Vec3{},
			InterceptorRescan: func(int, *Slot) pool.Handle {
				*calls++
				return link
			},
		}
		return svc, ports, slot
	}

	// A miss: no record, and the rescan still ran (it is retained work).
	calls := 0
	svc, ports, slot := newSpawner(0, &calls)
	if h, ok := TryFire(svc, slot, 0, Target{Kind: TargetPoint, X: fixedI(4), Z: fixedI(4)}, 1, ports); ok || h != 0 {
		t.Fatalf("a rescan that found nothing produced projectile %d (ok=%v); the shot stays pending [06 §11.2]", h, ok)
	}
	if calls != 1 {
		t.Fatalf("the rescan ran %d times, want exactly once before the reservation [06 §4.4]", calls)
	}
	if svc.Count() != 0 {
		t.Fatalf("a pending shot reserved %d records, want none [06 §11.2]", svc.Count())
	}

	// A hit: the record exists and carries the link.
	calls = 0
	const candidate = pool.Handle(7)
	svc, ports, slot = newSpawner(candidate, &calls)
	h, ok := TryFire(svc, slot, 0, Target{Kind: TargetPoint, X: fixedI(4), Z: fixedI(4)}, 1, ports)
	if !ok || h == 0 {
		t.Fatalf("a matched rescan produced no projectile (h=%d ok=%v) [06 §11.2]", h, ok)
	}
	if got := svc.Records[int(h)-1].TargetProjectile; got != candidate {
		t.Fatalf("the interceptor's matched-projectile link is %d, want the rescan's candidate %d [06 §6.6][06 §11.2]", got, candidate)
	}
}
