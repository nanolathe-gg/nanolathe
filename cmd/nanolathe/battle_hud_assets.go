package main

// Battle HUD asset resolution: the optional and required loaders for the
// side's GUI windows, interface GAFs and fonts, and the provider-aware
// diagnostics they report through [07 §5].

import (
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// hudProviders returns the ordered provider identities for diagnostics [AGENTS.md §Diagnostics].
func hudProviders(fs vfs.FSOps) []string {
	if fs == nil {
		return nil
	}
	if p, ok := fs.(interface{ Providers() []vfs.ProviderInfo }); ok {
		infos := p.Providers()
		out := make([]string, 0, len(infos))
		for _, info := range infos {
			id := info.ID
			if id == "" {
				id = info.Type
			}
			out = append(out, id)
		}
		return out
	}
	return nil
}

func hudProviderList(fs vfs.FSOps) string {
	return strings.Join(hudProviders(fs), ", ")
}

func hudAssetError(fs vfs.FSOps, logical, ctx string, err error) error {
	return fmt.Errorf("nanolathe: battle HUD: %s: logical path %s, providers searched [%s]: %w", ctx, logical, hudProviderList(fs), err)
}

func hudAssetWarning(fs vfs.FSOps, logical, ctx string, err error) {
	fmt.Fprintf(os.Stderr, "nanolathe: battle HUD: %s: logical path %s, providers searched [%s]: %v\n", ctx, logical, hudProviderList(fs), err)
}

func loadGAFOptional(fs vfs.FSOps, logical, ctx string) *formats.GAF {
	gaf, err := formats.LoadGAFFile(fs, logical)
	if err != nil {
		hudAssetWarning(fs, logical, ctx, err)
		return nil
	}
	return gaf
}

func loadGUIOptional(fs vfs.FSOps, logical, ctx string, captions ...gui.CaptionTranslator) *gui.Window {
	var translator gui.CaptionTranslator
	if len(captions) != 0 {
		translator = captions[0]
	}
	w, err := gui.LoadWithTranslation(fs, logical, translator)
	if err != nil {
		hudAssetWarning(fs, logical, ctx, err)
		return nil
	}
	return w
}

func hudCaptionTranslator(h *retailBattleHUD) gui.CaptionTranslator {
	if h != nil && h.windowContext != nil {
		return h.windowContext.captions()
	}
	return nil
}

// loadGAFFontOptional loads one GAF font's glyph entry, or nil with a
// provider-aware warning: a missing GAF font is a null slot, not fatal
// [03 R-FONT-01 §5].
func loadGAFFontOptional(fs vfs.FSOps, logical, ctx string) *formats.GAFEntry {
	gaf, err := formats.LoadGAFFile(fs, logical)
	if err != nil {
		hudAssetWarning(fs, logical, ctx, err)
		return nil
	}
	if len(gaf.Entries) == 0 || len(gaf.Entries[0].Frames) == 0 {
		hudAssetWarning(fs, logical, ctx+" has no glyph entry", fmt.Errorf("empty"))
		return nil
	}
	return &gaf.Entries[0]
}

// optionsMissionButton is the ARMOPT control the opener relabels outside a
// campaign, and optionsSettingsLabel the translation key it installs
// [07 R-FE-01 §7].
const (
	optionsMissionButton = "MISSION"
	optionsSettingsLabel = "Settings"
)

func battleFrameWithDiag(fs vfs.FSOps, g *formats.GAF, logical, name string) (*formats.GAFFrame, error) {
	f, err := battleFrame(g, name)
	if err != nil {
		return nil, hudAssetError(fs, logical, fmt.Sprintf("interface GAF missing %s [02 §6]", name), err)
	}
	return f, nil
}
