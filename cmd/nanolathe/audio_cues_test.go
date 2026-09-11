package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// TestOrderButtonCueFamilies locks the GUI order-button dispatcher's per-arm
// cue column [07 §9]. The split does not follow the latch numbering: UNLOAD
// (latch 5) is a `specialorders` arm while LOAD (latch 6) beside it is an
// `immediateorders` arm, so a reimplementation that groups by value rather than
// by button name fails here. The names go through the same parser the
// dispatcher uses, so a change to either the parse chain or the split is caught.
func TestOrderButtonCueFamilies(t *testing.T) {
	cases := []struct {
		button string
		want   string
	}{
		{"ARMMOVE", cueImmediateOrders},
		{"ARMATTACK", cueImmediateOrders},
		{"ARMBLAST", cueImmediateOrders},
		{"ARMDEFEND", cueImmediateOrders}, // DEFEND is the FOLLOW/GUARD family
		{"ARMPATROL", cueImmediateOrders},
		{"ARMLOAD", cueImmediateOrders},
		{"ARMREPAIR", cueSpecialOrders},
		{"ARMRECLAIM", cueSpecialOrders},
		{"ARMCAPTURE", cueSpecialOrders},
		{"ARMUNLOAD", cueSpecialOrders},
	}
	for _, c := range cases {
		latch := hud.ParseButtonLatch(c.button, 1)
		if got := orderButtonCue(latch); got != c.want {
			t.Errorf("orderButtonCue(%s -> latch %v) = %q, want %q [07 §9]", c.button, latch, got, c.want)
		}
	}
	// The idle latch is the one value the arm cannot be recovered from: STOP
	// writes it and so does any button whose runtime gate is zero. Its cue is
	// the caller's, so this helper stays silent.
	if got := orderButtonCue(input.LatchNormal); got != "" {
		t.Errorf("orderButtonCue(idle) = %q, want silence", got)
	}
}

// TestCountedBuildCueSign locks the counted factory-queue producer's single
// signed test [07 R-P0-11 §1]: one or more is `addbuild`, anything else is
// `subbuild`. The boundary is the point of the test — a `>= 0` reading would
// make a right-click add.
func TestCountedBuildCueSign(t *testing.T) {
	for _, c := range []struct {
		delta int
		want  string
	}{
		{1, cueAddBuild},
		{5, cueAddBuild},
		{-1, cueSubBuild},
		{-5, cueSubBuild},
		// Not reachable from a click (the count is always ±1 or ±5), but it is
		// the arm retail's signed test takes.
		{0, cueSubBuild},
	} {
		if got := countedBuildCue(c.delta); got != c.want {
			t.Errorf("countedBuildCue(%d) = %q, want %q [07 R-P0-11 §1]", c.delta, got, c.want)
		}
	}
}

// TestFrontendCueTable locks the cue column of the single-player transition
// table [07 R-FE-01 §2] plus `SKIRMISH` `Start`'s own `BigButton`
// [07 R-FE-01 §5]. Gadgets the table lists without a cue must stay silent.
func TestFrontendCueTable(t *testing.T) {
	cases := []struct {
		mode shellMode
		key  string
		want string
	}{
		{ui.ModeMain, "single", "BigButton"},
		{ui.ModeMain, "intro", "smlButton"},
		{ui.ModeMain, "credits", "smlButton"},
		{ui.ModeMain, "multi", ""},
		{ui.ModeMain, "exit", ""},
		{ui.ModeSingle, "newcamp", "BigButton"},
		{ui.ModeSingle, "anymsn", "bigButton"},
		{ui.ModeSingle, "skirmish", "skirmish"},
		{ui.ModeSingle, "loadgame", "BigButton"},
		{ui.ModeSingle, "options", "options"},
		{ui.ModeSingle, "prevmenu", "Previous"},
		{ui.ModeMission, "start", "bigButton"},
		{ui.ModeMission, "prevmenu", "Previous"},
		{ui.ModeMission, "difficulty", "SmlButton"},
		{ui.ModeMission, "side0", "SideSelect"},
		{ui.ModeMission, "side1", "SideSelect2"},
		{ui.ModeMap, "prevmenu", "Previous"},
		{ui.ModeMap, "load", ""},
		{ui.ModeSkirmish, "start", "BigButton"},
		{ui.ModeSkirmish, "prevmenu", ""},
		{ui.ModeSkirmish, "selectmap", ""},
	}
	for _, c := range cases {
		if got := frontendCue(c.mode, c.key); got != c.want {
			t.Errorf("frontendCue(%v, %q) = %q, want %q [07 R-FE-01 §2]", c.mode, c.key, got, c.want)
		}
	}
}

