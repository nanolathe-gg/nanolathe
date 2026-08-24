// Package save — retail bulk layouts per [P1-13].
//
// This file implements the byte-exact bulk record layouts, fix-up order,
// logical-ID resolution, partial-load and I/O policy, and deadline
// consumers that P1-13 closes [P1-13]. Retail saves are HAPIBANK banks
// with 34-byte headers and 32-byte account headers; string-pool offsets
// are trailing logical pool offsets [P1-13 §2.1]. Typed items (8-byte
// int, 12-byte double, 8-byte string) precede 16-byte box descriptors
// (nameOr-1, number, absPayloadOffset, length) plus payload bytes
// concatenated [P1-13 §2.1]. Logical refs are u16 stableID 0=null
// [P1-13 §2.1].
//
// Retail bulk beyond the established Summary/Camera/Players/GameTime/
// Alliances/Player%i tables is byte-exact per [P1-13 §2.2-2.10]:
//
//	Unit 0xB8 (184) with leak bits 17..19 at 0xB4 high3 leaked [P1-13 §2.2];
//	order 0x3A (58) plus subtypes g codes 2:0x36 3:0x2A 4:0x10 5:0x18 6:0x14
//	with leaked prefixes 00..07 / 00..03 discarded [P1-13 §2.4];
//	script 0x528 + stack*4 + pieces*0x6C (108 = 27 u32, slots 24/25 leak)
//	[P1-13 §2.6]; accessory 48 = 2×0x18 [P1-13 §2.5]; mobile 35 = 0x23
//	with low3 masked [P1-13 §2.5]; features n*0x80 + n*8 / n*10 / n*26
//	[P1-13 §2.7]; metal W*H [P1-13 §2.8]; playerFeatures W*H/2 packed
//	nibbles (b>>3&0xF)|(a&0xF8<<1) [P1-13 §2.8]; mapping W*H/2 raw
//	0x14273 [P1-13 §2.8]; meteor nine i32 scalars [P1-13 §2.9];
//	triggers per-type VictoryCondition_*/DefeatCondition_* scalars
//	Satisfied/Celebrated + NumUnits/NumLeftToKill [P1-13 §2.10];
//	radar image 8+W*H preview-only not consumed by battle load [P1-13].
//
// Fix-up order Players→Camera→Features→Metal→PlayerFeatures→Mapping→
// Units recursive lowest-free forced+limit→acc/mob→visibility [P1-13 §3.5].
// Partial-load is non-transactional: Version!=0x11 skips whole Units
// account but load returns 1; Gametype 1/2 else invalid; BetweenMissions
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P1-13 §3.3]. Scheduler 28B persisted verbatim via Players/GameTime
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P1-13 §8.11]; RNG states omitted and reseeded via QPC^0x66E29572|1
// and time(NULL) before any bank restore [P1-13 §5]; projectile/path/
// effect/radar surface omitted intentionally (bounded negative) [P1-13 §2.11].
package save

import (
	"encoding/binary"
	"fmt"
)

const (
	// Bulk record sizes [P1-13 §2.2-2.6][I13].
	UnitBoxSize        = 0xB8  // 184 bytes [P1-13 §2.2]
	UnitBoxCompatSize  = 0xB6  // 182 bytes compat branch clears ID [P1-13 §2.2]
	OrderBoxSize       = 0x3A  // 58 bytes [P1-13 §2.3]
	OrderSubtypeCode2  = 0x36  // 54 bytes [P1-13 §2.4]
	OrderSubtypeCode3  = 0x2A  // 42 bytes [P1-13 §2.4]
	OrderSubtypeCode4  = 0x10  // 16 bytes [P1-13 §2.4]
	OrderSubtypeCode5  = 0x18  // 24 bytes [P1-13 §2.4]
	OrderSubtypeCode6  = 0x14  // 20 bytes [P1-13 §2.4]
	ScriptSnapshotSize = 0x528 // 1320 bytes = 8*0xA4 blocks [P1-13 §2.6]
	ScriptPieceSize    = 0x6C  // 108 bytes = 27 u32, slots 24/25 leak [P1-13 §2.6]
	AccessorySize      = 48    // 2*0x18 split [P1-13 §2.5]
	MobileSize         = 0x23  // 35 bytes [P1-13 §2.5]
	FeatureTypeName    = 0x80  // 128 bytes padded [P1-13 §2.7]

	UnitsVersionRetail = 0x11 // required Version i32 [P1-13 §3.4]
)

