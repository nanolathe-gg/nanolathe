package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// drainWindowFrames mirrors the audio queue's one-voice-per-30-frames window
// [03 §8.3] C18. It is duplicated here rather than exported so the contract
// stays owned by the audio package.
const drainWindowFrames = 30

// TestAttachBattleAudio_CueReachesBackend proves the production composition
// step actually joins the session's audio state to the client, using a
// recording (headless) backend as the observation point.
//
// Before attachBattleAudio existed, no production path called SetAudioQueue,
// SetAudioCache or SetMusicController: the client drained a queue it had never
// been given, so an ordinary acknowledgement never reached playback in the
// windowed battle. This test fails if that seam is unwired again.
func TestAttachBattleAudio_CueReachesBackend(t *testing.T) {
	prev := audio.GlobalBackend()
	t.Cleanup(func() { audio.SetGlobalBackend(prev) })

	rec := audio.NewBackend(true) // headless: records aliases, constructs no device [I5]
	audio.SetGlobalBackend(rec)

	cat := testCatalogON05()
	authorTestSoundCategory(cat, "armcons")
	b := newTestBattle(cat, testWorldON05(40, 40))
	commander := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))

	cl, err := client.New(client.Options{
		Buffer:   b.sess.Snapshot,
		Width:    640,
		Height:   480,
		Headless: true,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(b.cam)

	attachBattleAudio(cl, b.sess, nil)

	if cl.AudioQueue() == nil || cl.AudioQueue() != b.sess.AudioQueue {
		t.Fatalf("client queue is not the session queue after composition")
	}
	if cl.AudioCache() == nil || cl.AudioCache() != b.sess.AudioCache {
		t.Fatalf("client cache is not the session cache after composition")
	}
	if cl.AudioMusic() == nil || cl.AudioMusic() != b.sess.AudioMusic {
		t.Fatalf("client music controller is not the session controller after composition")
	}
	// The composition must not have replaced the headless recording backend
	// with a device [I5][I6].
	if be := audio.GlobalBackend(); be == nil || !be.IsHeadless() {
		t.Fatalf("headless composition installed a device backend")
	}

	// At most one voice is audible per 30 rendered frames [03 §8.3] C18, so
	// step the client past the opening window on an empty queue first —
	// otherwise the single cue is resolved silently on the first frame.
	for i := 0; i < drainWindowFrames+1; i++ {
		cl.TickAudio()
	}

	// An ordinary order acknowledgement, emitted the way the session emits it.
	if !b.sess.EmitOK(commander.Handle) {
		t.Fatalf("EmitOK was refused; the queue rejected an ordinary acknowledgement")
	}
	if b.sess.AudioQueue.Count == 0 {
		t.Fatalf("acknowledgement did not reach the session queue")
	}

	// Drain the way the rendered frame does. The client owns the drain; the
	// test does not call Queue.Drain itself.
	before := rec.PlayCount()
	cl.TickAudio()
	if rec.PlayCount() == before {
		t.Fatalf("queued acknowledgement never reached the backend; "+
			"queue count %d, aliases %v", b.sess.AudioQueue.Count, rec.PlayedAliases())
	}
	if got := rec.PlayedAliases(); len(got) == 0 || got[len(got)-1] != "ok1" {
		t.Errorf("backend received %v, want the authored ok1 variant last", got)
	}

	// Simulation state must be untouched by the presentation drain [I6].
	if b.sess.Clock.GlobalTick != 0 {
		t.Errorf("audio drain advanced the simulation clock to %d", b.sess.Clock.GlobalTick)
	}
}

// TestAttachBattleAudio_UnwiredClientPlaysNothing is the control: without the
// composition step the same cue reaches the session queue and stops there.
// It documents exactly what was broken, so a regression is legible.
func TestAttachBattleAudio_UnwiredClientPlaysNothing(t *testing.T) {
	prev := audio.GlobalBackend()
	t.Cleanup(func() { audio.SetGlobalBackend(prev) })
	rec := audio.NewBackend(true)
	audio.SetGlobalBackend(rec)

	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	commander := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	b.sess.InitAudio(nil)

	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480, Headless: true})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(b.cam)
	// Deliberately no attachBattleAudio.

	if cl.AudioQueue() != nil {
		t.Fatalf("client already holds a queue without the composition step")
	}
	b.sess.EmitOK(commander.Handle)
	before := rec.PlayCount()
	for i := 0; i < 40; i++ {
		cl.TickAudio()
	}
	if rec.PlayCount() != before {
		t.Fatalf("unwired client played audio; the control case no longer isolates the seam")
	}
}

// TestDetachBattleAudio_ReleasesDevice locks that leaving the battle stops
// music and drops the process-global backend.
func TestDetachBattleAudio_ReleasesDevice(t *testing.T) {
	prev := audio.GlobalBackend()
	t.Cleanup(func() { audio.SetGlobalBackend(prev) })
	audio.SetGlobalBackend(audio.NewBackend(true))

	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480, Headless: true})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	cl.SetCamera(b.cam)
	attachBattleAudio(cl, b.sess, nil)

	detachBattleAudio(cl, b.sess)

	if audio.GlobalBackend() != nil {
		t.Errorf("detach left the global audio backend installed")
	}
	if b.sess.AudioMusic.IsPlaying() {
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
