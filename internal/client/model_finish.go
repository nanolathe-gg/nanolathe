package client

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The material annotation is authored presentation data, not retail material
// metadata (DESIGN_GPU_RENDERER §29.1). It lives in an authored TDF file rather
// than in Go so the art classification is reviewable and overridable as
// content: the reviewed stock texture sheet supplies neutral plate and painted
// camouflage, and unknown art keeps its existing lighting.

// MaterialTablePath is the logical path an install may supply to replace the
// embedded annotation wholesale — a remaster pack or a loose gamedata
// directory. Absent, the embedded table stands.
const MaterialTablePath = "nanolathe/materials.tdf"

// materialSection is the texture annotation's section, and effectsSection the
// per-family light strengths a content pack may set beside it (§19.4).
const (
	materialSection = "materials"
	effectsSection  = "effects"
)

// GlowFamilies is a content pack's strength for each family of Enhanced light,
// as a percentage of the tuned look (DESIGN_GPU_RENDERER §19.4): Weapons is the
// glow of beams, effect and projectile art and explosion flashes; Nanolathe is
// the nanolathe spray's glow and the light it casts; Ground is the terrain
// receiver of every battle light. Each is 0..GlowFamilyMax, 100 by default.
type GlowFamilies struct {
	Weapons, Nanolathe, Ground int
}

// GlowFamilyDefault and GlowFamilyMax bound a family's percentage; they match
// the executor's GlowStrengthDefault and GlowStrengthMax.
const (
	GlowFamilyDefault = 100
	GlowFamilyMax     = 200
)

// DefaultGlowFamilies is every family at its tuned look.
func DefaultGlowFamilies() GlowFamilies {
	return GlowFamilies{Weapons: GlowFamilyDefault, Nanolathe: GlowFamilyDefault, Ground: GlowFamilyDefault}
}

// glowFamilies is the content's family strengths in force, installed with the
// material annotation and read by every host before a modern frame.
var glowFamilies atomic.Pointer[GlowFamilies]

//go:embed materials/materials.tdf
var embeddedMaterialTDF []byte

// materialTable is the active annotation, replaced whole. It is read from the
// model compose path, which runs on the recording goroutines, and written only
// at load time, so the pointer swap is atomic and the map itself never mutates.
var materialTable atomic.Pointer[map[string]uint8]

// materialGeneration counts the annotations installed into materialTable. It
// exists so a resolved texture reference can carry the finish it read as a
// plain byte and still be invalidated when an install replaces the table: the
// per-face compose path compares this counter instead of lowercasing the
// texture name and probing the map on every textured face.
//
// A writer stores the table first and bumps the counter second, and a reader
// samples the counter first and the table second. Both orders are the
// conservative one: a byte read across an install is labelled with the older
// generation and is therefore discarded, never kept under the new one.
var materialGeneration atomic.Uint64

// materialForKey annotates an already-lowercased texture name and reports the
// generation it was read under, for a caller that wants to cache the byte.
func materialForKey(key string) (uint8, uint64) {
	gen := materialGeneration.Load()
	table := materialTable.Load()
	if table == nil {
		return drawlist.ModelMaterialDefault, gen
	}
	return (*table)[key], gen
}

func init() {
	table, families, err := parseContentTable(embeddedMaterialTDF)
	if err != nil || table == nil {
		// The embedded file ships inside the binary, so a parse failure is a
		// build defect rather than a content condition; the package tests fail
		// on it before anything reaches a player.
		panic(fmt.Sprintf("nanolathe: embedded material annotation is unreadable: %v", err))
	}
	materialTable.Store(&table)
	materialGeneration.Add(1)
	glowFamilies.Store(&families)
}

// GlowFamilies returns the installed content's family strengths, in the order
// the executor's SetGlowFamilies takes them (DESIGN_GPU_RENDERER §19.4). They
// are content, not a player preference, so every client reads the same values.
func (c *Client) GlowFamilies() (weapons, nanolathe, ground int) {
	f := glowFamilies.Load()
	if f == nil {
		return GlowFamilyDefault, GlowFamilyDefault, GlowFamilyDefault
	}
	return f.Weapons, f.Nanolathe, f.Ground
}

// findSection returns the root section named name, ignoring case, or nil.
func findSection(doc *formats.Document, name string) *formats.Section {
	for _, s := range doc.Root.Sections() {
		if strings.EqualFold(s.Name, name) {
			return s
		}
	}
	return nil
}

