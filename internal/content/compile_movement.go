// Package content compiles retail's authored data into immutable definitions.
// This file implements the movement class compiler [02 "Movement class record"].
package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// MovementClass is a compiled movement profile [02 "Movement class record"].
// DefinitionHeader must be the first field per catalog convention [02 §5].
type MovementClass struct {
	DefinitionHeader
	// Footprint in cells [02 "Movement class record"] 1-2: FootPrintX/Z — integer, default 0, stored as 16-bit.
	FootprintX int32 // 1: FootPrintX — integer, default 0, stored as 16-bit [02 "Movement class record"]
	FootprintZ int32 // 2: FootPrintZ — integer, default 0, stored as 16-bit [02 "Movement class record"]
	// Water depths [02 "Movement class record"] 3-4.
	MaxWaterDepth int32 // 3: maxwaterdepth — integer, default: the profile's current value (0 for a fresh def) [02 "Movement class record"]
	MinWaterDepth int32 // 4: minwaterdepth — integer, default: the profile's current value [02 "Movement class record"]
	// Slopes [02 "Movement class record"] 5-8.
	MaxSlope      int32 // 5: maxslope — integer, default: the profile's current value, stored as a byte [02 "Movement class record"]
	BadSlope      int32 // 6: badslope — integer, default: half the maxslope value just read [02 "Movement class record"]
	MaxWaterSlope int32 // 7: maxwaterslope — integer, default: the profile's current value, stored as a byte [02 "Movement class record"]
	BadWaterSlope int32 // 8: badwaterslope — integer, default: half the maxwaterslope value just read [02 "Movement class record"]
}

// compileMovementSection compiles a single CLASS section into a MovementClass.
// It reads the eight keys in order with chained defaults and then applies the
// three clamps in order [02 "Movement class record"].
//
// Read order is the contract:
//
//	1 FootPrintX default 0
//	2 FootPrintZ default 0
//	3 maxwaterdepth default 0 (profile current)
//	4 minwaterdepth default 0
//	5 maxslope default 0
//	6 badslope default half the maxslope just read
//	7 maxwaterslope default 0
//	8 badwaterslope default half the maxwaterslope just read
//
// Then three clamps in order:
//
//	1 if maxwaterslope < maxslope => maxslope = maxwaterslope
//	2 if maxslope < badslope => badslope = maxslope
//	3 if maxwaterslope < badwaterslope => badwaterslope = maxwaterslope
//
// The clamps are conditioned on maxwaterslope having been authored. Retail's
// spec states the defaults as "the profile's current value" which for a fresh
// definition is 0, so an unconditional clamp would zero every land class that
// omits MaxWaterSlope (12 of 15 retail classes). The conditional preserves
// the retail file's intent and matches the openta-go compiler's
// MaxWaterSlopePresent guard. If research later clarifies the intended
// behaviour for an authored zero vs a missing key (integer cannot distinguish
// [02 §4]), this can be adjusted with a TODO(question).
func compileMovementSection(section *formats.Section, className string, prov Provenance) *MovementClass {
	// Use only the typed accessors from formats/tdf_typed.go [02 §4].
	footprintX := section.IntValue("FootPrintX", 0)
	footprintZ := section.IntValue("FootPrintZ", 0)
	maxWaterDepth := section.IntValue("maxwaterdepth", 0)
	minWaterDepth := section.IntValue("minwaterdepth", 0)
	maxSlope := section.IntValue("maxslope", 0)
	// [02 "Movement class record"] badslope default is half the maxslope just read.
	badSlopeDefault := maxSlope / 2
	badSlope := section.IntValue("badslope", badSlopeDefault)
	maxWaterSlope := section.IntValue("maxwaterslope", 0)
	// [02 "Movement class record"] badwaterslope default is half the maxwaterslope just read.
	badWaterSlopeDefault := maxWaterSlope / 2
	badWaterSlope := section.IntValue("badwaterslope", badWaterSlopeDefault)

	// TODO(question): what is a movement profile's initial maxwaterslope,
	// before the CLASS section is read?
	//
	// [02 "Movement class record"] runs three clamps unconditionally, and keys
	// 3, 4, 5 and 7 default to "the profile's current value" rather than to
	// zero. We use zero, which makes clamp 1 collapse maxslope to zero for every
	// class that omits maxwaterslope — 12 of the reference install's 15, taking
	// kbotsf2, kbotss2, tankbh3, tankds2, tanksh2 and tanksh3 with it. No land
	// unit could climb anything.
	//
	// The presence branch below is a stand-in for that unknown default, not a
	// reading of the spec: it is a documented divergence, SPEC_CONFLICTS SC5.
	// If the profile is pre-initialized with a large maxwaterslope, all three
	// clamps run unconditionally and produce retail's behaviour with no branch
	// at all — that is the shape to aim for once the initial value is known.
	//
	// The string accessor is what distinguishes an absent key from an authored
	// zero; the integer accessor cannot [02 §4].
	_, maxWaterSlopePresent := section.StringValue("maxwaterslope", "")

	// Three clamps in order [02 "Movement class record"], gated per SC5.
	if maxWaterSlopePresent && maxWaterSlope < maxSlope {
		maxSlope = maxWaterSlope
	}
	if maxSlope < badSlope {
		badSlope = maxSlope
	}
	if maxWaterSlopePresent && maxWaterSlope < badWaterSlope {
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

// CompileMovement compiles movement classes from gamedata/moveinfo.tdf
// [02 "Movement class record"]. Discovery path is gamedata/moveinfo.tdf with
// [CLASS*] sections. It returns a map keyed by CanonicalKey(class Name).
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
		return nil, fmt.Errorf("content: gamedata/moveinfo.tdf: %w", err)
	}
	// Sections are in file order; stable iteration is required for
	// deterministic hash/canonical handling (I1). Sort the output map's
	// iteration elsewhere; here we just collect.
	result := make(map[string]*MovementClass)
	// Keep discovery stable: iterate sections in file order, then sort keys
	// for any downstream hash.
	for _, section := range doc.Root.Sections() {
		// Discovery path is gamedata/moveinfo.tdf with [CLASS*] sections.
		if !strings.HasPrefix(strings.ToLower(section.Name), "class") {
			continue
		}
		name, ok := section.StringValue("Name", "")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		mc := compileMovementSection(section, name, prov)
		key := CanonicalKey(name)
		// Duplicate canonical keys: last wins is file-order deterministic; a
		// later duplicate overwrites the earlier winner. No map iteration
		// influences the result (I1).
		result[key] = mc
	}
	return result, nil
}

// compileMovement is an unexported alias for future Catalog integration.
func compileMovement(fs vfs.FSOps) (map[string]*MovementClass, error) {
	return CompileMovement(fs)
}

// CompileMovementSorted returns the movement classes sorted by canonical key.
// This helper is useful for hash-stable iteration and tests (I1).
func CompileMovementSorted(fs vfs.FSOps) ([]*MovementClass, error) {
	m, err := CompileMovement(fs)
	if err != nil {
		return nil, err
	}
	out := make([]*MovementClass, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CanonicalKey < out[j].CanonicalKey })
	return out, nil
}
