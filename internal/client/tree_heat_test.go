package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// The modern experiment uses the published fire flag, not an animation name;
// death/reclaim art and hidden fire must not become heat emitters (GPU design §27).
func TestTreeHeatRequiresModernVisibleBurningBody(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		modern, visible, burning bool
		seq                      string
		want                     bool
	}{
		{"burning", true, true, true, "event-body", true},
		{"hidden", true, false, true, "event-body", false},
		{"reclaim", true, true, false, "event-body", false},
		{"classic", false, true, true, "event-body", false},
		{"missing art", true, true, true, "absent", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newFeatureRasterClient(t)
			buf := &frame.Buffer{}
			*buf.BeginWrite() = *stripTestFrame(tc.visible)
			if err := buf.Publish(42); err != nil {
				t.Fatal(err)
			}
			c.buffer, c.enhanced, c.frameTick = buf, tc.modern, 42
			c.interpolation = true
			c.SetTickFraction(0.5)
			f := featureRasterView()
			f.RuntimeLive, f.ShadowEnabled, f.IsBurning = true, true, tc.burning
			f.EventSeqName, f.EventSeqNameShad = tc.seq, "event-shadow"
			c.drawFeature(&f)
			found := false
			c.list.VisitSprites(func(sp drawlist.Sprite) {
				if sp.HeatSource {
					found = true
					if sp.Kind != drawlist.BlitFeatureNormal || sp.HeatTime != 42.5 || sp.LightingScale != 1 {
						t.Fatalf("invalid heat metadata: %+v", sp)
					}
				}
			})
			if found != tc.want {
				t.Fatalf("heat source = %v, want %v", found, tc.want)
			}
		})
	}
}
