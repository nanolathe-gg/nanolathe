package main

// Retail battle HUD composition. This file deliberately contains no Nanolathe
// layout constants for controls: the side's SIDEDATA anchors, intgaf PANEL
// frames, and authored .GUI windows are the layout. The few fixed coordinates
// below are the retail panel-shell call sites recovered from TotalA.exe.

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

type retailBattleHUD struct {
	side    *content.SideDef
	cat     *content.Catalog
	owner   uint8
	anchors hud.Anchors

	console *formats.FNT
	guiFont *formats.FNT
	pal     *palette.Tables

	panelTop    *formats.GAFFrame
	panelSide   *formats.GAFFrame
	panelBottom *formats.GAFFrame
	intGAF      *formats.GAF
	common      *formats.GAF
	oldMain     *formats.GAF
	share       *formats.GAF
	logos       *formats.GAF
	optionsGAF  *formats.GAF
	optionsWin  *gui.Window
	exitWin     *gui.Window
	confirmWin  *gui.Window
	// screenW/screenH is the negotiated surface size the chrome is currently
	// laid out for; applyDisplaySize re-places the size-dependent windows when
	// it changes [07 R-HUD-05].
	screenW, screenH   int32
	modalFont          *formats.GAFEntry
	pausedFrame        *formats.GAFFrame
	victoryFrame       *formats.GAFFrame  // [07 §11] igvictory from anims/igtitles.gaf via intgaf/gui machinery
	defeatFrame        *formats.GAFFrame  // [07 §11] igdefeat from anims/igtitles.gaf
	resultWin          *gui.Window        // [07 §11] authored ENDMSN.GUI result surface
	resultGAF          *formats.GAF       // [07 §11] authored endmsn.gaf outcome controls
	resultVictoryFrame *formats.GAFFrame  // [07 §11] authored endmsn.gaf victory copy
	resultDefeatFrame  *formats.GAFFrame  // [07 §11] authored endmsn.gaf defeat copy
	resultPanel        *ui.Panel          // shared authored gesture state [07 §3]
	resultState        resultPresentation // ENDMSN dynamic bars/reveal state [08 R-CAMP-01 §7]

	fs    vfs.FSOps
	pages map[string]*formats.GAF
	// pageChecked is separate from the GUI cache: a probe may establish that a
	// page window exists before the selected page is drawn, but it must not make
	// the later art lookup disappear. A nil page is a valid result when the
	// authored support-GAF fallback chain supplies the control art [07 §6].
	pageChecked map[string]bool
	// Cache resolved GUI/model once instead of reparsing on draw/click [ON-05 1][R-P0-03]
	windows    map[string]*gui.Window
	pageCounts map[string]int
	// generatedWindows are cloned command pages patched from the committed
	// download-menu slot records. The unmodified authored page/template stays
	// in windows so another generated page can safely reuse it [07 R-HUD-03
	// §6][07 §9].
	generatedWindows  map[string]*gui.Window
	generatedPageArt  map[string]*formats.GAF
	generatedProducts map[string]bool
	productGAFs       map[string]*formats.GAF
	productGAFChecked map[string]bool

	// Retail refreshes the displayed production/consumption counters on a
	// one-second (30 tick) cadence while the stock bars remain live [07 §6].
	rateSampleTick uint32
	rateSample     frame.EconomyView
	rateSampleOK   bool

	// radar owns the presentation-only PICTURE→MAPPED→FINAL lifecycle. Its
	// inputs are rebuilt from the committed frame at draw time [03 §3.6].
	radar *render.MinimapService

	// FX radar markers are authored indexed GAF bytes. They are retained with
	// the battle HUD so FINAL can copy the selected frame directly, without
	// recoloring through the GUI palette [03 §3.9].
	radarBlipGAF      *formats.GAFEntry
	radarCommanderGAF *formats.GAFEntry
	radarFeatureGAF   *formats.GAFEntry

	// Retail's battle composer copies FINAL to the origin of the fixed 126-pixel
	// radar canvas; aspect letterbox is inside that canvas [07 §6][07 §10].
	minimapAnchor   hud.Rect
	minimapAnchorOK bool

	// assetErr records a required authored page failure encountered while
	// resolving a committed command page. It is never converted into the
	// side-general page: GEN is selected only for the explicitly empty
	// selection state [07 §6][07 §9].
	assetErr error
	// dispatchErr retains the last enqueue failure for diagnostics/tests. HUD
	// clicks remain consumed; no new retail status text is synthesized [01
	// §4.4][07 §3].
	dispatchErr error
	// factoryDispatch is a narrow test seam for the presentation boundary;
	// production calls battleSession.DispatchFactoryBuildDelta directly.
	factoryDispatch func(*battleSession, string, int) error

	// hoveredGadget is the battle window tree's hovered-gadget index, -1 when
	// none, and hoveredGadgetName its authored name. It is the footer's
	// highest-priority source [07 R-HUD-03 §1]; the gadget-tree pointer pass
	// sets it from the authored rectangle containing the pointer, and it
	// returns to -1 whenever no page is open or the pointer leaves every
	// gadget, which is also what a window change produces.
	// hoveredGadgetOK carries the -1 state without depending on a constructor:
	// the zero value of a freshly built HUD is "no gadget", exactly as the
	// tree-build reset leaves it.
	hoveredGadget     int
	hoveredGadgetName string
	hoveredGadgetOK   bool

	// score is the Space-held Kills/Losses panel's presentation state: the
	// slide word the composer steps once per composed frame, and the two
	// per-slot flash byte arrays [07 R-HUD-04 §1]. Both are presentation only
	// and are never read by the simulation [I6].
	score      hud.ScoreSlide
	scoreFlash hud.ScoreFlash
	// scoreFlashTick is the committed tick the flash arrays last decayed at.
	// The decay is one step per unit of the scaled timer, which is the tick
	// [07 R-HUD-04 §1][07 R-CAM-01 §10].
	scoreFlashTick   uint32
	scoreFlashTickOK bool
	// scorePrevKills and scorePrevLosses are the previous committed tick's
	// per-slot counters. The kill-credit finalize is the arming site in retail;
	// the counters it writes are what the committed frame carries, so an
	// increment between two committed ticks is that finalize having run
	// [07 R-HUD-04 §1 "Correction (2026-09-02)"][I6]. scoreCountersOK guards
	// the first frame, whose counters are a baseline and not a kill.
	scorePrevKills  [frame.PlayerRowSlots]int
	scorePrevLosses [frame.PlayerRowSlots]int
	scoreCountersOK bool
}

// hoveredGadgetSource reports the footer's first source: the hovered-gadget
// index and its authored name, or hud.NoGadget when the pointer is over no
// gadget [07 R-HUD-03 §1].
func (h *retailBattleHUD) hoveredGadgetSource() (int, string) {
	if h == nil || !h.hoveredGadgetOK {
		return hud.NoGadget, ""
	}
	return h.hoveredGadget, h.hoveredGadgetName
}

// updateHoveredGadget runs the gadget-tree pointer pass over the open command
// page: the hovered index is the gadget whose authored rectangle contains the
// pointer, and it is reset when no window is open [07 R-HUD-03 §1]. A greyed
// product slot is still hovered; its name simply does not resolve to a
// definition, so the card draws nothing [07 R-HUD-03 §3][07 R-HUD-03 §6].
func (h *retailBattleHUD) updateHoveredGadget(b *battleSession, f *frame.Frame, x, y int32) {
	if h == nil {
		return
	}
	h.hoveredGadget, h.hoveredGadgetName, h.hoveredGadgetOK = 0, "", false
	if b == nil {
		return
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return
	}
	if window == nil {
		return
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.Kind != gui.KindButton {
			continue
		}
		// A command button the aggregate hides is deactivated, and hidden
		// gadgets are skipped before the hit test [07 R-HUD-03 §6]
		// [07 R-WGT-01 §1]. A greyed one is not: the hovered-gadget writer has
		// no grey test, so a greyed button still fills the footer's first
		// source [07 R-HUD-03 §1].
		if command, isCommand := commandGadgetVerdict(gad, f, paged); isCommand && command.hidden {
			continue
		}
		// The rail's gadget rectangles are fixed in authored coordinates and
		// the §6 slide never translates them [07 R-HUD-05] (WU-19-223).
		r := window.PlacedRect(i)
		if !guiRectContains(r, x, y) {
			continue
		}
		h.hoveredGadget, h.hoveredGadgetName, h.hoveredGadgetOK = i, gad.Name, true
		return
	}
}

// LastDispatchError returns the last factory-command enqueue failure observed
// by the HUD, if any. It is a diagnostic surface, not retail status text.
func (h *retailBattleHUD) LastDispatchError() error {
	if h == nil {
		return nil
	}
	return h.dispatchErr
}

func (h *retailBattleHUD) dispatchFactoryBuild(b *battleSession, product string, count int) error {
	if h != nil && h.factoryDispatch != nil {
		return h.factoryDispatch(b, product, count)
	}
	return b.DispatchFactoryBuildDelta(product, count)
}

// hudFS is the read surface we need for HUD loads plus provider listing for diagnostics.
type hudFS interface {
	vfs.FSOps
	Providers() []vfs.ProviderInfo
}

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

func loadGUIOptional(fs vfs.FSOps, logical, ctx string) *gui.Window {
	w, err := gui.Load(fs, logical)
	if err != nil {
		hudAssetWarning(fs, logical, ctx, err)
		return nil
	}
	return w
}

func battleFrameWithDiag(fs vfs.FSOps, g *formats.GAF, logical, name string) (*formats.GAFFrame, error) {
	f, err := battleFrame(g, name)
	if err != nil {
		return nil, hudAssetError(fs, logical, fmt.Sprintf("interface GAF missing %s [02 §6]", name), err)
	}
	return f, nil
}

