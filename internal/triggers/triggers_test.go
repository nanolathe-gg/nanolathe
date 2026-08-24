package triggers

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestVTableCompleteness(t *testing.T) {
	// All 18 trigger vtables present with their record sizes [08 "Trigger object"] [GAP T10] [C15].
	if len(VTables) != KindCount {
		t.Fatalf("VTables length %d want %d", len(VTables), KindCount)
	}
	if KindCount != 18 {
		t.Fatalf("KindCount %d want 18 [C15]", KindCount)
	}
	seen := make(map[Kind]bool)
	seenName := make(map[string]bool)
	for i, vt := range VTables {
		if Kind(i) != vt.Kind {
			t.Fatalf("VTables[%d] Kind %d want %d", i, vt.Kind, i)
		}
		if seen[vt.Kind] {
			t.Fatalf("duplicate Kind %d", vt.Kind)
		}
		seen[vt.Kind] = true
		if vt.Name != vt.Kind.String() {
			t.Fatalf("VTable %d Name %q want %q", vt.Kind, vt.Name, vt.Kind.String())
		}
		if seenName[vt.Name] {
			t.Fatalf("duplicate Name %q", vt.Name)
		}
		seenName[vt.Name] = true
		if vt.Slots != 8 {
			t.Fatalf("VTable %q Slots %d want 8 [notes/campaign/00_missions.md §5.3]", vt.Name, vt.Slots)
		}
		if vt.RecordSize != vt.Kind.RecordSize() {
			t.Fatalf("VTable %q RecordSize %d want %d", vt.Name, vt.RecordSize, vt.Kind.RecordSize())
		}
		// Documented buckets [GAP T10] [notes/campaign/00_missions.md §5.2].
		switch vt.RecordSize {
		case 0x0C, 0x10, 0x14, 0x30, 0x32, 0x36, 0x40:
		default:
			t.Fatalf("VTable %q RecordSize %d not in documented buckets {0xC,0x10,0x14,0x30,0x32,0x36,0x40}", vt.Name, vt.RecordSize)
		}
	}
	// Ensure every Kind is covered.
	for k := 0; k < KindCount; k++ {
		if !seen[Kind(k)] {
			t.Fatalf("missing Kind %d %q", k, Kind(k).String())
		}
		if Kind(k).RecordSize() == 0 {
			t.Fatalf("Kind %q RecordSize 0", Kind(k).String())
		}
	}
	// Spot-check documented sizes per bucket [GAP T10].
	checks := []struct {
		kind Kind
		size int
	}{
		{KindKillEnemyCommander, 0x0C},
		{KindDestroyAllUnits, 0x0C},
		{KindMoveUnitToRadius, 0x40},
		{KindVictoryTimerRunsOut, 0x10},
		{KindDeathTimerRunsOut, 0x10},
		{KindUnitTypePassesX, 0x14},
		{KindAnyUnitPassesZ, 0x14},
		{KindBuildUnitType, 0x30},
		{KindKillUnitType, 0x32},
		{KindAllUnitsKilledOfType, 0x36},
	}
	for _, c := range checks {
		if got := c.kind.RecordSize(); got != c.size {
			t.Fatalf("Kind %q RecordSize %d want %d", c.kind.String(), got, c.size)
		}
	}
	// Verify 18 distinct names as per C15 lists.
	expectedNames := map[string]bool{
		"KillEnemyCommander":   true,
		"DestroyAllUnits":      true,
		"KillAllMobileUnits":   true,
		"BuildUnitType":        true,
		"CaptureUnitType":      true,
		"KillAllOfType":        true,
		"KillUnitType":         true,
		"MoveUnitToRadius":     true,
		"UnitTypePassesX":      true,
		"UnitTypePassesZ":      true,
		"VictoryTimerRunsOut":  true,
		"CommanderKilled":      true,
		"AllUnitsKilled":       true,
		"AllUnitsKilledOfType": true,
		"UnitTypeKilled":       true,
		"DeathTimerRunsOut":    true,
		"AnyUnitPassesX":       true,
		"AnyUnitPassesZ":       true,
	}
	if len(expectedNames) != 18 {
		t.Fatalf("expected map size wrong")
	}
	for n := range expectedNames {
		if _, ok := KindByName[nToLower(n)]; !ok {
			t.Fatalf("KindByName missing %q [C15]", n)
		}
	}
}

