package client

// The record/submit pipeline (docs/DESIGN_GPU_RENDERER.md §13.10).
//
// Ebitengine's end-of-frame flush is synchronous with VSync on: once our Draw
// has returned, the game goroutine sits in the flush and the swap until the
// next Update. The renderer's Execute only enqueues — the device vertices were
// copied at enqueue — so from the moment Execute returns, the client's draw
// list and its scratch arenas are free for the whole of that wait. The pipeline
// spends it recording the NEXT frame on one persistent goroutine, and the game
// goroutine joins that record before it touches client state again.
//
// Determinism [I1][I6]: the pre-recorded list is Executed only when it is the
// list a synchronous record would have produced at this Draw. That is decided
// by comparing a PresentationInputs digest taken when the pre-record started
// against one taken at the Draw that would consume it; a mismatch discards the
// list, rolls back the one presentation stream a recording pass advances, and
// records synchronously exactly as before. Nothing here reaches authoritative
// state, and the pipeline goroutine never runs concurrently with input
// handling, the simulation step, or the audio drain.

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// PresentationInputs is the pipeline's validity digest: everything a recording
// pass reads that can change between two presented frames. Two records taken
// with equal digests record the same list, so an equal digest is what lets a
// pre-recorded list be Executed instead of re-recorded.
//
// Epoch carries the bulk of it. The host bumps it at every point where it can
// write client state — one bump per window Update, one per benchmark step — so
// input, selection, hover, the command page, the minimap viewport, pointer
// capture, focus, the renderer toggles, the camera the scroll pass moved and
// the newly published committed frame are all covered by one comparison rather
// than by a field list that would drift out of date behind them. The remaining
// fields are the state that changes at Draw time, after that bump: the two
// blend fractions, the audio drain's caption ring, the displayed resource pair,
// and the committed frame identity and camera origin as an epoch cross-check.
type PresentationInputs struct {
	// Epoch is the host's client-mutation counter (BumpPresentationEpoch).
	Epoch uint64
	// Committed is the committed frame this record reads, by identity and tick.
	Committed *frame.Frame
	Tick      uint32
	// TickFraction16 and CameraFraction16 are §13.5's two blend fractions in
	// the client's 16.16 domain. They are compared with a tolerance in the
	// window and exactly everywhere else; see PresentationInputs.missReason.
	TickFraction16    int32
	CameraFraction16  int32
	CameraFractionSet bool
	// The camera origin and its two stepped samples: what beginCameraBlend
	// blends between, and the origin every world site projects through.
	CamX, CamZ              int32
	CamSamples              uint8
	CamPrevView, CamCurView camera.PresentationView
	CamZoom                 camera.Zoom
	CamScale                camera.ViewScale
	// Interpolation and Enhanced are the two presentation switches a record
	// reads; Width and Height are the surface the pass composes for.
	Interpolation bool
	Enhanced      bool
	Width, Height int32
	// MessageProducer and MessageDisplay are the caption ring's cursors. The
	// audio drain is the one thing that runs on the game goroutine between a
	// pre-record and the Draw that consumes it, and the ring is what it writes
	// that the recorder reads [07 R-HUD-03 §14].
	MessageProducer uint16
	MessageDisplay  uint16
	// Resources is the pair the UI records, predicted for a pre-record and
	// advanced at the real host boundary before consumption.
	Resources DisplayedResources
}

// fractionsWithin reports whether the two digests' blend fractions agree to
// within tol quanta of the 16.16 fraction. tol is zero everywhere the recorded
// list must be byte-identical to a synchronous record.
func fractionsWithin(a, b PresentationInputs, tol int32) bool {
	if a.CameraFractionSet != b.CameraFractionSet {
		return false
	}
	d := a.TickFraction16 - b.TickFraction16
	if d < -tol || d > tol {
		return false
	}
	d = a.CameraFraction16 - b.CameraFraction16
	return d >= -tol && d <= tol
}

