package construction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestRetailMorningChainPlacementProfiles is asset-gated: the real ARM
// morning skirmish chain must carry a compiled movement/fallback profile for
// every definition that construction can validate. This deliberately uses
// PreflightSkirmish's authored selections rather than hard-coding a different
// product list [R-P0-08][05 "Factory production lifecycle"].
func TestRetailMorningChainPlacementProfiles(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	catalog, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
	manifest, err := content.PreflightSkirmish(fs, catalog, "Ashap Plateau", 0)
	if err != nil {
		t.Fatalf("preflight ARM morning chain: %v\ndiagnostics=%+v", err, manifest.Diagnostics)
	}

	keys := []string{manifest.Commander, manifest.Solar, manifest.Mex, manifest.KbotLab, manifest.LabProduct}
	var blockers []string
	for _, key := range keys {
		def, ok := catalog.Unit(key)
		if !ok || def == nil {
			t.Fatalf("preflight-selected definition %q is absent from catalog", key)
		}
		rules, err := placementRules(&Service{Catalog: catalog}, def)
		if err != nil {
			blockers = append(blockers, fmt.Sprintf("%s (%s): %v; movementclass=%q bmcode=%t waterline=%d minwater=%d maxwater=%d maxslope=%q unknown=%v", key, def.UnitName, err, def.MovementClass, def.BMCode, def.Waterline, def.MinWaterDepth, def.MaxWaterDepth, def.Unknown["MaxSlope"], def.UnknownKeysSorted()))
			continue
		}
		if !rules.ProfileResolved {
			t.Fatalf("placement profile for selected %s (%s) was not marked resolved", key, def.UnitName)
		}
	}
	if len(blockers) != 0 {
		// The citation is the class split of [R-P0-08]: a definition the
		// placement entry cannot classify has no validator to dispatch to. This
		// message also carried an open-question marker token, pointing at a marker
		// that no longer exists anywhere in this package (WU-19-166).
		t.Fatalf("selected ARM chain has unresolved placement profiles [R-P0-08]:\n%s", strings.Join(blockers, "\n"))
	}
}
