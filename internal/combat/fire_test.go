package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

func weaponForFire(id int32, reload int32, spray int32, burst int32, burstRate int32, stockpile bool, dropped bool, meteor bool, startSmoke bool, soundStart string, energy float64, metal float64) *content.WeaponDef {
	return &content.WeaponDef{
		ID:            id,
		ReloadTime:    reload,
		SprayAngle:    spray,
		Burst:         burst,
		BurstRate:     burstRate,
		Stockpile:     stockpile,
		Dropped:       dropped,
		Meteor:        meteor,
		StartSmoke:    startSmoke,
		SoundStart:    soundStart,
		EnergyPerShot: energy,
		MetalPerShot:  metal,
	}
}

func TestFireCallbackOrder(t *testing.T) {
	// Ordinary weapon: full chain alloc → startSound → FirePrimary → RockUnit → startSmoke [06 §4.1] C2.
	t.Run("ordinaryPrimary", func(t *testing.T) {
		var svc Service
		w := weaponForFire(1, 10, 0, 0, 0, false, false, false, true, "sound/start.wav", 0, 0)
		slot := &Slot{Weapon: w, Reload: 0, Target: Target{Kind: TargetPoint, X: numeric.FixedFromInt(10)}}
		spy := &FireSpy{}
		muzzle := func(int) int32 { return 5 }
		r := rng.NewSimulation(1)
		h, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint, X: numeric.FixedFromInt(10), Z: numeric.FixedFromInt(10)}, 0, 100, 100, 0, &r, muzzle, spy, nil)
		if !ok || h == 0 {
			t.Fatalf("expected fire ok")
		}
		want := []string{"alloc", "startSound", "FirePrimary", "RockUnit", "startSmoke"}
		if len(spy.Events) != len(want) {
			t.Fatalf("events %v want %v", spy.Events, want)
		}
		for i, ev := range want {
			if spy.Events[i] != ev {
				t.Fatalf("event %d got %s want %s (all %v)", i, spy.Events[i], ev, spy.Events)
			}
		}
	})
	t.Run("ordinarySecondary", func(t *testing.T) {
		var svc Service
		w := weaponForFire(2, 10, 0, 0, 0, false, false, false, true, "s.wav", 0, 0)
		slot := &Slot{Weapon: w}
		spy := &FireSpy{}
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 1, Target{Kind: TargetPoint, X: numeric.FixedFromInt(1)}, 0, 100, 100, 0, &r, func(int) int32 { return 1 }, spy, nil)
		if !ok {
			t.Fatalf("fire secondary failed")
		}
		want := []string{"alloc", "startSound", "FireSecondary", "RockUnit", "startSmoke"}
		if len(spy.Events) != len(want) {
			t.Fatalf("events %v want %v", spy.Events, want)
		}
		if spy.Events[2] != "FireSecondary" {
			t.Fatalf("got %s want FireSecondary", spy.Events[2])
		}
	})
	t.Run("ordinaryTertiary", func(t *testing.T) {
		var svc Service
		w := weaponForFire(3, 10, 0, 0, 0, false, false, false, false, "s.wav", 0, 0)
		slot := &Slot{Weapon: w}
		spy := &FireSpy{}
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 2, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, spy, nil)
		if !ok {
			t.Fatalf("tertiary failed")
		}
		// No startSmoke because flag false, but still FireTertiary + RockUnit
		want := []string{"alloc", "startSound", "FireTertiary", "RockUnit"}
		if len(spy.Events) != len(want) {
			t.Fatalf("events %v want %v", spy.Events, want)
		}
	})
	t.Run("dropped", func(t *testing.T) {
		var svc Service
		w := weaponForFire(4, 10, 0, 0, 0, false, true, false, true, "s.wav", 0, 0)
		slot := &Slot{Weapon: w}
		spy := &FireSpy{}
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, spy, nil)
		if !ok {
			t.Fatalf("dropped fire failed")
		}
		// Dropped emits neither Fire nor RockUnit [06 §4.1] C2, and start smoke suppressed [06 §13.2]
		// Expect only alloc + startSound
		want := []string{"alloc", "startSound"}
		if len(spy.Events) != len(want) {
			t.Fatalf("dropped events %v want %v", spy.Events, want)
		}
		for i, ev := range want {
			if spy.Events[i] != ev {
				t.Fatalf("dropped event %d %s", i, spy.Events[i])
			}
		}
	})
	t.Run("meteor", func(t *testing.T) {
		var svc Service
		w := weaponForFire(5, 10, 0, 0, 0, false, false, true, true, "s.wav", 0, 0)
		slot := &Slot{Weapon: w}
		spy := &FireSpy{}
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, spy, nil)
		if !ok {
			t.Fatalf("meteor fire failed")
		}
		// Meteor runs only common initializer [06 §4.1] C2
		want := []string{"alloc", "startSound"}
		if len(spy.Events) != len(want) {
			t.Fatalf("meteor events %v want %v", spy.Events, want)
		}
	})
	t.Run("burstRootHasNoBurstCloneCallbacks", func(t *testing.T) {
		var svc Service
		w := weaponForFire(6, 10, 0, 3, 5, false, false, false, true, "s.wav", 0, 0)
		slot := &Slot{Weapon: w}
		spy := &FireSpy{}
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, spy, nil)
		if !ok {
			t.Fatalf("burst root fire failed")
		}
		// Root still follows ordinary chain; clones will not.
		// Check root has burst state.
		if svc.Records[0].BurstRemaining != 3 {
			t.Fatalf("burst remaining %d want 3", svc.Records[0].BurstRemaining)
		}
	})
}

