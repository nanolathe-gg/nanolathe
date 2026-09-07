package session

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestUnitBoxNameMatchesRetailNaming pins the hand-built key against the
// established "u%04x" + suffix naming the writer and R-SAVE-UNIT-01 use, so
// the index lookups below key off exactly the names Other/mover boxes carry
// [08 R-SAVE-02 §6, §8].
func TestUnitBoxNameMatchesRetailNaming(t *testing.T) {
	cases := []struct {
		id     uint16
		suffix string
		want   string
	}{
		{7, "acc", "u0007acc"},
		{0x1234, "mob", "u1234mob"},
		{0xffff, "acc", "uffffacc"},
		{0, "mob", "u0000mob"},
	}
	for _, tc := range cases {
		if got := unitBoxName(tc.id, tc.suffix); got != tc.want {
			t.Fatalf("unitBoxName(%#x, %q) = %q, want %q", tc.id, tc.suffix, got, tc.want)
		}
	}
}

// restoreCoreFixtureUnit is one synthetic unit's identity and record for the
// R06 per-unit pass tests below.
type restoreCoreFixtureUnit struct {
	stableID uint16
	handle   pool.Handle
}

// newRestoreCoreFixture builds a minimal session with n forced-slot mobile
// units, exactly like reserveRetailUnits would leave them, but without the
// filesystem/catalog/mission machinery StageRetailBattle needs. Every field
// RestoreRetailBattleCore does not gate on nil (Vis, Features, World,
// Mission, AI, Build) is left nil; the per-unit later passes under test
// (account, mover, orders) only need Units, Econ, and Clock.
func newRestoreCoreFixture(t *testing.T, n int) (*Session, []restoreCoreFixtureUnit) {
	t.Helper()
	def := &content.UnitDef{
		UnitName:  "fixture",
		MaxDamage: 100,
		BMCode:    true, // mobile: EnsureUnit takes the rectangle-stamp path, not the yard-map one
		Script:    &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}},
	}
	// Player 0's slice runs [1, maxDefs]; size it well past the stable IDs
	// (7, 8, ...) the tests below use so CreateWithForcedSlot never rejects a
	// forced handle as out of the owning player's slice [P0-16 §3.1, §3.3].
	w := units.NewSliced(32, nil)
	econ := &economy.Service{}
	s := &Session{
		Clock: &clock.State{},
		Units: w,
		Econ:  econ,
	}
	var fixtures []restoreCoreFixtureUnit
	for i := 0; i < n; i++ {
		id := uint16(7 + i)
		h, err := w.CreateWithForcedSlot(def, 0, 0, 0, 0, pool.Handle(id))
		if err != nil {
			t.Fatalf("create fixture unit %d: %v", id, err)
		}
		econ.UnitBuckets(h)
		fixtures = append(fixtures, restoreCoreFixtureUnit{stableID: id, handle: h})
	}
	return s, fixtures
}

// unitRecordData returns an all-zero 0xB8 unit body with only the stable ID
// slot and, optionally, the established has-mover word set [08 R-SAVE-02
// §6]. RetailUnitBase reads every other field with its zero default, which is
// a valid restore body — no name parsing happens at this layer
// [08 R-SAVE-02 §6].
func unitRecordData(hasMover bool) []byte {
	data := make([]byte, save.UnitBoxSize)
	if hasMover {
		binary.LittleEndian.PutUint32(data[0x27:], 1)
	}
	return data
}

// accountBoxData builds a 48-byte u%04xacc image whose Energy production word
// (wire offset 0) is the given marker, so a restore that applied the wrong
// one of two duplicate boxes is observable in the final ledger
// [08 R-SAVE-02 §7].
func accountBoxData(energyProduction float32) []byte {
	data := make([]byte, 48)
	binary.LittleEndian.PutUint32(data[0:], math.Float32bits(energyProduction))
	return data
}

func stableUnitMap(fixtures []restoreCoreFixtureUnit) map[uint16]pool.Handle {
	m := make(map[uint16]pool.Handle, len(fixtures))
	for _, f := range fixtures {
		m[f.stableID] = f.handle
	}
	return m
}

// TestRestoreRetailBattleCoreAppliesDuplicateAccountBoxesInEncounterOrder is
// R06: the account pass applies EVERY box matching a unit's name, not just
// the first or last found by some other order, and each apply overwrites the
// ledger — so the box that landed last in Other order must be the one the
// restored ledger reflects, proving both were applied and in the array's
// encounter order [08 R-SAVE-02 §6].
func TestRestoreRetailBattleCoreAppliesDuplicateAccountBoxesInEncounterOrder(t *testing.T) {
	s, fixtures := newRestoreCoreFixture(t, 1)
	u := fixtures[0]
	stage := &RetailBattleStage{
		Session:    s,
		StableUnit: stableUnitMap(fixtures),
		Image: &save.BattleImage{
			Units: save.UnitImage{
				Records: []save.UnitRecord{{StableID: u.stableID, Data: unitRecordData(false)}},
				Other: []save.RawBox{
					{Name: "u0007acc", Data: accountBoxData(1)},
					{Name: "u0007acc", Data: accountBoxData(2)},
				},
			},
		},
	}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatalf("RestoreRetailBattleCore: %v", err)
	}
	buckets := s.Econ.UnitBuckets(u.handle)
	if buckets == nil {
		t.Fatal("restored unit carries no economy buckets")
	}
	if got := buckets[economy.Energy].Production; got != 2 {
		t.Fatalf("energy production = %v, want 2 (the second/last-encountered box, proving both applied in order)", got)
	}
}

