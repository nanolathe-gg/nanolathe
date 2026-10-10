package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy, user-approved 2026-10-09: acquisition follows
// current sight, and every borrowed patrol job retains a saved-position return.
func TestModernPatrolCurrentSightAcquisition(t *testing.T) {
	for _, air := range []bool{false, true} {
		for stance := uint32(0); stance < 3; stance++ {
			for _, outside := range []bool{false, true} {
				t.Run(fmt.Sprintf("air%v/stance%d/outside%v", air, stance, outside), func(t *testing.T) {
					f := newModernWorkFixture(air)
					f.u.X, f.u.Z = numeric.FixedFromInt(100), numeric.FixedFromInt(160)
					f.u.Def.SightDistance = 280
					f.u.Flags = f.u.Flags&^(stanceFieldMask<<stanceMoveShift) | stance<<stanceMoveShift
					target := modernPatient(2, 100, 440, true)
					if outside {
						target.Z++
					}
					f.units = []*units.Unit{target}
					before := f.sim
					f.visit(100)
					selected := f.q.Head() != f.patrol
					if selected == outside {
						t.Fatalf("selected=%v outside=%v: current sight-circle boundary", selected, outside)
					}
					if selected && f.q.Head().Target != target.Handle {
						t.Fatal("selected wrong frame")
					}
					if f.sim != before {
						t.Fatal("Modern selection drew RNG")
					}
				})
			}
		}
	}
}

func TestModernPatrolBorrowsWithSavedReturn(t *testing.T) {
	for _, air := range []bool{false, true} {
		for stance := uint32(0); stance < 3; stance++ {
			t.Run(fmt.Sprintf("air%v/stance%d", air, stance), func(t *testing.T) {
				f := newModernWorkFixture(air)
				f.u.X = numeric.FixedFromInt(100)
				f.u.Flags = f.u.Flags&^(stanceFieldMask<<stanceMoveShift) | stance<<stanceMoveShift
				f.units = []*units.Unit{modernPatient(2, 132, 0, true)}
				f.visit(100)
				rows := f.q.primary
				move := rowMoveGround
				if air {
					move = Lookup("VTOL_Move")
				}
				if len(rows) != 5 || rows[1].ID != move {
					t.Fatalf("borrowed queue has %d rows, want work/return/original three rows", len(rows))
				}
				back := rows[1]
				if back.GoalX != f.u.X || back.GoalY != f.u.Y || back.GoalZ != f.u.Z || rows[2] != f.patrol || rows[3] != f.next || rows[4] != f.successor {
					t.Fatal("borrow lost acquisition position, patrol identity or successor")
				}
			})
		}
	}
}

func TestModernPatrolFailedJobKeepsSavedReturn(t *testing.T) {
	for _, air := range []bool{false, true} {
		t.Run(fmt.Sprintf("air%v", air), func(t *testing.T) {
			f := newModernWorkFixture(air)
			f.units = []*units.Unit{modernPatient(2, 32, 0, true)}
			workID := rowHelpBuild
			moveID := rowMoveGround
			if air {
				workID, moveID = rowVTOLHelpBuild, Lookup("VTOL_Move")
			}
			attempts := 0
			f.q.SetOwnedHandler(workID, func(*units.Unit, *Node, uint32, uint32) (Code, bool) { attempts++; return 8, true })
			f.q.Pump(f.u, 100)
			if attempts != 1 || f.q.Head() == nil || f.q.Head().ID != moveID || len(f.q.primary) != 4 {
				t.Fatalf("failed job attempts=%d remaining=%d head=%v: want retained return then route", attempts, len(f.q.primary), f.q.Head())
			}
		})
	}
}

