package client

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
)

func TestLoadedModelTextureRegistrySharesOneLoadAndSeparatesPrimitives(t *testing.T) {
	entry := &formats.GAFEntry{Name: "animated", Frames: []formats.GAFFrameRef{
		{Value: 1, Frame: &formats.GAFFrame{Pixels: []byte{1}}},
		{Value: 1, Frame: &formats.GAFFrame{Pixels: []byte{2}}},
	}}
	ref := texRef{kind: texAnimated, key: "textures/default.gaf|animated", entry: entry, frame: entry.Frames[0].Frame}
	r := newModelTextureRegistry(nil, false)
	first, second := &compiledmodel.Model{}, &compiledmodel.Model{}
	firstLoad := modelTextureLoadKey{kind: modelLoadUnit, id: "first"}
	secondLoad := modelTextureLoadKey{kind: modelLoadUnit, id: "second"}
	r.byCompiled[first], r.byCompiled[second] = firstLoad, secondLoad
	firstPrimitive := modelTexturePrimitiveKey{load: firstLoad, piece: 0, primitive: 0}
	secondPrimitive := modelTexturePrimitiveKey{load: firstLoad, piece: 0, primitive: 1}
	secondLoadPrimitive := modelTexturePrimitiveKey{load: secondLoad, piece: 0, primitive: 0}
	r.bindings[firstPrimitive] = newModelTextureCursor(ref)
	r.bindings[secondPrimitive] = newModelTextureCursor(ref)
	r.bindings[secondLoadPrimitive] = newModelTextureCursor(ref)
	r.players = []phase7Stepper{r.bindings[firstPrimitive], r.bindings[secondPrimitive], r.bindings[secondLoadPrimitive]}

	// Two runtime instances resolve the same loaded primitive. Their identity is
	// deliberately absent from this API, so neither birth nor visibility can
	// allocate a fresh phase [03 R-CRD-005 §1].
	if got := r.animatedFrame(first, 0, 0, ref); got != entry.Frames[0].Frame {
		t.Fatal("loaded primitive did not begin at frame zero")
	}
	r.StepPhase7()
	if got := r.animatedFrame(first, 0, 0, ref); got != entry.Frames[1].Frame {
		t.Fatal("shared loaded primitive did not advance")
	}
	if got := r.animatedFrame(first, 0, 1, ref); got != entry.Frames[1].Frame {
		t.Fatal("separate primitive was not advanced by registry")
	}
	if got := r.animatedFrame(second, 0, 0, ref); got != entry.Frames[1].Frame {
		t.Fatal("distinct loaded model was not advanced independently")
	}
	if r.bindings[firstPrimitive] == r.bindings[secondPrimitive] || r.bindings[firstPrimitive] == r.bindings[secondLoadPrimitive] {
		t.Fatal("primitive or model-load cursors were aliased")
	}
}

func TestTextureResolutionUsesSideBeforeFallback(t *testing.T) {
	side := map[string]texRef{"panel": {key: "side|panel"}}
	fallback := map[string]texRef{"panel": {key: "default|panel"}}
	got, ok := resolveTextureRef(side, fallback, "PANEL")
	if !ok || got.key != "side|panel" {
		t.Fatalf("side texture did not win: %+v %v", got, ok)
	}
}

func TestCursorScaledDeltaIsIndependentOfSimulationTicks(t *testing.T) {
	entry := &formats.GAFEntry{FrameCount: 2, Frames: []formats.GAFFrameRef{
		{Value: 5, Frame: &formats.GAFFrame{Pixels: []byte{1}}},
		{Value: 5, Frame: &formats.GAFFrame{Pixels: []byte{2}}},
	}}
	cs := &Cursors{}
	cs.play.Bind(entry, 0, true)
	c := &Client{cursors: cs}
	c.StepCursorScaledDelta(0)
	if cs.play.Idx != 0 || cs.play.Countdown != 5 {
		t.Fatalf("zero scaled delta changed cursor: index=%d countdown=%d", cs.play.Idx, cs.play.Countdown)
	}
	c.StepCursorScaledDelta(1)
	if cs.play.Idx != 0 || cs.play.Countdown != 4 {
		t.Fatalf("one scaled unit: index=%d countdown=%d", cs.play.Idx, cs.play.Countdown)
	}
	c.StepCursorScaledDelta(3)
	if cs.play.Idx != 1 {
		t.Fatalf("scaled wall-clock delta did not advance cursor: index=%d", cs.play.Idx)
	}
}

type phase7RegistrySpy struct {
	name  string
	order *[]string
	add   func()
}

func (p *phase7RegistrySpy) stepPhase7() {
	*p.order = append(*p.order, p.name)
	if p.add != nil {
		p.add()
		p.add = nil
	}
}

func TestPhase7RegistryUsesDescendingSnapshotOrder(t *testing.T) {
	var order []string
	r := newModelTextureRegistry(nil, false)
	older := &phase7RegistrySpy{name: "older", order: &order}
	newer := &phase7RegistrySpy{name: "newer", order: &order}
	appended := &phase7RegistrySpy{name: "appended", order: &order}
	newer.add = func() { r.players = append(r.players, appended) }
	r.players = []phase7Stepper{older, newer}

	r.StepPhase7()
	if got, want := strings.Join(order, ","), "newer,older"; got != want {
		t.Fatalf("first phase-7 order = %q, want %q", got, want)
	}
	order = nil
	r.StepPhase7()
	if got, want := strings.Join(order, ","), "appended,newer,older"; got != want {
		t.Fatalf("second phase-7 order = %q, want %q", got, want)
	}
}

func TestModelTexturePlayersArePerPrimitive(t *testing.T) {
	c := &Client{}
	entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{
		{Value: 2, Frame: &formats.GAFFrame{Pixels: []byte{1}}},
		{Value: 2, Frame: &formats.GAFFrame{Pixels: []byte{2}}},
	}}
	ref := texRef{kind: texAnimated, key: "textures/default.gaf|animated", entry: entry}
	first := c.modelCursor(modelTextureKey{kind: modelCursorUnit, id: 7, tex: ref.key, piece: 0, primitive: 0}, ref)
	second := c.modelCursor(modelTextureKey{kind: modelCursorUnit, id: 7, tex: ref.key, piece: 0, primitive: 1}, ref)
	if first == second || len(c.modelPlayers) != 2 {
		t.Fatalf("same texture primitives shared playback: first=%p second=%p players=%d", first, second, len(c.modelPlayers))
	}
	first.player.Step()
	if got, _ := second.player.Frame(); got != content.AssetID(ref.key+"#0") {
		t.Fatalf("second primitive followed first cursor: %q", got)
	}
}
