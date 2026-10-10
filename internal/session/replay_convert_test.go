package session

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// replayEnv names the units a script's later steps act on; earlier steps
// find them in the recording.
type replayEnv struct {
	own, foe              pool.Handle
	scout, scout2, doomed pool.Handle
	// missing is a slot no unit has held: its capture carries serial 0.
	missing pool.Handle
	// tracked is the tracked move's local sequence, its receipt.
	tracked uint64
	// count is a unit count a step's preparation noted.
	count int
	// sequences are the local sequences of the step being run.
	sequences []uint64
}

// replayStep is one input batch and the pump that applies it.
type replayStep struct {
	name string
	// paused applies the batch at the paused-input boundary of the tick that
	// has not run (DESIGN_INTERFACE_HUD_INPUT §3.12); the pump follows.
	paused bool
	// ticks is the pump's size, one when zero.
	ticks int
	// prepare changes both battles identically before the batch. It is test
	// staging, not input.
	prepare  func(t *testing.T, s *Session)
	commands func(t *testing.T, s *Session) []HumanCommand
	// rejected is how many entries the playback refuses where the local
	// application changed nothing either.
	rejected int
	// check proves the step changed what it names, in both battles.
	check func(t *testing.T, s *Session)
}

func replayPoint(x, z int32) orders.ResolvePos {
	return orders.ResolvePos{X: numeric.Fixed(x) << 16, Y: 20 << 16, Z: numeric.Fixed(z) << 16}
}

// replayLastDraggable is the receipt the host's drag gesture reads from the
// committed queue: here the last record the Community drag accepts.
func replayLastDraggable(t *testing.T, s *Session, h pool.Handle) orders.CommunityOrderDragReceipt {
	t.Helper()
	q := orders.QueueOfUnit(s.Units.Unit(h))
	if q == nil {
		t.Fatalf("unit %d has no queue", h)
	}
	var receipt orders.CommunityOrderDragReceipt
	for i, n := range q.Primary() {
		if orders.CommunityOrderDraggable(n) {
			receipt = orders.CommunityOrderDragReceipt{Unit: h, Index: uint16(i), DescriptorID: int32(n.ID), CreationTick: n.CreationTick,
				Target: n.Target, GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ, BuildProduct: n.BuildDefKey, BuildFacing: uint8(n.BuildFacing)}
		}
	}
	if receipt.Unit == 0 {
		t.Fatalf("unit %d has no draggable record", h)
	}
	return receipt
}

// replayExpectGoal reports whether a unit's queue holds a record bound for
// the point.
func replayExpectGoal(t *testing.T, s *Session, h pool.Handle, x, z int32, want bool) {
	t.Helper()
	got := false
	if q := orders.QueueOfUnit(s.Units.Unit(h)); q != nil {
		for _, n := range q.Primary() {
			got = got || n.GoalX == numeric.Fixed(x)<<16 && n.GoalZ == numeric.Fixed(z)<<16
		}
	}
	if got != want {
		t.Fatalf("unit %d holds a record for (%d, %d): %v, want %v", h, x, z, got, want)
	}
}

