package orders

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestRetailRestoreOrdersKeepsFrontRearSequence(t *testing.T) {
	u := &units.Unit{Handle: pool.Handle(100), Alive: true}
	main := func(seq uint32, rear bool) []byte {
		data := make([]byte, save.OrderBoxSize)
		binary.LittleEndian.PutUint16(data, 9)
		data[8] = byte(Lookup("Move_Ground"))
		if rear {
			binary.LittleEndian.PutUint32(data[0x32:], 0x40000)
		}
		return data
	}
	records := []save.OrderRecord{
		{ParentStableID: 9, Sequence: 1, Secondary: true, Main: main(1, true), SubtypeCode: 4, Subtype: make([]byte, save.OrderSubtypeCode4)},
		{ParentStableID: 9, Sequence: 0, Main: main(0, false)},
	}
	if err := RetailRestoreOrders(u, records, map[uint16]pool.Handle{9: 100}, nil); err != nil {
		t.Fatal(err)
	}
	q := QueueOfUnit(u)
	if q == nil || q.LenPrimary() != 1 || q.LenSecondary() != 1 {
		t.Fatalf("queue segments: primary=%d secondary=%d", q.LenPrimary(), q.LenSecondary())
	}
	if q.Primary()[0].ID != ID(main(0, false)[8]) || q.Secondary()[0].ID != ID(main(1, true)[8]) {
		t.Fatal("saved front/rear records were not retained")
	}
	if q.Primary()[0].Flags&FlagActive == 0 {
		t.Fatal("restored primary queue has no active marker")
	}
}

func TestRetailRestoreSubtypeReferenceFixups(t *testing.T) {
	u := &units.Unit{Handle: 100, Alive: true}
	main := make([]byte, save.OrderBoxSize)
	binary.LittleEndian.PutUint16(main, 9)
	main[8] = byte(Lookup("Move_Ground"))
	payload := make([]byte, save.OrderSubtypeCode2)
	binary.LittleEndian.PutUint16(payload[8:], 9)
	binary.LittleEndian.PutUint16(payload[0x1a:], 10)
	recs := []save.OrderRecord{{ParentStableID: 9, Main: main, SubtypeCode: 2, Subtype: payload}}
	if err := RetailRestoreOrders(u, recs, map[uint16]pool.Handle{9: 100, 10: 101}, nil); err != nil {
		t.Fatal(err)
	}
	n := QueueOfUnit(u).Primary()[0]
	if n.RetailSubtypeUnitA != 100 || n.RetailSubtypeUnitB != 101 || len(n.RetailSubtypeWords16) != 5 || len(n.RetailSubtypeWords32) != 4 {
		t.Fatalf("subtype fixup: %#v", n)
	}
}

func TestRetailRestoreSubtypeThreeReferenceFixup(t *testing.T) {
	u := &units.Unit{Handle: 100, Alive: true}
	main := make([]byte, save.OrderBoxSize)
	binary.LittleEndian.PutUint16(main, 9)
	main[8] = byte(Lookup("Move_Ground"))
	payload := make([]byte, save.OrderSubtypeCode3)
	binary.LittleEndian.PutUint16(payload[8:], 10)
	recs := []save.OrderRecord{{ParentStableID: 9, Main: main, SubtypeCode: 3, Subtype: payload}}
	if err := RetailRestoreOrders(u, recs, map[uint16]pool.Handle{9: 100, 10: 101}, nil); err != nil {
		t.Fatal(err)
	}
	if got := QueueOfUnit(u).Primary()[0].RetailSubtypeUnitA; got != 101 {
		t.Fatalf("code-3 reference %d, want 101", got)
	}
}

func TestRetailRestoreSubtypeLengths(t *testing.T) {
	u := &units.Unit{Handle: 100, Alive: true}
	main := make([]byte, save.OrderBoxSize)
	binary.LittleEndian.PutUint16(main, 9)
	main[8] = byte(Lookup("Move_Ground"))
	for code, size := range map[uint32]int{2: save.OrderSubtypeCode2, 3: save.OrderSubtypeCode3, 4: save.OrderSubtypeCode4, 5: save.OrderSubtypeCode5, 6: save.OrderSubtypeCode6} {
		bad := make([]byte, size-1)
		err := RetailRestoreOrders(u, []save.OrderRecord{{ParentStableID: 9, Main: main, SubtypeCode: code, Subtype: bad}}, map[uint16]pool.Handle{9: 100}, nil)
		if err == nil {
			t.Fatalf("subtype %d short payload accepted", code)
		}
	}
}
