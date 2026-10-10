package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// replayPlay plays a tape back into a battle prepared for playback, as a
// replay file's reader would: before each recorded pump it queues the
// recorded commands bound to that pump's ticks, then runs the pump. It
// returns what an observer attached to the playback heard.
func replayPlay(t *testing.T, played *Session, tape *replayTape) *replayTape {
	t.Helper()
	observer := &replayTape{t: t}
	if err := played.SetReplayRecorder(observer); err != nil {
		t.Fatal(err)
	}
	next := 0
	for _, p := range tape.pumps {
		for next < len(tape.commands) && tape.commands[next].stamp.Tick <= p.lastTick {
			e := tape.commands[next]
			if err := played.EnqueueSeatCommand(e.stamp, e.command); err != nil {
				t.Fatalf("command %+v: %v", e.stamp, err)
			}
			next++
		}
		if err := played.StepRecordedPump(p.lastTick); err != nil {
			t.Fatal(err)
		}
		for _, r := range played.DrainCommandReceipts() {
			if r.Outcome == CommandRejected {
				t.Fatalf("playback refused %+v: %s", r.Stamp, r.Diagnostic)
			}
		}
	}
	if next != len(tape.commands) {
		t.Fatalf("%d recorded commands lie beyond the last pump", len(tape.commands)-next)
	}
	return observer
}

// replayRoundTripPump is one host pump of the round-trip plan: the plan is a
// host's session of pumps of one to five ticks, pauses whose boundary applies
// part of their input, and a mixed command script. A pump of 0 ticks is a
// paused boundary.
type replayRoundTripPump struct {
	ticks    int
	commands func(s *Session, own []pool.Handle) []HumanCommand
}

