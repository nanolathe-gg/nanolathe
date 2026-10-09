package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// These tests lock the approved host preference transactions and their shared
// preview, independent of gameplay mode (interface design §3.3 and §3.17).
func nlSidebarOption(t *testing.T, s *nlScreen, key string) nlCard {
	t.Helper()
	for _, card := range s.gameCards() {
		if card.key == "sidebar" {
			part := card.parts[0]
			if key == "sidebar-orders" {
				part = card.parts[1]
			}
			return nlCard{key: card.key, label: part.label, steps: part.steps, get: part.get, set: part.set, desc: card.desc}
		}
	}
	t.Fatalf("missing Game card %s", key)
	return nlCard{}
}

func TestNLSidebarChoicesUseDraftContentAndExplicitFreeFlow(t *testing.T) {
	mod := &modlibrary.Mod{Metadata: modlibrary.Metadata{ID: "twelve", Name: "Twelve", BuildMenuPageSize: 12}}
	g, s := settingsRegressionScreen(mod, settings.Defaults())
	build, orders := nlSidebarOption(t, s, "sidebar"), nlSidebarOption(t, s, "sidebar-orders")
	if build.label != "Build items" || !slices.Equal(build.steps, []string{"6 per page", "12 per page", "Free flow"}) || orders.label != "Orders below build" || !slices.Equal(orders.steps, []string{"When space permits", "Never"}) {
		t.Fatal("missing independent sidebar choices")
	}
	if build.get(&s.draft) != 1 || s.nlSidebarBuildLimit(&s.draft) != 12 || !strings.Contains(build.desc(&s.draft, 1), "content recommends 12") {
		t.Fatal("inherited count did not show the content recommendation")
	}
	s.mods = append(s.mods, modlibrary.Mod{Metadata: modlibrary.Metadata{ID: "six", Name: "Six", BuildMenuPageSize: 6}})
	s.draft.mod = 2
	if build.get(&s.draft) != 0 || s.nlSidebarBuildLimit(&s.draft) != 6 {
		t.Fatal("count followed running content instead of draft content")
	}
	s.draft.mod = 0
	if build.get(&s.draft) != 2 || s.nlSidebarBuildLimit(&s.draft) != 0 {
		t.Fatal("Stock content inherited the running mod's recommendation")
	}
	s.draft.mod = 1
	s.setCard(build, 2)
	if s.draft.pres.ExpandedSidebar != 1 || s.draft.pres.BuildMenuPageSize != 0 || s.nlSidebarBuildLimit(&s.draft) != 0 || g.presentation.BuildMenuPageSize != -1 {
		t.Fatal("explicit Free flow failed to override the mod in the draft")
	}
	before := s.draft.pres.BuildMenuPageSize
	s.setCard(orders, 1)
	if s.draft.pres.SidebarOrders != 0 || s.draft.pres.BuildMenuPageSize != before || s.draft.pres.ExpandedSidebar != 1 {
		t.Fatal("orders changed the independent build preference")
	}
	for choice, count := range map[int]int{0: 6, 1: 12, 2: 0} {
		build.set(&s.draft, choice)
		if s.draft.pres.ExpandedSidebar != 1 || s.draft.pres.BuildMenuPageSize != count || s.draft.pres.SidebarOrders != 0 {
			t.Fatalf("choice %d did not save count %d independently", choice, count)
		}
	}
}

