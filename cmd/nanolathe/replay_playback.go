package main

// Playing a replay in the window (docs/DESIGN_MULTIPLAYER.md §10 "Viewing is
// presentation"). A playback is an ordinary battle composed from the replay's
// header, played as the recorded seat: the host runs the recorded pumps in
// place of the clock's budget, as an online battle runs its granted ticks, so
// the local and viewing owners — simulation inputs in a single-player battle —
// never move, and every command the interface would submit is refused. Pause,
// speed, skipping ahead and the perspective are the host's alone; nothing
// here reaches a tick. A Replays screen drives it through replayPlayback's
// methods, and the battle's host step pumps it (pumpReplayPlayback).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// replaySpeed is one playback speed: Quarters quarter ticks per host step,
// the host stepping at 30 Hz, so 4 is the recorded game's own pace.
type replaySpeed struct {
	Quarters int
	Label    string
}

// replaySpeeds are the playback speeds, slowest first.
var replaySpeeds = []replaySpeed{{1, "¼×"}, {2, "½×"}, {4, "1×"}, {8, "2×"}, {16, "4×"}, {32, "8×"}}

// replayNormalSpeed is the index of 1×.
const replayNormalSpeed = 2

const (
	// replaySkipBudget is the host time one host step may spend skipping
	// ahead before it presents the progress so far.
	replaySkipBudget = 25 * time.Millisecond
	// replaySkipChunk bounds the ticks one skip step runs between budget
	// checks.
	replaySkipChunk = 30
)

// replayPerspective is one view a playback offers: a recorded human seat's,
// with that seat's fog, or the whole map without fog.
type replayPerspective struct {
	Label   string
	Seat    uint8
	FullMap bool
}

// replayStatus is where a playback stands.
type replayStatus struct {
	// Ended: every recorded pump has run, or playback stopped at an error.
	Ended bool
	// Truncated: the recording stops without its end entry and played to its
	// last whole chunk.
	Truncated bool
	// End is the recording's end reason once reached; zero for a truncated
	// recording.
	End replay.EndReason
	// Mismatch is the first checksum this build computed differently, which
	// stops playback there; Err is any other stop: a damaged recording or one
	// the session refused.
	Mismatch *replay.Mismatch
	Err      error
	// Verified counts the recorded checksums matched so far.
	Verified int
}

// replayPlayback is a replay being played in the window. Every method runs on
// the host (render) thread.
type replayPlayback struct {
	path   string
	header replay.Header
	sess   *session.Session
	player *replay.Player

	perspectives []replayPerspective
	perspective  int
	// viewChanged asks the next host step to publish the new perspective.
	viewChanged bool

	paused bool
	speed  int
	// quarters are the quarter ticks the speed has accrued and no pump has
	// spent; a pump longer than the accrual takes it below zero.
	quarters int
	// skipTarget is the tick a skip ahead runs to, zero when none.
	skipTarget uint32

	err     error
	dropped []frame.EventView

	// presentedPaused and presentedSpeed are what the battle's presentation
	// was last told.
	presentedPaused bool
	presentedSpeed  int
	presented       bool

	// menuHeld: the in-battle menu paused the playback, which was paused
	// before it opened when menuWasPaused (applySchedule).
	menuHeld, menuWasPaused bool
	// overlay is the playback overlay's host state (replay_overlay.go).
	overlay replayOverlayState
}

// errReplayPlaybackCommand refuses a command the interface would submit in a
// playback, which applies only the recorded ones.
var errReplayPlaybackCommand = errors.New("nanolathe: command refused: logical path <battle>, providers searched [replay playback], expected a battle that takes orders; a replay plays its recorded commands")

// replayContent is the installed content a playback composes from.
func replayContent(cs *contentSet, progress content.Progress) replay.Content {
	c := replay.Content{Progress: progress}
	if cs != nil {
		c.FS, c.Mod = cs.fs, matchModOf(cs.mod)
	}
	return c
}

// prepareReplayPlayback reads the replay at path and composes its battle from
// cs, prepared for its first recorded pump. It may run on a loader goroutine;
// enterReplayPlayback adopts it. An install that cannot compose the recorded
// battle returns an error wrapping replay.ErrIncompatible.
func prepareReplayPlayback(path string, cs *contentSet, progress content.Progress) (*replayPlayback, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("nanolathe: reading a replay failed: logical path %s, providers searched [replays], expected a readable file: %w", path, err)
	}
	return newReplayPlayback(path, data, cs, progress)
}

