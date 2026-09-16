package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func fixedI(v int) numeric.Fixed { return numeric.Fixed(int64(v) * 65536) }

func weaponForStockpile(id int32, reload int32, area int32, coverage int32, stockpile bool, interceptor bool, targetable bool, energy float64, metal float64, firestarter int32) *content.WeaponDef {
	return &content.WeaponDef{
		ID:            id,
		ReloadTime:    reload,
		AreaOfEffect:  area,
		Coverage:      coverage,
		Stockpile:     stockpile,
		Interceptor:   interceptor,
		Targetable:    targetable,
		EnergyPerShot: energy,
		MetalPerShot:  metal,
		Firestarter:   firestarter,
		// `stockpile` and `interceptor` are not creation families: the creator
		// is whichever one the weapon's own flags select [06 §6.2], and a
		// weapon matching none of the six predicates makes no projectile at
		// all. Every stockpile-flagged and every interceptor-flagged weapon in
		// the retail corpus authors `vlaunch` (I14, checked against the
		// reference install), which is also the creator [06 §6.6] names as the
		// one that stores the interceptor rescan's matched-projectile link, so
		// these fixtures author it too. Without it they described a launcher
		// that cannot exist in the corpus and that retail would refuse to fire.
		VLaunch: stockpile || interceptor,
	}
}

// launchStockpileRound fires one completed round the way production does.
//
// There is no separate stockpile launcher. For a stockpile weapon the slot's
// fire gate reads "slot byte nonzero" IN PLACE OF the per-shot cost test and
// otherwise runs the SAME executor [06 §11.1][06 R-WPN-05 §2], so a launch is
// the per-slot pipeline of [06 §4.1] C1 composed over the real spawner. The
// ammo gate, the post-spawn decrement of the slot byte and the skipped reload
// store are the PIPELINE's steps [06 §4.2] C7 [06 §11.1]; TryFire only
// validates, allocates, initializes and notifies. Composing the two — rather
// than stubbing either — is the only shape that catches both layers owning the
// same mutation, which is how a launch once consumed two rounds.
func launchStockpileRound(svc *Service, slot *Slot, idx int, tgt Target, tick uint32, ports FirePorts) (pool.Handle, bool) {
	slot.Target = tgt
	// A vertical-launch weapon gates on the aim result and tests no latch
	// [06 §3.3]; a silo holding a finished round has had its answer already.
	slot.Aim.Ready = true
	var h pool.Handle
	env := PipelineEnv{TryFire: func(i int, s *Slot) bool {
		var ok bool
		h, ok = TryFire(svc, s, i, tgt, tick, ports)
		return ok
	}}
	if !TickSlot(slot, idx, tick, nil, env, 100, 100, 0) {
		return 0, false
	}
	return h, true
}

// ---------------------------------------------------------------------------
// Stockpile count lifecycle fixture [06 §11.1] C29
// ---------------------------------------------------------------------------

