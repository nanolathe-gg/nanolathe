package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

type windGeneratorCallback struct {
	name string
	arg  int32
}

// windGeneratorRecorderVM is an authored two-entry COB fixture. Each entry
// writes its callback argument to a distinct test port, so the test observes
// the actual deferred script execution rather than a producer-side call.
func windGeneratorRecorderVM(record *[]windGeneratorCallback) *cob.VM {
	writeArgument := func(port uint32) []uint32 {
		return []uint32{
			0x10021001, port,
			0x10021002, 0, // push callback window word 0
			0x10082000, // engine write
			0x10065000, // return
		}
	}
	direction := writeArgument(1)
	speed := writeArgument(2)
	prog := &cob.Program{
		Code:        append(direction, speed...),
		Scripts:     map[string]int{"SetDirection": 0, "SetSpeed": len(direction)},
		ScriptsByID: []int{0, len(direction)},
		Pieces:      []string{"root"},
	}
	vm := cob.NewVM(prog)
	vm.BindPortBinding(1, cob.PortBinding{Write: func(value int32) {
		*record = append(*record, windGeneratorCallback{name: "direction", arg: value})
	}})
	vm.BindPortBinding(2, cob.PortBinding{Write: func(value int32) {
		*record = append(*record, windGeneratorCallback{name: "speed", arg: value})
	}})
	return vm
}

// TestWindGeneratorCallbacksUseTheGeneralUpdateDrain locks the general-update
// producer: it is neither an activation/completion path nor controller work,
// and its two deferred starts execute in this visit's one normal drain
// [04 R-MOV-03 §1][05 R-PROD-01 §3][04 R-CB-01 §5].
func TestWindGeneratorCallbacksUseTheGeneralUpdateDrain(t *testing.T) {
	s := strictNewSessionWithUnits(t, 1, 71, 19)
	u := s.Units.IterSliced()[0]
	u.Def.WindGenerator = 1
	u.Activated = false
	u.Remaining = 1
	// Remote owners enter the visit but not the controller-1/2 work block.
	// The notifier belongs to the earlier general update and still runs.
	s.Econ.Players[u.Owner].ControllerState = combat.ControlByteRemote
	s.Wind = &world.Wind{Changed: true, Heading: 0x9234, Strength: 123}

	var record []windGeneratorCallback
	vm := windGeneratorRecorderVM(&record)
	attachCallbackWindowVM(u, vm)
	u.ScriptState.Bridge = u.COBBinding().Callbacks
	vm.DrainCalls = 0

	s.stepUnitPhase(1)
	if got, want := record, []windGeneratorCallback{{name: "direction", arg: 0x9234}, {name: "speed", arg: 123 << 4}}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("wind callback record = %#v, want %#v", got, want)
	}
	if vm.DrainCalls != 1 {
		t.Fatalf("COB drains = %d, want one normal visit drain", vm.DrainCalls)
	}

	s.Wind.Changed = false
	s.stepUnitPhase(2)
	if len(record) != 2 {
		t.Fatalf("non-change visit repeated callbacks: %#v", record)
	}
	if vm.DrainCalls != 2 {
		t.Fatalf("non-change visit drains = %d, want its one normal drain only", vm.DrainCalls)
	}
}

// TestWindPhaseNotificationWaitsForTheNextSessionSweep verifies the phase
// boundary through Session.Step: phase 8 of tick N publishes the change, and
// the unit sweep of tick N+1 consumes it [04 R-MOV-03 §1][05 R-PROD-01 §3].
func TestWindPhaseNotificationWaitsForTheNextSessionSweep(t *testing.T) {
	s := strictNewSessionWithUnits(t, 1, 73, 23)
	u := s.Units.IterSliced()[0]
	u.Def.WindGenerator = 1
	var record []windGeneratorCallback
	attachCallbackWindowVM(u, windGeneratorRecorderVM(&record))
	u.ScriptState.Bridge = u.COBBinding().Callbacks

	s.Step(1)
	if s.Clock.GlobalTick != 1 {
		t.Fatalf("first Step tick = %d, want 1", s.Clock.GlobalTick)
	}
	if len(record) != 0 {
		t.Fatalf("phase-8 redraw notified during its own tick: %#v", record)
	}
	if !s.Wind.Changed {
		t.Fatal("phase 8 did not publish the changed flag")
	}
	wantHeading, wantStrength := s.Wind.Heading, s.Wind.Strength

	s.Step(2)
	if s.Clock.GlobalTick != 2 {
		t.Fatalf("second Step tick = %d, want 2", s.Clock.GlobalTick)
	}
	if got, want := record, []windGeneratorCallback{{name: "direction", arg: int32(wantHeading)}, {name: "speed", arg: wantStrength << 4}}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("next-sweep callback record = %#v, want %#v", got, want)
	}
}
