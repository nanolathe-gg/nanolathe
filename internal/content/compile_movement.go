// The movement class compiler.

package content

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The startup class template [04 §6.1 R-DOC04-A]: before any parse every class
// record holds these values, so an omitted key parses to them. 255 on every
// slope means "unauthored slope fields never limit"; the depth limits are wide
// enough that no stock terrain can trip them.
const (
	tmplSlope         = 255
	tmplMaxWaterDepth = 10000
	tmplMinWaterDepth = -10000
)

// MovementClass is a compiled movement profile [02 §5 "Movement class record"].
// DefinitionHeader must be the first field per catalog convention [02 §5].
type MovementClass struct {
	DefinitionHeader
	// Footprint in cells [02 §5 "Movement class record"] keys 1-2, stored as
	// 16-bit signed.
	FootprintX int32
	FootprintZ int32
	// Water depths in height units [02 §5] keys 3-4, stored as 16-bit signed;
	// consumers compare signed [04 §6.1 R-DOC04-B].
	MaxWaterDepth int32
	MinWaterDepth int32
	// Slope thresholds [02 §5] keys 5-8, stored as bytes; every consumer
	// compares them as unsigned bytes [04 §6.1 R-DOC04-B].
	MaxSlope      int32
	BadSlope      int32
	MaxWaterSlope int32
	BadWaterSlope int32
}

// compileMovementSection compiles a single CLASS section into a MovementClass.
//
// Eight keys are read in parse order — FootPrintX, FootPrintZ, MaxWaterDepth,
// MinWaterDepth, MaxSlope, BadSlope, MaxWaterSlope, BadWaterSlope [02 §5].
// FootPrintX/Z default 0; the depth and slope keys default to the record's
// prior value, which is the startup template's value because each class slot
// parses exactly once and the template is pre-filled before any parse
// [04 §6.1 R-DOC04-A]. BadSlope/BadWaterSlope instead default to half of the
// Max value just read, a logical shift of its low byte [02 §5]. Every field is
// stored truncated to its record width (16-bit signed, or 8-bit for slopes)
// [02 §5 "Per-field conversion"].
//
// Three min-clamps then run unconditionally on every class, in order, as
// unsigned byte comparisons [02 §5][04 §6.1 R-DOC04-A]:
//
//	1 if MaxWaterSlope < MaxSlope => MaxSlope = MaxWaterSlope
//	2 if MaxSlope < BadSlope      => BadSlope = MaxSlope
//	3 if MaxWaterSlope < BadWaterSlope => BadWaterSlope = MaxWaterSlope
//
// There is no key-presence gate: the template pre-fill plus the unconditional
// clamps reproduce retail exactly, and the earlier gated divergence
// (docs/SPEC_CONFLICTS.md SC5) is closed per [04 §6.1 R-DOC04-A]. With the
// template, the first clamp is the identity for every class that omits
// MaxWaterSlope, so compiled MaxSlope equals the authored value.
func compileMovementSection(section *formats.Section, className string, prov Provenance) *MovementClass {
	// Parse order is contract [02 §5]. Defaults chain off the record's prior
	// value; the prior is the startup template [04 §6.1 R-DOC04-A].
	footprintX := storeInt16(section.IntValue("FootPrintX", 0))
	footprintZ := storeInt16(section.IntValue("FootPrintZ", 0))
	maxWaterDepth := storeInt16(section.IntValue("maxwaterdepth", tmplMaxWaterDepth))
	minWaterDepth := storeInt16(section.IntValue("minwaterdepth", tmplMinWaterDepth))
	maxSlopeRead := section.IntValue("maxslope", tmplSlope)
	maxSlope := storeByte(maxSlopeRead)
	// badslope defaults to (maxslope just read & 0xFF) >> 1 — a logical shift,
	// 0..127 [02 §5 "Per-field conversion"].
	badSlope := storeByte(section.IntValue("badslope", storeByte(maxSlopeRead)>>1))
	maxWaterSlopeRead := section.IntValue("maxwaterslope", tmplSlope)
	maxWaterSlope := storeByte(maxWaterSlopeRead)
	// badwaterslope defaults to (maxwaterslope just read & 0xFF) >> 1 [02 §5].
	badWaterSlope := storeByte(section.IntValue("badwaterslope", storeByte(maxWaterSlopeRead)>>1))

	// Three clamps, in order, unconditional [02 §5][04 §6.1 R-DOC04-A]. The
	// operands are stored bytes (0..255), so int32 < is the unsigned byte
	// comparison retail performs.
	if maxWaterSlope < maxSlope { // clamp 1: MaxWaterSlope<MaxSlope => MaxSlope=MaxWaterSlope
		maxSlope = maxWaterSlope
	}
	if maxSlope < badSlope { // clamp 2: MaxSlope<BadSlope => BadSlope=MaxSlope
		badSlope = maxSlope
	}
	if maxWaterSlope < badWaterSlope { // clamp 3: MaxWaterSlope<BadWaterSlope => BadWaterSlope=MaxWaterSlope
		badWaterSlope = maxWaterSlope
	}

	mc := &MovementClass{
		DefinitionHeader: DefinitionHeader{
			CanonicalKey: CanonicalKey(className),
			Provenance:   prov,
		},
		FootprintX:    footprintX,
		FootprintZ:    footprintZ,
		MaxWaterDepth: maxWaterDepth,
		MinWaterDepth: minWaterDepth,
		MaxSlope:      maxSlope,
		BadSlope:      badSlope,
		MaxWaterSlope: maxWaterSlope,
		BadWaterSlope: badWaterSlope,
	}
	// Hash over canonical bytes including defaults, stable across runs (I1).
	// Use DefinitionHeader.Hash to hold the per-definition hash.
	canonical := fmt.Sprintf("%s|%d|%d|%d|%d|%d|%d|%d|%d",
		mc.CanonicalKey, mc.FootprintX, mc.FootprintZ, mc.MaxWaterDepth, mc.MinWaterDepth, mc.MaxSlope, mc.BadSlope, mc.MaxWaterSlope, mc.BadWaterSlope)
	mc.Hash = HashDefinition([]byte(canonical))
	return mc
}

