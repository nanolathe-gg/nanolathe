package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/vfs"
)

// featureLifecycleFS authors one feature bank with three event sequences of
// NONUNIFORM per-frame delays — no retail bytes:
//
//	treeburn   delays 3, 0, 2   (6 visits)
//	treedie    delays 3, 1, 2   (6 visits)
//	treerecl   delays 2, 4      (6 visits)
//
// plus the default effect bank the composition's smoke families read.
func featureLifecycleFS(t *testing.T) *vfs.FS {
	t.Helper()
	frameOf := func(w, h int, dur uint32) formats.GAFWriteFrame {
		p := make([]byte, w*h)
		for i := range p {
			p[i] = 1
		}
		return formats.GAFWriteFrame{Width: uint16(w), Height: uint16(h), Duration: dur, Pixels: p}
	}
	smoke := make([]formats.GAFWriteFrame, 12)
	for i := range smoke {
		smoke[i] = frameOf(4, 4, 2)
	}
	fx, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: smokePuffEntry, Frames: smoke}})
	if err != nil {
		t.Fatalf("encode effect bank: %v", err)
	}
	trees, err := formats.EncodeGAF([]formats.GAFWriteEntry{
		{Name: "treeburn", Frames: []formats.GAFWriteFrame{frameOf(20, 12, 3), frameOf(5, 7, 0), frameOf(16, 24, 2)}},
		{Name: "treedie", Frames: []formats.GAFWriteFrame{frameOf(8, 8, 3), frameOf(8, 8, 1), frameOf(8, 8, 2)}},
		{Name: "treerecl", Frames: []formats.GAFWriteFrame{frameOf(8, 8, 2), frameOf(8, 8, 4)}},
	})
	if err != nil {
		t.Fatalf("encode feature bank: %v", err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "anims"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "anims", "fx.gaf"), fx, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "anims", "trees.gaf"), trees, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

// featureLifecycleCatalog is the tree with distinct death, reclaim and burnt
// successors, each a resting sprite of its own.
func featureLifecycleCatalog() *content.Catalog {
	cat := minimalCatalogForStrict()
	tree := &content.FeatureDef{Filename: "trees", SeqName: "tree", SeqNameBurn: "treeburn", SeqNameDie: "treedie", SeqNameReclamate: "treerecl", FootprintX: 1, FootprintZ: 1, Damage: 10, Flamable: true, SparkTime: 150, SpreadChance: 0, Reclaimable: true, Metal: 5}
	tree.CanonicalKey = "tree1"
	dead := &content.FeatureDef{Filename: "trees", SeqName: "dead", FootprintX: 1, FootprintZ: 1}
	dead.CanonicalKey = "tree1dead"
	reclaimed := &content.FeatureDef{Filename: "trees", SeqName: "smudge", FootprintX: 1, FootprintZ: 1}
	reclaimed.CanonicalKey = "tree1reclaimed"
	burnt := &content.FeatureDef{Filename: "trees", SeqName: "ash", FootprintX: 1, FootprintZ: 1}
	burnt.CanonicalKey = "tree1burnt"
	tree.FeatureDeadDef, tree.FeatureReclamateDef, tree.FeatureBurntDef = dead, reclaimed, burnt
	for _, def := range []*content.FeatureDef{tree, dead, reclaimed, burnt} {
		cat.Features[def.CanonicalKey] = def
	}
	return cat
}

// featureLifecycleSession composes a battle over the authored banks. The
// terrain's feature table and name table are pre-seeded in one fixed order so
// the save's type-name side channel remaps by name on the other side
// [08 R-SAVE-FEATURE-01].
func featureLifecycleSession(t *testing.T, fs *vfs.FS, cat *content.Catalog) *Session {
	t.Helper()
	terrain := minimalTerrain()
	for _, key := range []string{"tree1", "tree1dead", "tree1reclaimed", "tree1burnt"} {
		terrain.FeatureDefs = append(terrain.FeatureDefs, cat.Features[key])
		terrain.FeatureNames = append(terrain.FeatureNames, key)
	}
	w, err := newSlicedWorldWithCOB(cat, fs)
	if err != nil {
		t.Fatalf("unit pool: %v", err)
	}
	s := &Session{
		Catalog: cat,
		World:   terrain,
		Mission: syntheticMission(),
		Units:   w,
		Clock:   &clock.State{},
		Econ:    &economy.Service{},
	}
	s.SeedSessionRNG(41, 43)
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	if s.Features.SequenceFrames == nil {
		t.Fatal("composition left the cursor metadata seam unbound")
	}
	return s
}