func TestNLSidebarOptionsDraftApplySaveCancel(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g, s := settingsRegressionScreen(nil, settings.Defaults())
	g.settingsWritable = true
	build, orders := nlSidebarOption(t, s, "sidebar"), nlSidebarOption(t, s, "sidebar-orders")
	s.setCard(build, 0)
	s.setCard(orders, 1)
	if s.draft.pres.BuildMenuPageSize != 6 || s.draft.pres.SidebarOrders != 0 || g.presentation.BuildMenuPageSize != -1 || g.presentation.SidebarOrders != 1 || s.dirty() != 1 {
		t.Fatal("sidebar draft changed live settings or lost its independent change count")
	}
	s.savePreset("Six without orders")
	stored, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Presentation.BuildMenuPageSize != -1 || stored.Presentation.SidebarOrders != 1 || len(stored.Presets) != 1 {
		t.Fatal("saving a draft preset changed the applied sidebar")
	}
	preset, err := settings.Layer(settings.Defaults(), stored.Presets[0].Settings)
	if err != nil || preset.Presentation.BuildMenuPageSize != 6 || preset.Presentation.SidebarOrders != 0 {
		t.Fatalf("saved preset lost sidebar options: %+v, %v", preset.Presentation, err)
	}
	s.apply()
	stored, err = settings.Load()
	if err != nil || g.presentation.BuildMenuPageSize != 6 || stored.Presentation.BuildMenuPageSize != 6 || g.presentation.SidebarOrders != 0 || stored.Presentation.SidebarOrders != 0 || s.dirty() != 0 {
		t.Fatalf("Apply did not persist both choices: %+v, %v", stored.Presentation, err)
	}
	s.setCard(build, 1)
	s.setCard(orders, 0)
	s.hide()
	stored, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	restarted, reopened := settingsRegressionScreen(nil, stored)
	if g.presentation.BuildMenuPageSize != 6 || g.presentation.SidebarOrders != 0 || restarted.presentation.BuildMenuPageSize != 6 || restarted.presentation.SidebarOrders != 0 || reopened.draft.pres.BuildMenuPageSize != 6 || reopened.draft.pres.SidebarOrders != 0 {
		t.Fatal("cancelled draft replaced saved sidebar options")
	}
}

func TestNLSidebarLegacyCountSurvivesUnrelatedApply(t *testing.T) {
	file := settings.Defaults()
	file.Presentation.BuildMenuPageSize = 10
	g, s := settingsRegressionScreen(nil, file)
	build, orders := nlSidebarOption(t, s, "sidebar"), nlSidebarOption(t, s, "sidebar-orders")
	if build.get(&s.draft) != 1 || !strings.Contains(build.desc(&s.draft, build.get(&s.draft)), "Current count: 10 per page") {
		t.Fatal("legacy count was concealed")
	}
	s.setCard(orders, 1)
	s.apply()
	if g.presentation.BuildMenuPageSize != 10 || s.draft.pres.BuildMenuPageSize != 10 {
		t.Fatal("applying the orders preference rewrote the saved count")
	}
	s.setCard(build, 1)
	if s.draft.pres.BuildMenuPageSize != 12 || s.dirty() != 1 {
		t.Fatal("choosing the displayed fixed count did not replace the legacy count")
	}
	s.apply()
	if g.presentation.BuildMenuPageSize != 12 {
		t.Fatal("Apply did not keep the explicit fixed count")
	}
	s.setCard(build, 0)
	s.apply()
	if g.presentation.BuildMenuPageSize != 6 {
		t.Fatal("explicit supported choice did not replace the legacy count")
	}
}

func TestNLSidebarGroupedOrdersChangePreservesExactCount(t *testing.T) {
	for _, count := range []int{-1, 10} {
		file := settings.Defaults()
		file.Presentation.BuildMenuPageSize = count
		g, s := settingsRegressionScreen(nil, file)
		card := s.sidebarCard()
		s.partSel[card.key] = 1
		s.step(card, card.get(&s.draft), 1)
		if s.draft.pres.BuildMenuPageSize != count || s.draft.pres.SidebarOrders != 0 || s.dirty() != 1 {
			t.Fatal("changing only grouped orders rewrote the build preference")
		}
		s.apply()
		if g.presentation.BuildMenuPageSize != count || g.presentation.SidebarOrders != 0 {
			t.Fatal("Apply lost the independent grouped choices")
		}
	}
}

func TestNLSidebarExplicitFreeFlowReplacesInheritedFreeFlow(t *testing.T) {
	g, s := settingsRegressionScreen(nil, settings.Defaults())
	build := nlSidebarOption(t, s, "sidebar")
	if build.get(&s.draft) != 2 || s.draft.pres.BuildMenuPageSize != -1 {
		t.Fatal("missing inherited Free flow default")
	}
	s.setCard(build, 2)
	if s.draft.pres.BuildMenuPageSize != 0 || g.presentation.BuildMenuPageSize != -1 || s.dirty() != 1 {
		t.Fatal("explicit Free flow looked unchanged or reached live preferences")
	}
	s.apply()
	if g.presentation.BuildMenuPageSize != 0 || s.dirty() != 0 {
		t.Fatal("Apply lost explicit Free flow")
	}
}