// loadRetailBattleHUD binds the same side-selected resources as the retail
// battle entry path. Mandatory assets are side, fonts, 30 anchors and core
// panel frames [02 §6][07 §6]; their absence fails before client creation with
// a provider-aware diagnostic (logical path + providers searched) [AGENTS.md §Diagnostics].
// Pause/options/exit/confirm/title resources are individually degradable when
// retail permits [07 §8][07 "Tab options menu and manual exit"]; missing
// cursor GAF is also degradable and retains the OS pointer [07 §8].
// Option/menu assets are resolved by side where established (ARM vs CORE) —
// CORE does not load ARM-specific options unconditionally [07 "Tab options menu and manual exit"].
// GUI/GAF loads are cached at battle entry in the HUD maps (windows/pages) so
// the frame loop does not re-read VFS [07 §4].
func loadRetailBattleHUD(fs vfs.FSOps, sess *session.Session, cat *content.Catalog, pal *palette.Tables, shell *gameShell) (*retailBattleHUD, error) {
	if fs == nil || sess == nil || cat == nil {
		return nil, fmt.Errorf("battle HUD: missing VFS, session, or catalog")
	}
	if pal == nil {
		return nil, fmt.Errorf("battle HUD: PALETTE.PAL tables are required [03 §4.3]")
	}
	side, err := battleSide(fs, sess, cat, shell)
	if err != nil {
		return nil, err
	}
	anchors, err := hud.AnchorsFromSide(side)
	if err != nil {
		logical := "gamedata/sidedata.tdf"
		return nil, hudAssetError(fs, logical, fmt.Sprintf("side %s missing anchor %s", side.Name, err), err)
	}
	if strings.TrimSpace(side.Font) == "" || strings.TrimSpace(side.FontGUI) == "" {
		logical := "gamedata/sidedata.tdf"
		return nil, hudAssetError(fs, logical, fmt.Sprintf("side %s has no console/gui font [02 §6]", side.Name), fmt.Errorf("missing font"))
	}
	// Mandatory fonts [02 §6][GAP T14] — fail with provider diagnostic.
	logicalConsole := "fonts/" + strings.ToLower(side.Font) + ".fnt"
	console, err := formats.LoadFNTFile(fs, logicalConsole)
	if err != nil {
		return nil, hudAssetError(fs, logicalConsole, fmt.Sprintf("side %s font %q [02 §6]", side.Name, side.Font), err)
	}
	logicalGUI := "fonts/" + strings.ToLower(side.FontGUI) + ".fnt"
	guiFont, err := formats.LoadFNTFile(fs, logicalGUI)
	if err != nil {
		return nil, hudAssetError(fs, logicalGUI, fmt.Sprintf("side %s GUI font %q [02 §6]", side.Name, side.FontGUI), err)
	}
	// Mandatory side intgaf [02 §6][07 §6].
	logicalIntGAF := "anims/" + strings.ToLower(side.IntGAF) + ".gaf"
	intGAF, err := formats.LoadGAFFile(fs, logicalIntGAF)
	if err != nil {
		return nil, hudAssetError(fs, logicalIntGAF, fmt.Sprintf("side %s intgaf %q [02 §6]", side.Name, side.IntGAF), err)
	}
	// Mandatory core panel frames [02 §6][07 §6].
	panelTop, err := battleFrameWithDiag(fs, intGAF, logicalIntGAF, "PANELTOP")
	if err != nil {
		return nil, err
	}
	panelSide, err := battleFrameWithDiag(fs, intGAF, logicalIntGAF, "PANELSIDE")
	if err != nil {
		return nil, err
	}
	panelBottom, err := battleFrameWithDiag(fs, intGAF, logicalIntGAF, "PANELBOT")
	if err != nil {
		return nil, err
	}
	// Optional support GAFs — degradable individually [07 §4][07 "Tab options menu and manual exit"].
	// Missing optional assets log a provider-aware warning and leave nil so battle still enters.
	common := loadGAFOptional(fs, "anims/commongui.gaf", "commongui.gaf")
	oldMain := loadGAFOptional(fs, "anims/oldmain.gaf", "oldmain.gaf")
	share := loadGAFOptional(fs, "anims/share.gaf", "share.gaf")
	logos := loadGAFOptional(fs, "textures/logos.gaf", "textures/logos.gaf")
	// The ESC options path is fixed to ARMOPT for every side; there is no
	// COROPT branch in the retail opener [07 §11]. Its support GAF is likewise
	// the ARMOPT root, and must never be selected from the local side prefix.
	// These modal resources are isolated optional bindings. The executable's
	// caller-level outcome for missing/malformed modal files is not established;
	// keep a nil window/GAF rather than converting that uncertainty into a
	// battle-entry failure or a fabricated modal [07 §11 "Missing and unknown"].
	optionsWin := loadGUIOptional(fs, "guis/armopt.gui", "options window [07 \"Tab options menu and manual exit\"]")
	optionsGAF := loadGAFOptional(fs, "anims/armopt.gaf", "options GAF [07 \"Tab options menu and manual exit\"]")
	exitWin := loadGUIOptional(fs, "guis/exitmenu.gui", "exitmenu.gui [07 \"Tab options menu and manual exit\"]")
	confirmWin := loadGUIOptional(fs, "guis/yesorno.gui", "yesorno.gui [07 \"Tab options menu and manual exit\"]")
	// Optional modal font — degradable [07 §4].
	var modalFont *formats.GAFEntry
	hattPath := "anims/hattfont12.gaf"
	if gaf, ferr := formats.LoadGAFFile(fs, hattPath); ferr == nil {
		if len(gaf.Entries) == 0 || len(gaf.Entries[0].Frames) == 0 {
			hudAssetWarning(fs, hattPath, "hattfont12.gaf has no glyph entry [07 §4]", fmt.Errorf("empty"))
		} else {
			modalFont = &gaf.Entries[0]
		}
	} else {
		hudAssetWarning(fs, hattPath, "hattfont12.gaf [07 §4]", ferr)
	}
	// Optional title art — degradable [07 §11]. Load via same intgaf/gui machinery as HUD panels [07 §6][07 §11].
	var pausedFrame, victoryFrame, defeatFrame *formats.GAFFrame
	titlesPath := "anims/igtitles.gaf"
	if gaf, ferr := formats.LoadGAFFile(fs, titlesPath); ferr == nil {
		if f, ferr2 := battleFrame(gaf, "igpaused"); ferr2 == nil {
			pausedFrame = f
		} else {
			hudAssetWarning(fs, titlesPath, "igtitles.gaf missing igpaused [07 §11]", ferr2)
		}
		if f, ferr2 := battleFrame(gaf, "igvictory"); ferr2 == nil {
			victoryFrame = f
		} else {
			hudAssetWarning(fs, titlesPath, "igtitles.gaf missing igvictory [07 §11]", ferr2)
		}
		if f, ferr2 := battleFrame(gaf, "igdefeat"); ferr2 == nil {
			defeatFrame = f
		} else {
			hudAssetWarning(fs, titlesPath, "igtitles.gaf missing igdefeat [07 §11]", ferr2)
		}
	} else {
		hudAssetWarning(fs, titlesPath, "igtitles.gaf [07 §11]", ferr)
	}
	// End-mission presentation is a separate authored frontend family. Keep
	// both records optional at battle entry: the title and result surface may be
	// absent from a development mount, but neither case permits a generated
	// replacement layout [07 §11][08 "Session end and reporting"].
	resultWin := loadGUIOptional(fs, "guis/endmsn.gui", "endmsn.gui [07 §11]")
	resultGAF := loadGAFOptional(fs, "anims/endmsn.gaf", "endmsn.gaf [07 §11]")
	var resultVictoryFrame, resultDefeatFrame *formats.GAFFrame
	if resultGAF != nil {
		if entry, ok := resultGAF.Find("victory"); ok && len(entry.Frames) != 0 {
			resultVictoryFrame = entry.Frames[0].Frame
		} else {
			hudAssetWarning(fs, "anims/endmsn.gaf", "endmsn.gaf missing victory copy [07 §11]", fmt.Errorf("missing authored entry"))
		}
		if entry, ok := resultGAF.Find("defeat"); ok && len(entry.Frames) != 0 {
			resultDefeatFrame = entry.Frames[0].Frame
		} else {
			hudAssetWarning(fs, "anims/endmsn.gaf", "endmsn.gaf missing defeat copy [07 §11]", fmt.Errorf("missing authored entry"))
		}
	}
	var resultPanel *ui.Panel
	if resultWin != nil {
		resultPanel = ui.NewPanel(resultWin)
		configureResultPanel(fs, sess, resultPanel)
	}
	h := &retailBattleHUD{
		side: side, cat: cat, owner: sess.LocalOwner, anchors: anchors, console: console, guiFont: guiFont, pal: pal,
		panelTop: panelTop, panelSide: panelSide, panelBottom: panelBottom,
		intGAF: intGAF, common: common, oldMain: oldMain, share: share, logos: logos,
		optionsGAF: optionsGAF, optionsWin: optionsWin, exitWin: exitWin, confirmWin: confirmWin,
		modalFont: modalFont, pausedFrame: pausedFrame, victoryFrame: victoryFrame, defeatFrame: defeatFrame,
		resultWin: resultWin, resultGAF: resultGAF, resultVictoryFrame: resultVictoryFrame, resultDefeatFrame: resultDefeatFrame, resultPanel: resultPanel,
		fs:                fs,
		pages:             make(map[string]*formats.GAF),
		windows:           make(map[string]*gui.Window),
		pageCounts:        make(map[string]int),
		generatedWindows:  make(map[string]*gui.Window),
		generatedPageArt:  make(map[string]*formats.GAF),
		generatedProducts: make(map[string]bool),
		productGAFs:       make(map[string]*formats.GAF),
		productGAFChecked: make(map[string]bool),
	}
	// The chrome is laid out for the authored 640x480 surface until the
	// composer sees the negotiated one [07 R-HUD-05].
	h.applyDisplaySize(retailScreenW, retailScreenH)
	// The empty-selection command page is the first page composed at battle
	// entry. Require its authored window now so a failed battle construction
	// cannot defer a missing GUI to a blank draw path. Numbered builder pages
	// are checked when their committed page is selected [07 §6][07 §9].
	if side.NamePrefix == "" {
		return nil, hudAssetError(fs, "gamedata/sidedata.tdf", "selected side has no prefix for the command GUI", fmt.Errorf("empty side prefix"))
	}
	if _, _, err := h.loadWindowRequired(strings.ToLower(side.NamePrefix) + "gen"); err != nil {
		return nil, err
	}
	picture := buildBattleRadar(fs, cat, sess.Skirmish.MapName, sess.World, pal)
	if picture == nil {
		// A battle HUD with no PICTURE cannot ever produce MAPPED or FINAL. Keep
		// this an entry-time content error instead of installing a service whose
		// later draw calls silently do nothing [03 §3.6][03 §3.7].
		logical := "maps/" + strings.ToLower(strings.TrimSpace(sess.Skirmish.MapName)) + ".tnt"
		if cat.Maps != nil {
			if mh := cat.Maps[content.CanonicalKey(sess.Skirmish.MapName)]; mh != nil && mh.LogicalTNT != "" {
				logical = mh.LogicalTNT
			}
		}
		return nil, hudAssetError(fs, logical, "radar picture unavailable [03 §3.7]", fmt.Errorf("neither authored TNT minimap nor generated terrain picture is valid"))
	}
	mapW, mapH := 0, 0
	if sess.World != nil {
		// MAPPED's source grid is the terrain's visibility-tile lattice. Keep
		// the HUD on authored frame/terrain dimensions instead of reading the
		// mutable visibility service during composition [03 §3.8][I6].
		mapW, mapH = int(sess.World.CellW/2), int(sess.World.CellH/2)
	}
	// MAPPED consumes the palette-install remap for explored-but-unseen cells
	// and the active logical fog index. Sensor callbacks are supplied from the
	// committed frame by rebuildRadar; the HUD never binds to mutable
	// visibility state [03 §3.4][03 §3.8].
	//
	// That remap is the gray table, not the GUI colour-field lookup: the
	// composite reads the same 256-byte grayscale-nearest LUT the main-view fog
	// overlay applies to fogged-but-explored terrain [03 §3.8 correction of
	// 2026-08-30][03 §3.3][03 §4.3.3]. GUIToBase resolves authored .GUI colour
	// fields and is not defined over image bytes, so feeding it terrain picture
	// indices painted explored terrain in unrelated interface colours instead
	// of desaturating it (playtest defect PT3-13).
	guiRemap := []byte(nil)
	fogFill := render.FogDarkPaletteIndex
	if pal != nil {
		guiRemap = pal.Gray[:]
		fogFill = pal.Logical[render.FogDarkPaletteIndex]
	}
	h.radar = render.NewMinimapService(render.MinimapServiceConfig{
		Picture: picture, MapW: mapW, MapH: mapH, LocalSlot: sess.LocalOwner,
		FogFill: fogFill, GUIRemap: guiRemap,
	})
	if fx := loadGAFOptional(fs, "anims/fx.gaf", "radar FX markers [03 §3.9]"); fx != nil {
		h.radarBlipGAF, _ = fx.Find("radlogohigh")
		h.radarCommanderGAF, _ = fx.Find("nuclogo")
		h.radarFeatureGAF, _ = fx.Find("h2oboom2")
	}
	h.minimapAnchor = hud.Rect{X1: 0, Y1: 0, X2: int32(camera.MinimapLongSide - 1), Y2: int32(camera.MinimapLongSide - 1)}
	h.minimapAnchorOK = true
	return h, nil
}

// buildBattleRadar installs the production radar picture from the same map
// asset that populated the session terrain. TNT's MiniMapPresent bit gates the
// authored bytes; render.BuildRadarPicture crops the stored bitmap's top-left
// used sub-rectangle (discarding the fill that pads its short axis on a
// non-square map, observed on 252×252 and 252×256 maps alike) before the
// bytes pass through the generic ALP source/destination path [fmt tnt][03 §3.7].
func buildBattleRadar(fs vfs.FSOps, cat *content.Catalog, mapName string, terrain *world.Terrain, pal *palette.Tables) *render.RadarSurface {
	if terrain == nil || pal == nil {
		return nil
	}
	playW, playH := terrain.PlayRight, terrain.PlayBottom
	layout := camera.LayoutMinimap(playW, playH)
	if layout.W <= 0 || layout.H <= 0 {
		return nil
	}
	var baked []byte
	var bakedW, bakedH int
	if fs != nil && cat != nil && cat.Maps != nil {
		if mh := cat.Maps[content.CanonicalKey(mapName)]; mh != nil && mh.LogicalTNT != "" {
			if data, err := fs.ReadFileLimit(mh.LogicalTNT, 32<<20); err == nil {
				if tnt, err := formats.LoadTNT(data); err == nil {
					w, h := int(tnt.MinimapWidth), int(tnt.MinimapHeight)
					if tnt.MiniMapPresent && w > 0 && h > 0 && len(tnt.Minimap) == w*h {
						baked, bakedW, bakedH = tnt.Minimap, w, h
					}
				}
			}
		}
	}
	return render.BuildRadarPicture(terrain, playW, playH, layout, baked, bakedW, bakedH, pal)
}

// applyDisplaySize lays the chrome out for a negotiated surface of w×h pixels
// [07 R-HUD-05]. Only the size-dependent windows need re-placing: EXITMENU and
// YESORNO are opened with the executable's "centre in the view" placement
// flag, so the window initializer replaces their authored origins with
// sentinels and centres them in the surface width left of the rail and in the
// full surface height, at the live size [07 "Tab options menu and manual
// exit"]. Everything else the composer derives from the surface size at draw
// time. Repeated calls at an unchanged size are no-ops.
func (h *retailBattleHUD) applyDisplaySize(w, height int) {
	if h == nil || w <= 0 || height <= 0 {
		return
	}
	if int32(w) == h.screenW && int32(height) == h.screenH {
		return
	}
	h.screenW, h.screenH = int32(w), int32(height)
	placeBattleModal(h.exitWin, w, height)
	placeBattleModal(h.confirmWin, w, height)
}

// placeBattleModal applies the established 0x1000 modal placement at the
// negotiated display size. The battle rail occupies x=0..127; modal centering
// therefore uses the remaining width and adds 128 [07 "Tab options menu and
// manual exit"][07 R-HUD-05].
func placeBattleModal(window *gui.Window, screenW, screenH int) {
	if window == nil {
		return
	}
	px, py := hud.ModalPlacement(int32(screenW), int32(screenH), window.Rect.W, window.Rect.H)
	x, y := int(px), int(py)
	window.Rect.X, window.Rect.Y = int32(x), int32(y)
	window.OriginX, window.OriginY = int32(x), int32(y)
	if len(window.Gadgets) != 0 {
		window.Gadgets[0].Rect.X = int32(x)
		window.Gadgets[0].Rect.Y = int32(y)
	}
}

func battleFrame(g *formats.GAF, name string) (*formats.GAFFrame, error) {
	if g == nil {
		return nil, fmt.Errorf("battle HUD: nil interface GAF while looking for %s", name)
	}
	e, ok := g.Find(name)
	if !ok || len(e.Frames) == 0 || e.Frames[0].Frame == nil {
		return nil, fmt.Errorf("battle HUD: interface GAF missing %s [02 §6]", name)
	}
	return e.Frames[0].Frame, nil
}

// battleSide resolves the side whose SIDEDATA anchors, fonts and interface GAF
// the battle HUD is built from.
//
// Skirmish retains the lobby's authored SIDE ordinal. A campaign mission has no
// lobby row: retail writes the registry `side` word (0 Arm, 1 Core) into the
// local player's side record when `SINGLE.GUI` opens, and the new-game panel
// then admits a campaign to the list only when its `[HEADER] campaignside`
// equals that side's name case-insensitively, or the literal `ALL`
// [08 "Enumeration of campaigns"][07 R-FE-01 §4]. The campaign file's
// `campaignside` is therefore the authored record of the side the mission is
// played as whenever it names one, and it is the only such record a session
// started from a campaign path carries. `shell` — the front end's own
// `missionSide` word, written by the new-game panel's Side0/Side1 gadgets —
// carries the registry side word for the one case a named campaign does not
// settle: `campaignside=ALL` [08 R-CAMP-01 §1].
//
// This replaces a scan of the local player's units for one whose `UnitName`
// matched a side's `Commander`. That was invented: nothing in retail derives
// the interface side from unit identity, and the scan simply failed on the Arm
// campaign's first mission (AC01), which gives the local player no commander at
// all — the HUD then refused to build with "local side -1 is unavailable".
func battleSide(fs vfs.FSOps, sess *session.Session, cat *content.Catalog, shell *gameShell) (*content.SideDef, error) {
	if cat == nil || len(cat.Sides) == 0 {
		return nil, fmt.Errorf("battle HUD: no compiled side definitions [02 §6]")
	}
	idx := 0
	if sess != nil {
		if sess.Mission != nil && sess.Mission.Type == mission.TypeCampaign {
			return campaignBattleSide(fs, sess.Mission, cat, shell)
		}
		owner := int(sess.LocalOwner)
		if owner >= 0 && owner < len(sess.Skirmish.Players) {
			idx = sess.Skirmish.Players[owner].Side
		}
	}
	if idx < 0 || idx >= len(cat.Sides) || cat.Sides[idx] == nil {
		return nil, fmt.Errorf("battle HUD: local side %d is unavailable [02 §6]", idx)
	}
	return cat.Sides[idx], nil
}

// campaignBattleSide resolves a campaign mission's interface side from the
// campaign file's `[HEADER] campaignside` name [08 "Enumeration of campaigns"].
//
// `campaignside` filters which campaigns the new-game panel offers; it never
// assigns a side. The side is decided first — the registry `side` word,
// rewritten by the Side0/Side1 gadgets — and a campaign that names a side is
// only ever offered to that one side, so the named value and the local side
// agree whenever a name is present [08 R-CAMP-01 §1]. `campaignside=ALL` is
// offered to both sides and settles nothing on its own; the local side for
// that case is the registry word carried here as `shell.missionSide`
// (frontend.go's field, written by the Side0/Side1 gadgets and already read
// from battle code as `b.shell.missionSide` — postbattle.go does the same for
// the between-missions summary) [07 R-HUD-04 §4].
func campaignBattleSide(fs vfs.FSOps, m *mission.Mission, cat *content.Catalog, shell *gameShell) (*content.SideDef, error) {
	logical := ""
	if m != nil {
		logical = m.CampaignPath
	}
	name, err := campaignSideName(fs, logical)
	if err != nil {
		return nil, err
	}
	if name == "" || name == "ALL" {
		if shell != nil {
			idx := shell.missionSide
			if idx >= 0 && idx < len(cat.Sides) && cat.Sides[idx] != nil {
				return cat.Sides[idx], nil
			}
		}
		return nil, hudAssetError(fs, logical,
			fmt.Sprintf("campaign HEADER campaignside naming a compiled side, got %q, and no local side word to fall back on [08 R-CAMP-01 §1]", name),
			fmt.Errorf("campaignside does not name a side"))
	}
	for _, side := range cat.Sides {
		if side != nil && strings.EqualFold(strings.TrimSpace(side.Name), name) {
			return side, nil
		}
	}
	return nil, hudAssetError(fs, logical,
		fmt.Sprintf("a compiled side named %q [02 §6]", name),
		fmt.Errorf("campaignside %q matches no compiled side", name))
}

