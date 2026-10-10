package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Exercise the grouped menu transaction, rather than writing the preference
// directly as a capture does (DESIGN_INTERFACE_HUD_INPUT §3.17).
func nlScaleControl(t *testing.T, s *nlScreen) (nlCard, int) {
	t.Helper()
	c := s.sidebarCard()
	for i, p := range c.parts {
		if p.key == "scale" {
			if !slices.Equal(p.steps, []string{"Auto", "1x", "2x"}) {
				t.Fatalf("scale choices = %v", p.steps)
			}
			return c, i
		}
	}
	t.Fatal("missing menu scale control")
	return nlCard{}, 0
}

func nlScaleFile(scale int) settings.Settings {
	file := settings.Defaults()
	file.Presentation.UIScale = scale
	file.Presentation.BuildMenuPageSize, file.Presentation.SidebarOrders = 10, 0
	file.Presentation.Glint, file.Presentation.ZoomLockPercent = 0, 137
	return file
}

// The menu has no CLI scale override; a zero-valued Options would force Auto.
func nlScaleScreen(mod *modlibrary.Mod, file settings.Settings) (*gameShell, *nlScreen) {
	g, s := settingsRegressionScreen(mod, file)
	g.opts.UIScale = -1
	g.applySettings(file)
	s.draft = s.freshDraft(g)
	s.bindSource(g)
	return g, s
}

func TestNLScreenScaleOnlyEditApplySaveReload(t *testing.T) {
	if settings.DefaultPresentation().UIScale != 0 {
		t.Fatal("scale default must remain Auto")
	}
	for _, scale := range []int{2, 1, 0} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
			initial := 0
			if scale == 0 {
				initial = 2
			}
			file := nlScaleFile(initial)
			g, s := nlScaleScreen(nil, file)
			g.settingsWritable = true
			c, part := nlScaleControl(t, s)
			s.setPart(c, part, scale)
			want := file.Presentation
			want.UIScale = scale
			if s.draft.pres != want || g.presentation != file.Presentation || !s.touched[c.key] || s.dirty() != 1 {
				t.Fatalf("scale-only edit: draft scale %d, live %d, touched %v, dirty %d", s.draft.pres.UIScale, g.presentation.UIScale, s.touched, s.dirty())
			}
			composed, err := s.draftSettings()
			if err != nil || composed.Presentation != want {
				t.Fatalf("pending Apply lost scale or sibling values: %+v, %v", composed.Presentation, err)
			}
			s.apply()
			stored, err := settings.Load()
			if err != nil || stored.Presentation != want || g.presentation != want || s.dirty() != 0 {
				t.Fatalf("Apply/save lost scale or sibling values: %+v, %v", stored.Presentation, err)
			}
			restarted, reopened := nlScaleScreen(nil, stored)
			if restarted.presentation != want || reopened.draft.pres != want {
				t.Fatal("restart lost scale or unrelated presentation settings")
			}
		})
	}
}

func TestNLScreenScaleCancelAndReset(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	file := nlScaleFile(2)
	g, s := nlScaleScreen(nil, file)
	g.settingsWritable = true
	g.saveSettings()
	c, part := nlScaleControl(t, s)
	s.setPart(c, part, 1)
	if s.draft.pres.UIScale != 1 || g.presentation.UIScale != 2 || s.dirty() != 1 {
		t.Fatal("Cancel fixture did not have a pending scale-only edit")
	}
	s.hide() // Back/Esc closes the draft without Apply.
	stored, err := settings.Load()
	if err != nil || stored.Presentation != file.Presentation || g.presentation != file.Presentation {
		t.Fatalf("Cancel saved the pending scale: %+v, %v", stored.Presentation, err)
	}
	g, s = nlScaleScreen(nil, stored)
	g.settingsWritable = true
	c, part = nlScaleControl(t, s)
	if s.draft.pres != file.Presentation || s.dirty() != 0 {
		t.Fatal("reopened menu retained the cancelled scale")
	}
	s.setPart(c, part, 0)
	s.apply()
	want := file.Presentation
	want.UIScale = 0
	stored, err = settings.Load()
	if err != nil || stored.Presentation != want || s.draft.pres != want {
		t.Fatalf("reset to Auto lost sibling settings or failed to save: %+v, %v", stored.Presentation, err)
	}
}

