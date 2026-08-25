package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Truthful trace contract [RX-03][ON-09 evidence contract]: trace kinds are
// emitted from observed per-visit outcomes, never inferred; the AI-aux stage
// has its own kind and VictoryLatch only fires on an actual latch.

func TestRX03_Trace_AimDispatchThenCOBReturnAreObserved(t *testing.T) {
	const simSeed, crtSeed uint32 = 77, 88
	rng.SeedGlobal(simSeed, crtSeed)
	cat := strictMinimalCatalog()
	wdef := &content.WeaponDef{ID: 1, WeaponVelocity: 65536 * 5, Range: 5000 * 65536, ReloadTime: 2, Damage: map[string]int32{"default": 500}, Turret: true}
	wdef.CanonicalKey = content.CanonicalKey("rx03gun")
	if cat.Weapons == nil {
		cat.Weapons = map[string]*content.WeaponDef{}
	}
	cat.Weapons[wdef.CanonicalKey] = wdef
	cat.RebuildWeaponIndex()
	shooterDef := cat.Units["armcom"]
	shooterDef.CanAttack = true
	shooterDef.MaxDamage = 1000
	shooterDef.Weapon1 = "rx03gun"
	shooterDef.Weapon1Def = wdef
	targetDef := cat.Units["corcom"]
	targetDef.MaxDamage = 200

	s := strictNewSessionWithUnits(0, simSeed, crtSeed)
	hShooter, _ := s.Units.Create(shooterDef, 0, numeric.Fixed(10*16*65536), 0, numeric.Fixed(10*16*65536))
	hTarget, _ := s.Units.Create(targetDef, 1, numeric.Fixed(12*16*65536), 0, numeric.Fixed(12*16*65536))
	shooter := s.Units.Unit(hShooter)
	target := s.Units.Unit(hTarget)
	shooter.Slots[0].Weapon = wdef
	shooter.Slots[0].Ammo = 10
	// Target is armed directly here: this test locks trace emission truth, not
	// production acquisition (the strict G4 gate owns that path).
	shooter.Slots[0].Target = units.Target{Kind: units.TargetUnit, Unit: hTarget}
	shooter.Slots[0].Flags |= 0x02
	// AimPrimary: push 1; return → same-tick nonzero return [06 §3.3]
	code := []uint32{0x10021001, 1, 0x10065000}
	prog := &cob.Program{Code: code, Scripts: map[string]int{"AimPrimary": 0}, Statics: 0, Pieces: []string{"base"}, ScriptsByID: []int{0}}
	shooter.SetScript(cob.NewVM(prog))
	publishOne(s, shooter)
	publishOne(s, target)
	s.SetTraceEnabled(true)
	s.ClearTrace()

	firstDispatch, firstReturn := -1, -1
	for tick := 1; tick <= 30 && firstDispatch == -1; tick++ {
		s.Step(int32(tick))
		evs := s.TraceEvents()
		for i, ev := range evs {
			if ev.Kind == TraceWeaponAimDispatch && ev.Handle == hShooter {
				firstDispatch = i
			} else if ev.Kind == TraceCOBReturn && ev.Handle == hShooter && firstDispatch >= 0 {
				firstReturn = i
				break
			}
		}
	}
	if firstDispatch < 0 {
		t.Fatalf("no WeaponAimDispatch observed for shooter in 30 ticks")
	}
	if firstReturn < firstDispatch {
		t.Fatalf("COBReturn (idx %d) must follow its AimDispatch (idx %d)", firstReturn, firstDispatch)
	}
	// Dispatch must carry the real slot/weapon identity.
	evs := s.TraceEvents()
	if evs[firstDispatch].Slot != 0 || evs[firstDispatch].WeaponID != wdef.ID {
		t.Fatalf("dispatch event slot=%d weapon=%d want 0/%d", evs[firstDispatch].Slot, evs[firstDispatch].WeaponID, wdef.ID)
	}
}

func TestRX03_Trace_NoVictoryLatchNoiseWithoutResult(t *testing.T) {
	s := strictNewSessionWithUnits(2, 91, 92)
	s.SetTraceEnabled(true)
	s.ClearTrace()
	for tick := 1; tick <= 20; tick++ {
		s.Step(int32(tick))
	}
	if res := s.GetResult(); res.Ended {
		t.Skipf("session latched a result early; noise assertion not applicable")
	}
	for _, ev := range s.TraceEvents() {
		if ev.Kind == TraceVictoryLatch {
			t.Fatalf("VictoryLatch emitted without a result latch [RX-03]")
		}
	}
	if len(s.TraceEvents()) == 0 {
		t.Fatalf("trace produced no events at all")
	}
	foundAux := false
	for _, ev := range s.TraceEvents() {
		if ev.Kind == TraceAIAux {
			foundAux = true
			break
		}
	}
	if !foundAux {
		t.Fatalf("AIAux stage marker missing [ON-09 stage 10]")
	}
}

// Weaponless scripted units must have their COB threads progressed by the
// authoritative loop's exactly-once per-visit drain [04 §4.2][04 §4.6].
func TestRX03_Drain_WeaponlessScriptThreadProgresses(t *testing.T) {
	s := strictNewSessionWithUnits(2, 93, 94)
	def := strictMinimalCatalog().Units["armcom"]
	h, _ := s.Units.Create(def, 0, strictCellToWorld(20), 0, strictCellToWorld(20))
	u := s.Units.Unit(h)
	// SlowReturn: sleep 67ms (=2 ticks); push 7; return.
	code := []uint32{
		0x10021001, 67,
		0x10013000,
		0x10021001, 7,
		0x10065000,
	}
	prog := &cob.Program{Code: code, Scripts: map[string]int{"SlowReturn": 0}, Statics: 0, Pieces: []string{"base"}, ScriptsByID: []int{0}}
	vm := cob.NewVM(prog)
	u.SetScript(vm)
	if !vm.StartByName("SlowReturn", nil) {
		t.Fatalf("could not start SlowReturn thread")
	}
	threadIdx := vm.LastStartedThread()
	publishOne(s, u)
	for tick := 1; tick <= 6; tick++ {
		s.Step(int32(tick))
	}
	val, ok := vm.ConsumeReturn(threadIdx)
	if !ok || val != 7 {
		t.Fatalf("scripted weaponless unit thread never progressed: ok=%v val=%d (want ok=true val=7)", ok, val)
	}
}
