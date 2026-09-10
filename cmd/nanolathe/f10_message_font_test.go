package main

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Established: battle adoption selects COMIX and its glyph height controls
// the message column's line spacing [07 R-HUD-03 §14.4].
func TestBattleMessageColumnUsesPrimaryFont(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	primary := &formats.FNT{Height: 3}
	primary.Glyphs['A'] = &formats.FNTGlyph{Width: 1, Height: 1, Bits: []byte{0x80}}
	console := &formats.FNT{Height: 7}
	console.Glyphs['A'] = &formats.FNTGlyph{Width: 2, Height: 1, Bits: []byte{0xc0}}
	b := &battleSession{
		sess: &session.Session{Snapshot: frame.NewBuffer()},
		hud:  &retailBattleHUD{primaryFont: primary, console: console},
	}
	c, err := client.New(client.Options{Width: 160, Height: 80})
	if err != nil {
		t.Fatal(err)
	}
	installBattleClient(c, b)
	// This fixture isolates the message column after the production adoption
	// path has installed its font, without unrelated HUD art.
	c.SetUIStage(nil)
	c.MessageRing().Append("A", 1, 0, 10, 0)
	c.MessageRing().Append("A", 1, 0, 10, 0)
	snap := c.ComposeFrameSnapshot()
	for _, y := range []int{52, 52 + int(primary.Height)} {
		if got := snap.Indexed[y*snap.Width+138]; got != 15 {
			t.Errorf("message pixel at (138,%d) = %d, want primary glyph", y, got)
		}
		if got := snap.Indexed[y*snap.Width+139]; got != 0 {
			t.Errorf("message pixel at (139,%d) = %d, want narrow primary glyph", y, got)
		}
	}
	if got := snap.Indexed[(52+int(console.Height))*snap.Width+138]; got != 0 {
		t.Errorf("console-spaced second line pixel = %d, want no glyph", got)
	}
}
