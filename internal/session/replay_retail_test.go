//go:build retail

package session

import (
	"sync"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/tactics"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utility"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// replayRetailMap is the map on which TestAdmittedSkirmishComposesTheLocalBattleRetail
// proves ordinary single-player entry and admitted entry compose one battle.
const replayRetailMap = "ashap plateau"

// replayRetailSchema is the schema the map-entry code selects for the seat
// count, as the room resolves it once.
func replayRetailSchema(t *testing.T, fs vfs.FSOps, cat *content.Catalog, players int) uint32 {
	t.Helper()
	m, err := mission.LoadWithType(fs, mission.TypeSkirmish, replayRetailMap, 0, players, nil)
	if err != nil {
		t.Fatal(err)
	}
	header := cat.Maps[content.CanonicalKey(m.TerrainKey)]
	if header == nil {
		t.Fatalf("no map header for %q", m.TerrainKey)
	}
	return mapSchemaIndex(header, m.Schema.Name)
}

// replayRetailItem is one input batch of the retail script, entered once the
// battle reaches its tick; paused ones enter at a paused-input boundary.
type replayRetailItem struct {
	at       uint32
	paused   bool
	commands func(s *Session) []HumanCommand
}

// replayRetailPlay is a host on the reference install: Session.Step with
// clock deltas of one to five ticks, pauses whose boundary applies the
// input it can, and a script on the side's opening chain — construction,
// developer-spawned kbots, orders, stances, tracked moves, the view and
// line-of-sight cheats, production, gifts, self-destruction, attacks on a
// unit that dies and on its dead handle, and an area work order.
func replayRetailPlay(t *testing.T, s *Session, cat *content.Catalog, ticks uint32) {
	t.Helper()
	ours := retailcat.SelectOpeningChain(t, cat, 0)
	theirs := retailcat.SelectOpeningChain(t, cat, 1)
	var commander, lab, enemy pool.Handle
	var kbots []pool.Handle
	var tracked uint64
	at := func(x, z int32) (numeric.Fixed, numeric.Fixed, numeric.Fixed) {
		u := s.Units.Unit(commander)
		wx, wz := u.X+numeric.Fixed(x)<<16, u.Z+numeric.Fixed(z)<<16
		return wx, s.World.HeightAt(wx, wz), wz
	}
	point := func(x, z int32) orders.ResolvePos {
		wx, wy, wz := at(x, z)
		return orders.ResolvePos{X: wx, Y: wy, Z: wz}
	}
	find := func(owner uint8, key string) []pool.Handle {
		var out []pool.Handle
		for _, u := range s.Units.Iter() {
			if u.Alive && u.Owner == owner && u.Def != nil && u.Def.CanonicalKey == content.CanonicalKey(key) {
				out = append(out, u.Handle)
			}
		}
		return out
	}
	script := []replayRetailItem{
		{at: 3, commands: func(s *Session) []HumanCommand {
			commander = find(s.LocalOwner, ours.Commander)[0]
			sx, sy, sz := at(96, 0)
			lx, ly, lz := at(0, 192)
			return []HumanCommand{
				{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: commander, Product: ours.Solar, WX: sx, WY: sy, WZ: sz}},
				{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: commander, Product: ours.KbotLab, WX: lx, WY: ly, WZ: lz, Queued: true}},
			}
		}},
		{at: 30, commands: func(s *Session) []HumanCommand {
			x, y, z := at(-96, -64)
			c := HumanCommand{Kind: HumanDeveloperSpawn, DeveloperSpawn: HumanDeveloperSpawnCommand{Pattern: ours.LabProduct, Owner: s.LocalOwner, X: x, Y: y, Z: z}}
			return []HumanCommand{c, c, c}
		}},
		{at: 60, paused: true, commands: func(s *Session) []HumanCommand {
			kbots = find(s.LocalOwner, ours.LabProduct)
			return []HumanCommand{
				{Kind: HumanStance, Stance: HumanStanceCommand{Handles: kbots, Fire: true, Value: 1}},
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: kbots, Code: 2, Position: point(-200, 40)}},
				{Kind: HumanView, View: HumanViewCommand{Player: 1}},
				{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}},
			}
		}},
		{at: 90, commands: func(s *Session) []HumanCommand {
			move := HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: kbots[:1], Code: 2, Queued: true, TrackQueuedMove: true, Position: point(-260, 120)}}
			return []HumanCommand{
				{Kind: HumanView, View: HumanViewCommand{Player: 0}},
				{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}},
				{Kind: HumanGroupAssign, Group: HumanGroupCommand{Handles: kbots, Group: 2}},
				move,
			}
		}},
		{at: 150, commands: func(s *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanCancelQueuedMove, CancelQueuedMove: HumanCancelQueuedMoveCommand{Sequence: tracked, Handles: kbots[:1]}}}
		}},
		{at: 200, commands: func(s *Session) []HumanCommand {
			x, y, z := at(-320, 0)
			return []HumanCommand{
				{Kind: HumanDeveloperSpawn, DeveloperSpawn: HumanDeveloperSpawnCommand{Pattern: theirs.LabProduct, Owner: 1, X: x, Y: y, Z: z}},
				{Kind: HumanCloak, Cloak: HumanCloakCommand{Handles: []pool.Handle{commander}, Cloak: true}},
			}
		}},
		{at: 210, commands: func(s *Session) []HumanCommand {
			if found := find(1, theirs.LabProduct); len(found) != 0 {
				enemy = found[0]
			}
			u := s.Units.Unit(enemy)
			if u == nil {
				return nil
			}
			return []HumanCommand{
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: kbots, Code: 3, Target: enemy, Position: orders.ResolvePos{X: u.X, Y: u.Y, Z: u.Z}}},
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: kbots, Code: 3, Target: enemy, Queued: true, Position: orders.ResolvePos{X: u.X, Y: u.Y, Z: u.Z}}},
			}
		}},
		{at: 300, commands: func(s *Session) []HumanCommand {
			labs := find(s.LocalOwner, ours.KbotLab)
			if len(labs) == 0 {
				return nil
			}
			lab = labs[0]
			return []HumanCommand{{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: lab, Product: ours.LabProduct, Count: 2}}}
		}},
		{at: 400, paused: true, commands: func(s *Session) []HumanCommand {
			return []HumanCommand{
				{Kind: HumanStop, Stop: HumanStopCommand{Handles: kbots}},
				{Kind: HumanATM},
				{Kind: HumanGive, Give: HumanGiveCommand{Player: 1, Amount: 100}},
			}
		}},
		{at: 450, commands: func(s *Session) []HumanCommand {
			// The enemy is dead by now or soon; either way the order keeps
			// its handle, as the single-player applier does (§7.4.3).
			return []HumanCommand{
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: kbots, Code: 3, Target: enemy, Position: point(-320, 0)}},
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: kbots, Code: 1, Target: enemy, Queued: true, Position: point(-300, 0)}},
			}
		}},
		{at: 500, commands: func(s *Session) []HumanCommand {
			return []HumanCommand{{Kind: HumanSelfDestruct, SelfDestruct: HumanSelfDestructCommand{Handles: kbots[1:2]}}}
		}},
		{at: 650, commands: func(s *Session) []HumanCommand {
			targets := []HumanOrderTarget{{Position: point(64, 64)}, {Target: enemy, Position: point(-320, 0)}, {Position: point(-64, 64)}}
			for i := range targets {
				targets[i].Position.HasFeature = targets[i].Target == 0
			}
			return []HumanCommand{
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{commander}, Code: 12, Targets: targets}},
				{Kind: HumanOrder, Order: HumanOrderCommand{Handles: kbots, Code: 2, Position: point(160, -160)}},
			}
		}},
	}
	deltas := []int32{1, 3, 2, 5, 4, 1, 2, 5, 3, 1, 1, 4}
	next := 0
	for i := 0; s.Clock == nil || s.Clock.GlobalTick < ticks; i++ {
		if i > 4*int(ticks) {
			t.Fatalf("the battle stopped advancing at tick %d", s.Clock.GlobalTick)
		}
		if s.Clock != nil && next < len(script) && s.State == StateBattle && s.Clock.GlobalTick >= script[next].at {
			item := script[next]
			next++
			for _, c := range item.commands(s) {
				seq, err := s.EnqueueHumanCommandWithSequence(c)
				if err != nil {
					t.Fatal(err)
				}
				if c.Order.TrackQueuedMove {
					tracked = seq
				}
			}
			if item.paused {
				s.SetPaused(true)
				s.Step(s.Clock.ScaledAnchor + 2)
				s.SetPaused(false)
			}
		}
		var anchor int32
		if s.Clock != nil {
			anchor = s.Clock.ScaledAnchor
		}
		s.Step(anchor + deltas[i%len(deltas)])
		if s.State != StateBattle && s.Clock != nil && s.Clock.GlobalTick > 0 {
			t.Fatalf("the battle ended at tick %d", s.Clock.GlobalTick)
		}
	}
	if next != len(script) {
		t.Fatalf("the script stopped at item %d", next)
	}
	if len(kbots) != 3 || commander == 0 || enemy == 0 {
		t.Fatalf("the script found commander %d, kbots %v and enemy %d", commander, kbots, enemy)
	}
}