func newReplayPlayback(path string, data []byte, cs *contentSet, progress content.Progress) (*replayPlayback, error) {
	r, err := replay.NewReader(data)
	if err != nil {
		return nil, err
	}
	h := r.Header()
	sess, err := replay.Compose(h, replayContent(cs, progress))
	if err != nil {
		return nil, err
	}
	player, err := replay.NewPlayer(sess, r)
	if err != nil {
		return nil, err
	}
	p := &replayPlayback{path: path, header: h, sess: sess, player: player, speed: replayNormalSpeed}
	for i, seat := range h.Seats {
		if seat.Role != session.MatchRoleHuman {
			continue
		}
		label := seat.Name
		if label == "" {
			label = fmt.Sprintf("Player %d", i+1)
		}
		if uint8(i) == h.LocalSeat {
			p.perspective = len(p.perspectives)
		}
		p.perspectives = append(p.perspectives, replayPerspective{Label: label, Seat: uint8(i)})
	}
	p.perspectives = append(p.perspectives, replayPerspective{Label: "Full map", Seat: h.LocalSeat, FullMap: true})
	return p, nil
}

// freshBattle is the composed battle as battle entry describes one.
func (p *replayPlayback) freshBattle() headless.FreshBattle {
	kind := headless.ScenarioSkirmish
	if p.header.Kind.Survival() {
		kind = headless.ScenarioSurvival
	}
	return headless.FreshBattle{Session: p.sess, Kind: kind, LocalOwner: p.sess.LocalOwner}
}

// Path is the replay file.
func (p *replayPlayback) Path() string { return p.path }

// Header is the replay's header: kind, map, seats and start time.
func (p *replayPlayback) Header() replay.Header { return p.header }

// Tick is the last tick played, and FinalTick the recording's last.
func (p *replayPlayback) Tick() uint32      { return p.player.Tick() }
func (p *replayPlayback) FinalTick() uint32 { return p.player.FinalTick() }

// Paused reports a paused playback; SetPaused pauses or resumes it.
func (p *replayPlayback) Paused() bool         { return p.paused }
func (p *replayPlayback) SetPaused(pause bool) { p.paused = pause }

// Speeds are the playback speeds, Speed the current one's index; SetSpeed
// clamps to them. A change starts the new pace from the next host step.
func (p *replayPlayback) Speeds() []replaySpeed { return replaySpeeds }
func (p *replayPlayback) Speed() int            { return p.speed }
func (p *replayPlayback) SetSpeed(i int) {
	p.speed = max(0, min(len(replaySpeeds)-1, i))
	p.quarters = min(p.quarters, 0)
}

// SkipTo runs ahead to tick as fast as the host allows, a few hundred ticks
// per host step with presentation held, landing on the first recorded pump
// boundary at or after it. A tick at or before the current one, or past the
// recording, is clamped; there is no rewind.
func (p *replayPlayback) SkipTo(tick uint32) {
	if final := p.player.FinalTick(); tick > final {
		tick = final
	}
	if tick <= p.player.Tick() {
		p.skipTarget = 0
		return
	}
	p.skipTarget = tick
}

// CancelSkip stops a skip ahead where it is.
func (p *replayPlayback) CancelSkip() { p.skipTarget = 0 }

// Skipping reports a skip ahead in progress and its target; Tick is how far
// it has come.
func (p *replayPlayback) Skipping() (uint32, bool) { return p.skipTarget, p.skipTarget != 0 }

// Perspectives are the views offered: each recorded human seat's, then the
// full map. Perspective is the current one's index.
func (p *replayPlayback) Perspectives() []replayPerspective { return p.perspectives }
func (p *replayPlayback) Perspective() int                  { return p.perspective }

// SetPerspective selects a view; the next host step shows it, paused or not.
func (p *replayPlayback) SetPerspective(i int) error {
	if i < 0 || i >= len(p.perspectives) {
		return fmt.Errorf("nanolathe: replay perspective refused: logical path perspective %d, providers searched [replay seats], expected 0..%d", i, len(p.perspectives)-1)
	}
	if i != p.perspective {
		p.perspective, p.viewChanged = i, true
	}
	return nil
}