// MissReason names why a pre-recorded list could not be presented. It is
// prototype diagnosis, not a contract: the pipeline behaves identically
// whatever the reason.
type MissReason int

const (
	MissNone MissReason = iota
	// MissEpoch: the host wrote client state after the launch.
	MissEpoch
	// MissCommitted: a new tick was published after the launch.
	MissCommitted
	// MissTickFraction / MissCameraFraction: the prediction was too far out.
	MissTickFraction
	MissCameraFraction
	// MissOther: some other digest field moved — the camera origin, the
	// surface, a presentation switch, or the caption ring the audio drain
	// writes.
	MissOther
)

// MissReasonNames indexes MissReason for a host readout.
var MissReasonNames = [...]string{"none", "epoch", "committed", "tick-fraction", "camera-fraction", "other"}

func (a PresentationInputs) missReason(b PresentationInputs, tol int32) MissReason {
	switch {
	case a.Epoch != b.Epoch:
		return MissEpoch
	case a.Committed != b.Committed || a.Tick != b.Tick:
		return MissCommitted
	}
	x, y := a, b
	x.TickFraction16, y.TickFraction16 = 0, 0
	x.CameraFraction16, y.CameraFraction16 = 0, 0
	if x != y {
		return MissOther
	}
	d := a.TickFraction16 - b.TickFraction16
	if d < -tol || d > tol {
		return MissTickFraction
	}
	if !fractionsWithin(a, b, tol) {
		return MissCameraFraction
	}
	return MissNone
}

// preRecorder owns the pipeline goroutine and the state of the one record that
// may be in flight. Only the game goroutine touches these fields outside the
// window between a wake and the matching done, and inside that window the game
// goroutine touches none of them.
type preRecorder struct {
	wake chan struct{}
	done chan struct{}
	// pending is set from the wake until the game goroutine has joined and
	// decided what to do with the list; joined says the done has been received.
	pending bool
	joined  bool
	// recorded is the digest the in-flight (or waiting) list was recorded for.
	recorded PresentationInputs
	// hostFraction records that this client's host settles the blend fraction
	// itself, which every pipeline host does: the Draw resolves one producer
	// sample before it builds the digest, and a launch installs its prediction
	// in place of it. From then on the recording pass consumes c.tickFraction16
	// as an input and never reads the producer, so the list is recorded for the
	// instant the digest names rather than for a later wall-clock sample
	// (§13.10). A client that never uses the pipeline leaves it false and
	// resolves inside the record exactly as before. Written on the game
	// goroutine only; the worker reads it and never writes it.
	hostFraction bool
	// nanos is the wall time the last completed pre-record spent on its
	// goroutine. Written before the done is sent, read after it is received.
	nanos int64
	// saveCRT and restoreCRT are the presentation CRT's snapshot pair: the
	// segmented-projectile pass draws from that stream during a recording pass,
	// so a discarded record must not leave it advanced [03 §2.4.1][I4]. They
	// close over one shared slot and are built once per bound stream — crtFor
	// is the pointer they were built for — so a launch neither allocates nor
	// names the simulation's generator type in a presentation file (DET-01).
	saveCRT, restoreCRT func()
	crtFor              any
	crtSaved            bool

	hits, misses, launches int64
	// missReasons counts why misses happened and driftTick/driftCamera are the
	// largest fraction errors a prediction has produced, both prototype
	// diagnosis for the host's readout.
	missReasons            [len(MissReasonNames)]int64
	driftTick, driftCamera int32
	// driftHitTick and driftHitCamera are the same maxima over the predictions
	// that were actually PRESENTED. Those are the divergence: a presented
	// frame shows the instant it was predicted for, so this pair is how far
	// from the measured instant any presented frame has been. The unqualified
	// maxima above include the predictions the tolerance threw out, which
	// diagnose the prediction rather than the picture.
	driftHitTick, driftHitCamera int32
	// driftHitSum and driftHitCount accumulate the presented tick drift so the
	// readout can print a mean beside the maximum. The maximum is a hitch — a
	// present interval that stretched — and the mean is what the picture
	// actually shows.
	driftHitSum   int64
	driftHitCount int64
}

