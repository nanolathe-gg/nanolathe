package client

import (
	"strings"
	"testing"

	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/testsupport/retailcat"
)

// TestRetailProjectileModelHeaderChildCensus records the corpus fact that
// bounds the standalone projectile renderer to a root and its first child.
// It intentionally logs the asset census for the clean-room reference rather
// than baking an install-specific count into presentation behavior.
func TestRetailProjectileModelHeaderChildCensus(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	seen := make(map[string]bool)
	multiNames := make([]string, 0, 1)
	var models, withChild, extraRoots, descendants int
	for _, w := range cat.Weapons {
		if w == nil || w.Model == "" || (w.RenderType != render.RenderTypeBaseSpriteModel && w.RenderType != render.RenderTypeRecordOrientation) {
			continue
		}
		name := strings.ToLower(w.Model)
		if seen[name] {
			continue
		}
		seen[name] = true
		path := name
		if !strings.HasSuffix(path, ".3do") {
			path = "objects3d/" + path + ".3do"
		}
		m, err := compiledmodel.Load(fs, path)
		if err != nil {
			t.Fatalf("load projectile model %q: %v", path, err)
		}
		models++
		if m.Root < 0 || m.Root >= len(m.Pieces) {
			t.Fatalf("projectile model %q has invalid root %d", path, m.Root)
		}
		children := m.Pieces[m.Root].Children
		if len(children) != 0 {
			withChild++
		}
		if len(children) > 1 {
			extraRoots++
			multiNames = append(multiNames, name)
		}
		for i := range m.Pieces {
			if i != m.Root && len(m.Pieces[i].Children) != 0 {
				descendants++
			}
		}
	}
	t.Logf("retail projectile models: %d unique, %d header children, %d multi-child roots %v, %d descendant-bearing nonroots", models, withChild, extraRoots, multiNames, descendants)
}
