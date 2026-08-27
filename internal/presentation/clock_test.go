package presentation

import (
	"testing"
	"time"
)

func TestClockSeparatesFrameAndSimulationDomains(t *testing.T) {
	var c Clock
	c.BeginFrame(12, 17*time.Millisecond)
	if c.SimTick != 12 || c.FrameSerial != 1 || c.WallDelta != 17*time.Millisecond || !c.NewSimTick {
		t.Fatalf("first frame = %#v", c)
	}
	if !c.ConsumeNewSimTick() || c.ConsumeNewSimTick() {
		t.Fatal("new-tick boundary was not one-shot")
	}
	c.BeginFrame(12, 3*time.Millisecond)
	if c.FrameSerial != 2 || c.NewSimTick {
		t.Fatalf("same-tick frame = %#v", c)
	}
	c.BeginFrame(13, 0)
	if c.FrameSerial != 3 || !c.NewSimTick || !c.ConsumeNewSimTick() {
		t.Fatalf("new tick was not reported once: %#v", c)
	}
}

func TestClockFrameSerialWrapsWithoutChangingTick(t *testing.T) {
	c := Clock{FrameSerial: ^uint32(0), SimTick: 4, initialized: true}
	c.BeginFrame(4, 0)
	if c.FrameSerial != 0 || c.NewSimTick {
		t.Fatalf("clock overflow handling = %#v", c)
	}
}