// replayEquivalenceSteps exercises every kind the single-player replay form
// carries, with the shapes the converter must translate: unsorted and
// repeated actors, dead and never-allocated actors and targets, area orders,
// tracked moves and their cancellation, non-canonical keys, both Community
// gestures with current and stale publication identities, the rule-set
// switch, both meteor forms, the view and line-of-sight cheats, and paused
// boundaries mixed with kinds the boundary refuses.
func replayEquivalenceSteps(env *replayEnv, mode gameplay.Mode) []replayStep {
	other := gameplay.Community39
	if mode == gameplay.Community39 {
		other = gameplay.Strict31
	}
	unknownSpawnRejected := 1
	if mode == gameplay.Modern {
		unknownSpawnRejected = 0
	}
	return []replayStep{{
		name: "developer spawns behind a paused boundary", paused: true,
		commands: func(*testing.T, *Session) []HumanCommand {
			c := HumanCommand{Kind: HumanDeveloperSpawn, DeveloperSpawn: HumanDeveloperSpawnCommand{Pattern: "PortScout", X: 160 << 16, Y: 20 << 16, Z: 200 << 16}}
			return []HumanCommand{c, c, c}
		},
		check: func(t *testing.T, s *Session) {
			var scouts []pool.Handle
			for _, h := range replayUnits(s, s.LocalOwner) {
				if h != env.own {
					scouts = append(scouts, h)
				}
			}
			if len(scouts) != 3 {
				t.Fatalf("developer spawn made %d scouts, want 3", len(scouts))
			}
			env.scout, env.scout2, env.doomed = scouts[0], scouts[1], scouts[2]
		},
	}, {
		name: "ordinary orders, unsorted and repeated", ticks: 2,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.scout, env.own, env.scout}, Code: 2, Position: replayPoint(300, 300)}},
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.scout2}, Code: 2, Position: replayPoint(250, 100)}},
			}
		},
	}, {
		name: "queued and tracked moves",
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.scout2}, Code: 2, Queued: true, Position: replayPoint(100, 400)}},
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.own}, Code: 2, Queued: true, TrackQueuedMove: true, Position: replayPoint(200, 200)}},
			}
		},
		check: func(t *testing.T, s *Session) {
			env.tracked = env.sequences[1]
			if s.Units.Unit(env.own) == nil || orders.QueueOfUnit(s.Units.Unit(env.own)).LenPrimary() < 2 {
				t.Fatal("the tracked move was not queued")
			}
		},
	}, {
		name: "targets alive, dead and never allocated, and a dead actor",
		prepare: func(t *testing.T, s *Session) {
			s.Units.Destroy(env.doomed, units.DeathKilled)
		},
		commands: func(t *testing.T, s *Session) []HumanCommand {
			foe := s.Units.Unit(env.foe)
			at := orders.ResolvePos{X: foe.X, Y: foe.Y, Z: foe.Z}
			return []HumanCommand{
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.own}, Code: 3, Target: env.foe, Position: at}},
				// A move whose target is gone stays a ground move that carries
				// the dead handle (§7.4.3), and stays queued long enough to be
				// compared.
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.scout}, Code: 2, Target: env.doomed, Position: replayPoint(500, 20)}},
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.scout}, Code: 2, Target: env.missing, Queued: true, Position: replayPoint(20, 500)}},
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.doomed, env.scout2}, Code: 2, Position: replayPoint(260, 120)}},
				{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: env.doomed, Activate: true}},
			}
		},
	}, {
		name: "area order with every entry kind",
		commands: func(t *testing.T, s *Session) []HumanCommand {
			feature := replayPoint(64, 64)
			feature.HasFeature, feature.IsWreck, feature.InterfaceType = true, true, 1
			return []HumanCommand{{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.scout, env.own}, Code: 7,
				Target: env.foe, AssignedPosition: true, Position: replayPoint(64, 64),
				Targets: []HumanOrderTarget{{Target: env.foe, Position: replayPoint(384, 320)}, {Position: feature},
					{Target: env.doomed, Position: replayPoint(1, 1)}, {Target: env.missing, Position: replayPoint(2, 2)}}}}}
		},
	}, {
		name: "assigned position", ticks: 3,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.scout}, Code: 2, AssignedPosition: true, Position: replayPoint(320, 80)}}}
		},
	}, {
		name: "stances, cloak and activation at a paused boundary", paused: true,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanStance, Stance: HumanStanceCommand{Handles: []pool.Handle{env.own, env.scout}, Value: 1}},
				{Kind: HumanStance, Stance: HumanStanceCommand{Handles: []pool.Handle{env.own}, Fire: true, Value: 2}},
				{Kind: HumanCloak, Cloak: HumanCloakCommand{Handles: []pool.Handle{env.own}, Cloak: true}},
				{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: env.own, Queued: true}},
			}
		},
	}, {
		name: "group assignment with a repeat, and an empty one",
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanGroupAssign, Group: HumanGroupCommand{Handles: []pool.Handle{env.own, env.scout, env.own}, Group: 3}},
				{Kind: HumanGroupAssign, Group: HumanGroupCommand{Group: 4}},
			}
		},
		check: func(t *testing.T, s *Session) {
			if s.Units.Unit(env.own).Group != 3 || s.Units.Unit(env.scout).Group != 3 {
				t.Fatal("the group was not assigned")
			}
		},
	}, {
		name: "cancel the tracked move by its receipt",
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanCancelQueuedMove, CancelQueuedMove: HumanCancelQueuedMoveCommand{Sequence: env.tracked, Handles: []pool.Handle{env.own, env.own}}}}
		},
	}, {
		name: "construction and production", ticks: 2, rejected: 1,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: env.own, Product: "PortScout", WX: 256 << 16, WY: 20 << 16, WZ: 256 << 16, Facing: 1}},
				{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: env.own, Product: "nosuchunit", WX: 288 << 16, WZ: 256 << 16, Queued: true}},
				{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: env.own, Product: "portscout"}},
				{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: env.own, Product: "portscout", Count: -2}},
				{Kind: HumanCancelProduction, CancelProduction: HumanCancelProductionCommand{Unit: env.own}},
				{Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: env.own, Count: -3}},
				{Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: env.own, Count: 2}},
			}
		},
	}, {
		name: "economy cheats at a paused boundary", paused: true,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanSetResource, SetResource: HumanSetResourceCommand{Player: 1, Resource: economy.Energy, Amount: 123.5}},
				{Kind: HumanATM},
				{Kind: HumanGive, Give: HumanGiveCommand{Player: 1, Resource: economy.Metal, Amount: -50}},
				{Kind: HumanSetLogo, SetLogo: HumanSetLogoCommand{Player: 1, Logo: 7}},
				{Kind: HumanMakeSelectable},
			}
		},
	}, {
		name: "view and line of sight at a paused boundary", paused: true,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanView, View: HumanViewCommand{Player: 1}},
				{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}},
			}
		},
		check: func(t *testing.T, s *Session) {
			if s.ViewingOwner != 1 || s.LocalOwner != 0 {
				t.Fatalf("view moved the viewing slot to %d and the local slot to %d", s.ViewingOwner, s.LocalOwner)
			}
		},
	}, {
		name: "view and line of sight back", ticks: 2,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanView, View: HumanViewCommand{Player: 0}},
				{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled, ClearMask: visibility.ModeHistoryEnabled}},
			}
		},
	}, {
		name: "damage gates and the shake driver",
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanDoubleShot}, {Kind: HumanHalfShot}, {Kind: HumanNoShake}}
		},
	}, {
		name: "meteor forms behind a paused boundary", paused: true, ticks: 5,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanMeteor, Meteor: HumanMeteorCommand{ArgumentPresent: true, Enabled: true}},
				{Kind: HumanMeteor, Meteor: HumanMeteorCommand{Enabled: true}},
				{Kind: HumanNoShake},
			}
		},
	}, {
		name: "builder options",
		commands: func(*testing.T, *Session) []HumanCommand {
			var options orders.BuilderOptions
			options.Guard = [3]orders.GuardHomeOption{1, 2, 0}
			options.Patrol = [3]orders.PatrolWorkOption{2, 1, 0}
			return []HumanCommand{{Kind: HumanBuilderOptions, BuilderOptions: HumanBuilderOptionsCommand{Owner: 0, Options: options}}}
		},
	}, {
		name: "a spawn", ticks: 2,
		prepare: func(t *testing.T, s *Session) { env.count = len(replayUnits(s, s.LocalOwner)) },
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanSpawn, Spawn: HumanSpawnCommand{Unit: "PortScout", X: 300 << 16, Y: 20 << 16, Z: 100 << 16}}}
		},
		check: func(t *testing.T, s *Session) {
			if spawned := len(replayUnits(s, s.LocalOwner)) == env.count+1; spawned != (mode == gameplay.Modern) {
				t.Fatalf("the spawn made a unit: %v, under %s", spawned, mode)
			}
		},
	}, {
		// Under Modern an unknown name is announced, which the recording
		// refuses (TestReplayConverterRefusesWhatItCannotExpress); an
		// off-map point is announced in both runs.
		name: "a spawn of an unknown name outside Modern, off the map under it", rejected: unknownSpawnRejected,
		commands: func(*testing.T, *Session) []HumanCommand {
			if mode == gameplay.Modern {
				return []HumanCommand{{Kind: HumanSpawn, Spawn: HumanSpawnCommand{Unit: "portscout", X: 600 << 16, Y: 20 << 16, Z: 100 << 16}}}
			}
			return []HumanCommand{{Kind: HumanSpawn, Spawn: HumanSpawnCommand{Unit: "nosuchunit", X: 300 << 16, Y: 20 << 16, Z: 100 << 16}}}
		},
	}, {
		name: "a move and a queued move to drag",
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.scout2}, Code: 2, Position: replayPoint(480, 480)}},
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{env.scout2}, Code: 2, Queued: true, Position: replayPoint(20, 480)}},
			}
		},
	}, {
		// Each Community gesture that must do nothing has a step of its own
		// and a destination of its own, so a conversion that applied it
		// would leave a record no later gesture overwrites.
		name: "Community order drag with a stale publication identity",
		commands: func(t *testing.T, s *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanCommunityOrderDrag, CommunityOrderDrag: HumanCommunityOrderDragCommand{
				InstanceID: replayInstance(t, s, env.scout2) + 1000, Receipt: replayLastDraggable(t, s, env.scout2),
				Position: orders.CommunityOrderDragDestination{X: 40 << 16, Y: 20 << 16, Z: 300 << 16}}}}
		},
		check: func(t *testing.T, s *Session) { replayExpectGoal(t, s, env.scout2, 40, 300, false) },
	}, {
		name: "Community order drag of a targeted receipt",
		commands: func(t *testing.T, s *Session) []HumanCommand {
			receipt := replayLastDraggable(t, s, env.scout2)
			receipt.Target = env.foe
			return []HumanCommand{{Kind: HumanCommunityOrderDrag, CommunityOrderDrag: HumanCommunityOrderDragCommand{
				InstanceID: replayInstance(t, s, env.scout2), Receipt: receipt,
				Position: orders.CommunityOrderDragDestination{X: 44 << 16, Y: 20 << 16, Z: 300 << 16}}}}
		},
		check: func(t *testing.T, s *Session) { replayExpectGoal(t, s, env.scout2, 44, 300, false) },
	}, {
		name: "Community order drag with the current identity",
		commands: func(t *testing.T, s *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanCommunityOrderDrag, CommunityOrderDrag: HumanCommunityOrderDragCommand{
				InstanceID: replayInstance(t, s, env.scout2), Receipt: replayLastDraggable(t, s, env.scout2),
				Position: orders.CommunityOrderDragDestination{X: 48 << 16, Y: 20 << 16, Z: 460 << 16}}}}
		},
		check: func(t *testing.T, s *Session) { replayExpectGoal(t, s, env.scout2, 48, 460, true) },
	}, {
		name: "Community kickout with a stale publication identity", ticks: 2,
		commands: func(t *testing.T, s *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanCommunityKickout, CommunityKickout: HumanCommunityKickoutCommand{
				Unit: env.scout, InstanceID: replayInstance(t, s, env.scout) + 1000, X: 400 << 16, Y: 20 << 16, Z: 40 << 16}}}
		},
		check: func(t *testing.T, s *Session) { replayExpectGoal(t, s, env.scout, 400, 40, false) },
	}, {
		name: "Community kickout with the current identity", ticks: 2,
		commands: func(t *testing.T, s *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanCommunityKickout, CommunityKickout: HumanCommunityKickoutCommand{
				Unit: env.scout, InstanceID: replayInstance(t, s, env.scout), X: 420 << 16, Y: 20 << 16, Z: 40 << 16}}}
		},
		// Strict 3.1 has no kickout feature.
		check: func(t *testing.T, s *Session) { replayExpectGoal(t, s, env.scout, 420, 40, mode != gameplay.Strict31) },
	}, {
		name: "self-destruct and its cancellation",
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanSelfDestruct, SelfDestruct: HumanSelfDestructCommand{Handles: []pool.Handle{env.scout2}}}}
		},
	}, {
		name: "self-destruct cancelled", ticks: 4,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanSelfDestruct, SelfDestruct: HumanSelfDestructCommand{Handles: []pool.Handle{env.scout2}, Queued: true}}}
		},
	}, {
		name: "rule-set switches", ticks: 3,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanGameplay, Gameplay: other}}
		},
		check: func(t *testing.T, s *Session) {
			if s.Gameplay.Normalize() != other {
				t.Fatalf("rule set %q, want %q", s.Gameplay, other)
			}
		},
	}, {
		name: "rule-set switch through the vocabulary fallback and back",
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanGameplay, Gameplay: "no-such-set"}, {Kind: HumanGameplay, Gameplay: mode}}
		},
	}, {
		name: "stale and never-allocated actors", ticks: 2,
		commands: func(*testing.T, *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{env.missing, env.own, env.doomed, env.missing}}},
				{Kind: HumanSelfDestruct, SelfDestruct: HumanSelfDestructCommand{Handles: []pool.Handle{env.doomed}}},
				{Kind: HumanCloak, Cloak: HumanCloakCommand{Handles: []pool.Handle{env.missing}}},
				{Kind: HumanStance, Stance: HumanStanceCommand{Handles: []pool.Handle{env.missing, env.missing}, Value: 2}},
			}
		},
	}, {
		name: "a long pump", ticks: 5,
		commands: func(*testing.T, *Session) []HumanCommand { return nil },
	}}
}

