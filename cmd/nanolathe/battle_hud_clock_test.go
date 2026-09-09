package main

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func clockTestFont(height byte) *formats.FNT {
	font := &formats.FNT{Height: height}
	font.Glyphs['G'] = &formats.FNTGlyph{Width: 1, Height: height, Bits: []byte{0x80}}
	return font
}

type standaloneClockStage struct {
	hud    *retailBattleHUD
	battle *battleSession
	frame  *frame.Frame
}

func (s standaloneClockStage) DrawUI(c *client.Client, _ client.UIFrame) {
	s.hud.drawClock(c, s.battle, s.frame)
}

func TestStandaloneClockUnsignedTimeAndPlacement(t *testing.T) {
	if got, want := standaloneClockText("Spielzeit", 30*(3600+62)), "Spielzeit : 01:01:02"; got != want {
		t.Fatalf("localized clock = %q, want %q", got, want)
	}
	if got, want := standaloneClockText("Game Time", ^uint32(0)), "Game Time : 39768:12:56"; got != want {
		t.Fatalf("wrapped unsigned clock = %q, want %q", got, want)
	}

	primary, side := clockTestFont(3), clockTestFont(7)
	h := &retailBattleHUD{primaryFont: primary, console: side}
	b := &battleSession{clockVisible: true, clockUsePrimaryFont: true}
	c, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.SetFNT(side)
	c.SetUIStage(standaloneClockStage{hud: h, battle: b, frame: &frame.Frame{Tick: 30}})
	snap := c.ComposeFrameSnapshot()
	y := 480 - standaloneClockBottomDY - int(primary.Height)
	if got := snap.Indexed[y*snap.Width+standaloneClockX]; got != 15 {
		t.Fatalf("primary clock origin pixel = %d, want 15 at (%d,%d)", got, standaloneClockX, y)
	}

	// A zero textlines setting skips the earlier COMIX selector, so the same
	// late draw inherits the taller side console and moves up by its metric.
	b.shell = &gameShell{clockVisible: true, messages: settings.Messages{TextLines: 0}}
	snap = c.ComposeFrameSnapshot()
	y = 480 - standaloneClockBottomDY - int(side.Height)
	if got := snap.Indexed[y*snap.Width+standaloneClockX]; got != 15 {
		t.Fatalf("side-font clock origin pixel = %d, want 15 at (%d,%d)", got, standaloneClockX, y)
	}
}

func TestClockCommandTogglesAndWritesSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, path)
	if err := settings.Defaults().Save(); err != nil {
		t.Fatal(err)
	}
	b := &battleSession{}
	b.dispatchLocalCommand("+cLoCk ignored")
	stored, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !b.clockShown() || !stored.ClockEnabled() {
		t.Fatalf("first toggle runtime=%t stored=%d, want on", b.clockShown(), stored.Clock)
	}
	b.dispatchLocalCommand("+Clock")
	stored, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if b.clockShown() || stored.ClockEnabled() {
		t.Fatalf("second toggle runtime=%t stored=%d, want off", b.clockShown(), stored.Clock)
	}
}
