package orders

import (
	"encoding/binary"
	"fmt"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/units"
)

// RetailStableID resolves live unit handles to save-stable logical IDs.
type RetailStableID func(pool.Handle) (uint16, bool)

// RetailOrderImage is a detached writer view. The session save assembler owns
// box naming and catalog type-name de-duplication [08 R-SAVE-ORDER-01].
type RetailOrderImage struct {
	ParentStableID uint16
	Sequence       uint32
	Secondary      bool
	Main           []byte
	SubtypeCode    uint32
	Subtype        []byte
	DescriptorName string
	BuildTypeName  string
}

// RetailOrderImages walks the primary segment then the secondary segment,
// preserving traversal order and continuing sequence numbers across both
// [08 R-SAVE-02 §6; R-SAVE-ORDER-01].
func RetailOrderImages(u *units.Unit, stableID RetailStableID) ([]RetailOrderImage, error) {
	if u == nil {
		return nil, fmt.Errorf("orders: retail save: nil owner")
	}
	ownerID, err := retailStableID(stableID, u.Handle, "owner")
	if err != nil {
		return nil, err
	}
	q := QueueOfUnit(u)
	if q == nil {
		return nil, nil
	}
	primary, secondary := q.Primary(), q.Secondary()
	out := make([]RetailOrderImage, 0, len(primary)+len(secondary))
	appendSegment := func(nodes []*Node, rear bool) error {
		for _, n := range nodes {
			if n == nil {
				return fmt.Errorf("orders: retail save: nil node in owner %d queue", ownerID)
			}
			if len(out) == int(^uint32(0)) {
				return fmt.Errorf("orders: retail save: owner %d queue exceeds sequence range", ownerID)
			}
			image, err := retailOrderImage(n, ownerID, uint32(len(out)), rear, stableID)
			if err != nil {
				return err
			}
			out = append(out, image)
		}
		return nil
	}
	if err := appendSegment(primary, false); err != nil {
		return nil, err
	}
	if err := appendSegment(secondary, true); err != nil {
		return nil, err
	}
	return out, nil
}

func retailOrderImage(n *Node, ownerID uint16, sequence uint32, rear bool, stableID RetailStableID) (RetailOrderImage, error) {
	resolvedOwner, err := retailStableID(stableID, n.Owner, "node owner")
	if err != nil {
		return RetailOrderImage{}, err
	}
	if resolvedOwner != ownerID {
		return RetailOrderImage{}, fmt.Errorf("orders: retail save: node owner %d does not match queue owner %d", resolvedOwner, ownerID)
	}
	targetID, err := optionalRetailStableID(stableID, n.Target, "node target")
	if err != nil {
		return RetailOrderImage{}, err
	}
	desc := DescriptorFor(n.ID)
	if n.ID == 0 || desc.Name == "" {
		return RetailOrderImage{}, fmt.Errorf("orders: retail save: invalid descriptor ordinal %d", n.ID)
	}
	if !orderFixedFits(int64(n.GoalX)) || !orderFixedFits(int64(n.GoalY)) || !orderFixedFits(int64(n.GoalZ)) {
		return RetailOrderImage{}, fmt.Errorf("orders: retail save: owner %d sequence %d goal is outside signed 32-bit 16.16", ownerID, sequence)
	}
	main := make([]byte, save.OrderBoxSize)
	binary.LittleEndian.PutUint16(main, ownerID)
	binary.LittleEndian.PutUint16(main[2:], targetID)
	binary.LittleEndian.PutUint32(main[4:], n.RetailSubtypeCode)
	main[8], main[9] = byte(n.ID), n.Phase
	binary.LittleEndian.PutUint32(main[0x0a:], n.DynamicGate)
	binary.LittleEndian.PutUint32(main[0x0e:], uint32(n.Deadline))
	putOrderI32(main[0x12:], int32(n.GoalX))
	putOrderI32(main[0x16:], int32(n.GoalY))
	putOrderI32(main[0x1a:], int32(n.GoalZ))
	binary.LittleEndian.PutUint16(main[0x1e:], uint16(n.GuardX))
	binary.LittleEndian.PutUint16(main[0x20:], uint16(n.GuardY))
	binary.LittleEndian.PutUint16(main[0x22:], uint16(n.CachedX))
	binary.LittleEndian.PutUint16(main[0x24:], uint16(n.CachedY))
	binary.LittleEndian.PutUint32(main[0x26:], n.Param1)
	binary.LittleEndian.PutUint32(main[0x2a:], n.Param2)
	binary.LittleEndian.PutUint32(main[0x2e:], n.Param3)
	// Nanolathe gives the runtime marker bits and the descriptor mask named
	// fields; retail stores their combined record word [08 R-SAVE-ORDER-01].
	queueFlags := n.StaticGate | n.Flags
	if rear {
		queueFlags |= 0x40000
	} else {
		queueFlags &^= 0x40000
	}
	binary.LittleEndian.PutUint32(main[0x32:], queueFlags)
	binary.LittleEndian.PutUint32(main[0x36:], n.Satisfied)

	subtype, err := retailSubtypeImage(n, stableID)
	if err != nil {
		return RetailOrderImage{}, fmt.Errorf("orders: retail save: owner %d sequence %d: %w", ownerID, sequence, err)
	}
	return RetailOrderImage{
		ParentStableID: ownerID, Sequence: sequence, Secondary: rear,
		Main: main, SubtypeCode: n.RetailSubtypeCode, Subtype: subtype,
		DescriptorName: desc.Name, BuildTypeName: n.BuildDefKey,
	}, nil
}