func TestFirePoolFullSuppressesCallbacksButRetainsDraws(t *testing.T) {
	// C4 callbacks not called when pool full [06 §4.1]; C5 up to two RNG draws retained when spread nonzero [06 §4.4] I4.
	var svc Service
	// Fill to capacity.
	for i := 0; i < ProjectileCapacity; i++ {
		if _, ok := svc.Reserve(); !ok {
			t.Fatalf("fill %d failed", i)
		}
	}
	if svc.Count() != ProjectileCapacity {
		t.Fatalf("count %d", svc.Count())
	}
	w := weaponForFire(10, 10, 45, 0, 0, false, false, false, true, "s.wav", 0, 0) // spray nonzero
	slot := &Slot{Weapon: w, Reload: 0, Target: Target{Kind: TargetPoint}}
	spy := &FireSpy{}
	muzzleCalls := 0
	muzzle := func(int) int32 { muzzleCalls++; return 2 }
	r := rng.NewSimulation(42)
	before := r.Draws()
	// Need to supply slot muzzle piece tracking; but we test retains.
	h, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint, X: numeric.FixedFromInt(5)}, 0, 100, 100, 0, &r, muzzle, spy, nil)
	if ok || h != 0 {
		t.Fatalf("pool-full fire should fail")
	}
	if len(spy.Events) != 0 {
		t.Fatalf("pool-full should suppress callbacks, got %v", spy.Events)
	}
	if muzzleCalls != 1 {
		t.Fatalf("muzzle query must be retained on pool-full [06 §4.4] C5, calls %d", muzzleCalls)
	}
	after := r.Draws()
	if after-before != 2 {
		t.Fatalf("pool-full with nonzero spread must retain 2 RNG draws [06 §4.4] I4, got %d draws", after-before)
	}
	// Zero spread should retain 0 draws.
	var svc2 Service
	for i := 0; i < ProjectileCapacity; i++ {
		svc2.Reserve()
	}
	w2 := weaponForFire(11, 10, 0, 0, 0, false, false, false, false, "", 0, 0)
	slot2 := &Slot{Weapon: w2}
	r2 := rng.NewSimulation(99)
	before2 := r2.Draws()
	_, ok2 := TryFire(&svc2, slot2, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r2, nil, &FireSpy{}, nil)
	if ok2 {
		t.Fatalf("should fail pool full zero spray")
	}
	if r2.Draws()-before2 != 0 {
		t.Fatalf("zero spray should consume 0 draws, got %d", r2.Draws()-before2)
	}
}

