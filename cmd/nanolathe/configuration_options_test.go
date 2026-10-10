package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Availability follows the existing host consumers, including the independent
// Strict megamap and Classic's still-usable No zoom choice (interface §3.15).
func TestConfigurationKeepsIndependentHostControls(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		for _, renderer := range []string{"classic", "modern"} {
			p := settings.DefaultPresentation()
			p.Renderer = renderer
			for _, key := range []string{"interface", "selection", "dblclick", "batch", "digits", "orderdrag"} {
				if reason := configurationUnavailable(key, mode, p); reason != "" {
					t.Fatalf("%s/%s disabled %s: %s", mode, renderer, key, reason)
				}
			}
			for _, key := range []string{"builddrag", "sidebar"} {
				if got := configurationUnavailable(key, mode, p) == ""; got != (renderer == "modern") {
					t.Fatalf("%s/%s %s available=%v", mode, renderer, key, got)
				}
			}
			if got := configurationUnavailable("zoomstyle", mode, p) == ""; got != (mode != gameplay.Strict31) {
				t.Fatalf("%s/%s zoom style available=%v", mode, renderer, got)
			}
			if got := configurationUnavailable("radardots", mode, p) == ""; got != (mode == gameplay.Modern && renderer == "modern") {
				t.Fatalf("%s/%s radar dots available=%v", mode, renderer, got)
			}
			p.ZoomStyle = settings.ZoomNone
			if got := configurationUnavailable("tab", mode, p) == ""; got != (mode != gameplay.Modern) {
				t.Fatalf("%s/%s no-zoom Tab choice available=%v", mode, renderer, got)
			}
			for _, key := range []string{"zoomlock", "iconstyle"} {
				if configurationUnavailable(key, mode, p) == "" {
					t.Fatalf("%s/%s no-zoom %s still available", mode, renderer, key)
				}
			}
		}
	}
	p := settings.DefaultPresentation()
	if configurationUnavailable("snapkey", gameplay.Strict31, p) == "" {
		t.Fatal("Strict offered a snap modifier with none of its consumers enabled")
	}
	p.QueuedOrderDrag = 1
	if reason := configurationUnavailable("snapkey", gameplay.Strict31, p); reason != "" {
		t.Fatalf("Strict's queued-drag cancel modifier was disabled: %s", reason)
	}
}

func TestConfigurationOptionsRejectInactiveWritesAndRecover(t *testing.T) {
	g, panel, cl := syntheticOptionsPage(t, "nanolathe", nanolatheOptionsPage)
	g.gameplay, g.presentation.Renderer = gameplay.Strict31, "classic"
	g.syncNanolatheOptions()
	for _, name := range []string{"NFPS", "NSIDEBAR", "NZOOM", "NICONS", "NRADARDOTS", "NGLOW", "NWATER", "NLIGHTS", "NFINISH", "NHEAT", "NMARKS"} {
		before, glow := g.presentation, g.display.Glow
		if panel.Window.Gadgets[panel.Index(name)].GrayedOut&1 == 0 {
			t.Fatalf("inactive %s is not greyed", name)
		}
		clickRowGadget(t, g, panel, cl, name, input.MouseButtonLeft)
		g.activateNanolatheOption(name) // direct callbacks have the same boundary
		if g.presentation != before || g.display.Glow != glow {
			t.Fatalf("inactive %s changed its stored preference", name)
		}
	}
	g.gameplay = gameplay.Modern
	g.syncNanolatheOptions()
	if panel.Window.Gadgets[panel.Index("NZOOM")].GrayedOut != 0 {
		t.Fatal("Classic lost its usable No zoom control")
	}
	for _, want := range []int{settings.ZoomNone, settings.ZoomSmooth} {
		clickRowGadget(t, g, panel, cl, "NZOOM", input.MouseButtonLeft)
		if g.presentation.ZoomStyle != want {
			t.Fatalf("Classic zoom=%d, want %d; Steps must be skipped", g.presentation.ZoomStyle, want)
		}
	}
	g.presentation.ZoomStyle = settings.ZoomStepped
	g.syncNanolatheOptions()
	if g.presentation.ZoomStyle != settings.ZoomStepped || panel.Window.Gadgets[panel.Index("NZOOM")].Labels[settings.ZoomStepped] != "Zoom: Classic" {
		t.Fatal("Classic changed the stored Steps preference or mislabelled its native behavior")
	}
	g.presentation.Renderer = "modern"
	g.syncNanolatheOptions()
	for _, name := range []string{"NFPS", "NSIDEBAR", "NZOOM", "NICONS", "NRADARDOTS", "NGLOW", "NWATER"} {
		if panel.Window.Gadgets[panel.Index(name)].GrayedOut != 0 {
			t.Fatalf("%s stayed greyed after selecting its renderer and mode", name)
		}
	}
	clickRowGadget(t, g, panel, cl, "NRADARDOTS", input.MouseButtonLeft)
	if g.presentation.RadarDots != settings.RadarDotsAttackable {
		t.Fatal("reactivated radar control did not commit its click")
	}
}

