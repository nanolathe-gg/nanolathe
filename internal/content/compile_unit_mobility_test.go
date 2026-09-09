package content

import (
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func compileMobilityFixture(t *testing.T, body string) *UnitDef {
	t.Helper()
	// Each assignment is terminated: a TDF value runs to the next ';' found by
	// a forward scan, so an unterminated one swallows the following lines
	// [02 R-MALF-01 §4].
	doc, err := formats.ParseTDF([]byte("[UNITINFO]{\n" + strings.ReplaceAll(body, "\n", ";\n") + ";\n}"))
	if err != nil {
		t.Fatal(err)
	}
	section := doc.Root.Section("UNITINFO")
	if section == nil {
		t.Fatal("missing UNITINFO")
	}
	return compileUnitSection(section, "units/test.fbi", "", Provenance{})
}

func TestCompileRetailAircraftMobilityDomain(t *testing.T) {
	root := skirmishAssetRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	cat, err := Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
	keys := make([]string, 0)
	for key, def := range cat.Units {
		if def != nil && def.CanFly {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	t.Logf("stock aircraft census: %d entries", len(keys))
	checks := []struct {
		key, side    string
		footX, footZ int32
	}{
		// Representative fighter, bomber, and construction aircraft on each side.
		{"armfig", "ARM", 2, 2},
		{"armthund", "ARM", 3, 3},
		{"armca", "ARM", 2, 2},
		{"corvamp", "CORE", 2, 2},
		{"corshad", "CORE", 3, 3},
		{"corca", "CORE", 3, 3},
	}
	for _, check := range checks {
		assertStockAircraft(t, cat, check.key, check.side, check.footX, check.footZ)
	}
	for _, check := range []struct {
		key, side    string
		footX, footZ int32
	}{{"armatlas", "ARM", 3, 3}, {"corvalk", "CORE", 3, 3}} {
		def, ok := cat.Units[CanonicalKey(check.key)]
		if !ok || def == nil {
			t.Logf("stock %s transport candidate absent; transport assertion skipped", check.key)
			continue
		}
		assertStockAircraft(t, cat, check.key, check.side, check.footX, check.footZ)
	}
}

func assertStockAircraft(t *testing.T, cat *Catalog, key, side string, footX, footZ int32) {
	t.Helper()
	def, ok := cat.Units[CanonicalKey(key)]
	if !ok || def == nil {
		t.Fatalf("stock %s %s aircraft missing", side, key)
	}
	if CanonicalKey(def.Side) != CanonicalKey(side) || def.BMCode == 0 || !def.CanFly || !def.CanMove || def.MovementClass != "" {
		t.Fatalf("stock %s fields side=%s bmcode=%d canfly=%t canmove=%t movementclass=%q; want %s/1/1/1/empty", key, def.Side, def.BMCode, def.CanFly, def.CanMove, def.MovementClass, side)
	}
	if def.FootprintX != footX || def.FootprintZ != footZ {
		t.Fatalf("stock %s footprint=%dx%d, want %dx%d", key, def.FootprintX, def.FootprintZ, footX, footZ)
	}
	if def.MobilityDomain != MobilityAircraft {
		t.Fatalf("stock %s mobility domain=%s, want aircraft", key, def.MobilityDomain)
	}
}

func TestCompileUnitMobilityDomain(t *testing.T) {
	air := compileMobilityFixture(t, "unitname=armfig\nbmcode=1\ncanfly=1\nfootprintx=2\nfootprintz=2")
	if air.MobilityDomain != MobilityAircraft {
		t.Fatalf("air mobility domain=%v, want %v", air.MobilityDomain, MobilityAircraft)
	}
	if air.MovementClass != "" {
		t.Fatalf("class-less aircraft unexpectedly has movement class %q", air.MovementClass)
	}

	ground := compileMobilityFixture(t, "unitname=broken\nbmcode=1\ncanfly=0\nfootprintx=1\nfootprintz=1")
	if ground.MobilityDomain != MobilityUnknown {
		t.Fatalf("class-less mobile domain=%v, want unknown", ground.MobilityDomain)
	}

	fixed := compileMobilityFixture(t, "unitname=armmex\nbmcode=0\ncanfly=0")
	if fixed.MobilityDomain != MobilityFixed {
		t.Fatalf("fixed domain=%v, want fixed", fixed.MobilityDomain)
	}

	profile := compileMobilityFixture(t, "unitname=armfav\nbmcode=1\nmovementclass=KBOTSS2")
	if profile.MobilityDomain != MobilityGround {
		t.Fatalf("profile-backed domain=%v, want ground", profile.MobilityDomain)
	}
}
