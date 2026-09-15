package gpurender

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// Cached art retains Doubled's exact pixels, masks, plain raster and signed
// geometry (DESIGN_GPU_RENDERER §14.3). Shared children keep their own identity
// across parents without changing the authored traversal [03 R-COMP-01 §2].
func TestFogVariantPreservesDoublingAndSharedChildren(t *testing.T) {
	leaf := fogTestLeaf(7, 3, -5, 3, 5)
	leaf.Pixels[2] = 9
	leaf.Transparent = make([]bool, len(leaf.Pixels))
	leaf.Transparent[2] = true
	leaf.PlainPixels, leaf.PlainTransparent = leaf.Pixels, leaf.Transparent
	leaf.AlternateBlitter, leaf.Compressed = 1, 1
	parent := fogTestComposite(leaf, nil, leaf)
	parent.SubframeCount = 3
	parent.Pixels, parent.PlainPixels = []byte{10}, []byte{11}
	other := fogTestComposite(leaf)
	var fog fogPass
	for _, scale := range []camera.ViewScale{0, camera.ViewScaleNative, camera.ViewScaleDetail} {
		got := fog.viewFrame(parent, scale)
		if scale.Native() {
			if got != parent || len(fog.variants) != 0 {
				t.Fatal("native fog lost source identity or allocated variants")
			}
			continue
		}
		want := parent.Doubled()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("scale %s variant changed resampled art: got %+v want %+v", scale, got, want)
		}
		shared := fog.viewFrame(leaf, scale)
		if got.Subframes[0] != shared || got.Subframes[2] != shared || fog.viewFrame(other, scale).Subframes[0] != shared {
			t.Fatalf("scale %s shared child acquired multiple identities", scale)
		}
		if &shared.Pixels[0] != &shared.PlainPixels[0] || &shared.Transparent[0] != &shared.PlainTransparent[0] {
			t.Fatal("resampling lost aliased plain raster")
		}
		if got != fog.viewFrame(parent, scale) {
			t.Fatal("repeated parent acquired a new identity")
		}
	}
	if parent.Subframes[0] != leaf || leaf.Width != 3 || leaf.XOffset != -3 || leaf.YOffset != 5 {
		t.Fatal("variant construction mutated its source")
	}
	if allocs := testing.AllocsPerRun(100, func() {
		fog.viewFrame(parent, camera.ViewScaleDetail)
	}); allocs != 0 {
		t.Fatalf("warmed variant lookup allocates %g objects", allocs)
	}
}

// Admission must agree with the actual stored resample, including narrow
// geometry wrapping, while needing no raster allocations (§11.2, §14.3).
func TestFogOrderedAdmissionMatchesVariantGeometryWithoutAllocations(t *testing.T) {
	for _, scale := range []camera.ViewScale{0, camera.ViewScaleNative, camera.ViewScaleDetail} {
		for _, width := range []uint16{0, 1, 3, 63, 64, 65, 32768, 65535} {
			for _, offset := range []int16{-32768, -43, -1, 0, 1, 32767} {
				// Zero height keeps the reference conversion device- and raster-free;
				// restore its independently scaled positive height before placement.
				fr := &formats.GAFFrame{Width: width, XOffset: offset, YOffset: -1}
				wantFrame := fr
				if !scale.Native() {
					wantFrame = fr.Doubled()
				}
				fr.Height = 3
				wantFrame.Height = uint16(scale.Project(3))
				_, _, fits := fogFrameTilePlacement(wantFrame, fogAtlasTile(scale))
				want := !fits && wantFrame.Width > 0 && wantFrame.Height > 0
				fg := fogTestCommand(fr, render.FogKindGAFCh0)
				if got := fogNeedsOrdered(fg, scale); got != want {
					t.Fatalf("scale %s width %d offset %d: ordered %t want %t", scale, width, offset, got, want)
				}
			}
		}
		fg := fogTestCommand(fogTestLeaf(7, 1, 3, 16, 16), render.FogKindGAFCh1)
		if allocs := testing.AllocsPerRun(100, func() { fogNeedsOrdered(fg, scale) }); allocs != 0 {
			t.Fatalf("scale %s admission allocates %g objects", scale, allocs)
		}
	}
}

