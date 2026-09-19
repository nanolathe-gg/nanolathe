//go:build retail

package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// Enter through the completed front-end transition: it preserves authored
// quickkey bytes, including case differences between physical and DL pages.
func TestRetailBuilderMenusAfterTransition(t *testing.T) {
	for side := 0; side < 2; side++ {
		t.Run(fmt.Sprint(side), func(t *testing.T) {
			opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 7}
			cs, err := openContent(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			cfg := session.SkirmishConfig{MapName: opts.Map, NumPlayers: 2}
			cfg.Players[0].Side = side
			cfg.Players[1].Controller = session.SkirmishControllerComputer
			cfg.ApplyDefaults()
			sess, cat, err := newBattleSessionWithConfig(opts, cs, cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx := newBattleWindowContext(cs, nil)
			ctx.completeTransition()
			pal := retailPaletteForTest(t, cs)
			h, err := loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, ctx)
			if err != nil {
				t.Fatal(err)
			}
			cl, err := client.New(client.Options{Width: 1280, Height: 768})
			if err != nil {
				t.Fatal(err)
			}
			cl.SetEnhanced(true)
			b := &battleSession{sess: sess, cat: cat, cl: cl, hud: h}
			var x, y, z numeric.Fixed
			for _, u := range sess.Units.Iter() {
				if u.Owner == sess.LocalOwner {
					x, y, z = u.X, u.Y, u.Z
				}
			}
			var names []string
			for name, d := range cat.Units {
				if d.Builder && d.BuildPageCount > 1 && strings.EqualFold(d.Side, cat.Sides[side].Name) {
					names = append(names, name)
				}
			}
			slices.Sort(names)
			if len(names) == 0 {
				t.Fatal("no builders")
			}
			step := int32(1)
			for _, name := range names {
				t.Run(name, func(t *testing.T) {
					for _, u := range sess.Units.Iter() {
						u.Flags &^= hud.SelectionFlag
					}
					d, _ := cat.Unit(name)
					handle, err := sess.Units.Create(d, sess.LocalOwner, x+numeric.FixedFromInt(160), y, z)
					if err != nil {
						t.Fatal(err)
					}
					u := sess.Units.Unit(handle)
					u.Flags = hud.EncodePageBits(u.Flags|hud.SelectionFlag, 1)
					for n := 0; n < 5; n++ {
						sess.Step(step)
						step++
					}
					f := sess.Snapshot.Current()
					for _, height := range []int{480, 768, 1080} {
						cl.Resize(1280, height)
						w, _, err := h.windowForRequired(b, f)
						if err != nil {
							t.Fatal(err)
						}
						if w == nil || !h.expandedSidebar.key.flat {
							t.Fatalf("height%d: builder retained separate authored pages", height)
						}
						state, _ := h.expandedSidebarPaging(b, f)
						controls := map[string]bool{}
						for _, g := range w.Gadgets {
							controls[commandButtonName(g.Name)] = true
						}
						for _, name := range []string{"MOVE", "STOP", "ATTACK", "REPAIR", "FIREORD"} {
							if !controls[name] {
								t.Fatalf("missing combined command %s", name)
							}
						}
						if len(sidebarVisibleProducts(w)) < 1 {
							t.Fatal("combined page has no build products")
						}
						first := sidebarVisibleProducts(w)
						h.selectExpandedSidebarPage(b, f, 0)
						if !slices.Equal(sidebarVisibleProducts(expandedWindow(t, b)), first) {
							t.Fatal("Orders hid the current build partition")
						}
						h.selectExpandedSidebarPage(b, f, state.Remembered)
						if dir := os.Getenv("NANOLATHE_MENU_SHOTS"); dir != "" && height == 768 {
							cl.SetUIStage(battleHUDUIStage{hud: h, battle: b})
							cl.SetSnapshot(sess.Snapshot)
							cl.SetPalette(pal)
							file, err := os.Create(filepath.Join(dir, fmt.Sprintf("ota-%s-%d.png", name, height)))
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
				})
			}
		})
	}
}
