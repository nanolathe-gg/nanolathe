//go:build retail

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// TestRetailMegamapOverlayCapture is a review capture, not a contract: with
// NANOLATHE_MEGAMAP_CAPTURE_DIR set it composes the megamap with fog cleared,
// the commander carrying Shift-queued moves and a build issued from the
// megamap itself, Shift held, a placement ghost armed and a box drag in
// progress, and writes one
// classic-executor PNG per content set (stock, and ProTA when
// NANOLATHE_MOD_ROOTS_PROTA is set). The megamap is one indexed surface both
// executors replay unchanged (DESIGN_INTERFACE_HUD_INPUT §3.15).
func TestRetailMegamapOverlayCapture(t *testing.T) {
	dir := os.Getenv("NANOLATHE_MEGAMAP_CAPTURE_DIR")
	if strings.TrimSpace(dir) == "" {
		t.Skip("NANOLATHE_MEGAMAP_CAPTURE_DIR is unset")
	}
	retail := testsupport.RetailRoot(t)
	sets := map[string][]string{"stock": {retail}}
	if value := os.Getenv("NANOLATHE_MOD_ROOTS_PROTA"); strings.TrimSpace(value) != "" {
		sets["prota"] = append([]string{retail}, filepath.SplitList(value)...)
	}
	for _, name := range []string{"stock", "prota"} {
		roots, ok := sets[name]
		if !ok {
			continue
		}
		for _, mapName := range []string{"ashap plateau", "great divide"} {
			megamapOverlayCapture(t, roots, mapName, filepath.Join(dir, "overlay-"+name+"-"+strings.ReplaceAll(mapName, " ", "-")+".png"))
		}
	}
}

func megamapOverlayCapture(t *testing.T, roots []string, mapName, out string) {
	t.Helper()
	opts := Options{Roots: roots, Map: mapName, Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	request, _, err := headlessFreshBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		t.Fatal(err)
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		t.Fatal(err)
	}
	sess := authoritative.Session
	var b *battleSession
	var cl *client.Client
	cl, err = client.New(client.Options{Buffer: sess.Snapshot, Width: 1280, Height: 960, Step: func(delta float64) { b.viewerStep(delta, cl) }})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetModelFS(cs.unmappedMount)
	b, err = composeBattleEntryWithDetail(sess, sess.Catalog, cs, cl, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.teardown(cl)
	b.setSurfaceSize(1280, 960)
	millis := &shotMillisSource{}
	b.millisSource = millis
	step := func(n int) {
		for range n {
			millis.step++
			b.viewerStep(1.0/30, cl)
		}
	}
	step(30)
	// Clear mapping and current sight so the terrain picture is reviewable.
	_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanVisibility, Visibility: session.HumanVisibilityCommand{ClearMask: visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled}})
	commander := pool.Handle(0)
	if f, ok := b.currentSnapshot(); ok {
		for _, u := range f.Units {
			if u.Owner == f.ViewingPlayer {
				commander = u.Slot
				break
			}
		}
	}
	if commander == 0 {
		t.Fatal("no own unit")
	}
	b.commitSelection([]pool.Handle{commander}, false)
	step(2)
	p := settings.DefaultPresentation()
	p.Overview = settings.OverviewMegamap
	b.hostPresentation = &p
	b.setMegamapShown(true, cl)
	lens := b.megamapLens()
	f, _ := b.currentSnapshot()
	u, _ := snapshotUnitByHandle(f, commander)
	ux, uy := lens.Project(radarMapPixel(u.X), radarMapPixel(u.Y), radarMapPixel(u.Z))
	at := func(dx, dy int32) (int32, int32) {
		return max(lens.X, min(lens.X+lens.W-1, lens.X+ux+dx)), max(lens.Y, min(lens.Y+lens.H-1, lens.Y+uy+dy))
	}
	// A build from the megamap at the first legal site found near the
	// commander, then two queued moves after it.
	if def, ok := b.cat.Unit("armsolar"); ok {
		b.armPlacement(def)
		placed := false
		for r := int32(12); r < 60 && !placed; r += 6 {
			mx, my := at(r, r/2)
			b.battleState().Input.PointerX, b.battleState().Input.PointerY = mx, my
			b.updatePlacement(mx, my)
			if b.battleState().Input.BuildOK {
				b.megamapWorldClick(cl, mx, my, true)
				placed = true
			}
		}
		if !placed {
			t.Logf("%s: no legal site found for the megamap build", mapName)
		}
		b.disarmPlacement()
	}
	for _, d := range [][2]int32{{120, 40}, {60, 160}} {
		mx, my := at(d[0], d[1])
		b.megamapNeutralOrder(mx, my, true)
	}
	step(3)
	// Shift held, the commander hovered (a focus unit) and a ghost armed.
	b.battleState().Input.ShiftHeld = true
	if def, ok := b.cat.Unit("armsolar"); ok {
		b.armPlacement(def)
		mx, my := at(-70, 60)
		b.battleState().Input.PointerX, b.battleState().Input.PointerY = mx, my
		b.updatePlacement(mx, my)
	}
	b.footerHoverUnit = commander
	// A box drag in progress, 40×30 image pixels from the image's middle.
	b.megamap.boxActive = true
	b.megamap.boxX0, b.megamap.boxY0 = lens.W/2, lens.H/2
	b.megamap.boxX1, b.megamap.boxY1 = lens.W/2+40, lens.H/2+30
	cl.BeginPresentationFrame()
	if err := encodeShotPNG(out, cl.ComposeFrame()); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}
