package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/audiobackend"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
	"time"
)

// attachBattleAudio is the single composition step that joins the session's
// audio state to the battle client [03 §8.2][03 §8.3][03 §8.4].
//
// The audio service owns the event queue, sample cache and music controller so
// simulation events can be queued without a client import cycle; the client
// binds that service and drains it once per rendered frame outside the tick.
//
// Everything here is presentation-only [I6]: no simulation state is read or
// written. The client owns the platform audio device at the presentation
// boundary [03 §8.1].
func attachBattleAudio(cl *client.Client, sess *session.Session, fs vfs.FSOps) {
	if cl == nil || sess == nil {
		return
	}
	// The session's audio service owns queue/cache/music; the client receives
	// only that owner and drains it at the presentation boundary.
	sess.InitAudio(fs)
	bindMusicClock(sess.Audio)
	cl.SetAudioService(sess.Audio)
	cl.SetPresentationCRT(sess.PresentationCRT())

	// The viewport is refreshed from the camera every frame by Client.Frame;
	// seed it once here so a cue queued before the first rendered frame still
	// pans against a real viewport rather than the zero value.
	cl.UpdateAudioViewportFromCamera()
}

// detachBattleAudio stops the battle's voices and music when the battle view
// closes. The queue, cache and controller stay owned by the audio service.
//
// The PCM device itself is not uninstalled. It is a process resource that the
// platform adapter installs once; tearing it down here is what previously
// forced the client to re-create one lazily on the next battle. Backend.Close
// stops every live voice and leaves the backend reusable.
func detachBattleAudio(cl *client.Client, sess *session.Session) {
	if sess != nil && sess.Audio != nil {
		sess.Audio.Close()
	}
	if cl == nil {
		return
	}
	if be, ok := audio.GlobalOutput().(*audiobackend.Backend); ok {
		be.Close()
	}
}

// ---------------------------------------------------------------------------
// Interface cues.
//
// [07 R-WGT-01 §3] is a bounded negative: no widget handler and no painter
// plays a sound. The alias names below are what the per-screen *fired*
// callbacks hand to the interface cue player when they act on a result, so a
// reimplementation plays the cue in the screen handler that consumes the
// result — which is where every helper here is called from. The aliases are
// ordinary authored sound aliases [02 "Sound aliases"], resolved through the
// same registry as every other alias, not a separate UI audio path.

// frontendCue is the single-player transition table's cue column
// [07 R-FE-01 §2]. It returns the alias the fired callback plays before the
// transition, or "" for a gadget the table lists without one.
//
// The table's case is reproduced as authored even though alias lookup is
// case-insensitive [02 "Sound aliases"], so the cue column stays diffable
// against the section.
func frontendCue(mode shellMode, key string) string {
	switch mode {
	case ui.ModeMain:
		switch key {
		case "single":
			return "BigButton"
		case "intro", "credits":
			return "smlButton"
		}
		// `MULTI` and `EXIT` are listed with no cue [07 R-FE-01 §2].
	case ui.ModeSingle:
		switch key {
		case "newcamp":
			return "BigButton"
		case "anymsn":
			return "bigButton"
		case "skirmish":
			return "skirmish"
		case "options":
			return "options"
		case "prevmenu":
			return "Previous"
		case "loadgame":
			// `LoadGame` shares `NewCamp`'s alias: the `SINGLE` callback's
			// third arm plays `BigButton` before opening the load dialog
			// [07 R-FE-01 §2].
			return "BigButton"
		}
	case ui.ModeMission:
		switch key {
		case "start":
			return "bigButton"
		case "prevmenu":
			return "Previous"
		case "difficulty":
			return "SmlButton"
		case "side0":
			return "SideSelect"
		case "side1":
			return "SideSelect2"
		}
	case ui.ModeMap:
		if key == "prevmenu" {
			return "Previous"
		}
		// `SELMAP`'s `LOAD`/`MAPNAMES` row carries no cue [07 R-FE-01 §2].
	case ui.ModeSkirmish:
		if key == "start" {
			// `SKIRMISH` `Start` runs "cue `BigButton`" first of all
			// [07 R-FE-01 §5]; the transition table's row omits it.
			return "BigButton"
		}
		// `SKIRMISH`'s `PrevMenu` and `SelectMap` rows carry no cue
		// [07 R-FE-01 §2].
	}
	return ""
}