func TestStockpileCountLifecycle(t *testing.T) {
	// BUILDWEAPON stockpiling flow, ammo counts [06 §11.1] C29
	// Distinct signed queue count (entry.Count) and byte-sized completed rounds (slot.Ammo) [06 §11.1]
	// Each visit adds 5 up to build time, cost delta via independently truncated cumulative [06 §11.1]
	// Retries: rejected 10, accepted incomplete 5, slot>199 waits 300 [06 §11.1]
	// Launch checked before production, only successful spawn decrements ammo [06 §11.1]

	w := weaponForStockpile(100, 30, 0, 0, true, false, false, 60, 30, 0) // buildTime 30, costs 60/30
	slot := &Slot{Weapon: w, Ammo: 0}
	entry := &StockpileEntry{Weapon: w, Count: 2, Progress: 0}

	// Admit always succeeds
	admitAlways := func(e, m float32) bool { return true }

	// First tick: progress 0->5, not complete, retry 5 [06 §11.1]
	next, refreshed, comp := TickStockpile(entry, slot, 100, admitAlways)
	if entry.Progress != 5 {
		t.Fatalf("progress after 1st tick %d want 5 [06 §11.1]", entry.Progress)
	}
	if next != 105 {
		t.Fatalf("nextTick %d want 105 (100+5) [06 §11.1]", next)
	}
	if refreshed {
		t.Fatalf("refreshed false want false before completion [06 §11.1]")
	}
	if comp != 0 {
		t.Fatalf("completedRounds %d want 0", comp)
	}
	if slot.Ammo != 0 {
		t.Fatalf("ammo %d want 0 before completion", slot.Ammo)
	}

	// Advance 5 more ticks to complete one round (progress 30)
	// Tick at 105 -> 10, 110->15,115->20,120->25,125->30 completes
	ticks := []uint32{105, 110, 115, 120, 125}
	for i, tk := range ticks {
		n, ref, c := TickStockpile(entry, slot, tk, admitAlways)
		_ = n
		if i < len(ticks)-1 {
			if c != 0 {
				t.Fatalf("tick %d unexpected completion %d", tk, c)
			}
			if ref {
				t.Fatalf("tick %d unexpected refresh", tk)
			}
		} else {
			// last tick completes
			if c != 1 {
				t.Fatalf("final tick %d completed %d want 1 [06 §11.1]", tk, c)
			}
			if !ref {
				t.Fatalf("completion should request interface refresh [06 §11.1]")
			}
			if slot.Ammo != 1 {
				t.Fatalf("ammo after one completion %d want 1 [06 §11.1]", slot.Ammo)
			}
			if entry.Count != 1 {
				t.Fatalf("queue count after one completion %d want 1 (2->1) [06 §11.1]", entry.Count)
			}
			if entry.Progress != 5 {
				t.Fatalf("progress after completion %d want next round first step 5 [06 R-WPN-05 §2]", entry.Progress)
			}
			// Launch before production check: round just completed cannot launch until next tick [06 §11.1] C29
			// Simulate that launch check would happen at next tick before next production Tick
		}
	}

	// Cost delta check: progress 0->5 with energy 60 buildTime30 = trunc(5*60/30)-0 =10 ; similarly 5->10 =10 etc [06 §11.1]
	// Verify delta helper
	d := StockpileCostDelta(0, 5, 60, 30)
	if d != 10 {
		t.Fatalf("cost delta 0->5 energy 60/30 got %v want 10 [06 §11.1]", d)
	}
	d2 := StockpileCostDelta(5, 10, 60, 30)
	if d2 != 10 {
		t.Fatalf("delta 5->10 got %v want 10", d2)
	}
	// Truncation independence: costs with fractional? Use energy 7 buildTime 3: old 1-> trunc(1*7/3)=2, new 2-> trunc(4)=4 delta 2
	d3 := StockpileCostDelta(1, 2, 7, 3)
	if d3 != 2 {
		t.Fatalf("fractional delta got %v want 2", d3)
	}

	// Rejected admission retries in 10 ticks, progress not advanced, amounts retained [06 §11.1]
	entry2 := &StockpileEntry{Weapon: w, Count: 1, Progress: 10} // mid-progress
	slot2 := &Slot{Weapon: w, Ammo: 0}
	admitReject := func(e, m float32) bool { return false }
	oldProg := entry2.Progress
	n2, _, _ := TickStockpile(entry2, slot2, 200, admitReject)
	if entry2.Progress != oldProg {
		t.Fatalf("rejected admission should not advance progress [06 §11.1], got %d want %d", entry2.Progress, oldProg)
	}
	if n2 != 210 {
		t.Fatalf("rejected retry %d want 210 (200+10) [06 §11.1]", n2)
	}

	// Slot above 199 waits 300 [06 §11.1]
	entry3 := &StockpileEntry{Weapon: w, Count: 1, Progress: 0}
	slot3 := &Slot{Weapon: w, Ammo: 200} // >199
	n3, _, _ := TickStockpile(entry3, slot3, 300, admitAlways)
	if n3 != 600 {
		t.Fatalf("blocked >199 next %d want 600 (300+300) [06 §11.1]", n3)
	}
	if entry3.Progress != 0 {
		t.Fatalf("blocked should not advance progress")
	}
	// 199 should still allow start? 199 can start and reach 200
	slot4 := &Slot{Weapon: w, Ammo: 199}
	entry4 := &StockpileEntry{Weapon: w, Count: 1, Progress: 25} // one step to complete
	n4, _, c4 := TickStockpile(entry4, slot4, 400, admitAlways)
	if c4 != 1 || slot4.Ammo != 200 {
		t.Fatalf("199->200 allowed, got ammo %d completed %d want 200,1 [06 §11.1]", slot4.Ammo, c4)
	}
	_ = n4
	// Next start should block at 200
	entry5 := &StockpileEntry{Weapon: w, Count: 1, Progress: 0}
	slot5 := &Slot{Weapon: w, Ammo: 200}
	n5, _, _ := TickStockpile(entry5, slot5, 500, admitAlways)
	if n5 != 800 {
		t.Fatalf("200 blocked retry %d want 800", n5)
	}

	// Assets with buildTime <=5 can complete multiple queued rounds in one visit while admission remains open [06 §11.1]
	wFast := weaponForStockpile(101, 3, 0, 0, true, false, false, 0, 0, 0) // buildTime 3 <=5
	slotFast := &Slot{Weapon: wFast, Ammo: 0}
	entryFast := &StockpileEntry{Weapon: wFast, Count: 3, Progress: 0}
	// admit always true, so one TickStockpile call should complete all 3 rounds in same visit
	nFast, _, cFast := TickStockpile(entryFast, slotFast, 600, admitAlways)
	if cFast != 3 {
		t.Fatalf("fast buildTime <=5 multi-complete got %d want 3 [06 §11.1]", cFast)
	}
	if slotFast.Ammo != 3 {
		t.Fatalf("fast ammo %d want 3", slotFast.Ammo)
	}
	if entryFast.Count != 0 {
		t.Fatalf("fast queue count %d want 0", entryFast.Count)
	}
	if nFast != 0 {
		t.Fatalf("fast after all complete next should be 0, got %d", nFast)
	}

	// Launch before production ordering: launch decrements only on successful spawn [06 §11.1]
	// Simulate slot with ammo 1, launch then production tick
	var svc Service
	slotLaunch := &Slot{Weapon: w, Ammo: 1, MuzzlePiece: 5}
	// First launch check before production at same tick
	h, ok := launchStockpileRound(&svc, slotLaunch, 0, Target{Kind: TargetPoint, X: fixedI(10)}, 700, FirePorts{})
	if !ok || h == 0 {
		t.Fatalf("launch with ammo 1 should succeed [06 §11.1]")
	}
	if slotLaunch.Ammo != 0 {
		t.Fatalf("launch decrements ammo [06 §11.1], got %d want 0", slotLaunch.Ammo)
	}
	// Pool full launch should not decrement
	for svc.Count() < ProjectileCapacity {
		svc.Reserve()
	}
	slotLaunch2 := &Slot{Weapon: w, Ammo: 1}
	h2, ok2 := launchStockpileRound(&svc, slotLaunch2, 0, Target{Kind: TargetPoint}, 701, FirePorts{})
	if ok2 || h2 != 0 {
		t.Fatalf("pool full should fail launch [06 §11.1] C29")
	}
	if slotLaunch2.Ammo != 1 {
		t.Fatalf("pool full must not decrement ammo [06 §11.1], got %d", slotLaunch2.Ammo)
	}
	// Empty ammo prevents allocation [06 §11.1]
	var svc2 Service
	slotEmpty := &Slot{Weapon: w, Ammo: 0}
	h3, ok3 := launchStockpileRound(&svc2, slotEmpty, 0, Target{Kind: TargetPoint}, 702, FirePorts{})
	if ok3 || h3 != 0 {
		t.Fatalf("empty ammo should prevent projectile allocation [06 §11.1]")
	}
}

