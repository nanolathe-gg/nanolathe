package content

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Test-only pointer-map views over the record-based compile internals.
//
// The compiler itself works on the retained record slice, because retained
// order is part of definition identity [02 R-CAT-01 §5]. A fixture test that
// compiles one family instead builds a keyed map, so these four wrappers turn
// such a map into the record slice the production function takes. They live
// here rather than in the package's source because nothing shipped calls
// them: the map shape exists only for fixtures.

// linkUnitWeapons resolves a fixture map's weapon1..3, explodeas and
// selfdestructas links; the contract is linkUnitWeaponRecords'.
func linkUnitWeapons(units map[string]*UnitDef, weapons map[string]*WeaponDef) {
	linkUnitWeaponRecords(unitMapRecords(units), weapons)
}

// validateRequiredModels validates a fixture map's required models; the
// contract is validateRequiredRecordModels'.
func validateRequiredModels(fs vfs.FSOps, units map[string]*UnitDef, weapons map[string]*WeaponDef, features map[string]*FeatureDef) error {
	return validateRequiredRecordModels(fs, unitMapRecords(units), weapons, features)
}

// fillUnitScripts binds a fixture map's programs; the contract is
// fillUnitRecordScripts'.
func fillUnitScripts(fs vfs.FSOps, units map[string]*UnitDef) []string {
	return fillUnitRecordScripts(fs, unitMapRecords(units))
}

// compileWeaponSection compiles one weapon section with no prior record, the
// shape a single-section fixture has. The production compiler always carries
// the slot's prior record so a re-parsed ID keeps its damage overrides
// [02 R-CONTENT-02].
func compileWeaponSection(section *formats.Section, sectionName string, prov Provenance) *WeaponDef {
	return compileWeaponSectionWithPrior(section, sectionName, prov, nil)
}
