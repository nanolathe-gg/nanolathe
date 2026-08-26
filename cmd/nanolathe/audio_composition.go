package main

import (
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/vfs"
)

// attachBattleAudio is the single composition step that joins the session's
// audio state to the battle client [03 §8.2][03 §8.3][03 §8.4].
//
// The session owns the event queue, sample cache and music controller so
// simulation events can be queued without a client import cycle; the client
// owns the device and drains the queue once per rendered frame outside the
// tick. Both halves existed, but nothing joined them: no production path
// called SetAudioQueue, SetAudioCache or SetMusicController, so Client.Frame
// drained a queue it had never been given and no ordinary acknowledgement or
// weapon cue could reach playback in the windowed battle.
//
// Everything here is presentation-only [I6]: no simulation state is read or
// written, and a headless client never gets a device [I5].
func attachBattleAudio(cl *client.Client, sess *session.Session, fs vfs.FSOps) {
	if cl == nil || sess == nil {
		return
	}
	// The session builds queue/cache/music lazily; make sure they exist before
	// they are handed over, and give the cache the mounted content FS so alias
	// resolution can reach retail samples.
	sess.InitAudio(fs)

	cl.SetAudioQueue(sess.AudioQueue)
	cl.SetAudioCache(sess.AudioCache)
	cl.SetMusicController(sess.AudioMusic)

	// The viewport is refreshed from the camera every frame by Client.Frame;
	// seed it once here so a cue queued before the first rendered frame still
	// pans against a real viewport rather than the zero value.
	cl.UpdateAudioViewportFromCamera()
}

// detachBattleAudio releases the device and stops music when the battle view
// closes. The queue, cache and controller stay owned by the session.
func detachBattleAudio(cl *client.Client, sess *session.Session) {
	if sess != nil && sess.AudioMusic != nil {
		sess.AudioMusic.Stop()
		sess.AudioMusic.Close()
	}
	if cl == nil {
		return
	}
	if be := audio.GlobalBackend(); be != nil {
		be.Close()
		audio.SetGlobalBackend(nil)
	}
}
