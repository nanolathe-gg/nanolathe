package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/platform/screenkit"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Presets on the Nanolathe screen (docs/DESIGN_MODS_MUTATORS.md §4.6): named
// sets of mod-scoped settings the player applies to whatever content is
// selected. The built-in ones are the original game's settings and each
// installed mod's recommendations; the player saves their own. Applying one
// can be limited to its rules, its graphics or its controls.

// nlPresetScopes are the parts of a preset the player can apply alone. No
// part holds the unit restrictions: they are a match selection, like the
// mutators, not a preference a preset carries (DESIGN_MODS_MUTATORS §15.9).
var nlPresetScopes = []struct {
	label string
	paths func() []string
}{
	{"Rules", func() []string { return []string{"gameplay", "gameplayFeatures", "unitLimit"} }},
	{"Graphics & effects", nlGraphicsPaths},
	{"Controls & interface", nlControlsPaths},
}

// nlGraphicsPaths are the presentation settings that change the picture.
func nlGraphicsPaths() []string {
	var out []string
	for _, k := range []string{"renderer", "fps", "expandedSidebar", "sidebarOrders", "buildMenuPageSize", "trailStrength", "strategicIconConfig",
		"waterSurface", "waterMotion", "waterFoam", "waterReflections", "hovercraftLandWash", "modelLight", "groundLight", "groundLightStrength",
		"finish", "glint", "supersample", "blastRings", "blastRingStrength", "fireShimmer", "wreckGlow", "wreckShimmer", "scorch", "softShadows",
		"arrival", "placementWeaponRanges", "weaponGlowStrength", "explosionGlowStrength", "nanoGlowStrength", "shadowSoftness"} {
		out = append(out, "presentation."+k)
	}
	return append(out, "display.glow", "display.glowStrength")
}

// nlControlsPaths are every other mod-scoped setting: keys, mouse, selection
// and the interface preferences a controls profile assigns — except the unit
// restrictions, which are mod-scoped but belong to no preset.
func nlControlsPaths() []string {
	taken := map[string]bool{"restrictions": true}
	for _, p := range append(nlGraphicsPaths(), "gameplay", "gameplayFeatures", "unitLimit") {
		taken[p] = true
	}
	var out []string
	doc := map[string]any{}
	if data, err := json.Marshal(settings.Defaults()); err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	pres, _ := doc["presentation"].(map[string]any)
	for _, k := range sortedMapKeys(pres) {
		if p := "presentation." + k; !taken[p] {
			out = append(out, p)
		}
	}
	for _, p := range settings.ModScoped {
		if p != "presentation" && !taken[p] {
			out = append(out, p)
		}
	}
	return out
}

func sortedMapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// nlPresetEntry is one row of the presets panel.
type nlPresetEntry struct {
	name   string
	source string // "Built in", a mod's name, or "Yours"
	patch  json.RawMessage
	user   int // index into the shell's presets, -1 for a built-in
}

// presetEntries lists the presets: the original game, each installed mod
// that recommends settings, then the player's own.
func (s *nlScreen) presetEntries() []nlPresetEntry {
	var out []nlPresetEntry
	if patch, err := nlCompletePreset(settings.Defaults()); err == nil {
		out = append(out, nlPresetEntry{name: "Original game", source: "Built in", patch: patch, user: -1})
	}
	for i := range s.mods {
		m := &s.mods[i]
		if rec := modRecommendations(m); len(rec) > 0 {
			out = append(out, nlPresetEntry{name: m.Name, source: "Mod", patch: rec, user: -1})
		}
	}
	if g := s.shell(); g != nil {
		for i, p := range g.presets {
			out = append(out, nlPresetEntry{name: p.Name, source: "Yours", patch: p.Settings, user: i})
		}
	}
	return out
}

// A complete preset must encode Auto and default keys explicitly: the normal
// settings file omits them, but omission in a patch would keep the previous
// content's explicit limit or bindings. Mod recommendations remain partial.
func nlCompletePreset(st settings.Settings) (json.RawMessage, error) {
	data, err := json.Marshal(struct {
		settings.Settings
		UnitLimit   int                  `json:"unitLimit"`
		KeyBindings settings.KeyBindings `json:"keyBindings"`
	}{Settings: st, UnitLimit: st.UnitLimit, KeyBindings: st.KeyBindings})
	if err != nil {
		return nil, err
	}
	return settings.Restrict(data, nlPresetPaths())
}

