package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func TestBattleSelectionDragBridgeMirrorsGestureAndClears(t *testing.T) {
	b := &battleSession{battleUI: ui.NewProductionBattleState()}
	cl, err := client.New(client.Options{Width: 200, Height: 100})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	b.battleState().Input = ui.BattleInputState{
		Latch: input.LatchNormal, DragActive: true,
		DragStartX: 140, DragStartY: 40, DragEndX: 144, DragEndY: 44,
	}
	b.syncSelectionDrag(cl)
	// The bridge is intentionally exercised through composition: active drag
	// state must reach the same visible-panel frame writer used by production.
	// The fallback palette is identity, so logical entry 15 is RGBA 15. An
	// ordinary selection drag is white, entry 15, not the armed 6/4 pair
	// [07 R-P0-11 §1 "The drawing."][07 §6 "Frame composition passes"].
	img := cl.ComposeFrame()
	if got := img.RGBAAt(140, 40).R; got != 15 {
		t.Fatalf("active drag pixel = %d, want outer logical entry 15", got)
	}

	b.battleState().Input.DragActive = false
	b.syncSelectionDrag(cl)
	img = cl.ComposeFrame()
	if got := img.RGBAAt(140, 40).R; got != 0 {
		t.Fatalf("released drag left stale overlay pixel %d, want cleared frame", got)
	}
}

// A drag draws in every rail state, including mid-slide. This test previously
// required the opposite ("unresolved mode must suppress"), locking a placeholder
// that no reading of [R-SEL-02A] offers: the composer's clip rectangle has one
// writer and it never consults the rail [03 §4.1] (WU-19-160).
func TestBattleSelectionDragBridgeDrawsInEveryPanelState(t *testing.T) {
	b := &battleSession{battleUI: ui.NewProductionBattleState()}
	cl, err := client.New(client.Options{Width: 200, Height: 100})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	b.battleState().Input.DragActive = true
	b.battleState().Input.DragStartX, b.battleState().Input.DragStartY = 140, 40
	b.battleState().Input.DragEndX, b.battleState().Input.DragEndY = 144, 44
	for _, offset := range []int8{ui.PanelParked, -1, 1, ui.PanelVisible} {
		b.battleState().PanelOffset = offset
		b.syncSelectionDrag(cl)
		if got := cl.ComposeFrame().RGBAAt(140, 40).R; got != 15 {
			t.Fatalf("panel offset %d emitted drag pixel %d, want outer logical entry 15", offset, got)
		}
	}
}
