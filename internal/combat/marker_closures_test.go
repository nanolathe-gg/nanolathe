package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The self-propelled acceleration block compares the scalar speed word
// against weaponvelocity UNSIGNED and is skipped once speed reaches it
// [06 §6.7] (closed 2026-09-02): a negative speed is never accelerated; a
// negative acceleration decrements until the sum would cross zero, then the
// overshoot clamp snaps it up to weaponvelocity.
func TestSelfPropAccelerationIsUnsigned(t *testing.T) {
	far := numeric.Fixed(0x7FFFFFFF)
	w := &content.WeaponDef{SelfProp: true, WeaponVelocity: 65536, WeaponAcceleration: 1000}
	p := Projectile{Speed: numeric.Fixed(-5000), ExpiryTick: 100}
	if res := AdvanceSelfProp(&p, w, 1, 0, far, GuidanceEnv{}); res != AdvanceAlive {
		t.Fatalf("advance: got %v", res)
	}
	if p.Speed.Raw() != -5000 {
		t.Fatalf("negative speed must never accelerate: got %d", p.Speed.Raw())
	}

	w2 := &content.WeaponDef{SelfProp: true, WeaponVelocity: 65536, WeaponAcceleration: -30000}
	q := Projectile{Speed: numeric.Fixed(40000), ExpiryTick: 100}
	AdvanceSelfProp(&q, w2, 1, 0, far, GuidanceEnv{})
	if q.Speed.Raw() != 10000 {
		t.Fatalf("negative acceleration decrements while non-negative: got %d want 10000", q.Speed.Raw())
	}
	AdvanceSelfProp(&q, w2, 2, 0, far, GuidanceEnv{})
	if q.Speed.Raw() != 65536 {
		t.Fatalf("crossing zero snaps up to weaponvelocity: got %d want 65536", q.Speed.Raw())
	}
	AdvanceSelfProp(&q, w2, 3, 0, far, GuidanceEnv{})
	if q.Speed.Raw() != 65536 {
		t.Fatalf("at weaponvelocity the block is skipped: got %d want 65536", q.Speed.Raw())
	}
}

// The water-damage height test is the signed high word of the 16.16 Y,
// inclusive against the zero-extended sea-level byte [04 §9.2].
func TestWaterDamageHeightIsHighWordInclusive(t *testing.T) {
	sea := uint8(10)
	if !IsInWaterForDamage(numeric.Fixed(10<<16|0xFFFF), sea) {
		t.Fatal("Y just under 11 has high word 10: in water")
	}
	if IsInWaterForDamage(numeric.Fixed(11<<16), sea) {
		t.Fatal("Y == 11 is above the byte: not in water")
	}
	if !IsInWaterForDamage(numeric.Fixed(-(1 << 15)), 0) {
		t.Fatal("Y == -0.5 floors to -1 <= 0: in water")
	}
}

// Meteor resolution falls back to weapon RECORD 0 — the ID-0 definition —
// never to the smallest ID present [06 §6.5] (refinement of 2026-09-02).
func TestResolveMeteorWeaponFallsBackToRecordZero(t *testing.T) {
	three := &content.WeaponDef{Name: "three", ID: 3}
	seven := &content.WeaponDef{Name: "seven", ID: 7, Meteor: true}
	weapons := map[string]*content.WeaponDef{"three": three, "seven": seven}
	if got := ResolveMeteorWeapon("seven", weapons); got != seven {
		t.Fatalf("meteor-flagged name resolves to itself, got %v", got)
	}
	if got := ResolveMeteorWeapon("three", weapons); got != nil {
		t.Fatalf("no ID-0 record: unresolved shower must not pick the smallest ID, got %v", got)
	}
	zero := &content.WeaponDef{Name: "noweapon", ID: 0}
	weapons["noweapon"] = zero
	if got := ResolveMeteorWeapon("missing", weapons); got != zero {
		t.Fatalf("miss resolves to record 0, got %v", got)
	}
	if got := ResolveMeteorWeapon("three", weapons); got != zero {
		t.Fatalf("non-meteor hit resolves to record 0, got %v", got)
	}
	if got := ResolveMeteorWeapon("  ", weapons); got != zero {
		t.Fatalf("empty selected name resolves to record 0, got %v", got)
	}
}

// The secondary-list gate is a boolean over the scanning player's OWN
// complete, activated istargetingupgrade units; allies do not count
// [06 §3.1] [04 R-SPEC-01 §8].
func TestTargetingUpgradeGate(t *testing.T) {
	up := &content.UnitDef{IsTargetingUpgrade: true}
	plain := &content.UnitDef{}
	mk := func(owner uint8, def *content.UnitDef, remaining float32, active bool) *units.Unit {
		return &units.Unit{Alive: true, Owner: owner, Def: def, Remaining: remaining, Activated: active}
	}
	list := []*units.Unit{
		nil,
		mk(0, plain, 0, true),
		mk(1, up, 0, true),   // an ally's upgrade: not own
		mk(0, up, 0.5, true), // still building
		mk(0, up, 0, false),  // deactivated
	}
	if TargetingUpgradeGate(list, 0) {
		t.Fatal("no own complete activated upgrade: gate must be clear")
	}
	if !TargetingUpgradeGate(list, 1) {
		t.Fatal("player 1 owns one: gate set")
	}
	list = append(list, mk(0, up, 0, true))
	if !TargetingUpgradeGate(list, 0) {
		t.Fatal("own complete activated upgrade: gate set")
	}
	dying := mk(0, up, 0, true)
	dying.Dying = true
	if TargetingUpgradeGate([]*units.Unit{dying}, 0) {
		t.Fatal("a dying upgrade does not count")
	}
}