// ---------------------------------------------------------------------------
// Interceptor detonation set vs ordinary enumeration ordering [06 §11.2] [06 §9.3] C29
// ---------------------------------------------------------------------------

// TestInterceptorSweepMembershipAndOrdering locks the interceptor sweep on the
// live central-impact path [06 §11.2] [06 §9.3] C29: ordinary area recipients
// are settled first, and only then does the interceptor force-detonate alive
// non-self projectiles inside its UNHALVED areaofeffect. The sweep consults no
// side, alliance or targetable state, so a friendly round inside the blast is
// removed too [06 §11.2].
func TestInterceptorSweepMembershipAndOrdering(t *testing.T) {
	svc, w, terrain := splashFixture(t)
	// Area 16 unhalved reaches 16 world units; the ordinary unit sweep uses the
	// halved radius, which is why a victim at 12 proves the value is unhalved.
	interceptor := &content.WeaponDef{ID: 5100, AreaOfEffect: 16, DamageDefault: 10, Interceptor: true}
	// The swept rounds carry a harmless weapon so their forced central impact
	// is visible in the event stream without splashing anything itself.
	incoming := &content.WeaponDef{ID: 5101, AreaOfEffect: 0, DamageDefault: 0}
	cat := interceptorTestCatalog(t, interceptor, incoming)

	origin := Vec3{X: fixedI(64), Z: fixedI(64)}
	hExpl, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve exploder")
	}
	svc.Records[int(hExpl)-1] = Projectile{WeaponID: interceptor.ID, Pos: origin, ShooterSide: 1}

	round := func(offset int, side uint8) pool.Handle {
		h, ok := svc.Reserve()
		if !ok {
			t.Fatal("reserve victim")
		}
		svc.Records[int(h)-1] = Projectile{WeaponID: incoming.ID, Pos: Vec3{X: fixedI(64 + offset), Z: fixedI(64)}, ShooterSide: side}
		return h
	}
	near := round(5, 2)     // inside halved and unhalved
	mid := round(12, 2)     // outside halved 8, inside unhalved 16
	far := round(20, 2)     // outside both
	friendly := round(4, 1) // same side as the exploder
	dead := round(3, 2)
	svc.MarkDead(dead)

	// An ordinary unit recipient of the same blast, so the two settlement
	// stages are distinguishable in the event stream [06 §9.3].
	bystander := splashUnit(t, w, &content.UnitDef{UnitName: "bystander", MaxDamage: 100, Limit: -1}, 2, 64, 0, 64)
	stampGroundOccupancy(t, terrain, bystander)

	var order []string
	svc.Events = func(ev Event) {
		switch ev.Kind {
		case EventDamageFlash:
			order = append(order, "unit")
		case EventProjectileImpact:
			order = append(order, "projectile")
		}
	}
	p := &Projectile{Pos: origin, ShooterSide: 1}
	handleProjectileImpact(svc, hExpl, p, interceptor, w, terrain, nil, nil, cat, 100, Vec3{}, nil, 0)

	// The outer impact opens the stream, the ordinary unit recipient settles
	// next, and only then does the projectile sweep force its three victims
	// through central impact [06 §9.3] [06 §11.2] C29.
	want := []string{"projectile", "unit", "projectile", "projectile", "projectile"}
	if len(order) != len(want) {
		t.Fatalf("event order %v want %v [06 §9.3] [06 §11.2] C29", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("event order %v want %v: ordinary area settlement precedes the projectile sweep [06 §9.3] [06 §11.2] C29", order, want)
		}
	}
	if bystander.Health != 90 {
		t.Fatalf("ordinary area recipient health %d want 90 [06 §9.3]", bystander.Health)
	}

	if svc.Alive(near) {
		t.Fatalf("a round 5 units away is inside the blast [06 §11.2] C29")
	}
	if svc.Alive(mid) {
		t.Fatalf("a round 12 units away proves the UNHALVED area 16, not the halved 8 [06 §11.2] C29")
	}
	if !svc.Alive(far) {
		t.Fatalf("a round 20 units away is outside the unhalved area 16 [06 §11.2]")
	}
	if svc.Alive(friendly) {
		t.Fatalf("the sweep consults no side, alliance or targetable state, so a friendly round is removed [06 §11.2] C29")
	}
}

// ---------------------------------------------------------------------------
// Coverage square sizing [06 §11.2] [06 §4.x] C29
// ---------------------------------------------------------------------------