func TestNLScreenScaleModPresetAndRecommendation(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(fmt.Sprintf("locked=%v", locked), func(t *testing.T) {
			t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
			mod := &modlibrary.Mod{Metadata: modlibrary.Metadata{ID: "scale-mod", Name: "Scale mod",
				Config: &modlibrary.Config{Settings: json.RawMessage(`{"presentation":{"uiScale":2}}`)}}}
			if locked {
				mod.Config.Locks = []string{"presentation.uiScale"}
			}
			file := nlScaleFile(1)
			other := json.RawMessage(`{"presentation":{"uiScale":2,"glint":1}}`)
			file.ModSettings = map[string]json.RawMessage{"other-mod": other}
			g, s := nlScaleScreen(mod, file)
			g.opts.UIScale, g.settingsWritable = -1, true
			c, part := nlScaleControl(t, s)
			if s.draft.pres.UIScale != 2 || s.cardSource(c) != "set" {
				t.Fatal("scale-only mod recommendation was not represented by the card")
			}
			s.setPart(c, part, 0)
			if locked {
				if s.dialog != "override" || s.draft.pres.UIScale != 2 {
					t.Fatal("scale-only edit bypassed the mod lock")
				}
				s.confirmOverride()
			}
			if s.draft.pres.UIScale != 0 || s.cardSource(c) != "changed" || g.presentation.UIScale != 2 {
				t.Fatal("mod scale edit did not stay in the pending draft")
			}
			s.savePreset("Auto scale")
			stored, err := settings.Load()
			if err != nil || stored.Presentation != file.Presentation || len(stored.Presets) != 1 {
				t.Fatalf("saving draft preset changed base scale %d or preset count %d: %v", stored.Presentation.UIScale, len(stored.Presets), err)
			}
			preset, err := settings.Layer(nlScaleFile(2), stored.Presets[0].Settings)
			if err != nil || preset.Presentation.UIScale != 0 || g.presentation.UIScale != 2 {
				t.Fatalf("saved preset lost explicit Auto or changed live scale: %+v, %v", preset.Presentation, err)
			}
			s.apply()
			stored, err = settings.Load()
			var keptOther, wantOther any
			otherErr := json.Unmarshal(stored.ModSettings["other-mod"], &keptOther)
			_ = json.Unmarshal(other, &wantOther)
			if err != nil || otherErr != nil || stored.Presentation != file.Presentation || !reflect.DeepEqual(keptOther, wantOther) {
				t.Fatalf("mod Apply changed base scale %d or another mod %s: %v, %v", stored.Presentation.UIScale, stored.ModSettings["other-mod"], err, otherErr)
			}
			restarted, reopened := nlScaleScreen(mod, stored)
			restarted.opts.UIScale, restarted.settingsWritable = -1, true
			want := file.Presentation
			want.UIScale = 0
			if restarted.presentation != want || reopened.draft.pres != want || len(stored.Presets) != 1 {
				t.Fatal("restart lost the per-mod Auto override, sibling settings or preset")
			}
			// This is the card's actual "Use the mod's" reset transaction.
			reopened.setCardDraft(reopened.sidebarCard(), reopened.src.rec)
			reopened.apply()
			stored, err = settings.Load()
			if err != nil || restarted.presentation.UIScale != 2 || stored.Presentation != file.Presentation {
				t.Fatalf("recommendation reset did not save in the mod scope: live %d, base %d, %v", restarted.presentation.UIScale, stored.Presentation.UIScale, err)
			}
			base, baseScreen := nlScaleScreen(nil, stored)
			base.settingsWritable = true
			baseScreen.applyPresetToDraft(nlPresetEntry{name: "Auto scale", patch: stored.Presets[0].Settings}, []bool{false, false, true})
			baseScreen.apply()
			if base.presentation.UIScale != 0 || base.presentation.BuildMenuPageSize != 10 || base.presentation.SidebarOrders != 0 {
				t.Fatal("reapplying the saved preset lost scale or unrelated sidebar choices")
			}
		})
	}
}

func TestNLScreenScaleAvailabilityAndPresetScope(t *testing.T) {
	for _, renderer := range []string{"classic", "modern"} {
		file := nlScaleFile(1)
		file.Presentation.Renderer = renderer
		g, s := nlScaleScreen(nil, file)
		c, part := nlScaleControl(t, s)
		s.setPart(c, part, 2)
		want := 1
		if renderer == "modern" {
			want = 2
		}
		s.apply()
		if g.presentation.UIScale != want || g.presentation.Renderer != renderer || g.presentation.BuildMenuPageSize != 10 {
			t.Fatalf("%s scale availability changed: %+v", renderer, g.presentation)
		}
	}
	// Retain the scale's existing Controls & interface preset ownership.
	for _, scopes := range [][]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		file := nlScaleFile(1)
		g, s := nlScaleScreen(nil, file)
		s.applyPresetToDraft(nlPresetEntry{name: "Scale", patch: json.RawMessage(`{"presentation":{"uiScale":2}}`)}, scopes)
		want := file.Presentation
		if scopes[2] {
			want.UIScale = 2
			if !s.touched["sidebar"] || s.dirty() != 1 {
				t.Fatal("scale-only preset failed to mark the menu card changed")
			}
		}
		s.apply()
		if g.presentation != want {
			t.Fatalf("preset scope %v changed unrelated settings: %+v", scopes, g.presentation)
		}
	}
	g, s := nlScaleScreen(nil, nlScaleFile(2))
	for _, preset := range s.presetEntries() {
		if preset.name == "Original game" {
			s.applyPresetToDraft(preset, []bool{false, false, true})
		}
	}
	s.apply()
	if g.presentation.UIScale != 0 || g.presentation.BuildMenuPageSize != 10 || g.presentation.SidebarOrders != 0 || g.presentation.Glint != 0 {
		t.Fatal("restoring the Controls preset lost Auto or changed graphics/sidebar settings")
	}
}
