package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

func TestFogSnapshotOriginRoundTrip(t *testing.T) {
	cache := visibility.NewFogCacheFromChannelsAt(2, 1, 7, -3, []uint8{1, 0}, []uint8{0, 0})
	ox, oz := cache.Origin()
	if ox != 7 || oz != -3 {
		t.Fatalf("origin got %d,%d want 7,-3", ox, oz)
	}
	ch0, ch1 := cache.Channels()
	copyCache := visibility.NewFogCacheFromChannelsAt(2, 1, ox, oz, ch0, ch1)
	if gotX, gotZ := copyCache.Origin(); gotX != ox || gotZ != oz {
		t.Fatalf("round-trip origin got %d,%d want %d,%d", gotX, gotZ, ox, oz)
	}
	ops := BuildFogOps(copyCache, &camera.Camera{}, 0, 0, 0, 0, nil, false)
	if len(ops) != 1 || ops[0].GridX != 7 || ops[0].GridY != -3 {
		t.Fatalf("origin-bearing cache addressed as %+v", ops)
	}
}

func TestMinimapServiceDirtyLifecycle(t *testing.T) {
	picture := &RadarSurface{W: 4, H: 2, Pitch: 4, Bits: []byte{1, 2, 3, 4, 5, 6, 7, 8}}
	s := NewMinimapService(MinimapServiceConfig{Picture: picture, MapW: 2, MapH: 1, FogFill: 9})
	if s.Picture() == picture || s.Dirty() != MinimapDirtyMapped|MinimapDirtyFinal {
		t.Fatalf("constructor must own picture and schedule derived surfaces")
	}
	if !s.RebuildMapped([]uint16{1, 1}, []uint8{1, 1}) || s.Dirty() != MinimapDirtyFinal {
		t.Fatalf("mapped rebuild dirty=%02x", s.Dirty())
	}
	if !s.RebuildFinal(camera.Minimap{W: 4, H: 2}, 2, 1, nil, nil, 10, 11, 12) || s.Dirty() != 0 {
		t.Fatalf("final rebuild dirty=%02x", s.Dirty())
	}
	s.Tick()
	if s.Dirty()&MinimapDirtyFinal == 0 {
		t.Fatalf("each tick must schedule final rebuild")
	}
	s.MarkPlacement()
	if s.Dirty()&MinimapDirtyMapped == 0 {
		t.Fatalf("placement must invalidate mapped")
	}
	s.Restore()
	if s.Dirty()&(MinimapDirtyMapped|MinimapDirtyFinal) != MinimapDirtyMapped|MinimapDirtyFinal {
		t.Fatalf("restore must invalidate derived surfaces")
	}
}

func TestRebuildFinalExactMissingArtAndMarker(t *testing.T) {
	mapped := &RadarSurface{W: 16, H: 16, Pitch: 16, Bits: make([]byte, 256)}
	for i := range mapped.Bits {
		mapped.Bits[i] = 7
	}
	contacts := []MinimapContact{{WorldX: 10, WorldZ: 10, Visible: true, Palette: 99}}
	final := RebuildFinalExact(mapped, camera.Minimap{W: 16, H: 16}, 100, 100, contacts, BlinkState{Phase: 1}, nil, 21, 22, 23)
	for i, got := range final.Bits {
		if got != 7 {
			t.Fatalf("missing authored blip must not invent pixels at %d: %d", i, got)
		}
	}
	DrawViewportMarker(final, 2, -120, 0, -24, 0, 0, 31)
	count := 0
	for _, got := range final.Bits {
		if got == 31 {
			count++
		}
	}
	if count != 5 {
		t.Fatalf("viewport marker must contain exactly five pixels, got %d", count)
	}
}
