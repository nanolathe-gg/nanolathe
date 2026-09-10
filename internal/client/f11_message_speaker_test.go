package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

type messageSpeakerCollector struct {
	messageGlyphCollector
	sprites []drawlist.Sprite
}

func (s *messageSpeakerCollector) Sprite(v drawlist.Sprite) { s.sprites = append(s.sprites, v) }

// Established: COMIX height controls two independently truncated expressions;
// the logo rectangle includes its far endpoint [07 R-HUD-03 §14.4].
func TestMessageSpeakerGeometryAndCommittedLogo(t *testing.T) {
	for _, tc := range []struct {
		height        uint8
		extent, textX int32
	}{{7, 5, 145}, {9, 7, 148}, {14, 11, 154}} {
		b := &frame.Buffer{}
		f := b.BeginWrite()
		f.Players[2] = frame.PlayerRow{Present: true, Logo: 1}
		if err := b.Publish(1); err != nil {
			t.Fatal(err)
		}
		frames := []formats.GAFFrameRef{
			{Frame: &formats.GAFFrame{Width: 2, Height: 2, Pixels: []byte{31, 31, 31, 31}}},
			{Frame: &formats.GAFFrame{Width: 2, Height: 2, Pixels: []byte{71, 71, 71, 71}}},
		}
		bank := &formats.GAF{Entries: []formats.GAFEntry{{Name: "32xlogos", Frames: frames}}}
		c := &Client{width: 320, height: 200, buffer: b, messageFNT: &formats.FNT{Height: tc.height}, messages: *frame.NewMessageRing()}
		c.SetMessageLogos(bank)
		// An uncommitted replacement must not select a different player's art.
		b.BeginWrite().Players[2] = frame.PlayerRow{Present: true, Logo: 0}
		c.messages.Append("filtered score", 2, 0, 10, 1)
		c.messages.Append("elimination", 4, 0, 2, 1)
		c.messages.Append("caption", 1, 0, 10, 1)
		c.drawMessageLines()
		var got messageSpeakerCollector
		c.list.Replay(&got)
		if len(got.sprites) != 1 || len(got.runs) != 2 {
			t.Fatalf("height %d: sprites=%d text=%d", tc.height, len(got.sprites), len(got.runs))
		}
		sp := got.sprites[0]
		if sp.Frame != frames[1].Frame || sp.Kind != drawlist.BlitScaled || sp.Dst != (drawlist.Rect{X: 138, Y: 52, W: tc.extent + 1, H: tc.extent + 1}) {
			t.Fatalf("height %d: logo=%+v", tc.height, sp)
		}
		if got.runs[0].X != tc.textX || got.runs[0].Y != 52 || got.runs[1].X != 138 || got.runs[1].Y != 52+int32(tc.height) {
			t.Fatalf("height %d: glyphs=%+v", tc.height, got.runs)
		}
		c.SetMessageLogos(nil)
		if c.messageLogo(2) != nil {
			t.Fatal("cleared art binding retained a logo")
		}
	}
}

// Established: only accepted real-speaker announcements request the
// arrival cue; neither class filtering nor repeated drawing replays it
// [07 R-HUD-03 §14.3–§14.4].
func TestMessageArrivalUsesAcceptedRetainedEvents(t *testing.T) {
	for _, tc := range []struct {
		name, text     string
		class, speaker uint8
		lines          uint16
		want           int
	}{
		{"elimination", "A", 4, 2, 10, 1},
		{"masked class", "A", 20, 2, 10, 1},
		{"sentinel", "A", 4, 10, 10, 0},
		{"score", "A", 2, 10, 10, 0},
		{"speaker independent of class", "A", 2, 2, 10, 1},
		{"empty", "", 4, 2, 10, 0},
		{"disabled", "A", 4, 2, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &frame.Buffer{}
			f := b.BeginWrite()
			f.Events = []frame.EventView{{Kind: frame.EventKindAnnounce, Tick: 1, StatusText: tc.text, StatusClass: tc.class, AnnounceSlot: tc.speaker}}
			if err := b.Publish(1); err != nil {
				t.Fatal(err)
			}
			b.BeginWrite()
			if err := b.Publish(2); err != nil {
				t.Fatal(err)
			}
			c := &Client{buffer: b, messageFNT: &formats.FNT{Height: 9}, messages: *frame.NewMessageRing()}
			c.ConfigureMessageLines(tc.lines, 10)
			a := audio.NewService(nil)
			if _, err := a.Cache.Put("MessageArrived", []byte{128, 128}); err != nil {
				t.Fatal(err)
			}
			c.SetAudioService(a)
			spy := &audioOutputSpy{}
			old := audio.GlobalOutput()
			audio.SetGlobalOutput(spy)
			defer audio.SetGlobalOutput(old)
			for i := 0; i < 3; i++ {
				c.TickAudio()
				c.drawMessageLines()
			}
			if spy.plays != tc.want {
				t.Fatalf("arrival cues=%d, want %d", spy.plays, tc.want)
			}
			wantLines := 1
			if tc.text == "" || tc.lines == 0 {
				wantLines = 0
			}
			if got := len(c.messages.Visible()); got != wantLines {
				t.Fatalf("stored lines=%d, want %d", got, wantLines)
			}
		})
	}
}