// Ended reports that the playback has nothing more to play.
func (p *replayPlayback) Ended() bool { return p.err != nil || p.player.Done() }

// Status is where the playback stands.
func (p *replayPlayback) Status() replayStatus {
	s := replayStatus{Ended: p.Ended(), Truncated: p.player.Truncated(), End: p.player.End(), Mismatch: p.player.Mismatch(), Verified: p.player.Verified()}
	if s.Mismatch == nil {
		s.Err = p.err
	}
	return s
}

// Message is the one line a playback overlay shows for its state, empty while
// it plays.
func (p *replayPlayback) Message() string {
	switch s := p.Status(); {
	case s.Mismatch != nil:
		return fmt.Sprintf("This replay no longer plays the same in this version: it diverged at %s.", replayGameTime(s.Mismatch.Tick))
	case s.Err != nil:
		return "This replay stopped: " + noticeReason(s.Err)
	case !s.Ended:
		return ""
	case s.Truncated:
		return "End of replay: the recording was cut short here."
	case s.End == replay.EndStopped:
		return "End of replay: the recording stopped here."
	case s.End == replay.EndRoomFailed:
		return "End of replay: the online game stopped here."
	case s.End == replay.EndLeft:
		return "End of replay: the player left here."
	}
	return "End of replay."
}