// The fraction tolerance is the host's, not a constant here. TakePreRecord's
// tol argument is what decides whether a pre-recorded list may be presented at
// the fraction it was recorded for instead of being re-recorded at the measured
// one, and the two hosts answer it differently (§13.10):
//
//   - The benchmark and `--shot` pass zero. They know the next frame's fraction
//     exactly, so a measured frame stays byte-identical to a synchronous record.
//   - The window passes one present interval, computed per Draw from its own
//     measured present period. It accepts the prediction: the presented frame
//     is shown at the instant it was predicted for, and the error is present
//     jitter, capped at one interval so a frame that arrived a whole refresh
//     late still takes the exact path.
//
// A fixed constant could not serve the window. The battle's tick fraction comes
// from a millisecond source, so at the nominal speed it moves in steps of
// 30/1000 of a tick — about 1966 quanta — and any tolerance smaller than one of
// its steps fails on a single millisecond of draw jitter, which is most of what
// a present interval contains.

// BumpPresentationEpoch records that the host has written client state. The
// pipeline treats every pre-record taken before the bump as stale. Call it
// once, before any batch of writes, not per write.
func (c *Client) BumpPresentationEpoch() {
	if c == nil {
		return
	}
	c.presentationEpoch++
}

// PresentationDigest takes the digest of the state a recording pass would read
// right now. Callers settle the blend fractions first (SetCameraFraction,
// ResolveTickFraction) so the digest and the record agree.
func (c *Client) PresentationDigest() PresentationInputs {
	if c == nil {
		return PresentationInputs{}
	}
	d := PresentationInputs{
		Epoch:             c.presentationEpoch,
		TickFraction16:    c.tickFraction16,
		CameraFraction16:  c.cameraFraction16,
		CameraFractionSet: c.cameraFractionSet,
		CamSamples:        c.camSamples,
		CamPrevView:       c.camPrevView, CamCurView: c.camCurView,
		Interpolation:   c.interpolation,
		Enhanced:        c.enhanced,
		Width:           int32(c.width),
		Height:          int32(c.height),
		MessageProducer: c.messages.Producer,
		MessageDisplay:  c.messages.Display,
		Resources:       c.displayedResources,
	}
	if c.buffer != nil {
		if cur := c.buffer.Current(); cur != nil {
			d.Committed, d.Tick = cur, cur.Tick
		}
	}
	if c.cam != nil {
		d.CamX, d.CamZ = c.cam.X, c.cam.Z
		d.CamZoom, d.CamScale = c.cam.Zoom, c.cam.Scale
	}
	return d
}

// StartPreRecord records the next frame on the pipeline goroutine at the
// supplied predicted fractions. It must be called after Execute has returned
// for the current frame and before the game goroutine blocks in the flush; the
// caller must JoinPreRecord before it writes client state again.
//
// A pre-record already in flight is joined and discarded first, so a caller
// that launches twice without consuming cannot leave two records racing.
func (c *Client) StartPreRecord(tickFraction16, cameraFraction16 int32, cameraFractionSet bool) {
	if c == nil {
		return
	}
	if c.pre.pending {
		c.JoinPreRecord()
		c.dropPreRecord()
	}
	if c.pre.wake == nil {
		c.pre.wake = make(chan struct{})
		c.pre.done = make(chan struct{})
		go c.servePreRecord()
	}
	// Install the predicted fractions. They are the recording pass's input, not
	// a hint it may re-derive: the worker blends with exactly these two numbers,
	// and the digest below is taken from them, so a hit presents the list the
	// digest describes. Re-reading the wall-clock producer inside the pass would
	// record the world at the launch instant while the camera used the
	// prediction, which is the world a present interval behind its camera
	// (§13.10). The Draw that consumes the list settles its own fractions and
	// the digest comparison decides; a miss simply re-records with those.
	c.pre.hostFraction = true
	c.tickFraction16 = tickFraction16
	c.cameraFraction16 = cameraFraction16
	c.cameraFractionSet = cameraFractionSet
	c.savePresentationCRT()
	c.pre.recorded = c.PresentationDigest()
	// Prediction is pure: retries and discards leave the retained pair alone.
	c.pre.recorded.Resources = c.nextDisplayedResources()
	c.pre.pending = true
	c.pre.joined = false
	c.pre.launches++
	c.pre.wake <- struct{}{}
}