// The local command and its recorded replay form apply alike: a battle that
// applied the local commands and a second battle composed from the same
// configuration that applied only their replay entries, in recorded pumps,
// hold the same canonical state after every pump — every kind of the
// single-player replay form included (DESIGN_MULTIPLAYER §7.4.4, §10).
func TestReplayCommandsApplyAsTheLocalCommands(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern, gameplay.Community39} {
		t.Run(string(mode), func(t *testing.T) {
			fs := replayFixtureFS(t)
			config := replayFixtureConfig(t, mode)
			local := replayFixtureBattle(t, fs, config, false)
			played := replayFixtureBattle(t, fs, config, true)
			tape := &replayTape{t: t}
			if err := local.SetReplayRecorder(tape); err != nil {
				t.Fatal(err)
			}
			env := &replayEnv{missing: 199}
			for _, u := range local.Units.Iter() {
				switch {
				case u.Owner == local.LocalOwner && env.own == 0:
					env.own = u.Handle
				case u.Owner != local.LocalOwner && env.foe == 0:
					env.foe = u.Handle
				}
			}
			if env.own == 0 || env.foe == 0 || local.Units.RawUnitRecord(env.missing) != nil && local.Units.RawUnitRecord(env.missing).AllocationSerial != 0 {
				t.Fatal("the fixture lacks its commanders or a never-allocated slot")
			}
			kinds := make([]bool, 256)
			enqueued, fed := 0, 0
			for _, step := range replayEquivalenceSteps(env, mode) {
				if step.prepare != nil {
					step.prepare(t, local)
					step.prepare(t, played)
				}
				env.sequences = env.sequences[:0]
				batch := step.commands(t, local)
				for _, c := range batch {
					seq, err := local.EnqueueHumanCommandWithSequence(c)
					if err != nil {
						t.Fatalf("%s: %v", step.name, err)
					}
					env.sequences = append(env.sequences, seq)
				}
				enqueued += len(batch)
				pumps := len(tape.pumps)
				if step.paused {
					local.SetPaused(true)
					local.ExecuteStep(StepPlan{run: true})
					local.SetPaused(false)
					if len(tape.pumps) != pumps {
						t.Fatalf("%s: a pump that ran no tick was recorded", step.name)
					}
				}
				ticks := max(step.ticks, 1)
				replayRequestCheckpoint(t, local)
				local.ExecuteStep(StepPlan{run: true, ticks: ticks})
				if len(tape.failures) != 0 {
					t.Fatalf("%s: recording failed: %v", step.name, tape.failures[0])
				}
				if len(tape.pumps) != pumps+1 || tape.pumps[pumps] != (replayPump{lastTick: local.Clock.GlobalTick, ticks: ticks}) {
					t.Fatalf("%s: pumps %+v after tick %d", step.name, tape.pumps[pumps:], local.Clock.GlobalTick)
				}
				entries := tape.commands[fed:]
				for _, e := range entries {
					kinds[e.command.Kind] = true
					if e.stamp.Seat != local.LocalOwner || e.stamp.Tick <= played.Clock.GlobalTick {
						t.Fatalf("%s: stamp %+v", step.name, e.stamp)
					}
					if err := played.EnqueueSeatCommand(e.stamp, e.command); err != nil {
						t.Fatalf("%s: %v", step.name, err)
					}
				}
				fed = len(tape.commands)
				replayRequestCheckpoint(t, played)
				if err := played.StepRecordedPump(tape.pumps[pumps].lastTick); err != nil {
					t.Fatalf("%s: %v", step.name, err)
				}
				receipts := played.DrainCommandReceipts()
				rejected := 0
				for _, r := range receipts {
					if r.Outcome == CommandRejected {
						rejected++
					}
				}
				if len(receipts) != len(entries) || rejected != step.rejected {
					t.Fatalf("%s: %d receipts with %d rejected for %d entries, want %d rejected: %+v", step.name, len(receipts), rejected, len(entries), step.rejected, receipts)
				}
				replayCompare(t, step.name, local, played)
				if step.check != nil {
					step.check(t, local)
					step.check(t, played)
				}
			}
			if fed != enqueued {
				t.Fatalf("recorded %d of %d local commands", fed, enqueued)
			}
			for _, k := range []SeatCommandKind{SeatOrder, SeatStop, SeatActivation, SeatMobileBuild, SeatFactoryBuild, SeatCancelProduction,
				SeatStockpile, SeatGroupAssign, SeatStance, SeatCloak, SeatSelfDestruct, SeatNoShake, SeatATM, SeatSetResource, SeatSetLogo,
				SeatView, SeatGive, SeatMakeSelectable, SeatVisibility, SeatDoubleShot, SeatHalfShot, SeatMeteor, SeatCancelQueuedMove,
				SeatSpawn, SeatBuilderOptions, SeatCommunityOrderDrag, SeatCommunityKickout, SeatDeveloperSpawn, SeatGameplay} {
				if replayKindAdmission(k) != "" || !kinds[k] {
					t.Errorf("replay kind %d not exercised", k)
				}
			}
			replayCompareHistories(t, local, played)
		})
	}
}

