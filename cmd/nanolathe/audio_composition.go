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
	cl.SetAudioService(sess.Audio)
	cl.SetPresentationCRT(sess.PresentationCRT())

	// The viewport is refreshed from the camera every frame by Client.Frame;
	// seed it once here so a cue queued before the first rendered frame still
	// pans against a real viewport rather than the zero value.
	cl.UpdateAudioViewportFromCamera()
}

// detachBattleAudio releases the device and stops music when the battle view
// closes. The queue, cache and controller stay owned by the audio service.
func detachBattleAudio(cl *client.Client, sess *session.Session) {
	if sess != nil && sess.Audio != nil {
		sess.Audio.Close()
	}
	if cl == nil {
		return
	}
	if output := audio.GlobalOutput(); output != nil {
		if be, ok := output.(*audio.Backend); ok {
			be.Close()
		}
		audio.SetGlobalOutput(nil)
	}
}
