package content

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

type failedFeatureReadFS struct {
	*fixtureFS
	cause error
}

func (f failedFeatureReadFS) ReadFileLimit(name string, limit int64) ([]byte, error) {
	if name == "features/tree.tdf" {
		return nil, f.cause
	}
	return f.fixtureFS.ReadFileLimit(name, limit)
}

// A failed read must not masquerade as an absent feature or successor. This is
// the host diagnostic policy in DESIGN_CONTENT_VFS §2.3, not a retail fallback.
func TestFeatureReadFailurePreservesCauseAndProvider(t *testing.T) {
	for _, cause := range []error{errors.New("authored read failure"), &fs.PathError{Op: "open", Path: "/private/fixture/features/tree.tdf", Err: fs.ErrPermission}} {
		fsys := failedFeatureReadFS{newFixtureFS(t, fixtureFile{path: "features/tree.tdf", data: "[tree]{}"}), cause}
		_, err := CompileFeatures(fsys)
		if !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), "nanolathe: feature definition read:") || !strings.Contains(err.Error(), "logical path features/tree.tdf, providers searched [fixture.hpi]") || strings.Contains(err.Error(), "/private/fixture") {
			t.Fatalf("feature read diagnostic = %v", err)
		}
		if pathCause, ok := cause.(*fs.PathError); ok {
			var got *fs.PathError
			if !errors.As(err, &got) || got != pathCause || !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("portable diagnostic lost its underlying cause: %v", err)
			}
		}
	}
}

type failedSecondaryPathFS struct {
	*fixtureFS
	statFailure bool
}

func (f failedSecondaryPathFS) Stat(name string) (vfs.EntryInfo, error) {
	if f.statFailure {
		return vfs.EntryInfo{}, &fs.PathError{Op: "stat", Path: "/private/fixture/units/a.fbi", Err: fs.ErrNotExist}
	}
	return f.fixtureFS.Stat(name)
}

func (f failedSecondaryPathFS) ReadFileLimit(string, int64) ([]byte, error) {
	return nil, &fs.PathError{Op: "open", Path: "/private/fixture/units/a.fbi", Err: fs.ErrPermission}
}

func TestSecondaryFilesystemWarningUsesLogicalPath(t *testing.T) {
	for _, statFailure := range []bool{false, true} {
		fsys := failedSecondaryPathFS{newFixtureFS(t, fixtureFile{path: "units/a.fbi", data: "[UNITINFO]{UnitName=a;}"}), statFailure}
		u := &UnitDef{UnitName: "a", DiscoveryOnly: true}
		warning, err := compileUnitSecondary(fsys, u, "")
		if err != nil || !u.DiscoveryOnly || !strings.Contains(warning, "logical path units/a.fbi") || strings.Contains(warning, "/private/fixture") {
			t.Fatalf("secondary warning = %q, error = %v", warning, err)
		}
		want := "permission denied"
		if statFailure {
			want = "file does not exist"
		}
		if !strings.Contains(warning, want) {
			t.Fatalf("secondary warning lost read cause: %q", warning)
		}
	}
}

func TestFeatureReadUsesParserByteBudget(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "features/tree.tdf", data: "[tree]{}" + strings.Repeat(" ", 1<<20)})
	features, err := CompileFeatures(fs)
	if err != nil || features["tree"] == nil {
		t.Fatalf("valid feature file above the former read cap was lost: %v", err)
	}
}

// Established discovery preserves the mismatched identity [02 R-CAT-01 §5].
// The host reports it and refuses required preflight use instead of inventing
// gameplay fields from the discovery record (DESIGN_CONTENT_VFS §2.3).
func TestSecondaryNameMismatchWarnsAndRefusesRequiredPreflight(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "units/download.fbi", data: "[UNITINFO]{UnitName=missing; Name=Download; MaxDamage=100;}"})
	result, err := compileUnitsWithLanguage(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	u := result.units["missing"]
	if u == nil || !u.DiscoveryOnly || u.MaxDamage != 0 || len(result.warnings) != 1 {
		t.Fatalf("discovery identity/diagnostic changed: unit=%+v warnings=%v", u, result.warnings)
	}
	for _, text := range []string{"logical path units/missing.fbi", "units/download.fbi", "fixture.hpi"} {
		if !strings.Contains(result.warnings[0], text) {
			t.Fatalf("warning %q lacks %q", result.warnings[0], text)
		}
	}
	for _, required := range []bool{false, true} {
		p := skirmishPreflight{fs: fs, catalog: &Catalog{Units: result.units}}
		p.unit("commander", u, required)
		if len(p.diags) != 1 {
			t.Fatalf("incomplete definition produced unrelated missing-art errors: %+v", p.diags)
		}
		for _, d := range p.diags {
			if d.Code != "incomplete-unit-definition" || d.Fatal != required || d.Logical != "units/missing.fbi" {
				t.Fatalf("preflight diagnostic = %+v, required=%t", d, required)
			}
		}
	}
}