// replayCompareHistories compares the two battles' canonical checkpoint
// histories by tick: the 30-tick digests and every tick's cheap record. The
// pump ordinal is the host's, and a recording's pumps that ran no tick are
// not replayed, so it is left out (DESIGN_MULTIPLAYER §9.1).
func replayCompareHistories(t *testing.T, a, b *Session) {
	t.Helper()
	ha, hb := a.CheckpointHistory(), b.CheckpointHistory()
	if len(ha.Records) != len(hb.Records) || len(ha.Ticks) != len(hb.Ticks) || len(ha.Records) < 2 {
		t.Fatalf("histories hold %d/%d records and %d/%d ticks", len(ha.Records), len(hb.Records), len(ha.Ticks), len(hb.Ticks))
	}
	for i := range ha.Records {
		ra, rb := ha.Records[i], hb.Records[i]
		if ra.Position.Tick != rb.Position.Tick || ra.Position.ConsumedInput != rb.Position.ConsumedInput || ra.Digests != rb.Digests {
			t.Fatalf("checkpoint %d differs: %+v / %+v", i, ra.Position, rb.Position)
		}
	}
	for i := range ha.Ticks {
		ra, rb := ha.Ticks[i], hb.Ticks[i]
		ra.Position.Pump, rb.Position.Pump = 0, 0
		if ra != rb {
			t.Fatalf("tick record %d differs: %+v / %+v", i, ra.Position, rb.Position)
		}
	}
}

