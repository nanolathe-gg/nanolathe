package main

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/platform/screenkit"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// These lock the Controls draft and pointer contract for the Modern host
// preference (DESIGN_GPU_RENDERER §16.6, DESIGN_INTERFACE_HUD_INPUT §3.17).
func zoomLockControl(t *testing.T, s *nlScreen) *nlCard {
	t.Helper()
	for _, c := range s.controlRows() {
		if c.key == "zoomlock" {
			return c
		}
	}
	t.Fatal("Zoom lock is missing from the Mouse controls")
	return nil
}

func TestNLScreenZoomLockDraftApplySaveCancel(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g, s := settingsRegressionScreen(nil, settings.Defaults())
	g.settingsWritable = true
	c := zoomLockControl(t, s)
	s.setCard(*c, 120-settings.ZoomLockMinPercent)
	if s.draft.pres.ZoomLockPercent != 120 || g.presentation.ZoomLockPercent != settings.ZoomLockDefaultPercent || s.dirty() != 1 {
		t.Fatalf("draft reached live settings or missed the change count: draft %d, live %d, dirty %d", s.draft.pres.ZoomLockPercent, g.presentation.ZoomLockPercent, s.dirty())
	}
	if c.steps[c.get(&s.draft)] != "1.20×" || !slices.Equal(s.cardPaths(*c, settings.Defaults()), []string{"presentation.zoomLockPercent"}) {
		t.Fatal("Zoom lock lost its factor label or settings path")
	}
	s.savePreset("Preferred zoom")
	stored, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Presentation.ZoomLockPercent != settings.ZoomLockDefaultPercent || len(stored.Presets) != 1 {
		t.Fatal("saving the draft as a preset changed the applied zoom")
	}
	preset, err := settings.Layer(settings.Defaults(), stored.Presets[0].Settings)
	if err != nil || preset.Presentation.ZoomLockPercent != 120 {
		t.Fatalf("saved preset lost draft zoom: %d, %v", preset.Presentation.ZoomLockPercent, err)
	}
	s.apply()
	stored, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if g.presentation.ZoomLockPercent != 120 || stored.Presentation.ZoomLockPercent != 120 || s.dirty() != 0 {
		t.Fatalf("Apply/save lost the choice: live %d, stored %d, dirty %d", g.presentation.ZoomLockPercent, stored.Presentation.ZoomLockPercent, s.dirty())
	}
	s.setCard(*c, 145-settings.ZoomLockMinPercent)
	s.hide()
	stored, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	restarted, reopened := settingsRegressionScreen(nil, stored)
	if g.presentation.ZoomLockPercent != 120 || restarted.presentation.ZoomLockPercent != 120 || reopened.draft.pres.ZoomLockPercent != 120 {
		t.Fatal("cancelled draft replaced the saved preference")
	}
	if zoomLockControl(t, s) != c {
		t.Fatal("editing rebuilt the cached controls catalogue")
	}
}

func TestNLScreenZoomStyleAndIconsApplyIndependently(t *testing.T) {
	for _, zoom := range []int{settings.ZoomNone, settings.ZoomStepped, settings.ZoomSmooth} {
		for _, icons := range []int{settings.StrategicIconsModern, settings.StrategicIconsCommunity} {
			t.Run(fmt.Sprintf("zoom%d-icons%d", zoom, icons), func(t *testing.T) {
				t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
				g, s := settingsRegressionScreen(nil, settings.Defaults())
				g.settingsWritable = true
				// Choose the icon preference while zoom is available. Turning
				// zoom off then preserves it for the next time zoom is enabled.
				s.setCard(configurationCard(t, s, "iconstyle"), icons)
				for _, c := range s.controlRows() {
					switch c.key {
					case "zoomstyle":
						if len(c.steps) != 3 || c.steps[settings.ZoomNone] != "No zoom" || c.steps[settings.ZoomSmooth] != "Continuous" {
							t.Fatal("missing camera zoom choices")
						}
						s.setCard(*c, zoom)
					case "iconstyle":
						if len(c.steps) != 2 {
							t.Fatal("missing zoomed-out icon choices")
						}
						s.setCard(*c, icons)
					}
				}
				s.apply()
				stored, err := settings.Load()
				if err != nil || stored.Presentation.ZoomStyle != zoom || stored.Presentation.StrategicIconStyle != icons {
					t.Fatalf("zoom and icons did not persist independently: %+v, %v", stored.Presentation, err)
				}
			})
		}
	}
}

