//go:build retail

package survival

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

func authoredProducts(m *content.BuildMenuPage) []string {
	if m == nil {
		return nil
	}
	return m.Buttons
}

// The tier walk reproduces the build ladder from data alone: a factory's
// product is one tier above the commander, the factory itself (built by the
// mobile commander) is tier 0, and the pool holds no builder or commander.
func TestTiersFollowTheBuildTree(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	chain := retailcat.SelectOpeningChain(t, cat, 0)
	tiers := Tiers(cat, authoredProducts)
	if got := tiers[chain.KbotLab]; got != 0 {
		t.Errorf("factory %s tier %d, want 0: the commander builds it", chain.KbotLab, got)
	}
	if got := tiers[chain.LabProduct]; got != 1 {
		t.Errorf("lab product %s tier %d, want 1", chain.LabProduct, got)
	}
	pool := BuildPool(cat, authoredProducts)
	if pool.MaxTier < 2 || pool.Tier1Median <= 0 {
		t.Fatalf("pool max tier %d median %d: the stock ladder has advanced factories", pool.MaxTier, pool.Tier1Median)
	}
	for _, u := range pool.Units {
		if u.Def.Builder || u.Def.Commander || !mobile(u.Def) || u.Tier < 1 {
			t.Errorf("%s in the pool: builder=%v commander=%v tier=%d", u.Key, u.Def.Builder, u.Def.Commander, u.Tier)
		}
		if tiers[u.Key] != u.Tier {
			t.Errorf("%s pool tier %d, walk tier %d", u.Key, u.Tier, tiers[u.Key])
		}
	}
}
