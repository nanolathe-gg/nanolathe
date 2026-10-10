package main

// The online screen's work: Create, Join, Ready and the battle Start enters
// (DESIGN_MULTIPLAYER §16.6.2). Compiling, composing, rehearsing and every
// network call that can wait run on a job goroutine; the game goroutine polls
// the jobs and the lobby's never-blocking state from the shell's step.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// onlineRelay is the hosted relay as the online screen uses it. Tests
// substitute a fake; production uses the relay package.
type onlineRelay interface {
	Describe(ctx context.Context, address, room string, options relay.HostedDialOptions) (relay.HostedRoomDescription, error)
	Open(ctx context.Context, address, room string, hello relay.LocalHello, config []byte, size int, options relay.HostedDialOptions) (onlineLobby, error)
}

// onlineLobby is one seat's room before Start (relay.HostedLobby).
type onlineLobby interface {
	Code() string
	Seat() uint8
	State() (relay.HostedLobbyState, error)
	Configuration() []byte
	SetConfiguration(config []byte) error
	SetTeam(team uint8) error
	SetSide(side uint8) error
	// SetColor chooses this seat's player colour, 0..relay.HostedColors-1.
	// A colour another present seat holds, or any change while ready,
	// changes nothing; otherwise every seat's ready clears.
	SetColor(color uint8) error
	// SetReady reports readiness with this seat's configuration-identity and
	// rehearsal digests; not ready carries zero digests (§16.6.1, §16.7).
	SetReady(ready bool, identity, rehearsal [32]byte) error
	Start() error
	// Battle is the grant stream once Started, nil before.
	Battle() lockstep.Client
	Close() error
}

type hostedOnlineRelay struct{}

func (hostedOnlineRelay) Describe(ctx context.Context, address, room string, options relay.HostedDialOptions) (relay.HostedRoomDescription, error) {
	return relay.DescribeHostedRoom(ctx, address, room, options)
}

func (hostedOnlineRelay) Open(ctx context.Context, address, room string, hello relay.LocalHello, config []byte, size int, options relay.HostedDialOptions) (onlineLobby, error) {
	l, err := relay.OpenHostedLobby(ctx, address, room, hello, config, size, options)
	if err != nil {
		return nil, err
	}
	return hostedOnlineLobby{l}, nil
}

type hostedOnlineLobby struct{ *relay.HostedLobby }

// Battle keeps a nil client a nil interface.
func (l hostedOnlineLobby) Battle() lockstep.Client {
	if c := l.HostedLobby.Battle(); c != nil {
		return c
	}
	return nil
}

var onlineRelayService onlineRelay = hostedOnlineRelay{}

// rehearsalDigest runs the pre-start rehearsal on the frozen inputs and
// configuration a seat composed its match from (DESIGN_MULTIPLAYER §16.7).
// Tests substitute one.
var rehearsalDigest = session.RehearsalDigest

// onlineClipboardWrite is the host clipboard's write; tests substitute one so
// they never replace the player's clipboard.
var onlineClipboardWrite = ebitenapp.WriteHostClipboard

// Network waits are host bounds, not simulation inputs.
const (
	onlineDescribeTimeout = 10 * time.Second
	onlineOpenTimeout     = 15 * time.Second
)

// onlineWork counts running jobs. A content reload waits for them before it
// closes the content they read.
var onlineWork sync.WaitGroup

type onlineJob struct {
	cancel context.CancelFunc
	done   chan onlineJobResult
}

// onlineRoom is a room the player is joining: where it is and the base
// configuration the relay described.
type onlineRoom struct {
	address string
	options relay.HostedDialOptions
	code    string
	config  []byte
}

type onlineJobResult struct {
	described *onlineRoom // a Describe that succeeded
	// An opened room: its lobby, the catalog compiled for it, its base
	// configuration and its server.
	lobby   onlineLobby
	cat     *content.Catalog
	base    session.EffectiveMatchConfig
	address string
	err     error
}