func TestFireMuzzleQuerySynchronousBeforeInit(t *testing.T) {
	// C3 muzzle piece queried synchronously before initialization [06 §4.1] C3.
	var svc Service
	w := weaponForFire(12, 10, 0, 0, 0, false, false, false, false, "s.wav", 0, 0)
	slot := &Slot{Weapon: w}
	called := false
	var calledSlot int
	muzzle := func(idx int) int32 {
		called = true
		calledSlot = idx
		return 7
	}
	r := rng.NewSimulation(1)
	TryFire(&svc, slot, 1, Target{Kind: TargetPoint}, 5, 100, 100, 0, &r, muzzle, nil, nil)
	if !called || calledSlot != 1 {
		t.Fatalf("muzzle query not called synchronously [06 §4.1] C3")
	}
	if slot.MuzzlePiece != 7 {
		t.Fatalf("slot muzzle piece %d want 7 [06 §4.1] C3", slot.MuzzlePiece)
	}
	if svc.Records[0].MuzzlePiece != 7 {
		t.Fatalf("projectile muzzle piece %d want 7 [06 §4.1] C3", svc.Records[0].MuzzlePiece)
	}
	// Negative result fallback path [06 §4.1] C3.
	var svc2 Service
	slot2 := &Slot{Weapon: w}
	muzzleNeg := func(int) int32 { return -1 }
	TryFire(&svc2, slot2, 0, Target{Kind: TargetPoint}, 5, 100, 100, 0, &r, muzzleNeg, nil, nil)
	if slot2.MuzzlePiece != -1 {
		t.Fatalf("negative muzzle should be stored as -1 fallback")
	}
	if svc2.Records[0].MuzzlePiece != -1 {
		t.Fatalf("negative muzzle piece fallback")
	}
}

func TestFireDebitOnlyOnSuccess(t *testing.T) {
	// C6 debit only after successful spawner return; both costs via immediate-debit both-or-neither [06 §4.2] C6.
	t.Run("successDebits", func(t *testing.T) {
		var svc Service
		w := weaponForFire(20, 10, 0, 0, 0, false, false, false, false, "", 5, 7)
		slot := &Slot{Weapon: w}
		player := &economy.Player{}
		player.Stock[economy.Energy] = 100
		player.Stock[economy.Metal] = 100
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, nil, player)
		if !ok {
			t.Fatalf("fire should succeed")
		}
		if player.Stock[economy.Energy] != 95 || player.Stock[economy.Metal] != 93 {
			t.Fatalf("debit both costs [06 §4.2] C6, energy %v metal %v", player.Stock[economy.Energy], player.Stock[economy.Metal])
		}
		if slot.Reload == 0 {
			t.Fatalf("reload should be stored on success [06 §4.2] C7")
		}
	})
	t.Run("poolFullNoDebitNoReload", func(t *testing.T) {
		var svc Service
		for i := 0; i < ProjectileCapacity; i++ {
			svc.Reserve()
		}
		w := weaponForFire(21, 30, 0, 0, 0, false, false, false, false, "", 5, 7)
		slot := &Slot{Weapon: w, Reload: 0}
		player := &economy.Player{}
		player.Stock[economy.Energy] = 100
		player.Stock[economy.Metal] = 100
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, nil, player)
		if ok {
			t.Fatalf("pool full should fail")
		}
		if player.Stock[economy.Energy] != 100 || player.Stock[economy.Metal] != 93-93 { // unchanged
			// check unchanged
		}
		if player.Stock[economy.Energy] != 100 || player.Stock[economy.Metal] != 100 {
			t.Fatalf("pool-full must not debit [06 §4.2] C6, got energy %v metal %v", player.Stock[economy.Energy], player.Stock[economy.Metal])
		}
		if slot.Reload != 0 {
			t.Fatalf("pool-full must not store reload [06 §4.2] C6, got %d", slot.Reload)
		}
		if slot.Ammo != 0 {
			t.Fatalf("no ammo change")
		}
	})
	t.Run("bothOrNeither", func(t *testing.T) {
		var svc Service
		w := weaponForFire(22, 10, 0, 0, 0, false, false, false, false, "", 10, 10)
		slot := &Slot{Weapon: w}
		player := &economy.Player{}
		player.Stock[economy.Energy] = 5 // insufficient energy
		player.Stock[economy.Metal] = 100
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, nil, player)
		if ok {
			t.Fatalf("precheck should fail when energy insufficient")
		}
		if svc.Count() != 0 {
			t.Fatalf("precheck fail should not allocate")
		}
		if player.Stock[economy.Metal] != 100 {
			t.Fatalf("both-or-neither: metal should not be debited on precheck fail")
		}
	})
	t.Run("stockpileNoDebitDecrementsAmmo", func(t *testing.T) {
		var svc Service
		w := weaponForFire(23, 30, 0, 0, 0, true, false, false, false, "", 100, 100) // stockpile high costs but should not debit
		slot := &Slot{Weapon: w, Ammo: 5}
		player := &economy.Player{}
		player.Stock[economy.Energy] = 10
		player.Stock[economy.Metal] = 10
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, nil, player)
		if !ok {
			t.Fatalf("stockpile fire should succeed even with low stock")
		}
		if slot.Ammo != 4 {
			t.Fatalf("stockpile should decrement ammo [06 §4.2] [06 §11.1], got %d", slot.Ammo)
		}
		if player.Stock[economy.Energy] != 10 || player.Stock[economy.Metal] != 10 {
			t.Fatalf("stockpile must perform no per-launch debit [06 §4.2] C6")
		}
		if slot.Reload != 0 {
			t.Fatalf("stockpile must not write reload [06 §4.2] C7, got %d", slot.Reload)
		}
	})
}

