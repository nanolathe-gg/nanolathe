package main

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestLocalMultiplayerLaunchFlags(t *testing.T) {
	for _, flag := range []string{"--local-mp-listen", "--local-mp-join"} {
		for _, address := range []string{"127.0.0.1:39031", "[::1]:39031"} {
			o, err := parseFlags([]string{flag, address, "--map", "ashap plateau"}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if !o.localMultiplayer() || !o.ignoresSavedSelection() || !o.GameplaySet || o.Gameplay != gameplay.Modern || !o.ArrivalSet || o.Arrival || o.Seed != -1 {
				t.Fatalf("wrong play-test launch: %+v", o)
			}
		}
	}
	base := []string{"--local-mp-listen", "127.0.0.1:39031", "--map", "ashap plateau"}
	for _, extra := range [][]string{
		{"--local-mp-join", "127.0.0.1:39031"}, {"--gameplay", "strict-3.1"}, {"--mod", "some-mod"}, {"--mod-config", "some.json"}, {"--root", "a", "--root", "b"},
		{"--mutator", "health=2"}, {"--restrict", "none"}, {"--ai-player", "all=modern"}, {"--unit-limit", "1000"},
		{"--survival"}, {"--mission", "campaign:mission"}, {"--load-save", "test.sav"}, {"--headless=false"}, {"--ticks", "0"},
		{"--shot", "test.png"}, {"--shot-ticks", "90"}, {"--film", "test.json"}, {"--live-trace", "test"}, {"--battle-benchmark", "coast"}, {"--check-install"},
		{"--arrival=true"}, {"--seed", "-1"},
	} {
		t.Run(strings.Join(extra, " "), func(t *testing.T) {
			var diagnostic strings.Builder
			if _, err := parseFlags(append(append([]string{}, base...), extra...), &diagnostic); err == nil {
				t.Fatal("unsupported combination admitted")
			}
			if diagnostic.Len() == 0 {
				t.Fatal("launch refusal was not reported")
			}
		})
	}
	for _, args := range [][]string{
		{"--local-mp-listen", "127.0.0.1:39031"}, {"--local-mp-listen", "", "--map", "m"},
		{"--local-mp-join", "localhost:39031", "--map", "m"}, {"--local-mp-listen", "0.0.0.0:39031", "--map", "m"},
		{"--local-mp-join", "127.0.0.1:0", "--map", "m"},
	} {
		if _, err := parseFlags(args, io.Discard); err == nil {
			t.Fatalf("admitted %v", args)
		}
	}
}

func TestLocalMultiplayerOrderDelayFlags(t *testing.T) {
	base := []string{"--local-mp-listen", "127.0.0.1:39031", "--map", "ashap plateau"}
	for _, delay := range []string{"0", "50", "100", "150", "250", "1000"} {
		if _, err := parseFlags(append(append([]string{}, base...), "--local-mp-command-delay-ms", delay), io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		append(append([]string{}, base...), "--local-mp-command-delay-ms", "-1"),
		append(append([]string{}, base...), "--local-mp-command-delay-ms", "1001"),
		{"--local-mp-command-delay-ms", "100"},
		{"--local-mp-join", "127.0.0.1:39031", "--map", "ashap plateau", "--local-mp-command-delay-ms", "0"},
	} {
		if _, err := parseFlags(args, io.Discard); err == nil {
			t.Fatalf("admitted invalid delay options %v", args)
		}
	}
}

func TestLocalMultiplayerConfigIsCommonAndExplicit(t *testing.T) {
	cs := &contentSet{profile: "retail", limits: content.RetailLimits()}
	first := Options{LocalMPListen: "127.0.0.1:39031", Map: "ashap plateau", Seed: -1, LocalMPCommandDelayMS: 150}
	second := first
	second.LocalMPListen, second.LocalMPJoin = "", first.LocalMPListen
	second.LocalMPCommandDelayMS = 0
	a, err := localMultiplayerConfig(first, cs, 3)
	if err != nil {
		t.Fatal(err)
	}
	b, err := localMultiplayerConfig(second, cs, 3)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest() != b.Digest() {
		t.Fatal("local seat changed configuration identity")
	}
	r := a.Request()
	if r.RuleName != "modern" || r.MapSchema != 3 || r.SimulationSeed != 7 || r.CRTSeed != 11 || r.CheatsAllowed || r.WatchingAllowed || len(r.Seats) != 2 {
		t.Fatalf("wrong fixed configuration: %+v", r)
	}
	for i, s := range r.Seats {
		wantID := session.MatchParticipantID{}
		wantID[0] = byte(i + 1)
		if s.Role != session.MatchRoleHuman || s.HostSeat != session.MatchHostNone || s.ComputerKind != 0 || s.Difficulty != 0 || len(s.AIParams) != 0 || s.Participant != wantID || s.BuilderOptions != session.RuleSetForMode(gameplay.Modern).Orders.DefaultBuilderOptions() {
			t.Fatalf("seat %d: %+v", i, s)
		}
	}
	if r.Seats[0].AllyGroup == r.Seats[1].AllyGroup || !r.PlayerView.FullMap || r.PlayerView.MinimumScale != 64 || r.PlayerView.MaximumScale != 2048 {
		t.Fatal("hostile seats or camera policy changed")
	}
	first.Seed = 29
	seeded, err := localMultiplayerConfig(first, cs, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := seeded.Request(); got.SimulationSeed != 29 || got.CRTSeed != 29 {
		t.Fatal("explicit seed not applied to both streams")
	}
	// An ordinary, unstamped test binary may play; this empty content is
	// refused for its content, not for the build.
	_, _, err = composeLocalMultiplayer(second, &contentSet{fs: &vfs.FS{}, profile: "retail", limits: content.RetailLimits()})
	if err == nil || strings.Contains(err.Error(), "stamped") || strings.Contains(err.Error(), "logical path binary") {
		t.Fatalf("unstamped launch: %v", err)
	}
}

type testLocalBattleDriver struct {
	commands      []session.HumanCommand
	pumps, closes int
	failure       error
}

func (d *testLocalBattleDriver) Pump() (bool, error) { d.pumps++; return false, d.failure }
func (d *testLocalBattleDriver) Submit(c session.HumanCommand) (uint64, error) {
	d.commands = append(d.commands, c)
	return uint64(len(d.commands)), nil
}
func (d *testLocalBattleDriver) Close() error { d.closes++; return nil }

func TestLocalMultiplayerCommandsResolveSelectionWithoutOptimisticAssignment(t *testing.T) {
	b, us := localBatchFixture(t)
	driver := &testLocalBattleDriver{}
	b.multiplayer = &battleMultiplayer{driver: driver}
	if err := b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{us[1].Handle}}}); err != nil {
		t.Fatal(err)
	}
	if err := b.DispatchOrderCommand(session.HumanOrderCommand{Code: 2}); err != nil {
		t.Fatal(err)
	}
	if err := b.DispatchGroupAssign(3); err != nil {
		t.Fatal(err)
	}
	if len(driver.commands) != 2 || !reflect.DeepEqual(driver.commands[0].Order.Handles, []pool.Handle{us[1].Handle}) || !reflect.DeepEqual(driver.commands[1].Group.Handles, []pool.Handle{us[1].Handle}) {
		t.Fatalf("unresolved commands: %+v", driver.commands)
	}
	if err := b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionClear}); err != nil {
		t.Fatal(err)
	}
	if err := b.DispatchGroupRecall(3, false); err != nil {
		t.Fatal(err)
	}
	if len(b.localState().SelectedRefs()) != 0 {
		t.Fatal("group membership appeared before its grant")
	}
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("network command was also enqueued locally")
	}
	shell := &gameShell{battle: b, builderOptions: settings.Defaults().BuilderOptions}
	next := shell.builderOptions
	next.Guard[0] = (next.Guard[0] + 1) % 3
	shell.setBuilderOptions(next)
	if len(driver.commands) != 3 || driver.commands[2].Kind != session.HumanBuilderOptions || driver.commands[2].BuilderOptions.Owner != b.sess.LocalOwner || shell.builderOptions != next {
		t.Fatal("builder preference bypassed submission or lost local preference")
	}
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("builder options enqueued locally")
	}
}