func (s *onlineScreen) run(work func(context.Context) onlineJobResult) {
	ctx, cancel := context.WithCancel(context.Background())
	job := &onlineJob{cancel: cancel, done: make(chan onlineJobResult, 1)}
	s.job = job
	onlineWork.Add(1)
	go func() {
		defer onlineWork.Done()
		job.done <- work(ctx)
	}()
}

// cancelJob abandons the running job. A room it opens anyway is closed.
func (s *onlineScreen) cancelJob() {
	job := s.job
	if job == nil {
		return
	}
	s.job = nil
	job.cancel()
	go func() {
		if r := <-job.done; r.lobby != nil {
			_ = r.lobby.Close()
		}
	}()
}

// onlineFail returns the screen to idle with a plain-words reason.
func (g *gameShell) onlineFail(err error) {
	if g.online != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: online: %v\n", err)
		g.onlineIdleStatus(onlineRefusalText(err))
	}
}

func (g *gameShell) onlineIdleStatus(status string) {
	s := g.online
	if s == nil {
		return
	}
	s.phase, s.status = onlineIdle, status
	g.refreshOnlinePanel()
}

// onlineServerChoice is the server the player chose (Server on the
// chooser), or the default.
func (g *gameShell) onlineServerChoice() (string, relay.HostedDialOptions, bool) {
	address, options, err := onlineServerAddress(g.onlineServer)
	if err != nil {
		g.onlineIdleStatus(onlineRefusalText(err) + " Choose Server to change it.")
		return "", options, false
	}
	g.online.detail = "Server " + address
	return address, options, true
}

// onlineHello is a lobby hello. The relay compares only the protocol before
// Ready, when the identity digest compares the rest; a joiner asks for any
// seat. The rule name is the one every room plays, for the hello's encoding.
func onlineHello(seat uint8) relay.LocalHello {
	return relay.LocalHello{Seat: seat, Identity: netproto.Identity{Protocol: netproto.CommandSchemaVersion, Rules: netproto.RuleIdentity{Name: string(gameplay.Modern)}}}
}

// startOnlineCreate opens a ten-seat room at once with the host's defaults: a
// skirmish on its current skirmish map with its skirmish options, its mod,
// mutators and restrictions, and a fresh seed pair (§16.6).
func (g *gameShell) startOnlineCreate() {
	s := g.online
	if s == nil || s.job != nil || s.phase != onlineIdle {
		return
	}
	address, options, ok := g.onlineServerChoice()
	if !ok {
		return
	}
	if err := onlineContentRefusal(g.cs); err != nil {
		g.onlineFail(err)
		return
	}
	if g.cs.mod != nil && matchModOf(g.cs.mod).Archive == ([32]byte{}) {
		// Configuration field 8 names a mod by its archive digest, which a
		// folder install does not have.
		g.onlineFail(localMultiplayerError("mod", "a mod installed from its archive, with an archive digest to send"))
		return
	}
	if strings.TrimSpace(g.setup.MapName) == "" {
		g.onlineIdleStatus("Choose a skirmish map first.")
		return
	}
	sim, crt, err := drawOnlineSeeds()
	if err != nil {
		g.onlineFail(err)
		return
	}
	settings := onlineSettingsFromSetup(g.setup)
	cs, mutators, restrictions := g.cs, g.opts.Mutators, g.opts.Restrictions
	s.phase, s.status = onlineConnecting, "Opening a room on "+g.onlineServerLabel()+"..."
	s.run(func(ctx context.Context) onlineJobResult {
		cat, err := cs.compileCatalog(nil)
		if err != nil {
			return onlineJobResult{err: err}
		}
		records, err := onlineMatchRestrictions(restrictions, cat)
		if err != nil {
			return onlineJobResult{err: err}
		}
		base, err := onlineBaseConfig(cs, cat, settings, onlineCreationFrozen(cs, [2]uint32{sim, crt}, mutators, restrictions, records))
		if err != nil {
			return onlineJobResult{err: err}
		}
		encoded, err := session.EncodeMatchConfig(base)
		if err != nil {
			return onlineJobResult{err: err}
		}
		dial, cancel := context.WithTimeout(ctx, onlineOpenTimeout)
		defer cancel()
		lobby, err := onlineRelayService.Open(dial, address, "", onlineHello(0), encoded, relay.HostedMaxSeats, options)
		if err != nil {
			return onlineJobResult{err: err}
		}
		return onlineJobResult{lobby: lobby, cat: cat, base: base, address: address}
	})
	g.refreshOnlinePanel()
}

