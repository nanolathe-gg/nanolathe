package utility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// fac_backoff: a factory placement whose batch reported a search that
// found no site, and whose builder is not building that factory at the
// next think (idle, or carrying on with its earlier order), rests its
// request point for facBackoffTicks, for that factory (part 1) or every
// factory (part 2). A builder on its way to build it, or a batch that found
// every site it searched for, rests nothing; with the switch off nothing
// rests.
func TestFactoryBackoff(t *testing.T) {
	type after struct {
		order  aikit.OrderClass
		target bool // the builder's first build order is the factory
	}
	idle, repairing, building := after{aikit.OrderIdle, false}, after{aikit.OrderRepair, false}, after{aikit.OrderBuild, true}
	for _, c := range []struct {
		part         int32
		noSite       bool
		then         after
		same, others bool
	}{
		{0, true, idle, false, false},
		{1, true, idle, true, false},
		{2, true, idle, true, true},
		{1, true, repairing, true, false},
		{2, true, building, false, false},
		{2, false, idle, false, false},
	} {
		p := DefaultParams()
		p.FacBackoff = c.part
		w := newDefWorld(p)
		d := w.d
		e, s := w.e, w.e.s
		lab := *d.fac // another factory
		lab.Key = "lab"
		w.think(6*1800, w.base())
		con := &w.obs.Own[1]
		*s.commitOf(con) = commitment{def: con.Info, gen: con.Gen, kind: cFactory, prod: d.fac, x: 900, z: 900, tick: s.tick}
		w.k.Last = aikit.ApplyStats{}
		if c.noSite {
			w.k.Last.Reasons[aikit.FailNoSite] = 1
		}
		own := w.base()
		own[1].Order = c.then.order
		if c.then.target {
			own[1].Target = d.fac
		}
		w.think(6*1800+15, own)
		e.Plan(w.b)
		if got := e.backedOff(d.fac, 900, 900); got != c.same {
			t.Errorf("%+v: the failed factory rests at its point: %v, want %v", c, got, c.same)
		}
		if got := e.backedOff(&lab, 900, 900); got != c.others {
			t.Errorf("%+v: another factory rests there: %v, want %v", c, got, c.others)
		}
		if e.backedOff(d.fac, 900, 1000) {
			t.Errorf("%+v: another request point rests", c)
		}
		s.tick += facBackoffTicks
		if e.backedOff(d.fac, 900, 900) {
			t.Errorf("%+v: the point still rests after %d ticks", c, facBackoffTicks)
		}
	}
}
