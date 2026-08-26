package units

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/vfs"
)

// RequiredCOBEntryPoints returns only callback roots that are mandatory for
// every strict production unit. Create is always required because unit
// initialization invokes it in mode I [04 §5.1]. Weapon and builder
// capabilities are deliberately not inferred from UnitDef fields: SC21 leaves
// those producer/capability gates unresolved, and retail-valid scripts may
// omit optional callbacks. Callers with an observed consumer should supply
// exact names or alternative groups through BindCOBWithRequirements.
func RequiredCOBEntryPoints(_ *content.UnitDef) []string { return []string{"Create"} }

func appendUniqueEntry(entries []string, names ...string) []string {
	for _, name := range names {
		found := false
		for _, prior := range entries {
			if prior == name {
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, name)
		}
	}
	return entries
}

// BindCOB strictly binds a compiled unit script to its loaded model and
// initializes its VM. The returned VM has completed the one mode-I Create
// start before this function returns [04 §4.1][04 §5.1]. A nil model or any
// missing/malformed asset is an error; use SyntheticCOBForTests for fixtures.
func BindCOB(fs vfs.FSOps, def *content.UnitDef, mdl *model.Model) (*cob.Binding, error) {
	if def == nil {
		return nil, fmt.Errorf("nanolathe: COB binding: nil unit definition")
	}
	if mdl == nil {
		return nil, &cob.BindingError{Diagnostics: []cob.BindingDiagnostic{{
			Code: cob.BindingMissingModel, Expected: "loaded 3DO model", Detail: fmt.Sprintf("unit %q has no model", def.UnitName),
		}}}
	}
	modelPieces := make([]string, len(mdl.Pieces))
	for i := range mdl.Pieces {
		modelPieces[i] = mdl.Pieces[i].Name
	}
	return cob.BindStrict(fs, cob.BindingRequest{
		UnitName:        def.UnitName,
		ScriptPath:      "scripts/" + def.UnitName + ".cob",
		ModelPieces:     modelPieces,
		RequiredScripts: RequiredCOBEntryPoints(def),
	})
}

// BindCOBWithEntries is the strict binding seam for callers that know a
// narrower callback set (for example a focused construction or model test).
// Create remains mandatory even when entries omits it, because production
// initialization must execute Create exactly once in mode I [04 §5.1].
func BindCOBWithEntries(fs vfs.FSOps, unitName string, mdl *model.Model, entries []string) (*cob.Binding, error) {
	return BindCOBWithRequirements(fs, unitName, mdl, entries, nil)
}

// BindCOBWithRequirements is the capability-specific strict binding seam.
// Every exact name is mandatory. Each alternative group is satisfied when at
// least one entry exists; for example, []string{"AimFromPrimary",
// "QueryPrimary"} represents the established AimFrom→Query fallback [04
// §5.3]. No capability is guessed from a unit's BMCode, CanMove, or name.
func BindCOBWithRequirements(fs vfs.FSOps, unitName string, mdl *model.Model, entries []string, alternatives [][]string) (*cob.Binding, error) {
	if mdl == nil {
		return nil, &cob.BindingError{Diagnostics: []cob.BindingDiagnostic{{Code: cob.BindingMissingModel, Expected: "loaded 3DO model", Detail: "nil model"}}}
	}
	modelPieces := make([]string, len(mdl.Pieces))
	for i := range mdl.Pieces {
		modelPieces[i] = mdl.Pieces[i].Name
	}
	entries = appendUniqueEntry(append([]string(nil), entries...), "Create")
	return cob.BindStrict(fs, cob.BindingRequest{UnitName: unitName, ModelPieces: modelPieces, RequiredScripts: entries, RequiredScriptGroups: alternatives})
}

// SyntheticCOBForTests is an explicit empty-script constructor for fixtures
// and tools. It must not be used by production composition.
func SyntheticCOBForTests() *cob.VM { return cob.NewSyntheticEmptyVM() }
