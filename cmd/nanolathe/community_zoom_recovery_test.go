package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Issue #99 also occurs with saved overview preferences, which a mod without
// camera recommendations inherits (DESIGN_INTERFACE_HUD_INPUT §3.15).
func TestEscalationSavedMegamapCanEnableZoomDirectly(t *testing.T) {
	meta, err := modlibrary.ReadConfigFile(shippedConfigPath(t, "escalation-10.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	mod := &modlibrary.Mod{Metadata: meta}
	for _, perMod := range []bool{false, true} {
		for _, zoom := range []int{settings.ZoomSmooth, settings.ZoomStepped} {
			t.Run(fmt.Sprintf("perMod%v-zoom%d", perMod, zoom), func(t *testing.T) {
				t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
				file := settings.Defaults()
				if perMod {
					file.ModSettings = map[string]json.RawMessage{mod.ID: json.RawMessage(`{"presentation":{"overview":1}}`)}
				} else {
					file.Presentation.Overview = settings.OverviewMegamap
				}
				g, s := settingsRegressionScreen(mod, file)
				g.settingsWritable = true
				b := zoomTestBattle()
				b.shell, b.sess = g, &session.Session{Gameplay: g.gameplay}
				if !b.megamapMode() || g.gameplay != gameplay.Community39 {
					t.Fatal("saved settings did not reproduce Community's disabled camera")
				}
				s.setCard(configurationCard(t, s, "zoomstyle"), zoom)
				if s.draft.pres.Overview != settings.OverviewZoom || s.draft.pres.ZoomStyle != zoom {
					t.Fatal("selecting camera zoom did not leave the saved megamap")
				}
				if g.presentation.Overview != settings.OverviewMegamap {
					t.Fatal("editing the draft changed the live camera before Apply")
				}
				s.apply()
				stored, err := settings.Load()
				if err != nil {
					t.Fatal(err)
				}
				restarted, _ := settingsRegressionScreen(mod, stored)
				if restarted.gameplay != gameplay.Community39 || restarted.presentation.Overview != settings.OverviewZoom || restarted.presentation.ZoomStyle != zoom {
					t.Fatal("the mod's minimum or saved settings undid the zoom selection")
				}
				if stored.Presentation.Overview != file.Presentation.Overview {
					t.Fatal("the per-mod camera choice changed the original game's settings")
				}
				b.shell = restarted
				b.syncCameraControls()
				b.wheelZoom(400, 250, 1)
				if b.megamapMode() || b.zoom.Target(b.cam) <= camera.ZoomUnit {
					t.Fatal("the saved zoom selection did not magnify the battle camera")
				}
				for range 100 {
					b.zoom.Step(b.cam)
				}
				factor := b.cam.EffectiveZoom()
				b.toggleViewScale(true)
				b.toggleViewScale(true)
				if b.cam.EffectiveZoom() != factor {
					t.Fatal("F9 did not restore the recovered camera zoom")
				}
			})
		}
	}
}

func TestCommunityZoomSelectionKeepsOverviewLock(t *testing.T) {
	mod := settingsRegressionMod()
	mod.Config.Settings = json.RawMessage(`{"gameplay":"community-3.9","presentation":{"overview":1}}`)
	mod.Config.Locks = []string{"presentation.overview"}
	g, s := settingsRegressionScreen(mod, settings.Defaults())
	card := configurationCard(t, s, "zoomstyle")
	before := s.draft.pres
	s.setCard(card, settings.ZoomSmooth)
	if s.dialog != "override" || s.draft.pres != before || g.presentation != before {
		t.Fatal("enabling camera zoom bypassed the content's overview lock")
	}
	s.confirmOverride()
	if s.draft.pres.Overview != settings.OverviewZoom || g.presentation.Overview != settings.OverviewMegamap {
		t.Fatal("the approved zoom edit missed its draft or reached live settings before Apply")
	}
	nlCopyCard(card, &s.draft, &s.src.rec)
	if s.draft.pres != before {
		t.Fatal("restoring the camera card did not restore its overview preference")
	}
	s.setCard(card, settings.ZoomNone)
	if s.draft.pres.Overview != settings.OverviewMegamap || s.draft.pres.ZoomStyle != settings.ZoomNone {
		t.Fatal("No zoom changed the independently selected megamap")
	}
}

func TestCommunityBattleOptionsCanEnableZoomDirectly(t *testing.T) {
	g, panel, cl := syntheticOptionsPage(t, "nanolathe", nanolatheOptionsPage)
	g.gameplay = gameplay.Community39
	g.presentation.Overview = settings.OverviewMegamap
	entry := g.presentation
	g.syncNanolatheOptions()
	clickRowGadget(t, g, panel, cl, "NZOOM", input.MouseButtonLeft)
	if g.presentation.Overview != settings.OverviewZoom || g.presentation.ZoomStyle != settings.ZoomStepped || g.gameplay != gameplay.Community39 {
		t.Fatal("the battle's zoom selector could not leave the megamap")
	}
	g.setNanolathePreferences(entry)
	if g.presentation != entry {
		t.Fatal("Undo did not restore the zoom selector's linked overview choice")
	}
}

func TestModernZoomCardRestoreKeepsIndependentTabChoice(t *testing.T) {
	g, s := settingsRegressionScreen(nil, settings.Defaults())
	s.draft.pres.Overview = settings.OverviewMegamap
	s.draft.pres.ZoomStyle = settings.ZoomNone
	card := configurationCard(t, s, "zoomstyle")
	s.setCard(card, settings.ZoomSmooth)
	saved := s.snapshot(g)
	nlCopyCard(card, &s.draft, &saved)
	if s.draft.pres.Overview != settings.OverviewMegamap {
		t.Fatal("restoring Modern camera zoom changed the independent Tab choice")
	}
}

func TestCommunityZoomUndoAfterModeSwitch(t *testing.T) {
	for _, entryMode := range []gameplay.Mode{gameplay.Community39, gameplay.Modern} {
		t.Run(string(entryMode), func(t *testing.T) {
			g, panel, cl := syntheticOptionsPage(t, "nanolathe", nanolatheOptionsPage)
			g.gameplay = entryMode
			g.presentation.Overview = settings.OverviewMegamap
			entry := g.presentation
			optionsState.snapshot = g.retailOptionsSnapshot()
			g.setGameplay(gameplay.Community39)
			g.syncNanolatheOptions()
			clickRowGadget(t, g, panel, cl, "NZOOM", input.MouseButtonLeft)
			g.setGameplay(gameplay.Modern)
			// This authored-fixture test has no GUI source to reopen; exercise
			// the complete Undo transaction before its ordinary reopen boundary.
			optionsAssets = nil
			g.undoRetailOptionsPage()
			if g.gameplay != entryMode || g.presentation != entry {
				t.Fatal("changing rules before Undo lost the camera selector's overview edit")
			}
		})
	}
}

func TestCommunityZoomApplyAfterModeSwitch(t *testing.T) {
	file := settings.Defaults()
	file.Gameplay = gameplay.Community39
	file.Presentation.Overview = settings.OverviewMegamap
	g, s := settingsRegressionScreen(nil, file)
	s.setCard(configurationCard(t, s, "zoomstyle"), settings.ZoomSmooth)
	if !s.touched["tab"] {
		t.Fatal("the camera selector did not record its explicit Tab edit")
	}
	s.setCard(configurationCard(t, s, "rules"), 2)
	s.apply()
	if g.gameplay != gameplay.Modern || g.presentation.Overview != settings.OverviewZoom {
		t.Fatal("changing rules before Apply lost the camera selector's overview edit")
	}
}
