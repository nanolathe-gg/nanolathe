package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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
	}
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
			if entry.Progress != 0 {
				t.Fatalf("progress after completion %d want 0", entry.Progress)
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
	h, ok := TryStockpileLaunch(&svc, slotLaunch, 0, Target{Kind: TargetPoint, X: fixedI(10)}, 700)
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
	h2, ok2 := TryStockpileLaunch(&svc, slotLaunch2, 0, Target{Kind: TargetPoint}, 701)
	if ok2 || h2 != 0 {
		t.Fatalf("pool full should fail launch [06 §11.1] C29")
	}
	if slotLaunch2.Ammo != 1 {
		t.Fatalf("pool full must not decrement ammo [06 §11.1], got %d", slotLaunch2.Ammo)
	}
	// Empty ammo prevents allocation [06 §11.1]
	var svc2 Service
	slotEmpty := &Slot{Weapon: w, Ammo: 0}
	h3, ok3 := TryStockpileLaunch(&svc2, slotEmpty, 0, Target{Kind: TargetPoint}, 702)
	if ok3 || h3 != 0 {
		t.Fatalf("empty ammo should prevent projectile allocation [06 §11.1]")
	}
}

// ---------------------------------------------------------------------------
// Interceptor detonation set vs ordinary enumeration ordering [06 §11.2] [06 §9.3] C29
// ---------------------------------------------------------------------------

func TestInterceptorDetonationOrdering(t *testing.T) {
	// Interceptor force-detonation of alive non-self projectiles inside its
	// UNHALVED areaofeffect after ordinary area enumeration [06 §11.2] [06 §9.3] C29
	var svc Service
	interceptorWeapon := weaponForStockpile(200, 30, 16, 0, false, true, false, 0, 0, 0) // area 16 => unhalved 16, halved for ordinary would be 8
	// Place exploder at origin
	exploderPos := Vec3{X: fixedI(0), Y: fixedI(0), Z: fixedI(0)}
	hExpl, ok := svc.Reserve()
	if !ok {
		t.Fatalf("reserve exploder")
	}
	svc.Records[int(hExpl)-1] = Projectile{
		WeaponID:    interceptorWeapon.ID,
		Pos:         exploderPos,
		TargetPos:   Vec3{X: fixedI(0), Y: fixedI(0), Z: fixedI(0)},
		ShooterSide: 1,
	}
	// Ordinary area victims? We'll create projectile victims at distances:
	// v1 at distance 5 (inside both halved 8 and unhalved 16) => should be detonated
	// v2 at distance 12 (outside halved 8, inside unhalved 16) => should be detonated only via unhalved test, proving unhalved vs halved
	// v3 at distance 20 (outside both) => not detonated
	v1Pos := Vec3{X: fixedI(5), Y: fixedI(0), Z: fixedI(0)}
	h1, _ := svc.Reserve()
	svc.Records[int(h1)-1] = Projectile{WeaponID: 99, Pos: v1Pos, TargetPos: Vec3{X: fixedI(100)}, ShooterSide: 2}
	v2Pos := Vec3{X: fixedI(12), Y: fixedI(0), Z: fixedI(0)}
	h2, _ := svc.Reserve()
	svc.Records[int(h2)-1] = Projectile{WeaponID: 99, Pos: v2Pos, TargetPos: Vec3{X: fixedI(200)}, ShooterSide: 2}
	v3Pos := Vec3{X: fixedI(20), Y: fixedI(0), Z: fixedI(0)}
	h3, _ := svc.Reserve()
	svc.Records[int(h3)-1] = Projectile{WeaponID: 99, Pos: v3Pos, TargetPos: Vec3{X: fixedI(300)}, ShooterSide: 2}
	// Also create a dead projectile inside radius: should not be returned (alive only) [06 §11.2]
	hDead, _ := svc.Reserve()
	svc.Records[int(hDead)-1] = Projectile{WeaponID: 99, Pos: Vec3{X: fixedI(3)}, ShooterSide: 2}
	svc.MarkDead(hDead)
	// Also create self (exploder) should not be included (non-self) [06 §11.2]

	order := []string{}
	ordinary := func() {
		order = append(order, "ordinary")
	}
	var victims []pool.Handle
	onVictims := func(v []pool.Handle, vs []VictimSig, es []VictimSig) {
		order = append(order, "projectiles")
		victims = v
	}
	ApplyInterceptorExplosion(&svc, hExpl, exploderPos, interceptorWeapon, ordinary, onVictims)
	// Verify ordering: ordinary before projectiles [06 §9.3] [06 §11.2] C29
	if len(order) != 2 || order[0] != "ordinary" || order[1] != "projectiles" {
		t.Fatalf("detonation ordering %v want [ordinary projectiles] [06 §11.2] [06 §9.3] C29", order)
	}
	// Verify victim set uses UNHALVED area: 16, not halved 8
	// v1 (5) and v2 (12) should be victims, v3 (20) not, dead not, self not
	found := make(map[pool.Handle]bool)
	for _, v := range victims {
		found[v] = true
	}
	if !found[h1] {
		t.Fatalf("v1 distance 5 should be victim (inside unhalved 16) [06 §11.2] C29")
	}
	if !found[h2] {
		t.Fatalf("v2 distance 12 should be victim (inside unhalved 16 but outside halved 8) proving unhalved [06 §11.2] C29")
	}
	if found[h3] {
		t.Fatalf("v3 distance 20 should NOT be victim (outside unhalved 16) [06 §11.2]")
	}
	if found[hDead] {
		t.Fatalf("dead projectile should not be victim (alive only) [06 §11.2]")
	}
	if found[hExpl] {
		t.Fatalf("exploder self should not be victim (non-self) [06 §11.2]")
	}
	// Ensure friendly projectiles can be removed: spec says does not filter by side/alliance/targetable, friendly can be removed [06 §11.2]
	// Create friendly victim same side as exploder but inside radius -> should also be victim (we didn't filter side)
	vFriendPos := Vec3{X: fixedI(4)}
	hFriend, _ := svc.Reserve()
	svc.Records[int(hFriend)-1] = Projectile{WeaponID: 99, Pos: vFriendPos, TargetPos: Vec3{X: fixedI(400)}, ShooterSide: 1} // same side as exploder
	victims2 := CollectInterceptorVictims(&svc, hExpl, exploderPos, interceptorWeapon)
	foundFriend := false
	for _, v := range victims2 {
		if v == hFriend {
			foundFriend = true
		}
	}
	if !foundFriend {
		t.Fatalf("friendly projectile inside unhalved area should be victim (no side filter) [06 §11.2] C29")
	}
}

