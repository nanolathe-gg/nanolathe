package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// teleportFlameEndpoints is a span long enough that the segment life
// floor(spanUnits/5) is well clear of zero, so each segment lives past the next
// segment's lay and the cadence is observable [03 R-FX-02 §2].
var teleportFlameFrom = [3]numeric.Fixed{
	numeric.FixedFromInt(100), numeric.FixedFromInt(20), numeric.FixedFromInt(200),
}
var teleportFlameTo = [3]numeric.Fixed{
	numeric.FixedFromInt(400), numeric.FixedFromInt(20), numeric.FixedFromInt(200),
}

// TestTeleportFlameContainerLaysFourSegmentsTenTicksApart locks the strip-5
// producer's shape against [03 R-LAYER §4]: a 30-tick container that lays one
// animated segment every 10 ticks between the moved unit's old and new
// position, and one CRT draw per segment for its random start frame
// [R-STRIP-01 §3].
//
// Four segments, not three or five: the constructor lays the first, and the
// phase-11 gate lays one at each of ticks +10, +20 and +30 before the window
// closes [03 R-FX-02 §2].
func TestTeleportFlameContainerLaysFourSegmentsTenTicksApart(t *testing.T) {
	s, crt := newStripTestSession(31, 31)
	s.Clock.GlobalTick = 500

	before := crt.Draws()
	if !s.appendStripFlameStream(teleportFlameFrom, teleportFlameTo) {
		t.Fatal("the producer built no container")
	}
	if got := crt.Draws() - before; got != 1 {
		t.Fatalf("the producer spent %d CRT draws, want exactly 1 — the first segment's start frame [R-STRIP-01 §3]", got)
	}

	objs := s.strips.strips[stripTeleportFlame]
	if len(objs) != 1 {
		t.Fatalf("strip %d holds %d objects, want the one fresh container [03 R-LAYER §4]", stripTeleportFlame, len(objs))
	}
	o := objs[0]
	if o.family != stripFamilyFlame {
		t.Fatalf("container family %d, want the flame-stream family [03 R-LAYER §4]", o.family)
	}
	if o.src != teleportFlameFrom || o.dst != teleportFlameTo {
		t.Fatalf("container endpoints %v→%v, want the producer's %v→%v — the segments fly from the OLD position to the displaced one [03 R-LAYER §4]",
			o.src, o.dst, teleportFlameFrom, teleportFlameTo)
	}
	if o.windowEnd != 500+uint32(flameContainerLifetime) {
		t.Fatalf("window ends at %d, want the 30-tick container's %d [03 R-FX-02 §2]", o.windowEnd, 500+uint32(flameContainerLifetime))
	}
	if o.spawnInterval != flameSegmentInterval {
		t.Fatalf("spawn interval %d, want the one-segment-per-10-ticks cadence %d [03 R-LAYER §4]", o.spawnInterval, flameSegmentInterval)
	}
	if len(o.particles) != 1 {
		t.Fatalf("the constructor laid %d segments, want its single first one [R-STRIP-01 §2]", len(o.particles))
	}

	// The sweep from the creation tick onward: one lay at each of +10, +20 and
	// +30, one CRT draw each, and nothing at +40 — that slot falls outside the
	// container's window [R-STRIP-01 §2].
	lays := 1
	seen := 1
	draws := crt.Draws()
	for tick := uint32(501); tick <= 620; tick++ {
		s.Clock.GlobalTick = tick
		held := len(s.strips.strips[stripTeleportFlame])
		s.strips.sweep(tick, s)
		if held == 0 {
			break
		}
		objs := s.strips.strips[stripTeleportFlame]
		if len(objs) == 0 {
			continue
		}
		if n := len(objs[0].particles); n > seen {
			lays += n - seen
		}
		seen = len(objs[0].particles)
	}
	if lays != 4 {
		t.Fatalf("the container laid %d segments, want the four of a 30-tick life at a 10-tick cadence [03 R-FX-02 §2]", lays)
	}
	if got := crt.Draws() - draws; got != 3 {
		t.Fatalf("the sweep spent %d CRT draws, want one per laid segment (3 after the constructor's) [R-STRIP-01 §3]", got)
	}
	if len(s.strips.strips[stripTeleportFlame]) != 0 {
		t.Fatal("the container outlived its last segment; the flame family's verdict is an empty sub-record list [R-STRIP-01 §2]")
	}
}

// TestTeleportPresentationSeamBuildsTheFlameContainer locks the binding itself:
// the `Teleport` row's presentation callback is the producer's only caller, and
// the row hands it the old and new positions because it calls BEFORE the
// position commit [04 R-ORD-01 §2][03 R-LAYER §4].
func TestTeleportPresentationSeamBuildsTheFlameContainer(t *testing.T) {
	s, _ := newStripTestSession(37, 37)
	s.Clock.GlobalTick = 12

	binding := s.newOrderBinding()
	if binding == nil || binding.Presentation == nil || binding.Presentation.Teleport == nil {
		t.Fatal("the composer left PresentationAdapter.Teleport unbound; the row would move units with no flame stream [03 R-LAYER §4]")
	}
	if !binding.Presentation.Teleport(nil,
		teleportFlameFrom[0], teleportFlameFrom[1], teleportFlameFrom[2],
		teleportFlameTo[0], teleportFlameTo[1], teleportFlameTo[2]) {
		t.Fatal("the seam refused to build a container")
	}
	objs := s.strips.strips[stripTeleportFlame]
	if len(objs) != 1 || objs[0].family != stripFamilyFlame {
		t.Fatalf("the seam put %d objects on strip %d, want one flame-stream container [03 R-LAYER §4]", len(objs), stripTeleportFlame)
	}
	if objs[0].src != teleportFlameFrom || objs[0].dst != teleportFlameTo {
		t.Fatalf("the container runs %v→%v, want the row's old→new pair", objs[0].src, objs[0].dst)
	}
}
