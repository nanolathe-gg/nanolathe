package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// Retirement belongs to the session's once-per-pump tail. Audio draining must
// not retire a second line when a client happens to have an audio owner.
func TestTickAudioDoesNotRetireMessageLines(t *testing.T) {
	b := &frame.Buffer{}
	b.BeginWrite()
	if err := b.Publish(31); err != nil {
		t.Fatal(err)
	}
	c := &Client{buffer: b, messages: *frame.NewMessageRing()}
	c.messages.Configure(4, 0)
	c.messages.Append("overdue", 1, 0, 10, 0)
	c.SetAudioService(audio.NewService(nil))
	c.TickAudio()
	if got := c.MessageLines(); len(got) != 1 || got[0].Text != "overdue" {
		t.Fatalf("audio drain retired message lines %#v", got)
	}
}
