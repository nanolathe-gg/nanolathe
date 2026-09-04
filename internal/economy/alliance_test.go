package economy

import "testing"

// TestAllianceRowIsElevenBytesWithForcedSelf locks the persisted shape of the
// first alliance row: eleven bytes indexed by player slot, non-zero meaning
// allied, the self column forced to 1, and column 10 — the neutral row nothing
// occupies — always zero [05 R-SHARE-01 §1] [05 "Player slot"].
func TestAllianceRowIsElevenBytesWithForcedSelf(t *testing.T) {
	var p Player
	p.Allies[3] = true
	row := p.AllianceRow(1)
	if len(row) != AllianceRowBytes || AllianceRowBytes != 11 {
		t.Fatalf("row width %d, want 11", len(row))
	}
	if row[1] != 1 {
		t.Fatalf("self column = %d, want the forced 1", row[1])
	}
	if row[3] != 1 {
		t.Fatalf("declared alliance lost: %v", row)
	}
	if row[0] != 0 || row[10] != 0 {
		t.Fatalf("undeclared columns set: %v", row)
	}
}

// TestSetAllianceRowAcceptsAnyNonZero locks the restore side: any non-zero
// byte is "allied", the self column is forced, and column 10 is dropped
// [05 R-SHARE-01 §1] [08 "Player records"].
func TestSetAllianceRowAcceptsAnyNonZero(t *testing.T) {
	var p Player
	p.Allies[8] = true // must be cleared by a row that does not declare it
	var row [AllianceRowBytes]byte
	row[2] = 0x7f
	row[10] = 1
	p.SetAllianceRow(5, row)
	if !p.Allies[2] {
		t.Fatalf("non-zero byte is allied: %v", p.Allies)
	}
	if !p.Allies[5] {
		t.Fatalf("self column not forced: %v", p.Allies)
	}
	if p.Allies[8] {
		t.Fatalf("stale declaration survived the restore: %v", p.Allies)
	}
}

// TestServiceAllianceRowRoundTrip locks that row i belongs to slot i through
// the Service-level seam the save projection uses [08 "Player records"].
func TestServiceAllianceRowRoundTrip(t *testing.T) {
	var s Service
	s.Players[0].Allies[1] = true
	s.Players[1].Allies[0] = true
	rows := [10][AllianceRowBytes]byte{}
	for i := range rows {
		rows[i] = s.AllianceRow(i)
	}
	var restored Service
	for i := range rows {
		restored.SetAllianceRow(i, rows[i])
	}
	if !restored.DeclaresAlliance(0, 1) || !restored.DeclaresAlliance(1, 0) {
		t.Fatal("ally pair did not survive the row round trip")
	}
	if restored.DeclaresAlliance(0, 2) || restored.DeclaresAlliance(2, 0) {
		t.Fatal("hostility did not survive the row round trip")
	}
	if s.AllianceRow(-1) != ([AllianceRowBytes]byte{}) || s.AllianceRow(10) != ([AllianceRowBytes]byte{}) {
		t.Fatal("an out-of-range slot must report the zero row")
	}
}
