package content

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

func TestSightShapesReadStorageInsteadOfRLECoverage(t *testing.T) {
	frames := make([]formats.GAFWriteFrame, 10)
	for i := range frames {
		frames[i] = formats.GAFWriteFrame{Width: 3, Height: 1, Pixels: []byte{7, 7, 7}, Transparent: []bool{true, true, true}}
	}
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "vismask", Frames: frames}})
	if err != nil {
		t.Fatal(err)
	}
	shapes, err := CompileSightShapes(newFixtureFS(t, fixtureFile{path: "anims/vismasks.gaf", data: string(data)}))
	if err != nil {
		t.Fatal(err)
	}
	for x := int32(0); x < 3; x++ {
		if !shapes.Shape(0).Covers(x, 0) {
			t.Fatalf("encoded storage byte %d was treated as RLE transparency", x)
		}
	}
}

func TestSightShapesRejectCompositeStorageWithProvenance(t *testing.T) {
	frames := make([]formats.GAFWriteFrame, 10)
	for i := range frames {
		frames[i] = formats.GAFWriteFrame{Width: 1, Height: 1, Subframes: []formats.GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{7}}}}
	}
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "vismask", Frames: frames}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CompileSightShapes(newFixtureFS(t, fixtureFile{path: "anims/vismasks.gaf", data: string(data)}))
	if err == nil {
		t.Fatal("composite mask was flattened")
	}
	for _, want := range []string{"logical path anims/vismasks.gaf", "bounded direct raster storage"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
}