// replayRetailVerify plays the tape into the prepared playback battle and
// requires every recorded checksum, every pump and the final state.
func replayRetailVerify(t *testing.T, recorded, played *Session, tape *replayTape) {
	t.Helper()
	if len(tape.failures) != 0 {
		t.Fatalf("recording failed: %v", tape.failures)
	}
	observer := replayPlay(t, played, tape)
	if len(tape.checksums) < 25 || len(observer.checksums) != len(tape.checksums) || len(observer.pumps) != len(tape.pumps) {
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
	if played.UnitStateChecksum() != recorded.UnitStateChecksum() || *played.SimRNG() != *recorded.SimRNG() || *played.CrtRNG() != *recorded.CrtRNG() {
		t.Fatal("the final state differs")
	}
	t.Logf("played back %d commands in %d pumps over %d ticks; %d checksums and %d units agree",
		len(tape.commands), len(tape.pumps), played.Clock.GlobalTick, len(tape.checksums), len(played.Units.AppendLive(nil)))
}

// A battle recorded through the ordinary single-player entry, against a
// Classic computer under Strict 3.1, plays back exactly from its replay
// header: the configuration resolved, encoded and decoded, the content
// frozen for it and composed through the admitted constructor
// (DESIGN_MULTIPLAYER §10).
func TestReplayRoundTripClassicRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	cfg := DirectSkirmishConfig(replayRetailMap)
	cfg.Gameplay = gameplay.Strict31
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 5006
	var options SkirmishEntryOptions
	recorded, err := NewSkirmishWithEntryOptions(fs, cat, cfg, options)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.AI[1] == nil || recorded.AI[1].Controller != ai.ControllerClassic {
		t.Fatal("slot 1 is not a Classic computer")
	}
	tape := &replayTape{t: t}
	if err := recorded.SetReplayRecorder(tape); err != nil {
		t.Fatal(err)
	}
	replayRetailPlay(t, recorded, cat, 900)

	config := replayHeaderConfig(t, cfg, options, replayRetailSchema(t, fs, cat, cfg.NumPlayers))
	inputs, err := FreezeMatchInputs(fs, cat, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if digest, ok := recorded.SimulationContentDigest(); !ok || digest != inputs.Digest() {
		t.Fatal("the header's content digest is not the recorded battle's")
	}
	played, err := NewAdmittedSkirmish(inputs, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := played.PrepareRecordedBattle(); err != nil {
		t.Fatal(err)
	}
	replayRetailVerify(t, recorded, played, tape)
}

// The same battle recorded and played back through the admitted constructor
// on both sides keeps canonical checkpoints, so the comparison covers all of
// the authoritative state — every queue, the effect pool, the planner's
// records — at each 30-tick checkpoint and every tick's cheap record, not
// only units (DESIGN_MULTIPLAYER §9.1).
func TestReplayCanonicalRoundTripRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	cfg := DirectSkirmishConfig(replayRetailMap)
	cfg.Gameplay = gameplay.Strict31
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 5006
	config := replayHeaderConfig(t, cfg, SkirmishEntryOptions{}, replayRetailSchema(t, fs, cat, cfg.NumPlayers))
	compose := func(playback bool) *Session {
		inputs, err := FreezeMatchInputs(fs, cat, config, nil)
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewAdmittedSkirmish(inputs, config, nil)
		if err != nil {
			t.Fatal(err)
		}
		if playback {
			if err := s.PrepareRecordedBattle(); err != nil {
				t.Fatal(err)
			}
		} else {
			for i := 0; s.State != StateBattle || s.IsPendingBattle(); i++ {
				if i == 8 {
					t.Fatal("entry did not reach its first pump")
				}
				s.PrepareStep(s.Clock.ScaledAnchor)
			}
		}
		if !s.PublishOpeningFrame() {
			t.Fatal("opening publication")
		}
		if err := s.EnableCheckpoints(); err != nil {
			t.Fatal(err)
		}
		return s
	}
	recorded, played := compose(false), compose(true)
	tape := &replayTape{t: t}
	if err := recorded.SetReplayRecorder(tape); err != nil {
		t.Fatal(err)
	}
	replayRetailPlay(t, recorded, cat, 900)
	replayRetailVerify(t, recorded, played, tape)
	replayCompareHistories(t, recorded, played)
}

// replayAsyncRuleSet binds the Modern AI host the way the arena's rule set
// does: a computer player given a host brain thinks through it, here on its
// worker goroutine. Test builds cannot link mods/aikit.
const replayAsyncRuleSet = "replay-async-test"

var replayAsyncOnce sync.Once

// A battle against a Modern computer whose persona thinks asynchronously
// plays back exactly: the think's worker never decides what a tick computes
// (DESIGN_GAMEPLAY_RULES "The Modern AI controller"), so a recording and its
// playback join the same thinks at the same deadlines.
func TestReplayRoundTripModernAsyncRetail(t *testing.T) {
	replayAsyncOnce.Do(func() {
		RegisterRuleSet(replayAsyncRuleSet, func() RuleSet {
			return RuleSet{Base: gameplay.Modern, Planner: aikit.HostPlanner{}, ComputerIncome: FullComputerIncome{}}
		})
	})
	cat, fs := retailcat.Shared(t)
	cfg := DirectSkirmishConfig(replayRetailMap)
	cfg.Gameplay = gameplay.Mode(replayAsyncRuleSet)
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 5006
	compose := func() *Session {
		s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		m := s.AI[1]
		if m == nil {
			t.Fatal("slot 1 has no computer")
		}
		strategy, economy, production := utility.Policies(utility.DefaultParams())
		persona := aikit.PersonaMed
		persona.Async = true
		host := aikit.NewHost(m, core.New("util+tac", strategy, economy, tactics.New(tactics.DefaultParams()), production), persona)
		m.Ext = host
		t.Cleanup(host.Close)
		if !(aikit.HostPlanner{}).ControlsModernAI(m) {
			t.Fatal("the computer is not a Modern AI player")
		}
		return s
	}
	recorded, played := compose(), compose()
	tape := &replayTape{t: t}
	if err := recorded.SetReplayRecorder(tape); err != nil {
		t.Fatal(err)
	}
	replayRetailPlay(t, recorded, cat, 900)
	if err := played.PrepareRecordedBattle(); err != nil {
		t.Fatal(err)
	}
	replayRetailVerify(t, recorded, played, tape)
	if built := replayUnits(played, 1); len(built) < 2 {
		t.Fatalf("the Modern computer built nothing: %d units", len(built))
	}
}