func TestLocalMultiplayerMenusAndClockCannotAdvanceWithoutGrant(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	d := &testLocalBattleDriver{}
	b.multiplayer = &battleMultiplayer{driver: d}
	b.sess.Clock.Requested, b.sess.Clock.Active = 10, 10
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	b.cl = cl
	cl.SetAsyncSimulation(true)
	defer cl.SetAsyncSimulation(false)
	b.syncSimulationMode(cl)
	if b.sim != nil {
		t.Fatal("online battle launched asynchronous wall-clock simulation")
	}
	b.applyGameSpeedSetting(settings.Settings{GameSpeed: 20})
	b.togglePause()
	b.setGameSpeed(5)
	b.openBattleMenu()
	if b.sess.Clock.Paused || b.battleState().Paused() || b.sess.Clock.Active != 10 || b.sess.Clock.Requested != 10 {
		t.Fatal("local preference changed online schedule")
	}
	for _, name := range []string{"SAVEGAME", "LOADGAME", "SAVEGAME\x00tail"} {
		b.activateBattleMenuButton(name, cl)
	}
	b.battleState().ShowExit()
	b.activateBattleMenuButton("RESTART", cl)
	if b.battleState().Modal() == ui.BattleModalRestart {
		t.Fatal("online restart dialog opened")
	}
	b.closeBattleMenu()
	b.openBattleMenu()
	before := b.sess.Clock.GlobalTick
	b.viewerStep(1, cl)
	if d.pumps != 1 || b.sess.Clock.GlobalTick != before {
		t.Fatal("modal update did not pump exactly once or stepped wall clock")
	}
	b.closeBattleMenu()
	c := NewBattleController(b, &scriptedMillisSource{samples: []uint32{0, 10000, 20000}})
	c.Step(BattleInputFrame{}, cl)
	c.Step(BattleInputFrame{}, cl)
	if d.pumps != 1 || b.sess.Clock.GlobalTick != before {
		t.Fatal("controller double pumped or released a wall-clock tick")
	}
	// Closed-world chat remains a visible refusal and never reaches local phase 1.
	b.dispatchLocalCommand("+dev")
	b.dispatchLocalCommand("+Now Film Chris Include Reload Assert")
	b.dispatchLocalCommand("+atm")
	b.dispatchLocalCommand("+spawn armcons")
	b.dispatchLocalCommand("+armcons")
	if b.developer.authorized || len(b.sess.PendingHumanCommands()) != 0 || len(d.commands) != 0 {
		t.Fatal("chat bypassed the network boundary")
	}
}