// TestRestoreRetailBattleCoreDuplicateMoverBoxesError is R06: two u%04xmob
// boxes for the same unit must still be rejected, not silently take
// whichever the index happens to visit first [08 R-SAVE-02 §6, §8].
func TestRestoreRetailBattleCoreDuplicateMoverBoxesError(t *testing.T) {
	s, fixtures := newRestoreCoreFixture(t, 1)
	u := fixtures[0]
	s.Movement = movement.NewSystem(nil, movement.Profile{}, movement.NewOccupancyGrid())
	stage := &RetailBattleStage{
		Session:    s,
		StableUnit: stableUnitMap(fixtures),
		Image: &save.BattleImage{
			Units: save.UnitImage{
				Records: []save.UnitRecord{{StableID: u.stableID, Data: unitRecordData(true)}},
				Other: []save.RawBox{
					{Name: "u0007mob", Data: make([]byte, 35)},
					{Name: "u0007mob", Data: make([]byte, 35)},
				},
			},
		},
	}
	err := RestoreRetailBattleCore(stage)
	if err == nil {
		t.Fatal("duplicate mover boxes were accepted")
	}
	if !strings.Contains(err.Error(), "duplicate mover boxes") {
		t.Fatalf("error = %q, want the duplicate-mover-boxes diagnostic", err.Error())
	}
}

// TestRestoreRetailBattleCoreMissingMoverBoxErrors is R06's other half: a
// HasMover unit with no matching box must still fail, not silently skip the
// mover pass [08 R-SAVE-02 §6, §8].
func TestRestoreRetailBattleCoreMissingMoverBoxErrors(t *testing.T) {
	s, fixtures := newRestoreCoreFixture(t, 1)
	u := fixtures[0]
	s.Movement = movement.NewSystem(nil, movement.Profile{}, movement.NewOccupancyGrid())
	stage := &RetailBattleStage{
		Session:    s,
		StableUnit: stableUnitMap(fixtures),
		Image: &save.BattleImage{
			Units: save.UnitImage{
				Records: []save.UnitRecord{{StableID: u.stableID, Data: unitRecordData(true)}},
			},
		},
	}
	err := RestoreRetailBattleCore(stage)
	if err == nil {
		t.Fatal("missing mover box with HasMover set was accepted")
	}
	if !strings.Contains(err.Error(), "no mover box") {
		t.Fatalf("error = %q, want the missing-mover-box diagnostic", err.Error())
	}
}

// TestRestoreRetailBattleCoreGroupsOrdersPerUnit is R06: orders interleaved
// across units in the saved Orders slice must still land on the correct
// unit's queue and only that unit's, in original sequence, never mixed by a
// grouping bug. The records are queued as secondary (Secondary: true) so the
// restore's front-queue pump — which needs an injected simulation RNG this
// fixture does not carry — never runs; that pump is unrelated to the
// per-unit grouping this test locks [08 R-SAVE-02 §6].
func TestRestoreRetailBattleCoreGroupsOrdersPerUnit(t *testing.T) {
	s, fixtures := newRestoreCoreFixture(t, 2)
	unitA, unitB := fixtures[0], fixtures[1]
	stopID := orders.Lookup("Stop")
	if stopID == 0 {
		t.Fatal("fixture requires the established Stop descriptor")
	}
	order := func(parent uint16, seq uint32, deadline int32) save.OrderRecord {
		main := make([]byte, save.OrderBoxSize)
		binary.LittleEndian.PutUint16(main[0:], parent)
		main[8] = byte(stopID)
		binary.LittleEndian.PutUint32(main[0x0E:], uint32(deadline))
		return save.OrderRecord{ParentStableID: parent, Sequence: seq, Secondary: true, Main: main}
	}
	// Interleaved in the saved array: B, A, B, A — the shape R06 flags as
	// unsafe for a scan keyed only by array position.
	stage := &RetailBattleStage{
		Session:    s,
		StableUnit: stableUnitMap(fixtures),
		Image: &save.BattleImage{
			Units: save.UnitImage{
				Records: []save.UnitRecord{
					{StableID: unitA.stableID, Data: unitRecordData(false)},
					{StableID: unitB.stableID, Data: unitRecordData(false)},
				},
				Orders: []save.OrderRecord{
					order(unitB.stableID, 0, 100),
					order(unitA.stableID, 0, 200),
					order(unitB.stableID, 1, 101),
					order(unitA.stableID, 1, 201),
				},
			},
		},
	}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatalf("RestoreRetailBattleCore: %v", err)
	}
	checkGroup := func(name string, h pool.Handle, wantDeadlines []int32) {
		t.Helper()
		u := s.Units.Unit(h)
		q := orders.QueueOfUnit(u)
		if q == nil {
			t.Fatalf("%s: restore built no queue", name)
		}
		if q.LenPrimary() != 0 {
			t.Fatalf("%s: primary segment = %d, want 0 (all orders are secondary)", name, q.LenPrimary())
		}
		got := q.Secondary()
		if len(got) != len(wantDeadlines) {
			t.Fatalf("%s: secondary segment = %d records, want %d", name, len(got), len(wantDeadlines))
		}
		for i, want := range wantDeadlines {
			if got[i].Deadline != want {
				t.Fatalf("%s: secondary[%d].Deadline = %d, want %d (sequence order within the unit's own group)", name, i, got[i].Deadline, want)
			}
		}
	}
	checkGroup("unit A", unitA.handle, []int32{200, 201})
	checkGroup("unit B", unitB.handle, []int32{100, 101})
}

