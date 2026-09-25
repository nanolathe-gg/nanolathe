package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// In the right-click interface a right release over a unit showing the select
// cursor sends Guard (code 7), and the prepared order then stays Guard until
// the next right release cancels it [draw-engine-interface "What a megamap
// send becomes"].
func TestMegamapGuardSendKeepsGuardPrepared(t *testing.T) {
	b, s, builder := placeClickFixture(t, 64, 48)
	cl, err := client.New(client.Options{Buffer: s.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	b.cl = cl
	p := settings.DefaultPresentation()
	p.Overview = settings.OverviewMegamap
	b.hostPresentation = &p
	b.interfaceType = settings.InterfaceTypeRightClick
	b.setSurfaceSize(640, 480)
	b.commitSelection([]pool.Handle{builder}, true)
	s.Step(s.Clock.ScaledAnchor + 1)
	f, ok := b.currentSnapshot()
	if !ok || len(f.Selection.Handles) != 1 {
		t.Fatalf("fixture selection not published: %+v", f.Selection)
	}
	b.setMegamapShown(true, cl)
	lens := b.megamapLens()
	u := f.Units[0]
	// The hit area is offset from the icon by one footprint: aim at the
	// reference point, position plus half the footprint.
	x, y := lens.Project(radarMapPixel(u.X)+int32(u.FootX)*8, 0, radarMapPixel(u.Z)+int32(u.FootZ)*8)
	mx, my := lens.X+x, lens.Y+y
	if h := b.megamapHoverUnit(f, mx, my); h != builder {
		t.Fatalf("hover = %d, want the builder", h)
	}
	if shape := b.cursorShapeAt(input.LatchNormal, mx, my); shape != render.CursorSelect {
		t.Fatalf("cursor over the own unit = %d, want select", shape)
	}
	b.megamapRightRelease(cl, mx, my, false)
	sent := false
	for _, c := range s.PendingHumanCommands() {
		sent = sent || c.Kind == session.HumanOrder && c.Order.Code == 7 && c.Order.Target == builder
	}
	if !sent || b.battleState().Input.Latch != input.LatchFollow || b.battleState().Input.ShiftLatchSticky {
		t.Fatalf("guard sent=%v latch=%d sticky=%v, want code 7 and Guard still prepared", sent, b.battleState().Input.Latch, b.battleState().Input.ShiftLatchSticky)
	}
	b.megamapRightRelease(cl, mx, my, false)
	if b.battleState().Input.Latch != input.LatchNormal {
		t.Fatalf("the next right release left latch %d, want neutral", b.battleState().Input.Latch)
	}
}