// UnitBulkLayout documents the 0xB8 retail unit record offsets [P1-13 §2.2]
// with leak bits 17..19 at 0xB4. All integers little-endian; file offsets
// are within the 0xB8 payload, not Go struct layout [I13].
var UnitBulkFieldDocs = map[int]string{
	0x00: "defName 32 bytes NUL pad [P1-13 §2.2]",
	0x20: "unit+0xFF u8",
	0x21: "stableID u16 low [P1-13 §2.2] 0=null [P1-13 §2.1]",
	0x23: "order count u32 from +0x5C/+0x60 scans",
	0x27: "alive bool u32 ([unit]!=0)",
	0x2B: "unit+0x6A u32",
	0x3B: "unit+0x68 low u16",
	0x3D: "unit+0x108 low u16",
	0x3F: "unit+0xB8 low u16",
	0x41: "3×24 embeddings n*0x1C [P1-13 §2.2]",
	0x89: "stableID for +0x86 unit if active else 0",
	0x8B: "stableID for +0xF0 unit if active else 0",
	0xB4: "packed unit+0x110 + nibble 0x10F bits 17..19 leak [P1-13 §2.2]",
}

// OrderBulkLayout documents the 0x3A order main box [P1-13 §2.3].
var OrderBulkFieldDocs = map[int]string{
	0x00: "parent stableID u16 must equal parent unit ID on load [P1-13 §2.3]",
	0x02: "linked unit stableID u16 resolved via [analysis omitted] 0=null",
	0x04: "subtype code *(order+8) 0 no subtype [P1-13 §2.3]",
	0x08: "descriptor index [order+4] u8",
	0x09: "byte [order+5] u8",
	0x0A: "twelve u32 from +0x06,+0x0A,+0x22…+0x4E [P1-13 §2.3]",
}

// ValidateUnitBoxSize returns true when sz is a valid unit box size
// (0xB8 or 0xB6 compat) [P1-13 §3.4]. 0xB6 clears stableID and restores
// nothing [P1-13 §3.4].
func ValidateUnitBoxSize(sz int) bool { return sz == UnitBoxSize || sz == UnitBoxCompatSize }

// ValidateOrderBoxSize returns true when sz is exactly 0x3A [P1-13 §2.3].
func ValidateOrderBoxSize(sz int) bool { return sz == OrderBoxSize }

// ValidateSubtypeSize returns true when sz matches the subtype code
// length table [P1-13 §2.4].
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

// IsUnitsLoadable reports whether the bank's Units account is loadable per
// the retail Version==0x11 gate [P1-13 §3.4]. Retail skips whole Units
// non-transactionally when Version!=0x11, but load returns 1 [P1-13 §3.3].
func IsUnitsLoadable(bank *Bank) bool {
	v, _, ok := ReadUnitsHeader(bank)
	if !ok {
		return false
	}
	return v == UnitsVersionRetail
}

// UnitStableID extracts the u16 stableID at 0x21 from a 0xB8 payload
// [P1-13 §2.2]. Returns 0 for null sentinel [P1-13 §2.1].
func UnitStableID(payload []byte) uint16 {
	if len(payload) < 0x23 {
		return 0
	}
	return binary.LittleEndian.Uint16(payload[0x21:])
}

// WriteUnitBox appends a numbered unit record with payload of exactly
// 0xB8 (or 0xB6 compat) [P1-13 §2.2][P1-13 §3.4]. Payload is copied
// byte-for-byte; leak bits 17..19 at 0xB4 are preserved verbatim
// [P1-13 §2.2][I13].
func WriteUnitBox(b *Builder, index int, payload []byte) error {
	if b == nil {
		return fmt.Errorf("save: nil builder")
	}
	if !ValidateUnitBoxSize(len(payload)) {
		return fmt.Errorf("save: unit box %d size %d not 0xB8 nor 0xB6 [P1-13 §2.2]", index, len(payload))
	}
	ac := builderAccount(b, UnitsAccount)
	ac.AppendBox("", int32(index), payload)
	return nil
}

