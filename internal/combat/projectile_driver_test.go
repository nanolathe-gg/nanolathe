package combat

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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

// TestTickProjectilesOpaqueLiquidStopsOnlyTheWaterLadder verifies the two
// opaque-liquid paths through the live driver. A water-cell miss stays live;
// an actual unit contact above the same cell remains a direct impact, and
// noexplode retains that contacted record after its packet [06 §8.2][06 §13.2].
func TestTickProjectilesOpaqueLiquidStopsOnlyTheWaterLadder(t *testing.T) {
	newWater := func(t *testing.T) (*units.World, *world.Terrain, int32, int32) {
		t.Helper()
		w, terrain := newContactFixture(t)
		const cx, cz = 15, 15
		terrain.SeaLevel = 20
		terrain.PlotAt(cx, cz).SetMinHeight(0)
		terrain.PlotAt(cx, cz).SetMaxHeight(0)
		return w, terrain, cx, cz
	}
	run := func(t *testing.T, withUnit, noExplode bool) (int, int32, []Event) {
		t.Helper()
		var svc Service
		bindFixtureControlBytes(&svc)
		svc.OpaqueLiquidMode = true
		w, terrain, cx, cz := newWater(t)
		var health int32
		var targetHandle pool.Handle
		if withUnit {
			h, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx), numeric.FixedFromInt(10), cellCentre(cz))
			if err != nil {
				t.Fatal(err)
			}
			stampGroundRect(terrain, cx, cz, 1, 1, h)
			health = w.Unit(h).Health
			targetHandle = h
		}
		weapon := driverWeapon(false)
		weapon.NoExplode = noExplode
		h, ok := svc.Reserve()
		if !ok {
			t.Fatal("reserve projectile")
		}
		p := &svc.Records[int(h)-1]
		p.WeaponID = weapon.ID
		p.ShooterSide = 0
		p.Pos = Vec3{X: cellCentre(cx), Y: numeric.FixedFromInt(10), Z: cellCentre(cz)}
		p.ExpiryTick = 10
		var events []Event
		svc.Events = func(ev Event) { events = append(events, ev) }
		svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
		if withUnit {
			health = w.Unit(targetHandle).Health
		}
		return svc.Count(), health, events
	}

	if count, _, events := run(t, false, false); count != 1 || len(events) != 0 {
		t.Fatalf("opaque water miss: count=%d events=%#v, want a live quiet projectile", count, events)
	}
	if count, health, _ := run(t, true, false); count != 0 || health >= 100 {
		t.Fatalf("opaque water direct contact: count=%d health=%d, want retirement after unit damage", count, health)
	}
	if count, health, events := run(t, true, true); count != 1 || health >= 100 || len(events) == 0 {
		t.Fatalf("opaque water noexplode direct contact: count=%d health=%d events=%#v, want retained damaged contact", count, health, events)
	}
}

// TestTickProjectilesExpiryImpactHasNoInventedDirectUnit leaves an occupancy
// candidate under a burn-blow expiry. The motion path enters central impact
// with a null direct recipient, so the small-area direct shortcut cannot
// damage that unrelated contact [06 §6.4][06 §9.1].
func TestTickProjectilesExpiryImpactHasNoInventedDirectUnit(t *testing.T) {
	var svc Service
	bindFixtureControlBytes(&svc)
	w, terrain := newContactFixture(t)
	const cx, cz = 18, 18
	hAccidental, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx), numeric.FixedFromInt(10), cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	stampGroundRect(terrain, cx, cz, 1, 1, hAccidental)
	accidental := w.Unit(hAccidental)
	weapon := driverWeapon(false)
	weapon.LineOfSight = false
	weapon.WeaponTimer = 1
	weapon.BurnBlow = true
	weapon.AreaOfEffect = 0 // direct-only threshold, so null direct means no damage.
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	p := &svc.Records[int(h)-1]
	p.WeaponID = weapon.ID
	p.ShooterSide = 0
	p.TargetUnit = hAccidental // retained guidance is not a direct contact.
	p.Pos = Vec3{X: cellCentre(cx), Y: numeric.FixedFromInt(10), Z: cellCentre(cz)}
	p.ExpiryTick = 1
	svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
	if accidental.Health != accidental.MaxHealth {
		t.Fatalf("expiry impact invented direct damage for occupancy handle %d: health=%d", hAccidental, accidental.Health)
	}
}

