package orders

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// RetailRestoreOrders rebuilds both queue segments from the detached save
// records. The supplied binding is installed before either segment becomes
// reachable, so no handler can run with incomplete session services [08
// R-SAVE-02 §11]. Records remain in saved sequence order within each segment.
func RetailRestoreOrders(u *units.Unit, records []save.OrderRecord, stable map[uint16]pool.Handle, binding *QueueBinding) error {
	return RetailRestoreOrdersAtTick(u, records, stable, binding, 0)
}

// RetailRestoreOrdersAtTick is RetailRestoreOrders with the current global
// tick supplied for each node's reinitialized creation stamp [05 "Saving
// economy, construction, and features"].
func RetailRestoreOrdersAtTick(u *units.Unit, records []save.OrderRecord, stable map[uint16]pool.Handle, binding *QueueBinding, tick uint32) error {
	if u == nil {
		return fmt.Errorf("orders: retail restore: nil owner")
	}
	front := make([]*Node, 0, len(records))
	rear := make([]*Node, 0, len(records))
	sort.SliceStable(records, func(i, j int) bool { return records[i].Sequence < records[j].Sequence })
	var ownerStableID uint16
	if len(records) != 0 {
		ownerStableID = records[0].ParentStableID
		if ownerStableID == 0 || stable == nil || stable[ownerStableID] != u.Handle {
			return fmt.Errorf("orders: retail restore: owner stable id %d does not resolve to unit %d", ownerStableID, u.Handle)
		}
	}
	for _, record := range records {
		if record.ParentStableID == 0 || record.ParentStableID != ownerStableID {
			return fmt.Errorf("orders: retail restore: order parent %d does not match owner stable id %d", record.ParentStableID, ownerStableID)
		}
		if len(record.Main) != save.OrderBoxSize {
			return fmt.Errorf("orders: retail restore: order %d size %d, want 0x3A", record.Sequence, len(record.Main))
		}
		if binary.LittleEndian.Uint16(record.Main[:]) != record.ParentStableID {
			return fmt.Errorf("orders: retail restore: order %d parent mismatch", record.Sequence)
		}
		id, err := restoreOrderID(record)
		if err != nil {
			return err
		}
		targetID := binary.LittleEndian.Uint16(record.Main[2:])
		var target pool.Handle
		if targetID != 0 {
			if stable == nil {
				return fmt.Errorf("orders: retail restore: unresolved order target %d", targetID)
			}
			var ok bool
			target, ok = stable[targetID]
			if !ok || target == 0 {
				return fmt.Errorf("orders: retail restore: unresolved order target %d", targetID)
			}
		}
		queueFlags := binary.LittleEndian.Uint32(record.Main[0x32:])
		n := &Node{
			ID: id, Phase: record.Main[9], DynamicGate: binary.LittleEndian.Uint32(record.Main[0x0A:]),
			Deadline: int32(binary.LittleEndian.Uint32(record.Main[0x0E:])), Owner: u.Handle, Target: target,
			GoalX:  numeric.Fixed(int32(binary.LittleEndian.Uint32(record.Main[0x12:]))),
			GoalY:  numeric.Fixed(int32(binary.LittleEndian.Uint32(record.Main[0x16:]))),
			GoalZ:  numeric.Fixed(int32(binary.LittleEndian.Uint32(record.Main[0x1A:]))),
			GuardX: int16(binary.LittleEndian.Uint16(record.Main[0x1E:])), GuardY: int16(binary.LittleEndian.Uint16(record.Main[0x20:])),
			CachedX: int16(binary.LittleEndian.Uint16(record.Main[0x22:])), CachedY: int16(binary.LittleEndian.Uint16(record.Main[0x24:])),
			Param1: binary.LittleEndian.Uint32(record.Main[0x26:]), Param2: binary.LittleEndian.Uint32(record.Main[0x2A:]), Param3: binary.LittleEndian.Uint32(record.Main[0x2E:]),
			StaticGate: queueFlags, Flags: queueFlags, Satisfied: binary.LittleEndian.Uint32(record.Main[0x36:]),
			BuildDefKey: record.BuildTypeName, CreationTick: tick,
			RetailSubtypeCode: record.SubtypeCode,
			RetailSubtype:     append([]byte(nil), record.Subtype...),
		}
		if err := restoreSubtypeWords(n, record.Subtype, stable); err != nil {
			return err
		}
		// CreationTick is reinitialized from the current session tick; it is not
		// a persisted order word [05 "Saving economy, construction, and features"].
		if record.Secondary {
			rear = append(rear, n)
		} else {
			front = append(front, n)
		}
	}
	q := NewQueueWith(front, rear)
	q.ensureSingleActive()
	q.SetBinding(binding)
	BindQueue(u, q)
	return nil
}

