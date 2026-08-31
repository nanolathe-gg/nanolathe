package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/frame"
)

func TestCommittedStatusEventReachesMessageLines(t *testing.T) {
	b := &frame.Buffer{}
	f := b.BeginWrite()
	f.Events = append(f.Events, frame.EventView{
		Kind: frame.EventKindStatus, Tick: 7, Source: 3,
		StatusKind: 7, StatusText: "ARMADA: Can't build", StatusClass: 1,
	})
	if err := b.Publish(7); err != nil {
		t.Fatal(err)
	}
	a := audio.NewService(nil)
	a.Queue.Register(3, nil, "ARMADA", true)
	c := &Client{buffer: b, messages: *frame.NewMessageRing(), screenChat: 0}
	c.SetAudioService(a)
	c.TickAudio()
	lines := c.MessageLines()
	if len(lines) != 1 || lines[0].Text != "ARMADA: Can't build" || lines[0].SourceUnit != 3 {
		t.Fatalf("message lines = %#v", lines)
	}
	c.TickAudio() // drawing the same committed frame must not duplicate it
	if got := len(c.MessageLines()); got != 1 {
		t.Fatalf("duplicate committed event produced %d lines", got)
	}
}
