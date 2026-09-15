package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Authored opening policy (GPU §36): input and the authoritative clock cannot
// advance during the opening, and its elapsed time cannot become tick debt.
func TestArrivalHoldsGameplayAndRebasesHandoff(t *testing.T) {
	cl := &client.Client{}
	cl.SetFocused(true)
	cl.StartArrival(frame.UnitView{Slot: 1})
	state := &clock.State{GlobalTick: 7, ScaledAnchor: 10, Requested: 10, Active: 10}
	b := &battleSession{sess: &session.Session{Clock: state}, millisSource: &scriptedMillisSource{samples: []uint32{10000}}}
	before := *state
	for i := 0; i < 120; i++ {
		b.viewerStep(1.0/60, cl)
	}
	if cl.ArrivalSeconds() != 0 || *state != before {
		t.Fatal("window startup consumed the opening")
	}
	cl.MarkArrivalPresented()
	for i := 0; i < 30; i++ {
		b.viewerStep(1.0/60, cl)
	}
	if *state != before {
		t.Fatal("intro advanced authoritative clock")
	}
	if !cl.ArrivalActive() {
		t.Fatal("intro ended before impact")
	}
	for i := 0; i < 200 && cl.ArrivalActive(); i++ {
		b.viewerStep(1.0/60, cl)
	}
	if cl.ArrivalActive() || state.GlobalTick != before.GlobalTick {
		t.Fatal("handoff advanced simulation or never completed")
	}
	if state.ScaledAnchor != 300 {
		t.Fatalf("handoff anchor = %d", state.ScaledAnchor)
	}
	if ticks := state.AdvanceSP(301); ticks != 1 {
		t.Fatalf("first gameplay budget = %d; intro produced catch-up debt", ticks)
	}
}

func TestArrivalImpactSoundPlaysOnceAndSkipStaysSilent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sounds"), 0700); err != nil {
		t.Fatal(err)
	}
	// Authored PCM, not copied game bytes.
	if err := os.WriteFile(filepath.Join(root, "sounds", "xplosml3.wav"), []byte{128, 140, 116, 128}, 0600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	old := audio.GlobalOutput()
	defer audio.SetGlobalOutput(old)
	spy := &cueGainSpy{}
	audio.SetGlobalOutput(spy)
	b := &battleSession{sess: &session.Session{Audio: audio.NewService(fs)}}
	cl, err := client.New(client.Options{Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetFocused(true)
	cl.StartArrival(frame.UnitView{Slot: 1})
	cl.MarkArrivalPresented()
	cl.SetArrivalSeconds(drawlist.ArrivalImpactSeconds - 0.02)
	b.stepArrival(0.01, cl)
	if len(spy.gains) != 0 {
		t.Fatal("impact sounded before contact")
	}
	b.stepArrival(0.02, cl)
	for i := 0; i < 15; i++ {
		b.stepArrival(0.02, cl)
	}
	if len(spy.gains) != 1 || spy.gains[0] >= audio.VolumeFromCentibel(audio.VolInView) || spy.gains[0] <= 0 {
		t.Fatalf("impact plays/gain: %v", spy.gains)
	}
	cl.StartArrival(frame.UnitView{Slot: 1})
	cl.MarkArrivalPresented()
	cl.Input().Kbd.SetKey(input.KeyEscape, true)
	b.stepArrival(0.02, cl)
	if len(spy.gains) != 1 {
		t.Fatal("skipping intro replayed impact")
	}
}

func TestArrivalEntryDistinguishesFreshMissionAndSavedBattle(t *testing.T) {
	cl, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cam := &camera.Camera{X: 400, Z: 700, ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	cl.SetCamera(cam)
	cl.SetFocused(true)
	buffer := frame.NewBuffer()
	buffer.BeginWrite().Units = []frame.UnitView{{Slot: 1, DefName: "commander", Owner: 0}}
	if err := buffer.Publish(0); err != nil {
		t.Fatal(err)
	}
	cl.SetSnapshot(buffer)
	b := &battleSession{cam: cam, cat: &content.Catalog{Units: map[string]*content.UnitDef{"commander": {Commander: true}}}, sess: &session.Session{Snapshot: buffer, Clock: &clock.State{}, Mission: &mission.Mission{Type: mission.TypeCampaign}}}
	opts := Options{Arrival: true, Renderer: "modern"}
	b.beginBattleArrival(opts, cl, false)
	if !cl.ArrivalActive() || !cl.ArrivalHasDrop() {
		t.Fatal("fresh mission omitted commander arrival")
	}
	// A loaded tick-zero save is still a save. Entry source, not tick count,
	// chooses reveal-only, and no frame is republished or camera recentered.
	beforeCamera, beforeFrame := *cam, buffer.Current()
	b.beginBattleArrival(opts, cl, true)
	if !cl.ArrivalActive() || cl.ArrivalHasDrop() || *cam != beforeCamera || buffer.Current() != beforeFrame {
		t.Fatal("save entry changed camera/frame or selected drop")
	}
	cl.MarkArrivalPresented()
	b.sess.Clock.GlobalTick = 800
	b.millisSource = &scriptedMillisSource{samples: []uint32{10000}}
	for i := 0; i < 100 && cl.ArrivalActive(); i++ {
		b.stepArrival(0.02, cl)
	}
	if cl.ArrivalActive() || b.sess.Clock.GlobalTick != 800 || b.sess.Clock.ScaledAnchor != 300 {
		t.Fatal("save reveal advanced simulation or did not hand off")
	}
	delete(b.cat.Units, "commander")
	b.sess.Clock.GlobalTick = 0
	b.beginBattleArrival(opts, cl, false)
	if !cl.ArrivalActive() || cl.ArrivalHasDrop() {
		t.Fatal("commander-less mission did not reveal scene")
	}
}

func TestArrivalDefaultsDoNotAnimateOrdinaryCaptures(t *testing.T) {
	for _, args := range [][]string{nil, {"--shot=frame.png"}, {"--shot=frame.png", "--renderer=classic"}, {"--shot=frame.png", "--shot-model=armsolar"}} {
		opts, err := parseFlags(args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if !opts.Arrival || opts.ShotArrivalTime != -1 {
			t.Fatal("opening default or capture sentinel changed")
		}
		if opts.Shot != "" {
			if err := validateShotOptions(opts); err != nil {
				t.Fatal(err)
			}
		}
	}
	opts, err := parseFlags([]string{"--arrival=false"}, io.Discard)
	if err != nil || opts.Arrival {
		t.Fatal("opening opt-out ignored")
	}
}