// campaignSideName reads the campaign file's `[HEADER] campaignside` value,
// upper-cased and trimmed. An absent HEADER or key yields the empty string;
// only an unreadable or unparsable file is an error.
func campaignSideName(fs vfs.FSOps, logical string) (string, error) {
	if fs == nil || strings.TrimSpace(logical) == "" {
		return "", fmt.Errorf("battle HUD: campaign mission carries no campaign file path [08 \"Enumeration of campaigns\"]")
	}
	data, err := fs.ReadFileLimit(logical, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		return "", hudAssetError(fs, logical, "the campaign file naming the mission's side", err)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return "", hudAssetError(fs, logical, "a parsable campaign TDF", err)
	}
	if doc == nil || doc.Root == nil {
		return "", nil
	}
	header := doc.Root.Section("HEADER")
	if header == nil {
		return "", nil
	}
	side, _ := header.StringValue("campaignside", "")
	return strings.ToUpper(strings.TrimSpace(side)), nil
}

// blitBattlePanel mirrors TotalA's battle-shell call sites: the desired final
// pixel origin is converted to the raw GAF coordinate by adding the frame's
// authored offset, which UIBlitAnchor then subtracts [07 §6].
func blitBattlePanel(c *client.Client, f *formats.GAFFrame, x, y int) {
	if c == nil || f == nil {
		return
	}
	c.UIBlitAnchor(f, x+int(f.XOffset), y+int(f.YOffset))
}

// pauseOverlayVisible synchronizes the UI state from a committed frame only
// before any UI-issued scheduling transition. Once a pause intent is applied,
// the canonical UI truth drives the overlay immediately even if pausing leaves
// the committed tick unchanged [01 §4.3][07 §11][I6].
func pauseOverlayVisible(b *battleSession, committed *frame.Frame) bool {
	if b == nil {
		return false
	}
	state := b.battleState()
	if committed != nil {
		state.SyncCommittedPause(committed.Paused)
	}
	return state.Paused()
}

func (h *retailBattleHUD) draw(c *client.Client, b *battleSession, presented client.UIFrame) {
	if h == nil || c == nil {
		return
	}
	// Read the immutable presentation pair before composing world overlays. The
	// queue walker is deliberately fed only published state; Shift is the live
	// presentation gate and releasing it returns without constructing or
	// mutating any authoritative data [I6][07 §9][R-P0-11 §4].
	cur := presented.Committed
	frameOK := cur != nil
	paused := pauseOverlayVisible(b, cur)
	// World-space overlays come first, while the composed world is still the
	// whole surface: retail draws the build ghost and the order-queue overlay
	// into the battle view and only then blits the GUI frames over them, so a
	// marker near the map edge is covered by the chrome instead of drawing on
	// top of it [07 §9].
	if b != nil {
		b.drawBuildGhost(c)
		if frameOK && cur != nil {
			// The walker's four full-mask sources [R-P0-11 §3]: the follow
			// camera's tracked unit, the unit whose command page is open, the
			// hovered unit id, and every selected unit. The first and third are
			// presentation-owned — the camera block's tracked slot
			// [07 R-CAM-01 §12], and the very same hover word the footer's
			// first hover source reads [07 R-HUD-03 §1] — so they are read from
			// the shell here rather than from the committed frame; the second
			// and fourth come out of the frame inside the walker.
			//
			// The committed selection primary used to stand in for the hover
			// source, which made the overlay follow the selection instead of
			// the pointer. Do not consult the live unit pool or run the pointer
			// picker from here — that would breach I6; `footerHoverUnit` is the
			// composer's own per-frame pointer record.
			drawQueueOverlay(c, b, cur, cur.Tick, b.battleState().Input.ShiftHeld, cur.Selection.LocalPlayer, b.cam.Tracked(), b.footerHoverUnit)
		}
	}
	// The shell call order is PANELTOP, PANELBOT, PANELSIDE. All three panel
	// entries are static at their authored origins — PANELTOP (129,0),
	// PANELBOT (129,H-32), PANELSIDE (0,0) [07 §6 "Panel asset binding and
	// draw origins"].
	//
	// None of them takes the §6 slide offset, and neither do the rail's GUI
	// windows. WU-19-223 corrects the opposite reading: this composer used to
	// blit PANELSIDE at (0, offset) and translate every rail gadget rectangle
	// by the same word, so holding Space lifted the whole left rail — art and
	// buttons — 31 pixels up the screen. Retail moves nothing there.
	// [07 R-HUD-05] establishes it twice: the first paint stamps PANELSIDE at
	// (0,0) and "PANELSIDE is stamped here and nowhere else", and the
	// "what follows the surface" table files "every rail window and gadget
	// rectangle" under *fixed in authored coordinates*. What the offset
	// actually drives is the bottom slide strip: "it is not a side rail but
	// the strip that slides up from the bottom edge of the view when Space is
	// held" [07 R-HUD-03 §1 "the panel-slide gate"], drawn by drawSlideStrip
	// below at the arithmetic of [07 R-HUD-04 §4].
	//
	// At a display mode larger than 640x480 the chrome extends by rule, not
	// by scaling [07 R-HUD-05]: the bottom strip sits at the surface height
	// minus 32, and both horizontal strips are stamped rightward — the top one
	// with PANELTOP once and then the side's PANELBOT frame, the bottom one
	// with PANELBOT throughout — each stamp advancing by its frame width until
	// the running x reaches the surface width [07 R-HUD-03 §1][07 R-HUD-03 §4].
	// At 640x480 every stock frame reaches the edge in one stamp.
	screenW, screenH := c.Size()
	h.applyDisplaySize(screenW, screenH)
	blitBattlePanel(c, h.panelTop, hud.ChromeRailX, 0)
	if h.panelTop != nil && h.panelBottom != nil {
		stamps := hud.StripStamps(int32(screenW), int32(h.panelTop.Width), int32(h.panelBottom.Width))
		for _, x := range stamps[1:] {
			// PANELBOT is 33 rows tall against the top strip's 32, and retail
			// repaints the strip only when the resource snapshot changes, after
			// the world: its 33rd row lands on the viewport's first row on those
			// frames and the world takes the row back on the others. This
			// composer repaints the strip every frame and has no such
			// alternation, so the extension is clipped to the strip's 32 rows —
			// the state retail shows whenever the strip is at rest.
			c.UIBlitClipped(h.panelBottom, int(x), 0, int(x), 0, int(h.panelBottom.Width), hud.ChromeStripHeight)
		}
	}
	if h.panelBottom != nil {
		bottomY := int(hud.BottomStripY(int32(screenH)))
		for _, x := range hud.StripStamps(int32(screenW), int32(h.panelBottom.Width), int32(h.panelBottom.Width)) {
			blitBattlePanel(c, h.panelBottom, int(x), bottomY)
		}
	}
	blitBattlePanel(c, h.panelSide, 0, 0)
	// The left rail keeps its authored 129x480 art: retail stamps PANELSIDE
	// once at battle start onto a surface cleared to palette index 0 and never
	// extends it, so on a surface taller than the art the band under the panel
	// stays index 0 for the whole battle [07 R-HUD-05]. This composer draws the
	// world across the whole framebuffer first, so the band is painted here.
	// The band is measured from the art's authored 480 rows, which is now also
	// the panel's only position: the slide does not move it.
	if h.panelSide != nil {
		if gap, ok := hud.RailGap(int32(screenH), int32(h.panelSide.Width), int32(h.panelSide.Height)); ok {
			c.UIFillRect(int(gap.X1), int(gap.Y1), int(gap.X2-gap.X1+1), int(gap.Y2-gap.Y1+1), 0)
		}
	}
	// The hovered-gadget index is the footer's first source, so the pointer
	// pass over the open page runs before the footer draws [07 R-HUD-03 §1].
	if c.Input() != nil && c.Input().Mouse != nil {
		mouse := c.Input().Mouse
		h.updateHoveredGadget(b, cur, int32(mouse.X), int32(mouse.Y))
	}
	ok := false
	ok = cur != nil
	if ok && cur != nil {
		h.drawResources(c, cur)
		h.drawFooter(c, b, cur)
	}
	h.drawSidePage(c, b, cur)
	// Stock ARMINT.GAF and CORINT.GAF inspection shows PANELSIDE's decoded
	// 129×480 raster is opaque at every pixel, including the radar area; there
	// is no authored transparent cutout to preserve by clipping [fmt gaf].
	// Compose the radar after every moving-rail pass so the fixed 126×126 radar
	// canvas survives both the side frame and panel slide states. Modal/result
	// overlays remain later layers and may cover it transiently, without
	// mutating the cached FINAL surface [03 §3.6][07 §6].
	h.drawMinimap(c, b, cur)
	// The §6 slide strip's three readouts, drawn over the bottom strip while
	// the slide is off its closed detent [07 R-HUD-04 §4]. It is composed
	// after the footer and the minimap — retail paints those early and the
	// slide strip "much later", with only the network meter, the message
	// column, the developer overlays and the options unfold after it
	// [07 R-HUD-03 §14.4]. This call used to run before the footer, so the
	// footer's own bottom-strip fields painted over the strip (WU-19-223).
	if cur != nil {
		h.drawSlideStrip(c, b, cur)
	}
	if paused {
		h.drawPausedTitle(c)
	}
	// The Space-held Kills/Losses panel is composed after the world and the
	// chrome, and before the in-battle menus that can cover it
	// [07 R-HUD-04 §1].
	h.drawScorePanel(c, b, cur)
	h.drawBattleMenu(c, b)
	// The unit information screen is a child window over the battle
	// [07 R-HUD-03 §8].
	h.drawUnitInfo(c)
	if b != nil {
		b.drawStatusMessage(c, cur)
	}
	var result frame.ResultView
	if cur != nil {
		result = cur.Result
	}
	h.drawResultOverlay(c, b, result)
	// Last layer: the frontend save/load dialog and the shell's message box.
	// ARMOPT opens them over the battle and ENDMSN over the results surface,
	// so they sit above both [07 R-FE-01 §7][07 R-FE-01 §8].
	h.drawFrontendDialog(c, b)
}

// minimapRect returns the battle composer's fixed radar canvas rectangle.
func (h *retailBattleHUD) minimapRect() (hud.Rect, bool) {
	if h == nil || !h.minimapAnchorOK {
		return hud.Rect{}, false
	}
	left, top, right, bottom := h.minimapAnchor.Ordered()
	if right < left || bottom < top {
		return hud.Rect{}, false
	}
	return h.minimapAnchor, true
}

// radarMapPixel performs the retail signed high-word narrowing before radar
// projection. Keeping the narrowing explicit matters for map coordinates below
// zero and for values whose high word does not fit an int32 map coordinate [03
// §3.9].
func radarMapPixel(v numeric.Fixed) int32 {
	return int32(int16(int64(v) >> 16))
}

func radarGAFFrame(entry *formats.GAFEntry, index int) *formats.GAFFrame {
	if entry == nil || index < 0 || index >= len(entry.Frames) {
		return nil
	}
	return entry.Frames[index].Frame
}

func radarGAFFrameCount(entry *formats.GAFEntry) int {
	if entry == nil {
		return 0
	}
	return len(entry.Frames)
}

// blitRadarGAF copies an authored marker around its projected anchor. GAF
// pixels are already PALETTE.PAL indexes; transparent bytes are skipped and
// no GUI remap is applied [03 §3.9][fmt gaf].
func blitRadarGAF(dst *render.RadarSurface, anchorX, anchorY int32, f *formats.GAFFrame) {
	if dst == nil || f == nil {
		return
	}
	left, top := int(anchorX)-int(f.XOffset), int(anchorY)-int(f.YOffset)
	for y := 0; y < int(f.Height); y++ {
		for x := 0; x < int(f.Width); x++ {
			p, ok := f.At(x, y)
			if ok {
				dst.Set(left+x, top+y, p)
			}
		}
	}
}

func radarContactAdmitted(c render.MinimapContact, blink render.BlinkState) bool {
	// Player zero is a valid owner, but zero-valued unpublished records must
	// not become visible merely because the local player is also zero. The
	// authoritative visible/friendly bits cover genuine local-player contacts;
	// nonzero owner identity is the only owner bypass at this seam [03 §3.9].
	admit := c.Visible || c.Options&(1<<9) != 0 || c.MinimapMode&3 == 0 || c.Status&0x300 != 0 || c.Owner != 0 && c.Owner == c.LocalPlayer
	return admit && (c.BlinkSuppress == 0 || blink.Phase&1 != 0) && (!c.Stealth || blink.IsBlinkOn())
}

func radarPublishedContactVisible(c frame.RadarContactView, local uint8) bool {
	// Owner-local is a retail bypass, but a zero-valued feature/projectile
	// record is not evidence of ownership. Publisher visibility/friendly state
	// is authoritative for player zero [03 §3.9].
	return c.Visible || c.Status&0x300 != 0 || c.OwnerKnown && c.Owner == local
}

func radarContactRangeEnabled(c frame.RadarContactView) bool {
	// The publisher resolves the selected/range status and activation definition
	// gate before the frame boundary. Consume that immutable result directly;
	// presentation does not reconstruct it from mutable unit state [03 §3.9].
	return c.RangeStatus
}

func radarProjectileDot(c frame.RadarContactView) bool {
	return c.Kind == frame.RadarContactProjectile && c.Status&(1<<29|1<<30|0x40) == 0
}

func (h *retailBattleHUD) radarOwnerFrameIndex(contact frame.RadarContactView, frameCount int) int {
	if frameCount <= 0 || !contact.PaletteKnown || int(contact.Palette) >= frameCount {
		return -1
	}
	return int(contact.Palette)
}