func TestModernPatrolPursuitUsesSavedSightCircle(t *testing.T) {
	for _, air := range []bool{false, true} {
		t.Run(fmt.Sprintf("air%v", air), func(t *testing.T) {
			f := newModernWorkFixture(air)
			f.u.X, f.u.Z = numeric.FixedFromInt(100), numeric.FixedFromInt(160)
			f.u.Def.SightDistance = 280
			target := modernPatient(2, 132, 160, true)
			f.units = []*units.Unit{target}
			f.visit(100)
			work := f.q.Head()
			f.u.X = numeric.FixedFromInt(1000)
			target.X, target.Z = numeric.FixedFromInt(100), numeric.FixedFromInt(440)
			if !f.q.binding.Rules.AutomaticWorkValid(f.u, work) {
				t.Fatal("inclusive saved sight circle refused")
			}
			target.Z++
			if f.q.binding.Rules.AutomaticWorkValid(f.u, work) {
				t.Fatal("pursuit escaped the saved sight circle")
			}
			f.q.Pump(f.u, 101)
			if !f.q.PatrolWorkPaused() || !f.q.Head().IsPatrolReturn() {
				t.Fatal("invalidated pursuit did not retain return and pause borrowing")
			}
		})
	}
}

func TestModernPatrolFailureRecoveryNeedsWaypointArrival(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, outcome := range []uint32{gateArrived, gateNoRoute, 0x80} {
			t.Run(fmt.Sprintf("air%v/outcome%x", air, outcome), func(t *testing.T) {
				f := newModernWorkFixture(air)
				target := modernPatient(2, 32, 0, true)
				f.units = []*units.Unit{target}
				id := rowHelpBuild
				if air {
					id = rowVTOLHelpBuild
				}
				f.q.SetOwnedHandler(id, func(*units.Unit, *Node, uint32, uint32) (Code, bool) { return 8, true })
				f.q.Pump(f.u, 100)
				if !f.q.PatrolWorkPaused() {
					t.Fatal("failed work did not pause route borrowing")
				}
				back := f.q.Head()
				move := back.ID
				f.q.SetOwnedHandler(move, func(_ *units.Unit, n *Node, s uint32, _ uint32) (Code, bool) {
					if s == 0 {
						n.DynamicGate = gateMoveOutcomes
						return 2, true
					}
					// Air reports code 5 for every movement outcome; ground reports 9 on failure.
					if !air && s&gateArrived == 0 {
						return 9, true
					}
					return 5, true
				})
				back.DynamicGate = gateMoveOutcomes
				back.Satisfied = outcome
				f.q.Pump(f.u, 101)
				if f.q.Head() != f.patrol || !f.q.PatrolWorkPaused() || len(f.q.primary) != 3 {
					t.Fatal("return outcome discarded route or cleared recovery pause")
				}
				scans := f.scans
				f.visit(1000)
				if f.scans != scans || f.q.Head() != f.patrol {
					t.Fatal("maintenance borrowed again before a patrol waypoint")
				}
				r := f.q.binding.Rules
				for _, s := range []uint32{0, gateNoRoute, 0x80} {
					r.AutomaticWorkResult(f.u, f.patrol, s, 6, 1001)
					if !f.q.PatrolWorkPaused() {
						t.Fatal("rotation/failure falsely counted as arrival")
					}
				}
				r.AutomaticWorkResult(f.u, f.patrol, gateArrived, 6, 1002)
				if f.q.PatrolWorkPaused() {
					t.Fatal("genuine patrol arrival did not restore borrowing")
				}
			})
		}
	}
}

func TestModernPatrolSemanticRepairFailure(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, failure := range []string{"dead", "airborne", "leash", "healthy"} {
			t.Run(fmt.Sprintf("air%v/%s", air, failure), func(t *testing.T) {
				f := newModernWorkFixture(air)
				target := modernPatient(2, 32, 0, false)
				f.units = []*units.Unit{target}
				f.visit(100)
				work := f.q.Head()
				switch failure {
				case "dead":
					target.Alive = false
				case "airborne":
					target.Move.ModeMirror = 2
				case "leash":
					target.Health = target.Def.MaxDamage
					f.u.X = numeric.FixedFromInt(1)
					work.Param3 = 1
				case "healthy":
					target.Health = target.Def.MaxDamage
				}
				// Both ordinary repair executors can report code 5 for unsuccessful endings.
				f.q.binding.Rules.AutomaticWorkResult(f.u, work, 0, 5, 101)
				if got := f.q.PatrolWorkPaused(); got != (failure != "healthy") {
					t.Fatalf("pause=%v for semantic repair outcome %s", got, failure)
				}
			})
		}
	}
}