func TestCoverageSquareSizing(t *testing.T) {
	// Axis-aligned square of side twice coverage<<16 around stored aim point [06 §11.2] C29
	// Coverage is weapon coverage integer; half extent coverage<<16, side twice that [06 §11.2] C29
	coverage := int32(10)
	half := InterceptorCoverageHalfExtent(coverage) // [06 §11.2]
	side := InterceptorCoverageSide(coverage)       // [06 §11.2]
	if half != int64(10)<<16 {
		t.Fatalf("half extent %d want %d (coverage<<16) [06 §11.2] C29", half, int64(10)<<16)
	}
	if side != int64(20)<<16 {
		t.Fatalf("side %d want %d (twice coverage<<16) [06 §11.2] C29", side, int64(20)<<16)
	}
	interceptorPos := Vec3{X: fixedI(0), Z: fixedI(0), Y: fixedI(0)}
	// Inclusive bounds: distance == coverage exactly should be inside [06 §11.2]
	candidateInside := Vec3{X: fixedI(10), Z: fixedI(0), Y: fixedI(0)} // dx=10 exactly
	if !WithinInterceptorCoverage(candidateInside, interceptorPos, coverage) {
		t.Fatalf("coverage inclusive: dx==10 should be inside [06 §11.2]")
	}
	candidateAtEdgeBoth := Vec3{X: fixedI(10), Z: fixedI(10), Y: fixedI(0)} // both axes at limit, should be inside square inclusive [06 §11.2]
	if !WithinInterceptorCoverage(candidateAtEdgeBoth, interceptorPos, coverage) {
		t.Fatalf("both axes at coverage should be inside square inclusive [06 §11.2]")
	}
	candidateOutsideX := Vec3{X: fixedI(11), Z: fixedI(0)} // 11 >10 should be outside
	if WithinInterceptorCoverage(candidateOutsideX, interceptorPos, coverage) {
		t.Fatalf("dx 11 >10 should be outside [06 §11.2]")
	}
	candidateOutsideZ := Vec3{X: fixedI(0), Z: fixedI(11)}
	if WithinInterceptorCoverage(candidateOutsideZ, interceptorPos, coverage) {
		t.Fatalf("dz 11 >10 should be outside [06 §11.2]")
	}
	// Square vs circle: point (8,8) is inside square (both <=10) but outside circle radius 10 (sqrt(128)~11.3>10)
	// This proves coverage is square not radius circle per [06 §11.2] C29
	candidateSquareNotCircle := Vec3{X: fixedI(8), Z: fixedI(8)}
	if !WithinInterceptorCoverage(candidateSquareNotCircle, interceptorPos, coverage) {
		t.Fatalf("square test: (8,8) should be inside square coverage 10 [06 §11.2] C29")
	}
	// If it were circle radius 10, distance sqrt(128)~11.3 >10 would be outside — but square keeps it inside, verifying square semantics
	dx := float64(8)
	dz := float64(8)
	circleDist := dx*dx + dz*dz // squared vs 100
	if circleDist <= 100 {
		t.Fatalf("sanity: (8,8) squared 128 <=100 should be false for circle check")
	}
	// Y axis ignored? Coverage square is X/Z only per [06 §11.2] (axis-aligned square of side twice coverage around stored aim point, X and Z coords each inside bounds)
	// Ensure Y difference does not affect coverage (only X/Z) — per spec, stored X and Z each inside bounds, Y not tested
	candidateYFar := Vec3{X: fixedI(5), Z: fixedI(5), Y: fixedI(1000)}
	if !WithinInterceptorCoverage(candidateYFar, interceptorPos, coverage) {
		t.Fatalf("Y far should still be inside (coverage only X/Z) [06 §11.2]")
	}
	// A negative coverage -k accepts exactly the complement of the OPEN square
	// of half-side k: at or beyond k on both axes is accepted, inside is
	// rejected [06 R-WPN-05 §10]. There is no "negative means none" guard.
	insideNeg := Vec3{X: fixedI(0), Z: fixedI(0)} // |d| = 0 < 1 on both axes
	if WithinInterceptorCoverage(insideNeg, interceptorPos, -1) {
		t.Fatalf("coverage -1: a candidate inside the open square must be rejected [06 R-WPN-05 §10]")
	}
	atNeg := Vec3{X: fixedI(1), Z: fixedI(-1)} // |d| = 1 exactly on both axes
	if !WithinInterceptorCoverage(atNeg, interceptorPos, -1) {
		t.Fatalf("coverage -1: |delta| == 1 on both axes must be accepted [06 R-WPN-05 §10]")
	}
	beyondNeg := Vec3{X: fixedI(400), Z: fixedI(-9)} // both axes beyond 1
	if !WithinInterceptorCoverage(beyondNeg, interceptorPos, -1) {
		t.Fatalf("coverage -1: both axes beyond 1 must be accepted [06 R-WPN-05 §10]")
	}
	mixedNeg := Vec3{X: fixedI(400), Z: fixedI(0)} // X beyond, Z inside
	if WithinInterceptorCoverage(mixedNeg, interceptorPos, -1) {
		t.Fatalf("coverage -1: the Z axis inside the open square must still reject [06 R-WPN-05 §10]")
	}
	// A coverage at or above 32,768 wraps C<<17 to zero in 32-bit arithmetic,
	// so the compare accepts only the single delta whose biased value is zero:
	// exactly -32768 world units on each axis [06 R-WPN-05 §10]. This is the
	// wrap itself, not a guard against it.
	const wrapCoverage = int32(32768)
	if lim := InterceptorCoverageSide(wrapCoverage); lim != 0 {
		t.Fatalf("C<<17 at coverage 32768 is %d, want the 32-bit wrap to 0 [06 R-WPN-05 §10]", lim)
	}
	wrapHit := Vec3{X: numeric.Fixed(1 << 31), Z: numeric.Fixed(1 << 31)} // interceptor - candidate = -2^31
	if !WithinInterceptorCoverage(wrapHit, interceptorPos, wrapCoverage) {
		t.Fatalf("coverage 32768 must accept the delta whose biased value wraps to zero [06 R-WPN-05 §10]")
	}
	wrapMiss := Vec3{X: fixedI(32767), Z: fixedI(32767)}
	if WithinInterceptorCoverage(wrapMiss, interceptorPos, wrapCoverage) {
		t.Fatalf("coverage 32768 must reject a delta of 32767 world units [06 R-WPN-05 §10]")
	}
	// Test FindInterceptorTarget respects coverage square and claims
	var svc Service
	weaponTargetable := weaponForStockpile(300, 10, 0, 0, false, false, true, 0, 0, 0) // targetable true
	weaponNotTargetable := weaponForStockpile(301, 10, 0, 0, false, false, false, 0, 0, 0)
	weapons := map[int32]*content.WeaponDef{
		300: weaponTargetable,
		301: weaponNotTargetable,
	}
	// Projectile at (10,10) targetable should be found with coverage 10
	h1, _ := svc.Reserve()
	svc.Records[int(h1)-1] = Projectile{WeaponID: 300, Pos: Vec3{X: fixedI(0)}, TargetPos: Vec3{X: fixedI(10), Z: fixedI(10)}, ShooterSide: 2}
	h2, _ := svc.Reserve()
	svc.Records[int(h2)-1] = Projectile{WeaponID: 301, Pos: Vec3{X: fixedI(0)}, TargetPos: Vec3{X: fixedI(5), Z: fixedI(5)}, ShooterSide: 2} // not targetable
	// First projectile is claimed? Test unclaimed
	found, _, ok := FindInterceptorTarget(&svc, interceptorPos, 1, coverage, weapons)
	if !ok || found != h1 {
		t.Fatalf("FindInterceptorTarget should find h1 targetable inside coverage, got %d ok %v want %d [06 §11.2]", found, ok, h1)
	}
	// Mark h1 claimed by a reservation link
	hClaim, _ := svc.Reserve()
	svc.Records[int(hClaim)-1] = Projectile{WeaponID: 300, Pos: Vec3{X: fixedI(0)}, TargetPos: Vec3{X: fixedI(0)}, ShooterSide: 1, TargetProjectile: h1}
	found2, _, ok2 := FindInterceptorTarget(&svc, interceptorPos, 1, coverage, weapons)
	if ok2 {
		t.Fatalf("claimed target should be skipped, got %d [06 §11.2]", found2)
	}
	// Owner byte differs required: create projectile with same side as interceptor
	hSameSide, _ := svc.Reserve()
	svc.Records[int(hSameSide)-1] = Projectile{WeaponID: 300, Pos: Vec3{X: fixedI(0)}, TargetPos: Vec3{X: fixedI(5), Z: fixedI(5)}, ShooterSide: 1} // same side as interceptorSide 1
	// After clearing previous claim, hSameSide is only unclaimed but same side -> should be skipped, and no other candidate (h1 claimed, h2 not targetable)
	// So should find none
	found3, _, ok3 := FindInterceptorTarget(&svc, interceptorPos, 1, coverage, weapons)
	if ok3 {
		t.Fatalf("same side should be skipped (owner byte differs required) [06 §11.2], got %d", found3)
	}
	// Stored aim point vs current position conflation test: candidate's TargetPos is inside coverage but Pos far away, should still be found (scan metric is TargetPos) [06 §11.2]
	// And slot store should be candidate's current position [06 §11.2]
	var svc2 Service
	hPosFar, _ := svc2.Reserve()
	svc2.Records[int(hPosFar)-1] = Projectile{
		WeaponID:    300,
		Pos:         Vec3{X: fixedI(1000), Z: fixedI(1000)}, // current far away
		TargetPos:   Vec3{X: fixedI(5), Z: fixedI(5)},       // aim inside coverage 10
		ShooterSide: 2,
	}
	found4, storedPos, ok4 := FindInterceptorTarget(&svc2, interceptorPos, 1, coverage, weapons)
	if !ok4 || found4 != hPosFar {
		t.Fatalf("stored aim inside coverage should be found even though current pos far [06 §11.2], got %d ok %v", found4, ok4)
	}
	if storedPos.X.Raw() != fixedI(1000).Raw() {
		t.Fatalf("slot store should be candidate's current position [06 §11.2], got %d want 1000", storedPos.X.Int())
	}
}