func TestNLSidebarOptionsGraphicsPresetScopeAndRestore(t *testing.T) {
	for _, path := range []string{"presentation.expandedSidebar", "presentation.buildMenuPageSize", "presentation.sidebarOrders"} {
		if !slices.Contains(nlGraphicsPaths(), path) || slices.Contains(nlControlsPaths(), path) {
			t.Fatalf("sidebar preference is outside Graphics scope: %s", path)
		}
	}
	patch := json.RawMessage(`{"presentation":{"expandedSidebar":0,"buildMenuPageSize":12,"sidebarOrders":0,"zoomLockPercent":120}}`)
	for _, tc := range []struct {
		name   string
		scopes []bool
		want   bool
	}{
		{"rules", []bool{true, false, false}, false},
		{"graphics", []bool{false, true, false}, true},
		{"controls", []bool{false, false, true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := settings.Defaults()
			file.Presentation.BuildMenuPageSize = 6
			g, s := settingsRegressionScreen(nil, file)
			s.applyPresetToDraft(nlPresetEntry{name: "Sidebar", patch: patch}, tc.scopes)
			want := file.Presentation
			if tc.want {
				want.ExpandedSidebar, want.BuildMenuPageSize, want.SidebarOrders = 1, 6, 0
			}
			if s.draft.pres.ExpandedSidebar != want.ExpandedSidebar || s.draft.pres.BuildMenuPageSize != want.BuildMenuPageSize || s.draft.pres.SidebarOrders != want.SidebarOrders {
				t.Fatalf("%s scope changed the wrong sidebar preferences", tc.name)
			}
			s.apply()
			if g.presentation.ExpandedSidebar != want.ExpandedSidebar || g.presentation.BuildMenuPageSize != want.BuildMenuPageSize || g.presentation.SidebarOrders != want.SidebarOrders {
				t.Fatalf("%s scoped Apply lost sidebar values", tc.name)
			}
			if tc.want {
				for _, preset := range s.presetEntries() {
					if preset.name == "Original game" {
						s.applyPresetToDraft(preset, []bool{false, true, false})
					}
				}
				s.apply()
				defaults := settings.DefaultPresentation()
				if g.presentation.ExpandedSidebar != defaults.ExpandedSidebar || g.presentation.BuildMenuPageSize != defaults.BuildMenuPageSize || g.presentation.SidebarOrders != defaults.SidebarOrders {
					t.Fatal("Graphics restore lost sidebar defaults")
				}
			}
		})
	}
}

func TestNLSidebarPageOwnsBothChoicesAndTheirPaths(t *testing.T) {
	_, s := settingsRegressionScreen(nil, settings.Defaults())
	card := s.sidebarCard()
	if card.label != "Sidebar" || card.kind != nlGroup || len(card.parts) != 3 || card.compare != nil {
		t.Fatal("sidebar choices do not share one page")
	}
	for _, c := range s.gameCards() {
		if c.key == "sidebar-orders" {
			t.Fatal("orders still has a separate page")
		}
	}
	paths := s.cardPaths(card, settings.Defaults())
	for _, path := range []string{"presentation.buildMenuPageSize", "presentation.sidebarOrders", "presentation.uiScale"} {
		if !slices.Contains(paths, path) {
			t.Fatalf("Sidebar paths omitted %s: %v", path, paths)
		}
	}
}

func TestNLSidebarShotChoicesCoverBothParts(t *testing.T) {
	_, s := settingsRegressionScreen(nil, settings.Defaults())
	steps := nlShotSteps(s, "sidebar")
	var build, orders []int
	for _, step := range steps {
		if step.draft == nil || step.gameSize.Y > 0 {
			continue
		}
		draft := s.draft
		step.draft(&draft)
		card := s.pages()[step.page].cards[step.card]
		switch step.part {
		case 0:
			build = append(build, card.parts[0].get(&draft))
		case 1:
			orders = append(orders, card.parts[1].get(&draft))
		}
	}
	if !slices.Equal(build, []int{0, 1, 2}) || !slices.Equal(orders, []int{0, 1}) {
		t.Fatalf("shot choices = build %v, orders %v", build, orders)
	}
}
