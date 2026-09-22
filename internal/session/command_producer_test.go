package session

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Preserve-queue descriptors still enter the producer boundary: leading auto
// removal and caption admission apply in both modes [04 R-ORD-01 §13]. Modern
// additionally clears danger without replacing the assigned mission.
func TestHumanMovementStanceAndCloakUseProducerBoundary(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		for _, tc := range []struct {
			name    string
			command HumanCommand
		}{
			{"Standing_MoveOrder", HumanCommand{Kind: HumanStance, Stance: HumanStanceCommand{Value: 0}}},
			{"Cloak_On", HumanCommand{Kind: HumanCloak, Cloak: HumanCloakCommand{Cloak: true}}},
			{"Cloak_Off", HumanCommand{Kind: HumanCloak}},
		} {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				s, u, enemy := modernDangerSession(t, mode)
				u.Flags |= 0x10 // selection [04 R-STANCE-01 §5]
				u.Def.MobileStandOrders = true
				u.Def.CloakCost = 1
				u.SetCloaked(tc.name == "Cloak_Off")
				q := orders.QueueOfUnit(u)
				mission := &orders.Node{ID: orders.Lookup("Move_Ground"), Owner: u.Handle, Phase: 1, DynamicGate: 0x10, Deadline: -1, Flags: orders.FlagActive, GoalX: u.X + 100<<16, GoalZ: u.Z}
				q.SetPrimary([]*orders.Node{{ID: orders.Lookup("Standby"), Owner: u.Handle, Flags: orders.FlagAutoOp}, mission})
				before := *mission
				orders.ObserveDanger(u, enemy, 10)
				orders.ObserveImpact(u, 16384, 10)
				if q.Binding().Rules.AllowAutomaticRepair(u, 11) != (mode == gameplay.Strict31) {
					t.Fatal("fixture did not establish mode-specific danger memory")
				}
				sim, crt, stock := *s.SimRNG(), *s.CrtRNG(), s.Econ.Players[u.Owner].Stock
				s.applyHumanCommand(tc.command, 11)
				primary := q.Primary()
				if len(primary) != 2 || primary[1] != mission || !reflect.DeepEqual(*mission, before) {
					t.Fatal("command did not remove leading auto alone while preserving the mission")
				}
				if head := q.Head(); head.ID != orders.Lookup(tc.name) || !head.CaptionPending || head.Flags&(orders.FlagAutoOp|orders.FlagActive) != 0 {
					t.Fatalf("command missed producer head-insertion bookkeeping: %+v", head)
				}
				if !q.Binding().Rules.AllowAutomaticRepair(u, 11) {
					t.Fatal("new command retained danger memory")
				}
				q.Pump(u, 11)
				if q.Head() != mission || q.LenPrimary() != 1 || mission.GoalX != before.GoalX || mission.GoalZ != before.GoalZ {
					t.Fatal("command completion lost the assigned destination")
				}
				if tc.command.Kind == HumanStance {
					if u.Flags>>units.StandingMoveShift&units.StandingFieldMask != 0 {
						t.Fatal("movement stance was not written")
					}
				} else if u.IsCloaked != tc.command.Cloak.Cloak {
					t.Fatal("cloak request was not written")
				}
				if *s.SimRNG() != sim || *s.CrtRNG() != crt || s.Econ.Players[u.Owner].Stock != stock {
					t.Fatal("command consumed RNG or resources")
				}
			})
		}
	}
}

// The fire-stance producer must retain Hold Fire's narrower preservation rule
// while movement stance and cloak supersede a Modern automatic withdrawal.
func TestHumanCommandDangerWithdrawalBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		command  HumanCommand
		preserve bool
	}{
		{"movement stance", HumanCommand{Kind: HumanStance, Stance: HumanStanceCommand{Value: 0}}, false},
		{"cloak", HumanCommand{Kind: HumanCloak, Cloak: HumanCloakCommand{Cloak: true}}, false},
		{"hold fire", HumanCommand{Kind: HumanStance, Stance: HumanStanceCommand{Fire: true, Value: 0}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, u, _ := modernDangerSession(t, gameplay.Modern)
			u.Flags |= 0x10
			u.Def.MobileStandOrders, u.Def.FireStandOrders = true, true
			u.Def.CloakCost = 1
			q := orders.QueueOfUnit(u)
			mission := &orders.Node{ID: orders.Lookup("Patrol"), Owner: u.Handle, Phase: 3, GoalX: u.X + 100<<16, GoalZ: u.Z}
			q.SetPrimary([]*orders.Node{mission})
			orders.ObserveImpact(u, 16384, 10)
			orders.StepDangerResponse(u, 10)
			response := q.Head()
			if response == mission || response.ID != orders.Lookup("Move_Ground") {
				t.Fatal("fixture did not stage a withdrawal")
			}
			sim, crt, stock := *s.SimRNG(), *s.CrtRNG(), s.Econ.Players[u.Owner].Stock
			s.applyHumanCommand(tc.command, 11)
			primary := q.Primary()
			if tc.preserve {
				if len(primary) != 3 || primary[1] != response || primary[2] != mission {
					t.Fatal("Hold Fire discarded withdrawal or assignment")
				}
				// Hold the follower response so this pump isolates the stance write.
				response.DynamicGate, response.Deadline = 0x10, -1
				q.Pump(u, 11)
				if q.Head() != response || u.Flags>>units.StandingFireShift&units.StandingFieldMask != 0 {
					t.Fatal("Hold Fire write did not preserve withdrawal")
				}
			} else if len(primary) != 2 || primary[1] != mission || mission.Phase != 0 {
				t.Fatal("command did not end withdrawal and restart the preserved assignment")
			}
			if q.Binding().Rules.AllowAutomaticRepair(u, 11) == tc.preserve {
				t.Fatal("command danger-memory precedence is wrong")
			}
			if *s.SimRNG() != sim || *s.CrtRNG() != crt || s.Econ.Players[u.Owner].Stock != stock {
				t.Fatal("command consumed RNG or resources")
			}
		})
	}
}
