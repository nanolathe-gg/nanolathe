package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
)

func TestSchedulingAPIUpdatesPauseSynchronously(t *testing.T) {
	s := &Session{Clock: &clock.State{ScaledAnchor: 90, Carry: 0.5, Requested: 10, Active: 10}}
	if !s.SetPaused(true) || !s.Clock.Paused {
		t.Fatal("SetPaused(true) did not set the gate synchronously")
	}
	if s.Clock.ScaledAnchor != 90 || s.Clock.Carry != 0.5 {
		t.Fatalf("pause changed SP anchor/carry: anchor=%d carry=%v", s.Clock.ScaledAnchor, s.Clock.Carry)
	}
	if !s.SetPaused(false) || s.Clock.Paused {
		t.Fatal("SetPaused(false) did not resume synchronously")
	}
}

func TestSchedulingAPISpeedClampAndActiveUpdate(t *testing.T) {
	s := &Session{Clock: &clock.State{Requested: 10, Active: 10}}
	if speed, changed := s.AdjustSpeed(-20); speed != 1 || !changed || s.Clock.Requested != 1 || s.Clock.Active != 1 {
		t.Fatalf("low speed result=%d changed=%t clock=%+v", speed, changed, s.Clock)
	}
	if speed, changed := s.AdjustSpeed(-1); speed != 1 || changed {
		t.Fatalf("low clamp result=%d changed=%t", speed, changed)
	}
	if speed, changed := s.AdjustSpeed(99); speed != 20 || !changed || s.Clock.Requested != 20 || s.Clock.Active != 20 {
		t.Fatalf("high speed result=%d changed=%t clock=%+v", speed, changed, s.Clock)
	}
}
