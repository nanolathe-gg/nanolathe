package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// scriptRecorder is the COB port. Unlike FireSpy it is what the runtime
// actually passes, so a shot that reaches it has really called the script.
type scriptRecorder struct {
	calls []string
}

func (s *scriptRecorder) FireWeapon(slotIdx int) {
	switch slotIdx {
	case 1:
		s.calls = append(s.calls, "FireSecondary")
	case 2:
		s.calls = append(s.calls, "FireTertiary")
	default:
		s.calls = append(s.calls, "FirePrimary")
	}
}

func (s *scriptRecorder) RockUnit(int) { s.calls = append(s.calls, "RockUnit") }

type eventRecorder struct {
	sounds []string
	smoke  []pool.Handle
}

func (e *eventRecorder) StartSound(name string)   { e.sounds = append(e.sounds, name) }
func (e *eventRecorder) StartSmoke(h pool.Handle) { e.smoke = append(e.smoke, h) }

// TestOrdinaryShotStartsAtTheMuzzleWithAUsableTrajectory is the shape the
// package-level fire tests could not catch: they proved the callback ORDER
// while the record itself was left at the world origin with zero velocity and
// a neutral owner. A projectile like that can never reach Gate 4.
func TestOrdinaryShotStartsAtTheMuzzleWithAUsableTrajectory(t *testing.T) {
	var svc Service
	w := &content.WeaponDef{
		ID:             7,
		LineOfSight:    true, // ordinary creation family [06 §6.2] C15
		WeaponVelocity: int32(numeric.FixedFromInt(4)),
		Range:          1200,
		SoundStart:     "sound/lasrfir.wav",
		StartSmoke:     true,
	}

	// A unit standing well away from the origin, with a muzzle piece offset
	// from its own centre.
	unitPos := Vec3{X: numeric.FixedFromInt(900), Y: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(700)}
	muzzlePos := Vec3{X: numeric.FixedFromInt(906), Y: numeric.FixedFromInt(52), Z: numeric.FixedFromInt(700)}
	targetPos := Vec3{X: numeric.FixedFromInt(1400), Y: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(700)}

	script := &scriptRecorder{}
	events := &eventRecorder{}
	r := rng.NewSimulation(1)
	ports := FirePorts{
		ShooterSide: 3,
		Origin:      unitPos,
		MuzzlePiece: func(int) int32 { return 11 },
		MuzzleWorld: func(piece int32) (Vec3, bool) {
			if piece != 11 {
				return Vec3{}, false
			}
			return muzzlePos, true
		},
		Script: script,
		Events: events,
		RNG:    &r,
	}

	slot := &Slot{Weapon: w}
	h, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint, X: targetPos.X, Y: targetPos.Y, Z: targetPos.Z}, 100, ports)
	if !ok || h == 0 {
		t.Fatalf("ordinary shot did not spawn")
	}
	p := &svc.Records[int(h)-1]

	if p.Pos != muzzlePos || p.StartPos != muzzlePos {
		t.Fatalf("shot started at %+v, want the resolved muzzle %+v", p.Pos, muzzlePos)
	}
	if p.ShooterSide != 3 {
		t.Fatalf("shooter side %d, want 3 — a neutral owner misattributes damage credit", p.ShooterSide)
	}
	if p.MuzzlePiece != 11 {
		t.Fatalf("muzzle piece %d, want 11 (burst clones re-query through it)", p.MuzzlePiece)
	}
	if p.WeaponID != w.ID {
		t.Fatalf("weapon id %d, want %d", p.WeaponID, w.ID)
	}
	if p.Velocity == (Vec3{}) {
		t.Fatalf("velocity is zero: the projectile would never move")
	}
	// The target is due +X of the muzzle, so the shot travels +X.
	if p.Velocity.X <= 0 {
		t.Fatalf("velocity.X %v, want positive toward the target", p.Velocity.X)
	}
	if p.ExpiryTick <= 100 {
		t.Fatalf("expiry %d is not in the future of tick 100", p.ExpiryTick)
	}

	// The ports were really called, in the fixed order [06 §4.1] C2.
	if len(events.sounds) != 1 || events.sounds[0] != "sound/lasrfir.wav" {
		t.Fatalf("start sound %v", events.sounds)
	}
	if len(script.calls) != 2 || script.calls[0] != "FirePrimary" || script.calls[1] != "RockUnit" {
		t.Fatalf("script calls %v, want FirePrimary then RockUnit", script.calls)
	}
	if len(events.smoke) != 1 || events.smoke[0] != h {
		t.Fatalf("start smoke %v, want the spawned handle %d", events.smoke, h)
	}

	// It advances toward the target rather than sitting still.
	before := p.Pos.X
	if res := Advance(p, w, 101, Vec3{}, 0, 0, GuidanceEnv{}); res == AdvanceRetire {
		t.Fatalf("shot retired on its first tick")
	}
	if p.Pos.X <= before {
		t.Fatalf("shot did not move: X %v then %v", before, p.Pos.X)
	}
}

// A unit target has no trajectory until its position is resolved, so a shot
// at one without the port fails rather than spawning an aimless record.
func TestUnitTargetNeedsAResolvedPosition(t *testing.T) {
	var svc Service
	w := &content.WeaponDef{ID: 8, LineOfSight: true, WeaponVelocity: int32(numeric.FixedFromInt(4)), Range: 900}
	slot := &Slot{Weapon: w}
	r := rng.NewSimulation(1)

	if _, ok := TryFire(&svc, slot, 0, Target{Kind: TargetUnit, Unit: 4}, 0, FirePorts{RNG: &r}); ok {
		t.Fatalf("unit target with no resolver should not spawn")
	}
	if svc.Count() != 0 {
		t.Fatalf("failed validation must not reserve a record")
	}

	target := Vec3{X: numeric.FixedFromInt(200), Z: numeric.FixedFromInt(300)}
	h, ok := TryFire(&svc, slot, 0, Target{Kind: TargetUnit, Unit: 4}, 0, FirePorts{
		RNG:         &r,
		TargetWorld: func(u pool.Handle) (Vec3, bool) { return target, u == 4 },
	})
	if !ok {
		t.Fatalf("unit target with a resolver should spawn")
	}
	p := &svc.Records[int(h)-1]
	if p.TargetUnit != 4 {
		t.Fatalf("target unit link %d, want 4", p.TargetUnit)
	}
	if p.TargetPos != target {
		t.Fatalf("target point %+v, want the resolved %+v", p.TargetPos, target)
	}
	if p.Velocity == (Vec3{}) {
		t.Fatalf("unit-target shot has no velocity")
	}
}

// A weapon matching none of the six creation predicates makes no projectile
// [06 §6.2] C15 — and must not consume a pool slot deciding that.
func TestWeaponWithNoCreationFamilyMakesNoProjectile(t *testing.T) {
	var svc Service
	w := &content.WeaponDef{ID: 9, ReloadTime: 10}
	slot := &Slot{Weapon: w}
	r := rng.NewSimulation(1)
	if _, ok := TryFire(&svc, slot, 0, Target{Kind: TargetPoint}, 0, FirePorts{RNG: &r}); ok {
		t.Fatalf("weapon with no creation family should not spawn")
	}
	if svc.Count() != 0 {
		t.Fatalf("no family should mean no reservation, got %d", svc.Count())
	}
}
