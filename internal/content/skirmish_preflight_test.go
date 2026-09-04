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

// TestSkirmishPreflightUnarmedUnitCompilesAndPasses locks WU-19-176: an
// unarmed stock unit's five weapon links resolve to the record-0 [noweapon]
// sentinel (never nil) [06 R-DMG-01 §5][02 §5 R-CONTENT-02], and the preflight
// unit gate does not demand QueryPrimary/AimFromPrimary/AimPrimary/FirePrimary
// of it — that requirement is keyed on IsWeaponInactive, not on the link
// being non-nil, so a sentinel primary does not force those COB entries.
func TestSkirmishPreflightUnarmedUnitCompilesAndPasses(t *testing.T) {
	root := skirmishAssetRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	catalog, err := Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
	// armdrag (Dragon's Teeth) authors none of weapon1..3/explodeas/
	// selfdestructas: a genuinely unarmed stock definition.
	u, ok := catalog.Unit("armdrag")
	if !ok {
		t.Fatal("armdrag missing from the stock catalog")
	}
	if u.Weapon1 != "" || u.Weapon2 != "" || u.Weapon3 != "" || u.ExplodeAs != "" || u.SelfDestructAs != "" {
		t.Fatalf("fixture precondition: armdrag authors a weapon link, not the unarmed case under test: %+v", u)
	}
	for _, link := range []struct {
		family string
		def    *WeaponDef
	}{
		{"weapon1", u.Weapon1Def}, {"weapon2", u.Weapon2Def}, {"weapon3", u.Weapon3Def},
		{"explodeas", u.ExplodeAsDef}, {"selfdestructas", u.SelfDestructAsDef},
	} {
		if link.def == nil {
			t.Fatalf("armdrag %s resolved to nil, want the record-0 sentinel", link.family)
		}
		if !IsWeaponInactive(link.def) {
			t.Fatalf("armdrag %s = %v, want the inactive record-0 sentinel", link.family, link.def)
		}
	}

	p := &skirmishPreflight{fs: fs, catalog: catalog, manifest: &SkirmishManifest{}}
	p.unit("test-unarmed", u, true)
	result := p.finish()
	for _, d := range result.Diagnostics {
		if d.Kind == "test-unarmed.cob" && (d.Entry == "QueryPrimary" || d.Entry == "AimFromPrimary" || d.Entry == "AimPrimary" || d.Entry == "FirePrimary") {
			t.Fatalf("unarmed unit's record-0 primary wrongly demanded a primary-fire COB entry: %+v", d)
		}
	}
	if result.Fatal() {
		t.Fatalf("unarmed unit failed preflight: %+v", result.Diagnostics)
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
