package ai

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

const applicationOrderNodePrefix = `00000000 00 0000 0000 00 00000000 ffffffff 00000000 00000000
	0000000000000000 0000000000000000 0000000000000000
	0000 0000 0000000000000000 71 00 21430000 00000000`
const applicationOrderNodeSuffix = `00000000 00000000 00 00000000 00000000 22220000
	00 00 00 0000000000000000 0000000000000000 00000000 00000000
	00000000 00000000 00000000`
const applicationOrderActorVector = `a4a3a2a1 b8b7b6b5b4b3b2b1`

const applicationOrderOperationsVector = `0100 ` + applicationOrderActorVector +
	`0200 ` + applicationOrderActorVector +
	`0300 ` + applicationOrderActorVector + `02 0300000000010000 ` + applicationOrderNodePrefix + `09000000 ` + applicationOrderNodeSuffix +
	`0a00 ` + applicationOrderActorVector + `01 0200000000000000 ffffffff 02000000 ` + applicationOrderNodePrefix + `01000000 ` + applicationOrderNodeSuffix +
	`0900 ` + applicationOrderActorVector + `01 ` +
	`0900 ` + applicationOrderActorVector + `02`

func applicationOrderReceiptsFixture() (checkpoint.Allocation, []orders.CheckpointOrderReceipt) {
	actor := checkpoint.Allocation{Handle: 0xa1a2a3a4, Serial: 0xb1b2b3b4b5b6b7b8}
	node := orders.Node{ID: 0x71, Owner: 0x4321, Target: 0x2222, Deadline: -1, Param2: 9}
	coalesced := node
	coalesced.Param2 = 1
	return actor, []orders.CheckpointOrderReceipt{
		{Kind: 1}, {Kind: 2},
		{Kind: 3, Segment: 2, Index: 1<<40 + 3, Node: node},
		{Kind: 4, Segment: 1, Index: 2, PreviousCount: ^uint32(0), Added: 2, Node: coalesced},
		{Kind: 5, Preparation: 1}, {Kind: 5, Preparation: 2},
	}
}

func TestApplicationOrderReceiptOperationVector(t *testing.T) {
	h := historyFixture(t, 1)
	a := h.BeginAttempt(77, h.NextSerial(), 2, classicOrderFixture().WriteCheckpoint)
	actor, receipts := applicationOrderReceiptsFixture()
	for _, receipt := range receipts {
		a.RecordOrder(actor, receipt)
	}
	want := applicationCodecHex(t, applicationOrderOperationsVector)
	if !bytes.Equal(a.operations.Bytes(), want) || a.operationCount != 6 || h.err != nil {
		t.Fatalf("order operations\n got %x\nwant %x\ncount %d error %v", a.operations.Bytes(), want, a.operationCount, h.err)
	}
	// The terminal is supplied independently. Preparations and empty removals
	// are receipts too; their mere presence does not prove a committed effect.
	a.Finish(1, 1)
	if s, err := h.Snapshot(); err != nil || s.Count != 1 {
		t.Fatalf("explicit accepted-no-op outcome = %+v, %v", s, err)
	}
}

func TestApplicationTypedCodecChainVectors(t *testing.T) {
	for _, tt := range []struct {
		kind   uint8
		write  func(*checkpoint.Encoder) error
		intent string
		want   string
	}{
		{1, classicOrderFixture().WriteCheckpoint, classicOrderVector + applicationOperandsVector, "0737f5734966a55692e23a4dda41949c696002940126aefd5dde317ce2bc504c"},
		{2, modernIntentFixture().WriteCheckpoint, modernIntentVector + applicationOperandsVector, "79231513c39cdd86de99dbfca14d64e3fec9d56a990aadb286a3d8e2be157974"},
	} {
		h := historyFixture(t, tt.kind)
		serial := h.NextSerial()
		a := h.BeginAttempt(0x01020304, serial, 2, tt.write)
		wantHeader := append([]byte{3, tt.kind}, applicationCodecHex(t, `04030201 0100000000000000 02000000`+tt.intent)...)
		if !bytes.Equal(a.header.Bytes(), wantHeader) {
			t.Fatalf("kind %d header\n got %x\nwant %x", tt.kind, a.header.Bytes(), wantHeader)
		}
		actor, receipts := applicationOrderReceiptsFixture()
		for _, receipt := range receipts {
			a.RecordOrder(actor, receipt)
		}
		apm := uint8(1)
		if tt.kind == 2 {
			apm = 2
		}
		a.Finish(apm, 4)
		s, err := h.Snapshot()
		if err != nil || s.Count != 1 || !bytes.Equal(s.Hash[:], applicationCodecHex(t, tt.want)) {
			t.Fatalf("kind %d chain = %x, count %d, error %v", tt.kind, s.Hash, s.Count, err)
		}
		if a.header.Cap() != 0 || a.operations.Cap() != 0 {
			t.Fatal("finished typed attempt retained borrowed values")
		}
		if tt.kind == 2 {
			// An APM-refused command in the same batch still has an ordinal
			// and typed intent, but no receipt operations.
			h.BeginAttempt(0x01020304, serial, 3, tt.write).Finish(3, 3)
			s, err = h.Snapshot()
			if err != nil || s.Count != 2 || !bytes.Equal(s.Hash[:], applicationCodecHex(t, "ac8f5463e9db5a720334133d1f5b56e6f24efc9e08ff93173b433b60b879e85e")) {
				t.Fatalf("APM rejection chain = %x, count %d, error %v", s.Hash, s.Count, err)
			}
		}
	}
}

