package parity

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStableHashSortsMapsAndPreservesFloatBits(t *testing.T) {
	a := map[string]any{"b": float32(1), "a": float32(0)}
	b := map[string]any{"a": float32(0), "b": float32(1)}
	ha, err := StableHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := StableHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatal("map insertion order changed parity hash")
	}
	hz, err := StableHash(float32(0))
	if err != nil {
		t.Fatal(err)
	}
	hn, err := StableHash(float32(1.4012985e-45))
	if err != nil {
		t.Fatal(err)
	}
	if hz == hn {
		t.Fatal("distinct float payloads aliased")
	}
}

func TestStableHashRejectsUnsupportedAndCyclicValues(t *testing.T) {
	if _, err := StableHash(func() {}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("function should be rejected, got %v", err)
	}
	type node struct{ Next *node }
	n := &node{}
	n.Next = n
	if _, err := StableHash(n); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("pointer cycle should be rejected, got %v", err)
	}
	loop := make([]any, 1)
	loop[0] = loop
	if _, err := StableHash(loop); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("slice cycle should be rejected, got %v", err)
	}
}

func TestStableHashRejectsNonFiniteFloats(t *testing.T) {
	for _, value := range []any{
		float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1)),
		math.NaN(), math.Inf(1), math.Inf(-1),
	} {
		if _, err := StableHash(value); err == nil || !strings.Contains(err.Error(), "non-finite") {
			t.Fatalf("accepted non-finite value %v: %v", value, err)
		}
	}
}

func TestStableHashPreservesWrappersAndConcreteTypes(t *testing.T) {
	var nilPointer *int
	hNil, err := StableHash(nil)
	if err != nil {
		t.Fatal(err)
	}
	hTypedNil, err := StableHash(nilPointer)
	if err != nil {
		t.Fatal(err)
	}
	if hNil == hTypedNil {
		t.Fatal("typed nil pointer collided with untyped nil")
	}
	value := 1
	hPointer, err := StableHash(&value)
	if err != nil {
		t.Fatal(err)
	}
	hValue, err := StableHash(value)
	if err != nil {
		t.Fatal(err)
	}
	if hPointer == hValue {
		t.Fatal("pointer wrapper collided with concrete value")
	}
	type interfaceHolder struct{ Value any }
	intHolder, err := StableHash(interfaceHolder{Value: int32(1)})
	if err != nil {
		t.Fatal(err)
	}
	uintHolder, err := StableHash(interfaceHolder{Value: uint32(1)})
	if err != nil {
		t.Fatal(err)
	}
	if intHolder == uintHolder {
		t.Fatal("interface concrete types collided")
	}
}

func TestCaptureRequiresExactFramebufferDimensions(t *testing.T) {
	b := NewBundle("P28-OBS-00A", "sha", "content", "map", 7)
	for _, in := range []Input{
		{Width: 2, Height: 2, IndexedFramebuffer: []byte{1}},
		{Width: 2, Height: 2, RGBAFramebuffer: make([]byte, 15)},
		{Width: 2, Height: 0, IndexedFramebuffer: []byte{1, 2}},
		{Width: 0, Height: 0, IndexedFramebuffer: []byte{1}},
	} {
		if err := b.Capture(in); err == nil {
			t.Fatalf("accepted malformed framebuffer input: %#v", in)
		}
	}
	if err := b.Capture(Input{Width: 2, Height: 2, IndexedFramebuffer: make([]byte, 4)}); err != nil {
		t.Fatal(err)
	}
	if err := b.Capture(Input{Width: 2, Height: 2, RGBAFramebuffer: make([]byte, 16)}); err != nil {
		t.Fatal(err)
	}
}

func TestCropRecordsOutOfBoundsAndExactCrop(t *testing.T) {
	pixels := make([]byte, 4*4*4)
	c, err := CropRGBAExact("part", pixels, 4, 4, 3, 3, 2, 2)
	if err == nil || c.BoundsError == "" || len(c.Pixels) != 4 {
		t.Fatalf("out-of-bounds crop was silent or wrong: crop=%+v err=%v", c, err)
	}
	c = CropRGBA("part", pixels, 4, 4, 1, 1, 2, 2)
	if c.BoundsError != "" || len(c.Pixels) != 16 || c.Hash == "" {
		t.Fatalf("exact crop was not captured: %+v", c)
	}
}

func TestBundleCaptureCopiesNestedRecordsAndWritesEvidence(t *testing.T) {
	b := NewBundle("P28-OBS-00A", "sha", "content", "map", 7)
	pixels := make([]byte, 2*2*4)
	route := []int32{1, 2}
	cropPixels := []byte{1, 2}
	if err := b.Capture(Input{
		Tick: 4, Width: 2, Height: 2,
		Authoritative: struct{ N int }{1}, Frame: struct{ N int }{2},
		RGBAFramebuffer: pixels,
		Movement:        []Movement{{CurrentRoute: route}},
		Crops:           []Crop{{Name: "hud", Pixels: cropPixels}},
	}); err != nil {
		t.Fatal(err)
	}
	pixels[0], route[0], cropPixels[0] = 9, 9, 9
	if b.Captures[0].RGBAFramebuffer[0] != 0 || b.Captures[0].Movement[0].CurrentRoute[0] != 1 || b.Captures[0].Crops[0].Pixels[0] != 1 {
		t.Fatal("capture did not deep-copy evidence values")
	}
	dir := t.TempDir()
	if err := b.WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bundle.json", "frame-000004.json", "frame-000004.png", "frame-000004-hud.crop"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing evidence %s: %v", name, err)
		}
	}
	if !reflect.DeepEqual(b.Captures[0].Handles, []Handle(nil)) {
		t.Fatal("unexpected handle mutation")
	}
}

func TestWriteDirRejectsUnsafeCropName(t *testing.T) {
	b := NewBundle("P28-OBS-00A", "sha", "content", "map", 7)
	if err := b.Capture(Input{Crops: []Crop{{Name: "../outside", Pixels: []byte{1}}}}); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteDir(t.TempDir()); err == nil {
		t.Fatal("unsafe crop name was accepted")
	}
}

func TestWriteDirRejectsDuplicateCropOutputsBeforeWriting(t *testing.T) {
	b := NewBundle("P28-OBS-00A", "sha", "content", "map", 7)
	if err := b.Capture(Input{Crops: []Crop{
		{Name: "Solar", Pixels: []byte{1}},
		{Name: "solar", Pixels: []byte{2}},
	}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := b.WriteDir(dir); err == nil || !strings.Contains(err.Error(), "duplicate evidence output") {
		t.Fatalf("duplicate crop output was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bundle.json")); !os.IsNotExist(err) {
		t.Fatalf("preflight failure wrote bundle.json: %v", err)
	}
}

func TestWriteDirRejectsExistingOutputBeforeWriting(t *testing.T) {
	b := NewBundle("P28-OBS-00A", "sha", "content", "map", 7)
	if err := b.Capture(Input{Crops: []Crop{{Name: "hud", Pixels: []byte{1}}}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteDir(dir); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing output was overwritten: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "bundle.json"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing output changed: %q, %v", data, err)
	}
}