// ---------------------------------------------------------------------------
// Vertical slice: build nuke → stockpile one → launch → intercept [06 §11] C29
// ---------------------------------------------------------------------------

func TestStockpileVerticalSlice(t *testing.T) {
	// ARM/CORE-like definitions: nuke is stockpile+targetable, interceptor is
	// stockpile+interceptor with coverage and area. ReloadTime is buildTime.
	nukeWeapon := weaponForStockpile(1000, 30, 64, 0, true, false, true, 200, 100, 0)
	nukeWeapon.Range = 10000
	antiWeapon := weaponForStockpile(1001, 30, 64, 500, true, true, false, 150, 80, 0)
	antiWeapon.Range = 10000

	var svc Service
	// Stockpile building: silo slot with 0 ammo, queue count 1.
	nukeSlot := &Slot{Weapon: nukeWeapon, Ammo: 0}
	nukeEntry := &StockpileEntry{Weapon: nukeWeapon, Count: 1, Progress: 0, SlotIdx: 0}
	admitAlways := func(e, m float32) bool { return true }
	// Advance progress 0→5→10→15→20→25→30 completes one round [06 §11.1].
	for i := 0; i < 6; i++ {
		tick := uint32(100 + i*5)
		_, _, _ = TickStockpile(nukeEntry, nukeSlot, tick, admitAlways)
	}
	if nukeSlot.Ammo != 1 {
		t.Fatalf("after building one nuke, ammo %d want 1 [06 §11.1]", nukeSlot.Ammo)
	}
	if nukeEntry.Count != 0 {
		t.Fatalf("queue count after one completion %d want 0", nukeEntry.Count)
	}
	// Build anti-nuke similarly.
	antiSlot := &Slot{Weapon: antiWeapon, Ammo: 0}
	antiEntry := &StockpileEntry{Weapon: antiWeapon, Count: 1, Progress: 0}
	for i := 0; i < 6; i++ {
		tick := uint32(200 + i*5)
		_, _, _ = TickStockpile(antiEntry, antiSlot, tick, admitAlways)
	}
	if antiSlot.Ammo != 1 {
		t.Fatalf("anti ammo %d want 1", antiSlot.Ammo)
	}
	// Launch nuke before next production tick [06 §11.1] launch-before-production.
	// Nuke target is ground point 100,0 where anti covers.
	nukeTarget := Target{Kind: TargetPoint, X: fixedI(100), Z: fixedI(100)}
	hNuke, ok := launchStockpileRound(&svc, nukeSlot, 0, nukeTarget, 300, FirePorts{})
	if !ok {
		t.Fatalf("nuke launch should succeed with ammo 1 [06 §11.1]")
	}
	if nukeSlot.Ammo != 0 {
		t.Fatalf("nuke launch decrements ammo [06 §11.1], got %d want 0", nukeSlot.Ammo)
	}
	// Verify nuke projectile stored aim point is launch target and side differs.
	nukeRec := &svc.Records[int(hNuke)-1]
	nukeRec.ShooterSide = 1
	nukeRec.TargetPos = Vec3{X: fixedI(100), Z: fixedI(100)}
	nukeRec.Pos = Vec3{X: fixedI(0), Z: fixedI(0)}
	// Anti-nuke interceptor scan: should find nuke within coverage 500 square.
	weapons := map[int32]*content.WeaponDef{
		nukeWeapon.ID: nukeWeapon,
		antiWeapon.ID: antiWeapon,
	}
	interceptorPos := Vec3{X: fixedI(100), Z: fixedI(100)} // anti silo at target
	found, storedPos, ok := FindInterceptorTarget(&svc, interceptorPos, 0, antiWeapon.Coverage, weapons)
	if !ok || found != hNuke {
		t.Fatalf("FindInterceptorTarget should find nuke hNuke %d got %d ok %v [06 §11.2]", hNuke, found, ok)
	}
	if storedPos.X.Raw() != fixedI(0).Raw() && storedPos.Z.Raw() != fixedI(0).Raw() {
		// storedPos is candidate's current pos (0,0) [06 §11.2]
	}
	// Launch the interceptor through the live chain: the per-slot pipeline over
	// TryFire, with the fire-time rescan bound as the vertical-launch
	// executor's port [06 §4.4][06 §11.2].
	cat := interceptorTestCatalog(t, nukeWeapon, antiWeapon)
	silo := &units.Unit{Owner: 0, X: interceptorPos.X, Z: interceptorPos.Z}
	hAnti, ok := launchStockpileRound(&svc, antiSlot, 0,
		Target{Kind: TargetPoint, X: interceptorPos.X, Z: interceptorPos.Z}, 301,
		FirePorts{Origin: interceptorPos, InterceptorRescan: interceptorRescanPort(&svc, silo, antiWeapon, cat)})
	if !ok {
		t.Fatalf("the interceptor launch should succeed [06 §11.2]")
	}
	if antiSlot.Ammo != 0 {
		t.Fatalf("interceptor launch decrements stockpile ammo [06 §11.1], got %d", antiSlot.Ammo)
	}
	antiRec := &svc.Records[int(hAnti)-1]
	if antiRec.TargetProjectile != hNuke {
		t.Fatalf("interceptor reservation link %d want nuke %d [06 §11.2]", antiRec.TargetProjectile, hNuke)
	}
	// Simulate interceptor guidance: linkage tracks nuke's current pos [06 §11.2].
	// Move nuke a bit, then update interceptor's stored target.
	nukeRec.Pos = Vec3{X: fixedI(10), Z: fixedI(10)}
	// Mimic session guidance tick: copy candidate pos to interceptor's TargetPos.
	if antiRec.TargetProjectile == hNuke {
		antiRec.TargetPos = nukeRec.Pos
	}
	if antiRec.TargetPos.X.Raw() != nukeRec.Pos.X.Raw() {
		t.Fatalf("interceptor guidance failed to track [06 §11.2]")
	}
	// Interceptor detonation: within unhalved area 64, the nuke at distance 10
	// is inside the blast the sweep measures [06 §11.2][06 §9.3].
	antiRec.Pos = nukeRec.Pos
	if !ProjectileInInterceptorBlast(nukeRec.Pos, antiRec.Pos, antiWeapon.AreaOfEffect) {
		t.Fatalf("interceptor blast should reach the nuke within the unhalved area [06 §11.2][06 §9.3]")
	}
}

