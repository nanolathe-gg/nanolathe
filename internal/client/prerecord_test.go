package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// These tests lock the record/submit pipeline's one safety property
// (docs/DESIGN_GPU_RENDERER.md §13.10): a pre-recorded list is Executed only
// when it is the list a synchronous record would have produced, and a list that
// is not is discarded without leaving the presentation streams advanced.

// pipelineClient is a client with two committed ticks and nothing else, which
// is all the pipeline needs: it records a degenerate surface, and the pipeline
// is about which record is used, not about what the record contains.
func pipelineClient(t *testing.T) (*Client, *frame.Buffer) {
	t.Helper()
	buf := frame.NewBuffer()
	for tick := uint32(1); tick <= 2; tick++ {
		f := buf.BeginWrite()
		f.Units = append(f.Units, unitAt(1, wu(int64(tick)*10), 0, 0))
		if err := buf.Publish(tick); err != nil {
			t.Fatalf("publish %d: %v", tick, err)
		}
	}
	c := &Client{buffer: buf}
	c.SetInterpolation(true)
	return c, buf
}

// The straightforward hit: nothing the recorder reads changed between the
// launch and the Draw that consumes it, so the pre-recorded list is presented.
func TestPreRecordHitsWhenNothingChanged(t *testing.T) {
	c, _ := pipelineClient(t)
	c.StartPreRecord(ClampTickFraction16(0.25), 0, false)
	c.JoinPreRecord()
	c.SetTickFraction(0.25)
	if _, ok := c.TakePreRecord(c.PresentationDigest(), 0); !ok {
		t.Fatal("an unchanged frame missed; the pipeline would never present a pre-recorded list")
	}
	hits, misses, launches := c.PreRecordCounts()
	if hits != 1 || misses != 0 || launches != 1 {
		t.Fatalf("counts = %d hits, %d misses, %d launches; want 1, 0, 1", hits, misses, launches)
	}
}

// The epoch is the host's declaration that it wrote client state — one bump per
// window Update, one per benchmark step. Every pre-record taken before it is
// stale, whatever else agrees.
func TestPreRecordMissesAcrossAMutationEpoch(t *testing.T) {
	c, _ := pipelineClient(t)
	c.SetTickFraction(0.25)
	c.StartPreRecord(ClampTickFraction16(0.25), 0, false)
	c.JoinPreRecord()
	c.BumpPresentationEpoch()
	if _, ok := c.TakePreRecord(c.PresentationDigest(), 0); ok {
		t.Fatal("a pre-record taken before the host wrote client state was presented")
	}
}

// A newly published committed tick is the mutation the benchmark's stepping
// draw makes, and the one a pre-record may never cross.
func TestPreRecordMissesOnANewCommittedTick(t *testing.T) {
	c, buf := pipelineClient(t)
	c.SetTickFraction(0.25)
	c.StartPreRecord(ClampTickFraction16(0.25), 0, false)
	c.JoinPreRecord()
	f := buf.BeginWrite()
	f.Units = append(f.Units, unitAt(1, wu(30), 0, 0))
	if err := buf.Publish(3); err != nil {
		t.Fatalf("publish 3: %v", err)
	}
	if _, ok := c.TakePreRecord(c.PresentationDigest(), 0); ok {
		t.Fatal("a pre-record from before a publication was presented")
	}
}

// The benchmark and `--shot` compare with zero tolerance, so a fraction that is
// one quantum out is a miss; a window tolerance accepts the same pair, and a
// drift past that tolerance is a miss again. The tolerance argument is the
// whole of the difference between the two hosts (§13.10).
func TestPreRecordFractionToleranceIsTheOnlySlack(t *testing.T) {
	c, _ := pipelineClient(t)
	predicted := ClampTickFraction16(0.25)
	// One present interval at 120 Hz against a 30 Hz update: a quarter tick.
	const windowTolerance = fractionOne / 4
	c.StartPreRecord(predicted, 0, false)
	c.JoinPreRecord()
	c.SetTickFraction(0.25)
	// One quantum of drift, well inside the window's tolerance.
	c.tickFraction16 = predicted + 1
	want := c.PresentationDigest()
	if _, ok := c.TakePreRecord(want, 0); ok {
		t.Fatal("a drifted fraction was accepted at zero tolerance; a measured frame would not be byte-identical")
	}
	c.StartPreRecord(predicted, 0, false)
	c.JoinPreRecord()
	c.tickFraction16 = predicted + 1
	if _, ok := c.TakePreRecord(c.PresentationDigest(), windowTolerance); !ok {
		t.Fatal("a fraction inside the window's tolerance was refused")
	}
	// A frame that arrived a whole present interval late is past the cap, and
	// takes the exact path however the tolerance was sized.
	c.StartPreRecord(predicted, 0, false)
	c.JoinPreRecord()
	c.tickFraction16 = predicted + windowTolerance + 1
	if _, ok := c.TakePreRecord(c.PresentationDigest(), windowTolerance); ok {
		t.Fatal("a drift past one present interval was presented instead of re-recorded")
	}
}

