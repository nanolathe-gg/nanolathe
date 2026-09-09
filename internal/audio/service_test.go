package audio

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func fixedPixels(x, y, z int32) [3]numeric.Fixed {
	return [3]numeric.Fixed{numeric.Fixed(int64(x) << 16), numeric.Fixed(int64(y) << 16), numeric.Fixed(int64(z) << 16)}
}

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
	// The queue clock is the committed global tick, not the rendered-frame
	// counter [03 §8.3]; the cue was inserted at tick 30, so drain there.
	s.DrainEvents(30, nil)
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
	s.DrainEvents(7, ev)
	if len(spy.plays) != 1 {
		t.Fatalf("presentation drain play count=%d want 1", len(spy.plays))
	}
	s.DrainEvents(7, ev)
	if len(spy.plays) != 1 {
		t.Fatalf("committed event replayed on second rendered frame: %d", len(spy.plays))
	}
}

func TestServicePositionalSoundModeSelectsMonoOrPlanarRolloff(t *testing.T) {
	s := NewService(testAudioFS(t, "shot"))
	old := GlobalOutput()
	spy := &outputSpy{}
	SetGlobalOutput(spy)
	t.Cleanup(func() { SetGlobalOutput(old) })

	v := Viewport{Left: 128, Top: 32, Width: 32, Height: 26, MapW: 64, MapH: 64, SoundMode: SoundModeMono}
	s.SetViewport(v)
	center := fixedPixels(384, 0, 240)
	if pan, volume, ok := s.PlayPositional("shot", center, func([3]numeric.Fixed) bool { return true }); !ok || pan != (Pan{}) || volume != VolInView {
		t.Fatalf("mono center = pan=%+v volume=%d ok=%t", pan, volume, ok)
	}
	if got, want := spy.plays[0].volume, VolumeFromCentibel(VolInView); got != want || spy.plays[0].pan != 0 {
		t.Fatalf("mono output = volume=%v pan=%v; want base, centered", got, spy.plays[0].pan)
	}

	// The same point is off-screen in Mono but its Z displacement drives 3-D
	// distance. This proves the mode, rather than device stereo capability,
	// selects the branch.
	offscreen := fixedPixels(384, 0, 240+928)
	if _, volume, ok := s.PlayPositional("shot", offscreen, func([3]numeric.Fixed) bool { return true }); !ok || volume != VolOffScreen {
		t.Fatalf("mono offscreen volume=%d ok=%t, want %d true", volume, ok, VolOffScreen)
	}
	v.SoundMode = SoundMode3D
	s.SetViewport(v)
	if pan, volume, ok := s.PlayPositional("shot", offscreen, func([3]numeric.Fixed) bool { return true }); !ok || volume != VolInView || pan.Z != -928 {
		t.Fatalf("3D placement = pan=%+v volume=%d ok=%t", pan, volume, ok)
	}
	if got, want := spy.plays[2].volume, VolumeFromCentibel(VolInView)*0.5; math.Abs(got-want) > 1e-12 {
		t.Fatalf("3D twice-min gain = %v, want %v", got, want)
	}
	v.SoundMode = SoundModeMono
	s.SetViewport(v)
	_, volume, _ := s.PlayPositional("shot", offscreen, func([3]numeric.Fixed) bool { return true })
	if volume != VolOffScreen {
		t.Fatalf("mode switch back to Mono volume=%d, want %d", volume, VolOffScreen)
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

// TestServiceQueueClockIsTheCommittedTick locks the single-clock contract of
// [03 §8.3]: producers stamp inserts with the global tick counter, and the
// drain must arbitrate against that same counter. When the drain ran on a
// private rendered-frame counter that outran the tick, the per-slot
// next-allowed frames it wrote were in the wrong domain, `tick < nextAllowed`
// held forever, and each slot went permanently silent after its first audible
// resolve — the "played rarely" defect.
func TestServiceQueueClockIsTheCommittedTick(t *testing.T) {
	s := NewService(testAudioFS(t, "voice"))
	old := GlobalOutput()
	spy := &outputSpy{}
	SetGlobalOutput(spy)
	t.Cleanup(func() { SetGlobalOutput(old) })

	// Slot 5 (`ok`) carries cooldown multiplier 1, i.e. a 30-frame window.
	s.Queue.Register(1, &Category{Rows: [24]Row{5: {Variants: []string{"voice"}}}}, "unit", true)

	// A 60 Hz host against the 30 Hz simulation: two drains per committed tick.
	audible := 0
	for tick := uint32(1); tick <= 600; tick++ {
		s.Emit(tick, SlotOK, 1, "")
		for i := 0; i < 2; i++ {
			before := len(spy.plays)
			s.DrainEvents(tick, nil)
			audible += len(spy.plays) - before
		}
	}
	// 600 ticks with a 30-frame window admit twenty audible resolves; the
	// exact count is the contract, not merely "more than one".
	if audible != 20 {
		t.Fatalf("audible resolves over 600 ticks = %d, want 20", audible)
	}
}

// TestServiceVoiceLinesDoNotConsumeAliasRegistry locks [R-AUD-01 §1]: a unit
// voice line is a mode-1 load read straight from the VFS under `sounds`, and
// never enters the 255-entry alias registry that the mode-0 aliases own. The
// stock corpus authors 219 distinct voice variants, so registering them here
// exhausted the registry and silenced every later cue.
func TestServiceVoiceLinesDoNotConsumeAliasRegistry(t *testing.T) {
	s := NewService(testAudioFS(t, "voice"))
	old := GlobalOutput()
	spy := &outputSpy{}
	SetGlobalOutput(spy)
	t.Cleanup(func() { SetGlobalOutput(old) })

	s.Queue.Register(1, &Category{Rows: [24]Row{1: {Variants: []string{"voice"}}}}, "unit", true)
	before := s.Registry.Count()
	tick := uint32(0)
	for i := 0; i < 300; i++ {
		tick += 30
		s.Emit(tick, SlotSelect, 1, "")
		s.DrainEvents(tick, nil)
	}
	if len(spy.plays) == 0 {
		t.Fatal("no voice line reached the backend")
	}
	if got := s.Registry.Count(); got != before {
		t.Fatalf("voice playback registered %d alias identities, want none", got-before)
	}
}
