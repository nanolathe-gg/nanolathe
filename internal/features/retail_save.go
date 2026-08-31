package features

import (
	"encoding/binary"
	"fmt"
)

// RetailFeatureRecord is one detached feature record in the exact wire shape
// used by its family. TypeName is the authored identity side channel; TypeID
// is the saved terrain/catalog ordinal. Data is copied so callers can safely
// assemble a bank after the simulation continues [08 R-SAVE-FEATURE-01].
type RetailFeatureRecord struct {
	X, Z     uint16
	TypeID   uint16
	TypeName string
	Data     []byte
}

// RetailFeatureImage is the feature portion of a live retail save. Records
// are in row-major anchor order within each family, and the three families are
// kept separate because retail writes three boxes [08 R-SAVE-FEATURE-01].
type RetailFeatureImage struct {
	TypeNames []string
	Normal    []RetailFeatureRecord
	Animating []RetailFeatureRecord
	ThreeD    []RetailFeatureRecord
}

const (
	RetailNormalFeatureSize    = 8
	RetailAnimatingFeatureSize = 10
	RetailThreeDFeatureSize    = 26
)

// RetailFeatureImage returns a detached projection of the live feature grid.
// The grid, rather than the service map, owns writer order: a feature is
// eligible only at a real anchor cell and is emitted once in z-then-x order.
// [08 R-SAVE-FEATURE-01]
func (s *Service) RetailFeatureImage() (RetailFeatureImage, error) {
	if s == nil || s.Terrain == nil {
		return RetailFeatureImage{}, fmt.Errorf("features: retail save: nil service or terrain")
	}
	t := s.Terrain
	if t.CellW <= 0 || t.CellH <= 0 {
		return RetailFeatureImage{}, fmt.Errorf("features: retail save: invalid terrain dimensions %dx%d", t.CellW, t.CellH)
	}
	total := int64(t.CellW) * int64(t.CellH)
	if total < 0 || total > int64(len(t.Plot)) {
		return RetailFeatureImage{}, fmt.Errorf("features: retail save: terrain plot has %d cells, expected at least %d", len(t.Plot), total)
	}
	image := RetailFeatureImage{TypeNames: append([]string(nil), t.FeatureNames...)}
	for cz := int32(0); cz < t.CellH; cz++ {
		for cx := int32(0); cx < t.CellW; cx++ {
			idx := int(cz*t.CellW + cx)
			cell := t.Plot[idx]
			feature := cell.Feature()
			if !cell.IsRealFeature() {
				continue
			}
			def, ok := t.FeatureDefAt(feature)
			if !ok || def == nil {
				return RetailFeatureImage{}, fmt.Errorf("features: retail save: feature (%d,%d) has unbound type %d", cx, cz, feature)
			}
			name := ""
			if int(feature) < len(t.FeatureNames) {
				name = t.FeatureNames[feature]
			}
			if name == "" {
				// A synthetic terrain may not carry the TNT side table. The
				// compiled canonical key is the only identity the runtime owns;
				// do not manufacture a display name [08 R-SAVE-FEATURE-01].
				name = def.CanonicalKey
			}
			if name == "" {
				return RetailFeatureImage{}, fmt.Errorf("features: retail save: feature (%d,%d) has no authored type identity", cx, cz)
			}

			inst := s.instances[idx]
			// Object-backed records always use the 3D family. For non-model
			// features, the live attached/animating bit selects the animation
			// family; all remaining anchors are normal [08 R-SAVE-FEATURE-01].
			family := 0
			if def.Object != "" {
				family = 2
			} else if cell.Occupied() || (inst != nil && inst.IsAnimating) {
				family = 1
			}
			if family != 0 && inst == nil {
				// TODO(question): the live animation/3D side is not recoverable
				// from Terrain.Plot alone; a save must not fill its words with
				// zeros. Resolve by binding the runtime instance side first.
				return RetailFeatureImage{}, fmt.Errorf("features: retail save: feature (%d,%d) family %d has no live instance state", cx, cz, family)
			}
			if family == 1 && inst.AnimationSelector > 2 {
				// [08 R-SESS-01 §4] The writer recognizes only burn, death,
				// and reclaim sequences. A live animation bound to another
				// sequence is absent from the save, rather than being relabeled
				// through a masked selector nibble.
				continue
			}
			row := RetailFeatureRecord{X: uint16(cx), Z: uint16(cz), TypeID: feature, TypeName: name}
			switch family {
			case 0:
				row.Data = make([]byte, RetailNormalFeatureSize)
				binary.LittleEndian.PutUint16(row.Data[0:], uint16(cx))
				binary.LittleEndian.PutUint16(row.Data[2:], uint16(cz))
				binary.LittleEndian.PutUint16(row.Data[4:], feature)
				binary.LittleEndian.PutUint16(row.Data[6:], cell.AnchorWord())
				image.Normal = append(image.Normal, row)
			case 1:
				row.Data = make([]byte, RetailAnimatingFeatureSize)
				binary.LittleEndian.PutUint16(row.Data[0:], uint16(cx))
				binary.LittleEndian.PutUint16(row.Data[2:], uint16(cz))
				binary.LittleEndian.PutUint16(row.Data[4:], feature)
				binary.LittleEndian.PutUint16(row.Data[6:], inst.AnimationState)
				row.Data[8] = inst.AnimationFrame
				row.Data[9] = (inst.AnimationCountdown&0x0f)<<4 | (inst.AnimationSelector & 0x0f)
				image.Animating = append(image.Animating, row)
			case 2:
				row.Data = make([]byte, RetailThreeDFeatureSize)
				binary.LittleEndian.PutUint16(row.Data[0:], uint16(cx))
				binary.LittleEndian.PutUint16(row.Data[2:], uint16(cz))
				binary.LittleEndian.PutUint16(row.Data[4:], feature)
				binary.LittleEndian.PutUint16(row.Data[6:], inst.AnimationState)
				copy(row.Data[8:], inst.OpaqueState[:])
				image.ThreeD = append(image.ThreeD, row)
			}
		}
	}
	return image, nil
}

// RetailSaveImage is an alias named for callers assembling a complete save.
func (s *Service) RetailSaveImage() (RetailFeatureImage, error) {
	return s.RetailFeatureImage()
}

// RetailFeatureRecordFamilySize returns the established payload size for one
// feature family, or zero for an unknown family [08 R-SAVE-FEATURE-01].
func RetailFeatureRecordFamilySize(family int) int {
	switch family {
	case 0:
		return RetailNormalFeatureSize
	case 1:
		return RetailAnimatingFeatureSize
	case 2:
		return RetailThreeDFeatureSize
	default:
		return 0
	}
}
