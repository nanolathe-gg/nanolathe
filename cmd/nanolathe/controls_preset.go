package main

// Controls presets: a mod's recommended settings, offered once and never
// forced (docs/DESIGN_MODS_MUTATORS.md §4.3, D13, P10). The preset's
// contents live in controlsPresetRows alone; the design document's §4.3 table
// lists the same rows.

import (
	"fmt"
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The preset names mod metadata and content profiles may carry.
const (
	controlsPresetCommunity = contentprofiles.ControlsCommunity
	controlsPresetRetail    = contentprofiles.ControlsRetail
)

// presetUnchanged is a row value the preset leaves alone.
const presetUnchanged = -1

// presetCustom is what a row that stands for several stored values reads
// when they match none of its named choices; no preset writes it.
const presetCustom = -2

// controlsPresetRow is one existing host option a preset assigns. Each value
// is the option's own stored integer. A row owns no new behaviour: it writes
// a setting the player can already change on an options page or with a chat
// command.
type controlsPresetRow struct {
	label     string
	community int
	retail    int
	// names displays a value; nil displays Off/On for 0/1, and numbers
	// otherwise.
	names []string
	get   func(g *gameShell) int
	set   func(g *gameShell, value int)
}

var onOffNames = []string{"Off", "On"}

// The Dot colours row's two named tables.
const (
	playerColoursDefault = 0
	playerColoursProTA   = 1
)

// presentationRow assigns one field of the presentation block.
func presentationRow(label string, community, retail int, field func(*settings.Presentation) *int) controlsPresetRow {
	return controlsPresetRow{
		label: label, community: community, retail: retail, names: onOffNames,
		get: func(g *gameShell) int {
			p := g.presentation
			return *field(&p)
		},
		set: func(g *gameShell, value int) {
			p := g.presentation
			*field(&p) = value
			g.setPresentation(p)
		},
	}
}

// controlsPresetRows is the whole content of both presets. The `community`
// column is ProTA 4.8's recommended settings: the Community host options
// (DESIGN_COMMUNITY_PATCH §7), the preferences ProTA's `ProTA.ini` pins
// through its `[REG]` block (research/extensions/community-patch-engine.md
// §4.1), its draw-engine megamap keys, and the victory cue its renderer
// always plays. The `retail` column is the retail default of each; a row
// the retail preset leaves alone says presetUnchanged.
var controlsPresetRows = []controlsPresetRow{
	presentationRow("Idle unit keys", 1, 0, func(p *settings.Presentation) *int { return &p.CommunitySelection }),
	presentationRow("Double-click select", 1, 0, func(p *settings.Presentation) *int { return &p.DoubleClickSelection }),
	presentationRow("Order drag", 1, 0, func(p *settings.Presentation) *int { return &p.QueuedOrderDrag }),
	{
		label: "Digit keys", community: 1, retail: settings.DefaultSwitchAlt,
		names: []string{"Pages", "Groups"},
		get:   func(g *gameShell) int { return boolInt(g.switchAlt) },
		set:   func(g *gameShell, value int) { g.switchAlt = value != 0 },
	},
	presentationRow("Counters", 1, 0, func(p *settings.Presentation) *int { return &p.CommunityCounters }),
	presentationRow("Reload bars", 1, 0, func(p *settings.Presentation) *int { return &p.ReloadBars }),
	presentationRow("Veterancy", 1, 0, func(p *settings.Presentation) *int { return &p.VeteranLabels }),
	presentationRow("Group digits", 1, 0, func(p *settings.Presentation) *int { return &p.GroupNumbers }),
	presentationRow("Wind/tide readout", 1, 0, func(p *settings.Presentation) *int { return &p.WeatherReport }),
	// The megamap rows are ProTA.ini's draw-engine keys
	// (DESIGN_INTERFACE_HUD_INPUT §3.15). The retail preset returns the
	// overview to Zoom and leaves the megamap's own preferences alone.
	{
		label: "Overview", community: settings.OverviewMegamap, retail: settings.OverviewZoom,
		names: []string{"Zoom", "Megamap"},
		get:   func(g *gameShell) int { return g.presentation.Overview },
		set: func(g *gameShell, value int) {
			p := g.presentation
			p.Overview = value
			g.setPresentation(p)
		},
	},
	presentationRow("Megamap wheel", 1, presetUnchanged, func(p *settings.Presentation) *int { return &p.MegamapWheel }),
	presentationRow("Wheel out moves camera", 1, presetUnchanged, func(p *settings.Presentation) *int { return &p.MegamapWheelMove }),
	presentationRow("Megamap double-click move", 0, presetUnchanged, func(p *settings.Presentation) *int { return &p.MegamapDoubleClickMove }),
	presentationRow("Under-attack flash", 1, presetUnchanged, func(p *settings.Presentation) *int { return &p.MegamapFlash }),
	{
		// One row for the four ring minimums, which ProTA.ini sets alike.
		label: "Megamap ring minimums", community: 0, retail: presetUnchanged,
		get: func(g *gameShell) int {
			p := g.presentation
			value := p.MegamapRadarMinimum
			if p.MegamapSonarMinimum != value || p.MegamapSonarJamMinimum != value || p.MegamapAntiNukeMinimum != value {
				return presetCustom
			}
			return value
		},
		set: func(g *gameShell, value int) {
			p := g.presentation
			p.MegamapRadarMinimum, p.MegamapSonarMinimum, p.MegamapSonarJamMinimum, p.MegamapAntiNukeMinimum = value, value, value, value
			g.setPresentation(p)
		},
	},
	{
		// The ten-entry dot colour table as one choice: the draw engine's
		// defaults (retail) or ProTA.ini's palette.
		label: "Dot colours", community: playerColoursProTA, retail: playerColoursDefault,
		names: []string{"Default", "ProTA"},
		get: func(g *gameShell) int {
			switch g.presentation.PlayerDotColors {
			case settings.DefaultPlayerDotColors:
				return playerColoursDefault
			case settings.ProTAPlayerDotColors:
				return playerColoursProTA
			}
			return presetCustom
		},
		set: func(g *gameShell, value int) {
			p := g.presentation
			p.PlayerDotColors = settings.DefaultPlayerDotColors
			if value == playerColoursProTA {
				p.PlayerDotColors = settings.ProTAPlayerDotColors
			}
			g.setPresentation(p)
		},
	},
	{
		label: "Game clock", community: 1, retail: settings.DefaultClock, names: onOffNames,
		get: func(g *gameShell) int { return boolInt(g.clockVisible) },
		set: func(g *gameShell, value int) { g.clockVisible = value != 0 },
	},
	{
		label: "Sound", community: settings.SoundMode3D, retail: settings.DefaultSoundMode,
		names: []string{"Off", "Mono", "3D"},
		get:   func(g *gameShell) int { return g.audioPrefs.SoundMode },
		set:   func(g *gameShell, value int) { g.audioPrefs.SoundMode = value },
	},
	presentationRow("Victory cue", 1, 0, func(p *settings.Presentation) *int { return &p.VictoryCue }),
	{
		// 128 voices is more than the mixer's 32 tracked slots, so no sound
		// is cut off for the voice limit [03 R-AUD-01 §1].
		label: "Sound voices", community: 128, retail: settings.DefaultMixingBuffers,
		get: func(g *gameShell) int { return g.audioPrefs.MixingBuffers },
		set: func(g *gameShell, value int) { g.audioPrefs.MixingBuffers = value },
	},
	{
		label: "Music", community: 2, retail: settings.DefaultCDMode,
		names: []string{"", "Play all", "Random", "Repeat", "Custom"},
		get:   func(g *gameShell) int { return g.audioPrefs.CDMode },
		set:   func(g *gameShell, value int) { g.audioPrefs.CDMode = value },
	},
	{
		// The skirmish screen's row count, `NumSkirmishPlayers`, as the
		// `*X` selector sets it [08 R-SKIR-01 §1]. Only the rows shown
		// change: each row keeps its controller, so the players a skirmish
		// starts with stay the same. The retail preset never removes rows.
		label: "Skirmish rows", community: settings.MaxPlayers, retail: presetUnchanged,
		get: func(g *gameShell) int { return g.setup.NumPlayers },
		set: func(g *gameShell, value int) {
			if !g.survivalMenu {
				g.setup.NumPlayers = value
			}
		},
	},
}

// presetValue is the row's value under a preset, or presetUnchanged.
func (r controlsPresetRow) presetValue(preset string) int {
	switch preset {
	case controlsPresetCommunity:
		return r.community
	case controlsPresetRetail:
		return r.retail
	}
	return presetUnchanged
}

func (r controlsPresetRow) valueText(value int) string {
	if value == presetCustom {
		return "Custom"
	}
	if value >= 0 && value < len(r.names) && r.names[value] != "" {
		return r.names[value]
	}
	return strconv.Itoa(value)
}

// applyControlsPreset writes a named assignment of existing host options
// once (§4.3, P10). Later changes by the player stick; nothing restores them.
func (g *gameShell) applyControlsPreset(name string) {
	if name != controlsPresetCommunity && name != controlsPresetRetail {
		return
	}
	for _, row := range controlsPresetRows {
		if value := row.presetValue(name); value != presetUnchanged {
			row.set(g, value)
		}
	}
	g.audioPrefs.Normalize()
	g.applyRetailAudioOptions()
	if g.audioOwner != nil && g.audioOwner.Music != nil {
		music := g.audioOwner.Music
		music.Configure(audio.PlayMode(g.audioPrefs.CDMode), music.DesiredCategory())
	}
}

// ---------------------------------------------------------------------------
// The one-time offer on the main menu (§4.3).

// controlsPresetOffer names the preset the running content recommends and
// the key its offer is remembered under: the mod's id, or `profile:<name>`
// for a content profile mounted without a mod. A mod carries its content
// profile's preset when its metadata names none (§4.5).
func (g *gameShell) controlsPresetOffer() (preset, key, name string) {
	if g == nil || g.cs == nil {
		return "", "", ""
	}
	if mod := g.cs.mod; mod != nil {
		if mod.Controls == "" {
			return "", "", ""
		}
		return mod.Controls, mod.ID, mod.Name
	}
	if g.cs.profileControls == "" {
		return "", "", ""
	}
	return g.cs.profileControls, "profile:" + g.cs.profile, "The " + g.cs.profile + " content"
}

// markControlsOffered records an offer, answered either way.
func (g *gameShell) markControlsOffered(key string) {
	g.controlsOffered = settings.MarkControlsOffered(g.controlsOffered, key)
}

// wantsControlsOffer reports whether the main menu should offer the running
// content's preset now. It is asked once per mod, only where the answer can
// be remembered, and never by a capture or benchmark.
func (g *gameShell) wantsControlsOffer() bool {
	if g == nil || !g.settingsWritable || g.opts.ignoresSavedSelection() || controlsOfferUI != nil {
		return false
	}
	// Only over the bare main menu: no child window or message is open.
	if g.frontend == nil || g.frontend.Mode != modeMenuMain || g.frontend.Panels.Len() != 1 || g.activePanel() == nil {
		return false
	}
	preset, key, _ := g.controlsPresetOffer()
	return preset != "" && !settings.ControlsWereOffered(g.controlsOffered, key)
}

// controlsOfferDialog is the offer's window state: which preset, for which
// content, and the base install its window template is read from.
type controlsOfferDialog struct {
	preset, key, name string
	base              *vfs.FS
	selected          int
}

var (
	controlsOfferUI     *controlsOfferDialog
	controlsOfferPanel  *ui.Panel
	controlsOfferAssets *retailPanelAssets
)

func (g *gameShell) controlsOfferActive() bool {
	return g != nil && controlsOfferUI != nil && controlsOfferPanel != nil && g.activePanel() == controlsOfferPanel
}

// pollControlsOffer opens the offer over the main menu when it is due.
func (g *gameShell) pollControlsOffer() {
	if !g.wantsControlsOffer() {
		return
	}
	if err := g.openControlsOffer(); err != nil {
		// The offer is a convenience; a missing template must not trap the
		// menu in a retry loop, so it counts as made.
		_, key, _ := g.controlsPresetOffer()
		g.markControlsOffered(key)
		g.saveSettings()
		reportRetailMessageError(g.showRetailMessage(err.Error()))
	}
}

func (g *gameShell) openControlsOffer() error {
	preset, key, name := g.controlsPresetOffer()
	if preset == "" {
		return nil
	}
	base := vfs.New()
	if err := base.MountGameDirectories(g.cs.baseRoots); err != nil {
		base.Close()
		base = nil
	}
	controlsOfferUI = &controlsOfferDialog{preset: preset, key: key, name: name, base: base}
	panel, assets, err := g.loadModsPanel(modsWindowOffer)
	if err != nil {
		g.releaseControlsOffer()
		return err
	}
	controlsOfferPanel, controlsOfferAssets = panel, assets
	g.frontend.Panels.Push(panel)
	flushWindowTokens(clPtr)
	g.refreshControlsOffer()
	return nil
}

// releaseControlsOffer drops the dialog's state without answering it.
func (g *gameShell) releaseControlsOffer() {
	if g != nil && controlsOfferPanel != nil && g.frontend.Panels.Top() == controlsOfferPanel {
		g.frontend.Panels.Pop()
	}
	if controlsOfferUI != nil && controlsOfferUI.base != nil {
		controlsOfferUI.base.Close()
	}
	controlsOfferUI, controlsOfferPanel, controlsOfferAssets = nil, nil, nil
}

// answerControlsOffer applies the preset when accepted, remembers the offer
// either way and closes the dialog.
func (g *gameShell) answerControlsOffer(accept bool) {
	if controlsOfferUI == nil {
		return
	}
	if accept {
		g.applyControlsPreset(controlsOfferUI.preset)
	}
	g.markControlsOffered(controlsOfferUI.key)
	g.saveSettings()
	g.releaseControlsOffer()
}

// controlsOfferRows lists every row the preset assigns, each with its new
// value and, where it differs, the player's current one.
func (g *gameShell) controlsOfferRows(preset string) (rows []string, changes int) {
	for _, row := range controlsPresetRows {
		value := row.presetValue(preset)
		if value == presetUnchanged {
			continue
		}
		text := row.label + ": " + row.valueText(value)
		if current := row.get(g); current != value {
			text += " (now " + row.valueText(current) + ")"
			changes++
		}
		rows = append(rows, text)
	}
	return rows, changes
}

func (g *gameShell) refreshControlsOffer() {
	state, p := controlsOfferUI, controlsOfferPanel
	if state == nil || p == nil {
		return
	}
	rows, changes := g.controlsOfferRows(state.preset)
	if g.activePanel() == p {
		g.setListItems("MAPNAMES", rows, state.selected)
	}
	explanation := state.name + " recommends these settings. Apply sets them now; Keep mine leaves yours. This is asked once."
	description := "Any of them can be changed later in Options."
	detail := fmt.Sprintf("%d of %d differ from yours", changes, len(rows))
	if changes == 0 {
		detail = "All already match yours"
	}
	p.SetText("OFFERTEXT", g.fitDetail(explanation, 116, 8))
	p.SetText("DESCRIPTION", g.fitDetail(description, 230, 2))
	p.SetText("SIZE", g.fitDetail(detail, 230, 1))
	p.SetText("LOAD", "Apply")
	p.SetText("PREVMENU", "Keep mine")
}

// activateControlsOfferGadget routes the dialog's buttons; the list only
// moves its highlight.
func (g *gameShell) activateControlsOfferGadget(name string) bool {
	if !g.controlsOfferActive() {
		return false
	}
	switch name {
	case "LOAD":
		g.answerControlsOffer(true)
	case "PREVMENU":
		g.answerControlsOffer(false)
	default:
		g.refreshControlsOffer()
	}
	return true
}

// modRecommendations is a mod's controls preset and gameplay minimum as the
// mount will see them: its metadata, else its content profile (§4.3).
func (s *modsScreen) modRecommendations(mod *modlibrary.Mod) *modlibrary.Mod {
	if s == nil || mod == nil {
		return mod
	}
	key := mod.ID + "@" + mod.Version
	if resolved, ok := s.recommended[key]; ok {
		return &resolved
	}
	if s.recommended == nil {
		s.recommended = map[string]modlibrary.Mod{}
	}
	resolved := modlibrary.ResolveProfileDefaults(s.baseRoots, *mod)
	s.recommended[key] = resolved
	return &resolved
}

// presetToggleText is the Mods & Mutators screen's preset toggle caption
// (§8.2): whether Apply also writes the mod's recommended settings.
func presetToggleText(use bool) string {
	if use {
		return "Yes"
	}
	return "No"
}