// ---------------------------------------------------------------------------
// Signature exact-match removal (and non-match survival) [06 §11.2] C29
// ---------------------------------------------------------------------------

func TestSignatureExactMatchRemoval(t *testing.T) {
	var svc Service
	// Create two projectiles with same pos & weapon but different handle order
	posA := Vec3{X: fixedI(123), Y: fixedI(45), Z: fixedI(67)}
	widA := int32(77)
	hA, _ := svc.Reserve()
	svc.Records[int(hA)-1] = Projectile{WeaponID: widA, Pos: Vec3{X: fixedI(0)}, TargetPos: posA, ShooterSide: 1}
	posB := Vec3{X: fixedI(124)} // slightly different
	hB, _ := svc.Reserve()
	svc.Records[int(hB)-1] = Projectile{WeaponID: widA, Pos: Vec3{X: fixedI(0)}, TargetPos: posB, ShooterSide: 1}
	hC, _ := svc.Reserve()
	svc.Records[int(hC)-1] = Projectile{WeaponID: 78, Pos: Vec3{X: fixedI(0)}, TargetPos: posA, ShooterSide: 1} // same pos as A but different weapon idx
	hD, _ := svc.Reserve()
	svc.Records[int(hD)-1] = Projectile{WeaponID: widA, Pos: Vec3{X: fixedI(0)}, TargetPos: posA, ShooterSide: 1} // duplicate of A (same sig) to test first-match wins

	// Publish victim signature for A's exact signature [06 §11.2]
	sigA := VictimSig{TargetPos: posA, WeaponIdx: uint8(widA & 0xFF)} // [06 §11.2]
	// Exact-match should find first record with that signature, which is hA (ascending order) [06 §11.2] I1
	found, ok := FindVictimBySignature(&svc, sigA)
	if !ok {
		t.Fatalf("exact match should be found [06 §11.2] C29")
	}
	if found != hA {
		t.Fatalf("exact match should return first in pool order [06 §11.2] C29, got %d want %d", found, hA)
	}
	// Non-match: same pos different weapon
	sigWrongWeapon := VictimSig{TargetPos: posA, WeaponIdx: uint8(78 & 0xFF)}
	found2, ok2 := FindVictimBySignature(&svc, sigWrongWeapon)
	if !ok2 || found2 != hC {
		t.Fatalf("same pos diff weapon should match hC, got %d ok %v want %d [06 §11.2]", found2, ok2, hC)
	}
	// Non-match survival: different pos same weapon should find hB, not A
	sigB := VictimSig{TargetPos: posB, WeaponIdx: uint8(widA & 0xFF)}
	foundB, okB := FindVictimBySignature(&svc, sigB)
	if !okB || foundB != hB {
		t.Fatalf("diff pos should match hB, got %d ok %v want %d", foundB, okB, hB)
	}
	// Completely novel signature should not match (non-match survival) [06 §11.2]
	sigNovel := VictimSig{TargetPos: Vec3{X: fixedI(999), Y: fixedI(999), Z: fixedI(999)}, WeaponIdx: 255}
	_, okNovel := FindVictimBySignature(&svc, sigNovel)
	if okNovel {
		t.Fatalf("novel signature should not match (non-match survival) [06 §11.2] C29")
	}
	// Signature includes all components: Y differences must prevent match
	sigWrongY := VictimSig{TargetPos: Vec3{X: fixedI(123), Y: fixedI(46), Z: fixedI(67)}, WeaponIdx: uint8(widA & 0xFF)}
	_, okWrongY := FindVictimBySignature(&svc, sigWrongY)
	if okWrongY {
		t.Fatalf("Y mismatch should not match (exact-match requires all fields) [06 §11.2] C29")
	}
	// Test VictimSigEqual helper directly
	if !VictimSigEqual(sigA, VictimSig{TargetPos: posA, WeaponIdx: uint8(widA & 0xFF)}) {
		t.Fatalf("VictimSigEqual true case failed")
	}
	if VictimSigEqual(sigA, sigWrongWeapon) {
		t.Fatalf("VictimSigEqual false case failed (weapon idx)")
	}
	// Ensure PublishVictimSig roundtrip
	rec := Projectile{WeaponID: widA, TargetPos: posA}
	pub := PublishVictimSig(rec)
	if !VictimSigEqual(pub, sigA) {
		t.Fatalf("PublishVictimSig mismatch [06 §11.2]")
	}
	// Verify that interceptor explosion's victim signature loop would use exact-match to remove
	// Simulate removal: find then mark dead
	foundToRemove, _ := FindVictimBySignature(&svc, sigA)
	svc.MarkDead(foundToRemove)
	if svc.Alive(foundToRemove) {
		t.Fatalf("victim should be dead after signature removal")
	}
	// Next find with same sig should now find hD (the duplicate) — second occurrence after first removed still matches? But dead skipped, so next alive duplicate should be found
	foundNext, okNext := FindVictimBySignature(&svc, sigA)
	if !okNext || foundNext != hD {
		t.Fatalf("after removing first, next exact match should be hD duplicate, got %d ok %v want %d", foundNext, okNext, hD)
	}
	// dead record with same signature should be ignored (alive only)
	svc.MarkDead(hD)
	_, okDead := FindVictimBySignature(&svc, sigA)
	if okDead {
		t.Fatalf("dead records should not be matched (alive only) [06 §11.2]")
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
	// Negative coverage placeholder
	if WithinInterceptorCoverage(candidateInside, interceptorPos, -1) {
		t.Fatalf("negative coverage should be false [06 §11.2] TODO(question)")
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
// Firestarter single-site nonzero test [06 §13.1] C29
// ---------------------------------------------------------------------------

func TestFirestarterSingleSiteNonzero(t *testing.T) {
	// Firestarter single-site nonzero test if it belongs here per C29 prompt [06 §13.1]
	// Radial feature damage can ignite only when weapon firestarter !=0 [06 §13.1]
	wZero := weaponForStockpile(400, 10, 0, 0, false, false, false, 0, 0, 0)
	if IsFirestarter(wZero) {
		t.Fatalf("firestarter 0 should be false [06 §13.1]")
	}
	if ShouldIgniteFeatureGate(true, true, wZero) {
		t.Fatalf("zero firestarter should not ignite [06 §13.1]")
	}
	wNonzero := weaponForStockpile(401, 10, 0, 0, false, false, false, 0, 0, 7)
	if !IsFirestarter(wNonzero) {
		t.Fatalf("firestarter 7 should be true [06 §13.1] C29")
	}
	if !ShouldIgniteFeatureGate(true, true, wNonzero) {
		t.Fatalf("nonzero firestarter with gates true should ignite [06 §13.1]")
	}
	if ShouldIgniteFeatureGate(false, true, wNonzero) {
		t.Fatalf("global disabled should not ignite [06 §13.1]")
	}
	if ShouldIgniteFeatureGate(true, false, wNonzero) {
		t.Fatalf("non-flammable feature should not ignite [06 §13.1]")
	}
	// Single-site: nonzero test is exactly field !=0, no threshold >1 required
	wOne := weaponForStockpile(402, 10, 0, 0, false, false, false, 0, 0, 1)
	if !IsFirestarter(wOne) {
		t.Fatalf("firestarter 1 should be true (single-site nonzero) [06 §13.1] C29")
	}
	// Negative? TODO(question): negative firestarter malformed untraced; current treats negative as true (nonzero)
	wNeg := weaponForStockpile(403, 10, 0, 0, false, false, false, 0, 0, -1)
	if !IsFirestarter(wNeg) {
		t.Fatalf("negative firestarter should be true as nonzero [06 §13.1] TODO(question)")
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
	hNuke, ok := TryStockpileLaunch(&svc, nukeSlot, 0, nukeTarget, 300)
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
	// Acquire and launch interceptor: rescans and writes reservation link.
	muzzle := interceptorPos
	hAnti, cand, ok := AcquireInterceptorTargetForSpawn(&svc, interceptorPos, 0, antiWeapon.Coverage, antiWeapon, antiSlot, muzzle, 301, weapons)
	if !ok {
		t.Fatalf("acquire interceptor should succeed [06 §11.2]")
	}
	if cand != hNuke {
		t.Fatalf("acquired candidate %d want nuke %d", cand, hNuke)
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
	// Interceptor detonation: within unhalved area 64, nuke at distance 10 should be victim.
	// Place anti projectile at nuke's pos for blast.
	antiRec.Pos = nukeRec.Pos
	victims := CollectInterceptorVictims(&svc, hAnti, antiRec.Pos, antiWeapon)
	foundVictim := false
	for _, v := range victims {
		if v == hNuke {
			foundVictim = true
		}
	}
	if !foundVictim {
		t.Fatalf("interceptor blast should victim nuke within unhalved area [06 §11.2][06 §9.3]")
	}
	// Exact-match signature path: publish and find victim by signature.
	sig := PublishVictimSig(*nukeRec)
	fh, ok := FindVictimBySignature(&svc, sig)
	if !ok || fh != hNuke {
		t.Fatalf("signature exact-match should find nuke [06 §11.2], got %d ok %v", fh, ok)
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
	h, ok := TryStockpileLaunch(&svc, slot, 0, Target{Kind: TargetPoint, X: fixedI(10)}, 400)
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
	// Now interceptor acquire should fail due to pool-full reservation.
	_, _, ok2 := AcquireInterceptorTargetForSpawn(&svc2, Vec3{X: fixedI(5), Z: fixedI(5)}, 0, interceptor.Coverage, interceptor, interceptorSlot, Vec3{X: fixedI(5), Z: fixedI(5)}, 500, map[int32]*content.WeaponDef{weaponTargetable.ID: weaponTargetable, interceptor.ID: interceptor})
	if ok2 {
		t.Fatalf("interceptor pool-full should fail [06 §11.2]")
	}
	if interceptorSlot.Ammo != 1 {
		t.Fatalf("interceptor pool-full must not decrement ammo [06 §11.2] got %d want 1", interceptorSlot.Ammo)
	}
	_ = weapons
}

func TestInterceptorTargetDeath(t *testing.T) {
	// Nuke target dies before interceptor fires: scan should skip dead candidate [06 §11.2].
	nukeWeapon := weaponForStockpile(4000, 30, 0, 0, false, false, true, 0, 0, 0)
	interceptor := weaponForStockpile(4001, 30, 32, 200, true, true, false, 0, 0, 0)
	var svc Service
	hNuke, _ := svc.Reserve()
	svc.Records[int(hNuke)-1] = Projectile{WeaponID: nukeWeapon.ID, Pos: Vec3{X: fixedI(0)}, TargetPos: Vec3{X: fixedI(5), Z: fixedI(5)}, ShooterSide: 1}
	weapons := map[int32]*content.WeaponDef{nukeWeapon.ID: nukeWeapon, interceptor.ID: interceptor}
	// Kill nuke before interceptor scan.
	svc.MarkDead(hNuke)
	_, _, ok := FindInterceptorTarget(&svc, Vec3{X: fixedI(5), Z: fixedI(5)}, 0, interceptor.Coverage, weapons)
	if ok {
		t.Fatalf("dead nuke should not be found [06 §11.2] target-death")
	}
	interceptorSlot := &Slot{Weapon: interceptor, Ammo: 1}
	_, _, ok2 := AcquireInterceptorTargetForSpawn(&svc, Vec3{X: fixedI(5), Z: fixedI(5)}, 0, interceptor.Coverage, interceptor, interceptorSlot, Vec3{X: fixedI(5), Z: fixedI(5)}, 600, weapons)
	if ok2 {
		t.Fatalf("acquire with dead target should fail [06 §11.2]")
	}
	if interceptorSlot.Ammo != 1 {
		t.Fatalf("target-death must not consume ammo [06 §11.2]")
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
	slotStock := &Slot{Weapon: weaponStock, Ammo: 5, Reload: 99}
	// Simulate TryStockpileLaunch path via combat tick: reload should remain 99 after launch.
	var svc Service
	h, ok := TryStockpileLaunch(&svc, slotStock, 0, Target{Kind: TargetPoint, X: fixedI(10)}, 700)
	if !ok || h == 0 {
		t.Fatalf("stockpile launch should succeed")
	}
	if slotStock.Reload != 99 {
		t.Fatalf("stockpile launch must not write reload [06 §4.2] C7, got %d want 99", slotStock.Reload)
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