func TestConfigurationOptionsFollowCommittedBattleRules(t *testing.T) {
	g, panel, cl := syntheticOptionsPage(t, "nanolathe", nanolatheOptionsPage)
	g.gameplay = gameplay.Modern
	sess := &session.Session{}
	sess.SetGameplay(gameplay.Strict31)
	b := &battleSession{shell: g, sess: sess}
	g.battle = b
	optionsState.inBattle = true
	g.syncNanolatheOptions()
	if panel.Window.Gadgets[panel.Index("NZOOM")].GrayedOut == 0 {
		t.Fatal("the shell's Modern selection overrode the battle's Strict controls")
	}
	sess.SetGameplay(gameplay.Modern)
	b.serviceBattleOptionsWidgets(panel, cl.Input())
	if panel.Window.Gadgets[panel.Index("NZOOM")].GrayedOut != 0 {
		t.Fatal("a committed gameplay change left controls greyed until the page reopened")
	}
}

func TestConfigurationBuilderFeatureGatesKeepSelectionUsable(t *testing.T) {
	g, panel, cl := syntheticOptionsPage(t, "builders", builderOptionsPage)
	g.gameplay = gameplay.Strict31
	g.builderOptions = settings.DefaultBuilderOptions()
	g.syncBuilderOptions()
	for _, names := range builderOptionNames {
		for _, name := range names {
			before := g.builderOptions
			if panel.Window.Gadgets[panel.Index(name)].GrayedOut == 0 {
				t.Fatalf("Strict left %s active", name)
			}
			g.activateBuilderOption(name)
			if g.builderOptions != before {
				t.Fatalf("Strict changed the unused %s preference", name)
			}
		}
	}
	for _, name := range []string{"NCYCLE", "NDOUBLE", "NHUNDRED", "NSWITCHALT", "NOVERVIEW"} {
		if panel.Window.Gadgets[panel.Index(name)].GrayedOut != 0 {
			t.Fatalf("Strict disabled the host control %s", name)
		}
	}
	clickRowGadget(t, g, panel, cl, "NCYCLE", input.MouseButtonLeft)
	if g.presentation.CommunitySelection != 1 {
		t.Fatal("Strict could not select Community host selection")
	}
	g.gameplay = gameplay.Community39
	guardOff, patrolOn := false, true
	g.gameplayFeatures = community.Overrides{GuardingBuildersHold: &guardOff, PatrollingBuilderFilters: &patrolOn}
	g.syncBuilderOptions()
	if panel.Window.Gadgets[panel.Index("BGHOLD")].GrayedOut == 0 || panel.Window.Gadgets[panel.Index("BPHOLD")].GrayedOut != 0 || panel.Window.Gadgets[panel.Index("NOVERVIEW")].GrayedOut != 0 {
		t.Fatal("Community ignored per-feature builder gates or disabled the host overview choice")
	}
	g.gameplay = gameplay.Modern
	patrolOn = false
	g.syncBuilderOptions()
	if panel.Window.Gadgets[panel.Index("BPHOLD")].GrayedOut != 0 {
		t.Fatal("Modern hid its patrol preference behind the Community feature flag")
	}
}