// TestTickProjectilesMixedBurstUsesCapturedSpanAfterOrdinaryImpact puts an
// ordinary terrain impact before a due burst anchor in the same captured span.
// The impact observes no burst draws, then the successful anchor attempt uses
// its two researched draws and leaves its clone beyond this tick's motion walk
// [06 §7.1][06 §4.3][06 §5.1].
func TestTickProjectilesMixedBurstUsesCapturedSpanAfterOrdinaryImpact(t *testing.T) {
	var svc Service
	w, terrain := newContactFixture(t)
	const cx, cz = 22, 22
	terrain.PlotAt(cx, cz).SetMinHeight(20)
	terrain.PlotAt(cx, cz).SetMaxHeight(20)
	ordinary := driverWeapon(false)
	ordinary.ID = 93
	burst := driverWeapon(false)
	burst.ID = 94
	burst.SprayAngle = 2
	burst.RandomDecay = 3
	burst.BurstRate = 1
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{
		"ordinary": ordinary,
		"burst":    burst,
	}}
	cat.RebuildWeaponIndex()
	hOrdinary, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve ordinary projectile")
	}
	pOrdinary := &svc.Records[int(hOrdinary)-1]
	pOrdinary.WeaponID = ordinary.ID
	pOrdinary.Pos = Vec3{X: cellCentre(cx), Y: numeric.FixedFromInt(21), Z: cellCentre(cz)}
	pOrdinary.Velocity.Y = numeric.FixedFromInt(-2)
	pOrdinary.ExpiryTick = 10
	hBurst, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve burst anchor")
	}
	anchorPos := Vec3{X: cellCentre(cx + 2), Y: numeric.FixedFromInt(30), Z: cellCentre(cz)}
	pBurst := &svc.Records[int(hBurst)-1]
	pBurst.WeaponID = burst.ID
	pBurst.Pos = anchorPos
	pBurst.StartPos = anchorPos
	pBurst.Velocity = Vec3{X: numeric.FixedFromInt(2)}
	pBurst.Speed = numeric.FixedFromInt(2)
	pBurst.BurstRemaining = 1
	pBurst.BurstDeadline = 1
	pBurst.ExpiryTick = 10
	sim := rng.NewSimulation(7)
	drawsAtImpact := uint64(^uint64(0))
	svc.Events = func(ev Event) {
		if ev.Kind == EventProjectileImpact {
			drawsAtImpact = sim.Draws()
		}
	}

	svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, cat, &sim, nil)
	if drawsAtImpact != 0 {
		t.Fatalf("ordinary impact observed %d burst draws, want zero: ascending captured span runs it first", drawsAtImpact)
	}
	if sim.Draws() != 2 {
		t.Fatalf("due successful burst used %d RNG draws, want decay then spray", sim.Draws())
	}
	if svc.Count() != 1 || svc.Records[0].Pos != anchorPos {
		t.Fatalf("same-tick burst clone was stepped: count=%d record=%+v, want stationary clone at %+v", svc.Count(), svc.Records[0], anchorPos)
	}
}

// TestTickProjectilesFeatureFringeUsesContactedCellHeightAndCache uses authored
// short and tall feature records on one fringe cell. The height comparison
// uses that fringe cell's ground word, then a retained noexplode record keeps
// its feature-cell cache and suppresses a repeat contact [06 §8.2].
func TestTickProjectilesFeatureFringeUsesContactedCellHeightAndCache(t *testing.T) {
	run := func(t *testing.T, height int32) (int, int) {
		t.Helper()
		var svc Service
		w, terrain := newContactFixture(t)
		const ax, az = 25, 25
		terrain.FeatureDefs = []*content.FeatureDef{{Height: height}}
		terrain.PlotAt(ax, az).SetFeature(0)
		terrain.PlotAt(ax, az).SetMinHeight(10)
		fringe := terrain.PlotAt(ax+1, az)
		fringe.SetFeature(world.PlotFeatureFringe)
		fringe.SetAnchorSigned(-1, 0)
		fringe.SetMinHeight(30)
		fringe.SetMaxHeight(30)
		weapon := driverWeapon(false)
		weapon.NoExplode = true
		h, ok := svc.Reserve()
		if !ok {
			t.Fatal("reserve projectile")
		}
		p := &svc.Records[int(h)-1]
		p.WeaponID = weapon.ID
		p.Pos = Vec3{X: cellCentre(ax + 1), Y: numeric.FixedFromInt(35), Z: cellCentre(az)}
		p.ExpiryTick = 10
		impacts := 0
		svc.Events = func(ev Event) {
			if ev.Kind == EventProjectileImpact {
				impacts++
			}
		}
		cat := driverCatalog(weapon)
		svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, cat, nil, nil)
		svc.TickProjectiles(2, w, terrain, nil, nil, nil, nil, cat, nil, nil)
		return impacts, svc.Count()
	}
	if impacts, count := run(t, 4); impacts != 0 || count != 1 {
		t.Fatalf("short fringe feature: impacts=%d count=%d, want no contact and live projectile", impacts, count)
	}
	if impacts, count := run(t, 10); impacts != 1 || count != 1 {
		t.Fatalf("tall fringe feature: impacts=%d count=%d, want one cached retained contact", impacts, count)
	}
}