// nlPresetPaths are the mod-scoped paths a preset saves: all of them but the
// unit restrictions, so saving a preset never captures a match selection and
// applying one never changes it (DESIGN_MODS_MUTATORS §15.9).
func nlPresetPaths() []string {
	return slices.DeleteFunc(slices.Clone(settings.ModScoped), func(p string) bool { return p == "restrictions" })
}

// draftSettings composes the same value Apply will write, without calling
// live shell setters: profile, pending presets, then touched cards and keys
// (DESIGN_INTERFACE_HUD_INPUT §3.17). The full settings value retains pending
// rows that have no card, such as audio, clock and gameplay feature overrides.
func (s *nlScreen) draftSettings() (settings.Settings, error) {
	g := s.shell()
	base := g.liveSettings()
	target := g.contentMod()
	if !g.cs.manualRoots {
		target = s.modAt(s.draft.mod)
	}
	if !sameMod(target, g.contentMod()) || s.draft.override != g.lockOverridden(target) {
		// A pending content switch or lock override first reloads the settings
		// layers. Resolve those layers from a captured value, without changing
		// either shell or its stored lock approvals.
		file := g.captureSettings()
		if target != nil {
			file.ModLockOverrides = slices.DeleteFunc(slices.Clone(file.ModLockOverrides), func(id string) bool { return id == target.ID })
			if s.draft.override {
				file.ModLockOverrides = append(file.ModLockOverrides, target.ID)
			}
		}
		layers := gameShell{cs: &contentSet{mod: target}}
		base = layers.effectiveSettings(file)
		base.Presentation = startupPresentation(g.opts, base.Presentation)
		base.Gameplay = startupGameplay(g.opts, base.Gameplay)
	}
	profile, err := nlControlsPresetPatch(base, nlControlsPresets[s.draft.controls].preset, g.survivalMenu)
	if err != nil {
		return base, err
	}
	patches := append([]json.RawMessage{profile}, s.pendingPresets...)
	base, err = settings.Layer(base, patches...)
	if err != nil {
		return base, err
	}
	next := s.draftOf(base)
	for _, page := range s.pages() {
		for _, c := range page.cards {
			if s.touched[c.key] && c.key != "content" {
				nlCopyCard(c, &next, &s.draft)
			}
		}
	}
	if s.draft.keys != nil && (s.touched["keys"] || s.draft.controls != 0) {
		next.keys = s.draft.keys
	}
	if minimum, ok := modMinimumGameplay(target); ok && gameplayBelow(next.gameplay, minimum) && !s.draft.override {
		next.gameplay = minimum
	}
	return settingsOf(base, next), nil
}

// nlControlsPresetPatch reads the existing assignment table as a settings
// patch. Its setters also update live presentation, so draft export must not
// call them. The keyboard and dot-colour rows encode named compound values;
// every other row already carries its saved scalar.
func nlControlsPresetPatch(base settings.Settings, preset string, survivalMenu bool) (json.RawMessage, error) {
	doc := map[string]any{}
	for _, row := range controlsPresetRows {
		value := row.presetValue(preset)
		if value == presetUnchanged || (row.path == "skirmish.numPlayers" && survivalMenu) {
			continue
		}
		var stored any = value
		switch row.path {
		case "keyBindings":
			keys := base.KeyBindings
			keys.Profile = keyProfiles[value]
			stored = keys
		case "presentation.playerDotColors":
			stored = settings.DefaultPlayerDotColors
			if value == playerColoursProTA {
				stored = settings.ProTAPlayerDotColors
			} else if value == playerColoursZero {
				stored = settings.ZeroPlayerDotColors
			}
		}
		path, into := strings.Split(row.path, "."), doc
		for _, part := range path[:len(path)-1] {
			child, ok := into[part].(map[string]any)
			if !ok {
				child = map[string]any{}
				into[part] = child
			}
			into = child
		}
		into[path[len(path)-1]] = stored
	}
	return json.Marshal(doc)
}

// settingsOf lays a draft's fields over a settings block.
func settingsOf(base settings.Settings, d nlDraft) settings.Settings {
	out := base
	out.Gameplay = d.gameplay
	out.Presentation = d.pres
	out.Display.Glow, out.Display.GlowStrength = d.glow, d.glowStrength
	out.Display.Width, out.Display.Height = d.resolution.W, d.resolution.H
	out.UnitLimit = d.unitLimit
	out.SwitchAlt = onOff(d.switchAlt)
	out.InterfaceType = d.interfaceType
	if d.keys != nil {
		out.KeyBindings = keyBindingsSetting(d.keys)
	}
	return out
}

