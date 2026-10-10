package session

import (
	"fmt"
	"sync"
)

// Replays (docs/DESIGN_MULTIPLAYER.md §10). A single-player replay holds the
// battle's configuration, every local command in the replay command form at
// the tick phase 1 applied it, the host's pump boundaries (§4.5) and a unit
// checksum every 30 ticks. Playback composes the battle from the
// configuration and runs the recorded pumps, applying the recorded commands,
// so a build that cannot reproduce a recording says so at the first checksum
// that differs. An online replay records the relay stream instead and plays
// it through EnqueueSeatCommand and StepGranted.
//
// Nothing here may change what a tick computes: with no recorder attached a
// battle is bit-identical, and a recorder only observes. The command form is
// replay_convert.go; the playback perspective is replay_view.go.

// ReplayChecksumInterval is the tick spacing of recorded unit checksums, the
// same cadence the online acknowledgement uses (§9.2).
const ReplayChecksumInterval = 30

// ReplayRecorder observes a single-player battle for a replay. The session
// calls it on the simulation thread, inside the pump, so an implementation
// must only copy what it is given and never call back into the session.
type ReplayRecorder interface {
	// Command reports one local human command as phase 1 applied it:
	// stamp.Tick is the applying tick, stamp.Seat the local seat and
	// stamp.Position the command's local sequence number. The command is in
	// the SinglePlayerReplay form, ready for EncodeSeatCommand.
	Command(stamp CommandStamp, c SeatCommand)
	// PumpEnded reports a host pump that ran ticks ticks (at least one),
	// ending at lastTick. A pump that runs no tick is not reported.
	PumpEnded(lastTick uint32, ticks int)
	// Checksum reports UnitStateChecksum after every tick that is a multiple
	// of ReplayChecksumInterval.
	Checksum(tick uint32, sum [32]byte)
	// Failed reports that the battle can no longer be replayed exactly, for
	// example a command the replay form cannot express. It is reported once;
	// the recorder hears nothing after it.
	Failed(err error)
}

// replayState is a battle's replay recorder and the presentation perspective
// of a playback. The recorder is written before the first tick and afterwards
// only by the simulation thread, which drops it when recording fails, so its
// readers need no lock. The perspective is written by the host and read by
// publication, which may run on the simulation goroutine, so it has one.
type replayState struct {
	recorder ReplayRecorder
	// pumpTicks counts the ticks the current pump has run, for PumpEnded.
	pumpTicks int

	mu          sync.Mutex
	perspective PresentationPerspective
}

// SetReplayRecorder attaches a recorder to a single-player battle before its
// first tick. It is refused for online battles, which record their relay
// stream, and once any tick has run or any local command has been applied,
// since a recording starts from the composed battle.
//
// A playback session may attach one too: it hears the pumps and checksums
// its recorded pumps produce, which is how a verifier compares them, and no
// commands, because playback applies stamped entries rather than local ones.
func (s *Session) SetReplayRecorder(r ReplayRecorder) error {
	if s == nil || r == nil {
		return fmt.Errorf("nanolathe: replay recorder refused: logical path session, providers searched [session], expected a session and a recorder")
	}
	if s.onlineResults != nil || s.OnlineCommandContext() {
		return fmt.Errorf("nanolathe: replay recorder refused: logical path session, providers searched [session], expected a single-player battle; an online battle records its relay stream (DESIGN_MULTIPLAYER §10)")
	}
	if s.Clock != nil && s.Clock.GlobalTick != 0 {
		return fmt.Errorf("nanolathe: replay recorder refused: logical path tick %d, providers searched [session], expected a battle whose first tick has not run", s.Clock.GlobalTick)
	}
	s.humanMu.Lock()
	queued := 0
	for i := range s.pendingHuman {
		if s.pendingHuman[i].seat == nil {
			queued++
		}
	}
	applied := s.nextHumanSequence > uint64(queued)
	s.humanMu.Unlock()
	if applied {
		// A paused-input boundary before the first tick applies commands
		// with tick 1; one applied before the recorder attached would be
		// missing from the recording.
		return fmt.Errorf("nanolathe: replay recorder refused: logical path session, providers searched [input queue], expected a battle that has applied no local command")
	}
	s.seatCommands.replay.recorder = r
	s.seatCommands.replay.pumpTicks = 0
	return nil
}

// applyAndRecordHumanCommand is phase 1's application of one queued element,
// at its tick or at the paused-input boundary with the tick that has not run
// (§4.2). With a recorder attached, a local command is converted to its
// replay form against the state it is about to be applied to — the instant
// that decides which of its references resolve — and reported with that
// applying tick, the tick its playback applies it in. A stamped entry is
// never reported: it is already a stream entry.
func (s *Session) applyAndRecordHumanCommand(c HumanCommand, tick uint32) {
	r := s.seatCommands.replay.recorder
	if r == nil || c.seat != nil {
		s.applyHumanCommand(c, tick)
		return
	}
	stamp := CommandStamp{Seat: s.LocalOwner, Tick: tick, Position: c.Sequence}
	out, err := s.replayCommand(c)
	s.applyHumanCommand(c, tick)
	if err == nil && stamp.Position == 0 {
		err = fmt.Errorf("a command with a local sequence; it did not enter through the input queue")
	}
	if err != nil {
		s.failReplay(fmt.Errorf("nanolathe: replay recording stopped: logical path tick %d sequence %d kind %d, providers searched [single-player replay command form], expected a command the replay form expresses exactly: %w", tick, c.Sequence, c.Kind, err))
		return
	}
	r.Command(stamp, out)
}

