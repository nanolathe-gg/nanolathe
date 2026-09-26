package world

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// TestPlacementLegalIsCheckPlacement locks the site-search form of the
// validator to the validator. Over queries that reach every gate CheckPlacement
// can refuse at, PlacementLegal says yes exactly where CheckPlacement returns
// no error, and both consult AdmitOccupant the same number of times: a search
// that asked a different question would choose sites the validator refuses
// when the order is carried out.
func TestPlacementLegalIsCheckPlacement(t *testing.T) {
	ter, queries := placementLegalCases()
	one, _ := NewFootprintExtent(1, 1)
	rect, _ := NewFootprintRect(NewFootprintAnchor(1, 1), one)
	type probe struct {
		ter *Terrain
		q   PlacementQuery
	}
	probes := []probe{
		{nil, PlacementQuery{Rect: rect, Mobile: true}},
		{&Terrain{CellW: 4, CellH: 4}, PlacementQuery{Rect: rect, Mobile: true}},
	}
	for _, q := range queries {
		probes = append(probes, probe{ter, q})
	}
	reached := map[placementReason]bool{}
	for i, p := range probes {
		q := p.q
		var calls [2]int
		counted := func(which int) PlacementQuery {
			c := q
			if q.AdmitOccupant != nil {
				c.AdmitOccupant = func(o uint16) bool { calls[which]++; return q.AdmitOccupant(o) }
			}
			return c
		}
		_, err := p.ter.CheckPlacement(counted(0))
		legal := p.ter.PlacementLegal(counted(1))
		if legal != (err == nil) {
			t.Fatalf("query %d: PlacementLegal %v, CheckPlacement error %v", i, legal, err)
		}
		if calls[0] != calls[1] {
			t.Fatalf("query %d: AdmitOccupant asked %d times by CheckPlacement, %d by PlacementLegal", i, calls[0], calls[1])
		}
		_, refusal := p.ter.placementGates(q)
		reached[refusal.reason] = true
	}
	// The fixture must keep reaching every gate, or the equivalence above
	// says less than it seems to. Three refusals follow the entry bounds and
	// cannot be reached by any rectangle that passes them.
	unreachable := map[placementReason]bool{refuseTerrainDimensions: true, refuseRectangleArea: true, refusePlotCell: true}
	for r := placementAccepted; r <= refuseMinWaterDepth; r++ {
		if !reached[r] && !unreachable[r] {
			t.Errorf("no query reaches placement gate %d", r)
		}
	}
}

// legalCaseMovers is an authored mover plane holding two cells.
type legalCaseMovers struct{}

func (legalCaseMovers) CellOccupant(cellX, cellZ int32) uint16 {
	switch {
	case cellX == 2 && cellZ == 2:
		return 11
	case cellX == 7 && cellZ == 2:
		return 12
	}
	return 0
}

// legalCaseViewer is a build-cursor player record whose grid covers the
// western half of the map, with one unseen and one unexplored cell.
type legalCaseViewer struct{ mapping bool }

func (legalCaseViewer) ExploredExtent() (int32, int32) { return 2, 2 }
func (legalCaseViewer) LocallyVisible(vx, vz int32) bool {
	return !(vx == 1 && vz == 1)
}
func (legalCaseViewer) Explored(vx, vz int32) bool { return !(vx == 0 && vz == 1) }
func (v legalCaseViewer) MappingOption() bool      { return v.mapping }

