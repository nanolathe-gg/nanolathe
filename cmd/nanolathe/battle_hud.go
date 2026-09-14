package main

// Retail battle HUD composition. This file deliberately contains no Nanolathe
// layout constants for controls: the side's SIDEDATA anchors, intgaf PANEL
// frames, and authored .GUI windows are the layout. The few fixed coordinates
// below are the retail panel-shell call sites recovered from TotalA.exe.

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type retailBattleHUD struct {
	commandWindowInput commandWindowInputState
	// palettePanels retain the generic widget state per selected command-window
	// instance. The GUI pointer, not a gadget name, is the identity because
	// authored pages may carry duplicate names [07 R-WGT-01 §3].
	palettePanels map[*gui.Window]*ui.Panel

	side    *content.SideDef
	cat     *content.Catalog
	owner   uint8
	anchors hud.Anchors

	console *formats.FNT
	guiFont *formats.FNT
	// primaryFont is the process UI FNT (`fonts/COMIX`). The stand-alone clock
	// uses it after the ordinary message-column selector has run; the side
	// console remains separately bound for the selector-inheritance edge
	// [07 R-CAM-01 §6][07 R-HUD-03 §14.4].
	primaryFont *formats.FNT
	pal         *palette.Tables

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
	talkWin     *gui.Window
	talkPanel   *ui.Panel
	talkBuilt   bool
	exitWin     *gui.Window
	confirmWin  *gui.Window
	restartWin  *gui.Window
	// Each ordinary battle child retains its indexed widget state from open
	// through close.  The panels are deliberately not rebuilt by composition:
	// a child close leaves its surviving parent and its input state intact
	// [07 R-WGT-01 §1][07 R-WGT-02 §2].
	optionsPanel *ui.Panel
	exitPanel    *ui.Panel
	confirmPanel *ui.Panel
	// screenW/screenH is the negotiated surface size the chrome is currently
	// laid out for; applyDisplaySize re-places the size-dependent windows when
	// it changes [07 R-HUD-05].
	screenW, screenH int32
	modalFont        *formats.GAFEntry
	// modalFontSmall is GAF-font slot 1, anims/hattfont11.gaf — the face the
	// composer selects for the slide strip's three readouts and the kind-13
	// score-bar painter selects for its decimal, each restoring slot 0
	// (modalFont, hattfont12) afterwards [03 R-FONT-01 §5][07 R-HUD-04 §4].
	modalFontSmall *formats.GAFEntry
	// stripArt is the cached second frame (index 1) of the common GUI GAF's
	// `LIGHTBAR` entry with its hotspot zeroed: the band the Space-held slide
	// strip is blitted from [07 R-HUD-04 §4].
	stripArt       *formats.GAFFrame
	pausedFrame    *formats.GAFFrame
	victoryFrame   *formats.GAFFrame  // [07 §11] igvictory from anims/igtitles.gaf via intgaf/gui machinery
	defeatFrame    *formats.GAFFrame  // [07 §11] igdefeat from anims/igtitles.gaf
	resultWin      *gui.Window        // [07 §11] authored ENDMSN.GUI result surface
	shell          *gameShell         // the front-end shell that owns ENDMSN's installed background bitmap [08 R-CAMP-01 §8]
	resultGAF      *formats.GAF       // [07 §11] authored endmsn.gaf outcome controls
	resultPanel    *ui.Panel          // shared authored gesture state [07 §3]
	resultState    resultPresentation // ENDMSN dynamic bars/reveal state [08 R-CAMP-01 §7]
	windowContext  *battleWindowContext
	optionsRelabel bool
	optionsBuilt   bool
	exitBuilt      bool
	confirmBuilt   bool
	restartBuilt   bool
	resultBuilt    bool

	fs    vfs.FSOps
	pages map[string]*formats.GAF
	// pageChecked is separate from the GUI cache: a probe may establish that a
	// page window exists before the selected page is drawn, but it must not make
	// the later art lookup disappear. A nil page is a valid result when the
	// authored support-GAF fallback chain supplies the control art [07 §6].
	pageChecked map[string]bool
	// Cache resolved GUI/model once instead of reparsing on draw/click [ON-05 1][R-P0-03]
	windows     map[string]*gui.Window
	windowBuilt map[string]bool
	// generatedWindows are cloned command pages patched from the committed
	// download-menu slot records. The unmodified authored page/template stays
	// in windows so another generated page can safely reuse it [07 R-HUD-03
	// §6][07 §9].
	generatedWindows  map[string]*gui.Window
	generatedPageArt  map[string]*formats.GAF
	generatedProducts map[string]bool
	productGAFs       map[string]*formats.GAF
	productGAFChecked map[string]bool

	// radar owns the presentation-only PICTURE→MAPPED→FINAL lifecycle. Its
	// inputs are rebuilt from the committed frame at draw time [03 §3.6].
	radar *render.MinimapService

	// FX radar markers are authored indexed GAF bytes. They are retained with
	// the battle HUD so FINAL can copy the selected frame directly, without
	// recoloring through the GUI palette [03 §3.9].
	radarBlipGAF      *formats.GAFEntry
	radarCommanderGAF *formats.GAFEntry
	radarFeatureGAF   *formats.GAFEntry

	// rebuildRadar's working storage, retained for the life of the HUD. The
	// contact list and the two blip-art lists are refilled from the committed
	// radar payload every frame and consumed inside that rebuild, and radarFinal
	// receives the FINAL copy the projectile/feature pass draws over. None of the
	// four outlives the call that fills it, so retaining them removes four
	// per-frame allocations of the whole radar payload
	// (docs/DESIGN_GPU_RENDERER.md §11.5 "CPU"). The art entries point into the
	// retained FX GAF, so a stale tail pins nothing the HUD does not already hold.
	radarContacts     []render.MinimapContact
	radarRegularArt   []*formats.GAFFrame
	radarCommanderArt []*formats.GAFFrame
	radarFinal        render.RadarSurface

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
	// [07 R-HUD-04 §1][I6]. scoreCountersOK guards the first frame, whose
	// counters are a baseline and not a kill.
	scorePrevKills  [frame.PlayerRowSlots]int
	scorePrevLosses [frame.PlayerRowSlots]int
	scoreCountersOK bool
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
func loadRetailBattleHUD(fs vfs.FSOps, sess *session.Session, cat *content.Catalog, pal *palette.Tables, shell *gameShell, windowContext *battleWindowContext) (*retailBattleHUD, error) {
	// TODO(question): identify the owned battle-root MAIN2.GUI opener. This HUD
	// has no existing MAIN2 load path, so do not synthesize one solely to run a
	// builder pass; its documented pre-transition build belongs at that opener.
	if fs == nil || sess == nil || cat == nil {
		return nil, fmt.Errorf("nanolathe: battle HUD load failed: no mounted content, session or catalog")
	}
	if pal == nil {
		return nil, fmt.Errorf("nanolathe: battle HUD load failed: the shared palette tables are required [03 §4.3]")
	}
	if windowContext == nil {
		return nil, fmt.Errorf("nanolathe: battle HUD load failed: no window build context")
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
	// COMIX is already a mandatory process resource for a frontend-backed
	// battle. Direct-map entry skips that frontend preload, so bind the same
	// authored FNT here rather than substituting a side font.
	primaryFont := (*formats.FNT)(nil)
	if shell != nil {
		primaryFont = shell.font
	}
	if primaryFont == nil {
		const logicalPrimary = "fonts/comix.fnt"
		primaryFont, err = formats.LoadFNTFile(fs, logicalPrimary)
		if err != nil {
			return nil, hudAssetError(fs, logicalPrimary, "the primary COMIX FNT [03 R-FONT-01 §5]", err)
		}
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
	captions := windowContext.captions()
	optionsWin := loadGUIOptional(fs, "guis/armopt.gui", "options window [07 \"Tab options menu and manual exit\"]", captions)
	talkWin := loadGUIOptional(fs, "guis/talk.gui", "single-player chat window [07 §5 \"Chat\"]", captions)
	optionsGAF := loadGAFOptional(fs, "anims/armopt.gaf", "options GAF [07 \"Tab options menu and manual exit\"]")
	exitWin := loadGUIOptional(fs, "guis/exitmenu.gui", "exitmenu.gui [07 \"Tab options menu and manual exit\"]", captions)
	confirmWin := loadGUIOptional(fs, "guis/yesorno.gui", "yesorno.gui [07 \"Tab options menu and manual exit\"]", captions)
	restartWin := loadGUIOptional(fs, "guis/restart.gui", "restart.gui [07 R-FE-01 §7]", captions)
	// Optional modal fonts — degradable [07 §4]. Startup hands the GUI window
	// slot 0 = hattfont12 and slot 1 = hattfont11; a missing GAF font is a null
	// slot, not fatal [03 R-FONT-01 §5].
	modalFont := loadGAFFontOptional(fs, "anims/hattfont12.gaf", "hattfont12.gaf [07 §4]")
	modalFontSmall := loadGAFFontOptional(fs, "anims/hattfont11.gaf", "hattfont11.gaf [03 R-FONT-01 §5]")
	// The slide strip's band: frame index 1 of the common GUI GAF's LIGHTBAR
	// entry, cached at battle-data initialization with its hotspot words
	// zeroed so the blit lands exactly at the strip origin [07 R-HUD-04 §4].
	var stripArt *formats.GAFFrame
	if common != nil {
		if entry, ok := common.Find("LIGHTBAR"); ok && len(entry.Frames) > 1 && entry.Frames[1].Frame != nil {
			stripArt = entry.Frames[1].Frame
		} else {
			hudAssetWarning(fs, "anims/commongui.gaf", "LIGHTBAR has no second frame for the slide strip [07 R-HUD-04 §4]", fmt.Errorf("missing authored frame"))
		}
	}
	// The options opener relabels `MISSION` to the translated `Settings`
	// whenever the session kind is skirmish or multiplayer; only a campaign
	// mission keeps the authored `Briefing` (and reaches BRIEFING.GUI from it;
	// the other kinds reach GAMEOPTIONS.GUI) [07 R-FE-01 §7]. The button is
	// relabelled, never hidden or greyed. With no translation table loaded
	// the key is returned verbatim [02 "Translation table"].
	optionsRelabel := optionsWin != nil && !(sess.Mission != nil && sess.Mission.Type == mission.TypeCampaign)
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
	resultWin := loadGUIOptional(fs, "guis/endmsn.gui", "endmsn.gui [07 §11]", captions)
	resultGAF := loadGAFOptional(fs, "anims/endmsn.gaf", "endmsn.gaf [07 §11]")
	var resultPanel *ui.Panel
	h := &retailBattleHUD{
		shell: shell, windowContext: windowContext, optionsRelabel: optionsRelabel,
		side: side, cat: cat, owner: sess.LocalOwner, anchors: anchors, console: console, guiFont: guiFont, primaryFont: primaryFont, pal: pal,
		panelTop: panelTop, panelSide: panelSide, panelBottom: panelBottom,
		intGAF: intGAF, common: common, oldMain: oldMain, share: share, logos: logos,
		optionsGAF: optionsGAF, optionsWin: optionsWin, talkWin: talkWin, exitWin: exitWin, confirmWin: confirmWin, restartWin: restartWin,
		modalFont: modalFont, modalFontSmall: modalFontSmall, stripArt: stripArt,
		pausedFrame: pausedFrame, victoryFrame: victoryFrame, defeatFrame: defeatFrame,
		resultWin: resultWin, resultGAF: resultGAF, resultPanel: resultPanel,
		fs:                fs,
		pages:             make(map[string]*formats.GAF),
		windows:           make(map[string]*gui.Window),
		windowBuilt:       make(map[string]bool),
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
	// The three contact-pass markers, in the order the contacts pass draws them
	// [03 §3.9]: the regular unit blip is `radlogo`, whose ten frames are the
	// ten player colours the owning-player selector indexes; the commander
	// marker is `radlogohigh` frame 0, one ring around whichever unit's
	// identity matches the commander slot; and the feature marker is
	// `nuclogo`, indexed by the same owning-player selector as the blip.
	// `h2oboom2` is loaded by the same FX initialization but no contacts-pass
	// branch reads it.
	if fx := loadGAFOptional(fs, "anims/fx.gaf", "radar FX markers [03 §3.9]"); fx != nil {
		h.radarBlipGAF, _ = fx.Find("radlogo")
		h.radarCommanderGAF, _ = fx.Find("radlogohigh")
		h.radarFeatureGAF, _ = fx.Find("nuclogo")
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
	placeBattleModal(h.restartWin, w, height)
	h.placeTalkWindow(w, height)
}

// openTalkWindow completes TALK.GUI's authored bottom-strip placement and
// builds its retained editor only when Enter opens it [07 §5 "Chat"].
func (h *retailBattleHUD) openTalkWindow() {
	if h == nil || h.talkBuilt {
		return
	}
	h.placeTalkWindow(int(h.screenW), int(h.screenH))
	h.installWindow(h.talkWin, nil)
	h.talkBuilt = true
	if h.talkWin != nil {
		h.talkPanel = ui.NewPanel(h.talkWin)
		if sendTo := h.talkPanel.Index("SENDTO"); sendTo >= 0 {
			h.talkPanel.SetActiveAt(sendTo, false)
		}
	}
}

func (h *retailBattleHUD) placeTalkWindow(w, height int) {
	if h == nil || h.talkWin == nil || w <= 0 || height <= 0 {
		return
	}
	x, y := int32(128), int32(height)-h.talkWin.Rect.H
	h.talkWin.Rect.X, h.talkWin.Rect.Y = x, y
	h.talkWin.OriginX, h.talkWin.OriginY = x, y
	if len(h.talkWin.Gadgets) != 0 {
		h.talkWin.Gadgets[0].Rect.X, h.talkWin.Gadgets[0].Rect.Y = x, y
	}
}

func battleFrame(g *formats.GAF, name string) (*formats.GAFFrame, error) {
	if g == nil {
		return nil, fmt.Errorf("nanolathe: battle HUD frame lookup failed: no interface GAF while looking for %s", name)
	}
	e, ok := g.Find(name)
	if !ok || len(e.Frames) == 0 || e.Frames[0].Frame == nil {
		return nil, fmt.Errorf("nanolathe: battle HUD frame lookup failed: the interface GAF has no %s [02 §6]", name)
	}
	return e.Frames[0].Frame, nil
}

// openOptionsWindow builds the parsed ARMOPT record only when the running
// battle opens it. The caller relabel follows the build and never reassigns
// the original caption's accelerator [07 R-WGT-01 §3].
func (h *retailBattleHUD) openOptionsWindow() {
	if h == nil || h.optionsBuilt {
		return
	}
	h.installWindow(h.optionsWin, h.optionsGAF)
	h.optionsBuilt = true
	if h.optionsWin != nil {
		h.optionsPanel = ui.NewPanel(h.optionsWin)
	}
	if !h.optionsRelabel || h.optionsWin == nil {
		return
	}
	label := optionsSettingsLabel
	if captions := hudCaptionTranslator(h); captions != nil {
		label = captions.Translate(label)
	}
	for i := range h.optionsWin.Gadgets {
		if gui.Name16Equal(h.optionsWin.Gadgets[i].Name, optionsMissionButton) {
			h.optionsWin.Gadgets[i].Text = label
			h.optionsWin.Gadgets[i].Labels = nil
		}
	}
}

func (h *retailBattleHUD) openExitWindow() {
	if h == nil || h.exitBuilt {
		return
	}
	h.installWindow(h.exitWin, nil)
	h.exitBuilt = true
	if h.exitWin != nil {
		h.exitPanel = ui.NewPanel(h.exitWin)
	}
}

func (h *retailBattleHUD) openConfirmWindow() {
	if h == nil || h.confirmBuilt {
		return
	}
	h.installWindow(h.confirmWin, nil)
	h.confirmBuilt = true
	if h.confirmWin != nil {
		h.confirmPanel = ui.NewPanel(h.confirmWin)
	}
}

// openRestartWindow builds RESTART.GUI only after EXITMENU has yielded to its
// child. The parsed record remains inert at battle entry [07 R-WGT-01 §3].
func (h *retailBattleHUD) openRestartWindow() {
	if h == nil || h.restartBuilt {
		return
	}
	h.installWindow(h.restartWin, nil)
	h.restartBuilt = true
}

func (h *retailBattleHUD) openResultWindow() {
	if h == nil || h.resultBuilt {
		return
	}
	h.installWindow(h.resultWin, h.resultGAF)
	h.resultBuilt = true
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
		return nil, fmt.Errorf("nanolathe: battle side selection failed: no compiled side definitions [02 §6]")
	}
	idx := 0
	if sess != nil {
		if sess.Mission != nil && sess.Mission.Type == mission.TypeCampaign {
			return campaignBattleSide(fs, sess.Mission, cat, shell)
		}
		// The PLAYER RECORD's side, not the setup row's: a load restores only
		// the rule words and the map name into the setup record, which would
		// leave a restored battle rendering the Arm interface for a Core
		// player [08 R-SKIR-01 §2] "Save persistence".
		if side, ok := sess.SideForOwner(int(sess.LocalOwner)); ok {
			idx = side
		}
	}
	if idx < 0 || idx >= len(cat.Sides) || cat.Sides[idx] == nil {
		return nil, fmt.Errorf("nanolathe: battle side selection failed: local side %d is unavailable [02 §6]", idx)
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
		return "", fmt.Errorf("nanolathe: campaign side lookup failed: the mission carries no campaign file path [08 \"Enumeration of campaigns\"]")
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
	//
	// Both are positioned from WORLD coordinates — each projects through the
	// camera at the RECORD step and subtracts the view origin — so both belong
	// inside a world region, and the frame's own region closed before this stage
	// began. Without the bracket the modern executor left them untransformed
	// while it shrank the world under them: at half scale the ghost sat twice as
	// far from the framebuffer origin as the site it marked, which is the
	// play-test report of build placement being "way off" when zoomed out
	// (DESIGN_GPU_RENDERER §16.3). Anything in this block positioned from the
	// pointer instead would have to stay outside the bracket; nothing is.
	//
	// The region is opened only on a frame that has one of the two to draw, so an
	// ordinary frame pays neither marker nor the schedule submission each one
	// costs the modern executor.
	if b != nil {
		queueFeedback := c.Enhanced() && b.resourceQueueFeedback != nil
		overlay := b.worldOverlayArmed(cur) || (cur != nil && queueFeedback)
		if overlay {
			c.BeginWorldOverlay()
		}
		b.drawBuildGhost(c)
		b.drawCommandDrag(c)
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
			drawQueueOverlay(c, b, cur, cur.Tick, b.battleState().Input.ShiftHeld || queueFeedback, cur.Selection.LocalPlayer, b.cam.Tracked(), b.footerHoverUnit)
		}
		if overlay {
			c.EndWorldOverlay()
		}
	}
	if b != nil {
		b.drawTacticalRangeLegend(c, cur)
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
		mouse, _ := c.Input().PointerSample()
		h.updateHoveredGadget(b, cur, int32(mouse.X), int32(mouse.Y))
	}
	ok := false
	ok = cur != nil
	if ok && cur != nil {
		h.drawResources(c, cur, presented.Resources)
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
	// The persisted stand-alone clock is a late composer layer. It is distinct
	// from the Space-held LIGHTBAR readout above and remains below every linked
	// battle window [07 R-CAM-01 §6][07 R-HUD-04 §4].
	h.drawClock(c, b, cur)
	h.drawBattleMenu(c, b)
	// The unit information screen is a child window over the battle
	// [07 R-HUD-03 §8].
	h.drawUnitInfo(c)
	// UNITINFO owns no keyboard, so Enter may open TALK while it remains on the
	// linked window stack. TALK is the newer child and paints above it [07 §3].
	h.drawTalk(c, b)
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
