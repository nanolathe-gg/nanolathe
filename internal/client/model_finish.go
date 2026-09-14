package client

import (
	_ "embed"
	"fmt"
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

// materialSection is the single section the annotation file authors.
const materialSection = "materials"

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
	table, err := parseMaterialTable(embeddedMaterialTDF)
	if err != nil {
		// The embedded file ships inside the binary, so a parse failure is a
		// build defect rather than a content condition; the package tests fail
		// on it before anything reaches a player.
		panic(fmt.Sprintf("nanolathe: embedded material annotation is unreadable: %v", err))
	}
	materialTable.Store(&table)
	materialGeneration.Add(1)
}

// parseMaterialTable reads the authored [materials] section into a lowercase
// texture-name map. Assignments are taken in source order, so a repeated
// spelling keeps its last value, matching TDF's own duplicate policy
// [fmt tdf "Duplicate keys"].
func parseMaterialTable(data []byte) (map[string]uint8, error) {
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, err
	}
	var section *formats.Section
	for _, s := range doc.Root.Sections() {
		if strings.EqualFold(s.Name, materialSection) {
			section = s
			break
		}
	}
	if section == nil {
		return nil, fmt.Errorf("no [%s] section", materialSection)
	}
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
	return fmt.Errorf("nanolathe: material annotation override is unreadable: logical path %s, providers searched [%s], expected a TDF [%s] section of texture=metal|paint: %w",
		logical, providers, materialSection, cause)
}

// SetMaterialTable replaces the active annotation with an authored table. The
// table in force is kept when the supplied bytes cannot be read, so a broken
// override falls back to the embedded annotation rather than to no finish.
func SetMaterialTable(logical string, data []byte, providers string) error {
	table, err := parseMaterialTable(data)
	if err != nil {
		return materialDiagnostic(logical, providers, err)
	}
	materialTable.Store(&table)
	materialGeneration.Add(1)
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
