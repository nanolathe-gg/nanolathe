package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

func TestEffectViewsStageAtCommittedStripBarriers(t *testing.T) {
	// The composer consumes these groups in barrier order: strip 2, strip 6,
	// fixed/unstripped, strip 7, then strip 9. Selection must retain admission
	// order within each group [03 §1][03 R-STRIP-01 §1–§3].
	effects := []frame.EffectView{
		{ID: 20, Strip: int8(frame.StripShockwave)},
		{ID: 60, Strip: int8(frame.StripBeam)},
		{ID: 70, Strip: -1}, // int8(frame.StripUnknown), the published sentinel
		{ID: 71, Strip: int8(frame.StripLightning)},
		{ID: 90, Strip: int8(frame.StripSmoke)},
		{ID: 21, Strip: int8(frame.StripShockwave)},
		{ID: 61, Strip: int8(frame.StripBeam)},
	}

	c := &Client{}
	cur := &frame.Frame{Tick: 1, Effects: effects}

	got := [][]uint32{
		effectIDs(c.effectViewsForStrip(cur, int8(frame.StripShockwave))),
		effectIDs(c.effectViewsForStrip(cur, int8(frame.StripBeam))),
		effectIDs(c.unstrippedEffectViews(cur)),
		effectIDs(c.effectViewsForStrip(cur, int8(frame.StripLightning))),
		effectIDs(c.effectViewsForStrip(cur, int8(frame.StripSmoke))),
	}
	want := [][]uint32{{20, 21}, {60, 61}, {70}, {71}, {90}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staged effect IDs = %v, want %v", got, want)
	}
	if got := c.effectViewsForStrip(cur, 0); len(got) != 0 {
		t.Fatalf("empty strip selection = %#v, want none", got)
	}

	// Buckets are reused across frames, so a later frame must not inherit the
	// previous one's records. A strip that emptied has to come back empty.
	next := &frame.Frame{Tick: 2, Effects: []frame.EffectView{{ID: 2, Strip: int8(frame.StripBeam)}}}
	if got := effectIDs(c.effectViewsForStrip(next, int8(frame.StripBeam))); !reflect.DeepEqual(got, []uint32{2}) {
		t.Fatalf("second frame beam strip = %v, want [2]", got)
	}
	for _, strip := range []int8{int8(frame.StripShockwave), int8(frame.StripLightning), int8(frame.StripSmoke)} {
		if got := c.effectViewsForStrip(next, strip); len(got) != 0 {
			t.Fatalf("strip %d carried %d record(s) over from the previous frame", strip, len(got))
		}
	}
	if got := c.unstrippedEffectViews(next); len(got) != 0 {
		t.Fatalf("fixed pool carried %d record(s) over from the previous frame", len(got))
	}
}

func effectIDs(effects []frame.EffectView) []uint32 {
	ids := make([]uint32, len(effects))
	for i := range effects {
		ids[i] = effects[i].ID
	}
	return ids
}
