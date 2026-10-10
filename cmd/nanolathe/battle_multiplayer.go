package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// The host alone owns transport. Neither a local menu nor a wall-clock budget
// can release a tick of this two-human play test (DESIGN_MULTIPLAYER §16.4.2).
type localBattleDriver interface {
	Pump() (bool, error)
	Submit(session.HumanCommand) (uint64, error)
	Close() error
}

type battleMultiplayer struct {
	driver    localBattleDriver
	relay     *relay.LocalRelay
	failure   error
	completed func() bool // Hosted results wait for explicit relay agreement.
	// net measures a hosted battle for the overlay and the summary.
	net *onlineNetStats
	// dropped holds the presentation events of ticks a hidden page ran,
	// between a background pump and the next (pumpBackground).
	dropped []frame.EventView
}

func (o Options) localMultiplayer() bool    { return o.LocalMPListen != "" || o.LocalMPJoin != "" }
func (o Options) multiplayerPlaytest() bool { return o.localMultiplayer() || o.RelayAddress != "" }

func localMultiplayerError(path, expected string) error {
	return fmt.Errorf("nanolathe: multiplayer play test refused: logical path %s, providers searched [two-human play test], expected %s", path, expected)
}

func validateLocalMultiplayerOptions(o Options) error {
	if o.LocalMPCommandDelayMS < 0 || o.LocalMPCommandDelayMS > 1000 || o.LocalMPCommandDelayMS != 0 && o.LocalMPListen == "" {
		return localMultiplayerError("command delay", "0..1000 milliseconds on the local listener")
	}
	if o.RelayAddress == "" && (o.RelayRoom != "" || o.RelayCA != "" || o.RelayInsecureLoopback) {
		return localMultiplayerError("relay options", "--relay-address host:port")
	}
	if !o.multiplayerPlaytest() {
		return nil
	}
	if o.LocalMPListen != "" && o.LocalMPJoin != "" {
		return localMultiplayerError("command line", "one of --local-mp-listen or --local-mp-join")
	}
	if o.RelayAddress != "" {
		if o.localMultiplayer() {
			return localMultiplayerError("transport", "one local or hosted relay")
		}
		if err := validateHostedAddress(o); err != nil {
			return err
		}
	} else {
		address := o.LocalMPListen
		if address == "" {
			address = o.LocalMPJoin
		}
		addr, err := netip.ParseAddrPort(address)
		if err != nil || !addr.Addr().IsLoopback() || addr.Addr().Zone() != "" || addr.Port() == 0 {
			return localMultiplayerError(address, "a numeric loopback address and nonzero port, for example 127.0.0.1:39031")
		}
	}
	if strings.TrimSpace(o.Map) == "" {
		return localMultiplayerError("map", "--map naming the same skirmish map on both clients")
	}
	if o.GameplaySet && o.Gameplay != gameplay.Modern || len(o.GameplayOverrides) != 0 || len(o.ComputerAI) != 0 || len(o.AIArgs) != 0 || o.UnitLimit != 0 {
		return localMultiplayerError("configuration", "the fixed Modern two-human configuration without AI, gameplay overrides or unit-limit overrides")
	}
	if o.Mod != "" && o.Mod != "none" || o.ModConfig != "" || o.InstallMod != "" || len(o.Roots) > 1 {
		return localMultiplayerError("content", "base content without mods, a config or a manual root stack")
	}
	if len(o.MutatorArgs) != 0 || !o.Mutators.IsZero() || !o.Restrictions.IsZero() {
		return localMultiplayerError("content transformations", "no mutators or unit restrictions")
	}
	if o.Survival || o.Mission != "" || o.LoadSave != "" || o.Headless || o.ListInstalls || o.CheckInstall || o.Ticks != 0 || o.Report != "" || o.Shot != "" || o.ShotModel != "" || o.ShotDebris != "" || o.ShotUnitViewer != "" || o.Film != "" || o.NLShot != "" || o.WalkPreview != "" || o.LiveTrace != "" || o.LiveScene != "" || o.LiveSpeed != 0 || o.BattleBenchmark != "" || o.BenchmarkCapture != "" {
		return localMultiplayerError("entry", "an ordinary map window without Survival, mission, save, headless, capture or benchmark options")
	}
	return nil
}

