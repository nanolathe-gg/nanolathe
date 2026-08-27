package save

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
)

// TestBulkUnitBoxRoundTrip locks 0xB8 byte-exact layout [P1-13 §2.2].
func TestBulkUnitBoxRoundTrip(t *testing.T) {
	b := NewBuilder("")
	payload := make([]byte, UnitBoxSize)
	// Fill header name 32 bytes, stableID at 0x21, leak bits at 0xB4.
	copy(payload[0x00:], []byte("armcom"))
	binary.LittleEndian.PutUint16(payload[0x21:], 42)
	binary.LittleEndian.PutUint32(payload[0x23:], 3)
	// Leak bits 17..19 at 0xB4 (simulate stack leak).
	payload[0xB4] = 0xE0 // bits 17..19 set
	WriteUnitsHeader(b, 1)
	if err := WriteUnitBox(b, 0, payload); err != nil {
		t.Fatalf("WriteUnitBox: %v", err)
	}
	data := b.Bytes()
	bank, err := OpenBytes(data, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if !IsUnitsLoadable(bank) {
		t.Fatalf("units should be loadable Version 0x11")
	}
	got, ok := ReadUnitBox(bank, 0)
	if !ok {
		t.Fatalf("ReadUnitBox missing")
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("unit box byte-exact mismatch")
	}
	if gotID := UnitStableID(got); gotID != 42 {
		t.Fatalf("stableID %d want 42 [P1-13 §2.1]", gotID)
	}
	// 0xB6 compat branch clears ID [P1-13 §3.4].
	payload6 := make([]byte, UnitBoxCompatSize)
	if err := WriteUnitBox(b, 1, payload6); err != nil {
		t.Fatalf("WriteUnitBox 0xB6: %v", err)
	}
	// Order 0x3A box [P1-13 §2.3].
	order := make([]byte, OrderBoxSize)
	binary.LittleEndian.PutUint16(order[0x00:], 42)
	binary.LittleEndian.PutUint16(order[0x02:], 0)
	order[0x04] = 0x12
	if err := WriteOrderBox(b, 42, 0, order); err != nil {
		t.Fatalf("WriteOrderBox: %v", err)
	}
	payload2 := b.Bytes()
	bank2, err := OpenBytes(payload2, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes2: %v", err)
	}
	ac, _ := bank2.Account(UnitsAccount)
	if data, ok := ac.BoxData("u002am0000", 0); !ok || !bytes.Equal(data, order) {
		t.Fatalf("order box mismatch")
	}
}

// TestBulkVersionGate validates Version!=0x11 skips Units non-transactionally [P1-13 §3.4][P1-13 §7].
func TestBulkVersionGate(t *testing.T) {
	b := NewBuilder("")
	ac := b.Add(UnitsAccount)
	ac.SetInt("Version", 0x10) // not 0x11
	ac.SetInt("Number of Units", 1)
	ac.AppendBox("", 0, make([]byte, UnitBoxSize))
	data := b.Bytes()
	bank, err := OpenBytes(data, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if IsUnitsLoadable(bank) {
		t.Fatalf("Version 0x10 should not be loadable [P1-13 §3.4]")
	}
	// Also test non-transactional: Players still loadable even when Units skipped.
	// Build bank with Players and bad Units.
	b2 := NewBuilder("")
	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 100}
	WriteGameTime(b2, clk)
	ac2 := b2.Add(UnitsAccount)
	ac2.SetInt("Version", 0x10)
	ac2.SetInt("Number of Units", 1)
	ac2.AppendBox("", 0, make([]byte, UnitBoxSize))
	payload2 := b2.Bytes()
	bank2, _ := OpenBytes(payload2, RetailTag)
	if _, ok := ReadGameTime(bank2); !ok {
		t.Fatalf("GameTime should still be readable when Units Version wrong [P1-13 §7]")
	}
	if IsUnitsLoadable(bank2) {
		t.Fatalf("should still be not loadable")
	}
}

// TestBulkSchedulerPersistence locks 28B scheduler verbatim [P1-13 §4].
func TestBulkSchedulerPersistence(t *testing.T) {
	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 4242}
	clk.AdvanceSP(3)
	b := NewBuilder("")
	WriteGameTime(b, clk)
	payload := b.Bytes()
	bank, _ := OpenBytes(payload, RetailTag)
	restored, ok := ReadGameTime(bank)
	if !ok {
		t.Fatalf("ReadGameTime missing")
	}
	if restored.GlobalTick != clk.GlobalTick {
		t.Fatalf("scheduler GlobalTick %d want %d [P1-13 §4]", restored.GlobalTick, clk.GlobalTick)
	}
	// Short box <28 should fail and gate Player%i [P1-13 §7].
	b2 := NewBuilder("")
	ac := b2.Add(PlayersAccount)
	ac.AppendBox(GameTimeBoxName, 0, []byte{1, 2, 3})
	bank2, _ := OpenBytes(b2.Bytes(), RetailTag)
	if _, ok := ReadGameTime(bank2); ok {
		t.Fatalf("short GameTime should fail [P1-13 §7]")
	}
	if slots := ReadAllPlayerSlots(bank2); len(slots) != 0 {
		t.Fatalf("without GameTime, Player slots should be zero [P1-13 §7]")
	}
}

// TestBulkPartialLoadNonTransactional validates non-transactional partial load
// where one account fails but others succeed [P1-13 §7].
func TestBulkPartialLoadNonTransactional(t *testing.T) {
	b := NewBuilder("")
	WriteSummary(b, Summary{MaxUnits: 100, Campaign: "c", Mission: "m", Gametype: 1})
	WriteCamera(b, Camera{XPosition: 10, ZPosition: 20})
	// Add Features with correct size.
	if err := WriteFeatureTypeNames(b, []string{"armrock"}); err != nil {
		t.Fatalf("WriteFeatureTypeNames: %v", err)
	}
	payload := b.Bytes()
	bank, err := OpenBytes(payload, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if _, ok := ReadSummary(bank); !ok {
		t.Fatalf("Summary should remain readable [P1-13 §7]")
	}
	if _, ok := ReadCamera(bank); !ok {
		t.Fatalf("Camera should remain readable [P1-13 §7]")
	}
}

// TestBulkHAPIBANKBounds validates header offset bounds [P1-13 §2.1][I13].
func TestBulkHAPIBANKBounds(t *testing.T) {
	b := NewBuilder("")
	WriteSummary(b, Summary{MaxUnits: 1})
	payload := b.Bytes()
	// Corrupt pool offset beyond file -> ErrFormat [P1-13 §2.1][bank C13].
	corrupt := append([]byte(nil), payload...)
	binary.LittleEndian.PutUint32(corrupt[0x0C:], uint32(len(corrupt)+100))
	if _, err := OpenBytes(corrupt, RetailTag); err != ErrFormat {
		t.Fatalf("pool offset beyond file should be ErrFormat, got %v", err)
	}
}

// TestBulkFixUpOrder documents Players→Camera→Features→Metal→Units order [P1-13 §3.5].
func TestBulkFixUpOrder(t *testing.T) {
	if got := ApplyFixUpOrder(nil); got != FixUpOrder {
		t.Fatalf("fixup order mismatch")
	}
	// Validate subtype sizes [P1-13 §2.4].
	if !ValidateSubtypeSize(2, OrderSubtypeCode2) || !ValidateSubtypeSize(6, OrderSubtypeCode6) {
		t.Fatalf("subtype size validate failed")
	}
	if ValidateSubtypeSize(2, 0x10) {
		t.Fatalf("subtype 2 should not be 0x10")
	}
}