func replayRoundTripPlan(foe pool.Handle) []replayRoundTripPump {
	move := func(hs []pool.Handle, x, z int32, queued bool) HumanCommand {
		return HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: hs, Code: 2, Queued: queued, Position: replayPoint(x, z)}}
	}
	sizes := []int{1, 3, 2, 5, 4, 1, 1, 2, 5, 3, 4, 2}
	var plan []replayRoundTripPump
	for i := 0; len(plan) < 110; i++ {
		plan = append(plan, replayRoundTripPump{ticks: sizes[i%len(sizes)]})
	}
	at := func(i int, ticks int, f func(s *Session, own []pool.Handle) []HumanCommand) {
		plan[i] = replayRoundTripPump{ticks: ticks, commands: f}
	}
	at(1, 2, func(*Session, []pool.Handle) []HumanCommand {
		c := HumanCommand{Kind: HumanDeveloperSpawn, DeveloperSpawn: HumanDeveloperSpawnCommand{Pattern: "portscout", X: 140 << 16, Y: 20 << 16, Z: 160 << 16}}
		return []HumanCommand{c, c}
	})
	at(4, 3, func(_ *Session, own []pool.Handle) []HumanCommand {
		return []HumanCommand{move(own, 400, 400, false), move(own[1:], 60, 450, true)}
	})
	// A paused boundary: the view and the tracked move apply there; the
	// argument-free meteor stops the drain and waits, with what follows it,
	// for the tick.
	at(9, 0, func(_ *Session, own []pool.Handle) []HumanCommand {
		tracked := move(own[:1], 300, 60, true)
		tracked.Order.TrackQueuedMove = true
		return []HumanCommand{{Kind: HumanView, View: HumanViewCommand{Player: 1}}, tracked,
			{Kind: HumanMeteor}, {Kind: HumanStance, Stance: HumanStanceCommand{Handles: own, Value: 2}}}
	})
	at(10, 0, func(_ *Session, own []pool.Handle) []HumanCommand {
		return []HumanCommand{{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}}}
	})
	at(14, 2, func(_ *Session, own []pool.Handle) []HumanCommand {
		return []HumanCommand{{Kind: HumanView, View: HumanViewCommand{Player: 0}},
			{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}},
			{Kind: HumanGroupAssign, Group: HumanGroupCommand{Handles: own, Group: 2}}}
	})
	at(20, 1, func(s *Session, own []pool.Handle) []HumanCommand {
		q := orders.QueueOfUnit(s.Units.Unit(own[1]))
		var receipt orders.CommunityOrderDragReceipt
		for i, n := range q.Primary() {
			if orders.CommunityOrderDraggable(n) {
				receipt = orders.CommunityOrderDragReceipt{Unit: own[1], Index: uint16(i), DescriptorID: int32(n.ID), CreationTick: n.CreationTick,
					GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ, BuildProduct: n.BuildDefKey, BuildFacing: uint8(n.BuildFacing)}
			}
		}
		if receipt.Unit == 0 {
			return nil
		}
		var instance uint64
		for _, v := range s.Snapshot.Current().Units {
			if v.Slot == own[1] {
				instance = v.InstanceID
			}
		}
		return []HumanCommand{{Kind: HumanCommunityOrderDrag, CommunityOrderDrag: HumanCommunityOrderDragCommand{InstanceID: instance, Receipt: receipt,
			Position: orders.CommunityOrderDragDestination{X: 30 << 16, Y: 20 << 16, Z: 300 << 16}}}}
	})
	at(25, 4, func(_ *Session, own []pool.Handle) []HumanCommand {
		return []HumanCommand{{Kind: HumanSpawn, Spawn: HumanSpawnCommand{Unit: "portscout", X: 250 << 16, Y: 20 << 16, Z: 250 << 16}},
			{Kind: HumanCloak, Cloak: HumanCloakCommand{Handles: own[:1], Cloak: true}},
			{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: own[0]}}}
	})
	at(31, 0, func(_ *Session, own []pool.Handle) []HumanCommand {
		return []HumanCommand{{Kind: HumanStop, Stop: HumanStopCommand{Handles: own[1:]}}, {Kind: HumanATM},
			{Kind: HumanCommunityKickout, CommunityKickout: HumanCommunityKickoutCommand{Unit: own[1], X: 200 << 16, Y: 20 << 16, Z: 40 << 16}}}
	})
	at(40, 5, func(_ *Session, own []pool.Handle) []HumanCommand {
		return []HumanCommand{{Kind: HumanSelfDestruct, SelfDestruct: HumanSelfDestructCommand{Handles: own[2:3]}},
			{Kind: HumanOrder, Order: HumanOrderCommand{Handles: own[:2], Code: 3, Target: foe, Position: replayPoint(384, 320)}}}
	})
	at(55, 3, func(_ *Session, own []pool.Handle) []HumanCommand {
		return []HumanCommand{{Kind: HumanGameplay, Gameplay: gameplay.Community39}, {Kind: HumanNoShake}, move(own, 100, 100, false)}
	})
	at(70, 0, func(_ *Session, own []pool.Handle) []HumanCommand {
		return []HumanCommand{move(own, 450, 60, true), {Kind: HumanMeteor, Meteor: HumanMeteorCommand{ArgumentPresent: true, Enabled: true}}}
	})
	at(85, 2, func(_ *Session, own []pool.Handle) []HumanCommand {
		return []HumanCommand{{Kind: HumanGameplay, Gameplay: gameplay.Modern}, move(own, 256, 256, false)}
	})
	return plan
}