func TestLocalMultiplayerTransportFailureFreezesAndClosesOnce(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	sentinel := errors.New("test disconnect")
	d := &testLocalBattleDriver{failure: sentinel}
	b.multiplayer = &battleMultiplayer{driver: d}
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	b.cl = cl
	cl.MessageRing().TextLines = 10
	b.pumpLocalMultiplayer(cl)
	producer := cl.MessageRing().Producer
	b.pumpLocalMultiplayer(cl)
	if d.pumps != 1 || d.closes != 1 || b.multiplayer.failure != sentinel || cl.MessageRing().Producer != producer {
		t.Fatal("failure was retried or reported repeatedly")
	}
	if b.isResultVisible() || b.sess.GetResult().Ended {
		t.Fatal("disconnect awarded a result")
	}
	b.multiplayer.close()
	if d.closes != 1 {
		t.Fatal("close was not idempotent")
	}
	if _, err := b.submitHumanCommand(session.HumanCommand{Kind: session.HumanStop}); err == nil {
		t.Fatal("stopped transport accepted input")
	}
}

func TestLocalMultiplayerLocalResultWaitsForSharedEnd(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	d := &testLocalBattleDriver{}
	b.multiplayer = &battleMultiplayer{driver: d}
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	staging := b.sess.Snapshot.BeginWrite()
	staging.Result = frame.ResultView{Ended: true, Kind: "defeat"}
	if err := b.sess.Snapshot.Publish(1); err != nil {
		t.Fatal(err)
	}
	if b.isResultVisible() {
		t.Fatal("local defeat exposed transport teardown before shared end")
	}
	b.viewerStep(0, cl)
	if d.pumps != 1 || b.isResultVisible() {
		t.Fatal("local result prevented other seat's simulation from finishing")
	}
	// A hosted seat defeated while the others play on sees its result before
	// any relay completion (DESIGN_MULTIPLAYER §16.6.2); the shared end's wait
	// for completion is TestHostedRelayLatencyRetail's.
	b.multiplayer = &battleMultiplayer{driver: d, completed: func() bool { return false }}
	if !b.isResultVisible() {
		t.Fatal("hosted early defeat waited for the shared end")
	}
	b.multiplayer = nil
	if !b.isResultVisible() {
		t.Fatal("ordinary local result was suppressed")
	}
}

// fakeBackgroundFrames is a browser page's frame gate.
type fakeBackgroundFrames struct {
	idle, held bool
	holds      []bool
}

