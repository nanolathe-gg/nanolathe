package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/snapshot"
)

func TestBuildEffectDrawsCopiesAdmissionOrderAndMetadata(t *testing.T) {
	in := []snapshot.EffectView{
		{ID: 4, EventSeq: 99, Kind: "impact", Graphic: "explosion", SeqA: 2, Light: true},
		{ID: 5, EventSeq: 100, Kind: "smoke", Graphic: "smoke", SeqB: 3},
	}
	out := BuildEffectDraws(in)
	in[0].Kind = "mutated"
	if len(out) != 2 || out[0].Kind != "impact" || out[0].EventSeq != 99 || out[1].FrameB != 3 {
		t.Fatalf("effect metadata/order lost: %+v", out)
	}
}

func TestBuildEffectDrawsEmptyDoesNotSynthesize(t *testing.T) {
	if got := BuildEffectDraws(nil); got != nil {
		t.Fatalf("nil effects should remain nil, got %+v", got)
	}
}
