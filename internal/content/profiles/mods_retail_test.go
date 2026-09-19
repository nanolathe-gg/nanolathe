//go:build retail

package profiles_test

import (
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// modRootsEnvPrefix is followed by the profile's upper-case name. The value is
// a host path list — the mod's own roots in load order, appended after the
// retail install. The paths are never committed: this check skips when the
// variable is unset, which is how it behaves on every machine but the one that
// has the content.
//
//	NANOLATHE_MOD_ROOTS_ESCALATION="<esc install files>"
//	NANOLATHE_MOD_ROOTS_PROTA="<ProTA root>"
//	NANOLATHE_MOD_ROOTS_ZERO="<TA Zero base>:<TA Zero alpha>"
const modRootsEnvPrefix = "NANOLATHE_MOD_ROOTS_"

func modRoots(t *testing.T, profile string) []string {
	t.Helper()
	name := modRootsEnvPrefix + strings.ToUpper(profile)
	value := os.Getenv(name)
	if strings.TrimSpace(value) == "" {
		t.Skipf("%s is unset: this check needs the %s content set's roots in load order", name, profile)
	}
	var roots []string
	for _, root := range strings.Split(value, string(os.PathListSeparator)) {
		if strings.TrimSpace(root) != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		t.Fatalf("%s=%q names no root", name, value)
	}
	return roots
}

// mountWithMod mounts the retail install and then the mod's roots in load
// order, the overlay a player of that content set runs.
func mountWithMod(t *testing.T, profile string) *vfs.FS {
	t.Helper()
	roots := append([]string{testsupport.RetailRoot(t)}, modRoots(t, profile)...)
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountGameDirectories(roots); err != nil {
		t.Fatalf("mount %s roots: %v", profile, err)
	}
	return fs
}

// unitDefinitionFiles counts the unit definitions the profile's directory
// table makes visible under the retail name. It is the measurement that does
// not depend on the definition domain or the read caps, so it holds for all
// three content sets while E2 and E3 are outstanding.
func unitDefinitionFiles(t *testing.T, view vfs.FSOps) int {
	t.Helper()
	entries, err := view.ReadDir("units")
	if err != nil {
		t.Fatalf("ReadDir units through the directory table: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir || !strings.HasSuffix(strings.ToLower(entry.Name), ".fbi") {
			continue
		}
		count++
		if !strings.HasPrefix(entry.Path, "units/") {
			t.Fatalf("entry path %q kept the content set's own directory name", entry.Path)
		}
	}
	return count
}

// TestZeroContentSetCompilesThroughItsDirectoryTable is the end-to-end proof
// on real content: TA Zero's trees are all renamed, and with the profile's
// table in place the whole catalog compiles, at the definition count the
// inventory recorded, with retail-named provenance.
func TestZeroContentSetCompilesThroughItsDirectoryTable(t *testing.T) {
	fs := mountWithMod(t, "zero")
	profile, err := profiles.Detect(fs)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if profile.Name != "zero" {
		t.Fatalf("detected %q, want zero", profile.Name)
	}
	view := profile.Layout().Apply(fs)
	if got := unitDefinitionFiles(t, view); got != 269 {
		t.Fatalf("unit definition files = %d, want the inventory's 269", got)
	}
	catalog, err := content.Compile(view)
	if err != nil {
		t.Fatalf("compile TA Zero: %v", err)
	}
	if len(catalog.Units) != 269 {
		t.Fatalf("compiled units = %d, want the inventory's 269", len(catalog.Units))
	}
	for key, unit := range catalog.Units {
		if unit == nil {
			continue
		}
		if !strings.HasPrefix(unit.Provenance.LogicalPath, "units/") {
			t.Fatalf("unit %s provenance %q kept the content set's own directory name", key, unit.Provenance.LogicalPath)
		}
	}
}

// TestProTAContentSetCompilesUnderItsProfile is the end-to-end proof on real
// content: ProTA renames five trees and ships a ninety-table LOS file, and
// under its profile's directory table and read caps the whole catalog compiles
// at the definition count the inventory recorded.
func TestProTAContentSetCompilesUnderItsProfile(t *testing.T) {
	fs := mountWithMod(t, "prota")
	profile, err := profiles.Detect(fs)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if profile.Name != "prota" {
		t.Fatalf("detected %q, want prota", profile.Name)
	}
	view := profile.Layout().Apply(fs)
	if got := unitDefinitionFiles(t, view); got != 317 {
		t.Fatalf("unit definition files = %d, want the inventory's 317", got)
	}
	catalog, err := content.CompileWithOptions(view, content.Options{Limits: content.LimitsFromProfile(profile.Limits)})
	if err != nil {
		t.Fatalf("compile ProTA: %v", err)
	}
	if len(catalog.Units) != 317 {
		t.Fatalf("compiled units = %d, want the inventory's 317", len(catalog.Units))
	}
	assertLargeLOSTableCompiled(t, catalog)
	// The retail caps still refuse the same content, which is what keeps the
	// raised caps a profile decision rather than a change to retail admission.
	if _, retailErr := content.Compile(view); retailErr == nil {
		t.Fatal("retail read caps no longer refuse ProTA's enlarged LOS table")
	} else {
		for _, want := range []string{"logical path gamedata/los.tdf", "exceeds read limit"} {
			if !strings.Contains(retailErr.Error(), want) {
				t.Fatalf("retail compile error %q is not the read-cap refusal: it lacks %q", retailErr, want)
			}
		}
	}
}

// TestEscalationReadCapsAdmitItsMapAndLOSTable is the E3 statement for TA:
// Escalation, whose 19.3 MB `[esc] dark prime.tnt` and 2.4 MB `los.tdf` are
// both larger than a retail install is sized for. Under the profile's caps
// both are read; under the retail caps both are refused, which is what keeps
// the raised caps a profile decision rather than a change to retail admission.
//
// The whole-catalog compile is a separate statement below, because Escalation
// stops on a packaging gap that is not this unit's.
func TestEscalationReadCapsAdmitItsMapAndLOSTable(t *testing.T) {
	fs := mountWithMod(t, "escalation")
	profile, err := profiles.Detect(fs)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if profile.Name != "escalation" {
		t.Fatalf("detected %q, want escalation", profile.Name)
	}
	view := profile.Layout().Apply(fs)
	limits := content.LimitsFromProfile(profile.Limits)

	// Layout must retain the real mount's header-range fast path. That path
	// never reads the whole terrain and is independent of a whole-file cap.
	if _, ok := view.(vfs.RangeReader); !ok {
		t.Fatal("profile layout hid the mount's byte-range capability")
	}
	if _, err := content.CompileMaps(view); err != nil {
		t.Fatalf("ranged map census: %v", err)
	}
	// Deliberately expose only FSOps to exercise the capped fallback, rather
	// than depending on a wrapper accidentally hiding RangeReader.
	wholeFileView := struct{ vfs.FSOps }{view}
	if _, err := content.CompileMaps(wholeFileView); err == nil {
		t.Fatal("the retail map read cap admitted Escalation's oversize terrain file")
	} else {
		for _, want := range []string{"[esc] dark prime.tnt", "exceeds read limit"} {
			if !strings.Contains(strings.ToLower(err.Error()), want) {
				t.Fatalf("retail map compile error %q is not the read-cap refusal: it lacks %q", err, want)
			}
		}
	}
	// The map cap is what admits it and nothing else: the profile's limits with
	// only that count put back to retail refuse the same file again.
	mapCapHeldBack := limits
	mapCapHeldBack.TNTBytes = content.RetailTNTBytes
	if _, err := content.CompileWithOptions(wholeFileView, content.Options{Limits: mapCapHeldBack}); err == nil {
		t.Fatal("the retail map read cap admitted Escalation's oversize terrain file")
	} else if !strings.Contains(strings.ToLower(err.Error()), "[esc] dark prime.tnt") {
		t.Fatalf("holding the map cap at retail did not stop the compile on that map: %v", err)
	}
	if _, err := content.CompileWithOptions(wholeFileView, content.Options{Limits: limits}); err == nil || !strings.Contains(err.Error(), "_dead.3do") {
		t.Fatalf("profile cap should pass the whole-file map census and reach the known missing-model gap: %v", err)
	}

	if _, err := content.CompileLOSTables(view, content.RetailLimits()); err == nil {
		t.Fatal("the retail battle-table read cap admitted Escalation's enlarged LOS table")
	} else {
		for _, want := range []string{"logical path gamedata/los.tdf", "exceeds read limit"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("retail LOS compile error %q is not the read-cap refusal: it lacks %q", err, want)
			}
		}
	}
	tables, err := content.CompileLOSTables(view, limits)
	if err != nil {
		t.Fatalf("LOS tables under the profile's read cap: %v", err)
	}
	assertLargeLOSTable(t, tables)
}

