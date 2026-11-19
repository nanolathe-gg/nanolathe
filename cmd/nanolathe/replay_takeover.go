package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// replayTakeover owns a local continuation (DESIGN_MULTIPLAYER §10.1). The
// source retains the recorded room's content mount until battle teardown,
// but its player can never read another entry after the transfer.
type replayTakeover struct {
	source   *replayPlayback
	position uint64
	owner    uint8
	err      error
}

func (p *replayPlayback) takeoverChoice() (replayPerspective, error) {
	if p == nil || p.player == nil || p.perspective < 0 || p.perspective >= len(p.perspectives) {
		return replayPerspective{}, fmt.Errorf("nanolathe: replay takeover refused: logical path view, providers searched [replay], expected a selected player")
	}
	v := p.perspectives[p.perspective]
	if v.FullMap {
		return v, fmt.Errorf("nanolathe: replay takeover refused: logical path view, providers searched [replay], expected a player's view; use View to choose a player")
	}
	if p.err != nil {
		return v, p.err
	}
	return v, p.player.CanTakeOver(v.Seat)
}

// serviceReplayTakeover commits the overlay's request before the next host
// step. Preparing the faction HUD may fail; that leaves the replay paused and
// intact, with a visible diagnostic, rather than a half-transferred battle.
func (b *battleSession) serviceReplayTakeover(cl *client.Client) bool {
	if b == nil || b.playback == nil || !b.playback.overlay.takeover {
		return false
	}
	p := b.playback
	p.overlay.takeover = false
	v, err := p.takeoverChoice()
	if err == nil {
		err = b.takeOverReplay(v, cl)
	}
	if err != nil {
		p.overlay.notice = "Cannot take over: " + noticeReason(err)
		p.paused = true
	}
	return true
}

func (b *battleSession) takeOverReplay(v replayPerspective, cl *client.Client) error {
	p := b.playback
	if p == nil || p.player == nil || b.sess == nil || b.hud == nil {
		return fmt.Errorf("nanolathe: replay takeover refused: logical path battle, providers searched [window], expected a prepared replay battle")
	}
	if err := p.player.CanTakeOver(v.Seat); err != nil {
		return err
	}
	nextHUD, err := loadRetailBattleHUDForOwner(b.fs, b.sess, b.cat, b.hud.pal, b.shell, newBattleWindowContext(b.cs, b.shell), v.Seat)
	if err != nil {
		return err
	}
	var origin *save.SidecarOnlineReplay
	if b.sess.OnlineCommandContext() {
		// Read before the handoff, which keeps no copy of the room.
		if origin, err = onlineReplayRecord(p.header); err != nil {
			return err
		}
	}
	position, err := p.player.TakeOver(v.Seat)
	if err != nil {
		return err
	}
	if origin != nil {
		origin.Difficulty = sidecarSeatDifficulties(b.sess.SeatDifficulties())
	}
	b.onlineOrigin = origin
	label := strings.TrimSuffix(v.Label, " (computer)")
	b.takeover = &replayTakeover{source: p, position: position, owner: v.Seat}
	b.playback, b.survivalBest = nil, nil
	b.hud = nextHUD
	b.battleState().ResetInteraction()
	// The selection made while watching stays, less any unit the new player
	// does not own; build pages and BigBrother stay with it.
	b.localState().ReplaceSelection(b.localRefs(b.selectedHandlesInSlotOrder()))
	// The panel fields shown while watching are the recording's; live
	// control starts from a fresh fold [07 R-HUD-03 §13].
	b.localState().MarkPanelStale()
	b.modernDrag, b.resourceClick = nil, nil
	b.resourceQueueFeedback = nil
	b.radial.close()
	b.gestures = battleGestures{}
	b.zoom.CancelWheel()
	b.tickFiredValid, b.lastTickFraction = false, 0
	b.present = presentClock{}
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	b.controller = NewBattleController(b, b.millisSource)
	// Replay speed, a menu hold and the time spent watching must contribute
	// no elapsed budget to the continuation's first pump.
	*b.sess.Clock = clock.State{GlobalTick: b.sess.Clock.GlobalTick, Requested: 10, Active: 10, ScaledAnchor: clock.ScaledNow(b.millisSource.Millis32())}
	b.battleState().SetPauseTruth(false)
	if b.shell != nil {
		b.shell.lastBattleSurvival = b.sess.Survival != nil
	}
	if cl != nil {
		if in := cl.Input(); in != nil {
			*in = *input.NewState()
		}
		// Installing the new player's interface places the battle-start
		// camera; the continuation keeps the view the player was watching.
		var camX, camZ int32
		if b.cam != nil {
			camX, camZ = b.cam.X, b.cam.Z
		}
		installBattleClient(cl, b)
		if b.cam != nil {
			b.cam.JumpTo(camX, camZ)
		}
		// Installation normally applies a saved game-speed preference. A
		// replay fork always starts at normal speed.
		*b.sess.Clock = clock.State{GlobalTick: b.sess.Clock.GlobalTick, Requested: 10, Active: 10, ScaledAnchor: clock.ScaledNow(b.millisSource.Millis32())}
		cl.SetPresentationPaused(false)
		cl.PrepareBattlePresentation()
	}
	b.syncLocalInterface()
	b.onlineNotice("Now playing as " + label)
	return nil
}

