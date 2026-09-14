package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// Established: frontend UI does not own the battle message column; a frozen
// battle frame still does [07 R-HUD-03 §14.4].
func TestMessageColumnRequiresCommittedBattleFrame(t *testing.T) {
	c := &Client{width: 160, height: 80, indexed: make([]byte, 160*80),
		messageFNT: &formats.FNT{Height: 3}, messages: *frame.NewMessageRing()}
	c.messages.Append("old battle", 1, 7, 10, 48000)
	c.SetSnapshot(&frame.Buffer{}) // the frontend's publication binding
	c.drawInterface(c.presentationFrame())
	var menu messageGlyphCollector
	c.list.Replay(&menu)
	if len(menu.runs) != 0 || len(c.messages.Visible()) != 1 {
		t.Fatal("frontend painted or erased the retained battle caption")
	}
	battle := frame.NewBuffer()
	battle.BeginWrite().Paused = true
	if err := battle.Publish(1); err != nil {
		t.Fatal(err)
	}
	c.SetSnapshot(battle)
	c.SetPresentationPaused(true)
	c.drawInterface(c.presentationFrame())
	var paused messageGlyphCollector
	c.list.Replay(&paused)
	if len(paused.runs) != 1 || paused.runs[0].Text != "old battle" {
		t.Fatalf("paused battle lost message column: %+v", paused.runs)
	}
	terminal := battle.BeginWrite()
	terminal.Paused = true
	terminal.Result.Ended = true
	if err := battle.Publish(2); err != nil {
		t.Fatal(err)
	}
	c.drawInterface(c.presentationFrame())
	var result messageGlyphCollector
	c.list.Replay(&result)
	if len(result.runs) != len(paused.runs) || len(c.messages.Visible()) != 1 {
		t.Fatal("terminal result painted or erased the retained battle caption")
	}
}
