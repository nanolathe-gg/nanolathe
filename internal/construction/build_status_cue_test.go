package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

// statusRaise is one (kind, text) the shared status emitter received
// [04 R-ORD-01 §1].
type statusRaise struct {
	Kind uint8
	Text string
}

// captureStatus installs a presentation adapter on the unit's queue that
// records every status raise. The adapter is the seam orders.NotifyStatus
// reaches, so this observes exactly what the session would stage as a
// committed-frame status event [04 R-ORD-01 §1][03 §8.3].
func captureStatus(u *units.Unit, out *[]statusRaise) {
	q := orders.QueueForUnit(u)
	binding := q.Binding()
	if binding == nil {
		binding = &orders.QueueBinding{}
	}
	binding.Presentation = &orders.PresentationAdapter{
		Ready: func() bool { return true },
		Status: func(_ *units.Unit, kind uint8, text string) bool {
			*out = append(*out, statusRaise{Kind: kind, Text: text})
			return true
		},
	}
	q.SetBinding(binding)
}

func hasRaise(raises []statusRaise, kind uint8, text string) bool {
	for _, r := range raises {
		if r.Kind == kind && r.Text == text {
			return true
		}
	}
	return false
}

// TestFactoryPhase4RaisesUnitComplete locks the factory's "unit ready" voice:
// `BuildingBuild` phase 4 emits status kind 8 with no text, so the slot's
// static default caption `Nanolathe Complete` stands [04 R-ORD-01 §5]
// [03 §8.3]. The raise is on the BUILDER, which is what makes the factory's own
// sound category supply the variant [05 "the build-order caption census"].
func TestFactoryPhase4RaisesUnitComplete(t *testing.T) {
	svc, factory, _, node := factoryWithAttachedProduct(t)
	var raises []statusRaise
	captureStatus(factory, &raises)

	node.Phase = uint8(State4)
	svc.handleState4(factory, node, 100)

	if !hasRaise(raises, 8, "") {
		t.Fatalf("factory phase 4 raises = %v, want kind 8 with no text [04 R-ORD-01 §5]", raises)
	}
}

// TestFactoryStopRaisesCant locks `BuildingBuild`'s satisfied-bit-3 arm: status
// 7 with `Construction stopped` [04 R-ORD-01 §5][05 C22].
func TestFactoryStopRaisesCant(t *testing.T) {
	svc, factory, _, node := factoryWithAttachedProduct(t)
	var raises []statusRaise
	captureStatus(factory, &raises)

	svc.handleStop(factory, node, 100)

	if !hasRaise(raises, 7, "Construction stopped") {
		t.Fatalf("stop raises = %v, want kind 7 \"Construction stopped\" [04 R-ORD-01 §5]", raises)
	}
}
