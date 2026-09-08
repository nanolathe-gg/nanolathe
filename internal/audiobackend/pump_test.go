package audiobackend

import (
	"testing"
	"time"
)

func TestPumpReapsLastBatchAtInclusiveDeadline(t *testing.T) {
	b := New()
	cue := &observedPlayer{playing: true}
	stream := &observedPlayer{playing: true}
	b.players = []voice{{player: cue}}
	b.streams = []voice{{player: stream}}
	now := time.Unix(100, 0)
	b.Pump(now)
	cue.playing, stream.playing = false, false
	b.Pump(now.Add(99 * time.Millisecond))
	if cue.stops != 0 || stream.stops != 0 {
		t.Fatal("reaped before 100 ms")
	}
	b.Pump(now.Add(100 * time.Millisecond))
	if len(b.players) != 0 || len(b.streams) != 0 || cue.stops != 1 || stream.stops != 1 {
		t.Fatal("deadline did not free the last batch without another play")
	}
	b.Pump(now.Add(200 * time.Millisecond))
	if cue.stops != 1 || stream.stops != 1 {
		t.Fatal("freed buffers retained by pump")
	}
}