func TestRestoreRetailBattleCoreRejectsIncompleteStage(t *testing.T) {
	if err := RestoreRetailBattleCore(nil); err == nil {
		t.Fatal("nil staged restore accepted")
	}
}

func TestRestoreRetailFeaturesPrevalidatesBeforePlacement(t *testing.T) {
	const width, height = 4, 4
	attrs := make([]formats.TNTAttribute, width*height)
	for i := range attrs {
		attrs[i].Feature = world.PlotFeatureNone
	}
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"}, FootprintX: 1, FootprintZ: 1}
	terrain := &world.Terrain{CellW: width, CellH: height, Plot: world.ExpandPlot(attrs, width, height), FeatureDefs: []*content.FeatureDef{def}}
	svc := features.NewService(terrain, nil, nil, nil)
	img := save.FeatureImage{Normal: []save.FeatureRecord{
		{X: 1, Z: 1, TypeID: 0, Data: make([]byte, 8)},
		{X: 2, Z: 2, TypeID: 1, Data: make([]byte, 8)}, // unresolved after the first valid row
	}}
	if err := restoreRetailFeatures(svc, nil, img); err == nil {
		t.Fatal("feature restore accepted an unresolved definition")
	}
	if len(svc.Instances()) != 0 || terrain.PlotAt(1, 1).Occupied() {
		t.Fatal("feature restore mutated placement before validation completed")
	}
}

// The Mapping box is the explored-memory word grid verbatim, exact-size gated
// like the two Plotmap boxes [08 R-SAVE-02 §12].
func TestApplyRetailMappingWordsIsExactSizeGated(t *testing.T) {
	words := make([]uint16, 3)
	for _, data := range [][]byte{nil, make([]byte, 5), make([]byte, 8)} {
		if err := applyRetailMappingWords(words, data); err == nil {
			t.Fatalf("Mapping box of %d bytes accepted for %d words", len(data), len(words))
		}
	}
	for i := range words {
		words[i] = 0x03ff // the fill a history-disabled rebuild leaves behind
	}
	if err := applyRetailMappingWords(words, []byte{0x01, 0x00, 0x00, 0x02, 0xff, 0x03}); err != nil {
		t.Fatalf("apply Mapping words: %v", err)
	}
	for i, want := range []uint16{0x0001, 0x0200, 0x03ff} {
		if words[i] != want {
			t.Fatalf("word %d = %#04x, want %#04x", i, words[i], want)
		}
	}
}

// An absent Meteor account decodes as nine zeros, which "silently disables and
// de-activates the shower rather than failing the load"
// [08 "Account inventory"]. The four coordinates are sixteen-bit globals the
// reader truncates back to sixteen bits.
func TestRestoreRetailMeteorAppliesSavedScalars(t *testing.T) {
	s := &Session{}
	s.Meteor.Active = true
	s.Meteor.NextStrike = 4242
	restoreRetailMeteor(s, save.MeteorScalars{})
	if !s.Meteor.Initialized {
		t.Fatal("restore did not install the authored meteor parameters")
	}
	if s.Meteor.Enabled || s.Meteor.Active || s.Meteor.NextStrike != 0 || s.Meteor.StrikeEnds != 0 || s.Meteor.NextHit != 0 {
		t.Fatalf("missing Meteor box did not take the zero default: %+v", s.Meteor)
	}

	s = &Session{}
	restoreRetailMeteor(s, save.MeteorScalars{
		Enabled: 1, Active: 1, NextStrikeTime: 9000, TimeStrikeEnds: 8700, NextHitTime: 8650,
		OriginX: -7, OriginZ: 33, TargetX: int32(int16(-32768)), TargetZ: 44,
	})
	if !s.Meteor.Enabled || !s.Meteor.Active {
		t.Fatalf("saved enable/active lost: %+v", s.Meteor)
	}
	if s.Meteor.NextStrike != 9000 || s.Meteor.StrikeEnds != 8700 || s.Meteor.NextHit != 8650 {
		t.Fatalf("saved deadlines lost: %+v", s.Meteor)
	}
	if s.Meteor.OriginX != -7 || s.Meteor.OriginZ != 33 || s.Meteor.TargetX != -32768 || s.Meteor.TargetZ != 44 {
		t.Fatalf("saved geometry lost: %+v", s.Meteor)
	}
}
