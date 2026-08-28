package main

// Retail battle HUD composition. This file deliberately contains no Nanolathe
// layout constants for controls: the side's SIDEDATA anchors, intgaf PANEL
// frames, and authored .GUI windows are the layout. The few fixed coordinates
// below are the retail panel-shell call sites recovered from TotalA.exe.

import (
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

	panelTop           *formats.GAFFrame
	panelSide          *formats.GAFFrame
	panelBottom        *formats.GAFFrame
	intGAF             *formats.GAF
	common             *formats.GAF
	oldMain            *formats.GAF
	share              *formats.GAF
	logos              *formats.GAF
	optionsGAF         *formats.GAF
	optionsWin         *gui.Window
	exitWin            *gui.Window
	confirmWin         *gui.Window
	modalFont          *formats.GAFEntry
	pausedFrame        *formats.GAFFrame
	victoryFrame       *formats.GAFFrame // [07 §11] igvictory from anims/igtitles.gaf via intgaf/gui machinery
	defeatFrame        *formats.GAFFrame // [07 §11] igdefeat from anims/igtitles.gaf
	resultWin          *gui.Window       // [07 §11] authored ENDMSN.GUI result surface
	resultGAF          *formats.GAF      // [07 §11] authored endmsn.gaf outcome controls
	resultVictoryFrame *formats.GAFFrame // [07 §11] authored endmsn.gaf victory copy
	resultDefeatFrame  *formats.GAFFrame // [07 §11] authored endmsn.gaf defeat copy
	resultPanel        *ui.Panel         // shared authored gesture state [07 §3]

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
func loadRetailBattleHUD(fs vfs.FSOps, sess *session.Session, cat *content.Catalog, pal *palette.Tables) (*retailBattleHUD, error) {
	if fs == nil || sess == nil || cat == nil {
		return nil, fmt.Errorf("battle HUD: missing VFS, session, or catalog")
	}
	if pal == nil {
		return nil, fmt.Errorf("battle HUD: PALETTE.PAL tables are required [03 §4.3]")
	}
	side, err := battleSide(sess, cat)
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
	// EXITMENU and YESORNO are opened with the executable's 0x1000 placement
	// flag. The modal initializer replaces their authored origins with sentinels,
	// then centers them in the 512-pixel playfield to the right of the rail.
	if exitWin != nil {
		placeBattleModal(exitWin, 640, 480)
	}
	if confirmWin != nil {
		placeBattleModal(confirmWin, 640, 480)
	}
	h := &retailBattleHUD{
		side: side, cat: cat, owner: sess.LocalOwner, anchors: anchors, console: console, guiFont: guiFont, pal: pal,
		panelTop: panelTop, panelSide: panelSide, panelBottom: panelBottom,
		intGAF: intGAF, common: common, oldMain: oldMain, share: share, logos: logos,
		optionsGAF: optionsGAF, optionsWin: optionsWin, exitWin: exitWin, confirmWin: confirmWin,
		modalFont: modalFont, pausedFrame: pausedFrame, victoryFrame: victoryFrame, defeatFrame: defeatFrame,
		resultWin: resultWin, resultGAF: resultGAF, resultVictoryFrame: resultVictoryFrame, resultDefeatFrame: resultDefeatFrame, resultPanel: resultPanel,
		fs:         fs,
		pages:      make(map[string]*formats.GAF),
		windows:    make(map[string]*gui.Window),
		pageCounts: make(map[string]int),
	}
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
	// MAPPED consumes the palette-install GUI remap and the active logical fog
	// index. Sensor callbacks are supplied from the committed frame by
	// rebuildRadar; the HUD never binds to mutable visibility state [03
	// §3.4][03 §3.8].
	guiRemap := []byte(nil)
	fogFill := render.FogDarkPaletteIndex
	if pal != nil {
		mapped := pal.GUIToBase()
		guiRemap = mapped[:]
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
// authored bytes; their recorded dimensions pass through the generic ALP
// source/destination path, including the observed 252×252 and 252×256 maps
// [fmt tnt][03 §3.7].
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

// placeBattleModal applies the established 0x1000 modal placement at the
// negotiated logical display size. The battle rail occupies x=0..127; modal
// centering therefore uses the remaining width and adds 128 [07 "Tab options
// menu and manual exit"].
func placeBattleModal(window *gui.Window, screenW, screenH int) {
	if window == nil {
		return
	}
	x := (screenW-128-int(window.Rect.W))/2 + 128
	y := (screenH - int(window.Rect.H)) / 2
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

func battleSide(sess *session.Session, cat *content.Catalog) (*content.SideDef, error) {
	if cat == nil || len(cat.Sides) == 0 {
		return nil, fmt.Errorf("battle HUD: no compiled side definitions [02 §6]")
	}
	idx := 0
	if sess != nil && sess.Mission != nil && sess.Mission.Type == mission.TypeCampaign { // campaign missions use commander identity below
		idx = -1
	}
	if sess != nil {
		// Skirmish retains the lobby's authored SIDE ordinal. Mission sessions
		// do not have a lobby row, so their local commander selects the side.
		if sess.Mission == nil || sess.Mission.Type != mission.TypeCampaign {
			owner := int(sess.LocalOwner)
			if owner >= 0 && owner < len(sess.Skirmish.Players) {
				idx = sess.Skirmish.Players[owner].Side
			}
		}
		if idx < 0 {
			if sess.Units != nil {
				for _, u := range sess.Units.Iter() {
					if u == nil || !u.Alive || u.Owner != sess.LocalOwner || u.Def == nil {
						continue
					}
					for i, side := range cat.Sides {
						if side != nil && strings.EqualFold(side.Commander, u.Def.UnitName) {
							idx = i
							break
						}
					}
					if idx >= 0 {
						break
					}
				}
			}
		}
	}
	if idx < 0 || idx >= len(cat.Sides) || cat.Sides[idx] == nil {
		return nil, fmt.Errorf("battle HUD: local side %d is unavailable [02 §6]", idx)
	}
	return cat.Sides[idx], nil
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
			// Selection.Primary is the authoritative focus handle published with
			// the frame. A separate hover field is not yet established at this
			// seam, so do not consult the live unit pool or pointer picker.
			drawQueueOverlay(c, cur, cur.Tick, b.battleState().Input.ShiftHeld, cur.Selection.LocalPlayer, cur.Selection.Primary)
		}
	}
	// The shell call order is PANELTOP, PANELBOT, PANELSIDE. The two horizontal
	// frames are static at the authored 129-pixel rail boundary; only the side
	// strip and its GUI contents use the panel slide offset [07 §6].
	blitBattlePanel(c, h.panelTop, 129, 0)
	blitBattlePanel(c, h.panelBottom, 129, 480-32)
	offset := 0
	if b != nil {
		offset = int(b.battleState().PanelOffset)
	}
	blitBattlePanel(c, h.panelSide, 0, offset)

	ok := false
	ok = cur != nil
	if ok && cur != nil {
		h.drawResources(c, cur)
		h.drawSelectedUnit(c, cur)
		h.drawTopStatusValues(c, cur)
	}
	h.drawSidePage(c, b, offset, cur)
	// Stock ARMINT.GAF and CORINT.GAF inspection shows PANELSIDE's decoded
	// 129×480 raster is opaque at every pixel, including the radar area; there
	// is no authored transparent cutout to preserve by clipping [fmt gaf].
	// Compose the radar after every moving-rail pass so the fixed 126×126 radar
	// canvas survives both the side frame and panel slide states. Modal/result
	// overlays remain later layers and may cover it transiently, without
	// mutating the cached FINAL surface [03 §3.6][07 §6].
	h.drawMinimap(c, b, cur)
	if paused {
		h.drawPausedTitle(c)
	}
	h.drawBattleMenu(c, b)
	if b != nil {
		b.drawStatusMessage(c, cur)
	}
	var result frame.ResultView
	if cur != nil {
		result = cur.Result
	}
	h.drawResultOverlay(c, b, result)
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
	// Wipe and replay the completed callback sequence for this frame. This
	// presentation cache is intentionally rebuilt from immutable values, so a
	// stale callback cannot survive a frame with no sensors [03 §3.10].
	h.radar.Wipe()
	for _, circle := range cur.Radar.Circles {
		switch circle.Kind {
		case 1:
			h.radar.RadarJam(circle.U, circle.V, circle.Radius)
		case 2:
			h.radar.SonarJam(circle.U, circle.V, circle.Radius)
		default:
			h.radar.Sensor(circle.U, circle.V, circle.Radius)
		}
	}
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
		// Range circles belong to the selected/range-status branch. An active
		// unit contributes its authored circles; an on/off-capable unit must also
		// be active. Unselected units never inherit circles from their blip [03
		// §3.9].
		rangeCircles := radarContactRangeEnabled(published)
		contact := render.MinimapContact{
			WorldX:      radarMapPixel(published.X),
			WorldZ:      radarMapPixel(published.Z),
			WorldY:      radarMapPixel(published.Y),
			Owner:       published.Owner,
			IsCommander: published.Commander, Stealth: published.Stealth,
			// The renderer's NoRadar slot carries the reviewed selected-unit
			// circle gate: an on/off-capable unit contributes its authored range only
			// while active. Cloak is kept separate for the blip blink gate [03 §3.9].
			NoRadar: !rangeCircles, Status: published.Status,
			BlinkSuppress: published.BlinkSuppress, Visible: published.Visible,
			LocalPlayer:  cur.Selection.LocalPlayer,
			RawDistRadar: published.RadarDistance, RawDistSonar: published.SonarDistance,
			RawDistJamR: published.RadarJam, RawDistJamS: published.SonarJam,
			MinimapMode: 1,
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
				NoRadar: contact.NoRadar, BlinkSuppress: contact.BlinkSuppress,
				Visible: contact.Visible, LocalPlayer: contact.LocalPlayer, MinimapMode: 1,
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
	viewW, viewH := b.cam.EffectiveView()
	centerX, centerZ := b.cam.X+viewW/2, b.cam.Z+viewH/2
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return
	}
	markerX, markerY := render.RadarProjection(centerX, centerZ, 0, playW, playH, layout)
	// Drawing and input receive the same layout and destination rectangle.
	c.DrawMinimapLayout(surf, dst, layout, cur.Radar.MarkerMode, markerX+layout.PadX, markerY+layout.PadY, h.paletteIndex(15))
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
			if gad.Kind != gui.KindButton && (int(frame.Width) != int(r.W) || int(frame.Height) != int(r.H)) {
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
		if text != "" && h.modalFont != nil && (gad.Kind == gui.KindButton || gad.Kind == gui.KindLabel || gad.Kind == gui.KindText) {
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

// modalGadgetFrame keeps modal art within the window's own GAF and the common
// GUI stock controls. Side-page GAFs contain unrelated entries with colliding
// names (notably EXIT) and are not part of ARMOPT's retail binding.
func (h *retailBattleHUD) modalGadgetFrame(gad gui.Gadget, page *formats.GAF, pressed, disabled bool) *formats.GAFFrame {
	name := gad.Art
	if name == "" {
		name = gad.Name
	}
	for _, gaf := range []*formats.GAF{page, h.common} {
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
	h.drawTextAt(c, hud.AnchorEnergyProduced, formatEnergyRate(rates.EnergyProduced), h.guiColor(10))
	h.drawTextAt(c, hud.AnchorEnergyConsumed, formatEnergyRate(-rates.EnergyConsumed), h.guiColor(12))
	h.drawTextAt(c, hud.AnchorMetalProduced, fmt.Sprintf("%.1f", rates.MetalProduced), h.guiColor(10))
	h.drawTextAt(c, hud.AnchorMetalConsumed, fmt.Sprintf("%.1f", -rates.MetalConsumed), h.guiColor(12))
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
	if value > 99999 || value < -99999 {
		return fmt.Sprintf("%dK", int(value/1000))
	}
	return fmt.Sprintf("%d", int(value))
}

func (h *retailBattleHUD) drawTopStatusValues(c *client.Client, f *frame.Frame) {
	if f == nil {
		return
	}
	localUnits := 0
	for i := range f.Units {
		if f.Units[i].Owner == h.owner {
			localUnits++
		}
	}
	if r, ok := h.anchors.ByIndex(hud.AnchorTotalUnits); ok {
		c.UIText(h.console, fmt.Sprintf("%d", localUnits), int(r.X1), int(r.Y1), h.guiColor(15))
	}
	if r, ok := h.anchors.ByIndex(hud.AnchorTotalTime); ok {
		c.UIText(h.console, hud.FormatGameTime(int(f.Tick)), int(r.X1), int(r.Y1), h.guiColor(15))
	}
}

func (h *retailBattleHUD) drawSelectedUnit(c *client.Client, f *frame.Frame) {
	var selected *frame.UnitView
	for i := range f.Units {
		u := &f.Units[i]
		if u.Owner == h.owner && u.Flags&hud.SelectionFlag != 0 {
			selected = u
			break
		}
	}
	if selected == nil {
		return
	}
	def, ok := h.defFor(selected)
	if !ok || def == nil {
		return
	}
	name := def.Name
	if name == "" {
		name = def.UnitName
	}
	if r, ok := h.anchors.ByIndex(hud.AnchorUnitName); ok {
		c.UIText(h.console, name, int(r.X1), int(r.Y1), h.guiColor(15))
	}
	if r, ok := h.anchors.ByIndex(hud.AnchorDescription); ok && def.Description != "" {
		c.UITextWidth(h.console, def.Description, int(r.X1), int(r.Y1), int(r.X2-r.X1), h.guiColor(15))
	}
	if r, ok := h.anchors.ByIndex(hud.AnchorDamageBar); ok {
		h.drawHealthBar(c, r, selected.Health, selected.MaxHealth)
	}
}

func (h *retailBattleHUD) drawHealthBar(c *client.Client, r hud.Rect, health, max int32) {
	left, top, right, bottom := r.Ordered()
	if right <= left || bottom <= top || max <= 0 {
		return
	}
	outer := h.guiColor(0)
	inner := h.guiColor(12)
	third := max / 3
	if health > third*2 {
		inner = h.guiColor(10)
	} else if health > third {
		inner = h.guiColor(14)
	}
	c.UIFillRect(int(left), int(top), int(right-left), int(bottom-top), outer)
	left++
	top++
	bottom--
	if right <= left || bottom <= top {
		return
	}
	width := int((int64(health) * int64(right-left)) / int64(max))
	if width > 0 {
		if width > int(right-left) {
			width = int(right - left)
		}
		c.UIFillRect(int(left), int(top), width, int(bottom-top), inner)
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

func (h *retailBattleHUD) drawSidePage(c *client.Client, b *battleSession, offset int, f *frame.Frame) {
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
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.Kind == gui.KindFont || gad.Kind == gui.KindPanel {
			continue
		}
		r := window.PlacedRect(i)
		r.Y += int32(offset)
		pressed := false
		if c.Input() != nil && c.Input().Mouse != nil && c.Input().Mouse.Held(input.MouseButtonLeft) {
			pressed = guiRectContains(r, int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y))
		}
		frame := h.gadgetFrame(gad, pageGAF, pressed, gad.GrayedOut != 0)
		if frame != nil {
			// .GUI controls use the authored rectangle origin; unlike the PANEL
			// shell, their GAF offsets are not applied [07 §4].
			c.UIBlit(frame, int(r.X), int(r.Y))
		}
		if gad.Kind == gui.KindButton && gad.Text != "" {
			text := gad.Text
			if len(gad.Labels) != 0 {
				text = gad.Labels[0]
			}
			if f != nil {
				candidates := []string{gad.Name, gad.Text}
				candidates = append(candidates, gad.Labels...)
				for _, candidate := range candidates {
					if label := hud.QueueCountLabel(f.OrderQueues, candidate); label != "" {
						text += " " + label
						break
					}
				}
			}
			if text != "" {
				c.UITextWidth(h.guiFont, text, int(r.X)+3, int(r.Y)+(int(r.H)-int(h.guiFont.Height))/2, int(r.W), h.guiColor(byte(gad.ColorF)))
			}
		}
	}
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
	// A committed frame is required for every HUD action [I6]. A frame without
	// a command-page builder still exposes the authored general/order controls.
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
	offset := int32(0)
	if b != nil {
		offset = int32(b.battleState().PanelOffset)
	}
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut != 0 || gad.Kind != gui.KindButton {
			continue
		}
		r := window.PlacedRect(i)
		r.Y += offset
		if !guiRectContains(r, x, y) {
			continue
		}
		upperName := strings.ToUpper(gad.Name)
		upperText := strings.ToUpper(gad.Text)
		// Page navigation data-driven [R-P0-03][07 §9] C10
		if strings.Contains(upperName, "NEXTPAGE") || strings.Contains(upperName, "NEXT") && strings.Contains(upperName, "PAGE") || strings.Contains(upperName, "PAGEDOWN") {
			if rightClick {
				return true
			}
			b.nextBuildPage()
			return true
		}
		if strings.Contains(upperName, "PREVPAGE") || strings.Contains(upperName, "PREV") && strings.Contains(upperName, "PAGE") || strings.Contains(upperName, "PAGEUP") {
			if rightClick {
				return true
			}
			b.prevBuildPage()
			return true
		}
		if strings.Contains(upperName, "NEXT") || strings.Contains(upperText, "NEXT") {
			if rightClick {
				return true
			}
			// Generic NEXT is active only when the committed page has another
			// page [07 §9].
			if f.CommandPage.PageCount > 1 {
				b.nextBuildPage()
				return true
			}
		}
		if strings.Contains(upperName, "PREV") || strings.Contains(upperText, "PREV") {
			if rightClick {
				return true
			}
			if f.CommandPage.PageCount > 1 {
				b.prevBuildPage()
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
					if err := h.dispatchFactoryBuild(b, prodKey, factoryBuildDelta(b.battleState().Input.ShiftHeld, rightClick)); err != nil {
						h.dispatchErr = err
					}
					return true
				}
				if rightClick {
					return true
				}
				b.armPlacement(prodDef)
				return true
			}
		}
		upper := strings.ToUpper(gad.Name)
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
	offset := int32(0)
	if b != nil {
		offset = int32(b.battleState().PanelOffset)
	}
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut != 0 || gad.Kind != gui.KindButton {
			continue
		}
		r := window.PlacedRect(i)
		r.Y += offset
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
	offset := int32(0)
	if b != nil {
		offset = int32(b.battleState().PanelOffset)
	}
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut != 0 || gad.Kind != gui.KindButton {
			continue
		}
		r := window.PlacedRect(i)
		r.Y += offset
		if guiRectContains(r, x, y) {
			return i
		}
	}
	return -1
}

func (h *retailBattleHUD) sameButton(b *battleSession, x0, y0, x1, y1 int32) bool {
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
	if b == nil || f == nil || f.CommandPage.Builder == 0 {
		window, page := h.loadWindow(name)
		return window, page, nil
	}
	if b.cat == nil || f.CommandPage.PageCount == 0 {
		// A nonzero builder with no valid page count is malformed command-page
		// state, not the established empty-selection case.
		return nil, nil, nil
	}
	// The committed CommandPage identifies both the builder and page. No live
	// unit selection or synthesized view participates in GUI selection [I6].
	view, found := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !found || view.Owner != h.owner || !b.snapshotBuilder(view) {
		// A non-empty command-page identity is not an empty selection. Do not
		// display GEN for stale/malformed builder state [07 §9].
		return nil, nil, nil
	}
	def, ok := h.defFor(&view)
	if !ok || def == nil || !def.Builder {
		return nil, nil, nil
	}
	pageNum := hud.ClampPage(int(f.CommandPage.Page), int(f.CommandPage.PageCount))
	name = strings.ToLower(def.UnitName) + fmt.Sprintf("%d", pageNum+1)
	window, page, err := h.loadWindowRequired(name)
	if err != nil {
		return nil, nil, err
	}
	return window, page, nil
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

func (h *retailBattleHUD) gadgetFrame(gad gui.Gadget, page *formats.GAF, pressed, disabled bool) *formats.GAFFrame {
	name := gad.Art
	if name == "" {
		name = gad.Name
	}
	var entry *formats.GAFEntry
	stockButtons := false
	for _, g := range []*formats.GAF{page, h.intGAF, h.oldMain, h.share, h.common} {
		if g == nil {
			continue
		}
		if found, ok := g.Find(name); ok {
			entry = found
			break
		}
	}
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
	bottom := int32(480)
	if h.panelBottom != nil {
		bottom = 480 - int32(h.panelBottom.Height)
	}
	return y >= top && y < bottom
}
