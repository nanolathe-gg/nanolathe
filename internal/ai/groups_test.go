package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestUpdateGroupsDoesNotDiscoverUnits(t *testing.T) {
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
		t.Fatalf("ordinary unit creation populated tactical vectors through cleanup: %d members", got)
	}
	// Cleanup is separate from the recovered 30-entry classifier. A second
	// cleanup must not synthesize a member or apply a combat-capability fallback.
	m.updateGroups(w)
	if got := m.groupMemberCount(); got != 0 {
		t.Fatalf("repeated refresh populated tactical vectors: %d members", got)
	}
}

func TestClassifyGroupsMatches00408830Destinations(t *testing.T) {
	defs := []*content.UnitDef{
		{UnitName: "maker", MakesMetal: 1},
		{UnitName: "builder", Builder: true},
		{UnitName: "air", CanFly: true},
		{UnitName: "slope", MaxSlope: 1},
		{UnitName: "flagged", MaxSlope: 0},
		{UnitName: "none"},
	}
	cat := &content.Catalog{Units: make(map[string]*content.UnitDef, len(defs))}
	for _, def := range defs {
		def.CanonicalKey = content.CanonicalKey(def.UnitName)
		cat.Units[def.CanonicalKey] = def
	}
	w := units.New(32, cat)
	makeUnit := func(def *content.UnitDef, flags uint32) pool.Handle {
		h, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		u := w.Unit(h)
		u.Flags = flags
		return h
	}
	resource := makeUnit(defs[0], 0x20|0x20000000)
	construction := makeUnit(defs[1], 0x20)
	explore := makeUnit(defs[2], 0x20)
	regroupB := makeUnit(defs[3], 0x20)
	regroupA := makeUnit(defs[4], 0x20|0x80000000)
	noGroup := makeUnit(defs[5], 0x20)
	foreign, err := w.Create(defs[1], 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(foreign).Flags = 0x20

	m := &Manager{Player: 0}
	m.classifyGroups(w)
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// group-zero gate. A non-capturer takes the B arm; the capture arm is
	// checked below on a separately grouped unit.
	if got := w.Unit(resource).Flags; got&classifierOutputA != 0 || got&classifierOutputB == 0 || got&classifierOutputMask != 0 || got&classifierOutputSet == 0 {
		t.Fatalf("resource classifier status=%#x want B set, A/mask clear, set bit on", got)
	}
	if got, want := m.GroupResource, []pool.Handle{resource}; !sameHandles(got, want) {
		t.Fatalf("resource=%v want %v", got, want)
	}
	if got, want := m.GroupConstruction, []pool.Handle{construction}; !sameHandles(got, want) {
		t.Fatalf("construction=%v want %v", got, want)
	}
	if got, want := m.GroupExplore, []pool.Handle{explore}; !sameHandles(got, want) {
		t.Fatalf("explore=%v want %v", got, want)
	}
	if got, want := m.GroupRegroupB, []pool.Handle{regroupB}; !sameHandles(got, want) {
		t.Fatalf("regroupB=%v want %v", got, want)
	}
	if got, want := m.GroupRegroupA, []pool.Handle{regroupA}; !sameHandles(got, want) {
		t.Fatalf("regroupA=%v want %v", got, want)
	}
	if w.Unit(noGroup).Group != 0 || w.Unit(foreign).Group != 0 {
		t.Fatalf("unclassified/foreign units assigned: noGroup=%d foreign=%d", w.Unit(noGroup).Group, w.Unit(foreign).Group)
	}
	// The classifier is gated by Group==0; a second cadence does not duplicate
	// already inserted handles or reorder the vectors.
	m.classifyGroups(w)
	if len(m.GroupResource) != 1 || len(m.GroupConstruction) != 1 || len(m.GroupExplore) != 1 || len(m.GroupRegroupA) != 1 || len(m.GroupRegroupB) != 1 {
		t.Fatalf("second classification duplicated vectors: resource=%v construction=%v explore=%v regroupA=%v regroupB=%v", m.GroupResource, m.GroupConstruction, m.GroupExplore, m.GroupRegroupA, m.GroupRegroupB)
	}
}

func TestGroupWriterCoversAllRecordsAndSwapDeletes(t *testing.T) {
	m := &Manager{}
	for group := uint8(1); group <= 9; group++ {
		if m.groupVector(group) == nil {
			t.Fatalf("group %d has no destination vector", group)
		}
	}
	u := &units.Unit{Handle: 7, Group: 3}
	m.GroupRegroupA = []pool.Handle{1, u.Handle, 9}
	m.writeGroup(u, 8)
	if u.Group != 8 || !sameHandles(m.GroupRegroupA, []pool.Handle{1, 9}) || !sameHandles(m.GroupExplore, []pool.Handle{u.Handle}) {
		t.Fatalf("writer move group=%d old=%v new=%v", u.Group, m.GroupRegroupA, m.GroupExplore)
	}
	// Removing the same unit is source-only and must not leave a destination
	// duplicate or draw any random state.
	m.writeGroup(u, -1)
	if u.Group != 0 || len(m.GroupExplore) != 0 {
		t.Fatalf("writer removal group=%d explore=%v", u.Group, m.GroupExplore)
	}
}

func sameHandles(a, b []pool.Handle) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// seedAIGroup models an established admission path (classifier, load, or
// control-group assignment) in task fixtures. Task consumers intentionally do
// not rediscover members from the entire world.
func seedAIGroup(m *Manager, u *units.Unit, group uint8) {
	if m == nil || u == nil {
		return
	}
	m.writeGroup(u, int8(group))
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
	return len(m.GroupResource) + len(m.GroupWaveA) + len(m.GroupRegroupA) + len(m.GroupConstruction) + len(m.GroupNull) + len(m.GroupWaveB) + len(m.GroupRegroupB) + len(m.GroupExplore) + len(m.GroupRally)
}