// placementLegalCases builds one authored terrain that reaches every gate of
// the placement validator — bounds by class, the known-site gate, the
// structure-yard mark, both occupancy halves with self and admitted
// occupants, blocking, void, unbound and indestructible features, fringe hops
// live and dead, the geothermal requirement, the mobile per-cell terrain test
// and the building aggregates — and a set of queries that walks every anchor
// in and just outside the map with footprints, yards, profiles and options
// varied by position. It is shared by the validator's equivalence test.
func placementLegalCases() (*Terrain, []PlacementQuery) {
	const w, h = 10, 8
	attrs := make([]formats.TNTAttribute, w*h)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 100, Feature: PlotFeatureNone}
	}
	ter := &Terrain{
		CellW: w, CellH: h, SeaLevel: 100,
		Plot: ExpandPlot(attrs, w, h),
		FeatureDefs: []*content.FeatureDef{
			{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"}, Blocking: true},
			{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "rock"}, Indestructible: true},
			{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "vent"}, Geothermal: true},
			nil, // a real index that binds to no definition
		},
		Movers: legalCaseMovers{},
	}
	for z := int32(0); z < h; z++ {
		for x := int32(0); x < w; x++ {
			c := ter.PlotAt(x, z)
			low := 70 + (x*37+z*23)%60 // straddles sea level 100
			c.SetMinHeight(uint8(low))
			c.SetMaxHeight(uint8(low + ((x+z)%4)*5))
			c.SetHeight(uint8(low))
		}
	}
	ter.PlotAt(3, 2).SetFeature(0) // blocking
	ter.PlotAt(4, 2).SetFeature(PlotFeatureFringe)
	ter.PlotAt(4, 2).SetAnchorSigned(-1, 0) // live hop to the blocking tree
	ter.PlotAt(1, 6).SetFeature(PlotFeatureFringe)
	ter.PlotAt(1, 6).SetAnchorSigned(1, 0) // dead hop to an empty cell
	ter.PlotAt(6, 3).SetFeature(1)         // indestructible
	ter.PlotAt(2, 5).SetFeature(2)         // geothermal
	ter.PlotAt(7, 6).SetFeature(3)         // unbound
	ter.PlotAt(5, 1).SetFeature(PlotFeatureVoid)
	ter.PlotAt(8, 4).SetFeature(20) // past the catalog
	ter.PlotAt(4, 5).SetStructureYard(true)
	ter.PlotAt(5, 5).SetStructureYard(true)
	ter.PlotAt(6, 1).SetOccupantA(7)
	ter.PlotAt(1, 3).SetOccupantA(9)

	yardBytes := []YardCell{0x2f, 0x8f, 0x35, 0x37, 0x00, 0x29, 0x6f, 0x2b, 0x31, 0x2d}
	buildingRules := []PlacementRules{
		{Domain: content.MobilityFixed, ProfileResolved: true, MaxSlope: 12, MaxWaterDepth: 10, MinWaterDepth: -10000},
		{Domain: content.MobilityFixed, ProfileResolved: true, MaxSlope: 40, MaxWaterDepth: 255, MinWaterDepth: 5, Waterline: 3},
		{Domain: content.MobilityFixed, ProfileResolved: true, MaxSlope: 0, MaxWaterDepth: 0, MinWaterDepth: -10000, Waterline: 7},
		{Domain: content.MobilityFixed}, // unresolved: terrain half skipped
	}
	mobileRules := []PlacementRules{
		{Domain: content.MobilityGround, ProfileResolved: true, MaxSlope: 10, MaxWaterSlope: 10, MaxWaterDepth: 12, MinWaterDepth: -10000},
		{Domain: content.MobilityGround, ProfileResolved: true, MaxSlope: 12, MaxWaterSlope: 255, MaxWaterDepth: 255, MinWaterDepth: -10000},
		{Domain: content.MobilityGround, ProfileResolved: true, MaxSlope: 255, MaxWaterSlope: 255, MaxWaterDepth: 255, MinWaterDepth: 8},
		{Domain: content.MobilityAircraft, ProfileResolved: true},
	}
	extents := [][2]int32{{1, 1}, {2, 2}, {3, 2}, {4, 4}}
	admitNine := func(occ uint16) bool { return occ == 9 || occ == 12 }

	var queries []PlacementQuery
	n := 0
	for _, ext := range extents {
		extent, _ := NewFootprintExtent(ext[0], ext[1])
		area := int(ext[0] * ext[1])
		for z := int32(-1); z <= h; z++ {
			for x := int32(-1); x <= w; x++ {
				rect, err := NewFootprintRect(NewFootprintAnchor(x, z), extent)
				if err != nil {
					continue
				}
				// Every yard byte alone, all ten in turn, then every mobile profile.
				for k := 0; k < len(yardBytes)+1+len(mobileRules); k++ {
					n++
					q := PlacementQuery{Rect: rect}
					switch n % 5 {
					case 1:
						q.Self = 7
					case 2:
						q.Self = 11
					case 3:
						q.AdmitOccupant = admitNine
					}
					if n%7 == 0 {
						q.Viewer = legalCaseViewer{mapping: n%14 == 0}
					}
					q.SkipTerrainAggregates = n%11 == 0
					if k <= len(yardBytes) {
						yard := make([]YardCell, area)
						for i := range yard {
							if k == len(yardBytes) {
								yard[i] = yardBytes[(i+n)%len(yardBytes)]
							} else {
								yard[i] = yardBytes[k]
							}
						}
						if n%97 == 0 {
							yard = yard[:area-1] // a yard that disagrees with the rectangle
						}
						q.Yard = yard
						q.Rules = buildingRules[n%len(buildingRules)]
					} else {
						q.Mobile = true
						q.Rules = mobileRules[k-len(yardBytes)-1]
					}
					queries = append(queries, q)
				}
			}
		}
	}
	// A zero rectangle has no extent at all.
	queries = append(queries, PlacementQuery{Mobile: true})
	return ter, queries
}
