//go:build pathbench && retail

package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func init() {
	pbRegister(
		pbCase{"transport-pickup-unload", "transport", "Retail air transport approaches, picks up, then unloads through ordinary commands", []int{1}, 2400, pbTransport(false)},
		pbCase{"transport-crowded-unload", "transport", "Same transport commands with idle ground units at the unload site", []int{1}, 2400, pbTransport(true)},
		pbCase{"naval-shipyard-rally", "naval_base", "Retail shipyard builds three patrol boats amid crossing traffic", []int{8}, 2400, pbShipyard},
	)
}
func pbTransport(crowded bool) func(*testing.T, string, int) *pbScene {
	return func(t *testing.T, rules string, n int) *pbScene {
		sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
		carrier := pbAdd(t, sc, "armatlas", "carrier", 0, 12, 32)
		cargo := pbAdd(t, sc, "armflea", "cargo", 0, 40, 32)
		if admitted := sc.S.Movement.CanTransport(carrier.Handle, cargo.Handle, sc.S.Units); !admitted.Allowed {
			t.Fatalf("retail fixture cargo admission: %s", admitted.Reason)
		}
		if crowded {
			for z := int32(44); z <= 52; z += 2 {
				for x := int32(92); x <= 100; x += 2 {
					pbAdd(t, sc, "armflea", "unload_blocker", 0, x, z)
				}
			}
		}
		pos := orders.ResolvePos{X: pbCell(40), Y: sc.S.World.HeightAt(pbCell(40), pbCell(32)), Z: pbCell(32)}
		if err := sc.S.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{carrier.Handle}, Code: 6, Target: cargo.Handle, Position: pos}}); err != nil {
			t.Fatal(err)
		}
		carrier.GoalActive = true
		carrier.GoalTick = 1
		carrier.Goal = [2]numeric.Fixed{pos.X, pos.Z}
		carrier.Radius = 32
		sc.Events = []pbEvent{{Tick: 600, Label: "issue ordinary replacement unload command at world-center cell (96,48) at tick 600", Apply: func(t *testing.T, s *pbScene) {
			goal := orders.ResolvePos{X: pbCell(96), Y: s.S.World.HeightAt(pbCell(96), pbCell(48)), Z: pbCell(48)}
			if err := s.S.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{carrier.Handle}, Code: 5, Position: goal, Queued: false}}); err != nil {
				t.Fatal(err)
			}
			for _, a := range []*pbActor{carrier, cargo} {
				a.GoalActive = true
				a.GoalTick = 600
				a.Goal = [2]numeric.Fixed{goal.X, goal.Z}
				a.Radius = 32
			}
		}}}
		sc.Regions = []pbRegion{{"pickup", 36, 28, 46, 38}, {"unload", 88, 40, 106, 58}}
		sc.Inputs = append(sc.Inputs, fmt.Sprintf("armatlas (12,32) pickup armflea (40,32), code6 tick1; replacement code5 (96,48) tick600; crowded=%t, blockers grid x92..100 z44..52 stride2", crowded))
		sc.Notes = append(sc.Notes, "Cargo proximity while carried is not unload success; carrier linkage and move mode are reported separately. Pickup and unload use ordinary command resolution and retail COB callbacks.")
		return sc
	}
}
func pbShipyard(t *testing.T, rules string, n int) *pbScene {
	sc := pbNew(t, rules, pbTerrain(t, 144, 96, 20, 80))
	yard := pbAdd(t, sc, "armsy", "shipyard", 0, 70, 48)
	boats := make([]*pbActor, 0, n)
	for i := 0; i < n; i++ {
		boats = append(boats, pbAdd(t, sc, "armpt", "crossing", 0, 10+int32(i%4)*7, 30+int32(i/4)*8))
	}
	pbMove(t, sc, boats, 120, 52, false, false)
	pbFund(sc)
	pbMove(t, sc, []*pbActor{yard}, 114, 64, false, false)
	yard.GoalActive = false
	if err := sc.S.EnqueueHumanCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: yard.Handle, Product: "armpt", Count: 3}}); err != nil {
		t.Fatal(err)
	}
	sc.Regions = []pbRegion{{"shipyard_exit", 60, 38, 83, 60}, {"rally", 107, 57, 125, 74}}
	sc.Inputs = append(sc.Inputs, "deep water height20 sea80; armsy (70,48), three armpt products, rally (114,64); 8 crossing armpt grid (10+7*(i%4),30+8*(i/4)), click(120,52); stocks/capacity100000")
	sc.Notes = append(sc.Notes, "Shipyard products contribute to the whole-unit census and trajectory; their production is measured, not assumed.")
	return sc
}