// An online continuation retains the one-tick pump contract and its online
// command admission, but the local host supplies both without a transport,
// and pauses as a single-player battle does.
func (b *battleSession) pumpReplayTakeover(cl *client.Client) {
	if b == nil || b.takeover == nil || b.takeover.err != nil || b.sess == nil || !b.sess.OnlineCommandContext() || b.sess.State != session.StateBattle || b.sess.OnlineBattleEnded() {
		return
	}
	if b.sess.Clock != nil && b.sess.Clock.Paused {
		return // a local continuation pauses (applyBattleSchedule)
	}
	b.sess.SetPublicationObserver(func(cur *frame.Frame) {
		b.applyPublishedCamera(cur)
		if cl != nil {
			cl.ObserveCommittedTick()
		}
	})
	err := b.sess.StepGranted(b.sess.Clock.GlobalTick + 1)
	b.sess.SetPublicationObserver(nil)
	for _, receipt := range b.sess.DrainCommandReceipts() {
		if receipt.Outcome == session.CommandRejected {
			b.onlineNotice(receipt.Diagnostic)
		}
	}
	if err != nil {
		b.takeover.err = err
		b.onlineNotice("Replay continuation stopped: " + noticeReason(err))
		return
	}
	b.syncLocalInterface()
	b.noteTickTiming()
	if cl != nil {
		cl.NoteTicksReleased(1)
	}
}

func (b *battleSession) submitReplayTakeover(c session.HumanCommand) (uint64, error) {
	t := b.takeover
	if t == nil || t.err != nil || t.position == ^uint64(0) || b.sess.Clock.GlobalTick == ^uint32(0) {
		return 0, fmt.Errorf("nanolathe: replay continuation command refused: logical path battle, providers searched [local continuation], expected a running continuation with remaining command positions")
	}
	command, err := b.sess.CaptureOnlineCommand(c)
	if err != nil {
		return 0, err
	}
	stamp := session.CommandStamp{Seat: t.owner, Tick: b.sess.Clock.GlobalTick + 1, Position: t.position + 1}
	if err := b.sess.EnqueueSeatCommand(stamp, command); err != nil {
		return 0, err
	}
	t.position = stamp.Position
	return stamp.Position, nil
}

// takeoverRefusal is why a replay continuation refuses a save, load or
// restart, or "" when it allows it or the battle is not a continuation. A
// continuation saves and loads as an offline skirmish, one continued from an
// online replay included (onlineOrigin). No continuation restarts: a restart
// enters the recorded setup afresh, with the recorded seat, not this one
// (DESIGN_MULTIPLAYER §10.1).
func (b *battleSession) takeoverRefusal(action string) string {
	if b == nil || b.takeover == nil || action != "RESTART" {
		return ""
	}
	return "Restart is unavailable when continuing a replay"
}

// onlineReplayRecord starts the record an offline save of a continued online
// replay carries (DESIGN_MULTIPLAYER §10.1): the room content the battle runs
// on and whether the room's teams shared sight. The computers' difficulties
// are the session's, after the handoff.
func onlineReplayRecord(h replay.Header) (*save.SidecarOnlineReplay, error) {
	config, err := h.Configuration()
	if err != nil {
		return nil, err
	}
	r := config.Request()
	out := &save.SidecarOnlineReplay{SharedSight: r.SessionKind == session.MatchOnlineSkirmish && r.AlliedSight.Shared(true)}
	for _, e := range r.RoomContent {
		out.Content = append(out.Content, save.SidecarRoomContent{Kind: uint8(e.Kind), Name: e.Name, Version: e.Version, Size: e.Size, SHA256: hex.EncodeToString(e.Digest[:])})
	}
	return out, nil
}

func sidecarSeatDifficulties(seats []session.SeatDifficulty) []save.SidecarSeatDifficulty {
	var out []save.SidecarSeatDifficulty
	for _, d := range seats {
		out = append(out, save.SidecarSeatDifficulty{Player: d.Player, Difficulty: d.Difficulty})
	}
	return out
}

// roomContentOf is a continued online replay's recorded room content as
// the mount reads it (mountRoomContent).
func roomContentOf(r *save.SidecarOnlineReplay) []session.MatchContentEntry {
	out := make([]session.MatchContentEntry, 0, len(r.Content))
	for _, c := range r.Content {
		e := session.MatchContentEntry{Kind: session.MatchContentKind(c.Kind), Name: c.Name, Version: c.Version, Size: c.Size}
		_, _ = hex.Decode(e.Digest[:], []byte(c.SHA256)) // the sidecar reader checked its length
		out = append(out, e)
	}
	return out
}

// multiplayerPersistence reports a battle whose saves and loads the
// multiplayer rules refuse: an online battle or a playback, but not a
// replay continuation, which takeoverRefusal answers for.
func (b *battleSession) multiplayerPersistence() bool {
	return b != nil && b.takeover == nil && b.onlineBattle()
}

// configuredUnitLimit is the configured unit-limit word a save records: the
// setup's, or a continued replay's recorded word, which sized its battle
// (DESIGN_MULTIPLAYER §10.1).
func (g *gameShell) configuredUnitLimit() int {
	if g.battle != nil && g.battle.takeover != nil && g.battle.sess != nil {
		return g.battle.sess.Skirmish.UnitLimit
	}
	return g.setup.UnitLimit
}