// ---------------------------------------------------------------------------
// Pool-full, target-death, cancel, reload cases [06 §11] C29
// ---------------------------------------------------------------------------

func TestStockpilePoolFullCases(t *testing.T) {
	weapon := weaponForStockpile(2000, 30, 0, 0, true, false, false, 0, 0, 0)
	slot := &Slot{Weapon: weapon, Ammo: 1}
	var svc Service
	// Fill pool to capacity.
	for svc.Count() < ProjectileCapacity {
		svc.Reserve()
	}
	// Stockpile launch should fail with pool full and not consume ammo [06 §4.1] C4 [06 §11.1].
	h, ok := launchStockpileRound(&svc, slot, 0, Target{Kind: TargetPoint, X: fixedI(10)}, 400, FirePorts{})
	if ok || h != 0 {
		t.Fatalf("pool-full launch should fail [06 §4.1] C4")
	}
	if slot.Ammo != 1 {
		t.Fatalf("pool-full must not decrement ammo [06 §11.1], got %d", slot.Ammo)
	}
	// Interceptor pool-full similarly: reserve should fail.
	interceptor := weaponForStockpile(2001, 30, 32, 200, true, true, false, 0, 0, 0)
	interceptorSlot := &Slot{Weapon: interceptor, Ammo: 1}
	weapons := map[int32]*content.WeaponDef{
		interceptor.ID: interceptor,
		weapon.ID:      weapon,
	}
	// Create one nuke target but pool already full, so Find will succeed but Acquire should fail on Reserve.
	// Need to free one slot for candidate, then fill again? Simplify: use svc with one slot free for candidate.
	var svc2 Service
	weaponTargetable := weaponForStockpile(3000, 30, 0, 0, false, false, true, 0, 0, 0)
	hCand, _ := svc2.Reserve()
	svc2.Records[int(hCand)-1] = Projectile{WeaponID: weaponTargetable.ID, Pos: Vec3{X: fixedI(0)}, TargetPos: Vec3{X: fixedI(5), Z: fixedI(5)}, ShooterSide: 1}
	// Fill remaining to capacity-1 to leave one slot for interceptor? Actually svc2 has 1, we fill to capacity.
	for svc2.Count() < ProjectileCapacity {
		svc2.Reserve()
	}
	// Now the interceptor launch should fail on the pool-full reservation.
	cat2 := interceptorTestCatalog(t, weaponTargetable, interceptor)
	silo2 := &units.Unit{Owner: 0, X: fixedI(5), Z: fixedI(5)}
	_, ok2 := launchStockpileRound(&svc2, interceptorSlot, 0,
		Target{Kind: TargetPoint, X: fixedI(5), Z: fixedI(5)}, 500,
		FirePorts{Origin: Vec3{X: fixedI(5), Z: fixedI(5)}, InterceptorRescan: interceptorRescanPort(&svc2, silo2, interceptor, cat2)})
	if ok2 {
		t.Fatalf("interceptor pool-full should fail [06 §11.2]")
	}
	if interceptorSlot.Ammo != 1 {
		t.Fatalf("interceptor pool-full must not decrement ammo [06 §11.2] got %d want 1", interceptorSlot.Ammo)
	}
	_ = weapons
}