// TestTickProjectilesGuidanceReadsStaleTailSlotNextTick first compacts a dead
// linked tail, then on the following tick reads the preserved bytes beyond the
// active count. Guidance selects that raw point over the stored fallback
// [06 §5.2][06 §6.5].
func TestTickProjectilesGuidanceReadsStaleTailSlotNextTick(t *testing.T) {
	var svc Service
	weapon := &content.WeaponDef{ID: 95, SelfProp: true, Guidance: true, TurnRate: 32767, WeaponVelocity: 65536, WeaponAcceleration: 65536}
	tailWeapon := driverWeapon(false)
	tailWeapon.ID = 96
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"guided": weapon, "tail": tailWeapon}}
	cat.RebuildWeaponIndex()
	source, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve source")
	}
	tail, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve tail")
	}
	linkedPos := Vec3{X: numeric.FixedFromInt(100), Z: numeric.FixedFromInt(0)}
	pSource := &svc.Records[int(source)-1]
	pSource.WeaponID = weapon.ID
	pSource.TargetProjectile = tail
	pSource.TargetPos = Vec3{X: numeric.FixedFromInt(0), Z: numeric.FixedFromInt(100)}
	pSource.ExpiryTick = 10
	pTail := &svc.Records[int(tail)-1]
	pTail.WeaponID = tailWeapon.ID
	pTail.Pos = linkedPos
	pTail.ExpiryTick = 0
	svc.MarkDead(tail)

	svc.TickProjectiles(1, nil, nil, nil, nil, nil, nil, cat, nil, nil)
	if svc.Count() != 1 {
		t.Fatalf("tail compaction count=%d, want source survivor", svc.Count())
	}
	if int(tail) <= svc.Count() || int(tail) > len(svc.Records) {
		t.Fatalf("stale target handle %d must be beyond count %d but within backing arena %d", tail, svc.Count(), len(svc.Records))
	}
	stalePoint := svc.Records[int(tail)-1].Pos
	stored := svc.Records[0].TargetPos
	before := svc.Records[0].Pos
	svc.TickProjectiles(2, nil, nil, nil, nil, nil, nil, cat, nil, nil)
	if want := YawFromDelta(stalePoint.X.Sub(before.X), stalePoint.Z.Sub(before.Z)); svc.Records[0].Yaw != want {
		fallback := YawFromDelta(stored.X.Sub(before.X), stored.Z.Sub(before.Z))
		t.Fatalf("tick-2 guided source yaw=%d, want stale tail yaw=%d; stored fallback yaw=%d", svc.Records[0].Yaw, want, fallback)
	}
}

