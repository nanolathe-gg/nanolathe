package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// featureShadowClient is a composition client with one committed frame whose
// sea level is the only input the ordinal-0 suppression reads.
func featureShadowClient(t *testing.T, sea int32) *Client {
	t.Helper()
	c := compositionClient(t)
	c.shadows, c.vehicleShadows, c.shading = true, true, true
	c.buffer = frame.NewBuffer()
	w := c.buffer.BeginWrite()
	w.Visibility.SeaLevel = numeric.Fixed(sea) << 16
	if err := c.buffer.Publish(1); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestFeatureModelShadowGateSuppressesBelowSeaLevel locks the structure
// branch's extra predicate for the feature pseudo-unit: the branch is skipped
// when the definition ordinal is 0 and hi16(unitY) < seaLevel, and ordinal 0 is
// the feature pseudo-unit, so a 3DO wreck below the waterline casts no shadow
// while one at or above it does [03 R-REN-03D §1][03 R-RAST-01 §4].
//
// The identification of ordinal 0 with the feature pseudo-unit is the Supported
// inference stated in [03 R-REN-03D §1]; the branch and its predicate are
// Established. The comparison strictness is the part that is easy to regress:
// a wreck exactly at sea level still casts.
func TestFeatureModelShadowGateSuppressesBelowSeaLevel(t *testing.T) {
	const sea = 60
	c := featureShadowClient(t, sea)

	for _, tc := range []struct {
		name string
		y    numeric.Fixed
		want bool
	}{
		{"one world unit below sea level", numeric.Fixed(sea-1) << 16, false},
		{"far below sea level", numeric.Fixed(-40) << 16, false},
		{"a fraction below sea level", numeric.Fixed(sea)<<16 - 1, false},
		{"exactly at sea level", numeric.Fixed(sea) << 16, true},
		{"above sea level", numeric.Fixed(sea+20) << 16, true},
	} {
		if got := c.featureCastsModelShadow(tc.y); got != tc.want {
			t.Fatalf("featureCastsModelShadow(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	// The common gate still applies: clearing the master shadow bit suppresses
	// the feature shadow wherever it stands [03 R-REN-03D §1]. The feature
	// definition authors no `noshadow` key, and Digger, canhover and floater
	// are clear for every feature draw, so the master bit is the whole of the
	// common gate here.
	c.shadows = false
	if c.featureCastsModelShadow(numeric.Fixed(sea+20) << 16) {
		t.Fatal("master shadows off must suppress an above-water wreck's shadow")
	}
	c.shadows = true
	// The structure branch does not read the vehicle-shadow bit.
	c.vehicleShadows = false
	if !c.featureCastsModelShadow(numeric.Fixed(sea+20) << 16) {
		t.Fatal("the structure branch must not read the vehicle-shadow bit")
	}
}

// featureShadowModel is a raised quad: a flat one at the model origin would
// shear onto its own body and prove nothing about placement.
func featureShadowModel() *unitModel {
	return &unitModel{compiled: &compiledmodel.Model{
		Root: 0,
		Pieces: []compiledmodel.Piece{{
			Parent: -1,
			Vertices: [][3]numeric.Fixed{
				{0, 16 << 16, 0}, {24 << 16, 16 << 16, 0}, {24 << 16, 16 << 16, -24 << 16}, {0, 16 << 16, -24 << 16},
			},
			Primitives: []compiledmodel.Primitive{{ColorIndex: 9, IsColored: 1, VertexIndices: []uint16{0, 1, 2, 3}}},
		}},
	}}
}

// TestFeatureDrawRecordsStructureShadow is the end-to-end half: a 3DO feature
// draw must reach the classic sink with a shadow image above the waterline and
// with none below it. The feature pseudo-unit sets the structure class bit, so
// it takes the re-rasterized, punched structure branch rather than a
// silhouette copy [03 R-REN-03D §1][03 R-RAST-01 §6].
func TestFeatureDrawRecordsStructureShadow(t *testing.T) {
	const sea = 60
	shadowed := func(y numeric.Fixed) bool {
		c := featureShadowClient(t, sea)
		c.models = map[string]*unitModel{"wreck": featureShadowModel()}
		f := frame.FeatureView{InstanceID: 7, Model: "wreck", DefName: "wreck", X: 24 << 16, Y: y, Z: 24 << 16}
		if !c.drawFeatureModel(f) {
			t.Fatalf("feature at Y=%d did not draw", y.Floor())
		}
		found := false
		c.list.VisitModels(func(m drawlist.Model) {
			if m.Classic != nil && m.Classic.Shadow != nil {
				found = true
			}
		})
		return found
	}
	if !shadowed(numeric.Fixed(sea+20) << 16) {
		t.Fatal("a wreck above the waterline must record its structure shadow")
	}
	if shadowed(numeric.Fixed(sea-20) << 16) {
		t.Fatal("a wreck below the waterline must record no shadow")
	}
}
