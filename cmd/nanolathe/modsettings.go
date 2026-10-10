package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Per-mod settings (docs/DESIGN_MODS_MUTATORS.md §4.6). The shell's live
// preferences are the effective settings for the running content: the base
// block, then the mod's recommendations, then the player's own changes for
// that mod. The shell keeps the layers it was built from, so a save writes
// the player's changes back to the right layer.

// modSettingsKey is the key the player's changes for the running content are
// kept under; the original game has none, since its settings are the base.
func modSettingsKey(m *modlibrary.Mod) string {
	if m == nil {
		return ""
	}
	return m.ID
}

// modRecommendations is the mod's own settings patch from its config file
// (DESIGN_MODS_MUTATORS §4.2): its settings document, its recommended rule
// set and its keyboard, limited to the mod-scoped paths. A mod without a
// config recommends nothing.
func modRecommendations(m *modlibrary.Mod) json.RawMessage {
	if m == nil || m.Config == nil {
		return nil
	}
	c := m.Config
	doc := map[string]any{}
	if len(c.Settings) > 0 {
		if err := json.Unmarshal(c.Settings, &doc); err != nil {
			return nil
		}
	}
	if c.Rules.Gameplay != "" {
		doc["gameplay"] = string(c.Rules.Gameplay)
	}
	if c.Keys != nil {
		doc["keyBindings"] = c.Keys
	}
	if ranges := c.Content.Presentation.PlacementWeaponRanges; ranges != nil {
		pres, _ := doc["presentation"].(map[string]any)
		if pres == nil {
			pres = map[string]any{}
			doc["presentation"] = pres
		}
		// The content preference predates the settings layer. Keep it as a
		// recommendation unless the mod authored the newer settings path.
		if _, authored := pres["placementWeaponRanges"]; !authored {
			pres["placementWeaponRanges"] = onOff(*ranges)
		}
	}
	if len(doc) == 0 {
		return nil
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil
	}
	out, err := settings.Restrict(data, settings.ModScoped)
	if err != nil {
		return nil
	}
	return out
}

// effectiveSettings layers the file's blocks for the running content.
func (g *gameShell) effectiveSettings(file settings.Settings) settings.Settings {
	key := modSettingsKey(g.contentMod())
	if key == "" {
		return file
	}
	patch := file.ModSettings[key]
	if locks := modLocks(g.contentMod()); len(locks) > 0 && !slices.Contains(file.ModLockOverrides, key) {
		// A locked setting plays the mod's value until the player overrides
		// the mod's locks (§4.3 "Overriding a rule lock").
		patch, _ = settings.Without(patch, locks)
	}
	eff, err := settings.Layer(file, modRecommendations(g.contentMod()), patch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: settings for %s: %v (using the base settings)\n", key, err)
		return file
	}
	return eff
}

// fileSettings is the block a save writes: the base keeps its mod-scoped
// values while a mod runs, taking only the player's global settings from
// the live ones, and the player's changes go to the mod's patch.
func (g *gameShell) fileSettings(live settings.Settings) settings.Settings {
	key := modSettingsKey(g.contentMod())
	if key == "" {
		return live
	}
	// The base layer states the base block's unit restrictions even when
	// there are none, so the original game never takes the running mod's
	// set (docs/DESIGN_MODS_MUTATORS.md §15.9).
	base, err := settings.BaseLayer(g.baseSettings)
	out := live
	if err == nil {
		out, err = settings.Layer(live, base)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: settings for %s: %v\n", key, err)
		return live
	}
	baseline, err := settings.Layer(out, modRecommendations(g.contentMod()))
	var diff json.RawMessage
	if err == nil {
		diff, err = settings.Diff(live, baseline, settings.ModScoped)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: settings for %s: %v\n", key, err)
		return live
	}
	if locks := modLocks(g.contentMod()); len(locks) > 0 && !g.lockOverridden(g.contentMod()) {
		// While locked, the live values on locked paths are the mod's; the
		// player's own values there are kept for when they override.
		kept, _ := settings.Restrict(g.baseSettings.ModSettings[key], locks)
		diff, _ = settings.Without(diff, locks)
		diff, _ = settings.Merge(diff, kept)
	}
	patches := map[string]json.RawMessage{}
	for k, v := range g.baseSettings.ModSettings {
		patches[k] = v
	}
	if diff == nil {
		delete(patches, key)
	} else {
		patches[key] = diff
	}
	if len(patches) == 0 {
		patches = nil
	}
	out.ModSettings = patches
	return out
}

// contentMod is the mod the running content mounted, nil for the original
// game or a manual root stack.
func (g *gameShell) contentMod() *modlibrary.Mod {
	if g == nil || g.cs == nil {
		return nil
	}
	return g.cs.mod
}

// modLocks are the settings paths the mod's config asks the player not to
// change, nil for a mod without a config.
func modLocks(m *modlibrary.Mod) []string {
	if m == nil || m.Config == nil {
		return nil
	}
	locks := slices.Clone(m.Config.Locks)
	for i, path := range locks {
		// The legacy preference and its lock still name the same host choice
		// (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale"). Settings patches
		// migrate before filtering, so their locks must use the new path too.
		if path == "presentation.sidebarScale" {
			locks[i] = "presentation.uiScale"
		}
	}
	return locks
}