// applyPresetToDraft lays the preset's chosen parts over the draft; every
// card it moves counts as changed, and Apply writes the rest of the preset
// (audio, megamap and other settings no card shows).
func (s *nlScreen) applyPresetToDraft(e nlPresetEntry, scopes []bool) {
	var paths []string
	for i, sc := range nlPresetScopes {
		if scopes[i] {
			paths = append(paths, sc.paths()...)
		}
	}
	patch, err := settings.Restrict(e.patch, paths)
	if err != nil || len(patch) == 0 {
		s.toast, s.toastLeft = "That preset has nothing in the chosen parts", 2.5
		return
	}
	if s.sourceActive() && !s.src.overridden && !s.draft.override {
		var doc map[string]any
		_ = json.Unmarshal(patch, &doc)
		var changedPaths []string
		collectPaths(doc, "", func(path string) { changedPaths = append(changedPaths, path) })
		if pathsLocked(changedPaths, s.src.locks) {
			parts := slices.Clone(scopes)
			s.capture = nlCapture{}
			s.pendingAction = func() { s.applyPresetToDraft(e, parts) }
			s.pendingWhat, s.dialog = "the preset's settings", "override"
			return
		}
	}
	current, err := s.draftSettings()
	if err != nil {
		s.toast, s.toastLeft = err.Error(), 3
		return
	}
	before := s.draftOf(current)
	before.mod, before.controls, before.override = s.draft.mod, s.draft.controls, s.draft.override
	before.fullscreen, before.mutators = s.draft.fullscreen, s.draft.mutators
	before.monitor = s.draft.monitor
	before.restrictions = s.draft.restrictions
	next, err := settings.Layer(current, patch)
	if err != nil {
		s.toast, s.toastLeft = err.Error(), 3
		return
	}
	d := &s.draft
	d.gameplay = next.Gameplay
	d.pres = next.Presentation
	d.glow, d.glowStrength = next.Display.Glow, next.Display.GlowStrength
	d.shadows, d.vehicleShadows = next.Display.Shadows, next.Display.VehicleShadows
	d.unitLimit = next.UnitLimit
	d.switchAlt = next.SwitchAltEnabled()
	d.interfaceType = next.InterfaceType
	d.keys = keyMapFromSettings(next.KeyBindings)
	// A preset cannot take the rules below the content's lock.
	if minimum, ok := modMinimumGameplay(s.modAt(d.mod)); ok && gameplayBelow(d.gameplay, minimum) && !d.override {
		d.gameplay = minimum
	}
	for _, page := range s.pages() {
		for _, c := range page.cards {
			if !nlCardEqual(c, &before, d) {
				s.touched[c.key] = true
			}
		}
	}
	if keys, _ := settings.Restrict(patch, []string{"keyBindings"}); len(keys) > 0 {
		s.touched["keys"] = true
	}
	s.pendingPresets = append(s.pendingPresets, patch)
	s.toast, s.toastLeft = fmt.Sprintf("%s applied — Apply to keep it", e.name), 2.5
}

// savePreset stores the draft's mod-scoped settings under name, replacing a
// preset of the same name.
func (s *nlScreen) savePreset(name string) {
	g := s.shell()
	name = strings.TrimSpace(name)
	if g == nil || name == "" {
		return
	}
	draft, err := s.draftSettings()
	if err != nil {
		s.toast, s.toastLeft = err.Error(), 3
		return
	}
	patch, err := nlCompletePreset(draft)
	if err != nil {
		return
	}
	p := settings.Preset{Name: name, Settings: patch}
	if i := slices.IndexFunc(g.presets, func(q settings.Preset) bool { return strings.EqualFold(q.Name, name) }); i >= 0 {
		g.presets[i] = p
	} else {
		g.presets = append(g.presets, p)
	}
	g.saveSettings()
	s.toast, s.toastLeft = fmt.Sprintf("Saved preset %s", name), 2.2
	s.presetName = ""
}