func TestFireReloadTruncationOrder(t *testing.T) {
	// C7 reload integer-truncated in documented order [06 §4.2].
	tests := []struct {
		health, maxHealth int32
		kills             int32
		authored          int32
		want              int32
	}{
		{100, 100, 0, 30, 30},
		{100, 100, 7, 30, 28},
		{50, 100, 12, 30, 28},
		{3, 7, 0, 30, 33},
		{0, 100, 0, 30, 36},
	}
	for i, tc := range tests {
		var svc Service
		w := weaponForFire(30+int32(i), tc.authored, 0, 0, 0, false, false, false, false, "", 0, 0)
		slot := &Slot{Weapon: w}
		r := rng.NewSimulation(1)
		_, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, tc.health, tc.maxHealth, tc.kills, &r, nil, nil, nil)
		if !ok {
			t.Fatalf("case %d fire failed", i)
		}
		if slot.Reload != tc.want {
			t.Fatalf("case %d reload %d want %d (health %d/%d kills %d authored %d) [06 §4.2] C7", i, slot.Reload, tc.want, tc.health, tc.maxHealth, tc.kills, tc.authored)
		}
		// Also verify ComputeStoredReload matches
		got2 := ComputeStoredReload(tc.health, tc.maxHealth, tc.kills, tc.authored)
		if got2 != tc.want {
			t.Fatalf("ComputeStoredReload case %d %d want %d", i, got2, tc.want)
		}
	}
}

func TestBurstAnchor(t *testing.T) {
	// C8 burst spawns N pellets plus one silent anchor [06 §4.3].
	var svc Service
	w := weaponForFire(40, 10, 0, 3, 1, false, false, false, false, "", 0, 0)
	weapons := map[int32]*content.WeaponDef{w.ID: w}
	slot := &Slot{Weapon: w}
	r := rng.NewSimulation(1)
	// Fire root anchor at tick 0.
	h, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, nil, nil)
	if !ok || h == 0 {
		t.Fatalf("burst root fire failed")
	}
	if svc.Count() != 1 {
		t.Fatalf("after root fire count %d want 1 anchor", svc.Count())
	}
	anch := &svc.Records[0]
	if anch.BurstRemaining != 3 {
		t.Fatalf("anchor remaining %d want 3 [06 §4.3] C8", anch.BurstRemaining)
	}
	if anch.BurstDeadline != 1 {
		t.Fatalf("anchor deadline %d want 1 (0+1) [06 §4.3]", anch.BurstDeadline)
	}
	// Tick bursts: interval 1 → one clone per tick.
	// Tick 0 not due (deadline 1)
	if n := svc.AdvanceBursts(0, &r, weapons, nil); n != 0 {
		t.Fatalf("tick0 clones %d want 0", n)
	}
	if svc.Count() != 1 {
		t.Fatalf("tick0 count %d want 1", svc.Count())
	}
	// Tick 1 due → first pellet
	if n := svc.AdvanceBursts(1, &r, weapons, nil); n != 1 {
		t.Fatalf("tick1 clones %d want 1", n)
	}
	if svc.Count() != 2 {
		t.Fatalf("tick1 count %d want 2", svc.Count())
	}
	if svc.Records[0].BurstRemaining != 2 {
		t.Fatalf("after tick1 remaining %d want 2", svc.Records[0].BurstRemaining)
	}
	// Pellet should have cleared burst state
	if svc.Records[1].BurstRemaining != 0 {
		t.Fatalf("pellet clone must have remaining 0 [06 §4.3], got %d", svc.Records[1].BurstRemaining)
	}
	// Tick 2 → second pellet
	if n := svc.AdvanceBursts(2, &r, weapons, nil); n != 1 {
		t.Fatalf("tick2 clones %d want 1", n)
	}
	// Tick 3 → third pellet, anchor should die silently
	if n := svc.AdvanceBursts(3, &r, weapons, nil); n != 1 {
		t.Fatalf("tick3 clones %d want 1", n)
	}
	if svc.Count() != 4 {
		t.Fatalf("after tick3 count %d want 4 (N pellets + anchor)", svc.Count())
	}
	// Anchor should be dead but still in pool until compact [06 §5.1]
	if !svc.IsDead(h) {
		t.Fatalf("anchor should be dead after N pellets [06 §4.3] C8")
	}
	// Anchor die is silent: no explosion etc — we just verify dead flag, not damage.
	// After compaction, only pellets survive.
	svc.Compact(nil)
	if svc.Count() != 3 {
		t.Fatalf("after compact count %d want 3 pellets", svc.Count())
	}
	for i := 0; i < svc.Count(); i++ {
		if svc.Records[i].BurstRemaining != 0 {
			t.Fatalf("survivor %d has burst remaining %d", i, svc.Records[i].BurstRemaining)
		}
	}
}

