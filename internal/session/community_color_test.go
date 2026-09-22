package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"testing"
)

func TestNanoColorMetadataPreservesDrawsAndSpawnOrder(t *testing.T) {
	s, crt := newStripTestSession(7, 7)
	plain, plainCRT := newStripTestSession(7, 7)
	src := [3]numeric.Fixed{}
	dst := [3]numeric.Fixed{numeric.FixedFromInt(100)}
	// A discarded zero-length hop has no colour assignment.
	s.appendStripNanoEmitter(src, src, 3)
	plain.appendStripNanoEmitter(src, src)
	for i := 0; i < 2; i++ {
		s.appendStripNanoEmitter(src, dst, 3)
		plain.appendStripNanoEmitter(src, dst)
	}
	views := s.appendStripViews(0, nil)
	if len(views) != 10 {
		t.Fatalf("particles=%d", len(views))
	}
	for i, v := range views {
		if !v.NanoOwnerColorKnown || v.NanoOwnerColor != 3 || v.ColorSequence != uint32(i) || v.ColorSample != uint8(i%5) {
			t.Fatalf("particle %d: %+v", i, v)
		}
	}
	s.phaseObjectSweeps(1)
	plain.phaseObjectSweeps(1)
	if crt.Draws() != plainCRT.Draws() || crt.State != plainCRT.State {
		t.Fatal("colour metadata changed CRT stream")
	}
	again := s.appendStripViews(1, nil)
	if len(again) == 0 || again[0].ColorSequence != views[0].ColorSequence || again[0].ColorSample != views[0].ColorSample {
		t.Fatal("aging changed colour assignment")
	}
}