// replayGameTime is a tick as game time, m:ss or h:mm:ss.
func replayGameTime(tick uint32) string {
	seconds := tick / 30
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

// advance runs the recorded pumps one host step owes: a skip ahead's next
// stretch, or the speed's accrual unless paused. It returns the ticks run and
// whether they were a skip, whose presentation is held.
func (p *replayPlayback) advance(now func() time.Time) (int, bool) {
	// The battle menu holds active skips as well as normal playback
	// (DESIGN_INTERFACE_HUD_INPUT "Replays"). A paused playback may still skip.
	if p.menuHeld {
		return 0, false
	}
	if p.Ended() {
		p.skipTarget = 0
		return 0, false
	}
	if p.skipTarget != 0 {
		deadline := now().Add(replaySkipBudget)
		ran := 0
		for p.skipTarget > p.player.Tick() && !p.Ended() {
			n := p.step(int(min(p.skipTarget-p.player.Tick(), replaySkipChunk)))
			ran += n
			// A skipped tick's cues and notices are never presented.
			if p.sess.Snapshot != nil {
				p.dropped = p.sess.Snapshot.DrainCommittedEvents(p.dropped)
				clear(p.dropped)
			}
			if n == 0 || now().After(deadline) {
				break
			}
		}
		if p.skipTarget <= p.player.Tick() || p.Ended() {
			p.skipTarget, p.quarters = 0, 0
		}
		return ran, true
	}
	if p.paused {
		return 0, false
	}
	p.quarters += replaySpeeds[p.speed].Quarters
	owed := p.quarters / 4
	if owed < 1 {
		return 0, false
	}
	n := p.step(owed)
	p.quarters -= 4 * n
	if p.Ended() {
		p.quarters = 0
	}
	return n, false
}

// step runs whole recorded pumps within ticks, at least one, and drains the
// command receipts they produced: a playback's receipts are the recording's.
func (p *replayPlayback) step(ticks int) int {
	n, err := p.player.Step(ticks)
	p.sess.DrainCommandReceipts()
	if err != nil && !errors.Is(err, io.EOF) && p.err == nil {
		p.err = err
		fmt.Fprintf(os.Stderr, "nanolathe: replay %s: %v\n", p.path, err)
	}
	return n
}

// applyPerspective sets the selected view on the session's publication.
func (p *replayPlayback) applyPerspective() {
	v := p.perspectives[p.perspective]
	var view session.PresentationPerspective
	switch {
	case v.FullMap:
		view.RevealAll = true
	case v.Seat != p.sess.LocalOwner:
		view.Owner, view.Override = v.Seat, true
	}
	_ = p.sess.SetPresentationPerspective(view)
}

// replaySpeedClock is the clock speed word for a playback speed, read only
// by the Enhanced blend (tickFraction) so a slow playback moves smoothly
// between its ticks; a playback runs recorded pumps, never the clock's
// budget, so the word reaches no tick. 10 is 1× and the blend caps at 20.
func replaySpeedClock(i int) int32 {
	return int32(min(20, (replaySpeeds[i].Quarters*10+2)/4))
}

// attachReplayPlayback makes b the playback's battle, before it is committed:
// pumps come from the playback (pumpReplayPlayback), the battle takes no
// orders, pauses, saves or cheats (onlineBattle), and the result overlay
// stays down so the playback's own end state shows; a Replays screen may
// clear ResultDismissed to offer it.
func (b *battleSession) attachReplayPlayback(p *replayPlayback) {
	b.playback = p
	b.battleState().Input.ResultDismissed = true
	if c := b.sess.Clock; c != nil {
		c.Paused = false
	}
}

// replayPlayback is the battle's playback, nil for an ordinary battle.
func (b *battleSession) replayPlayback() *replayPlayback {
	if b == nil {
		return nil
	}
	return b.playback
}

// pumpReplayPlayback is a playback battle's host-step pump, in place of the
// clock-driven step: it runs what the playback owes and presents it, as an
// online battle presents its granted ticks (pumpLocalMultiplayer).
func (b *battleSession) pumpReplayPlayback(cl *client.Client) {
	p := b.playback
	if p == nil || b.sess == nil || b.sess.Clock == nil {
		return
	}
	if !p.presented || p.presentedSpeed != p.speed {
		b.sess.Clock.Requested = replaySpeedClock(p.speed)
		b.sess.Clock.Active = b.sess.Clock.Requested
		p.presentedSpeed = p.speed
	}
	if paused := p.paused || p.Ended(); !p.presented || paused != p.presentedPaused {
		b.battleState().SetPauseTruth(paused)
		if cl != nil {
			cl.SetPresentationPaused(paused)
		}
		p.presentedPaused = paused
	}
	if !p.presented || p.viewChanged {
		p.applyPerspective()
	}
	p.presented = true
	skipping := p.skipTarget != 0
	if !skipping {
		// Every played tick is observed before the next replaces it, as the
		// synchronous step observes its catch-up ticks.
		b.sess.SetPublicationObserver(func(cur *frame.Frame) {
			b.applyPublishedCamera(cur)
			if cl != nil {
				cl.ObserveCommittedTick()
			}
		})
	}
	ran, skipped := p.advance(time.Now)
	b.sess.SetPublicationObserver(nil)
	if ran == 0 && p.viewChanged {
		// A paused or waiting playback shows the new view at once.
		if b.sess.RepublishPresentation() && cl != nil {
			cl.ObserveCommittedTick()
		}
	}
	if ran == 0 {
		if p.viewChanged {
			b.syncLocalInterface()
		}
		p.viewChanged = false
		return
	}
	p.viewChanged = false
	if skipped {
		b.applyPublishedCamera(b.sess.Snapshot.Current())
		if cl != nil {
			cl.ObserveCommittedTick()
		}
	}
	b.syncLocalInterface()
	b.noteTickTiming()
	if cl != nil {
		cl.NoteTicksReleased(ran)
	}
}

// enterReplayPlayback adopts a prepared playback as the shell's battle. A
// playback opens without the battle arrival: the recording's first tick is
// already the battle. The battle returns to the main menu like any other; a
// Replays screen may replace returnToMenu to come back to itself.
func (g *gameShell) enterReplayPlayback(p *replayPlayback) error {
	if g == nil || p == nil || p.sess == nil {
		return fmt.Errorf("nanolathe: replay playback failed: logical path <battle>, providers searched [replays], expected a prepared playback")
	}
	sess := p.sess
	if g.audioOwner != nil {
		sess.Audio = g.audioOwner
	}
	// The loading transition's second half, as a fresh battle takes it: the
	// battle takes the chosen display size before its HUD opens.
	g.applyDisplayMode(clPtr)
	battle, err := composeBattleEntryDetached(sess, sess.Catalog, g.cs, g, nil)
	if err != nil {
		return err
	}
	battle.attachReplayPlayback(p)
	g.importedRetailBattle, g.lastBattleSurvival = false, false
	g.commitBattleCandidate(battle)
	if clPtr != nil {
		clPtr.PrepareBattlePresentation()
	}
	return nil
}
