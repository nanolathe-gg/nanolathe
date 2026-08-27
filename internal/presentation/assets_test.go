package presentation

import "testing"

func TestAssetsCopiesInputsAndReturnsDetachedValues(t *testing.T) {
	frames := []AssetID{"a", "b"}
	durations := []uint32{2, 3}
	sides := map[uint8]AssetID{1: "side"}
	spec := AssetCatalog{Models: map[AssetID]ModelAsset{
		"unit": {ID: "unit", Textures: TextureSet{ID: "tex", Side: sides, Default: "default", Durations: durations}},
	}, Effects: map[AssetID]EffectAsset{
		"boom": {ID: "boom", Sequence: AssetSequence{ID: "seq", Frames: frames, Durations: durations, Loop: true}},
	}}
	assets := NewAssets(spec)
	frames[0] = "changed"
	durations[0] = 99
	sides[1] = "changed"
	model, ok := assets.Model("unit")
	if !ok || model.Textures.Side[1] != "side" || model.Textures.Durations[0] != 2 {
		t.Fatalf("input mutation reached catalog: %#v", model)
	}
	effect, ok := assets.Effect("boom")
	if !ok || effect.Sequence.Frames[0] != "a" {
		t.Fatalf("effect input mutation reached catalog: %#v", effect)
	}
	effect.Sequence.Frames[0] = "caller"
	effectAgain, _ := assets.Effect("boom")
	if effectAgain.Sequence.Frames[0] != "a" {
		t.Fatal("lookup returned mutable catalog storage")
	}
	if _, ok := assets.Cursor("missing"); ok {
		t.Fatal("missing asset lookup reported present")
	}
}
