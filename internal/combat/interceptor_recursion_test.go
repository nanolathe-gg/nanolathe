package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestNoExplodeInterceptorMutualSweepTerminates locks the cycle cut in
// impactProjectile.
//
// The interceptor-flagged explosion sweep walks the live pool and forces every
// record inside the unhalved area of effect through the ordinary impact
// selector [06 §11.2]. The exploder is normally retired before its own sweep
// runs, so the sweep cannot select it again — but `noexplode` gates exactly
// that retirement [06 §13.2] C28. Two records whose weapons carry both
// `interceptor` and `noexplode`, each inside the other's blast, therefore swept
// one another until the stack was exhausted and the process died.
//
// The fixture is authored, not retail: no stock weapon carries both flags (the
// four stock interceptors are all `noexplode=0`), so this is a modded-content
// crash, and the guard cannot move a stock game.
func TestNoExplodeInterceptorMutualSweepTerminates(t *testing.T) {
	var svc Service
	bindFixtureControlBytes(&svc)
	weapon := &content.WeaponDef{
		ID:            41,
		Interceptor:   true,
		NoExplode:     true,
		AreaOfEffect:  96,
		DamageDefault: 10,
	}
	catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"mutual": weapon}}

	first, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve first record")
	}
	second, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve second record")
	}
	// Both records sit two world units apart, well inside the unhalved 96-unit
	// area the sweep measures [06 R-WPN-05 §10].
	svc.Records[first-1] = Projectile{WeaponID: weapon.ID, Pos: Vec3{X: numeric.FixedFromInt(100), Z: numeric.FixedFromInt(100)}}
	svc.Records[second-1] = Projectile{WeaponID: weapon.ID, Pos: Vec3{X: numeric.FixedFromInt(102), Z: numeric.FixedFromInt(100)}}

	impacts := 0
	svc.Events = func(ev Event) {
		if ev.Kind == EventProjectileImpact {
			impacts++
		}
	}

	// Without the guard this recurses until the stack is exhausted, which kills
	// the test binary rather than failing this test.
	impactProjectile(&svc, first, &svc.Records[first-1], weapon, nil, nil, nil, nil, catalog, 12, Vec3{}, nil, 0)

	// The exploder sweeps its one neighbour, that neighbour sweeps back and is
	// refused, and both survive: `noexplode` retires neither [06 §13.2] C28.
	if impacts != 2 {
		t.Fatalf("projectile impacts = %d, want 2 (the exploder and the one record its sweep selects)", impacts)
	}
	if !svc.Alive(first) || !svc.Alive(second) {
		t.Fatalf("alive after impact = (%v, %v), want both live: noexplode gates the ordinary retirement", svc.Alive(first), svc.Alive(second))
	}
	if len(svc.impactStack) != 0 {
		t.Fatalf("impact stack = %d entries after the outermost impact returned, want 0", len(svc.impactStack))
	}

	// The guard is per impact stack, not per record: a record that already
	// impacted and returned is still eligible for the next impact, so the
	// sweep's visit set is unchanged for everything that does not re-enter.
	impacts = 0
	impactProjectile(&svc, second, &svc.Records[second-1], weapon, nil, nil, nil, nil, catalog, 13, Vec3{}, nil, 0)
	if impacts != 2 {
		t.Fatalf("second round projectile impacts = %d, want 2; the guard must not remember returned impacts", impacts)
	}
}