// onlineCodeHint says why a normalized code is not one: the first character
// codes never use, such as O or 1, or else its length.
func onlineCodeHint(code string) string {
	for _, r := range code {
		if !strings.ContainsRune(relay.RoomCodeAlphabet, r) {
			return fmt.Sprintf("Room codes never use %q. Check the code your friend sent.", r)
		}
	}
	return "Room codes are six letters and digits. Check the code your friend sent."
}

// startOnlineJoin asks the relay for the typed room's configuration.
func (g *gameShell) startOnlineJoin() {
	s := g.online
	if s == nil || s.job != nil || s.phase != onlineIdle {
		return
	}
	code, ok := relay.NormalizeRoomCode(s.panel.TextOf("ADDRESS"))
	if !ok {
		g.onlineIdleStatus(onlineCodeHint(code))
		return
	}
	address, options, ok := g.onlineServerChoice()
	if !ok {
		return
	}
	if err := onlineContentRefusal(g.cs); err != nil {
		g.onlineFail(err)
		return
	}
	s.phase, s.status = onlineDescribing, "Looking for game "+code+"..."
	s.run(func(ctx context.Context) onlineJobResult {
		ctx, cancel := context.WithTimeout(ctx, onlineDescribeTimeout)
		defer cancel()
		d, err := onlineRelayService.Describe(ctx, address, code, options)
		if err == nil && len(d.Config) == 0 {
			err = localMultiplayerError("room configuration", "the host's configuration")
		}
		if err != nil {
			return onlineJobResult{err: err}
		}
		return onlineJobResult{described: &onlineRoom{address: address, options: options, code: code, config: d.Config}}
	})
	g.refreshOnlinePanel()
}

// adoptOnlineRoom checks that this install can play the described room and
// joins it. The room's mod is fixed; one that is installed but not mounted is
// mounted through the ordinary content reload, which resumes the join on the
// new shell (resumeOnlineJoin). The room's map may change until Start, so a
// missing map is reported in the lobby instead.
func (g *gameShell) adoptOnlineRoom(room onlineRoom, remounted bool) {
	s := g.online
	if s == nil {
		return
	}
	base, err := session.DecodeMatchConfig(room.config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: online: %v\n", err)
		g.onlineIdleStatus("This game's settings could not be read. The host may be running a different release.")
		return
	}
	if want := base.Request().Mod; want != matchModOf(g.cs.mod) {
		if remounted {
			g.onlineIdleStatus("This game's mod " + onlineModName(want) + " could not be selected.")
			return
		}
		selector, refusal := g.onlineModPlan(want)
		if refusal != "" {
			g.onlineIdleStatus(refusal)
			return
		}
		s.phase, s.status = onlineConnecting, "Loading "+onlineModName(want)+" for this game..."
		g.refreshOnlinePanel()
		pendingContentReload = &contentReloadRequest{
			selector: selector,
			mod:      settings.ModSelection{ID: want.ID, Version: want.Version},
			mutators: g.opts.Mutators,
			online:   &room,
		}
		return
	}
	cs := g.cs
	s.phase = onlineConnecting
	if !remounted {
		s.status = "Joining game " + room.code + "..."
	}
	s.run(func(ctx context.Context) onlineJobResult {
		cat, err := cs.compileCatalog(nil)
		if err != nil {
			return onlineJobResult{err: err}
		}
		dial, cancel := context.WithTimeout(ctx, onlineOpenTimeout)
		defer cancel()
		lobby, err := onlineRelayService.Open(dial, room.address, room.code, onlineHello(1), nil, 0, room.options)
		if err != nil {
			return onlineJobResult{err: err}
		}
		return onlineJobResult{lobby: lobby, cat: cat, base: base, address: room.address}
	})
	g.refreshOnlinePanel()
}

