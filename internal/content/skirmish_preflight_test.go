package content

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// skirmishAssetRoot is also called from compile_unit_mobility_test.go and
// compile_unit_placement_test.go (not owned by this change).
func skirmishAssetRoot(t *testing.T) string {
	t.Helper()
	return testsupport.RetailRoot(t)
}

func TestSkirmishPreflightRetailBundle(t *testing.T) {
	root := skirmishAssetRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	catalog, err := Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
	manifest, preflightErr := PreflightSkirmish(fs, catalog, "Ashap Plateau", 0)
	if preflightErr != nil {
		t.Fatalf("retail morning bundle failed: %v\ndiagnostics=%+v", preflightErr, manifest.Diagnostics)
	}
	if manifest.Hash == "" || len(manifest.Assets) == 0 {
		t.Fatal("preflight returned no stable asset manifest")
	}
	if manifest.Commander == "" || manifest.Solar == "" || manifest.Mex == "" || manifest.KbotLab == "" || manifest.LabProduct == "" {
		t.Fatalf("incomplete build chain: %+v", manifest)
	}
	if manifest.Commander != "armcom" || manifest.Solar != "armsolar" || manifest.Mex != "armmex" || manifest.KbotLab != "armlab" || manifest.LabProduct != "armpw" {
		t.Fatalf("unexpected ARM chain: commander=%s solar=%s mex=%s lab=%s product=%s", manifest.Commander, manifest.Solar, manifest.Mex, manifest.KbotLab, manifest.LabProduct)
	}
	if manifest.AIProfile == "" {
		t.Fatal("AI profile was not resolved")
	}
	second, secondErr := PreflightSkirmish(fs, catalog, "Ashap Plateau", 0)
	if secondErr != nil || second.Hash != manifest.Hash {
		t.Fatalf("preflight is not deterministic: first=%s second=%s err=%v", manifest.Hash, second.Hash, secondErr)
	}
	core, coreErr := PreflightSkirmish(fs, catalog, "Ashap Plateau", 1)
	if coreErr != nil {
		t.Fatalf("retail CORE morning bundle failed: %v\ndiagnostics=%+v", coreErr, core.Diagnostics)
	}
	if core.Commander != "corcom" || core.Solar != "corsolar" || core.Mex != "cormex" || core.KbotLab != "corlab" || core.LabProduct != "corak" {
		t.Fatalf("unexpected CORE chain: commander=%s solar=%s mex=%s lab=%s product=%s", core.Commander, core.Solar, core.Mex, core.KbotLab, core.LabProduct)
	}
}

func TestSkirmishManifestHashIncludesDiagnostics(t *testing.T) {
	a := &SkirmishManifest{MapKey: "map", Side: 0, SideKey: "SIDE0", Assets: []SkirmishAsset{{Kind: "map", Logical: "maps/a.ota"}}}
	a.Hash = skirmishManifestHash(a)
	b := &SkirmishManifest{MapKey: "map", Side: 0, SideKey: "SIDE0", Assets: []SkirmishAsset{{Kind: "map", Logical: "maps/a.ota"}}, Diagnostics: []SkirmishDiagnostic{{Code: "missing-required", Fatal: true, Kind: "map", Logical: "maps/a.tnt"}}}
	b.Hash = skirmishManifestHash(b)
	if a.Hash == b.Hash {
		t.Fatal("manifest hash ignored diagnostics")
	}
}
