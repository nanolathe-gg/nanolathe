//go:build pathbench && retail

package session

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func init() {
	pbRegister(
		pbCase{"lifecycle/stop_reverse", "order_change", "Stop during approach, then reverse the group", []int{8, 16}, 700, pbStopReverse},
		pbCase{"lifecycle/rapid_queue", "order_change", "Rapid replacement and two queued waypoints", []int{1, 8}, 700, pbRapidQueue},
		pbCase{"lifecycle/patrol", "order_change", "Crossing patrol orders in open space", []int{8, 16}, 800, pbPatrolCross},
		pbCase{"lifecycle/moving_targets", "target_order", "Guard, attack and repair toward moving unit targets", []int{1}, 700, pbMovingTargets},
		pbCase{"lifecycle/builder_work", "base", "Commander approaches a mobile build site while other units pass", []int{8}, 1200, pbBuilderWork},
		pbCase{"lifecycle/factory_rally", "base", "Factory queue and rally amid crossing traffic", []int{8}, 1600, pbFactoryRally},
		pbCase{"lifecycle/death_capture_transport", "removal", "Death, ownership transfer and cargo attach during route search", []int{1}, 600, pbDeathCaptureTransport},
	)
}

func pbLifecycleScene(t *testing.T, rules string) *pbScene {
	t.Helper()
	return pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
}
func pbQueueHuman(t *testing.T, sc *pbScene, c HumanCommand, label string) {
	t.Helper()
	if err := sc.S.EnqueueHumanCommand(c); err != nil {
		t.Fatal(err)
	}
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("tick=%d %s", sc.S.Clock.GlobalTick+1, label))
}
func pbSetReportingGoal(sc *pbScene, a *pbActor, x, z numeric.Fixed, radius int32) {
	a.Goal = [2]numeric.Fixed{x, z}
	a.GoalActive = true
	a.GoalTick = int(sc.S.Clock.GlobalTick) + 1
	a.Radius = radius
	a.CaptureGoal = false
}
func pbTargetOrder(t *testing.T, sc *pbScene, actor *pbActor, code int, target *pbActor, label string) {
	t.Helper()
	u := sc.S.Units.Unit(target.Handle)
	if u == nil {
		t.Fatalf("target %d missing", target.Handle)
	}
	pbQueueHuman(t, sc, HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{actor.Handle}, Code: code, Target: target.Handle, Position: orders.ResolvePos{X: u.X, Y: u.Y, Z: u.Z}}}, fmt.Sprintf("%s actor=%d target=%d code=%d", label, actor.Handle, target.Handle, code))
	pbSetReportingGoal(sc, actor, u.X, u.Z, 32)
}

func pbStopReverse(t *testing.T, rules string, size int) *pbScene {
	sc := pbLifecycleScene(t, rules)
	actors := pbGroundGrid(t, sc, "armflea", "reversing", 0, size, 12, 40, 8, 2)
	pbMove(t, sc, actors, 105, 48, false, false)
	sc.Events = append(sc.Events,
		pbEvent{Tick: 45, Label: "stop all commanded actors at tick 45", Apply: func(t *testing.T, s *pbScene) {
			h := make([]pool.Handle, len(actors))
			for i, a := range actors {
				h[i] = a.Handle
				a.GoalActive = false
				a.GoalTick = 45
			}
			pbQueueHuman(t, s, HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: h}}, fmt.Sprintf("stop handles=%v", h))
		}},
		pbEvent{Tick: 50, Label: "reverse all actors to click (10,48) at tick 50", Apply: func(t *testing.T, s *pbScene) { pbMove(t, s, actors, 10, 48, false, false) }},
	)
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("%d fleas from grid (12,40) stride2 cols8, initial click (105,48)", size))
	return sc
}

