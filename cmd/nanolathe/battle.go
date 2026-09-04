package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// battleSession is the composition root for the windowed battle view. It owns
// the integrated session (all twelve kernel phases) and the interaction state:
// selection, order latch, and build placement.
type battleSession struct {
	sess  *session.Session
	cat   *content.Catalog
	cam   *camera.Camera
	hud   *retailBattleHUD
	fs    vfs.FSOps
	shell *gameShell

	millisSource clock.MillisSource // host millisecond source for Session.Step [01 §4.1]

	battleUI         *ui.BattleState
	returnToMenu     func(*client.Client)
	returnToSkirmish func(*client.Client)
	ended            bool

	anchors   hud.Anchors
	anchorsOK bool
	guiWin    *gui.Window
	guiOK     bool

	controller *BattleController

	// postBattle is created once, at the first committed terminal ResultView.
	// It owns the frozen result presentation sequence; the live Session is not
	// consulted after this boundary [03 §2.4][08 R-CAMP-01 §6].
	postBattle           *session.PostBattleController
	postBattleClock      float64
	postBattleLastUnit   int64
	postBattleEffectPos  int
	postBattleGlamour    *formats.PCX
	postBattleNormalPal  *palette.Tables
	postBattleFadePal    palette.Tables
	postBattleFadeCur    [1024]byte
	postBattleFadeDst    [1024]byte
	postBattleFadeStep   [1024]int16
	postBattleFadeReady  bool
	postBattleFadeLevels []int

	// AppliedShake records only the last committed camera offset consumed by
	// this battle owner. The client renderer remains a pure frame reader; the
	// battle camera applies the authoritative phase-10 random walk once after
	// Session.Step, before the following draw [03 §5.6][I6].
	appliedShakeX int32
	appliedShakeY int32

	// The scroll pass's own clock [07 §10]. scrollAnchor is the previous host
	// frame's scaled reading (floor(hostMillis*30/1000), the timebase of
	// [01 §4.1]) and scrollDelta is this frame's reading minus it — the same
	// raw delta retail's tick-budget step stores and its scroll pass consumes.
	// Presentation-only [I6].
	scrollAnchor    int32
	scrollDelta     int32
	scrollAnchorSet bool

	// scrollSpeedByte caches the persisted scrollspeed byte [02 "Settings"]
	// [07 §10] C2. It is read once — at battle entry, by primeScrollSetting —
	// rather than from disk on every host frame [WU-19-114]; scrollSetting
	// falls back to loading it lazily so a battleSession built without going
	// through the composition root (tests) still resolves a real value. Valid
	// bytes are 1..255, so 0 doubles as "not primed yet".
	scrollSpeedByte byte

	// The footer's pointer record [07 R-HUD-03 §1]. Both words are
	// presentation-only: the simulation neither writes nor reads them [I6].
	// footerHoverUnit is rewritten only while the pointer is inside the view
	// with no drag rectangle armed, or over the minimap; anywhere else — the
	// side rail, the top or bottom strip — it keeps its previous value, so a
	// unit hovered on the way to the panel stays in the footer.
	// footerHoverFeature is recomputed every frame wherever the pointer is.
	footerHoverUnit    pool.Handle
	footerHoverFeature string

	// The battle shell's message ring [07 R-HUD-03 §14.3]. F12 clears it, F3
	// walks it, and the game-speed announcement posts into it.
	messages *frame.MessageRing

	// panelHoldFlag is interface-flags bit 0x80. It has exactly two readers:
	// the score panel's showing test, and the kill-credit finalize's flash arm
	// [07 R-HUD-04 §1][07 R-CAM-01 §14]. Presentation-only [I6].
	panelHoldFlag bool

	// watcherSlot latches the world-rebuild tail's watcher branch for the local
	// slot. Besides the camera jump, that tail clears render-flags bits 0 and 1
	// — the mapping and LOS masks — so a watcher's minimap is unmasked from its
	// first frame [07 R-CAM-01 §14][03 R-MM-01 §3]. Presentation-only [I6].
	watcherSlot bool

	// visitedUnits and currentUnit are the `n` unit cycle's state
	// [07 R-CAM-01 §2]. Retail keeps the visited bits in each unit's status
	// word; presentation may not write simulation state, so the cycle keeps its
	// own set here [I6]. The set is only looked up, never ranged, so it takes
	// part in no order-producing iteration [I1].
	visitedUnits map[pool.Handle]bool
	currentUnit  pool.Handle
}

// factoryBuildDelta applies the retail signed button count: left click adds
// one, Shift-left adds five; right-click variants pass negative values.
func factoryBuildDelta(shiftHeld, rightClick bool) int {
	count := 1
	if shiftHeld {
		count = 5
	}
	if rightClick {
		count = -count
	}
	return count
}

var clPtr *client.Client

// runBattleView launches the windowed battle view over the real session.
func runBattleView(opts Options, cs *contentSet) error {
	request, err := directMapBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return err
	}
	sess, cat := authoritative.Session, authoritative.Session.Catalog
	var (
		b  *battleSession
		cl *client.Client
	)
	cl, err = client.New(client.Options{
		Buffer: sess.Snapshot,
		Width:  retailScreenW,
		Height: retailScreenH,
		Title:  "Nanolathe — " + opts.Map,
		Step: func(delta float64) {
			b.viewerStep(delta, cl)
		},
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	cl.SetModelFS(cs.fs)
	b, err = composeBattleEntry(sess, cat, cs, cl, nil)
	if err != nil {
		return err
	}
	clPtr = cl
	b.returnToMenu = func(cl *client.Client) {
		// The battle view has no menu shell callback; mark it ended and exit.
		b.ended = true
		if cl != nil {
			cl.RequestExit()
		}
	}
	defer b.teardown(cl)
	// Software cursor [07 §8]. The cursor GAF is mandatory for a windowed
	// battle, and installation happens before entering Ebitengine's loop.
	cursors, cerr := client.LoadCursors(cs.fs)
	if cerr != nil {
		return cerr
	}
	cl.SetCursors(cursors)
	fmt.Fprintln(os.Stderr, "nanolathe: battle view — drag=select left-click=action right-click=deselect/cancel M=move A=attack P=patrol R=repair E=reclaim C=capture G=guard D=blast B=build X=cancel O=on/off N=stockpile Esc=cancel 1..9=buildpage Shift=queue")
	return ebitenapp.Run(cl)
}

// composeBattleEntry is the single presentation composition for every
// constructed battle session. Session construction has already completed the
// authoritative entry tail's tick-zero per-player priming and second resource
// grant [08 R-ENTRY-01 §8]. This helper does not change which authored HUD
// surfaces the existing battle loader provides.
func composeBattleEntry(sess *session.Session, cat *content.Catalog, cs *contentSet, cl *client.Client, shell *gameShell) (*battleSession, error) {
	b, err := composeBattleEntryDetached(sess, cat, cs, shell, nil)
	if err != nil {
		return nil, err
	}
	if cl != nil {
		installBattleClient(cl, b)
	}
	return b, nil
}

// composeBattleEntryDetached performs every fallible presentation
// construction without touching the active client or frontend. A saved
// camera is applied only to this candidate and is installed at the same
// render-thread commit as the rest of the battle presentation [08
// R-SAVE-02 §11–§12].
func composeBattleEntryDetached(sess *session.Session, cat *content.Catalog, cs *contentSet, shell *gameShell, savedCamera *save.Camera) (*battleSession, error) {
	if sess == nil {
		return nil, fmt.Errorf("nil session")
	}
	terrain := sess.World
	if terrain == nil {
		return nil, fmt.Errorf("selected mission has no terrain data")
	}
	pal, err := loadPaletteStrict(cs)
	if err != nil {
		return nil, err
	}

	terrainW := int32(terrain.CellW * 16)
	terrainH := int32(terrain.CellH * 16)
	// Camera clamp uses the same playable insets consumed by minimap input and
	// marker projection; raw terrain extents include the void margins [07 §10].
	cam := camera.NewFromTerrain(terrainW, terrainH, terrain.PlayRight, terrain.PlayBottom, retailScreenW, retailScreenH)
	// The world rebuild resets the camera block before any battle-start writer
	// runs [08 R-ENTRY-01 §3 step 12]. The reset zeroes twenty-three
	// consecutive words of that block and writes the scroll-setting byte back;
	// the block spans the **current origin** and the **desired origin** as well
	// as the tracked object, follow target, bookmarks and hold state, so after
	// it `current = desired = (0, 0)` [07 R-CAM-01 §14 "the camera-block reset
	// leaves both origins at (0, 0)"]. §10's earlier list omitted the two
	// origins, which is why this used to be a bare pan that left the desired
	// origin alone. A campaign without a start-position special keeps (0, 0)
	// as both origins [08 "Campaign camera"].
	cam.JumpTo(0, 0)
	if savedCamera != nil {
		applyRetailSavedCamera(cam, savedCamera)
	} else {
		centerBattleStartCamera(sess, cam)
	}

	// The battle HUD is mandatory retail content: side-selected PANELTOP,
	// PANELSIDE, PANELBOT, the 30 SIDEDATA anchors, side fonts, and the authored
	// general command page [07 §6][07 §9].
	hud, err := loadRetailBattleHUD(cs.fs, sess, cat, pal)
	if err != nil {
		return nil, err
	}
	b := &battleSession{
		sess: sess, cat: cat, cam: cam, hud: hud, fs: cs.fs, shell: shell,
		millisSource: newMonotonicMillisSource(), battleUI: ui.NewProductionBattleState(),
		watcherSlot: sessionLocalIsWatcher(sess),
	}
	// Prime the scroll-speed cache once here, at battle entry, instead of
	// leaving the first camera-pan frame to fault it in lazily [WU-19-114].
	b.primeScrollSetting()
	// Rail detent cues are emitted by canonical UI state; this callback only
	// adapts the authored cue to the session audio sink [07 §6][I6].
	b.battleUI.SetPanelCue(func(name string) {
		if sess.Audio != nil {
			_ = sess.Audio.PlayUICue(name)
		}
	})
	return b, nil
}

// applyRetailSavedCamera loads the `Camera` account's origin. The load is a
// *jump*, not a glide: retail reads `X Position` / `Z Position` with the
// current origin as each default, writes them to the current origin, sets the
// view-dirty bit, clamps, copies current into desired, and clears the terrain
// cache-valid bit [07 R-CAM-01 §14 "a saved-camera load is a jump"]. Camera.
// JumpTo is exactly that write-clamp-copy, so the load no longer leaves a
// stale desired origin behind the restored one.
//
// The two "state bits" every camera writer touches are the view-dirty bit and
// the terrain view-cache-valid bit; neither is authored or saved, and a build
// that rebuilds the view every frame — this one — needs nothing beyond
// `desired := current`. That is what closes the old marker here.
func applyRetailSavedCamera(cam *camera.Camera, saved *save.Camera) {
	if cam == nil || saved == nil {
		return
	}
	cam.JumpTo(saved.XPosition, saved.ZPosition)
}

// installBattleClient is the non-fallible render-thread half of battle
// adoption. Resource construction and authoritative restoration have already
// succeeded before this grouped setter sequence runs [I6].
func installBattleClient(cl *client.Client, b *battleSession) {
	if cl == nil || b == nil || b.sess == nil {
		return
	}
	cl.SetSnapshot(b.sess.Snapshot)
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(b.cam)
	cl.SetPalette(b.hud.pal)
	cl.SetFNT(b.hud.console)
	s := loadedSettings()
	applyDamageBarsSetting(s)
	// `textlines`/`textscroll` configure the message ring and `screenchat`
	// sets its class filter; retail's startup loader installs these the same
	// way it installs damagebars [02 §3][07 R-HUD-03 §14.3][07 R-HUD-03 §14.4].
	applyMessageLineSettings(cl, s)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
	// The presentation effect pool needs each admitted effect's authored
	// per-frame holds, which live in the GAF entry the event names — an asset
	// the client owns and the session does not [06 R-WFX-01 §1][03 §1]. This
	// is the shell filling that seam with the same bank cache the draw pass
	// resolves frames through, so the cursor the pool advances and the frame
	// the composer blits can never come from two different readings of one
	// entry.
	b.sess.SetEffectTimingResolver(func(e render.Event) (render.FrameTiming, bool) {
		return cl.EffectFrameTiming(e.AssetID, e.Graphic)
	})
	// The strip families draw each smoke puff up to its own last frame, which
	// is drawn against the bound entry's frame count [03 R-STRIP-01 §2] — the
	// same asset the timing resolver above reads, through the same bank cache.
	b.sess.SetEffectEntryFrameCount(cl.EffectEntryFrameCount)
	// The feature phase reads two things out of a definition's GAF entries: the
	// current burn frame's geometry, which pass 3a scales the smoke jitter by,
	// and the die/reclaim/burn lifetime in visits, which is when an animation
	// record's successor is stamped [05 R-FEAT-01 §10]. Both come from the
	// client's own feature-GAF cache, so the frames the feature pass draws and
	// the frames the simulation counts are one reading. The warm pass compiles
	// the whole catalog here, off the simulation path, so no visit ever waits
	// on a load.
	b.sess.SetFeatureSequenceResolver(cl.FeatureSequence)
	if b.sess.Catalog != nil {
		cl.WarmFeatureSequences(b.sess.Catalog.Features)
	}
	attachBattleAudio(cl, b.sess, b.fs)
}

// teardown is the one idempotent battle-exit boundary. Presentation joins are
// detached before authoritative references are dropped, and every interaction
// latch is reset so a later load cannot reach the old battle [08 "Session
// states"][08 R-ENTRY-01 §8][I6].
func (b *battleSession) teardown(cl *client.Client) {
	if b == nil {
		return
	}
	if b.battleUI != nil {
		b.closeBattleMenu()
		b.battleUI.SetPanelCue(nil)
		b.battleUI.ResetInteraction()
	}
	if cl != nil {
		cl.SetUIStage(nil)
		cl.SetAudioService(nil)
		cl.SetTerrain(nil)
		cl.SetCamera(nil)
		cl.SetPalette(nil)
		cl.SetFNT(nil)
		cl.SetSnapshot(&frame.Buffer{})
		if in := cl.Input(); in != nil {
			*in = *input.NewState()
		}
	}
	detachBattleAudio(cl, b.sess)
	if b.controller != nil {
		b.controller.battle = nil
	}
	b.ended = true
	b.sess = nil
	b.cat = nil
	b.cam = nil
	b.hud = nil
	b.fs = nil
	b.shell = nil
	b.battleUI = nil
	b.controller = nil
	b.returnToMenu = nil
	b.returnToSkirmish = nil
}

// newBattleSession builds the integrated skirmish session for the window.
// It uses the canonical DirectSkirmishConfig normalization [08 "Skirmish configuration"] [GAP T14].
func newBattleSession(opts Options, cs *contentSet) (*session.Session, *content.Catalog, error) {
	request, err := directMapBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return nil, nil, err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return nil, nil, err
	}
	return authoritative.Session, authoritative.Session.Catalog, nil
}

// newBattleSessionWithConfig is the windowed composition path used by the
// skirmish lobby. The menu's per-slot and round settings reach the canonical
// session constructor [08 "Skirmish configuration"].
func newBattleSessionWithConfig(opts Options, cs *contentSet, cfg session.SkirmishConfig) (*session.Session, *content.Catalog, error) {
	return newBattleSessionWithConfigAndSource(opts, cs, cfg, newBattleSeedSource(opts))
}

// newBattleSessionWithConfigAndSource is the injectable composition seam for
// battle entry. The selected pair is copied into the session configuration
// before NewSkirmishWithFS performs any setup-owned draw [R-CORE-02].
func newBattleSessionWithConfigAndSource(opts Options, cs *contentSet, cfg session.SkirmishConfig, source BattleSeedSource) (*session.Session, *content.Catalog, error) {
	request, err := skirmishBattleRequest(opts, cs, cfg, headlessScenarioSkirmish, nil, source)
	if err != nil {
		return nil, nil, err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return nil, nil, err
	}
	return authoritative.Session, authoritative.Session.Catalog, nil
}

// configWithBattleSeeds is the composition boundary for skirmish setup. It
// asks the source exactly once and copies that pair into the config consumed
// by the session constructor [R-CORE-02].
func configWithBattleSeeds(cfg session.SkirmishConfig, source BattleSeedSource) session.SkirmishConfig {
	if source == nil {
		return cfg
	}
	seeds := source.NextBattleSeeds()
	cfg.RNGSimSeed = uint32(seeds.Simulation)
	cfg.RNGCrtSeed = seeds.CRT
	return cfg
}