// ReadUnitBox reads numbered unit record index requiring exactly 0xB8 or
// 0xB6 [P1-13 §3.4]. Returns payload copy and whether box existed with
// valid size. Short or wrong size is not an error for retail; it skips
// that record (compat path clears ID) [P1-13 §3.4].
func ReadUnitBox(bank *Bank, index int) ([]byte, bool) {
	ac, ok := bank.Account(UnitsAccount)
	if !ok {
		return nil, false
	}
	data, ok := ac.BoxData("", int32(index))
	if !ok {
		return nil, false
	}
	if !ValidateUnitBoxSize(len(data)) {
		return nil, false
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, true
}

// Order helpers [P1-13 §2.3-2.4].

// OrderParentID extracts parent stableID at 0x00 from a 0x3A payload
// [P1-13 §2.3].
func OrderParentID(payload []byte) uint16 {
	if len(payload) < 2 {
		return 0
	}
	return binary.LittleEndian.Uint16(payload[0x00:])
}

// OrderLinkedID extracts linked unit stableID at 0x02 [P1-13 §2.3].
func OrderLinkedID(payload []byte) uint16 {
	if len(payload) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint16(payload[0x02:])
}

// WriteOrderBox appends a u%04xm%04x order box of exactly 0x3A bytes
// [P1-13 §2.3]. Name is derived from parent stableID and seq for
// diagnostics; loader uses box name plus _name side-channel [P1-13 §2.3].
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

// Script helpers [P1-13 §2.6].

// ScriptBoxName returns the retail Script%i box name for enumeration index
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func ScriptBoxName(enumIdx int) string { return fmt.Sprintf("Script%d", enumIdx) }

// ValidateScriptBoxSize returns true when total size equals
// 0x528 + stackWords*4 + pieceCount*0x6C [P1-13 §2.6].
func ValidateScriptBoxSize(total, stackWords, pieceCount int) bool {
	expected := ScriptSnapshotSize + stackWords*4 + pieceCount*ScriptPieceSize
	return total == expected
}

// Feature helpers [P1-13 §2.7].

const FeaturesAccount = "Features"

// Feature box names [P1-13 §2.7].
const (
	FeatureTypeNamesBox = "Feature Type Names"
	FeatureNormalBox    = "Normal"
	FeatureAnimatingBox = "Animating"
	Feature3DBox        = "3D"
)

// WriteFeatureTypeNames writes the n*0x80 type name box [P1-13 §2.7].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func WriteFeatureTypeNames(b *Builder, names []string) error {
	if b == nil {
		return fmt.Errorf("save: nil builder")
	}
	if len(names) == 0 {
		return nil
	}
	payload := make([]byte, len(names)*FeatureTypeName)
	for i, n := range names {
		off := i * FeatureTypeName
		copy(payload[off:], []byte(n))
	}
	ac := builderAccount(b, FeaturesAccount)
	ac.AppendBox(FeatureTypeNamesBox, 0, payload)
	return nil
}

// ValidateFeatureTypeNamesSize returns true when size % 128 ==0 [P1-13 §2.7].
func ValidateFeatureTypeNamesSize(sz int) bool { return sz%FeatureTypeName == 0 }

// Metal / PlayerFeatures / Mapping helpers [P1-13 §2.8].

const (
	MetalAccount          = "Metal"
	PlayerFeaturesAccount = "PlayerFeatures"
	MappingAccount        = "Mapping"
	PlotmapBox            = "Plotmap"
	MappingDataBox        = "Data"
)

// ValidateMetalSize returns true when size equals W*H [P1-13 §2.8].
func ValidateMetalSize(sz, w, h int) bool { return sz == w*h }

// ValidateMappingSize returns true when size equals W*H/2 [P1-13 §2.8].
func ValidateMappingSize(sz, w, h int) bool { return sz == (w*h)/2 }

// Meteor helpers [P1-13 §2.9].

const MeteorAccount = "Meteor"

// WriteMeteorScalars writes the nine i32 scalars of the Meteor account
// [P1-13 §2.9].
func WriteMeteorScalars(b *Builder, fields map[string]int32) {
	if b == nil {
		return
	}
	ac := builderAccount(b, MeteorAccount)
	for k, v := range fields {
		ac.SetInt(k, v)
	}
}

// Trigger helpers [P1-13 §2.10].

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// account intentionally [P1-13 §2.10].
func IsTriggerGated(gametype int32) bool { return gametype == 1 }

// FixUpOrder documents the retail fix-up recursion order
// Players→Camera→Features→Metal→Units recursive lowest-free
// forced+limit→acc/mob→visibility [P1-13 §3.5].
const FixUpOrder = "Players→Camera→Features→Metal→PlayerFeatures→Mapping→Units(recursive lowest-free forced+limit→acc/mob→visibility)→Meteor→Triggers"

// ApplyFixUpOrder is a stub that documents the order; actual pointer
// fix-up uses stableID scan of Number of Units for matching stableID
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// 0x800000+0x15A check [P1-13 §3.4]. The function is a no-op in this
// fixture but preserves the order string for audit.
func ApplyFixUpOrder(bank *Bank) string { return FixUpOrder }