// TestTickProjectilesSelfPropImpactContinuesAfterCentralImpact locks the
// self-propelled burn-blow ordering: central impact sees the pre-motion point,
// then the same dead-or-retained visit advances and runs the next-cell contact
// [06 §6.6][06 §7.1].
func TestTickProjectilesSelfPropImpactContinuesAfterCentralImpact(t *testing.T) {
	for _, noExplode := range []bool{false, true} {
		t.Run(map[bool]string{false: "retires", true: "retains"}[noExplode], func(t *testing.T) {
			var svc Service
			bindFixtureControlBytes(&svc)
			w, terrain := newContactFixture(t)
			const cx, cz = 4, 4
			targetH, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx+1), numeric.FixedFromInt(10), cellCentre(cz))
			if err != nil {
				t.Fatal(err)
			}
			target := w.Unit(targetH)
			stampGroundRect(terrain, cx+1, cz, 1, 1, targetH)
			weapon := &content.WeaponDef{ID: 97, SelfProp: true, BurnBlow: true, NoExplode: noExplode, AreaOfEffect: 0, DamageDefault: 20}
			h, ok := svc.Reserve()
			if !ok {
				t.Fatal("reserve projectile")
			}
			p := &svc.Records[int(h)-1]
			p.WeaponID = weapon.ID
			p.ShooterSide = 0
			p.Pos = Vec3{X: cellCentre(cx), Y: numeric.FixedFromInt(10), Z: cellCentre(cz)}
			p.Velocity.X = numeric.FixedFromInt(16)
			p.ExpiryTick = 1
			var impacts []Vec3
			svc.Events = func(ev Event) {
				if ev.Kind == EventProjectileImpact {
					impacts = append(impacts, ev.Position)
				}
			}

			svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
			if len(impacts) != 2 || impacts[0] != (Vec3{X: cellCentre(cx), Y: numeric.FixedFromInt(10), Z: cellCentre(cz)}) || impacts[1].X != cellCentre(cx+1) {
				t.Fatalf("self-propelled impact positions=%#v, want pre-motion central then post-motion collision", impacts)
			}
			if target.Health >= target.MaxHealth {
				t.Fatalf("post-impact self-propelled collision did not damage target: health=%d", target.Health)
			}
			wantCount := 0
			if noExplode {
				wantCount = 1
			}
			if svc.Count() != wantCount {
				t.Fatalf("NoExplode=%v left count %d, want %d", noExplode, svc.Count(), wantCount)
			}
		})
	}
}

// TestTickProjectilesTwoPhaseTransitionStillCollides verifies the first
// two-phase expiry transition does not skip the collision and common tail
// after its gravity-and-position continuation [06 §6.6][06 §7.1].
func TestTickProjectilesTwoPhaseTransitionStillCollides(t *testing.T) {
	var svc Service
	bindFixtureControlBytes(&svc)
	w, terrain := newContactFixture(t)
	const cx, cz = 7, 7
	targetH, err := w.Create(contactDef(contactModelTop), 1, cellCentre(cx+1), numeric.FixedFromInt(10), cellCentre(cz))
	if err != nil {
		t.Fatal(err)
	}
	target := w.Unit(targetH)
	stampGroundRect(terrain, cx+1, cz, 1, 1, targetH)
	weapon := &content.WeaponDef{ID: 98, SelfProp: true, TwoPhase: true, FlightTime: 2, AreaOfEffect: 0, DamageDefault: 20}
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	p := &svc.Records[int(h)-1]
	p.WeaponID = weapon.ID
	p.ShooterSide = 0
	p.Pos = Vec3{X: cellCentre(cx), Y: numeric.FixedFromInt(10), Z: cellCentre(cz)}
	p.Velocity.X = numeric.FixedFromInt(16)
	p.ExpiryTick = 1

	svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
	if !svc.Records[0].TwoPhase || svc.Records[0].ExpiryTick != 3 {
		t.Fatalf("phase transition state=%+v, want first phase with expiry 3", svc.Records[0])
	}
	if target.Health >= target.MaxHealth {
		t.Fatalf("two-phase transition skipped post-motion collision: health=%d", target.Health)
	}
}

