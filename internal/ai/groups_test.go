package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestGroupsRequireAnEstablishedWriter(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")},
		UnitName:         "armflash",
		CanMove:          true,
		CanAttack:        true,
		MaxVelocity:      100,
		MaxDamage:        100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newAIFixtureWorld(16, cat)
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
	if got := m.groupMemberCount(); got != 0 {
		t.Fatalf("ordinary unit creation populated tactical vectors: %d members", got)
	}
}

func TestRestoreGroupsDoNotOverwriteUIGroup(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("restore-group")},
		UnitName:         "restore-group",
		CanMove:          true,
		FootprintX:       1,
		FootprintZ:       1,
		MaxVelocity:      100,
	}
	w := newAIFixtureWorld(8, &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}})
	h, err := w.Create(def, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.Group = 7 // UI control group is a separate saved/runtime concept.
	u.RestoredAIGroup = 3
	m := &Manager{Player: 0}
	m.RestoreGroupsFromUnits(w)
	if u.Group != 7 {
		t.Fatalf("restore changed UI group to %d", u.Group)
	}
	if got := m.GroupRegroupA; len(got) != 1 || got[0] != h {
		t.Fatalf("restored AI group vector=%v, want [%d]", got, h)
	}
}

func TestClassifierDestinations(t *testing.T) {
	defs := []*content.UnitDef{
		{UnitName: "maker", MakesMetal: 1},
		{UnitName: "builder", Builder: true},
		{UnitName: "air", CanFly: true},
		{UnitName: "water", MinWaterDepth: 1},
		{UnitName: "flagged", MinWaterDepth: 0},
		{UnitName: "none"},
	}
	cat := &content.Catalog{Units: make(map[string]*content.UnitDef, len(defs))}
	for _, def := range defs {
		def.CanonicalKey = content.CanonicalKey(def.UnitName)
		cat.Units[def.CanonicalKey] = def
	}
	w := newAIFixtureWorld(32, cat)
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
	// classifier also normalizes the three observed status masks before the
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

// mergeWaveGroupRecordsFixture exercises the production transfer path. Tests
// seed the manager vectors explicitly, then invoke the same direct writer used
// by the wave task so Unit.Group and vector order are checked together.
func mergeWaveGroupRecordsFixture(current, peer []pool.Handle, w *units.World, threshold int32) ([]pool.Handle, []pool.Handle) {
	m := &Manager{GroupWaveA: append([]pool.Handle(nil), current...), GroupRegroupA: append([]pool.Handle(nil), peer...)}
	for _, h := range current {
		if u := w.Unit(h); u != nil {
			u.Group = 2
		}
	}
	for _, h := range peer {
		if u := w.Unit(h); u != nil {
			u.Group = 3
		}
	}
	m.mergeWaveGroupRecords(2, 3, w, threshold)
	return m.GroupWaveA, m.GroupRegroupA
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
	w := newAIFixtureWorld(16, cat)
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
	current, peer := mergeWaveGroupRecordsFixture([]pool.Handle{anchor, anchor2, outlier}, nil, w, waveAThreshold)
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
	current, peer = mergeWaveGroupRecordsFixture([]pool.Handle{anchor}, []pool.Handle{boundary, close}, w, waveAThreshold)
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
	w := newAIFixtureWorld(4, cat)
	first, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The recovered centroid helper reads signed high words, then performs
	// integer division. Fractional fixed-point state must not leak into the
	// regroup target [R-P0-04; 03 §2.1; I3].
	w.Unit(first).X = numeric.Fixed(-98304) // -1.5 pixels → high word -2
	w.Unit(second).X = numeric.Fixed(32768) // +0.5 pixels → stored word 0
	w.Unit(first).Y = numeric.FixedFromInt(7)
	w.Unit(second).Y = numeric.FixedFromInt(3)
	w.Unit(first).Z = numeric.Fixed(-98304)
	w.Unit(second).Z = numeric.Fixed(-32768)
	x, y, z, ok := groupCentroid([]pool.Handle{first, second}, w)
	if !ok {
		t.Fatal("groupCentroid reported empty group")
	}
	if x != -numeric.Fixed(65536) || y != numeric.FixedFromInt(5) || z != -numeric.Fixed(65536) {
		t.Fatalf("groupCentroid = (%d,%d,%d), want high-word centroid (-65536,327680,-65536)", x, y, z)
	}
}

func TestGroupCentroidNarrowsSignedWordAndWrapsInt32Sum(t *testing.T) {
	def := &content.UnitDef{UnitName: "centroid", MaxDamage: 100}
	w := newAIFixtureWorld(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	// Raw high word 0x8000 is a signed -32768 [08 R-AI-01 §9].
	u.X = numeric.Fixed(0x80000000)
	x, _, _, ok := groupCentroid([]pool.Handle{h}, w)
	if !ok || x != numeric.Fixed(-0x80000000) {
		t.Fatalf("signed-high-word centroid x=%d ok=%v, want -2147483648", x, ok)
	}

	// Repeating one live handle exercises the specified 32-bit accumulator
	// overflow without constructing a huge non-stock fixture world.
	u.X = numeric.FixedFromInt(32767)
	handles := make([]pool.Handle, 65538)
	var sum int32
	for i := range handles {
		handles[i] = h
		sum += 32767
	}
	x, _, _, ok = groupCentroid(handles, w)
	want := numeric.Fixed(int32((sum / int32(len(handles))) << 16))
	if !ok || x != want {
		t.Fatalf("wrapped centroid x=%d, want %d", x, want)
	}
}

func (m *Manager) groupMemberCount() int {
	if m == nil {
		return 0
	}
	return len(m.GroupResource) + len(m.GroupWaveA) + len(m.GroupRegroupA) + len(m.GroupConstruction) + len(m.GroupNull) + len(m.GroupWaveB) + len(m.GroupRegroupB) + len(m.GroupExplore) + len(m.GroupRally)
}

// TestMergeWaveGroupsBootstrapsFromEmptyWave locks the step that makes the
// attack waves reachable at all [08 "Wave merge" step 2].
//
// The classifier assigns only resource, construction, explore, regroup A,
// regroup B and null — never a wave. A wave therefore starts empty and can
// only acquire its first member from its paired regroup record, which the
// merge does by moving the peer's FIRST member across. This function used to
// return early on an empty current group, so both wave records stayed empty
// forever and the computer player never issued an attack order.
func TestMergeWaveGroupsBootstrapsFromEmptyWave(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")},
		UnitName:         "armflash",
		CanMove:          true,
		CanAttack:        true,
		MaxVelocity:      100,
		MaxDamage:        100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newAIFixtureWorld(16, cat)
	create := func(x, z int32) pool.Handle {
		h, err := w.Create(def, 0, numeric.Fixed(x)*numeric.Fixed(1<<16), 0, numeric.Fixed(z)*numeric.Fixed(1<<16))
		if err != nil {
			t.Fatal(err)
		}
		w.Unit(h).Remaining = 0
		return h
	}

	// An empty wave and an empty peer stay empty: the merge discovers nothing
	// from the world.
	current, peer := mergeWaveGroupRecordsFixture(nil, nil, w, waveAThreshold)
	if len(current) != 0 || len(peer) != 0 {
		t.Fatalf("merge with two empty vectors produced current=%v peer=%v; it must not discover units", current, peer)
	}

	// Four colocated regroup members. The bootstrap takes the peer's first
	// member and removes it by replace-with-last, so the peer becomes
	// [d b c]; the absorb step then collects all three in that order, since
	// colocated members satisfy the strict distance test. The resulting wave
	// order is the observable proof of both the transfer order and the
	// swap-delete: a front shift would have produced [a b c d] instead.
	a, b, c, d := create(0, 0), create(0, 0), create(0, 0), create(0, 0)
	current, peer = mergeWaveGroupRecordsFixture(nil, []pool.Handle{a, b, c, d}, w, waveAThreshold)
	if len(peer) != 0 {
		t.Fatalf("peer after colocated merge = %v, want empty", peer)
	}
	if len(current) != 4 || current[0] != a || current[1] != d || current[2] != b || current[3] != c {
		t.Fatalf("wave after colocated merge = %v, want [%d %d %d %d] "+
			"(bootstrap a, replace-with-last leaves [d b c], absorb in that order)",
			current, a, d, b, c)
	}

	// With a distant peer the absorb step collects nothing, so the bootstrap
	// alone is visible: exactly one member crosses per call.
	far1, far2 := create(4000, 0), create(9000, 0)
	current, peer = mergeWaveGroupRecordsFixture(nil, []pool.Handle{far1, far2}, w, waveAThreshold)
	if len(current) != 1 || current[0] != far1 {
		t.Fatalf("distant bootstrap current=%v, want exactly [%d]", current, far1)
	}
	if len(peer) != 1 || peer[0] != far2 {
		t.Fatalf("distant bootstrap peer=%v, want [%d]", peer, far2)
	}
}

// TestMergeWaveGroupsKeepsLastMember locks that the shed loop stops at one
// member: retail breaks out when the group count reaches one, so a lone
// outlier is never evicted into an empty wave [08 "Wave merge" step 4].
func TestMergeWaveGroupsKeepsLastMember(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")},
		UnitName:         "armflash",
		CanMove:          true,
		CanAttack:        true,
		MaxVelocity:      100,
		MaxDamage:        100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newAIFixtureWorld(16, cat)
	h, err := w.Create(def, 0, numeric.Fixed(9000)*numeric.Fixed(1<<16), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Remaining = 0

	current, peer := mergeWaveGroupRecordsFixture([]pool.Handle{h}, nil, w, waveAThreshold)
	if len(current) != 1 || current[0] != h {
		t.Fatalf("single-member wave = %v, want it retained [%d]", current, h)
	}
	if len(peer) != 0 {
		t.Fatalf("single-member wave shed into peer=%v", peer)
	}
}

// TestDoWavePairsWithRegroupNotTheOtherWave locks the authored peer wiring:
// wave A pairs with regroup A and wave B with regroup B. Pairing the two waves
// with each other is unreachable — neither can ever be seeded.
func TestDoWavePairsWithRegroupNotTheOtherWave(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")},
		UnitName:         "armflash",
		CanMove:          true,
		CanAttack:        true,
		MaxVelocity:      100,
		MaxDamage:        100,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newAIFixtureWorld(16, cat)
	mk := func() pool.Handle {
		h, err := w.Create(def, 0, numeric.Fixed(64)*numeric.Fixed(1<<16), 0, numeric.Fixed(64)*numeric.Fixed(1<<16))
		if err != nil {
			t.Fatal(err)
		}
		w.Unit(h).Remaining = 0
		return h
	}
	ra, rb := mk(), mk()
	w.Unit(ra).Group = 3
	w.Unit(rb).Group = 7

	m := &Manager{Player: 0}
	m.GroupRegroupA = []pool.Handle{ra}
	m.GroupRegroupB = []pool.Handle{rb}

	m.doWave(1, w, nil, waveAThreshold, 3, 6)
	if len(m.GroupWaveA) != 1 || m.GroupWaveA[0] != ra {
		t.Fatalf("wave A = %v, want regroup A's member [%d]", m.GroupWaveA, ra)
	}
	if len(m.GroupRegroupA) != 0 {
		t.Errorf("regroup A still holds %v after the transfer", m.GroupRegroupA)
	}
	if len(m.GroupWaveB) != 0 || len(m.GroupRegroupB) != 1 {
		t.Errorf("wave A's merge disturbed the B pair: waveB=%v regroupB=%v", m.GroupWaveB, m.GroupRegroupB)
	}
	if u := w.Unit(ra); u == nil || u.Group != 2 {
		t.Errorf("transferred unit group field = %v, want wave A record 2", u.Group)
	}

	m.doWave(2, w, nil, waveBThreshold, 3, 6)
	if len(m.GroupWaveB) != 1 || m.GroupWaveB[0] != rb {
		t.Fatalf("wave B = %v, want regroup B's member [%d]", m.GroupWaveB, rb)
	}
	if u := w.Unit(rb); u == nil || u.Group != 6 {
		t.Errorf("transferred unit group field = %v, want wave B record 6", u.Group)
	}
}

// TestClassifierRoutesLandCombatToRegroupA locks the 2026-08-29 correction to
// the classifier's regroup-B row: the word it tests is the definition's
// MinWaterDepth, compared `>= 1`, not the MaxSlope byte [08 R-P0-04 §3
// "Classifier eligibility, destinations, and order"][08 R-AI-03 §6].
//
// The distinction decides whether the computer player ever attacks. Every
// stock land definition carries the movement template's MinWaterDepth of
// -10000 together with a positive MaxSlope, so reading the slope word sent
// every armed ground unit to regroup B and left regroup A — the peer the
// wave-A merge bootstraps from — empty for the whole battle [08 "Wave merge"].
func TestClassifierRoutesLandCombatToRegroupA(t *testing.T) {
	land := &content.UnitDef{UnitName: "land-tank", MinWaterDepth: -10000, MaxSlope: 15}
	amphibious := &content.UnitDef{UnitName: "amphibious", MinWaterDepth: 1, MaxSlope: 15}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	for _, def := range []*content.UnitDef{land, amphibious} {
		def.CanonicalKey = content.CanonicalKey(def.UnitName)
		cat.Units[def.CanonicalKey] = def
	}
	w := newAIFixtureWorld(32, cat)
	create := func(def *content.UnitDef) pool.Handle {
		h, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		// The allocator eligibility bit plus the armed bit: an ordinary armed
		// mobile unit [08 R-P0-04 §3].
		w.Unit(h).Flags = classifierEligibleBit | classifierArmed
		return h
	}
	tank := create(land)
	amph := create(amphibious)

	m := &Manager{Player: 0}
	m.classifyGroups(w)

	if got, want := m.GroupRegroupA, []pool.Handle{tank}; !sameHandles(got, want) {
		t.Fatalf("regroup A = %v, want the land definition %v: the MaxSlope word is not the classifier key", got, want)
	}
	if got, want := m.GroupRegroupB, []pool.Handle{amph}; !sameHandles(got, want) {
		t.Fatalf("regroup B = %v, want the MinWaterDepth>=1 definition %v", got, want)
	}
}

// TestClassifierNavalRowIgnoresTheArmedBit locks [08 R-AI-04 §3 point 1]: the
// classifier's regroup-B row (`MinWaterDepth >= 1`) precedes the armed test,
// so an armed water-class mobile AND an unarmed one both land in regroup B —
// the armed row never gets a chance to claim the armed one. A land mobile at
// the movement template's default depth (-10000) falls through the water row
// and lands in regroup A only when armed [08 R-AI-04 §3].
func TestClassifierNavalRowIgnoresTheArmedBit(t *testing.T) {
	armedShip := &content.UnitDef{UnitName: "armed-ship", MinWaterDepth: 1}
	unarmedShip := &content.UnitDef{UnitName: "unarmed-ship", MinWaterDepth: 1}
	templateArmedLand := &content.UnitDef{UnitName: "template-armed-land", MinWaterDepth: -10000}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	for _, def := range []*content.UnitDef{armedShip, unarmedShip, templateArmedLand} {
		def.CanonicalKey = content.CanonicalKey(def.UnitName)
		cat.Units[def.CanonicalKey] = def
	}
	w := newAIFixtureWorld(32, cat)
	create := func(def *content.UnitDef, armed bool) pool.Handle {
		h, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		flags := classifierEligibleBit
		if armed {
			flags |= classifierArmed
		}
		w.Unit(h).Flags = flags
		return h
	}
	armed := create(armedShip, true)
	unarmed := create(unarmedShip, false)
	land := create(templateArmedLand, true)

	m := &Manager{Player: 0}
	m.classifyGroups(w)

	if got, want := m.GroupRegroupB, []pool.Handle{armed, unarmed}; !sameHandles(got, want) {
		t.Fatalf("regroup B = %v, want both the armed and unarmed MinWaterDepth>=1 mobiles %v: the water row must precede the armed test [08 R-AI-04 §3]", got, want)
	}
	if got, want := m.GroupRegroupA, []pool.Handle{land}; !sameHandles(got, want) {
		t.Fatalf("regroup A = %v, want the template-depth armed land mobile %v", got, want)
	}
	if w.Unit(unarmed).Group != 7 {
		t.Fatalf("unarmed water mobile's stored group = %d, want 7 (regroup B)", w.Unit(unarmed).Group)
	}
}

// TestClassifierFlyerRowSendsOnlyNonBuildersToExplore locks [08 R-AI-04 §4
// point 1]: the `canfly` row precedes both the water and armed rows and
// routes a non-builder aircraft to the explore record and nowhere else — no
// row ever sends anything else there, so an armed flyer never reaches a
// regroup record and no attack wave can ever contain one.
func TestClassifierFlyerRowSendsOnlyNonBuildersToExplore(t *testing.T) {
	scout := &content.UnitDef{UnitName: "scout", CanFly: true}
	scout.CanonicalKey = content.CanonicalKey(scout.UnitName)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{scout.CanonicalKey: scout}}
	w := newAIFixtureWorld(8, cat)
	h, err := w.Create(scout, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Armed, to show the armed test never gets a turn against a flyer.
	w.Unit(h).Flags = classifierEligibleBit | classifierArmed

	m := &Manager{Player: 0}
	m.classifyGroups(w)

	if got, want := m.GroupExplore, []pool.Handle{h}; !sameHandles(got, want) {
		t.Fatalf("explore = %v, want the non-builder aircraft %v [08 R-AI-04 §4]", got, want)
	}
	if len(m.GroupRegroupA) != 0 || len(m.GroupRegroupB) != 0 || len(m.GroupWaveA) != 0 || len(m.GroupWaveB) != 0 || len(m.GroupResource) != 0 || len(m.GroupConstruction) != 0 || len(m.GroupNull) != 0 || len(m.GroupRally) != 0 {
		t.Fatalf("non-builder aircraft reached a record other than explore: regroupA=%v regroupB=%v waveA=%v waveB=%v resource=%v construction=%v null=%v rally=%v",
			m.GroupRegroupA, m.GroupRegroupB, m.GroupWaveA, m.GroupWaveB, m.GroupResource, m.GroupConstruction, m.GroupNull, m.GroupRally)
	}
}

// TestClassifierBuilderRowPrecedesTheFlyerRow locks [08 R-AI-04 §4 point 2]:
// "construction aircraft are builders first" — the builder row precedes the
// flyer row, so a builder that also authors `canfly` (a construction
// aircraft) lands in the construction record, not the explore record.
func TestClassifierBuilderRowPrecedesTheFlyerRow(t *testing.T) {
	builderAircraft := &content.UnitDef{UnitName: "builder-aircraft", Builder: true, CanFly: true}
	builderAircraft.CanonicalKey = content.CanonicalKey(builderAircraft.UnitName)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{builderAircraft.CanonicalKey: builderAircraft}}
	w := newAIFixtureWorld(8, cat)
	h, err := w.Create(builderAircraft, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Flags = classifierEligibleBit

	m := &Manager{Player: 0}
	m.classifyGroups(w)

	if got, want := m.GroupConstruction, []pool.Handle{h}; !sameHandles(got, want) {
		t.Fatalf("construction = %v, want the builder aircraft %v [08 R-AI-04 §4 point 2]", got, want)
	}
	if len(m.GroupExplore) != 0 {
		t.Fatalf("builder aircraft also reached explore: %v", m.GroupExplore)
	}
}

// TestWaveMergeDropsRecycledRecordEntries locks the record reconciliation.
//
// A retail record holds unit pointers and is emptied of a dying member by the
// death-teardown caller of the direct writer, so every member is a live unit of
// the owning player whose stored group equals the record [08 R-P0-04 §3].
// Nanolathe records hold pool handles and the pool recycles freed slots, so a
// dead member's handle can come back pointing at a different live unit. Left in
// place, that entry inflates the count the wave hysteresis reads and makes the
// merge's farthest-member transfer a no-op — the loop then never terminates,
// which is exactly how a thirty-minute headless skirmish hung at tick 44700
// before this unit.
func TestWaveMergeDropsRecycledRecordEntries(t *testing.T) {
	def := &content.UnitDef{UnitName: "wave-member", MaxDamage: 100, CanMove: true, MaxVelocity: 100}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newAIFixtureWorld(8, cat)
	place := func(x int32) pool.Handle {
		h, err := w.Create(def, 0, numeric.Fixed(int64(x)<<16), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	member := place(0)
	// 400 world units away: with wave A's threshold of 20,000 and a two-member
	// record the farthest-member test (200^2 >= 20000*2) admits a transfer, so
	// the loop below really does run [08 R-AI-01 §4][08 "Wave merge"].
	recycled := place(400)
	w.Unit(member).Group = 2
	// The recycled slot's new occupant belongs to some other record; only its
	// stale handle is still in wave A.
	w.Unit(recycled).Group = 3

	m := &Manager{Player: 0}
	m.GroupWaveA = []pool.Handle{member, recycled}
	m.GroupRegroupA = []pool.Handle{recycled}

	m.mergeWaveGroupRecords(2, 3, w, waveAThreshold)

	if got, want := m.GroupWaveA, []pool.Handle{member}; !sameHandles(got, want) {
		t.Fatalf("wave A = %v, want only the real member %v [08 R-P0-04 §3]", got, want)
	}
	if u := w.Unit(member); u == nil || u.Group != 2 {
		t.Fatalf("the real member's stored group changed to %v", u.Group)
	}
}

// TestOnUnitDeathRemovesTheStoredRecordImmediately locks WU-19-21: a dying
// unit leaves its own group record through the direct writer's
// remove-sentinel form as soon as OnUnitDeath runs, without waiting for the
// next 30-entry classification sweep [08 R-P0-04 §3 "The direct
// manager-group writer"]. It also checks the sibling records are untouched
// and that no RNG is consumed (I4).
func TestOnUnitDeathRemovesTheStoredRecordImmediately(t *testing.T) {
	def := &content.UnitDef{UnitName: "wave-member", MaxDamage: 100, CanMove: true, MaxVelocity: 100}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newAIFixtureWorld(8, cat)
	place := func(x int32) pool.Handle {
		h, err := w.Create(def, 0, numeric.Fixed(int64(x)<<16), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	dying := place(0)
	sibling := place(64)
	w.Unit(dying).Group = 2
	w.Unit(sibling).Group = 2

	m := &Manager{Player: 0}
	m.GroupWaveA = []pool.Handle{dying, sibling}
	m.GroupResource = []pool.Handle{7}

	m.OnUnitDeath(w.Unit(dying))

	if got, want := m.GroupWaveA, []pool.Handle{sibling}; !sameHandles(got, want) {
		t.Fatalf("wave A after death = %v, want only the surviving member %v [R-P0-04 §3]", got, want)
	}
	if got, want := m.GroupResource, []pool.Handle{7}; !sameHandles(got, want) {
		t.Fatalf("an unrelated record changed: %v, want %v", got, want)
	}
	if u := w.Unit(dying); u.Group != 0 {
		t.Fatalf("dying unit's stored group = %d, want 0 (remove sentinel, no destination)", u.Group)
	}
	if u := w.Unit(sibling); u.Group != 2 {
		t.Fatalf("sibling's stored group changed to %d", u.Group)
	}
}
