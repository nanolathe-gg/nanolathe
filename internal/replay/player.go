package replay

import (
	"errors"
	"fmt"
	"io"

	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// Mismatch is the first unit checksum playback computed differently from
// the recording. It is the error Step returns and wraps ErrMismatch.
type Mismatch struct {
	Tick     uint32
	Expected [32]byte
	// Got is the playback's checksum, zero when playback observed none at
	// Tick.
	Got [32]byte
}

func (m *Mismatch) Error() string {
	if m.Got == ([32]byte{}) {
		return fmt.Sprintf("%v: logical path tick %d, providers searched [replay checksums], expected checksum %x, observed none", ErrMismatch, m.Tick, m.Expected[:8])
	}
	return fmt.Sprintf("%v: logical path tick %d, providers searched [replay checksums], expected checksum %x, got %x", ErrMismatch, m.Tick, m.Expected[:8], m.Got[:8])
}

func (m *Mismatch) Unwrap() error { return ErrMismatch }

// checksumAt is one unit checksum and its tick.
type checksumAt struct {
	tick uint32
	sum  [32]byte
}

// Player plays a replay synchronously on a session Compose built: each Step
// queues the recorded commands with their recorded stamps and runs whole
// recorded pumps — single-player through StepRecordedPump, so each executor
// tail runs where the recording's ran, online one granted tick at a time
// through StepGranted — and compares every recorded unit checksum with the
// playback's own. Playback stops at the first that differs.
//
// It has one caller, which owns the session between steps and drains its
// command receipts. Nothing here reads a clock: a host chooses how many
// ticks each Step may run.
type Player struct {
	s      *session.Session
	r      *Reader
	online bool
	ctx    session.CommandContext
	tick   uint32
	final  uint32
	// runCount pumps of runTicks ticks are read and not yet run.
	runCount, runTicks uint32
	// peeked is one entry read ahead, with its error.
	peeked    *Entry
	peekedErr error
	expected  []checksumAt
	observed  []checksumAt
	// unobservable is the session's report that playback can no longer
	// observe checksums.
	unobservable error
	verified     int
	mismatch     *Mismatch
	err          error
	done         bool
	truncated    bool
	end          EndReason
}

// verifier observes a single-player playback's checksums where the session
// computes them, inside the pump, exactly as the recording's recorder did.
type verifier struct{ p *Player }

func (v verifier) Command(session.CommandStamp, session.SeatCommand) {}
func (v verifier) PumpEnded(uint32, int)                             {}
func (v verifier) Checksum(tick uint32, sum [32]byte) {
	v.p.observed = append(v.p.observed, checksumAt{tick, sum})
}
func (v verifier) Failed(err error) {
	if v.p.unobservable == nil {
		v.p.unobservable = err
	}
}

// NewPlayer prepares to play r on s, the battle Compose built from r's
// header and not yet ticked. It checks the battle's initial unit checksum
// against the header's and returns a *Mismatch at tick 0 when they differ.
// A single-player playback observes its checksums through the session's
// replay recorder, which NewPlayer attaches.
func NewPlayer(s *session.Session, r *Reader) (*Player, error) {
	if s == nil || r == nil || s.Clock == nil || s.Clock.GlobalTick != 0 {
		return nil, fmt.Errorf("nanolathe: replay playback refused: logical path session, providers searched [replay format v%d], expected a battle composed for the replay and not yet ticked", FormatVersion)
	}
	h := r.header
	if s.OnlineCommandContext() != h.Kind.Online() {
		return nil, fmt.Errorf("nanolathe: replay playback refused: logical path session, providers searched [replay format v%d], expected a battle of the replay's %s kind", FormatVersion, h.Kind)
	}
	p := &Player{s: s, r: r, online: h.Kind.Online(), ctx: h.Kind.commandContext()}
	if summary, err := Summarize(r.data); err == nil {
		p.final = summary.FinalTick
	}
	if got := s.UnitStateChecksum(); h.InitialChecksum != ([32]byte{}) && got != h.InitialChecksum {
		return nil, &Mismatch{Tick: 0, Expected: h.InitialChecksum, Got: got}
	}
	if !p.online {
		if err := s.SetReplayRecorder(verifier{p}); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Tick is the last tick played.
func (p *Player) Tick() uint32 { return p.tick }

// FinalTick is the recording's last tick: its end entry's, or its last
// whole chunk's when it stops without one.
func (p *Player) FinalTick() uint32 { return p.final }

// Done reports that every recorded pump has run, or that playback stopped
// at an error.
func (p *Player) Done() bool { return p.done || p.err != nil }

// Truncated reports a recording that stops without its end entry; it plays
// up to its last whole chunk.
func (p *Player) Truncated() bool { return p.truncated }

// End is the recording's end reason once playback reaches it, zero before
// and for a truncated recording.
func (p *Player) End() EndReason { return p.end }

// Verified is how many recorded checksums playback has matched.
func (p *Player) Verified() int { return p.verified }

// Mismatch is the first checksum playback computed differently, or nil.
func (p *Player) Mismatch() *Mismatch { return p.mismatch }

// Step runs whole recorded pumps while their ticks stay within maxTicks, and
// always at least one pump when maxTicks is positive and one remains, since a
// pump is never split. It returns the ticks run. It returns io.EOF, with no
// tick, once every recorded pump has run; a *Mismatch when a checksum
// differs; and any other error for a malformed recording or a session that
// refused a recorded input. An error is sticky.
func (p *Player) Step(maxTicks int) (int, error) {
	ran := 0
	for ran < maxTicks && p.err == nil && !p.done {
		if p.runCount == 0 {
			if err := p.readUntilPump(); err != nil {
				return ran, p.fail(err)
			}
			if p.done {
				break
			}
		}
		if ran > 0 && ran+int(p.runTicks) > maxTicks {
			break
		}
		if err := p.pump(); err != nil {
			return ran, p.fail(err)
		}
		ran += int(p.runTicks)
		if p.runCount--; p.runCount == 0 {
			p.readChecksums()
		}
		if err := p.compare(); err != nil {
			return ran, p.fail(err)
		}
	}
	if p.err != nil {
		return ran, p.err
	}
	if p.done {
		if err := p.compare(); err != nil {
			return ran, p.fail(err)
		}
		if ran == 0 {
			return 0, io.EOF
		}
	}
	return ran, nil
}

func (p *Player) fail(err error) error {
	if p.err == nil {
		p.err = err
	}
	return p.err
}

// next returns the entry read ahead, or reads one.
func (p *Player) next() (Entry, error) {
	if p.peeked != nil || p.peekedErr != nil {
		e, err := p.peeked, p.peekedErr
		p.peeked, p.peekedErr = nil, nil
		if err != nil {
			return Entry{}, err
		}
		return *e, nil
	}
	return p.r.Next()
}

// readUntilPump queues commands and expected checksums up to the next run
// of pumps, or to the end of the recording.
func (p *Player) readUntilPump() error {
	for {
		e, err := p.next()
		switch {
		case errors.Is(err, ErrTruncated):
			p.done, p.truncated = true, true
			return nil
		case err == io.EOF:
			p.done = true
			return nil
		case err != nil:
			return err
		}
		switch e.Kind {
		case EntryCommand:
			if err := p.enqueue(e); err != nil {
				return err
			}
		case EntryChecksum:
			p.expected = append(p.expected, checksumAt{e.Tick, e.Sum})
		case EntryPumps:
			p.runCount, p.runTicks = e.Pumps, e.PumpTicks
			return nil
		case EntryEnd:
			p.end = e.Reason
		}
	}
}

// readChecksums takes the checksums recorded right after a run — an online
// seat acknowledges a tick's checksum after it runs the tick — and keeps
// the entry after them, or the error that ends the stream, for the next
// read.
func (p *Player) readChecksums() {
	for {
		e, err := p.next()
		if err != nil {
			p.peekedErr = err
			return
		}
		if e.Kind != EntryChecksum {
			p.peeked = &e
			return
		}
		p.expected = append(p.expected, checksumAt{e.Tick, e.Sum})
	}
}

// enqueue decodes a recorded command and queues it for phase 1 of its tick
// with its recorded stamp.
func (p *Player) enqueue(e Entry) error {
	c, err := session.DecodeSeatCommand(p.ctx, e.Payload)
	if err != nil {
		return fmt.Errorf("%w: %w", formatError(fmt.Sprintf("command at tick %d position %d", e.Tick, e.Position), "a command this build decodes"), err)
	}
	return p.s.EnqueueSeatCommand(session.CommandStamp{Seat: e.Seat, Tick: e.Tick, Position: e.Position}, c)
}

// pump runs the next recorded pump.
func (p *Player) pump() error {
	last := p.tick + p.runTicks
	if p.online {
		if err := p.s.StepGranted(last); err != nil {
			return err
		}
		if last%session.ReplayChecksumInterval == 0 {
			p.observed = append(p.observed, checksumAt{last, p.s.UnitStateChecksum()})
		}
	} else if err := p.s.StepRecordedPump(last); err != nil {
		return err
	}
	if p.unobservable != nil {
		return fmt.Errorf("nanolathe: replay playback cannot observe checksums: logical path tick %d, providers searched [session replay recorder], expected checksum reports: %w", last, p.unobservable)
	}
	if got := p.s.Clock.GlobalTick; got != last {
		return fmt.Errorf("%w: logical path tick %d, providers searched [session], expected the recorded pump to end at tick %d, the battle stopped at %d", ErrMismatch, last, last, got)
	}
	p.tick = last
	return nil
}

// compare matches the recorded checksums with the observed ones by tick.
// An observation the recording has no checksum for is dropped; a recorded
// checksum for a tick already played that playback never observed is a
// mismatch.
func (p *Player) compare() error {
	for len(p.expected) > 0 && len(p.observed) > 0 {
		e, o := p.expected[0], p.observed[0]
		switch {
		case e.tick == o.tick:
			if e.sum != o.sum {
				p.mismatch = &Mismatch{Tick: e.tick, Expected: e.sum, Got: o.sum}
				return p.mismatch
			}
			p.verified++
			p.expected, p.observed = p.expected[1:], p.observed[1:]
		case o.tick < e.tick:
			p.observed = p.observed[1:]
		default:
			p.mismatch = &Mismatch{Tick: e.tick, Expected: e.sum}
			return p.mismatch
		}
	}
	p.observed = p.observed[:0]
	if len(p.expected) > 0 && p.expected[0].tick <= p.tick {
		p.mismatch = &Mismatch{Tick: p.expected[0].tick, Expected: p.expected[0].sum}
		return p.mismatch
	}
	return nil
}
