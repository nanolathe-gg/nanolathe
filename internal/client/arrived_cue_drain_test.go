package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// arrivedDrainFixture publishes one committed kind-6 status event and returns
// a client bound to a fresh audio service.
func arrivedDrainFixture(t *testing.T) (*Client, *audio.Service, []frame.EventView) {
	t.Helper()
	b := &frame.Buffer{}
	f := b.BeginWrite()
	f.Events = append(f.Events, frame.EventView{
		Kind: frame.EventKindStatus, Tick: 11, Source: 4,
		StatusKind: 6, StatusText: "Arrived", StatusClass: 1,
	})
	if err := b.Publish(11); err != nil {
		t.Fatal(err)
	}
	a := audio.NewService(nil)
	a.Queue.Register(4, nil, "ARMPW", true)
	c := &Client{buffer: b, messages: *frame.NewMessageRing(), screenChat: 0}
	c.SetAudioService(a)
	return c, a, f.Events
}

// TestCommittedArrivedStatusIsVoiceSlotSix locks the last hop of the order
// acknowledgement path: a committed status event of kind 6 becomes voice-cue
// slot 6 (`arrived`), priority 3, cooldown 4 seconds, carrying the row's
// `Arrived` text [04 R-ORD-01 §1][03 §8.3]. The producers — `Move_Ground`
// phase 1, `Attack_Kamikaze` phase 1, `VTOL_Move` phase 2 — only record the
// event inside the sub-tick; this admission runs on the presentation side of
// the publication boundary [I6].
func TestCommittedArrivedStatusIsVoiceSlotSix(t *testing.T) {
	c, a, events := arrivedDrainFixture(t)
	c.enqueueStatusEvents(11, events)
	if a.Queue.Count != 1 {
		t.Fatalf("queue count = %d, want the one committed acknowledgement admitted", a.Queue.Count)
	}
	e := a.Queue.Entries[0]
	if e.Slot != audio.SlotArrived || e.Unit != 4 || e.Text != "Arrived" {
		t.Fatalf("queued entry = %+v, want slot 6 on unit 4 with text %q [03 §8.3]", e, "Arrived")
	}
	if _, speech, priority, cooldown, ok := audio.SlotStatic(audio.SlotArrived); !ok || speech != "Arrived" || priority != 3 || cooldown != 4 {
		t.Fatalf("slot 6 static row = (%q, %d, %d, %v), want (%q, 3, 4, true) [03 §8.3]", speech, priority, cooldown, ok, "Arrived")
	}
	if e.Priority != 3 {
		t.Fatalf("queued priority = %d, want the slot's static 3 [03 §8.3]", e.Priority)
	}
}

// TestArrivedCaptionNeedsUnitchatFull locks the caption half against the
// UNITCHAT gate `10 − unitchattext < priority` [07 R-HUD-03 §14.1], whose
// level bytes are [07 R-CAM-01 §7]'s `0` / `5` / `10`. Slot
// 6 carries priority 3, so at the shipped default (`Medium`, `unitchattext` 5)
// the `Arrived` line does **not** reach the message ring; only `Full` admits
// it. This is the same arithmetic that keeps `Building complete` off screen at
// the default, and it is visible to a player — a build that showed every
// caption alike would be wrong in the one place it can be seen.
//
// The voice half is gated separately, by `unitchat` (default 10), so the
// acknowledgement is audible at the default even while its caption is not.
func TestArrivedCaptionNeedsUnitchatFull(t *testing.T) {
	for _, tc := range []struct {
		name            string
		speechThreshold uint8
		wantLines       int
	}{
		{"Medium, the shipped default", 5, 0},
		{"Full", 10, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, a, _ := arrivedDrainFixture(t)
			a.Queue.Configure(10, tc.speechThreshold, true, true)
			c.TickAudio()
			if got := len(c.MessageLines()); got != tc.wantLines {
				t.Fatalf("message lines = %d (%#v), want %d [07 R-HUD-03 §14.1]", got, c.MessageLines(), tc.wantLines)
			}
			if tc.wantLines == 1 && c.MessageLines()[0].Text != "ARMPW: Arrived" {
				t.Fatalf("caption = %q, want %q [03 §8.3]", c.MessageLines()[0].Text, "ARMPW: Arrived")
			}
		})
	}
}
