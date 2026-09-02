package frame

import "testing"

// A frame reused for the next tick must not present the previous tick's player
// rows: every slot is rewritten by the publisher each tick, and a slot whose
// record has gone away publishes its zero value [07 R-HUD-04 §1][I6].
func TestResetClearsPlayerRows(t *testing.T) {
	var f Frame
	f.Players[3] = PlayerRow{Present: true, Name: "Gone", Kills: 9, Rank: 3}
	f.Reset()
	if f.Players != ([PlayerRowSlots]PlayerRow{}) {
		t.Fatalf("Reset left player rows behind: %+v", f.Players)
	}
}

// The buffer resets the slot it hands the writer, so a publisher that skips a
// slot cannot leak the other slot's row into the committed frame.
func TestBeginWriteClearsPlayerRows(t *testing.T) {
	b := NewBuffer()
	w := b.BeginWrite()
	w.Players[0] = PlayerRow{Present: true, Name: "First", Kills: 4}
	if err := b.Publish(1); err != nil {
		t.Fatal(err)
	}
	w = b.BeginWrite()
	if w.Players[0] != (PlayerRow{}) {
		t.Fatalf("second write slot still carries a row: %+v", w.Players[0])
	}
	// The committed frame keeps its own copy.
	if got := b.Current().Players[0]; got.Name != "First" || got.Kills != 4 {
		t.Fatalf("committed rows were disturbed by the next BeginWrite: %+v", got)
	}
}