// centerBattleStartCamera is the retail battle-start camera placement: the one
// camera writer between the world rebuild's camera reset and the first composed
// frame, and a *jump* rather than a glide [07 R-CAM-01 §12 "Battle-start
// placement"]. It branches on the session kind, exactly as retail does.
//
// Campaign (kind 1): the first start-position special with stored number 0 —
// the special authored as StartPos1 — is centred in the battle viewport. A
// mission with no such special keeps the world-rebuild reset position, and
// there is no diagnostic [08 "Campaign camera"]. The campaign spawner creates
// units straight from the mission's placement records and no commander, so
// nothing here looks for one.
//
// Skirmish (kind 2): retail's per-slot stamp helper resolves the slot's
// StartPos, creates the side's commander there, and centres the camera on the
// local player's commander [08 "Resource grant" and the stamp paragraph above
// it]. It never searches for a commander by name — it centres on the unit it
// just created — so the commander identity used here is the only one retail
// has: the definition name equals the Commander name on the owner's side
// record [08 R-SKIR-01 §3]. (Until this commit the search was a `"com"` suffix
// test on the unit name, which found no unit at all in Arm campaign mission 1 —
// it fields ARMFAV, ARMPW, ARMFLASH, ARMSTUMP, ARMROCK, ARMHAM and ARMGATE and
// no commander — leaving the camera at the reset origin and the world viewport
// black, and which would equally match any unit whose name merely ends in
// those letters.)
//
// Both branches centre through Camera.JumpToBattleViewCenter, which halves the
// battle viewport rather than the framebuffer and converts retail's camera
// origin into this build's [07 R-CAM-01 §12][03 §4.1]. Neither shears: both
// battle-start writers call the jump with `x = stampX − trunc(viewWidth/2)`
// and `z = stampZ − trunc(viewHeight/2)` and never read the Y word. The
// `(z − y/2)` shear of §12 belongs to the unit-position *glide* conversion
// alone [07 R-CAM-01 §14 "the battle-start jump has no height shear"], which
// is what glideToUnit applies and this path does not — the reading this file
// already had, now established rather than chosen.
//
// A watcher slot takes neither branch: see watcherBattleStartCamera.
func centerBattleStartCamera(sess *session.Session, cam *camera.Camera) {
	if sess == nil || cam == nil {
		return
	}
	if sessionLocalIsWatcher(sess) {
		watcherBattleStartCamera(cam)
		return
	}
	if sess.Mission != nil && sess.Mission.Type == mission.TypeCampaign {
		if start, ok := campaignStartPosition(sess.Mission); ok {
			cam.JumpToBattleViewCenter(int32(start.X), int32(start.Z))
		}
		// No special → keep the reset position, emit nothing
		// [08 "Campaign camera"].
		return
	}
	if u, ok := localCommanderUnit(sess); ok {
		cam.JumpToBattleViewCenter(int32(u.X>>16), int32(u.Z>>16))
	}
}

// sessionLocalIsWatcher reports whether the local slot carries the lobby
// record's watcher bit. The observer byte skirmish setup writes is the same
// exclusion everywhere else it is spent, so it is ORed in here exactly as the
// score panel's row filter does [07 R-HUD-04 §1].
func sessionLocalIsWatcher(sess *session.Session) bool {
	if sess == nil || sess.Econ == nil {
		return false
	}
	slot := int(sess.LocalOwner)
	if slot < 0 || slot >= len(sess.Econ.Players) {
		return false
	}
	p := sess.Econ.Players[slot]
	return p.Watcher || p.IsObserver
}

// watcherBattleStartCamera is the world-rebuild tail's watcher branch: instead
// of a stamp position it jumps to the camera origin
// `(trunc(viewWidth / 2), trunc(viewHeight / 2))` [07 R-CAM-01 §14 "the
// battle-start jump has no height shear"].
//
// A retail camera origin is the world point drawn at the *viewport's*
// top-left corner; this build's is the world point drawn at the framebuffer's
// top-left, so the leading inset comes off as well. BattleViewCenterOrigin is
// the only public converter that applies that inset, and it also subtracts
// half the span — so the point handed to it is retail's origin plus that same
// half span, `2 * trunc(view / 2)`, which reproduces the truncation exactly on
// an odd span too [07 R-CAM-01 §12][03 §4.1].
// minimapMaskWord is the render-flags word's mapping and LOS mask bits as the
// minimap contact pass reads them: the blip gate admits a unit when both are
// clear [03 R-MM-01 §3]. They are the `+Mapping`/`+LOS` toggles, and no `+`
// command vocabulary exists in this build, so an ordinary slot reads them set.
// The world-rebuild tail clears both for a watcher slot, whose view is
// unmasked from its first frame [07 R-CAM-01 §14].
func (b *battleSession) minimapMaskWord() uint8 {
	if b != nil && b.watcherSlot {
		return 0
	}
	return 1
}

func watcherBattleStartCamera(cam *camera.Camera) {
	if cam == nil {
		return
	}
	viewW, viewH := cam.BattleView()
	cam.JumpToBattleViewCenter(2*(viewW/2), 2*(viewH/2))
}

// campaignStartPosition returns the start-position special the campaign camera
// jumps to: the first special in authored record order that is a start position
// and whose stored number is 0. Retail stores the number as the authored suffix
// minus one, so StartPos1 is stored number 0; mission.Special keeps the suffix
// itself, hence the comparison against 1 [08 "Campaign camera"] [fmt ota].
// Record order is the OTA enumeration order and is deliberately not sorted:
// "first" is a scan, not a minimum.
func campaignStartPosition(m *mission.Mission) (mission.Special, bool) {
	if m == nil {
		return mission.Special{}, false
	}
	for _, sp := range m.Specials {
		if sp.Kind == 1 && sp.ID == 1 {
			return sp, true
		}
	}
	return mission.Special{}, false
}

// localCommanderUnit finds the local player's commander using retail's only
// commander identity: the unit's definition name equals the Commander name on
// its owner's side record [08 R-SKIR-01 §3]. Scan order is unit-pool record
// order, the order retail's own sweeps use.
func localCommanderUnit(sess *session.Session) (*units.Unit, bool) {
	if sess == nil || sess.Units == nil || sess.Catalog == nil {
		return nil, false
	}
	owner := int(sess.LocalOwner)
	if owner < 0 || owner >= len(sess.Skirmish.Players) {
		return nil, false
	}
	side := sess.Skirmish.Players[owner].Side
	if side < 0 || side >= len(sess.Catalog.Sides) || sess.Catalog.Sides[side] == nil {
		return nil, false
	}
	name := strings.TrimSpace(sess.Catalog.Sides[side].Commander)
	if name == "" {
		return nil, false
	}
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive || u.Def == nil || int(u.Owner) != owner {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(u.Def.UnitName), name) {
			return u, true
		}
	}
	return nil, false
}