func TestBurstPoolFullConsumesAttemptNoRNGNoClone(t *testing.T) {
	// Pool-full burst clone consumes attempt with no spray and no RNG draw [06 §4.3] C8.
	var svc Service
	w := weaponForFire(50, 10, 45, 1, 1, false, false, false, false, "", 0, 0) // spray nonzero, burst 1
	weapons := map[int32]*content.WeaponDef{w.ID: w}
	slot := &Slot{Weapon: w}
	r := rng.NewSimulation(77)
	h, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, nil, nil)
	if !ok {
		t.Fatalf("root fire failed")
	}
	// Fill pool to capacity after anchor, leaving no room for clone.
	for svc.Count() < ProjectileCapacity {
		svc.Reserve()
	}
	beforeDraws := r.Draws()
	// Anchor deadline 1, tick 1 due but pool full.
	n := svc.AdvanceBursts(1, &r, weapons, nil)
	if n != 0 {
		t.Fatalf("pool-full burst should create 0 clones, got %d", n)
	}
	afterDraws := r.Draws()
	if afterDraws != beforeDraws {
		t.Fatalf("pool-full burst must consume no spray/RNG draw [06 §4.3] C8, before %d after %d", beforeDraws, afterDraws)
	}
	// Attempt consumed: remaining should be 0 and anchor dead.
	idx := int(h) - 1
	if svc.Records[idx].BurstRemaining != 0 {
		t.Fatalf("burst remaining %d want 0 after consumed attempt", svc.Records[idx].BurstRemaining)
	}
	if !svc.IsDead(h) {
		t.Fatalf("anchor should be dead after consuming last attempt even on pool-full [06 §4.3]")
	}
}