// TestPlaySelectionCueRoutes locks the selection refresh's two paths [07 §9]:
// exactly one selected unit takes the single-unit path, which is that unit's
// own category voice on slot 1 of the static table [03 §8.3], so it enters the
// eight-slot queue; more than one takes the multiple-unit path, which is the
// flat `SelectMultipleUnits` interface alias and never enters the queue.
func TestPlaySelectionCueRoutes(t *testing.T) {
	queuedSlots := func(handles []pool.Handle) []audio.Slot {
		sess := &session.Session{Audio: audio.NewService(nil)}
		playSelectionCue(sess, handles)
		var slots []audio.Slot
		for i := 0; i < sess.Audio.Queue.Count; i++ {
			slots = append(slots, sess.Audio.Queue.Entries[i].Slot)
		}
		return slots
	}
	if got := queuedSlots([]pool.Handle{7}); len(got) != 1 || got[0] != audio.SlotSelect {
		t.Fatalf("single selection queued %v, want one slot-1 select cue [03 §8.3]", got)
	}
	if got := queuedSlots([]pool.Handle{7, 9}); len(got) != 0 {
		t.Fatalf("multiple selection queued %v, want the flat interface alias instead [07 §9]", got)
	}
	if got := queuedSlots(nil); len(got) != 0 {
		t.Fatalf("empty selection queued %v, want neither path [07 §9]", got)
	}
}

// TestFrontendCueAliasesResolve proves the front-end cue names are live: they
// are mode-0 alias registrations [02 "Sound aliases"][R-AUD-01 §1], so without
// the authored alias table they would probe `sounds/<name>.wav`, which no
// install carries, and every menu click would be silently mute. Skipped when
// the retail assets are absent.
func TestFrontendCueAliasesResolve(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()

	g := &gameShell{cs: cs}
	svc := g.ensureFrontendAudio()
	if svc == nil {
		t.Fatal("ensureFrontendAudio returned no service")
	}
	// The battle's own interface cues register through the same RegisterPath
	// seam (Service.BindCatalog walks the catalog's alias order), so they are
	// locked here alongside the front end's.
	aliases := []string{
		"BigButton", "smlButton", "Previous", "options", "skirmish", "SideSelect", "SideSelect2",
		"oktobuild", "notoktobuild", "addbuild", "subbuild", "nextbuildmenu",
		"immediateorders", "specialorders", "setmoveorders", "setfireorders",
		"ordersbutton", "buildbutton",
		"SelectMultipleUnits", "CreateSquad", "SelectSquad", "Panel",
	}
	for _, alias := range aliases {
		sample, err := svc.Load(alias)
		if err != nil || sample == nil {
			t.Errorf("front-end cue %q does not resolve (%v) [07 R-FE-01 §2]", alias, err)
		}
	}
}

// TestUnitCompleteStatusReachesBackend walks the whole "build finished" path
// this unit wired: a build handler raises status kind 8 on the builder
// [04 R-ORD-01 §5], the session stages it as a committed-frame status event,
// the client's rendered-frame drain inserts it into the eight-slot queue, and
// the queue resolves the builder's own `unitcomplete` variant [03 §8.3].
// internal/construction's factory phase 4 is the raise this locks the tail of.
func TestUnitCompleteStatusReachesBackend(t *testing.T) {
	prev := audio.GlobalOutput()
	t.Cleanup(func() { audio.SetGlobalOutput(prev) })
	rec := &compositionAudioSpy{}
	audio.SetGlobalOutput(rec)

	cat := testCatalogON05()
	authorTestSoundCategory(cat, "armcons")
	// The category fixture authors only the `ok` row; the build-finished voice
	// is slot 8 [03 §8.3].
	sc := cat.Sounds[content.CanonicalKey("TESTVOICE")]
	sc.Slots[audio.SlotUnitComplete].Variants = []string{"unitcomplete1"}
	sc.Slots[audio.SlotUnitComplete].Captions = []string{""}

	b := newTestBattle(cat, testWorldON05(40, 40))
	builder := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))

	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(b.cam)
	attachBattleAudio(cl, b.sess, authoredVoiceFS(t, "unitcomplete1"))

	// Clear the opening thirty-frame window on an empty queue [03 §8.3].
	for i := int32(1); i <= drainWindowFrames+1; i++ {
		b.sess.Step(i)
		cl.TickAudio()
	}

	// Publish the committed status event a build handler's raise produces. The
	// fixture session composes no order binding, so the raise itself is locked
	// in internal/construction; what is locked here is everything downstream of
	// it — the committed event, the client's drain, the queue insert and the
	// category resolve.
	before := len(rec.aliases)
	tick := uint32(drainWindowFrames + 2)
	published := b.sess.Snapshot.BeginWrite()
	published.Events = []frame.EventView{{
		Tick:        tick,
		Kind:        frame.EventKindStatus,
		Source:      builder.Handle,
		StatusKind:  8, // `unitcomplete` [04 R-ORD-01 §1][03 §8.3]
		StatusClass: 1,
	}}
	if err := b.sess.Snapshot.Publish(tick); err != nil {
		t.Fatalf("publishing the status frame: %v", err)
	}
	cl.TickAudio()

	if len(rec.aliases) == before {
		t.Fatalf("the build-finished status never reached playback; aliases %v", rec.aliases)
	}
	if got := rec.aliases[len(rec.aliases)-1]; got != "unitcomplete1" {
		t.Errorf("backend received %q last, want the category's unitcomplete variant [03 §8.3]", got)
	}
}
