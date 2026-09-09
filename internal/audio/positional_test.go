package audio

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestAttenuate_InsideVsOutside(t *testing.T) {
	v := Viewport{Left: 0, Top: 0, Width: 20, Height: 15} // pixels, but Attenuate uses *0x10
	// right = 0+20*16=320, bottom=0+15*16=240
	inside := [3]numeric.Fixed{numeric.Fixed(160 * 65536), 0, numeric.Fixed(120 * 65536)}
	if got := Attenuate(inside, v); got != VolInView {
		t.Fatalf("inside vol %d want %d", got, VolInView)
	}
	// exactly on border inclusive
	border := [3]numeric.Fixed{numeric.Fixed(320 * 65536), 0, numeric.Fixed(240 * 65536)}
	if got := Attenuate(border, v); got != VolInView {
		t.Fatalf("border vol %d want in-view", got)
	}
	// outside
	outside := [3]numeric.Fixed{numeric.Fixed(321 * 65536), 0, numeric.Fixed(0)}
	if got := Attenuate(outside, v); got != VolOffScreen {
		t.Fatalf("outside vol %d want off", got)
	}
	// off-screen still not silent, just attenuated
	if VolOffScreen == 0 {
		t.Fatal("off-screen vol should be negative")
	}
}

func TestComputePan_Centered(t *testing.T) {
	v := Viewport{Left: 128, Top: 32, Width: 32, Height: 26, MapW: 64, MapH: 64, SoundMode: SoundMode3D}
	pos := [3]numeric.Fixed{numeric.Fixed(384 * 65536), 0, numeric.Fixed(240 * 65536)}
	pan := ComputePan(pos, v)
	if pan != (Pan{}) {
		t.Fatalf("center pan = %+v, want zero", pan)
	}
	left := ComputePan([3]numeric.Fixed{numeric.Fixed((384 - 160) * 65536), 0, numeric.Fixed(240 * 65536)}, v)
	right := ComputePan([3]numeric.Fixed{numeric.Fixed((384 + 160) * 65536), 0, numeric.Fixed(240 * 65536)}, v)
	if left.X != -right.X || left.Z != 0 || right.Z != 0 {
		t.Fatalf("left/right pan = %+v / %+v, want horizontal symmetry", left, right)
	}
}

func TestDistanceGainUsesBothPlanarCoordinatesAndClampsAtMax(t *testing.T) {
	v := Viewport{Width: 32, Height: 26, MapW: 64, MapH: 64, SoundMode: SoundMode3D}
	minDist, maxDist := DistanceBounds(v)
	if minDist != 464 || maxDist != 2048 {
		t.Fatalf("distance bounds = %d, %d; want 464, 2048", minDist, maxDist)
	}
	if gain := DistanceGain(Pan{X: minDist}, v); gain != 1 {
		t.Fatalf("min-distance gain = %v, want 1", gain)
	}
	if gain := DistanceGain(Pan{X: minDist * 2}, v); gain != 0.5 {
		t.Fatalf("twice-min gain = %v, want 0.5", gain)
	}
	forward := DistanceGain(Pan{Z: minDist * 2}, v)
	if forward != 0.5 {
		t.Fatalf("same-X forward gain = %v, want 0.5", forward)
	}
	atMax := DistanceGain(Pan{X: maxDist}, v)
	beyond := DistanceGain(Pan{X: maxDist * 2}, v)
	if atMax != beyond {
		t.Fatalf("max gain = %v, beyond-max = %v; expected held max", atMax, beyond)
	}
}

func TestSpatialModeFromPreference(t *testing.T) {
	if got := SpatialModeFromPreference(1); got != SoundModeMono {
		t.Fatalf("mono preference = %v, want Mono", got)
	}
	if got := SpatialModeFromPreference(2); got != SoundMode3D {
		t.Fatalf("3D preference = %v, want 3D", got)
	}
	if got := SpatialModeFromPreference(7); got != SoundModeMono {
		t.Fatalf("other stored preference = %v, want Mono", got)
	}
}
