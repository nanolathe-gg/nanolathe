package main

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

var builderOptionNames = [2][3]string{{"BGHOLD", "BGMAN", "BGROAM"}, {"BPHOLD", "BPMAN", "BPROAM"}}

// Builder preferences use the existing options transaction: live command,
// Undo/Cancel restoration, then persistence on OK (DESIGN_COMMUNITY_PATCH D5).
func (g *gameShell) setBuilderOptions(options settings.BuilderOptions) {
	options.Normalize()
	if options == g.builderOptions {
		return
	}
	if g.battle != nil && !g.battle.ended && g.battle.sess != nil {
		command := session.HumanCommand{Kind: session.HumanBuilderOptions, BuilderOptions: session.HumanBuilderOptionsCommand{
			Owner: g.battle.sess.LocalOwner, Options: *sessionBuilderOptions(options),
		}}
		if err := g.battle.sess.EnqueueHumanCommand(command); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
	}
	g.builderOptions = options
}

func builderOptionsPage(window *gui.Window) error {
	index := window.GadgetIndex("SHADING")
	if index < 0 {
		return fmt.Errorf("missing SHADING control template")
	}
	button := window.Gadgets[index]
	var label gui.Gadget
	found := false
	for _, gad := range window.Gadgets[1:] {
		if gad.Kind == gui.KindLabel && (!found || gad.Rect.Y < label.Rect.Y) {
			label, found = gad, true
		}
	}
	if !found {
		return fmt.Errorf("missing label template")
	}
	kept := []gui.Gadget{window.Gadgets[0]}
	for _, gad := range window.Gadgets[1:] {
		if gad.Name == "RESTORE" || gad.Name == "UNDO" {
			kept = append(kept, gad)
		}
	}
	button.Art, button.ArtFrame, button.Attribs, button.QuickKey, button.Status = "", 0, 1, 0, 0
	label.Link = ""
	label.Rect.X, label.Rect.W = button.Rect.X, button.Rect.W
	y := label.Rect.Y
	// One pixel between buttons leaves room for every host control above
	// Restore Defaults in the shorter in-battle column.
	pitch := button.Rect.H + 1
	stances := [3]string{"Hold", "Man.", "Roam"}
	for group, title := range [2]string{"Guard home", "Patrol work"} {
		caption := label
		caption.Name, caption.SourceName, caption.Text = title, title, title
		caption.Rect.Y = y
		kept = append(kept, caption)
		y += caption.Rect.H
		for i, name := range builderOptionNames[group] {
			control := button
			control.Name, control.SourceName, control.Stages = name, name, 3
			labels := [3]string{"Stay", "Cavedog", "Scatter"}
			if group == 1 {
				labels = [3]string{"Reclaim", "Both", "Assist"}
			}
			control.Text = fmt.Sprintf("%s: %s|%s: %s|%s: %s", stances[i], labels[0], stances[i], labels[1], stances[i], labels[2])
			control.Rect.Y = y
			kept = append(kept, control)
			y += pitch
		}
	}
	for _, row := range []struct {
		name, text string
		stages     uint8
	}{
		{"NCYCLE", "Select: Retail|Select: Comm.|Select: Zero", 3},
		{"NDOUBLE", "2-click: Off|2-click: On", 2},
		{"NHUNDRED", "100 batch: Off|100 batch: On", 2},
		// The persisted SwitchAlt mux [07 R-CAM-01 §4]: plain digits pick
		// build pages (retail's default) or recall groups. `+switchalt`
		// changes the same value from the message line.
		{"NSWITCHALT", "Digits: Pages|Digits: Groups", 2},
		// The overview Tab and the wheel open: today's smooth zoom, or the
		// optional megamap (DESIGN_INTERFACE_HUD_INPUT §3.15).
		{"NOVERVIEW", "Tab: Options|Tab: Megamap", 2},
	} {
		control := button
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, row.stages
		control.Rect.Y = y
		kept = append(kept, control)
		y += pitch
	}
	window.Gadgets = kept
	return nil
}

func (g *gameShell) syncBuilderOptions() {
	if optionsState == nil || optionsState.page != "builders" || optionsPanel == nil {
		return
	}
	optionsPanel.SetStageAt(optionsPanel.Index("NCYCLE"), g.presentation.CommunitySelection)
	optionsPanel.SetStageAt(optionsPanel.Index("NDOUBLE"), g.presentation.DoubleClickSelection)
	optionsPanel.SetStageAt(optionsPanel.Index("NHUNDRED"), g.presentation.FactoryHundredBatch)
	optionsPanel.SetStageAt(optionsPanel.Index("NSWITCHALT"), boolInt(g.switchAlt))
	optionsPanel.SetStageAt(optionsPanel.Index("NOVERVIEW"), boolInt(g.presentation.Overview == settings.OverviewMegamap))
	for group, names := range builderOptionNames {
		values := g.builderOptions.Guard
		if group == 1 {
			values = g.builderOptions.Patrol
		}
		for i, name := range names {
			optionsPanel.SetStageAt(optionsPanel.Index(name), values[i])
		}
	}
}

func (g *gameShell) activateBuilderOption(name string) bool {
	if name == "NSWITCHALT" {
		g.setSwitchAlt(g.retailOptionsStage(name, 2, boolInt(g.switchAlt)) != 0)
		g.syncBuilderOptions()
		return true
	}
	if name == "NOVERVIEW" {
		p := g.presentation
		p.Overview = g.retailOptionsStage(name, 2, boolInt(p.Overview == settings.OverviewMegamap))
		g.setPresentation(p)
		g.syncBuilderOptions()
		return true
	}
	if name == "NCYCLE" || name == "NDOUBLE" || name == "NHUNDRED" {
		p := g.presentation
		value := &p.CommunitySelection
		stages := 3
		switch name {
		case "NDOUBLE":
			value = &p.DoubleClickSelection
			stages = 2
		case "NHUNDRED":
			value = &p.FactoryHundredBatch
			stages = 2
		}
		*value = g.retailOptionsStage(name, stages, *value)
		g.setPresentation(p)
		g.syncBuilderOptions()
		return true
	}
	for group, names := range builderOptionNames {
		for i, candidate := range names {
			if name != candidate {
				continue
			}
			next := g.builderOptions
			value := &next.Guard[i]
			if group == 1 {
				value = &next.Patrol[i]
			}
			*value = g.retailOptionsStage(name, 3, *value)
			g.setBuilderOptions(next)
			g.syncBuilderOptions()
			return true
		}
	}
	return false
}

func (g *gameShell) setSelectionPreferences(p settings.Presentation) {
	next := g.presentation
	next.CommunitySelection, next.DoubleClickSelection = p.CommunitySelection, p.DoubleClickSelection
	next.FactoryHundredBatch = p.FactoryHundredBatch
	next.Overview = p.Overview
	g.setPresentation(next)
}

// setSwitchAlt changes the digit-key mux the options page shows. A running
// battle captured the bit at entry, so it takes the new value too, as
// `+switchalt` does [07 R-CAM-01 §4]; the options transaction persists it on
// OK and takes it back on Undo and Cancel.
func (g *gameShell) setSwitchAlt(on bool) {
	g.switchAlt = on
	if g.battle != nil {
		g.battle.switchAlt = on
	}
}