// A discarded pre-record must leave no trace on the presentation streams a
// recording pass advances. The CRT is the one of them that is a stream rather
// than a per-tick guarded cursor, so it is the one the rollback puts back
// [03 §2.4.1][I4].
func TestDiscardedPreRecordRollsBackThePresentationCRT(t *testing.T) {
	c, _ := pipelineClient(t)
	seed := rng.NewCRT(12345)
	c.SetPresentationCRT(&seed)
	before := *c.crt
	c.StartPreRecord(ClampTickFraction16(0.25), 0, false)
	c.JoinPreRecord()
	// Advance the stream the way a recording pass with segmented projectiles
	// would, then force the miss.
	c.crt.Rand()
	c.BumpPresentationEpoch()
	if _, ok := c.TakePreRecord(c.PresentationDigest(), 0); ok {
		t.Fatal("the forced miss was presented")
	}
	if *c.crt != before {
		t.Fatalf("presentation CRT after a discarded pre-record = %+v, want the launch value %+v", *c.crt, before)
	}
}

// syncBlendedX records one frame synchronously at fraction f through the
// ordinary producer path and reports the blended unit's X — the pose that pass
// put in front of every world draw. The degenerate pipeline client carries no
// unit art, so the blended view is where the recorded fraction is observable;
// the draw list it produces holds no fraction-dependent command.
func syncBlendedX(t *testing.T, f float32) numeric.Fixed {
	t.Helper()
	c, _ := pipelineClient(t)
	c.opts.TickFraction = func() float32 { return f }
	c.RecordModernFrame()
	return c.interp.view.Units[0].X
}

// A pre-recorded frame is recorded for the instant it was predicted for, so the
// recording pass consumes the fraction the launch installed
// (docs/DESIGN_GPU_RENDERER.md §13.10). The pass used to re-read the wall-clock
// producer instead: the world was recorded at the launch-time sample while the
// digest and the camera blend both used the prediction, and the presented pose
// trailed the camera framing it by about one present interval (§13.5).
func TestPreRecordBlendsAtThePredictedFraction(t *testing.T) {
	predicted := ClampTickFraction16(0.25)
	want := syncBlendedX(t, 0.25)

	// A producer whose every read is a later wall-clock sample, which is what a
	// millisecond source is. The drift stays inside the tolerance below, so a
	// pass that read it would still be presented — with the wrong pose.
	c, _ := pipelineClient(t)
	var calls int
	c.opts.TickFraction = func() float32 {
		calls++
		return 0.25 + 0.05*float32(calls)
	}

	// The tail of the previous Draw: predict, launch, record off the path.
	c.StartPreRecord(predicted, 0, false)
	c.JoinPreRecord()
	preRecordCalls := calls

	// The Draw that consumes it: the host boundary, the pipeline's one producer
	// sample, the digest, the take. One present interval of slack is what the
	// window passes.
	const windowTolerance = fractionOne / 4
	c.BeginPresentationFrame()
	c.ResolveTickFraction()
	if _, ok := c.TakePreRecord(c.PresentationDigest(), windowTolerance); !ok {
		t.Fatal("a prediction inside one present interval missed; the pipeline would never present a pre-recorded list")
	}
	if got := c.interp.view.Units[0].X; got != want {
		t.Fatalf("pre-recorded pose X = %d, want the synchronous record at the predicted fraction %d", got, want)
	}
	if preRecordCalls != 0 {
		t.Fatalf("the pre-record read the fraction producer %d times; it consumes the installed prediction", preRecordCalls)
	}
	if calls != 1 {
		t.Fatalf("the presented frame read the fraction producer %d times, want the pipeline's single sample (§13.10)", calls)
	}
}

// The miss path takes that same single sample: the frame recorded synchronously
// is the frame the digest compared, not a second, later one (§13.10).
func TestSynchronousRecordConsumesTheDigestedFraction(t *testing.T) {
	c, _ := pipelineClient(t)
	var calls int
	c.opts.TickFraction = func() float32 {
		calls++
		return 0.25 * float32(calls)
	}
	c.BeginPresentationFrame()
	c.ResolveTickFraction()
	// Nothing is in flight, so this Draw records synchronously.
	if _, ok := c.TakePreRecord(c.PresentationDigest(), 0); ok {
		t.Fatal("a list was taken with nothing in flight")
	}
	c.RecordModernFrame()
	if got, want := c.interp.view.Units[0].X, syncBlendedX(t, 0.25); got != want {
		t.Fatalf("synchronously recorded pose X = %d, want the pose the digest named %d", got, want)
	}
	if calls != 1 {
		t.Fatalf("the presented frame read the fraction producer %d times, want 1 (§13.10)", calls)
	}
}

// A client that never drives the pipeline — classic, `--shot`, a test harness —
// has no host settling its fraction, so every record resolves from the producer
// exactly as before (§13.10).
func TestRecordWithoutThePipelineResolvesFromTheProducer(t *testing.T) {
	c, _ := pipelineClient(t)
	fractions := []float32{0.25, 0.75}
	var calls int
	c.opts.TickFraction = func() float32 {
		f := fractions[calls%len(fractions)]
		calls++
		return f
	}
	for _, f := range fractions {
		c.RecordModernFrame()
		if got, want := c.interp.view.Units[0].X, syncBlendedX(t, f); got != want {
			t.Fatalf("pose X recorded at producer fraction %v = %d, want %d", f, got, want)
		}
	}
	if calls != len(fractions) {
		t.Fatalf("the producer was read %d times over %d records, want one per record", calls, len(fractions))
	}
}
