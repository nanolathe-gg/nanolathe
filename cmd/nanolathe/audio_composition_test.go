package main

import (
	"bytes"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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

	attachBattleAudio(cl, b.sess, nil)

	if b.sess.Audio == nil || b.sess.Audio.Queue == nil || b.sess.Audio.Cache == nil || b.sess.Audio.Music == nil {
		t.Fatalf("session audio service was not initialized")
	}
	if audio.GlobalOutput() != rec {
		t.Fatalf("composition replaced the presentation output")
	}
	// The play sink resolves the drawn alias through the registry's sample
	// cache and is silent when no sample exists [03 §8.2]. The fixture has no
	// VFS, so author the sample directly: a byte string with no container
	// marker is a raw 8-bit mono sample [fmt wav].
	if _, err := b.sess.Audio.Cache.Put("ok1", bytes.Repeat([]byte{0x80}, 64)); err != nil {
		t.Fatalf("authoring the ok1 sample: %v", err)
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

// TestDetachBattleAudio_ReleasesDevice locks that leaving the battle stops
// music and drops the process-global backend.
func TestDetachBattleAudio_ReleasesDevice(t *testing.T) {
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

	if audio.GlobalOutput() != nil {
		t.Errorf("detach left the global audio backend installed")
	}
	if b.sess.Audio.Music.IsPlaying() {
		t.Errorf("detach left music playing")
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