// TestEscalationContentSetStopsAtItsPackagingGap records exactly how far the
// content profile takes TA: Escalation. Its 549 definitions, its oversize map
// and its enlarged LOS table are all admitted, and the compile then stops on a
// corpse model the content set does not ship — a cross-reference failure
// retail also treats as fatal [02 "Cross-reference failure policy"], not a
// limit and not a path. That gap is content packaging, so it is not settled by
// raising anything.
func TestEscalationContentSetStopsAtItsPackagingGap(t *testing.T) {
	fs := mountWithMod(t, "escalation")
	profile, err := profiles.Detect(fs)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	view := profile.Layout().Apply(fs)
	if got := unitDefinitionFiles(t, view); got != 549 {
		t.Fatalf("unit definition files = %d, want the inventory's 549", got)
	}
	_, err = content.CompileWithOptions(view, content.Options{Limits: content.LimitsFromProfile(profile.Limits)})
	if err == nil {
		t.Fatal("TA: Escalation compiled: the packaging gap is closed, so update this check")
	}
	for _, unwanted := range []string{"exceeds read limit", "unit definitions exceed"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Fatalf("a limit is still the blocker under the profile's limits: %v", err)
		}
	}
	for _, want := range []string{"_dead.3do", "expected valid 3DO model"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("compile error %q is not the missing-corpse-model packaging gap: it lacks %q", err, want)
		}
	}
	// The retail limits still refuse the same content on the definition domain,
	// the first gate it fails.
	if _, retailErr := content.Compile(view); retailErr == nil || !strings.Contains(retailErr.Error(), "549 unit definitions exceed 511 usable IDs in 512-bit domain") {
		t.Fatalf("retail limits no longer refuse 549 definitions: %v", retailErr)
	}
}

