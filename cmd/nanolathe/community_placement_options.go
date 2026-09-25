package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

const communityPlacementRadiusLimit = 9

type communityPlacementRadiusSpec struct {
	maximum       int
	defaultRadius int
	inBattle      bool
}

// communityPlacementOptionsPage maps the extension's host settings onto the
// existing VISUALS button geometry. Radius stages are bounded by the running
// feature table in battle; outside battle all persisted values 0..9 remain
// available and are resolved against the table when a battle starts.
func (g *gameShell) communityPlacementOptionsPage(window *gui.Window) error {
	index := window.GadgetIndex("SHADING")
	if index < 0 {
		return fmt.Errorf("missing SHADING control template")
	}
	button := window.Gadgets[index]
	kept := []gui.Gadget{window.Gadgets[0]}
	for _, gad := range window.Gadgets[1:] {
		if gad.Name == "RESTORE" || gad.Name == "UNDO" {
			kept = append(kept, gad)
		}
	}
	button.Art, button.ArtFrame, button.Attribs, button.QuickKey, button.Status = "", 0, 1, 0, 0
	rows := []struct {
		name   string
		text   string
		stages int
	}{
		{"NPREVIEW", "Preview: Pulse|Preview: Full|Preview: Wire|Preview: Off", 4},
		{"NROVERLAY", "Rotate art: Off|Rotate art: On", 2},
		{"NORDERDRAG", "Order drag: Off|Order drag: On", 2},
		{"NTEAMNANO", "Team nano: Off|Team nano: On", 2},
	}
	for _, radius := range []struct {
		name, label string
		spec        communityPlacementRadiusSpec
	}{
		{"NMEXSNAP", "Mex", g.communityPlacementRadiusSpec(true)},
		{"NWRECKSNAP", "Wreck", g.communityPlacementRadiusSpec(false)},
	} {
		rows = append(rows, struct {
			name   string
			text   string
			stages int
		}{radius.name, communityPlacementRadiusText(radius.label, radius.spec), radius.spec.maximum + 2})
	}
	rows = append(rows, struct {
		name   string
		text   string
		stages int
	}{"NSNAPMOD", "Override: Alt|Override: Ctrl|Override: Shift", 3})

	const pitch = int32(26)
	for i, row := range rows {
		control := button
		control.Name, control.SourceName, control.Text = row.name, row.name, row.text
		control.Stages = uint8(row.stages)
		control.Rect.Y = button.Rect.Y + int32(i)*pitch
		kept = append(kept, control)
	}
	window.Gadgets = kept
	return nil
}

func (g *gameShell) communityPlacementRadiusSpec(mex bool) communityPlacementRadiusSpec {
	spec := communityPlacementRadiusSpec{maximum: communityPlacementRadiusLimit}
	if optionsState == nil || !optionsState.inBattle {
		return spec
	}
	spec.inBattle = true
	b := g.battleOptionsSession()
	if b == nil || b.sess == nil {
		spec.maximum = 0
		return spec
	}
	features := b.sess.Community
	enabled := features.WreckSnap
	tableDefault, tableMaximum := features.WreckSnapRadius, features.WreckSnapRadiusMax
	if mex {
		enabled = features.MexSnap
		tableDefault, tableMaximum = features.MexSnapRadius, features.MexSnapRadiusMax
	}
	if !enabled {
		tableMaximum = 0
	}
	if tableMaximum < 0 {
		tableMaximum = 0
	}
	if tableMaximum > communityPlacementRadiusLimit {
		tableMaximum = communityPlacementRadiusLimit
	}
	spec.maximum = tableMaximum
	spec.defaultRadius = int(effectiveClickSnapRadius(-1, tableDefault, tableMaximum, enabled))
	return spec
}

func communityPlacementRadiusText(label string, spec communityPlacementRadiusSpec) string {
	auto := label + ": Auto"
	if spec.inBattle {
		auto = fmt.Sprintf("%s: Auto %d", label, spec.defaultRadius)
	}
	parts := []string{auto, label + ": Off"}
	for radius := 1; radius <= spec.maximum; radius++ {
		parts = append(parts, fmt.Sprintf("%s: %d", label, radius))
	}
	return strings.Join(parts, "|")
}

func communityPlacementRadiusStage(value int, spec communityPlacementRadiusSpec) int {
	if value < 0 || spec.inBattle && value > spec.maximum {
		return 0
	}
	if value > spec.maximum {
		value = spec.maximum
	}
	return value + 1
}

