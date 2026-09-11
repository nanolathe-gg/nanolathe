package features

import (
	"encoding/binary"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
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
	// FeatureNames is the map's original TNT table; definitions admitted by
	// corpse or successor placement extend FeatureDefs alone. The save names
	// every current definition in ordinal order [08 R-SAVE-FEATURE-01].
	names := append([]string(nil), t.FeatureNames...)
	if len(names) < len(t.FeatureDefs) {
		names = append(names, make([]string, len(t.FeatureDefs)-len(names))...)
	}
	for i, def := range t.FeatureDefs {
		if names[i] == "" && def != nil {
			names[i] = def.CanonicalKey
		}
	}
	image := RetailFeatureImage{TypeNames: names}
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
			if int(feature) < len(image.TypeNames) {
				name = image.TypeNames[feature]
			}
			if name == "" {
				return RetailFeatureImage{}, fmt.Errorf("features: retail save: feature (%d,%d) has no authored type identity", cx, cz)
			}

			inst := s.instances[idx]
			// Object-backed records always use the 3D family. For non-model
			// features, the cell's animation-present bit selects the animation
			// family; all remaining anchors are normal [08 R-SAVE-FEATURE-01].
			// The bit is set for a sprite only by ignition and the die/reclaim
			// transitions [05 R-FEAT-01 §15], so in this build it is "the
			// anchor carries an event record" — a resting sprite whose cell
			// bit is set with no record behind it has no live sequence pointer
			// to match a family and is skipped [08 R-SESS-01 §4].
			family := 0
			if def.Object != "" {
				family = 2
			} else if cell.Occupied() || (inst != nil && (inst.IsBurning || inst.IsAnimating)) {
				family = 1
			}
			if family == 2 && inst == nil {
				// Not a retail unknown: [08 R-SESS-01 §4] states what the
				// record carries, and the live 3D side simply is not
				// recoverable from Terrain.Plot alone. Refusing is the only
				// honest answer — a save must not fill those words with zeros.
				return RetailFeatureImage{}, fmt.Errorf("features: retail save: feature (%d,%d) family %d has no live instance state", cx, cz, family)
			}
			if family == 1 && (inst == nil || !(inst.IsBurning || inst.IsAnimating) || !inst.cursor.running() || inst.AnimationSelector > 2) {
				// [08 R-SESS-01 §4]: the nibble is chosen by comparing the
				// cell's live sequence pointer against the definition's three
				// family sequences, and a cell matching NONE is skipped
				// entirely — no record, no count. An attached bit with no
				// record, a record with no running cursor, and a selector
				// outside the three families are all that case.
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
				// The live record's accumulator word, the live main cursor's
				// frame byte, and the selector in the low nibble with the live
				// countdown's HIGH nibble in the high nibble — the low nibble
				// of the countdown is the save's documented loss
				// [08 R-SAVE-FEATURE-01]. Both animation words are read off the
				// authoritative cursor and countdown, never a mirror.
				binary.LittleEndian.PutUint16(row.Data[6:], inst.DamageAccumulator)
				row.Data[8] = uint8(inst.cursor.frame)
				row.Data[9] = (uint8(inst.BurnCountdown) & 0xf0) | (inst.AnimationSelector & 0x0f)
				image.Animating = append(image.Animating, row)
			case 2:
				// The 3D record's five values, named [08 R-SAVE-FEATURE-01]:
				// the instance's position triple at 0x08..0x13 as three 16.16
				// world coordinates, then its orientation — bank and heading at
				// 0x14..0x17 (retail's one 32-bit copy of the pair) and pitch at
				// 0x18..0x19 — with the damage accumulator sharing 0x06..0x07
				// with the other two families. Y is the instance's CURRENT
				// height, which is why a sinking wreck's descent is saved.
				row.Data = make([]byte, RetailThreeDFeatureSize)
				binary.LittleEndian.PutUint16(row.Data[0:], uint16(cx))
				binary.LittleEndian.PutUint16(row.Data[2:], uint16(cz))
				binary.LittleEndian.PutUint16(row.Data[4:], feature)
				binary.LittleEndian.PutUint16(row.Data[6:], inst.DamageAccumulator)
				putFixedWire(row.Data[0x08:], inst.X)
				putFixedWire(row.Data[0x0c:], inst.Y)
				putFixedWire(row.Data[0x10:], inst.Z)
				binary.LittleEndian.PutUint16(row.Data[0x14:], inst.Bank)
				binary.LittleEndian.PutUint16(row.Data[0x16:], inst.Heading)
				binary.LittleEndian.PutUint16(row.Data[0x18:], inst.Pitch)
				image.ThreeD = append(image.ThreeD, row)
			}
		}
	}
	return image, nil
}

// putFixedWire writes one world coordinate as the save's 32-bit 16.16 word, and
// fixedFromWire reads it back sign-extended. The live value's backing width is
// wider than the wire's, which is the narrowing every save-boundary coordinate
// takes [08 R-SAVE-FEATURE-01][I13].
func putFixedWire(dst []byte, v numeric.Fixed) {
	binary.LittleEndian.PutUint32(dst, uint32(int32(v.Raw())))
}

func fixedFromWire(src []byte) numeric.Fixed {
	return numeric.Fixed(int32(binary.LittleEndian.Uint32(src)))
}

// RetailSaveImage is an alias named for callers assembling a complete save.
func (s *Service) RetailSaveImage() (RetailFeatureImage, error) {
	return s.RetailFeatureImage()
}
