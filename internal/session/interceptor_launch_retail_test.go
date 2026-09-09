package session

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The anti-nuke chain, end to end on retail content: an ARM Protector holding
// one stockpiled interceptor must engage a CORE-owned nuclear missile aimed at
// the ground it stands on [06 §11.2] C29.
//
// The two authored halves of the chain, confirmed against the corpus rather
// than assumed (I14): ARMAMD carries AMD_ROCKET, which is `interceptor` +
// `stockpile` + `vlaunch` with `coverage = 2000`; ARMSILO carries
// NUCLEAR_MISSILE, which is `targetable` + `stockpile` + `vlaunch`. The four
// interceptor-flagged weapons in the corpus are AMD_ROCKET, ARMSCAB_WEAPON,
// CORMABM_WEAPON and FMD_ROCKET, and all four are `vlaunch`, which is why the
// vertical-launch executor is the one that owns the fire-time rescan
// [06 §4.4][06 §6.6].
const (
	retailAntiNuke     = "ARMAMD"
	retailNukeSilo     = "ARMSILO"
	retailInterceptorW = "AMD_ROCKET"
	retailNukeW        = "NUCLEAR_MISSILE"
)

// placeCompleteRetailUnit puts a finished unit of the named definition at a
// world point and registers it the way battle-entry placement does.
func placeCompleteRetailUnit(t *testing.T, s *Session, key string, owner uint8, x, z numeric.Fixed) *units.Unit {
	t.Helper()
	def, ok := s.Catalog.Unit(key)
	if !ok || def == nil {
		t.Skipf("retail fixture unit %q is absent", key)
	}
	y := s.World.HeightAt(x, z)
	h, err := s.Units.Create(def, owner, x, y, z)
	if err != nil {
		t.Fatalf("place %s: %v", key, err)
	}
	u := s.Units.Unit(h)
	if u == nil {
		t.Fatalf("created %s not resolvable", key)
	}
	s.CompleteUnit(h)
	return u
}

// armStockpileSlot puts one round in the unit's stockpile slot and returns it,
// skipping when the authored definition does not carry the expected weapon.
// The slot byte is what the stockpile fire gate reads in place of the per-shot
// cost test [06 §11.1][06 R-WPN-05 §2]; the BUILDWEAPON queue that normally
// fills it is not what this probe is about.
func armStockpileSlot(t *testing.T, u *units.Unit, weaponName string) *units.Slot {
	t.Helper()
	slot := u.SlotAt(0)
	if slot == nil || slot.Weapon == nil {
		t.Fatalf("%s slot 0 is unpopulated", u.Def.UnitName)
	}
	if !strings.EqualFold(slot.Weapon.Name, weaponName) && !strings.EqualFold(weaponKey(slot), weaponName) {
		t.Skipf("%s slot 0 carries %q, want %q", u.Def.UnitName, weaponKey(slot), weaponName)
	}
	slot.Ammo = 1
	return slot
}

func weaponKey(slot *units.Slot) string {
	if slot == nil || slot.Weapon == nil {
		return ""
	}
	return slot.Weapon.CanonicalKey
}

// projectilesOfWeapon reports how many live pool records carry the named
// weapon, and returns the first such handle.
func projectilesOfWeapon(s *Session, name string) (int, pool.Handle, *combat.Projectile) {
	if s.Combat == nil {
		return 0, 0, nil
	}
	count := 0
	var first pool.Handle
	var rec *combat.Projectile
	for i := 0; i < s.Combat.Count(); i++ {
		p := &s.Combat.Records[i]
		w, ok := s.Catalog.WeaponByID(p.WeaponID)
		if !ok || w == nil || !strings.EqualFold(w.CanonicalKey, name) && !strings.EqualFold(w.Name, name) {
			continue
		}
		count++
		if first == 0 {
			first = pool.Handle(i + 1)
			rec = p
		}
	}
	return count, first, rec
}

