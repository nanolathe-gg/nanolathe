package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type compositionAudioSpy struct {
	aliases []string
}

func (s *compositionAudioSpy) PlaySample(sample *audio.Sample, _ float64, _ float64) error {
	if sample != nil {
		s.aliases = append(s.aliases, sample.Alias)
	}
	return nil
}

// drainWindowFrames mirrors the audio queue's one-voice-per-30-frames window
// [03 §8.3] C18. It is duplicated here rather than exported so the contract
// stays owned by the audio package.
const drainWindowFrames = 30

// Mode-1 voices resolve filenames through VFS rather than registered aliases
// [03 R-AUD-01 §1]. These authored bytes are raw 8-bit mono PCM [fmt wav].
func authoredVoiceFS(t *testing.T, name string) *vfs.FS {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sounds"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sounds", name+".wav"), bytes.Repeat([]byte{0x80}, 64), 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

// TestAttachBattleAudio_CueReachesBackend proves the production composition
// step joins the session's audio state to the client, using a presentation
// output observer as the observation point.
//
// The composition step must bind the concrete service before the client
// drains it, so an ordinary acknowledgement reaches playback in the windowed
// battle. This test fails if that seam is unwired again.
func TestAttachBattleAudio_CueReachesBackend(t *testing.T) {
	prev := audio.GlobalOutput()
	t.Cleanup(func() { audio.SetGlobalOutput(prev) })

	rec := &compositionAudioSpy{}
	audio.SetGlobalOutput(rec)

	cat := testCatalogON05()
	authorTestSoundCategory(cat, "armcons")
	b := newTestBattle(cat, testWorldON05(40, 40))
	commander := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))

	cl, err := client.New(client.Options{
		Buffer: b.sess.Snapshot,
		Width:  640,
		Height: 480,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(b.cam)

	attachBattleAudio(cl, b.sess, authoredVoiceFS(t, "ok1"))

	if b.sess.Audio == nil || b.sess.Audio.Queue == nil || b.sess.Audio.Cache == nil || b.sess.Audio.Music == nil {
		t.Fatalf("session audio service was not initialized")
	}
	if audio.GlobalOutput() != rec {
		t.Fatalf("composition replaced the presentation output")
	}

	// At most one voice is audible per 30 ticks [03 §8.3] C18, so advance the
	// committed tick past the opening window on an empty queue first —
	// otherwise the single cue is resolved silently on the first drain. The
	// window is measured on the global tick counter the producers stamp their
	// inserts with, not on a presentation frame count [03 §8.3][R-AUD-01 §3],
	// so the session has to advance for the window to clear; ticking the
	// client alone leaves the committed tick at zero forever.
	for i := int32(1); i <= drainWindowFrames+1; i++ {
		b.sess.Step(i)
		cl.TickAudio()
	}

	// An ordinary order acknowledgement, emitted the way the session emits it.
	if !b.sess.EmitOK(commander.Handle) {
		t.Fatalf("EmitOK was refused; the queue rejected an ordinary acknowledgement")
	}
	if b.sess.Audio.Queue.Count == 0 {
		t.Fatalf("acknowledgement did not reach the session queue")
	}

	// Drain the way the rendered frame does. The client owns the drain; the
	// test does not call Queue.Drain itself.
	before := len(rec.aliases)
	tickBeforeDrain := b.sess.Clock.GlobalTick
	cl.TickAudio()
	if len(rec.aliases) == before {
		t.Fatalf("queued acknowledgement never reached the backend; "+
			"queue count %d, aliases %v", b.sess.Audio.Queue.Count, rec.aliases)
	}
	if got := rec.aliases; len(got) == 0 || got[len(got)-1] != "ok1" {
		t.Errorf("backend received %v, want the authored ok1 variant last", got)
	}

	// Simulation state must be untouched by the presentation drain [I6]. The
	// clock is no longer zero here — clearing the opening window above is what
	// advanced it — so the invariant is asserted against the tick captured
	// before the drain rather than against a literal.
	if b.sess.Clock.GlobalTick != tickBeforeDrain {
		t.Errorf("audio drain advanced the simulation clock from %d to %d",
			tickBeforeDrain, b.sess.Clock.GlobalTick)
	}
}

// TestDetachBattleAudio_StopsBattleAudioAndKeepsTheDevice locks that leaving
// the battle stops this battle's music and voices while the process-wide PCM
// device stays installed.
//
// Detach used to uninstall the device, which is why the client re-created one
// lazily the next time audio was bound — the path that pulled the concrete
// device package into a client that otherwise touches no hardware. The device
// is installed once by the platform adapter and outlives any one battle
// [03 §8.1][I6].
func TestDetachBattleAudio_StopsBattleAudioAndKeepsTheDevice(t *testing.T) {
	prev := audio.GlobalOutput()
	t.Cleanup(func() { audio.SetGlobalOutput(prev) })
	audio.SetGlobalOutput(&compositionAudioSpy{})

	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	cl.SetCamera(b.cam)
	attachBattleAudio(cl, b.sess, nil)

	detachBattleAudio(cl, b.sess)

	if audio.GlobalOutput() == nil {
		t.Errorf("detach uninstalled the process-wide audio device")
	}
	if b.sess.Audio.Music.IsPlaying() {
		t.Errorf("detach left music playing")
	}
}

// TestNewGameShellPreloadsFrontendAudio locks WU-19-224's fix: the frontend
// audio owner and its alias registry exist as soon as the shell is
// constructed, before any frame is drawn or any interface click is handled.
//
// Retail finishes registering (and probe-decoding) every allsound.tdf alias
// as part of session bring-up, before the shell opens [03 R-AUD-01 §1 mode
// 0][03 §8.3 "Alias registration"] — the same timing as the two startup FNT
// loads [03 R-FONT-01 §5]. Before this fix, ensureFrontendAudio only ran
// lazily on the first playMenuCue call, so the first interface click paid for
// constructing the service and decoding the whole alias table; a play-test
// observed that as visible latency before the first click's sound played.
// Skipped when the retail assets are not opted in.
func TestNewGameShellPreloadsFrontendAudio(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()

	shell, err := newGameShell(Options{Root: root}, cs)
	if err != nil {
		t.Fatalf("newGameShell: %v", err)
	}

	if shell.audioOwner == nil {
		t.Fatal("newGameShell returned with no frontend audio owner; registration is still deferred to the first click")
	}
	if shell.audioOwner.Registry == nil || shell.audioOwner.Registry.Count() == 0 {
		t.Fatalf("frontend audio owner's alias registry is empty at construction; got %d aliases, want the allsound.tdf table already registered", shell.audioOwner.Registry.Count())
	}
	if !shell.frontendAliasesBound {
		t.Error("frontendAliasesBound is false after newGameShell; the alias bind was expected to happen at construction, not on first cue")
	}
	// A representative interface cue must already resolve to a real sample —
	// not just an occupied slot — the same assertion TestFrontendCueAliasesResolve
	// makes against the lazy path.
	if sample, err := shell.audioOwner.Load("BigButton"); err != nil || sample == nil {
		t.Errorf("BigButton does not resolve immediately after construction (%v) [07 R-FE-01 §2]", err)
	}
}

// authorTestSoundCategory gives a unit definition an authored sound category
// with an OK variant, so the queue's resolver produces a real alias. The
// fixture is authored by us; no retail bytes are involved.
func authorTestSoundCategory(cat *content.Catalog, unitName string) {
	const category = "TESTVOICE"
	key := content.CanonicalKey(category)
	sc := &content.SoundCategory{Name: category}
	sc.CanonicalKey = key
	sc.DefinitionHeader.CanonicalKey = key
	sc.Slots[audio.SlotOK].Variants = []string{"ok1"}
	sc.Slots[audio.SlotOK].Captions = []string{"Affirmative"}
	if cat.Sounds == nil {
		cat.Sounds = map[string]*content.SoundCategory{}
	}
	cat.Sounds[key] = sc
	if def, ok := cat.Unit(unitName); ok && def != nil {
		def.SoundCategory = category
	}
}