// viewerStep runs one rendered frame: input → session ticks → camera pan.
func (b *battleSession) viewerStep(delta float64, cl *client.Client) {
	if b == nil || cl == nil {
		return
	}
	// The client composes the drag frame after world/fog and before the HUD;
	// this deferred bridge mirrors the input-owned gesture after every early
	// return as well as the normal controller path [03 §1][R-SEL-02A].
	defer b.syncSelectionDrag(cl)
	in := cl.Input()
	// Advance the canonical panel state during the host-frame update. Drawing
	// must remain a pure read of this state so hit testing and raster placement
	// use the same offset [07 §6][I6].
	// The rail slide's test is Space alone, unless a text editor has the focus
	// [07 §6]. Interface-flags bit 0x80 is *not* a term of it: that bit has
	// exactly two readers, the score panel's showing test and the kill-credit
	// finalize's flash arm [07 R-HUD-04 §1][07 R-CAM-01 §14]. It used to be
	// ORed in here, which pinned the rail open as well as the panel.
	spaceHeld := in != nil && in.Kbd != nil && in.Kbd.KeyHeld(input.KeySpace)
	editorFocused := b.hud != nil && b.hud.editorFocused()
	b.battleState().AdvancePanelNow(spaceHeld, editorFocused)
	// End-mission presentation takes ownership of the frame once the
	// authoritative result is latched. The authored result panel owns any
	// release-inside gesture; no battle hotkey or world command leaks through
	// [07 §3][07 §11].
	if b.isResultVisible() {
		// The first terminal frame freezes the result and installs the one
		// post-battle controller. From here on the controller and its effect
		// cursor own the presentation sequence; no live-world value is read
		// [03 §2.4][08 R-CAMP-01 §6].
		b.ensurePostBattleController()
		b.stepPostBattle(delta, in, cl)
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	state := b.battleState()
	// Modal ownership is decided at frame entry. Closing ARMOPT with Tab or
	// Escape must not hand the same frame's remaining mouse/key edges to the
	// battle controller [07 §2][07 §3].
	modalAtFrameStart := state.Modal() != ui.BattleModalClosed
	keyDown := func(key input.Key) bool {
		return in != nil && in.Kbd != nil && in.Kbd.KeyDown(key)
	}
	shiftHeld := in != nil && in.Kbd != nil && in.Kbd.HasShift()
	// F2 is the options window's own key, and Tab toggles it in battle mode;
	// Escape only ever closes it. The "ESC bit" name survives because Escape is
	// the bit's clearer, but token 0xE3 is F2, not Escape
	// [07 R-CAM-01 §2 "Escape versus F2"].
	if modalAtFrameStart {
		// Tab is the authored root-modal toggle. Handle it before the modal
		// dispatcher so the same edge cannot also activate a newly closed/opened
		// window. Escape remains owned by the modal handler (which applies its
		// back transition), but the whole frame is consumed either way.
		if (keyDown(input.KeyTab) || keyDown(input.KeyF2)) && state.Modal() == ui.BattleModalOptions {
			b.closeBattleMenu()
		} else {
			b.handleBattleMenuInput(in, cl)
		}
		if b.ended {
			return
		}
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	// The keyboard-ownership seam for a battle child window [07 §3]. The host
	// frame dispatches the active GUI first and "only afterwards runs the
	// battle hotkey dispatcher", so while the unit-information screen is up it
	// owns the queued keyboard tokens and no battle hotkey — Tab, F2, Escape,
	// F1 or any other — sees them. It runs after the options-modal block above
	// because a modal opened later sits higher on the GUI stack.
	//
	// A consumed token is replaced by zero for everything downstream
	// [07 R-WGT-01 §2], so the rest of the frame runs against a keyboard with
	// no edges. The held-key state is deliberately preserved: Shift and Ctrl
	// are live asynchronous queries made at draw and dispatch time, not entries
	// in the token queue [07 R-CAM-01 §2][R-P0-11 §3]. The mouse is untouched,
	// so the DONE button and the world outside the window still take clicks.
	if in != nil && b.unitInfoConsumeKeys(in.Kbd) {
		quiet := input.KeyboardState{}
		if in.Kbd != nil {
			quiet = *in.Kbd
			quiet.ResetEdges()
		}
		in = &input.State{Mouse: in.Mouse, Kbd: &quiet}
	}
	if keyDown(input.KeyTab) || (keyDown(input.KeyF2) && !shiftHeld) {
		b.openBattleMenu()
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	// Escape with the options window closed: an armed latch or placement
	// returns to idle, and an idle latch deselects everything. It does not open
	// the options window — that is F2's and Tab's row [07 R-CAM-01 §2].
	if keyDown(input.KeyEscape) {
		if b.battleState().Input.Latch == input.LatchNormal && b.battleState().Input.BuildDef == "" {
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
		}
		b.disarmPlacement()
		b.battleState().Input.Latch = input.LatchNormal
		b.battleState().Input.ShiftLatchSticky = false
		b.battleState().Input.HUDCaptured = false
		b.battleState().Input.DragActive = false
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	if state.Modal() != ui.BattleModalClosed {
		b.handleBattleMenuInput(in, cl)
	} else {
		if b.controller == nil {
			b.controller = NewBattleController(b)
		}
		// The scroll pass's raw delta is refreshed here, at retail's tick-budget
		// step, rather than down in the camera pass: the budget step runs before
		// hotkey dispatch, so the frame that presses pause still refreshes the
		// delta and it is that frame's value the pause freezes [07 §1][07 §10].
		b.refreshScrollDelta()
		// Phase 10's follow step runs inside the sub-tick loop, before the
		// hotkey dispatch and the scroll pass [07 R-CAM-01 §1 steps 3-5]
		// [07 R-CAM-01 §12]: a `t` or Ctrl+C pressed below therefore begins its
		// glide on the following frame, as retail's does.
		b.stepFollowCamera()
		b.controller.Step(input.SampleFromState(in, delta), cl)
		b.applyCommittedShake()
	}
	if b.ended {
		return
	}
	if state.Modal() != ui.BattleModalClosed {
		cl.Cursors().SetIndex(render.CursorNormal)
	} else {
		b.updateCursor(cl)
	}
	// Camera pan: exact predicates per [07 §10] C2/C3; presentation-only [I6].
	// - delta = scrollSettingByte * rawTimeDelta capped at 128 [07 §10] (C2)
	// - direction predicates: exact-edge bands plus less-than-100px beyond-edge forced strip with focus [07 §10]
	// - held-arrow gated on TALK.GUI suppression, edge never suppressed by TALK [07 §10]
	// - minimap interaction region suppresses edge [07 §10]
	// - modal GUI suppresses edge [07 §10]
	// - focus gating before edge [07 §10]
	// - scroll setting from persisted settings byte [02 "Settings"] default 32 [C-5]
	// W/A/S/D remain unbound per ON-05 (do not pan) [F-P1-008].
	if b.cam != nil && state.Modal() == ui.BattleModalClosed {
		kbd := cl.Input().Kbd
		mouse := cl.Input().Mouse
		rawDelta := b.scrollDelta // refreshed at the budget step above [07 §1]
		scrollSetting := b.scrollSetting()
		focused := cl.IsFocused()
		w, h := cl.Size()
		wi, hi := int32(w), int32(h)
		mx, my := int32(mouse.X), int32(mouse.Y)
		// Beyond-edge forced strip: pointer outside right/bottom <100px beyond with focus is forced to edge [07 §10].
		effX, effY := mx, my
		if focused {
			if mx >= wi && mx < wi+100 {
				effX = wi - 1
			}
			if my >= hi && my < hi+100 {
				effY = hi - 1
			}
		}
		talkActive := b.isTalkGUIActive()
		overMinimap := b.isOverMinimap(effX, effY)
		modalActive := state.Modal() != ui.BattleModalClosed
		// Every scroll-pass write is a jump by delta, and a jump by the scroll
		// pass cancels the follow triple [07 R-CAM-01 §12].
		scroll := func(dir camera.Direction) {
			b.cam.Scroll(scrollSetting, rawDelta, dir)
			b.cam.ClearFollow()
		}
		// Held-arrow branches gated on TALK absence [07 §10]; edge branches gated on focus, modal, and minimap.
		// Left: (Left held && !talk) OR (x==0 && y<H) [07 §10]
		if kbd.KeyHeld(input.KeyLeft) && !talkActive {
			scroll(camera.DirLeft)
		} else if focused && !modalActive && !overMinimap && effX == 0 && effY < hi {
			scroll(camera.DirLeft)
		}
		// Right: (Right held && !talk) OR x==W-1 [07 §10]
		if kbd.KeyHeld(input.KeyRight) && !talkActive {
			scroll(camera.DirRight)
		} else if focused && !modalActive && !overMinimap && effX == wi-1 {
			scroll(camera.DirRight)
		}
		// Up: (Up held && !talk) OR (y==0 && x<W) [07 §10]
		if kbd.KeyHeld(input.KeyUp) && !talkActive {
			scroll(camera.DirUp)
		} else if focused && !modalActive && !overMinimap && effY == 0 && effX < wi {
			scroll(camera.DirUp)
		}
		// Down: (Down held && !talk) OR y==H-1 [07 §10]
		if kbd.KeyHeld(input.KeyDown) && !talkActive {
			scroll(camera.DirDown)
		} else if focused && !modalActive && !overMinimap && effY == hi-1 {
			scroll(camera.DirDown)
		}
		// Middle-drag camera pan [F-P1-008]: presentation-only, uses mouse delta / scale.
		if mouse.Held(input.MouseButtonMiddle) && mouse.Moved() {
			dx := int32(mouse.X - b.battleState().Input.PrevMouseX)
			dy := int32(mouse.Y - b.battleState().Input.PrevMouseY)
			if dx != 0 || dy != 0 {
				b.cam.Drag(dx, dy)
			}
		}
		// Wheel belongs to the active GUI list under the pointer. It is not a
		// battle-camera control [07 §2][07 §10]. The UI boundary consumes it
		// before this camera pass.
		b.battleState().Input.PrevMouseX = mouse.X
		b.battleState().Input.PrevMouseY = mouse.Y
	}
}

func (b *battleSession) syncSelectionDrag(cl *client.Client) {
	if cl == nil || b == nil || b.battleState() == nil {
		if cl != nil {
			cl.SetSelectionDrag(client.SelectionDrag{})
		}
		return
	}
	state := b.battleState()
	in := state.Input
	// The outer colour is chosen by the armed latch, not by the fact that a
	// drag is running: an ordinary selection drag is white (logical entry 15),
	// and only an armed MOBILEBUILD latch takes the 6/4 pair
	// [07 R-P0-11 §1 "The drawing."][07 §6 "Frame composition passes"].
	// Passing DragActive as the box-mode selector made every drag take entry 4
	// — a dark red — and left the entry-15 branch unreachable.
	cl.SetSelectionDrag(client.SelectionDrag{
		Active:           in.DragActive,
		StartX:           in.DragStartX,
		StartY:           in.DragStartY,
		EndX:             in.DragEndX,
		EndY:             in.DragEndY,
		MobileBuildLatch: in.Latch == input.LatchMobileBuild,
		// Latch-flag bit 0x40 is the pointer-flags byte's **site-valid** bit,
		// and it has exactly one writer: the in-view placement preview, which
		// the frame handler runs only when the pointer is over the view and
		// the latch is MOBILEBUILD; the world rebuild clears it
		// [07 R-CAM-01 §14 step 1]. This build's placement verdict is that
		// bit, so the drag box's colour follows the same word the placement
		// cursor and the build click read [07 §9].
		SpecialLatchFlag: in.BuildOK,
		VisiblePanel:     state.PanelOffset == ui.PanelVisible,
	})
}

// applyCommittedShake transfers the cumulative phase-10 displacement from the
// current committed frame to the one camera used by input and rendering. It
// lives with battle camera ownership rather than Client so repainting the same
// frame cannot mutate camera state or consume another random value [03 §5.6].
func (b *battleSession) applyCommittedShake() {
	if b == nil || b.cam == nil || b.sess == nil || b.sess.Snapshot == nil {
		return
	}
	cur := b.sess.Snapshot.Current()
	if cur == nil {
		return
	}
	dx := cur.ShakeOffsetX - b.appliedShakeX
	dy := cur.ShakeOffsetY - b.appliedShakeY
	if dx != 0 || dy != 0 {
		b.cam.Pan(dx, dy)
	}
	b.appliedShakeX = cur.ShakeOffsetX
	b.appliedShakeY = cur.ShakeOffsetY
}

// minimapLayout is the sole production adapter for radar geometry. PlayRight
// and PlayBottom are authored by map loading; the rail destination is the
// battle composer's fixed 126-pixel canvas origin [07 §6][07 §10].
func (b *battleSession) minimapLayout() (camera.Minimap, hud.Rect, bool) {
	if b == nil || b.sess == nil || b.hud == nil {
		return camera.Minimap{}, hud.Rect{}, false
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return camera.Minimap{}, hud.Rect{}, false
	}
	dst, ok := b.hud.minimapRect()
	if !ok {
		return camera.Minimap{}, hud.Rect{}, false
	}
	return camera.LayoutMinimap(playW, playH), dst, true
}

// isOverMinimap identifies the authored radar interaction region [07 §10].
func (b *battleSession) isOverMinimap(x, y int32) bool {
	_, dst, ok := b.minimapLayout()
	return ok && dst.Contains(x, y)
}

// isOnRadar distinguishes the fitted radar rectangle from its 126-pixel
// canvas letterbox. The canvas remains the interaction capture region, while
// only the fitted rectangle selects the direct lens branch [07 §10].
func (b *battleSession) isOnRadar(x, y int32) bool {
	m, dst, ok := b.minimapLayout()
	if !ok {
		return false
	}
	dl, dt, dr, db := dst.Ordered()
	if x < dl || x > dr || y < dt || y > db {
		return false
	}
	cx, cy, _ := m.DisplayToCanvas(x, y, dl, dt, dr-dl+1, db-dt+1)
	return m.HitTest(cx, cy)
}

// minimapPointerWorld is the minimap branch of step 1's pointer
// classification: the lens conversion of [07 R-CAM-01 §11], with no
// half-viewport term. It yields map pixels, which the ground resolver then
// turns into a world point exactly as it does for the view branch [03 §3.11].
//
// The branch is taken only when the pointer is inside the minimap rectangle
// **and no drag rectangle is active**, so a selection drag begun in the view
// keeps resolving against the view even as the pointer crosses the rail
// [07 R-CAM-01 §11].
func (b *battleSession) minimapPointerWorld(x, y int32) (int32, int32, bool) {
	if b == nil || b.sess == nil || b.battleState().Input.DragActive {
		return 0, 0, false
	}
	m, dst, ok := b.minimapLayout()
	if !ok {
		return 0, 0, false
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return 0, 0, false
	}
	return client.MinimapPointerWorld(m, dst, playW, playH, x, y)
}

// minimapCameraLatch is the retail minimap latch. Under the default
// `Interface Type 0` polarity the **right** button sets it over the minimap,
// and while it is held every host frame writes the camera origin from the
// pointer, so a right-drag pans continuously; right up releases it
// [07 R-CAM-01 §5][07 R-CAM-01 §11]. The clicked map point becomes the view
// *centre*, and Camera.JumpToBattleViewCenter owns that recenter for this
// build's framebuffer-origin camera.
//
// It reports whether it consumed the pointer for this frame. Presentation
// only; no sim state is written [I6].
func (b *battleSession) minimapCameraLatch(mx, my int32, mouse *input.MouseState) bool {
	if b == nil || b.cam == nil || mouse == nil {
		return false
	}
	if !mouse.Pressed(input.MouseButtonRight) && !mouse.Held(input.MouseButtonRight) {
		return false
	}
	m, dst, ok := b.minimapLayout()
	if !ok {
		return false
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return false
	}
	intent, consumed := client.MinimapCameraIntent(m, dst, playW, playH, mx, my)
	if !consumed {
		return false
	}
	// The minimap latch jumps, and a minimap jump cancels the follow triple
	// [07 R-CAM-01 §12].
	b.cam.ClearFollow()
	b.cam.JumpToBattleViewCenter(intent.X, intent.Z)
	return true
}

// minimapClickOrder is retail's left-button path over the minimap under
// `Interface Type 0`. The world-click handler is **region-agnostic**: with the
// armed-order latch not idle the frame handler routes a left-down to it
// whatever the region, and with the latch idle a left-down over the minimap
// reaches it at once — no box drag starts there. Its branches run in the order
// below [07 R-CAM-01 §14 "the world-click handler is region-agnostic, and its
// branch order"].
//
// Orders route through orderSelected, the single order producer. pickTarget
// and cursorWorld already take the minimap branch of the pointer
// classification, so the view and the minimap differ only in how the pointer's
// world point and unit word are resolved [07 R-CAM-01 §11][07 R-HUD-03 §1].
func (b *battleSession) minimapClickOrder(cl *client.Client, mx, my int32, additive bool) {
	if b == nil || !b.isOnRadar(mx, my) {
		return
	}
	// Branch 1: the MOBILEBUILD latch. The site-valid bit has one writer — the
	// in-view placement preview, which the frame handler runs only over the
	// view — so over the minimap the bit still holds the verdict of the last
	// in-view hover. A click while placement is armed therefore sites the
	// building at the minimap-resolved world point when that verdict was OK,
	// and plays `notoktobuild` when it was not. Dropping the click, as this
	// path used to, is the one thing retail does not do [07 R-CAM-01 §14].
	if b.battleState().Input.BuildDef != "" {
		if !b.battleState().Input.BuildOK {
			b.playUICue(cl, "notoktobuild")
			return
		}
		b.siteBuildAtMinimapPoint(cl, mx, my, additive)
		return
	}
	if b.battleState().Input.Latch != input.LatchNormal {
		code := hud.LatchToCode(b.battleState().Input.Latch)
		if code != 0 {
			b.orderSelected(code, mx, my, additive)
		}
		// The latch retires after dispatch unless Shift keeps it, as it does
		// for a world click [07 §9][P0-I14].
		if additive {
			b.battleState().Input.ShiftLatchSticky = true
		} else {
			b.battleState().Input.Latch = input.LatchNormal
			b.battleState().Input.ShiftLatchSticky = false
		}
		return
	}
	// Branch 2: cursor kind 0x0F, the resolver's "select" answer. With the
	// latch idle and the hovered unit an own selectable unit the click selects
	// it — Shift toggles the selected bit, otherwise the selection is replaced.
	// The hovered unit over the minimap is the blip-dot winner within squared
	// pixel distance 4, so clicking a blip selects that unit
	// [07 R-CAM-01 §14 step 2][07 R-HUD-03 §1]. This branch used to be missing
	// here, which made an own blip unclickable.
	if f, ok := b.currentSnapshot(); ok {
		if h := b.minimapHoverUnit(f, mx, my); h != 0 {
			// The branch is cursor kind `0x0F`, so its admission is the shared
			// eligibility predicate `E(u)`, not ownership alone: a blip whose
			// remaining-build fraction is nonzero is not selectable
			// [07 R-CAM-01 §14 step 2][07 R-WGT-01 §10].
			if v, found := snapshotUnitByHandle(f, h); found && b.ownSelectableUnit(f, v) {
				kind := session.HumanSelectionReplace
				if additive {
					kind = session.HumanSelectionToggle
				}
				_ = b.enqueueHumanCommand(session.HumanCommand{Kind: kind, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{h}}})
				playSelectionCue(b.sess, []pool.Handle{h}) // [07 §9]
				return
			}
		}
	}
	// Branch 3: the contextual order, which orders.Resolve specialises from the
	// target and the point [04 §3.4][07 §9].
	if b.hasSelection() {
		b.orderSelected(1, mx, my, additive)
	}
}

// siteBuildAtMinimapPoint is branch 1's issue arm: resolve MOBILEBUILD against
// the armed product and the pointer's world point snapped to the product's
// footprint grid, queued when Shift is held, play `oktobuild`, and keep the
// latch only while Shift is held [07 R-CAM-01 §14 step 1].
//
// It deliberately does not run updatePlacement: the site-valid bit's one
// writer is the in-view preview, and re-validating from the minimap point
// would overwrite the in-view verdict the branch has already consulted. The
// snap is the same PlacementAnchor the preview applies, and cursorWorld takes
// the minimap lens branch for a pointer over the radar rectangle
// [07 R-CAM-01 §11][03 §3.11].
func (b *battleSession) siteBuildAtMinimapPoint(cl *client.Client, mx, my int32, additive bool) {
	in := &b.battleState().Input
	wx, _, wz := b.cursorWorld(mx, my)
	in.BuildMX, in.BuildMY = mx, my
	in.BuildCellX, in.BuildCellZ = world.PlacementAnchor(wx, wz, in.BuildFootX, in.BuildFootZ)
	if !b.commitBuild(additive) {
		// A command rejection is a failed commit, not an armed placement state,
		// exactly as on the in-view path.
		b.disarmPlacement()
		return
	}
	b.playUICue(cl, "oktobuild")
	if additive {
		in.BuildSticky = true
	} else {
		b.disarmPlacement()
	}
}

// The battle hotkey census — every row of [07 R-CAM-01 §2]'s token table
// against what this dispatcher does, in table order. Retail folds Ctrl into
// the token itself (`Ctrl+A..Z` -> 0xAA..0xC3, `Ctrl+0..9` -> 0xC4..0xCD,
// `Ctrl+F1..F12` -> 0xCE..0xD9), so a Ctrl-composed token can never reach an
// unmodified key's case; this key-based layer reproduces that by testing the
// held Ctrl state on both arms.
//
//	token                  key            state
//	0x09                   Tab            done — opens/closes the options window (viewerStep)
//	0xE3                   F2             done — same window; Shift+F2's Unit Builder Probe is a developer overlay, out of scope
//	0x0D                   Enter          out of scope — chat is `TALK.GUI` with its own dialog, focus and recipient rows [07 §5 "Chat"]; single player drops the packet unsent
//	0x1B                   Escape         done — options close, latch cancel, else deselect all
//	0x21 0x23 0x2A         ! # *          done — Shift+1/3/8, the same "label every unit" bit as ` and ~ [07 R-CAM-01 §14]
//	0x60 0x7E              ` ~            done — "label every unit" bit
//	0x2B 0x3D              + =            done — speed up, with the ring announcement
//	0x2D 0x5F              - _            done — speed down, with the ring announcement
//	0x2C                   ,              done — previous build page, `nextbuildmenu`
//	0x2E                   .              done — next build page, `nextbuildmenu`
//	0x31..0x39             1..9           done — SwitchAlt mux; group recall plays `SelectSquad`; reached by an unshifted digit or Alt+digit, so additive recall is Shift+Alt+digit [07 R-CAM-01 §14]
//	0x54 0x74              T t            done — follow camera, previous/next selected unit
//	0x5C                   \              out of scope — developer mode only
//	0x68                   h              out of scope — `SHARE.GUI` is multiplayer
//	0x6E                   n              done — next unvisited own unit, camera glide, no selection change; `N` (0x4E) has no case [07 R-CAM-01 §14]
//	0xAA                   Ctrl+A         done — select every own selectable unit, additive
//	0xAC                   Ctrl+C         done — `CTRL_C` category select, then follow the commander
//	0xAD                   Ctrl+D         done — self-destruct the selection
//	0xAB 0xAE..0xBB        Ctrl+B, E..R   done — `CTRL_%c` category select
//	0xBD..0xC2             Ctrl+T..Y      done — `CTRL_%c` category select
//	0xBC                   Ctrl+S         done — select the own selectable units on screen, replacing
//	0xC3                   Ctrl+Z         done — select every own unit sharing a selected definition
//	0xC5..0xCD             Ctrl+1..9      done — group assign, `CreateSquad`; Ctrl+0 has no case
//	0xD2..0xD5             Ctrl+F5..F8    done — store bookmark 0..3, `SelectSquad`
//	0xE6..0xE9             F5..F8         done — recall bookmark 0..3, `SelectSquad`
//	0xD6                   Ctrl+F9        out of scope — screenshot; the battle shell has no in-battle capture writer
//	0xD7                   Ctrl+F10       out of scope — developer mode only
//	0xE2                   F1             done — opens `UNITINFOx.GUI` for the hovered unit or the hovered build button's product [07 R-HUD-03 §8]
//	0xE4                   F3             done — message-source glide
//	0xE5                   F4             done — interface-flags bit 0x80
//	0xEC                   F11            out of scope — developer mode only
//	0xED                   F12            done — clear the message ring
//	0xF8                   Pause          done — pause toggle
//	0x20, 0xC4, 0xF0..0xF7 Space, arrows… no case — the arrows are the scroll pass's held-key queries, not ring tokens
//
// The order latches below (`m`, `a`, `p`, `r`, `e`, `c`, `g`, `d`, `x`, `o`)
// have no row in that table at all: retail reaches them through the command
// palette's authored gadget quick keys [07 §2 "GUI quick keys"], not through
// the battle dispatcher. They are kept as direct bindings here because the
// authored quick-key path belongs to the palette's owner.
//
// handleInput processes selection, orders, and build placement.
// It converts input into complete canonical commands with target/position and
// queue modifiers (shift-queued) via one picking routine that respects fog,
// unit/feature overlap, and command validity [07 §9][03 §3.2] C8 [P0-I14].
// Order buttons enqueue session-owned commands directly [R-P0-03]; build products are
// data-driven from cat.BuildMenus; input-capture latch prevents HUD presses
// from leaking into world drag [F-P0-003][F-P1-008].
func (b *battleSession) handleInput(in *input.State, cl *client.Client) {
	kbd := in.Kbd
	mouse := in.Mouse
	mx, my := int32(mouse.X), int32(mouse.Y)
	b.battleState().Input.ShiftHeld = kbd.HasShift()
	b.battleState().Input.PointerX, b.battleState().Input.PointerY = mx, my
	// Any latch held by Shift retires on the live Shift-up, regardless of
	// order family [R-P0-11].
	if !b.battleState().Input.ShiftHeld && b.battleState().Input.ShiftLatchSticky {
		b.battleState().Input.Latch = input.LatchNormal
		b.battleState().Input.ShiftLatchSticky = false
	}

	// Minimap input, under the default `Interface Type 0` polarity
	// [07 R-CAM-01 §5]: right down over the minimap sets the minimap latch, and
	// while it is held every host frame jumps the camera from the pointer, so a
	// right-drag pans continuously [07 R-CAM-01 §11]; left down over the minimap
	// issues the armed order or the world click at the minimap's world point.
	//
	// This used to be inverted — left jumped the camera, and no button issued an
	// order at all.
	if b.isOverMinimap(mx, my) {
		if b.minimapCameraLatch(mx, my, mouse) {
			return
		}
		if mouse.Pressed(input.MouseButtonLeft) {
			b.minimapClickOrder(cl, mx, my, kbd.HasShift())
			return
		}
		// While over the minimap, suppress world drag/selection [07 §10].
		if mouse.Held(input.MouseButtonLeft) {
			return
		}
	}

	// Ctrl is a held-key query made at dispatch time, and a Ctrl-composed token
	// never reaches an unmodified key's case [07 R-CAM-01 §2]. Every unmodified
	// arm below is therefore gated on Ctrl being clear, so Ctrl+A selects and
	// does not also arm the attack latch.
	ctrlHeld := kbd.KeyHeld(input.KeyCtrl)
	if !ctrlHeld {
		// Latch arming via hotkeys — retail latch byte IS dispatcher switch key [GAP T22][07 §9] C11.
		// Preserve authored button order and pagination for build menu [02 "Build-menu catalog keys"].
		if kbd.KeyDown(input.KeyM) {
			b.battleState().Input.Latch = input.LatchMove
		}
		if kbd.KeyDown(input.KeyA) {
			b.battleState().Input.Latch = input.LatchAttack
		}
		if kbd.KeyDown(input.KeyP) {
			b.battleState().Input.Latch = input.LatchPatrol
		}
		if kbd.KeyDown(input.KeyR) {
			b.battleState().Input.Latch = input.LatchRepair
		}
		if kbd.KeyDown(input.KeyE) {
			b.battleState().Input.Latch = input.LatchReclaim
		}
		if kbd.KeyDown(input.KeyC) {
			b.battleState().Input.Latch = input.LatchCapture
		}
		if kbd.KeyDown(input.KeyG) {
			b.battleState().Input.Latch = input.LatchFollow
		}
		if kbd.KeyDown(input.KeyD) {
			b.battleState().Input.Latch = input.LatchBlast
		}
		if kbd.KeyDown(input.KeyX) {
			b.cancelSelectedProduction()
		}
		if kbd.KeyDown(input.KeyO) {
			b.toggleOnOffSelected(kbd.HasShift())
		}
		// `n` (0x6E) cycles the next unvisited own unit. `N` (0x4E) is a
		// separate character token and the dispatcher has no case for it, so
		// Shift+n does nothing: the stockpile round is enqueued only by the
		// palette's `MAKENUKE`/`MAKEANTI` gadgets [07 R-CAM-01 §14 item 3]
		// [07 §6]. The Shift+N stand-in that used to call stockpileSelected
		// here is gone; stockpileSelected keeps its one authored caller, the
		// gadget dispatch of DispatchStockpile.
		if kbd.KeyDown(input.KeyN) && !kbd.HasShift() {
			b.cycleNextUnvisitedUnit()
		}
	}
	// The "label every unit" bit. Retail's dispatcher has a case for each of
	// five character tokens — `!` `#` `*` `` ` `` `~` — and every one of them
	// flips interface-flags bit 0 and writes all settings back
	// [07 R-CAM-01 §2][07 R-HUD-03 §7]. Both backquote tokens are the same
	// physical key, shifted and unshifted, so one key edge covers them.
	//
	// The other three are the shifted digits: the window procedure pushes the
	// translated *character*, so Shift+1/3/8 on the US layout the retail
	// install assumes are `!` `#` `*` and never the digit token
	// [07 R-CAM-01 §14 "the key-token producer"]. The digit case of §2 is
	// reached by an unshifted digit character or by Alt+digit, so the label
	// toggle and group recall never meet — the apparent contradiction this
	// site used to record was two different tokens. The shifted-digit arm
	// lives in the digit loop below, where the same key edge is classified.
	if kbd.KeyDown(input.KeyBackquote) {
		b.toggleDamageBars()
	}
	// Game-speed and pause keys [07 §2][07 §11]: +/- clamp Requested 1..20
	// with localized messages; Pause toggles pause with the retail message.
	if kbd.KeyDown(input.KeyPause) {
		b.togglePause()
	}
	if kbd.KeyDown(input.KeyEqual) || kbd.KeyDown(input.KeyNumpadAdd) {
		b.adjustGameSpeed(1)
	}
	if kbd.KeyDown(input.KeyMinus) || kbd.KeyDown(input.KeyNumpadSubtract) {
		b.adjustGameSpeed(-1)
	}
	// Digit routing uses the established SwitchAlt gate [07 R-CAM-01 §4].
	// Ctrl+digit is assignment and plays `CreateSquad`; the non-page branch is
	// group recall with Shift as preserve / toggle and plays `SelectSquad`
	// [07 R-CAM-01 §2][07 §9] C9-C10. Ctrl+0 has no case.
	//
	// Which physical edges reach the digit case is the key-token producer of
	// [07 R-CAM-01 §14]. Ctrl composes the token itself, so Ctrl+Shift+digit
	// still assigns. Without Ctrl the digit case is reached by an *unshifted*
	// digit character or by Alt+digit — Alt's system key-down pushes the raw
	// digit value and produces no character message. Shift with Alt therefore
	// keeps the digit token and the Shift argument recall reads is live only
	// for Shift+Alt+digit; Shift without Alt yields the shifted character
	// instead, and only `!` `#` `*` (Shift+1/3/8) have a case — the label
	// toggle. The other six shifted digits do nothing. Under the default
	// SwitchAlt = 0 that makes additive recall Shift+Alt+digit; with
	// SwitchAlt = 1 a plain digit recalls and additive recall is unreachable
	// from the keyboard, which is retail's behaviour and not a gap to patch.
	for d := 1; d <= 9; d++ {
		var key input.Key
		switch d {
		case 1:
			key = input.Key1
		case 2:
			key = input.Key2
		case 3:
			key = input.Key3
		case 4:
			key = input.Key4
		case 5:
			key = input.Key5
		case 6:
			key = input.Key6
		case 7:
			key = input.Key7
		case 8:
			key = input.Key8
		case 9:
			key = input.Key9
		}
		if !kbd.KeyDown(key) {
			continue
		}
		altHeld := kbd.KeyHeld(input.KeyAlt)
		switch {
		case ctrlHeld:
			if b.DispatchGroupAssign(d) == nil {
				b.playUICue(cl, "CreateSquad") // [07 R-CAM-01 §2]
			}
		case kbd.HasShift() && !altHeld:
			// The shifted-digit character tokens. `!` `#` `*` flip the label
			// bit; the other six have no case [07 R-CAM-01 §14 item 2].
			if d == 1 || d == 3 || d == 8 {
				b.toggleDamageBars()
			}
		default:
			b.routeDigit(d, altHeld, kbd.HasShift(), cl)
		}
	}
	// Page next/prev data-driven with guard [R-P0-03][07 §9] C10: no hardcoding.
	// `,` is the previous page and `.` the next, both with the `nextbuildmenu`
	// cue [07 R-CAM-01 §2]; PageUp/PageDown and the shifted arrows are this
	// build's extra bindings and keep working.
	if kbd.KeyDown(input.KeyPrior) || kbd.KeyDown(input.KeyRight) && kbd.HasShift() {
		b.nextBuildPage()
	}
	if kbd.KeyDown(input.KeyNext) || kbd.KeyDown(input.KeyLeft) && kbd.HasShift() {
		b.prevBuildPage()
	}
	// `.` and `,` are the next/previous build page of [07 R-CAM-01 §2]. The
	// `nextbuildmenu` cue that row names belongs to the page-switch routine
	// itself [07 §9 "Page encoding is closed"], so it is raised inside the page
	// helpers below and not a second time here.
	if !ctrlHeld && kbd.KeyDown(input.KeyPeriod) {
		b.nextBuildPage()
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyComma) {
		b.prevBuildPage()
	}
	// The follow camera. `t` tracks the next selected unit after the current
	// tracked object in slot order and `T` (Shift held) the previous, wrapping
	// within the local slot range; with nothing selected the tracked object
	// becomes null. Neither moves the camera itself — the follow step does
	// [07 R-CAM-01 §2][07 R-CAM-01 §12].
	if !ctrlHeld && kbd.KeyDown(input.KeyT) {
		b.cycleFollowTarget(kbd.HasShift())
	}
	if ctrlHeld {
		b.dispatchCtrlLetters(kbd)
	}
	// Camera bookmarks: Ctrl+F5..F8 store the current origin into slot 0..3 and
	// F5..F8 recall it, both with the `SelectSquad` cue [07 R-CAM-01 §2]
	// [07 R-CAM-01 §12].
	for slot, key := range [camera.BookmarkSlots]input.Key{input.KeyF5, input.KeyF6, input.KeyF7, input.KeyF8} {
		if !kbd.KeyDown(key) {
			continue
		}
		if ctrlHeld {
			if b.cam.StoreBookmark(slot) {
				b.playUICue(cl, "SelectSquad")
			}
			continue
		}
		if b.cam.RecallBookmark(slot) {
			b.playUICue(cl, "SelectSquad")
		}
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyF1) {
		b.openUnitInfo()
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyF3) {
		b.glideToMessageSource()
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyF4) {
		// Interface-flags bit 0x80 has exactly two readers, and F4 pins both
		// [07 R-CAM-01 §14 "F4 pins the score panel open and arms the kill/loss
		// flash"]. The score panel shows while the bit is set as if Space were
		// held ([07 R-HUD-04 §1]), and the kill-credit finalize arms the
		// crediting slot's kill flash and the victim slot's loss flash to 30
		// **only** while it is set — with F4 off the arrays are never armed and
		// a Space-held panel shows steady numbers. Both readers are wired; the
		// bit is not a term of the rail slide.
		//
		// TODO(question): the bit's user-facing name. No string in the image
		// names it [07 R-CAM-01 §14]; nothing observable turns on the name.
		b.panelHoldFlag = !b.panelHoldFlag
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyF12) {
		b.messageRing().Clear() // [07 R-CAM-01 §2]
	}
	// Escape: an armed latch or placement returns to idle; an idle latch
	// deselects everything [07 R-CAM-01 §2]. viewerStep intercepts the same
	// edge ahead of this dispatcher, so both copies apply the one arm.
	if kbd.KeyDown(input.KeyEscape) {
		if b.battleState().Input.Latch == input.LatchNormal && b.battleState().Input.BuildDef == "" {
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
		}
		b.disarmPlacement()
		b.battleState().Input.Latch = input.LatchNormal
		b.battleState().Input.ShiftLatchSticky = false
		b.battleState().Input.HUDCaptured = false
		b.battleState().Input.DragActive = false
		return
	}
	// Right button is deselect/cancel only: every world order fires on left [07 §9][04 §3.4].
	if mouse.Pressed(input.MouseButtonRight) {
		// A factory product button is the one right-click exception: it
		// subtracts one/five from the matching tail node [R-P0-11].
		if b.hud != nil && b.hud.hitTestFor(b, mx, my) && b.hud.consumeRightClick(b, mx, my) {
			return
		}
		if b.battleState().Input.BuildDef != "" {
			// Cancel armed placement before affecting selection [R-P0-03][F-P0-003][07 §9].
			b.disarmPlacement()
			b.battleState().Input.HUDCaptured = false
			return
		}
		if b.battleState().Input.Latch != input.LatchNormal {
			// Cancel armed order latch to idle [07 §9][07 §8][07 §9].
			b.battleState().Input.Latch = input.LatchNormal
			b.battleState().Input.ShiftLatchSticky = false
			return
		}
		if b.hasSelection() {
			// Deselect is a typed command; UI never mutates live flags [I6].
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
			return
		}
		return
	}

	// Input-capture latch [F-P0-003][F-P1-008]: press that begins on HUD chrome
	// never starts/completes world drag selection even if released over world.
	// Detect HUD origin on the pressed edge.
	if mouse.Pressed(input.MouseButtonLeft) {
		overHUD := false
		if !b.overWorld(mx, my) {
			overHUD = true
		}
		if b.hud != nil && b.hud.hitTestFor(b, mx, my) {
			overHUD = true
		}
		if overHUD {
			b.battleState().Input.HUDCaptured = true
			b.battleState().Input.HUDPressX = mx
			b.battleState().Input.HUDPressY = my
			b.battleState().Input.DragActive = false
		}
	}
	if b.battleState().Input.HUDCaptured && mouse.Released(input.MouseButtonLeft) {
		// Retail buttons arm while held and activate once on release-inside.
		// Requiring the same authored gadget at both endpoints prevents a drag
		// across the rail from activating a different control [07 §3][07 §4].
		b.battleState().Input.HUDCaptured = false
		b.battleState().Input.DragActive = false
		if b.hud != nil && b.hud.sameButton(b, b.battleState().Input.HUDPressX, b.battleState().Input.HUDPressY, mx, my) && b.hud.consumeClick(b, mx, my) {
			return
		}
		return
	}
	if b.battleState().Input.HUDCaptured {
		return
	}

	// A left press the placement path already consumed owns that button until
	// it is released. Retail routes one press/release pair through exactly one
	// world path [07 §9]; because placement commits on the press edge and then
	// disarms, the remaining held frames of an ordinary human click used to
	// fall through to drag selection, and its release issued a contextual Move
	// that purged the build order the same click had just queued.
	if b.battleState().Input.PlaceCaptured {
		if !mouse.Held(input.MouseButtonLeft) {
			b.battleState().Input.PlaceCaptured = false
		}
		if b.battleState().Input.BuildDef != "" {
			b.updatePlacement(mx, my)
		}
		return
	}

	// Build placement mode captures left-clicks before selection handling [R-P0-03].
	// Right-click cancellation is handled at the top of handleInput with the
	// latch/selection precedence of [07 §9].
	//
	// A shift-click leaves the mode armed so the next click places another copy;
	// retail records that on a placement-valid-pending bit and drops back to the
	// idle latch as soon as shift is released, whether or not another click
	// arrives. Arming from a build button does not set the bit, so a first
	// placement without shift still gets its click.
	if b.battleState().Input.BuildSticky && !kbd.HasShift() {
		b.disarmPlacement()
	}
	if b.battleState().Input.BuildDef != "" {
		b.updatePlacement(mx, my)
		if mouse.Pressed(input.MouseButtonLeft) {
			// The press belongs to placement whatever it decides below —
			// placed, refused, or rejected by the command boundary.
			b.battleState().Input.PlaceCaptured = true
			if !b.battleState().Input.BuildOK {
				// An illegal site queues nothing and stays armed; the player
				// hears the refusal and can move the ghost [07 §9].
				b.playUICue(cl, "notoktobuild")
				return
			}
			queued := kbd.HasShift()
			if !b.commitBuild(queued) {
				// A command rejection is a failed commit, not an armed
				// placement state. All cancellation exits share disarmPlacement.
				b.disarmPlacement()
				return
			}
			b.playUICue(cl, "oktobuild")
			if queued {
				b.battleState().Input.BuildSticky = true
			} else {
				b.disarmPlacement()
			}
		}
		return
	}

	leftHeld := mouse.Held(input.MouseButtonLeft)
	additive := kbd.HasShift()
	if leftHeld && !b.battleState().Input.DragActive {
		b.battleState().Input.DragActive = true
		b.battleState().Input.DragStartX, b.battleState().Input.DragStartY = mx, my
		b.battleState().Input.DragEndX, b.battleState().Input.DragEndY = mx, my
	} else if leftHeld && b.battleState().Input.DragActive {
		b.battleState().Input.DragEndX, b.battleState().Input.DragEndY = mx, my
	} else if !leftHeld && b.battleState().Input.DragActive {
		b.battleState().Input.DragActive = false
		rect := client.NormalizeRect(b.battleState().Input.DragStartX, b.battleState().Input.DragStartY, b.battleState().Input.DragEndX, b.battleState().Input.DragEndY)
		w, h := rect.MaxX-rect.MinX, rect.MaxY-rect.MinY
		if w < 3 && h < 3 {
			// Small click precedence [07 §8][07 §9][RS-P0-003]: armed latch dispatches on left-click; idle latch left-click selects or issues contextual order; right-click never issues an order [04 §3.4][07 §9].
			// Uses the immutable committed-frame picker so fog, radius, strict tie,
			// and viewer rules are shared by selection and targeting [07 §9][03 §3.2].
			if b.battleState().Input.Latch != input.LatchNormal {
				code := hud.LatchToCode(b.battleState().Input.Latch)
				if code != 0 {
					b.orderSelected(code, mx, my, additive)
				}
				// Return latch to Normal after dispatch unless shift-queuing keeps it [07 §9][P0-I14].
				if additive {
					b.battleState().Input.ShiftLatchSticky = true
				} else {
					b.battleState().Input.Latch = input.LatchNormal
					b.battleState().Input.ShiftLatchSticky = false
				}
			} else {
				// Idle latch left-click: every world command is left-click; right-click is deselect/cancel only [07 §9][04 §3.4].
				// When clicking an own visible unit we change selection; otherwise with a selection we issue the contextual order (code 1) which delegates to move/attack/repair/etc. based on the target [04 §3.4].
				viewer := visibility.PlayerID(0)
				if b.sess != nil {
					viewer = visibility.PlayerID(b.sess.LocalOwner)
				}
				f, ok := b.currentSnapshot()
				if !ok {
					return
				}
				var bh pool.Handle
				var bu frame.UnitView
				var hit bool
				// The framebuffer composer already rebases the projected world point
				// from the beam origin before drawing it. Mouse coordinates are in that
				// same logical framebuffer, so do not subtract the HUD viewport origin
				// a second time [03 §2.5][07 §8].
				shellX, shellY := mx, my
				bh, bu, hit = client.PickSnapshotUnit(f, shellX, shellY, b.cam, uint8(viewer))
				// Branch 2 of the world-click handler is cursor kind `0x0F`,
				// "the resolver's select answer: latch idle and the hovered unit
				// is an own SELECTABLE unit (own slot, selectable bit,
				// remaining-build fraction `0.0`, post-capture grace zero,
				// carrier null or itself a visible carrier)"
				// [07 R-CAM-01 §14 step 2]. That list is the shared eligibility
				// predicate `E(u)` of [07 R-WGT-01 §9][07 R-WGT-01 §10], and
				// ownSelectableUnit is this build's one copy of it.
				//
				// The test used to be ownership alone, so a click on an own
				// nanoframe selected it — and, because the select branch runs
				// before the order branch, ate the click a builder meant as an
				// assist. Failing `E(u)` here is what lets the click reach
				// branch 3, where the contextual code resolves "nano-reach
				// passes and the target is unfinished → code 8" into HelpBuild
				// [07 R-CAM-01 §14 step 3][04 R-ORD-02 §1].
				hitOwn := hit && bh != 0 && b.ownSelectableUnit(f, bu)
				if hitOwn {
					if additive {
						_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionToggle, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{bh}}})
					} else {
						_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{bh}}})
					}
					playSelectionCue(b.sess, []pool.Handle{bh}) // [07 §9]
				} else {
					if b.hasSelection() {
						// Left-click contextual order when a selection exists and the click is not on own unit [04 §3.4][07 §9].
						b.orderSelected(1, mx, my, additive)
					} else {
						// No selection and click not on own unit: clear if not additive, else preserve [07 §9] C6.
						if !additive {
							_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
						}
					}
				}
			}
		} else {
			// The drag rectangle is already in the framebuffer coordinate space
			// used by the rendered world [03 §2.5][07 §8].
			shellRect := rect
			f, ok := b.currentSnapshot()
			if !ok {
				return
			}
			handles := b.eligibleHandlesInRect(f, shellRect)
			kind := session.HumanSelectionReplace
			if additive {
				kind = session.HumanSelectionToggle
			}
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: kind, Selection: session.HumanSelectionCommand{Handles: handles}})
			playSelectionCue(b.sess, handles) // [07 §9]
		}
	}
	// No right-button order path: right-click is deselect/cancel only, handled at the top [07 §9][04 §3.4].
}

