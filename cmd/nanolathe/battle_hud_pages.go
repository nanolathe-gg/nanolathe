package main

// Authored .GUI window resolution for the side rail: which window a committed
// command page selects, the generated numbered pages, and the frame each
// gadget draws [07 §6].

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/vfs"
)

// Two dead helpers stood here. buildPageCount counted authored <unit>N.GUI
// pages [07 §9]; nothing called it, and the page-count contract is locked in
// internal/content by TestBuildPageCountFollowsAuthoredPageWindowsAndDownloads.
// windowFor was a two-value wrapper over windowForRequired that discarded the
// construction error onto h.assetErr and returned nils, kept "to keep older
// inspection helpers source-compatible"; those helpers are gone and it had no
// callers.

// windowForRequired returns a selected authored page construction error to
// every presentation and input caller [07 §6][07 §9].
func (h *retailBattleHUD) windowForRequired(b *battleSession, f *frame.Frame) (*gui.Window, *formats.GAF, error) {
	// Cache resolved GUI/model once instead of reparsing on draw/click [ON-05 1]
	// Name selection is data-driven with paging: builder's page bits select guis/<unit><page>.gui [R-P0-03][07 §9] C10
	name := ""
	if h.side != nil {
		name = strings.ToLower(h.side.NamePrefix) + "gen"
	} else {
		name = "gen"
	}
	// [07 §6] "Command-window switch is closed": when the selected-unit count
	// becomes zero the switch closes the command windows down to the root
	// <prefix>MAIN2.GUI and opens nothing, so only the root shows through — no
	// command page is composed. The page close of [07 R-HUD-04 §3] runs on every
	// selection change and leaves the command window on top with nothing above
	// it. A frame that has not been committed yet carries no selection either,
	// and is the same closed state.
	if b == nil || f == nil || len(f.Selection.Handles) == 0 {
		return nil, nil, nil
	}
	// A multiple selection, or a single non-builder selection, formats and opens
	// <prefix>GEN.GUI; a single builder opens its authored page below [07 §6].
	if f.CommandPage.Builder == 0 {
		window, page := h.loadWindow(name)
		return window, page, nil
	}
	if b.cat == nil {
		// A nonzero builder with no catalog is malformed command-page state,
		// not the established empty-selection case.
		return nil, nil, nil
	}
	if f.CommandPage.PageCount == 0 {
		// A builder whose definition authors no page window has no build page
		// to open. That is the orders state — the side's "%sGEN.GUI", with
		// BUILD and ORDERS greyed on the "page count 0" arm of
		// [07 R-HUD-03 §6] — not an absent panel. Eight stock builders
		// (ARMASP/CORASP, ARMCARRY/CORCARRY, ARMDECOM/CORDECOM, ARMFARK,
		// CORNECRO) reach this, and returning no window left them showing
		// neither a build page nor an order palette.
		window, page := h.loadWindow(name)
		return window, page, nil
	}
	// The committed CommandPage identifies both the builder and page. No live
	// unit selection or synthesized view participates in GUI selection [I6].
	view, found := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !found || view.Owner != h.owner || !b.snapshotBuilder(view) {
		// A non-empty command-page identity is not an empty selection. Do not
		// display GEN for stale/malformed builder state [07 §9].
		return nil, nil, nil
	}
	// The switch reads the builder's page-shown bit first: page 0 — that bit
	// clear — is the orders state and opens the side's "%sGEN.GUI", and only a
	// page N >= 1 composes "%s%d.GUI" from the builder's internal name, so
	// ARMCOM1.GUI is page 1 [07 R-HUD-03 §6]. The published page count is the
	// maximum authored page plus one and page N carries entries (N-1)*6..N*6-1,
	// so both halves of the pair are reachable and every factory keeps a build
	// page. Before that count landed the orders state had no slot of its own and
	// this switch opened a page for every builder, which is why ORDERS drew
	// selected over a build page.
	def, ok := h.defFor(&view)
	if !ok || def == nil || !def.Builder {
		return nil, nil, nil
	}
	// Page 0 can also compose a page window, when the definition's word A bit 31
	// is set — written at definition load by probing guis/<internal name>0.GUI
	// [07 R-HUD-03 §6]. A census of the reference install's 375 guis/ entries
	// finds no such file (WU-17-13), so stock content never reaches that branch
	// and none is written here; a page-shown bit with a zero page field takes the
	// orders window instead of composing "<name>0.GUI".
	paged, pageNum := commandPageIsPaged(f), int(f.CommandPage.Page)
	name = commandWindowName(sideNamePrefix(h.side), def.UnitName, paged, pageNum)
	if !paged || pageNum == 0 {
		window, page := h.loadWindow(name)
		return window, page, nil
	}
	return h.numberedPage(name, f.CommandPage.GeneratedProducts)
}