// Reject explicit probe options even when their argument happens to equal its
// default. Presentation-only window options remain available.
func localMultiplayerFlagAllowed(name string) bool {
	for _, prefix := range []string{"shot", "film", "nl-shot", "walk-preview", "live-", "benchmark", "survival"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	switch name {
	case "battle-benchmark", "headless", "ticks", "mission", "difficulty", "load-save", "report", "list-installs", "check-install", "install-mod", "mutator", "restrict", "ai", "ai-player", "gameplay-feature", "unit-limit", "cpuprofile", "memprofile", "profile-seconds", "replay", "verify-replay", "record-replay":
		return false
	}
	return true
}

// localMultiplayerConfig is the command-line play test's configuration: the
// --map of both clients, seeds 7/11 or both --seed, and base content with no
// transformations (DESIGN_MULTIPLAYER §16.4.2).
func localMultiplayerConfig(o Options, cs *contentSet, schema uint32) (session.EffectiveMatchConfig, error) {
	spec := onlineMatchSpec{mapName: o.Map, simSeed: 7, crtSeed: 11}
	if o.Seed >= 0 {
		spec.simSeed, spec.crtSeed = uint32(o.Seed), uint32(o.Seed)
	}
	return onlineMatchConfig(spec, cs, schema)
}

// composeLocalMultiplayer composes the command-line play test's battle,
// prepared for its first grant, and the identity its hello reports.
func composeLocalMultiplayer(o Options, cs *contentSet) (headless.FreshBattle, netproto.Identity, error) {
	var empty headless.FreshBattle
	var identity netproto.Identity
	if err := validateLocalMultiplayerOptions(o); err != nil {
		return empty, identity, err
	}
	if cs == nil || cs.fs == nil {
		return empty, identity, unavailableBattleContentError()
	}
	if cs.mod != nil || cs.config != nil || cs.manualRoots {
		return empty, identity, localMultiplayerError("mounted content", "base content with no mod or config")
	}
	// Any build may play: command-line rooms start without a rehearsal, and
	// the 30-tick unit checksum stops a match whose seats diverge.
	cat, err := cs.compileCatalog(nil)
	if err != nil {
		return empty, identity, err
	}
	schema, err := session.OnlineMapSchema(cs.fs, cat, o.Map, 2)
	if err != nil {
		return empty, identity, err
	}
	config, err := localMultiplayerConfig(o, cs, schema)
	if err != nil {
		return empty, identity, err
	}
	var seat uint8
	if o.LocalMPJoin != "" || o.RelayRoom != "" {
		seat = 1
	}
	sess, _, identity, err := composeOnlineMatch(cs, cat, config, seat)
	if err != nil {
		return empty, identity, err
	}
	return headless.FreshBattle{Session: sess, Kind: headless.ScenarioSkirmish}, identity, nil
}

// startLocalMultiplayer connects a battle composeLocalMultiplayer already
// prepared and entered.
func (b *battleSession) startLocalMultiplayer(o Options, identity netproto.Identity) error {
	if o.RelayAddress != "" {
		return b.startHostedMultiplayer(o, identity)
	}
	mp := &battleMultiplayer{}
	b.multiplayer = mp
	address := o.LocalMPJoin
	if o.LocalMPListen != "" {
		var r *relay.LocalRelay
		var err error
		if o.LocalMPCommandDelayMS == 0 {
			r, err = relay.ListenLocal(o.LocalMPListen)
		} else {
			r, err = relay.ListenLocalWithCommandDelay(o.LocalMPListen, time.Duration(o.LocalMPCommandDelayMS)*time.Millisecond)
		}
		if err != nil {
			return err
		}
		mp.relay, address = r, r.Addr()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := relay.DialLocal(ctx, address, relay.LocalHello{Seat: b.sess.LocalOwner, Identity: identity, InitialChecksum: b.sess.UnitStateChecksum()})
	if err != nil {
		mp.close()
		return err
	}
	mp.driver, err = lockstep.NewLocalDriver(b.sess, connection)
	if err != nil {
		_ = connection.Close()
		mp.close()
		return err
	}
	b.onlineNotice(fmt.Sprintf("Local multiplayer seat %d: waiting for both clients at %s", b.sess.LocalOwner+1, address))
	if o.LocalMPCommandDelayMS != 0 {
		b.onlineNotice(fmt.Sprintf("Responsiveness test: +%d ms order delay for both seats; network timing unchanged", o.LocalMPCommandDelayMS))
	}
	return nil
}

// onlineBattle reports a battle the host does not step from its clock and
// that takes no local pause, save, load, restart or cheat: an online battle,
// and a replay playback, which runs recorded pumps the way an online battle
// runs granted ticks (replay_playback.go).
func (b *battleSession) onlineBattle() bool {
	return b != nil && (b.multiplayer != nil || b.playback != nil || b.sess != nil && b.sess.OnlineCommandContext())
}

func (m *battleMultiplayer) close() {
	if m == nil {
		return
	}
	if m.driver != nil {
		_ = m.driver.Close()
		m.driver = nil
	}
	if m.relay != nil {
		_ = m.relay.Close()
		m.relay = nil
	}
}

func (b *battleSession) onlineNotice(message string) {
	fmt.Fprintln(os.Stderr, message)
	if ring := b.messageRing(); ring != nil {
		ring.Append(message, 4, 0, 10, b.currentTick())
	}
}

func (b *battleSession) pumpLocalMultiplayer(cl *client.Client) {
	if b == nil || b.multiplayer == nil || b.multiplayer.driver == nil || b.multiplayer.failure != nil {
		return
	}
	// A browser page that stops presenting frames keeps pumping from its
	// background step (online_browser_js.go); native hosts have none.
	watchOnlineBackground(b.shell, cl)
	advanced, err := b.multiplayer.driver.Pump()
	b.multiplayer.net.observe(b.sess, time.Now())
	for _, receipt := range b.sess.DrainCommandReceipts() {
		if receipt.Stamp.Seat == b.sess.LocalOwner && receipt.Outcome == session.CommandRejected {
			b.onlineNotice(receipt.Diagnostic)
		}
	}
	if err != nil {
		b.multiplayer.failure = err
		b.multiplayer.close()
		b.onlineNotice("Multiplayer stopped: " + err.Error())
	}
	if advanced {
		b.applyPublishedCamera(b.sess.Snapshot.Current())
		b.syncLocalInterface()
		b.noteTickTiming()
		if cl != nil {
			cl.ObserveCommittedTick()
			cl.NoteTicksReleased(1)
		}
	}
}

func (b *battleSession) submitLocalMultiplayer(c session.HumanCommand) (uint64, error) {
	if b.multiplayer == nil || b.multiplayer.driver == nil {
		return 0, localMultiplayerError("command", "a connected, running local relay")
	}
	return b.multiplayer.driver.Submit(c)
}

// ---------------------------------------------------------------------------
// Online play while a browser page is hidden (DESIGN_BROWSER_HOST §4
// contract 10). The relay holds every seat within 30 ticks of the slowest
// acknowledgement and ends a match that makes no progress for 10 seconds
// (DESIGN_MULTIPLAYER §16.5.1–§16.5.2), and a browser stops animation frames,
// and with them every Update and Draw, while its page is hidden. A seat in the
// lobby or an online battle therefore gets a background step whenever its
// page presents no frames: it runs exactly what the host step would have run
// for the room, never at the same time as a frame.

// onlineBackgroundFrames is the page's frame loop as the background step sees
// it (online_browser_js.go).
type onlineBackgroundFrames interface {
	// Idle reports that no frame is running and none is being presented: the
	// page is hidden, or its animation frames have stopped.
	Idle() bool
	// Hold keeps the loop from starting a frame while held.
	Hold(bool)
}

// serviceOnlineBackground runs step while the page's frames are idle, holding
// them so that no Update or Draw can start until step returns. It reports
// whether step ran.
func serviceOnlineBackground(frames onlineBackgroundFrames, step func()) bool {
	if frames == nil || !frames.Idle() {
		return false
	}
	frames.Hold(true)
	defer frames.Hold(false)
	step()
	return true
}

// onlineBackgroundStep is a hidden page's host step for the shell's online
// game, and reports whether one remains to keep in step. An online battle
// runs its granted ticks; an open room is followed, so a Start the host makes
// meanwhile enters the battle. Anything else — the main menu, a single-player
// battle — waits for the page's frames as it always has.
func (g *gameShell) onlineBackgroundStep(cl *client.Client) bool {
	if g == nil {
		return false
	}
	if b := g.battle; b != nil {
		if b.multiplayer == nil {
			return false
		}
		b.pumpBackground(cl)
		return b.multiplayer.driver != nil && b.multiplayer.failure == nil
	}
	if s := g.online; s != nil && (s.job != nil || s.room.lobby != nil) {
		g.pollOnline()
		return g.online != nil || g.battle != nil
	}
	return false
}

// pumpBackground runs the ticks the relay has granted, as the host step does,
// and drops the cues and notices they raised: no frame presented them, and a
// page shown again resumes from its latest committed tick rather than
// replaying them all at once.
func (b *battleSession) pumpBackground(cl *client.Client) {
	b.pumpLocalMultiplayer(cl)
	if m := b.multiplayer; m != nil && b.sess != nil && b.sess.Snapshot != nil {
		m.dropped = b.sess.Snapshot.DrainCommittedEvents(m.dropped)
		clear(m.dropped)
	}
}
