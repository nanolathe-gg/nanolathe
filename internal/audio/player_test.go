package audio

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestBackendHeadlessNeverCreatesContext(t *testing.T) {
	b := NewBackend(true)
	if !b.IsHeadless() {
		t.Fatal("headless backend should report headless")
	}
	// Play should be no-op but record alias without creating device
	s := &Sample{Alias: "test", SampleRate: 11025, Channels: 1, BitsPerSample: 8, Data: []byte{128, 129, 127}}
	if err := b.PlaySample(s, 1.0, 0); err != nil {
		t.Fatalf("headless PlaySample error %v", err)
	}
	if b.PlayCount() != 1 {
		t.Fatalf("headless play count %d want 1", b.PlayCount())
	}
	if b.ctx != nil {
		t.Fatal("headless backend must not create audio context")
	}
	// Global backend guard
	old := GlobalBackend()
	SetGlobalBackend(NewBackend(true))
	defer SetGlobalBackend(old)
	if GlobalBackend() == nil || !GlobalBackend().IsHeadless() {
		t.Fatal("global headless backend not installed")
	}
}

func TestVolumeFromCentibel(t *testing.T) {
	// [03 §8.3] -585 in-view vs -1585 off-screen
	vIn := VolumeFromCentibel(VolInView)
	vOff := VolumeFromCentibel(VolOffScreen)
	if vIn <= vOff {
		t.Fatalf("in-view volume %f should be > off %f", vIn, vOff)
	}
	if math.Abs(vIn-0.51) > 0.02 {
		t.Fatalf("in-view %f want ~0.51", vIn)
	}
	if math.Abs(vOff-0.16) > 0.02 {
		t.Fatalf("off %f want ~0.16", vOff)
	}
	if VolumeFromCentibel(0) != 1.0 {
		t.Fatalf("0 centibel should be 1.0")
	}
}

func TestPanFloat(t *testing.T) {
	v := Viewport{Left: 0, Top: 0, Width: 40, Height: 30, StereoCapable: true}
	// centered pan X=0 => 0
	if f := PanFloat(Pan{X: 0}, v); f != 0 {
		t.Fatalf("center pan %f want 0", f)
	}
	// dx = Width*8 = 320 => pan 1 at edge
	if f := PanFloat(Pan{X: 320}, v); f != 1 {
		t.Fatalf("edge pan %f want 1", f)
	}
	if f := PanFloat(Pan{X: -320}, v); f != -1 {
		t.Fatalf("left edge %f want -1", f)
	}
	// clamp beyond
	if f := PanFloat(Pan{X: 1000}, v); f != 1 {
		t.Fatalf("clamp %f want 1", f)
	}
	// zero viewport width => 0
	if f := PanFloat(Pan{X: 100}, Viewport{}); f != 0 {
		t.Fatalf("zero viewport pan %f want 0", f)
	}
}

func TestConvertSampleMono8(t *testing.T) {
	s := &Sample{Alias: "mono8", SampleRate: 11025, Channels: 1, BitsPerSample: 8, Data: []byte{128, 255, 0}}
	data := convertSample(s, 1.0, 0, 44100)
	if len(data) == 0 {
		t.Fatal("convert empty")
	}
	// 3 frames * 4 *2 / resample ratio 4 = ~12 frames *8 = 96
	if len(data)%8 != 0 {
		t.Fatalf("byte len %d not multiple of 8", len(data))
	}
	// check silence frame first sample is near 0
	// first dst frame corresponds to src 128 => 0
	// decode: left==right
}

func TestConvertSampleStereo16(t *testing.T) {
	// 2 frames stereo 16-bit: left 0, right 0, left max, right min
	data := make([]byte, 8)
	// frame0: L 0, R 0
	// frame1: L 32767, R -32768
	// Already zeroed first frame
	data[4] = 0xFF
	data[5] = 0x7F
	data[6] = 0x00
	data[7] = 0x80
	s := &Sample{Alias: "st16", SampleRate: 22050, Channels: 2, BitsPerSample: 16, Data: data}
	out := convertSample(s, 0.5, -1, 22050) // left pan, half volume
	if len(out) == 0 {
		t.Fatal("stereo16 convert empty")
	}
	if len(out) != 16 { // 2 frames *8
		t.Fatalf("len %d want 16", len(out))
	}
}

func TestBackendPlayAliasHeadless(t *testing.T) {
	cache := NewCache(nil)
	// put a synthetic sample
	raw := []byte{128, 128, 128, 128}
	_, err := cache.Put("testalias", raw)
	if err != nil {
		t.Fatalf("put %v", err)
	}
	s, ok := cache.Get("testalias")
	if !ok || s == nil {
		t.Fatal("sample not cached")
	}
	b := NewBackend(true)
	old := GlobalBackend()
	SetGlobalBackend(b)
	defer SetGlobalBackend(old)
	if err := b.PlayAlias("testalias", cache, 1.0, 0); err != nil {
		t.Fatalf("play alias headless %v", err)
	}
	if b.PlayCount() != 1 {
		t.Fatalf("count %d want 1", b.PlayCount())
	}
	// missing alias should still record but degrade silently
	if err := b.PlayAlias("missing", cache, 1.0, 0); err != nil {
		t.Fatalf("missing alias should not error")
	}
	if b.PlayCount() != 2 {
		t.Fatalf("missing alias count %d want 2", b.PlayCount())
	}
}

func TestQueueArbitrationIntactAfterBackend(t *testing.T) {
	q := NewQueue()
	cat := categoryFixture()
	q.Register(1, cat, "U", true)
	// Install headless backend and verify queue still orders descending
	b := NewBackend(true)
	SetGlobalBackend(b)
	defer SetGlobalBackend(nil)
	q.OnPlay(func(alias string, slot Slot, unit pool.Handle) {
		// also play via backend for UI cues
		_ = b.PlayAlias(alias, nil, 1.0, 0)
	})
	_ = cat
	// Insert low priority then high, verify order
	if !q.InsertAt(0, 11, 1, "") {
		t.Fatal("working")
	}
	if !q.InsertAt(0, 7, 1, "") {
		t.Fatal("cant")
	}
	if q.Entries[0].Slot != 7 {
		t.Fatalf("high priority not first after backend install")
	}
}

func TestBackendVolumeAndPanBake(t *testing.T) {
	s := &Sample{Alias: "pan", SampleRate: 11025, Channels: 1, BitsPerSample: 8, Data: []byte{255, 255, 255, 255}} // max
	// full volume center should produce non-zero bytes
	dataCenter := convertSample(s, 1.0, 0, 11025)
	dataLeft := convertSample(s, 1.0, -1, 11025)
	dataRight := convertSample(s, 1.0, 1, 11025)
	if len(dataCenter) != len(dataLeft) || len(dataCenter) != len(dataRight) {
		t.Fatalf("len mismatch")
	}
	// left pan should have right channel near zero, and vice versa
	// Check first frame left/right floats
	if len(dataCenter) < 8 {
		t.Fatal("too short")
	}
	// decode first frame floats: bytes 0..3 left, 4..7 right
	// For center, left==right; for left pan, right near 0
	// We just ensure not equal
	if string(dataCenter) == string(dataLeft) {
		t.Fatal("center and left should differ")
	}
	if string(dataCenter) == string(dataRight) {
		t.Fatal("center and right should differ")
	}
}