func TestModernPatrolRecoveryReplacementAndStrictBypass(t *testing.T) {
	f := newModernWorkFixture(false)
	f.q.patrolWorkPaused = true
	f.q.Push(rowMoveGround, Node{QueuedIssue: true})
	if !f.q.PatrolWorkPaused() {
		t.Fatal("queued successor reset failure pause")
	}
	f.q.Push(Lookup("Standing_MoveOrder"), Node{})
	if !f.q.PatrolWorkPaused() {
		t.Fatal("stance issue reset assignment pause")
	}
	f.q.PurgeUnprotected()
	f.q.Push(rowRepairPatrol, Node{})
	if f.q.PatrolWorkPaused() {
		t.Fatal("replacement patrol retained old assignment pause")
	}
	for _, r := range []Rules{StrictRules{}, &CommunityRules{}} {
		f.q.patrolWorkPaused = true
		state, resources := f.sim, f.resources
		if code := r.AutomaticWorkResult(f.u, f.patrol, gateArrived, 6, 200); code != 6 || !f.q.PatrolWorkPaused() || f.sim != state || f.resources != resources {
			t.Fatalf("%T changed bypass state", r)
		}
	}
}

func TestModernPatrolSuccessfulWorkReturnsBeforeBorrowing(t *testing.T) {
	for _, air := range []bool{false, true} {
		t.Run(fmt.Sprintf("air%v", air), func(t *testing.T) {
			f := newModernWorkFixture(air)
			target := modernPatient(2, 32, 0, true)
			f.units = []*units.Unit{target}
			id := rowHelpBuild
			if air {
				id = rowVTOLHelpBuild
			}
			f.q.SetOwnedHandler(id, func(*units.Unit, *Node, uint32, uint32) (Code, bool) { target.Remaining = 0; return 5, true })
			f.q.Pump(f.u, 100)
			back := f.q.Head()
			if !back.IsPatrolReturn() || f.q.PatrolWorkPaused() {
				t.Fatal("successful work did not enter unpaused saved return")
			}
			scans := f.scans
			f.q.Pump(f.u, 200)
			if f.q.Head() != back || f.scans != scans {
				t.Fatal("borrowed from the side-job location before return arrival")
			}
		})
	}
}

func TestModernPatrolDangerInterruptionRetainsReturnAndRecovery(t *testing.T) {
	f := newModernWorkFixture(false)
	f.units = []*units.Unit{modernPatient(2, 32, 0, false)}
	f.visit(100)
	back := f.q.primary[1]
	f.q.suspendDangerAssignment(f.u)
	if f.q.Head() != back || f.q.danger.resume != back || !f.q.PatrolWorkPaused() || f.q.primary[1] != f.patrol {
		t.Fatal("danger discarded saved return or failure recovery")
	}
	f.q.binding.Rules = StrictRules{}
	f.q.SetBinding(f.q.binding)
	f.q.binding.Rules = &ModernRules{}
	f.q.SetBinding(f.q.binding)
	if f.q.Head() != back || !f.q.PatrolWorkPaused() {
		t.Fatal("rebinding or mode switch discarded staged assignment state")
	}
	// A danger response must keep construction already in progress protected.
	f = newModernWorkFixture(false)
	f.units = []*units.Unit{modernPatient(2, 32, 0, true)}
	f.visit(100)
	if !protectedDangerWork(f.q) {
		t.Fatal("borrowed construction lost danger protection")
	}
}

func TestModernPatrolFeatureCurrentSightBoundary(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, outside := range []bool{false, true} {
			t.Run(fmt.Sprintf("air%v/outside%v", air, outside), func(t *testing.T) {
				f := newModernWorkFixture(air)
				f.u.Z = numeric.FixedFromInt(160)
				f.u.Def.SightDistance = 280
				f.resources.Stock[0] = 0
				feature := modernFeature(1, 0, 440, 10, 0)
				if outside {
					feature.Z++
				}
				f.features = []FeatureView{feature}
				before := f.sim
				f.visit(100)
				if selected := f.q.Head() != f.patrol; selected == outside || f.sim != before {
					t.Fatal("feature acquisition violated sight boundary or selection RNG")
				}
			})
		}
	}
}

