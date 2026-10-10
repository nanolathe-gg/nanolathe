package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The explicit-altitude port carries the signed offset as Y, leaving the
// movement-owned setter to derive terrain height [04 R-AIR-01 §4]. Narrow the
// authored word before signed halving [04 R-ORD-01 §7].
func TestConstructorBaselineTakeoffAltitude(t *testing.T) {
	for _, row := range []struct{ cruise, half int32 }{
		{201, 100}, {-3, -1}, {65535, 0}, {65533, -1}, {32768, -16384}, {65737, 100},
	} {
		t.Run(fmt.Sprint(row.cruise), func(t *testing.T) {
			q, builder, _ := vtolWorkFixture()
			builder.Def.CruiseAlt = row.cruise
			var markers []AirGoalRequest
			q.Binding().Movement.SetInstallAir(func(req AirGoalRequest) bool {
				markers = append(markers, req)
				return true
			})
			n := &Node{ID: Lookup("VTOL_HelpBuild"), Owner: builder.Handle}
			beforeSim, beforeCRT := rng.Global.Sim.Draws(), rng.Global.Crt.Draws()
			if code := airWorkPreamble(builder, n, "Building"); code != 1 {
				t.Fatalf("takeoff returned %d, want advance", code)
			}
			if len(markers) != 1 {
				t.Fatalf("takeoff installed %d markers, want one", len(markers))
			}
			got := markers[0]
			if got.Flags != 0x08 || got.Y != numeric.FixedFromInt(int64(row.half)) {
				t.Errorf("takeoff flags/Y = %#x/%d, want explicit signed offset %#x/%d", got.Flags, got.Y, 0x08, numeric.FixedFromInt(int64(row.half)))
			}
			if got.X != builder.X || got.Z != builder.Z || got.Radius != 0 || got.Node != n || got.Owner != builder.Handle {
				t.Errorf("takeoff changed point identity or horizontal geometry: %+v", got)
			}
			if n.DynamicGate != gateMoveOutcomes || builder.Move.Mode&3 != 2 {
				t.Errorf("takeoff gate/mode = %#x/%d, want %#x/2", n.DynamicGate, builder.Move.Mode&3, gateMoveOutcomes)
			}
			builder.Move.ModeMirror = 2
			if code := airWorkPreamble(builder, n, "Building"); code != 1 || len(markers) != 1 {
				t.Errorf("airborne preamble reinstalled takeoff: code=%d markers=%d", code, len(markers))
			}
			if rng.Global.Sim.Draws() != beforeSim || rng.Global.Crt.Draws() != beforeCRT {
				t.Error("takeoff changed a random stream")
			}
		})
	}
}

func TestConstructorBaselineFullCruiseMarkers(t *testing.T) {
	for _, row := range []struct {
		name     string
		handler  func(*units.Unit, *Node, uint32, uint32) Code
		gate     uint32
		deadline int32
	}{
		{"VTOL_RepairUnit", vtolRepairUnitHandler, gateWorkApproach, -1},
		{"VTOL_RepairPatrol", vtolRepairPatrolHandler, gateMoveOutcomes | gateDeadline, 145},
	} {
		for _, cruise := range []int32{201, 65533} {
			t.Run(fmt.Sprintf("%s/%d", row.name, cruise), func(t *testing.T) {
				q, builder, target := vtolWorkFixture()
				builder.Def.CruiseAlt = cruise
				var markers []AirGoalRequest
				q.Binding().Movement.SetInstallAir(func(req AirGoalRequest) bool {
					markers = append(markers, req)
					return true
				})
				n := &Node{ID: Lookup(row.name), Owner: builder.Handle, Target: target.Handle,
					Phase: 1, Deadline: -1, GoalX: target.X, GoalY: target.Y, GoalZ: target.Z}
				row.handler(builder, n, 0, 100)
				if len(markers) != 1 {
					t.Fatalf("installed %d markers, want one", len(markers))
				}
				got := markers[0]
				if got.Flags != 0x08 || got.Y != numeric.FixedFromInt(int64(int16(cruise))) {
					t.Errorf("flags/Y = %#x/%d, want explicit full signed cruise offset", got.Flags, got.Y)
				}
				if got.X != target.X || got.Z != target.Z || got.Radius != 0 || got.Node != n {
					t.Errorf("full-cruise marker changed point geometry or identity: %+v", got)
				}
				if n.DynamicGate != row.gate || n.Deadline != row.deadline {
					t.Errorf("gate/deadline = %#x/%d, want %#x/%d", n.DynamicGate, n.Deadline, row.gate, row.deadline)
				}
			})
		}
	}
}

