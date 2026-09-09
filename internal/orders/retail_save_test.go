package orders

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestRetailOrderImagesRoundTripPreservesQueueOrderAndReferences(t *testing.T) {
	u := &units.Unit{Handle: 100, Alive: true}
	front := &Node{ID: Lookup("Move_Ground"), Owner: 100, Target: 101, Phase: 3, DynamicGate: 4, Deadline: -1, Param1: 5, StaticGate: 0x402, Flags: FlagActive, Satisfied: 6, BuildDefKey: "armmex"}
	subtype := make([]byte, save.OrderSubtypeCode2)
	rear := &Node{ID: Lookup("VTOL_Move"), Owner: 100, StaticGate: 0x40000, RetailSubtypeCode: 2, RetailSubtype: subtype, RetailSubtypeUnitA: 100, RetailSubtypeUnitB: 101}
	BindQueue(u, NewQueueWith([]*Node{front}, []*Node{rear}))
	ids := map[pool.Handle]uint16{100: 9, 101: 12}
	resolve := func(h pool.Handle) (uint16, bool) { id, ok := ids[h]; return id, ok }
	images, err := RetailOrderImages(u, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || images[0].Sequence != 0 || images[0].Secondary || images[1].Sequence != 1 || !images[1].Secondary {
		t.Fatalf("queue order = %#v", images)
	}
	if binary.LittleEndian.Uint16(images[0].Main) != 9 || binary.LittleEndian.Uint16(images[0].Main[2:]) != 12 || binary.LittleEndian.Uint16(images[1].Subtype[8:]) != 9 || binary.LittleEndian.Uint16(images[1].Subtype[0x1a:]) != 12 {
		t.Fatal("order references were not resolved through stable IDs")
	}
	records := make([]save.OrderRecord, len(images))
	for i, image := range images {
		records[i] = save.OrderRecord{ParentStableID: image.ParentStableID, Sequence: image.Sequence, Secondary: image.Secondary, Main: image.Main, SubtypeCode: image.SubtypeCode, Subtype: image.Subtype, DescriptorName: image.DescriptorName, BuildTypeName: image.BuildTypeName}
	}
	v := &units.Unit{Handle: 200, Alive: true}
	if err := RetailRestoreOrders(v, records, map[uint16]pool.Handle{9: 200, 12: 201}, nil); err != nil {
		t.Fatal(err)
	}
	q := QueueOfUnit(v)
	if q.LenPrimary() != 1 || q.LenSecondary() != 1 || q.Primary()[0].Target != 201 || q.Secondary()[0].RetailSubtypeUnitB != 201 || q.Primary()[0].BuildDefKey != "armmex" {
		t.Fatalf("restored queue mismatch: front=%#v rear=%#v", q.Primary(), q.Secondary())
	}
}

func TestRetailOrderImagesMainTargetLiveness(t *testing.T) {
	owner := &units.Unit{Handle: 100, Alive: true}
	target := pool.Handle(101)
	BindQueue(owner, NewQueueWith([]*Node{{ID: Lookup("Move_Ground"), Owner: owner.Handle, Target: target}}, nil))
	resolveOwnerOnly := func(h pool.Handle) (uint16, bool) {
		if h != owner.Handle {
			return 0, false
		}
		return 9, true
	}

	dead, err := RetailOrderImages(owner, resolveOwnerOnly, func(pool.Handle) bool { return false })
	if err != nil {
		t.Fatalf("dead main target blocked save: %v", err)
	}
	if got := binary.LittleEndian.Uint16(dead[0].Main[2:]); got != 0 {
		t.Fatalf("dead main target stable ID=%d, want wire null", got)
	}

	if _, err := RetailOrderImages(owner, resolveOwnerOnly, func(h pool.Handle) bool { return h == target }); err == nil {
		t.Fatal("live main target without a stable ID did not fail")
	}
}
