//go:build retail

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// replayTestSettings points the settings file at a fresh default one without
// the battle opening, so a window battle starts ticking at once.
func replayTestSettings(t *testing.T) {
	t.Helper()
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	prefs := settings.Defaults()
	prefs.Presentation.Arrival = 0
	if err := prefs.Save(); err != nil {
		t.Fatal(err)
	}
}

// replayTestOptions parses a command line on the retail install.
func replayTestOptions(t *testing.T, args ...string) Options {
	t.Helper()
	var out bytes.Buffer
	// The software renderer: a capture through the modern one needs the
	// process's main thread.
	opts, err := parseFlags(append([]string{"--root", testsupport.RetailRoot(t), "--renderer", "classic"}, args...), &out)
	if err != nil {
		t.Fatalf("%v: %s", err, &out)
	}
	return opts
}

// replayTestContent mounts the content opts names.
func replayTestContent(t *testing.T, opts Options) *contentSet {
	t.Helper()
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// replayWindow composes the windowed --map or --replay battle the command
// line names, as the window would before its first frame, without opening
// one; step runs one 30 Hz host step that advances the scaled clock by units.
type replayWindow struct {
	shell  *gameShell
	cl     *client.Client
	millis shotMillisSource
}

func openReplayWindow(t *testing.T, opts Options, cs *contentSet) *replayWindow {
	t.Helper()
	previous := clPtr
	t.Cleanup(func() { clPtr = previous })
	shell, cl, err := newDirectBattleView(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	w := &replayWindow{shell: shell, cl: cl}
	t.Cleanup(func() { shell.teardownBattle(cl) })
	return w
}

func (w *replayWindow) step(units uint32) {
	if b := w.shell.battle; b != nil {
		b.millisSource = &w.millis
	}
	w.millis.step += units
	w.cl.Step(1.0 / 30)
}

// localCommanderOf is the local player's commander.
func localCommanderOf(t *testing.T, s *session.Session) *units.Unit {
	t.Helper()
	for _, u := range s.Units.IterSliced() {
		if u.Alive && u.Owner == s.LocalOwner && u.Def != nil && u.Def.Commander {
			return u
		}
	}
	t.Fatal("no local commander")
	return nil
}

// sideProduct is the commander's side's version of an Arm product.
func sideProduct(commander *units.Unit, arm, core string) string {
	if strings.HasPrefix(strings.ToLower(commander.Def.UnitName), "cor") {
		return core
	}
	return arm
}

// playScriptedWindowBattle plays a recorded window battle to ticks through
// the host's own input path: host steps of one to five scaled units, a
// paused step now and then whose command the paused-input boundary applies,
// the commander moved, stopped and set building, a factory ordered to
// produce. It returns the battle's final unit checksum.
func playScriptedWindowBattle(t *testing.T, w *replayWindow, ticks uint32) [32]byte {
	t.Helper()
	b := w.shell.battle
	s := b.sess
	commander := localCommanderOf(t, s)
	submit := func(c session.HumanCommand) {
		t.Helper()
		if _, err := b.submitHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	pausedCommands := 0
	for i := 0; s.Clock.GlobalTick < ticks; i++ {
		if i > 20000 {
			t.Fatalf("battle stalled at tick %d", s.Clock.GlobalTick)
		}
		units := uint32(1 + (i*7/5)%5)
		units = min(units, ticks-s.Clock.GlobalTick)
		paused := i%13 == 6
		if paused {
			b.applyBattleSchedule(ui.PauseIntent(true))
		}
		alive := commander.Alive
		switch {
		case alive && i == 30:
			x, z := commander.X+96<<16, commander.Z
			submit(session.HumanCommand{Kind: session.HumanMobileBuild, MobileBuild: session.HumanMobileBuildCommand{Builder: commander.Handle,
				Product: sideProduct(commander, "armsolar", "corsolar"), WX: x, WZ: z, WY: s.World.HeightAt(x, z)}})
		case alive && i == 200:
			x, z := commander.X-128<<16, commander.Z+64<<16
			submit(session.HumanCommand{Kind: session.HumanMobileBuild, MobileBuild: session.HumanMobileBuildCommand{Builder: commander.Handle,
				Product: sideProduct(commander, "armlab", "corlab"), WX: x, WZ: z, WY: s.World.HeightAt(x, z)}})
		case alive && i%9 == 2 && i > 600 || alive && paused:
			submit(session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Handles: []pool.Handle{commander.Handle}, Code: 2, Queued: i%2 == 0,
				Position: orders.ResolvePos{X: commander.X + numeric.Fixed(i%80-40)<<16, Y: commander.Y, Z: commander.Z + numeric.Fixed(i%50-25)<<16}}})
			if paused {
				pausedCommands++
			}
		case alive && i%17 == 5 && i > 600:
			submit(session.HumanCommand{Kind: session.HumanStop, Stop: session.HumanStopCommand{Handles: []pool.Handle{commander.Handle}}})
		case i%50 == 0:
			for _, u := range s.Units.IterSliced() {
				if u.Alive && u.Owner == s.LocalOwner && u.Def != nil && u.Def.Builder && !u.Def.Commander {
					submit(session.HumanCommand{Kind: session.HumanFactoryBuild, FactoryBuild: session.HumanFactoryBuildCommand{Builder: u.Handle,
						Product: sideProduct(commander, "armpw", "corak"), Count: 2}})
					break
				}
			}
		}
		before := s.Clock.GlobalTick
		w.step(units)
		if paused {
			if s.Clock.GlobalTick != before {
				t.Fatal("a paused host step ran a tick")
			}
			b.applyBattleSchedule(ui.PauseIntent(false))
		}
	}
	if pausedCommands == 0 {
		t.Fatal("no command was applied at a paused-input boundary")
	}
	return s.UnitStateChecksum()
}

// readReplay reads the replay's pump sizes and its commands.
func readReplay(t *testing.T, data []byte) (pumpSizes []uint32, commands []replay.Entry, checksums map[uint32][32]byte) {
	t.Helper()
	r, err := replay.NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	checksums = map[uint32][32]byte{}
	for {
		e, err := r.Next()
		if err == io.EOF {
			return pumpSizes, commands, checksums
		}
		if err != nil {
			t.Fatal(err)
		}
		switch e.Kind {
		case replay.EntryPumps:
			if !slices.Contains(pumpSizes, e.PumpTicks) {
				pumpSizes = append(pumpSizes, e.PumpTicks)
			}
		case replay.EntryCommand:
			commands = append(commands, e)
		case replay.EntryChecksum:
			checksums[e.Tick] = e.Sum
		}
	}
}

// onlyReplay is the one finished replay in dir.
func onlyReplay(t *testing.T, dir string) (replayListing, []byte) {
	t.Helper()
	list, err := listReplays(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("replays %+v: %v", list, err)
	}
	data, err := os.ReadFile(list[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	return list[0], data
}

// A window battle records through the host's own recording path — attached
// at --map battle entry, written by the host, finished at teardown — and the
// headless verifier plays it back with every checksum matched, whatever pump
// sizes and paused boundaries the host produced. The Modern computer thinks
// on its own goroutine (an asynchronous persona, mods/aikit) and its battle
// publishes the arrival's opening frame before its first tick.
func TestReplayWindowRecordingVerifiesRetail(t *testing.T) {
	ticks := uint32(3600)
	if testing.Short() {
		ticks = 900
	}
	for _, c := range []struct {
		name    string
		args    []string
		opening bool
	}{
		{"strict-classic", nil, false},
		{"strict-modern-async", []string{"--ai-player", "all=modern"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			replayTestSettings(t)
			dir := t.TempDir()
			opts := replayTestOptions(t, append([]string{"--map", "ashap plateau", "--seed", "7", "--gameplay", "strict-3.1", "--replay-dir", dir}, c.args...)...)
			cs := replayTestContent(t, opts)
			w := openReplayWindow(t, opts, cs)
			b := w.shell.battle
			if b.replay == nil {
				t.Fatal("the window battle is not recording")
			}
			if c.opening && !b.sess.PublishOpeningFrame() {
				t.Fatal("no opening frame")
			}
			started := time.Now()
			final := playScriptedWindowBattle(t, w, ticks)
			played := b.sess.Clock.GlobalTick
			elapsed := time.Since(started)
			computer := 0
			for _, u := range b.sess.Units.IterSliced() {
				if u.Alive && u.Owner != b.sess.LocalOwner {
					computer++
				}
			}
			if computer < 2 && !testing.Short() {
				t.Fatalf("the computer player built nothing: %d units", computer)
			}
			w.shell.teardownBattle(w.cl)

			listing, data := onlyReplay(t, dir)
			if listing.Incomplete || listing.End != replay.EndLeft || listing.FinalTick != played || listing.Header.Kind != replay.KindSkirmish {
				t.Fatalf("listing %+v", listing)
			}
			sizes, commands, _ := readReplay(t, data)
			if len(sizes) < 3 || len(commands) < 20 {
				t.Fatalf("pump sizes %v, %d commands", sizes, len(commands))
			}
			v, err := verifyReplay(data, cs)
			if err != nil {
				t.Fatal(err)
			}
			if v.Ticks != played || v.Verified != int(played/30) || v.Truncated || v.End != replay.EndLeft || v.Final != final {
				t.Fatalf("verification %+v of %d ticks", v, played)
			}
			t.Logf("%s: %d ticks in %s, %d computer units, %d commands, pump sizes %v, %d bytes; %s", c.name, played, elapsed.Round(time.Millisecond), computer, len(commands), sizes, len(data), v)
		})
	}
}

// Two seats play an online match through a loopback hosted relay, each
// recording its own relay stream: seat 1 through the command-line hosted
// path (startHostedMultiplayer), seat 0 as the online lobby's battle start
// records it (gameShell.recordOnlineReplay wrapping the relay client before
// the network statistics and the driver). A self-destruct ends the match;
// both replays end finished, verify headless, and hold the same command
// stream and checksums.
func TestReplayOnlineSeatsRecordTheSameStreamRetail(t *testing.T) {
	replayTestSettings(t)
	server, err := relay.ListenHosted("127.0.0.1:0", relay.HostedConfig{InsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	base := replayTestOptions(t, "--relay-address", server.Addr(), "--relay-insecure-loopback", "--map", "ashap plateau", "--seed", "11")
	cs := replayTestContent(t, base)
	var seats [2]*battleSession
	var dirs [2]string
	for seat := range seats {
		dirs[seat] = t.TempDir()
		shell := &gameShell{opts: Options{ReplayDir: dirs[seat]}, cs: cs}
		if seat == 0 {
			// The lobby's prepared battle (composeOnlineReady) for the
			// play test's configuration, and its seat creating the room.
			cat, err := cs.compileCatalog(nil)
			if err != nil {
				t.Fatal(err)
			}
			schema, err := session.OnlineMapSchema(cs.fs, cat, base.Map, 2)
			if err != nil {
				t.Fatal(err)
			}
			config, err := localMultiplayerConfig(base, cs, schema)
			if err != nil {
				t.Fatal(err)
			}
			sess, inputs, identity, err := composeOnlineMatch(cs, cat, config, 0)
			if err != nil {
				t.Fatal(err)
			}
			p := &onlinePrepared{sess: sess, inputs: inputs, config: config, identity: onlineIdentityDigest(identity)}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			var conn lockstep.Client
			c, room, err := relay.DialHosted(ctx, base.RelayAddress, "", relay.LocalHello{Seat: 0, Identity: identity, InitialChecksum: sess.UnitStateChecksum()}, relay.HostedDialOptions{InsecureLoopback: true})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			conn = c
			// enterOnlineBattle with its recording line.
			conn = shell.recordOnlineReplay(p, conn)
			if _, ok := conn.(reportingOnlineReplayClient); !ok {
				t.Fatalf("seat 0 is not recording: %T", conn)
			}
			driver, err := lockstep.NewPacedDriver(sess, newOnlineNetStats(conn, 0, 2))
			if err != nil {
				t.Fatal(err)
			}
			seats[seat] = &battleSession{sess: sess, cs: cs, shell: shell, multiplayer: &battleMultiplayer{driver: driver, completed: driver.Completed}}
			base.RelayRoom = room
			continue
		}
		battle, identity, err := composeLocalMultiplayer(base, cs)
		if err != nil {
			t.Fatal(err)
		}
		b := &battleSession{sess: battle.Session, cs: cs, shell: shell}
		seats[seat] = b
		if err := b.startHostedMultiplayer(base, identity); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, b := range seats {
			b.multiplayer.close()
		}
	}()
	var commanders [2]pool.Handle
	for seat, b := range seats {
		commanders[seat] = localCommanderOf(t, b.sess).Handle
	}
	host := newProbeHost(60)
	defer host.ticker.Stop()
	const play = uint32(450)
	deadline := time.Now().Add(60 * time.Second)
	ended := false
	for !seats[0].multiplayer.completed() || !seats[1].multiplayer.completed() {
		if time.Now().After(deadline) {
			t.Fatalf("match stalled at ticks %d/%d", seats[0].sess.Clock.GlobalTick, seats[1].sess.Clock.GlobalTick)
		}
		for range host.steps() {
			for seat, b := range seats {
				d := b.multiplayer.driver
				advanced, err := d.Pump()
				if err != nil {
					t.Fatal(err)
				}
				for _, receipt := range b.sess.DrainCommandReceipts() {
					if receipt.Outcome == session.CommandRejected {
						t.Fatal(receipt.Diagnostic)
					}
				}
				tick := b.sess.Clock.GlobalTick
				if !advanced || b.sess.OnlineBattleEnded() {
					continue
				}
				u := b.sess.Units.Unit(commanders[seat])
				switch {
				case u == nil || !u.Alive:
				case tick >= play && seat == 1 && !ended:
					ended = true
					if _, err := d.Submit(session.HumanCommand{Kind: session.HumanSelfDestruct, SelfDestruct: session.HumanSelfDestructCommand{Handles: []pool.Handle{u.Handle}}}); err != nil {
						t.Fatal(err)
					}
				case tick%11 == uint32(seat)*5 && tick < play:
					if _, err := d.Submit(session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Handles: []pool.Handle{u.Handle}, Code: 2,
						Position: orders.ResolvePos{X: u.X + numeric.Fixed(int32(tick%64)-32)<<16, Y: u.Y, Z: u.Z + numeric.Fixed(int32(tick%48)-24)<<16}}}); err != nil {
						t.Fatal(err)
					}
				case tick%37 == 0 && tick < play:
					if _, err := d.Submit(session.HumanCommand{Kind: session.HumanStop, Stop: session.HumanStopCommand{Handles: []pool.Handle{u.Handle}}}); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	finals := [2][32]byte{seats[0].sess.UnitStateChecksum(), seats[1].sess.UnitStateChecksum()}
	if finals[0] != finals[1] {
		t.Fatal("the seats ended in different states")
	}
	for _, b := range seats {
		b.multiplayer.close()
	}
	var streams [2][]replay.Entry
	var sums [2]map[uint32][32]byte
	var paths [2]string
	for seat, dir := range dirs {
		listing, data := onlyReplay(t, dir)
		paths[seat] = listing.Path
		if listing.Incomplete || listing.End != replay.EndFinished || listing.Header.Kind != replay.KindOnlineSkirmish || listing.Header.LocalSeat != uint8(seat) {
			t.Fatalf("seat %d listing %+v", seat, listing)
		}
		_, streams[seat], sums[seat] = readReplay(t, data)
		v, err := verifyReplay(data, cs)
		if err != nil {
			t.Fatalf("seat %d: %v", seat, err)
		}
		if v.Ticks != listing.FinalTick || v.Verified != int(v.Ticks/30) || v.Final != finals[seat] {
			t.Fatalf("seat %d verification %+v", seat, v)
		}
		t.Logf("seat %d: %d commands, %d bytes; %s", seat, len(streams[seat]), len(data), v)
	}
	if len(streams[0]) < 30 || !slices.EqualFunc(streams[0], streams[1], func(a, b replay.Entry) bool {
		return a.Tick == b.Tick && a.Seat == b.Seat && a.Position == b.Position && bytes.Equal(a.Payload, b.Payload)
	}) {
		t.Fatalf("the seats recorded different command streams: %d and %d commands", len(streams[0]), len(streams[1]))
	}
	if len(sums[0]) == 0 || !mapsEqual(sums[0], sums[1]) {
		t.Fatal("the seats recorded different checksums")
	}
	// Seat 0's recording plays in the window, from seat 2's view part of
	// the way, to the same end.
	w := openReplayWindow(t, replayTestOptions(t, "--replay", paths[0]), cs)
	b := w.shell.battle
	p := b.replayPlayback()
	if p == nil || len(p.Perspectives()) != 3 || p.Perspectives()[1].Seat != 1 || !p.Perspectives()[2].FullMap || p.Perspective() != 0 {
		t.Fatalf("online playback perspectives %+v", p.Perspectives())
	}
	if err := p.SetPerspective(1); err != nil {
		t.Fatal(err)
	}
	p.SkipTo(300)
	for i := 0; p.Tick() < 300; i++ {
		if i > 100 {
			t.Fatal("the skip stalled")
		}
		w.step(1)
	}
	if cur := b.sess.Snapshot.Current(); cur.Fog.Valid || b.sess.LocalOwner != 0 {
		t.Fatal("seat 2's view moved the simulation's owner or kept seat 1's fog")
	}
	p.SkipTo(p.FinalTick())
	for i := 0; !p.Ended(); i++ {
		if i > 200 {
			t.Fatal("the skip did not reach the end")
		}
		w.step(1)
	}
	if s := p.Status(); s.Mismatch != nil || s.Err != nil || s.End != replay.EndFinished || s.Verified != int(p.FinalTick()/30) || b.sess.UnitStateChecksum() != finals[0] || b.isResultVisible() {
		t.Fatalf("online playback ended %+v", s)
	}
}

func mapsEqual(a, b map[uint32][32]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// A --shot capture records through its windowed composition (--record-replay)
// and the recording verifies headless, through the command line too.
func TestReplayShotRecordingVerifiesRetail(t *testing.T) {
	replayTestSettings(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.nlreplay")
	opts := replayTestOptions(t, "--map", "ashap plateau", "--seed", "5", "--shot", filepath.Join(dir, "shot.png"), "--shot-ticks", "300", "--record-replay", path, "--ai-player", "all=modern")
	cs := replayTestContent(t, opts)
	previous := clPtr
	defer func() { clPtr = previous }()
	if err := runShot(opts, cs); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := verifyReplay(data, cs)
	if err != nil {
		t.Fatal(err)
	}
	if v.Ticks != 300 || v.Verified != 10 || v.End != replay.EndLeft || v.Truncated {
		t.Fatalf("verification %+v", v)
	}
	// The command line's verifier: the replay picks its own content.
	out, errOut := filepath.Join(dir, "out.txt"), filepath.Join(dir, "err.txt")
	if code := runReplayCommand(t, out, errOut, "--root", testsupport.RetailRoot(t), "--verify-replay", path); code != 0 {
		t.Fatalf("--verify-replay exited %d: %s", code, readText(t, errOut))
	}
	if got := readText(t, out); !strings.Contains(got, "replay verified: skirmish on ashap plateau, 300 ticks, 10 checksums matched, complete, left") {
		t.Fatalf("summary %q", got)
	}
	// A recording this build computes differently fails, naming the tick.
	broken := rewriteChecksum(t, data, 150)
	brokenPath := filepath.Join(dir, "broken.nlreplay")
	if err := os.WriteFile(brokenPath, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	var mismatch *replay.Mismatch
	if _, err := verifyReplay(broken, cs); !errors.As(err, &mismatch) || mismatch.Tick != 150 {
		t.Fatalf("damaged checksum: %v", err)
	}
	if code := runReplayCommand(t, out, errOut, "--root", testsupport.RetailRoot(t), "--verify-replay", brokenPath); code == 0 || !strings.Contains(readText(t, errOut), "logical path tick 150") {
		t.Fatalf("--verify-replay exited %d: %s", code, readText(t, errOut))
	}
}

func runReplayCommand(t *testing.T, outPath, errPath string, args ...string) int {
	t.Helper()
	var parse bytes.Buffer
	opts, err := parseFlags(args, &parse)
	if err != nil {
		t.Fatalf("%v: %s", err, &parse)
	}
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	errOut, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	defer errOut.Close()
	return runOptions(opts, out, errOut)
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// rewriteChecksum re-records a replay with its checksum at tick changed.
func rewriteChecksum(t *testing.T, data []byte, tick uint32) []byte {
	t.Helper()
	r, err := replay.NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w, err := replay.NewWriter(&out, r.Header())
	if err != nil {
		t.Fatal(err)
	}
	for {
		e, err := r.Next()
		if err == io.EOF {
			return out.Bytes()
		}
		if err != nil {
			t.Fatal(err)
		}
		switch e.Kind {
		case replay.EntryCommand:
			err = w.Command(e.Tick, e.Seat, e.Position, e.Payload)
		case replay.EntryChecksum:
			if e.Tick == tick {
				e.Sum[0] ^= 1
			}
			err = w.Checksum(e.Tick, e.Sum)
		case replay.EntryPumps:
			at := e.Tick - e.Pumps*e.PumpTicks
			for range e.Pumps {
				at += e.PumpTicks
				if err = w.Pump(at, int(e.PumpTicks)); err != nil {
					break
				}
			}
		case replay.EntryEnd:
			err = w.Close(e.Reason)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A displayless run recorded with --record-replay composes for window
// playback (--replay) and plays there as the recorded seat: orders are
// refused, a paused playback holds and shows a new perspective at once, the
// speeds and a skip ahead run whole recorded pumps, and the playback ends
// with every checksum matched.
func TestReplayHeadlessRecordingPlaysInTheWindowRetail(t *testing.T) {
	replayTestSettings(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "headless.nlreplay")
	opts := replayTestOptions(t, "--map", "ashap plateau", "--seed", "3", "--headless", "--ticks", "450", "--record-replay", path, "--report", filepath.Join(dir, "report.json"))
	cs := replayTestContent(t, opts)
	if err := runHeadless(opts, cs, io.Discard); !errors.Is(err, errHeadlessTickLimit) {
		t.Fatalf("headless run: %v", err)
	}
	w := openReplayWindow(t, replayTestOptions(t, "--replay", path), cs)
	b := w.shell.battle
	p := b.replayPlayback()
	if p == nil || !b.onlineBattle() || b.replay != nil {
		t.Fatal("the window did not open the replay as a playback")
	}
	if p.FinalTick() != 450 || len(p.Perspectives()) != 2 || !p.Perspectives()[1].FullMap || p.Perspective() != 0 {
		t.Fatalf("playback of %d ticks, perspectives %+v at %d", p.FinalTick(), p.Perspectives(), p.Perspective())
	}
	commander := localCommanderOf(t, b.sess)
	if _, err := b.submitHumanCommand(session.HumanCommand{Kind: session.HumanStop, Stop: session.HumanStopCommand{Handles: []pool.Handle{commander.Handle}}}); !errors.Is(err, errReplayPlaybackCommand) {
		t.Fatalf("a playback took an order: %v", err)
	}
	for range 60 {
		w.step(1)
	}
	if tick := p.Tick(); tick < 50 || tick > 70 {
		t.Fatalf("1× played %d ticks in 60 host steps", tick)
	}
	p.SetPaused(true)
	held := p.Tick()
	for range 10 {
		w.step(1)
	}
	if p.Tick() != held || !b.battleState().Paused() {
		t.Fatal("a paused playback ran")
	}
	if err := p.SetPerspective(1); err != nil {
		t.Fatal(err)
	}
	w.step(1)
	cur := b.sess.Snapshot.Current()
	if cur.Fog.Valid || len(cur.Visibility.Visible) == 0 || slices.Contains(cur.Visibility.Visible, 0) {
		t.Fatal("the full-map perspective did not show while paused")
	}
	p.SetPaused(false)
	p.SetSpeed(len(replaySpeeds) - 1)
	before := p.Tick()
	for range 5 {
		w.step(1)
	}
	if ran := p.Tick() - before; ran < 35 || ran > 45 {
		t.Fatalf("8× played %d ticks in 5 host steps", ran)
	}
	p.SkipTo(p.FinalTick())
	b.applyBattleSchedule(ui.PauseIntent(true))
	held = p.Tick()
	for range 3 {
		w.step(1)
	}
	if target, skipping := p.Skipping(); p.Tick() != held || !skipping || target != p.FinalTick() {
		t.Fatalf("menu did not hold active skip: tick %d, held %d, target %d, skipping %v", p.Tick(), held, target, skipping)
	}
	b.applyBattleSchedule(ui.PauseIntent(false))
	for i := 0; !p.Ended(); i++ {
		if i > 200 {
			t.Fatal("the skip did not reach the end")
		}
		w.step(1)
	}
	s := p.Status()
	if s.Mismatch != nil || s.Err != nil || s.Truncated || s.End != replay.EndLeft || s.Verified != 15 || p.Tick() != 450 {
		t.Fatalf("playback ended %+v at %d", s, p.Tick())
	}
	if p.Message() != "End of replay: the player left here." || b.isResultVisible() {
		t.Fatalf("end state %q", p.Message())
	}
}

// What recording costs the simulation thread: the 30-tick unit checksum, the
// one cost that grows with the battle, and the Writer's appends, measured on
// a battle a minute in. Logged only.
func TestReplayRecordingCostRetail(t *testing.T) {
	if testing.Short() {
		t.Skip("measurement")
	}
	replayTestSettings(t)
	opts := replayTestOptions(t, "--map", "ashap plateau", "--seed", "9", "--ai-player", "all=modern")
	cs := replayTestContent(t, opts)
	measure := func(record bool) (time.Duration, int) {
		request, err := directMapBattleRequest(opts, cs, newBattleSeedSource(opts))
		if err != nil {
			t.Fatal(err)
		}
		battle, err := composeAuthoritativeBattle(request)
		if err != nil {
			t.Fatal(err)
		}
		s := battle.Session
		if _, err := installHeadlessModelTextureRegistry(s, cs.unmappedMount, cs.presentation.TeamLogos); err != nil {
			t.Fatal(err)
		}
		var rec *replayRecording
		if record {
			if rec, err = startSinglePlayerReplay(replayTarget{path: filepath.Join(t.TempDir(), "cost.nlreplay")}, request.value, cs, s); err != nil {
				t.Fatal(err)
			}
		}
		started := time.Now()
		for s.Clock.GlobalTick < 3600 {
			s.ExecuteStep(s.PrepareStep(s.Clock.ScaledAnchor + int32(1+s.Clock.GlobalTick%3)))
		}
		elapsed := time.Since(started)
		size := 0
		if rec != nil {
			path, err := rec.finish(replay.EndLeft, s.Clock.GlobalTick)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			size = int(info.Size())
		}
		const n = 200
		at := time.Now()
		for range n {
			s.UnitStateChecksum()
		}
		t.Logf("record=%v: 3600 ticks in %s (%.1f µs/tick), %d units, checksum %.1f µs, file %d bytes",
			record, elapsed.Round(time.Millisecond), float64(elapsed.Microseconds())/3600, len(s.Units.IterSliced()), float64(time.Since(at).Microseconds())/n, size)
		return elapsed, size
	}
	for range 2 {
		measure(false)
		measure(true)
	}
	var sink replaySink
	w, err := replay.NewWriter(&sink, fileTestHeader(replay.KindSkirmish, "x", time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	const n = 100000
	at := time.Now()
	tick := uint32(0)
	for i := range n {
		k := 1 + i%3
		tick += uint32(k)
		_ = w.Pump(tick, k)
	}
	t.Logf("Writer.Pump %.0f ns per pump", float64(time.Since(at).Nanoseconds())/n)
}

// The menus' battles record too: a skirmish started from SKIRMISH and a
// Survival battle, each composed on the loading screen's goroutine with the
// recorder attached there and handed to the battle at adoption, end as
// finished-or-left replays that verify headless.
func TestReplayMenuBattlesRecordRetail(t *testing.T) {
	replayTestSettings(t)
	dir := t.TempDir()
	opts := replayTestOptions(t, "--replay-dir", dir, "--seed", "13")
	cs := replayTestContent(t, opts)
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	previous := clPtr
	clPtr = cl
	defer func() { clPtr = previous }()
	defer shell.teardownBattle(cl)
	shell.ensureRetailSkirmishControllers()
	shell.setup.MapName = "ashap plateau"
	shell.setup.NumPlayers = 2
	shell.retailControllers[0], shell.retailControllers[1] = 1, 2
	shell.setup.Players[0].AllyGroup, shell.setup.Players[1].AllyGroup = 0, 1
	adopt := func() *battleSession {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		for shell.battle == nil {
			if time.Now().After(deadline) || shell.loading == nil {
				t.Fatal("the battle did not load")
			}
			shell.stepLoading(1.0 / 30)
			time.Sleep(5 * time.Millisecond)
		}
		if shell.battle.replay == nil {
			t.Fatal("the menu battle is not recording")
		}
		return shell.battle
	}
	play := func(b *battleSession, ticks uint32) uint32 {
		t.Helper()
		millis := &shotMillisSource{}
		b.millisSource = millis
		commander := localCommanderOf(t, b.sess)
		for i := 0; b.sess.Clock.GlobalTick < ticks; i++ {
			if i%20 == 3 && commander.Alive {
				if _, err := b.submitHumanCommand(session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Handles: []pool.Handle{commander.Handle}, Code: 2,
					Position: orders.ResolvePos{X: commander.X + numeric.Fixed(i%60-30)<<16, Y: commander.Y, Z: commander.Z + 16<<16}}}); err != nil {
					t.Fatal(err)
				}
			}
			millis.step += uint32(1 + i%4)
			b.viewerStep(1.0/30, cl)
			if i > 2000 {
				t.Fatal("the battle stalled")
			}
		}
		return b.sess.Clock.GlobalTick
	}
	shell.startBattleLoad(shell.setup.MapName)
	skirmishTicks := play(adopt(), 300)

	cfg := shell.survivalConfig()
	request, err := skirmishBattleRequest(shell.opts, shell.cs, cfg, headless.ScenarioSurvival, nil, newBattleSeedSource(shell.opts))
	if err != nil {
		t.Fatal(err)
	}
	shell.lastBattleSurvival = true
	// The skirmish's recording ends when the next battle replaces it.
	shell.beginFreshBattleLoad(cfg.MapName, modeMenuSkirmish, request, nil)
	survivalTicks := play(adopt(), 450)
	shell.teardownBattle(cl)

	list, err := listReplays(dir)
	if err != nil || len(list) != 2 {
		t.Fatalf("replays %+v: %v", list, err)
	}
	for _, l := range list {
		want := map[replay.Kind]uint32{replay.KindSkirmish: skirmishTicks, replay.KindSurvival: survivalTicks}[l.Header.Kind]
		if l.Incomplete || l.End != replay.EndLeft || l.FinalTick != want || want == 0 {
			t.Fatalf("listing %+v", l)
		}
		data, err := os.ReadFile(l.Path)
		if err != nil {
			t.Fatal(err)
		}
		v, err := verifyReplay(data, cs)
		if err != nil {
			t.Fatalf("%s: %v", l.Name, err)
		}
		if v.Ticks != want || v.Verified != int(want/30) {
			t.Fatalf("%s: %+v", l.Name, v)
		}
		t.Logf("%s: %s", l.Name, v)
	}
}