// numberedPage opens an ordinary physical page when it has no generated
// placements. A physical page with placements is cloned and patched; an
// absent physical page is always cloned from the side's DL template, including
// when every authored product failed catalog resolution. An existing malformed
// page is still an error: only ErrNotFound selects DL [07 R-HUD-03 §6].
func (h *retailBattleHUD) numberedPage(name string, placements []frame.GeneratedProductPlacement) (*gui.Window, *formats.GAF, error) {
	if h == nil || name == "" {
		return nil, nil, nil
	}
	if cached := h.generatedWindows[name]; cached != nil {
		return cached, h.generatedPageArt[name], nil
	}
	physical := h.windows[name] != nil
	if !physical {
		logical := "guis/" + name + ".gui"
		if _, err := h.fs.Stat(logical); err == nil {
			physical = true
		} else {
			if !errors.Is(err, vfs.ErrNotFound) {
				return nil, nil, hudAssetError(h.fs, logical, "builder GUI probe "+name+" [07 R-HUD-03 §6]", err)
			}
		}
	}
	if physical && len(placements) == 0 {
		return h.loadWindowRequired(name)
	}
	sourceName := name
	if !physical {
		sourceName = strings.ToLower(sideNamePrefix(h.side)) + "dl"
	}
	source, sourceArt, err := h.loadWindowRequired(sourceName)
	if err != nil {
		return nil, nil, err
	}
	if source == nil {
		return nil, nil, hudAssetError(h.fs, "guis/"+sourceName+".gui", "generated build-page source "+sourceName+" [07 R-HUD-03 §6]", vfs.ErrNotFound)
	}
	window := cloneGUIWindow(source)
	for _, placement := range placements {
		// Stock generated pages have exactly six authored product slots at
		// gadget indexes BUTTON+4. Retain the authored byte and refuse values
		// outside that safe representable range; clamping would invent a slot
		// [07 §9][fmt tdf].
		if placement.Button >= hud.RetailBuildButtonsPerPage {
			hudAssetWarning(h.fs, "download/*.tdf", fmt.Sprintf("generated page %s product %s has invalid BUTTON %d [07 §9]", name, placement.ProductKey, placement.Button), fmt.Errorf("button outside six stock slots"))
			continue
		}
		index := int(placement.Button) + 4
		if index < 4 || index >= len(window.Gadgets) {
			hudAssetWarning(h.fs, "guis/"+sourceName+".gui", fmt.Sprintf("generated page %s has no gadget index %d for product %s [07 §9]", name, index, placement.ProductKey), fmt.Errorf("template does not expose authored slot"))
			continue
		}
		gad := &window.Gadgets[index]
		gad.Name = placement.ProductKey
		gad.Art = placement.ProductKey
		gad.GrayedOut = 0
		gad.CommonAttribs = 4
		if h.generatedProducts == nil {
			h.generatedProducts = make(map[string]bool)
		}
		h.generatedProducts[content.CanonicalKey(placement.ProductKey)] = true
	}
	if h.generatedWindows == nil {
		h.generatedWindows = make(map[string]*gui.Window)
	}
	h.generatedWindows[name] = window
	if h.generatedPageArt == nil {
		h.generatedPageArt = make(map[string]*formats.GAF)
	}
	h.generatedPageArt[name] = sourceArt
	return window, sourceArt, nil
}