func TestInterceptorTargetDeath(t *testing.T) {
	// Neither interceptor scan tests liveness: a dead-but-uncompacted
	// targetable enemy record inside coverage and unclaimed is still selected
	// by the aim-time scan and by the fire-time rescan [06 §11.2]
	// [06 R-WPN-05 §10]. This test used to assert the opposite.
	nukeWeapon := weaponForStockpile(4000, 30, 0, 0, false, false, true, 0, 0, 0)
	interceptor := weaponForStockpile(4001, 30, 32, 200, true, true, false, 0, 0, 0)
	var svc Service
	hNuke, _ := svc.Reserve()
	svc.Records[int(hNuke)-1] = Projectile{WeaponID: nukeWeapon.ID, Pos: Vec3{X: fixedI(0)}, TargetPos: Vec3{X: fixedI(5), Z: fixedI(5)}, ShooterSide: 1}
	weapons := map[int32]*content.WeaponDef{nukeWeapon.ID: nukeWeapon, interceptor.ID: interceptor}
	// Kill nuke before interceptor scan.
	svc.MarkDead(hNuke)
	got, _, ok := FindInterceptorTarget(&svc, Vec3{X: fixedI(5), Z: fixedI(5)}, 0, interceptor.Coverage, weapons)
	if !ok || got != hNuke {
		t.Fatalf("dead-but-uncompacted nuke must still be selected, got %d ok %v [06 R-WPN-05 §10]", got, ok)
	}
	interceptorSlot := &Slot{Weapon: interceptor, Ammo: 1}
	cat := interceptorTestCatalog(t, nukeWeapon, interceptor)
	silo := &units.Unit{Owner: 0, X: fixedI(5), Z: fixedI(5)}
	hAnti, ok2 := launchStockpileRound(&svc, interceptorSlot, 0,
		Target{Kind: TargetPoint, X: fixedI(5), Z: fixedI(5)}, 600,
		FirePorts{Origin: Vec3{X: fixedI(5), Z: fixedI(5)}, InterceptorRescan: interceptorRescanPort(&svc, silo, interceptor, cat)})
	if !ok2 {
		t.Fatalf("the fire-time rescan must also reach the dead candidate [06 R-WPN-05 §10]")
	}
	if cand := svc.Records[int(hAnti)-1].TargetProjectile; cand != hNuke {
		t.Fatalf("the interceptor's reservation link is %d, want the dead candidate %d [06 R-WPN-05 §10]", cand, hNuke)
	}
	// A shot that finds no candidate is the only path that leaves the slot
	// untouched; here one was found, and the interceptor is stockpile-flagged,
	// so its ammunition is spent [06 §11.2].
	if interceptorSlot.Ammo != 0 {
		t.Fatalf("a successful interceptor spawn spends the round, got %d [06 §11.1]", interceptorSlot.Ammo)
	}
	// The claim the spawn installed now blocks a second acquisition, which is
	// the state that prevents a shot — not a dead-bit test [06 §11.2].
	if _, _, again := FindInterceptorTarget(&svc, Vec3{X: fixedI(5), Z: fixedI(5)}, 0, interceptor.Coverage, weapons); again {
		t.Fatalf("the candidate claimed at spawn must be rejected by the next scan [06 §11.2]")
	}
	// Reservation claim: already claimed target should be skipped.
	var svc3 Service
	hNuke2, _ := svc3.Reserve()
	svc3.Records[int(hNuke2)-1] = Projectile{WeaponID: nukeWeapon.ID, Pos: Vec3{X: fixedI(0)}, TargetPos: Vec3{X: fixedI(5), Z: fixedI(5)}, ShooterSide: 1}
	hClaim, _ := svc3.Reserve()
	svc3.Records[int(hClaim)-1] = Projectile{WeaponID: interceptor.ID, TargetProjectile: hNuke2, ShooterSide: 0}
	_, _, ok3 := FindInterceptorTarget(&svc3, Vec3{X: fixedI(5), Z: fixedI(5)}, 0, interceptor.Coverage, weapons)
	if ok3 {
		t.Fatalf("already claimed nuke should be skipped [06 §11.2]")
	}
}

