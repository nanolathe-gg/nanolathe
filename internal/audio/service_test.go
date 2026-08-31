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
	s.DrainEvents(30, 0, nil)
	if calls != 1 {
		t.Fatalf("Drain reconfigured playback callback: calls=%d", calls)
	}
}

func TestServiceDrainEventsDefersPositionalPlaybackUntilPresentation(t *testing.T) {
	s := NewService(testAudioFS(t, "shot"))
	old := GlobalOutput()
	spy := &outputSpy{}
	SetGlobalOutput(spy)
	t.Cleanup(func() { SetGlobalOutput(old) })
	if _, err := s.Load("shot"); err != nil {
		t.Fatalf("fixture positional sample load failed: %v", err)
	}
	ev := []frame.EventView{{Kind: frame.EventKindAudio, AudioPositional: true, AudioAudible: true, Sound: "shot"}}
	if len(spy.plays) != 0 {
		t.Fatal("authoritative event fixture played before drain")
	}
	s.DrainEvents(30, 7, ev)
	if len(spy.plays) != 1 {
		t.Fatalf("presentation drain play count=%d want 1", len(spy.plays))
	}
	s.DrainEvents(31, 7, ev)
	if len(spy.plays) != 1 {
		t.Fatalf("committed event replayed on second rendered frame: %d", len(spy.plays))
	}
}

func TestServiceStreamDelayCancelPlayOnceAndStop(t *testing.T) {
	s := NewService(testAudioFS(t, "voice"))
	old := GlobalOutput()
	spy := &streamOutputSpy{}
	SetGlobalOutput(spy)
	t.Cleanup(func() { SetGlobalOutput(old) })

	s.StartStream("sounds/voice.wav", 0, 60, 0)
	s.TickStream(59)
	if len(spy.streamPlays) != 0 {
		t.Fatal("stream played before its 60-unit due time")
	}
	s.TickStream(60)
	s.TickStream(61)
	if len(spy.streamPlays) != 1 {
		t.Fatalf("stream play count=%d, want one", len(spy.streamPlays))
	}
	if spy.streamPlays[0].Alias != "sounds/voice.wav" {
		t.Fatalf("stream alias=%q, want authored path", spy.streamPlays[0].Alias)
	}
	s.StopStream()
	if spy.streamStops != 1 {
		t.Fatalf("active stream stop count=%d, want one", spy.streamStops)
	}

	s.StartStream("sounds/voice.wav", 0, 60, 100)
	s.StopStream()
	s.TickStream(160)
	if len(spy.streamPlays) != 1 {
		t.Fatalf("cancelled pending stream played: %d", len(spy.streamPlays))
	}
}

func TestServiceStreamEarlierTimerUsesLaterPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sounds"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "later"} {
		if err := os.WriteFile(filepath.Join(root, "sounds", name+".wav"), buildRIFF(1, 11025, 8, []byte{128, 129}), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	s := NewService(fs)
	old := GlobalOutput()
	spy := &streamOutputSpy{}
	SetGlobalOutput(spy)
	t.Cleanup(func() { SetGlobalOutput(old) })

	s.StartStream("sounds/first.wav", 0, 100, 0)
	s.StartStream("sounds/later.wav", 0, 200, 0)
	s.TickStream(99)
	s.TickStream(100)
	if len(spy.streamPlays) != 1 || spy.streamPlays[0].Alias != "sounds/later.wav" {
		t.Fatalf("earlier timer did not play later path once: plays=%d", len(spy.streamPlays))
	}
	s.TickStream(200)
	if len(spy.streamPlays) != 1 {
		t.Fatalf("later recorded timer replayed stream: %d", len(spy.streamPlays))
	}
}
