package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// nanoSegmentFixture is a strip session with a publication window, so a
// submitted segment can be observed on both halves at once: the frame event
// the client reads and the strip-6 emitter that spends the CRT draws.
func nanoSegmentFixture(t *testing.T) *Session {
	t.Helper()
	s, _ := newStripTestSession(21, 21)
	s.publication = newPublicationState(frame.NewEventBuffer(frame.Limits{}), 0)
	return s
}

func fx(v int64) numeric.Fixed { return numeric.FixedFromInt(v) }

// buildSegmentEvent is the event shape construction publishes for an accepted
// mobile-build or factory work step (construction.Service.emitAcceptedNano):
// the spray runs FROM the builder's nano piece INTO the product's box, so the
// box rides the event at the destination end [05 R-WORK-01 §8].
func buildSegmentEvent() frame.Event {
	return frame.Event{
		Tick: 7, Source: 3, Target: 4, Piece: 1,
		X: fx(100), Y: fx(10), Z: fx(100),
		TargetX: fx(300), TargetY: fx(0), TargetZ: fx(300),
		EffectID: 6, Mode: 1,
		Producer:                frame.ProducerBeam,
		PaletteRow:              6,
		NanolatheGeometryKnown:  true,
		NanolatheTargetBoxKnown: true,
		NanolatheTargetMin:      [3]numeric.Fixed{fx(280), fx(0), fx(280)},
		NanolatheTargetMax:      [3]numeric.Fixed{fx(320), fx(40), fx(320)},
	}
}

// TestBuildPresentationSpendsStripDraws is audit finding C-10's lock. Mobile
// construction, factory production and unit reclaim are emission producers of
// [05 R-P0-06 §1], so an accepted work step owes a frame event AND a strip-6
// emitter; the emitter's five construction-time particles cost six CRT draws
// each [03 R-STRIP-01 §3][03 §5.5]. The service's Presentation seam used to be
// bound straight to the frame event buffer, which published the cue and spent
// nothing — the CRT stream then sat where retail's never would, and it times
// wind, meteors and the victory instant [01 §7.5].
func TestBuildPresentationSpendsStripDraws(t *testing.T) {
	s := nanoSegmentFixture(t)
	s.Build = &construction.Service{}
	s.bindBuildPresentation()
	if s.Build.Presentation == nil {
		t.Fatal("construction presentation seam left unbound")
	}

	crt := s.CrtRNG()
	before := crt.Draws()
	if !s.Build.Presentation.EmitNanolathe(buildSegmentEvent()) {
		t.Fatal("build segment was not admitted to the frame window")
	}
	const want = nanoParticlesPerSpawnTick * nanoDrawsPerParticle
	if got := crt.Draws() - before; got != want {
		t.Fatalf("mobile build spent %d CRT draws, want %d", got, want)
	}
	if n := len(s.strips.strips[6]); n != 1 {
		t.Fatalf("strip 6 holds %d objects after one accepted work step, want 1", n)
	}
	if n := len(s.strips.strips[6][0].particles); n != nanoParticlesPerSpawnTick {
		t.Fatalf("emitter spawned %d particles at construction, want %d", n, nanoParticlesPerSpawnTick)
	}
	if len(s.publication.events.Events()) != 1 {
		t.Fatalf("frame events %d, want the one presentation cue", len(s.publication.events.Events()))
	}

	// A second accepted step is a second segment: the cost is per admitted
	// work step, not per builder [05 R-P0-06 §1].
	before = crt.Draws()
	s.Build.Presentation.EmitNanolathe(buildSegmentEvent())
	if got := crt.Draws() - before; got != want {
		t.Fatalf("second accepted step spent %d CRT draws, want %d", got, want)
	}
}

// TestNanolathePublicationCopiesTheAuthoritativeParticles locks the boundary
// between phase 11 and presentation. The strip object owns the two spawn
// ticks, particle movement, colour walk, expiry and CRT draws; publication
// copies the current particle records without spending a draw or creating a
// presentation-side lifetime [03 R-STRIP-01 §2–§3][03 §5.5][I6].
func TestNanolathePublicationCopiesTheAuthoritativeParticles(t *testing.T) {
	s := nanoSegmentFixture(t)
	s.Snapshot = frame.NewBuffer()
	s.Clock.GlobalTick = 7
	s.submitNanoSegment(buildSegmentEvent())

	crt := s.CrtRNG()
	draws := crt.Draws()
	s.publishSnapshot(7)
	first := s.Snapshot.Current()
	if first == nil {
		t.Fatal("construction tick did not publish a frame")
	}
	if got := len(first.Strips); got != nanoParticlesPerSpawnTick {
		t.Fatalf("published %d nano particles on the construction tick, want %d", got, nanoParticlesPerSpawnTick)
	}
	particles := s.strips.strips[6][0].particles
	for i, v := range first.Strips {
		p := particles[i]
		if v.Strip != 6 || v.Family != frame.StripFamilyNano || v.X != p.x || v.Y != p.y || v.Z != p.z || v.Fill != p.color {
			t.Fatalf("published particle %d = %+v, want the authoritative record at (%v,%v,%v) colour %#x", i, v, p.x, p.y, p.z, p.color)
		}
	}
	if got := crt.Draws(); got != draws {
		t.Fatalf("publication spent %d CRT draws, want none", got-draws)
	}

	// Five phase-11 visits move the first five particles, perform the second
	// five-particle spawn, then keep advancing the authoritative records. No
	// intermediate publication occurs: a later client can skip every one of
	// these frames and still receive exactly the final state.
	for tick := uint32(8); tick <= 12; tick++ {
		s.phaseObjectSweeps(tick)
	}
	if got := crt.Draws() - draws; got != nanoParticlesPerSpawnTick*nanoDrawsPerParticle {
		t.Fatalf("second nano spawn spent %d CRT draws, want %d", got, nanoParticlesPerSpawnTick*nanoDrawsPerParticle)
	}
	draws = crt.Draws()
	s.publishSnapshot(12)
	second := s.Snapshot.Current()
	if got := len(second.Strips); got != 2*nanoParticlesPerSpawnTick {
		t.Fatalf("published %d nano particles after five strip ticks, want %d", got, 2*nanoParticlesPerSpawnTick)
	}
	particles = s.strips.strips[6][0].particles
	for i, v := range second.Strips {
		p := particles[i]
		if v.Strip != 6 || v.Family != frame.StripFamilyNano || v.X != p.x || v.Y != p.y || v.Z != p.z || v.Fill != p.color {
			t.Fatalf("final published particle %d = %+v, want the authoritative record at (%v,%v,%v) colour %#x", i, v, p.x, p.y, p.z, p.color)
		}
	}
	if got := crt.Draws(); got != draws {
		t.Fatalf("final publication spent %d CRT draws, want none", got-draws)
	}
	if second.Strips[0].X == first.Strips[0].X && second.Strips[0].Y == first.Strips[0].Y && second.Strips[0].Z == first.Strips[0].Z {
		t.Fatal("the next committed frame retained the first particle's old position instead of phase-11 state")
	}
	if second.Strips[0].Fill == first.Strips[0].Fill {
		t.Fatal("the next committed frame retained the first particle's old green-ramp byte")
	}
}

