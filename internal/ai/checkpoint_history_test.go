package ai

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

func historyFixture(t *testing.T, kind uint8) *ApplicationHistory {
	t.Helper()
	var identity checkpoint.Identity
	for i := range identity.Content {
		identity.Content[i] = byte(i)
		identity.Config[i] = byte(i + 32)
	}
	h, err := NewApplicationHistory(identity, 3, kind)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func historyIntent(value uint16) func(*checkpoint.Encoder) error {
	return func(e *checkpoint.Encoder) error { e.U16(value); return e.Err() }
}

func historyActor(e *checkpoint.Encoder) error {
	e.Allocation(checkpoint.Allocation{Handle: 7, Serial: 9})
	return e.Err()
}

// Independent SHA-256 vectors use the literal domains, fixed-width envelope,
// one fixture intent word, and two operation payloads. No owner encoder builds
// the expected digest. The second attempt is rejected by APM and still counts.
func TestApplicationHistoryEnvelopeVectors(t *testing.T) {
	h := historyFixture(t, 2)
	assertDigest := func(want string, count uint64) {
		t.Helper()
		s, err := h.Snapshot()
		if err != nil || !s.Enabled || s.Player != 3 || s.Kind != 2 || s.Count != count || !bytes.Equal(s.Hash[:], aiCheckpointHex(t, want)) {
			t.Fatalf("state %+v, %v", s, err)
		}
	}
	assertDigest("c924bf6f869847079b9825ec2c9e397fced230e8bdce9b93af56c1450045a4cd", 0)
	serial := h.NextSerial()
	if serial != 1 {
		t.Fatal(serial)
	}
	a := h.BeginAttempt(77, serial, 0, historyIntent(0x1234))
	a.Operation(1, historyActor)
	a.Operation(6, func(e *checkpoint.Encoder) error { _ = historyActor(e); e.Bool(true); return e.Err() })
	a.Finish(2, 2)
	assertDigest("269bb49d065846716eb30060e3c640a672d97836d14a532b3e1ba13053a137c5", 1)
	if a.header.Len() != 0 || a.operations.Len() != 0 || a.header.Cap() != 0 || a.operations.Cap() != 0 {
		t.Fatal("attempt retained its buffers")
	}
	h.BeginAttempt(77, serial, 1, historyIntent(0xabcd)).Finish(3, 3)
	assertDigest("60a7765cf0254f5cfca3cd5f493ffdad0b28fe5ddd67238394df1705d29cdb01", 2)
	var out bytes.Buffer
	if err := h.WriteCheckpoint(checkpoint.NewEncoder(&out)); err != nil {
		t.Fatal(err)
	}
	want := aiCheckpointHex(t, "01020000000000000060a7765cf0254f5cfca3cd5f493ffdad0b28fe5ddd67238394df1705d29cdb0102020000000000000003")
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("history bytes %x", out.Bytes())
	}
	var s checkpoint.Summary
	s.Word(37)
	if err := h.AppendCheckpointSummary(&s); err != nil {
		t.Fatal(err)
	}
	if n, sum := s.Result(); n != 6 || sum != 7474043769438899176 {
		t.Fatalf("summary %d %d", n, sum)
	}
	if n := testing.AllocsPerRun(20, func() {
		var s checkpoint.Summary
		if err := h.AppendCheckpointSummary(&s); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("summary allocations %g", n)
	}
}

func TestApplicationHistoryFailureAndQuiescence(t *testing.T) {
	h := historyFixture(t, 1)
	serial := h.NextSerial()
	a := h.BeginAttempt(0, serial, 0, historyIntent(1))
	var s checkpoint.Summary
	s.Word(99)
	before := s
	if _, err := h.Snapshot(); err == nil {
		t.Fatal("active attempt captured")
	}
	if err := h.AppendCheckpointSummary(&s); err == nil || s != before {
		t.Fatal("active summary not atomic")
	}
	var out bytes.Buffer
	if err := h.WriteCheckpoint(checkpoint.NewEncoder(&out)); err == nil || out.Len() != 0 {
		t.Fatal("active full capture admitted")
	}
	// A read at an invalid boundary does not poison a later valid boundary.
	a.Finish(1, 1)
	if _, err := h.Snapshot(); err != nil {
		t.Fatal(err)
	}
	beforeCount, beforeHash := h.count, h.digest
	a = h.BeginAttempt(1, serial, 1, historyIntent(2))
	a.Operation(1, func(e *checkpoint.Encoder) error { e.U8(9); return errors.New("fixture failure") })
	err := h.err
	called := false
	a.Operation(2, func(*checkpoint.Encoder) error { called = true; return nil })
	a.Finish(1, 4)
	h.Fail(errors.New("later error"))
	if called || h.err != err || h.count != beforeCount || h.digest != beforeHash || h.active != nil || a.operations.Cap() != 0 {
		t.Fatal("diagnostic failure was not isolated and sticky")
	}
	if h.NextSerial() != 0 {
		t.Fatal("failed history assigned identity")
	}
	if _, err := h.Snapshot(); err == nil {
		t.Fatal("failed history reported")
	}
}

func TestApplicationHistoryDisabledAndSerials(t *testing.T) {
	var h *ApplicationHistory
	called := false
	a := h.BeginAttempt(0, 0, 0, func(*checkpoint.Encoder) error { called = true; return nil })
	a.Operation(1, func(*checkpoint.Encoder) error { called = true; return nil })
	a.Finish(1, 1)
	h.Fail(errors.New("ignored"))
	if called || h.NextSerial() != 0 {
		t.Fatal("disabled history did work")
	}
	s, err := h.Snapshot()
	if err != nil || s != (ApplicationHistoryState{}) {
		t.Fatal(s, err)
	}
	var out bytes.Buffer
	if err := h.WriteCheckpoint(checkpoint.NewEncoder(&out)); err != nil || !bytes.Equal(out.Bytes(), []byte{0}) {
		t.Fatal("disabled presence", err)
	}
	if err := h.AppendCheckpointSummary(&checkpoint.Summary{}); err == nil {
		t.Fatal("absent summary admitted")
	}
	if err := h.WriteCheckpoint(nil); err == nil {
		t.Fatal("nil encoder")
	}
	if err := h.AppendCheckpointSummary(nil); err == nil {
		t.Fatal("nil summary")
	}
	h = historyFixture(t, 1)
	if h.NextSerial() != 1 || h.NextSerial() != 2 {
		t.Fatal("empty batches did not get serials")
	}
	h.nextSerial = math.MaxUint64
	if h.NextSerial() != 0 || h.nextSerial != math.MaxUint64 || h.err == nil {
		t.Fatal("serial wrapped")
	}
	for _, v := range [][2]uint8{{10, 1}, {0, 0}, {0, 3}} {
		if _, err := NewApplicationHistory(checkpoint.Identity{}, v[0], v[1]); err == nil {
			t.Fatal("bad identity", v)
		}
	}
}

func TestApplicationHistoryRejectsMalformedAndExhaustedRecords(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ApplicationHistory, *ApplicationAttempt)
	}{
		{"count", func(h *ApplicationHistory, a *ApplicationAttempt) { h.count = math.MaxUint64; a.Finish(2, 2) }},
		{"operations", func(h *ApplicationHistory, a *ApplicationAttempt) {
			a.operationCount = math.MaxUint32
			a.Operation(1, historyActor)
			a.Finish(2, 4)
		}},
		{"unknown operation", func(_ *ApplicationHistory, a *ApplicationAttempt) { a.Operation(11, historyActor); a.Finish(2, 4) }},
		{"missing operation codec", func(_ *ApplicationHistory, a *ApplicationAttempt) { a.Operation(1, nil); a.Finish(2, 4) }},
		{"overlap", func(h *ApplicationHistory, a *ApplicationAttempt) {
			h.BeginAttempt(0, 1, 0, historyIntent(1))
			a.Finish(2, 4)
		}},
		{"serial during attempt", func(h *ApplicationHistory, a *ApplicationAttempt) { h.NextSerial(); a.Finish(2, 4) }},
		{"APM", func(_ *ApplicationHistory, a *ApplicationAttempt) { a.Finish(0, 2) }},
		{"terminal", func(_ *ApplicationHistory, a *ApplicationAttempt) { a.Finish(2, 0) }},
		{"rejection operations", func(_ *ApplicationHistory, a *ApplicationAttempt) { a.Operation(1, historyActor); a.Finish(3, 3) }},
		{"rejection outcome", func(_ *ApplicationHistory, a *ApplicationAttempt) { a.Finish(3, 1) }},
		{"double finish", func(_ *ApplicationHistory, a *ApplicationAttempt) { a.Finish(2, 2); a.Finish(2, 2) }},
		{"late operation", func(_ *ApplicationHistory, a *ApplicationAttempt) { a.Finish(2, 2); a.Operation(1, historyActor) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := historyFixture(t, 2)
			a := h.BeginAttempt(0, h.NextSerial(), 0, historyIntent(1))
			tc.edit(h, a)
			if _, err := h.Snapshot(); err == nil {
				t.Fatal("invalid history admitted")
			}
		})
	}
	for _, serial := range []uint64{0, 2} {
		h := historyFixture(t, 2)
		h.NextSerial()
		if a := h.BeginAttempt(0, serial, 0, historyIntent(1)); a != nil || h.err == nil {
			t.Fatal("unassigned serial")
		}
	}
	h := historyFixture(t, 2)
	h.BeginAttempt(0, h.NextSerial(), 0, nil)
	if h.err == nil {
		t.Fatal("missing intent codec")
	}
	h = historyFixture(t, 2)
	a := h.BeginAttempt(0, h.NextSerial(), 0, func(*checkpoint.Encoder) error { return errors.New("intent failure") })
	a.Finish(2, 4)
	if h.err == nil || h.count != 0 {
		t.Fatal("invalid intent counted")
	}
	h = historyFixture(t, 1)
	h.BeginAttempt(0, h.NextSerial(), 0, historyIntent(1)).Finish(2, 2)
	if h.err == nil {
		t.Fatal("Classic APM debit admitted")
	}
}
