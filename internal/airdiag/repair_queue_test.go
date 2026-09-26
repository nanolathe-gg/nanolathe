package airdiag

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Real aircraft, COB pad queries, flight, attachment and the resource-admitted
// repair helper must drain a queue larger than the pad's capacity. No extra
// order is supplied to make a healed aircraft vacate its piece.
func TestModernAircraftRepairQueueDrains(t *testing.T) {
	h := newHarness(t)
	if err := h.Session.SetRules("modern"); err != nil {
		t.Fatal(err)
	}
	pad := spawnPad(t, h)
	planes := make([]*units.Unit, 5)
	for i := range planes {
		planes[i] = spawnAircraft(t, h, diagAircraft, int32(6+i*4), 6)
	}
	h.Step(45)
	for _, u := range planes {
		u.Health = u.Def.MaxDamage - 20
		if err := h.Order(u, 2, pad.Handle, pad.X, pad.Y, pad.Z); err != nil {
			t.Fatal(err)
		}
	}
	var waited, billed bool
	var repaired [5]bool
	for tick := 0; tick < 2400; tick++ {
		h.Step(1)
		all := true
		for i, u := range planes {
			q := orders.QueueForUnit(u)
			if n := q.Head(); n != nil && orders.DescriptorFor(n.ID).Name == "VTOL_Landing" && n.Phase == 1 && n.DynamicGate == 1 {
				waited = true
				if u.Health != u.Def.MaxDamage-20 || u.Attachment.Carrier != 0 {
					t.Fatal("waiting healed or attached an aircraft")
				}
			}
			if u.Health == u.Def.MaxDamage {
				repaired[i] = true
			}
			if !repaired[i] || u.Attachment.Carrier != 0 {
				all = false
			}
			if !u.Alive {
				t.Fatal("repair patient died in the peaceful fixture")
			}
		}
		billed = billed || h.Session.Econ.UnitBuckets(pad.Handle)[economy.Energy].Requested > 0
		// Check actual pad-piece exclusivity, not only reservation bookkeeping.
		for i, a := range planes {
			for _, b := range planes[i+1:] {
				if a.Attachment.Carrier == pad.Handle && b.Attachment.Carrier == pad.Handle && a.Attachment.AttachPiece == b.Attachment.AttachPiece {
					t.Fatal("two patients attached to the same piece")
				}
			}
		}
		if all {
			if !waited || !billed {
				t.Fatalf("queue or ordinary energy admission was not exercised: waiting=%v billed=%v", waited, billed)
			}
			return
		}
	}
	for _, u := range planes {
		t.Logf("health=%d/%d carrier=%d %s", u.Health, u.Def.MaxDamage, u.Attachment.Carrier, h.Observe(u).Format())
	}
	t.Fatal("aircraft did not all finish repair and free their pad pieces")
}
