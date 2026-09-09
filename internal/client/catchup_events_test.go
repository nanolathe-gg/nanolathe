package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// publishAudioTick publishes one committed tick carrying the supplied events.
func publishTickEvents(t *testing.T, b *frame.Buffer, tick uint32, events ...frame.EventView) {
	t.Helper()
	f := b.BeginWrite()
	f.Events = append(f.Events, events...)
	if err := b.Publish(tick); err != nil {
		t.Fatalf("publish tick %d: %v", tick, err)
	}
}

func admittedCue(tick uint32, alias string) frame.EventView {
	return frame.EventView{
		Kind: frame.EventKindAudio, Tick: tick, Sound: alias,
		AudioPositional: true, AudioAudible: true,
		X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0),
	}
}

// TestCatchUpTicksDeliverEveryAdmittedCue is the R17 regression: a cue admitted
// on a tick that is superseded by later publications before the client draws
// must still be played exactly once, in raise order [03 R-AUD-01 §7][03 §8.3].
func TestCatchUpTicksDeliverEveryAdmittedCue(t *testing.T) {
	for batch := 1; batch <= 5; batch++ {
		b := &frame.Buffer{}
		a := audio.NewService(nil)
		if _, err := a.Cache.Put("shot", []byte{128, 129}); err != nil {
			t.Fatal(err)
		}
		a.Registry.SetCache(a.Cache)
		c := &Client{buffer: b, messages: *frame.NewMessageRing()}
		c.SetAudioService(a)

		spy := &audioOutputSpy{}
		old := audio.GlobalOutput()
		audio.SetGlobalOutput(spy)

		for i := 0; i < batch; i++ {
			publishTickEvents(t, b, uint32(i+1), admittedCue(uint32(i+1), "shot"))
		}
		c.TickAudio()
		plays := spy.plays
		c.TickAudio()
		replays := spy.plays
		audio.SetGlobalOutput(old)

		if plays != batch {
			t.Fatalf("batch of %d sub-ticks played %d cues, want %d [03 R-AUD-01 §7]", batch, plays, batch)
		}
		if replays != batch {
			t.Fatalf("batch of %d sub-ticks replayed on a second draw: %d plays [03 R-AUD-01 §7]", batch, replays)
		}
	}
}

// TestStatusCaptionSurvivesLaterEmptyTick is the second R17 probe: a status
// request committed at tick 7 must still reach the message ring when tick 8
// publishes nothing before the client's single drain.
func TestStatusCaptionSurvivesLaterEmptyTick(t *testing.T) {
	b := &frame.Buffer{}
	a := audio.NewService(nil)
	a.Queue.Register(4, nil, "ARMPW", true)
	a.Queue.Configure(10, 10, true, true)
	c := &Client{buffer: b, messages: *frame.NewMessageRing()}
	c.SetAudioService(a)

	publishTickEvents(t, b, 7, frame.EventView{
		Kind: frame.EventKindStatus, Tick: 7, Source: 4,
		StatusKind: 6, StatusText: "Arrived", StatusClass: 1,
	})
	publishTickEvents(t, b, 8)

	c.TickAudio()
	lines := c.MessageLines()
	if len(lines) != 1 || lines[0].Text != "ARMPW: Arrived" {
		t.Fatalf("message lines = %#v, want the tick-7 caption [03 R-AUD-01 §7][07 R-HUD-03 §14]", lines)
	}
}

// TestAccumulatedStatusKeepsItsRaiseTick locks the half of [03 R-AUD-01 §7]
// that an accumulated batch is easiest to get wrong: the insert is timed
// against the raise's own global tick, not the drain's. Stamping the batch
// with the drain tick would advance an earlier request past its slot's
// next-allowed frame and rewrite the §8.3 cooldown arbitration.
func TestAccumulatedStatusKeepsItsRaiseTick(t *testing.T) {
	b := &frame.Buffer{}
	a := audio.NewService(nil)
	a.Queue.Register(4, nil, "ARMPW", true)
	c := &Client{buffer: b, messages: *frame.NewMessageRing()}
	c.SetAudioService(a)

	publishTickEvents(t, b, 7, frame.EventView{
		Kind: frame.EventKindStatus, Tick: 7, Source: 4,
		StatusKind: 6, StatusText: "Arrived", StatusClass: 1,
	})
	publishTickEvents(t, b, 9)

	c.committedEvents = b.DrainCommittedEvents(c.committedEvents)
	c.enqueueStatusEvents(9, c.committedEvents)
	if a.Queue.Count != 1 {
		t.Fatalf("queue count = %d, want the one accumulated request [03 R-AUD-01 §7]", a.Queue.Count)
	}
	if got := a.Queue.Entries[0].Frame; got != 7 {
		t.Fatalf("queued at frame %d, want the raise tick 7 [03 R-AUD-01 §7][03 §8.3]", got)
	}
	if c.messageEventsTick != 9 {
		t.Fatalf("caption ageing origin = %d, want the drain tick 9 [07 R-HUD-03 §14.3]", c.messageEventsTick)
	}
}

// TestPurgedStatusRequestStaysPurgedAcrossAccumulation pairs with the session's
// teardown purge: a unit removed in its raise tick has the staged request's
// slot zeroed before publication, and slot 0 is §8.3's unused sentinel that the
// insert refuses. Retaining the batch across catch-up ticks must not resurrect
// it, and must not disturb another unit's request from the same tick
// [03 R-AUD-01 §7].
func TestPurgedStatusRequestStaysPurgedAcrossAccumulation(t *testing.T) {
	b := &frame.Buffer{}
	a := audio.NewService(nil)
	a.Queue.Register(4, nil, "ARMPW", true)
	a.Queue.Register(5, nil, "ARMPW", true)
	a.Queue.Configure(10, 10, true, true)
	c := &Client{buffer: b, messages: *frame.NewMessageRing()}
	c.SetAudioService(a)

	publishTickEvents(t, b, 7,
		// The purged shape: purgeStatusCues clears kind and text in place.
		frame.EventView{Kind: frame.EventKindStatus, Tick: 7, Source: 4, StatusKind: 0, StatusClass: 1},
		frame.EventView{Kind: frame.EventKindStatus, Tick: 7, Source: 5, StatusKind: 6, StatusText: "Arrived", StatusClass: 1},
	)
	publishTickEvents(t, b, 8)

	c.TickAudio()
	if lines := c.MessageLines(); len(lines) != 1 || lines[0].Text != "ARMPW: Arrived" {
		t.Fatalf("message lines = %#v, want only the surviving unit's caption [03 R-AUD-01 §7]", lines)
	}
	if a.Queue.Count != 0 {
		t.Fatalf("queue still holds %d entries, want the purged request never admitted", a.Queue.Count)
	}
}