// featureSaveLoad saves the source session's features through the session
// writer and restores them into a freshly composed session through the
// session loader's feature path — the same two functions the battle
// Save/Load pair reaches. It returns the restored session and the number of
// simulation draws the restore consumed.
func featureSaveLoad(t *testing.T, src *Session, fs *vfs.FS, cat *content.Catalog) (*Session, uint64) {
	t.Helper()
	cells := int64(src.World.CellW) * int64(src.World.CellH)
	p, err := ProjectRetailSession(src, RetailSaveInputs{
		Summary: save.Summary{Gametype: 1, Players: 1, IsBattle: true},
		Mapping: make([]byte, cells>>1),
	})
	if err != nil {
		t.Fatalf("ProjectRetailSession: %v", err)
	}
	dst := featureLifecycleSession(t, fs, cat)
	dst.Features.ResetForRestore()
	before := dst.SimRNG().Draws()
	if err := restoreRetailFeatures(dst.Features, dst.Catalog, p.Features); err != nil {
		t.Fatalf("restoreRetailFeatures: %v", err)
	}
	return dst, dst.SimRNG().Draws() - before
}

// TestFeatureDeathAndReclaimRecordsSurviveSaveLoad is FL-01's gate: a death or
// reclaim animation attached by the ordinary transition, advanced past frame
// 0, saved through the session writer and restored through the session
// loader comes back bound to its sequence at the SAVED frame with frame 0's
// delay — the documented loss — and then completes on the visit its remaining
// frames run out, stamping the family's successor
// [05 R-FEAT-01 §5][05 R-FEAT-01 §10][08 R-SAVE-FEATURE-01].
func TestFeatureDeathAndReclaimRecordsSurviveSaveLoad(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause features.Cause
		// ticks before the save, the saved frame, the visit index the
		// presentation sees after the restore, ticks after the restore until
		// the successor, and the successor.
		before        int
		savedFrame    int32
		restoredVisit int32
		after         int
		successor     string
		seq           string
	}{
		// treedie 3,1,2: four visits reach frame 2 (delay 2). Restored as
		// frame 2 with frame 0's delay 3, it holds three more visits.
		{name: "death", cause: features.CauseDead, before: 4, savedFrame: 2, restoredVisit: 4, after: 3, successor: "tree1dead", seq: "treedie"},
		// treerecl 2,4: three visits reach frame 1 (delay 3). Restored as
		// frame 1 with frame 0's delay 2, it holds two more visits.
		{name: "reclaim", cause: features.CauseReclaim, before: 3, savedFrame: 1, restoredVisit: 2, after: 2, successor: "tree1reclaimed", seq: "treerecl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := featureLifecycleFS(t)
			cat := featureLifecycleCatalog()
			src := featureLifecycleSession(t, fs, cat)
			tree := cat.Features["tree1"]
			if src.Features.PlaceAt(5, 5, tree) == nil {
				t.Fatal("tree placement failed")
			}
			src.Features.RemoveFeatureAt(5, 5, tc.cause)
			inst := src.Features.InstanceAt(5, 5)
			if inst == nil || !inst.IsAnimating || inst.Def != tree {
				t.Fatalf("the transition did not attach an animation record: %#v", inst)
			}
			for tick := 1; tick <= tc.before; tick++ {
				src.Features.TickLifecycle(uint32(tick))
			}
			if inst.CursorFrame() != tc.savedFrame {
				t.Fatalf("after %d visits the cursor is on frame %d, want %d", tc.before, inst.CursorFrame(), tc.savedFrame)
			}

			dst, draws := featureSaveLoad(t, src, fs, cat)
			if draws != 0 {
				t.Fatalf("restoring a %s record drew %d times; only a burn's re-ignition draws", tc.name, draws)
			}
			back := dst.Features.InstanceAt(5, 5)
			if back == nil || back.Def != tree || !back.IsAnimating || back.IsBurning {
				t.Fatalf("restored anchor holds %#v, want the tree's live %s record", back, tc.name)
			}
			seq, _, visit, ok := back.EventSequence()
			if !ok || seq != tc.seq || visit != tc.restoredVisit {
				t.Fatalf("restored record presents %q at visit %d (ok=%v), want %q at visit %d", seq, visit, ok, tc.seq, tc.restoredVisit)
			}
			if back.CursorFrame() != tc.savedFrame {
				t.Fatalf("restored cursor frame %d, want the saved %d", back.CursorFrame(), tc.savedFrame)
			}
			// Placement and reclaim queries see the live record, not the
			// successor, until the sequence ends.
			if _, _, ok := dst.Features.ReclaimAt(5, 5); ok {
				t.Fatal("a reclaim payout was accepted on a cell playing its event animation [05 R-FEAT-01 §15]")
			}
			for tick := 1; tick < tc.after; tick++ {
				dst.Features.TickLifecycle(uint32(tick))
				if dst.Features.InstanceAt(5, 5) != back {
					t.Fatalf("visit %d after the restore replaced the record early", tick)
				}
			}
			dst.Features.TickLifecycle(uint32(tc.after))
			got := dst.Features.InstanceAt(5, 5)
			if got == nil || got.Def == nil || got.Def.CanonicalKey != tc.successor {
				t.Fatalf("after the restored sequence ended the anchor holds %v, want %q", got, tc.successor)
			}
			if got.IsAnimating || got.IsBurning {
				t.Fatal("the successor inherited the event record")
			}
			if !dst.World.PlotAt(5, 5).IsRealFeature() || dst.World.PlotAt(5, 5).Occupied() {
				t.Fatal("the successor's cell still carries the animation's attached bit")
			}
		})
	}
}

