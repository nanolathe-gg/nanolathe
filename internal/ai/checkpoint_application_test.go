package ai

import (
	"bytes"
	"crypto/sha256"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

func TestCheckpointApplicationEntryBinding(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "same"}, UnitName: "same"}
	c := aiCheckpointContext(t, def)
	for _, kind := range []Controller{ControllerClassic, ControllerModern} {
		m := &Manager{Player: 3, Controller: kind}
		if m.CheckpointApplicationHistory() != nil {
			t.Fatal("ordinary manager has history")
		}
		if err := m.EnableCheckpointApplications(checkpoint.Identity{}, c.Units.Keys); err != nil {
			t.Fatal(err)
		}
		h := m.CheckpointApplicationHistory()
		v, err := h.Snapshot()
		if err != nil || v.Kind != uint8(kind)+1 || v.Player != 3 || v.NextSerial != 1 {
			t.Fatal(v, err)
		}
		got, err := m.CheckpointApplicationKey(def)
		if err != nil || got.Key != "unit/same" {
			t.Fatal(got, err)
		}
		foreign := *def
		if _, err := m.CheckpointApplicationKey(&foreign); err == nil {
			t.Fatal("foreign equal-name definition accepted")
		}
		if err := m.EnableCheckpointApplications(checkpoint.Identity{}, c.Units.Keys); err == nil || m.CheckpointApplicationHistory() != h {
			t.Fatal("history reset")
		}
	}
	var absent *Manager
	if absent.CheckpointApplicationHistory() != nil {
		t.Fatal("nil manager history")
	}
	for _, m := range []*Manager{nil, {Player: 10}, {Controller: Controller(255)}, {Ext: (*int)(nil)}} {
		if err := m.EnableCheckpointApplications(checkpoint.Identity{}, c.Units.Keys); err == nil {
			t.Fatal("unsupported entry")
		}
	}
	m := &Manager{}
	if err := m.EnableCheckpointApplications(checkpoint.Identity{}, nil); err == nil || m.checkpointHistory != nil || m.checkpointKeys != nil {
		t.Fatal("missing keys mutated manager")
	}
	if _, err := m.CheckpointApplicationKey(def); err == nil {
		t.Fatal("disabled key lookup")
	}
	if _, err := absent.CheckpointApplicationKey(def); err == nil {
		t.Fatal("nil key lookup")
	}
}

func TestCheckpointObservedAllocationSurvivesRetirement(t *testing.T) {
	old := &units.Unit{Handle: 7, AllocationSerial: 1, Alive: false}
	fresh := &units.Unit{Handle: 7, AllocationSerial: 2, Alive: true}
	a, err := CheckpointAllocation(old)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CheckpointAllocation(fresh)
	if err != nil || a == b || a.Handle != b.Handle {
		t.Fatal("slot reuse collapsed", a, b, err)
	}
	for _, u := range []*units.Unit{nil, {}, {Handle: 7}, {AllocationSerial: 1}} {
		if _, err := CheckpointAllocation(u); err == nil {
			t.Fatal("missing allocation identity")
		}
	}
}

func TestCheckpointControllerValueVector(t *testing.T) {
	v := ControllerCheckpoint{Present: true, Initialized: true, NextThinkPresent: true, NextThinkTick: 23, DeadlinePresent: true, DeadlineTick: 0xffffffff, NextBatchSerial: 12, ApplicationCount: 7, Tokens: -4, LastFill: 99}
	for i := range v.ApplicationHash {
		v.ApplicationHash[i] = byte(i)
	}
	var out bytes.Buffer
	if err := v.WriteCheckpoint(checkpoint.NewEncoder(&out)); err != nil {
		t.Fatal(err)
	}
	want := aiCheckpointHex(t, "0700000000000000000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f01ffffffff01630000000c00000000000000011700000001fcffffffffffffff")
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("controller bytes %x", out.Bytes())
	}
	var s checkpoint.Summary
	s.Word(37)
	if err := v.AppendCheckpointSummary(&s); err != nil {
		t.Fatal(err)
	}
	if n, sum := s.Result(); n != 12 || sum != 9182095628243900521 {
		t.Fatal(n, sum)
	}
	if n := testing.AllocsPerRun(20, func() {
		var s checkpoint.Summary
		if err := v.AppendCheckpointSummary(&s); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatal(n)
	}
	before := s
	v.Present = false
	v.Initialized = false
	v.NextBatchSerial++
	s = checkpoint.Summary{}
	s.Word(37)
	_ = v.AppendCheckpointSummary(&s)
	if s != before {
		t.Fatal("unselected controller metadata affected summary")
	}
	out.Reset()
	_ = v.WriteCheckpoint(checkpoint.NewEncoder(&out))
	if bytes.Equal(out.Bytes(), want) {
		t.Fatal("full writer lost summary blind spots")
	}
	v.DeadlinePresent = false
	s = checkpoint.Summary{}
	s.Word(37)
	_ = v.AppendCheckpointSummary(&s)
	if s == before {
		t.Fatal("deadline absence collapsed into tick")
	}
	if err := v.WriteCheckpoint(nil); err == nil {
		t.Fatal("nil encoder")
	}
	if err := v.AppendCheckpointSummary(nil); err == nil {
		t.Fatal("nil summary")
	}
}

