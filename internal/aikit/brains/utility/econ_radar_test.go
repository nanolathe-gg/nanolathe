package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// The Modern AI reserves a radar's authored range, across definitions and
// before construction finishes. A mobile sensor does not reserve a site.
func TestRadarCoverage(t *testing.T) {
	for _, tc := range []struct {
		name              string
		candidate, sensor int32
		distance          int32
		other, frame      bool
		mobile, want      bool
	}{
		{name: "same tower nearby", candidate: 600, sensor: 600, distance: 599},
		{name: "range boundary", candidate: 600, sensor: 600, distance: 600, want: true},
		{name: "distant expansion", candidate: 600, sensor: 600, distance: 1200, want: true},
		{name: "longer existing radar", candidate: 600, sensor: 1200, distance: 1199, other: true},
		{name: "longer candidate radar", candidate: 1200, sensor: 600, distance: 1199, other: true},
		{name: "other definition boundary", candidate: 1200, sensor: 600, distance: 1200, other: true, want: true},
		{name: "unfinished tower", candidate: 600, sensor: 600, distance: 64, frame: true},
		{name: "mobile radar passing by", candidate: 600, sensor: 2000, distance: 64, mobile: true, want: true},
		{name: "building without radar", candidate: 600, distance: 64, other: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newDefWorld(DefaultParams())
			w.d.rad.Radar = tc.candidate
			sensor := w.d.rad
			if tc.other {
				sensor = w.d.llt
			} else if tc.mobile {
				sensor = w.d.con
			}
			sensor.Radar = tc.sensor
			u := own(9, sensor, 2000+tc.distance, 2000)
			u.Built = !tc.frame
			w.think(6*1800, append(w.base(), u))
			// Hold the request point fixed to exercise the coverage decision
			// independently of which building the front follows.
			w.b.RallyX, w.b.RallyZ = 2000, 2000
			w.e.s.frontX, w.e.s.frontZ = 2000, 2000
			randBefore := *w.k.Rand
			metalBefore, energyBefore := w.obs.Metal, w.obs.Energy
			c := w.e.evalRadar(w.b, &w.obs.Own[1], w.d.rad)
			if got := c.score > 0; got != tc.want {
				t.Errorf("radar offered = %v, want %v (score %d)", got, tc.want, c.score)
			}
			if *w.k.Rand != randBefore || w.obs.Metal != metalBefore || w.obs.Energy != energyBefore {
				t.Fatal("evaluating radar spent resources or drew randomness")
			}
		})
	}
}

func TestRadarReservations(t *testing.T) {
	w := newDefWorld(DefaultParams())
	w.d.rad.Radar = 1200
	base := append(w.base(), own(9, w.d.con, 720, 720))
	w.think(6*1800, base)
	e, s := w.e, w.e.s
	u := &w.obs.Own[1]
	c := e.evalRadar(w.b, u, w.d.rad)
	if c.score < minScore {
		t.Fatalf("first radar is not worth building: score %d", c.score)
	}
	e.assign(w.b, u, s.commitOf(u), &c)
	if next := e.evalRadar(w.b, &w.obs.Own[8], w.d.rad); next.score != 0 {
		t.Fatalf("another builder offered the reserved radar in the same think: score %d", next.score)
	}
	for _, tc := range []struct {
		name    string
		order   aikit.OrderClass
		target  *aikit.UnitInfo
		reused  bool
		removed bool
		want    bool
	}{
		{name: "walking to build", order: aikit.OrderMove, target: w.d.rad},
		{name: "still constructing", order: aikit.OrderBuild, target: w.d.rad},
		{name: "failed placement", order: aikit.OrderIdle, want: true},
		{name: "cancelled for a move", order: aikit.OrderMove, want: true},
		{name: "different construction", order: aikit.OrderBuild, target: w.d.sol, want: true},
		{name: "recycled builder slot", order: aikit.OrderBuild, target: w.d.rad, reused: true, want: true},
		{name: "builder lost", removed: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			units := append([]aikit.OwnUnit(nil), base...)
			units[1].Order, units[1].Target = tc.order, tc.target
			if tc.reused {
				units[1].Gen++
			}
			if tc.removed {
				units = append(units[:1], units[2:]...)
			}
			// A live build continues to reserve coverage even when slow
			// construction outlasts the economy's pending-output estimate.
			w.think(9*1800, units)
			next := e.evalRadar(w.b, &w.obs.Own[len(w.obs.Own)-1], w.d.rad)
			if got := next.score > 0; got != tc.want {
				t.Errorf("radar offered = %v, want %v (score %d)", got, tc.want, next.score)
			}
		})
	}
}

func TestRadarRebuildAfterLoss(t *testing.T) {
	w := newDefWorld(DefaultParams())
	w.d.rad.Radar = 1200
	w.think(6*1800, w.base())
	c := w.e.evalRadar(w.b, &w.obs.Own[1], w.d.rad)
	tower := own(9, w.d.rad, c.x, c.z)
	w.think(6*1800+15, append(w.base(), tower))
	if next := w.e.evalRadar(w.b, &w.obs.Own[1], w.d.rad); next.score != 0 {
		t.Fatalf("completed radar did not cover its site: score %d", next.score)
	}
	w.think(6*1800+30, w.base())
	if next := w.e.evalRadar(w.b, &w.obs.Own[1], w.d.rad); next.score < minScore {
		t.Fatalf("lost radar cannot be rebuilt: score %d", next.score)
	}
}