// Repair's setters do not belong to the assistance-radius and feature-reclaim
// destination markers [04 R-ORD-01 §7]. The construction orbit has its separate
// heading-only contract [04 §10.3].
func TestConstructorBaselineDefaultWorkMarkers(t *testing.T) {
	q, builder, target := vtolWorkFixture()
	var marker AirGoalRequest
	q.Binding().Movement.SetInstallAir(func(req AirGoalRequest) bool { marker = req; return true })
	n := &Node{ID: Lookup("VTOL_HelpBuild"), Owner: builder.Handle, Target: target.Handle, Phase: 1}
	if code := vtolHelpBuildHandler(builder, n, 0, 100); code != 1 || marker.Flags != 0 || marker.Y != target.Y || marker.Radius != builder.Def.BuildDistance {
		t.Fatalf("assistance marker changed: code=%d marker=%+v", code, marker)
	}
	n = &Node{ID: Lookup("VTOL_Reclaim"), Owner: builder.Handle, Phase: 1,
		GoalX: builder.X, GoalY: builder.Y, GoalZ: builder.Z}
	if code := vtolReclaimHandler(builder, n, 0, 100); code != 1 || marker.Flags != 0 || marker.Y != builder.Y || marker.Radius != 0 {
		t.Fatalf("reclaim destination gained a setter: code=%d marker=%+v", code, marker)
	}
}

// Every selected reclaim arm releases the patrol payload before inserting work.
// Keep the retained route, release wake, cleared gate and reached tournament
// draws together [04 R-ORD-01 §4, §7].
func TestConstructorBaselineReclaimHandoff(t *testing.T) {
	for _, mode := range []struct {
		name  string
		rules Rules
	}{
		{"strict", StrictRules{}}, {"community", CommunityRules{}},
	} {
		for _, air := range []bool{false, true} {
			for _, resource := range []struct {
				name          string
				stock         [2]float32
				metal, energy int32
				draws         uint64
			}{
				{"metal shortage", [2]float32{0, 100}, 10, 10, 6},
				{"metal fits", [2]float32{50, 0}, 10, 0, 3},
				{"energy shortage", [2]float32{50, 0}, 10, 10, 6},
				{"energy fallback", [2]float32{0, 50}, 0, 10, 3},
			} {
				t.Run(fmt.Sprintf("%s/air=%v/%s", mode.name, air, resource.name), func(t *testing.T) {
					actor, candidates, sim, q := repairPatrolRefusalFixture(t, air)
					for _, candidate := range candidates {
						candidate.Health = candidate.Def.MaxDamage
					}
					b := q.Binding()
					b.Rules = mode.rules
					b.Community.PatrollingBuilderFilters = true
					actor.Flags |= 1 << units.StandingMoveShift
					b.SetResources(func(uint8) (ResourceView, bool) {
						return ResourceView{Stock: resource.stock, Capacity: [2]float32{100, 100}}, true
					})
					b.World.SetLookupFeature(func(int32, int32) (FeatureView, bool) {
						return FeatureView{Metal: resource.metal, Energy: resource.energy, Reclaimable: true, Autoreclaimable: true}, true
					})
					name, workName, handler := "RepairPatrol", "Reclaim", repairPatrolHandler
					if air {
						name, workName, handler = "VTOL_RepairPatrol", "VTOL_Reclaim", vtolRepairPatrolHandler
					}
					n := &Node{ID: Lookup(name), Owner: actor.Handle, Phase: 1, GoalX: numeric.FixedFromInt(400), Deadline: -1}
					next := &Node{ID: Lookup(name), Owner: actor.Handle, GoalZ: numeric.FixedFromInt(500)}
					q.SetPrimary([]*Node{n, next})
					var payload *Node
					installed := func() { payload = n }
					b.Movement.SetInstallPoint(func(PointGoalRequest) bool { installed(); return true })
					b.Movement.SetInstallAir(func(AirGoalRequest) bool { installed(); return true })
					released := false
					b.Movement.SetRelease(func(owner *Node) bool {
						if owner != n || payload != n {
							return false
						}
						if q.Primary()[0] != n {
							t.Error("patrol released only after reclaim was inserted")
						}
						released, payload = true, nil
						n.Satisfied |= 0x80
						return true
					})
					if code := handler(actor, n, 0, 100); code != 3 {
						t.Fatalf("selected reclaim returned %d, want wait", code)
					}
					if !released || payload != nil || n.Satisfied&0x80 == 0 {
						t.Errorf("patrol payload was retained across reclaim: release=%v payload-retained=%v wake=%#x", released, payload != nil, n.Satisfied)
					}
					if n.DynamicGate != 0 {
						t.Errorf("patrol retained gate %#x", n.DynamicGate)
					}
					if got := q.Primary(); len(got) != 3 || got[0].ID != Lookup(workName) || got[1] != n || got[2] != next {
						t.Errorf("reclaim did not retain exact patrol route: %+v", got)
					}
					if sim.Draws() != resource.draws {
						t.Errorf("reclaim draws=%d, want %d", sim.Draws(), resource.draws)
					}
				})
			}
		}
	}
}

