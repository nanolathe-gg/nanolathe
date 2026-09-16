// Established raw bulk helpers layered on HAPIBANK
// [08 "Unit and script records"].

package save

import (
	"encoding/binary"
	"fmt"
)

const (
	// Established raw box sizes [08 "Save-file organization"].
	UnitBoxSize        = 0xB8
	UnitBoxCompatSize  = 0xB6
	OrderBoxSize       = 0x3A
	OrderSubtypeCode2  = 0x36
	OrderSubtypeCode3  = 0x2A
	OrderSubtypeCode4  = 0x10
	OrderSubtypeCode5  = 0x18
	OrderSubtypeCode6  = 0x14
	ScriptSnapshotSize = 0x528
	FeatureTypeName    = 0x80

	UnitsVersionRetail = 0x11
)

// ValidateUnitBoxSize accepts the two established raw unit-box lengths
// [08 "Unit and script records"].
func ValidateUnitBoxSize(sz int) bool { return sz == UnitBoxSize || sz == UnitBoxCompatSize }

// ValidateSubtypeSize accepts an established order subtype length
// [08 "Unit and script records"].
func ValidateSubtypeSize(code, sz int) bool {
	switch code {
	case 2:
		return sz == OrderSubtypeCode2
	case 3:
		return sz == OrderSubtypeCode3
	case 4:
		return sz == OrderSubtypeCode4
	case 5:
		return sz == OrderSubtypeCode5
	case 6:
		return sz == OrderSubtypeCode6
	default:
		return false
	}
}

// Units account helpers [P1-13 §3.4].

// UnitsAccount holds the unit and script records [08 "Unit and script records"].
const UnitsAccount = "Units"

// WriteUnitsHeader writes the Units account Version and Number of Units
// scalars [P1-13 §3.4]. Version must be 0x11 for retail load to consider
// units; other values skip whole Units non-transactionally [P1-13 §3.4].
func WriteUnitsHeader(b *Builder, count int) {
	if b == nil {
		return
	}
	ac := builderAccount(b, UnitsAccount)
	ac.SetInt("Version", UnitsVersionRetail)
	ac.SetInt("Number of Units", int32(count))
}

// ReadUnitsHeader reads Version and Number of Units from the Units account
// [P1-13 §3.4]. Returns version, count, and whether the account exists.
// Missing account is not an error; Version!=0x11 is the retail skip gate.
func ReadUnitsHeader(bank *Bank) (version int32, count int32, ok bool) {
	ac, ok := bank.Account(UnitsAccount)
	if !ok {
		return 0, 0, false
	}
	v, _ := ac.Int("Version")
	c, _ := ac.Int("Number of Units")
	return v, c, true
}

// UnitStableID extracts the u16 stableID at 0x21 from a 0xB8 payload
// [P1-13 §2.2]. Returns 0 for null sentinel [P1-13 §2.1].
func UnitStableID(payload []byte) uint16 {
	if len(payload) < 0x23 {
		return 0
	}
	return binary.LittleEndian.Uint16(payload[0x21:])
}

// WriteUnitBox appends an opaque raw unit record of an established length.
// Field interpretation and unit reconstruction remain unsupported
// [08 "Unit and script records"].
func WriteUnitBox(b *Builder, index int, payload []byte) error {
	if b == nil {
		return fmt.Errorf("save: nil builder")
	}
	if !ValidateUnitBoxSize(len(payload)) {
		return fmt.Errorf("save: unit box %d size %d is not an established unit-box length", index, len(payload))
	}
	ac := builderAccount(b, UnitsAccount)
	ac.AppendBox("", int32(index), payload)
	return nil
}

// WriteOrderBox appends an opaque raw order record using the established
// numbered box name [08 "Account inventory"].
func WriteOrderBox(b *Builder, parentStableID uint16, seq int, payload []byte) error {
	if b == nil {
		return fmt.Errorf("save: nil builder")
	}
	if len(payload) != OrderBoxSize {
		return fmt.Errorf("save: order box size %d not 0x3A [P1-13 §2.3]", len(payload))
	}
	ac := builderAccount(b, UnitsAccount)
	name := fmt.Sprintf("u%04xm%04x", parentStableID, seq)
	ac.AppendBox(name, 0, payload)
	return nil
}

// ScriptBoxName returns the established sequential script-box name. Script
// body interpretation is intentionally unsupported [08 "Unit and script records"].
func ScriptBoxName(enumIdx int) string { return fmt.Sprintf("Script%d", enumIdx) }

// FeaturesAccount holds the feature records [08 "Feature records"].
const FeaturesAccount = "Features"

// FeatureTypeNamesBox is the padded feature-name table inside FeaturesAccount.
const FeatureTypeNamesBox = "Feature Type Names"

// WriteFeatureTypeNames writes the established padded feature-name table.
// Feature record bodies remain unsupported [08 "Feature records"].
func WriteFeatureTypeNames(b *Builder, names []string) error {
	if b == nil {
		return fmt.Errorf("save: nil builder")
	}
	if len(names) == 0 {
		return nil
	}
	payload := make([]byte, len(names)*FeatureTypeName)
	for i, n := range names {
		copy(payload[i*FeatureTypeName:], n)
	}
	ac := builderAccount(b, FeaturesAccount)
	ac.AppendBox(FeatureTypeNamesBox, 0, payload)
	return nil
}

// ValidateFeatureTypeNamesSize checks complete padded name entries
// [08 "Feature records"].
func ValidateFeatureTypeNamesSize(sz int) bool { return sz%FeatureTypeName == 0 }

// MeteorAccount is the established scheduler account [08 "Meteor showers"].
const MeteorAccount = "Meteor"

// MeteorScalars is the complete established Meteor account value. All nine
// wire items are integers; no weapon identity is persisted [08 "Meteor showers"].
type MeteorScalars struct {
	Enabled        int32
	Active         int32
	NextStrikeTime int32
	TimeStrikeEnds int32
	NextHitTime    int32
	OriginX        int32
	OriginZ        int32
	TargetX        int32
	TargetZ        int32
}

var meteorScalarNames = [...]string{
	"Enabled",
	"Active",
	"Next Strike Time",
	"Time Strike Ends",
	"Next Hit Time",
	"Origin X",
	"Origin Z",
	"Target X",
	"Target Z",
}

func (m MeteorScalars) values() [len(meteorScalarNames)]int32 {
	return [...]int32{
		m.Enabled,
		m.Active,
		m.NextStrikeTime,
		m.TimeStrikeEnds,
		m.NextHitTime,
		m.OriginX,
		m.OriginZ,
		m.TargetX,
		m.TargetZ,
	}
}

// WriteMeteorScalars writes all nine Meteor integer items in established
// writer order [08 "Account inventory"].
func WriteMeteorScalars(b *Builder, m MeteorScalars) {
	if b == nil {
		return
	}
	ac := builderAccount(b, MeteorAccount)
	values := m.values()
	for i, name := range meteorScalarNames {
		ac.SetInt(name, values[i])
	}
}