// onlineModPlan is the content reload that mounts want, or why it cannot.
func (g *gameShell) onlineModPlan(want session.MatchMod) (selector, refusal string) {
	if onlineContentRefusal(g.cs) != nil {
		return "", onlineRefusalText(onlineContentRefusal(g.cs))
	}
	name := onlineModName(want)
	if want.ID == "" {
		return "none", ""
	}
	lib, err := openModLibrary()
	if err != nil {
		return "", "This game needs the mod " + name + ", but the mod library is unavailable."
	}
	mod, ok, err := lib.Lookup(want.ID, want.Version)
	if err != nil || !ok {
		return "", "This game needs the mod " + name + ", which is not installed. Install it from NANOLATHE, Mods, then join again."
	}
	if sameMod(&mod, g.cs.mod) || want.Archive != ([32]byte{}) && matchModOf(&mod).Archive != want.Archive {
		return "", "This game needs a different copy of " + name + " than the one installed."
	}
	if missing := unmetModRequirements(g.cs.baseRoots, mod); len(missing) > 0 {
		return "", "This game needs " + name + ", which needs " + missing[0] + " from the base install."
	}
	return modSelectorOf(mod.ID, mod.Version), ""
}

// resumeOnlineJoin continues a join on the shell a content reload built for
// the room's mod.
func (g *gameShell) resumeOnlineJoin(room onlineRoom) {
	if err := g.openOnlineScreen(); err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	s := g.online
	if err := g.showOnlineView(onlineCodeEntry); err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	s.panel.SetText("ADDRESS", room.code)
	s.detail = "Server " + room.address
	mod := "the base game"
	if g.cs.mod != nil {
		mod = g.cs.mod.Name + " " + g.cs.mod.Version
	}
	s.status = "Switched to " + mod + " for this game. Joining..."
	g.adoptOnlineRoom(room, true)
}

// hasSkirmishMap reports whether the census lists name.
func (g *gameShell) hasSkirmishMap(name string) bool {
	for _, m := range g.maps {
		if strings.EqualFold(m, name) {
			return true
		}
	}
	return false
}

// pollOnline takes a finished job and follows the lobby. It runs every shell
// step; nothing in it waits.
func (g *gameShell) pollOnline() {
	s := g.online
	if s == nil {
		return
	}
	if job := s.job; job != nil {
		select {
		case r := <-job.done:
			s.job = nil
			job.cancel()
			g.finishOnlineJob(r)
		default:
		}
	}
	g.syncOnlineCodeField()
	if s = g.online; s != nil && s.phase == onlineInLobby && s.room.lobby != nil {
		g.pollOnlineLobby()
	}
}

func (g *gameShell) finishOnlineJob(r onlineJobResult) {
	switch {
	case r.err != nil:
		g.onlineFail(r.err)
	case r.described != nil:
		g.adoptOnlineRoom(*r.described, false)
	case r.lobby != nil:
		g.openOnlineLobby(r.lobby, r.cat, r.base, r.address)
	default:
		g.onlineIdleStatus("")
	}
}

// ---------------------------------------------------------------------------
// Ready.

// onlinePrepared is this seat's final configuration, composed and prepared
// for its first grant, with what Ready reports and the room key it was
// composed for.
type onlinePrepared struct {
	sess      *session.Session
	inputs    *content.SimulationInputs
	config    session.EffectiveMatchConfig
	detail    *client.DetailArt
	slot      uint8
	key       onlineRoomKey
	identity  [32]byte
	rehearsal [32]byte
}

type onlineReadyJob struct {
	cancel context.CancelFunc
	done   chan onlineReadyResult
}

type onlineReadyResult struct {
	prepared *onlinePrepared
	err      error
}

