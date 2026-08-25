package save

import (
	"testing"
)

// TestRS10_ConstructionLinksRoundTrip verifies builder-product links survive Marshal round-trip [RS-10][05 C18].
func TestRS10_ConstructionLinksRoundTrip(t *testing.T) {
	st := &StateV1{
		Version:      StateV1VersionConst,
		CatalogHash:  "cat",
		ManifestHash: "man",
		Construction: ConstructionSnapshot{
			BuilderLinks: []BuilderLinkRecord{
				{Builder: 5, Product: 10},
				{Builder: 2, Product: 3},
				{Builder: 7, Product: 10}, // duplicate product should be last-wins? Our code keeps last, but test sort: product 3 then 10
			},
		},
	}
	// Ensure deterministic sorting: after marshal, links sorted by Product then Builder
	data := MarshalStateV1(st)
	decoded, err := UnmarshalStateV1(data, "cat", "man")
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(decoded.Construction.BuilderLinks) != 3 {
		t.Fatalf("links len %d want 3", len(decoded.Construction.BuilderLinks))
	}
	// Sorted order should be (2,3), (5,10), (7,10) => product 3 first, then product 10 sorted by builder
	if decoded.Construction.BuilderLinks[0].Product != 3 || decoded.Construction.BuilderLinks[0].Builder != 2 {
		t.Fatalf("first link wrong %+v", decoded.Construction.BuilderLinks[0])
	}
	if decoded.Construction.BuilderLinks[1].Product != 10 || decoded.Construction.BuilderLinks[1].Builder != 5 {
		t.Fatalf("second link wrong %+v", decoded.Construction.BuilderLinks[1])
	}
	if decoded.Construction.BuilderLinks[2].Product != 10 || decoded.Construction.BuilderLinks[2].Builder != 7 {
		t.Fatalf("third link wrong %+v", decoded.Construction.BuilderLinks[2])
	}
	// Empty construction snapshot round-trip for version 6 (should be 0 len, not nil panic)
	st2 := &StateV1{Version: StateV1VersionConst, CatalogHash: "cat", ManifestHash: "man"}
	data2 := MarshalStateV1(st2)
	decoded2, err := UnmarshalStateV1(data2, "cat", "man")
	if err != nil {
		t.Fatalf("Unmarshal empty: %v", err)
	}
	if len(decoded2.Construction.BuilderLinks) != 0 {
		t.Fatalf("empty links should remain empty, got %d", len(decoded2.Construction.BuilderLinks))
	}
}

// TestRS10_BackwardCompatVersion5 ensures version 5 saves without construction still decode [RS-10].
func TestRS10_BackwardCompatVersion5(t *testing.T) {
	st := &StateV1{
		Version:      StateV1Version5,
		CatalogHash:  "cat",
		ManifestHash: "man",
		Units:        []UnitRecord{{Slot: 1, DefName: "armcom", Owner: 0, Remaining: 0.5}},
	}
	data := MarshalStateV1(st)
	// Data version should be 5 and contain no construction bytes
	decoded, err := UnmarshalStateV1(data, "cat", "man")
	if err != nil {
		t.Fatalf("Unmarshal v5: %v", err)
	}
	if decoded.Version != StateV1Version5 {
		t.Fatalf("version mismatch got %d want %d", decoded.Version, StateV1Version5)
	}
	if len(decoded.Construction.BuilderLinks) != 0 {
		t.Fatalf("v5 should have no construction links")
	}
	if len(decoded.Units) != 1 || decoded.Units[0].Remaining != 0.5 {
		t.Fatalf("units not preserved across v5")
	}
	// Also ensure v5 data can be read by v6 decoder (already tested) and vice versa? v6 empty construction should be readable as v5? Not needed.
}