func TestFogRepeatedCommandsKeepAtlasAndVariantStorage(t *testing.T) {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		for _, shape := range []string{"simple", "composite", "oversized"} {
			t.Run(scale.String()+"/"+shape, func(t *testing.T) {
				r, _ := schedulerFixture(t)
				leaf := fogTestLeaf(7, 1, 3, 3, 5)
				fr := leaf
				switch shape {
				case "composite":
					alternate := fogTestComposite(leaf)
					alternate.AlternateBlitter = 1
					fr = fogTestComposite(leaf, alternate)
				case "oversized":
					// At magnified scales this needs its own scene atlas page.
					fr = fogTestLeaf(7, 0, 0, 1500, 1)
				}
				fg := fogTestCommand(fr, render.FogKindGAFCh0)
				fg.Ops[0].Scale = scale
				compile := func() {
					r.sched.resetFrame(64, 64)
					r.Fog(fg)
					fg.Ops[0].Kind = render.FogKindGAFCh1
					r.Fog(fg)
					fg.Ops[0].Patterned = true
					r.Fog(fg)
					fg.Ops[0].Kind, fg.Ops[0].Patterned = render.FogKindGAFCh0, false
				}
				compile()
				frames, pages, variants := len(r.scene.frames), len(r.scene.pages), len(r.fog.variants)
				atlas := r.fog.atlas
				if allocs := testing.AllocsPerRun(100, compile); allocs != 0 {
					t.Fatalf("warmed fog command compilation allocates %g objects", allocs)
				}
				if len(r.scene.frames) != frames || len(r.scene.pages) != pages || len(r.fog.variants) != variants || r.fog.atlas != atlas {
					t.Fatal("repeated fog grew source frames, variants or atlas pages")
				}
			})
		}
	}
}

func TestFogSourceResetReleasesVariants(t *testing.T) {
	var r Renderer
	fr := fogTestComposite(fogTestLeaf(7, 0, 0, 3, 5))
	old := r.fog.viewFrame(fr, camera.ViewScaleDetail)
	r.scene.frames = map[*formats.GAFFrame]sceneEntry{old.Subframes[0]: {ok: true}}
	r.resetSources(func(*ebiten.Image) { t.Fatal("device-free variants unexpectedly owned an image") })
	if r.fog.variants != nil || len(r.scene.frames) != 0 {
		t.Fatal("source reset retained fog variants or scene identities")
	}
	if next := r.fog.viewFrame(fr, camera.ViewScaleDetail); next == old || next.Subframes[0] == old.Subframes[0] {
		t.Fatal("new source generation reused a retired fog variant")
	}
}

// Run inside the existing hidden device loop. Replaying admitted composites
// and dedicated-page art must preserve pixels and stop growing source storage
// after warm-up, including after a source reset (§2.3, §11.2).
func checkFogVariantDeviceStorageAt(scale camera.ViewScale) error {
	const w, h = 64, 48
	pal := fixturePalette()
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	defer r.ResetSources()
	leaf := fogTestLeaf(120, 3, 5, 5, 7)
	leaf.Transparent = make([]bool, len(leaf.Pixels))
	leaf.Transparent[1] = true
	alternate := fogTestComposite(leaf, fogTestLeaf(200, 5, 7, 5, 7))
	alternate.AlternateBlitter = 1
	composite := fogTestComposite(leaf, alternate)
	oversized := fogTestLeaf(60, 0, 0, 1500, 1)
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 100})
	for _, fr := range []*formats.GAFFrame{composite, oversized} {
		for _, kind := range []render.FogKind{render.FogKindGAFCh0, render.FogKindGAFCh1} {
			fg := fogTestCommand(fr, kind)
			fg.Ops[0].Scale = scale
			list.RecordFog(fg)
		}
	}
	list.RecordExpand()
	pixels, first := make([]byte, w*h*4), make([]byte, w*h*4)
	r.Execute(&list, w, h).ReadPixels(first)
	frames, pages, variants := len(r.scene.frames), len(r.scene.pages), len(r.fog.variants)
	for i := 0; i < 20; i++ {
		r.Execute(&list, w, h)
	}
	r.Execute(&list, w, h).ReadPixels(pixels)
	if len(r.scene.frames) != frames || len(r.scene.pages) != pages || len(r.fog.variants) != variants {
		return fmt.Errorf("scale %s repeated device fog grew source frames, variants or pages", scale)
	}
	if !bytes.Equal(pixels, first) {
		return fmt.Errorf("scale %s repeated device fog changed pixels", scale)
	}
	r.ResetSources()
	if r.fog.variants != nil || len(r.scene.frames) != 0 || len(r.scene.pages) != 0 {
		return fmt.Errorf("scale %s source reset retained device fog storage", scale)
	}
	r.Execute(&list, w, h).ReadPixels(pixels)
	if !bytes.Equal(pixels, first) {
		return fmt.Errorf("scale %s rebuilding fog after source reset changed pixels", scale)
	}
	return nil
}
