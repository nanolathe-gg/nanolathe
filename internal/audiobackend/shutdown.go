package audiobackend

import "time"

// shutdownDrain is a host grace period, not a retail timer. Oto smooths a
// volume change over its next mixed buffer. At our 44100 Hz rate its macOS
// queue holds about 139 ms, with about 35 ms for that final gain ramp; 200 ms
// leaves a margin before stopping the sources (DESIGN_PRESENTATION_CLIENT §2.6).
const shutdownDrain = 200 * time.Millisecond

// Shutdown silences every output before releasing it at process exit. Close
// remains immediate for ordinary resource disposal and battle transitions.
func (b *Backend) Shutdown() { b.shutdown(time.Sleep) }

func (b *Backend) shutdown(wait func(time.Duration)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shutdownDone {
		return
	}
	b.master = false
	// Even a stopped player can have audible PCM already queued in the
	// device. IsPlaying only describes the source/mixer's lifetime.
	needDrain := b.ctx != nil
	mute := func(player outputPlayer) {
		if player != nil {
			needDrain = true
			player.SetVolume(0)
		}
	}
	for _, voices := range [][]voice{b.players, b.streams, b.untracked} {
		for _, v := range voices {
			mute(v.player)
		}
	}
	for _, group := range b.statics {
		for _, instance := range group.instances {
			mute(instance.player)
		}
	}
	for _, music := range b.music {
		needDrain = true
		music.SetVolume(0)
	}
	if needDrain {
		// Keep sources alive while the mixer applies zero gain and the device
		// consumes queued sound. An unopened backend needs no wait.
		wait(shutdownDrain)
	}
	b.closeLocked()
	b.shutdownDone = true
}