func (c *Client) servePreRecord() {
	for range c.pre.wake {
		start := time.Now()
		c.recordNextResources = true
		c.RecordModernFrame()
		c.recordNextResources = false
		c.pre.nanos = int64(time.Since(start))
		c.pre.done <- struct{}{}
	}
}

// JoinPreRecord waits for an in-flight pre-record to finish. It is the barrier
// that keeps input handling, the simulation step and the audio drain off the
// pipeline goroutine's back: nothing that writes client state may run before it
// has returned. It is cheap and idempotent when nothing is in flight.
func (c *Client) JoinPreRecord() {
	if c == nil || !c.pre.pending || c.pre.joined {
		return
	}
	<-c.pre.done
	c.pre.joined = true
}

// TakePreRecord hands back the pre-recorded list when it is the list a
// synchronous record would produce for `want`, and reports the miss otherwise.
// tol is the fraction tolerance; pass zero where the list must be
// byte-identical to a synchronous record.
func (c *Client) TakePreRecord(want PresentationInputs, tol int32) (*drawlist.List, bool) {
	if c == nil || !c.pre.pending {
		return nil, false
	}
	c.JoinPreRecord()
	c.pre.pending = false
	reason := c.pre.recorded.missReason(want, tol)
	c.pre.observeDrift(want, reason == MissNone)
	if reason == MissNone {
		c.pre.hits++
		return &c.list, true
	}
	c.pre.misses++
	c.pre.missReasons[reason]++
	c.dropPreRecord()
	return nil, false
}

// dropPreRecord discards a joined pre-record's product. The list itself needs
// no undoing — the next recording pass resets it — but the presentation CRT the
// segmented-projectile pass drew from is a stream, and it is put back where the
// launch found it so the re-record draws the same values a single record would
// have [I4]. Everything else a recording pass writes is either rebuilt from
// scratch by the next pass (the blended view, the strip buckets, the arenas) or
// guarded against advancing twice within one committed tick (the trail layer,
// the feature animation cursors).
func (c *Client) dropPreRecord() {
	if c.pre.crtSaved && c.pre.restoreCRT != nil {
		c.pre.restoreCRT()
	}
	c.pre.crtSaved = false
}

// SnapshotPresentationCRT copies the presentation CRT and returns the function
// that puts the copy back. It is the pre-recorder's rollback made available to
// a host that composes the same committed frame twice — the `--shot-renderer
// both` route, whose two recordings must each start from the CRT state a
// single recording would have seen, or the segmented-projectile pass of [03
// §5.4] draws different points in the second one [I4].
//
// The returned function is idempotent and safe to call once per snapshot; a
// client with no bound stream returns a no-op. Like the pair below, it infers
// the generator from the bound pointer and never names it (DET-01). It does
// not touch the pre-recorder's own snapshot slot, so a host may hold one of
// these across a launch without disturbing dropPreRecord.
func (c *Client) SnapshotPresentationCRT() func() {
	if c == nil || c.crt == nil {
		return func() {}
	}
	p := c.crt
	v := *p
	return func() { *p = v }
}