func (b *battleSession) routeDigit(digit int, altHeld, shiftHeld bool, cl *client.Client) {
	// The gate is `switchAlt == alt` [07 R-CAM-01 §4]: by default digits pick
	// build pages and Alt+digit recalls groups; with the persistent `SwitchAlt`
	// option set the two swap. The option is absent from this build's settings
	// block, and its documented default when the registry value is absent is
	// zero, which is the value passed here. Wiring the persisted option is the
	// settings owner's; the gate itself is now the traced one, not the
	// "battle-mode flag" the earlier text named [07 R-CAM-01 §4].
	const switchAltDefault byte = 0
	if hud.RoutesToPage(switchAltDefault, altHeld) {
		b.switchBuildPage(digit)
		return
	}
	if b.DispatchGroupRecall(digit, shiftHeld) == nil {
		b.playUICue(cl, "SelectSquad") // [07 R-CAM-01 §2]
	}
}

func (b *battleSession) currentSnapshot() (*frame.Frame, bool) {
	if b == nil || b.sess == nil || b.sess.Snapshot == nil {
		return nil, false
	}
	cur := b.sess.Snapshot.Current()
	return cur, cur != nil
}

func (b *battleSession) hasSelection() bool {
	if f, ok := b.currentSnapshot(); ok {
		return len(f.Selection.Handles) != 0
	}
	return false
}

