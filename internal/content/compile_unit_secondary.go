package content

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// compileUnitDiscovery models the first pass's small write set. Unparsed
// gameplay fields keep their allocation zeros [02 R-CAT-01 §4].
func compileUnitDiscovery(section *formats.Section, language string, prov Provenance) *UnitDef {
	name, _ := section.StringValue("unitname", "")
	name = boundedString(name, 31)
	display, _ := section.LanguageString(language, "name", "")
	side, _ := section.StringValue("side", "")
	weight, _ := section.StringValue("ai_weight", "")
	object, present := section.StringValue("objectname", "")
	if !present {
		object = name
	}
	return &UnitDef{
		DefinitionHeader:    DefinitionHeader{CanonicalKey: CanonicalKey(name), Provenance: prov},
		DiscoveryProvenance: prov,
		DiscoveryOnly:       true,
		UnitName:            name,
		Name:                boundedString(display, 31),
		Side:                boundedString(side, 29),
		AIWeight:            boundedString(weight, 63),
		ObjectName:          boundedString(object, 31),
		BuildCostEnergy:     float32(section.IntValue("buildcostenergy", 0)),
		BuildCostMetal:      float32(section.IntValue("buildcostmetal", 0)),
		NoRestrict:          storedFlag(section, "norestrict", false),
		Wacky:               storedFlag(section, "wacky", false),
		Unknown:             unitSourceUnknown(section, language),
	}
}

// compileUnitSecondary runs once per sorted record, using its stored name.
// Successful parsing replaces the runtime fields; the explicit discovery-only
// fields below survive. Missing keys use parser defaults, never discovery
// values. There is no admission check or second sort [02 R-CAT-01 §5].
func compileUnitSecondary(fs vfs.FSOps, discovery *UnitDef, language string) (string, error) {
	logicalPath := vfs.ResourcePath("units", discovery.UnitName, "fbi")
	info, err := fs.Stat(logicalPath)
	if err != nil {
		return unitSecondaryDiagnostic(fs, discovery, portableContentCause(err).Error()), nil
	}
	if info.IsDir || info.Size <= 0 {
		return unitSecondaryDiagnostic(fs, discovery, "secondary resource is empty or not a file"), nil
	}
	data, err := fs.ReadFileLimit(logicalPath, 1<<20)
	if err != nil {
		return unitSecondaryDiagnostic(fs, discovery, portableContentCause(err).Error()), nil
	}
	if len(data) == 0 {
		return unitSecondaryDiagnostic(fs, discovery, "secondary resource read returned no bytes"), nil
	}
	// TODO(question): retail may parse unwritten temporary bytes after a
	// successful short read; their run-specific contents are unknown. Parse
	// only returned bytes under the bounded host policy [02 R-CAT-01 §5].
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return "", formats.WithTDFContext(fs, err, logicalPath)
	}
	section := doc.Root.Section("UNITINFO")
	if section == nil {
		return unitSecondaryDiagnostic(fs, discovery, "secondary resource has no UNITINFO section"), nil
	}
	secondary := compileUnitSection(section, logicalPath, language, ProvenanceFrom(info))
	secondary.DiscoveryProvenance = discovery.DiscoveryProvenance
	secondary.Side = discovery.Side
	secondary.AIWeight = discovery.AIWeight
	secondary.Wacky = discovery.Wacky
	secondary.UnitDefID = discovery.UnitDefID
	// Untyped text follows the active source (for example TEDClass used by
	// host presentation). The discovery-only ai_limit text is the exception
	// because retail does not reread that field [02 R-CAT-01 §5].
	for _, key := range secondary.UnknownKeysSorted() {
		if asciiFoldContent(key) == "ai_limit" {
			delete(secondary.Unknown, key)
		}
	}
	for _, key := range discovery.UnknownKeysSorted() {
		if asciiFoldContent(key) == "ai_limit" {
			if secondary.Unknown == nil {
				secondary.Unknown = make(map[string]string)
			}
			secondary.Unknown[key] = discovery.Unknown[key]
		}
	}
	*discovery = *secondary
	return "", nil
}

// Discovery-only records keep their authored identity and allocation defaults;
// the host diagnostic explains why no gameplay definition was read
// [02 R-CAT-01 §5], DESIGN_CONTENT_VFS §2.3.
func unitSecondaryDiagnostic(fs vfs.FSOps, u *UnitDef, reason string) string {
	logical := vfs.ResourcePath("units", u.UnitName, "fbi")
	return fmt.Sprintf("nanolathe: unit secondary definition unavailable: logical path %s, providers searched [%s], expected UNITINFO gameplay definition for unit %q discovered at %s from provider %s: %s",
		logical, strings.Join(searchedProviderIDs(fs, logical), ", "), u.UnitName,
		u.DiscoveryProvenance.LogicalPath, u.DiscoveryProvenance.ProviderID, reason)
}
