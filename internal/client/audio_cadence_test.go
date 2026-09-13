package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Nanolathe host policy: repeated draws cannot silently consume the second
// acknowledgement just before the global-tick audible window opens.
func TestAudioHostOpportunityRetainsNextAcknowledgement(t *testing.T) {
	b := &frame.Buffer{}
	cl, err := New(Options{Buffer: b, Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	s := audio.NewService(nil)
	cl.SetAudioService(s)
	cat := &audio.Category{}
	cat.Rows[audio.SlotSelect] = audio.Row{Variants: []string{"select"}}
	cat.Rows[audio.SlotOK] = audio.Row{Variants: []string{"ok"}}
	s.Queue.Register(1, cat, "unit", true)
	var played []audio.Slot
	s.Queue.OnPlay(func(_ string, slot audio.Slot, _ pool.Handle) { played = append(played, slot) })
	b.BeginWrite()
	if err := b.Publish(29); err != nil {
		t.Fatal(err)
	}
	s.Emit(29, audio.SlotSelect, 1, "")
	s.Emit(29, audio.SlotOK, 1, "")
	cl.TickAudio()
	cl.TickAudio()
	if s.Queue.Count != 1 || len(played) != 0 {
		t.Fatalf("repeat draw: count=%d, played=%v", s.Queue.Count, played)
	}
	cl.Step(1.0 / 30)
	b.BeginWrite()
	if err := b.Publish(30); err != nil {
		t.Fatal(err)
	}
	cl.TickAudio()
	if len(played) != 1 || played[0] != audio.SlotOK {
		t.Fatalf("window winner=%v", played)
	}
}

func TestAudioHostOpportunityPauseSpeedAndEventDelivery(t *testing.T) {
	b := &frame.Buffer{}
	cl, err := New(Options{Buffer: b, Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	s := audio.NewService(nil)
	cl.SetAudioService(s)
	_, err = s.Cache.Put("event", []byte{128, 128})
	if err != nil {
		t.Fatal(err)
	}
	spy := &audioOutputSpy{}
	old := audio.GlobalOutput()
	audio.SetGlobalOutput(spy)
	t.Cleanup(func() { audio.SetGlobalOutput(old) })
	publish := func(tick uint32) {
		f := b.BeginWrite()
		f.Events = append(f.Events, frame.EventView{Kind: frame.EventKindAudio, Sound: "event", AudioAudible: true, Tick: tick})
		if err := b.Publish(tick); err != nil {
			t.Fatal(err)
		}
	}
	publish(100)
	s.Emit(100, audio.SlotSelect, 1, "")
	s.Emit(100, audio.SlotOK, 1, "")
	s.Emit(100, audio.SlotCant, 1, "")
	cl.TickAudio()
	// Multiple simulation ticks before the next presentation retain every cue,
	// but grant no extra acknowledgement opportunity in this host step.
	publish(101)
	publish(105)
	cl.TickAudio()
	cl.TickAudio()
	if spy.plays != 3 || s.Queue.Count != 2 {
		t.Fatalf("speed batch: plays=%d, queue=%d", spy.plays, s.Queue.Count)
	}
	// Host steps continue while the committed global tick is paused. One pop
	// occurs per step, without advancing the tick or replaying retained cues.
	for range 2 {
		cl.Step(1.0 / 30)
		cl.TickAudio()
		cl.TickAudio()
	}
	if s.Queue.Count != 0 || spy.plays != 3 || b.Current().Tick != 105 {
		t.Fatalf("paused: queue=%d, plays=%d, tick=%d", s.Queue.Count, spy.plays, b.Current().Tick)
	}
}
