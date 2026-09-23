package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// submergedEnemyFixture turns the icon fixture's unit into an enemy walking
// below the sea plane with no sonar contact: its hull top is under the
// sea-level byte, so the direct-visibility predicate's step 3 rejects it
// [03 §3.2]. lit selects whether the viewer's word grid covers its tile.
func submergedEnemyFixture(t *testing.T, lit bool) (*Client, *frame.Frame) {
	t.Helper()
	c, f := iconLayoutFixture(t)
	const w, h = 64, 64
	f.Visibility = frame.VisibilityView{W: w, H: h, WordVisible: make([]uint16, w*h), Valid: true, SeaLevel: numeric.FixedFromInt(40)}
	if lit {
		for i := range f.Visibility.WordVisible {
			f.Visibility.WordVisible[i] = 1 // viewer 0's bit
		}
	}
	u := &f.Units[0]
	u.Owner, u.DefName = 1, "walker"
	u.Y, u.HullYExtent = numeric.FixedFromInt(5), numeric.FixedFromInt(20) // top 25 < sea 40
	u.HullXExtent, u.HullZExtent = numeric.FixedFromInt(16), numeric.FixedFromInt(16)
	u.HullOffsetY = u.HullYExtent
	u.UnderwaterExempt = false // no sonar bit
	p := &f.Radar.Contacts[0]
	p.Owner, p.Y, p.Graphic = 1, u.Y, "armcom"
	f.Radar.MappingLOS = 3
	// Publication's own resolution of the blip gate: seen bit only from the
	// line-of-sight probe, since radar cannot contact a submerged hull
	// [03 R-VIS-01 §4] pass 5 [03 R-VIS-01 §5].
	p.Status, p.Visible, p.Seen = 0, false, false
	if lit {
		p.Status, p.Visible, p.Seen = 0x100, true, true
	}
	return c, f
}

// TestSubmergedEnemyWithoutSonarOrSightHasNoMark locks the tester report's
// contract: a submerged enemy with neither sonar contact nor line of sight
// produces no strategic dot, no minimap blip and no selectable hit.
func TestSubmergedEnemyWithoutSonarOrSightHasNoMark(t *testing.T) {
	c, f := submergedEnemyFixture(t, false)
	if unitVisibleForFrame(f, f.Units[0], 0) {
		t.Fatal("submerged enemy without sonar passed the world visibility gate")
	}
	p := f.Radar.Contacts[0]
	mc := render.MinimapContact{Owner: p.Owner, Status: p.Status, Visible: p.Visible, LocalPlayer: 0, MinimapMode: f.Radar.MappingLOS}
	if render.MinimapBlipAdmitted(mc, render.BlinkState{Phase: 1}) {
		t.Fatal("minimap admitted a submerged enemy without sonar or sight")
	}
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 0 {
		t.Fatalf("strategic layer drew %d marks for an undetected submerged enemy", len(c.markerArena))
	}
	if _, _, ok := c.PickPresentedUnit(f, 300, 200, 0); ok {
		t.Fatal("undetected submerged enemy is selectable in the strategic view")
	}
	assertHullGateRejects(t, c, f)
}

// TestSubmergedEnemyInSightWithoutSonarIsNeverIdentified covers the case the
// report most likely shows. Retail's line-of-sight probe sets the seen bit
// with no sea-level term, and the minimap blip gate reads that bit, so the
// retail minimap blips this unit [03 R-VIS-01 §4] pass 5 [03 §3.9]
// [03 R-MM-01 §3]. The model, icon and every unit hit stay behind the
// direct-visibility predicate, which rejects it without the sonar bit
// [03 §3.2] step 3 [03 R-REN-03A §8].
func TestSubmergedEnemyInSightWithoutSonarIsNeverIdentified(t *testing.T) {
	c, f := submergedEnemyFixture(t, true)
	if unitVisibleForFrame(f, f.Units[0], 0) {
		t.Fatal("submerged enemy without sonar passed the world visibility gate in line of sight")
	}
	p := f.Radar.Contacts[0]
	mc := render.MinimapContact{Owner: p.Owner, Status: p.Status, Visible: p.Visible, LocalPlayer: 0, MinimapMode: f.Radar.MappingLOS}
	if !render.MinimapBlipAdmitted(mc, render.BlinkState{Phase: 1}) {
		t.Fatal("retail minimap blip gate must admit a seen contact [03 R-MM-01 §3]")
	}
	c.drawStrategicMarkers(f)
	for _, m := range c.markerArena {
		if m.IconAtlas != nil || m.Size != strategicMarkerSize {
			t.Fatal("strategic layer identified a submerged enemy without sonar")
		}
	}
	if _, _, ok := c.PickPresentedUnit(f, 300, 200, 0); ok {
		t.Fatal("submerged enemy without sonar is selectable in the strategic view")
	}
	assertHullGateRejects(t, c, f)
	// With sonar contact the same unit is identified.
	c.cam.Zoom = strategicModelCut
	f.Units[0].UnderwaterExempt = true
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 || c.markerArena[0].IconAtlas == nil {
		t.Fatal("sonar-contacted submerged enemy in sight was not identified")
	}
}

// assertHullGateRejects checks the normal-zoom consumers: the hull picker,
// hover and the drag band all admit a unit only through SnapshotVisible, and
// the band over the whole map must not return the submerged enemy.
func assertHullGateRejects(t *testing.T, c *Client, f *frame.Frame) {
	t.Helper()
	if SnapshotVisible(f, f.Units[0], 0) {
		t.Fatal("normal-zoom hull gate admitted a submerged enemy without sonar")
	}
	band := SelectionBand{Record: Rect{MinX: -4096, MinY: -4096, MaxX: 4096, MaxY: 4096}}
	band.Surface = band.Record
	if got := SnapshotUnitHandlesInBand(f, c.cam, band, 0); len(got) != 0 {
		t.Fatalf("drag band returned %v for a submerged enemy without sonar", got)
	}
	// The same band does reach the unit once it carries the sonar bit, so the
	// rejection above is the gate and not the geometry.
	g := *f
	g.Units = append([]frame.UnitView(nil), f.Units...)
	g.Units[0].UnderwaterExempt = true
	if !SnapshotVisible(&g, g.Units[0], 0) && f.Radar.Contacts[0].Seen {
		t.Fatal("sonar-exempt unit in sight was rejected by the hull gate")
	}
	if f.Radar.Contacts[0].Seen && len(SnapshotUnitHandlesInBand(&g, c.cam, band, 0)) != 1 {
		t.Fatal("drag band did not reach the sonar-exempt unit; the fixture is vacuous")
	}
}
