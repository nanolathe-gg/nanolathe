package model

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

func orderedObject(vertices []int32, faces ...[]uint16) formats.ThreeDOObject {
	result := formats.ThreeDOObject{Version: 1, Name: "base", Selection: -1, Parent: -1}
	for _, y := range vertices {
		result.Vertices = append(result.Vertices, formats.ThreeDOVertex{Y: y})
	}
	for sourceIndex, face := range faces {
		result.Primitives = append(result.Primitives, formats.ThreeDOPrimitive{ColorIndex: uint32(sourceIndex), VertexIndices: face})
	}
	return result
}

func primitiveSources(piece Piece) []int {
	got := make([]int, len(piece.Primitives))
	for i, primitive := range piece.Primitives {
		got[i] = int(primitive.SourceIndex)
	}
	return got
}

func TestBuildModelCompilesSelectionAndStablePrimitiveOrder(t *testing.T) {
	object := orderedObject(
		[]int32{20, -3, 0, 50},
		[]uint16{0},       // source 0: mean 20
		[]uint16{1, 2},    // source 1: -3/2 truncates toward zero to -1
		[]uint16{3, 3, 3}, // source 2: selected
		[]uint16{0},       // source 3: equal key; remains after source 0
	)
	object.Selection = 2
	if mean, err := primitiveMeanY(object, 1); err != nil || mean != -1 {
		t.Fatalf("negative mean = %d, %v; want truncation toward zero to -1", mean, err)
	}
	authored := object
	authored.Primitives = append([]formats.ThreeDOPrimitive(nil), object.Primitives...)
	model, err := buildModel(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{object}}, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := primitiveSources(model.Pieces[0]), []int{2, 1, 0, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("compiled source order=%v, want %v", got, want)
	}
	if !model.Pieces[0].Selection {
		t.Fatal("compiled selection missing")
	}
	plates := model.SelectionPrimitives()
	if len(plates) != 1 || plates[0].PrimitiveIndex != 0 || plates[0].SourcePrimitive != 2 || plates[0].Primitive.SourceIndex != 2 {
		t.Fatalf("selection identity=%+v, want compiled slot 0 / authored source 2", plates)
	}
	if !reflect.DeepEqual(object, authored) {
		t.Fatalf("compile mutated authored primitive source: got %+v want %+v", object, authored)
	}
}

func TestBuildModelUsesWrappingSignedMean(t *testing.T) {
	object := orderedObject(
		[]int32{0x7fffffff, 1, 0},
		[]uint16{0},    // slot zero is excluded
		[]uint16{2},    // key 0
		[]uint16{0, 1}, // wrapping key -1073741824
	)
	model, err := buildModel(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{object}}, "wrap")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := primitiveSources(model.Pieces[0]), []int{0, 2, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapping sort source order=%v, want %v", got, want)
	}
}

func TestBuildModelDoesNotCompareTwoPrimitiveObject(t *testing.T) {
	object := orderedObject([]int32{0}, []uint16{0}, nil)
	model, err := buildModel(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{object}}, "two-faces")
	if err != nil {
		t.Fatalf("two primitive object acquired an ordering divide requirement: %v", err)
	}
	if got, want := primitiveSources(model.Pieces[0]), []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("two primitive source order=%v, want %v", got, want)
	}
}

func TestBuildModelDoesNotSelectEmptyPrimitiveTable(t *testing.T) {
	object := orderedObject([]int32{0})
	object.Selection = 0
	model, err := buildModel(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{object}}, "empty-selection")
	if err != nil {
		t.Fatal(err)
	}
	if model.Pieces[0].Selection || len(model.Pieces[0].Primitives) != 0 {
		t.Fatalf("empty primitive table compiled as selection=%t primitives=%d", model.Pieces[0].Selection, len(model.Pieces[0].Primitives))
	}
}

func TestBuildModelRejectsComparedMalformedPrimitive(t *testing.T) {
	object := orderedObject([]int32{0}, []uint16{0}, nil, []uint16{0})
	_, err := buildModel(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{object}}, "malformed")
	if err == nil || !strings.Contains(err.Error(), "zero vertex indexes") {
		t.Fatalf("error=%v, want explicit zero-count ordering diagnostic", err)
	}
}

func TestBuildModelRejectsInvalidSelection(t *testing.T) {
	object := orderedObject([]int32{0}, []uint16{0})
	object.Selection = 9
	_, err := buildModel(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{object}}, "bad-selection")
	if err == nil || !strings.Contains(err.Error(), "selection primitive 9") {
		t.Fatalf("error=%v, want explicit selection diagnostic", err)
	}
}
