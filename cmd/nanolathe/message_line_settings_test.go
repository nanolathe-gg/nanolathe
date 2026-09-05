package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/settings"
)

// captionFixture publishes one committed kind-6 status event (an "Arrived"
// acknowledgement) and returns a client bound to a fresh audio service ready
// to drain it, mirroring the fixture internal/client's own caption tests use.
func captionFixture(t *testing.T) (*client.Client, *audio.Service) {
	t.Helper()
	b := &frame.Buffer{}
	f := b.BeginWrite()
	f.Events = append(f.Events, frame.EventView{
		Kind: frame.EventKindStatus, Tick: 5, Source: 4,
		StatusKind: 6, StatusText: "Arrived", StatusClass: 1,
	})
	if err := b.Publish(5); err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: b, Width: 640, Height: 480})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	a := audio.NewService(nil)
	a.Queue.Register(4, nil, "ARMPW", true)
	// Full: every caption is admitted regardless of priority, so only the
	// ring configuration below decides whether the line survives.
	a.Queue.Configure(10, 10, true, true)
	cl.SetAudioService(a)
	return cl, a
}

// TestApplyMessageLineSettingsConfiguresTheRing locks the composition seam
// that retires the accepted-placeholder marker in internal/client/client.go: the
// persisted `textlines` has to reach the battle client's message ring through
// ConfigureMessageLines rather than the client's own built-in default of 10
// standing for the whole session [02 §3][07 R-HUD-03 §14.3].
//
// `textlines: 0` disables ring storage outright [07 R-HUD-03 §14.3] — a
// sharp, unambiguous signal that the configured value reached the ring,
// because the client's own default (10) would otherwise let the line
// through. applyMessageLineSettings calls ConfigureMessageLines and
// SetScreenChat as one unconditional pair, so this also exercises the
// SetScreenChat call on the same path.
func TestApplyMessageLineSettingsConfiguresTheRing(t *testing.T) {
	t.Run("textlines 0 disables storage", func(t *testing.T) {
		cl, _ := captionFixture(t)
		s := settings.Defaults()
		s.Messages.TextLines = 0
		applyMessageLineSettings(cl, s)
		cl.TickAudio()
		if got := len(cl.MessageLines()); got != 0 {
			t.Fatalf("message lines = %d, want 0 (textlines=0 must disable ring storage)", got)
		}
	})

	t.Run("default textlines admits the line", func(t *testing.T) {
		cl, _ := captionFixture(t)
		applyMessageLineSettings(cl, settings.Defaults())
		cl.TickAudio()
		lines := cl.MessageLines()
		if len(lines) != 1 || lines[0].Text != "ARMPW: Arrived" {
			t.Fatalf("message lines = %+v, want one line %q", lines, "ARMPW: Arrived")
		}
	})

	t.Run("nil client is a no-op", func(t *testing.T) {
		applyMessageLineSettings(nil, settings.Defaults())
	})
}
