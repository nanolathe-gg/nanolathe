package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestPhase2AlreadyDyingRunsNormalStagesAndFinalizesOnce(t *testing.T) {
	s := newLoopTestSession(t, 0)
	// The unit must be allocated with its script so the engine-port handlers
	// are bound at attachment; a VM installed directly through SetScript has
	// no port 5 receiver, and the write below would land nowhere.
	def := s.Catalog.Units["armcom"]
	def.Script = &cob.Program{Code: []uint32{
		0x10021001, 5, // push INBUILDSTANCE port
		0x10021001, 1, // push true
		0x10082000, // set port during the normal COB stage
	}}
	h, err := s.Units.Create(def, 0, numeric.Fixed(10*65536), 0, numeric.Fixed(10*65536))
	if err != nil {
		t.Fatalf("create scripted unit: %v", err)
	}
	ensureMovementForAll(s)
	u := s.Units.Unit(h)
	vm := u.GetScript()
	if vm == nil {
		t.Fatal("scripted unit has no VM")
	}
	vm.Threads[0].Status = cob.ThreadRunning
	u.PriorSample = 17
	u.Dying = true
	u.DeathCause = units.DeathKilled
	var primary, extra int
	s.Units.OnDeath = func(pool.Handle, units.DeathCause, *units.Unit) { primary++ }
	s.Units.OnDeathExtra = func(pool.Handle, units.DeathCause, *units.Unit) { extra++ }

	s.stepUnitPhase(1)
	if s.Units.Unit(h) != nil {
		t.Fatal("already-dying unit was not finalized at its phase-2 slot")
	}
	if primary != 1 || extra != 1 {
		t.Fatalf("death callbacks=%d/%d, want exactly once", primary, extra)
	}
	if !u.InBuildStance || vm.Threads[0].Status == cob.ThreadRunning {
		t.Fatal("already-dying unit did not run its normal COB stage")
	}
}

func TestPhase3DeathSurvivesKillTickAndFinalizesNextPhase2(t *testing.T) {
	s := newLoopTestSession(t, 2)
	unitsInOrder := s.Units.IterSliced()
	victim := unitsInOrder[0]
	h := pool.Handle(victim.Handle)
	deathCallbacks := 0
	s.Units.OnDeath = func(pool.Handle, units.DeathCause, *units.Unit) { deathCallbacks++ }

	// Complete phase 2, then model a lethal phase-3 damage result. The remaining
	// phases and publication must still observe the death-marked record.
	s.stepUnitPhase(1)
	s.Units.Destroy(h, units.DeathKilled)
	if deathCallbacks != 0 {
		t.Fatalf("phase-3 death callback fired before next phase-2: %d", deathCallbacks)
	}
	s.phaseProjectiles(1)
	s.phaseEffects(1)
	s.phaseOrders(1)
	s.phaseFeatureLifecycle(1)
	s.phaseSequences(1)
	s.phaseWind(1)
	s.phaseMeteorShower(1)
	s.phaseCameraShake(1)
	s.phaseObjectSweeps(1)
	s.phaseCadenceFlip(1)
	s.publishSnapshot(1)
	if got := s.Units.Unit(h); got == nil || !got.Dying {
		t.Fatal("phase-3 death was not observable through kill-tick publication")
	}
	if frame := s.Snapshot.Current(); frame == nil || len(frame.Units) == 0 {
		t.Fatal("kill-tick snapshot omitted the dying unit")
	}

	s.stepUnitPhase(2)
	if deathCallbacks != 1 {
		t.Fatalf("phase-3 death callbacks=%d, want one at next phase-2", deathCallbacks)
	}
	if s.Units.Unit(h) != nil {
		t.Fatal("phase-3 death survived its next phase-2 slot")
	}
	replacement, err := s.Units.Create(s.Catalog.Units["armcom"], victim.Owner, victim.X, victim.Y, victim.Z)
	if err != nil {
		t.Fatalf("reuse allocation: %v", err)
	}
	if replacement != h {
		t.Fatalf("replacement handle=%d, want immediate lowest-slot reuse %d", replacement, h)
	}
}