func TestApplicationOrderReceiptRefusalsAreSticky(t *testing.T) {
	actor, _ := applicationOrderReceiptsFixture()
	for _, receipt := range []orders.CheckpointOrderReceipt{
		{}, {Kind: 6}, {Kind: 255},
		{Kind: 3}, {Kind: 3, Segment: 3}, {Kind: 3, Segment: 1, Index: -1},
		{Kind: 4}, {Kind: 4, Segment: 3}, {Kind: 4, Segment: 2, Index: -1},
		{Kind: 5}, {Kind: 5, Preparation: 3},
		{Kind: 3, Segment: 1, Node: orders.Node{GoalSupplied: true}},
		{Kind: 4, Segment: 2, Node: orders.Node{QueuedIssue: true}},
	} {
		h := historyFixture(t, 1)
		before := h.digest
		a := h.BeginAttempt(1, h.NextSerial(), 0, (ClassicApplicationIntent{Kind: 3}).WriteCheckpoint)
		a.RecordOrder(actor, receipt)
		first := h.err
		if first == nil || a.operationCount != 0 {
			t.Fatalf("accepted malformed receipt %+v", receipt)
		}
		a.RecordOrder(actor, orders.CheckpointOrderReceipt{Kind: 1})
		h.Fail(errors.New("later diagnostic failure"))
		a.Finish(1, 4)
		if _, err := h.Snapshot(); err != first || h.digest != before || h.count != 0 || a.operationCount != 0 || a.operations.Cap() != 0 {
			t.Fatal("codec failure changed or advanced the history")
		}
	}
	for _, invalid := range []checkpoint.Allocation{{}, {Handle: 1}, {Serial: 1}} {
		h := historyFixture(t, 1)
		a := h.BeginAttempt(0, h.NextSerial(), 0, (ClassicApplicationIntent{Kind: 3}).WriteCheckpoint)
		a.RecordOrder(invalid, orders.CheckpointOrderReceipt{Kind: 1})
		a.Finish(1, 1)
		if _, err := h.Snapshot(); err == nil || h.count != 0 {
			t.Fatalf("accepted invalid actor %+v", invalid)
		}
	}
	var disabled *ApplicationAttempt
	if n := testing.AllocsPerRun(100, func() {
		disabled.RecordOrder(checkpoint.Allocation{}, orders.CheckpointOrderReceipt{Kind: 255})
	}); n != 0 {
		t.Fatalf("disabled receipt allocated %g", n)
	}
}

func TestApplicationOrderReceiptsConsumeActualValuesWithoutNormalization(t *testing.T) {
	h := historyFixture(t, 1)
	a := h.BeginAttempt(0, h.NextSerial(), 0, (ClassicApplicationIntent{Kind: 3}).WriteCheckpoint)
	actor, receipts := applicationOrderReceiptsFixture()
	r := receipts[3]
	// The codec is a receipt leaf: it does not recompute previous+add, nor
	// infer a count from requested values. The existing producer owns that.
	r.PreviousCount, r.Added, r.Node.Param2 = 5, 0, 19
	r.Node.RetailSubtype = []byte{1, 2}
	r.Node.RetailSubtypeWords16 = []uint16{3}
	r.Node.RetailSubtypeWords32 = []uint32{4}
	before := r
	a.RecordOrder(actor, r)
	if !reflect.DeepEqual(r, before) || h.err != nil || a.operationCount != 1 {
		t.Fatal("receipt codec changed an observed operand")
	}
	want := applicationCodecHex(t, `0a00 `+applicationOrderActorVector+`01 0200000000000000 05000000 00000000 `+applicationOrderNodePrefix+`13000000 `+applicationOrderNodeSuffix)
	if !bytes.Equal(a.operations.Bytes(), want) {
		t.Fatalf("actual counts/owner were normalized: %x", a.operations.Bytes())
	}
	r.Node.Param2 = 23
	r.Node.RetailSubtype[0], r.Node.RetailSubtypeWords16[0], r.Node.RetailSubtypeWords32[0] = 9, 10, 11
	if !bytes.Equal(a.operations.Bytes(), want) {
		t.Fatal("receipt retained a node or borrowed payload")
	}
	// Staging payloads do not enter the existing retained node schema.
	r.Node.Param2 = 19
	r.Node.RetailSubtypeCode, r.Node.RetailSubtypeUnitA = 7, 8
	a.RecordOrder(actor, r)
	if got := a.operations.Bytes(); !bytes.Equal(got[:len(want)], got[len(want):]) {
		t.Fatal("save staging changed retained operation bytes")
	}
	a.Finish(1, 4)
}
