package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestUpdateGroupsLeavesStockVectorsEmpty(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")},
		UnitName:         "armflash",
		CanMove:          true,
		CanAttack:        true,
		MaxVelocity:      100,
		MaxDamage:        100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := units.New(16, cat)
	for i := 0; i < 12; i++ {
		h, err := w.Create(def, 0, world.CellToWorld(int32(i+1)), 0, world.CellToWorld(1))
		if err != nil {
			t.Fatalf("create unit %d: %v", i, err)
		}
		if u := w.Unit(h); u != nil {
			u.Remaining = 0
		}
	}

	m := &Manager{Player: 0}
	m.updateGroups(w)
	if got := m.groupMemberCount(); got != 0 {
		t.Fatalf("ordinary unit creation populated tactical vectors: %d members", got)
	}
	// A second refresh must remain inert; in particular, no cap/fallback bucket
	// may receive a unit after the first vectors are empty.
	m.updateGroups(w)
	if got := m.groupMemberCount(); got != 0 {
		t.Fatalf("repeated refresh populated tactical vectors: %d members", got)
	}
}

func TestUpdateGroupsCleansExistingVectorsInStableOrder(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")},
		UnitName:         "armflash",
		CanMove:          true,
		CanAttack:        true,
		MaxVelocity:      100,
		MaxDamage:        100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := units.New(16, cat)
	valid, err := w.Create(def, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatal(err)
	}
	dead, err := w.Create(def, 0, world.CellToWorld(2), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := w.Create(def, 1, world.CellToWorld(3), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatal(err)
	}
	incomplete, err := w.Create(def, 0, world.CellToWorld(4), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(valid).Remaining = 0
	w.Unit(dead).Alive = false
	w.Unit(incomplete).Remaining = 0.5

	m := &Manager{
		Player: 0,
		GroupWaveA: []pool.Handle{
			valid,
			dead,
			foreign,
			incomplete,
			pool.Handle(0xffff),
		},
		// Seed another vector to prove every manager vector uses the same
		// ownership/completion cleanup rather than only wave A.
		GroupExplore: []pool.Handle{valid, foreign},
	}
	m.updateGroups(w)
	if len(m.GroupWaveA) != 1 || m.GroupWaveA[0] != valid {
		t.Fatalf("wave cleanup = %v, want stable [valid=%d]", m.GroupWaveA, valid)
	}
	if len(m.GroupExplore) != 1 || m.GroupExplore[0] != valid {
		t.Fatalf("explore cleanup = %v, want stable [valid=%d]", m.GroupExplore, valid)
	}
}

func TestMergeWaveGroupsTransfersOnlyEstablishedMembers(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")},
		UnitName:         "armflash",
		CanMove:          true,
		CanAttack:        true,
		MaxVelocity:      100,
		MaxDamage:        100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := units.New(16, cat)
	create := func(x, z int32) pool.Handle {
		h, err := w.Create(def, 0, numeric.Fixed(x)*numeric.Fixed(1<<16), 0, numeric.Fixed(z)*numeric.Fixed(1<<16))
		if err != nil {
			t.Fatal(err)
		}
		w.Unit(h).Remaining = 0
		return h
	}

	// The third current member is an outlier from the current centroid:
	// its distance² is greater than threshold*count (20,000*3), while the
	// two colocated anchors remain.
	anchor := create(0, 0)
	anchor2 := create(0, 0)
	outlier := create(500, 0)
	current, peer := mergeWaveGroups([]pool.Handle{anchor, anchor2, outlier}, nil, w, waveAThreshold)
	if len(current) != 2 || current[0] != anchor || current[1] != anchor2 {
		t.Fatalf("outlier merge current=%v, want [%d %d]", current, anchor, anchor2)
	}
	if len(peer) != 1 || peer[0] != outlier {
		t.Fatalf("outlier merge peer=%v, want [%d]", peer, outlier)
	}

	// Peer collection is strict: a member at exactly sqrt(threshold) in each
	// axis has distance² == threshold and must not move. A closer member does.
	close := create(10, 0)
	boundary := create(100, 100)
	current, peer = mergeWaveGroups([]pool.Handle{anchor}, []pool.Handle{boundary, close}, w, waveAThreshold)
	if len(current) != 2 || current[1] != close {
		t.Fatalf("peer collection current=%v, want [%d %d]", current, anchor, close)
	}
	if len(peer) != 1 || peer[0] != boundary {
		t.Fatalf("peer collection peer=%v, want boundary [%d]", peer, boundary)
	}
}

func TestGroupCentroidUsesStoredPixelDomain(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")},
		UnitName:         "armflash",
		CanMove:          true,
		MaxVelocity:      100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := units.New(4, cat)
	first, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// integer division. Fractional fixed-point state must not leak into the
	// regroup target [R-P0-04; 03 §2.1; I3].
	w.Unit(first).X = numeric.Fixed(-98304) // -1.5 pixels → high word -2
	w.Unit(second).X = numeric.Fixed(32768) // +0.5 pixels → stored word 0
	w.Unit(first).Z = numeric.Fixed(-98304)
	w.Unit(second).Z = numeric.Fixed(-32768)
	x, z, ok := groupCentroid([]pool.Handle{first, second}, w)
	if !ok {
		t.Fatal("groupCentroid reported empty group")
	}
	if x != -numeric.Fixed(65536) || z != -numeric.Fixed(65536) {
		t.Fatalf("groupCentroid = (%d,%d), want high-word centroid (-65536,-65536)", x, z)
	}
}

func (m *Manager) groupMemberCount() int {
	if m == nil {
		return 0
	}
	return len(m.GroupWaveA) + len(m.GroupWaveB) + len(m.GroupExplore) + len(m.GroupRally) + len(m.GroupRegroupA) + len(m.GroupRegroupB)
}