// A recorded battle plays back exactly: a second battle composed from the
// recording's configuration and driven only by the tape — its pumps and its
// stamped commands — matches every recorded checksum, ends in the same state,
// and holds the same canonical checkpoints. A third battle that ran the same
// local input with no recorder ends identically, so recording changes
// nothing (DESIGN_MULTIPLAYER §4.5, §9.1, §10).
func TestReplayRoundTripReproducesTheBattle(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			fs := replayFixtureFS(t)
			config := replayFixtureConfig(t, mode)
			recorded := replayFixtureBattle(t, fs, config, false)
			plain := replayFixtureBattle(t, fs, config, false)
			tape := &replayTape{t: t}
			if err := recorded.SetReplayRecorder(tape); err != nil {
				t.Fatal(err)
			}
			var foe pool.Handle
			for _, u := range recorded.Units.Iter() {
				if u.Owner != recorded.LocalOwner {
					foe = u.Handle
					break
				}
			}
			paused, ticks := 0, 0
			for _, pump := range replayRoundTripPlan(foe) {
				for _, s := range []*Session{recorded, plain} {
					if pump.commands != nil {
						for _, c := range pump.commands(s, replayUnits(s, s.LocalOwner)) {
							if err := s.EnqueueHumanCommand(c); err != nil {
								t.Fatal(err)
							}
						}
					}
					s.SetPaused(pump.ticks == 0)
					s.ExecuteStep(StepPlan{run: true, ticks: pump.ticks})
				}
				if pump.ticks == 0 {
					paused++
				}
				ticks += pump.ticks
			}
			recorded.SetPaused(false)
			if len(tape.failures) != 0 {
				t.Fatalf("recording failed: %v", tape.failures)
			}
			if recorded.Clock.GlobalTick != uint32(ticks) || len(tape.pumps)+paused != len(replayRoundTripPlan(foe)) || len(tape.commands) < 25 {
				t.Fatalf("recorded %d pumps and %d commands over %d ticks", len(tape.pumps), len(tape.commands), recorded.Clock.GlobalTick)
			}
			for i, c := range tape.checksums {
				if c.tick != uint32(i+1)*ReplayChecksumInterval {
					t.Fatalf("checksum %d at tick %d", i, c.tick)
				}
			}
			if len(tape.checksums) != ticks/ReplayChecksumInterval {
				t.Fatalf("%d checksums over %d ticks", len(tape.checksums), ticks)
			}

			played := replayFixtureBattle(t, fs, config, true)
			observer := replayPlay(t, played, tape)
			if len(observer.checksums) != len(tape.checksums) || len(observer.pumps) != len(tape.pumps) || len(observer.commands) != 0 {
				t.Fatalf("playback heard %d checksums and %d pumps, recording %d and %d", len(observer.checksums), len(observer.pumps), len(tape.checksums), len(tape.pumps))
			}
			for i := range tape.checksums {
				if observer.checksums[i] != tape.checksums[i] {
					t.Fatalf("checksum at tick %d differs", tape.checksums[i].tick)
				}
			}
			for i := range tape.pumps {
				if observer.pumps[i] != tape.pumps[i] {
					t.Fatalf("pump %d: %+v, recorded %+v", i, observer.pumps[i], tape.pumps[i])
				}
			}
			for _, s := range []*Session{played, plain} {
				if s.UnitStateChecksum() != recorded.UnitStateChecksum() || *s.SimRNG() != *recorded.SimRNG() || *s.CrtRNG() != *recorded.CrtRNG() {
					t.Fatal("the final state differs")
				}
				if s.LocalOwner != recorded.LocalOwner || s.ViewingOwner != recorded.ViewingOwner || s.Gameplay != recorded.Gameplay {
					t.Fatal("the final seats or rule set differ")
				}
			}
			replayCompareHistories(t, recorded, played)
			replayCompareHistories(t, recorded, plain)
		})
	}
}