// ensureFrontendAudio returns the shell's one semantic audio owner, creating it
// on first use and registering the authored alias table it resolves interface
// cue names through. The battle adopts this same owner, so a cue played in the
// front end and one played in battle share one registry and one sample cache
// [03 R-AUD-02 §1][I6].
func (g *gameShell) ensureFrontendAudio() *audio.Service {
	if g == nil || g.cs == nil || g.cs.fs == nil {
		return nil
	}
	if g.audioOwner == nil {
		g.audioOwner = audio.NewService(g.cs.fs)
		bindMusicClock(g.audioOwner)
		g.audioOwner.ConfigureMusic(false)
		g.audioOwner.Music.Configure(audio.PlayMode(g.audioPrefs.CDMode), 4)
		g.audioOwner.Music.SetVolume(g.audioPrefs.MusicVol)
		g.audioOwner.Music.SetEnabled(g.audioPrefs.MusicMode != 0)
	}
	if !g.frontendAliasesBound && g.audioOwner.Registry != nil {
		// Interface cue names are mode-0 alias registrations, so they resolve
		// only once `allsound.tdf`'s sections are registered; without them
		// `BigButton` would probe `sounds/BigButton.wav`, which no install has
		// [02 "Sound aliases"][R-AUD-01 §1]. Registration order is the authored
		// section order, which is what the ordered compile returns.
		if _, ordered, err := content.CompileSoundAliasesOrdered(g.cs.fs); err == nil {
			for _, alias := range ordered {
				if alias != nil {
					g.audioOwner.Registry.RegisterPath(alias.Alias, alias.Sound)
				}
			}
		}
		g.frontendAliasesBound = true
	}
	return g.audioOwner
}

// playMenuCue plays one front-end interface cue. A missing alias or a missing
// sample stays silent, as it does in retail [03 §8.2].
func (g *gameShell) playMenuCue(alias string) {
	if alias == "" {
		return
	}
	if svc := g.ensureFrontendAudio(); svc != nil {
		_ = svc.PlayUICue(alias)
	}
}

const menuBGMAlias = "BGM"

func (g *gameShell) armMenuBGM() {
	if g != nil {
		g.menuBGMPending = true
	}
}

func (g *gameShell) playPendingMenuBGM() {
	if g == nil || !g.menuBGMPending {
		return
	}
	g.menuBGMPending = false
	if svc := g.ensureFrontendAudio(); svc != nil {
		_ = svc.PlayLoopingUICue(menuBGMAlias)
	}
}

func (g *gameShell) stopOrdinaryAudio() {
	if g != nil && g.audioOwner != nil {
		g.audioOwner.StopVoices()
	}
}

// orderButtonCue is the cue the GUI order-button dispatcher plays for the arm
// that matched the button name [07 §9 "The GUI order-button dispatcher"]. The
// per-arm column is Established: MOVE, STOP, ATTACK, BLAST, DEFEND, PATROL and
// LOAD play `immediateorders`; REPAIR, RECLAIM, CAPTURE and UNLOAD play
// `specialorders`. UNLOAD sits with the special family even though its latch
// neighbours LOAD, so the split is not the latch's numeric order.
//
// The cue belongs to the arm, not to the value written: an arm whose runtime
// gate is zero writes the idle latch and still plays its own family's cue. The
// key here is the parsed latch because the caller always parses with a nonzero
// gate, which makes the arm and the latch one-to-one — except for STOP, whose
// arm also writes the idle latch. The STOP cue is therefore the caller's to
// play; see `handleHudOrderButton`.
func orderButtonCue(latch input.Latch) string {
	switch latch {
	case input.LatchMove, input.LatchAttack, input.LatchBlast, input.LatchFollow,
		input.LatchPatrol, input.LatchPickup:
		return cueImmediateOrders
	case input.LatchRepair, input.LatchReclaim, input.LatchCapture, input.LatchUnload:
		return cueSpecialOrders
	}
	// The idle latch is not an arm: it is what a zero gate (and STOP) writes,
	// and the arm that produced it is no longer recoverable from the value.
	return ""
}

