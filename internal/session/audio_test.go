package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestInitAudioSecondAttachPreservesMusicState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "music"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "music", "track.wav"), []byte{1}, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	s := &Session{}
	s.InitAudio(fs)
	if s.Audio == nil || s.Audio.Music.NumTracks() != 1 || !s.Audio.Music.Play(1) {
		t.Fatal("first audio attach did not configure music")
	}
	s.Audio.Music.SetPosition(1234)
	s.InitAudio(fs)
	if s.Audio.Music.NumTracks() != 1 || s.Audio.Music.CurTrack() != 1 ||
		s.Audio.Music.Status() != 1 || s.Audio.Music.Position() != 1234 {
		t.Fatalf("second audio attach reset music state: tracks=%d track=%d status=%d pos=%d",
			s.Audio.Music.NumTracks(), s.Audio.Music.CurTrack(), s.Audio.Music.Status(), s.Audio.Music.Position())
	}
}

func TestAdmittedAudioSurvivesAudienceChangeBeforeDrain(t *testing.T) {
	s := &Session{
		Audio:       audio.NewService(nil),
		Clock:       &clock.State{GlobalTick: 7},
		Vis:         visibility.New(&world.Terrain{CellW: 2, CellH: 2}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled),
		publication: newPublicationState(frame.NewEventBuffer(frame.Limits{})),
	}
	cache := s.Audio.Cache
	if _, err := cache.Put("shot", []byte{128, 129}); err != nil {
		t.Fatal(err)
	}
	s.Audio.Registry.SetCache(cache)
	s.Vis.ByteGrid(0)[0] = 1
	if _, _, ok := s.EmitWeaponHit("shot", [3]numeric.Fixed{}, false); !ok {
		t.Fatal("audible event was not admitted")
	}
	s.Vis.ByteGrid(0)[0] = 0
	old := audio.GlobalBackend()
	b := audio.NewBackend(true)
	audio.SetGlobalBackend(b)
	t.Cleanup(func() { audio.SetGlobalBackend(old) })
	s.Audio.DrainEvents(30, 7, s.publication.events.SnapshotEvents())
	if b.PlayCount() != 1 {
		t.Fatalf("admitted event was suppressed after audience changed: plays=%d", b.PlayCount())
	}
}
