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
	stances := [3]string{"Hold", "Man.", "Roam"}
	for group, title := range [2]string{"Guard home", "Patrol work"} {
		caption := label
		caption.Name, caption.SourceName, caption.Text = title, title, title
		caption.Rect.Y = y
		kept = append(kept, caption)
		y += caption.Rect.H + 4
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
			y += 24
		}
		y += 12
	}
	for _, row := range []struct{ name, text string }{
		{"NCYCLE", "Idle keys: Off|Idle keys: On"},
		{"NDOUBLE", "2-click: Off|2-click: On"},
	} {
		control := button
		control.Name, control.SourceName, control.Text, control.Stages = row.name, row.name, row.text, 2
		control.Rect.Y = y
		kept = append(kept, control)
		y += 22
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
	if name == "NCYCLE" || name == "NDOUBLE" {
		p := g.presentation
		value := &p.CommunitySelection
		if name == "NDOUBLE" {
			value = &p.DoubleClickSelection
		}
		*value = g.retailOptionsStage(name, 2, *value)
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
	g.setPresentation(next)
}
