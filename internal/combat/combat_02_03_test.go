package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestCompiledCategoryMasksUseUnitMembership(t *testing.T) {
	var arm, vtol content.CategoryMask
	arm.Words[0] = 1 << 3
	vtol.Words[0] = 1 << 7
	if !arm.Contains(3) || arm.Contains(7) {
		t.Fatalf("compiled ARM membership = %#v", arm.Words)
	}
	if !IsPreferredCategoryMask(arm, vtol) {
		t.Fatal("different unit-ID membership sets should be preferred")
	}
	if IsPreferredCategoryMask(arm, arm) {
		t.Fatal("intersecting membership set must be fallback")
	}
}

func TestImpactEventOrderAndKilledDedup(t *testing.T) {
	var svc Service
	bindFixtureControlBytes(&svc) // [06 R-DMG-01 §8] gate 1 needs a player record
	var got []EventKind
	svc.Events = func(ev Event) { got = append(got, ev.Kind) }
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	target.Health = 10
	target.MaxHealth = 10
	weapon := &content.WeaponDef{
		ID:             1,
		LineOfSight:    true,
		WeaponVelocity: int32(numeric.FixedFromInt(1)),
		Range:          100,
		ShakeMagnitude: 4,
		ShakeDuration:  2,
		SoundHit:       "hit",
		ExplosionGaf:   "boom",
		ExplosionArt:   "boomart",
		DamageDefault:  100,
	}
	p := &Projectile{Shooter: shooter.Handle, TargetUnit: target.Handle, Pos: Vec3{X: target.X, Y: target.Y, Z: target.Z}}
	handleProjectileImpact(&svc, 1, p, weapon, w, terrain, nil, nil, nil, 4, Vec3{}, nil, p.TargetUnit)
	// The damage flash sits between the impact event and the lethal
	// notification: [06 §9.1] step 4 writes it for every accepted non-heal
	// packet, before the reaction step [06 R-WPN-04 §2].
	wantPrefix := []EventKind{EventShake, EventHitSound, EventExplosion, EventProjectileImpact, EventDamageFlash, EventUnitKilled}
	if len(got) != len(wantPrefix) {
		t.Fatalf("events = %#v, want %#v", got, wantPrefix)
	}
	for i := range wantPrefix {
		if got[i] != wantPrefix[i] {
			t.Fatalf("event %d = %v, want %v (presentation, impact, then lethal notification)", i, got[i], wantPrefix[i])
		}
	}
	if !target.Dying {
		t.Fatal("lethal direct impact did not mark target dying")
	}
	// Death notification is deduplicated for the same live unit, but cleanup
	// deliberately permits the retail slot alias to be reused by a new unit.
	targetHandle := target.Handle
	w.TeardownCleanup()
	newHandle, err := w.Create(target.Def, target.Owner, target.X, target.Y, target.Z)
	if err != nil || newHandle != targetHandle {
		t.Fatalf("reused target handle = %d, err=%v, want %d", newHandle, err, targetHandle)
	}
	newTarget := w.Unit(newHandle)
	newTarget.Health, newTarget.MaxHealth = 10, 10
	p.TargetUnit = newHandle
	handleProjectileImpact(&svc, 2, p, weapon, w, terrain, nil, nil, nil, 4, Vec3{}, nil, p.TargetUnit)
	kills := 0
	for _, kind := range got {
		if kind == EventUnitKilled {
			kills++
		}
	}
	if kills != 2 {
		t.Fatalf("killed events = %d, want one per unit identity across handle reuse: %#v", kills, got)
	}
}

func TestMuzzleWorldPosComposesMappedRotatedHierarchy(t *testing.T) {
	one := numeric.FixedFromInt(1)
	mdl := &model.Model{Root: 0, Pieces: []model.Piece{
		{Name: "modelRoot", Parent: -1, Translate: [3]numeric.Fixed{numeric.FixedFromInt(10), 0, numeric.FixedFromInt(20)}},
		{Name: "modelMuzzle", Parent: 0, Translate: [3]numeric.Fixed{numeric.FixedFromInt(3), numeric.FixedFromInt(4), numeric.FixedFromInt(5)}},
	}}
	vm := cob.NewVM(&cob.Program{Pieces: []string{"cobMuzzle", "cobRoot"}})
	vm.Pieces[0] = model.PieceState{RotZ: 16384, Trans: [3]numeric.Fixed{one, numeric.FixedFromInt(2), numeric.FixedFromInt(3)}}
	vm.Pieces[1] = model.PieceState{RotY: 8192, Trans: [3]numeric.Fixed{numeric.FixedFromInt(4), numeric.FixedFromInt(5), numeric.FixedFromInt(6)}}
	binding := &cob.Binding{VM: vm, Model: mdl, PieceMap: []int{1, 0}}
	u := &units.Unit{X: numeric.FixedFromInt(100), Y: numeric.FixedFromInt(7), Z: numeric.FixedFromInt(-20), Script: vm, ScriptState: &units.ScriptState{VM: vm, Binding: binding}}

	got, ok := muzzleWorldPosResolved(u, 0)
	if !ok {
		t.Fatal("strict binding did not resolve muzzle piece")
	}
	states := make([]model.PieceState, len(mdl.Pieces))
	states[1] = vm.Pieces[0]
	states[0] = vm.Pieces[1]
	wantLocal := model.Compose(mdl, states, 1).Origin
	// Z is subtracted because Compose's own product is MODEL space, mirrored
	// in Z against world space [03 R-RAST-01 §2]; equivalently this is
	// `unit + Transform.WorldOffset()`, the locator's world triple added with
	// no further sign [03 R-RAST-01 §8]. What this test locks is that the
	// muzzle comes from the composed hierarchy rather than the raw COB
	// translation, which the naive comparison below is the point of; the sign
	// is asserted against the established convention, not restated from the
	// implementation. TestMuzzleWorldPointIsUnitPlusLocatorOffset owns the
	// sign itself, in authored coordinates.
	want := Vec3{X: u.X.Add(wantLocal[0]), Y: u.Y.Add(wantLocal[1]), Z: u.Z.Sub(wantLocal[2])}
	if got != want {
		t.Fatalf("composed muzzle = %#v, want %#v", got, want)
	}
	naive := Vec3{X: u.X.Add(vm.Pieces[0].Trans[0]), Y: u.Y.Add(vm.Pieces[0].Trans[1]), Z: u.Z.Sub(vm.Pieces[0].Trans[2])}
	if got == naive {
		t.Fatalf("muzzle unexpectedly used raw COB translation: %#v", got)
	}
}