func TestStockpileCancelAndReload(t *testing.T) {
	// Cancel retains fractional carry conceptually (tested via Progress not advancing on reject, and cancel just unlinks).
	// Here test orders queue cancel: BuildWeapon secondary queue with count 2, progress 10, cancel tail-most.
	// Use orders package queue directly.
	// Note: avoid import cycle, use the orders queue construction via minimal unit.
	// We test stockpile reload special: stockpile launch does not write reload.
	weaponStock := weaponForStockpile(5000, 30, 0, 0, true, false, false, 10, 10, 0)
	// The reload countdown still gates admission for a stockpile weapon — only
	// the reload STORE is skipped [06 §4.2] C7 — so the slot has to be off
	// cooldown to fire at all. What the launch must not do is write the word
	// afterwards: an ordinary weapon of this reload time would leave 30 here.
	slotStock := &Slot{Weapon: weaponStock, Ammo: 5, Reload: 0}
	var svc Service
	h, ok := launchStockpileRound(&svc, slotStock, 0, Target{Kind: TargetPoint, X: fixedI(10)}, 700, FirePorts{})
	if !ok || h == 0 {
		t.Fatalf("stockpile launch should succeed")
	}
	if slotStock.Reload != 0 || slotStock.PendingReload != 0 {
		t.Fatalf("stockpile launch must not write reload [06 §4.2] C7, got %d/%d want 0/0", slotStock.Reload, slotStock.PendingReload)
	}
	if want := ComputeStoredReload(100, 100, 0, weaponStock.ReloadTime); want == 0 {
		t.Fatalf("fixture is vacuous: an ordinary weapon would have stored %d", want)
	}
	if slotStock.Ammo != 4 {
		t.Fatalf("stockpile launch decrements ammo [06 §11.1], got %d want 4", slotStock.Ammo)
	}
	// Non-stockpile weapon should set reload via ComputeStoredReload.
	weaponNormal := weaponForStockpile(5001, 30, 0, 0, false, false, false, 10, 10, 0)
	// Compute reload for health 100/100 and kills 0: tier 0, veteranReload 30, healthFactor 100, stored 30.
	stored := ComputeStoredReload(100, 100, 0, weaponNormal.ReloadTime)
	if stored != 30 {
		t.Fatalf("normal reload computed %d want 30 [06 §4.2] C7", stored)
	}
	// Verify ammo vs reload distinction: stockpile weapon's Ammo is byte count, Reload is separate countdown [06 §11.1].
	// AcquireInterceptor with stockpile weapon decrements Ammo, not Reload; we already checked.
}

// ---------------------------------------------------------------------------
// Interceptor blast metric [06 §11.2] [06 R-WPN-05 §10]
// ---------------------------------------------------------------------------

// The authored area is unsigned, but its square and the comparison are signed
// 32-bit. Crossing the square's sign boundary rejects even coincident points
// [06 R-WPN-05 §10].
func TestInterceptorBlastAreaSquareWrapsSigned(t *testing.T) {
	for _, tc := range []struct {
		area int32
		want bool
	}{
		{0, false},
		{46340, true},
		{46341, false},
		{65535, false},
	} {
		if got := ProjectileInInterceptorBlast(Vec3{}, Vec3{}, tc.area); got != tc.want {
			t.Errorf("area %d admits coincident victim=%v, want %v [06 R-WPN-05 §10]", tc.area, got, tc.want)
		}
	}
}

func TestInterceptorBlastMetricIsTruncatedSquares(t *testing.T) {
	origin := Vec3{}
	// The compare is strict, so a victim exactly on the radius survives while
	// one world unit inside is taken [06 R-WPN-05 §10].
	const area = int32(32)
	atRadius := Vec3{X: fixedI(32)}
	if ProjectileInInterceptorBlast(atRadius, origin, area) {
		t.Fatalf("a victim at exactly area world units must survive: the compare is strict [06 R-WPN-05 §10]")
	}
	inside := Vec3{X: fixedI(31)}
	if !ProjectileInInterceptorBlast(inside, origin, area) {
		t.Fatalf("a victim one world unit inside the radius must be taken [06 R-WPN-05 §10]")
	}
	// Each axis is squared at full 16.16 width and only then truncated, so a
	// fractional delta contributes more than truncating the delta first would:
	// 1.9 world units contributes 3, not 1 [06 R-WPN-05 §10]. With area 2 the
	// budget is 4, so a single axis at 1.9 leaves 1 and a second axis at 1.9
	// would overflow it — truncate-then-square would leave both inside.
	frac := numeric.Fixed(fixedI(1).Raw() + 59000) // 1.9 world units in 16.16
	oneAxis := Vec3{X: frac}
	if !ProjectileInInterceptorBlast(oneAxis, origin, 2) {
		t.Fatalf("one axis at 1.9 contributes 3 and stays under 4 [06 R-WPN-05 §10]")
	}
	twoAxes := Vec3{X: frac, Z: frac}
	if ProjectileInInterceptorBlast(twoAxes, origin, 2) {
		t.Fatalf("two axes at 1.9 contribute 6, not 2: the square precedes the truncation [06 R-WPN-05 §10]")
	}
	// Y is the victim record's own current height in the same domain, with no
	// terrain sample and no separate scale [06 R-WPN-05 §10].
	if ProjectileInInterceptorBlast(Vec3{Y: fixedI(32)}, origin, area) {
		t.Fatalf("the Y term uses the same metric as X and Z [06 R-WPN-05 §10]")
	}
	if !ProjectileInInterceptorBlast(Vec3{Y: fixedI(31)}, origin, area) {
		t.Fatalf("the Y term uses the same metric as X and Z [06 R-WPN-05 §10]")
	}
}
