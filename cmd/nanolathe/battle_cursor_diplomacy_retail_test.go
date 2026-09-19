//go:build retail

package main

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestRetailCursorDiplomacyUsesInstalledCursors runs the real pointer pass
// with the retail cursor bank. Set NANOLATHE_CURSOR_DIPLOMACY_SHOT to a path
// prefix to retain allied and enemy composed-frame PNGs for visual inspection.
func TestRetailCursorDiplomacyUsesInstalledCursors(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	dst := withMinimap(b)
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		t.Fatal("fixture battle has no play area")
	}
	layout, _, ok := b.minimapLayout()
	if !ok {
		t.Fatal("fixture battle has no minimap layout")
	}
	const mx, my = int32(70), int32(50)
	wx, wz, ok := client.MinimapPointerWorld(layout, dst, playW, playH, mx, my)
	if !ok {
		t.Fatal("fixture minimap pointer did not resolve")
	}
	target := placeUnit(b, "armcons", numeric.Fixed(wx<<16), numeric.Fixed(wz<<16))
	actor := placeUnit(b, "armcons", numeric.Fixed(8<<16), numeric.Fixed(8<<16))
	actor.Def.CanAttack = true
	replaceSelectionForTest(t, b, actor)
	b.applyInterfaceTypeSetting(settings.Settings{InterfaceType: settings.InterfaceTypeRightClick})
	cl, closeContent := minimapCursorClient(t, b)
	defer closeContent()
	// Compose through the same palette/terrain/client path as a battle frame so
	// the opt-in captures expose the installed cursor colours, not raw indices.
	shotContent, err := openContent(Options{Root: probeRetail(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer shotContent.Close()
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(b.cam)
	cl.SetPalette(retailPaletteForTest(t, shotContent))
	cl.SetModelFS(shotContent.unmappedMount)

	// The target is deliberately a different owner only in the committed
	// presentation copy. snapshotUnitCopy therefore has no usable queue
	// binding, which is the boundary this test protects.
	cur, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("fixture did not publish a frame")
	}
	write := func(allied bool, reverse bool) {
		written := b.sess.Snapshot.BeginWrite()
		*written = *cur
		written.Units = append([]frame.UnitView(nil), cur.Units...)
		for i := range written.Units {
			if written.Units[i].Slot == target.Handle {
				written.Units[i].Owner = 1
			}
		}
		written.Selection.Handles = []pool.Handle{actor.Handle}
		written.Selection.Primary = actor.Handle
		written.Selection.Count = 1
		written.Selection.LocalPlayer = 0
		// A foreign minimap dot still needs the committed visibility input;
		// keep every fixture tile visible so the cursor test isolates diplomacy.
		written.Visibility = frame.VisibilityView{W: 32, H: 32, Valid: true, CoverageBytes: true, Visible: make([]uint8, 32*32)}
		for i := range written.Visibility.Visible {
			written.Visibility.Visible[i] = 1
		}
		written.Players[0].Allies[1] = allied
		written.Players[1].Allies[0] = reverse
		if err := b.sess.Snapshot.Publish(cur.Tick + 1); err != nil {
			t.Fatal(err)
		}
		cur, _ = b.currentSnapshot()
	}
	setPointer := func() {
		cl.Input().Mouse.SetPosition(float32(mx), float32(my))
		b.updateCursor(cl)
	}
	shot := func(suffix string) {
		prefix := os.Getenv("NANOLATHE_CURSOR_DIPLOMACY_SHOT")
		if prefix == "" {
			return
		}
		file, err := os.Create(prefix + suffix + ".png")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := encodeShot(file, cl); err != nil {
			t.Fatal(err)
		}
	}

	write(true, false)
	setPointer()
	if got := cl.Cursors().Index(); got != render.CursorGrn {
		t.Fatalf("allied different-owner cursor = %d, want cursorgrn %d", got, render.CursorGrn)
	}
	shot("-ally")

	write(false, true)
	_, hoverTarget, _ := b.pickTarget(mx, my)
	if hoverTarget == nil || hoverTarget.Owner != 1 {
		t.Fatalf("enemy fixture hover target = %+v, want committed owner 1", hoverTarget)
	}
	actorView, found := snapshotUnitByHandle(cur, actor.Handle)
	if !found {
		t.Fatal("enemy fixture lost selected actor")
	}
	if !b.hostile(b.snapshotUnitCopy(actorView), hoverTarget) {
		t.Fatalf("enemy fixture lost actor-direction hostility: rows=%+v", cur.Players[:2])
	}
	if !b.interfaceTypeRightClick() || len(cur.Selection.Handles) != 1 || cur.Selection.Handles[0] != actor.Handle {
		t.Fatalf("enemy fixture cursor inputs polarity=%v selection=%+v, want type 1 and actor %d", b.interfaceTypeRightClick(), cur.Selection, actor.Handle)
	}
	setPointer()
	if got := cl.Cursors().Index(); got != render.CursorRed {
		t.Fatalf("enemy cursor = %d, want cursorred %d", got, render.CursorRed)
	}
	shot("-enemy")

	// Only the actor's row controls hostility. The target's declaration back
	// toward the actor remains true, so this is an asymmetric-row assertion.
	if cur.Players[1].Allies[0] != true {
		t.Fatal("fixture lost the target's reverse declaration")
	}
}
