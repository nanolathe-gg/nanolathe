//go:build retail

package main

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A legal snapped Coast to Coast metal spot must actually draw green with the
// installed palettes. The placement predicate already accepted this site while
// the preview incorrectly sent the patch's clear-site selector through GUIPAL.
func TestCoastToCoastMexPreviewIsGreen(t *testing.T) {
	opts := Options{Root: testsupport.RetailRoot(t), Map: "coast to coast", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	b, err := composeBattleEntry(sess, cat, cs, cl, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.teardown(cl)
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Owner == sess.LocalOwner && u.Def != nil && u.Def.Commander {
			replaceSelectionForTest(t, b, u)
			break
		}
	}
	def, ok := cat.Unit("armmex")
	if !ok {
		t.Fatal("missing stock extractor")
	}
	b.armPlacement(def)
	state := &b.battleState().Input
	found := false
	for my := int32(32); my < 447 && !found; my += 4 {
		for mx := int32(129); mx < 639; mx += 4 {
			b.updatePlacement(mx, my)
			wx, _, wz := b.cursorWorld(mx, my)
			rawX, rawZ := world.PlacementAnchor(wx, wz, state.BuildFootX, state.BuildFootZ)
			if !state.BuildOK || state.BuildNeedsClear || rawX == state.BuildCellX && rawZ == state.BuildCellZ {
				continue
			}
			if sess.PlacementSnapSample(state.BuildCellX+state.BuildFootX/2, state.BuildCellZ+state.BuildFootZ/2, state.BuildFootX, state.BuildFootZ).MetalAboveSurface == 0 {
				continue
			}
			state.PointerX, state.PointerY = mx, my
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no clear, visible snapped metal site in the starting view")
	}
	shot := cl.ComposeFrameSnapshot()
	if path := os.Getenv("NANOLATHE_MEX_PREVIEW_SHOT"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := encodeShot(file, cl); err != nil {
			t.Fatal(err)
		}
	}
	left, top, _, _ := b.placementRect()
	i := int(top)*shot.Width + int(left)
	if i < 0 || i >= len(shot.Indexed) {
		t.Fatal("snapped footprint outside capture")
	}
	rgba := shot.RGBA[i*4 : i*4+4]
	if rgba[1] <= rgba[0] || rgba[1] <= rgba[2] {
		t.Fatalf("accepted mex at %d,%d draws palette %d RGBA %v, want green", state.BuildCellX, state.BuildCellZ, shot.Indexed[i], rgba)
	}
}