// rebuildRadar consumes only the committed frame's radar payload. In
// particular, circles come from the published callback list and contacts are
// not reconstructed from the live unit or visibility services [03 §3.4][03
// §3.6][03 §3.9].
func (h *retailBattleHUD) rebuildRadar(b *battleSession, cur *frame.Frame, layout camera.Minimap) *render.RadarSurface {
	if h == nil || b == nil || b.sess == nil || h.radar == nil || cur == nil {
		return nil
	}
	// Consume only the committed phase. Presentation may redraw the same frame
	// repeatedly without advancing or otherwise owning the cadence [R-CORE-03]
	// [03 §3.6][I6].
	h.radar.SetBlinkPhase(cur.Radar.BlinkPhase)
	if cur.Visibility.Valid {
		h.radar.RebuildMapped(cur.Visibility.WordVisible, cur.Visibility.Visible)
	}
	// The committed contacts are the whole circle input: the sensor phase has no
	// surface of its own and rasterizes nothing [03 §3.10] correction of
	// 2026-08-29. FINAL is wiped from MAPPED and rebuilt from these records
	// every tick, so no stale circle can survive a frame.
	contacts := make([]render.MinimapContact, 0, len(cur.Radar.Contacts))
	regularArt := make([]*formats.GAFFrame, 0, len(cur.Radar.Contacts))
	commanderArt := make([]*formats.GAFFrame, 0, len(cur.Radar.Contacts))
	blink := h.radar.Blink()
	for _, published := range cur.Radar.Contacts {
		// The renderer's contact adapter owns the unit/commander/ring passes.
		// Projectile and feature records are applied below, after rings, in the
		// order required by the retail contacts pass [03 §3.9].
		if published.Kind != frame.RadarContactUnit {
			continue
		}
		// The selected-unit circle gate [03 §3.9]. Only a selected unit's
		// authored distances reach layer 4, and an on/off-capable one must also
		// be active; the publisher folded both terms before the frame boundary.
		rangeCircles := radarContactRangeEnabled(published)
		contact := render.MinimapContact{
			WorldX:      radarMapPixel(published.X),
			WorldZ:      radarMapPixel(published.Z),
			WorldY:      radarMapPixel(published.Y),
			Owner:       published.Owner,
			IsCommander: published.Commander, Stealth: published.Stealth,
			// RangeStatus is the selected-unit circle gate; Stealth stays separate
			// because it is a blip-blink term, not a circle term [03 §3.9].
			RangeStatus: rangeCircles, Status: published.Status,
			BlinkSuppress: published.BlinkSuppress, Visible: published.Visible,
			LocalPlayer:  cur.Selection.LocalPlayer,
			RawDistRadar: published.RadarDistance, RawDistSonar: published.SonarDistance,
			RawDistJamR: published.RadarJam, RawDistJamS: published.SonarJam,
			MinimapMode: b.minimapMaskWord(),
		}
		if radarContactAdmitted(contact, blink) {
			regularArt = append(regularArt, radarGAFFrame(h.radarBlipGAF, h.radarOwnerFrameIndex(published, radarGAFFrameCount(h.radarBlipGAF))))
			if contact.IsCommander {
				commanderArt = append(commanderArt, radarGAFFrame(h.radarCommanderGAF, 0))
			}
		}
		if len(published.Rings) != 0 {
			ring := published.Rings[0]
			contact.RingEnabled = ring.Enabled
			contact.RingDashed = ring.Dashed
			contact.RingRange = ring.Range
		}
		contacts = append(contacts, contact)
		for i := 1; i < len(published.Rings); i++ {
			ring := published.Rings[i]
			ringContact := render.MinimapContact{
				WorldX: contact.WorldX, WorldZ: contact.WorldZ, WorldY: contact.WorldY,
				Owner: contact.Owner, Status: contact.Status, Stealth: contact.Stealth,
				RangeStatus: contact.RangeStatus, BlinkSuppress: contact.BlinkSuppress,
				Visible: contact.Visible, LocalPlayer: contact.LocalPlayer, MinimapMode: contact.MinimapMode,
				RingEnabled: ring.Enabled, RingDashed: ring.Dashed, RingRange: ring.Range,
			}
			contacts = append(contacts, ringContact)
			if radarContactAdmitted(ringContact, blink) {
				regularArt = append(regularArt, nil)
			}
		}
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return nil
	}
	regularIndex, commanderIndex := 0, 0
	returnFinal := h.radar.RebuildFinal(layout, playW, playH, contacts, func(dst *render.RadarSurface, x, y int, p byte, commander bool) {
		if commander {
			if commanderIndex < len(commanderArt) {
				blitRadarGAF(dst, int32(x), int32(y), commanderArt[commanderIndex])
			}
			commanderIndex++
			return
		}
		if regularIndex < len(regularArt) {
			blitRadarGAF(dst, int32(x), int32(y), regularArt[regularIndex])
		}
		regularIndex++
	}, h.paletteIndex(10), h.paletteIndex(12), h.paletteIndex(15))
	if !returnFinal {
		return nil
	}
	final := h.radar.Final()
	if final == nil {
		return nil
	}
	// The projectile/feature pass follows rings. The published payload carries
	// the status and owner/visibility gates. Projectile dots use the dedicated
	// palette entry; other status selects the authored feature marker [03 §3.9].
	for _, published := range cur.Radar.Contacts {
		if published.Kind == frame.RadarContactUnit || !radarPublishedContactVisible(published, cur.Selection.LocalPlayer) {
			continue
		}
		rx, ry := render.RadarProjection(radarMapPixel(published.X), radarMapPixel(published.Z), radarMapPixel(published.Y), playW, playH, layout)
		if radarProjectileDot(published) {
			final.Set(int(rx), int(ry), h.paletteIndex(14))
			continue
		}
		if published.Kind == frame.RadarContactFeature || published.Kind == frame.RadarContactProjectile {
			index := h.radarOwnerFrameIndex(published, radarGAFFrameCount(h.radarFeatureGAF))
			blitRadarGAF(final, rx, ry, radarGAFFrame(h.radarFeatureGAF, index))
		}
	}
	return final
}

func (h *retailBattleHUD) drawMinimap(c *client.Client, b *battleSession, cur *frame.Frame) {
	if h == nil || c == nil || b == nil || b.sess == nil || b.cam == nil || cur == nil {
		return
	}
	layout, dst, ok := b.minimapLayout()
	if !ok {
		return
	}
	surf := h.rebuildRadar(b, cur, layout)
	if surf == nil {
		return
	}
	// Drawing and input receive the same layout and destination rectangle.
	c.DrawMinimapLayout(surf, dst, layout)
	// Then the viewport rectangle, exactly as retail's minimap repaint pre-pass
	// strokes the camera-to-radar rectangle over the copied radar surface
	// [03 R-MM-01 §1][03 R-COMP-02 §5]. The five-pixel cross that used to be
	// drawn here instead is a film-mode diagnostic the **world** composer draws
	// over the game viewport [03 §3.12] — a different figure.
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return
	}
	if marker, ok := hud.MinimapViewportRect(b.cam, layout, playW, playH, dst); ok {
		c.DrawMinimapViewportRect(dst, marker, h.paletteIndex(hud.ViewportMarkerLogicalColor))
	}
}

func (h *retailBattleHUD) drawPausedTitle(c *client.Client) {
	if h == nil || c == nil || h.pausedFrame == nil {
		return
	}
	w, height := c.Size()
	x := (w - int(h.pausedFrame.Width)) / 2
	y := (height - int(h.pausedFrame.Height)) / 2
	c.UIBlit(h.pausedFrame, x, y)
}

func (h *retailBattleHUD) drawBattleMenu(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil || b.battleState() == nil || b.battleState().Modal() == ui.BattleModalClosed {
		return
	}
	state := b.battleState()
	if state.HasOptionsLayer() {
		h.drawGUIWindow(c, h.optionsWin, h.optionsGAF, "")
	}
	if state.HasExitLayer() {
		h.drawGUIWindow(c, h.exitWin, nil, "")
	}
	if state.Modal() == ui.BattleModalConfirmMain {
		h.drawGUIWindow(c, h.confirmWin, nil, state.ConfirmTitle())
	} else if state.Modal() == ui.BattleModalConfirmExit {
		h.drawGUIWindow(c, h.confirmWin, nil, state.ConfirmTitle())
	}
}

// drawFrontendDialog paints the frontend panel stack's open child window over
// the battle and over the results surface.
//
// `ARMOPT` opens the two `LOADGAME.GUI` modes as child windows over whatever
// surface raised them, and `ENDMSN`'s `SaveGame`/`LoadGame` do the same over
// the results screen [07 R-FE-01 §7][07 R-FE-01 §8]. The dialog is therefore
// the last layer of the battle composition: above the options window it was
// opened from and above the result overlay. Until this call the dialog was
// driven but never painted, because the shell's own draw returns early in
// battle mode; the seam is the one battle_menu.go's opener records.
//
// The shell's authored `MSGBOX` follows it: the load direction's "no saved
// games" refusal is raised from inside a battle and must be visible there
// [07 R-FE-01 §9].
func (h *retailBattleHUD) drawFrontendDialog(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil || b.shell == nil {
		return
	}
	g := b.shell
	if g.saveLoadPanelActive() && saveLoadPanel != nil {
		g.drawRetailWindow(c, g.panelMode(saveLoadPanel), saveLoadPanel)
	}
	g.drawRetailModal(c)
}

// battleSessionKind is doc 08's session-kind word for the running battle: 1
// for a campaign mission, otherwise 2 (skirmish). Multiplayer (3) is out of
// scope for this build, so no path produces it [08 "Session kinds"].
func battleSessionKind(b *battleSession) uint8 {
	if b != nil && b.sess != nil && b.sess.Mission != nil && b.sess.Mission.Type == mission.TypeCampaign {
		return 1
	}
	return 2
}

// drawScorePanel is the Space-held Kills/Losses panel [07 R-HUD-04 §1].
//
// The composer draws it after the world and chrome and only when the session
// kind is 2 or 3; a campaign mission never calls it, so its slide word stays
// inert there. The slide word is stepped once per composed frame with no
// wall-clock throttle — unlike the §6 bottom strip — which is why the step
// lives in the composer rather than in the host-frame update.
func (h *retailBattleHUD) drawScorePanel(c *client.Client, b *battleSession, cur *frame.Frame) {
	if h == nil || c == nil || b == nil {
		return
	}
	// The flash arrays decay whether or not the panel is showing, one step per
	// unit of the scaled timer, and arm only while the F4 interface bit is set
	// [07 R-HUD-04 §1].
	h.stepScoreFlash(cur, b.panelHoldFlag)
	if !hud.ScoreSessionKindDraws(battleSessionKind(b)) {
		return
	}
	spaceHeld := false
	if c.Input() != nil && c.Input().Kbd != nil {
		spaceHeld = c.Input().Kbd.KeyHeld(input.KeySpace)
	}
	showing := hud.ScoreShowing(b.panelHoldFlag, spaceHeld, h.editorFocused())
	visible, cue := h.score.Step(showing)
	if cue != "" {
		b.playUICue(c, cue)
	}
	if !visible || cur == nil {
		return
	}
	width, _ := c.Size()
	// The player-count word is the number of occupied player slots, which the
	// committed frame publishes one economy row per [I6].
	rect := hud.ScorePanelGeometry(int32(width), h.score, len(cur.Economy))
	c.UIShadeRect(h.pal, int(rect.X0), int(rect.Y0), int(rect.X1-rect.X0), int(rect.Y1-rect.Y0), hud.ScorePanelShadeLevel)
	// The localised headings: `Kills` at (x0+2, 32), `Losses` right-aligned at
	// (x1 - textWidth - 2, 32), both at width limit 119 and light row 0.
	h.drawScoreText(c, "Kills", int(rect.X0)+2, hud.ScorePanelTop, 0)
	lossesHeading := "Losses"
	h.drawScoreText(c, lossesHeading, int(rect.X1)-retailGAFTextWidth(h.modalFont, lossesHeading)-2, hud.ScorePanelTop, 0)

	// Rows are emitted in rank order over the ten player slots the committed
	// frame publishes every tick, filtered on the row filter's six terms
	// [07 R-HUD-04 §1]. Both the filter and the rank scan with its vacated-rank
	// compaction live in internal/hud; this loop only paints what they return.
	// The compacted rank bytes are dropped: retail writes them back to the slot
	// records, and presentation may not write simulation state [I6]. With the
	// ranks published in slot order and the kill-lead maintenance of
	// [08 R-CAMP-01 §9] not implemented (the marker is on the publisher), no
	// frame presents a vacated rank to write back.
	slots := make([]hud.ScoreSlot, frame.PlayerRowSlots)
	for i := range cur.Players {
		row := cur.Players[i]
		slots[i] = hud.ScoreSlot{
			Present:    row.Present,
			Controller: row.Controller,
			Side:       row.Side,
			LiveUnits:  row.LiveUnits,
			Auxiliary:  row.Auxiliary,
			Watcher:    row.Watcher,
			Rank:       row.Rank,
		}
	}
	order, _ := hud.ScoreRowOrder(slots, len(cur.Economy))
	for drawn, slot := range order {
		h.drawScoreRow(c, b, cur, rect, drawn, slot, cur.Players[slot])
	}
}

// stepScoreFlash arms and then decays the two flash arrays, at most once per
// committed tick [07 R-HUD-04 §1][07 R-CAM-01 §10].
//
// Arming is the kill-credit finalize's, and it happens **only while the F4
// interface bit is set** — with the bit clear the finalize skips the arm and
// both arrays stay zero, so a Space-held panel shows steady numbers. That gate
// is the F4 bit's second visible effect, and it is why nothing armed these
// arrays before [07 R-HUD-04 §1 "Correction (2026-09-02)"][07 R-CAM-01 §14].
//
// The finalize's own writes are the per-slot kill and loss counters the
// committed frame carries, so an increment between two committed ticks is one
// or more credited kills at that slot. A commander kill increments the
// ordinary counter too, so the commander pair needs no separate watch even in
// Deathmatch, where the panel prints it. Arming precedes the decay because the
// finalize runs inside the tick and the panel routine decays afterwards.
func (h *retailBattleHUD) stepScoreFlash(cur *frame.Frame, armed bool) {
	if cur == nil {
		return
	}
	if h.scoreFlashTickOK && h.scoreFlashTick == cur.Tick {
		return
	}
	h.scoreFlashTick, h.scoreFlashTickOK = cur.Tick, true
	for i := range cur.Players {
		kills, losses := cur.Players[i].Kills, cur.Players[i].Losses
		if h.scoreCountersOK && armed {
			if kills > h.scorePrevKills[i] {
				h.scoreFlash.Credit(i, -1)
			}
			if losses > h.scorePrevLosses[i] {
				h.scoreFlash.Credit(-1, i)
			}
		}
		h.scorePrevKills[i], h.scorePrevLosses[i] = kills, losses
	}
	h.scoreCountersOK = true
	h.scoreFlash.Decay()
}

// drawScoreRow paints one player's row: the local player's two lightening
// passes, the player name, and the kill and loss counts with their flash
// brightness [07 R-HUD-04 §1].
func (h *retailBattleHUD) drawScoreRow(c *client.Client, b *battleSession, cur *frame.Frame, rect hud.ScorePanelRect, drawn, slot int, row frame.PlayerRow) {
	y := hud.ScoreRowTop(drawn)
	if slot >= 0 && slot < hud.ScorePanelSlots && uint8(slot) == cur.Selection.LocalPlayer {
		// (x0+4, y-1)-(x1-4, y+38), lightened at 31 then 20.
		x, w := int(rect.X0)+4, int(rect.X1-rect.X0)-8
		top, height := int(y)-1, 40
		c.UILightRect(h.pal, x, top, w, height, hud.ScorePanelLocalLightA)
		c.UILightRect(h.pal, x, top, w, height, hud.ScorePanelLocalLightB)
	}
	// The row's side logo: the frame numbered by the lobby record's logo byte,
	// quad mapped from the frame *interior* — source corners (1,1) (w-1,1)
	// (w-1,h-1) (1,h-1) — onto (x0+7, y+1)-(x0+119, y+37), i.e. stretched to
	// 112 x 36 [07 R-HUD-04 §1][03 R-RAST-01 §1]. The entry is `32xlogos` of
	// textures/logos.gaf [07 R-HUD-04 §4], which is the same handle the
	// footer's LOGO2 draw and the result surface read.
	if logo := h.sideLogoFrame(row.Logo); logo != nil {
		width, height := c.Size()
		c.UIBlitFrameSourceRectScaledClipped(logo, 1, 1, int(logo.Width)-1, int(logo.Height)-1,
			int(rect.X0)+7, int(y)+1, hud.ScorePanelLogoWidth, hud.ScorePanelLogoHeight,
			0, 0, width, height)
	}
	h.drawScoreText(c, row.Name, int(rect.X0)+9, int(y)+6, 0)
	kills, losses := hud.ScoreCounters(row, commanderDeathOption(b))
	killText := fmt.Sprintf("%d", kills)
	lossText := fmt.Sprintf("%d", losses)
	killShade, lossShade := 0, 0
	if slot >= 0 && slot < hud.ScorePanelSlots {
		killShade = int(h.scoreFlash.Kills[slot])
		lossShade = int(h.scoreFlash.Losses[slot])
	}
	h.drawScoreText(c, killText, int(rect.X0)+9, int(y)+21, killShade)
	h.drawScoreText(c, lossText, int(rect.X0)+119-retailGAFTextWidth(h.modalFont, lossText)-2, int(y)+21, lossShade)
}

// sideLogoFrame resolves the side-logo frame for a lobby colour byte. Both
// battle logo draws — the footer's LOGO2 and the score panel's row logo — read
// one GAF handle bound during battle-data initialization, and the frame index
// is the owner's lobby colour byte:
//
//	frame = logos.gaf["32xlogos"].Frames[lobbyColour]
//
// [07 R-HUD-04 §4]. An absent file or an out-of-range colour draws nothing;
// the art is retail content, and this seam does not substitute for it.
func (h *retailBattleHUD) sideLogoFrame(colour uint8) *formats.GAFFrame {
	if h == nil || h.logos == nil {
		return nil
	}
	entry, ok := h.logos.Find(sideLogoEntry)
	if !ok || int(colour) >= len(entry.Frames) {
		return nil
	}
	return entry.Frames[colour].Frame
}

