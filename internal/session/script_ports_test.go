package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func newScriptPortFixture(t *testing.T) (*Session, *units.Unit, *units.Unit) {
	t.Helper()
	def := &content.UnitDef{UnitName: "port-unit", BMCode: 1, MaxDamage: 100}
	def.CanonicalKey = "port-unit"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"port-unit": def}}
	w := newSessionFixtureWorld(3, cat)
	readingHandle, err := w.Create(def, 2, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	targetHandle, err := w.CreateNanoframe(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	reading, target := w.Unit(readingHandle), w.Unit(targetHandle)
	reading.Kills = 7
	target.Remaining = 0.5
	s := &Session{Units: w, Econ: &economy.Service{}, Skirmish: SkirmishConfig{UnitLimit: 3}}
	for owner := range 3 {
		s.Econ.Players[owner].Exists = true
	}
	s.Econ.Players[1].ControllerState = 2
	s.Econ.Players[2].ControllerState = 1
	s.Econ.Players[2].Allies[1] = true
	s.Econ.Players[2].Allies[2] = true
	if err := s.SetRules(CommunityRuleSetName); err != nil {
		t.Fatal(err)
	}
	return s, reading, target
}

// The table locks exactly the eight adopted identifiers and their operand
// sources. Other recorder groups remain on the VM's zero default
// (DESIGN_COMMUNITY_PATCH §4.5, Q10).
func TestCommunityScriptPorts(t *testing.T) {
	s, reading, target := newScriptPortFixture(t)
	rules := s.scriptPortRules()
	for _, tc := range []struct {
		port cob.Port
		arg  int32
		want int32
	}{
		{32, 0, 700},
		{69, 0, 1},
		{70, 0, 30},
		{71, 0, int32(uint16(reading.Handle))},
		{72, int32(target.Handle), 1},
		{73, int32(target.Handle), 50},
		{74, int32(target.Handle), 1},
		{75, int32(target.Handle), 1},
	} {
		if got := rules.ReadScriptPort(s, reading, tc.port, [4]int32{tc.arg}); got != tc.want {
			t.Errorf("port %d(%d) = %d, want %d", tc.port, tc.arg, got, tc.want)
		}
	}
	if got := rules.ReadScriptPort(s, reading, 76, [4]int32{}); got != 0 {
		t.Fatalf("unadopted port 76 = %d, want zero", got)
	}
}

// Port 73 uses the source's single-precision product and truncates it before
// adding one. Raw target slots have no occupancy gate, so a freed record keeps
// the fraction until the slot is reused
// [research/extensions/script-ports.md "Port table", "Boundary behavior"].
func TestCommunityScriptPort73ArithmeticAndStaleRecord(t *testing.T) {
	s, reading, target := newScriptPortFixture(t)
	rules := s.scriptPortRules()
	for _, tc := range []struct {
		remaining float32
		want      int32
	}{
		{0, 0},
		{0.0001, 1},
		{0.5, 50},
		{float32(1) / 3, 34},
		{1, 100},
	} {
		target.Remaining = tc.remaining
		if got := rules.ReadScriptPort(s, reading, 73, [4]int32{int32(target.Handle)}); got != tc.want {
			t.Errorf("remaining %v: port 73 = %d, want %d", tc.remaining, got, tc.want)
		}
	}
	target.Remaining = 0.5
	s.Units.Destroy(target.Handle, units.DeathKilled)
	if result := s.Units.FinalizeDeath(target.Handle, 1); !result.Freed {
		t.Fatal("target slot was not freed")
	}
	for _, tc := range []struct {
		port cob.Port
		want int32
	}{{72, 1}, {73, 50}, {74, 1}, {75, 1}} {
		if got := rules.ReadScriptPort(s, reading, tc.port, [4]int32{int32(target.Handle)}); got != tc.want {
			t.Errorf("stale port %d = %d, want %d", tc.port, got, tc.want)
		}
	}
}

func TestCommunityScriptPortInvalidAndSlotZeroAnswers(t *testing.T) {
	s, reading, target := newScriptPortFixture(t)
	rules := s.scriptPortRules()
	outside := int32(0x7fff)
	for _, tc := range []struct {
		name string
		port cob.Port
		arg  int32
		want int32
	}{
		{"port72 slot zero is no unit", 72, 0, 0},
		{"port74 slot zero is no unit", 74, 0, 0},
		{"port73 outside is defined zero", 73, outside, 0},
		{"port75 outside is guarded zero", 75, outside, 0},
		{"low sixteen bits select slot zero", 72, 0x10000, 0},
		{"port73 zero means reading unit", 73, 0, 0},
		{"port75 zero means reading unit", 75, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules.ReadScriptPort(s, reading, tc.port, [4]int32{tc.arg}); got != tc.want {
				t.Fatalf("port %d(%d) = %d, want %d", tc.port, tc.arg, got, tc.want)
			}
		})
	}

	// Port 74 alone maps a nonzero out-of-range target to owner zero before
	// consulting the reading owner's one-way alliance row.
	s.Econ.Players[2].Allies[0] = true
	if got := rules.ReadScriptPort(s, reading, 74, [4]int32{outside}); got != 1 {
		t.Fatalf("port 74 out-of-range owner-zero comparison = %d, want 1", got)
	}
	s.Econ.Players[2].Allies[0] = false
	if got := rules.ReadScriptPort(s, reading, 74, [4]int32{outside}); got != 0 {
		t.Fatalf("port 74 out-of-range hostile owner-zero comparison = %d, want 0", got)
	}
	s.Econ.Players[2].Allies[1] = false
	s.Econ.Players[1].Allies[2] = true
	if got := rules.ReadScriptPort(s, reading, 74, [4]int32{int32(target.Handle)}); got != 0 {
		t.Fatalf("port 74 reverse-only alliance comparison = %d, want 0", got)
	}

	s.Econ.Players[1].ControllerState = 3
	if got := rules.ReadScriptPort(s, reading, 75, [4]int32{int32(target.Handle)}); got != 0 {
		t.Fatalf("port 75 remote controller = %d, want 0", got)
	}
}