// TestTickProjectilesSteeringFailureRebuildsAfterImpact makes the impact
// callback observe the inherited velocity, then verifies the failed guidance
// visit rebuilds velocity before its same-visit motion [06 §6.7].
func TestTickProjectilesSteeringFailureRebuildsAfterImpact(t *testing.T) {
	var svc Service
	weapon := &content.WeaponDef{
		ID: 99, SelfProp: true, Guidance: true, BurnBlow: true,
		NoExplode: true, TurnRate: 1, WeaponVelocity: 65536,
		AreaOfEffect: 0,
	}
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	p := &svc.Records[int(h)-1]
	p.WeaponID = weapon.ID
	p.Speed = numeric.FixedFromInt(1)
	p.Velocity = Vec3{X: numeric.FixedFromInt(3)}
	p.TargetPos = Vec3{X: numeric.FixedFromInt(-100)}
	p.Yaw = 16384
	p.ExpiryTick = 10
	if desired := YawFromDelta(p.TargetPos.X, p.TargetPos.Z); absU16(uint16(int16(desired-p.Yaw))) <= 27000 {
		t.Fatalf("fixture target yaw %d does not take the established burn-blow steering-failure branch", desired)
	}
	oldYaw, oldPitch := p.Yaw, p.Pitch
	oldVelocity := p.Velocity
	seenVelocity := Vec3{}
	svc.Events = func(ev Event) {
		if ev.Kind == EventProjectileImpact {
			seenVelocity = svc.Records[int(h)-1].Velocity
			if got := &svc.Records[int(h)-1]; got.Yaw != oldYaw || got.Pitch != oldPitch {
				t.Errorf("impact observed mutated failing guidance: yaw=%d pitch=%d", got.Yaw, got.Pitch)
			}
		}
	}

	svc.TickProjectiles(1, nil, nil, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
	if seenVelocity != oldVelocity {
		t.Fatalf("central impact saw velocity %v, want inherited pre-rebuild %v", seenVelocity, oldVelocity)
	}
	if svc.Records[0].Velocity == oldVelocity || svc.Records[0].Pos == (Vec3{}) {
		t.Fatalf("steering failure did not rebuild then move: velocity=%v position=%v", svc.Records[0].Velocity, svc.Records[0].Pos)
	}
}

// Feature-cache suppression resumes the actual terrain/water ladder; bounce
// is conditional on the authored flag and shifts the signed velocity before
// negation [06 §8.1][06 §8.2]. These replace the test-only ladder simulation.
func TestTickProjectilesCachedFeatureContinuesToTerrainAndWater(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ground, sea   uint8
		bounce        bool
		impacts, live int
	}{
		{"ground impact", 20, 0, false, 1, 0},
		{"ground bounce", 20, 0, true, 0, 1},
		{"water impact", 0, 20, false, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var svc Service
			w, terrain := newContactFixture(t)
			const cx, cz = 8, 8
			terrain.SeaLevel = tc.sea
			terrain.FeatureDefs = []*content.FeatureDef{{Height: 40}}
			cell := terrain.PlotAt(cx, cz)
			cell.SetFeature(0)
			cell.SetMinHeight(tc.ground)
			cell.SetMaxHeight(tc.ground)
			weapon := driverWeapon(false)
			weapon.GroundBounce = tc.bounce
			h, ok := svc.Reserve()
			if !ok {
				t.Fatal("reserve")
			}
			p := &svc.Records[int(h)-1]
			p.WeaponID = weapon.ID
			p.Pos = Vec3{X: cellCentre(cx), Y: numeric.FixedFromInt(10), Z: cellCentre(cz)}
			p.Velocity.Y = -7
			p.CacheCellX, p.CacheCellZ = cx, cz
			p.ExpiryTick = 10
			impacts := 0
			svc.Events = func(ev Event) {
				if ev.Kind == EventProjectileImpact {
					impacts++
				}
			}
			svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
			if impacts != tc.impacts || svc.Count() != tc.live {
				t.Fatalf("impacts/live = %d/%d, want %d/%d", impacts, svc.Count(), tc.impacts, tc.live)
			}
			if tc.bounce && p.Velocity.Y != 2 {
				t.Fatalf("bounce velocity = %d, want 2", p.Velocity.Y)
			}
		})
	}
}

// Leaving the map retires even noexplode without central-impact effects
// [06 §8.1].
func TestTickProjectilesOffMapNoExplodeRetiresWithoutImpact(t *testing.T) {
	var svc Service
	w, terrain := newContactFixture(t)
	weapon := driverWeapon(false)
	weapon.NoExplode = true
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve")
	}
	p := &svc.Records[int(h)-1]
	p.WeaponID = weapon.ID
	p.Pos = Vec3{X: -1, Y: numeric.FixedFromInt(10), Z: cellCentre(1)}
	p.ExpiryTick = 10
	impacts := 0
	svc.Events = func(ev Event) {
		if ev.Kind == EventProjectileImpact {
			impacts++
		}
	}
	svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
	if svc.Count() != 0 || impacts != 0 {
		t.Fatalf("live/impacts = %d/%d, want 0/0", svc.Count(), impacts)
	}
}

