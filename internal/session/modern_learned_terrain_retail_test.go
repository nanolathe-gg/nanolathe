//go:build retail

package session

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

const learnedTerrainMap = "Crystal Maze"

// mazeFlea composes the default skirmish on Crystal Maze and sends one ARMFLEA
// from cell (156,15) toward cell (276,226): the play-test report behind the
// policy. Under the default options (Mapping on, True LOS) its route runs into
// a south-rising face near cell (202,86) that its owner's search reads as
// unexplored.
func mazeFlea(t *testing.T, cat *content.Catalog, fs *vfs.FS, mode gameplay.Mode) (*Session, *units.Unit) {
	t.Helper()
	cfg := DirectSkirmishConfig(learnedTerrainMap)
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 1, 1
	cfg.ApplyDefaults()
	if cfg.Mapping == 0 {
		t.Fatal("the default skirmish no longer enables Mapping; the case proves nothing")
	}
	s, err := NewSkirmishWithProgress(fs, cat, cfg, nil)
	if err != nil {
		t.Fatalf("construct %q: %v", learnedTerrainMap, err)
	}
	s.SetGameplay(mode)
	stepRetail(s, 2)
	flea := placeCompleteRetailUnit(t, s, "ARMFLEA", s.LocalOwner, world.CellToWorld(156)+16, world.CellToWorld(15)+16)
	gx, gz := world.CellToWorld(276)+8, world.CellToWorld(226)+8
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
		Handles: []pool.Handle{flea.Handle}, Code: 2,
		Position: orders.ResolvePos{X: gx, Y: s.World.HeightAt(gx, gz), Z: gz, InterfaceType: orders.InterfaceTypeRightClick},
	}}); err != nil {
		t.Fatal(err)
	}
	return s, flea
}

// cellsMoved steps the session and reports how far, in cells on the longer
// axis, the unit ended from where it began.
func cellsMoved(s *Session, u *units.Unit, ticks int) int32 {
	x, z := world.WorldToCell(u.X), world.WorldToCell(u.Z)
	for i := 0; i < ticks && u.Alive; i++ {
		s.Step(s.Clock.ScaledAnchor + 1)
	}
	dx, dz := world.WorldToCell(u.X)-x, world.WorldToCell(u.Z)-z
	return max(dx, -dx, dz, -dz)
}

// Nanolathe Modern policy (docs/DESIGN_MOVEMENT_PATH.md "Modern learned
// terrain"): the rejected step teaches the owner, the ordinary 60-tick repath
// routes around, and the flea moves on. Strict 3.1 keeps the retail loop, in
// which the repath is provably the route it replaced [04 R-MOV-01 §7].
//
// That the Strict flea parks at THIS face rests on this build's LOS stamp set,
// which [04 R-PATH-01 §2] records as not yet observed in retail; the Strict
// half is here as the bypass evidence for the policy.
func TestModernLearnedTerrainFreesTheMazeFlea(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	if _, ok := cat.Maps[content.CanonicalKey(learnedTerrainMap)]; !ok {
		t.Skipf("retail map %q is absent", learnedTerrainMap)
	}
	for _, tc := range []struct {
		mode  gameplay.Mode
		freed bool
	}{{gameplay.Modern, true}, {gameplay.Strict31, false}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			s, flea := mazeFlea(t, cat, fs, tc.mode)
			cellsMoved(s, flea, 700) // both modes are at the face by now
			moved := cellsMoved(s, flea, 500)
			if !flea.Alive {
				t.Skip("the flea was killed en route; the case proves nothing")
			}
			if learned := s.Movement.Rules.LearnedTerrain(s.Movement) != nil; learned != tc.freed {
				t.Fatalf("%s: learned terrain present=%v, want %v", tc.mode, learned, tc.freed)
			}
			if freed := moved >= 30; freed != tc.freed {
				t.Fatalf("%s: the flea moved %d cells in 500 ticks after reaching the face (now at cell %d,%d), want freed=%v", tc.mode, moved, world.WorldToCell(flea.X), world.WorldToCell(flea.Z), tc.freed)
			}
		})
	}
}

// The learned grid is derived state with no retail save field
// [docs/DESIGN_GAMEPLAY_RULES.md §6]: a save written mid-lesson restores with
// nothing learned, the visibility mapping grid the save DOES carry was never
// written by a lesson, and the restored flea relearns and still gets free.
func TestModernLearnedTerrainIsRelearnedAfterALoad(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	if _, ok := cat.Maps[content.CanonicalKey(learnedTerrainMap)]; !ok {
		t.Skipf("retail map %q is absent", learnedTerrainMap)
	}
	src, flea := mazeFlea(t, cat, fs, gameplay.Modern)
	cellsMoved(src, flea, 700)
	learned := src.Movement.Rules.LearnedTerrain(src.Movement)
	if learned == nil || !flea.Alive {
		t.Skip("the flea learned nothing by the save tick; the case proves nothing")
	}
	// A lesson is indexed in the search's ground frame and never reaches the
	// publisher's grid: some learned block is still unmapped for the owner.
	w, h := src.Vis.GridDimensions()
	words, private := src.Vis.WordMask(), false
	for bz := int32(0); bz < h && !private; bz++ {
		for bx := int32(0); bx < w && !private; bx++ {
			private = learned.Known(bx, bz, src.LocalOwner) && words[bz*w+bx]&(1<<src.LocalOwner) == 0
		}
	}
	if !private {
		t.Fatal("every learned block is also mapped; a lesson may be writing the visibility grid")
	}

	in, err := src.RetailBattleSaveInputs(RetailBattleSummary(src, "learned", "0", SkirmishDefaultUnitLimit), save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "LEARNED.SAV")
	if err := src.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write save: %v", err)
	}
	result, err := LoadRetailSavePath(path, RetailLoadDeps{FS: fs, Catalog: cat, SimSeed: 1, CRTSeed: 1, UnitLimit: src.Skirmish.UnitLimit})
	if err != nil || result.Battle == nil || result.Battle.Session == nil {
		t.Fatalf("load save: %v", err)
	}
	dst := result.Battle.Session
	if _, modern := dst.Movement.Rules.(*movement.ModernRules); !modern {
		t.Fatalf("the restored movement system holds %T; a load binds the session's current set", dst.Movement.Rules)
	}
	if dst.Movement.Rules.LearnedTerrain(dst.Movement) != nil {
		t.Fatal("a load restored learned terrain; the grid has no save field")
	}
	restored := dst.Units.Unit(flea.Handle)
	if restored == nil || restored.Def == nil || restored.Def.UnitName != flea.Def.UnitName {
		t.Fatal("the flea did not restore in its slot")
	}
	moved := cellsMoved(dst, restored, 600)
	if !restored.Alive {
		t.Skip("the restored flea was killed en route; the case proves nothing")
	}
	if moved < 30 {
		t.Fatalf("the restored flea moved %d cells in 600 ticks; it did not relearn the face", moved)
	}
}
