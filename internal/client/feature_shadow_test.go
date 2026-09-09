package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

const (
	featureRasterBackground = uint8(201)
	featureShadowSource     = uint8(37)
	featureBodySource       = uint8(41)
)

func newFeatureRasterClient(t *testing.T) (*Client, *formats.GAFFrame, *formats.GAFFrame) {
	t.Helper()
	c, err := New(Options{Width: 8, Height: 8})
	if err != nil {
		t.Fatal(err)
	}
	c.modelFS = vfs.New()
	shadow := &formats.GAFFrame{
		Width: 2, Height: 1, XOffset: 2, YOffset: 1,
		ColorKey: 9, Pixels: []byte{featureShadowSource, 9},
		Transparent: []bool{false, true},
	}
	body := &formats.GAFFrame{
		Width: 2, Height: 1, XOffset: 2, YOffset: 1,
		ColorKey: 9, Pixels: []byte{featureBodySource, 9},
		Transparent: []bool{false, true},
	}
	c.featureGAFs["feature-shadow-fixture"] = &formats.GAF{Entries: []formats.GAFEntry{
		{Name: "shadow", Frames: []formats.GAFFrameRef{{Frame: shadow}}},
		{Name: "body", Frames: []formats.GAFFrameRef{{Frame: body}}},
		{Name: "event-shadow", Frames: []formats.GAFFrameRef{{Frame: shadow}}},
		{Name: "event-body", Frames: []formats.GAFFrameRef{{Frame: body}}},
		{Name: "transparent-body", Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{
			Width: 2, Height: 1, XOffset: 2, YOffset: 1,
			ColorKey: 9, Pixels: []byte{9, 9}, Transparent: []bool{true, true},
		}}}},
	}}
	p := &palette.Tables{}
	p.Alpha[int(featureShadowSource)*256+int(featureRasterBackground)] = 88
	p.Alpha[int(featureBodySource)*256+int(featureRasterBackground)] = 99
	c.SetPalette(p)
	return c, shadow, body
}

func resetFeatureRaster(c *Client) {
	c.resetListForTest()
	for i := range c.indexed {
		c.indexed[i] = featureRasterBackground
	}
}

func featureRasterView() frame.FeatureView {
	return frame.FeatureView{
		X:           numeric.Fixed(4 << 16),
		Z:           numeric.Fixed(3 << 16),
		Filename:    "feature-shadow-fixture",
		SeqNameShad: "shadow",
	}
}

func featureRasterPixel(c *Client, x, y int) uint8 {
	return c.indexed[y*c.width+x]
}

// FeatureShadows is a direct option bit. Per-category preferences can leave it
// enabled while the model-shadow master, vehicle-shadow and Shading selectors
// are clear [03 §5.3][R-REN-03D §4].
func TestFeatureShadowGateIsDirectAndDefaultsEnabled(t *testing.T) {
	c, _, _ := newFeatureRasterClient(t)
	if !c.FeatureShadows() {
		t.Fatal("new client disabled feature shadows; the display default is enabled")
	}
	c.SetShadowOptions(false, false, false)
	t.Cleanup(func() { c.SetShadowOptions(true, true, true) })
	resetFeatureRaster(c)
	v := featureRasterView()
	c.drawFeature(&v)
	c.replayForTest()
	if got := featureRasterPixel(c, 2, 2); got != featureShadowSource {
		t.Fatalf("feature shadow with model-shadow and Shading bits clear = %d, want keyed source %d", got, featureShadowSource)
	}

	c.SetFeatureShadows(false)
	c.SetShadowOptions(true, true, true)
	resetFeatureRaster(c)
	c.drawFeature(&v)
	c.replayForTest()
	if got := featureRasterPixel(c, 2, 2); got != featureRasterBackground {
		t.Fatalf("disabled feature-shadow bit wrote %d, want untouched %d", got, featureRasterBackground)
	}
}

// Static feature shadows select one of two complete blitters. shadtrans=0
// copies each non-key source byte; shadtrans=1 uses
// ALP[source*256+destination]. Both preserve the authored anchor geometry and
// transparent pixels write nothing [03 R-RAST-01 §6][03 §5.3.1]
// [R-REN-03D §4].
func TestFeatureShadowRasterFamiliesUseSourceIndices(t *testing.T) {
	c, _, _ := newFeatureRasterClient(t)
	for _, tc := range []struct {
		name  string
		trans bool
		want  uint8
	}{
		{name: "opaque keyed", want: featureShadowSource},
		{name: "translucent ALP", trans: true, want: 88},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetFeatureRaster(c)
			v := featureRasterView()
			v.ShadTrans = tc.trans
			c.drawFeature(&v)
			c.replayForTest()
			if got := featureRasterPixel(c, 2, 2); got != tc.want {
				t.Fatalf("shadow pixel = %d, want %d", got, tc.want)
			}
			if got := featureRasterPixel(c, 3, 2); got != featureRasterBackground {
				t.Fatalf("transparent shadow texel wrote %d, want untouched %d", got, featureRasterBackground)
			}
			if got := featureRasterPixel(c, 4, 3); got != featureRasterBackground {
				t.Fatalf("shadow landed at anchor instead of authored top-left: pixel = %d", got)
			}
		})
	}
}

// A static body follows animtrans, while both cursor frames of a running live
// event remain opaque even when the definition carries animtrans/shadtrans
// [03 R-RAST-01 §6].
func TestFeatureStaticAndLiveEventRasterSelection(t *testing.T) {
	c, _, _ := newFeatureRasterClient(t)

	resetFeatureRaster(c)
	static := featureRasterView()
	static.SeqNameShad = ""
	static.SeqName = "body"
	static.AnimTrans = true
	c.drawFeature(&static)
	c.replayForTest()
	if got := featureRasterPixel(c, 2, 2); got != 99 {
		t.Fatalf("static animtrans body pixel = %d, want ALP result 99", got)
	}

	resetFeatureRaster(c)
	liveShadow := featureRasterView()
	liveShadow.EventSeqName = "transparent-body"
	liveShadow.EventSeqNameShad = "event-shadow"
	liveShadow.RuntimeLive = true
	liveShadow.ShadowEnabled = true
	liveShadow.ShadTrans = true
	c.drawFeature(&liveShadow)
	c.replayForTest()
	if got := featureRasterPixel(c, 2, 2); got != featureShadowSource {
		t.Fatalf("live event shadow pixel = %d, want opaque source %d", got, featureShadowSource)
	}

	resetFeatureRaster(c)
	liveBody := featureRasterView()
	liveBody.SeqNameShad = ""
	liveBody.EventSeqName = "event-body"
	liveBody.RuntimeLive = true
	liveBody.AnimTrans = true
	c.drawFeature(&liveBody)
	c.replayForTest()
	if got := featureRasterPixel(c, 2, 2); got != featureBodySource {
		t.Fatalf("live event body pixel = %d, want opaque source %d", got, featureBodySource)
	}
}
