//go:build retail

package movement_test

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The stock construction aircraft must finish landing in separate footprints,
// then take off and contribute real work when a patrol finds a nanoframe
// [04 R-AIR-01 §6a][04 R-ORD-01 §7]. The third aircraft starts apart as a
// control for patrol assistance independently of the overlap.
func TestRetailLandedAircraftResumePatrolAssistance(t *testing.T) {
	f := pt4Session(t)
	com := f.unit(0, pt4ARM)
	if com == nil {
		t.Fatal("ARM commander not spawned")
	}
	f.step(30) // let the commander's ordinary LOS sweep map the staging ground
	x, z := f.legalSite(t, pt4CA, com.X+world.CellToWorld(8), com.Z+world.CellToWorld(8))
	var aircraft []*units.Unit
	for i := 0; i < 3; i++ {
		px := x
		if i == 2 {
			px += world.CellToWorld(8)
		}
		u := f.place(t, pt4CA, 0, px, z)
		f.s.Movement.SetMoverMode(u, 2)
		if !f.s.Movement.PlaceUnit(orders.PlaceRequest{Unit: u.Handle, X: px, Y: u.Y + numeric.Fixed(u.Def.CruiseAlt)<<16, Z: z}) {
			t.Fatal("stage airborne aircraft")
		}
		aircraft = append(aircraft, u)
	}
	f.step(1) // publish the aircraft's sight before asking it to land
	for _, u := range aircraft {
		q := orders.QueueForUnit(u)
		q.PurgeUnprotected()
		id := orders.Lookup("VTOL_LandIfCan")
		q.Push(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, f.s.Clock.GlobalTick, u.Handle, false))
	}
	for tick := 0; tick < 600; tick++ {
		f.step(1)
		landed := true
		for _, u := range aircraft {
			landed = landed && u.Move.Mode == 1
		}
		if landed {
			break
		}
	}
	for _, u := range aircraft {
		if u.Move.Mode != 1 || u.Move.ModeMirror != 1 {
			t.Errorf("stock aircraft %d failed landing: request=%d mirror=%d", u.Handle, u.Move.Mode, u.Move.ModeMirror)
		}
	}

	sx, sz := f.legalSite(t, pt4Solar, x+world.CellToWorld(12), z)
	def, _ := f.cat.Unit(pt4Solar)
	h, err := f.s.Units.CreateNanoframe(def, 0, sx, f.s.World.HeightAt(sx, sz), sz)
	if err != nil {
		t.Fatal(err)
	}
	target := f.s.Units.Unit(h)
	before := target.Remaining
	// Observe the ordinary service's fraction write, so a HelpBuild caption
	// or target health bar alone cannot satisfy this check.
	var contributed [3]bool
	work := orders.QueueForUnit(aircraft[0]).Binding().Work
	assist := work.Assist
	work.Assist = func(builder *units.Unit, n *orders.Node, tick uint32) bool {
		previous := target.Remaining
		result := assist(builder, n, tick)
		if n.Target == h && target.Remaining < previous {
			for i, u := range aircraft {
				if builder == u {
					contributed[i] = true
				}
			}
		}
		return result
	}
	var handles []pool.Handle
	for _, u := range aircraft {
		handles = append(handles, u.Handle)
	}
	if err := f.s.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{
		Handles: handles, Code: 9, Position: orders.ResolvePos{X: sx, Y: target.Y, Z: sz},
	}}); err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < 300 && contributed != [3]bool{true, true, true}; tick++ {
		f.step(1)
	}
	for i, u := range aircraft {
		if !contributed[i] {
			name := "<none>"
			if n := orders.QueueForUnit(u).Head(); n != nil {
				name = orders.DescriptorFor(n.ID).Name
			}
			t.Errorf("stock aircraft %d did not assist: mode=%d/%d order=%s range=%d", u.Handle, u.Move.Mode, u.Move.ModeMirror, name, pt4Dist(u, target.X, target.Z))
		}
	}
	t.Logf("actual construction contributions=%v, target remaining %g -> %g", contributed, before, target.Remaining)
}