// setOnlineReady reports this seat ready, composing, preparing and
// rehearsing the final configuration first unless the room is unchanged
// since the last time; or not ready, with zero digests.
func (g *gameShell) setOnlineReady(ready bool) {
	r := &g.online.room
	if !ready {
		if r.readyJob != nil {
			r.readyJob.cancel()
			r.readyJob = nil
		}
		if r.ready {
			if err := r.lobby.SetReady(false, [32]byte{}, [32]byte{}); err != nil {
				g.leaveOnlineLobby(onlineRefusalText(err))
				return
			}
		}
		r.ready, r.notice = false, ""
		g.refreshOnlineLobby()
		return
	}
	if r.ready || r.readyJob != nil || g.onlineLobbyBlock() != "" {
		return
	}
	if p := r.prepared; p != nil && p.key == r.key() {
		g.reportOnlineReady(p)
		return
	}
	r.prepared, r.readyErr, r.notice = nil, nil, ""
	seats, slot, ok := onlineSeatsOf(r.state, r.lobby.Seat(), r.settings.survival)
	if !ok {
		return
	}
	key, base, settings, cat, cs, opts := r.key(), r.base, r.settings, r.cat, g.cs, g.opts
	ctx, cancel := context.WithCancel(context.Background())
	job := &onlineReadyJob{cancel: cancel, done: make(chan onlineReadyResult, 1)}
	r.readyJob = job
	onlineWork.Add(1)
	go func() {
		defer onlineWork.Done()
		job.done <- composeOnlineReady(ctx, cs, cat, opts, base, settings, seats, slot, key)
	}()
	g.refreshOnlineLobby()
}

// composeOnlineReady composes the final configuration — the base plus the
// present seats as slots with their teams and sides, then the base's
// computers — prepares this seat's battle at its slot and rehearses it
// (§16.6, §16.7).
func composeOnlineReady(ctx context.Context, cs *contentSet, cat *content.Catalog, opts Options, base session.EffectiveMatchConfig, settings onlineSettings, seats []session.OnlineSeat, slot uint8, key onlineRoomKey) onlineReadyResult {
	config, err := onlineConfig(cs, cat, settings, seats, onlineFrozenOf(base.Request()))
	if err != nil {
		return onlineReadyResult{err: err}
	}
	sess, inputs, identity, err := composeOnlineMatch(cs, cat, config, slot)
	if err != nil {
		return onlineReadyResult{err: err}
	}
	if err := ctx.Err(); err != nil {
		return onlineReadyResult{err: err}
	}
	rehearsal, err := rehearsalDigest(inputs, config)
	if err != nil {
		return onlineReadyResult{err: err}
	}
	if err := ctx.Err(); err != nil {
		return onlineReadyResult{err: err}
	}
	// Optional load-time art, prepared here as the loading screen's goroutine
	// prepares it (DESIGN_GPU_RENDERER §14.4).
	detail := detailArtFor(opts, cs, sess.World, nil)
	return onlineReadyResult{prepared: &onlinePrepared{sess: sess, inputs: inputs, config: config, detail: detail, slot: slot, key: key,
		identity: onlineIdentityDigest(identity), rehearsal: rehearsal}}
}

// finishOnlineReady reports Ready once the check passed for the room as it
// still is; a room that changed meanwhile needs another Ready.
func (g *gameShell) finishOnlineReady(result onlineReadyResult) {
	r := &g.online.room
	switch {
	case result.err != nil:
		if errors.Is(result.err, context.Canceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "nanolathe: online ready: %v\n", result.err)
		r.readyErr = result.err
	case result.prepared.key != r.key():
		r.notice = "The game changed while it was being checked. Choose Ready again."
	default:
		r.prepared = result.prepared
		g.reportOnlineReady(result.prepared)
	}
}

func (g *gameShell) reportOnlineReady(p *onlinePrepared) {
	r := &g.online.room
	if err := r.lobby.SetReady(true, p.identity, p.rehearsal); err != nil {
		g.leaveOnlineLobby(onlineRefusalText(err))
		return
	}
	r.ready, r.notice = true, ""
	g.refreshOnlineLobby()
}

// ---------------------------------------------------------------------------
// The battle.

