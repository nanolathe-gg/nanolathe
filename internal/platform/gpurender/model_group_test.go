package gpurender

import (
	"fmt"
	"github.com/hajimehoshi/ebiten/v2"
	"image"
	"image/color"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// If the last child cannot be placed, no earlier child image was rasterized
// either. Their shadows must not read apparently valid empty atlas regions.
func TestModelGroupAllocationFailureInvalidatesEarlierChildren(t *testing.T) {
	var d modelDirectLane
	d.resetFrame()
	d.page = modelDirectMaxPages - 1
	d.packX, d.packY = modelDirectAtlasW-120, modelDirectAtlasH-44
	parent := directSubject(0, 0, 40, 20)
	child := directSubject(0, 0, 4, 4)
	child.Reveal = &drawlist.ModelReveal{Above: -2, Below: -2, Band: -2}
	child.Shadow = &drawlist.ModelGeometry{Silhouette: true}
	later := directSubject(0, 0, 8, 4)
	parent.Children = []drawlist.ModelChild{{Geometry: child}, {Geometry: later}}
	if region := d.assignGroupRegions(parent); region.ok {
		t.Fatal("group unexpectedly fit after its second child ran out of atlas space")
	}
	for _, g := range []*drawlist.ModelGeometry{parent, child, later} {
		if d.regions[g].ok {
			t.Fatal("failed group retained a valid unwritten raster")
		}
	}
	if _, ok := d.shadowSource(child); ok {
		t.Fatal("failed group's earlier child still provides a shadow image")
	}
}

// Reflected children keep their own finished alpha, and compare against the
// group's surviving key too. An erased high face must not reflect the plate
// underneath at the child's height, even when its key ties the plate.
func checkModelGroupReflectionPixels() error {
	shader, err := newReflectionSourceShader()
	if err != nil {
		return err
	}
	defer shader.Deallocate()
	own := image.NewRGBA(image.Rect(0, 0, 16, 16))
	keys := image.NewRGBA(own.Bounds())
	group := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 4; y++ {
		for x := 0; x < 12; x++ {
			if x >= 4 {
				own.SetRGBA(x, y, color.RGBA{100, 100, 100, 255})
			}
			keys.SetRGBA(x, y, color.RGBA{30, 0, 0, 255})
			key := uint8(30)
			if x >= 4 && x < 8 {
				key = 40
			}
			group.SetRGBA(x+8, y+4, color.RGBA{key, 0, 0, 255})
		}
	}
	src, ownKey, groupKey := ebiten.NewImageFromImage(own), ebiten.NewImageFromImage(keys), ebiten.NewImageFromImage(group)
	defer src.Deallocate()
	defer ownKey.Deallocate()
	defer groupKey.Deallocate()
	dst := ebiten.NewImage(12, 4)
	defer dst.Deallocate()
	var v [4]ebiten.Vertex
	for i, xy := range [4][2]float32{{0, 0}, {12, 0}, {12, 4}, {0, 4}} {
		v[i] = ebiten.Vertex{DstX: xy[0], DstY: xy[1], SrcX: xy[0], SrcY: xy[1], ColorB: 8, ColorA: 4, Custom0: 10, Custom1: 30, Custom3: -1}
	}
	dst.DrawTrianglesShader(v[:], []uint16{0, 1, 2, 0, 2, 3}, shader, &ebiten.DrawTrianglesShaderOptions{
		Images:   [4]*ebiten.Image{src, ownKey, groupKey},
		Uniforms: map[string]any{"Metadata": float32(0), "RecordScale": float32(1), "Surface": []float32{0, 0, 1, 0}},
	})
	pix := make([]byte, 12*4*4)
	dst.ReadPixels(pix)
	for _, sample := range []struct {
		x    int
		want byte
	}{{2, 0}, {6, 0}, {10, 255}} {
		if got := pix[(12+sample.x)*4+3]; got != sample.want {
			return fmt.Errorf("construction reflection at %d alpha=%d want=%d", sample.x, got, sample.want)
		}
	}
	return nil
}

func TestModelGroupReflectionKeepsWorldPlacement(t *testing.T) {
	r := &Renderer{}
	r.reflections.active = &drawlist.ModelGeometry{ReflectWater: true, WorldHeight: 10}
	r.reflections.region = modelDirectRegion{x: 200, y: 300, page: 1, bounds: image.Rect(30, 40, 50, 60), ok: true}
	r.modelDirect.groupReflection = modelDirectRegion{x: 20, y: 40, page: 0, bounds: image.Rect(10, 20, 60, 70), ok: true}
	face := directFace(0, 0, 8, 8, 100, 30, 30)
	r.reflectModelFace(&face, 200, 300, 2, 0, 0, 0, 1, 1)
	if len(r.reflections.runs) != 1 || len(r.reflections.verts) != 4 {
		t.Fatal("missing reflected child")
	}
	run, v := r.reflections.runs[0], r.reflections.verts[0]
	if run.page != 1 || run.occlusionPage != 0 || v.SrcX != 200 || v.SrcY != 300 || v.DstX != 30 || v.DstY != 50 || v.ColorB != -140 || v.ColorA != -220 || v.Custom3 != -1 {
		t.Fatalf("reflection source/group mapping changed placement: run=%+v vertex=%+v", run, v)
	}
}