func (f *fakeBackgroundFrames) Idle() bool { return f.idle && !f.held }
func (f *fakeBackgroundFrames) Hold(held bool) {
	f.held = held
	f.holds = append(f.holds, held)
}

// The background step runs only while the page's frames are idle, and holds
// them for exactly its own duration, so no Update or Draw can start inside it
// (DESIGN_BROWSER_HOST §4 contract 10).
func TestOnlineBackgroundStepsOnlyBetweenHeldFrames(t *testing.T) {
	frames := &fakeBackgroundFrames{}
	steps := 0
	step := func() {
		steps++
		if !frames.held {
			t.Fatal("the background step ran with the frames free to start")
		}
	}
	if serviceOnlineBackground(frames, step) || steps != 0 || len(frames.holds) != 0 {
		t.Fatal("a presenting page ran the background step")
	}
	frames.idle = true
	if !serviceOnlineBackground(frames, step) || steps != 1 || !reflect.DeepEqual(frames.holds, []bool{true, false}) || frames.held {
		t.Fatalf("idle page: steps %d, holds %v", steps, frames.holds)
	}
	if serviceOnlineBackground(nil, step) || steps != 1 {
		t.Fatal("a page without a frame gate ran the background step")
	}
}

// A hidden page runs the online battle's granted ticks as the host step does,
// and drops their presentation events rather than playing them all once the
// page is shown again; the host step itself keeps them for its frame.
func TestOnlineBackgroundPumpsTheBattleAndDropsItsEvents(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	d := &testLocalBattleDriver{}
	b.multiplayer = &battleMultiplayer{driver: d}
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	b.cl = cl
	g := &gameShell{battle: b}
	b.shell = g
	raise := func(tick uint32) {
		staging := b.sess.Snapshot.BeginWrite()
		staging.Events = []frame.EventView{{Tick: tick}}
		if err := b.sess.Snapshot.Publish(tick); err != nil {
			t.Fatal(err)
		}
	}
	raise(1)
	if !g.onlineBackgroundStep(cl) || d.pumps != 1 || b.sess.Snapshot.PendingCommittedEvents() != 0 {
		t.Fatalf("background step: pumps %d, events kept %d", d.pumps, b.sess.Snapshot.PendingCommittedEvents())
	}
	raise(2)
	b.pumpLocalMultiplayer(cl)
	if d.pumps != 2 || b.sess.Snapshot.PendingCommittedEvents() != 1 {
		t.Fatal("the host step dropped events its frame presents")
	}
	// A failed transport leaves nothing to keep in step.
	d.failure = errors.New("test disconnect")
	if g.onlineBackgroundStep(cl) || d.pumps != 3 {
		t.Fatal("a failed battle still asks for background steps")
	}
	if g.onlineBackgroundStep(cl) || d.pumps != 3 {
		t.Fatal("a failed battle was pumped again")
	}
}

// Single-player battles and an idle menu keep waiting for the page's frames.
func TestOnlineBackgroundLeavesOtherGamesAlone(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	before := b.sess.Clock.GlobalTick
	g := &gameShell{battle: b}
	if g.onlineBackgroundStep(nil) || b.sess.Clock.GlobalTick != before {
		t.Fatal("a single-player battle advanced in the background")
	}
	g = onlineTestShell(t)
	if g.onlineBackgroundStep(nil) {
		t.Fatal("the main menu asked for background steps")
	}
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	if g.onlineBackgroundStep(nil) {
		t.Fatal("the chooser, with no room opening, asked for background steps")
	}
}

// An open room is followed while the page is hidden: a Start the host makes
// meanwhile reaches this seat without waiting for its frames.
func TestOnlineBackgroundFollowsTheLobby(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	stream := &fakeGrantStream{}
	fake := newFakeOnlineLobby(1, 0, 1)
	fake.battle = stream
	openTestLobby(t, g, fake, onlineTestCatalog())
	fake.state.Seats[2].Present = true
	if !g.onlineBackgroundStep(nil) || !g.online.room.state.Seats[2].Present {
		t.Fatal("a hidden lobby did not follow the room")
	}
	fake.state.Started, fake.state.Slot = true, 1
	if !g.onlineBackgroundStep(nil) || stream.closes != 1 || g.online == nil || !strings.Contains(g.online.status, "without your prepared battle") {
		t.Fatal("a hidden lobby did not take the host's Start")
	}
	if g.onlineBackgroundStep(nil) {
		t.Fatal("the chooser after leaving still asks for background steps")
	}
}