// TestRetailAntiNukeIntercept is the probe the T25 marker at the phase-3 site
// asked for. Before WU-19-234 nothing launched an interceptor: the automatic
// interceptor scan never ran, so the Protector's slot never held a target, and
// combat.FirePorts.InterceptorRescan was never bound, so nothing could have
// supplied the matched-projectile link even if it had fired.
func TestRetailAntiNukeIntercept(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	stepRetail(s, 2)
	if s.State != StateBattle {
		t.Fatalf("session state %v, want battle", s.State)
	}

	// The Protector belongs to player 0; the silo to the hostile player 1. The
	// two are placed a long way apart so the nuke is genuinely in flight when
	// the scan reaches it, and the silo's aim point is the ground the Protector
	// stands on — the scan metric is the candidate's STORED aim point, not its
	// current position [06 §11.2].
	protectorX := numeric.Fixed(int64(400) << 16)
	protectorZ := numeric.Fixed(int64(400) << 16)
	siloX := numeric.Fixed(int64(1600) << 16)
	siloZ := numeric.Fixed(int64(1600) << 16)

	protector := placeCompleteRetailUnit(t, s, retailAntiNuke, 0, protectorX, protectorZ)
	silo := placeCompleteRetailUnit(t, s, retailNukeSilo, 1, siloX, siloZ)

	interceptorSlot := armStockpileSlot(t, protector, retailInterceptorW)
	nukeSlot := armStockpileSlot(t, silo, retailNukeW)
	if interceptorSlot.Weapon == nil || !interceptorSlot.Weapon.Interceptor {
		t.Skipf("authored %s slot 0 is not an interceptor weapon", retailAntiNuke)
	}
	if nukeSlot.Weapon == nil || !nukeSlot.Weapon.Targetable {
		t.Skipf("authored %s slot 0 is not a targetable weapon", retailNukeSilo)
	}
	coverage := interceptorSlot.Weapon.Coverage
	t.Logf("interceptor coverage = %d world units", coverage)

	// Install the manual order's target and take the slot out of autonomy,
	// as the ordinary attack handler does [04 R-ORD-01 §7][06 §3.2].
	// A bare point store leaves a computer-owned slot available to the scan.
	nukeSlot.Target = units.Target{Kind: units.TargetGround, X: protectorX, Z: protectorZ}
	nukeSlot.Flags &^= units.SlotFlagAutonomous
	nukeSlot.Reload = 0

	// Step until the nuke is airborne.
	var nukeHandle pool.Handle
	for tick := 3; tick < 400 && nukeHandle == 0; tick++ {
		s.Step(int32(tick))
		if n, h, _ := projectilesOfWeapon(s, retailNukeW); n > 0 {
			nukeHandle = h
		}
	}
	if nukeHandle == 0 {
		t.Fatalf("the %s never launched its %s; the probe cannot test the interceptor without an incoming missile", retailNukeNameFor(silo), retailNukeW)
	}
	t.Logf("nuke launched as projectile %d", nukeHandle)

	// Now the interceptor half. Give the chain a generous window: the scan runs
	// from the autonomous scan's per-slot position, which is round-robin over
	// the player's units [06 §3.2].
	var interceptorHandle pool.Handle
	var interceptorRec *combat.Projectile
	for tick := 400; tick < 1200 && interceptorHandle == 0; tick++ {
		s.Step(int32(tick))
		if n, h, rec := projectilesOfWeapon(s, retailInterceptorW); n > 0 {
			interceptorHandle, interceptorRec = h, rec
		}
	}
	if interceptorHandle == 0 {
		t.Fatalf("no %s was ever launched against the incoming %s [06 §11.2] C29: "+
			"slot target kind %v, ammo %d", retailInterceptorW, retailNukeW,
			interceptorSlot.Target.Kind, interceptorSlot.Ammo)
	}
	t.Logf("interceptor launched as projectile %d, reservation link %d",
		interceptorHandle, interceptorRec.TargetProjectile)

	// The link is the authoritative reservation: the candidate's record is what
	// the spawn stores, and it is what later scans test when rejecting
	// candidates already claimed [06 §11.2].
	if interceptorRec.TargetProjectile == 0 {
		t.Fatalf("the interceptor carries no matched-projectile link; the vertical creator "+
			"retains the link the fire-time rescan supplied [06 §6.6][06 §11.2] C29 (want %d)", nukeHandle)
	}
	if interceptorRec.TargetProjectile != nukeHandle {
		t.Fatalf("the interceptor's reservation link is %d, want the incoming missile %d [06 §11.2]",
			interceptorRec.TargetProjectile, nukeHandle)
	}

	// The post-launch halves the phase-3 T25 marker guarded: guidance retargets
	// the live interceptor at its linked candidate every tick, and the
	// interceptor-flagged explosion sweep removes projectiles inside the blast
	// [06 §11.2]. Both were already implemented and simply unreachable, so the
	// probe follows the chain to its end rather than stopping at the launch.
	killed := false
	for tick := 1200; tick < 4200 && !killed; tick++ {
		s.Step(int32(tick))
		if n, _, _ := projectilesOfWeapon(s, retailNukeW); n == 0 {
			killed = true
		}
	}
	if !killed {
		t.Errorf("the %s was launched and linked but the incoming %s survived: "+
			"guidance or the interceptor-flagged explosion sweep did not close the chain [06 §11.2] C29",
			retailInterceptorW, retailNukeW)
	}
}

func retailNukeNameFor(u *units.Unit) string {
	if u == nil || u.Def == nil {
		return "silo"
	}
	return u.Def.UnitName
}