// Input applied at a paused-input boundary is recorded at the tick that had
// not run, and its playback applies it in phase 1 of that tick: the state is
// the same at that tick and after, though the recording ran an executor tail
// between the input and the tick (DESIGN_MULTIPLAYER §4.2, §9.1). Temporary
// sight from a death is live while the boundary applies, so the tail has
// work to repeat.
func TestReplayPausedBoundaryCommandsReplayInPhaseOne(t *testing.T) {
	fs := replayFixtureFS(t)
	config := replayFixtureConfig(t, gameplay.Modern)
	recorded := replayFixtureBattle(t, fs, config, false)
	played := replayFixtureBattle(t, fs, config, true)
	tape := &replayTape{t: t}
	if err := recorded.SetReplayRecorder(tape); err != nil {
		t.Fatal(err)
	}
	spawn := HumanCommand{Kind: HumanDeveloperSpawn, DeveloperSpawn: HumanDeveloperSpawnCommand{Pattern: "portscout", X: 140 << 16, Y: 20 << 16, Z: 160 << 16}}
	opening := []HumanCommand{spawn, spawn}
	if !recorded.Vis.Mode().CurrentEnabled() {
		// Temporary sight needs current line of sight (`+los`).
		opening = append(opening, HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}})
	}
	for _, c := range opening {
		if err := recorded.EnqueueHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	recorded.ExecuteStep(StepPlan{run: true, ticks: 3})
	replayPlayPartial(t, played, tape, 0)
	own := replayUnits(recorded, recorded.LocalOwner)
	if len(own) != 3 {
		t.Fatalf("%d local units", len(own))
	}
	// A death the viewing player sees leaves temporary sight that the next
	// tails expire (eyeball.go).
	for _, s := range []*Session{recorded, played} {
		s.Units.Destroy(own[2], units.DeathKilled)
	}
	for i := 0; len(postLoopStateFor(recorded).eyeballs.records) == 0; i++ {
		if i == 60 {
			t.Fatal("the death left no temporary sight")
		}
		recorded.ExecuteStep(StepPlan{run: true, ticks: 1})
		replayPlayPartial(t, played, tape, len(tape.pumps)-1)
	}
	if len(postLoopStateFor(played).eyeballs.records) != 1 {
		t.Fatal("the playback has no temporary sight")
	}
	committed := recorded.Clock.GlobalTick
	commands := []HumanCommand{
		{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeTerrainRay}},
		{Kind: HumanView, View: HumanViewCommand{Player: 1}},
		{Kind: HumanOrder, Order: HumanOrderCommand{Handles: own[:2], Code: 2, Position: replayPoint(420, 420)}},
		{Kind: HumanStance, Stance: HumanStanceCommand{Handles: own[:1], Value: 1}},
	}
	for _, c := range commands {
		if err := recorded.EnqueueHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	recorded.SetPaused(true)
	for range 3 {
		recorded.ExecuteStep(StepPlan{run: true})
	}
	recorded.SetPaused(false)
	if len(recorded.PendingHumanCommands()) != 0 || recorded.ViewingOwner != 1 || recorded.Clock.GlobalTick != committed {
		t.Fatal("the paused boundary did not apply the input without a tick")
	}
	recordedAt := tape.commands[len(tape.commands)-len(commands):]
	for _, e := range recordedAt {
		if e.stamp.Tick != committed+1 {
			t.Fatalf("paused input recorded at tick %d, want %d", e.stamp.Tick, committed+1)
		}
	}
	replayRequestCheckpoint(t, recorded)
	recorded.ExecuteStep(StepPlan{run: true, ticks: 1})
	replayRequestCheckpoint(t, played)
	replayPlayPartial(t, played, tape, len(tape.pumps)-1)
	replayCompare(t, "the paused tick", recorded, played)
	for range 70 {
		replayRequestCheckpoint(t, recorded)
		recorded.ExecuteStep(StepPlan{run: true, ticks: 1})
		replayRequestCheckpoint(t, played)
		replayPlayPartial(t, played, tape, len(tape.pumps)-1)
		replayCompare(t, "after the paused tick", recorded, played)
	}
	if len(postLoopStateFor(recorded).eyeballs.records) != 0 {
		t.Fatal("the temporary sight never expired")
	}
}

// replayPlayPartial plays the tape's pumps from index from to its end into a
// playback that has played every earlier pump.
func replayPlayPartial(t *testing.T, played *Session, tape *replayTape, from int) {
	t.Helper()
	for _, p := range tape.pumps[from:] {
		for _, e := range tape.commands {
			if e.stamp.Tick > played.Clock.GlobalTick && e.stamp.Tick <= p.lastTick {
				if err := played.EnqueueSeatCommand(e.stamp, e.command); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := played.StepRecordedPump(p.lastTick); err != nil {
			t.Fatal(err)
		}
		for _, r := range played.DrainCommandReceipts() {
			if r.Outcome != CommandApplied {
				t.Fatalf("playback receipt %+v", r)
			}
		}
	}
}
