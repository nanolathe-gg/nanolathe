package airdiag

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

const (
	diagAirBuilder = "ARMCA"    // ARM construction aircraft
	diagProduct    = "ARMSOLAR" // a cheap building it can place
)

// mobileBuild issues the builder's own build command, which is the boundary the
// client uses for a construction aircraft [07 §9].
func mobileBuild(h *Harness, builder *units.Unit, x, z numeric.Fixed) error {
	return h.Session.EnqueueHumanCommand(session.HumanCommand{
		Kind: session.HumanMobileBuild,
		MobileBuild: session.HumanMobileBuildCommand{
			Builder: builder.Handle,
			Product: diagProduct,
			WX:      x,
			WZ:      z,
			WY:      goalHeight(h, x, z),
		},
	})
}

// TestAirBuilderOrbitsWhileBuilding is the construction aircraft's holding
// pattern [04 R-ORD-02 §2][04 §10.3]. The work body rebuilds an orbit marker on
// every tick where tick mod 150 == 0, placing it at the product's position plus
// the UN-negated component pair at bearing(me -> product) + 0xDB6E, radius
// builddistance. Successive stations advance about -51.43 degrees, so seven of
// them close the circle: the builder circles its site rather than sitting on it
// or shuttling across it.
func TestAirBuilderOrbitsWhileBuilding(t *testing.T) {
	h := newHarness(t)
	u := spawnAircraft(t, h, diagAirBuilder, 6, 6)
	t.Logf("%s canfly=%v builder=%v builddistance=%d workertime=%d",
		u.Def.UnitName, u.Def.CanFly, u.Def.Builder, u.Def.BuildDistance, u.Def.WorkerTime)

	// A site the map's own placement pass accepts: the ground control in this
	// package confirms a walker builds here, so a failure below is the air path.
	com := h.Unit(0, diagAnchor)
	if com == nil {
		t.Skip("the ARM commander was not placed on this map")
	}
	// Seven cells east: six landed the 5x5 solar footprint on the fringe of a
	// real 2x2 feature at the commander's side, which retail refuses too now
	// that bootstrap writes fringe over every covered cell [05 R-FEAT-01 §17].
	siteX := com.X.Add(world.CellToWorld(7))
	siteZ := com.Z
	if err := mobileBuild(h, u, siteX, siteZ); err != nil {
		t.Fatalf("mobile build: %v", err)
	}

	rows := h.Trace(u, 900)
	dump(t, rows, 75)

	var (
		sawRecord  bool
		product    pool.Handle
		bearings   []float64
		minR, maxR = math.MaxFloat64, 0.0
	)
	for _, r := range rows {
		if r.HeadName == "VTOL_MobileBuild" {
			sawRecord = true
		}
	}
	// The product the builder created, if any.
	if cand := h.Unit(0, diagProduct); cand != nil {
		product = cand.Handle
	}
	t.Logf("VTOL_MobileBuild record seen=%v product created=%v", sawRecord, product != 0)
	if !sawRecord {
		t.Fatalf("no VTOL_MobileBuild record was ever at the head; the build order did not reach the air builder")
	}
	if product == 0 {
		t.Fatalf("the builder never created its product nanoframe, so the work body never ran [04 R-ORD-02 §2]")
	}
	p := h.Session.Units.Unit(product)

	// Measure the builder's radius and bearing about the product once it is
	// established, sampling the second half of the trace.
	for _, r := range rows[len(rows)/2:] {
		dx := float64(int64(r.X)-int64(p.X)) / 65536.0
		dz := float64(int64(r.Z)-int64(p.Z)) / 65536.0
		d := math.Hypot(dx, dz)
		if d < minR {
			minR = d
		}
		if d > maxR {
			maxR = d
		}
		bearings = append(bearings, math.Atan2(dx, dz))
	}
	// Angular travel around the product across the sampled window.
	total := 0.0
	for i := 1; i < len(bearings); i++ {
		d := bearings[i] - bearings[i-1]
		for d > math.Pi {
			d -= 2 * math.Pi
		}
		for d < -math.Pi {
			d += 2 * math.Pi
		}
		total += d
	}
	t.Logf("radius about product: min=%.1f max=%.1f (builddistance=%d); angular travel=%.2f rad over %d ticks",
		minR, maxR, u.Def.BuildDistance, total, len(bearings))

	// The station advances by 0xDB6E every 150 ticks — about -51.43 degrees, or
	// -0.897 rad, seven of which close the circle [04 §10.3]. Over this window
	// the builder should sweep roughly that rate, and negatively: the sign is
	// the whole point of the un-negated component pair, and getting it backwards
	// would still look like an orbit while circling the wrong way.
	stations := float64(len(bearings)) / 150.0
	want := -0.897 * stations
	if total > -1.0 {
		t.Errorf("the builder swept %.2f rad about its product in %d ticks, want about %.2f: it is not "+
			"flying a holding pattern [04 R-ORD-02 §2 work body][04 §10.3]", total, len(bearings), want)
	}
	if math.Abs(total-want) > 1.0 {
		t.Errorf("swept %.2f rad over %.1f orbit periods, want about %.2f (0xDB6E per 150 ticks) [04 §10.3]",
			total, stations, want)
	}
	// The orbit rides at builddistance; the marker carries no radius setter, so
	// the aircraft flies to the station itself [04 R-ORD-02 §2].
	bd := float64(u.Def.BuildDistance)
	if maxR > bd*1.5 || minR < bd*0.5 {
		t.Errorf("orbit radius ranged %.1f..%.1f about a builddistance of %.0f [04 §10.3]", minR, maxR, bd)
	}
}
