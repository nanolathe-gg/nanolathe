package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestAuditStockFeatureShapes is opt-in through the reference install. The
// names are stable authored feature keys, while the assertions are limited to
// the movement-relevant shape: blocking, authored footprint, and the presence
// of a sprite asset. This keeps the census from depending on incidental role
// or presentation tokens [02 "Feature record"][05 "Feature catalog and placement"].
func TestAuditStockFeatureShapes(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail assets: %v", err)
	}
	defs, err := content.CompileFeatures(fs)
	if err != nil {
		t.Fatalf("compile retail features: %v", err)
	}

	// Tree1 and Rock1a are the representative stock blocking definitions called
	// out by the feature partition in [05 "Feature catalog and placement"].
	for _, key := range []string{"tree1", "rock1a"} {
		def, ok := defs[key]
		if !ok || def == nil {
			t.Skipf("stock feature %q is absent from the mounted assets", key)
		}
		if !def.Blocking || def.FootprintX <= 0 || def.FootprintZ <= 0 {
			t.Fatalf("stock %s shape: blocking=%t footprint=%dx%d", key, def.Blocking, def.FootprintX, def.FootprintZ)
		}
		terrain := syntheticTerrain(16, 16, 0)
		terrain.FeatureDefs = []*content.FeatureDef{def}
		if err := terrain.StampFeatureRect(4, 4, 0, def.FootprintX, def.FootprintZ); err != nil {
			t.Fatalf("stamp stock %s: %v", key, err)
		}
		a := AuditCell(terrain, Template(), Cell{X: 4, Z: 4}, AuditContext{})
		if !a.Feature.Resolved || a.Feature.Definition != key || !a.Feature.Blocking || a.Profile.Verdict != AuditBlocked {
			t.Fatalf("stock %s audit: %#v", key, a)
		}
		t.Logf("stock %s: ref=%d blocking=%t footprint=%dx%d", key, a.Feature.ResolvedRef, a.Feature.Blocking, a.Feature.FootprintX, a.Feature.FootprintZ)
	}

	// Smudge01 is the authored inert/decorative sprite representative. Some
	// installs omit it; that is an asset-corpus difference, not a reason to
	// substitute an arbitrary catalog entry.
	def, ok := defs["smudge01"]
	if !ok || def == nil {
		t.Skip("stock decorative feature smudge01 is absent from the mounted assets")
	}
	if def.Blocking || def.FootprintX <= 0 || def.FootprintZ <= 0 || def.Filename == "" {
		t.Fatalf("stock smudge01 shape: blocking=%t footprint=%dx%d filename=%q", def.Blocking, def.FootprintX, def.FootprintZ, def.Filename)
	}
	terrain := syntheticTerrain(8, 8, 0)
	terrain.FeatureDefs = []*content.FeatureDef{def}
	if err := terrain.StampFeatureRect(2, 2, 0, def.FootprintX, def.FootprintZ); err != nil {
		t.Fatalf("stamp stock smudge01: %v", err)
	}
	a := AuditCell(terrain, Template(), Cell{X: 2, Z: 2}, AuditContext{})
	if !a.Feature.Resolved || a.Feature.Definition != "smudge01" || a.Feature.Blocking || !a.Feature.Sprite || a.Profile.Verdict != AuditPass {
		t.Fatalf("stock smudge01 audit: %#v", a)
	}
	t.Logf("stock smudge01: ref=%d blocking=%t footprint=%dx%d sprite=%q", a.Feature.ResolvedRef, a.Feature.Blocking, a.Feature.FootprintX, a.Feature.FootprintZ, a.Feature.SpriteAsset)
}