// assertLargeLOSTableCompiled is assertLargeLOSTable for a whole catalog.
func assertLargeLOSTableCompiled(t *testing.T, catalog *content.Catalog) {
	t.Helper()
	assertLargeLOSTable(t, catalog.LOS)
}

// assertLargeLOSTable states what E3 bought for the terrain-ray path: the
// compiled table list has one slot per declared table, filled by the loader's
// own naming rule, at a count far past the nine a retail install declares
// [03 R-COMP-02 §1]. Nothing in the compiled shape is bounded by a slot count
// — the retail loader sizes its table list to the declared numtables — so an
// enlarged file is read the way the retail one is.
func assertLargeLOSTable(t *testing.T, tables *content.LOSTables) {
	t.Helper()
	if tables == nil {
		t.Fatal("no LOS tables compiled")
	}
	if tables.NumTables <= 9 {
		t.Fatalf("numtables = %d, want the content set's enlarged table", tables.NumTables)
	}
	if len(tables.Tables) < int(tables.NumTables) {
		t.Fatalf("compiled %d tables for %d declared slots", len(tables.Tables), tables.NumTables)
	}
	for slot := 0; slot < int(tables.NumTables); slot++ {
		if got := tables.Tables[slot].TableNum; got != slot+1 {
			t.Fatalf("slot %d holds TABLE%d, want TABLE%d", slot, got, slot+1)
		}
	}
	// The highest table any observer reaches is the one below the declared
	// count, and it must carry spokes: an empty top table would mean the
	// enlarged file had been read but not filled.
	top := tables.Tables[tables.NumTables-2]
	if len(top.Lines) == 0 {
		t.Fatalf("TABLE%d, the highest reachable table, compiled no lines", top.TableNum)
	}
}