func TestModernPatrolFeatureCompletionUsesExecutorPhase(t *testing.T) {
	for _, air := range []bool{false, true} {
		t.Run(fmt.Sprintf("air%v", air), func(t *testing.T) {
			f := newModernWorkFixture(air)
			f.resources.Stock[0] = 0
			f.features = []FeatureView{modernFeature(1, 32, 0, 10, 0)}
			f.visit(100)
			work := f.q.Head()
			work.Phase = 5
			if air {
				work.Phase = 4
			}
			if code := f.q.binding.Rules.AutomaticWorkResult(f.u, work, 0, 5, 101); code != 5 || f.q.PatrolWorkPaused() {
				t.Fatal("completed feature payout was classified as failed work")
			}
		})
	}
}

func TestModernPatrolAirReturnTakeoffIsNotDestinationArrival(t *testing.T) {
	f := newModernWorkFixture(true)
	f.units = []*units.Unit{modernPatient(2, 32, 0, true)}
	f.visit(100)
	back := f.q.primary[1]
	back.Phase = 1
	r := f.q.binding.Rules
	r.AutomaticWorkResult(f.u, back, gateArrived, 1, 101)
	back.Phase = 2
	if code := r.AutomaticWorkResult(f.u, back, gateNoRoute, 5, 102); code != 8 || !f.q.PatrolWorkPaused() {
		t.Fatal("takeoff arrival hid a failed saved-position flight")
	}
}

func TestModernPatrolArrivalRetainsNormalPayloadRelease(t *testing.T) {
	for _, air := range []bool{false, true} {
		t.Run(fmt.Sprintf("air%v", air), func(t *testing.T) {
			f := newModernWorkFixture(air)
			f.units = []*units.Unit{modernPatient(2, 32, 0, true)}
			f.visit(100)
			back := f.q.primary[1]
			back.Phase = 1
			if air {
				back.Phase = 2
			}
			r := f.q.binding.Rules
			// The movement producer raises arrival before normal payload detach, so
			// both bits survive to the executor [04 R-PATH-01 §8][04 R-AIR-01 §1].
			if code := r.AutomaticWorkResult(f.u, back, gateArrived|0x80, 5, 101); code != 5 || !back.PatrolReturnArrived() || f.q.PatrolWorkPaused() {
				t.Fatal("normal arrived-and-detached return was classified as failure")
			}
			f.q.patrolWorkPaused = true
			r.AutomaticWorkResult(f.u, f.patrol, gateArrived|0x80, 6, 102)
			if f.q.PatrolWorkPaused() {
				t.Fatal("normal waypoint arrival did not clear recovery pause")
			}
		})
	}
}

func TestModernPatrolRetailSaveKeepsReturnButLosesReceipts(t *testing.T) {
	f := newModernWorkFixture(false)
	f.u.X, f.u.Y, f.u.Z = numeric.FixedFromInt(100), numeric.FixedFromInt(17), numeric.FixedFromInt(160)
	f.units = []*units.Unit{modernPatient(2, 132, 160, true)}
	f.visit(100)
	f.q.patrolWorkPaused = true
	ids := map[pool.Handle]uint16{f.u.Handle: 1, f.units[0].Handle: 2}
	images, err := RetailOrderImagesWithPayload(f.u, func(h pool.Handle) (uint16, bool) { id, ok := ids[h]; return id, ok }, func(pool.Handle) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]save.OrderRecord, len(images))
	for i, image := range images {
		records[i] = save.OrderRecord{ParentStableID: 1, Sequence: uint32(i), Main: image.Main, DescriptorName: image.DescriptorName, SubtypeCode: image.SubtypeCode, Subtype: image.Subtype}
	}
	if err := RetailRestoreOrdersAtTick(f.u, records, map[uint16]pool.Handle{1: f.u.Handle, 2: f.units[0].Handle}, f.q.binding, 101); err != nil {
		t.Fatal(err)
	}
	q := QueueOfUnit(f.u)
	back := q.primary[1]
	if back.ID != rowMoveGround || back.GoalX != f.u.X || back.GoalY != f.u.Y || back.GoalZ != f.u.Z || back.IsPatrolReturn() || q.PatrolWorkPaused() || q.Head().workAssignment != nil || q.Head().workReturn != nil || !q.binding.Rules.AutomaticWorkValid(f.u, q.Head()) {
		t.Fatal("retail save boundary changed ordinary return or inferred producer receipts")
	}
}