// selectedCommandUnits resolves the committed selection for typed commands.
// Live selection flags are presentation state; command application owns all
// authoritative selection and queue mutation [01 §4.4][07 §9].
func (b *battleSession) selectedCommandUnits() []*units.Unit {
	if b == nil || b.sess == nil {
		return nil
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	// The committed frame is the complete selection source. Do not dereference
	// the live pool here: command construction happens from immutable facts and
	// every mutation remains a typed Session command [I6].
	out := make([]*units.Unit, 0, len(f.Selection.Handles))
	for _, h := range f.Selection.Handles {
		if v, found := snapshotUnitByHandle(f, h); found && v.Owner == f.Selection.LocalPlayer {
			out = append(out, b.snapshotUnitCopy(v))
		}
	}
	return out
}

// catalogDefID returns the stable compiled catalog identity used by the HUD
// page guard. It is deliberately not a literal placeholder and does not use
// the allocation order of the live unit pool [02 §5][07 §9] C10.
func (b *battleSession) catalogDefID(u *units.Unit) uint16 {
	if b == nil || b.cat == nil || u == nil || u.Def == nil {
		return 0
	}
	id, ok := b.cat.UnitDefIndex(u.Def.CanonicalKey)
	if !ok || id == 0 || id > 0xffff {
		return 0
	}
	return uint16(id)
}

// switchBuildPage handles digit 1..9 build page switching [07 §9] C10.
// Page number lives in flag bits 23-25 with bit 22 paged indicator [07 §9].
func (b *battleSession) switchBuildPage(digit int) {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount == 0 || digit < 1 || digit > 9 {
		return
	}
	target := hud.ClampPage(hud.DigitToPage(digit), int(frame.CommandPage.PageCount))
	_ = b.dispatchBuildPageCued(target)
}

// nextBuildPage advances one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) nextBuildPage() {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
		return
	}
	target := hud.ClampPage(int(frame.CommandPage.Page)+1, int(frame.CommandPage.PageCount))
	_ = b.dispatchBuildPageCued(target)
}

// prevBuildPage goes back one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) prevBuildPage() {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
		return
	}
	target := hud.ClampPage(int(frame.CommandPage.Page)-1, int(frame.CommandPage.PageCount))
	_ = b.dispatchBuildPageCued(target)
}

// handleHudOrderButton binds named order buttons to the session command path
// [R-P0-03][07 §9]. It is used by retail HUD consumeClick.
func (b *battleSession) handleHudOrderButton(name string) {
	latch := hud.ParseButtonLatch(name, 1)
	// STOP is a distinct immediate command. It must never dispatch contextual
	// code 1 at the map origin before the Stop descriptor [04 §3.4][07 §9].
	if latch == input.LatchNormal && containsStop(name) {
		_ = b.dispatchStopCommand()
		b.battleState().Input.Latch = input.LatchNormal
		return
	}
	if latch.IsValid() {
		b.battleState().Input.Latch = latch
		// "Each armed write … plays the `immediateorders` cue … or the
		// `specialorders` cue" [07 §9]; the cue follows the armed write, not the
		// hit test, so a button whose parse yields no valid latch is silent.
		b.playUICue(nil, orderButtonCue(latch))
	}
}

func containsStop(s string) bool {
	upper := strings.ToUpper(s)
	return strings.Contains(upper, "STOP")
}

// cancelSelectedProduction cancels the tail-most matching factory/mobile build for selected units [04 §3.3][P1-14].
// It walks each selected factory/builder's primary queue tail-most and decrements or frees via
// construction.CancelTailMost / CancelMobileTailMost. Tombstone bit ensures weapon-target-clear skip [04 §3.3].
func (b *battleSession) cancelSelectedProduction() {
	for _, u := range b.selectedCommandUnits() {
		if u != nil {
			_ = b.DispatchCancelProduction(u.Handle)
		}
	}
}

// toggleOnOffSelected issues Activate/Deactivate for OnOffable units [02 "Unit record"].
// OnOffable is data-driven; the command is Activate/Deactivate via orders.NewNodeForOrder [P0-I14].
func (b *battleSession) toggleOnOffSelected(queued bool) {
	for _, u := range b.selectedCommandUnits() {
		if u == nil || u.Def == nil || !u.Def.OnOffable {
			continue
		}
		// Activation state is typed unit state, not the unrelated order flag
		// word. The queued command is consumed by the ordinary order/COB edge
		// machinery [05 "Unit instance economy state"].
		_ = b.DispatchActivation(session.HumanActivationCommand{
			Unit: u.Handle, Activate: !u.Activated, Queued: queued,
		})
	}
}

// cycleStance is one press of the side panel's MOVEORD or FIREORD gadget, in
// the order [04 R-STANCE-01 §2] gives it: read the published three-bit panel
// field, compute the next value with that section's cycle, transmit the
// standing order through the selection broadcast, and play the cue. Step 3 —
// the local write-back into the panel word — is display only and has no place
// here: this build recomputes the aggregate from the selection at every
// publication boundary [I6], which is the same refresh retail's step 3 is
// overwritten by.
//
// A field reading 4 is not applicable to this selection; the gadget is greyed
// and matches no arm of the cycle, so the press does nothing at all.
func (b *battleSession) cycleStance(fire bool) {
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	current := f.CommandPage.MoveStance
	cue := "setmoveorders"
	if fire {
		current = f.CommandPage.FireStance
		cue = "setfireorders"
	}
	var next int32
	switch current {
	case 0:
		next = 1
	case 1:
		next = 2
	case 2, 3: // the mixed sentinel cycles to hold [04 R-STANCE-01 §2]
		next = 0
	default:
		return // 4: not applicable, the gadget is grey
	}
	if err := b.enqueueHumanCommand(session.HumanCommand{
		Kind: session.HumanStance, Stance: session.HumanStanceCommand{Fire: fire, Value: next},
	}); err != nil {
		return
	}
	b.playUICue(nil, cue)
}

// stockpileSelected queues one BuildWeapon round for stockpile weapons [06 §11.1].
// Stockpile launch requires BuildWeapon descriptor (rear segment 0x40000) with count.
func (b *battleSession) stockpileSelected(queued bool) {
	for _, u := range b.selectedCommandUnits() {
		if u != nil {
			_ = b.DispatchStockpile(u.Handle, queued)
		}
	}
}

// scrollSetting returns the persisted scroll speed byte [02 "Settings"] [07 §10] C2.
// It is presentation-only and never touches sim [I6].
//
// The value is cached on b.scrollSpeedByte rather than re-read from disk on
// every call: the camera pan block in the per-frame input path (~handleInput)
// calls this once per host frame, and settings.Load is a full file open plus
// JSON parse [WU-19-114, reported by WU-19-109's profile: 8.8% of the frame
// loop]. primeScrollSetting fills the cache once at battle entry the way
// applyDamageBarsSetting(loadedSettings()) already primes the damage-bars bit;
// the lazy fallback here only fires for a battleSession built without going
// through that entry path (unit tests construct battleSession{} directly).
func (b *battleSession) scrollSetting() byte { // [07 §10] [02 "Settings"]
	if b == nil {
		return byte(settings.DefaultScrollSpeed)
	}
	if b.scrollSpeedByte == 0 {
		b.scrollSpeedByte = b.readScrollSetting()
	}
	return b.scrollSpeedByte
}

// readScrollSetting resolves the scrollspeed byte without caching. A battle
// composed with a frontend shell attached reads the shell's own copy —
// attachSettings already loaded it once at process startup [02 "Settings"],
// so this costs no I/O at all — and a shell-less battle (tests, --shot) falls
// back to a direct settings.Load, matching the one-read-per-battle behaviour
// the entry-point priming gives the windowed path.
func (b *battleSession) readScrollSetting() byte {
	var ss int
	if b != nil && b.shell != nil {
		ss = b.shell.scrollSpeed
	} else {
		s, _ := settings.Load()
		ss = s.ScrollSpeed
	}
	if ss <= 0 || ss > 255 {
		ss = settings.DefaultScrollSpeed
	}
	return byte(ss)
}

// primeScrollSetting caches the scrollspeed byte once at battle entry
// [WU-19-114]. Retail's in-battle ARMOPT modal chain (options -> exit ->
// confirm [07 "Tab options menu and manual exit"]) has no live settings
// editor — it only routes to save/load/main-menu/exit — so nothing inside a
// running battle can change the persisted scrollspeed; the frontend's own
// settings screens are reachable only before a battle exists (or after one
// ends, since returning to the main menu ends the battleSession), and each
// new battle re-primes the cache from composeBattleEntryDetached. Callable
// more than once if that ever changes; it always re-reads rather than
// trusting the existing cache.
func (b *battleSession) primeScrollSetting() {
	if b == nil {
		return
	}
	b.scrollSpeedByte = b.readScrollSetting()
}

// refreshScrollDelta advances the scroll pass's clock and returns this host
// frame's raw delta [07 §10].
//
// Retail's scroll pass does not measure the frame in milliseconds: it reads
// the delta word the tick-budget step stored earlier in the same host frame,
// which is this frame's scaled reading minus the previous frame's, and the
// scaled reading is floor(hostMillis*30/1000) — thirtieths of a second, the
// simulation timebase of [01 §4.1]. Feeding milliseconds here multiplies the
// scroll rate by thirty at the source and then pins every frame to the 128-pixel
// cap, which is defect PT3-11
// [07 §10 "Correction — the raw delta is thirtieths of a second"].
//
// Because the reading is integral, most frames at 60 Hz return 0 and scroll
// nothing; the sustained rate is scrollByte*30 map pixels per second at any
// frame rate.
//
// The paused branch below looks like the bug this function fixes and is not:
// it is what retail does, established from the image and written up in
// [07 §10 "The scroll pass while paused"]. In single-player the pump gates the
// budget call behind the pause test, and the delta word has exactly one writer
// — that budget step — so while paused neither the delta nor the anchor is
// refreshed. The scroll pass itself is gated only on the in-battle options
// window, never on pause, so it keeps running and keeps re-multiplying the
// frozen delta. Retail therefore scrolls a paused camera at scrollByte map
// pixels per host frame when the pause landed on a frame whose delta was 1,
// and not at all when it landed on a frame whose delta was 0. Do not "fix"
// this into a zero: paused camera movement is a feature (the player looks
// around a frozen battle), and its rate is retail's.
//
// Leaving the anchor alone across the pause is the same contract: the first
// unpaused frame spends the whole pause in one delta and takes a single
// 128-pixel capped step, the scroll-pass twin of the single-player unpause
// burst of [01 §4.3].
func (b *battleSession) refreshScrollDelta() int32 { // [07 §10]
	if b == nil {
		return 0
	}
	if b.sess != nil && b.sess.Clock != nil && b.sess.Clock.Paused {
		// Established, not inferred: neither delta nor anchor moves while
		// single-player is paused [07 §10 "The scroll pass while paused"].
		// Multiplayer differs — its budget runs every iteration — but this is a
		// single-player engine.
		return b.scrollDelta
	}
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	now := clock.ScaledNow(b.millisSource.Millis32())
	if !b.scrollAnchorSet {
		// Retail's anchor is already tracking wall-clock when the battle mode
		// takes over, so its first battle frame sees an ordinary one-frame
		// delta. Seeding here reproduces that instead of charging the whole
		// pre-battle uptime to the first frame.
		b.scrollAnchor, b.scrollAnchorSet, b.scrollDelta = now, true, 0
		return 0
	}
	b.scrollDelta = now - b.scrollAnchor // signed; a wrapped host counter reverses one frame [07 §10]
	b.scrollAnchor = now
	return b.scrollDelta
}

// loadedSettings reads the persisted block, ignoring a read failure the same
// way scrollSetting does: a preferences file that cannot be read yields the
// defaults rather than refusing to start the battle.
func loadedSettings() settings.Settings {
	s, _ := settings.Load()
	return s
}

// applyDamageBarsSetting installs bit 0 of the interface-flags word from the
// loaded block. Retail reads `damagebars` once at settings load
// [07 R-HUD-03 §7]; the bit's only consumer is the composer's label walk
// [03 R-FX-01 §6].
func applyDamageBarsSetting(s settings.Settings) {
	client.SetDamageBars(s.DamageBarsEnabled())
}

// applyMessageLineSettings installs the loaded block's message-column ring
// configuration onto the battle client. `textlines` is the ring's line
// budget and `textscroll` its line-age limit; both are read once at settings
// load, the same as `damagebars` [02 §3][07 R-HUD-03 §14.3]. `screenchat`
// sets the class filter the message column paints through [07 R-HUD-03
// §14.4]. Normalize (already run by settings.Load) guarantees non-negative
// values here.
func applyMessageLineSettings(cl *client.Client, s settings.Settings) {
	if cl == nil {
		return
	}
	m := s.Messages
	cl.ConfigureMessageLines(uint16(m.TextLines), uint16(m.TextScroll))
	cl.SetScreenChat(uint8(m.ScreenChat))
}

// damageBarsSettingValue is the live bit, in the form the persisted block
// stores it. The whole block is written from live state, so the value the
// frontend writes back has to come from the interface word, not from the file
// [07 R-HUD-03 §7].
func damageBarsSettingValue() int {
	if client.DamageBars() {
		return settings.InterfaceFlagDamageBars
	}
	return 0
}

// toggleDamageBars is the battle key command of [07 R-CAM-01 §2]: it flips bit
// 0 of the interface-flags word and writes every setting back immediately
// [07 R-HUD-03 §7]. A failed write costs the persistence, never the toggle.
func (b *battleSession) toggleDamageBars() {
	on := client.ToggleDamageBars()
	if err := settings.StoreDamageBars(on); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", err)
	}
}

// isTalkGUIActive reports whether TALK.GUI suppresses held-arrow movement [07 §10].
// Retail suppresses only held-arrow, not edge. In Nanolathe chat is not yet fully
// wired, so we treat any active modal that would correspond to chat as active.
// For now, no dedicated TALK.GUI state exists, so held-arrow is never suppressed
// except when a modal menu is active which already suppresses edge separately.
// This preserves retail's distinction: TALK suppresses arrow but not edge.
func (b *battleSession) isTalkGUIActive() bool { // [07 §10]
	// Chat is out of scope for this build — it is `TALK.GUI` with its own
	// dialog, focus and recipient rows [07 §5 "Chat"], and a single-player
	// session drops the packet unsent — so no TALK.GUI is ever active and the
	// suppression this predicate gates never fires. Not an open question.
	return false
}

func (b *battleSession) cursorWorld(sx, sy int32) (wx, wy, wz numeric.Fixed) {
	if b.cam == nil {
		return 0, 0, 0
	}
	// Step 1 of the host frame classifies the pointer before any ground
	// resolution. A pointer inside the minimap rectangle takes the lens
	// conversion, not the view's cursor-to-world projection — the two paths do
	// not share a routine in retail either — and the resulting map pixels then
	// go through the same ground resolver [07 R-CAM-01 §11][03 §3.11].
	if mpx, mpz, ok := b.minimapPointerWorld(sx, sy); ok {
		if b.sess != nil {
			if wx, wy, wz, ok := b.sess.CursorToWorld(mpx, mpz); ok {
				return wx, wy, wz
			}
		}
		return numeric.Fixed(mpx) << 16, 0, numeric.Fixed(mpz) << 16
	}
	// Clamp pointer into the battle viewport before ground resolution [07 §8] step 1 [C-2].
	clampedX := sx
	clampedY := sy
	if clampedX < camera.OriginX+1 {
		clampedX = camera.OriginX + 1
	} else if clampedX > 639 {
		clampedX = 639
	}
	if clampedY < camera.OriginY {
		clampedY = camera.OriginY
	} else if clampedY > 447 {
		clampedY = 447
	}
	// The renderer stores world points at beam position minus OriginX/Y. Restore
	// those fixed offsets for the camera inverse [03 §2.5].
	fx, fz := b.cam.ScreenToWorld(clampedX+camera.OriginX, clampedY+camera.OriginY)
	if b.sess == nil {
		return fx, 0, fz
	}
	if wx, wy, wz, ok := b.sess.CursorToWorld(int32(fx>>16), int32(fz>>16)); ok {
		return wx, wy, wz
	}
	return fx, 0, fz
}

