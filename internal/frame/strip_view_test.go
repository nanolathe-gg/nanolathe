package frame

import "testing"

// TestResetClearsTheStripChannel locks the publication contract for the strip
// channel: every committed frame carries the strip records of its own tick and
// none of the previous one's. The channel is rebuilt from the live table at
// every publication [03 R-STRIP-01 §2][I6], so a slot that kept last tick's
// records would draw sub-records the sweep has already destroyed.
//
// The capacity is retained, like every other slice on the frame: the mirror
// runs once a tick for the life of a battle.
func TestResetClearsTheStripChannel(t *testing.T) {
	f := &Frame{}
	f.Strips = append(f.Strips,
		StripView{Strip: 4, Family: StripFamilyVentSteam, Bank: "fx", Entry: "smoke 1"},
		StripView{Strip: 9, Family: StripFamilySmokePuff, Bank: "fx", Entry: "smoke 2"},
	)
	capacity := cap(f.Strips)

	f.Reset()

	if len(f.Strips) != 0 {
		t.Fatalf("Reset left %d strip records on the frame", len(f.Strips))
	}
	if cap(f.Strips) != capacity {
		t.Fatalf("Reset dropped the strip channel's capacity: %d, want %d", cap(f.Strips), capacity)
	}
	// The retained backing array must not still name the old art: a later
	// re-slice would otherwise resurrect an identity the sweep retired.
	for _, v := range f.Strips[:capacity] {
		if v.Entry != "" || v.Family != StripFamilyNone {
			t.Fatalf("Reset left %+v in the retained backing array", v)
		}
	}
}
