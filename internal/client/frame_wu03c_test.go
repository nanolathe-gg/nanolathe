package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

func TestLiveComposerAdapterOrder(t *testing.T) {
	c, err := New(Options{Width: 32, Height: 32, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCamera(&camera.Camera{ViewW: 32, ViewH: 32, MapW: 256, MapH: 256})
	frame := &snapshot.Frame{Tick: 1, Visibility: snapshot.VisibilityView{Valid: false}}
	c.composeIndexed(1, frame, frame, true)
	want := []string{"preworld", "terrain", "minimap", "clip", "strip0", "strip1", "strip2", "bucket", "features", "strip3", "strip4", "units", "strip5", "strip6", "projectiles", "effects", "strip7", "strip8", "strip9", "fog", "selection", "interface"}
	if !reflect.DeepEqual(c.operationTrace, want) {
		t.Fatalf("live adapter order = %v, want %v", c.operationTrace, want)
	}
}

func TestSelectionChromeIsAfterFog(t *testing.T) {
	c, err := New(Options{Width: 32, Height: 32, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCamera(&camera.Camera{ViewW: 32, ViewH: 32, MapW: 256, MapH: 256})
	c.ensureComposer()
	c.composeCur = &snapshot.Frame{Tick: 1}
	c.composeOK = true
	c.selectionChrome = []selectionChrome{{
		view:    snapshot.UnitView{FootX: 1, FootZ: 1, Flags: SelectionFlag},
		screenX: 12, screenY: 12,
	}}
	c.composer.Hooks.Fog = func() {
		c.traceAdapter("fog")
		// Simulate the fog adapter's final world overwrite at a selection
		// corner; the real selection stage must replace it afterward.
		c.indexed[4*c.width+4] = 1
	}
	c.operationTrace = nil
	c.composer.Frame(c.composeCur, 1, 1)
	if c.indexed[4*c.width+4] != unitStyle.SelectWhite {
		t.Fatalf("selection chrome did not remain above fog: pixel=%d style=%d trace=%v", c.indexed[4*c.width+4], unitStyle.SelectWhite, c.operationTrace)
	}
	fog, selection := -1, -1
	for i, op := range c.operationTrace {
		if op == "fog" {
			fog = i
		}
		if op == "selection" {
			selection = i
		}
	}
	if fog < 0 || selection < 0 || fog >= selection {
		t.Fatalf("stage trace does not place selection after fog: %v", c.operationTrace)
	}
}

// TestFrameReplayDoesNotRepeatTickPresentation proves that repainting one
// immutable snapshot does not consume a second shake event or CRT pair. The
// camera displacement is permanent and is the same origin used by projection
// on the first tick [03 §5.6][I4][I6].
func TestFrameReplayDoesNotRepeatTickPresentation(t *testing.T) {
	c, err := New(Options{Width: 64, Height: 64, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	cam := &camera.Camera{X: 500, Z: 500, ViewW: 64, ViewH: 64, MapW: 2000, MapH: 2000}
	c.SetCamera(cam)
	c.SetPresentationCRT(presentation.NewCRTRandom(1))
	frame := &snapshot.Frame{Tick: 1, Events: []snapshot.EventView{{
		ID: 7, Sequence: 11, Tick: 1, Kind: snapshot.EventKindShake,
		Magnitude: 32, Lifetime: 4,
	}}}

	c.composeIndexed(1, frame, frame, true)
	draws := c.crt.Draws()
	x, z := cam.X, cam.Z
	if draws != 2 {
		t.Fatalf("first tick CRT draws %d, want 2", draws)
	}
	ledger := c.crt.Ledger()
	if len(ledger) != 2 || ledger[0].Consumer != "shake-x" || ledger[1].Consumer != "shake-y" {
		t.Fatalf("shake CRT ledger = %#v, want labeled x/y pair", ledger)
	}

	c.composeIndexed(1, frame, frame, true)
	if c.crt.Draws() != draws || cam.X != x || cam.Z != z {
		t.Fatalf("replayed snapshot advanced presentation: draws %d/%d camera %d,%d -> %d,%d", c.crt.Draws(), draws, x, z, cam.X, cam.Z)
	}

	next := &snapshot.Frame{Tick: 2, Events: []snapshot.EventView{{
		ID: 8, Sequence: 12, Tick: 2, Kind: snapshot.EventKindShake,
		Magnitude: 32, Lifetime: 4,
	}}}
	c.composeIndexed(1, frame, next, true)
	if c.crt.Draws() != draws+2 {
		t.Fatalf("next simulation tick CRT draws %d, want %d", c.crt.Draws(), draws+2)
	}
	if cam.X == x && cam.Z == z {
		t.Fatal("same-tick shake did not mutate the real camera origin")
	}
}

func TestProjectileSegmentedUsesSharedCRTLedger(t *testing.T) {
	c := &Client{}
	c.SetPresentationCRT(presentation.NewCRTRandom(1))
	v := snapshot.ProjectileView{
		X:     numeric.Fixed(20 << 16),
		TailX: 0,
	}
	first, second, ok := c.projectileDispatchOptions().SegmentPoints(v)
	if !ok || len(first) != 5 || len(second) != 5 {
		t.Fatalf("segmented passes = (%d, %d, %v), want (5, 5, true)", len(first), len(second), ok)
	}
	ledger := c.crt.Ledger()
	if len(ledger) != 18 {
		t.Fatalf("segmented ledger length = %d, want 18", len(ledger))
	}
	for i, entry := range ledger {
		if entry.Consumer != "projectile-segmented" || entry.Draw != uint64(i+1) {
			t.Fatalf("ledger[%d] = %+v, want projectile-segmented draw %d", i, entry, i+1)
		}
	}
}