func nToLower(s string) string {
	// helper for test; equivalent to strings.ToLower
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func TestBoundaryTolerance(t *testing.T) {
	// Boundary conditions compare signed world coordinate vs threshold,
	// satisfied when absolute difference is below three world units (±2)
	// [08 "Evaluation"] [C17].
	cases := []struct {
		pos, thresh int32
		wantPass    bool
	}{
		{100, 100, true},
		{102, 100, true},  // diff 2 passes
		{98, 100, true},   // diff 2 passes
		{103, 100, false}, // diff 3 fails
		{97, 100, false},  // diff 3 fails
		{0, 0, true},
		{-2, 0, true},
		{-3, 0, false},
		{100, 102, true},
		{100, 103, false},
	}
	for _, c := range cases {
		tr := New(KindAnyUnitPassesX, "", c.thresh)
		ctx := Context{X: c.pos}
		got := tr.Poll(ctx)
		if got != c.wantPass {
			t.Fatalf("AnyUnitPassesX pos %d thresh %d: got %v want %v (diff %d)", c.pos, c.thresh, got, c.wantPass, c.pos-c.thresh)
		}
		// Reset for Z variant
		tr2 := New(KindAnyUnitPassesZ, "", c.thresh)
		ctx2 := Context{Z: c.pos}
		got2 := tr2.Poll(ctx2)
		if got2 != c.wantPass {
			t.Fatalf("AnyUnitPassesZ pos %d thresh %d: got %v want %v", c.pos, c.thresh, got2, c.wantPass)
		}
	}
	// Ensure pure poll mutates only Completed when satisfied.
	tr := New(KindAnyUnitPassesX, "", 500)
	before := tr.Args[0]
	ctx := Context{X: 502}
	if !tr.Poll(ctx) {
		t.Fatalf("should pass diff 2")
	}
	if tr.Args[0] != before {
		t.Fatalf("boundary poll mutated Args %d -> %d, should only mutate Completed [C17]", before, tr.Args[0])
	}
	if !tr.Completed {
		t.Fatalf("Completed not set")
	}
}

func TestBoundaryToleranceWithTypeGate(t *testing.T) {
	// UnitTypePassesX gated by type and ANYTYPE [C14].
	def := &content.UnitDef{}
	def.UnitName = "ARMCOM"
	def.Commander = false
	u := &units.Unit{Def: def}
	// Create ANYTYPE trigger should pass regardless of unit type.
	trAny := New(KindUnitTypePassesX, "ANYTYPE", 1000)
	ctxAny := Context{X: 1001, Unit: u}
	if !trAny.Poll(ctxAny) {
		t.Fatalf("ANYTYPE should match any type")
	}
	// Specific type mismatch should not pass.
	trSpec := New(KindUnitTypePassesX, "CORCOM", 1000)
	ctxSpec := Context{X: 1000, Unit: u}
	if trSpec.Poll(ctxSpec) {
		t.Fatalf("type mismatch should not pass")
	}
	// Matching type should pass.
	trMatch := New(KindUnitTypePassesX, "ARMCOM", 1000)
	if !trMatch.Poll(ctxSpec) {
		t.Fatalf("matching type should pass")
	}
	// Case-insensitive ANYTYPE
	trLower := New(KindUnitTypePassesX, "anytype", 1000)
	if !IsANYTYPE(trLower.Type) {
		t.Fatalf("IsANYTYPE case-insensitive failed")
	}
}

func TestKillUnitTypeCountdown(t *testing.T) {
	// KillUnitType decrements countdown and completes at zero or below [08 "Evaluation"] [C17].
	def := &content.UnitDef{}
	def.UnitName = "CORLAB"
	u := &units.Unit{Def: def}
	tr := New(KindKillUnitType, "CORLAB", 2)
	if tr.Args[0] != 2 {
		t.Fatalf("initial count %d", tr.Args[0])
	}
	// First kill decrements to 1, not completed.
	if tr.Poll(Context{Unit: u, Event: EventUnitDied}) {
		t.Fatalf("should not complete at 1 remaining")
	}
	if tr.Completed {
		t.Fatalf("Completed premature")
	}
	if tr.Args[0] != 1 {
		t.Fatalf("after first decrement Args[0] %d want 1", tr.Args[0])
	}
	// Second kill completes.
	if !tr.Poll(Context{Unit: u, Event: EventUnitDied}) {
		t.Fatalf("should complete at zero")
	}
	if !tr.Completed || tr.Args[0] != 0 {
		t.Fatalf("after completion Args[0]=%d Completed=%v", tr.Args[0], tr.Completed)
	}
	// Subsequent polls stay completed and do not further decrement (pure idempotent).
	before := tr.Args[0]
	if !tr.Poll(Context{Unit: u, Event: EventUnitDied}) {
		t.Fatalf("already completed should stay true")
	}
	if tr.Args[0] != before {
		t.Fatalf("completed trigger should not further mutate Args")
	}
	// Non-matching type does not decrement.
	tr2 := New(KindKillUnitType, "ARMCOM", 1)
	if tr2.Poll(Context{Unit: u, Event: EventUnitDied}) {
		t.Fatalf("non-matching type should not count")
	}
	if tr2.Args[0] != 1 || tr2.Completed {
		t.Fatalf("non-matching poll mutated state")
	}
	// ANYTYPE matches any.
	trAny := New(KindKillUnitType, "ANYTYPE", 1)
	if !trAny.Poll(Context{Unit: u, Event: EventUnitDied}) {
		t.Fatalf("ANYTYPE should match")
	}
}

func TestTimerSecondsToTicks(t *testing.T) {
	// Timer triggers compare tick count against seconds×30 [08 "Trigger object"] [08 "Evaluation"] [C17].
	tr := NewTimer(KindVictoryTimerRunsOut, 10)
	if tr.Args[0] != 300 {
		t.Fatalf("seconds 10 -> ticks %d want 300", tr.Args[0])
	}
	if tr.Poll(Context{Tick: 299}) {
		t.Fatalf("tick 299 should not fire 300 deadline")
	}
	if tr.Completed {
		t.Fatalf("should not be completed before deadline")
	}
	if !tr.Poll(Context{Tick: 300}) {
		t.Fatalf("tick 300 should fire")
	}
	if !tr.Completed {
		t.Fatalf("should be completed at deadline")
	}
	// Subsequent poll stays completed even at earlier tick (pure Completed flag).
	if !tr.Poll(Context{Tick: 0}) {
		t.Fatalf("completed should stay true regardless of tick")
	}
	// Death timer also uses ×30.
	tr2 := NewTimer(KindDeathTimerRunsOut, 1200)
	if tr2.Args[0] != 36000 {
		t.Fatalf("1200 sec -> %d want 36000", tr2.Args[0])
	}
}

func TestDefaultTriggers(t *testing.T) {
	// With no authored victory condition the engine inserts a default destroy-all-units;
	// with no defeat condition, a default all-units-killed [08 "Default triggers"] [C16].
	var vic []*Trigger
	var def []*Trigger
	vic, def = EnsureDefaults(vic, def)
	if len(vic) != 1 || vic[0].Kind != KindDestroyAllUnits {
		t.Fatalf("default victory want DestroyAllUnits got %v", vic)
	}
	if len(def) != 1 || def[0].Kind != KindAllUnitsKilled {
		t.Fatalf("default defeat want AllUnitsKilled got %v", def)
	}
	// Non-empty queues not injected.
	vic2 := []*Trigger{New(KindKillUnitType, "ARMCOM", 1)}
	def2 := []*Trigger{New(KindDeathTimerRunsOut, "", SecondsToTicks(60))}
	vic2, def2 = EnsureDefaults(vic2, def2)
	if len(vic2) != 1 || vic2[0].Kind != KindKillUnitType {
		t.Fatalf("non-empty victory should not inject")
	}
	if len(def2) != 1 {
		t.Fatalf("non-empty defeat should not inject")
	}
}

func TestEvaluationOrderPurePoll(t *testing.T) {
	// Evaluators are pure polls mutating only their own completed flag [08 "Evaluation"] [C17].
	def := &content.UnitDef{}
	def.UnitName = "ARMLAB"
	u := &units.Unit{Def: def}
	vic := []*Trigger{
		New(KindKillUnitType, "ARMLAB", 1),
		New(KindVictoryTimerRunsOut, "", SecondsToTicks(5)),
	}
	defeat := []*Trigger{
		New(KindAnyUnitPassesX, "", 9999),
	}
	// Poll victory timer at tick 0 should not complete.
	ctx := Context{Tick: 0, Unit: u, Event: EventUnitDied, X: 0}
	// First trigger (KillUnitType) will complete because EventUnitDied matches.
	// Second (VictoryTimer) should not complete.
	// Ensure Poll does not affect the other trigger's Completed.
	vic[0].Poll(ctx)
	if !vic[0].Completed {
		t.Fatalf("first victory should be completed")
	}
	if vic[1].Completed {
		t.Fatalf("second victory should not be completed yet")
	}
	if defeat[0].Completed {
		t.Fatalf("defeat should not be completed")
	}
	// Test Evaluate helper: victory=AND, defeat=OR, simultaneous=victory.
	vic2 := []*Trigger{New(KindAnyUnitPassesX, "", 100), New(KindAnyUnitPassesZ, "", 200)}
	def2 := []*Trigger{New(KindAnyUnitPassesX, "", 300)}
	// Make all victory complete but no defeat.
	vic2[0].Completed = true
	vic2[1].Completed = true
	vDone, dDone := Evaluate(vic2, def2, Context{})
	if !vDone || dDone {
		t.Fatalf("victory AND should be done, defeat not done: %v %v", vDone, dDone)
	}
	// Make defeat complete too -> simultaneous should be victory.
	def2[0].Completed = true
	vDone, dDone = Evaluate(vic2, def2, Context{})
	if !vDone || dDone {
		t.Fatalf("simultaneous should be victory only: %v %v", vDone, dDone)
	}
	// Victory not all done -> not victory.
	vic2[1].Completed = false
	vDone, _ = Evaluate(vic2, def2, Context{})
	if vDone {
		t.Fatalf("victory not all done should not be victory")
	}
	// Defeat OR: any defeat completes -> defeat done.
	def3 := []*Trigger{New(KindDeathTimerRunsOut, "", SecondsToTicks(10)), New(KindAnyUnitPassesX, "", 0)}
	def3[1].Completed = true
	_, dDone = Evaluate(nil, def3, Context{X: 0})
	if !dDone {
		t.Fatalf("defeat OR should be done when any completes")
	}
}

func TestParseArgsFormats(t *testing.T) {
	// Two argument formats: <name>,<int> and <name>,<int>,<int>,<int> [C14][08].
	typ, args, isFour, err := ParseArgs("CORLAB, 1")
	if err != nil || typ != "CORLAB" || args[0] != 1 || isFour {
		t.Fatalf("ParseArgs CORLAB,1: %v %q %v %v", err, typ, args, isFour)
	}
	typ, args, isFour, err = ParseArgs("ARMCOM, 1942, 1519, 100")
	if err != nil || typ != "ARMCOM" || args[0] != 1942 || args[1] != 1519 || args[2] != 100 || !isFour {
		t.Fatalf("ParseArgs ARMCOM,1942,1519,100: %v %q %v %v", err, typ, args, isFour)
	}
	// ANYTYPE wildcard [C14].
	typ, _, _, err = ParseArgs("ANYTYPE, 5")
	if err != nil || !IsANYTYPE(typ) {
		t.Fatalf("ANYTYPE parse failed %v %q", err, typ)
	}
	// Single int boundary for Any* [08 "Defeat trigger types"].
	typ, args, _, err = ParseArgs("4500")
	if err != nil || typ != "" || args[0] != 4500 {
		t.Fatalf("single int parse %v %q %v", err, typ, args)
	}
	// Mixed case ANYTYPE
	if !IsANYTYPE("anytype") || !IsANYTYPE("AnYtYpE") {
		t.Fatalf("IsANYTYPE case-insensitive")
	}
	if IsANYTYPE("ARMCOM") {
		t.Fatalf("IsANYTYPE false positive")
	}
}

func TestParseLine(t *testing.T) {
	// Flag-only
	tr, err := ParseLine("KillEnemyCommander=1;")
	if err != nil || tr.Kind != KindKillEnemyCommander {
		t.Fatalf("ParseLine KillEnemyCommander %v %v", err, tr)
	}
	// Type+count
	tr, err = ParseLine("KillUnitType=CORLAB, 1")
	if err != nil || tr.Kind != KindKillUnitType || tr.Type != "CORLAB" || tr.Args[0] != 1 {
		t.Fatalf("ParseLine KillUnitType %v %v %v", err, tr.Type, tr.Args)
	}
	// Timer seconds×30 [C17]
	tr, err = ParseLine("VictoryTimerRunsOut=3600")
	if err != nil || tr.Args[0] != 3600*30 {
		t.Fatalf("timer %v %v", err, tr)
	}
	// Boundary single int
	tr, err = ParseLine("AnyUnitPassesX=4500;")
	if err != nil || tr.Kind != KindAnyUnitPassesX || tr.Args[0] != 4500 {
		t.Fatalf("AnyUnitPassesX %v %v", err, tr)
	}
	// Radius three ints
	tr, err = ParseLine("MoveUnitToRadius=ARMCOM, 1942, 1519, 100")
	if err != nil || tr.Kind != KindMoveUnitToRadius || tr.Type != "ARMCOM" || tr.Args[0] != 1942 || tr.Args[1] != 1519 || tr.Args[2] != 100 {
		t.Fatalf("MoveUnitToRadius %v %v", err, tr)
	}
	// ANYTYPE in boundary
	tr, err = ParseLine("UnitTypePassesX=ANYTYPE, 6000")
	if err != nil || !IsANYTYPE(tr.Type) || tr.Args[0] != 6000 {
		t.Fatalf("ANYTYPE boundary %v %v", err, tr)
	}
	// AllUnitsKilledOfType type only
	tr, err = ParseLine("AllUnitsKilledOfType=ARMGATE")
	if err != nil || tr.Kind != KindAllUnitsKilledOfType || tr.Type != "ARMGATE" {
		t.Fatalf("AllUnitsKilledOfType %v %v", err, tr)
	}
	// Unknown condition
	if _, err = ParseLine("UnknownTrigger=1"); err == nil {
		t.Fatalf("unknown should error")
	}
}

func TestMoveUnitToRadius(t *testing.T) {
	tr := New(KindMoveUnitToRadius, "ARMCOM", 100, 200, 10)
	def := &content.UnitDef{}
	def.UnitName = "ARMCOM"
	u := &units.Unit{Def: def}
	// Inside radius 10 at (100,200): pos (105,200) distance 5 <=10 -> pass
	if !tr.Poll(Context{X: 105, Z: 200, Unit: u}) {
		t.Fatalf("inside radius should pass")
	}
	// Reset for next check
	tr2 := New(KindMoveUnitToRadius, "ARMCOM", 100, 200, 10)
	if tr2.Poll(Context{X: 115, Z: 200, Unit: u}) {
		t.Fatalf("outside radius should fail")
	}
	// ANYTYPE passes any unit
	trAny := New(KindMoveUnitToRadius, "ANYTYPE", 0, 0, 5)
	def2 := &content.UnitDef{}
	def2.UnitName = "CORCOM"
	u2 := &units.Unit{Def: def2}
	if !trAny.Poll(Context{X: 3, Z: 4, Unit: u2}) {
		t.Fatalf("ANYTYPE radius inside should pass (3-4-5)")
	}
}

func TestTriggerPollPureOnlyCompleted(t *testing.T) {
	// Verify Poll does not mutate Type or other triggers [C17].
	tr := New(KindVictoryTimerRunsOut, "", SecondsToTicks(100))
	origType := tr.Type
	origArgs := tr.Args
	tr.Poll(Context{Tick: 10})
	if tr.Type != origType || tr.Args != origArgs {
		t.Fatalf("poll mutated non-Completed fields before deadline")
	}
	tr.Poll(Context{Tick: 3000})
	if !tr.Completed {
		t.Fatalf("should complete at deadline")
	}
	if tr.Type != origType {
		t.Fatalf("poll mutated Type after completion")
	}
}
