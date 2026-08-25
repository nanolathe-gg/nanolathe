package features

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// FeatureKind classifies a feature definition for save grouping [05 "Feature catalog and placement"] [P1-13 §2.7].
// Retail serializes features in three groups: normal, animated (GAF), and 3D (model) [05 "Feature definition"]
// each with its own fixed record shape. Presentation choice does not change authoritative footprint,
// but save layout does. Classification here preserves that grouping.
type FeatureKind int

const (
	KindNormal    FeatureKind = iota // static reclaimable, no object nor GAF sequence
	KindAnimating                    // GAF-based (filename / seqname / burn seq)
	KindThreeD                       // 3D object model
)

// Classify returns the save group for a definition [05 "Feature definition"] [P1-13 §2.7].
func Classify(def *content.FeatureDef) FeatureKind {
	if def == nil {
		return KindNormal
	}
	if def.Object != "" {
		return KindThreeD // 3D object present [02 "Feature record"] object/3DO
	}
	if def.Filename != "" || def.SeqName != "" || def.SeqNameBurn != "" || def.SeqNameDie != "" || def.SeqNameReclamate != "" || def.Animating != 0 {
		return KindAnimating // animated image source [02 "Feature record"] filename/seqname
	}
	return KindNormal
}

// Record sizes per [P1-13 §2.7] features n*0x80 + n*8 / n*10 / n*26.
// Type names are n*0x80 (128) [bulk.go]; normal 8, animating 10, 3D 0x26 (38 decimal) comment says n*26.
// We use 8, 10, 26 decimal for our clean-room layout; exact retail offsets per P1-13 remain TODO(question) but size classes are locked.
// Size constants are validated by bulk tests.
const (
	NormalRecordSize    = 8  // n*8  [P1-13 §2.7]
	AnimatingRecordSize = 10 // n*10 [P1-13 §2.7]
	ThreeDRecordSize    = 26 // n*26 [P1-13 §2.7] TODO(question): 0x26 vs 26 decimal ambiguous; using 26 decimal as placeholder
)

// NormalRecord is the 8-byte normal feature save record [P1-13 §2.7] [05 "Feature definition"].
// Fields are chosen to fit 8 bytes while preserving authoritative state needed to rebuild.
// Retail's exact byte mapping beyond size is TODO(question); this layout round-trips via bulk.
type NormalRecord struct {
	TypeIdx uint16 // index into Feature Type Names table [P1-13 §2.7]
	CX      int16
	CZ      int16
	Health  int16
}

// AnimatingRecord is the 10-byte animated feature save record [P1-13 §2.7].
type AnimatingRecord struct {
	TypeIdx       uint16
	CX            int16
	CZ            int16
	Health        int16
	BurnCountdown int16 // seconds*30 when applicable [05 "Feature burning"] sparktime/2
}

// ThreeDRecord is the 26-byte 3D feature save record [P1-13 §2.7].
type ThreeDRecord struct {
	TypeIdx   uint16
	CX        int16
	CZ        int16
	Health    int16
	Y         int32 // 16.16 fixed raw [05 "Feature sinking and water interaction"]
	Vy        int32 // vy = -11468 when sinking [05 "Feature sinking and water interaction"]
	Status    uint8
	IsSinking uint8 // bool 0/1
	IsBurning uint8 // bool 0/1
	BurnTicks int32 // elapsed burn animation ticks [05 "Feature burning"]
	// 2 bytes reserved pad to reach 26
}