func TestAnnouncementWithoutAudioStillReachesMessageRing(t *testing.T) {
	b := &frame.Buffer{}
	b.BeginWrite().Events = []frame.EventView{{Kind: frame.EventKindAnnounce, StatusText: "elimination", StatusClass: 4, AnnounceSlot: 2}}
	if err := b.Publish(1); err != nil {
		t.Fatal(err)
	}
	c := &Client{buffer: b, messages: *frame.NewMessageRing()}
	c.TickAudio()
	c.TickAudio()
	if got := c.MessageLines(); len(got) != 1 || got[0].SpeakerSlot != 2 {
		t.Fatalf("announcement=%+v", got)
	}
}

// The speaker field controls logo composition independently of the class;
// screenchat only controls which rows occupy line positions [07 R-HUD-03 §14.4].
func TestMessageSpeakerClassFilterAndMissingArt(t *testing.T) {
	b := &frame.Buffer{}
	f := b.BeginWrite()
	f.Players[1] = frame.PlayerRow{Present: true, Logo: 0}
	if err := b.Publish(1); err != nil {
		t.Fatal(err)
	}
	art := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{71}}
	c := &Client{width: 320, height: 200, buffer: b, messageFNT: &formats.FNT{Height: 9}, messages: *frame.NewMessageRing()}
	c.SetMessageLogos(&formats.GAF{Entries: []formats.GAFEntry{{Name: "32xlogos", Frames: []formats.GAFFrameRef{{Frame: art}}}}})
	c.messages.Append("score", 2, 0, 1, 1)
	c.messages.Append("chat", 4, 0, 10, 1)
	c.SetScreenChat(1)
	c.drawMessageLines()
	var all messageSpeakerCollector
	c.list.Replay(&all)
	if len(all.sprites) != 1 || len(all.runs) != 2 || all.runs[0].X != 148 || all.runs[1].X != 138 {
		t.Fatalf("all classes: sprites=%d glyphs=%+v", len(all.sprites), all.runs)
	}
	c.list.Reset()
	c.SetScreenChat(0)
	c.drawMessageLines()
	var filtered messageSpeakerCollector
	c.list.Replay(&filtered)
	if len(filtered.sprites) != 0 || len(filtered.runs) != 1 || filtered.runs[0].Y != 52 {
		t.Fatalf("filtered classes: sprites=%d glyphs=%+v", len(filtered.sprites), filtered.runs)
	}
	c.list.Reset()
	c.SetScreenChat(1)
	c.SetMessageLogos(nil)
	c.drawMessageLines()
	var missing messageSpeakerCollector
	c.list.Replay(&missing)
	if len(missing.sprites) != 0 || len(missing.runs) != 2 || missing.runs[0].X != 148 {
		t.Fatalf("missing art: sprites=%d glyphs=%+v", len(missing.sprites), missing.runs)
	}
	if c.messageLogo(10) != nil || c.messageLogo(255) != nil || c.messageLogo(9) != nil {
		t.Fatal("missing or sentinel speaker acquired logo")
	}
}
