package main

import (
	"testing"
	"time"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/ui"
)

type pumpProbe struct {
	calls int
	now   time.Time
}

func (*pumpProbe) PlaySample(*audio.Sample, float64, float64) error { return nil }
func (p *pumpProbe) Pump(now time.Time)                             { p.calls++; p.now = now }

func TestShellPumpsAudioWithoutBattleTick(t *testing.T) {
	prior := audio.GlobalOutput()
	t.Cleanup(func() { audio.SetGlobalOutput(prior) })
	probe := &pumpProbe{}
	audio.SetGlobalOutput(probe)
	// No session or client exists: the common presentation edge must still
	// release finished buffers, rather than depending on a battle tick.
	g := &gameShell{frontend: &ui.Frontend{Mode: modeBattle}}
	g.step(0, nil)
	if probe.calls != 1 || probe.now.IsZero() {
		t.Fatal("shell did not pump the output independently of battle ticks")
	}
}