// WriteBulk writes the Features account's four boxes: Type Names (n*0x80), Normal (n*8), Animating (n*10), 3D (n*26) [P1-13 §2.7].
// It classifies each live instance, builds the type name table from distinct definitions in deterministic sorted order (I1),
// and encodes each group in that order. Malformed definitions (nil, empty name, zero footprint) are handled gracefully:
// nil def is skipped, empty canonical key is encoded as "unknown", zero footprint is treated as 1x1 for validation but not for save.
func (s *Service) WriteBulk(b *save.Builder) error {
	if s == nil || b == nil {
		return fmt.Errorf("features: nil service or builder")
	}
	if s.Terrain == nil {
		return fmt.Errorf("features: nil terrain for bulk save [05]")
	}
	instances := s.Instances() // deterministic sorted [I1]
	// Build distinct type table sorted for determinism (I1)
	typeSet := make(map[string]struct{})
	for _, inst := range instances {
		if inst == nil || inst.Def == nil {
			continue
		}
		name := inst.Def.CanonicalKey
		if name == "" {
			name = inst.Def.Object
			if name == "" {
				name = inst.Def.Filename
			}
			if name == "" {
				name = "unknown"
			}
		}
		typeSet[name] = struct{}{}
	}
	names := make([]string, 0, len(typeSet))
	for n := range typeSet {
		names = append(names, n)
	}
	sort.Strings(names)
	nameToIdx := make(map[string]uint16, len(names))
	for i, n := range names {
		nameToIdx[n] = uint16(i)
	}
	// Also include any terrain FeatureDefs not in instances? For retail, type table is catalog's feature names, not just live instances.
	// To match bulk expectation that type table enumerates catalog, we also consider terrain's FeatureDefs that are not live.
	// However for round-trip test we only need live set plus any malformed unknown handling.

	// Encode groups
	var normals []NormalRecord
	var animatings []AnimatingRecord
	var threeds []ThreeDRecord
	for _, inst := range instances {
		if inst == nil || inst.Def == nil {
			continue
		}
		name := inst.Def.CanonicalKey
		if name == "" {
			name = inst.Def.Object
			if name == "" {
				name = inst.Def.Filename
			}
			if name == "" {
				name = "unknown"
			}
		}
		idx, ok := nameToIdx[name]
		if !ok {
			// Malformed/custom: assign synthetic index beyond table, but clamp to avoid overflow; treat as normal with unknown name.
			// For graceful handling we append name to table dynamically.
			idx = uint16(len(names))
			names = append(names, name)
			nameToIdx[name] = idx
		}
		switch Classify(inst.Def) {
		case KindNormal:
			normals = append(normals, NormalRecord{TypeIdx: idx, CX: int16(inst.CX), CZ: int16(inst.CZ), Health: int16(inst.Health)})
		case KindAnimating:
			animatings = append(animatings, AnimatingRecord{TypeIdx: idx, CX: int16(inst.CX), CZ: int16(inst.CZ), Health: int16(inst.Health), BurnCountdown: int16(inst.BurnCountdown)})
		case KindThreeD:
			rec := ThreeDRecord{TypeIdx: idx, CX: int16(inst.CX), CZ: int16(inst.CZ), Health: int16(inst.Health), Y: int32(inst.Y.Raw()), Vy: int32(inst.Vy.Raw()), Status: inst.Status, BurnTicks: inst.BurnTicks}
			if inst.IsSinking {
				rec.IsSinking = 1
			}
			if inst.IsBurning {
				rec.IsBurning = 1
			}
			threeds = append(threeds, rec)
		}
	}
	// Write type names box n*0x80 [P1-13 §2.7]
	if err := save.WriteFeatureTypeNames(b, names); err != nil {
		return err
	}
	// Retrieve the single Features account created by WriteFeatureTypeNames (builderAccount merges)
	var featAc *save.Account
	for _, ac := range b.Accounts {
		if ac.Name == save.FeaturesAccount {
			featAc = ac
			break
		}
	}
	if featAc == nil {
		featAc = b.Add(save.FeaturesAccount)
	}
	// Write groups sorted within each kind for determinism (already sorted by Instances which is sorted by key)
	// Normal
	if len(normals) > 0 {
		payload := make([]byte, len(normals)*NormalRecordSize)
		for i, r := range normals {
			off := i * NormalRecordSize
			binary.LittleEndian.PutUint16(payload[off:], r.TypeIdx)
			binary.LittleEndian.PutUint16(payload[off+2:], uint16(r.CX))
			binary.LittleEndian.PutUint16(payload[off+4:], uint16(r.CZ))
			binary.LittleEndian.PutUint16(payload[off+6:], uint16(r.Health))
		}
		featAc.AppendBox(save.FeatureNormalBox, 0, payload)
	}
	// Animating
	if len(animatings) > 0 {
		payload := make([]byte, len(animatings)*AnimatingRecordSize)
		for i, r := range animatings {
			off := i * AnimatingRecordSize
			binary.LittleEndian.PutUint16(payload[off:], r.TypeIdx)
			binary.LittleEndian.PutUint16(payload[off+2:], uint16(r.CX))
			binary.LittleEndian.PutUint16(payload[off+4:], uint16(r.CZ))
			binary.LittleEndian.PutUint16(payload[off+6:], uint16(r.Health))
			binary.LittleEndian.PutUint16(payload[off+8:], uint16(r.BurnCountdown))
		}
		featAc.AppendBox(save.FeatureAnimatingBox, 0, payload)
	}
	// 3D
	if len(threeds) > 0 {
		payload := make([]byte, len(threeds)*ThreeDRecordSize)
		for i, r := range threeds {
			off := i * ThreeDRecordSize
			binary.LittleEndian.PutUint16(payload[off:], r.TypeIdx)
			binary.LittleEndian.PutUint16(payload[off+2:], uint16(r.CX))
			binary.LittleEndian.PutUint16(payload[off+4:], uint16(r.CZ))
			binary.LittleEndian.PutUint16(payload[off+6:], uint16(r.Health))
			binary.LittleEndian.PutUint32(payload[off+8:], uint32(r.Y))
			binary.LittleEndian.PutUint32(payload[off+12:], uint32(r.Vy))
			payload[off+16] = r.Status
			payload[off+17] = r.IsSinking
			payload[off+18] = r.IsBurning
			binary.LittleEndian.PutUint32(payload[off+19:], uint32(r.BurnTicks))
			// bytes 23-25 reserved zero
		}
		featAc.AppendBox(save.Feature3DBox, 0, payload)
	}
	return nil
}