// TestNanoSegmentGeometryFollowsPublishedBox locks the one mapping every
// producer shares. [05 R-P0-06 §4] builds the endpoints from the nano piece
// and the target grown by its footprint/model extents, and [05 R-WORK-01 §8]
// says which end carries the box: reclaim and capture reverse the spray. The
// draw cost is the same either way, so a mis-read flag is silent in the
// stream and visible only on screen.
func TestNanoSegmentGeometryFollowsPublishedBox(t *testing.T) {
	boxMin := [3]numeric.Fixed{fx(280), fx(0), fx(280)}
	boxMax := [3]numeric.Fixed{fx(320), fx(40), fx(320)}
	piece := [3]numeric.Fixed{fx(100), fx(10), fx(100)}

	// Every producer narrows its box to the 4/11..7/11 band before storing it
	// [03 §5.5], so the expectations come from the same helper.
	boxOrigin, boxExtent := narrowBox(boxMin, boxMax)
	pieceOrigin, pieceExtent := narrowBox(piece, piece)

	// Forward (build/assist/repair/resurrection): piece at the source, box at
	// the destination.
	s := nanoSegmentFixture(t)
	s.submitNanoSegment(buildSegmentEvent())
	o := s.strips.strips[6][0]
	if o.src != pieceOrigin || o.srcExtent != pieceExtent {
		t.Fatalf("forward source %v extent %v, want the degenerate nano piece %v", o.src, o.srcExtent, piece)
	}
	if o.dst != boxOrigin || o.dstExtent != boxExtent {
		t.Fatalf("forward destination %v extent %v, want the target box %v..%v", o.dst, o.dstExtent, boxMin, boxMax)
	}
	if o.dstExtent == ([3]numeric.Fixed{}) {
		t.Fatal("forward destination collapsed to a point: the target box was dropped")
	}

	// Reversed (unit reclaim/capture/feature reclaim): the box is the source
	// and the nano piece is the degenerate destination.
	r := nanoSegmentFixture(t)
	e := buildSegmentEvent()
	e.Mode = 2
	e.X, e.Y, e.Z = boxMin[0], boxMin[1], boxMin[2]
	e.TargetX, e.TargetY, e.TargetZ = piece[0], piece[1], piece[2]
	e.NanolatheBoxAtSource = true
	r.submitNanoSegment(e)
	o = r.strips.strips[6][0]
	if o.src != boxOrigin || o.srcExtent != boxExtent {
		t.Fatalf("reversed source %v extent %v, want the target box", o.src, o.srcExtent)
	}
	if o.dst != pieceOrigin || o.dstExtent != pieceExtent {
		t.Fatalf("reversed destination %v extent %v, want the degenerate nano piece", o.dst, o.dstExtent)
	}
}

// TestNanoSegmentDrawsSurviveFrameWindowOverflow keeps the presentation window
// out of the simulation's way [I6]: the frame event buffer is a bounded
// per-tick window, and its capacity must not decide how many CRT draws a tick
// spends.
func TestNanoSegmentDrawsSurviveFrameWindowOverflow(t *testing.T) {
	s, _ := newStripTestSession(22, 22)
	s.publication = newPublicationState(frame.NewEventBuffer(frame.Limits{MaxEvents: 1}), 0)
	s.Build = &construction.Service{}
	s.bindBuildPresentation()

	crt := s.CrtRNG()
	s.Build.Presentation.EmitNanolathe(buildSegmentEvent())
	before := crt.Draws()
	// The window is full, so this cue is dropped; the work step still happened.
	if s.Build.Presentation.EmitNanolathe(buildSegmentEvent()) {
		t.Fatal("event window admitted past its own limit; fixture no longer overflows")
	}
	const want = nanoParticlesPerSpawnTick * nanoDrawsPerParticle
	if got := crt.Draws() - before; got != want {
		t.Fatalf("dropped cue spent %d CRT draws, want %d: presentation capacity must not move the stream", got, want)
	}
}