func cloneGUIWindow(source *gui.Window) *gui.Window {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Gadgets = append([]gui.Gadget(nil), source.Gadgets...)
	for i := range clone.Gadgets {
		clone.Gadgets[i].Labels = append([]string(nil), source.Gadgets[i].Labels...)
	}
	return &clone
}

// sideNamePrefix is the side's authored nameprefix, the "%s" of "%sGEN.GUI"
// [07 R-HUD-03 §6]. A HUD built without a side resolves the bare name, which is
// what the empty-selection path above already does.
func sideNamePrefix(side *content.SideDef) string {
	if side == nil {
		return ""
	}
	return side.NamePrefix
}

// commandWindowName composes the window the command switch opens for a selected
// builder [07 R-HUD-03 §6]. With the page-shown bit clear — page 0, the orders
// state — that is the side's "%sGEN.GUI"; with it set, "%s%d.GUI" from the
// builder's own internal name and the page number, so page 1 is ARMCOM1.GUI.
// The returned name carries no extension: the loader appends it.
func commandWindowName(namePrefix, unitName string, paged bool, page int) string {
	if !paged || page <= 0 {
		return strings.ToLower(namePrefix) + "gen"
	}
	return fmt.Sprintf("%s%d", strings.ToLower(unitName), page)
}

func (h *retailBattleHUD) loadWindow(name string) (*gui.Window, *formats.GAF) {
	window, page, _ := h.loadWindowInternal(name, false)
	return window, page
}

// loadWindowProbe is used only while finding the contiguous authored page
// prefix. The first absent page is the established terminator and must not
// become a construction error [07 §9].
func (h *retailBattleHUD) loadWindowProbe(name string) (*gui.Window, *formats.GAF) {
	window, page, _ := h.loadWindowInternal(name, false)
	return window, page
}

// loadWindowRequired resolves a committed numbered builder page. Unlike a
// probe, a selected existing page reports a missing/malformed GUI and returns
// no usable window. Numbered page art remains optional because the established
// support-GAF and BUTTONS0 fallback chain supplies control frames [07 §6][07 §9].
func (h *retailBattleHUD) loadWindowRequired(name string) (*gui.Window, *formats.GAF, error) {
	return h.loadWindowInternal(name, true)
}

func (h *retailBattleHUD) loadWindowInternal(name string, required bool) (*gui.Window, *formats.GAF, error) {
	if h == nil || h.fs == nil || name == "" {
		return nil, nil, nil
	}
	if cached, ok := h.windows[name]; ok {
		if cached != nil {
			return cached, h.resolvePageArt(name), nil
		}
		return nil, nil, nil
	}
	window, err := gui.Load(h.fs, "guis/"+name+".gui")
	if err != nil {
		if required {
			return nil, nil, hudAssetError(h.fs, "guis/"+name+".gui", "builder GUI "+name+" [07 §9]", err)
		}
		return nil, nil, nil
	}
	if h.windows == nil {
		h.windows = make(map[string]*gui.Window)
	}
	h.windows[name] = window
	return window, h.resolvePageArt(name), nil
}

// resolvePageArt validates the page-specific GAF independently of the GUI
// cache. Missing or malformed page art is a normal null result: gadgetFrame
// then searches side/main support GAFs and common BUTTONS0 [07 §6].
func (h *retailBattleHUD) resolvePageArt(name string) *formats.GAF {
	if h == nil || h.side == nil || name == "" || name == strings.ToLower(h.side.NamePrefix)+"main" || name == strings.ToLower(h.side.NamePrefix)+"gen" {
		return nil
	}
	if h.pageChecked == nil {
		h.pageChecked = make(map[string]bool)
	}
	if h.pageChecked[name] {
		return h.pages[name]
	}
	h.pageChecked[name] = true
	if loaded, err := formats.LoadGAFFile(h.fs, "anims/"+name+".gaf"); err == nil {
		if h.pages == nil {
			h.pages = make(map[string]*formats.GAF)
		}
		h.pages[name] = loaded
		return loaded
	}
	if h.pages == nil {
		h.pages = make(map[string]*formats.GAF)
	}
	h.pages[name] = nil
	return nil
}