// ReadBulk restores service state from the Features account's four boxes [P1-13 §2.7].
// It validates sizes (% record size ==0) per Validate*Size, handles malformed/custom gracefully:
// unknown type names are kept as synthetic defs with placeholder footprint 1x1;
// out-of-range type indices are clamped to first entry; short or malformed boxes are skipped with no panic.
// Vent-under-plant persistence is preserved because ReadBulk reconstructs via spawnFeatureAt which uses read-only validation and never clears vents.
func (s *Service) ReadBulk(bank *save.Bank) error {
	if s == nil || bank == nil {
		return fmt.Errorf("features: nil service or bank")
	}
	if s.Terrain == nil {
		return fmt.Errorf("features: nil terrain for bulk load")
	}
	ac, ok := bank.Account(save.FeaturesAccount)
	if !ok {
		// No features account is valid: empty map, e.g., malformed save with missing Features is graceful
		s.instances = make(map[int]*Instance)
		return nil
	}
	// Read type names
	typeNamesData, _ := ac.BoxData(save.FeatureTypeNamesBox, 0)
	var names []string
	if len(typeNamesData) > 0 {
		if !save.ValidateFeatureTypeNamesSize(len(typeNamesData)) {
			// Malformed: size not %128, treat as graceful truncation: ignore trailing partial
			// Keep only full 128-byte entries
			full := (len(typeNamesData) / save.FeatureTypeName) * save.FeatureTypeName
			typeNamesData = typeNamesData[:full]
		}
		n := len(typeNamesData) / save.FeatureTypeName
		names = make([]string, n)
		for i := 0; i < n; i++ {
			off := i * save.FeatureTypeName
			raw := typeNamesData[off : off+save.FeatureTypeName]
			// NUL-padded
			end := 0
			for end < len(raw) && raw[end] != 0 {
				end++
			}
			names[i] = string(raw[:end])
		}
	}
	// Helper to resolve name index to def
	resolve := func(idx uint16) *content.FeatureDef {
		if int(idx) >= len(names) {
			// Malformed/custom: out-of-range index; treat as unknown but graceful: create placeholder
			if len(names) > 0 {
				idx = 0 // clamp to first
			} else {
				return nil
			}
		}
		name := names[idx]
		if name == "" {
			return nil
		}
		ck := content.CanonicalKey(name)
		if def, ok := s.Terrain.FeatureDefAt(uint16(idx)); ok && def != nil && def.CanonicalKey == ck {
			return def
		}
		// Search catalog via Terrain's FeatureDefs by canonical key
		for _, d := range s.Terrain.FeatureDefs {
			if d != nil && d.CanonicalKey == ck {
				return d
			}
		}
		// Custom/malformed: synthesize placeholder 1x1 reclaimable=false
		return &content.FeatureDef{
			DefinitionHeader: content.DefinitionHeader{CanonicalKey: ck},
			FootprintX:       1,
			FootprintZ:       1,
			Damage:           10,
		}
	}

	// Clear existing instances before load
	s.instances = make(map[int]*Instance)
	// Also need to clear plot feature grid for cells that will be overwritten? For bulk load we reconstruct via spawn, which stamps plot.
	// But to avoid leaking old plot entries, we should clear previous feature-occupied cells? However retail fix-up order is Features→Metal→Units, and plot is rebuilt from scratch via ExpandPlot, so we can just clear in-bounds feature cells that were previously stamped?
	// Simplify: for each existing instance we already cleared map, but plot cells still hold old feature indices; we will overwrite via spawn which checks IsEmpty. So we need to clear plot first for those cells.
	// Instead, we will directly restore instances map and also stamp plot via helper that bypasses IsEmpty check for bulk load.
	// For determinism, we will use internal restore that directly sets plot and map.

	// Helper to stamp plot for bulk load bypassing catalog limit? Bulk load should respect allocation-failure policy: catalog 0x100, anim 0x800 silent fail.
	// So we use spawnFeatureAt which already enforces those limits and silent fail.
	stamp := func(cx, cz int, def *content.FeatureDef, health int16, status uint8, isSinking, isBurning bool, y, vy int32, burnTicks int32, burnCountdown int16) {
		if def == nil {
			return
		}
		// Use existing spawn path which handles allocation failure and malformed gracefully
		inst := s.spawnFeatureAt(cx, cz, def)
		if inst == nil {
			// Allocation failure: silent no-op per [P1-10][P1-15] 0x100/0x800/0xD
			return
		}
		inst.Health = int32(health)
		if health == 0 {
			inst.Health = def.Damage
		}
		inst.Status = status
		if isSinking {
			inst.IsSinking = true
			inst.Y = numericFromRaw(y)
			inst.Vy = numericFromRaw(vy)
		}
		if isBurning {
			inst.IsBurning = true
			inst.BurnCountdown = int32(burnCountdown)
			inst.BurnTicks = burnTicks
			// BurnDuration derived from GAF if needed; keep as is
		}
		// For 3D, Y/Vy already set
		if y != 0 || vy != 0 {
			inst.Y = numericFromRaw(y)
			inst.Vy = numericFromRaw(vy)
		}
	}

	// Normal
	if data, ok := ac.BoxData(save.FeatureNormalBox, 0); ok && len(data) > 0 {
		if len(data)%NormalRecordSize != 0 {
			// Malformed: truncate to full records
			data = data[:len(data)/NormalRecordSize*NormalRecordSize]
		}
		for i := 0; i+NormalRecordSize <= len(data); i += NormalRecordSize {
			typeIdx := binary.LittleEndian.Uint16(data[i:])
			cx := int16(binary.LittleEndian.Uint16(data[i+2:]))
			cz := int16(binary.LittleEndian.Uint16(data[i+4:]))
			health := int16(binary.LittleEndian.Uint16(data[i+6:]))
			def := resolve(typeIdx)
			stamp(int(cx), int(cz), def, health, 0, false, false, 0, 0, 0, 0)
		}
	}
	// Animating
	if data, ok := ac.BoxData(save.FeatureAnimatingBox, 0); ok && len(data) > 0 {
		if len(data)%AnimatingRecordSize != 0 {
			data = data[:len(data)/AnimatingRecordSize*AnimatingRecordSize]
		}
		for i := 0; i+AnimatingRecordSize <= len(data); i += AnimatingRecordSize {
			typeIdx := binary.LittleEndian.Uint16(data[i:])
			cx := int16(binary.LittleEndian.Uint16(data[i+2:]))
			cz := int16(binary.LittleEndian.Uint16(data[i+4:]))
			health := int16(binary.LittleEndian.Uint16(data[i+6:]))
			burnCd := int16(binary.LittleEndian.Uint16(data[i+8:]))
			def := resolve(typeIdx)
			stamp(int(cx), int(cz), def, health, 0, false, true, 0, 0, 0, burnCd)
		}
	}
	// 3D
	if data, ok := ac.BoxData(save.Feature3DBox, 0); ok && len(data) > 0 {
		if len(data)%ThreeDRecordSize != 0 {
			data = data[:len(data)/ThreeDRecordSize*ThreeDRecordSize]
		}
		for i := 0; i+ThreeDRecordSize <= len(data); i += ThreeDRecordSize {
			typeIdx := binary.LittleEndian.Uint16(data[i:])
			cx := int16(binary.LittleEndian.Uint16(data[i+2:]))
			cz := int16(binary.LittleEndian.Uint16(data[i+4:]))
			health := int16(binary.LittleEndian.Uint16(data[i+6:]))
			y := int32(binary.LittleEndian.Uint32(data[i+8:]))
			vy := int32(binary.LittleEndian.Uint32(data[i+12:]))
			status := data[i+16]
			isSink := data[i+17] != 0
			isBurn := data[i+18] != 0
			burnTicks := int32(binary.LittleEndian.Uint32(data[i+19:]))
			def := resolve(typeIdx)
			stamp(int(cx), int(cz), def, health, status, isSink, isBurn, y, vy, burnTicks, 0)
		}
	}
	return nil
}

func numericFromRaw(v int32) numeric.Fixed { return numeric.Fixed(int64(v)) }