// armPlacement enters build-placement mode for a product [07 §9]. Retail arms
// the MOBILEBUILD latch, stores the product's definition id in a pending-build
// word and plays the `addbuild` cue; nothing is queued until the world click.
func (b *battleSession) armPlacement(def *content.UnitDef) {
	if def == nil {
		return
	}
	footX, footZ := footprintCellsForCatalog(b.cat, def)
	b.battleState().ArmPlacement(def.CanonicalKey, footX, footZ)
	b.battleState().Input.Latch = input.LatchMobileBuild
}

// disarmPlacement returns the battle screen to the idle latch after a placement
// ends, whether it ended in a click, a cancel, or shift being released [07 §9].
func (b *battleSession) disarmPlacement() {
	if b == nil {
		return
	}
	b.battleState().ClearPlacement()
}

// playUICue plays a non-positional interface sound by its authored alias
// [07 §9][03 §8.3]. Retail's placement path plays `oktobuild` on a placed site
// and `notoktobuild` on a refused one; both are ordinary sound aliases, not a
// separate UI audio path.
func (b *battleSession) playUICue(cl *client.Client, alias string) {
	if b == nil || b.sess == nil || b.sess.Audio == nil {
		return
	}
	_ = b.sess.Audio.PlayUICue(alias)
}

// updatePlacement tracks the ghost under the cursor and validates it against
// the world [04 §6.2][PLAN_08 C17].
func (b *battleSession) updatePlacement(mx, my int32) {
	b.battleState().Input.BuildMX, b.battleState().Input.BuildMY = mx, my
	wx, _, wz := b.cursorWorld(mx, my)
	b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ = world.PlacementAnchor(wx, wz, b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ)
	self := uint16(0)
	if frame, ok := b.currentSnapshot(); ok {
		self = uint16(frame.CommandPage.Builder)
	}
	footX, footZ := b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ
	var def *content.UnitDef
	if b.cat != nil {
		def, _ = b.cat.Unit(b.battleState().Input.BuildDef)
	}
	result, err := b.checkProductPlacement(b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ, def, footX, footZ, self)
	b.battleState().Input.BuildOK = err == nil
	if err == nil {
		b.battleState().Input.BuildSiteH = result.SiteHeight
	} else {
		// PreviewPlacement returns the same derived height on rejection; retaining
		// this value avoids a presentation-side terrain read or fallback rule.
		b.battleState().Input.BuildSiteH = result.SiteHeight
	}
}

// checkProductPlacement is the battle-side adapter to the canonical
// preview/commit predicate. It resolves movement/FBI terrain rules and uses
// the product's compiled footprint, matching construction exactly [R-P0-08]
// [07 §9].
//
// The human build cursor is the one caller in the whole executable that binds
// the blocker's fourth argument to a PLAYER record rather than null
// [04 R-P0-08-B §1], so the ghost goes through PreviewPlacementForCursor: it
// runs the known-site gate — the footprint centre projected onto the
// 32-world-unit LOS grid, a site off that grid or one the local viewing slot
// cannot currently see rejected outright — before the mapping option decides
// whether the occupancy rejections apply. The null-player form this used to
// call accepts a site the local player cannot see.
func (b *battleSession) checkProductPlacement(cx, cz int32, def *content.UnitDef, footX, footZ int32, self uint16) (world.PlacementResult, error) {
	if b == nil || b.sess == nil {
		return world.PlacementResult{}, fmt.Errorf("battle: placement world unavailable")
	}
	return b.sess.PreviewPlacementForCursor(cx, cz, def, footX, footZ, pool.Handle(self))
}

// placementRect returns the armed site's footprint as a screen rectangle
// [07 §9]. Retail projects the two cell-aligned corners with the ordinary
// half-height shear, using the site height for both, so the ghost lies flat on
// the ground the building will stand on rather than following the cursor.
func (b *battleSession) placementRect() (left, top, right, bottom int32) {
	l := b.battleState().Input.BuildCellX * 16
	t := b.battleState().Input.BuildCellZ * 16
	r := l + b.battleState().Input.BuildFootX*16
	btm := t + b.battleState().Input.BuildFootZ*16
	return b.siteRectToScreen(l, t, r, btm, b.battleState().Input.BuildSiteH)
}

// siteRectToScreen projects a map-pixel footprint rectangle standing at height
// h (in map-pixel height units) into viewport-relative screen pixels [03 §2.5].
// Both corners take the same height, which is what flattens the marker onto the
// ground plane instead of tilting it.
func (b *battleSession) siteRectToScreen(l, t, r, btm, h int32) (left, top, right, bottom int32) {
	if b.cam == nil {
		return l, t, r, btm
	}
	px := func(v int32) numeric.Fixed { return numeric.Fixed(int64(v) << 16) }
	x0, y0 := b.cam.WorldToScreen(px(l), px(h), px(t))
	x1, y1 := b.cam.WorldToScreen(px(r), px(h), px(btm))
	return x0 - camera.OriginX, y0 - camera.OriginY, x1 - camera.OriginX, y1 - camera.OriginY
}

// yardMapFor returns the placed definition's yard text when known.
func (b *battleSession) yardMapFor() string {
	def, found := b.cat.Unit(b.battleState().Input.BuildDef)
	if !found || def == nil {
		return ""
	}
	return def.YardMap
}

// commitBuild queues a mobile-build order through the ordinary construction
// path [PLAN_08 C12/C23][P0-I05]; the session's construction pump drives the lifecycle.
// Site coordinates are passed at queue time and preserved on the queued node as GoalX/Y/Z [P0-I05]:
// the validated footprint center and site height carry the selected site via QueueMobileBuild.
// It uses catalog indices (not FNV hash) [P0-I05] and respects queue modifier (shift=queued) [04 §3.3][P0-I14].
// Every producer goes through one canonical payload constructor [P0-I03]: orders.NewMobileBuildNode / QueueMobileBuild.
// It is data-driven: product must be in builder's BuildMenus list; illegal placement queues nothing [R-P0-03].
func (b *battleSession) commitBuild(queued bool) bool {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 {
		return false
	}
	v, found := snapshotUnitByHandle(frame, frame.CommandPage.Builder)
	if !found || b.sess == nil || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) {
		return false
	}
	if b.cat != nil && !hud.ValidateBuildProduct(b.cat, v.DefName, b.battleState().Input.BuildDef) {
		return false // GUI may not invent products absent from authored list [R-P0-03]
	}
	if !b.battleState().Input.BuildOK {
		return false // illegal placement queues nothing [R-P0-03]
	}
	// The order carries the footprint's center and the site height, not the raw
	// cursor point: retail recomputes the same cell-aligned anchor the ghost was
	// drawn on and stores `((foot + 2*cell) << 19)` per axis with the validator's
	// site height as Y [07 §9]. Sending the cursor point instead would put the
	// building half a footprint off the box the player aimed with.
	wx, wz := world.PlacementCenter(b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ, b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ)
	// A queued (Shift) click on a point that already carries a queued order of
	// this kind removes that order and issues nothing — one node, front-most
	// match, goal within one map cell on X and Z, product not part of the match
	// [07 R-P0-11 §6]. That test lives at the authoritative order-insertion
	// boundary, where the builder's live queue is, not here: the presentation
	// sends the same command either way and the session decides whether it adds
	// or removes. The Shift-gated overlay walks the live queues every frame, so
	// a removal stops drawing on the next published tick with no invalidation
	// of its own.
	//
	// Queue the typed command; the session applies it at the authoritative input
	// phase [01 §4.4][07 §9].
	wy := numeric.Fixed(int64(b.battleState().Input.BuildSiteH) << 16)
	if err := b.DispatchMobileBuild(b.battleState().Input.BuildDef, wx, wy, wz, queued); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: build %s: %v\n", b.battleState().Input.BuildDef, err)
		return false
	}
	return true
}

// drawBuildGhost draws the armed build site the way retail does [07 §9].
//
// The ghost is two nested one-pixel outlines on the footprint's own cell-aligned
// rectangle — not a box centered on the cursor — and both are drawn in a single
// color chosen by site validity: logical palette entry 10 when the site is legal
// and 4 when it is not. Retail draws no text next to it; the product name and
// its cost live on the build button, and the cursor (`cursorfindsite` versus
// `cursortoofar`) carries the validity as well.
//
// Retail suppresses the ghost whenever the pointer leaves the world viewport,
// so it never appears over the side panel or the minimap.
func (b *battleSession) drawBuildGhost(c *client.Client) {
	if b.battleState().Input.BuildDef == "" || b.cam == nil {
		return
	}
	if !b.overWorld(b.battleState().Input.PointerX, b.battleState().Input.PointerY) {
		return
	}
	l, t, r, btm := b.placementRect()
	col := c.GUIColor(hud.GhostColorIllegal)
	if b.battleState().Input.BuildOK {
		col = c.GUIColor(hud.GhostColorLegal)
	}
	c.UIFrameRect(int(l), int(t), int(r-l), int(btm-t), col)
	c.UIFrameRect(int(l)+1, int(t)+1, int(r-l)-2, int(btm-t)-2, col)
}

// footprintCells returns a definition's footprint, clamped to at least one cell
// on each axis so a definition that authors neither still occupies a square.
func footprintCells(def *content.UnitDef) (footX, footZ int32) {
	footX, footZ = def.FootprintX, def.FootprintZ
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	return footX, footZ
}

func footprintCellsForCatalog(cat *content.Catalog, def *content.UnitDef) (footX, footZ int32) {
	// HUD preview must resolve the same compiled movement/FBI footprint as the
	// sim [R-P0-08][07 §9] C-7: prefer the world helper so the two cannot
	// diverge.
	return world.FootprintForUnit(cat, def)
}

// loadPalette loads the retail palette tables for compatibility with focused
// presentation tests. Production construction uses loadPaletteStrict so a
// missing or malformed shared palette cannot become an unannounced nil.
func loadPalette(cs *contentSet) *palette.Tables {
	p, _ := loadPaletteStrict(cs)
	return p
}

func loadPaletteStrict(cs *contentSet) (*palette.Tables, error) {
	if cs == nil || cs.fs == nil {
		return nil, retailFrontendAssetError(cs, "retail palette", "palettes/PALETTE.PAL", "the shared retail palette tables", fmt.Errorf("missing VFS"))
	}
	p, err := palette.Load(cs.fs)
	if err != nil {
		return nil, retailFrontendAssetError(cs, "retail palette", "palettes/PALETTE.PAL", "the shared retail palette tables", err)
	}
	return p, nil
}

// loadFNT loads the first available UI font, nil on failure.
func loadFNT(cs *contentSet) *formats.FNT {
	for _, name := range []string{"fonts/smlfont.fnt", "fonts/armfont.fnt", "fonts/hatt12.fnt"} {
		if data, err := cs.fs.ReadFileLimit(name, 1<<20); err == nil {
			if parsed, ferr := formats.LoadFNT(data); ferr == nil {
				return parsed
			}
		}
	}
	return nil
}

// pickTarget returns the unit handle under the cursor if any, else ground pos.
// It is the ONE picking routine that respects fog (local-player word), overlap
// (nearest squared distance wins with strict < tie-break so lower slot wins on
// equal), unit/feature overlap (units win when both within radius, else feature
// under cell wins), and command validity via orders.Resolve gate [04 §3.5][07 §9][03 §3.2] C8 [P0-I03][P0-I14].
// Feature picking sets ResolvePos.HasFeature when a feature footprint covers the
// clicked cell and is visible; unit picking is tried first [07 §8][07 §9][P0-I14].
//
// The short-lived copy carries the committed remaining-build fraction. It is a
// clause of the shared eligibility predicate `E(u)` and the word the shape
// chooser reads in the opposite, *unfinished* sense for its cursorrepair rows
// [07 R-WGT-01 §10]; leaving it zero made every nanoframe look finished, so the
// idle branch offered `cursorselect` over one.
func (b *battleSession) pickTarget(sx, sy int32) (pool.Handle, *units.Unit, *orders.ResolvePos) {
	wx, wy, wz := b.cursorWorld(sx, sy)
	pos := &orders.ResolvePos{X: wx, Y: wy, Z: wz}
	if b.sess == nil || b.cam == nil {
		return 0, nil, pos
	}
	// Presentation picking reads only the immutable committed frame. The
	// returned unit is a short-lived copy for cursor semantics, never a live
	// world pointer [07 §8][07 §9][I6].
	//
	// Over the minimap the pointer's unit word is not the view's hot-units
	// winner but the unit whose minimap dot lies within squared pixel distance
	// 4 of the pointer, nearest first [07 R-HUD-03 §1]. cursorWorld above has
	// already taken the lens branch for the position, so only the unit word
	// differs — and both take it under the same condition, an armed drag
	// rectangle keeping the pointer in the view branch [07 R-CAM-01 §11].
	if b.isOverMinimap(sx, sy) && !b.battleState().Input.DragActive {
		f, ok := b.currentSnapshot()
		if !ok {
			return 0, nil, pos
		}
		handle := b.minimapHoverUnit(f, sx, sy)
		if handle == 0 {
			return 0, nil, pos
		}
		for i := range f.Units {
			view := f.Units[i]
			if view.Slot != handle {
				continue
			}
			hit := &units.Unit{Handle: handle, Owner: view.Owner, X: view.X, Y: view.Y, Z: view.Z, Flags: view.Flags, Health: view.Health, MaxHealth: view.MaxHealth, Remaining: view.BuildRemaining, Alive: true}
			if b.cat != nil && view.DefName != "" {
				hit.Def, _ = b.cat.Unit(view.DefName)
			}
			return handle, hit, pos
		}
		return 0, nil, pos
	}
	if f, ok := b.currentSnapshot(); ok {
		if bh, view, hit := client.PickSnapshotUnit(f, sx, sy, b.cam, b.sess.LocalOwner); hit {
			copy := &units.Unit{Handle: bh, Owner: view.Owner, X: view.X, Y: view.Y, Z: view.Z, Flags: view.Flags, Health: view.Health, MaxHealth: view.MaxHealth, Remaining: view.BuildRemaining, Alive: true}
			if b.cat != nil && view.DefName != "" {
				copy.Def, _ = b.cat.Unit(view.DefName)
			}
			return bh, copy, pos
		}
	}
	// Feature picking also consumes only the committed frame. Features are
	// admitted by their published visibility cell and footprint metadata [I6].
	if f, ok := b.currentSnapshot(); ok {
		cx := world.WorldToCell(wx)
		cz := world.WorldToCell(wz)
		for _, fv := range f.Features {
			footX, footZ := int32(fv.FootX), int32(fv.FootZ)
			if footX <= 0 {
				footX = 1
			}
			if footZ <= 0 {
				footZ = 1
			}
			if cx < fv.CX || cx >= fv.CX+footX || cz < fv.CZ || cz >= fv.CZ+footZ {
				continue
			}
			if !snapshotFeatureVisible(f, fv, b.sess.LocalOwner) {
				continue
			}
			pos.HasFeature = true
			pos.IsWreck = b.isCorpseName(fv.DefName)
			pos.FeatureResurrectable = pos.IsWreck && fv.Reclaimable
			break
		}
	}
	return 0, nil, pos
}

func snapshotFeatureVisible(f *frame.Frame, v frame.FeatureView, viewer uint8) bool {
	if v.OwnerKnown && v.Owner == viewer {
		return true
	}
	if f == nil {
		return false
	}
	footX, footZ := int32(v.FootX), int32(v.FootZ)
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	// Feature LOS uses the committed two-corner footprint form. CX/CZ are
	// 16-pixel attribute cells; visibility tiles are 32 pixels, and the shared
	// projected-point helper performs that conversion plus Y shear [03 §3.2].
	minX, minZ := world.CellToWorld(v.CX), world.CellToWorld(v.CZ)
	maxX, maxZ := world.CellToWorld(v.CX+footX), world.CellToWorld(v.CZ+footZ)
	return client.SnapshotPointVisible(f.Visibility, minX, v.Y, minZ, viewer) ||
		client.SnapshotPointVisible(f.Visibility, maxX, v.Y, maxZ, viewer)
}

func (b *battleSession) isCorpseName(name string) bool {
	if b == nil || b.cat == nil {
		return false
	}
	key := content.CanonicalKey(name)
	if i := strings.IndexByte(key, '_'); i >= 0 {
		key = key[:i]
	}
	if key == "" {
		return false
	}
	_, ok := b.cat.Unit(key)
	return ok
}

// isCorpseFeature remains a definition-only helper for authored feature
// checks. Picking itself never calls the live feature service [07 §8][I6].
func (b *battleSession) isCorpseFeature(def *content.FeatureDef) bool {
	if def == nil {
		return false
	}
	return b.isCorpseName(def.CanonicalKey)
}