func pbRapidQueue(t *testing.T, rules string, size int) *pbScene {
	sc := pbLifecycleScene(t, rules)
	actors := pbGroundGrid(t, sc, "armflea", "rapid", 0, size, 12, 28, 8, 2)
	pbMove(t, sc, actors, 100, 28, false, false)
	for _, event := range []struct {
		tick   int
		x, z   int32
		queued bool
	}{{2, 100, 48, false}, {3, 105, 68, false}, {4, 80, 75, true}, {5, 20, 75, true}} {
		e := event
		sc.Events = append(sc.Events, pbEvent{Tick: e.tick, Label: fmt.Sprintf("tick %d move click (%d,%d), queued=%t", e.tick, e.x, e.z, e.queued), Apply: func(t *testing.T, s *pbScene) { pbMove(t, s, actors, e.x, e.z, false, e.queued) }})
	}
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("%d fleas start grid (12,28) stride2 cols8; initial click (100,28); replacement ticks 2,3 then queued ticks 4,5", size))
	sc.Notes = append(sc.Notes, "Queued reporting goals are future waypoints; proximity before the queued record becomes active is observational only.")
	return sc
}

func pbPatrolCross(t *testing.T, rules string, size int) *pbScene {
	sc := pbLifecycleScene(t, rules)
	a := pbGroundGrid(t, sc, "armflea", "east_patrol", 0, size/2, 12, 38, 8, 2)
	b := pbGroundGrid(t, sc, "armflea", "west_patrol", 0, size-size/2, 92, 54, 8, 2)
	issue := func(actors []*pbActor, x, z int32) {
		h := make([]pool.Handle, len(actors))
		for i, v := range actors {
			h[i] = v.Handle
			pbSetReportingGoal(sc, v, pbCell(x), pbCell(z), 32)
		}
		pbQueueHuman(t, sc, HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: h, Code: 9, Position: orders.ResolvePos{X: pbCell(x), Y: sc.S.World.HeightAt(pbCell(x), pbCell(z)), Z: pbCell(z)}}}, fmt.Sprintf("patrol handles=%v click=(%d,%d)", h, x, z))
	}
	issue(a, 105, 54)
	issue(b, 12, 38)
	sc.Regions = append(sc.Regions, pbRegion{"cross", 50, 40, 70, 60})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("%d patrol actors split %d/%d; starts (12,38)/(92,54), stride2 cols8, clicks (105,54)/(12,38)", size, len(a), len(b)))
	sc.Notes = append(sc.Notes, "Patrol is ongoing; a radius visit is not patrol completion.")
	return sc
}

func pbMovingTargets(t *testing.T, rules string, size int) *pbScene {
	sc := pbLifecycleScene(t, rules)
	sc.S.Econ.Players[0].Allies[1] = false
	sc.S.Econ.Players[1].Allies[0] = false
	guard := pbAdd(t, sc, "armflea", "guard", 0, 12, 20)
	attack := pbAdd(t, sc, "armflash", "attack", 0, 12, 40)
	repair := pbAdd(t, sc, "armcom", "repair", 0, 12, 66)
	friendly := pbAdd(t, sc, "armflea", "moving_friendly", 0, 80, 20)
	hostile := pbAdd(t, sc, "corak", "moving_hostile", 1, 80, 40)
	damaged := pbAdd(t, sc, "armflea", "moving_damaged", 0, 80, 66)
	if u := sc.S.Units.Unit(damaged.Handle); u != nil {
		u.Health /= 2
	}
	pbTargetOrder(t, sc, guard, 7, friendly, "guard")
	pbTargetOrder(t, sc, attack, 3, hostile, "attack")
	pbTargetOrder(t, sc, repair, 8, damaged, "repair")
	for _, item := range []struct {
		a    *pbActor
		x, z int32
	}{{friendly, 100, 20}, {hostile, 100, 40}, {damaged, 100, 66}} {
		v := item
		sc.Events = append(sc.Events, pbEvent{Tick: 30, Label: fmt.Sprintf("target %d moves to (%d,%d) at tick 30", v.a.Handle, v.x, v.z), Apply: func(t *testing.T, s *pbScene) {
			if v.a == hostile {
				pbScriptedOwnerMove(t, s, []*pbActor{v.a}, v.x, v.z)
			} else {
				pbMove(t, s, []*pbActor{v.a}, v.x, v.z, false, false)
			}
		}})
	}
	sc.Inputs = append(sc.Inputs, "owners 0/1 hostile; guard flea, attacking flash, repairing commander target initial x=80 at z=20/40/66; target move tick=30 to x=100; damaged flea begins at half health")
	sc.Notes = append(sc.Notes, "Target orders use a moving unit handle. Reporting radius is around the initial target location, not a claim of attack, guard or repair completion; the runner must report queue state separately.")
	return sc
}

