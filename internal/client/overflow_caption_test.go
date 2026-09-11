package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// A full cue queue resolves its displaced tail during insertion, before the
// ordinary drain. Both captions start ageing at this presentation tick
// [03 §8.3][07 R-HUD-03 §14.3].
func TestOverflowCaptionUsesCurrentDrainTick(t *testing.T) {
	b := &frame.Buffer{}
	a := audio.NewService(nil)
	a.Queue.Register(4, nil, "ARMPW", true)
	a.Queue.Configure(10, 10, true, true)
	c := &Client{buffer: b, messages: *frame.NewMessageRing(), messageEventsTick: 7}
	c.SetAudioService(a)
	var events []frame.EventView
	for slot := uint8(1); slot <= 9; slot++ {
		events = append(events, frame.EventView{
			Kind: frame.EventKindStatus, Tick: 500, Source: 4,
			StatusKind: slot, StatusText: "status",
		})
	}
	publishTickEvents(t, b, 500, events...)
	c.TickAudio()
	lines := c.MessageLines()
	if len(lines) != 2 {
		t.Fatalf("captions = %#v, want overflow and ordinary drain", lines)
	}
	for _, line := range lines {
		if line.StoredTick != 500 {
			t.Fatalf("caption stored at %d, want current drain tick 500", line.StoredTick)
		}
	}
	if c.messages.RetireOne(500) {
		t.Fatal("fresh overflow caption expired immediately")
	}
}
