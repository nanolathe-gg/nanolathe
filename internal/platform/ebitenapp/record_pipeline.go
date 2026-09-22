package ebitenapp

// The window's half of the record/submit pipeline
// (docs/DESIGN_GPU_RENDERER.md §13.10). The client owns the goroutine and the
// validity digest; everything here is the host's part of the contract: bumping
// the mutation epoch before it writes client state, joining the pre-record
// before anything else runs, and predicting the next presented frame's two
// blend fractions well enough that the prediction is usually right.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
)

// fractionOne is the 16.16 unit the client carries both blend fractions in.
// The window works in the same domain so a predicted fraction and a measured
// one are the same kind of number (§13.5).
const fractionOne = int32(1) << 16

// presentPeriodFloor and presentPeriodCeiling bound the measured Draw-to-Draw
// interval the prediction extrapolates over. A first Draw, a resumed window or
// a frame that missed several refreshes must not turn into a wild prediction:
// outside these bounds the pipeline simply does not launch.
const (
	presentPeriodFloor   = time.Millisecond
	presentPeriodCeiling = 40 * time.Millisecond
)

// pipeline is the window's pre-record state. It lives on app; nothing in it
// reaches the client or the simulation [I6].
type pipeline struct {
	// lastDrawAt is when the previous modern Draw began, and period the
	// interval between the last two. presentDue's cap is folded in by the
	// caller.
	lastDrawAt time.Time
	period     time.Duration
	// tickAt and tick16 are the previous Draw's settled tick fraction and when
	// it was settled, so the next one can be extrapolated at the rate the
	// battle's own millisecond source is actually advancing rather than at the
	// nominal one. The rate is not nominal whenever the game speed is not 1.
	tickAt  time.Time
	tick16  int32
	hasTick bool
	// launchPeriod is the present interval the outstanding pre-record's
	// prediction was extrapolated over. The tolerance is computed from THIS and
	// not from the period measured at the Draw that consumes it: a frame that
	// arrived late measures a longer period, which would widen its own
	// tolerance by exactly the lateness it is supposed to be caught by.
	launchPeriod time.Duration
	// armed says a pre-record is outstanding and awaits a decision at the next
	// Draw. hits and misses are this window's running counts.
	armed        bool
	hits, misses int64
	synchronous  int64
	// reported is the frame total the last readout covered, and reportedAt,
	// reportedBodies and reportedTick are the wall clock, update-body count and
	// committed tick it covered them at. The three turn the readout into a
	// cadence sanity check: update bodies and committed ticks per second must
	// both stay at the update rate however the deferral of §13.10 moves the
	// bodies inside a period.
	reported         int64
	reportedAt       time.Time
	reportedBodies   int64
	reportedTick     uint32
	reportedPolls    int64
	reportedPollTime time.Duration
}

// updateLedger places each scheduled 30 Hz host body — at its input poll, or at
// the end of the modern Draw the call precedes (§13.10). Deferring is what lets
// a frame that crosses an update be pre-recorded: the body runs before the
// launch instead of after it, so nothing writes client state between a launch
// and the Draw that consumes it.
//
// Its one invariant is arithmetic: every scheduled host step is answered by exactly
// one body. The simulation therefore cannot step twice for one update period,
// and cannot skip one, whatever the window does with its Draws.
type updateLedger struct {
	// owed is how many counted calls have no body yet — at most one while the
	// Draw tail is running, because a call declines to defer when one is owed
	// already.
	owed int
	// tailAlive says a modern Draw tail ran since the last call that did not
	// defer. Without it a window that stopped presenting — the classic
	// executor, an occluded or capped Draw — would defer bodies to a tail that
	// never comes and stall the simulation.
	tailAlive bool
}

// call counts one scheduled host step and reports how many bodies to run now.
// deferrable says the Draw that follows will reach a modern tail.
func (l *updateLedger) call(deferrable bool) int {
	l.owed++
	if deferrable && l.tailAlive && l.owed == 1 {
		return 0
	}
	n := l.owed
	l.owed = 0
	l.tailAlive = false
	return n
}

// tail marks a modern Draw tail and reports whether it owes a body.
func (l *updateLedger) tail() bool {
	l.tailAlive = true
	if l.owed == 0 {
		return false
	}
	l.owed--
	return true
}