// updatePresets handles the panel's keys: typing names a new preset, Enter
// saves it, Esc closes.
func (s *nlScreen) updatePresets(in screenkit.Input) {
	if in.WheelY != 0 && s.presetList.Contains(in.X, in.Y) {
		s.wheel += in.WheelY
		if math.Abs(s.wheel) >= 1 {
			s.presetTop -= int(math.Copysign(1, s.wheel))
			s.wheel = 0
		}
	}
	for _, k := range in.Keys {
		switch k {
		case ebiten.KeyEscape:
			s.dialog = ""
			return
		case ebiten.KeyEnter:
			if s.presetName != "" {
				s.savePreset(s.presetName)
			}
			return
		case ebiten.KeyBackspace:
			if n := len(s.presetName); n > 0 {
				_, size := utf8.DecodeLastRuneInString(s.presetName)
				s.presetName = s.presetName[:n-size]
			}
		case ebiten.KeyArrowDown:
			s.selectPreset(s.presetSel + 1)
		case ebiten.KeyArrowUp:
			s.selectPreset(s.presetSel - 1)
		case ebiten.KeyPageDown:
			s.selectPreset(s.presetSel + s.presetVisible())
		case ebiten.KeyPageUp:
			s.selectPreset(s.presetSel - s.presetVisible())
		case ebiten.KeyHome:
			s.selectPreset(0)
		case ebiten.KeyEnd:
			s.selectPreset(len(s.presetEntries()) - 1)
		}
	}
	for _, r := range ebiten.AppendInputChars(nil) {
		if r >= ' ' && utf8.RuneCountInString(s.presetName) < 32 {
			s.presetName += string(r)
		}
	}
}

func (s *nlScreen) presetVisible() int {
	if s.u() <= 0 {
		return 1
	}
	return max(1, int((s.presetList.H-12*s.u())/(40*s.u())))
}

func (s *nlScreen) selectPreset(row int) {
	s.presetSel = max(0, min(row, len(s.presetEntries())-1))
	if s.presetSel < s.presetTop {
		s.presetTop = s.presetSel
	}
	if visible := s.presetVisible(); s.presetSel >= s.presetTop+visible {
		s.presetTop = s.presetSel - visible + 1
	}
}