// Bounds admission precedes linked proximity, so an off-map interceptor cannot
// explode and remove its still-in-map quarry [06 §8.1][06 R-DMG-01 §14].
func TestTickProjectilesOffMapBeforeLinkedProximity(t *testing.T) {
	edge := world.CellToWorld(contactCellsPerSide)
	for _, tc := range []struct {
		name         string
		x, z, tx, tz numeric.Fixed
	}{
		{"west", -1, cellCentre(1), 0, cellCentre(1)},
		{"north", cellCentre(1), -1, cellCentre(1), 0},
		{"east", edge, cellCentre(1), edge - 1, cellCentre(1)},
		{"south", cellCentre(1), edge, cellCentre(1), edge - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var svc Service
			w, terrain := newContactFixture(t)
			interceptor := driverWeapon(false)
			interceptor.Interceptor, interceptor.NoExplode = true, true
			incoming := driverWeapon(false)
			incoming.ID++
			cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"interceptor": interceptor, "incoming": incoming}}
			cat.RebuildWeaponIndex()
			h, _ := svc.Reserve()
			target, _ := svc.Reserve()
			p, quarry := &svc.Records[int(h)-1], &svc.Records[int(target)-1]
			p.WeaponID, p.TargetProjectile, p.ExpiryTick = interceptor.ID, target, 10
			p.Pos = Vec3{X: tc.x, Y: numeric.FixedFromInt(20), Z: tc.z}
			quarry.WeaponID, quarry.ExpiryTick = incoming.ID, 10
			quarry.Pos = Vec3{X: tc.tx, Y: p.Pos.Y, Z: tc.tz}
			var events []Event
			svc.Events = func(ev Event) { events = append(events, ev) }
			svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, cat, nil, nil)
			if len(events) != 0 || svc.Count() != 1 || svc.Records[0].WeaponID != incoming.ID {
				t.Fatalf("off-map proximity emitted %v; survivors=%d, want silent retirement and quarry survival [06 §8.1]", events, svc.Count())
			}
		})
	}
}

// Self-propelled expiry impacts before motion and before collision's bounds
// admission; leaving the map afterwards must not erase that impact [06 §6.6].
func TestTickProjectilesExpiryImpactBeforeOffMapRetirement(t *testing.T) {
	var svc Service
	w, terrain := newContactFixture(t)
	weapon := &content.WeaponDef{ID: 91, SelfProp: true, BurnBlow: true}
	h, _ := svc.Reserve()
	p := &svc.Records[int(h)-1]
	p.WeaponID, p.ExpiryTick = weapon.ID, 1
	p.Pos = Vec3{X: numeric.FixedFromInt(1), Y: numeric.FixedFromInt(20), Z: cellCentre(1)}
	p.Velocity.X = numeric.FixedFromInt(-2)
	before := p.Pos
	var impacts []Vec3
	svc.Events = func(ev Event) {
		if ev.Kind == EventProjectileImpact {
			impacts = append(impacts, ev.Position)
		}
	}
	svc.TickProjectiles(1, w, terrain, nil, nil, nil, nil, driverCatalog(weapon), nil, nil)
	if len(impacts) != 1 || impacts[0] != before || svc.Count() != 0 {
		t.Fatalf("impacts=%v survivors=%d, want pre-motion impact then off-map retirement [06 §6.6][06 §8.1]", impacts, svc.Count())
	}
}

// The area walk's positive bound is exclusive at centre+span, without an
// extra row/column. Its centre uses signed whole-word division toward zero,
// unlike collision's arithmetic cell shift [06 §9.3].
func TestAreaEnumerationExactSpanAndSignedCentre(t *testing.T) {
	for _, tc := range []struct {
		name   string
		x, z   numeric.Fixed
		radius int32
		want   [][2]int32
	}{
		{"origin", 0, 0, 0, [][2]int32{{0, 0}}},
		{"interior", world.CellToWorld(2), world.CellToWorld(2), 0, [][2]int32{{1, 1}, {2, 1}, {1, 2}, {2, 2}}},
		{"negative fraction", -1, -1, 0, [][2]int32{{0, 0}}},
		{"negative partial cell", numeric.FixedFromInt(-15), numeric.FixedFromInt(-15), 0, [][2]int32{{0, 0}}},
		{"negative whole cell", numeric.FixedFromInt(-16), 0, 0, nil},
		{"signed word wrap", numeric.FixedFromInt(65536), 0, 0, [][2]int32{{0, 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got [][2]int32
			EnumerateArea(Vec3{X: tc.x, Z: tc.z}, tc.radius, 8, 8, func(x, z int32) { got = append(got, [2]int32{x, z}) })
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("cells = %v, want %v", got, tc.want)
			}
		})
	}
}
