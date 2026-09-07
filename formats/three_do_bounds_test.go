package formats

import (
	"strings"
	"testing"
)

func TestThreeDOAggregateIndexBudget(t *testing.T) {
	model := &ThreeDO{Root: 0, Objects: []ThreeDOObject{{
		Version: 1, Name: "root", Selection: -1, Parent: -1, FirstChild: -1, NextSibling: -1,
		Vertices:   []ThreeDOVertex{{}},
		Primitives: []ThreeDOPrimitive{{VertexIndices: []uint16{0}}, {VertexIndices: []uint16{0}}},
	}}}
	data, err := EncodeThreeDO(model)
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultThreeDOLimits()
	limits.MaxIndices = 1
	if _, err := LoadThreeDOWithLimits(data, limits); err == nil || !strings.Contains(err.Error(), "aggregate polygon indexes") {
		t.Fatalf("index budget error = %v", err)
	}
}

func TestThreeDOLongSiblingChainUsesBoundedCallDepth(t *testing.T) {
	const siblings = 4096
	objects := make([]ThreeDOObject, siblings+1)
	objects[0] = ThreeDOObject{Version: 1, Name: "root", Selection: -1, Parent: -1, FirstChild: 1, NextSibling: -1}
	for i := 1; i <= siblings; i++ {
		next := int32(-1)
		if i != siblings {
			next = int32(i + 1)
		}
		objects[i] = ThreeDOObject{Version: 1, Name: "piece", Selection: -1, Parent: 0, FirstChild: -1, NextSibling: next}
	}
	data, err := EncodeThreeDO(&ThreeDO{Root: 0, Objects: objects})
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultThreeDOLimits()
	limits.MaxDepth = 1
	limits.MaxObjects = siblings + 1
	got, err := LoadThreeDOWithLimits(data, limits)
	if err != nil {
		t.Fatalf("long sibling chain: %v", err)
	}
	if len(got.Objects) != siblings+1 || got.Objects[1].Parent != 0 || got.Objects[siblings].NextSibling != -1 {
		t.Fatalf("sibling chain topology changed: objects=%d first=%+v last=%+v", len(got.Objects), got.Objects[1], got.Objects[siblings])
	}
}
