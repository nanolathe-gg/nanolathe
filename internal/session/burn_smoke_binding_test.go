package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestBurnSmokeProducerSpendsTheContainersLastFrameDraw locks the third of the
// emission's three CRT draws at the place [05 R-FEAT-01 §16] puts it: inside
// the producer, once per emission, because the one-shot container's init calls
// its spawn virtual and that spawn draws the puff's last frame
// [03 R-STRIP-01 §2]. The feature site's two jitter draws are the other two and
// are locked in internal/features.
//
// It also pins the container the binding builds: one strip-5 object holding one
// puff, with the trail/`endsmoke` init row's lifetime of 0 — so the window is
// already closed at the creation tick and the container never spawns again.
func TestBurnSmokeProducerSpendsTheContainersLastFrameDraw(t *testing.T) {
	s, crt := newStripTestSession(23, 23)
	s.Clock.GlobalTick = 40
	s.Features = &features.Service{}
	s.bindFeatureStripProducers()
	if s.Features.BurnSmoke == nil {
		t.Fatal("the composer left BurnSmoke unbound; the emission would lose its third draw")
	}

	pos := [3]numeric.Fixed{numeric.FixedFromInt(48), numeric.FixedFromInt(12), numeric.FixedFromInt(80)}
	before := crt.Draws()
	s.Features.BurnSmoke(pos)
	if got := crt.Draws() - before; got != 1 {
		t.Fatalf("the producer spent %d CRT draws, want exactly 1 — the puff's last frame [05 R-FEAT-01 §16]", got)
	}

	objs := s.strips.strips[stripBurningFeatureSmoke]
	if len(objs) != 1 {
		t.Fatalf("strip %d holds %d objects, want the one fresh container [05 R-FEAT-01 §16]", stripBurningFeatureSmoke, len(objs))
	}
	o := objs[0]
	if len(o.particles) != 1 {
		t.Fatalf("the container holds %d puffs, want the constructor's single one [05 R-FEAT-01 §16]", len(o.particles))
	}
	if o.src != pos {
		t.Fatalf("the container sits at %v, want the jittered point %v", o.src, pos)
	}
	// Lifetime 0: the window ends at the creation tick, which is what refuses
	// the second spawn and retires the container once its puff is gone
	// [03 R-FX-01 §3 addendum].
	if o.windowEnd != s.Clock.GlobalTick {
		t.Fatalf("window ends at %d, want the creation tick %d (lifetime 0) [05 R-FEAT-01 §16]", o.windowEnd, s.Clock.GlobalTick)
	}
	if o.smokeSelector != 0 {
		t.Fatalf("smoke selector %d, want 0 — the puff is `smoke 1` [05 R-FEAT-01 §16]", o.smokeSelector)
	}
	if o.frameDelayParam != smokeDefaultFrameDelay {
		t.Fatalf("frame hold %d, want the family default %d (the init row's frameHold is 0) [05 R-FEAT-01 §16]",
			o.frameDelayParam, smokeDefaultFrameDelay)
	}
}