// parseContentTable reads the whole annotation file: the [materials] texture
// table, nil when the file has no such section, and the [effects] family
// strengths, every family at its default when the file has none. A file with
// neither section is unreadable, and so is one whose [materials] section
// annotates nothing or whose [effects] section holds a value that is not a
// whole number.
func parseContentTable(data []byte) (map[string]uint8, GlowFamilies, error) {
	families := DefaultGlowFamilies()
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, families, err
	}
	materials, effects := findSection(doc, materialSection), findSection(doc, effectsSection)
	if materials == nil && effects == nil {
		return nil, families, fmt.Errorf("no [%s] or [%s] section", materialSection, effectsSection)
	}
	var table map[string]uint8
	if materials != nil {
		if table, err = materialSectionTable(materials); err != nil {
			return nil, families, err
		}
	}
	if effects != nil {
		if families, err = parseGlowFamilies(effects); err != nil {
			return nil, families, err
		}
	}
	return table, families, nil
}

// parseGlowFamilies reads the [effects] section: weapons=, nanolathe= and
// ground= percentages, each clamped to 0..GlowFamilyMax, last write winning
// [fmt tdf "Duplicate keys"]. An unknown key is ignored, so a later build's
// family does not make an older build reject the file.
func parseGlowFamilies(section *formats.Section) (GlowFamilies, error) {
	families := DefaultGlowFamilies()
	for _, item := range section.Assignments() {
		var field *int
		switch strings.ToLower(strings.TrimSpace(item.Key)) {
		case "weapons":
			field = &families.Weapons
		case "nanolathe":
			field = &families.Nanolathe
		case "ground":
			field = &families.Ground
		default:
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(item.Value))
		if err != nil {
			return DefaultGlowFamilies(), fmt.Errorf("[%s] %s=%q is not a whole percentage", effectsSection, item.Key, item.Value)
		}
		*field = min(max(v, 0), GlowFamilyMax)
	}
	return families, nil
}

// materialSectionTable reads the authored [materials] section into a lowercase
// texture-name map. Assignments are taken in source order, so a repeated
// spelling keeps its last value, matching TDF's own duplicate policy
// [fmt tdf "Duplicate keys"].
func materialSectionTable(section *formats.Section) (map[string]uint8, error) {
	table := make(map[string]uint8)
	for _, item := range section.Assignments() {
		name := strings.ToLower(strings.TrimSpace(item.Key))
		if name == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Value)) {
		case "metal":
			table[name] = drawlist.ModelMaterialMetal
		case "paint":
			table[name] = drawlist.ModelMaterialPaint
		default:
			// An unrecognised class leaves the texture unannotated, which is
			// what an absent name does too. Removing rather than skipping is
			// what last-write-wins means for a name annotated earlier in the
			// same file.
			delete(table, name)
		}
	}
	if len(table) == 0 {
		return nil, fmt.Errorf("[%s] annotates no texture", materialSection)
	}
	return table, nil
}

// materialDiagnostic is the one diagnostic shape this loader reports
// [AGENTS.md §Diagnostics].
func materialDiagnostic(logical, providers string, cause error) error {
	return fmt.Errorf("nanolathe: material annotation override is unreadable: logical path %s, providers searched [%s], expected a TDF [%s] section of texture=metal|paint and/or an [%s] section of family=percent: %w",
		logical, providers, materialSection, effectsSection, cause)
}

// SetMaterialTable installs an authored annotation file. Its [materials]
// section replaces the texture table whole; a file without one keeps the table
// in force, so a pack may set only its light strengths. Its [effects] section
// replaces the family strengths, and a file without one restores the defaults.
// Everything in force is kept when the supplied bytes cannot be read, so a
// broken override falls back to the embedded annotation rather than to no
// finish.
func SetMaterialTable(logical string, data []byte, providers string) error {
	table, families, err := parseContentTable(data)
	if err != nil {
		return materialDiagnostic(logical, providers, err)
	}
	if table != nil {
		materialTable.Store(&table)
		materialGeneration.Add(1)
	}
	glowFamilies.Store(&families)
	return nil
}

// LoadMaterialTable installs an override when the mounted content supplies one
// at MaterialTablePath. No such file is the ordinary case and not an error; a
// file that cannot be read leaves the embedded table in place and reports why.
func LoadMaterialTable(fs vfs.FSOps) error {
	if fs == nil {
		return nil
	}
	if _, err := fs.Stat(MaterialTablePath); err != nil {
		return nil
	}
	data, err := fs.ReadFileLimit(MaterialTablePath, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		return materialDiagnostic(MaterialTablePath, cursorProviders(fs), err)
	}
	return SetMaterialTable(MaterialTablePath, data, cursorProviders(fs))
}

// modelTextureMaterial annotates a textured unit face as default, metal or
// paint from the active table (DESIGN_GPU_RENDERER §29.1). Unknown art keeps
// its existing lighting.
func modelTextureMaterial(texture string) uint8 {
	table := materialTable.Load()
	if table == nil {
		return drawlist.ModelMaterialDefault
	}
	return (*table)[strings.ToLower(texture)]
}
