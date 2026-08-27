package render

import "testing"

type mockObj struct {
	id        int
	shouldRem bool
	updated   int
	latch     bool
	latchSet  bool
}

func (m *mockObj) ShouldRemove(_ uint32) bool { return m.shouldRem }
func (m *mockObj) Update(_ uint32) {
	m.updated++
	if m.latch && !m.latchSet {
		m.latchSet = true
		m.shouldRem = true
	}
}

func TestRemovalBeforeUpdate(t *testing.T) {
	var s Strip
	a := &mockObj{id: 1}
	b := &mockObj{id: 2, shouldRem: true}
	s.Append(a)
	s.Append(b)
	s.Update(42)
	if len(s.Objects) != 1 || s.Objects[0] != a || a.updated != 1 || b.updated != 0 {
		t.Fatalf("removal-before-update mismatch: objects=%d a=%d b=%d", len(s.Objects), a.updated, b.updated)
	}
}

func TestStableCompaction(t *testing.T) {
	var s Strip
	a := &mockObj{id: 1, latch: true}
	b := &mockObj{id: 2}
	c := &mockObj{id: 3, shouldRem: true}
	s.Append(a)
	s.Append(b)
	s.Append(c)
	s.Update(1)
	if len(s.Objects) != 2 || s.Objects[0] != a || s.Objects[1] != b {
		t.Fatalf("stable compaction mismatch after first update")
	}
	s.Update(2)
	if len(s.Objects) != 1 || s.Objects[0] != b {
		t.Fatalf("latch removal mismatch after second update")
	}
}

func TestStripEviction(t *testing.T) {
	var s Strip
	for i := 0; i < 401; i++ {
		s.Append(&mockObj{id: i})
	}
	if len(s.Objects) != 401 || s.Objects[0].(*mockObj).id != 0 {
		t.Fatalf("initial strip capacity mismatch")
	}
	s.Append(&mockObj{id: 401})
	if len(s.Objects) != 401 || s.Objects[0].(*mockObj).id != 1 || s.Objects[400].(*mockObj).id != 401 {
		t.Fatalf("FIFO strip eviction mismatch")
	}
}