// A command the replay form cannot express stops the recording once; the
// battle goes on exactly as it would have without a recorder.
func TestReplayRecordingStopsAtAnInexpressibleCommand(t *testing.T) {
	fs := replayFixtureFS(t)
	config := replayFixtureConfig(t, gameplay.Modern)
	recorded := replayFixtureBattle(t, fs, config, false)
	plain := replayFixtureBattle(t, fs, config, false)
	tape := &replayTape{t: t}
	if err := recorded.SetReplayRecorder(tape); err != nil {
		t.Fatal(err)
	}
	own := replayUnits(recorded, recorded.LocalOwner)[0]
	script := [][]HumanCommand{
		{{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{own}, Code: 2, Position: replayPoint(300, 300)}}},
		// A live actor stopped twice is two applications; the form has no
		// repeated actor.
		{{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{own, own}}}},
		{{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{own}, Code: 2, Position: replayPoint(100, 300)}}},
		nil,
	}
	for i, batch := range script {
		for _, s := range []*Session{recorded, plain} {
			for _, c := range batch {
				if err := s.EnqueueHumanCommand(c); err != nil {
					t.Fatal(err)
				}
			}
			replayRequestCheckpoint(t, s)
			s.ExecuteStep(StepPlan{run: true, ticks: 2})
		}
		replayCompare(t, "a recorder changes nothing", recorded, plain)
		if i == 0 && (len(tape.commands) != 1 || len(tape.pumps) != 1 || len(tape.failures) != 0) {
			t.Fatalf("before the failure: %d commands, %d pumps, %v", len(tape.commands), len(tape.pumps), tape.failures)
		}
	}
	if len(tape.failures) != 1 || len(tape.commands) != 1 || len(tape.pumps) != 1 {
		t.Fatalf("after the failure: %d failures, %d commands, %d pumps", len(tape.failures), len(tape.commands), len(tape.pumps))
	}
	if !strings.Contains(tape.failures[0].Error(), "repeat") {
		t.Fatalf("failure %q does not name the repeated actor", tape.failures[0])
	}
}

