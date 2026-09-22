package main

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

type communityHUDSwitch struct {
	name, label string
	value       *int
}

func communityHUDSwitches(p *settings.Presentation) []communityHUDSwitch {
	return []communityHUDSwitch{
		{"NCOUNTERS", "Counters", &p.CommunityCounters},
		{"NRELOAD", "Reload bars", &p.ReloadBars},
		{"NVETERAN", "Veterancy", &p.VeteranLabels},
		{"NGROUPS", "Group digits", &p.GroupNumbers},
		{"NALLIES", "Allied bars", &p.AlliedResources},
		{"NWEATHER", "Weather", &p.WeatherReport},
	}
}

func communityHUDOptionsPage(window *gui.Window) error {
	index := window.GadgetIndex("SHADING")
	if index < 0 {
		return fmt.Errorf("missing SHADING control template")
	}
	button := window.Gadgets[index]
	kept := []gui.Gadget{window.Gadgets[0]}
	for _, g := range window.Gadgets[1:] {
		if g.Name == "RESTORE" || g.Name == "UNDO" {
			kept = append(kept, g)
		}
	}
	button.Art, button.ArtFrame, button.Attribs, button.QuickKey, button.Status = "", 0, 1, 0, 0
	p := settings.DefaultPresentation()
	for i, row := range communityHUDSwitches(&p) {
		control := button
		control.Name, control.SourceName, control.Stages = row.name, row.name, 2
		control.Text = row.label + ": Off|" + row.label + ": On"
		control.Rect.Y = button.Rect.Y + int32(i)*26
		kept = append(kept, control)
	}
	window.Gadgets = kept
	return nil
}

func (g *gameShell) syncCommunityHUDOptions() {
	if optionsState == nil || optionsState.page != "communityhud" || optionsPanel == nil {
		return
	}
	p := g.presentation
	for _, row := range communityHUDSwitches(&p) {
		optionsPanel.SetStageAt(optionsPanel.Index(row.name), *row.value)
	}
}
func (g *gameShell) activateCommunityHUDOption(name string) bool {
	p := g.presentation
	for _, row := range communityHUDSwitches(&p) {
		if row.name == name {
			*row.value = g.retailOptionsStage(name, 2, *row.value)
			g.setPresentation(p)
			g.syncCommunityHUDOptions()
			return true
		}
	}
	return false
}
func (g *gameShell) setCommunityHUDPreferences(p settings.Presentation) {
	next := g.presentation
	from, to := communityHUDSwitches(&p), communityHUDSwitches(&next)
	for i := range to {
		*to[i].value = *from[i].value
	}
	g.setPresentation(next)
}
func applyCommunityHUDOptions(cl *client.Client, p settings.Presentation) {
	if cl == nil {
		return
	}
	cl.SetCommunityColorOptions(client.CommunityColorOptions{TeamColorNanolathe: p.TeamColorNanolathe != 0, PlayerStreamColors: p.PlayerStreamColors, PlayerFrameColors: p.PlayerFrameColors})
	cl.SetCommunityHUDOptions(client.CommunityHUDOptions{Counters: p.CommunityCounters != 0, ReloadBars: p.ReloadBars != 0, VeteranLabel: p.VeteranLabels != 0, DisableGroupNumbers: p.GroupNumbers == 0})
}
func (b *battleSession) hostPreferences() settings.Presentation {
	if b != nil && b.shell != nil {
		return b.shell.presentation
	}
	if b != nil && b.hostPresentation != nil {
		return *b.hostPresentation
	}
	return settings.DefaultPresentation()
}