// The generic ownership check accepts a pointer even when its element is not
// comparable, and never compares a foreign Ext interface to another interface.
func TestCheckpointControllerApplicationOwnership(t *testing.T) {
	type controller struct{ rows []int }
	owner := &controller{rows: []int{1}}
	m := &Manager{Player: 3, Controller: ControllerModern, Ext: owner}
	keys := &content.CheckpointKeys{}
	var identity checkpoint.Identity
	identity.Content[0], identity.Config[0] = 7, 9
	if err := EnableControllerCheckpointApplications(m, owner, identity, keys); err != nil {
		t.Fatal(err)
	}
	state, err := m.checkpointHistory.Snapshot()
	if err != nil || !state.Enabled || state.Player != 3 || state.Kind != 2 || state.Count != 0 || state.NextSerial != 1 || m.checkpointKeys != keys {
		t.Fatalf("fresh controller history=%+v err=%v", state, err)
	}
	// Independently authored initial envelope, including schema version and slot.
	envelope := append([]byte("NLCPAIST"), 2, 0)
	envelope = append(envelope, 7)
	envelope = append(envelope, make([]byte, 31)...)
	envelope = append(envelope, 9)
	envelope = append(envelope, make([]byte, 31)...)
	envelope = append(envelope, 3, 2)
	if state.Hash != sha256.Sum256(envelope) {
		t.Fatal("fresh chain changed its identity envelope")
	}
	history := m.checkpointHistory
	if err := EnableControllerCheckpointApplications(m, owner, checkpoint.Identity{}, keys); err == nil || m.checkpointHistory != history {
		t.Fatal("repeated attachment reset history")
	}
	if after, err := history.Snapshot(); err != nil || after != state {
		t.Fatal("refusal changed history", err)
	}
}

func TestCheckpointControllerApplicationRefusalsAreAtomic(t *testing.T) {
	type controller struct{ marker byte }
	owner, foreign := &controller{1}, &controller{2}
	keys, oldKeys := &content.CheckpointKeys{}, &content.CheckpointKeys{}
	existing, err := NewApplicationHistory(checkpoint.Identity{}, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		m        *Manager
		expected *controller
		keys     *content.CheckpointKeys
	}{
		{"nil manager", nil, owner, keys},
		{"nil expected", &Manager{Controller: ControllerModern, Ext: owner}, nil, keys},
		{"nil keys", &Manager{Controller: ControllerModern, Ext: owner}, owner, nil},
		{"classic", &Manager{Controller: ControllerClassic, Ext: owner}, owner, keys},
		{"unknown kind", &Manager{Controller: 255, Ext: owner}, owner, keys},
		{"invalid player", &Manager{Player: 10, Controller: ControllerModern, Ext: owner}, owner, keys},
		{"nil Ext", &Manager{Controller: ControllerModern}, owner, keys},
		{"typed nil Ext", &Manager{Controller: ControllerModern, Ext: (*controller)(nil)}, owner, keys},
		{"foreign pointer", &Manager{Controller: ControllerModern, Ext: foreign}, owner, keys},
		{"foreign noncomparable", &Manager{Controller: ControllerModern, Ext: []int{1, 2}}, owner, keys},
		{"existing history", &Manager{Player: 3, Controller: ControllerModern, Ext: owner, checkpointHistory: existing}, owner, keys},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before *ApplicationHistory
			if tc.m != nil {
				tc.m.checkpointKeys = oldKeys
				before = tc.m.checkpointHistory
			}
			if err := EnableControllerCheckpointApplications(tc.m, tc.expected, checkpoint.Identity{}, tc.keys); err == nil {
				t.Fatal("invalid controller ownership accepted")
			}
			if tc.m != nil && (tc.m.checkpointHistory != before || tc.m.checkpointKeys != oldKeys) {
				t.Fatal("refusal partially installed history/keys")
			}
		})
	}
	// The old API still forbids Ext, including the exact valid owner pointer.
	m := &Manager{Controller: ControllerModern, Ext: owner}
	if err := m.EnableCheckpointApplications(checkpoint.Identity{}, keys); err == nil || m.checkpointHistory != nil || m.checkpointKeys != nil {
		t.Fatal("old no-Ext contract was relaxed")
	}
}