func TestNLScreenCommunityCanRestoreCameraZoom(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	file := settings.Defaults()
	file.Gameplay = gameplay.Community39
	file.Presentation.Overview = settings.OverviewMegamap
	g, s := settingsRegressionScreen(nil, file)
	g.settingsWritable = true
	if reason := s.cardUnavailable(configurationCard(t, s, "zoomstyle")); reason != "" {
		t.Fatalf("megamap prevented selecting camera zoom: %s", reason)
	}
	s.setCard(configurationCard(t, s, "tab"), settings.OverviewZoom)
	for _, key := range []string{"zoomstyle", "zoomlock", "iconstyle"} {
		if reason := s.cardUnavailable(configurationCard(t, s, key)); reason != "" {
			t.Fatalf("Community %s stayed unavailable: %s", key, reason)
		}
	}
	s.setCard(configurationCard(t, s, "zoomstyle"), settings.ZoomStepped)
	s.setCard(configurationCard(t, s, "zoomlock"), 120-settings.ZoomLockMinPercent)
	s.apply()
	stored, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if g.gameplay != gameplay.Community39 || stored.Gameplay != gameplay.Community39 ||
		g.presentation.Overview != settings.OverviewZoom || stored.Presentation.Overview != settings.OverviewZoom ||
		g.presentation.ZoomStyle != settings.ZoomStepped || stored.Presentation.ZoomStyle != settings.ZoomStepped ||
		g.presentation.ZoomLockPercent != 120 || stored.Presentation.ZoomLockPercent != 120 {
		t.Fatalf("camera preference did not apply and persist independently: %+v", stored)
	}
}

func TestNLScreenZoomLockControlsPresetScope(t *testing.T) {
	path := "presentation.zoomLockPercent"
	if !slices.Contains(nlControlsPaths(), path) || slices.Contains(nlGraphicsPaths(), path) {
		t.Fatal("Zoom lock is outside the Controls preset scope")
	}
	patch := json.RawMessage(`{"gameplay":"strict-3.1","presentation":{"zoomLockPercent":120,"waterSurface":0}}`)
	for _, tc := range []struct {
		name   string
		scopes []bool
		want   int
	}{
		{"rules", []bool{true, false, false}, 130},
		{"graphics", []bool{false, true, false}, 130},
		{"controls", []bool{false, false, true}, 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := settings.Defaults()
			file.Presentation.ZoomLockPercent = 130
			g, s := settingsRegressionScreen(nil, file)
			s.applyPresetToDraft(nlPresetEntry{name: "Scoped zoom", patch: patch}, tc.scopes)
			if s.draft.pres.ZoomLockPercent != tc.want {
				t.Fatalf("%s preset changed zoom to %d, want %d", tc.name, s.draft.pres.ZoomLockPercent, tc.want)
			}
			s.apply()
			if g.presentation.ZoomLockPercent != tc.want {
				t.Fatalf("%s Apply changed zoom to %d, want %d", tc.name, g.presentation.ZoomLockPercent, tc.want)
			}
		})
	}
	for profile := 1; profile < len(nlControlsPresets); profile++ {
		t.Run(nlControlsPresets[profile].label, func(t *testing.T) {
			file := settings.Defaults()
			file.Presentation.ZoomLockPercent = 130
			g, s := settingsRegressionScreen(nil, file)
			s.chooseProfile(profile)
			s.apply()
			if g.presentation.ZoomLockPercent != 130 {
				t.Fatal("a controls profile replaced the preferred zoom")
			}
			s.chooseProfile(profile)
			s.setCard(*zoomLockControl(t, s), 120-settings.ZoomLockMinPercent)
			s.apply()
			if g.presentation.ZoomLockPercent != 120 {
				t.Fatal("a controls profile discarded an explicit zoom edit")
			}
		})
	}
}

