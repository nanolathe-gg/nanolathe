package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"testing"
)

// The command keeps the issuing session's interface option across enqueue and
// resolution; another session's opposite option cannot contaminate it [04 R-ORD-02 §1].
func TestInputResolutionKeepsEachSessionsVariant(t *testing.T) {
	for _, variant := range []int{orders.InterfaceTypeRightClick, orders.InterfaceTypeLeftClick} {
		def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", BMCode: 1, CanMove: true, CanReclamate: true, MaxDamage: 100}
		cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": def}}
		w := newSessionFixtureWorld(8, cat)
		actor, _ := w.Create(def, 0, 0, 0, 0)
		target, _ := w.Create(def, 0, 40<<16, 0, 0)
		w.Unit(target).Health = 50
		w.Unit(target).Flags |= units.ClassifierEligibleStatus
		s := &Session{Units: w, Catalog: cat, World: &world.Terrain{}, LocalOwner: 0}
		cmd := HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{actor}, Code: 1, Target: target, Position: orders.ResolvePos{InterfaceType: variant}}}
		if err := s.EnqueueHumanCommand(cmd); err != nil {
			t.Fatal(err)
		}
		s.applyHumanCommands(1)
		q := orders.QueueForUnit(w.Unit(actor))
		if variant == orders.InterfaceTypeRightClick {
			if q.LenPrimary() != 1 || q.Primary()[0].ID != orders.Lookup("RepairUnit") {
				t.Fatalf("right variant queue=%+v", q.Primary())
			}
		} else if q.LenPrimary() != 0 {
			t.Fatalf("default variant queue=%+v", q.Primary())
		}
	}
}