func restoreSubtypeWords(n *Node, data []byte, stable map[uint16]pool.Handle) error {
	if n == nil || n.RetailSubtypeCode == 0 {
		return nil
	}
	if (n.RetailSubtypeCode == 2 || n.RetailSubtypeCode == 3) && len(data) < 8 {
		return fmt.Errorf("orders: retail restore: subtype %d payload too short", n.RetailSubtypeCode)
	}
	u16 := func(off int) uint16 { return binary.LittleEndian.Uint16(data[off:]) }
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(data[off:]) }
	resolve := func(id uint16) (pool.Handle, error) {
		if id == 0 {
			return 0, nil
		}
		h, ok := stable[id]
		if !ok || h == 0 {
			return 0, fmt.Errorf("orders: retail restore: unresolved subtype unit %d", id)
		}
		return h, nil
	}
	switch n.RetailSubtypeCode {
	case 2:
		if len(data) != save.OrderSubtypeCode2 {
			return fmt.Errorf("orders: retail restore: subtype 2 size %d, want %d", len(data), save.OrderSubtypeCode2)
		}
		var err error
		if n.RetailSubtypeUnitA, err = resolve(u16(8)); err != nil {
			return err
		}
		if n.RetailSubtypeUnitB, err = resolve(u16(0x1a)); err != nil {
			return err
		}
		for off := 0x1c; off < 0x26; off += 2 {
			n.RetailSubtypeWords16 = append(n.RetailSubtypeWords16, u16(off))
		}
		for off := 0x26; off < 0x36; off += 4 {
			n.RetailSubtypeWords32 = append(n.RetailSubtypeWords32, u32(off))
		}
	case 3:
		if len(data) != save.OrderSubtypeCode3 {
			return fmt.Errorf("orders: retail restore: subtype 3 size %d, want %d", len(data), save.OrderSubtypeCode3)
		}
		var err error
		if n.RetailSubtypeUnitA, err = resolve(u16(8)); err != nil {
			return err
		}
		n.RetailSubtypeWords16 = append(n.RetailSubtypeWords16, u16(0x0a), u16(0x24), u16(0x26), u16(0x28))
		for off := 0x0c; off < 0x24; off += 4 {
			n.RetailSubtypeWords32 = append(n.RetailSubtypeWords32, u32(off))
		}
	case 4, 5, 6:
		want := 0
		switch n.RetailSubtypeCode {
		case 4:
			want = save.OrderSubtypeCode4
		case 5:
			want = save.OrderSubtypeCode5
		case 6:
			want = save.OrderSubtypeCode6
		}
		if len(data) != want {
			return fmt.Errorf("orders: retail restore: subtype %d size %d, want %d", n.RetailSubtypeCode, len(data), want)
		}
		start := 4
		for off := start; off+4 <= len(data); off += 4 {
			n.RetailSubtypeWords32 = append(n.RetailSubtypeWords32, u32(off))
		}
	default:
		return fmt.Errorf("orders: retail restore: unsupported subtype code %d", n.RetailSubtypeCode)
	}
	return nil
}

func restoreOrderID(record save.OrderRecord) (ID, error) {
	if record.DescriptorName != "" {
		id := Lookup(record.DescriptorName)
		if id == 0 {
			return 0, fmt.Errorf("orders: retail restore: unknown descriptor %q", record.DescriptorName)
		}
		return id, nil
	}
	if len(record.Main) != save.OrderBoxSize {
		return 0, fmt.Errorf("orders: retail restore: missing descriptor in malformed record")
	}
	id := ID(record.Main[8])
	if id == 0 || DescriptorFor(id).Name == "" {
		return 0, fmt.Errorf("orders: retail restore: invalid descriptor ordinal %d", id)
	}
	return id, nil
}