// orderSelected resolves code at the clicked world position and submits one
// typed order command. Descriptor selection remains solely in orders.Resolve;
// this integration layer does not guess an attack descriptor for ground clicks
// [04 §3.4][07 §9].
func (b *battleSession) orderSelected(code int, sx, sy int32, queued bool) {
	targetHandle, _, pos := b.pickTarget(sx, sy)
	if pos == nil {
		return
	}
	// The HUD latch table is the single semantic mapping between an armed
	// order and the session order code. Validate the caller's code by running
	// it through that table; do not maintain a second switch here [07 §9].
	latch := input.Latch(code)
	if hud.LatchToCode(latch) != code {
		return
	}
	_ = b.DispatchOrderCommand(session.HumanOrderCommand{
		Code: code, Target: targetHandle,
		Position: *pos, Queued: queued,
	})
}

// updateCursor resolves the software-cursor shape for this frame [07 §8].
// The pointer region, the armed latch, the selection, and the world pick under
// the pointer are the whole input; the chooser owns the truth table.
func (b *battleSession) updateCursor(cl *client.Client) {
	cursors := cl.Cursors()
	if cursors == nil || b.sess == nil {
		return
	}
	mouse := cl.Input().Mouse
	mx, my := int32(mouse.X), int32(mouse.Y)
	// The footer's pointer record is written by the same per-frame pointer
	// pass, and by nothing else [07 R-HUD-03 §1].
	b.updateFooterHover(mx, my)
	hover := hud.CursorHover{
		OverWorld:      b.overWorld(mx, my),
		Placing:        b.battleState().Input.BuildDef != "",
		PlacementValid: b.battleState().Input.BuildOK,
	}
	if hover.OverWorld && !hover.Placing {
		_, hover.Target, _ = b.pickTarget(mx, my)
		hover.Feature = b.hoverFeature(mx, my)
	}
	sel := hud.CursorSelection{Viewer: b.sess.LocalOwner, Hostile: b.hostile}
	if f, ok := b.currentSnapshot(); ok {
		for _, handle := range f.Selection.Handles {
			for i := range f.Units {
				v := f.Units[i]
				if v.Slot == handle && v.Owner == b.sess.LocalOwner {
					sel.Units = append(sel.Units, b.snapshotUnitCopy(v))
					break
				}
			}
		}
	}
	if f, ok := b.currentSnapshot(); ok {
		for _, ev := range f.Economy {
			if ev.Player == b.sess.LocalOwner {
				sel.Metal, sel.Energy = ev.Metal, ev.Energy
				break
			}
		}
	}
	cursors.SetIndex(hud.ChooseCursor(b.battleState().Input.Latch, sel, hover))
}

// overWorld reports whether a pointer position lies in the world viewport
// rather than on the HUD chrome; chrome forces the idle cursor shape [07 §8].
// It uses the same drawn-chrome layout as the click path, so the shape and
// click destination cannot disagree [C-3][07 §6][07 §8].
func (b *battleSession) overWorld(x, y int32) bool {
	if b.hud != nil {
		return b.hud.overWorld(x, y)
	}
	// When no authored HUD layout is available, use the viewport transform's
	// drawn-chrome rectangle [C-3][07 §8].
	vt := client.NewViewportTransform(b.cam, nil, 640, 480)
	return vt.Viewport.Contains(x, y)
}

// hoverFeature returns the definition of the feature occupying the cell under
// the pointer, or nil [07 §8][05 "Feature instance and terrain cell"].
func (b *battleSession) hoverFeature(sx, sy int32) *content.FeatureDef {
	if b.cam == nil {
		return nil
	}
	wx, _, wz := b.cursorWorld(sx, sy)
	cx, cz := world.WorldToCell(wx), world.WorldToCell(wz)
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	for _, v := range f.Features {
		footX, footZ := int32(v.FootX), int32(v.FootZ)
		if footX <= 0 {
			footX = 1
		}
		if footZ <= 0 {
			footZ = 1
		}
		if cx < v.CX || cx >= v.CX+footX || cz < v.CZ || cz >= v.CZ+footZ || !snapshotFeatureVisible(f, v, b.sess.LocalOwner) {
			continue
		}
		return &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: v.DefName}, FootprintX: int32(v.FootX), FootprintZ: int32(v.FootZ), Height: v.Height, Reclaimable: v.Reclaimable, Geothermal: v.Geothermal, Blocking: v.Blocking}
	}
	return nil
}

// updateFooterHover runs the battle pointer handler's per-frame pass over the
// footer's two world sources [07 R-HUD-03 §1]. The hovered-unit word is
// rewritten only inside the view with no drag-selection rectangle armed, or
// over the minimap; everywhere else it is left alone. The hovered-feature
// word is the feature under the pointer's ground cell and is recomputed every
// frame wherever the pointer is. Neither reads the selection.
func (b *battleSession) updateFooterHover(mx, my int32) {
	if b == nil || b.sess == nil {
		return
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	switch {
	case b.isOverMinimap(mx, my):
		b.footerHoverUnit = b.minimapHoverUnit(f, mx, my)
	case b.overWorld(mx, my) && !b.battleState().Input.DragActive:
		handle, _, _ := client.PickSnapshotUnit(f, mx, my, b.cam, b.sess.LocalOwner)
		b.footerHoverUnit = handle
	}
	b.footerHoverFeature = ""
	if def := b.hoverFeature(mx, my); def != nil {
		b.footerHoverFeature = def.CanonicalKey
	}
}

// minimapHoverUnit is the minimap half of the pointer's unit word: the unit
// whose minimap dot lies within squared pixel distance < 4 of the pointer,
// nearest first, else 0 [07 R-HUD-03 §1]. Ties keep the lower pool slot so the
// result is stable [I1].
func (b *battleSession) minimapHoverUnit(f *frame.Frame, mx, my int32) pool.Handle {
	layout, dst, ok := b.minimapLayout()
	if !ok {
		return 0
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return 0
	}
	left, top, right, bottom := dst.Ordered()
	width, height := right-left+1, bottom-top+1
	best := pool.Handle(0)
	bestDist := int64(1 << 62)
	for i := range f.Units {
		v := f.Units[i]
		if v.Slot == 0 || !client.SnapshotVisible(f, v, b.sess.LocalOwner) {
			continue
		}
		rx, ry := render.RadarProjection(radarMapPixel(v.X), radarMapPixel(v.Z), radarMapPixel(v.Y), playW, playH, layout)
		px, py, ok := layout.CanvasToDisplay(rx+layout.PadX, ry+layout.PadY, left, top, width, height)
		if !ok {
			continue
		}
		dx, dy := int64(px-mx), int64(py-my)
		d := dx*dx + dy*dy
		if d >= 4 {
			continue
		}
		if d < bestDist || (d == bestDist && (best == 0 || v.Slot < best)) {
			bestDist, best = d, v.Slot
		}
	}
	return best
}

// footerHover assembles the footer's pointer record for this frame. The
// gadget index comes from the HUD's own hit test; the visibility predicate is
// the committed mask, which only the composer can project into
// [07 R-HUD-03 §1][03 R-VIS-01 §4][I6].
func (b *battleSession) footerHover(f *frame.Frame) hud.FooterHover {
	out := hud.FooterHover{Gadget: hud.NoGadget}
	if b == nil {
		return out
	}
	out.Unit = b.footerHoverUnit
	out.Feature = b.footerHoverFeature
	out.Visible = func(v *frame.UnitView) bool {
		return v != nil && client.SnapshotVisible(f, *v, b.sess.LocalOwner)
	}
	out.Gadget, out.GadgetName = b.hud.hoveredGadgetSource()
	out.StockpilePercent = footerStockpilePercent(f, b.cat, out.Unit)
	return out
}

// footerStockpilePercent is the build-page percentage of a stockpiling
// weapon: the BuildWeapon node's progress times 100 over the compiled reload
// time of the weapon its slot index selects, an integer divide [06 §11.1].
// Stockpile nodes live in the unit's secondary queue.
func footerStockpilePercent(f *frame.Frame, cat *content.Catalog, handle pool.Handle) int32 {
	if f == nil || cat == nil || handle == 0 {
		return 0
	}
	view := hud.FooterUnit(f, handle)
	if view == nil || view.DefName == "" {
		return 0
	}
	def, ok := cat.Unit(view.DefName)
	if !ok || def == nil {
		return 0
	}
	for i := range f.OrderQueues {
		q := &f.OrderQueues[i]
		if q.Unit != handle {
			continue
		}
		for _, node := range q.Secondary {
			if node.Kind != "BuildWeapon" {
				continue
			}
			weapon := stockpileWeaponForSlot(def, int(node.Param1))
			if weapon == nil || weapon.ReloadTime <= 0 {
				continue
			}
			return int32(int64(node.Param3) * 100 / int64(weapon.ReloadTime))
		}
		break
	}
	return 0
}

func stockpileWeaponForSlot(def *content.UnitDef, slot int) *content.WeaponDef {
	slots := [3]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def}
	if slot < 0 || slot >= len(slots) {
		return nil
	}
	weapon := slots[slot]
	if content.IsWeaponInactive(weapon) || !weapon.Stockpile {
		return nil
	}
	return weapon
}

func (b *battleSession) snapshotUnitCopy(v frame.UnitView) *units.Unit {
	u := &units.Unit{Handle: v.Slot, Owner: v.Owner, X: v.X, Y: v.Y, Z: v.Z, Flags: v.Flags, Health: v.Health, MaxHealth: v.MaxHealth, Activated: v.Activated, Alive: true}
	if b != nil && b.cat != nil && v.DefName != "" {
		u.Def, _ = b.cat.Unit(v.DefName)
	}
	return u
}

// hostile routes the cursor's side test through the acting unit's own
// diplomacy predicate, the one the order resolver consults [04 §3.4].
func (b *battleSession) hostile(actor, target *units.Unit) bool {
	if actor == nil || target == nil {
		return false
	}
	if q := orders.QueueForUnit(actor); q != nil {
		if binding := q.Binding(); binding != nil && binding.Hostility != nil {
			return binding.Hostility(actor, target)
		}
	}
	return actor.Owner != target.Owner
}

// isResultVisible reports whether the authoritative result overlay should be shown [RS-05][08][P1-01].
// It is presentation-only and reads only the committed result view (I6).
// The overlay is visible when the terminal result is latched (Ended) and has not been dismissed.
func (b *battleSession) isResultVisible() bool {
	if b == nil || b.battleState().Input.ResultDismissed {
		return false
	}
	cur, ok := b.currentSnapshot()
	return ok && cur.Result.Ended
}

// resultView returns the current authoritative result view for overlay [RS-05].
func (b *battleSession) resultView() frame.ResultView {
	if b == nil {
		return frame.ResultView{}
	}
	cur, ok := b.currentSnapshot()
	if !ok || !cur.Result.Ended {
		return frame.ResultView{}
	}
	return cur.Result
}

// doResultAction executes the result overlay button action through the state graph [RS-05][08 "Session states"].
func (b *battleSession) doResultAction(kind ui.ResultAction, cl *client.Client) {
	if b == nil {
		return
	}
	switch kind {
	case ui.ResultActionSkirmish:
		if b.returnToSkirmish != nil {
			b.returnToSkirmish(cl)
		} else if b.shell != nil {
			b.shell.openMenu(modeMenuSkirmish)
		} else if b.returnToMenu != nil {
			b.returnToMenu(cl)
		}
		b.battleState().Input.ResultDismissed = true
	case ui.ResultActionMainMenu:
		if b.postBattle != nil {
			if b.postBattle.Handle(session.PostBattleControlMainMenu, b.postBattleNow()) {
				b.consumePostBattleEffects(b.postBattleNow(), cl)
			}
			return
		}
		if b.returnToMenu != nil {
			b.returnToMenu(cl)
		} else if b.shell != nil {
			b.shell.openMenu(modeMenuMain)
		}
		b.battleState().Input.ResultDismissed = true
	case ui.ResultActionContinue:
		if b.postBattle == nil {
			return
		}
		if b.postBattle.Handle(session.PostBattleControlStart, b.postBattleNow()) {
			b.consumePostBattleEffects(b.postBattleNow(), cl)
			b.routePostBattleStart(cl)
		}
	}
}

func resultContinuesCampaign(view frame.ResultView) bool {
	return view.Ended && !view.Draw && strings.EqualFold(view.Kind, "victory")
}

// setStatusMessage stores a transient on-screen message [07 §11][07 §2] presentation-only (I6).
func (b *battleSession) setStatusMessage(msg string) {
	if b == nil {
		return
	}
	cur, ok := b.currentSnapshot()
	b.battleState().Input.StatusMessage = msg
	if !ok {
		// Keep the semantic message latched for a later publication, but never
		// make it visible or assign a lifetime from the live clock [I6].
		b.battleState().Input.StatusUntil = 0
		return
	}
	// Display for 90 committed ticks (~3 seconds at 30 Hz). The exact duration
	// remains an implementation boundary, but its lifetime uses frame ticks.
	b.battleState().Input.StatusUntil = cur.Tick + 90
}

// statusVisible reports whether the transient message should be drawn [07 §11].
func (b *battleSession) statusVisible(cur *frame.Frame) bool {
	if b == nil || cur == nil || b.battleState().Input.StatusMessage == "" {
		return false
	}
	return cur.Tick <= b.battleState().Input.StatusUntil
}

// adjustGameSpeed emits a concrete UI scheduling intent; Session performs the
// clamp and applies it at the scheduling boundary [07 §11][07 §2].
func (b *battleSession) adjustGameSpeed(delta int) {
	if b == nil || b.sess == nil {
		return
	}
	b.applyBattleSchedule(ui.SpeedIntent(delta))
}

func (b *battleSession) setGameSpeed(delta int) {
	if b == nil || b.sess == nil {
		return
	}
	// The clamp is the setter's: 1..20 on signed compares, and the
	// announcement is posted only when the clamped target actually changes
	// [07 R-CAM-01 §3]. AdjustSpeed owns both.
	newReq, changed := b.sess.AdjustSpeed(delta)
	if !changed {
		return
	}
	// The announcement goes to the message ring as kind 2, with no source unit
	// and silent [07 R-CAM-01 §3]. It is also latched as the transient status
	// line, which is what this build's HUD draws today.
	msg := frame.SpeedAnnouncement(int(newReq))
	b.messageRing().PostSilent(msg, frame.MessageClassSpeed, b.currentTick())
	b.setStatusMessage(msg)
}

// togglePause flips the pause bit. Retail's established pause presentation is
// the authored igpaused title; the exact localized status strings are unknown,
// so this path intentionally emits no invented text [07 §11].
func (b *battleSession) togglePause() {
	if b == nil || b.sess == nil {
		return
	}
	b.applyBattleSchedule(ui.PauseIntent(!b.battleState().Paused()))
}

// messageRing is the battle shell's presentation message ring [07 R-HUD-03
// §14.3]. The caption ring bound to the audio queue lives in internal/client
// and is a second instance of the same value type; unifying the two is the
// business of that package's owner, not of the hotkey dispatcher.
func (b *battleSession) messageRing() *frame.MessageRing {
	if b == nil {
		return nil
	}
	if b.messages == nil {
		b.messages = frame.NewMessageRing()
	}
	return b.messages
}

// currentTick is the committed tick used to stamp presentation ring lines.
func (b *battleSession) currentTick() uint32 {
	if f, ok := b.currentSnapshot(); ok {
		return f.Tick
	}
	return 0
}

// ownSelectableUnit is the census's "own selectable unit" predicate
// [07 R-CAM-01 §2], byte-for-byte the predicate the rectangle selection of
// [07 §9] and the trigger system's eligible-unit test [08 R-TRIG-01 §3] share.
// Its four clauses, in that section's order: the status word carries
// selection bit 5 (`0x20`); construction remaining is `0.0`; the post-capture
// grace counter is `0`; and either the carrier reference is null or the
// carrier's own status word carries the cargo-selectable bit 30.
//
// The last two used to be missing, on the grounds that neither crossed the
// frame boundary. Neither needs to:
//
//   - The grace counter "is armed to 150 ticks only by a capture whose new
//     owner is a remote (multiplayer) controller … in single-player it is
//     always zero" [08 R-TRIG-01 §3]. Nanolathe is single-player, so the
//     clause is satisfied by construction and is recorded here rather than
//     published. It becomes a real field the day a remote controller exists.
//   - Bit 30 (`0x40000000`) is not a dynamic transport bit at all: it is a
//     static mirror of the carrier definition's `isairbase` flag, written once
//     by the unit initializer and never touched again [04 R-UNIT-06 §3]. The
//     committed carrier's definition answers it, the same way the production
//     dispatcher's builder check reads a compiled definition, so no live pool
//     read and no new frame field are involved [I6].
//
// The visible consequence: a landed aircraft parked on an airbase — or a
// factory product whose factory definition is an airbase — is selectable,
// while cargo aboard an ordinary transport still is not.
func (b *battleSession) ownSelectableUnit(f *frame.Frame, v frame.UnitView) bool {
	if b == nil || b.sess == nil || v.Slot == 0 {
		return false
	}
	if v.Owner != b.sess.LocalOwner {
		return false
	}
	if v.Flags&units.ClassifierEligibleStatus == 0 {
		return false
	}
	if v.BuildRemaining != 0 {
		return false
	}
	// The post-capture grace counter, always zero in single-player
	// [08 R-TRIG-01 §3].
	if v.Carrier == 0 {
		return true
	}
	carrier, ok := snapshotUnitByHandle(f, v.Carrier)
	return ok && b.snapshotAirBase(carrier)
}