type testScriptPortOverride struct{}

func (testScriptPortOverride) ReadScriptPort(_ *Session, _ *units.Unit, port cob.Port, args [4]int32) int32 {
	return int32(port) + args[0]
}

const (
	testScriptPortFeatureSet  = "session-test-script-port-feature"
	testScriptPortOverrideSet = "session-test-script-port-override"
)

var testScriptPortsDisabled = false

func init() {
	RegisterRuleSet(testScriptPortFeatureSet, func() RuleSet {
		return RuleSet{
			Base:     gameplay.Community39,
			Features: community.Overrides{ScriptPorts: &testScriptPortsDisabled},
		}
	})
	RegisterRuleSet(testScriptPortOverrideSet, func() RuleSet {
		return RuleSet{Base: gameplay.Community39, ScriptPorts: testScriptPortOverride{}}
	})
}

func scriptPortReadVM(port cob.Port, arg int32, fiveArguments bool) *cob.VM {
	code := []uint32{0x10021001, uint32(port)}
	if fiveArguments {
		code = append(code,
			0x10021001, uint32(arg),
			0x10021001, 0,
			0x10021001, 0,
			0x10021001, 0,
			0x10043000,
		)
	} else {
		code = append(code, 0x10042000)
	}
	code = append(code, 0x10065000)
	return cob.NewVM(&cob.Program{Code: code, Scripts: map[string]int{"Read": 0}, ScriptsByID: []int{0}})
}

func readScriptPortVM(t *testing.T, vm *cob.VM) int32 {
	t.Helper()
	if !vm.StartByName("Read", nil) {
		t.Fatal("start script-port reader")
	}
	vm.Drain(0)
	value, ok := vm.ConsumeReturn(0)
	if !ok {
		t.Fatal("script-port reader returned no value")
	}
	return value
}

// Handlers stay attached to an existing VM and ask the current bound seam on
// every read. This covers the Strict bypass, the resolved feature flag, a
// registered-style seam override, and both switch directions without
// replacing the VM (DESIGN_GAMEPLAY_RULES §5, §9).
func TestScriptPortBindingFollowsCurrentRulesAndFeatures(t *testing.T) {
	s, reading, _ := newScriptPortFixture(t)
	vm := scriptPortReadVM(32, 0, false)
	s.bindQueryPorts(&cob.Binding{VM: vm}, reading)

	if err := s.SetRules(StrictRuleSetName); err != nil {
		t.Fatal(err)
	}
	if got := readScriptPortVM(t, vm); got != 0 {
		t.Fatalf("Strict port 32 = %d, want zero", got)
	}
	if err := s.SetRules(CommunityRuleSetName); err != nil {
		t.Fatal(err)
	}
	if got := readScriptPortVM(t, vm); got != 700 {
		t.Fatalf("Community port 32 = %d, want 700", got)
	}

	if err := s.SetRules(testScriptPortFeatureSet); err != nil {
		t.Fatal(err)
	}
	if got := readScriptPortVM(t, vm); got != 0 {
		t.Fatalf("registered feature-disabled Community port 32 = %d, want zero", got)
	}

	if err := s.SetRules(testScriptPortOverrideSet); err != nil {
		t.Fatal(err)
	}
	if got := readScriptPortVM(t, vm); got != 32 {
		t.Fatalf("registered override on existing-VM port 32 = %d, want 32", got)
	}
	if err := s.SetRules(StrictRuleSetName); err != nil {
		t.Fatal(err)
	}
	if got := readScriptPortVM(t, vm); got != 0 {
		t.Fatalf("switch back to Strict port 32 = %d, want zero", got)
	}
}