func TestNLScreenZoomLockMousePointerHits(t *testing.T) {
	oldW, oldH := nlScreenW, nlScreenH
	t.Cleanup(func() { nlScreenW, nlScreenH = oldW, oldH })
	for _, size := range [][2]int{{640, 480}, {1560, 900}, {1920, 1080}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			nlScreenW, nlScreenH = float64(size[0]), float64(size[1])
			g, s := settingsRegressionScreen(nil, settings.Defaults())
			s.fonts, s.art = screenkit.LoadFonts(), &nlArt{}
			s.ctlGroup, s.ctlScroll = len(nlControlGroups)-1, len(s.controlRows())
			s.hits.Begin()
			s.drawControls(ebiten.NewImage(size[0], size[1]))
			s.hits.End()
			u := s.u()
			rowH := max(30, 58*u)
			zoomRow := -1
			for i, c := range s.controlRows() {
				if c.key == "zoomlock" {
					zoomRow = i - s.ctlScroll
				}
			}
			if zoomRow < 0 || s.ctlScroll == 0 {
				t.Fatal("scrolling did not reach Zoom lock")
			}
			r := screenkit.Rect{X: s.ctlTable.X, Y: s.ctlTable.Y + 8*u + float64(zoomRow)*rowH, W: s.ctlTable.W, H: rowH}
			minus, value, plus, reset, track := nlZoomLockRects(r, u)
			for _, hit := range []screenkit.Rect{minus, value, plus, reset, track} {
				if hit.X < r.X || hit.X+hit.W > r.X+r.W || hit.Y < r.Y || hit.Y+hit.H > r.Y+r.H || hit.Y+hit.H > s.ctlTable.Y+s.ctlTable.H {
					t.Fatalf("Zoom lock control is clipped at %dx%d: %+v", size[0], size[1], hit)
				}
			}
			if plus.X+plus.W >= reset.X || value.Y+value.H >= track.Y {
				t.Fatal("Zoom lock hit targets overlap")
			}
			click := func(id string, hit screenkit.Rect) {
				t.Helper()
				x, y := math.Round(hit.X+hit.W/2), math.Round(hit.Y+hit.H/2)
				s.hits.Update(screenkit.Input{X: x, Y: y, Pressed: true}, 0)
				if s.hits.Hot() != id {
					t.Fatalf("pointer hit %q, want %q", s.hits.Hot(), id)
				}
				s.hits.Update(screenkit.Input{X: x, Y: y, Released: true}, 0)
			}
			click("ctl-zoomlock-plus", plus)
			if s.draft.pres.ZoomLockPercent != 101 {
				t.Fatal("plus did not change the draft by one percent")
			}
			click("ctl-zoomlock-minus", minus)
			if s.draft.pres.ZoomLockPercent != 100 {
				t.Fatal("minus did not change the draft by one percent")
			}
			y := math.Round(track.Y + track.H/2)
			x := math.Round(track.X + track.W*119/199)
			s.hits.Update(screenkit.Input{X: x, Y: y, Pressed: true}, 0)
			if s.hits.Hot() != "ctl-zoomlock-track" || s.draft.pres.ZoomLockPercent != 120 {
				t.Fatal("track did not select 1.20×")
			}
			s.hits.Update(screenkit.Input{X: track.X + track.W + 20, Y: y, Down: true}, 0)
			if s.draft.pres.ZoomLockPercent != settings.ZoomLockMaxPercent {
				t.Fatal("drag outside the track did not clamp to 2×")
			}
			s.hits.Update(screenkit.Input{X: track.X - 20, Y: y, Down: true}, 0)
			s.hits.Update(screenkit.Input{Released: true}, 0)
			if s.draft.pres.ZoomLockPercent != settings.ZoomLockMinPercent {
				t.Fatal("drag outside the track did not clamp to its minimum")
			}
			click("ctl-zoomlock-minus", minus)
			if s.draft.pres.ZoomLockPercent != settings.ZoomLockMinPercent {
				t.Fatal("minus wrapped at the minimum")
			}
			click("ctl-zoomlock-reset", reset)
			if s.draft.pres.ZoomLockPercent != settings.ZoomLockDefaultPercent || g.presentation.ZoomLockPercent != settings.ZoomLockDefaultPercent {
				t.Fatal("reset lost 1.00× or a pointer edit reached live settings")
			}
		})
	}
}
