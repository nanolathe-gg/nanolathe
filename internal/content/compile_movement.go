// Package content compiles retail's authored data into immutable definitions.
// This file implements the movement class compiler [02 "Movement class record"] [P1-03].
package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// MovementClass is a compiled movement profile [02 "Movement class record"] [P1-03].
// DefinitionHeader must be the first field per catalog convention [02 §5].
type MovementClass struct {
	DefinitionHeader
	// Footprint in cells [02 "Movement class record"] 1-2: FootPrintX/Z — integer, default 0, stored as 16-bit.
	FootprintX int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	FootprintZ int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Water depths [02 "Movement class record"] 3-4.
	MaxWaterDepth int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	MinWaterDepth int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Slopes [02 "Movement class record"] 5-8.
	MaxSlope      int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	BadSlope      int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	MaxWaterSlope int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	BadWaterSlope int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// compileMovementSection compiles a single CLASS section into a MovementClass [P1-03].
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// MinWaterDepth, MaxSlope, BadSlope (Max>>1 default), MaxWaterSlope,
// BadWaterSlope (MaxWater>>1) [P1-03]. Defaults preserve prior value vs >>1
// [P1-03]: FootPrint* default 0, depths/slopes preserve prior (BSS 0 on first),
// Bad* default half of just-read Max via SHR AL,1.
//
// Then three clamps run unconditionally per binary trace (no authored gate)
// [P1-03][02 "Movement class record"]:
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//	2 if maxslope < badslope => badslope = maxslope
//	3 if maxwaterslope < badwaterslope => badwaterslope = maxwaterslope
//
// All comparisons are unsigned byte CMP/JB, strictly < not <= [P1-03].
// BSS zero would destroy land MaxSlope (12 of 15 retail classes omit
// maxwaterslope) — stock hypothesis is profile template 255 before parse
// [P1-03]. Writer NEGATIVE-BOUNDED absent in 848/3901 scan [P1-03].
// Nanolathe keeps gated divergence with A10 comment until writer proven [P1-03][SPEC_CONFLICTS SC5].
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// slope via SeaLevel<=bMin branch swapping MaxSlope/MaxWaterSlope, passability
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// halfNeg = FootPrint*-0x100000/2, halfPos = FootPrint<<0x14/2, fullSpan = halfPos-halfNeg.
func compileMovementSection(section *formats.Section, className string, prov Provenance) *MovementClass {
	// Use only the typed accessors from formats/tdf_typed.go [02 §4].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	footprintX := section.IntValue("FootPrintX", 0)       // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	footprintZ := section.IntValue("FootPrintZ", 0)       // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	maxWaterDepth := section.IntValue("maxwaterdepth", 0) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	minWaterDepth := section.IntValue("minwaterdepth", 0) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	maxSlope := section.IntValue("maxslope", 0)           // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// [02 "Movement class record"] badslope default is half the maxslope just read [P1-03] via SHR AL,1.
	badSlopeDefault := maxSlope / 2
	badSlope := section.IntValue("badslope", badSlopeDefault) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	maxWaterSlope := section.IntValue("maxwaterslope", 0)     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// [02 "Movement class record"] badwaterslope default is half the maxwaterslope just read [P1-03].
	badWaterSlopeDefault := maxWaterSlope / 2
	badWaterSlope := section.IntValue("badwaterslope", badWaterSlopeDefault) // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// TODO(question): what is a movement profile's initial maxwaterslope
	// before the CLASS section is read? Hypothesis is template 255 [P1-03].
	//
	// [02 "Movement class record"] runs three clamps unconditionally per binary
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// default, clamp 1 would set maxslope=0 for every class that omits
	// maxwaterslope — 12 of 15 retail classes, taking kbotsf2, kbotss2, tankbh3
	// etc with it, making land impassable — contradicting stock play. Stock
	// ship data only authors MaxWaterSlope=255 for TANKHOVER3/4 [P1-03]; the
	// other thirteen omit it yet remain land-passable, so template must carry
	// large MaxWaterSlope (≈255) before parsing so absent preserves 255 and
	// clamps are identity [P1-03]. Writer NEGATIVE-BOUNDED absent in 848/3901
	// scan [P1-03].
	//
	// The presence branch below is a gated divergence per SPEC_CONFLICTS SC5 and
	// A10, not a reading of the spec: it reproduces stock slopes with no
	// template. If profile is pre-initialized with 255, all three clamps run
	// unconditionally and produce retail's behaviour with no branch — that is
	// the shape to aim for once initial value is known [P1-03].
	//
	// The string accessor distinguishes absent key from authored zero; the
	// integer accessor cannot [02 §4][P1-03].
	_, maxWaterSlopePresent := section.StringValue("maxwaterslope", "")

	// Three clamps in order [02 "Movement class record"][P1-03], gated per SC5 A10.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// we gate clamps 1 and 3 on authored until template writer proven.
	if maxWaterSlopePresent && maxWaterSlope < maxSlope { // clamp 1: MaxWaterSlope<MaxSlope→MaxSlope=MaxWaterSlope [P1-03] unsigned byte
		maxSlope = maxWaterSlope
	}
	if maxSlope < badSlope { // clamp 2: MaxSlope<BadSlope→BadSlope [P1-03] unconditional in retail, also here
		badSlope = maxSlope
	}
	if maxWaterSlopePresent && maxWaterSlope < badWaterSlope { // clamp 3: MaxWaterSlope<BadWaterSlope→BadWaterSlope [P1-03]
		badWaterSlope = maxWaterSlope
	}

	// FBI materialization derives half-extents at 1<<20 scale [P1-03]:
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Validator via SeaLevel<=bMin branch swapping MaxSlope/MaxWaterSlope, pass < not <= [P1-03].

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
// [02 "Movement class record"] [P1-03]. Discovery path is gamedata/moveinfo.tdf with
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