// startOnlineBattle enters the prepared battle with the stream the lobby
// handed over. The relay's slot for this seat must be the slot it composed.
func (g *gameShell) startOnlineBattle(state relay.HostedLobbyState) {
	r := &g.online.room
	p := r.prepared
	conn := r.lobby.Battle()
	switch {
	case conn == nil:
		g.leaveOnlineLobby("The game could not start.")
		return
	case p == nil || !r.ready || p.slot != state.Slot:
		_ = conn.Close()
		g.leaveOnlineLobby("The game started without your prepared battle. Join again.")
		return
	}
	// The players are the human rows; the host's computers and Survival's
	// attacker are rows every client runs, not seats.
	humans := onlineHumanCount(p.config.Request())
	code := r.lobby.Code()
	if err := g.enterOnlineBattle(p, conn, code, humans); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: online: %v\n", err)
		g.leaveOnlineLobby("The game could not start: " + noticeReason(err))
		return
	}
	// The battle owns the connection now, and the lobby's Close does nothing.
	// The lobby window stays under the battle until it returns to the menu.
	g.online = nil
}

// enterOnlineBattle adopts a prepared online battle and drives it from conn,
// as the command-line path does after its dial (startHostedMultiplayer). An
// online battle has no opening arrival: the opening holds the pump, and
// grants keep arriving (§16.4.2). The driver is made before the presentation
// so a failure closes the connection with nothing adopted.
func (g *gameShell) enterOnlineBattle(p *onlinePrepared, conn lockstep.Client, code string, humans int) error {
	if p == nil || p.sess == nil {
		_ = conn.Close()
		return localMultiplayerError("battle", "a prepared online battle")
	}
	sess := p.sess
	// Every online seat records the stream it executes (DESIGN_MULTIPLAYER
	// §10); a battle that cannot be recorded keeps conn as it is.
	conn = g.recordOnlineReplay(p, conn)
	stats := newOnlineNetStats(conn, sess.LocalOwner, humans)
	driver, err := lockstep.NewPacedDriver(sess, stats)
	if err != nil {
		_ = conn.Close()
		return err
	}
	if g.audioOwner != nil {
		sess.Audio = g.audioOwner
	}
	// The loading transition's second half: the battle takes the chosen
	// display size before its HUD opens [07 "The loading screen"].
	g.applyDisplayMode(clPtr)
	battle, err := composeBattleEntryDetached(sess, sess.Catalog, g.cs, g, nil)
	if err != nil {
		_ = driver.Close()
		g.applyDisplaySize(clPtr, retailScreenW, retailScreenH)
		return err
	}
	g.importedRetailBattle, g.lastBattleSurvival = false, false
	g.pendingDetail = p.detail
	g.commitBattleCandidate(battle)
	battle.multiplayer = &battleMultiplayer{driver: driver, completed: driver.Completed, net: stats}
	battle.returnToMenu = g.returnFromOnlineBattle
	battle.returnToSkirmish = g.returnFromOnlineBattle
	if clPtr != nil {
		clPtr.PrepareBattlePresentation()
	}
	battle.onlineNotice(fmt.Sprintf("Online game %s: you are player %d of %d", code, sess.LocalOwner+1, humans))
	return nil
}

// returnFromOnlineBattle leaves an online battle, or its result, for the
// online screen, saying how the game ended. A seat whose result is final may
// leave while the others play on (§16.6).
func (g *gameShell) returnFromOnlineBattle(cl *client.Client) {
	message := "You left the game."
	if b := g.battle; b != nil {
		switch {
		case b.multiplayer != nil && b.multiplayer.failure != nil:
			message = "The game stopped: " + onlineRefusalText(b.multiplayer.failure)
		case b.sess != nil && b.sess.OnlineBattleEnded():
			message = "The game has ended."
		case b.sess != nil && b.sess.ResultForSeat(b.sess.LocalOwner).Ended:
			message = "You left after your defeat; the others play on."
		}
	}
	g.returnFromBattle(cl)
	if err := g.openOnlineScreen(); err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
		return
	}
	g.online.status = message
	g.refreshOnlinePanel()
}
