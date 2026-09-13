package client

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The image blit uses source-major ALP, after the shadow, and keys on the
// transparent index at both native and detail scales [03 R-REN-03D §4].
func TestCloakedBodyALPCommitAndClone(t *testing.T) {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		c := &Client{width: 8, height: 2, indexed: bytes.Repeat([]byte{7}, 16), pal: &palette.Tables{}}
		c.pal.Alpha[0*256+7] = 8
		c.pal.Alpha[9*256+8] = 42
		c.pal.Alpha[7*256+9] = 43
		body := classicReplayImage([]byte{9, 1}, []bool{false, true}, nil, 0, 0)
		body.Blit = scale
		target := classicModelTarget(body)
		packet := c.classicModelForCommit(pendingModelCommit{m: composedModel{cloaked: true}, blit: target, body: true})
		packet.Shadow = classicReplayImage([]byte{0}, []bool{true}, nil, 0, 0)
		packet.Shadow.Blit = scale
		packet = packet.Clone()
		c.classicSink().Model(drawlist.Model{Classic: packet})
		if c.indexed[0] != 42 {
			t.Fatalf("scale %v: source-major ALP = %d, want 42", scale, c.indexed[0])
		}
		if c.indexed[scale.Project(1)] != 7 {
			t.Fatal("transparent body pixel overwrote terrain")
		}
	}
}

// Cloak belongs to the current image commit, not the cached raster: stationary
// units change appearance immediately and other instances remain opaque
// [03 R-RAST-01 §7].
func TestCloakChangesRetainedBodyCommit(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.pal = &palette.Tables{}
	c.pal.Alpha[99*256] = 42
	cachedLiveReplay(t, c, v)
	base := c.cachedModelBodies[v.InstanceID]
	if bytes.Count(c.indexed, []byte{99}) == 0 {
		t.Fatal("opaque body missing")
	}
	v.Cloaked = true
	cachedLiveReplay(t, c, v)
	if c.cachedModelBodies[v.InstanceID] != base {
		t.Fatal("cloak rebuilt cached body")
	}
	if bytes.Count(c.indexed, []byte{42}) == 0 || bytes.Count(c.indexed, []byte{99}) != 0 {
		t.Fatal("cloaked body was opaque")
	}
	other := v
	other.InstanceID++
	other.Slot++
	other.Cloaked = false
	cachedLiveReplay(t, c, other)
	if bytes.Count(c.indexed, []byte{99}) == 0 {
		t.Fatal("shared model tinted another instance")
	}
	v.Cloaked = false
	cachedLiveReplay(t, c, v)
	if bytes.Count(c.indexed, []byte{99}) == 0 || c.cachedModelBodies[v.InstanceID] != base {
		t.Fatal("decloak failed to restore opaque cached body")
	}
}

func TestCloakGeometryRefreshAndDirectLiveLane(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.geometryOnlyModels = true
	c.modelScratch.active = true
	v.ZBuffer = false
	v.BMCode = true
	for _, cloak := range []bool{false, true, false} {
		c.modelScratch.reset()
		v.Cloaked = cloak
		body, live := c.unitGeometryPair(v, false)
		if body == nil || body.Cloaked != cloak {
			t.Fatal("current cloak state missing from body commit")
		}
		if live == nil || live.Cloaked {
			t.Fatal("direct live polygon lane inherited image blit cloak")
		}
	}
}

// A modern detail preview must magnify geometry, while the following classic
// call returns to the ordinary indexed-image blit mode (GPU design §14.2).
func TestCloakPreviewDetailGeometry(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	preview, err := NewModelPreviewRenderer(fs)
	if err != nil {
		t.Fatal(err)
	}
	opts := ModelPreviewOptions{Model: "armcom", Width: 192, Height: 160, Scale: 1, KeyPlane: true, Cloaked: true}
	native, err := preview.RecordGeometry(opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Scale = 2
	detail, err := preview.RecordGeometry(opts)
	if err != nil {
		t.Fatal(err)
	}
	n, d := native.List.ModelCommands()[0].Geometry, detail.List.ModelCommands()[0].Geometry
	// Composition includes two margin pixels at each edge; those fixed image
	// margins do not magnify with the geometry [03 R-REN-03A §1].
	if d.Width < n.Width*2-4 || d.Height < n.Height*2-4 {
		t.Fatalf("detail geometry %dx%d did not magnify native %dx%d", d.Width, d.Height, n.Width, n.Height)
	}
	if !n.Cloaked || !d.Cloaked {
		t.Fatal("preview dropped cloak")
	}
	if preview.client.enhanced {
		t.Fatal("modern preview leaked executor mode")
	}
	classic, err := preview.RecordModel(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !classic.List.ModelCommands()[0].Classic.Cloaked {
		t.Fatal("classic preview dropped cloak")
	}
}
