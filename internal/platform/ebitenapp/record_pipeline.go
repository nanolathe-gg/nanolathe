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

// updateMargin is how much of an update the prediction keeps clear of the next
// Update. A Draw predicted to land inside it is treated as landing after the
// Update instead: an Update writes input, steps the client and publishes a new
// committed frame, so a pre-record across one is certain to be discarded and
// launching it would only spend a goroutine and a rollback.
const updateMargin = fractionOne / 8

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
	// armed says a pre-record is outstanding and awaits a decision at the next
	// Draw. hits and misses are this window's running counts.
	armed        bool
	hits, misses int64
	synchronous  int64
	// reported is the frame total the last readout covered.
	reported int64
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

// predictNext returns the two blend fractions the next presented frame is
// expected to be recorded at, and whether a pre-record is worth launching at
// all.
//
// camera16 is where the next Draw will sit in the current Update: the camera
// advances on the window's Update grid, so its fraction is (next Draw −
// updatedAt) × 30 (§13.5). A prediction that reaches the end of the update
// declines: the Update that follows rewrites client state and the record would
// be discarded.
//
// tick16 is the battle's own fraction, extrapolated at the rate the last two
// Draws measured. A prediction that reaches one declines for the same reason
// in the simulation's units: the next committed tick will have been published.
func (p *pipeline) predictNext(now time.Time, period time.Duration, updatedAt time.Time, tick16 int32) (nextTick16, nextCamera16 int32, ok bool) {
	if period <= 0 || updatedAt.IsZero() {
		return 0, 0, false
	}
	// The camera fraction the next Draw will compute.
	camera := int64(float64(now.Add(period).Sub(updatedAt).Seconds()) * presentationTPS * float64(fractionOne))
	if camera < 0 || camera >= int64(fractionOne-updateMargin) {
		return 0, 0, false
	}
	// The tick fraction it will settle. The measured rate is the honest one;
	// with no measurement yet, the nominal 30-per-second rate stands in.
	step := int64(period.Seconds() * presentationTPS * float64(fractionOne))
	if p.hasTick && p.tickAt.Before(now) {
		if d := int64(tick16) - int64(p.tick16); d > 0 {
			elapsed := now.Sub(p.tickAt).Seconds()
			if elapsed > 0 {
				step = int64(float64(d) / elapsed * period.Seconds())
			}
		}
	}
	tick := int64(tick16) + step
	if tick < 0 || tick >= int64(fractionOne) {
		return 0, 0, false
	}
	return int32(tick), int32(camera), true
}

// observeTick remembers this Draw's settled tick fraction for the next
// extrapolation.
func (p *pipeline) observeTick(now time.Time, tick16 int32) {
	p.tickAt, p.tick16, p.hasTick = now, tick16, true
}

// pipelineReportEvery is how many presented modern frames separate two
// readouts. At the Enhanced presentation rate that is a few seconds, which is
// long enough for a hit rate to mean something and rare enough that the line
// itself costs nothing.
const pipelineReportEvery = 600

// reportPipeline prints the window's pipeline counters — periodically while the
// window runs, and once at exit. It is a prototype readout on the host's own
// stderr, never a sim-path log (AGENTS.md "Diagnostics").
func (a *app) reportPipeline() {
	total := a.pipe.hits + a.pipe.misses + a.pipe.synchronous
	if total == 0 || total == a.pipe.reported {
		return
	}
	a.pipe.reported = total
	reasons, driftTick, driftCamera := a.c.PreRecordMisses()
	var why strings.Builder
	for i, n := range reasons {
		if n == 0 || client.MissReason(i) == client.MissNone {
			continue
		}
		fmt.Fprintf(&why, " %s=%d", client.MissReasonNames[i], n)
	}
	fmt.Fprintf(os.Stderr, "nanolathe: record pipeline: %d/%d modern frames pre-recorded (%.1f%%), %d predicted misses, %d never launched; misses:%s; max drift tick=%d camera=%d quanta\n",
		a.pipe.hits, total, 100*float64(a.pipe.hits)/float64(total), a.pipe.misses, a.pipe.synchronous, why.String(), driftTick, driftCamera)
}

// reportPipelinePeriodically prints a readout every pipelineReportEvery
// presented modern frames.
func (a *app) reportPipelinePeriodically() {
	if total := a.pipe.hits + a.pipe.misses + a.pipe.synchronous; total-a.pipe.reported >= pipelineReportEvery {
		a.reportPipeline()
	}
}
