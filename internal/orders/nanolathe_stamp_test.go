package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestWorkVisitsStampTheCloakDeadline locks the nanolathe-active stamp: a work
// handler that does work writes the acting unit's shared reveal/cloak deadline
// outright, at `tick + 150` for the repair pair, `tick + 300` for the build and
// feature families and `tick + 900` for capture [04 R-ORD-01 §1]
// [04 R-ORD-01 §5][05 R-WORK-01 §3][05 R-WORK-01 §6]. Its economy consumer is
// the cloak-payment gate, which pays only once the global tick has reached the
// deadline [05 R-ECO-01 §9], so a working unit stops paying cloak upkeep for
// the stamped span.
func TestWorkVisitsStampTheCloakDeadline(t *testing.T) {
	cases := []struct {
		name string
		tick uint32
		want uint32
		run  func(builder, target *units.Unit, tick uint32) Code
	}{
		{
			name: "RepairUnitNoMove phase 1 stamps 150",
			tick: 2,
			want: 2 + 150,
			run: func(builder, target *units.Unit, tick uint32) Code {
				target.Health = 50
				n := &Node{ID: Lookup("RepairUnitNoMove"), Owner: builder.Handle, Target: target.Handle, Phase: 1}
				return repairUnitNoMoveHandler(builder, n, 0, tick)
			},
		},
		{
			name: "RepairUnit phase 3 stamps 150",
			tick: 40,
			want: 40 + 150,
			run: func(builder, target *units.Unit, tick uint32) Code {
				target.Health = 50
				n := &Node{ID: Lookup("RepairUnit"), Owner: builder.Handle, Target: target.Handle, Phase: 3}
				return repairUnitHandler(builder, n, 0, tick)
			},
		},
		{
			name: "HelpBuild phase 3 stamps 300",
			tick: 7,
			want: 7 + 300,
			run: func(builder, target *units.Unit, tick uint32) Code {
				target.Remaining = 1
				n := &Node{ID: Lookup("HelpBuild"), Owner: builder.Handle, Target: target.Handle, Phase: 3}
				return helpBuildHandler(builder, n, 0, tick)
			},
		},
		{
			name: "Capture phase 4 stamps 900",
			tick: 11,
			want: 11 + 900,
			run: func(builder, target *units.Unit, tick uint32) Code {
				n := &Node{ID: Lookup("Capture"), Owner: builder.Handle, Target: target.Handle,
					Phase: 4, Param1: 0, Param2: 100}
				return captureHandler(builder, n, 0, tick)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, builder, target := workFixture()
			builder.RevealDeadline = 1 // a stale stamp the visit must overwrite outright
			tc.run(builder, target, tc.tick)
			if builder.RevealDeadline != tc.want {
				t.Fatalf("cloak deadline = %d, want %d [04 R-ORD-01 §1][05 R-ECO-01 §9]",
					builder.RevealDeadline, tc.want)
			}
		})
	}
}

// TestSelfRepairStampsThePatient locks the one site whose stamp does not land
// on a builder: `SelfRepair` lives on the PATIENT, and the stamp goes on the
// unit running the order, not on the repairer whose energy is billed
// [04 R-ORD-01 §2][05 R-WORK-01 §3].
func TestSelfRepairStampsThePatient(t *testing.T) {
	q, repairer, patient := workFixture()
	// The record lives on the patient, so the patient is the unit whose queue
	// resolves the repairer handle [04 R-ORD-01 §2].
	pq := &Queue{}
	pq.SetBinding(q.Binding())
	BindQueue(patient, pq)
	repairer.Remaining = 0
	patient.Health = 10
	patient.MaxHealth = 100
	n := &Node{ID: Lookup("SelfRepair"), Owner: patient.Handle, Target: repairer.Handle, Phase: 1}
	selfRepairHandler(patient, n, 0, 500)
	if patient.RevealDeadline != 500+150 {
		t.Fatalf("patient cloak deadline = %d, want %d", patient.RevealDeadline, 500+150)
	}
	if repairer.RevealDeadline != 0 {
		t.Fatalf("repairer was stamped %d; the row stamps the order's own unit", repairer.RevealDeadline)
	}
}
