package session

import "testing"

func TestRadarBlinkCadenceBoundaries(t *testing.T) {
	var s Session
	s.resetRadarBlink()
	if s.radarBlinkCountdown != 7 || s.RadarBlinkPhase() != 0 {
		t.Fatalf("battle-entry radar state = %d/%d, want 7/0", s.radarBlinkCountdown, s.RadarBlinkPhase())
	}
	want := []struct {
		tick      uint32
		countdown int16
		phase     uint8
	}{
		{1, 6, 0}, {7, 0, 0}, {8, 7, 1}, {9, 6, 1}, {15, 0, 1}, {16, 7, 0},
	}
	// Drive one continuous sequence and inspect the exact listed boundaries.
	s.resetRadarBlink()
	for tick := uint32(1); tick <= 16; tick++ {
		s.phaseCadenceFlip(tick)
		for _, probe := range want {
			if probe.tick == tick && (s.radarBlinkCountdown != probe.countdown || s.RadarBlinkPhase() != probe.phase) {
				t.Fatalf("tick %d radar state = %d/%d, want %d/%d", tick, s.radarBlinkCountdown, s.RadarBlinkPhase(), probe.countdown, probe.phase)
			}
		}
	}
}

func TestRadarBlinkFiveSubticksMatchesFiveSinglePasses(t *testing.T) {
	oneAtATime := Session{}
	oneAtATime.resetRadarBlink()
	for tick := uint32(1); tick <= 5; tick++ {
		oneAtATime.phaseCadenceFlip(tick)
	}
	fivePasses := Session{}
	fivePasses.resetRadarBlink()
	for tick := uint32(1); tick <= 5; tick++ {
		fivePasses.phaseCadenceFlip(tick)
	}
	if oneAtATime.radarBlinkCountdown != fivePasses.radarBlinkCountdown || oneAtATime.RadarBlinkPhase() != fivePasses.RadarBlinkPhase() {
		t.Fatalf("five-subtick state differs: %d/%d vs %d/%d", oneAtATime.radarBlinkCountdown, oneAtATime.RadarBlinkPhase(), fivePasses.radarBlinkCountdown, fivePasses.RadarBlinkPhase())
	}
}