// fractionTolerance is the window's per-Draw fraction tolerance, in quanta of
// the 16.16 blend fraction (§13.10). A pre-recorded list whose predicted
// fractions land within it is presented AT THE FRACTION IT WAS RECORDED FOR
// rather than re-recorded at the measured one: the window accepts the
// prediction, so a presented frame shows the instant it was predicted for and
// the error is present jitter.
//
// One present interval is the cap, and it is the guard rail rather than the
// target: a frame that arrived a whole refresh late is further out than any
// jitter and takes the exact path instead. The interval is expressed in the
// blend's units — thirtieths of a second — so period × 30 × 65536 is the
// number of quanta the fractions advance across one present.
//
// A fixed constant cannot serve here. The battle's tick fraction reads a
// millisecond source and moves in steps of about 1966 quanta at the nominal
// speed, so any tolerance below one of its steps fails on one millisecond of
// draw jitter, and one millisecond is an eighth of a 120 Hz present.
func fractionTolerance(period time.Duration) int32 {
	if period <= 0 {
		return 0
	}
	q := int64(period.Seconds() * presentationTPS * float64(fractionOne))
	if q <= 0 {
		return 0
	}
	if q > int64(fractionOne)-1 {
		return fractionOne - 1
	}
	return int32(q)
}

// tolerancePeriod picks the interval fractionTolerance is computed from: the
// interval the window NOMINALLY presents at — the `--fps` cap, the display's
// own rate, or the wider of the two when both are known. That, and not the
// interval this particular prediction was extrapolated over, is what makes the
// cap a guard rail. A frame that hitched measures a long period, and computing
// its tolerance from that period would widen it by exactly the lateness it
// exists to catch: the hitch would be presented at a pose most of a tick from
// where it belongs instead of taking the exact path.
//
// The period the last launch measured is the last resort, for a window that
// has not established a rate yet. With none of the three the tolerance is
// zero, which is the exact path.
func (p *pipeline) tolerancePeriod(cap time.Duration, refreshRate float64) time.Duration {
	nominal := cap
	if refreshRate > 0 {
		if d := time.Duration(float64(time.Second) / refreshRate); d > nominal {
			nominal = d
		}
	}
	if nominal <= 0 {
		nominal = p.launchPeriod
	}
	if nominal < presentPeriodFloor {
		return 0
	}
	if nominal > presentPeriodCeiling {
		return presentPeriodCeiling
	}
	return nominal
}

// observeDraw records this Draw's spacing and returns the interval the
// prediction should extrapolate over, or zero when there is no usable one.
func (p *pipeline) observeDraw(now time.Time, cap time.Duration) time.Duration {
	period := time.Duration(0)
	if !p.lastDrawAt.IsZero() {
		period = now.Sub(p.lastDrawAt)
	}
	p.lastDrawAt = now
	if period > 0 {
		p.period = period
	}
	period = p.period
	if cap > period {
		period = cap
	}
	if period < presentPeriodFloor || period > presentPeriodCeiling {
		return 0
	}
	return period
}

// clampFraction16 narrows a predicted fraction into the half-open [0, 1) the
// client stores both blend fractions in. The prediction must land on the same
// side of that clamp as the measured value or the two could not compare equal:
// the last presented frame of an update period saturates at the top, and the
// measured value saturates there too.
func clampFraction16(v int64) int32 {
	if v < 0 {
		return 0
	}
	if v > int64(fractionOne)-1 {
		return fractionOne - 1
	}
	return int32(v)
}

// predictNext returns the two blend fractions the Draw at nextAt is expected to
// settle, and whether a pre-record can be launched for it at all.
//
// camera16 is where that Draw will sit in the current update: the camera
// advances on the window's update grid, so its fraction is (next Draw −
// updatedAt) × 30 (§13.5).
//
// tick16 is the battle's own fraction as sampled at sampleAt, extrapolated over
// the interval that remains at the rate the last two samples measured. With no
// forward measurement — the first Draw, or a sample taken just after an update
// body published a tick and restarted the fraction — the nominal
// 30-per-second rate stands in.
//
// Neither fraction declines at the end of its range any more. It used to:
// an Ebitengine Update wrote client state between two Draws, and a pre-record
// across one was certain to be discarded. The update body now runs at the end
// of a Draw, before the launch (app.drawModern), so nothing writes client state
// between a launch and the Draw that consumes it and the frame at the end of an
// update period is as launchable as any other. What it needs instead is the
// client's own clamp, because that is where its measured fraction will be.
func (p *pipeline) predictNext(nextAt, sampleAt, updatedAt time.Time, tick16 int32) (nextTick16, nextCamera16 int32, ok bool) {
	if updatedAt.IsZero() {
		return 0, 0, false
	}
	ahead := nextAt.Sub(sampleAt)
	if ahead < 0 {
		ahead = 0
	}
	camera := clampFraction16(int64(nextAt.Sub(updatedAt).Seconds() * presentationTPS * float64(fractionOne)))
	step := int64(ahead.Seconds() * presentationTPS * float64(fractionOne))
	if p.hasTick && p.tickAt.Before(sampleAt) {
		if d := int64(tick16) - int64(p.tick16); d > 0 {
			if elapsed := sampleAt.Sub(p.tickAt).Seconds(); elapsed > 0 {
				step = int64(float64(d) / elapsed * ahead.Seconds())
			}
		}
	}
	return clampFraction16(int64(tick16) + step), camera, true
}

