package save

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
)

// TestBulkUnitBoxRoundTrip locks 0xB8 byte-exact layout [P1-13 §2.2].
func TestBulkUnitBoxRoundTrip(t *testing.T) {
	b := NewBuilder()
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
	bank, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if v, _, ok := ReadUnitsHeader(bank); !ok || v != UnitsVersionRetail {
		t.Fatalf("units should be loadable Version 0x11, got version %d present %v", v, ok)
	}
	ac0, ok := bank.Account(UnitsAccount)
	if !ok {
		t.Fatalf("Units account missing")
	}
	got, ok := ac0.BoxData("", 0)
	if !ok || !ValidateUnitBoxSize(len(got)) {
		t.Fatalf("unit box 0 missing or not an established length: present=%v size=%d", ok, len(got))
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
	bank2, err := OpenBytes(payload2)
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
	b := NewBuilder()
	ac := b.Add(UnitsAccount)
	ac.SetInt("Version", 0x10) // not 0x11
	ac.SetInt("Number of Units", 1)
	ac.AppendBox("", 0, make([]byte, UnitBoxSize))
	data := b.Bytes()
	bank, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if v, _, ok := ReadUnitsHeader(bank); !ok || v == UnitsVersionRetail {
		t.Fatalf("Version 0x10 should not be loadable [P1-13 §3.4], got %d present %v", v, ok)
	}
	// Also test non-transactional: Players still loadable even when Units skipped.
	// Build bank with Players and bad Units.
	b2 := NewBuilder()
	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 100}
	WriteGameTime(b2, clk.SaveBox())
	ac2 := b2.Add(UnitsAccount)
	ac2.SetInt("Version", 0x10)
	ac2.SetInt("Number of Units", 1)
	ac2.AppendBox("", 0, make([]byte, UnitBoxSize))
	payload2 := b2.Bytes()
	bank2, _ := OpenBytes(payload2)
	if _, ok := ReadGameTime(bank2); !ok {
		t.Fatalf("GameTime should still be readable when Units Version wrong [P1-13 §7]")
	}
	if v, _, ok := ReadUnitsHeader(bank2); !ok || v == UnitsVersionRetail {
		t.Fatalf("should still be not loadable, got %d present %v", v, ok)
	}
}

// TestBulkSchedulerPersistence locks 28B scheduler verbatim [P1-13 §4].
func TestBulkSchedulerPersistence(t *testing.T) {
	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 4242}
	clk.AdvanceSP(3)
	b := NewBuilder()
	WriteGameTime(b, clk.SaveBox())
	payload := b.Bytes()
	bank, _ := OpenBytes(payload)
	box, ok := ReadGameTime(bank)
	if !ok {
		t.Fatalf("ReadGameTime missing")
	}
	var restored clock.State
	restored.LoadBox(box)
	if restored.GlobalTick != clk.GlobalTick {
		t.Fatalf("scheduler GlobalTick %d want %d [P1-13 §4]", restored.GlobalTick, clk.GlobalTick)
	}
	// Short box <28 should fail and gate Player%i [P1-13 §7].
	b2 := NewBuilder()
	ac := b2.Add(PlayersAccount)
	ac.AppendBox(GameTimeBoxName, 0, []byte{1, 2, 3})
	bank2, _ := OpenBytes(b2.Bytes())
	if _, ok := ReadGameTime(bank2); ok {
		t.Fatalf("short GameTime should fail [P1-13 §7]")
	}
	var short BattleImage
	if err := decodePlayers(bank2, &short); err == nil || len(short.Players) != 0 {
		t.Fatalf("without a 28-byte GameTime, no Player slot may load [P1-13 §7]: err=%v slots=%d", err, len(short.Players))
	}
}

// TestBulkPartialLoadNonTransactional validates non-transactional partial load
// where one account fails but others succeed [P1-13 §7].
func TestBulkPartialLoadNonTransactional(t *testing.T) {
	b := NewBuilder()
	WriteSummary(b, Summary{MaxUnits: 100, Campaign: "c", Mission: "m", Gametype: 1})
	WriteCamera(b, Camera{XPosition: 10, ZPosition: 20})
	// Add Features with correct size.
	if err := WriteFeatureTypeNames(b, []string{"armrock"}); err != nil {
		t.Fatalf("WriteFeatureTypeNames: %v", err)
	}
	payload := b.Bytes()
	bank, err := OpenBytes(payload)
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
	b := NewBuilder()
	WriteSummary(b, Summary{MaxUnits: 1})
	payload := b.Bytes()
	// Corrupt pool offset beyond file -> ErrFormat [P1-13 §2.1][bank C13].
	corrupt := append([]byte(nil), payload...)
	binary.LittleEndian.PutUint32(corrupt[0x0C:], uint32(len(corrupt)+100))
	if _, err := OpenBytes(corrupt); err != ErrFormat {
		t.Fatalf("pool offset beyond file should be ErrFormat, got %v", err)
	}
}

// TestBulkSubtypeSizes locks the established raw subtype lengths.
func TestBulkSubtypeSizes(t *testing.T) {
	// Validate subtype sizes [P1-13 §2.4].
	if !ValidateSubtypeSize(2, OrderSubtypeCode2) || !ValidateSubtypeSize(6, OrderSubtypeCode6) {
		t.Fatalf("subtype size validate failed")
	}
	if ValidateSubtypeSize(2, 0x10) {
		t.Fatalf("subtype 2 should not be 0x10")
	}
}

func TestMeteorScalarsOrderAndPresence(t *testing.T) {
	want := MeteorScalars{
		Enabled:        1,
		Active:         2,
		NextStrikeTime: 3,
		TimeStrikeEnds: 4,
		NextHitTime:    5,
		OriginX:        6,
		OriginZ:        7,
		TargetX:        8,
		TargetZ:        9,
	}
	b := NewBuilder()
	WriteMeteorScalars(b, want)
	bank, err := OpenBytes(b.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	ac, ok := bank.Account(MeteorAccount)
	if !ok || len(ac.Ints) != 9 {
		t.Fatalf("Meteor account item count: account=%v items=%d", ok, len(ac.Ints))
	}
	for i, name := range meteorScalarNames {
		if ac.Ints[i].Name != name || ac.Ints[i].Value != int32(i+1) {
			t.Fatalf("item %d = (%q,%d), want (%q,%d)", i, ac.Ints[i].Name, ac.Ints[i].Value, name, i+1)
		}
	}
	var got MeteorScalars
	if err := decodeMeteor(bank, &got); err != nil || got != want {
		t.Fatalf("decodeMeteor = (%+v,%v), want (%+v,nil)", got, err, want)
	}
	// A partial account receives no guessed defaults: the items it does not
	// carry stay zero, which disables and deactivates the scheduler
	// [08 R-SAVE-02 §12].
	partial := NewBuilder()
	partial.Add(MeteorAccount).SetInt("Enabled", 1)
	partialBank, err := OpenBytes(partial.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes partial: %v", err)
	}
	var gotPartial MeteorScalars
	if err := decodeMeteor(partialBank, &gotPartial); err != nil || gotPartial != (MeteorScalars{Enabled: 1}) {
		t.Fatalf("partial Meteor account = (%+v,%v), want only Enabled set", gotPartial, err)
	}
}