func TestCommunityConfigurationCanLeaveMegamapForCameraZoom(t *testing.T) {
	g, panel, cl := syntheticOptionsPage(t, "builders", builderOptionsPage)
	g.gameplay = gameplay.Community39
	g.presentation.Overview = settings.OverviewMegamap
	g.syncBuilderOptions()
	if reason := configurationUnavailable("zoomstyle", g.gameplay, g.presentation); reason != "" {
		t.Fatalf("megamap prevented selecting camera zoom: %s", reason)
	}
	clickRowGadget(t, g, panel, cl, "NOVERVIEW", input.MouseButtonLeft)
	if g.presentation.Overview != settings.OverviewZoom || g.gameplay != gameplay.Community39 {
		t.Fatal("overview change missed the host preference or changed gameplay")
	}
	for _, key := range []string{"zoomstyle", "zoomlock", "iconstyle"} {
		if reason := configurationUnavailable(key, g.gameplay, g.presentation); reason != "" {
			t.Fatalf("Community camera %s remained unavailable: %s", key, reason)
		}
	}
}

func TestConfigurationPlacementKeepsAllModePresentation(t *testing.T) {
	g, panel, cl := syntheticOptionsPage(t, "placement", func(w *gui.Window) error {
		return (&gameShell{}).communityPlacementOptionsPage(w)
	})
	g.gameplay = gameplay.Strict31
	g.syncCommunityPlacementOptions()
	for _, name := range []string{"NROVERLAY", "NMEXSNAP", "NWRECKSNAP", "NSNAPMOD"} {
		before := g.presentation
		if panel.Window.Gadgets[panel.Index(name)].GrayedOut == 0 {
			t.Fatalf("Strict left %s active", name)
		}
		g.activateCommunityPlacementOption(name)
		if g.presentation != before {
			t.Fatalf("Strict changed inactive %s", name)
		}
	}
	for _, name := range []string{"NPREVIEW", "NORDERDRAG", "NTEAMNANO"} {
		if panel.Window.Gadgets[panel.Index(name)].GrayedOut != 0 {
			t.Fatalf("Strict disabled host presentation %s", name)
		}
	}
	clickRowGadget(t, g, panel, cl, "NORDERDRAG", input.MouseButtonLeft)
	if panel.Window.Gadgets[panel.Index("NSNAPMOD")].GrayedOut != 0 {
		t.Fatal("queued dragging did not enable its override/cancel modifier")
	}
	clickRowGadget(t, g, panel, cl, "NSNAPMOD", input.MouseButtonLeft)
	if g.presentation.ClickSnapOverrideKey != "ctrl" {
		t.Fatal("Strict's enabled queued-drag modifier did not change")
	}
}

func TestConfigurationHUDHealthBarDependency(t *testing.T) {
	previous := client.DamageBars()
	client.SetDamageBars(false)
	t.Cleanup(func() { client.SetDamageBars(previous) })
	g, panel, cl := syntheticOptionsPage(t, "communityhud", communityHUDOptionsPage)
	g.syncCommunityHUDOptions()
	for _, name := range []string{"NCOUNTERS", "NRELOAD"} {
		before := g.presentation
		if panel.Window.Gadgets[panel.Index(name)].GrayedOut == 0 {
			t.Fatalf("%s remained active without health bars", name)
		}
		if got := panel.HelpOf(name); got != "Enable Health bars first." {
			t.Fatalf("inactive %s help = %q", name, got)
		}
		g.activateCommunityHUDOption(name)
		if before != g.presentation {
			t.Fatalf("inactive %s changed its preference", name)
		}
	}
	clickRowGadget(t, g, panel, cl, "NHEALTH", input.MouseButtonLeft)
	clickRowGadget(t, g, panel, cl, "NCOUNTERS", input.MouseButtonLeft)
	if g.presentation.CommunityCounters != 1 || panel.Window.Gadgets[panel.Index("NRELOAD")].GrayedOut != 0 {
		t.Fatal("enabling health bars did not restore the dependent controls")
	}
	if got := panel.HelpOf("NRELOAD"); got == "" || got == "Enable Health bars first." {
		t.Fatalf("reenabled reload help = %q", got)
	}
	clickRowGadget(t, g, panel, cl, "NHEALTH", input.MouseButtonLeft)
	if g.presentation.CommunityCounters != 1 {
		t.Fatal("disabling a prerequisite erased the saved dependent preference")
	}
}