// The converter's refusals: each is a shape the local adapter applies and the
// replay form cannot say, or a key spelling a refusal would record
// differently. A refusal names the field, not a generic error.
func TestReplayConverterRefusesWhatItCannotExpress(t *testing.T) {
	fs := replayFixtureFS(t)
	s := replayFixtureBattle(t, fs, replayFixtureConfig(t, gameplay.Modern), false)
	own := replayUnits(s, s.LocalOwner)[0]
	for _, tc := range []struct {
		name string
		c    HumanCommand
		want string
	}{
		{"staged count", HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{own}, Code: 2, StagedCount: 4}}, "staged"},
		{"order code", HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{own}, Code: 15}}, "order code"},
		{"interface type", HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{own}, Code: 2, Position: orders.ResolvePos{InterfaceType: 2}}}, "interface type"},
		{"unknown spawn under Modern", HumanCommand{Kind: HumanSpawn, Spawn: HumanSpawnCommand{Unit: "nosuchunit"}}, "admitted unit"},
		{"repeated live area actor", HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{own, own}, Code: 7,
			Targets: []HumanOrderTarget{{Position: replayPoint(10, 10)}}}}, "repeat"},
		{"two assigned actors", HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{own, own + 1}, Code: 2, AssignedPosition: true}}, "assigned"},
		{"no actor", HumanCommand{Kind: HumanActivation}, "named actor"},
		{"factory key spelling", HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: own, Product: "PortScout"}}, "canonical"},
		{"count beyond 32 bits", HumanCommand{Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: own, Count: 1 << 40}}, "count"},
		{"stance value", HumanCommand{Kind: HumanStance, Stance: HumanStanceCommand{Handles: []pool.Handle{own}, Value: 3}}, "stance"},
		{"player 10", HumanCommand{Kind: HumanGive, Give: HumanGiveCommand{Player: 10, Resource: economy.Metal}}, "player"},
		{"resource", HumanCommand{Kind: HumanSetResource, SetResource: HumanSetResourceCommand{Resource: 2}}, "resource"},
		{"visibility mask", HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: 8}}, "mask"},
		{"padded developer pattern", HumanCommand{Kind: HumanDeveloperSpawn, DeveloperSpawn: HumanDeveloperSpawnCommand{Pattern: " portscout"}}, "pattern"},
		{"local interface kind", HumanCommand{Kind: HumanSelectionReplace}, "kind"},
		{"unknown kind", HumanCommand{Kind: 200}, "kind"},
	} {
		if _, err := s.replayCommand(tc.c); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want a refusal naming %q", tc.name, err, tc.want)
		}
	}
	// A capture from a slot that held no allocation keeps its handle, which
	// a single-player ordinary order to a dead target keeps in its record
	// (§7.4.3), under a serial no allocation has had.
	missing := pool.Handle(199)
	out, err := s.replayCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{own, missing}, Code: 3, Target: missing}})
	if err != nil {
		t.Fatal(err)
	}
	stale := pool.UnitRef{Handle: missing, Serial: s.Units.LastAllocationSerial() + 1}
	if out.Order.Target != stale || len(out.Order.Actors) != 2 || out.Order.Actors[1] != stale || s.Units.LookupReference(stale) != nil {
		t.Fatalf("the never-allocated slot converted to target %+v and actors %+v", out.Order.Target, out.Order.Actors)
	}
	// Shapes that only look inexpressible convert.
	for _, c := range []HumanCommand{
		{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{own, 0, own}, Code: 2}},
		{Kind: HumanGroupAssign, Group: HumanGroupCommand{Handles: []pool.Handle{own, own}, Group: 1}},
		{Kind: HumanMeteor, Meteor: HumanMeteorCommand{Enabled: true}},
		{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: own, Product: " PortScout "}},
		{Kind: HumanGameplay, Gameplay: "no-such-set"},
	} {
		if _, err := s.replayCommand(c); err != nil {
			t.Errorf("kind %d: %v", c.Kind, err)
		}
	}
}

