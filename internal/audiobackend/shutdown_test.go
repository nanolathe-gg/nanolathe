package audiobackend

import (
	"io"
	"testing"
	"time"
)

type shutdownReader struct{ closes int }

func (*shutdownReader) Read([]byte) (int, error) { return 0, io.EOF }
func (r *shutdownReader) Close() error           { r.closes++; return nil }

// Process shutdown must mute every ownership class before releasing sources.
// Music/movie output bypasses FX gain, and static/untracked output can outlive
// the tracked voice list (DESIGN_PRESENTATION_CLIENT §2.6).
func TestShutdownMutesBeforeDrainAndRelease(t *testing.T) {
	b := New()
	players := make([]*observedPlayer, 6)
	for i := range players {
		players[i] = &observedPlayer{volume: 0.5, playing: true}
	}
	b.players = []voice{{player: players[0], loop: true}}
	b.streams = []voice{{player: players[1]}}
	b.untracked = []voice{{player: players[2]}}
	b.transients = []voice{{player: players[2]}}
	b.statics = []staticSample{{instances: [staticInstances]staticInstance{
		{player: players[0]}, {player: players[3]},
	}}}
	var readers []*shutdownReader
	for _, player := range players[4:] { // music and movie share this owner
		reader := &shutdownReader{}
		readers = append(readers, reader)
		b.music = append(b.music, &musicPlayer{player: player, reader: &musicReader{ReadCloser: reader}})
	}
	waits := 0
	b.shutdown(func(delay time.Duration) {
		waits++
		if delay != shutdownDrain || b.master {
			t.Fatalf("drain=%v master=%v", delay, b.master)
		}
		for _, player := range players {
			if player.volume != 0 || !player.playing || player.stops != 0 {
				t.Fatal("drain started before mute, or a player stopped before drain")
			}
		}
		for _, reader := range readers {
			if reader.closes != 0 {
				t.Fatal("decoder closed before device drain")
			}
		}
	})
	if waits != 1 {
		t.Fatalf("waits=%d, want one", waits)
	}
	for _, player := range players {
		if player.playing || player.stops != 1 {
			t.Fatalf("playing=%v releases=%d, want false/one", player.playing, player.stops)
		}
	}
	for _, reader := range readers {
		if reader.closes != 1 {
			t.Fatalf("decoder closes=%d, want one", reader.closes)
		}
	}
	b.shutdown(func(time.Duration) { t.Fatal("repeated shutdown waited") })
	b.Close()
	for _, player := range players {
		if player.stops != 1 {
			t.Fatal("repeated shutdown released a player twice")
		}
	}
	if len(b.players)+len(b.streams)+len(b.untracked)+len(b.transients)+len(b.statics)+len(b.music) != 0 {
		t.Fatal("shutdown retained output ownership")
	}
}

func TestShutdownUnopenedOutputDoesNotWaitOrOpenDevice(t *testing.T) {
	for _, b := range []*Backend{nil, New()} {
		b.shutdown(func(time.Duration) { t.Fatal("unopened output waited") })
		if b != nil && b.ctx != nil {
			t.Fatal("shutdown opened an audio device")
		}
	}
}

func TestShutdownAllowsStoppedPlayerDeviceTailToDrain(t *testing.T) {
	player := &observedPlayer{}
	b := &Backend{players: []voice{{player: player}}}
	waited := false
	b.shutdown(func(time.Duration) {
		waited = true
		if player.stops != 0 {
			t.Fatal("released before the queued device tail could drain")
		}
	})
	if !waited || player.stops != 1 {
		t.Fatal("stopped player bypassed device drain")
	}
}