// Interface cue aliases named by the sections that own each producer. They are
// authored alias names [02 "Sound aliases"], never file paths.
const (
	cueImmediateOrders = "immediateorders"     // [07 §9] order-button arm
	cueSpecialOrders   = "specialorders"       // [07 §9] order-button arm, on/off and cloak arms
	cueNextBuildMenu   = "nextbuildmenu"       // [07 §9] page switch, [07 R-CAM-01 §2] `,`/`.`
	cueAddBuild        = "addbuild"            // [07 §9], [07 R-P0-11 §1]
	cueSubBuild        = "subbuild"            // [07 R-P0-11 §1]
	cueSelectMultiple  = "SelectMultipleUnits" // [07 §9] selection refresh

	// The save/load dialog's own rows [08 R-SAVE-02 §1].
	cueSaveLoadCommit = "smlbutton"   // the commit arm of either direction
	cueDeleteSaveSlot = "SmallButton" // `DELETE`; a different alias from the above
	cuePreviousScreen = "Previous"    // `CANCEL`, as everywhere else
)

// playSelectionCue is the selection refresh's cue [07 §9]: "one selected unit
// takes the single-unit presentation path and multiple units take the
// multiple-unit path; any change … plays `SelectMultipleUnits` or the single
// select cue." The single select cue is the unit's own category voice, slot 1
// of the static table [03 §8.3], so it goes through the eight-slot queue with
// that slot's priority and cooldown; the multiple-unit cue is a flat interface
// alias. A change that leaves nothing selected reaches neither path.
//
// This runs on the presentation side, at the click that produced the selection
// command, and touches no simulation state [I6].
func playSelectionCue(sess *session.Session, handles []pool.Handle) {
	if sess == nil || sess.Audio == nil {
		return
	}
	live := make([]pool.Handle, 0, len(handles))
	for _, h := range handles {
		if h != 0 {
			live = append(live, h)
		}
	}
	switch len(live) {
	case 0:
		return
	case 1:
		sess.EmitSelect(live[0])
	default:
		_ = sess.Audio.PlayUICue(cueSelectMultiple)
	}
}

// dispatchBuildPageCued is the page-switch routine's cue seam. "Switching sets
// battle-interface dirty bit `0x10` and plays the `nextbuildmenu` cue"
// [07 §9 "Page encoding is closed"], and the routine validates the
// selected-builder identity and the page-count guard first — so a refused
// switch is silent. Pending page commands participate in the old-page
// identity so repeated input before publication only cues actual transitions.
func (b *battleSession) dispatchBuildPageCued(page int) error {
	f, ok := b.currentSnapshot()
	if ok && b.effectiveBuildPage(f) == page {
		return nil
	}
	err := b.DispatchBuildPage(page)
	if err == nil {
		b.playUICue(nil, cueNextBuildMenu)
	}
	return err
}

// countedBuildCue is the cue the counted factory-queue producer plays for one
// signed click count [07 R-P0-11 §1]. The split is a single signed test on the
// count: one or more plays `addbuild` and anything else plays `subbuild`. There
// is no silent arm — the routine's local-player gate is the only thing that
// can suppress the cue, and a click count is always ±1 or ±5, so the zero case
// below is retail's branch and not a state the interface can reach.
func countedBuildCue(delta int) string {
	if delta > 0 {
		return cueAddBuild
	}
	return cueSubBuild
}

// pumpAudio runs from the shell's common presentation step, including menus
// and paused battles. The backend owns the wall-clock media cadence; semantic
// audio state and simulation clocks do not advance here [03 R-AUD-02 §2][I6].
func pumpAudio(now time.Time) {
	if output, ok := audio.GlobalOutput().(interface{ Pump(time.Time) }); ok {
		output.Pump(now)
	}
}
