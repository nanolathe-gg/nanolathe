package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestEffectTimingLeavesPixelsUnmaterializedAndEvictionKeepsFrames(t *testing.T) {
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "burst", Loop: true, Frames: []formats.GAFWriteFrame{
		{Width: 1, Height: 1, Duration: 0, Pixels: []byte{7}},
		{Width: 1, Height: 1, Duration: 3, Pixels: []byte{8}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "anims"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "anims", "effect.gaf"), data, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	c := &Client{modelFS: fs}
	// Exercise the transient tier even with this small authored fixture.
	c.effectCacheLocked().durableBytes = effectDurableBytes
	timing, ok := c.EffectFrameTiming("EFFECT", "BURST")
	if !ok || timing.Loop || len(timing.Durations) != 2 || timing.Durations[0] != 1 || timing.Durations[1] != 3 {
		t.Fatalf("timing=%+v", timing)
	}
	if c.effectArt.frames.bytes != 0 {
		t.Fatal("timing decoded pixels")
	}
	entry, _ := c.effectEntry("effect", "burst")
	if len(entry.Frames[0].Frame.Pixels) != 0 {
		t.Fatal("durable metadata owns pixels")
	}
	first, ok := c.effectFrame("effect", "burst", -1)
	if !ok || first.Pixels[0] != 7 {
		t.Fatal("first frame missing")
	}
	last, ok := c.effectFrame("effect", "burst", 99)
	if !ok || last.Pixels[0] != 8 {
		t.Fatal("last-frame clamp changed")
	}
	// Explicitly evict cache ownership while a recorded command retains first.
	c.effectArt.frames = effectLRU[*formats.GAFFrame, *formats.GAFFrame]{}
	c.effectArt.sources = effectLRU[string, *formats.GAFSource]{}
	again, ok := c.effectFrame("effect", "burst", 0)
	if !ok || again == first || again.Pixels[0] != first.Pixels[0] {
		t.Fatal("eviction changed immutable result")
	}
	if len(entry.Frames[0].Frame.Pixels) != 0 {
		t.Fatal("selected frame escaped into durable metadata")
	}
	// The same logical source cannot silently acquire new pixels after its
	// timing metadata has been bound.
	data[len(data)-1] ^= 1
	if err := os.WriteFile(filepath.Join(root, "anims", "effect.gaf"), data, 0644); err != nil {
		t.Fatal(err)
	}
	c.effectArt.frames = effectLRU[*formats.GAFFrame, *formats.GAFFrame]{}
	c.effectArt.sources = effectLRU[string, *formats.GAFSource]{}
	if _, ok := c.effectFrame("effect", "burst", 0); ok {
		t.Fatal("changed source accepted under old metadata")
	}
}

func TestEffectLRUBoundsRetainedFrames(t *testing.T) {
	var cache effectLRU[int, *formats.GAFFrame]
	first := &formats.GAFFrame{Pixels: []byte{1}, Transparent: []bool{false}}
	cache.put(0, first, 2, 4)
	for i := 1; i < 100; i++ {
		cache.put(i, &formats.GAFFrame{Pixels: []byte{byte(i)}}, 2, 4)
	}
	if cache.bytes != 4 || len(cache.items) != 2 || first.Pixels[0] != 1 {
		t.Fatal("cache grew or mutated evicted frame")
	}
	if _, ok := cache.get(0); ok {
		t.Fatal("old frame retained")
	}
	cache.put(100, first, 5, 4)
	if _, ok := cache.get(100); ok {
		t.Fatal("oversized frame cached")
	}
}

func TestDurableEffectTierRejectsSharedChildExpansion(t *testing.T) {
	child := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{7}}
	root := &formats.GAFFrame{Subframes: []*formats.GAFFrame{child, child}}
	if effectFrameIsTree(root) {
		t.Fatal("shared child would expand outside durable source charge")
	}
	copyChild := *child
	root.Subframes[1] = &copyChild
	if !effectFrameIsTree(root) {
		t.Fatal("ordinary composite rejected from durable tier")
	}
}

func TestEffectSourceEscalationCacheResidency(t *testing.T) {
	root := os.Getenv("NANOLATHE_ESC_GAF_ROOT")
	if root == "" {
		t.Skip("set NANOLATHE_ESC_GAF_ROOT for the authored cache sweep")
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	c := &Client{modelFS: fs}
	for _, name := range []string{"esc_nuke_a_02", "esc_nuke_x_01", "esc_weap_x_01"} {
		bank := c.EffectBank(name)
		if bank == nil {
			t.Fatalf("%s metadata rejected: %+v", name, c.artDiagnostics)
		}
		for _, entry := range bank.Entries {
			for i, ref := range entry.Frames {
				f, ok := c.effectFrame(name, entry.Name, int32(i))
				if !ok || f == nil {
					t.Fatalf("%s frame %d unresolved", name, i)
				}
				if len(ref.Frame.Pixels) != 0 {
					t.Fatal("metadata retained selected pixels")
				}
				a := c.effectArt
				if a.sources.bytes > effectSourceBytes || a.frames.bytes > effectFrameBytes || a.durableBytes > effectDurableBytes {
					t.Fatalf("cache exceeded its budget: %+v", c.effectCacheSnapshot())
				}
			}
		}
		t.Logf("%s: %+v", name, c.effectCacheSnapshot())
	}
	c.SetModelFS(fs)
	if c.effectArt != nil || c.effectBanks != nil || c.doubledFrames != nil {
		t.Fatal("source reset retained effect cache ownership")
	}
}
