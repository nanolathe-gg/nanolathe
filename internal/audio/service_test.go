package audio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/vfs"
)

func testAudioFS(t *testing.T, name string) vfs.FSOps {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sounds"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sounds", name+".wav"), buildRIFF(1, 11025, 8, []byte{128, 129}), 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

func TestServiceInitBindsLaterFSWithoutReplacingStateOrPlayback(t *testing.T) {
	s := NewService(nil)
	customCache := NewCache(nil)
	s.Cache = customCache
	var calls int
	s.Queue.OnPlay(func(string, Slot, pool.Handle) { calls++ })
	s.Queue.Register(1, &Category{Rows: [24]Row{{}, {Variants: []string{"VOICE"}}}}, "unit", true)
	if !s.Queue.InsertAt(30, SlotSelect, 1, "") {
		t.Fatal("queue insert rejected before filesystem attach")
	}
	fs := testAudioFS(t, "voice")
	s.Init(fs)
	if s.Cache != customCache {
		t.Fatal("Init replaced injected cache")
	}
	id := s.Registry.RegisterPath("VOICE", "sounds/voice.wav")
	if sample, err := s.Registry.Load(id); err != nil || sample == nil {
		t.Fatalf("later filesystem was not bound: sample=%v err=%v", sample, err)
	}
	s.Drain(30)
	if calls != 1 {
		t.Fatalf("Drain reconfigured playback callback: calls=%d", calls)
	}
}

func TestServiceDrainEventsDefersPositionalPlaybackUntilPresentation(t *testing.T) {
	s := NewService(testAudioFS(t, "shot"))
	old := GlobalBackend()
	b := NewBackend(true)
	SetGlobalBackend(b)
	t.Cleanup(func() { SetGlobalBackend(old) })
	if _, err := s.Load("shot"); err != nil {
		t.Fatalf("fixture positional sample load failed: %v", err)
	}
	ev := []frame.EventView{{Kind: frame.EventKindAudio, AudioPositional: true, AudioAudible: true, Sound: "shot"}}
	if b.PlayCount() != 0 {
		t.Fatal("authoritative event fixture played before drain")
	}
	s.DrainEvents(30, 7, ev)
	if b.PlayCount() != 1 {
		t.Fatalf("presentation drain play count=%d want 1", b.PlayCount())
	}
	s.DrainEvents(31, 7, ev)
	if b.PlayCount() != 1 {
		t.Fatalf("committed event replayed on second rendered frame: %d", b.PlayCount())
	}
}