// storeInt16 narrows an authored 32-bit read to the record's signed 16-bit
// storage width [02 §5 "Per-field conversion"].
func storeInt16(v int32) int32 { return int32(int16(v)) }

// storeByte narrows an authored 32-bit read to the record's byte storage: the
// low 8 bits, which every consumer compares as unsigned [02 §5 "Per-field
// conversion"].
func storeByte(v int32) int32 { return int32(uint8(v)) }

// CompileMovement compiles movement classes from gamedata/moveinfo.tdf
// [02 §5 "Movement class record"]. Discovery path is gamedata/moveinfo.tdf with
// exact CLASS0..CLASS31 sections. It returns a map keyed by CanonicalKey(class Name).
func CompileMovement(fs vfs.FSOps) (map[string]*MovementClass, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	// VFS paths are case-insensitive via cleanPath [02 §2]; try the canonical
	// lower-case form first.
	data, err := fs.ReadFileLimit("gamedata/moveinfo.tdf", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("content: gamedata/moveinfo.tdf: %w", err)
	}
	prov := Provenance{}
	if info, statErr := fs.Stat("gamedata/moveinfo.tdf"); statErr == nil {
		prov = ProvenanceFrom(info)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, formats.WithTDFContext(fs, err, "gamedata/moveinfo.tdf")
	}
	result := make(map[string]*MovementClass)
	// Retail probes the bounded numeric slots, skipping gaps. Section lookup
	// chooses the first matching section [02 §4][02 "Movement class record"].
	for i := 0; i < 32; i++ {
		section := doc.Root.Section(fmt.Sprintf("CLASS%d", i))
		if section == nil {
			continue
		}
		name, ok := section.StringValue("Name", "")
		if !ok || trimTDFSemantic(name) == "" {
			continue
		}
		key := CanonicalKey(name)
		// FBI name resolution returns the first equal name in numeric slot
		// order [02 "Movement class record"].
		if _, found := result[key]; !found {
			result[key] = compileMovementSection(section, name, prov)
		}
	}
	return result, nil
}