func pbFund(sc *pbScene) {
	p := &sc.S.Econ.Players[0]
	p.InstallStorageBonus(100000, 100000)
	p.Capacity[economy.Metal], p.Capacity[economy.Energy] = 100000, 100000
	p.Stock[economy.Metal], p.Stock[economy.Energy] = 100000, 100000
}
func pbBuilderWork(t *testing.T, rules string, size int) *pbScene {
	sc := pbLifecycleScene(t, rules)
	builder := pbAdd(t, sc, "armcom", "builder", 0, 12, 45)
	cross := pbGroundGrid(t, sc, "armflea", "crossing", 0, size, 18, 70, 8, 2)
	pbMove(t, sc, cross, 105, 42, false, false)
	pbFund(sc)
	x, z := pbCell(80), pbCell(44)
	pbQueueHuman(t, sc, HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: builder.Handle, Product: "armsolar", WX: x, WZ: z, WY: sc.S.World.HeightAt(x, z)}}, "mobile build armcom -> armsolar site=(80,44)")
	pbSetReportingGoal(sc, builder, x, z, 96)
	sc.Regions = append(sc.Regions, pbRegion{"work_site", 75, 39, 88, 52})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("armcom start (12,45), armsolar site=(80,44), %d crossing fleas start (18,70) stride2 cols8 click=(105,42); player0 stock/capacity=100000 each", size))
	sc.Notes = append(sc.Notes, "Builder proximity to the site is not construction completion; inspect build/order state separately.")
	return sc
}
func pbFactoryRally(t *testing.T, rules string, size int) *pbScene {
	sc := pbLifecycleScene(t, rules)
	factory := pbAdd(t, sc, "armlab", "factory", 0, 70, 44)
	cross := pbGroundGrid(t, sc, "armflea", "crossing", 0, size, 12, 28, 8, 2)
	pbMove(t, sc, cross, 110, 55, false, false)
	pbFund(sc)
	pbMove(t, sc, []*pbActor{factory}, 105, 48, false, false)
	// The factory's QMove is a rally marker, not a travelling actor goal.
	factory.GoalActive = false
	pbQueueHuman(t, sc, HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: factory.Handle, Product: "armflea", Count: 3}}, "factory armlab queues three armflea products")
	sc.Regions = append(sc.Regions, pbRegion{"factory_exit", 65, 39, 83, 57}, pbRegion{"rally", 100, 43, 112, 55})
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("armlab at (70,44), rally click=(105,48), three armflea products, %d crossing fleas from (12,28) stride2 cols8 click=(110,55); player0 stock/capacity=100000 each", size))
	sc.Notes = append(sc.Notes, "Factory products are spawned by production and are not predeclared actors; inspect product census and queue state. Factory rally marker itself has no movement goal.")
	return sc
}