func TestBurstMuzzleRequeryAndSprayOrder(t *testing.T) {
	// Muzzle re-query when interval>4 or remaining odd [06 §4.3] C8; clone before spray [06 §4.3].
	var svc Service
	w := weaponForFire(60, 10, 30, 2, 5, false, false, false, false, "", 0, 0) // spray nonzero, interval 5 >4
	weapons := map[int32]*content.WeaponDef{w.ID: w}
	slot := &Slot{Weapon: w}
	r := rng.NewSimulation(123)
	_, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, func(int) int32 { return 1 }, nil, nil)
	if !ok {
		t.Fatalf("fire")
	}
	// Set parent velocity to a known value. Speed is the authoritative scalar
	// the spray recompute reads back [06 §6.7], so give it one consistent with
	// the velocity rather than leaving it zero.
	svc.Records[0].Velocity = Vec3{X: numeric.FixedFromInt(10)}
	svc.Records[0].Speed = numeric.FixedFromInt(10)
	svc.Records[0].Yaw = 0
	svc.Records[0].Pitch = 0
	// Muzzle pos spy: returns distinct pos per piece.
	muzzlePosCalls := 0
	muzzlePos := func(piece int16) Vec3 {
		muzzlePosCalls++
		return Vec3{X: numeric.FixedFromInt(int64(100 + muzzlePosCalls)), Y: numeric.FixedFromInt(0), Z: numeric.FixedFromInt(200)}
	}
	// Interval 5 >4 so should re-query even though remaining 2 is even.
	beforeDraws := r.Draws()
	n := svc.AdvanceBursts(5, &r, weapons, muzzlePos)
	if n != 1 {
		t.Fatalf("burst clone %d", n)
	}
	if muzzlePosCalls != 1 {
		t.Fatalf("muzzle re-query should happen when interval>4 [06 §4.3], calls %d", muzzlePosCalls)
	}
	// The clone is copied BEFORE spray, so it keeps the pre-spray velocity;
	// spray then mutates the parent, preparing the NEXT clone [06 §4.3] C8.
	if cloneVel := svc.Records[1].Velocity.X.Int(); cloneVel != 10 {
		t.Fatalf("clone copied after spray [06 §4.3]: clone vel %d want 10", cloneVel)
	}
	// The parent's stored angles moved and its velocity was recomputed from
	// them, rather than being nudged componentwise [06 §4.3].
	if svc.Records[0].Yaw == 0 && svc.Records[0].Pitch == 0 {
		t.Fatalf("spray did not perturb the parent's stored yaw/pitch")
	}
	wantVel := VelocityFromAngles(svc.Records[0].Yaw, svc.Records[0].Pitch, svc.Records[0].Speed)
	if svc.Records[0].Velocity != wantVel {
		t.Fatalf("parent velocity %v is not the recompute of its own angles %v",
			svc.Records[0].Velocity, wantVel)
	}
	// RNG draws: successful spray consumes 2 draws
	if r.Draws()-beforeDraws != 2 {
		t.Fatalf("successful spray should consume 2 draws, got %d", r.Draws()-beforeDraws)
	}

	// Test interval <=4 and even remaining → no re-query
	var svc2 Service
	w2 := weaponForFire(61, 10, 0, 2, 2, false, false, false, false, "", 0, 0) // interval 2, remaining 2 even
	weapons2 := map[int32]*content.WeaponDef{w2.ID: w2}
	slot2 := &Slot{Weapon: w2}
	r2 := rng.NewSimulation(1)
	TryFire(&svc2, slot2, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r2, nil, nil, nil)
	svc2.Records[0].Velocity = Vec3{X: numeric.FixedFromInt(5)}
	muzzleCalls2 := 0
	muzzlePos2 := func(int16) Vec3 { muzzleCalls2++; return Vec3{X: numeric.FixedFromInt(999)} }
	// First due tick 2: remaining 2 even and interval 2 <=4 → no re-query [06 §4.3]
	svc2.AdvanceBursts(2, &r2, weapons2, muzzlePos2)
	if muzzleCalls2 != 0 {
		t.Fatalf("even remaining with interval<=4 should not re-query, got %d", muzzleCalls2)
	}
	// Next tick 4: remaining 1 odd → should re-query
	muzzleCalls2 = 0
	svc2.AdvanceBursts(4, &r2, weapons2, muzzlePos2)
	if muzzleCalls2 != 1 {
		t.Fatalf("odd remaining should re-query [06 §4.3], got %d", muzzleCalls2)
	}
}

func TestFireZeroBurstFollowsOrdinaryPath(t *testing.T) {
	// Root with burst 0 follows ordinary moving-projectile path instead of burst branch [06 §4.3] C8.
	var svc Service
	w := weaponForFire(70, 10, 0, 0, 0, false, false, false, false, "", 0, 0)
	slot := &Slot{Weapon: w}
	r := rng.NewSimulation(1)
	h, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, &r, nil, nil, nil)
	if !ok {
		t.Fatalf("fire")
	}
	p := svc.Records[int(h)-1]
	if p.BurstRemaining != 0 {
		t.Fatalf("zero burst should have remaining 0")
	}
	// AdvanceBursts should do nothing for zero burst.
	weapons := map[int32]*content.WeaponDef{w.ID: w}
	if n := svc.AdvanceBursts(10, &r, weapons, nil); n != 0 {
		t.Fatalf("zero burst anchor should not spawn, got %d", n)
	}
	// Ensure not dead
	if svc.IsDead(h) {
		t.Fatalf("zero burst projectile should not be dead anchor")
	}
}

func TestProjectilePoolHandle(t *testing.T) {
	// Quick sanity: pool handle 0 null.
	var svc Service
	if _, ok := TryFire(&svc, nil, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, nil, nil, nil, nil); ok {
		t.Fatalf("nil slot should not fire")
	}
	if _, ok := TryFire(&svc, &Slot{}, 0, Target{Kind: TargetPoint}, 0, 100, 100, 0, nil, nil, nil, nil); ok {
		t.Fatalf("nil weapon should not fire")
	}
	// TargetNone should not fire even with valid weapon
	w := weaponForFire(71, 10, 0, 0, 0, false, false, false, false, "", 0, 0)
	if _, ok := TryFire(&svc, &Slot{Weapon: w}, 0, Target{Kind: TargetNone}, 0, 100, 100, 0, nil, nil, nil, nil); ok {
		t.Fatalf("TargetNone should not fire")
	}
	_ = pool.Handle(0)
}