// sideLogoEntry is the GAF entry both battle logo draws address
// [07 R-HUD-04 §4].
const sideLogoEntry = "32xlogos"

// drawScoreText is the panel's text writer: the GAF font, the 119-pixel width
// limit, and the light-table row as the brightness argument
// [07 R-HUD-04 §1][03 R-FONT-01 §6].
func (h *retailBattleHUD) drawScoreText(c *client.Client, text string, x, y, shade int) {
	if text == "" || h.modalFont == nil {
		return
	}
	drawRetailGAFTextLit(c, h.modalFont, text, x, y, hud.ScorePanelTextLimit, h.pal, shade)
}

// commanderDeathOption is the session's commander-death option word. Value 2
// is Deathmatch, which swaps the panel's counters for the commander pair. It
// is immutable session setup, chosen in the lobby before the battle starts
// [07 R-HUD-04 §1][07 R-FE-01 §7][08 R-SKIR-01 §3].
func commanderDeathOption(b *battleSession) int {
	if b == nil || b.sess == nil {
		return 0
	}
	return b.sess.Skirmish.CommanderDeath
}

// drawGUIWindow composes an authored modal into its retail private surface.
// Every write is clipped to the window rectangle before that surface is
// presented, and buttons take the runtime dimensions of their selected art.
func (h *retailBattleHUD) drawGUIWindow(c *client.Client, window *gui.Window, page *formats.GAF, title string) {
	if window == nil {
		return
	}
	clip := window.Rect
	h.drawWindowBackground(c, window, page)
	for i, gad := range window.Gadgets {
		active := gad.Active != 0
		if window == h.resultWin && h.resultPanel != nil {
			active = h.resultPanel.ActiveOf(gad.Name)
		}
		if i == 0 || !active || gad.Kind == gui.KindFont || gad.Kind == gui.KindPanel {
			continue
		}
		r := h.modalGadgetRect(window, i, page)
		pressed := false
		if c.Input() != nil && c.Input().Mouse != nil && c.Input().Mouse.Held(input.MouseButtonLeft) {
			pressed = guiRectContains(r, int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y))
		}
		frame := h.modalGadgetFrame(gad, page, pressed, gad.GrayedOut != 0)
		if frame != nil {
			if modalArtResampled(gad.Kind, frame, r) {
				c.UIBlitFrameScaledClipped(frame, int(r.X), int(r.Y), int(r.W), int(r.H), int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
			} else {
				c.UIBlitClipped(frame, int(r.X), int(r.Y), int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
			}
		}
		text := gad.Text
		if strings.EqualFold(gad.Name, "TITLE") && title != "" {
			text = title
		} else if gad.Kind == gui.KindButton && len(gad.Labels) != 0 {
			text = gad.Labels[0]
		}
		if text != "" && h.modalFont != nil && (gad.Kind == gui.KindButton || gad.Kind == gui.KindLabel) {
			textWidth := retailGAFTextWidth(h.modalFont, text)
			x := int(r.X)
			switch {
			case gad.Attribs&1 != 0:
				x += 3
			case gad.Attribs&4 != 0:
				x = int(r.X+r.W) - textWidth - 3
				if x < int(r.X) {
					x = int(r.X)
				}
			case gad.Attribs&2 != 0:
				x += (int(r.W)-1-textWidth)/2 + 1
			default:
				x += 3
			}
			if pressed {
				x += boolInt(gad.Attribs&1 != 0 || gad.Attribs&2 != 0)
			}
			y := retailTextPenY(gad, r, retailGAFTextHeight(h.modalFont))
			drawRetailGAFTextClipped(c, h.modalFont, text, x, y, int(r.W), int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
		}
	}
}

// modalArtResampled reports whether a modal gadget's selected frame is
// texture-mapped onto its authored rectangle rather than stamped at the
// translated gadget origin.
//
// Retail resamples in exactly one place: a blank surface (kind 6) whose frame
// is raw. That renderer branches on the frame's `Compressed` byte — an RLE
// frame is stamped once at the gadget origin, a raw frame is texture-mapped
// through the four-corner blitter with the destination spanning
// `(x,y)..(x+w-1,y+h-1)` and the source spanning `(0,0)..(frameW-1,frameH-1)`,
// which is what makes skirmish's raw 32x32 `logos.gaf` team colours fit their
// authored 20x20 records. Every other control stamps: a button's runtime
// dimensions simply become its selected frame's, and a picture box (kind 12)
// "blits its frame" [07 "Retail frontend control activation and raster rules"]
// [07 R-WGT-01 §8].
//
// This used to resample every non-button gadget whose art did not match its
// authored rectangle. That was invented, and it was the in-battle pause menu's
// visible defect: `ARMOPT.GUI`'s `OPTBG` picture box is authored 128x362 while
// the RLE frame behind it is 128x354, so the panel plate was stretched eight
// rows taller than the art and each recess drifted progressively down the rail
// away from the button meant to sit in it — up to seven pixels by `Resume`.
// The drift is authored-size business and has nothing to do with the display
// mode; it was equally wrong at 640x480.
func modalArtResampled(kind gui.Kind, frame *formats.GAFFrame, r gui.Rect) bool {
	if frame == nil || kind != gui.KindSurface || frame.Compressed != 0 {
		return false
	}
	return int32(frame.Width) != r.W || int32(frame.Height) != r.H
}

func (h *retailBattleHUD) drawWindowBackground(c *client.Client, window *gui.Window, page *formats.GAF) {
	if window == nil || window.Rect.W <= 0 || window.Rect.H <= 0 {
		return
	}
	frame := h.modalArtFrame(window.Header.Panel, page)
	if frame == nil {
		// YESORNO.GUI has no usable PANEL value. Retail does not treat that as
		// a transparent dialog: the initializer makes one final literal BackTile
		// lookup after the file-specific and GUI-context searches.
		frame = h.modalArtFrame("BackTile", page)
	}
	if frame == nil || frame.Width == 0 || frame.Height == 0 {
		return
	}
	for y := int(window.Rect.Y); y < int(window.Rect.Y+window.Rect.H); y += int(frame.Height) {
		for x := int(window.Rect.X); x < int(window.Rect.X+window.Rect.W); x += int(frame.Width) {
			c.UIBlitClipped(frame, x, y, int(window.Rect.X), int(window.Rect.Y), int(window.Rect.W), int(window.Rect.H))
		}
	}
}

func (h *retailBattleHUD) modalArtFrame(name string, page *formats.GAF) *formats.GAFFrame {
	if name == "" {
		return nil
	}
	for _, gaf := range []*formats.GAF{page, h.intGAF, h.oldMain, h.common} {
		if gaf == nil {
			continue
		}
		if entry, ok := gaf.Find(name); ok && len(entry.Frames) != 0 && entry.Frames[0].Frame != nil {
			return entry.Frames[0].Frame
		}
	}
	return nil
}

// modalGadgetRect returns the runtime rectangle installed by the modal
// initializer.
// Stock button selection replaces the authored width/height with the chosen
// frame dimensions (YESORNO's 95x20 choices therefore become 96x20).
func (h *retailBattleHUD) modalGadgetRect(window *gui.Window, index int, page *formats.GAF) gui.Rect {
	if window == nil || index < 0 || index >= len(window.Gadgets) {
		return gui.Rect{}
	}
	r := window.PlacedRect(index)
	gad := window.Gadgets[index]
	if gad.Kind == gui.KindButton {
		if frame := h.modalGadgetFrame(gad, page, false, gad.GrayedOut != 0); frame != nil {
			r.W = int32(frame.Width)
			r.H = int32(frame.Height)
		}
	}
	return r
}

func (h *retailBattleHUD) modalPage(window *gui.Window) *formats.GAF {
	if h != nil && window == h.optionsWin {
		return h.optionsGAF
	}
	return nil
}

func (h *retailBattleHUD) modalButtonAt(window *gui.Window, x, y int32) int {
	if h == nil || window == nil {
		return -1
	}
	page := h.modalPage(window)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Kind != gui.KindButton || gad.Active == 0 || gad.GrayedOut != 0 {
			continue
		}
		if guiRectContains(h.modalGadgetRect(window, i, page), x, y) {
			return i
		}
	}
	return -1
}

// modalGadgetFrame resolves modal control art through [07 §4]'s three-link
// chain: the gadget's own named entry in the window's own GAF, then the
// side-specific interface GAF, then the built-in fallback (the common GUI
// stock controls, and BUTTONS0 for a button).
//
// The middle link used to be missing here — the one art chain in the shell
// that skipped it, while the window background [07 §4] and the side page
// [07 §6] both already walked `page → intGAF → … → common`. The side GAF is
// resolved through `content.SideDef.IntGAF` [02 §6] and is the same handle the
// three battle panel frames come from, so a modal control the side authors
// (rather than the stock common set) now resolves instead of falling through
// to the generic button plate.
//
// Side-*page* GAFs are still not consulted: they carry unrelated entries with
// colliding names (notably EXIT) and are not part of ARMOPT's retail binding.
func (h *retailBattleHUD) modalGadgetFrame(gad gui.Gadget, page *formats.GAF, pressed, disabled bool) *formats.GAFFrame {
	name := gad.Art
	if name == "" {
		name = gad.Name
	}
	for _, gaf := range []*formats.GAF{page, h.intGAF, h.common} {
		if gaf == nil {
			continue
		}
		if entry, ok := gaf.Find(name); ok {
			return selectGadgetFrame(entry, gad, pressed, disabled, false)
		}
	}
	if gad.Kind == gui.KindButton && h.common != nil {
		if entry, ok := h.common.Find("BUTTONS0"); ok {
			return selectGadgetFrame(entry, gad, pressed, disabled, true)
		}
	}
	return nil
}

func (h *retailBattleHUD) drawResources(c *client.Client, f *frame.Frame) {
	var res *frame.EconomyView
	for i := range f.Economy {
		if f.Economy[i].Player == h.owner {
			res = &f.Economy[i]
			break
		}
	}
	if res == nil {
		return
	}
	if !h.rateSampleOK || f.Tick < h.rateSampleTick || f.Tick-h.rateSampleTick >= 30 {
		h.rateSampleTick = f.Tick
		h.rateSample = *res
		h.rateSampleOK = true
	}
	rates := &h.rateSample
	energy := h.side.EnergyColor
	metal := h.side.MetalColor
	if energy < 0 || energy > 255 {
		energy = 0
	}
	if metal < 0 || metal > 255 {
		metal = 0
	}
	energyBar, _ := h.anchors.ByIndex(hud.AnchorEnergyBar)
	metalBar, _ := h.anchors.ByIndex(hud.AnchorMetalBar)
	h.drawResourceBar(c, energyBar, hud.ResourceFraction(res.Energy, res.EnergyCapacity), byte(energy))
	h.drawResourceBar(c, metalBar, hud.ResourceFraction(res.Metal, res.MetalCapacity), byte(metal))
	h.drawNumber(c, hud.AnchorEnergyNum, float32(res.Energy))
	h.drawNumber(c, hud.AnchorMetalNum, float32(res.Metal))
	h.drawNumberRight(c, hud.AnchorEnergyMax, res.EnergyCapacity)
	h.drawNumberRight(c, hud.AnchorMetalMax, res.MetalCapacity)
	h.drawTextAt(c, hud.AnchorEnergy0, "0", h.guiColor(15))
	h.drawTextAt(c, hud.AnchorMetal0, "0", h.guiColor(15))
	h.drawTextAt(c, hud.AnchorEnergyProduced, hud.FormatEnergyProduced(rates.EnergyProduced), h.guiColor(10))
	h.drawTextAt(c, hud.AnchorEnergyConsumed, hud.FormatEnergyConsumed(rates.EnergyConsumed), h.guiColor(12))
	h.drawTextAt(c, hud.AnchorMetalProduced, hud.FormatMetalProduced(rates.MetalProduced), h.guiColor(10))
	h.drawTextAt(c, hud.AnchorMetalConsumed, hud.FormatMetalConsumed(rates.MetalConsumed), h.guiColor(12))
}

func (h *retailBattleHUD) drawResourceBar(c *client.Client, r hud.Rect, fraction float32, inner byte) {
	left, top, right, bottom := r.Ordered()
	if right <= left || bottom <= top {
		return
	}
	filled := int(float32(right-left) * fraction)
	if filled <= 0 {
		return
	}
	c.UIFillRect(int(left), int(top), filled, int(bottom-top), inner)
}

func (h *retailBattleHUD) drawNumber(c *client.Client, index int, value float32) {
	r, ok := h.anchors.ByIndex(index)
	if !ok {
		return
	}
	h.drawNumberAtPoint(c, r.X1, r.Y1, value)
}

func (h *retailBattleHUD) drawNumberRight(c *client.Client, index int, value float32) {
	r, ok := h.anchors.ByIndex(index)
	if !ok {
		return
	}
	text := fmt.Sprintf("%d", int(value))
	x := r.X1 - int32(client.MeasureText(h.console, text))
	c.UIText(h.console, text, int(x), int(r.Y1), h.guiColor(15))
}

func (h *retailBattleHUD) drawNumberAtPoint(c *client.Client, x, y int32, value float32) {
	// The retail resource display is an integer text field; the authoritative
	// stock remains float32, and conversion here truncates toward zero [01 §8].
	c.UIText(h.console, fmt.Sprintf("%d", int(value)), int(x), int(y), h.guiColor(15))
}

func (h *retailBattleHUD) drawTextAt(c *client.Client, index int, text string, color byte) {
	r, ok := h.anchors.ByIndex(index)
	if !ok || text == "" {
		return
	}
	c.UIText(h.console, text, int(r.X1), int(r.Y1), color)
}

func formatEnergyRate(value float32) string {
	return hud.FormatEnergyRate(value)
}

// The unit count and the game clock were drawn here, at the TOTALUNITS and
// TOTALTIME anchors. That was our own defect, not authored data: the side
// anchor block has exactly two consumers, the top-strip painter and the
// footer, and TOTALUNITS/TOTALTIME are "loaded, never read"
// [07 R-HUD-03 §5]. Stock ARM authors TOTALTIME at (605,7) and
// ENERGYPRODUCED at (609,5), so painting the clock there overlapped the
// energy production reading and partly occluded it. The running display
// belongs to the Space-held slide strip, whose three translated lines are
// `Game Time:` as hh:mm:ss, `Total Units: %d (Max %d)` and `Game Speed: %s%s`
// [07 R-HUD-03 §6].
//
// The slide strip's text placement is now established [07 R-HUD-04 §4]: with
// `x` the composer surface rectangle's left edge, `yBottom` its bottom edge
// and `off` the slide offset (-31..0, drawn only while non-zero), the three
// strings are written on one line at `yBottom + off + 10` — `Game Time:` at
// `x + 25`, `Total Units:` at `x + 190`, `Game Speed:` at `x + 380` — in the
// default font at light-table row 0. drawSlideStrip below is that draw; the
// strip used to paint its art with no text at all.

// Slide-strip text offsets [07 R-HUD-04 §4]. `x` is the composer surface
// rectangle's left edge and `yBottom` its bottom edge.
const (
	slideStripTimeX  = 25
	slideStripUnitsX = 190
	slideStripSpeedX = 380
	slideStripTextY  = 10
	// slideStripNormalSpeed is the speed word at which the line prints the
	// localized normal word instead of an offset [07 §6][07 R-CAM-01 §3].
	slideStripNormalSpeed = 10
)

// drawSlideStrip writes the §6 strip's three readouts. The strip is drawn only
// while the slide offset is non-zero — at 0 it is off screen — and every string
// sits on one line at `yBottom + off + 10` [07 R-HUD-04 §4][07 §6].
//
// The composer steps this strip in every session kind, unlike the Space-held
// score panel of [07 R-HUD-04 §1], and its show test is Space unless a text
// editor has the focus. That test is the rail state this build already owns, so
// the offset is read rather than recomputed.
//
// This is the offset's ONE consumer. It is not a side rail: "it is not a side
// rail but the strip that slides up from the bottom edge of the view when Space
// is held" [07 R-HUD-03 §1 "the panel-slide gate"], and neither PANELSIDE nor
// any rail window or gadget rectangle moves with it [07 R-HUD-05].
//
// TODO(question): [07 R-HUD-04 §4] says "the strip art is blitted at
// (x, yBottom + off)" without naming which GAF entry that art is, and no other
// section names it, so only the three text readouts are drawn here and the
// bottom strip's own PANELBOT backdrop shows through behind them. A trace of
// the composer's slide-strip draw naming the cached entry the blit reads — or a
// retail capture of the Space-held bottom band next to the parked one — would
// settle it. Note also that [07 §6]'s "Panel slide" paragraph labels -31
// "parked" and 0 "fully visible", which is the reverse of what [07 R-HUD-04 §4]
// ("drawn only while non-zero") and [07 R-HUD-03 §1] ("slides up ... when Space
// is held", and Space held drives toward -31) establish; the arithmetic in both
// readings agrees, only the two labels disagree, and this code follows the two
// later closures.
func (h *retailBattleHUD) drawSlideStrip(c *client.Client, b *battleSession, cur *frame.Frame) {
	if h == nil || c == nil || b == nil || cur == nil || h.console == nil {
		return
	}
	off := int(b.battleState().PanelOffset)
	if off == 0 {
		return
	}
	_, height := c.Size()
	y := height + off + slideStripTextY
	write := func(x int, text string) {
		c.UIText(h.console, text, x, y, h.guiColor(0))
	}
	write(slideStripTimeX, "Game Time: "+retailSummaryTime(int32(cur.Tick)))
	live := 0
	if slot := int(cur.Selection.LocalPlayer); slot >= 0 && slot < len(cur.Players) {
		live = cur.Players[slot].LiveUnits
	}
	write(slideStripUnitsX, fmt.Sprintf("Total Units: %d (Max %d)", live, cur.Strip.UnitLimit))
	write(slideStripSpeedX, "Game Speed: "+slideStripSpeedText(cur.Strip))
}

// slideStripSpeedText is the composer's own speed formatter, which is separate
// from the message-ring announcement of [07 R-CAM-01 §3]: `Normal` at the
// target word 10, otherwise `%+d`, with ` (%+d)` appended while the adapted
// current speed differs from the target [07 §6].
//
// Both `%+d` arguments are the **offset from normal**, not the raw speed word:
// each speed word is widened from 16 bits without sign extension and 10 is
// subtracted before it is formatted [07 R-CAM-01 §3]. The suffix test is a
// plain inequality of the two words and runs
// after the `Normal` branch has joined, so `Normal (+2)` is reachable.
func slideStripSpeedText(strip frame.StripReadout) string {
	target := strip.RequestedSpeed
	text := fmt.Sprintf("%+d", target-slideStripNormalSpeed)
	if target == slideStripNormalSpeed {
		text = "Normal"
	}
	if strip.ActiveSpeed != target {
		text += fmt.Sprintf(" (%+d)", strip.ActiveSpeed-slideStripNormalSpeed)
	}
	return text
}

// drawFooter paints the ordinary footer [07 R-HUD-03 §1–§3]. It replaces the
// former drawSelectedUnit, which drew the SELECTED unit's name, description
// and bar at the wrong anchors: the footer never reads the selection. Its
// three sources are the hovered gadget, the hovered world unit and the
// hovered feature, in that fixed priority.
//
// Placement is shared by every field: each anchor's y is offset by
// dy = screenHeight − baseheight (the side's [GENERAL] baseheight, default
// 480 [02 §6]) and x is never shifted. Text uses the side's console face and,
// unless a rule names a colour-map entry, the raw palette index 83. There is
// no maximum width, so no footer field wraps, ellipsises or truncates.
func (h *retailBattleHUD) drawFooter(c *client.Client, b *battleSession, f *frame.Frame) {
	if h == nil || c == nil || f == nil {
		return
	}
	footer := hud.BuildFooter(f, h.cat, h.owner, b.footerHover(f), false)
	if footer.Empty() {
		return
	}
	dy := h.footerDY(c)
	for _, bar := range footer.Bars {
		r, ok := h.anchors.ByIndex(bar.Anchor)
		if !ok {
			continue
		}
		h.drawFooterBar(c, r, dy, bar.HP, bar.Max)
	}
	// The owner's logo at LOGO2: the frame of the side-logo GAF indexed by the
	// owner's lobby colour byte, at the frame's full size, at the anchor
	// shifted by dy [07 R-HUD-03 §2]. The entry is `32xlogos` of
	// textures/logos.gaf — one handle bound at battle-data initialization that
	// this draw and the score panel's row logo both read
	// [07 R-HUD-04 §4]. footer.Logos already carried the resolved frame index;
	// only the entry name was missing.
	for _, logo := range footer.Logos {
		r, ok := h.anchors.ByIndex(logo.Anchor)
		if !ok {
			continue
		}
		art := h.sideLogoFrame(uint8(logo.Frame))
		if art == nil {
			continue
		}
		c.UIBlit(art, int(r.X1), int(r.Y1+dy))
	}
	for _, text := range footer.Texts {
		r, ok := h.anchors.ByIndex(text.Anchor)
		if !ok || text.Text == "" {
			continue
		}
		x, y := r.X1, r.Y1
		if text.FromY2 {
			y = r.Y2
		}
		y += text.OffsetY + dy
		if text.Centered {
			// "Centred at A" is x = A.x1 − trunc(textWidth/2), a signed divide
			// truncating toward zero [07 R-HUD-03 §1].
			x -= int32(client.MeasureText(h.console, text.Text)) / 2
		}
		color := text.Color.Value
		if text.Color.Logical {
			color = h.guiColor(color)
		}
		c.UIText(h.console, text.Text, int(x), int(y), color)
	}
}

// footerDY is the shared vertical offset dy = screenHeight − baseheight
// [07 R-HUD-03 §1][02 §6]. x is never shifted.
func (h *retailBattleHUD) footerDY(c *client.Client) int32 {
	base := int32(480)
	if h.side != nil && h.side.BaseHeight > 0 {
		base = h.side.BaseHeight
	}
	_, height := c.Size()
	return int32(height) - base
}

// drawFooterBar is the footer's two-part inclusive fill [07 R-HUD-03 §2]: the
// filled span [x1..fill] takes dcb[10] and, when fill is not already x2, the
// remainder [fill+1..x2] takes dcb[4]. A dead-level hp still paints the one
// pixel column at x1; there is no threshold colouring here — that belongs to
// the world health bar [03 R-FX-01 §6].
func (h *retailBattleHUD) drawFooterBar(c *client.Client, r hud.Rect, dy int32, health, max int32) {
	left, top, right, bottom := r.Ordered()
	if right < left || bottom < top || max <= 0 {
		return
	}
	fill := hud.FooterBarFill(hud.Rect{X1: left, Y1: top, X2: right, Y2: bottom}, health, max)
	top += dy
	bottom += dy
	c.UIFillRect(int(left), int(top), int(fill-left+1), int(bottom-top+1), h.guiColor(hud.PaletteProduction))
	if fill != right {
		c.UIFillRect(int(fill+1), int(top), int(right-fill), int(bottom-top+1), h.guiColor(hud.FooterBarRemainder))
	}
}

func (h *retailBattleHUD) defFor(u *frame.UnitView) (*content.UnitDef, bool) {
	if h == nil || h.cat == nil || u == nil {
		return nil, false
	}
	if u.DefName != "" {
		if def, ok := h.cat.Unit(u.DefName); ok {
			return def, true
		}
	}
	return h.cat.UnitDefByIndex(uint32(u.DefID))
}

func (h *retailBattleHUD) drawSidePage(c *client.Client, b *battleSession, f *frame.Frame) {
	if b == nil || b.cat == nil {
		return
	}
	window, pageGAF, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return
	}
	if window == nil {
		return
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.Kind == gui.KindFont || gad.Kind == gui.KindPanel {
			continue
		}
		// Fixed in authored coordinates: the §6 slide moves no rail window and
		// no gadget rectangle [07 R-HUD-05] (WU-19-223).
		r := window.PlacedRect(i)
		pressed := false
		if c.Input() != nil && c.Input().Mouse != nil && c.Input().Mouse.Held(input.MouseButtonLeft) {
			pressed = guiRectContains(r, int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y))
		}
		command, isCommand := commandGadgetVerdict(gad, f, paged)
		if isCommand && command.hidden {
			// A hidden command button is one the switch deactivates outright —
			// LOAD without the transport bit, BLAST with it [07 R-HUD-03 §6].
			// An inactive gadget paints nothing [07 §3].
			continue
		}
		grey := gad.GrayedOut != 0 || (isCommand && command.grey)
		var frameArt *formats.GAFFrame
		if isCommand {
			frameArt = commandButtonFrame(h.gadgetArtEntry(gad, pageGAF), gad, command.stage, grey, pressed)
		} else {
			frameArt = h.gadgetFrame(gad, pageGAF, pressed, grey)
		}
		if frameArt != nil {
			// .GUI controls use the authored rectangle origin; unlike the PANEL
			// shell, their GAF offsets are not applied [07 §4].
			c.UIBlit(frameArt, int(r.X), int(r.Y))
		}
		// A greyed button's rectangle goes through the rectangle shader after
		// the frame blit, at level -20 — PALETTE.SHD darken row 12 — unless the
		// button carries attribute 0x80 [07 R-HUD-04 §4][03 R-COMP-02 §5]. That
		// settles what §6's "20 palette steps" means: the two are one operator,
		// and the level indexes the SHD rows as level + 32. The cycle branch
		// (attribute 0x100) is excluded because it has its own greyed frame,
		// frames-1, and §3 attaches the darken clause to the other three greyed
		// sub-branches only [07 R-WGT-01 §3].
		if frameArt != nil && grey && gad.Attribs&guiAttribCheckbox == 0 && !cycleButton(gad) {
			c.UIShadeRect(h.pal, int(r.X), int(r.Y), int(r.W), int(r.H), retailGreyedButtonShade)
		}
		// A command button never draws its caption: the painter reads the art
		// alone [07 R-HUD-03 §6]. Stock content authors these gadgets with an
		// empty label anyway.
		if !isCommand && gad.Kind == gui.KindButton {
			text := gad.Text
			if len(gad.Labels) != 0 {
				text = gad.Labels[0]
			}
			// The count-label writer runs over the open page's toys after every
			// enqueue or cancel and writes the count **into the toy's own text
			// slot**; the ordinary window text pass then draws that slot with
			// the window's font and the toy's authored rectangle and GUI colour
			// fields [07 R-P0-11 §2]. The toy's `commonattribs` byte selects the
			// format, bit 0x04 tested before bit 0x08:
			//
			//   0x04 — resolve the toy's *name* to a unit definition and write
			//          "+%d" of that product's queued total; a zero total clears
			//          the slot.
			//   0x08 — the MAKENUKE/MAKEANTI stockpile toys: "%d" of the
			//          builder's stockpile count, with " +%d" of the pending
			//          build-weapon total appended.
			//
			// Stock content authors 0x04 on all 480 build-product buttons and
			// 0x08 on the eight stockpile buttons; every other side-panel button
			// authors 0 (asset census over the reference install's guis/*.gui).
			// The 0x08 half is not written here: the committed frame carries no
			// stockpile count for the selected builder, only the hovered unit's
			// percentage, so there is nothing to format yet.
			//
			// This replaces a gate of `gad.Text != ""` that appended " N" to an
			// authored caption. Every build-product button authors an empty
			// caption, so the count could never appear on the only toys that
			// carry one.
			if f != nil && gad.CommonAttribs&0x04 != 0 {
				text = productQueueCountLabel(f, gad.Name)
			}
			if text != "" {
				h.drawProductButtonCaption(c, gad, r, text)
			}
		}
	}
}