// gadgetArtEntry resolves a gadget's GAF entry through the authored lookup
// chain: the page's own GAF first, then the side interface GAF and the shared
// support GAFs [07 §4].
func (h *retailBattleHUD) gadgetArtEntry(gad gui.Gadget, page *formats.GAF) *formats.GAFEntry {
	name := gad.Art
	if name == "" {
		name = gad.Name
	}
	if page != nil {
		if found, ok := page.Find(name); ok {
			return found
		}
	}
	// A generated product first resolves anims/<product>_gadget.gaf entry
	// <product>; only then does it fall through to the generic interface/support
	// GAFs [fmt gaf][07 R-HUD-03 §6]. Both hits and misses are cached, so the
	// draw/click loop never reloads the VFS per frame.
	key := content.CanonicalKey(gad.Name)
	if h.generatedProducts[key] {
		if productGAF := h.generatedProductGAF(gad.Name); productGAF != nil {
			if found, ok := productGAF.Find(gad.Name); ok {
				return found
			}
		}
	}
	for _, g := range []*formats.GAF{h.intGAF, h.oldMain, h.share, h.common} {
		if g == nil {
			continue
		}
		if found, ok := g.Find(name); ok {
			return found
		}
	}
	return nil
}

func (h *retailBattleHUD) generatedProductGAF(product string) *formats.GAF {
	if h == nil || h.fs == nil {
		return nil
	}
	key := content.CanonicalKey(product)
	if h.productGAFChecked[key] {
		return h.productGAFs[key]
	}
	if h.productGAFChecked == nil {
		h.productGAFChecked = make(map[string]bool)
	}
	if h.productGAFs == nil {
		h.productGAFs = make(map[string]*formats.GAF)
	}
	h.productGAFChecked[key] = true
	logical := "anims/" + strings.ToLower(strings.TrimSpace(product)) + "_gadget.gaf"
	loaded, err := formats.LoadGAFFile(h.fs, logical)
	if err != nil {
		h.productGAFs[key] = nil
		return nil
	}
	h.productGAFs[key] = loaded
	return loaded
}

func (h *retailBattleHUD) gadgetFrame(gad gui.Gadget, page *formats.GAF, pressed, disabled bool) *formats.GAFFrame {
	entry := h.gadgetArtEntry(gad, page)
	stockButtons := false
	if entry == nil && gad.Kind == gui.KindButton && h.common != nil {
		entry, _ = h.common.Find("BUTTONS0")
		stockButtons = entry != nil
	}
	return selectGadgetFrame(entry, gad, pressed, disabled, stockButtons)
}

func selectGadgetFrame(entry *formats.GAFEntry, gad gui.Gadget, pressed, disabled, stockButtons bool) *formats.GAFFrame {
	if entry == nil || len(entry.Frames) == 0 {
		return nil
	}
	idx := 0
	if stockButtons {
		// BUTTONS0 is four frames per authored stock size. Pick the group
		// whose normal frame matches the .GUI rectangle, then apply the
		// retail normal/pressed stage within that group.
		best, bestScore := -1, int(^uint(0)>>1)
		for i, ref := range entry.Frames {
			if ref.Frame == nil {
				continue
			}
			score := absInt(int(ref.Frame.Width)-int(gad.Rect.W)) + absInt(int(ref.Frame.Height)-int(gad.Rect.H))
			if score < bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 {
			return nil
		}
		idx = (best / 4) * 4
		if disabled {
			idx += 2
		} else if pressed {
			idx++
		}
	} else if disabled && len(entry.Frames) > 2 {
		idx = 2
	} else if pressed && len(entry.Frames) > 1 {
		idx = 1
	}
	if idx >= len(entry.Frames) {
		idx = len(entry.Frames) - 1
	}
	return entry.Frames[idx].Frame
}

func (h *retailBattleHUD) guiColor(source byte) byte {
	if h != nil && h.pal != nil {
		return h.pal.GUIColor(source)
	}
	return source
}

func (h *retailBattleHUD) paletteIndex(logical byte) byte {
	if h != nil && h.pal != nil {
		return h.pal.Logical[logical]
	}
	return logical
}
