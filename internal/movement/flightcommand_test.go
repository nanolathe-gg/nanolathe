package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// headingProbePayload is a goal payload that reports a fixed goal and records
// whether the producer consulted its heading supply. Step 5's ordering is the
// contract under test, so the consultation itself is an assertion.
type headingProbePayload struct {
	goal       Vec3
	suggestion uint16
	supply     bool
	consulted  bool
}

func (p *headingProbePayload) UpdateGoal(_ *units.Unit, dst *Vec3) { *dst = p.goal }
func (p *headingProbePayload) Arrived(_ *units.Unit) bool          { return false }
func (p *headingProbePayload) SupplyHeading(_ *units.Unit, dst *uint16) bool {
	p.consulted = true
	if !p.supply {
		return false
	}
	*dst = p.suggestion
	return true
}
func (p *headingProbePayload) Persistent() bool { return true }
func (p *headingProbePayload) Release()         {}

// TestProducerHeadingRule locks step 5 of the per-tick command producer
// [04 R-AIR-01 §1]: beyond 320 world units the command heading is the bearing to
// the goal and the payload is not consulted at all; inside that a supplied
// suggestion stands; and inside 16 world units with no suggestion the command
// heading is left completely unchanged.
func TestProducerHeadingRule(t *testing.T) {
	const wu = 65536
	cases := []struct {
		name          string
		goalZ         int64 // goal Z in world units; the unit sits at the origin
		supply        bool
		suggestion    uint16
		wantHeading   uint16
		wantConsulted bool
	}{
		{name: "beyond 320 world units the payload is not consulted", goalZ: 400, supply: true, suggestion: 1234, wantHeading: 32768, wantConsulted: false},
		{name: "payload suggestion stands inside 320", goalZ: 100, supply: true, suggestion: 1234, wantHeading: 1234, wantConsulted: true},
		{name: "inside 16 with no suggestion the heading is unchanged", goalZ: 8, supply: false, wantHeading: 4321, wantConsulted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &units.Unit{}
			p := &headingProbePayload{goal: Vec3{Z: numeric.Fixed(tc.goalZ * wu)}, suggestion: tc.suggestion, supply: tc.supply}
			c := &FlightCommand{Unit: u, Payload: p, Heading: 4321}
			c.produce(u, nil, nil)
			if c.Heading != tc.wantHeading {
				t.Fatalf("command heading = %d, want %d", c.Heading, tc.wantHeading)
			}
			if p.consulted != tc.wantConsulted {
				t.Fatalf("payload consulted = %v, want %v", p.consulted, tc.wantConsulted)
			}
		})
	}
}

// TestLeanAccumulatorDecay locks the lean accumulator's per-tick decay
// [04 R-AIR-01 §2]: each component is multiplied by 62259 and shifted down 16,
// which for a component of 2^20 is exactly 62259 << 4. A zero delta decays the
// accumulator once; it does not snap it to zero, which is the levelling
// contract the mover-mode setter relies on.
func TestLeanAccumulatorDecay(t *testing.T) {
	s := &FlightState{LeanX: 1 << 20, LeanY: 1 << 20, LeanZ: 1 << 20, Gravity: 0x1FDB}
	s.ApplyLean(0, 0, 0)
	const want = 0xF333 << 4 // 996144
	if s.LeanX != want || s.LeanY != want || s.LeanZ != want {
		t.Fatalf("lean after one zero-delta tick = (%d, %d, %d), want %d in each component", s.LeanX, s.LeanY, s.LeanZ, want)
	}
}

// TestAirSectorGridSmoothedMaximum locks the grid's two height bytes
// [04 R-AIR-01 §5]: the sweep raises a sector's own byte to the highest derived
// per-cell maximum inside it, floored at sea level, and the smoothed byte is the
// maximum of that over the 3x3 block of sectors around it.
func TestAirSectorGridSmoothedMaximum(t *testing.T) {
	// 32 x 32 attribute cells is 512 x 512 world units, which is 4 x 4 sectors.
	ter := &world.Terrain{CellW: 32, CellH: 32, Plot: make([]world.PlotCell, 32*32), SeaLevel: 10}
	ter.PlotAt(0, 0).SetMaxHeight(200)

	g := NewAirSectorGrid(ter)
	if g == nil || g.Columns != 4 || g.Rows != 4 {
		t.Fatalf("grid = %+v, want 4 x 4 sectors", g)
	}
	const wu = 65536
	for _, tc := range []struct {
		name   string
		x, z   int64 // world units
		want   uint8
		linked bool
	}{
		{name: "the peak's own sector", x: 4, z: 4, want: 200, linked: true},
		{name: "the neighbouring sector", x: 200, z: 4, want: 200, linked: true},
		{name: "two sectors away is sea level", x: 300, z: 300, want: 10, linked: true},
		{name: "off the map links to the out-of-bounds record", x: -8, z: 4, want: 0, linked: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, linked := g.SectorHeightAt(numeric.Fixed(tc.x*wu), numeric.Fixed(tc.z*wu))
			if got != tc.want || linked != tc.linked {
				t.Fatalf("sector height = (%d, %v), want (%d, %v)", got, linked, tc.want, tc.linked)
			}
		})
	}
}
