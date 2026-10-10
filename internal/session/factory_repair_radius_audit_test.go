package session

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The factory allocator really attaches its unfinished product in mode 1.
// It would pass the repair visitor, but the radius population must omit it
// before that visitor runs in Strict/Community [04 R-COLL-01 §11][04 R-ORD-02 §4].
// Modern separately selects unfinished products through its all-unit work scan
// (DESIGN_UNITS_ORDERS_COB "Modern patrol work").
func TestFactoryProductExcludedFromRepairRadius(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			for _, detached := range []bool{false, true} {
				s := assistSeamSession(t)
				s.SetGameplay(mode)
				s.Movement.BindWorld(s.Units)
				factoryDef := s.Catalog.Units["armcom"]
				factoryDef.BMCode, factoryDef.Builder, factoryDef.WorkerTime = 0, true, 30
				factoryDef.Commander = false
				builderDef := s.Catalog.Units["assistseamcon"]
				builderDef.CanPatrol, builderDef.CanReclamate, builderDef.SightDistance = true, true, 256
				create := func(key string, x, z int32) *units.Unit {
					h, err := s.Units.Create(s.Catalog.Units[key], 0, world.CellToWorld(x), 0, world.CellToWorld(z))
					if err != nil {
						t.Fatal(err)
					}
					u := s.Units.Unit(h)
					s.Movement.EnsureUnit(u)
					s.bindOrderQueue(u)
					return u
				}
				factory := create("armcom", 8, 8)
				builder := create("assistseamcon", 12, 8)
				patient := create("assistseamprod", 14, 8)
				patient.Health = 50
				// Authored callback returns the sole build piece, displaced beyond the
				// factory footprint. The readiness level stands in for its door script.
				prog := &cob.Program{
					Code:    []uint32{0x10021001, 0, 0x10023002, 0, 0x10065000},
					Scripts: map[string]int{"QueryBuildInfo": 0}, ScriptsByID: []int{0}, Pieces: []string{"base"},
				}
				vm := cob.NewVM(prog)
				mdl := &model.Model{Root: 0, Pieces: []model.Piece{{Name: "base", Parent: -1, Translate: [3]numeric.Fixed{32 << 16, 0, 0}}}}
				factory.Script = vm
				factory.ScriptState = &units.ScriptState{VM: vm, Binding: &cob.Binding{VM: vm, Model: mdl, PieceMap: []int{0}, Callbacks: cob.NewCallbackBridge(vm)}}
				factory.InBuildStance = true
				if err := construction.QueueFactoryBuild(factory, "assistseamprod", 1, s.Catalog); err != nil {
					t.Fatal(err)
				}
				fq := orders.QueueForUnit(factory)
				for tick := uint32(1); tick <= 3 && fq.Head().Target == 0; tick++ {
					if result := s.Build.StepUnit(construction.TickContext{Tick: tick}, factory.Handle); result.Err != nil {
						t.Fatal(result.Err)
					}
				}
				product := s.Units.Unit(fq.Head().Target)
				if product == nil || product.Attachment.Carrier != factory.Handle || product.Move.ModeMirror != 1 || product.Remaining != 1 {
					t.Fatalf("factory did not create carried grounded nanoframe: %+v", product)
				}
				s.Movement.SyncCarriedMotion(s.Units)
				if detached {
					// Use the actual release helper while leaving the frame unfinished to
					// isolate population from the repair visitor's health/progress gates.
					if _, ok := movement.DetachFactoryProduct(s.Units, product.Handle); !ok {
						t.Fatal("release refused")
					}
				}
				var visited []pool.Handle
				q := orders.QueueForUnit(builder)
				binding := q.Binding()
				scan := binding.World.ForEachUnitInRadiusHook()
				binding.World.SetForEachUnitInRadius(func(x, z, radius numeric.Fixed, visit func(pool.Handle, *units.Unit) bool) {
					scan(x, z, radius, func(h pool.Handle, u *units.Unit) bool {
						visited = append(visited, h)
						return visit(h, u)
					})
				})
				// Community Roam issues no return move; Modern retains its saved return.
				// Only the baseline radius candidate-count pick draws in this visit.
				builder.Flags = builder.Flags&^(uint32(3)<<units.StandingMoveShift) | uint32(2)<<units.StandingMoveShift
				id := orders.Lookup("RepairPatrol")
				q.Push(id, orders.Node{Owner: builder.Handle, Phase: 1, GoalX: builder.X, GoalZ: builder.Z})
				before, crtBefore := s.SimRNG().Draws(), s.CrtRNG().Draws()
				stock := s.Econ.Players[0].Stock
				code := orders.DescriptorFor(id).Handler(builder, q.Head(), 0, 100)
				if mode == gameplay.Modern {
					if len(visited) != 0 || code != 2 || q.Head().ID != orders.Lookup("HelpBuild") || q.Head().Target != product.Handle {
						t.Fatalf("detached=%v: Modern product assistance code=%d head=%+v radius population=%v", detached, code, q.Head(), visited)
					}
					if s.SimRNG().Draws() != before || s.CrtRNG().Draws() != crtBefore {
						t.Fatal("Modern work selection changed either RNG")
					}
					if got := s.parityUnit(builder).Orders[0].WorkAssignmentOrdinal; got != 3 {
						t.Fatalf("borrowed work lost its retained patrol identity in the parity projection: %d", got)
					}
				} else {
					wantDraws := uint64(0)
					if detached {
						wantDraws = 1
					}
					if !slices.Contains(visited, patient.Handle) || slices.Contains(visited, product.Handle) != detached {
						t.Fatalf("detached=%v: radius population %v, patient=%d product=%d", detached, visited, patient.Handle, product.Handle)
					}
					if got := s.SimRNG().Draws() - before; got != wantDraws || s.CrtRNG().Draws() != crtBefore {
						t.Fatalf("detached=%v: sim draws=%d want %d; CRT changed=%v", detached, got, wantDraws, s.CrtRNG().Draws() != crtBefore)
					}
					if code != 6 || q.Head().ID == id || (!detached && q.Head().Target != patient.Handle) {
						t.Fatalf("detached=%v: repair result=%d head=%+v", detached, code, q.Head())
					}
				}
				if s.Econ.Players[0].Stock != stock {
					t.Fatal("selection spent resources before repair work")
				}
			}
		})
	}
}
