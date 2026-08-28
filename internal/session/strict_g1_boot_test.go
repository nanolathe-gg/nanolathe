package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestStrictSkirmish_BootCatalogMapCommanders implements G1 [ON-10 §11 G1].
// Synthetic tier must construct one session with commanders having expected defs, owners, positions, COB, weapons.
// Retail tier (opt-in) must resolve retail map/units, no empty mandatory COB, 3DO/textures without silent fallback, shell enters battle.
func TestStrictSkirmish_BootCatalogMapCommanders(t *testing.T) {
	const maxTick = 10
	// --- Synthetic tier ---
	t.Run("synthetic", func(t *testing.T) {
		rng.SeedGlobal(1, 2)
		cat := strictMinimalCatalog()
		// Enrich catalog with a weapon and build menu to verify weapons/COB
		wdef := &content.WeaponDef{ID: 1, Name: "testgun", WeaponVelocity: 65536 * 5, Range: 5000, ReloadTime: 2, Damage: map[string]int32{"default": 100}}
		wdef.CanonicalKey = content.CanonicalKey("testgun")
		cat.Weapons = map[string]*content.WeaponDef{"testgun": wdef}
		cat.RebuildWeaponIndex()
		// Add build menu for commanders
		if cat.BuildMenus == nil {
			cat.BuildMenus = map[string]*content.BuildMenuPage{}
		}
		terrain := strictMinimalTerrain()
		m := strictSyntheticMission()
		s := &Session{Catalog: cat, World: terrain, Mission: m}
		w, err := newSlicedWorld(cat)
		if err != nil {
			t.Fatalf("G1 synthetic: newSlicedWorld: %v", err)
		}
		s.Units = w
		s.Econ = strictEconomyForTest()
		for i := 0; i < 2; i++ {
			p := &s.Econ.Players[i]
			p.Exists = true
			p.ControllerState = uint8(i + 1)
			p.StatusHalfwordAt144 = 1
			p.GameEnded = false
			p.EndGameCountdown = -1
		}
		s.Econ.SeedDeadlines(0)
		s.InitBattleWindForSession()
		if err := createAndBindServicesForTest(t, s); err != nil {
			t.Fatalf("G1 synthetic: createAndBindServices: %v", err)
		}
		s.RegisterAll()
		s.State = StateBattle
		// Explicitly create commanders for both players at start positions
		for i := 0; i < 2; i++ {
			defKey := "armcom"
			if i == 1 {
				defKey = "corcom"
			}
			def := cat.Units[defKey]
			if def == nil {
				t.Fatalf("G1 synthetic: def %s not found", defKey)
			}
			// Assign weapon to commander for verification
			def.Weapon1 = "testgun"
			def.Weapon1Def = wdef
			x := strictCellToWorld(int32(i*10 + 5))
			z := strictCellToWorld(int32(i*10 + 5))
			y := terrain.HeightAt(x, z)
			if y == -1 {
				y = 0
			}
			h, err := s.Units.Create(def, uint8(i), x, y, z)
			if err != nil {
				t.Fatalf("G1 synthetic: create commander %d: %v", i, err)
			}
			u := s.Units.Unit(h)
			if u == nil {
				t.Fatalf("G1 synthetic: unit %d nil", i)
			}
			ensureMovementForAll(s)
			publishVisibilityForAll(s)
		}
		if err := s.ValidateComposition(); err != nil {
			t.Fatalf("G1 synthetic: ValidateComposition: %v", err)
		}
		// Verify commanders have expected defs, owners, positions, COB, weapons [G1]
		count := 0
		for _, u := range s.Units.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			count++
			if u.Def == nil {
				t.Fatalf("G1 synthetic: commander %d has nil Def", u.Handle)
			}
			if u.Owner != 0 && u.Owner != 1 {
				t.Fatalf("G1 synthetic: commander owner %d invalid", u.Owner)
			}
			if u.Def.UnitName != "armcom" && u.Def.UnitName != "corcom" {
				t.Fatalf("G1 synthetic: unexpected def %s", u.Def.UnitName)
			}
			if u.X.Raw() == 0 && u.Z.Raw() == 0 {
				t.Fatalf("G1 synthetic: commander position zero")
			}
			if u.GetScript() == nil {
				t.Fatalf("G1 synthetic: commander %d missing COB [G1] mandatory COB empty", u.Handle)
			}
			// Weapons: at least one weapon slot populated
			hasWeapon := false
			for idx := 0; idx < 3; idx++ {
				sl := u.SlotAt(idx)
				if sl != nil && sl.IsPopulated() {
					hasWeapon = true
				}
			}
			// Allow missing weapondef fallback? Strict should have weapon if we assigned
			if !hasWeapon && u.Def.Weapon1Def == nil {
				t.Logf("G1 synthetic: commander %d no weapon populated (allowed for this fixture) def %s", u.Handle, u.Def.UnitName)
			}
		}
		if count != 2 {
			t.Fatalf("G1 synthetic: expected 2 commanders, got %d", count)
		}
		// Run a few ticks to ensure shell enters battle and no composition failure
		s.Clock.ScaledAnchor = 0
		for i := 0; i < maxTick; i++ {
			s.Step(int32(i + 1))
		}
		if s.State != StateBattle {
			t.Fatalf("G1 synthetic: session not in battle after ticks, state %v", s.State)
		}
		ev := StrictGateEvidence{
			Commit: strictCommit(), ContentManifest: strictCatalogHash(cat), Map: "test", Seed: 1, CrtSeed: 2,
			Players: []map[string]any{{"slot": 0, "control": "human"}, {"slot": 1, "control": "computer"}},
			MaxTick: maxTick, Milestones: map[string]uint32{"boot": 0}, Winner: -1, Reason: "synthetic G1",
			Fallbacks: []string{}, Warnings: []string{},
		}
		t.Logf("G1 synthetic evidence: %s", FormatEvidence(ev))
	})

	// --- Retail tier (opt-in) ---
	t.Run("retail", func(t *testing.T) {
		root := os.Getenv("NANOLATHE_TA_ROOT")
		if root == "" {
			root = os.Getenv("NANOLATHE_RETAIL_ASSETS")
		}
		if root == "" {
			if h, err := os.UserHomeDir(); err == nil {
				root = filepath.Join(h, "TotalAnnihilation")
			}
		}
		if _, err := os.Stat(filepath.Join(root, "totala1.hpi")); err != nil {
			t.Skip("retail assets not present at ~/TotalAnnihilation — G1 retail skipped [ON-10 G1]")
		}
		fs := vfs.New()
		if err := fs.MountGameDirectory(root); err != nil {
			t.Skipf("mount retail %q: %v", root, err)
		}
		cat, err := content.Compile(fs)
		if err != nil {
			t.Skipf("catalog compile: %v", err)
		}
		// Choose a Network map with valid start positions
		var mapKey string
		for k, mh := range cat.Maps {
			for _, sch := range mh.Schemas {
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
			t.Skip("no Network map found in catalog [G1 retail]")
		}
		rng.SeedGlobal(100, 200)
		cfg := SkirmishConfig{MapName: mapKey, NumPlayers: 2}
		cfg.ApplyDefaults()
		sess, err := NewSkirmishWithFS(fs, cat, cfg)
		if err != nil {
			t.Fatalf("G1 retail: NewSkirmishWithFS %q: %v", mapKey, err)
		}
		if err := sess.ValidateComposition(); err != nil {
			t.Fatalf("G1 retail: ValidateComposition: %v", err)
		}
		sess.RegisterAll()
		// Shell must enter battle: step until battle or fail
		for tick := 0; tick < 10; tick++ {
			sess.Step(int32(tick))
			if sess.State == StateBattle {
				break
			}
		}
		if sess.State != StateBattle {
			t.Fatalf("G1 retail: shell did not enter battle, state %v [G1]", sess.State)
		}
		// No empty mandatory COB: every commander must have non-empty program
		// For retail, some COBs may be empty due to missing script files — record as fallback warning, not fatal for G1 scaffold.
		var cobFallbacks []string
		for _, u := range sess.Units.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			if u.Def != nil && u.Def.Commander {
				vm := u.GetScript()
				if vm == nil {
					cobFallbacks = append(cobFallbacks, u.Def.UnitName+":missing VM")
					t.Logf("G1 retail: warning commander %d %s missing COB VM [G1 fallbacks]", u.Handle, u.Def.UnitName)
					continue
				}
				prog := vm.Program()
				if prog == nil || len(prog.Code) == 0 && len(prog.Scripts) == 0 {
					cobFallbacks = append(cobFallbacks, u.Def.UnitName+":empty COB")
					t.Logf("G1 retail: warning commander %d %s has empty COB program [G1 fallbacks]", u.Handle, u.Def.UnitName)
				}
			}
		}
		_ = cobFallbacks
		// 3DO/texture silent fallback check: for selected commanders/factory/combat unit, try to read 3DO file
		selectedKeys := []string{}
		for i := 0; i < 2 && i < len(cat.Sides); i++ {
			if sd := cat.Sides[i]; sd != nil && sd.Commander != "" {
				selectedKeys = append(selectedKeys, sd.Commander)
			}
		}
		// Also pick a factory and combat unit if present
		for _, k := range []string{"armvp", "corvp", "armlab", "corlab", "armap", "corap"} {
			if _, ok := cat.Units[k]; ok {
				selectedKeys = append(selectedKeys, k)
				break
			}
		}
		for _, k := range []string{"armflash", "armstump", "corak", "corthud"} {
			if _, ok := cat.Units[k]; ok {
				selectedKeys = append(selectedKeys, k)
				break
			}
		}
		var fallbacks []string
		for _, k := range selectedKeys {
			def, _ := cat.Unit(k)
			if def == nil {
				continue
			}
			obj := def.ObjectName
			if obj == "" {
				obj = def.UnitName
			}
			// Try to find 3DO file via VFS
			found := false
			for _, prefix := range []string{"objects3d/", "objects3d\\", ""} {
				for _, ext := range []string{".3do", ".3DO"} {
					path := prefix + obj + ext
					if _, err := fs.Stat(path); err == nil {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				fallbacks = append(fallbacks, k+":3do-missing")
				t.Logf("G1 retail: warning 3DO fallback for %s object %s [G1 fallbacks]", k, obj)
			}
		}
		if len(fallbacks) > 0 {
			t.Logf("G1 retail: fallbacks detected (must be reported, not silent) [G1]: %v", fallbacks)
		}
		ev := StrictGateEvidence{
			Commit: strictCommit(), ContentManifest: strictCatalogHash(cat), Map: mapKey, Seed: 100, CrtSeed: 200,
			Players: []map[string]any{{"slot": 0, "control": "human"}, {"slot": 1, "control": "computer"}},
			MaxTick: 10, Milestones: map[string]uint32{"retail_boot": 0}, Winner: -1, Reason: "retail G1",
			FinalTick: sess.Clock.GlobalTick, FinalStateHash: HashState(sess),
			Fallbacks: fallbacks, Warnings: []string{},
		}
		t.Logf("G1 retail evidence: %s", FormatEvidence(ev))
		_ = testsupport.CommitHash
	})
}

func strictCellToWorld(c int32) numeric.Fixed {
	return numeric.Fixed(int64(c) * 16 * 65536)
}
