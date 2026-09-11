package gpurender

import (
	"bytes"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Invoked only by the existing opt-in device loop. The same recording must
// repopulate retired resources and retain identical pixels; replacement maps
// must retain only their own source population.
func checkSourceLifecycleDevicePixels() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	defer r.ResetSources()
	makeList := func() drawlist.List {
		list := fixtureModelList()
		list.RecordTerrain(drawlist.Terrain{Terrain: terrainFixtureTerrain(), DstW: 8, DstH: 8})
		list.RecordSurface(drawlist.Surface{Pixels: []byte{31, 47, 59, 71}, SrcW: 2, SrcH: 2, Dst: drawlist.Rect{X: 76, Y: 46, W: 2, H: 2}, Identity: 1, Revision: 1})
		list.RecordExpand()
		return list
	}
	counts := func() [4]int {
		return [4]int{len(r.tileAtlases), len(r.gafImages), len(r.scene.frames), len(r.textureAtlas.slots)}
	}
	list := makeList()
	img := r.Execute(&list, 80, 48)
	if img == nil {
		return fmt.Errorf("source lifecycle initial frame missing")
	}
	before := make([]byte, 80*48*4)
	after := make([]byte, len(before))
	img.ReadPixels(before)
	expected := counts()
	if expected[0] != 1 || expected[1] == 0 || expected[2] == 0 || expected[3] == 0 {
		return fmt.Errorf("source lifecycle fixture did not populate caches: %v", expected)
	}
	shader, output := r.scene2D, r.surfaces[0]
	for pass := 0; pass < 3; pass++ {
		r.ResetSources()
		if counts() != [4]int{} || len(r.scene.pages) != 0 {
			return fmt.Errorf("source lifecycle retained retired sources on pass %d", pass)
		}
		if r.scene2D != shader || r.surfaces[0] != output {
			return fmt.Errorf("source lifecycle replaced program/output")
		}
		// First replay uses the identical list. Subsequent passes model same-map
		// restarts: newly loaded terrain, sprite and model identities, same pixels.
		if pass != 0 {
			list = makeList()
		}
		img = r.Execute(&list, 80, 48)
		if img == nil {
			return fmt.Errorf("source lifecycle replay missing on pass %d", pass)
		}
		img.ReadPixels(after)
		if !bytes.Equal(before, after) {
			return fmt.Errorf("source lifecycle changed pixels after reset on pass %d", pass)
		}
		if counts() != expected {
			return fmt.Errorf("source lifecycle retained replacement-map caches: got %v want %v", counts(), expected)
		}
	}
	return nil
}
