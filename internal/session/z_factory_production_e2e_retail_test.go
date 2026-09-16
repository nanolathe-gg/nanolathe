// Retail-assets end-to-end check for the reported factory regression: a
// commander builds a kbot lab, the player queues its first product, and the
// lab must animate open, attach and complete a mobile nanoframe instead of
// deadlocking in silent blocked exit-spot revalidation [05 "Factory production
// lifecycle"]. Skipped when ~/TotalAnnihilation is absent.
package session

import (
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func cellsToWorld(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 20) }

func cellOf(x numeric.Fixed) int32 { return int32(int64(x) >> 20) }

// waitCompletedUnit steps the session until an alive unit matching pred has
// finished building (remaining zero, completion flag set), or nil on timeout.
func waitCompletedUnit(t *testing.T, sess *Session, stepOne func(), maxSteps int, pred func(*units.Unit) bool) *units.Unit {
	t.Helper()
	for i := 0; i < maxSteps; i++ {
		stepOne()
		for _, u := range sess.Units.IterSliced() {
			if u == nil || !u.Alive || u.Def == nil || !pred(u) {
				continue
			}
			if u.Remaining <= 0 && u.Flags&construction.FlagCompleted != 0 {
				return u
			}
		}
	}
	return nil
}

// TestFactoryProductionEndToEndRetail is read-only against the catalog — it
// only looks up units, menus and defs, it never writes into it, so it shares
// the process-wide compile [internal/testsupport/retailcat].
func TestFactoryProductionEndToEndRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)

	// Deterministic map choice: first sorted map with a Network schema.
	mapKeys := make([]string, 0, len(cat.Maps))
	for k := range cat.Maps {
		mapKeys = append(mapKeys, k)
	}
	sort.Strings(mapKeys)
	mapKey := ""
	for _, k := range mapKeys {
		for _, sch := range cat.Maps[k].Schemas {
			if len(sch.Type) >= 7 && sch.Type[:7] == "Network" {
				mapKey = k
				break
			}
		}
		if mapKey != "" {
			break
		}
	}
	if mapKey == "" {
		t.Skip("no Network map in catalog")
	}

	cfg := SkirmishConfig{MapName: mapKey, NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0 // human
	cfg.Players[1].Controller = 1 // computer
	sess, err := NewSkirmishWithProgress(fs, cat, cfg, nil)
	if err != nil {
		t.Fatalf("skirmish: %v", err)
	}
	driver := int32(1 << 20)
	stepOne := func() { sess.Step(driver); driver++ }
	for tick := 0; tick < 60 && sess.State != StateBattle; tick++ {
		stepOne()
	}
	if sess.State != StateBattle {
		t.Fatalf("shell did not enter battle")
	}

	local := int(sess.LocalOwner)
	var com *units.Unit
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Def != nil && u.Def.Commander && int(u.Owner) == local {
			com = u
			break
		}
	}
	if com == nil {
		t.Fatal("no human commander")
	}
	labKey := content.CanonicalKey("armlab")

	// Commander builds armlab one site-ring out toward the map interior, as a
	// player click would. Ring offsets absorb spawn-side variance across maps.
	var lab *units.Unit
	siteOffsets := []int32{24, -24, 32, -32, 16}
	for _, off := range siteOffsets {
		siteX := com.X + cellsToWorld(off)
		if err := construction.QueueMobileBuild(com, "armlab", siteX, com.Z, 1, cat); err != nil {
			t.Fatalf("QueueMobileBuild armlab: %v", err)
		}
		lab = waitCompletedUnit(t, sess, stepOne, 8000, func(u *units.Unit) bool {
			return u.Def.CanonicalKey == labKey && int(u.Owner) == local
		})
		if lab != nil {
			break
		}
		// Order consumed without producing (edge of map etc): purge and try
		// the next ring offset.
		if q := orders.QueueForUnit(com); q != nil {
			q.PurgeUnprotected()
		}
	}
	if lab == nil {
		t.Fatalf("armlab never completed near commander; messages=%v", sess.Build.Messages())
	}
	t.Logf("armlab completed: handle=%d hp=%d at cell (%d,%d)", lab.Handle, lab.Health, cellOf(lab.X), cellOf(lab.Z))

	// Player selects the lab and queues its first authored product.
	menu, ok := cat.BuildMenus[lab.Def.CanonicalKey]
	if !ok || len(menu.Buttons) == 0 {
		t.Fatalf("armlab has no build menu")
	}
	productKey := ""
	for _, b := range menu.Buttons {
		if d, okD := cat.Unit(b); okD && d.BMCode != 0 {
			productKey = d.CanonicalKey
			break
		}
	}
	if productKey == "" {
		t.Skip("armlab menu lacks a mobile product def")
	}
	if err := construction.QueueFactoryBuild(lab, productKey, 1, cat); err != nil {
		t.Fatalf("QueueFactoryBuild %q: %v", productKey, err)
	}

	// A lone commander makes 25 energy per settlement pass while the lab's
	// accepted work draws close to 39, so the first product builds at the
	// energy-starved rate the two-stage settlement allows [05 "Two-stage
	// settlement algorithm"]. The exit-spot deadlock this test was written for
	// now resolves on the first state-2 visit; what remains is that build rate,
	// which needs roughly 7,500 ticks for ARMCK on the stock economy — the
	// former 6,000-step budget expired with the product at remaining 0.17.
	prod := waitCompletedUnit(t, sess, stepOne, 12000, func(u *units.Unit) bool {
		return u.Def.CanonicalKey == productKey && int(u.Owner) == local
	})
	if prod == nil {
		head := ""
		if q := orders.QueueForUnit(lab); q != nil && q.LenPrimary() > 0 {
			hn := q.Primary()[0]
			head = hn.BuildDefKey
		}
		t.Fatalf("factory never produced %q; head=%q messages=%v", productKey, head, sess.Build.Messages())
	}
	t.Logf("product completed: %s handle=%d hp=%d at cell (%d,%d)", productKey, prod.Handle, prod.Health, cellOf(prod.X), cellOf(prod.Z))
}