func retailSubtypeImage(n *Node, stableID RetailStableID) ([]byte, error) {
	code := n.RetailSubtypeCode
	if code == 0 {
		if len(n.RetailSubtype) != 0 {
			return nil, fmt.Errorf("subtype payload present with zero code")
		}
		return nil, nil
	}
	if !save.ValidateSubtypeSize(int(code), len(n.RetailSubtype)) {
		return nil, fmt.Errorf("subtype %d has size %d", code, len(n.RetailSubtype))
	}
	data := append([]byte(nil), n.RetailSubtype...)
	switch code {
	case 2:
		a, err := optionalRetailStableID(stableID, n.RetailSubtypeUnitA, "subtype unit A")
		if err != nil {
			return nil, err
		}
		b, err := optionalRetailStableID(stableID, n.RetailSubtypeUnitB, "subtype unit B")
		if err != nil {
			return nil, err
		}
		binary.LittleEndian.PutUint16(data[8:], a)
		binary.LittleEndian.PutUint16(data[0x1a:], b)
	case 3:
		a, err := optionalRetailStableID(stableID, n.RetailSubtypeUnitA, "subtype unit A")
		if err != nil {
			return nil, err
		}
		binary.LittleEndian.PutUint16(data[8:], a)
	case 4, 5, 6:
	default:
		return nil, fmt.Errorf("unsupported subtype code %d", code)
	}
	return data, nil
}

func retailStableID(resolve RetailStableID, h pool.Handle, kind string) (uint16, error) {
	if h == 0 || resolve == nil {
		return 0, fmt.Errorf("orders: retail save: unresolved %s handle %d", kind, h)
	}
	id, ok := resolve(h)
	if !ok || id == 0 {
		return 0, fmt.Errorf("orders: retail save: unresolved %s handle %d", kind, h)
	}
	return id, nil
}

func optionalRetailStableID(resolve RetailStableID, h pool.Handle, kind string) (uint16, error) {
	if h == 0 {
		return 0, nil
	}
	return retailStableID(resolve, h, kind)
}

func putOrderI32(dst []byte, value int32) { binary.LittleEndian.PutUint32(dst, uint32(value)) }

func orderFixedFits(value int64) bool { return value >= -1<<31 && value <= 1<<31-1 }