// observeTick remembers this Draw's settled tick fraction for the next
// extrapolation. It runs after the prediction, not before it: the rate the
// prediction extrapolates at is the one measured between the previous sample
// and this one.
func (p *pipeline) observeTick(now time.Time, tick16 int32) {
	p.tickAt, p.tick16, p.hasTick = now, tick16, true
}

// pipelineReportEvery is how many presented modern frames separate two
// readouts. At the Enhanced presentation rate that is a few seconds, which is
// long enough for a hit rate to mean something and rare enough that the line
// itself costs nothing.
const pipelineReportEvery = 600

// reportPipeline prints the window's pipeline counters — periodically while the
// window runs, and once at exit, only with RunOptions.Stats. It is a host-only
// stderr readout, never a sim-path log (AGENTS.md "Diagnostics").
func (a *app) reportPipeline() {
	if !a.options.Stats {
		return
	}
	total := a.pipe.hits + a.pipe.misses + a.pipe.synchronous
	if total == 0 || total == a.pipe.reported {
		return
	}
	frames := total - a.pipe.reported
	a.pipe.reported = total
	reasons, driftTick, driftCamera := a.c.PreRecordMisses()
	var why strings.Builder
	for i, n := range reasons {
		if n == 0 || client.MissReason(i) == client.MissNone {
			continue
		}
		fmt.Fprintf(&why, " %s=%d", client.MissReasonNames[i], n)
	}
	presentedTick, presentedCamera, meanTick := a.c.PreRecordPresentedDrift()
	fmt.Fprintf(os.Stderr, "nanolathe: record pipeline: %d/%d modern frames pre-recorded (%.1f%%), %d predicted misses, %d never launched; misses:%s; max drift presented tick=%d camera=%d (mean tick %.0f), over all predictions tick=%d camera=%d quanta\n",
		a.pipe.hits, total, 100*float64(a.pipe.hits)/float64(total), a.pipe.misses, a.pipe.synchronous, why.String(),
		presentedTick, presentedCamera, meanTick, driftTick, driftCamera)
	a.reportCadence(frames)
	if a.paused.records+a.paused.reuses > 0 {
		fmt.Fprintf(os.Stderr, "nanolathe: paused world: %d recordings, %d reuses; foreground remains live\n", a.paused.records, a.paused.reuses)
	}
}

// reportCadence prints the sanity line beside the pipeline readout: presented
// frames, update bodies and committed simulation ticks per second over the
// interval since the last readout. The deferral of §13.10 moves an update body
// from the top of a frame to the tail of the Draw before it, so bodies and
// ticks per second are the two figures that say it moved them and did not
// duplicate or drop one.
func (a *app) reportCadence(frames int64) {
	now := time.Now()
	tick := uint32(0)
	if buf := a.c.Buffer(); buf != nil {
		if cur := buf.Current(); cur != nil {
			tick = cur.Tick
		}
	}
	if !a.pipe.reportedAt.IsZero() {
		if elapsed := now.Sub(a.pipe.reportedAt).Seconds(); elapsed > 0 {
			fmt.Fprintf(os.Stderr, "nanolathe: record pipeline: cadence over %.1fs: %.1f presented/s, %.2f update bodies/s, %.2f committed ticks/s\n",
				elapsed, float64(frames)/elapsed, float64(a.bodies-a.pipe.reportedBodies)/elapsed, float64(tick-a.pipe.reportedTick)/elapsed)
			polls := a.inputPolls - a.pipe.reportedPolls
			if polls > 0 {
				pollTime := a.inputPollTime - a.pipe.reportedPollTime
				fmt.Fprintf(os.Stderr, "nanolathe: input polling: %.1f polls/s, %.2f us/poll elapsed, %.3f ms/s elapsed\n",
					float64(polls)/elapsed, float64(pollTime.Nanoseconds())/float64(polls)/1000, float64(pollTime.Nanoseconds())/elapsed/1e6)
			}
		}
	}
	a.pipe.reportedAt, a.pipe.reportedBodies, a.pipe.reportedTick = now, a.bodies, tick
	a.pipe.reportedPolls, a.pipe.reportedPollTime = a.inputPolls, a.inputPollTime
}

// reportPipelinePeriodically prints a readout every pipelineReportEvery
// presented modern frames.
func (a *app) reportPipelinePeriodically() {
	if !a.options.Stats {
		return
	}
	if total := a.pipe.hits + a.pipe.misses + a.pipe.synchronous; total-a.pipe.reported >= pipelineReportEvery {
		a.reportPipeline()
	}
}