// Attachment and playback refusals.
func TestReplaySeamRefusals(t *testing.T) {
	fs := replayFixtureFS(t)
	config := replayFixtureConfig(t, gameplay.Modern)
	s := replayFixtureBattle(t, fs, config, false)
	if err := s.SetReplayRecorder(nil); err == nil {
		t.Fatal("a nil recorder was attached")
	}
	// A command applied at a paused boundary before the first tick is not
	// in any later recording.
	own := replayUnits(s, s.LocalOwner)[0]
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{own}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReplayRecorder(&replayTape{t: t}); err != nil {
		t.Fatalf("a queued command refused the recorder: %v", err)
	}
	s.SetPaused(true)
	s.ExecuteStep(StepPlan{run: true})
	s.SetPaused(false)
	late := replayFixtureBattle(t, fs, config, false)
	if err := late.EnqueueHumanCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{own}}}); err != nil {
		t.Fatal(err)
	}
	late.SetPaused(true)
	late.ExecuteStep(StepPlan{run: true})
	late.SetPaused(false)
	if err := late.SetReplayRecorder(&replayTape{t: t}); err == nil {
		t.Fatal("a recorder attached after a command was applied")
	}
	late.ExecuteStep(StepPlan{run: true, ticks: 1})
	if err := late.SetReplayRecorder(&replayTape{t: t}); err == nil {
		t.Fatal("a recorder attached after the first tick")
	}
	if err := late.PrepareRecordedBattle(); err == nil {
		t.Fatal("a battle that ran prepared for playback")
	}

	played := replayFixtureBattle(t, fs, config, true)
	if err := played.StepRecordedPump(0); err == nil {
		t.Fatal("a pump ending at the committed tick ran")
	}
	if err := played.EnqueueHumanCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{own}}}); err != nil {
		t.Fatal(err)
	}
	if err := played.StepRecordedPump(2); err == nil || played.Clock.GlobalTick != 0 {
		t.Fatal("a playback pump ran with a local command queued")
	}

	online := newSeatFixture(t, true, false)
	if err := online.s.SetReplayRecorder(&replayTape{t: t}); err == nil {
		t.Fatal("an online battle attached a single-player recorder")
	}
	if err := online.s.PrepareRecordedBattle(); err == nil {
		t.Fatal("an online battle prepared for single-player playback")
	}
	if err := online.s.StepRecordedPump(online.s.Clock.GlobalTick + 1); err == nil {
		t.Fatal("an online battle ran a recorded pump")
	}
	if _, ok := online.s.SimulationContentDigest(); ok {
		t.Fatal("a battle without frozen inputs reported a content digest")
	}
	var nilSession *Session
	if err := nilSession.StepRecordedPump(1); err == nil {
		t.Fatal("a nil session ran a recorded pump")
	}
}
