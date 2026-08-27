package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/presentation"
)

func TestTexturePlayersAreIndependentAndTickBound(t *testing.T) {
	seq := presentation.AssetSequence{
		Frames: []presentation.AssetID{"a", "b"}, Durations: []uint32{2, 1},
	}
	a := NewTexturePlayer(seq)
	b := NewTexturePlayer(seq)
	if got, _ := a.Frame(); got != "a" {
		t.Fatalf("initial frame %q", got)
	}
	a.Advance(false)
	a.Advance(true)
	if got, _ := a.Frame(); got != "a" {
		t.Fatalf("render pass advanced texture to %q", got)
	}
	if got, _ := b.Frame(); got != "a" {
		t.Fatalf("other instance changed to %q", got)
	}
	a.Advance(true)
	if got, _ := a.Frame(); got != "b" {
		t.Fatalf("second simulation tick frame %q", got)
	}
	if got, _ := b.Frame(); got != "a" {
		t.Fatalf("independent instance frame %q", got)
	}
}
func TestShadeRowForNormalUsesUnnormalizedAverage(t *testing.T) {
	light := [3]float64{0, 1, 0}
	if got := ShadeRowForNormal([3]float64{0, 0.5, 0}, light, false); got != 2 {
		t.Fatalf("row %d, want trunc(0.5*5)=2", got)
	}
	if got := ShadeRowForNormal([3]float64{0, 1, 0}, light, true); got != 15 {
		t.Fatalf("dont-shade row %d", got)
	}
	if got := ShadeRowForNormal([3]float64{0, -1, 0}, light, false); got != 27 {
		t.Fatalf("wrapped negative row %d", got)
	}
}

func TestModelShadowGatesAndInclusiveClip(t *testing.T) {
	options := ShadowMaster | ShadowVehicles
	if !ModelShadowEnabled(options, false, true) {
		t.Fatal("enabled model shadow rejected")
	}
	if ModelShadowEnabled(options, true, true) || ModelShadowEnabled(options, false, false) {
		t.Fatal("suppressed model shadow admitted")
	}
	if !ShadowDepthVisible(10, 10, 0) || ShadowDepthVisible(11, 10, 0) {
		t.Fatal("depth comparison")
	}
	minX, minY, maxX, maxY, ok := ShadowClipInclusive(-1, 2, 9, 8, 8, 8)
	if !ok || minX != 0 || minY != 2 || maxX != 7 || maxY != 7 {
		t.Fatalf("clip %d,%d..%d,%d,%v", minX, minY, maxX, maxY, ok)
	}
}