// snapshotAirBase answers the carrier's cargo-selectable status bit from the
// compiled definition it mirrors [04 R-UNIT-06 §3][08 R-TRIG-01 §3].
func (b *battleSession) snapshotAirBase(v frame.UnitView) bool {
	if b == nil || b.cat == nil {
		return false
	}
	def, ok := b.cat.Unit(v.DefName)
	return ok && def != nil && def.IsAirBase
}

// eligibleHandlesInRect is the drag-rectangle walk of [07 §9] as
// [07 R-WGT-01 §10] restates it: "walk the local player's unit slice in
// ascending record order; for each record with `E(u)` true, apply the inclusive
// rectangle test... A record failing `E(u)` is neither written, toggled, nor
// counted, regardless of position."
//
// The eligibility filter used to be missing here: the rect walk returned every
// VISIBLE unit, so a drag across a battle line selected the enemy's units and
// half-built nanoframes alongside one's own. `ownSelectableUnit` is this
// build's `E(u)` — the selectable status bit, the remaining-build fraction, and
// the carrier clause through the carrier definition's airbase mirror — so the
// two are composed rather than the predicate being written twice. Both inputs
// are frame order, which is pool-slot order [I1], so the result stays ascending.
func (b *battleSession) eligibleHandlesInRect(f *frame.Frame, rect client.Rect) []pool.Handle {
	if b == nil || f == nil {
		return nil
	}
	eligible := make(map[pool.Handle]struct{}, len(f.Units))
	for i := range f.Units {
		if v := f.Units[i]; b.ownSelectableUnit(f, v) {
			eligible[v.Slot] = struct{}{}
		}
	}
	inRect := client.SnapshotUnitHandlesInRect(f, b.cam, rect, b.sess.LocalOwner)
	out := make([]pool.Handle, 0, len(inRect))
	for _, h := range inRect {
		if _, ok := eligible[h]; ok {
			out = append(out, h)
		}
	}
	return out
}

// ownSelectableHandles walks the committed frame in slot order and returns the
// own selectable units, optionally filtered [07 R-CAM-01 §2]. Frame order is
// pool-slot order, which is the order retail's selection walks [I1].
func (b *battleSession) ownSelectableHandles(keep func(frame.UnitView) bool) []pool.Handle {
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	out := make([]pool.Handle, 0, len(f.Units))
	for i := range f.Units {
		v := f.Units[i]
		if !b.ownSelectableUnit(f, v) {
			continue
		}
		if keep != nil && !keep(v) {
			continue
		}
		out = append(out, v.Slot)
	}
	return out
}

// selectedHandlesInSlotOrder returns the committed selection in slot order.
func (b *battleSession) selectedHandlesInSlotOrder() []pool.Handle {
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	out := make([]pool.Handle, 0, len(f.Selection.Handles))
	for i := range f.Units {
		if containsHandle(f.Selection.Handles, f.Units[i].Slot) {
			out = append(out, f.Units[i].Slot)
		}
	}
	return out
}

func containsHandle(list []pool.Handle, h pool.Handle) bool {
	for _, v := range list {
		if v == h {
			return true
		}
	}
	return false
}

// commitSelection sends a selection to the session. Additive means "add to the
// current selection" — the census's word for Ctrl+A and for a Shift-held
// category select — which is a union followed by a replace, not the toggle a
// Shift-click performs [07 R-CAM-01 §2][07 §9].
func (b *battleSession) commitSelection(handles []pool.Handle, additive bool) {
	if additive {
		f, ok := b.currentSnapshot()
		if ok {
			merged := make([]pool.Handle, 0, len(handles)+len(f.Selection.Handles))
			merged = append(merged, f.Selection.Handles...)
			for _, h := range handles {
				if !containsHandle(merged, h) {
					merged = append(merged, h)
				}
			}
			handles = merged
		}
	}
	_ = b.enqueueHumanCommand(session.HumanCommand{
		Kind:      session.HumanSelectionReplace,
		Selection: session.HumanSelectionCommand{Handles: handles},
	})
}

// categoryMask resolves an authored category token against the compiled
// registry. Membership is catalog data; no hand-written unit list exists here
// [02 §5][R-P0-03].
func (b *battleSession) categoryMask(name string) (content.CategoryMask, bool) {
	if b == nil || b.cat == nil {
		return content.CategoryMask{}, false
	}
	return b.cat.Category(name)
}

// inCategory reports whether a frame unit's definition is in a membership set.
func (b *battleSession) inCategory(v frame.UnitView, mask content.CategoryMask) bool {
	if b == nil || b.cat == nil {
		return false
	}
	def, ok := b.cat.Unit(v.DefName)
	if !ok || def == nil {
		return false
	}
	return mask.Contains(def.UnitDefID)
}

// dispatchCtrlLetters is the Ctrl+letter column of [07 R-CAM-01 §2]: Ctrl+A,
// Ctrl+C, Ctrl+D, Ctrl+S, Ctrl+Z, and the `CTRL_%c` category selects on
// Ctrl+B and Ctrl+E..R and Ctrl+T..Y. Ctrl+A..Z reach retail as tokens
// 0xAA..0xC3, so exactly one arm runs per press.
func (b *battleSession) dispatchCtrlLetters(kbd *input.KeyboardState) {
	shift := kbd.HasShift()
	switch {
	case kbd.KeyDown(input.KeyA):
		// Select every own selectable unit, additive over the current
		// selection, and clear the current build-menu unit [07 R-CAM-01 §2].
		// This build recomputes the command page's builder from the selection
		// at every publication boundary, so clearing the build-menu unit here
		// means dropping the armed placement.
		b.commitSelection(b.ownSelectableHandles(nil), true)
		b.disarmPlacement()
		return
	case kbd.KeyDown(input.KeyD):
		b.selfDestructSelection()
		return
	case kbd.KeyDown(input.KeyS):
		// The on-screen list, replacing the selection [07 R-CAM-01 §2][07 §8].
		b.commitSelection(b.ownSelectableHandles(b.onScreenUnit), false)
		return
	case kbd.KeyDown(input.KeyZ):
		// Every own selectable unit whose definition matches any currently
		// selected unit's definition [07 R-CAM-01 §2].
		f, ok := b.currentSnapshot()
		if !ok {
			return
		}
		var mask content.CategoryMask
		for i := range f.Units {
			v := f.Units[i]
			if !containsHandle(f.Selection.Handles, v.Slot) {
				continue
			}
			if def, found := b.cat.Unit(v.DefName); found && def != nil {
				mask = mask.Or(def.UnitMask)
			}
		}
		if mask.IsZero() {
			return
		}
		b.commitSelection(b.ownSelectableHandles(func(v frame.UnitView) bool {
			return b.inCategory(v, mask)
		}), false)
		return
	}
	// The category letters. Ctrl+C additionally sets the follow-camera tracked
	// object to the last own unit in the `Commander` category set
	// [07 R-CAM-01 §2][07 R-CAM-01 §12].
	for _, letter := range ctrlCategoryLetters {
		if !kbd.KeyDown(letter.key) {
			continue
		}
		mask, ok := b.categoryMask("CTRL_" + string(letter.name))
		if ok && !mask.IsZero() {
			b.commitSelection(b.ownSelectableHandles(func(v frame.UnitView) bool {
				return b.inCategory(v, mask)
			}), shift)
		}
		if letter.name == 'C' {
			b.followCommander()
		}
		return
	}
}

// ctrlCategoryLetters is the census's `CTRL_%c` domain: Ctrl+B (0xAB),
// Ctrl+C (0xAC), Ctrl+E..Ctrl+R (0xAE..0xBB) and Ctrl+T..Ctrl+Y (0xBD..0xC2).
// Ctrl+A, Ctrl+D, Ctrl+S and Ctrl+Z have their own cases and are absent here
// [07 R-CAM-01 §2]. Stock content authors CTRL_B, CTRL_C, CTRL_F, CTRL_M,
// CTRL_P, CTRL_R, CTRL_V and CTRL_W; every other letter selects nothing, which
// the catalog lookup produces on its own.
var ctrlCategoryLetters = []struct {
	key  input.Key
	name byte
}{
	{input.KeyB, 'B'}, {input.KeyC, 'C'},
	{input.KeyE, 'E'}, {input.KeyF, 'F'}, {input.KeyG, 'G'}, {input.KeyH, 'H'},
	{input.KeyI, 'I'}, {input.KeyJ, 'J'}, {input.KeyK, 'K'}, {input.KeyL, 'L'},
	{input.KeyM, 'M'}, {input.KeyN, 'N'}, {input.KeyO, 'O'}, {input.KeyP, 'P'},
	{input.KeyQ, 'Q'}, {input.KeyR, 'R'},
	{input.KeyT, 'T'}, {input.KeyU, 'U'}, {input.KeyV, 'V'}, {input.KeyW, 'W'},
	{input.KeyX, 'X'}, {input.KeyY, 'Y'},
}

// followCommander latches the follow camera onto the last own unit in the
// `Commander` category set [07 R-CAM-01 §2][07 R-CAM-01 §12].
func (b *battleSession) followCommander() {
	if b == nil || b.cam == nil {
		return
	}
	mask, ok := b.categoryMask("Commander")
	if !ok || mask.IsZero() {
		return
	}
	f, found := b.currentSnapshot()
	if !found {
		return
	}
	var last pool.Handle
	for i := range f.Units {
		v := f.Units[i]
		if b.sess == nil || v.Owner != b.sess.LocalOwner || v.Slot == 0 {
			continue
		}
		if b.inCategory(v, mask) {
			last = v.Slot
		}
	}
	if last != 0 {
		b.cam.SetTracked(last)
	}
}

// selfDestructSelection is Ctrl+D. Retail resolves the SELFDESTRUCT order
// descriptor, fires the button's script path for every selected unit that
// carries that button, and otherwise issues the order through the order
// dispatcher for the selection [07 R-CAM-01 §2]. The palette's button is not
// wired here, so the order path runs for the whole selection; the descriptor
// is the front-segment `SelfDestructFG`, which [04 R-ORD-01 §2] names as the
// button's own.
func (b *battleSession) selfDestructSelection() {
	handles := b.selectedHandlesInSlotOrder()
	if len(handles) == 0 {
		return
	}
	_ = b.enqueueHumanCommand(session.HumanCommand{
		Kind:         session.HumanSelfDestruct,
		SelfDestruct: session.HumanSelfDestructCommand{Handles: handles},
	})
}

// onScreenUnit reports whether a committed unit projects inside the battle
// viewport — the on-screen list Ctrl+S selects from and the `n` cycle marks
// visited [07 R-CAM-01 §2][07 §8].
func (b *battleSession) onScreenUnit(v frame.UnitView) bool {
	if b == nil || b.cam == nil {
		return false
	}
	sx, sy := b.cam.WorldToScreen(v.X, v.Y, v.Z)
	w, h := b.cam.BattleView()
	sx -= camera.OriginX
	sy -= camera.OriginY
	return sx >= 0 && sy >= 0 && sx < w && sy < h
}

// cycleFollowTarget is `t` / `T`: the tracked object becomes the next (or, with
// Shift, the previous) selected unit after the current tracked object in
// unit-slot order, wrapping; with nothing selected it becomes null
// [07 R-CAM-01 §2][07 R-CAM-01 §12].
func (b *battleSession) cycleFollowTarget(previous bool) {
	if b == nil || b.cam == nil {
		return
	}
	sel := b.selectedHandlesInSlotOrder()
	if len(sel) == 0 {
		b.cam.SetTracked(0)
		return
	}
	current := b.cam.Tracked()
	idx := -1
	for i, h := range sel {
		if h == current {
			idx = i
			break
		}
	}
	var next pool.Handle
	switch {
	case idx < 0 && previous:
		next = sel[len(sel)-1]
	case idx < 0:
		next = sel[0]
	case previous:
		next = sel[(idx+len(sel)-1)%len(sel)]
	default:
		next = sel[(idx+1)%len(sel)]
	}
	b.cam.SetTracked(next)
}

// cycleNextUnvisitedUnit is `n`: find the next own unit this cycle has not
// visited, glide the camera to it, record it as the current unit, and mark it
// and every on-screen own unit visited. When every unit has been visited the
// visited set is cleared and the cycle restarts. It does not change the
// selection [07 R-CAM-01 §2][07 R-CAM-01 §12].
func (b *battleSession) cycleNextUnvisitedUnit() {
	f, ok := b.currentSnapshot()
	if !ok || b.cam == nil || b.sess == nil {
		return
	}
	pick, found := b.firstUnvisitedOwnUnit(f)
	if !found {
		b.visitedUnits = nil
		pick, found = b.firstUnvisitedOwnUnit(f)
	}
	if !found {
		return
	}
	b.glideToUnit(pick)
	b.currentUnit = pick.Slot
	b.markVisited(pick.Slot)
	for i := range f.Units {
		v := f.Units[i]
		if v.Slot != 0 && v.Owner == b.sess.LocalOwner && b.onScreenUnit(v) {
			b.markVisited(v.Slot)
		}
	}
}

func (b *battleSession) firstUnvisitedOwnUnit(f *frame.Frame) (frame.UnitView, bool) {
	for i := range f.Units {
		v := f.Units[i]
		if v.Slot == 0 || b.sess == nil || v.Owner != b.sess.LocalOwner {
			continue
		}
		if b.visitedUnits[v.Slot] {
			continue
		}
		return v, true
	}
	return frame.UnitView{}, false
}

func (b *battleSession) markVisited(h pool.Handle) {
	if b.visitedUnits == nil {
		b.visitedUnits = make(map[pool.Handle]bool)
	}
	b.visitedUnits[h] = true
}

// glideToUnit writes the desired origin from a unit position through the one
// conversion of [07 R-CAM-01 §12]: the map-pixel X and the height-sheared Z,
// recentred on the battle viewport.
func (b *battleSession) glideToUnit(v frame.UnitView) {
	if b.cam == nil {
		return
	}
	x := radarMapPixel(v.X)
	y := radarMapPixel(v.Y)
	z := radarMapPixel(v.Z)
	ox, oz := b.cam.BattleViewCenterOrigin(x, z-y/2)
	b.cam.GlideTo(ox, oz)
}

// glideToMessageSource is F3 [07 R-CAM-01 §14 "F3's leading clear is a
// different bit from the visited bit"]:
//
//  1. clear bit 0x20 on every one of the thirty message-ring records;
//  2. scan from the display index toward the producer index, wrapping at 30 —
//     oldest displayed message first — for a record whose source unit id is
//     nonzero, whose bit 0x10 is clear and whose unit is alive; on a hit set
//     both bits and glide to that unit;
//  3. if the scan finds nothing, clear bit 0x10 on every record and scan once
//     more.
//
// The leading clear and the scan's test are different bits, so "not yet
// visited" is a real test and the retry runs exactly when every live-source
// message has been visited. This site used to clear the bit the scan tests,
// which made both the test and the retry arm dead.
//
// The glide reads the unit's map-pixel X/Z words directly and subtracts the
// half viewport without the height shear [07 R-CAM-01 §12].
func (b *battleSession) glideToMessageSource() {
	ring := b.messageRing()
	if ring == nil || b.cam == nil {
		return
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	alive := func(h pool.Handle) bool {
		v, found := snapshotUnitByHandle(f, h)
		return found && v.Slot != 0
	}
	ring.ClearJumped()
	src, found := ring.NextUnvisitedSource(alive)
	if !found {
		ring.ClearVisited()
		src, found = ring.NextUnvisitedSource(alive)
	}
	if !found {
		return
	}
	v, ok := snapshotUnitByHandle(f, src)
	if !ok {
		return
	}
	ox, oz := b.cam.BattleViewCenterOrigin(radarMapPixel(v.X), radarMapPixel(v.Z))
	b.cam.GlideTo(ox, oz)
}

// openUnitInfo is F1. Retail opens `UNITINFOx.GUI` for the hovered unit, or
// for a hovered build button's product [07 R-CAM-01 §2][07 R-HUD-03 §8].
// cmd/nanolathe/unitinfo.go owns the screen and its close.
func (b *battleSession) openUnitInfo() { b.toggleUnitInfo() }

// stepFollowCamera is the follow half of phase 10 [01 §4.4][07 R-CAM-01 §12]:
// a live tracked object recomputes the desired origin every pass and the
// current origin closes the gap; a tracked object that has died clears the
// follow triple; with no tracked object a glide left by `n` or F3 continues.
func (b *battleSession) stepFollowCamera() {
	if b == nil || b.cam == nil {
		return
	}
	tracked := b.cam.Tracked()
	if tracked == 0 {
		b.cam.StepGlide()
		return
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	v, found := snapshotUnitByHandle(f, tracked)
	if !found || v.Slot == 0 {
		b.cam.ClearFollow()
		return
	}
	b.cam.FollowTo(camera.TargetPoint{
		X: radarMapPixel(v.X),
		Y: radarMapPixel(v.Y),
		Z: radarMapPixel(v.Z),
	})
	b.cam.Clamp()
}