// The emitter port executes a real synchronous authored QueryNanoPiece script.
// It deliberately refuses segment storage after the query: effect allocation
// cannot erase payment or the callback side effect [05 R-P0-06 §1, §2, §6].
func TestConstructorBaselineRepairAdmissionAndEmission(t *testing.T) {
	for _, mode := range []struct {
		name  string
		rules Rules
	}{
		{"strict", StrictRules{}}, {"community", CommunityRules{}}, {"modern", &ModernRules{}},
	} {
		for _, row := range []struct {
			name      string
			phase     uint8
			air, self bool
			handler   func(*units.Unit, *Node, uint32, uint32) Code
		}{
			{"RepairUnit", 3, false, false, repairUnitHandler},
			{"RepairUnitNoMove", 1, false, false, repairUnitNoMoveHandler},
			{"SelfRepair", 1, false, true, selfRepairHandler},
			{"VTOL_RepairUnit", 2, true, false, vtolRepairUnitHandler},
		} {
			for _, paid := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/paid=%v", mode.name, row.name, paid), func(t *testing.T) {
					q, owner, target := vtolWorkFixture()
					q.Binding().Rules = mode.rules
					BindQueue(target, &Queue{binding: q.Binding()})
					repairer, patient := owner, target
					if row.self {
						repairer, patient = target, owner
					}
					patient.Health = 50
					// Authored COB: assign static 0 to 7 and return piece 0.
					script := &cob.Program{Statics: 1, Pieces: []string{"base"}, Scripts: map[string]int{"QueryNanoPiece": 0}, ScriptsByID: []int{0},
						Code: []uint32{0x10021001, 7, 0x10023004, 0, 0x10021001, 0, 0x10065000}}
					scriptUnit, vm := cbUnit(script)
					repairer.ScriptState = scriptUnit.ScriptState
					bucket := q.Binding().Economy.UnitBuckets(repairer.Handle)
					if !paid {
						bucket[economy.Energy].Carry = 1
					}
					attempts, sprays := 0, 0
					q.Binding().Work = NewWorkAdapter(WorkAdapterConfig{Repair: func(builder, victim *units.Unit, _ *Node, _ uint32) bool {
						attempts++
						if builder != repairer || victim != patient {
							t.Fatal("repair reversed payer and patient")
						}
						if !economy.AdmitOneResource(bucket, 1) {
							return false
						}
						patient.Health++
						return true
					}})
					q.Binding().Presentation = NewPresentationAdapter(PresentationAdapterConfig{Nanolathe: func(builder *units.Unit, _ *Node, _ uint32) bool {
						sprays++
						if builder != repairer || attempts != 1 || bucket[economy.Energy].Requested != 1 {
							t.Fatal("nano query preceded repair attempt")
						}
						result := callbackBridgeFor(builder).QueryNanoPiece()
						if !result.Completed {
							t.Fatal("authored nano query did not finish synchronously")
						}
						return false // exhausted segment pool, after the callback
					}})
					n := &Node{ID: Lookup(row.name), Owner: owner.Handle, Target: target.Handle, Phase: row.phase, Deadline: -1}
					beforeSim, beforeCRT := rng.Global.Sim.Draws(), rng.Global.Crt.Draws()
					if code := row.handler(owner, n, 0, 100); code != 2 {
						t.Fatalf("repair returned %d, want retry hold", code)
					}
					wantSprays, wantHealth, wantAccepted := 0, int32(50), float32(0)
					if paid {
						wantSprays, wantHealth, wantAccepted = 1, 51, 1
					}
					if row.air {
						wantSprays = 1
					}
					if attempts != 1 || sprays != wantSprays {
						t.Errorf("attempts/sprays=%d/%d, want 1/%d", attempts, sprays, wantSprays)
					}
					if static := vm.DebugSnapshot().Statics[0]; (static == 7) != (wantSprays == 1) {
						t.Errorf("nano callback static=%d, want query calls=%d", static, wantSprays)
					}
					if patient.Health != wantHealth || bucket[economy.Energy].Requested != 1 || bucket[economy.Energy].Accepted != wantAccepted || bucket[economy.Metal] != (economy.Bucket{}) {
						t.Errorf("repair health/buckets=%d/%+v, want health=%d requested=1 accepted=%v and no metal", patient.Health, bucket, wantHealth, wantAccepted)
					}
					if n.Deadline != 101 || n.DynamicGate != gateDeadline|pendTargetRemoved {
						t.Errorf("repair retry=%d/%#x", n.Deadline, n.DynamicGate)
					}
					if row.air && owner.RevealDeadline != 0 {
						t.Error("air repair gained a nanolathe stamp")
					}
					if rng.Global.Sim.Draws() != beforeSim || rng.Global.Crt.Draws() != beforeCRT {
						t.Error("executor/query added random draws before refused segment storage")
					}
					patient.Health = patient.Def.MaxDamage
					attempts, sprays = 0, 0
					if code := row.handler(owner, n, 0, 101); code != 1 || attempts != 0 || sprays != 0 {
						t.Errorf("full-health repair attempted work or spray: code=%d attempts=%d sprays=%d", code, attempts, sprays)
					}
				})
			}
		}
	}
}
