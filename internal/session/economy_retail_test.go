// Retail-assets end-to-end check for the reported economy regression: a solar
// collector must add its authored twenty energy to the settlement pass, and a
// metal extractor built on a map deposit must sample the deposit's seeded
// metal byte rather than the map's uniform surface metal
// [05 R-FEAT-01 §7][05 R-PROD-01 §6]. Skipped when ~/TotalAnnihilation is
// absent.
package session

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestSolarAndDepositExtractorRetail(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		if h, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(h, "TotalAnnihilation")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "totala1.hpi")); err != nil {
		t.Skip("retail assets not present at ~/TotalAnnihilation")
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount retail: %v", err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("catalog compile: %v", err)
	}

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
	sess, err := NewSkirmishWithFS(fs, cat, cfg)
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

	// --- Solar: one authored collector adds exactly its twenty per pass. ---
	// Stock solars author EnergyUse = -20 and no EnergyMake; a map cannot
	// scale that (`solarstrength` has no reader) [05 R-PROD-01 §1].
	solarDef, ok := cat.Unit("armsolar")
	if !ok || solarDef == nil {
		t.Skip("armsolar not in catalog")
	}
	wantDelta := float32(-solarDef.EnergyUse)
	if wantDelta != 20 {
		t.Fatalf("armsolar authored energyuse = %v, expected -20 [05 R-PROD-01 §1]", solarDef.EnergyUse)
	}
	beforeEnergy := sess.Econ.Players[local].PassProduced[economy.Energy]
	solarKey := solarDef.CanonicalKey
	var solar *units.Unit
	for _, off := range []int32{6, -6, 8, -8, 10} {
		if err := construction.QueueMobileBuild(com, "armsolar", com.X+cellsToWorld(off), com.Z, 1, cat); err != nil {
			t.Fatalf("QueueMobileBuild armsolar: %v", err)
		}
		solar = waitCompletedUnit(t, sess, stepOne, 4000, func(u *units.Unit) bool {
			return u.Def.CanonicalKey == solarKey && int(u.Owner) == local
		})
		if solar != nil {
			break
		}
		if q := orders.QueueForUnit(com); q != nil {
			q.PurgeUnprotected()
		}
	}
	if solar == nil {
		t.Fatalf("armsolar never completed near commander; messages=%v", sess.Build.Messages())
	}
	// One full settlement window so the completed collector is counted.
	for i := 0; i < 30; i++ {
		stepOne()
	}
	afterEnergy := sess.Econ.Players[local].PassProduced[economy.Energy]
	if got := afterEnergy - beforeEnergy; got != wantDelta {
		t.Fatalf("PassProduced[Energy] rose by %v, want exactly %v (one solar collector) [05 R-PROD-01 §1]", got, wantDelta)
	}

	// --- Extractor: the deposit's seeded byte, not the uniform seed. ---
	// Sites are the anchor cells of indestructible metal-bearing features,
	// nearest the commander first [05 R-FEAT-01 §7].
	comCX, comCZ := cellOf(com.X), cellOf(com.Z)
	type depositSite struct {
		cx, cz  int32
		metal   int32
		dist    int64
		footX   int32
		footZ   int32
		deposit uint8
	}
	var sites []depositSite
	for cz := int32(0); cz < sess.World.CellH; cz++ {
		for cx := int32(0); cx < sess.World.CellW; cx++ {
			def, ok := sess.World.FeatureDefAt(sess.World.PlotAt(cx, cz).Feature())
			if !ok || def == nil || def.Metal == 0 || !def.Indestructible {
				continue
			}
			dx, dz := int64(cx-comCX), int64(cz-comCZ)
			sites = append(sites, depositSite{cx, cz, def.Metal, dx*dx + dz*dz, def.FootprintX, def.FootprintZ, uint8(def.Metal)})
		}
	}
	if len(sites) == 0 {
		t.Skipf("map %q has no indestructible metal deposit", mapKey)
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].dist != sites[j].dist {
			return sites[i].dist < sites[j].dist
		}
		if sites[i].cz != sites[j].cz {
			return sites[i].cz < sites[j].cz
		}
		return sites[i].cx < sites[j].cx
	})

	// C1 on the plot itself: every cell of the nearest deposit's footprint
	// carries the definition's metal byte, not the map's uniform seed.
	near := sites[0]
	for dz := int32(0); dz < near.footZ; dz++ {
		for dx := int32(0); dx < near.footX; dx++ {
			cell := sess.World.PlotAt(near.cx+dx, near.cz+dz)
			if cell == nil {
				continue
			}
			if got := cell.Metal(); got != near.deposit {
				t.Fatalf("deposit cell (%d,%d) metal byte = %d, want %d [05 R-FEAT-01 §7]",
					near.cx+dx, near.cz+dz, got, near.deposit)
			}
		}
	}

	mexDef, ok := cat.Unit("armmex")
	if !ok || mexDef == nil {
		t.Skip("armmex not in catalog")
	}
	mexKey := mexDef.CanonicalKey
	var mex *units.Unit
	for i := 0; i < 12 && i < len(sites); i++ {
		s := sites[i]
		// Aim at the deposit's footprint centre so the extractor's own
		// footprint lands on the block.
		x := cellsToWorld(s.cx) + cellsToWorld(s.footX/2)
		z := cellsToWorld(s.cz) + cellsToWorld(s.footZ/2)
		if err := construction.QueueMobileBuild(com, "armmex", x, z, 1, cat); err != nil {
			t.Fatalf("QueueMobileBuild armmex: %v", err)
		}
		mex = waitCompletedUnit(t, sess, stepOne, 3000, func(u *units.Unit) bool {
			return u.Def.CanonicalKey == mexKey && int(u.Owner) == local
		})
		if mex != nil {
			break
		}
		if q := orders.QueueForUnit(com); q != nil {
			q.PurgeUnprotected()
		}
	}
	if mex == nil {
		t.Fatalf("armmex never completed on a deposit; messages=%v", sess.Build.Messages())
	}

	// SpotMetal is sampled once at placement as extractsmetal x sum(byte+1)
	// over the stamped footprint [05 R-PROD-01 §6]. Recompute it from the
	// plot bytes under the extractor's own footprint.
	anchorX := cellOf(mex.X) - mexDef.FootprintX/2
	anchorZ := cellOf(mex.Z) - mexDef.FootprintZ/2
	sum := int64(0)
	seeded := false
	for dz := int32(0); dz < mexDef.FootprintZ; dz++ {
		for dx := int32(0); dx < mexDef.FootprintX; dx++ {
			cell := sess.World.PlotAt(anchorX+dx, anchorZ+dz)
			if cell == nil {
				continue // off-map cells contribute nothing at all
			}
			b := cell.Metal()
			if b == near.deposit && near.deposit != 0 {
				seeded = true
			}
			sum += int64(b) + 1
		}
	}
	want := float32(sum) * float32(mexDef.ExtractsMetal)
	if mex.SpotMetal != want {
		t.Fatalf("SpotMetal = %v, want extractsmetal %v x sum(byte+1) %d = %v [05 R-PROD-01 §6]",
			mex.SpotMetal, mexDef.ExtractsMetal, sum, want)
	}
	if !seeded {
		t.Fatalf("extractor at cell (%d,%d) sampled no deposit byte %d; the deposit pass did not seed [05 R-FEAT-01 §7]",
			anchorX, anchorZ, near.deposit)
	}
	if mex.SpotMetal <= 0 {
		t.Fatalf("extractor yield %v is not positive", mex.SpotMetal)
	}
	t.Logf("mex at cell (%d,%d) on deposit byte %d: SpotMetal=%v (sum=%d)", anchorX, anchorZ, near.deposit, mex.SpotMetal, sum)
}
