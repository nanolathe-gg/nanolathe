package session

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func patrolTraceProjection(t *testing.T, authored string) string {
	t.Helper()
	var unit ParityUnit
	if err := json.Unmarshal([]byte(authored), &unit); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	writeParityUnit(func(format string, args ...interface{}) { fmt.Fprintf(&out, format, args...) }, unit)
	return out.String()
}

// Return references, provenance, arrival and route-wide pause all affect future
// patrol work. Default receipts must keep the existing partial parity stream.
func TestPatrolTraceIncludesReturnAndRecoveryState(t *testing.T) {
	base := patrolTraceProjection(t, `{"Orders":[{"ID":1}]}`)
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(base))); got != "6390244eb0625fd5868a451a2559feb77537459c879df19656fe421b180eac6e" {
		t.Fatalf("default patrol receipts changed the pre-existing parity stream: %s", got)
	}
	if got := patrolTraceProjection(t, `{"patrolWorkPaused":false,"Orders":[{"ID":1,"workReturnOrdinal":0,"patrolReturn":false,"patrolReturnArrived":false}]}`); got != base {
		t.Fatal("default patrol receipts changed the legacy parity stream")
	}
	states := []string{
		`{"patrolWorkPaused":true,"Orders":[{"ID":1}]}`,
		`{"Orders":[{"ID":1,"workReturnOrdinal":2}]}`,
		`{"Orders":[{"ID":1,"workReturnOrdinal":3}]}`,
		`{"Orders":[{"ID":1,"workReturnOrdinal":-1}]}`,
		`{"Orders":[{"ID":1,"patrolReturn":true}]}`,
		`{"Orders":[{"ID":1,"patrolReturnArrived":true}]}`,
	}
	seen := []string{base}
	for _, authored := range states {
		got := patrolTraceProjection(t, authored)
		for _, previous := range seen {
			if got == previous {
				t.Fatalf("patrol receipt is missing or conflated: %s", authored)
			}
		}
		seen = append(seen, got)
	}
}

// Ordinary Modern selection and pump outcomes produce the receipts. This
// checks their live projection as well as the writer's authored value contract.
func TestPatrolTraceProjectsLiveReturnAndRecovery(t *testing.T) {
	for _, air := range []bool{false, true} {
		t.Run(fmt.Sprintf("air%v", air), func(t *testing.T) {
			rules := &orders.ModernRules{}
			builder := &units.Unit{Handle: 1, Alive: true, Health: 100, MaxHealth: 100,
				X: numeric.FixedFromInt(100) + 1, Y: numeric.FixedFromInt(40) + 2, Z: numeric.FixedFromInt(160) + 3,
				Def: &content.UnitDef{BMCode: 1, Builder: true, CanFly: air, CanReclamate: true, SightDistance: 280, MaxDamage: 100}}
			target := &units.Unit{Handle: 2, Alive: true, Health: 1, Remaining: 1,
				X: numeric.FixedFromInt(132), Z: numeric.FixedFromInt(160), Def: &content.UnitDef{MaxDamage: 100}}
			target.Move.Mode, target.Move.ModeMirror = 1, 1
			name := "RepairPatrol"
			if air {
				name = "VTOL_RepairPatrol"
			}
			patrol, next := &orders.Node{ID: orders.Lookup(name), Phase: 1}, &orders.Node{ID: orders.Lookup(name), Phase: 1}
			q := orders.NewQueueWith([]*orders.Node{patrol, next}, nil)
			builder.Orders = q
			binding := &orders.QueueBinding{Rules: rules, World: orders.NewWorldQueryAdapter(orders.WorldQueryAdapterConfig{
				SeaLevel: func() uint8 { return 0 },
				ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
					visit(target.Handle, target)
				},
			})}
			binding.SetLookup(func(h pool.Handle) *units.Unit {
				if h == target.Handle {
					return target
				}
				return nil
			})
			binding.SetResources(func(uint8) (orders.ResourceView, bool) {
				return orders.ResourceView{Stock: [2]float32{100, 100}, Capacity: [2]float32{100, 100}}, true
			})
			q.SetBinding(binding)
			if _, handled := rules.PatrolWorkVisit(builder, patrol, 100); !handled || q.Head() == patrol || q.PrimaryLen() != 4 {
				t.Fatal("fixture did not borrow work with an ordinary saved-position return")
			}
			s := &Session{}
			borrowed := s.parityUnit(builder)
			work, back := q.PrimaryAt(0), q.PrimaryAt(1)
			if borrowed.PatrolWorkPaused || borrowed.Orders[0].WorkAssignmentOrdinal != 3 || borrowed.Orders[0].WorkReturnOrdinal != 2 ||
				borrowed.Orders[1].WorkAssignmentOrdinal != 3 || !borrowed.Orders[1].PatrolReturn || borrowed.Orders[1].PatrolReturnArrived ||
				borrowed.Orders[1].GoalX != builder.X.Raw() || borrowed.Orders[1].GoalY != builder.Y.Raw() || borrowed.Orders[1].GoalZ != builder.Z.Raw() {
				t.Fatalf("borrowed projection lost receipts or exact anchor: %+v", borrowed.Orders)
			}
			q.SetOwnedHandler(work.ID, func(*units.Unit, *orders.Node, uint32, uint32) (orders.Code, bool) { return 8, true })
			destinationPhase := uint8(1)
			if air {
				destinationPhase = 2
			}
			q.SetOwnedHandler(back.ID, func(_ *units.Unit, n *orders.Node, _ uint32, _ uint32) (orders.Code, bool) {
				n.Phase, n.DynamicGate = destinationPhase, 0xE0 // movement outcome mask [04 R-ORD-01 §0]
				return 2, true
			})
			q.Pump(builder, 101)
			failed := s.parityUnit(builder)
			if q.Head() != back || !failed.PatrolWorkPaused || !failed.Orders[0].PatrolReturn || failed.Orders[0].WorkAssignmentOrdinal != 2 {
				t.Fatalf("failed-work projection lost recovery: %+v", failed)
			}
			back.Satisfied = 0x20 // arrived [04 R-ORD-01 §0]
			q.Pump(builder, 102)
			arrived := s.parityUnit(builder)
			if !arrived.Orders[0].PatrolReturnArrived || !arrived.PatrolWorkPaused {
				t.Fatal("return arrival projection lost arrival or cleared route pause")
			}
			if borrowed.PatrolWorkPaused || borrowed.Orders[1].PatrolReturnArrived || failed.Orders[0].PatrolReturnArrived {
				t.Fatal("later lifecycle state mutated an earlier trace value")
			}
		})
	}
}
