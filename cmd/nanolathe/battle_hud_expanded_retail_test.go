//go:build retail

package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

func TestRetailExpandedSidebarFlatCommanderProducts(t *testing.T) {
	cs, err := openContent(Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	sess, cat, err := newBattleSession(Options{Map: "ashap plateau", Seed: 7}, cs)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Owner == sess.LocalOwner && u.Def != nil && u.Def.Builder {
			u.Flags |= hud.SelectionFlag
			break
		}
	}
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
	}
	// This capacity leaves a partial final page and previously exposed a third
	// page containing only the generated template's unused slots.
	cl, err := client.New(client.Options{Width: 1280, Height: 844})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetEnhanced(true)
	b := &battleSession{sess: sess, cat: cat, cl: cl}
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, retailPaletteForTest(t, cs), nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	f := sess.Snapshot.Current()
	w, _, err := b.hud.windowForRequired(b, f)
	if err != nil || w == nil {
		t.Fatalf("commander window: %v", err)
	}
	view, ok := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !ok {
		t.Fatal("no selected commander")
	}
	def, _ := cat.Unit(view.DefName)
	// Expected sequence comes from resolved authored records, independent of
	// CANBUILD and of the layout compiler under test.
	type productSource struct {
		window *gui.Window
		name   string
	}
	var want []productSource
	for page := 1; page < int(f.CommandPage.PageCount); page++ {
		source, _ := b.hud.sidebarBuildPage(cat, def, page)
		if source == nil {
			t.Fatalf("missing source page %d", page)
		}
		var indices []int
		for i, g := range source.Gadgets {
			if g.Active != 0 && g.CommonAttribs&4 != 0 && !sidebarEmptySlot(g.Name) {
				indices = append(indices, i)
			}
		}
		slices.SortStableFunc(indices, func(i, j int) int {
			a, b := source.PlacedRect(i), source.PlacedRect(j)
			if a.Y != b.Y {
				return int(a.Y - b.Y)
			}
			return int(a.X - b.X)
		})
		for _, i := range indices {
			want = append(want, productSource{source, source.Gadgets[i].Name})
		}
	}
	for _, height := range []int{480, 768, 1080} {
		t.Run(fmt.Sprintf("height%d", height), func(t *testing.T) {
			cl.Resize(1280, height)
			state, active := b.hud.expandedSidebarPaging(b, f)
			if !active {
				t.Fatal("retail flat paging unavailable")
			}
			var got []productSource
			controlRects := make(map[string]gui.Rect)
			for page := 1; page < state.Count; page++ {
				b.hud.selectExpandedSidebarPage(b, f, page)
				composed, _, err := b.hud.windowForRequired(b, f)
				if err != nil {
					t.Fatal(err)
				}
				commands := make(map[string]bool)
				for _, g := range composed.Gadgets {
					commands[commandButtonName(g.Name)] = true
				}
				for _, command := range []string{"MOVE", "STOP", "ATTACK", "REPAIR", "CAPTURE"} {
					if !commands[command] {
						t.Fatalf("build page %d lost %s", page, command)
					}
				}
				products := 0
				for i, g := range composed.Gadgets {
					r := composed.PlacedRect(i)
					if g.CommonAttribs&4 != 0 && !sidebarEmptySlot(g.Name) {
						products++
						got = append(got, productSource{b.hud.expandedSidebar.sources[i].window, g.Name})
						if r.W != 64 || r.H != 64 || r.Y < 128 || r.Y+r.H > int32(height) {
							t.Fatalf("product %s is not a fitted grid cell: %+v", g.Name, r)
						}
						if hit := composed.HitTest(r.X+r.W/2, r.Y+r.H/2); hit != i {
							t.Fatalf("product %s covered by gadget %d", g.Name, hit)
						}
					}
					if commandButtonName(g.Name) != "" || sidebarNavigation(g) {
						if page == 1 {
							controlRects[g.Name] = r
						} else if r != controlRects[g.Name] {
							t.Fatalf("page %d moved %s: %+v want %+v", page, g.Name, r, controlRects[g.Name])
						}
					}
				}
				if products == 0 {
					t.Fatalf("empty adaptive page %d", page)
				}
				if dir := os.Getenv("NANOLATHE_MENU_SHOTS"); dir != "" && (page == 1 || page == state.Count-1) {
					cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
					cl.SetSnapshot(sess.Snapshot)
					cl.SetPalette(retailPaletteForTest(t, cs))
					file, err := os.Create(filepath.Join(dir, fmt.Sprintf("ota-modern-%d-page%d.png", height, page)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(file, cl.ComposeFrame())
					closeErr := file.Close()
					if err != nil {
						t.Fatal(err)
					}
					if closeErr != nil {
						t.Fatal(closeErr)
					}
				}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("product sequence mismatch: got %v want %v", got, want)
			}
			b.hud.selectExpandedSidebarPage(b, f, 0)
			orders, _, err := b.hud.windowForRequired(b, f)
			if err != nil {
				t.Fatal(err)
			}
			commands := make(map[string]bool)
			for _, g := range orders.Gadgets {
				commands[commandButtonName(g.Name)] = true
			}
			if !commands["REPAIR"] || !commands["CAPTURE"] {
				t.Fatalf("Orders lost commander commands: %v", commands)
			}
		})
	}

}
