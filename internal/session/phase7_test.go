package session

import "testing"

type phase7Probe struct{ calls int }

func (p *phase7Probe) StepPhase7() { p.calls++ }

func TestPhase7ServiceRunsOncePerSubTick(t *testing.T) {
	var s Session
	probe := &phase7Probe{}
	s.SetPhase7Service(probe)
	for i := 0; i < 5; i++ {
		s.stepOneSubTick(uint32(i + 1))
	}
	if probe.calls != 5 {
		t.Fatalf("phase-7 calls = %d, want 5", probe.calls)
	}
}

func TestPhase7ServiceCanBeCleared(t *testing.T) {
	var s Session
	probe := &phase7Probe{}
	s.SetPhase7Service(probe)
	s.phaseSequences(1)
	s.SetPhase7Service(nil)
	s.phaseSequences(2)
	if probe.calls != 1 {
		t.Fatalf("cleared phase-7 service calls = %d, want 1", probe.calls)
	}
}