// replayTickEnded runs after each completed sub-tick of a pump: it counts the
// pump's ticks and reports the unit checksum at the 30-tick cadence (§9.2).
// The checksum reads unit records only, which the executor tail does not
// touch, so a tick inside a pump is checked as a one-tick pump's is.
func (s *Session) replayTickEnded(tick uint32) {
	r := s.seatCommands.replay.recorder
	if r == nil {
		return
	}
	s.seatCommands.replay.pumpTicks++
	if tick%ReplayChecksumInterval == 0 {
		r.Checksum(tick, s.UnitStateChecksum())
	}
}

// replayPumpEnded runs after the pump's executor tail. A pump that ran ticks
// is a recorded boundary, because the tail's temporary-sight expiry depends
// on where pumps end (§4.5); a pump that ran none changes no authoritative
// state and is not reported.
func (s *Session) replayPumpEnded() {
	r := s.seatCommands.replay.recorder
	if r == nil {
		return
	}
	ticks := s.seatCommands.replay.pumpTicks
	s.seatCommands.replay.pumpTicks = 0
	if ticks > 0 && s.Clock != nil {
		r.PumpEnded(s.Clock.GlobalTick, ticks)
	}
}

// failReplay reports that the battle can no longer be replayed exactly and
// detaches the recorder; the battle itself goes on unchanged.
func (s *Session) failReplay(err error) {
	r := s.seatCommands.replay.recorder
	s.seatCommands.replay.recorder = nil
	s.seatCommands.replay.pumpTicks = 0
	if r != nil {
		r.Failed(err)
	}
}

// PrepareRecordedBattle finishes entry dispatch for a battle composed for
// playback, without a wall-clock budget, as PrepareGrantedBattle does online.
// Entry dispatch runs no tick, so finishing it here and running the first
// recorded pump later is the battle the recording host entered when its
// first pump dispatched entry and then ticked.
func (s *Session) PrepareRecordedBattle() error {
	if s == nil || s.onlineResults != nil || s.OnlineCommandContext() {
		return fmt.Errorf("nanolathe: recorded entry refused: logical path session, providers searched [session], expected a single-player battle")
	}
	if s.Clock != nil && s.Clock.GlobalTick != 0 {
		return fmt.Errorf("nanolathe: recorded entry refused: logical path tick %d, providers searched [session], expected a battle whose first tick has not run", s.Clock.GlobalTick)
	}
	for i := 0; i < 10 && (s.State != StateBattle || s.IsPendingBattle()); i++ {
		s.Advance()
	}
	if s.State != StateBattle || s.IsPendingBattle() || s.Clock == nil {
		return fmt.Errorf("nanolathe: recorded entry failed: logical path session, providers searched [entry dispatch], expected a ready battle")
	}
	return nil
}

// StepRecordedPump runs one recorded pump: every tick up to and including
// lastTick, with the executor tail once at the end, exactly as the recording
// host's pump ran them. Commands for those ticks must already be queued with
// EnqueueSeatCommand. A local command queued in a playback would be applied
// where no recording applied it, so the pump is refused while one waits.
//
// A battle that ends inside the pump stops there, as the recording's did; if
// that is before lastTick, the recording ran on and the playback diverged.
func (s *Session) StepRecordedPump(lastTick uint32) error {
	if s == nil || s.onlineResults != nil || s.OnlineCommandContext() || s.Clock == nil || s.State != StateBattle || s.IsPendingBattle() {
		return fmt.Errorf("nanolathe: recorded pump refused: logical path tick %d, providers searched [session], expected a prepared single-player battle", lastTick)
	}
	committed := s.Clock.GlobalTick
	if lastTick <= committed {
		return fmt.Errorf("nanolathe: recorded pump refused: logical path tick %d, providers searched [session], expected a tick after committed tick %d", lastTick, committed)
	}
	s.humanMu.Lock()
	local := false
	for i := range s.pendingHuman {
		if s.pendingHuman[i].seat == nil {
			local = true
			break
		}
	}
	s.humanMu.Unlock()
	if local {
		return fmt.Errorf("nanolathe: recorded pump refused: logical path tick %d, providers searched [input queue], expected only recorded entries in a playback", lastTick)
	}
	s.ExecuteStep(StepPlan{run: true, ticks: int(lastTick - committed)})
	if s.Clock.GlobalTick != lastTick {
		return fmt.Errorf("nanolathe: recorded pump ended early: logical path tick %d, providers searched [session], expected the battle to run through recorded tick %d", s.Clock.GlobalTick, lastTick)
	}
	return nil
}

// SimulationContentDigest is the digest of the frozen simulation inputs a
// replay header records beside the configuration, so playback refuses
// content that would compose a different battle (§8.7). It reports false when
// the battle has no frozen inputs to digest: campaign missions and restores,
// which read their live sources.
func (s *Session) SimulationContentDigest() ([32]byte, bool) {
	if s == nil || s.Units == nil {
		return [32]byte{}, false
	}
	fs, _ := s.Units.COBSource()
	inputs := frozenInputsOf(fs)
	if inputs == nil {
		return [32]byte{}, false
	}
	return inputs.Digest(), true
}