// TestFeatureBurnRecordSurvivesSaveLoad is the burn half: the restore re-runs
// ignition — a fresh simulation draw — and then overwrites the countdown with
// the saved HIGH nibble (low nibble zero) and the cursor frame with the saved
// byte, so the burn resumes with its spark countdown truncated and completes
// from its authored frames [05 R-FEAT-01 §9][08 R-SAVE-FEATURE-01].
func TestFeatureBurnRecordSurvivesSaveLoad(t *testing.T) {
	fs := featureLifecycleFS(t)
	cat := featureLifecycleCatalog()
	src := featureLifecycleSession(t, fs, cat)
	tree := cat.Features["tree1"]
	if src.Features.PlaceAt(6, 6, tree) == nil {
		t.Fatal("tree placement failed")
	}
	if !src.Features.Ignite(6, 6, 1, 0) {
		t.Fatal("ignition refused")
	}
	inst := src.Features.InstanceAt(6, 6)
	// treeburn 3,0,2: four visits reach frame 2 (delay 2, one visit left).
	for tick := 1; tick <= 4; tick++ {
		src.Features.TickLifecycle(uint32(tick))
	}
	if inst.CursorFrame() != 2 || !inst.IsBurning {
		t.Fatalf("after four visits the burn cursor is on frame %d burning=%v, want frame 2", inst.CursorFrame(), inst.IsBurning)
	}
	countdown := inst.BurnCountdown
	if countdown < 16 {
		t.Fatalf("fixture countdown %d has no high nibble to lose; reseed", countdown)
	}

	dst, draws := featureSaveLoad(t, src, fs, cat)
	if draws != 1 {
		t.Fatalf("restoring one burn record drew %d times, want ignition's one [05 R-FEAT-01 §9]", draws)
	}
	back := dst.Features.InstanceAt(6, 6)
	if back == nil || !back.IsBurning || back.Def != tree {
		t.Fatalf("restored anchor holds %#v, want the burning tree", back)
	}
	if back.BurnCountdown != countdown&^0x0f {
		t.Fatalf("restored countdown %d, want the saved %d with its low nibble lost: %d", back.BurnCountdown, countdown, countdown&^0x0f)
	}
	if back.CursorFrame() != 2 || back.CursorDelay() != 3 {
		t.Fatalf("restored burn cursor frame %d delay %d, want the saved frame 2 with frame 0's delay 3", back.CursorFrame(), back.CursorDelay())
	}
	if !dst.World.PlotAt(6, 6).Occupied() {
		t.Fatal("the restored burn did not attach the anchor's instance bit")
	}
	// Frame 2 holds for frame 0's delay (3 visits), then the burn ends and
	// `featureburnt` is stamped at the snapped centre.
	for tick := 1; tick < 3; tick++ {
		dst.Features.TickLifecycle(uint32(tick))
		if dst.Features.InstanceAt(6, 6) != back {
			t.Fatalf("visit %d after the restore ended the burn early", tick)
		}
	}
	dst.Features.TickLifecycle(3)
	got := dst.Features.InstanceAt(6, 6)
	if got == nil || got.Def == nil || got.Def.CanonicalKey != "tree1burnt" {
		t.Fatalf("after the restored burn ended the anchor holds %v, want tree1burnt [05 R-FEAT-01 §10 pass 3c]", got)
	}
}
