package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
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

	got := [][]uint32{
		effectIDs(effectViewsForStrip(effects, int8(frame.StripShockwave))),
		effectIDs(effectViewsForStrip(effects, int8(frame.StripBeam))),
		effectIDs(unstrippedEffectViews(effects)),
		effectIDs(effectViewsForStrip(effects, int8(frame.StripLightning))),
		effectIDs(effectViewsForStrip(effects, int8(frame.StripSmoke))),
	}
	want := [][]uint32{{20, 21}, {60, 61}, {70}, {71}, {90}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staged effect IDs = %v, want %v", got, want)
	}
	if got := effectViewsForStrip(effects, 0); got != nil {
		t.Fatalf("empty strip selection = %#v, want nil", got)
	}
	if got := unstrippedEffectViews([]frame.EffectView{{ID: 2, Strip: int8(frame.StripBeam)}}); got != nil {
		t.Fatalf("empty fixed selection = %#v, want nil", got)
	}
}

func effectIDs(effects []frame.EffectView) []uint32 {
	ids := make([]uint32, len(effects))
	for i := range effects {
		ids[i] = effects[i].ID
	}
	return ids
}
