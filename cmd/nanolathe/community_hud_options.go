package main

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

type communityHUDSwitch struct {
	name, label, help string
	value             *int
}

func communityHUDSwitches(p *settings.Presentation) []communityHUDSwitch {
	return []communityHUDSwitch{
		{"NCOUNTERS", "Counters", "Stockpile/cargo", &p.CommunityCounters},
		{"NRELOAD", "Reload bars", "Tagged + HP bars", &p.ReloadBars},
		{"NVETERAN", "Veterancy", "Hover veteran", &p.VeteranLabels},
		{"NGROUPS", "Group digits", "Assigned groups", &p.GroupNumbers},
		{"NALLIES", "Allied bars", "Active allies", &p.AlliedResources},
		{"NWEATHER", "Weather", "Wind/tide in game", &p.WeatherReport},
	}
}

func communityHUDOptionsPage(window *gui.Window) error {
	index := window.GadgetIndex("SHADING")
	if index < 0 {
		return fmt.Errorf("missing SHADING control template")
	}
	button := window.Gadgets[index]
	var help gui.Gadget
	foundHelpLabel := false
	kept := []gui.Gadget{window.Gadgets[0]}
	for _, g := range window.Gadgets[1:] {
		if g.Name == "RESTORE" || g.Name == "UNDO" {
			kept = append(kept, g)
		}
		if g.Kind == gui.KindLabel && (!foundHelpLabel || g.Rect.Y < help.Rect.Y) {
			help, foundHelpLabel = g, true
		}
	}
	if !foundHelpLabel {
		return fmt.Errorf("missing label template")
	}
	button.Art, button.ArtFrame, button.Attribs, button.QuickKey, button.Status = "", 0, 1, 0, 0
	p := settings.DefaultPresentation()
	rows := []communityHUDSwitch{{name: "NHEALTH", label: "Health bars", help: "Counters + reload"}}
	rows = append(rows, communityHUDSwitches(&p)...)
	for i, row := range rows {
		control := button
		control.Name, control.SourceName, control.Stages = row.name, row.name, 2
		control.Text = row.label + ": Off|" + row.label + ": On"
		control.Help = row.help
		control.Rect.Y = button.Rect.Y + int32(i)*22
		kept = append(kept, control)
	}
	help.Name, help.SourceName, help.Text, help.Link = "HELPTEXT", "HELPTEXT", "", ""
	help.Attribs, help.QuickKey = gui.AttribInert, 0
	help.Rect.X, help.Rect.Y, help.Rect.W = button.Rect.X, button.Rect.Y+int32(len(rows))*22+4, 180
	kept = append(kept, help)
	window.Gadgets = kept
	return nil
}

func (g *gameShell) syncCommunityHUDOptions() {
	if optionsState == nil || optionsState.page != "communityhud" || optionsPanel == nil {
		return
	}
	p := g.presentation
	optionsPanel.SetStageAt(optionsPanel.Index("NHEALTH"), boolInt(client.DamageBars()))
	for _, row := range communityHUDSwitches(&p) {
		optionsPanel.SetStageAt(optionsPanel.Index(row.name), *row.value)
	}
}
func (g *gameShell) activateCommunityHUDOption(name string) bool {
	if name == "NHEALTH" {
		g.setCommunityHealthBars(g.retailOptionsStage(name, 2, boolInt(client.DamageBars())) != 0)
		g.syncCommunityHUDOptions()
		return true
	}
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

// setCommunityHealthBars changes the existing interface bit only after any
// speculative recorder has stopped reading it. The settings writer persists
// the live bit with the rest of the options snapshot [07 R-HUD-03 §7][I6].
func (g *gameShell) setCommunityHealthBars(on bool) {
	if client.DamageBars() == on {
		return
	}
	if clPtr != nil {
		clPtr.JoinPreRecord()
	}
	client.SetDamageBars(on)
	if clPtr != nil {
		clPtr.BumpPresentationEpoch()
	}
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
