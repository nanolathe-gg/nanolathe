package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestPublishedPropellerSpinChangesOnlyChildCPUTransform is a committed-view
// CPU render check. The model body keeps its yaw/pitch/base-roll state while
// the already-selected child receives the published spin before its strict
// expiry deadline [03 §5.2][06 R-WFX-01 §4].
func TestPublishedPropellerSpinChangesOnlyChildCPUTransform(t *testing.T) {
	m := &model.Model{
		Root: 0,
		Pieces: []model.Piece{
			{Name: "body", Parent: -1},
			{Name: "propeller", Parent: 0},
		},
	}
	base := []model.PieceState{{RotZ: 77}, {}}
	states := BuildProjectilePieceStates(m, base, 0, 0)
	v := frame.ProjectileView{Roll: 0x4000, Propeller: true, ExpiryTick: 9}
	FoldPublishedPropellerSpin(states, 1, v, 8)
	if states[0].RotZ != 77 || states[0].RotY != halfCircle || states[0].RotX != halfCircle {
		t.Fatalf("body orientation changed: %+v", states[0])
	}
	if states[1].RotZ != 0x4000 || states[1].RotY != 0 || states[1].RotX != 0 {
		t.Fatalf("child orientation = %+v, want only published roll", states[1])
	}
	child := model.Compose(m, states, 1)
	got := child.Apply([3]numeric.Fixed{numeric.FixedFromInt(1), 0, 0})
	withoutSpin := BuildProjectilePieceStates(m, base, 0, 0)
	baseline := model.Compose(m, withoutSpin, 1).Apply([3]numeric.Fixed{numeric.FixedFromInt(1), 0, 0})
	if got == baseline {
		t.Fatalf("child CPU vertex = %v before and after committed spin", got)
	}

	deadline := BuildProjectilePieceStates(m, base, 0, 0)
	FoldPublishedPropellerSpin(deadline, 1, v, 9)
	if deadline[1].RotZ != 0 {
		t.Fatalf("expiry equality spun child by %#04x, want strict suppression", deadline[1].RotZ)
	}
	nonPropeller := BuildProjectilePieceStates(m, base, 0, 0)
	v.Propeller = false
	FoldPublishedPropellerSpin(nonPropeller, 1, v, 8)
	if nonPropeller[1].RotZ != 0 {
		t.Fatalf("non-propeller child spun by %#04x", nonPropeller[1].RotZ)
	}
}