func pbDeathCaptureTransport(t *testing.T, rules string, size int) *pbScene {
	sc := pbLifecycleScene(t, rules)
	death := pbAdd(t, sc, "armflea", "dies", 0, 12, 18)
	capture := pbAdd(t, sc, "armflea", "captured", 0, 12, 38)
	cargo := pbAdd(t, sc, "armflea", "cargo", 0, 12, 60)
	carrier := pbAdd(t, sc, "armatlas", "carrier", 0, 80, 70)
	for _, a := range []*pbActor{death, capture, cargo} {
		pbMove(t, sc, []*pbActor{a}, 108, a.Start[1], false, false)
	}
	sc.Events = append(sc.Events,
		pbEvent{Tick: 2, Label: "mark death of moving flea at tick 2", Apply: func(t *testing.T, s *pbScene) {
			s.S.Units.Destroy(death.Handle, units.DeathKilled)
			death.ExpectedRemoval = true
			death.GoalActive = false
		}},
		pbEvent{Tick: 3, Label: "transfer moving flea from owner 0 to 1 at tick 3", Apply: func(t *testing.T, s *pbScene) {
			u := s.S.Units.Unit(capture.Handle)
			if u == nil {
				t.Fatal("capture victim disappeared")
			}
			repl, ok := s.S.Build.TransferOwnership(u, 1)
			if !ok || repl == nil {
				t.Fatal("ownership transfer refused")
			}
			capture.ExpectedRemoval = true
			capture.GoalActive = false
			s.S.Movement.EnsureUnit(repl)
			s.Actors = append(s.Actors, &pbActor{Handle: repl.Handle, Key: "armflea", Cohort: "replacement", Start: [2]int32{12, 38}, Radius: 16})
		}},
		pbEvent{Tick: 4, Label: "attach moving flea to armatlas root at tick 4", Apply: func(t *testing.T, s *pbScene) {
			if !movement.AttachCargo(s.S.Units, carrier.Handle, cargo.Handle, -1) {
				t.Fatal("cargo attach refused")
			}
			cargo.GoalActive = false
		}},
	)
	sc.Inputs = append(sc.Inputs, "three armflea at (12,18),(12,38),(12,60) move to x=108; armatlas at (80,70); death tick2, capture owner0->1 tick3, cargo attach tick4")
	sc.Notes = append(sc.Notes, "Cargo attachment calls the production commit helper directly to isolate the lifecycle boundary; it bypasses approach and pickup admission. Cargo remains alive without an active goal. Death and capture remove the old handles.")
	return sc
}

func TestPathBenchLifecycleSmoke(t *testing.T) {
	if os.Getenv("NANOLATHE_PATH_BENCH_SMOKE") == "" {
		t.Skip("opt-in lifecycle smoke")
	}
	for _, c := range pbCases {
		if len(c.ID) < len("lifecycle/") || c.ID[:len("lifecycle/")] != "lifecycle/" {
			continue
		}
		c := c
		t.Run(c.ID, func(t *testing.T) {
			sizes := c.Sizes[:1]
			if os.Getenv("NANOLATHE_PATH_BENCH_SMOKE_FULL") != "" {
				sizes = c.Sizes
			}
			for _, size := range sizes {
				t.Run(fmt.Sprintf("n%d", size), func(t *testing.T) {
					for _, rules := range []string{"modern", "strict-3.1"} {
						t.Run(rules, func(t *testing.T) {
							sc := c.Build(t, rules, size)
							publishVisibilityForAll(sc.S)
							cycles := 40
							if c.ID == "lifecycle/stop_reverse" && os.Getenv("NANOLATHE_PATH_BENCH_SMOKE_FULL") != "" {
								cycles = 52 // exercise stop and reverse events
							}
							for i := 1; i <= cycles; i++ {
								for _, e := range sc.Events {
									if e.Tick == i {
										e.Apply(t, sc)
									}
								}
								stamp := sc.S.Clock.BeginSubTick()
								sc.S.stepAuthoritativePhases(stamp)
								if i == 1 {
									pbAssertStartOrders(t, sc, c.ID)
								}
							}
						})
					}
				})
			}
		})
	}
}