func communityPlacementRadiusValue(stage int) int {
	if stage <= 0 {
		return -1
	}
	return stage - 1
}

func communityPlacementModifierStage(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ctrl", "control":
		return 1
	case "shift":
		return 2
	default:
		return 0
	}
}

var communityPlacementModifierValues = [...]string{"alt", "ctrl", "shift"}

func (g *gameShell) syncCommunityPlacementOptions() {
	if optionsState == nil || optionsState.page != "placement" || optionsPanel == nil {
		return
	}
	p := g.presentation
	optionsPanel.SetStageAt(optionsPanel.Index("NPREVIEW"), p.NanoframePreview)
	optionsPanel.SetStageAt(optionsPanel.Index("NROVERLAY"), boolInt(p.BuildRotationOverlay != 0))
	optionsPanel.SetStageAt(optionsPanel.Index("NORDERDRAG"), boolInt(p.QueuedOrderDrag != 0))
	optionsPanel.SetStageAt(optionsPanel.Index("NTEAMNANO"), boolInt(p.TeamColorNanolathe != 0))
	for _, radius := range []struct {
		name  string
		value int
		spec  communityPlacementRadiusSpec
	}{
		{"NMEXSNAP", p.MexSnapRadius, g.communityPlacementRadiusSpec(true)},
		{"NWRECKSNAP", p.WreckSnapRadius, g.communityPlacementRadiusSpec(false)},
	} {
		optionsPanel.SetStageAt(optionsPanel.Index(radius.name), communityPlacementRadiusStage(radius.value, radius.spec))
		retailGreyGadget(optionsAssets.window, radius.name, radius.spec.inBattle && radius.spec.maximum == 0)
	}
	optionsPanel.SetStageAt(optionsPanel.Index("NSNAPMOD"), communityPlacementModifierStage(p.ClickSnapOverrideKey))
}

func (g *gameShell) activateCommunityPlacementOption(name string) bool {
	p := g.presentation
	switch name {
	case "NPREVIEW":
		p.NanoframePreview = g.retailOptionsStage(name, 4, p.NanoframePreview)
	case "NROVERLAY":
		p.BuildRotationOverlay = g.retailOptionsStage(name, 2, boolInt(p.BuildRotationOverlay != 0))
	case "NORDERDRAG":
		p.QueuedOrderDrag = g.retailOptionsStage(name, 2, boolInt(p.QueuedOrderDrag != 0))
	case "NTEAMNANO":
		p.TeamColorNanolathe = g.retailOptionsStage(name, 2, boolInt(p.TeamColorNanolathe != 0))
	case "NMEXSNAP", "NWRECKSNAP":
		spec := g.communityPlacementRadiusSpec(name == "NMEXSNAP")
		if spec.inBattle && spec.maximum == 0 {
			return true
		}
		current := p.WreckSnapRadius
		if name == "NMEXSNAP" {
			current = p.MexSnapRadius
		}
		stage := g.retailOptionsStage(name, spec.maximum+2, communityPlacementRadiusStage(current, spec))
		if name == "NMEXSNAP" {
			p.MexSnapRadius = communityPlacementRadiusValue(stage)
		} else {
			p.WreckSnapRadius = communityPlacementRadiusValue(stage)
		}
	case "NSNAPMOD":
		stage := g.retailOptionsStage(name, len(communityPlacementModifierValues), communityPlacementModifierStage(p.ClickSnapOverrideKey))
		p.ClickSnapOverrideKey = communityPlacementModifierValues[stage]
	default:
		return false
	}
	g.setPresentation(p)
	g.syncCommunityPlacementOptions()
	return true
}

// setCommunityPlacementPreferences restores only fields exposed by this page.
// BuildRotateKey stays hand-editable in settings and is outside the page's
// transaction because the compact host UI has no text editor for it.
func (g *gameShell) setCommunityPlacementPreferences(p settings.Presentation) {
	next := g.presentation
	next.MexSnapRadius = p.MexSnapRadius
	next.WreckSnapRadius = p.WreckSnapRadius
	next.ClickSnapOverrideKey = p.ClickSnapOverrideKey
	next.BuildRotationOverlay = p.BuildRotationOverlay
	next.NanoframePreview = p.NanoframePreview
	next.QueuedOrderDrag = p.QueuedOrderDrag
	next.TeamColorNanolathe = p.TeamColorNanolathe
	g.setPresentation(next)
}