// savePresentationCRT copies the presentation CRT into the snapshot pair's
// shared slot, building the pair the first time and again whenever a different
// stream has been bound. The pair is written here rather than in audio.go so
// the generator's type is inferred from the bound pointer and never named: a
// presentation package that spells internal/sim/rng is exactly what DET-01's
// guard rejects, and the rollback needs the value, not the name.
func (c *Client) savePresentationCRT() {
	if c.crt == nil {
		c.pre.crtSaved = false
		return
	}
	if c.pre.crtFor != c.crt || c.pre.saveCRT == nil {
		p := c.crt
		v := *p
		c.pre.saveCRT = func() { v = *p }
		c.pre.restoreCRT = func() { *p = v }
		c.pre.crtFor = p
	}
	c.pre.saveCRT()
	c.pre.crtSaved = true
}

// observeDrift records the largest prediction error the pipeline has seen, in
// quanta of the 16.16 fraction, over every prediction and again over the
// predictions that were presented. The first says whether the tolerance is the
// right size for this host's present jitter; the second is the divergence
// itself, since a presented frame is shown at the instant it was predicted for
// (§13.10).
func (p *preRecorder) observeDrift(want PresentationInputs, presented bool) {
	tick := numeric.Abs(p.recorded.TickFraction16 - want.TickFraction16)
	if tick > p.driftTick {
		p.driftTick = tick
	}
	if presented {
		if tick > p.driftHitTick {
			p.driftHitTick = tick
		}
		p.driftHitSum += int64(tick)
		p.driftHitCount++
	}
	if !p.recorded.CameraFractionSet || !want.CameraFractionSet {
		return
	}
	camera := numeric.Abs(p.recorded.CameraFraction16 - want.CameraFraction16)
	if camera > p.driftCamera {
		p.driftCamera = camera
	}
	if presented && camera > p.driftHitCamera {
		p.driftHitCamera = camera
	}
}

// PreRecordMisses reports the miss counts by reason, indexed by MissReason, and
// the largest tick and camera fraction prediction errors seen, in quanta.
func (c *Client) PreRecordMisses() (reasons [len(MissReasonNames)]int64, driftTick, driftCamera int32) {
	if c == nil {
		return reasons, 0, 0
	}
	return c.pre.missReasons, c.pre.driftTick, c.pre.driftCamera
}

// PreRecordPresentedDrift reports the largest tick and camera fraction errors
// over the predictions that were PRESENTED, in quanta. That pair is the
// pipeline's presentation divergence measured rather than bounded: how far the
// instant a presented frame was recorded for has been from the instant it was
// presented at (§13.10), with the mean tick error beside it.
func (c *Client) PreRecordPresentedDrift() (driftTick, driftCamera int32, meanTick float64) {
	if c == nil {
		return 0, 0, 0
	}
	if c.pre.driftHitCount > 0 {
		meanTick = float64(c.pre.driftHitSum) / float64(c.pre.driftHitCount)
	}
	return c.pre.driftHitTick, c.pre.driftHitCamera, meanTick
}

// PreRecordNanos is the wall time the last completed pre-record spent on the
// pipeline goroutine.
func (c *Client) PreRecordNanos() int64 {
	if c == nil {
		return 0
	}
	return c.pre.nanos
}

// PreRecordCounts reports the pipeline's hits, misses and launches for the life
// of the client. A launch that is never consumed — the last frame of a run —
// counts as neither a hit nor a miss.
func (c *Client) PreRecordCounts() (hits, misses, launches int64) {
	if c == nil {
		return 0, 0, 0
	}
	return c.pre.hits, c.pre.misses, c.pre.launches
}

// CancelPreRecord joins and discards speculative work before entering the
// paused synchronous split. Roll back its presentation CRT exactly as on a
// digest miss; no update or paused foreground can race the worker (§13.10).
func (c *Client) CancelPreRecord() {
	if c == nil || !c.pre.pending {
		return
	}
	c.JoinPreRecord()
	c.pre.pending = false
	c.dropPreRecord()
}