// drawPresets is the presets panel: the list, the parts to apply, and a
// name field to save the current settings.
func (s *nlScreen) drawPresets(screen *ebiten.Image) {
	u := s.u()
	screenkit.Fill(screen, screenkit.Rect{W: s.w(), H: s.h()}, color.RGBA{0, 0, 0, 160})
	s.hits.Add(screenkit.Region{ID: "presets-block", Rect: screenkit.Rect{W: s.w(), H: s.h()}})
	w, h := 820*u, 560*u
	r := screenkit.Rect{X: s.w()/2 - w/2, Y: s.h()/2 - h/2, W: w, H: h}
	s.well(screen, r, 1)
	df, bf := s.fonts.Display, s.fonts.Body
	df.Draw(screen, "Presets", r.X+32*u, r.Y+52*u, screenkit.Style{Size: 30 * u, Tracking: 0.04, Top: nlGoldTop, Bottom: nlGoldBottom})
	scope := "the original game"
	if m := s.modAt(s.draft.mod); m != nil {
		scope = m.Name
	}
	bf.Draw(screen, "Applying a preset changes the settings for "+scope+". Apply keeps it.", r.X+32*u, r.Y+80*u, screenkit.Style{Size: 12 * u, Top: nlBody})
	entries := s.presetEntries()
	s.presetSel = max(0, min(s.presetSel, len(entries)-1))
	list := screenkit.Rect{X: r.X + 28*u, Y: r.Y + 100*u, W: 400 * u, H: h - 190*u}
	s.presetList = list
	visible := s.presetVisible()
	s.presetTop = max(0, min(s.presetTop, len(entries)-visible))
	screenkit.Fill(screen, list, color.RGBA{8, 12, 8, 220})
	screenkit.Outline(screen, list, 1*u, color.RGBA{50, 60, 46, 255})
	rowH := 40 * u
	for row := 0; row < visible && s.presetTop+row < len(entries); row++ {
		i := s.presetTop + row
		e := entries[i]
		rr := screenkit.Rect{X: list.X + 6*u, Y: list.Y + 6*u + float64(row)*rowH, W: list.W - 12*u, H: rowH - 4*u}
		id := s.ui.id("preset", "", i, -1)
		if i == s.presetSel {
			screenkit.HGradient(screen, rr, color.RGBA{40, 100, 44, 220}, color.RGBA{16, 30, 16, 120})
			screenkit.Outline(screen, rr, 1.5*u, color.RGBA{255, 227, 138, 255})
		} else if hv := s.hits.HoverAmount(id); hv > 0 {
			screenkit.Fill(screen, rr, color.RGBA{60, 70, 50, uint8(90 * hv)})
		}
		bf.Draw(screen, e.name, rr.X+12*u, rr.Y+rr.H/2+5*u, screenkit.Style{Size: 13.5 * u, Top: nlCream})
		df.Draw(screen, e.source, rr.X+rr.W-10*u, rr.Y+rr.H/2+4*u, screenkit.Style{Size: 10 * u, Tracking: 0.14, Top: nlKicker, Upper: true, Align: 2})
		s.hits.Add(screenkit.Region{ID: id, Rect: rr, Click: func() { s.selectPreset(i) }})
	}
	if len(entries) > visible {
		track := screenkit.Rect{X: list.X + list.W + 8*u, Y: list.Y, W: 5 * u, H: list.H}
		screenkit.Fill(screen, track, color.RGBA{20, 24, 20, 200})
		thumbH := track.H * float64(visible) / float64(len(entries))
		thumbY := track.Y + (track.H-thumbH)*float64(s.presetTop)/float64(len(entries)-visible)
		screenkit.Fill(screen, screenkit.Rect{X: track.X, Y: thumbY, W: track.W, H: thumbH}, color.RGBA{120, 200, 120, 220})
	}
	// The parts to apply.
	px := list.X + list.W + 28*u
	py := list.Y
	df.Draw(screen, "Apply these parts", px, py+12*u, screenkit.Style{Size: 11 * u, Tracking: 0.24, Top: nlKicker, Upper: true})
	py += 26 * u
	for i, sc := range nlPresetScopes {
		br := screenkit.Rect{X: px, Y: py, W: 320 * u, H: 38 * u}
		id := s.ui.id("preset-scope", "", i, -1)
		on := s.presetScopes[i]
		screenkit.Fill(screen, br, color.RGBA{14, 16, 12, 220})
		if on {
			screenkit.HGradient(screen, br, color.RGBA{40, 100, 44, 220}, color.RGBA{14, 30, 16, 180})
		}
		edge := color.RGBA{70, 70, 60, 255}
		if on {
			edge = color.RGBA{255, 227, 138, 255}
		}
		screenkit.Outline(screen, br, 1.5*u, edge)
		s.lamp(screen, br.X+18*u, br.Y+br.H/2, 6*u, nlGreen, on)
		bf.Draw(screen, sc.label, br.X+36*u, br.Y+br.H/2+5*u, screenkit.Style{Size: 13 * u, Top: nlCream})
		s.hits.Add(screenkit.Region{ID: id, Rect: br, Click: func() { s.presetScopes[i] = !s.presetScopes[i] }})
		py += 46 * u
	}
	if len(entries) > 0 {
		e := entries[s.presetSel]
		s.button(screen, "preset-apply", screenkit.Rect{X: px, Y: py + 6*u, W: 150 * u, H: 40 * u}, "Apply preset", true, false, func() {
			s.dialog = ""
			s.applyPresetToDraft(e, s.presetScopes[:])
		})
		if e.user >= 0 {
			s.button(screen, "preset-delete", screenkit.Rect{X: px + 162*u, Y: py + 6*u, W: 120 * u, H: 40 * u}, "Delete", false, false, func() {
				g := s.shell()
				g.presets = slices.Delete(g.presets, e.user, e.user+1)
				g.saveSettings()
			})
		}
	}
	// Save the current settings under a name.
	fy := r.Y + h - 70*u
	df.Draw(screen, "Save current settings as", r.X+32*u, fy-10*u, screenkit.Style{Size: 11 * u, Tracking: 0.24, Top: nlKicker, Upper: true})
	field := screenkit.Rect{X: r.X + 32*u, Y: fy, W: 420 * u, H: 40 * u}
	screenkit.Fill(screen, field, color.RGBA{4, 8, 4, 240})
	screenkit.Outline(screen, field, 1.5*u, color.RGBA{90, 110, 86, 255})
	text := s.presetName
	caret := ""
	if int(s.clock*2)%2 == 0 {
		caret = "|"
	}
	// What the player types is drawn as typed.
	st := screenkit.Style{Size: 14 * u, Top: nlCream, Verbatim: true}
	if text == "" {
		bf.Draw(screen, "Type a name", field.X+12*u, field.Y+26*u, screenkit.Style{Size: 14 * u, Top: nlDim})
		bf.Draw(screen, caret, field.X+10*u, field.Y+26*u, st)
	} else {
		bf.Draw(screen, text+caret, field.X+12*u, field.Y+26*u, st)
	}
	s.button(screen, "preset-save", screenkit.Rect{X: field.X + field.W + 12*u, Y: fy, W: 120 * u, H: 40 * u}, "Save", false, false, func() { s.savePreset(s.presetName) })
	s.button(screen, "preset-close", screenkit.Rect{X: r.X + w - 32*u - 120*u, Y: fy, W: 120 * u, H: 40 * u}, "Close", false, false, func() { s.dialog = "" })
}