// productQueueCountLabel is the bit-0x04 format of the count-label writer
// [07 R-P0-11 §2]: the toy's name resolves to a product definition, and the
// label is "+%d" of the counts summed over **both** the selected builder's
// primary and secondary order lists for that product. A zero total clears the
// label; there is no clamp and no display cap.
//
// It is a local copy of hud.QueueCountLabel rather than a call to it: the two
// now agree (WU-19-135 corrected hud.QueueCountLabel's stale "two separate
// numbers" reading to this same one-sum "+%d" shape), but hud.QueueCountLabel
// takes a []frame.OrderQueueView slice rather than the committed frame this
// composer already holds, and internal/hud is not this unit's package to
// re-plumb a caller into.
func productQueueCountLabel(f *frame.Frame, product string) string {
	key := content.CanonicalKey(product)
	if f == nil || key == "" || f.CommandPage.Builder == 0 {
		return ""
	}
	// The writer is handed the single selected builder, so only that unit's
	// queues are counted [07 R-P0-11 §1]. Retail additionally admits only nodes
	// carrying the counted-production flag; internal/orders models no such flag
	// yet, and only build nodes carry a product key at all, so matching on the
	// product is equivalent over the queues nanolathe produces today.
	total := uint32(0)
	for i := range f.OrderQueues {
		q := &f.OrderQueues[i]
		if q.Unit != f.CommandPage.Builder {
			continue
		}
		for _, o := range q.Primary {
			if content.CanonicalKey(o.BuildProduct) == key {
				total += o.BuildCount
			}
		}
		for _, o := range q.Secondary {
			if content.CanonicalKey(o.BuildProduct) == key {
				total += o.BuildCount
			}
		}
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("+%d", total)
}

// queueCountLabelPen is the retail button painter's pen arithmetic
// [03 R-FONT-01 §6] for a button caption, applied to the queue-count text
// written into a build-product toy's own text slot [07 R-P0-11 §2]. `s` is 1
// when the gadget's `stages` field is non-zero. The vertical pen is
// `gy + trunc((h-1-metric)/2) + s` for the left/right/centre attributes, but
// the build-attribute variant (attribute bit 0x20) keeps the centred
// horizontal pen and instead anchors near the bottom edge:
// `bottom - 4 - metric + s`. `metric` is the line metric of the family the
// caption is drawn with — the capital-I frame height plus two for a GAF font
// (the case here; see drawProductButtonCaption), or the FNT header height
// field on the GAF pen's null-slot fallback.
//
// Build-product buttons author attribute 0x20 and no left/right/centre bit
// (asset census over the reference install's guis/*.gui files, the same
// census that backs [07 R-P0-11 §2]'s refinement: all 480 author
// `attribs = 32` alongside `commonattribs = 4`), so the count lands at the
// bottom-centre of the button, not the vertically-centred left inset a
// left-aligned button would use.
func queueCountLabelPen(gad gui.Gadget, r gui.Rect, textWidth, metric int) (x, y int) {
	s := 0
	if gad.Stages != 0 {
		s = 1
	}
	gx, gy, w, h := int(r.X), int(r.Y), int(r.W), int(r.H)
	right := gx + w - 1
	bottom := gy + h - 1
	centredX := gx + (right-textWidth-gx)/2 + s + 1
	switch {
	case gad.Attribs&1 != 0: // left
		return gx + 3 + s, gy + (h-1-metric)/2 + s
	case gad.Attribs&4 != 0: // right
		x := right - 3 - textWidth
		if x < gx {
			x = gx
		}
		return x, gy + (h-1-metric)/2 + s
	case gad.Attribs&2 != 0: // centre
		return centredX, gy + (h-1-metric)/2 + s
	case gad.Attribs&0x20 != 0: // build-attribute variant
		return centredX, bottom - 4 - metric + s
	default:
		return gx + 3 + s, gy + (h-1-metric)/2 + s
	}
}

// productButtonCaptionLayout picks the family and pen the retail button
// painter would use for a side-page button caption — in practice the queue
// count the count-label writer left in the toy's own text slot
// [07 R-P0-11 §2].
//
// Family. Every text call in the button painter goes through the GAF-font pen
// with mode 0, so a button caption is drawn with the window's *current GAF
// font*; the pen reaches the FNT drawer only when that slot is null, and then
// with the width limit dropped (maxW = -1) [03 R-FONT-01 §6]. This is the
// correction WU-19-221 makes: the count was drawn with the side font's FNT,
// the wrong family.
//
// Which GAF slot. Startup hands the GUI window slot 0 = anims/hattfont12.gaf
// and slot 1 = anims/hattfont11.gaf; the button painter switches the current
// slot to 1 only for a button carrying the small-font attribute bit 0x8000,
// and restores slot 0 when it is done, so slot 0 is what a button without
// that bit draws with [03 R-FONT-01 §5]. No count-bearing product button
// carries it: an asset census over the reference install's guis/*.gui files —
// the same census that backs [07 R-P0-11 §2]'s refinement — finds all 488
// count-bearing buttons (kind 1 with `commonattribs` 4 or 8) authoring
// `attribs = 32` and a 64x64 rectangle, and none of them 0x8000. The count is
// therefore hattfont12, the GAF font this HUD already loads for its other
// retail text.
//
// (The sentence in [03 R-FONT-01 §5] that has "the build-card count label"
// switching to slot 1 for its duration describes the routine RWU-19-34
// re-identified as the kind-13 score-bar painter — the same mis-subject that
// correction fixed in [03 R-FONT-01 §6], where it also settles that the
// side-page build count "is a button caption and never passes through this
// routine". Reported for a doc correction; the button rule above is what the
// count follows.)
//
// Colour. The GAF pen colours from the frame bytes with mode 0 and never
// reads the foreground the painter installs, so the button rule's map entry
// `colorf` (map entry 0 unless mid-flash, and nothing on this page ever
// writes the flash word) only reaches pixels on the FNT fallback
// [03 R-FONT-01 §6].
func (h *retailBattleHUD) productButtonCaptionLayout(gad gui.Gadget, r gui.Rect, text string) (x, y int, font *formats.GAFEntry) {
	if font = h.buttonCaptionGAFFont(); font != nil {
		x, y = queueCountLabelPen(gad, r, retailGAFTextWidth(font, text), retailGAFTextHeight(font))
		return x, y, font
	}
	if h.guiFont == nil {
		return 0, 0, nil
	}
	x, y = queueCountLabelPen(gad, r, client.MeasureText(h.guiFont, text), int(h.guiFont.Height))
	return x, y, nil
}

// buttonCaptionGAFFont is the window's current GAF-font slot for a button
// caption: slot 0, anims/hattfont12.gaf, which battle entry already loads
// [03 R-FONT-01 §5]. A missing font file leaves the slot null rather than
// failing battle entry, which is the pen's FNT-fallback case.
func (h *retailBattleHUD) buttonCaptionGAFFont() *formats.GAFEntry {
	if h == nil || h.modalFont == nil || len(h.modalFont.Frames) == 0 {
		return nil
	}
	return h.modalFont
}

// drawProductButtonCaption draws that caption where productButtonCaptionLayout
// puts it. The GAF pen blits each glyph at `penX - XOffset, penY -
// normalizedYOffset` (the load-time baseline normalization of [07 §4]) and
// stops on the first glyph wider than the remaining width, the painter's
// `maxW = w` [03 R-FONT-01 §6]. On the null-slot fallback the FNT drawer is
// called with the width limit dropped, which is what maxWidth < 0 means to
// UITextWidth.
func (h *retailBattleHUD) drawProductButtonCaption(c *client.Client, gad gui.Gadget, r gui.Rect, text string) {
	x, y, font := h.productButtonCaptionLayout(gad, r, text)
	if font != nil {
		drawRetailGAFText(c, font, text, x, y, int(r.W))
		return
	}
	if h.guiFont == nil {
		return
	}
	c.UITextWidth(h.guiFont, text, x, y, -1, h.guiColor(0))
}

// commandPageIsPaged reports the selected builder's page-shown bit (status bit
// 22) off the committed frame [07 §9]. BUILD stages from that bit and ORDERS
// from its inverse [07 R-HUD-03 §6].
//
// Every caller derives it again from the frame it is acting on rather than
// keeping a copy, so the painter, the pointer pass and the click path cannot
// drift apart and nothing about a gadget is latched [I6].
func commandPageIsPaged(f *frame.Frame) bool {
	if f == nil || f.CommandPage.Builder == 0 {
		return false
	}
	builder, found := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !found {
		return false
	}
	return hud.IsPaged(builder.Flags)
}

// buildButtonPage is the build page a BUILD click selects. Selecting page 0
// clears the page-shown bit and leaves the page field alone [07 §9], so that
// field still names the build page the builder was last on and setting the bit
// again brings that page back.
//
// No click writes the page field: it is seeded at **unit creation**, page 1
// with the paged bit set when the definition's page-count byte is at least 2
// and both cleared otherwise [07 R-HUD-04 §4 "First build page"]. The seed
// lives in internal/units' allocator initializer, which is why the zero-field
// case this function used to guess about no longer arises for a multi-page
// builder. The clamp below stays as a bounds guard for a single-page or
// malformed record, not as a stand-in for the missing producer.
func buildButtonPage(f *frame.Frame) int {
	if f == nil || f.CommandPage.Builder == 0 {
		return 0
	}
	remembered := 0
	if builder, found := snapshotUnitByHandle(f, f.CommandPage.Builder); found {
		remembered = hud.RememberedPage(builder.Flags)
	}
	if remembered <= 0 || remembered >= int(f.CommandPage.PageCount) {
		return 1
	}
	return remembered
}

// commandGadgetVerdict resolves one authored gadget against the command-button
// stage and grey table [07 R-HUD-03 §6]. It is the single decision the painter,
// the pointer pass and the click path all consult, so what a button looks like
// and whether it responds can never disagree. The second result is false for a
// gadget the table does not name — a product slot, NEXT/PREV, a label.
func commandGadgetVerdict(gad gui.Gadget, f *frame.Frame, paged bool) (commandButtonVerdict, bool) {
	return commandButtonState(commandButtonName(gad.Name), f, paged)
}

// commandButtonNames are the gadget names of the command-button stage and grey
// table [07 R-HUD-03 §6]. Every one of them is authored with the side's
// nameprefix in stock content — ARMONOFF, CORMOVEORD, ARMUNLOAD — the same
// shape §6 spells out for "%sPREV"/"%sNEXT", so a gadget is matched by the
// table name that is a suffix of its own.
var commandButtonNames = [...]string{
	"BUILD", "ORDERS", "CLOAK", "ONOFF", "MOVEORD", "FIREORD",
	"MOVE", "STOP", "ATTACK", "DEFEND", "PATROL", "RECLAIM", "CAPTURE", "REPAIR",
	"LOAD", "UNLOAD", "BLAST",
}

// ordersButtonCue and buildButtonCue are the two stage buttons' own cues,
// named by the click handler itself rather than by the page-switch routine
// [07 R-HUD-04 §5]. They are `allsound.tdf` aliases like every other interface
// cue; a mount that does not author one leaves the click silent.
const (
	ordersButtonCue = "ordersbutton"
	buildButtonCue  = "buildbutton"
)

// commandButtonName returns the table row a gadget belongs to, or "" when it is
// not a command button. The longest matching suffix wins, which is what keeps
// ARMUNLOAD out of the LOAD row and ARMMOVEORD out of the MOVE row.
func commandButtonName(gadget string) string {
	upper := strings.ToUpper(gadget)
	best := ""
	for _, name := range commandButtonNames {
		if len(name) > len(best) && strings.HasSuffix(upper, name) {
			best = name
		}
	}
	return best
}

// commandButtonVerdict is one row of the stage and grey table, evaluated
// against the committed frame [07 R-HUD-03 §6]. It is derived per draw and
// never written back to the gadget, so nothing latches [I6].
type commandButtonVerdict struct {
	stage  int
	grey   bool
	hidden bool
}

// commandButtonState evaluates the stage and grey table of [07 R-HUD-03 §6] for
// one command button. paged is the selected builder's page-shown bit. The
// second result is false when the name is not a command button at all.
//
// The stance and pair values come from the committed selection aggregate the
// command-window switch computes [07 §9][07 R-HUD-03 §13]. A stance field greys
// at 4 and a cloak/on-off pair at 3: both are the not-applicable value their
// fold starts from, so a selection carrying nothing that accepts the command
// greys its button. A disagreeing selection folds to one below that instead — 3
// and 2 — which stages the generic orders plate rather than greying.
//
// The capability folds are a disjunction, so a button greys only when no
// selected unit can perform its command [07 R-HUD-03 §13].
func commandButtonState(name string, f *frame.Frame, paged bool) (commandButtonVerdict, bool) {
	if name == "" || f == nil {
		return commandButtonVerdict{}, false
	}
	page := f.CommandPage
	// BUILD and ORDERS are the two halves of the page-shown bit; both grey when
	// there is no builder or the builder has no pages.
	noPages := page.Builder == 0 || page.PageCount == 0
	switch name {
	case "BUILD":
		return commandButtonVerdict{stage: boolStage(paged), grey: noPages}, true
	case "ORDERS":
		return commandButtonVerdict{stage: boolStage(!paged), grey: noPages}, true
	case "CLOAK":
		return commandButtonVerdict{stage: int(page.CloakState), grey: page.CloakState == 3}, true
	case "ONOFF":
		return commandButtonVerdict{stage: int(page.OnOffState), grey: page.OnOffState == 3}, true
	case "MOVEORD":
		return commandButtonVerdict{stage: int(page.MoveStance), grey: page.MoveStance == 4}, true
	case "FIREORD":
		return commandButtonVerdict{stage: int(page.FireStance), grey: page.FireStance == 4}, true
	// The eight capability buttons carry no stage; each greys when the
	// selection's aggregate bit for its command is clear.
	case "MOVE":
		return commandButtonVerdict{grey: !page.CanMove}, true
	case "STOP":
		return commandButtonVerdict{grey: !page.CanStop}, true
	case "ATTACK":
		return commandButtonVerdict{grey: !page.CanAttack}, true
	case "DEFEND":
		return commandButtonVerdict{grey: !page.CanDefend}, true
	case "PATROL":
		return commandButtonVerdict{grey: !page.CanPatrol}, true
	case "RECLAIM":
		return commandButtonVerdict{grey: !page.CanReclaim}, true
	case "CAPTURE":
		return commandButtonVerdict{grey: !page.CanCapture}, true
	case "REPAIR":
		return commandButtonVerdict{grey: !page.CanRepair}, true
	// The transport trio is the one row that hides rather than greys: without
	// the transport bit LOAD disappears and UNLOAD greys, with it BLAST
	// disappears. BLAST otherwise greys unless the selection carries the blast
	// bit.
	case "LOAD":
		return commandButtonVerdict{hidden: !page.IsTransport}, true
	case "UNLOAD":
		return commandButtonVerdict{grey: !page.IsTransport}, true
	case "BLAST":
		if page.IsTransport {
			return commandButtonVerdict{hidden: true}, true
		}
		return commandButtonVerdict{grey: !page.CanBlast}, true
	}
	return commandButtonVerdict{}, false
}

func boolStage(v bool) int {
	if v {
		return 1
	}
	return 0
}

// guiAttribCycle is the cycle-button attribute bit [fmt gui][07 R-WGT-01 §3].
// Every staged command button in stock content carries it: ARMONOFF, ARMCLOAK,
// ARMMOVEORD and ARMFIREORD are all authored with it.
const guiAttribCycle = 0x100

// guiAttribCheckbox is attribute 0x80, the checkbox fallback bit. It is also
// the one exemption from the greyed-button darkening [07 R-WGT-01 §3]
// [07 R-HUD-04 §4].
const guiAttribCheckbox = 0x80

// retailGreyedButtonShade is the rectangle-shader level a greyed button's
// rectangle is darkened at: -20, which indexes PALETTE.SHD row 12 as
// level + 32 [07 R-HUD-04 §4][03 R-COMP-02 §5].
const retailGreyedButtonShade = -20

func cycleButton(gad gui.Gadget) bool { return gad.Attribs&guiAttribCycle != 0 }

// commandButtonFrame is the button painter's frame choice
// [07 R-WGT-01 §3 "the painter's frame choice"], which completes and corrects
// [07 R-HUD-03 §6]. The authored `status` field is the button's **down-state
// word**, not the frame a stage counts from; the frame *base* comes from art
// resolution and is frame 0 on the named-art path every command button takes.
// §6's reading of `status` as a base is superseded [07 R-HUD-04 §4].
//
// With `down` the down-state word and `stage` the current-stage byte:
//
//   - greyed and a cycle button (0x100) -> frames - 1;
//   - greyed otherwise                  -> base + min(state + 2, frames - 1);
//   - a cycle button                     -> base + its state, with no held look;
//   - down set on staged art             -> frames - 2, the pressed look;
//   - down set otherwise                 -> base + down;
//   - otherwise                          -> base + the state.
//
// `state` is the runtime index the command-button table supplies: the
// down-state word for a cycle button and the current-stage byte for staged
// art, which is the one value [07 R-HUD-03 §6] calls the stage.
//
// A press sets the down-state word to 1 while the button is captured — except
// on a cycle button, which has no held look at all: a press advances its state
// and fires immediately, so it keeps showing its stage while the mouse is down.
// The greyed darkening is applied by the caller, not folded into the frame.
func commandButtonFrame(entry *formats.GAFEntry, gad gui.Gadget, stage int, grey, pressed bool) *formats.GAFFrame {
	if entry == nil || len(entry.Frames) == 0 {
		return nil
	}
	last := len(entry.Frames) - 1
	cycle := cycleButton(gad)
	// The frame base of a named-art gadget is frame 0, and the authored
	// `status` is the down-state word that sits on top of it. A press sets the
	// down-state to 1 while the button is captured, except on a cycle button.
	const base = 0
	down := int(gad.Status)
	if pressed && !cycle {
		down = 1
	}
	idx := 0
	switch {
	case grey && cycle:
		idx = last
	case grey:
		idx = base + min(stage+down+2, last)
	case cycle:
		idx = base + stage
	case gad.Stages != 0 && down != 0:
		idx = last - 1
	case down != 0:
		idx = base + down
	default:
		idx = base + stage
	}
	// Authored art shorter than the frame the table asks for is a bounds guard,
	// not a retail behavior: an out-of-range index would panic here [I11].
	if idx < 0 {
		idx = 0
	}
	if idx > last {
		idx = last
	}
	return entry.Frames[idx].Frame
}

// consumeClick applies the retail order-button latch parser and data-driven build
// product binding to a visible authored side-panel button [R-P0-03][07 §9].
// Build products are validated against cat.BuildMenus (no invention) and
// dispatched via injected callbacks: mobile builders arm placement (definition
// retained, cursorfindsite [07 §8] 0xE), factories queue immediately [F-P1-008].
// Order buttons are bound via ParseButtonLatch and handleHudOrderButton which
// routes through the injected command dispatch [R-P0-03]. Any HUD gadget hit
// is consumed to prevent leak into world drag [07 §3][F-P0-003]. Page next/prev
// are data-driven with count guard [R-P0-03][07 §9] C10.
func (h *retailBattleHUD) consumeClick(b *battleSession, x, y int32) bool {
	return h.consumeClickDelta(b, x, y, false)
}

// consumeRightClick handles the signed cancellation form of a factory
// product button. Other authored controls are consumed without an action;
// right-click remains deselect/cancel on the world [R-P0-11].
func (h *retailBattleHUD) consumeRightClick(b *battleSession, x, y int32) bool {
	return h.consumeClickDelta(b, x, y, true)
}

func (h *retailBattleHUD) consumeClickDelta(b *battleSession, x, y int32, rightClick bool) bool {
	if h == nil || b == nil {
		return false
	}
	// The unit information screen is a child window on top of the battle: it
	// services the release before the command page does, and a release inside
	// it never reaches a side-panel control [07 §3][07 R-WGT-01 §1].
	if h.unitInfoConsumeClick(x, y) {
		return true
	}
	// A committed frame is required for every HUD action [I6]. A frame with a
	// non-empty selection but no command-page builder still exposes the authored
	// general/order controls; with an empty selection the command windows are
	// closed to the root and there is nothing above it to click [07 §6].
	f, ok := b.currentSnapshot()
	if !ok {
		return false
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return false
	}
	if window == nil {
		return false
	}
	// Builder and product data are optional for general/order controls, but if
	// present they come only from the immutable CommandPage [I6].
	var selectedDef *content.UnitDef
	var snapshotProducts []string
	if f.CommandPage.Builder != 0 && f.CommandPage.PageCount != 0 && b.sess != nil && b.cat != nil {
		if builderView, found := snapshotUnitByHandle(f, f.CommandPage.Builder); found && builderView.Owner == b.sess.LocalOwner {
			if def, found := b.cat.Unit(builderView.DefName); found && def != nil && def.Builder {
				selectedDef = def
				snapshotProducts = f.CommandPage.ProductKeys
			}
		}
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut != 0 || gad.Kind != gui.KindButton {
			continue
		}
		// The painter's verdict decides the click too. Greying a command button
		// from the selection aggregate [07 R-HUD-03 §6] does not touch the
		// authored gadget, so the authored-grey test above cannot see it, and a
		// button drawn greyed used to act on a click anyway.
		command, isCommand := commandGadgetVerdict(gad, f, paged)
		if isCommand && command.hidden {
			// A hidden command button is deactivated outright — LOAD without the
			// transport bit, BLAST with it [07 R-HUD-03 §6]. Hidden gadgets are
			// skipped before the hit test [07 R-WGT-01 §1], so one neither acts
			// nor shields whatever lies behind it.
			continue
		}
		// Fixed in authored coordinates: the §6 slide moves no rail gadget
		// rectangle, so the hit test never follows it [07 R-HUD-05]
		// (WU-19-223).
		r := window.PlacedRect(i)
		if !guiRectContains(r, x, y) {
			continue
		}
		if isCommand && command.grey {
			// "Greyed buttons ignore everything" [07 R-WGT-01 §3]: the grey test
			// runs before any activation effect [07 §3], so a greyed button takes
			// no capture and fires nothing. It is still hit-tested — only hidden
			// gadgets are skipped before that — so it does not fire and the pass
			// simply goes on to the gadgets after it [07 R-WGT-01 §1].
			continue
		}
		upperName := strings.ToUpper(gad.Name)
		upperText := strings.ToUpper(gad.Text)
		// BUILD and ORDERS are the two halves of the page-shown bit: they stage
		// from it and from its inverse [07 R-HUD-03 §6], and clicking one sets
		// the state its stage names. ORDERS selects page 0, which is what clears
		// the bit; BUILD selects a build page, which is what sets it.
		if isCommand {
			switch commandButtonName(gad.Name) {
			case "ORDERS":
				if rightClick {
					return true
				}
				// The gadget's own cue, played by the click handler before the
				// stage bit is consumed [07 R-HUD-04 §5]. It is not the page
				// cycle's `nextbuildmenu` — that belongs to `,`/`.` and to
				// NEXT/PREV [07 §9].
				b.playUICue(nil, ordersButtonCue)
				_ = b.DispatchBuildPage(0)
				return true
			case "BUILD":
				if rightClick {
					return true
				}
				b.playUICue(nil, buildButtonCue) // [07 R-HUD-04 §5]
				_ = b.DispatchBuildPage(buildButtonPage(f))
				return true
			}
		}
		// Page navigation. The NEXT and PREV gadgets are the two rows of the
		// page cycle that never return to page 0 [07 R-HUD-03 §6]; the target is
		// computed from the committed page and dispatched as an absolute page,
		// so the cycle's wrap lives in one place [I6].
		nextPage := hud.NextPageButton(int(f.CommandPage.Page), int(f.CommandPage.PageCount))
		prevPage := hud.PrevPageButton(int(f.CommandPage.Page), int(f.CommandPage.PageCount))
		if strings.Contains(upperName, "NEXTPAGE") || strings.Contains(upperName, "NEXT") && strings.Contains(upperName, "PAGE") || strings.Contains(upperName, "PAGEDOWN") {
			if rightClick {
				return true
			}
			_ = b.dispatchBuildPageCued(nextPage)
			return true
		}
		if strings.Contains(upperName, "PREVPAGE") || strings.Contains(upperName, "PREV") && strings.Contains(upperName, "PAGE") || strings.Contains(upperName, "PAGEUP") {
			if rightClick {
				return true
			}
			_ = b.dispatchBuildPageCued(prevPage)
			return true
		}
		if strings.Contains(upperName, "NEXT") || strings.Contains(upperText, "NEXT") {
			if rightClick {
				return true
			}
			// PREV and NEXT are deactivated outright after a page opens when the
			// page-count byte is below 2 [07 R-HUD-03 §6]. With page 0 counted,
			// a builder that has any authored page has a count of at least 2, so
			// the test only ever refuses a builder with no build page at all.
			if f.CommandPage.PageCount > 1 {
				_ = b.dispatchBuildPageCued(nextPage)
				return true
			}
		}
		if strings.Contains(upperName, "PREV") || strings.Contains(upperText, "PREV") {
			if rightClick {
				return true
			}
			if f.CommandPage.PageCount > 1 {
				_ = b.dispatchBuildPageCued(prevPage)
				return true
			}
		}
		// Build product binding data-driven [R-P0-03][02 "Build-menu catalog keys"].
		// GUI may not invent products absent from authored build list.
		if selectedDef != nil && b.cat != nil {
			candidates := []string{gad.Name, gad.Text}
			candidates = append(candidates, gad.Labels...)
			for _, cand := range candidates {
				if cand == "" {
					continue
				}
				// Direct canonical match or case-insensitive
				var pageProductKey string
				for _, key := range snapshotProducts {
					if strings.EqualFold(content.CanonicalKey(key), content.CanonicalKey(cand)) {
						pageProductKey = key
						break
					}
				}
				if pageProductKey == "" {
					continue
				}
				// Resolve canonical product name for dispatch
				prodKey := cand
				if pageProductKey != "" {
					// Production uses the immutable page key as identity; do not
					// recover an alias by consulting the live catalog menu.
					prodKey = pageProductKey
				}
				prodDef, _ := b.cat.Unit(prodKey)
				if prodDef == nil {
					// The committed page is the sole product identity source. An
					// unresolved key is consumed but cannot be classified or queued.
					return true
				}
				prodKey = prodDef.CanonicalKey
				// Retail branches on the product's BMcode, not on the builder
				// [07 §9]. Product BMcode determines queue versus placement.
				if !hud.ProductArmsPlacement(prodDef) {
					delta := factoryBuildDelta(b.battleState().Input.ShiftHeld, rightClick)
					// The counted-add routine's own cue runs before the
					// descriptor routing and before the queue coalesce, so a
					// click that ends up changing nothing is still audible
					// [07 R-P0-11 §1].
					b.playUICue(nil, countedBuildCue(delta))
					if err := h.dispatchFactoryBuild(b, prodKey, delta); err != nil {
						h.dispatchErr = err
					}
					return true
				}
				if rightClick {
					return true
				}
				// The build-button handler "arms the MOBILEBUILD latch (`0xE`),
				// stores the id in a pending-build word and plays the `addbuild`
				// cue — the click arms it, not the ghost show" [07 §9].
				b.armPlacement(prodDef)
				b.playUICue(nil, cueAddBuild)
				return true
			}
		}
		upper := strings.ToUpper(gad.Name)
		// The side-prefixed ONOFF gadget (ARMONOFF / CORONOFF) is the button
		// form of the on/off command [07 R-HUD-03 §6]; it issues exactly what
		// key `O` issues, for every selected onoffable unit, with Shift queuing
		// the record [04 R-ORD-01 §2].
		if strings.HasSuffix(upper, "ONOFF") {
			if rightClick {
				return true
			}
			b.toggleOnOffSelected(b.battleState().Input.ShiftHeld)
			// "the on/off and cloak arms of the same handler play the
			// already-documented `specialorders`" [07 §9]. The cue belongs to the
			// side-panel gadget arm, not to the on/off command itself, so the
			// hotkey path that shares toggleOnOffSelected does not raise it.
			b.playUICue(nil, cueSpecialOrders)
			return true
		}
		// The two stance gadgets are resolved by the same longest-suffix table
		// the stage and grey pass uses, so ARMMOVEORD reaches the stance arm
		// and never the MOVE substring arm below [04 R-STANCE-01 §2].
		switch commandButtonName(gad.Name) {
		case "MOVEORD":
			if rightClick {
				return true
			}
			b.cycleStance(false)
			return true
		case "FIREORD":
			if rightClick {
				return true
			}
			b.cycleStance(true)
			return true
		}
		if strings.Contains(upper, "MOVE") ||
			strings.Contains(upper, "ATTACK") || strings.Contains(upper, "BLAST") ||
			strings.Contains(upper, "DEFEND") || strings.Contains(upper, "REPAIR") ||
			strings.Contains(upper, "PATROL") || strings.Contains(upper, "RECLAIM") ||
			strings.Contains(upper, "CAPTURE") || strings.Contains(upper, "LOAD") ||
			strings.Contains(upper, "UNLOAD") || strings.Contains(upper, "STOP") {
			b.handleHudOrderButton(gad.Name)
			return true
		}
		// Any other GUI button still consumes the click to prevent world leak [07 §3]
		return true
	}
	return false
}

// hitTestFor is the session-aware hit test used by battleSession handleInput [F-P0-003].
func (h *retailBattleHUD) hitTestFor(b *battleSession, x, y int32) bool {
	if h == nil || b == nil {
		return false
	}
	// An open modal that covers the pointer owns the press: world picking and
	// camera edge behavior are gated out under it [07 §3].
	if unitInfoCovers(x, y) {
		return true
	}
	var f *frame.Frame
	if b.sess != nil && b.sess.Snapshot != nil {
		f = b.sess.Snapshot.Current()
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return false
	}
	if window == nil {
		return false
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut != 0 || gad.Kind != gui.KindButton {
			continue
		}
		// Neither a greyed nor a hidden command button is an activation target
		// [07 R-WGT-01 §3][07 R-HUD-03 §6], so neither reports a hit here — the
		// same reading the authored-grey test above already applies.
		if command, isCommand := commandGadgetVerdict(gad, f, paged); isCommand && (command.grey || command.hidden) {
			continue
		}
		// Fixed in authored coordinates: the §6 slide moves no rail gadget
		// rectangle, so the hit test never follows it [07 R-HUD-05]
		// (WU-19-223).
		r := window.PlacedRect(i)
		if guiRectContains(r, x, y) {
			return true
		}
	}
	return false
}

func (h *retailBattleHUD) buttonAt(b *battleSession, x, y int32) int {
	if h == nil || b == nil {
		return -1
	}
	var f *frame.Frame
	if b.sess != nil && b.sess.Snapshot != nil {
		f = b.sess.Snapshot.Current()
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return -1
	}
	if window == nil {
		return -1
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut != 0 || gad.Kind != gui.KindButton {
			continue
		}
		// A greyed button takes no capture and a hidden one is skipped before
		// the hit test [07 R-WGT-01 §3][07 R-WGT-01 §1], so neither can be the
		// gadget a press and its release identify.
		if command, isCommand := commandGadgetVerdict(gad, f, paged); isCommand && (command.grey || command.hidden) {
			continue
		}
		// Fixed in authored coordinates: the §6 slide moves no rail gadget
		// rectangle, so the hit test never follows it [07 R-HUD-05]
		// (WU-19-223).
		r := window.PlacedRect(i)
		if guiRectContains(r, x, y) {
			return i
		}
	}
	return -1
}

func (h *retailBattleHUD) sameButton(b *battleSession, x0, y0, x1, y1 int32) bool {
	// While the unit information screen is up it is the active window: the
	// press/release pair is identified against it, not against the command
	// page underneath [07 §3][07 R-WGT-01 §1].
	if unitInfoOpen() {
		return unitInfoCovers(x0, y0) && unitInfoCovers(x1, y1)
	}
	pressed := h.buttonAt(b, x0, y0)
	return pressed >= 0 && pressed == h.buttonAt(b, x1, y1)
}

// buildPageCount returns the number of authored <unit>N.GUI pages. Retail's
// unit definition page-count byte is populated from the DOWNLOADMENU records;
// the mounted generated pages are the clean data equivalent and keep modded
// layouts data-driven [07 §9]. Page flag encoding itself has only three bits.
func (h *retailBattleHUD) buildPageCount(def *content.UnitDef) int {
	if h == nil || def == nil {
		return 0
	}
	key := strings.ToLower(def.UnitName)
	if count, ok := h.pageCounts[key]; ok {
		return count
	}
	count := 0
	for page := 1; page <= 8; page++ {
		name := fmt.Sprintf("%s%d", key, page)
		if window, _ := h.loadWindowProbe(name); window == nil {
			break
		}
		count++
	}
	h.pageCounts[key] = count
	return count
}

func (h *retailBattleHUD) windowFor(b *battleSession, f *frame.Frame) (*gui.Window, *formats.GAF) {
	window, page, err := h.windowForRequired(b, f)
	if err != nil {
		if h != nil {
			h.assetErr = err
		}
		return nil, nil
	}
	return window, page
}

// windowForRequired returns a selected authored page construction error to
// every presentation and input caller. The two-value windowFor wrapper keeps
// older inspection helpers source-compatible while retaining the diagnostic
// on the HUD [07 §6][07 §9].
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

func guiRectContains(r gui.Rect, x, y int32) bool {
	left, top, right, bottom := r.X, r.Y, r.X+r.W, r.Y+r.H
	return x >= left && x <= right && y >= top && y <= bottom
}

// overWorld reports whether a pointer position lies in the world viewport
// rather than on the HUD chrome [07 §8]. The rail boundary is the authored
// 129-pixel column; the top and bottom strips are as tall as their frames.
// A pointer on the chrome forces the idle cursor shape [07 §8].
//
// The minimap is on the rail and is consumed before this world-region test;
// pointers over it therefore remain chrome [07 §8][07 §10].
func (h *retailBattleHUD) overWorld(x, y int32) bool {
	if h == nil {
		return true
	}
	const railX = 129 // authored rail boundary, matching the PANELSIDE blit
	if x < railX {
		return false
	}
	top := int32(0)
	if h.panelTop != nil {
		top = int32(h.panelTop.Height)
	}
	// The world viewport's last row is `H-33` inclusive at every display
	// mode, one row above the bottom strip's origin [03 §4.1][07 R-HUD-05].
	screenH := h.screenH
	if screenH <= 0 {
		screenH = retailScreenH
	}
	bottom := hud.BottomStripY(screenH)
	return y >= top && y < bottom
}
