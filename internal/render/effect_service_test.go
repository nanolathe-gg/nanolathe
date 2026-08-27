package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// stubEffectPool captures admitted views for EffectService tests.
type stubEffectPool struct {
	views   []frame.EffectView
	updates int
}

func (p *stubEffectPool) Len() int      { return len(p.views) }
func (p *stubEffectPool) Update(uint32) { p.updates++ }
func (p *stubEffectPool) AppendView(v frame.EffectView) bool {
	p.views = append(p.views, v)
	return true
}
func (p *stubEffectPool) SnapshotViews() []frame.EffectView { return p.SnapshotViewsInto(nil) }
func (p *stubEffectPool) SnapshotViewsInto(out []frame.EffectView) []frame.EffectView {
	out = out[:0]
	out = append(out, p.views...)
	return out
}
func (p *stubEffectPool) RemoveMatching(source, target pool.Handle, kind string) {
	for i, v := range p.views {
		if v.Kind == kind && v.Source == source && (v.Target == target || target == 0 || v.Target == 0) {
			copy(p.views[i:], p.views[i+1:])
			p.views = p.views[:len(p.views)-1]
			return
		}
	}
}

// TestNanolatheBeamStripAndGeometryFlag locks the nanolathe draw gate [03 §5.5]:
// a construction/reclaim nanolathe event whose producer is the beam-family
// strip-6 identity and whose geometry is authoritative survives admission with
// Strip 6 and NanolatheGeometryKnown true, so the client's strip-6 draw branch
// fires instead of skipping the beam. The event is routed through the same
// RoutedEvent step the event buffer applies at admission [03 §5.5].
func TestNanolatheBeamStripAndGeometryFlag(t *testing.T) {
	owner := &stubEffectPool{}
	svc := NewEffectServiceWithPool(2, owner)
	ev := frame.RoutedEvent(Event{
		ID: 1, Sequence: 1, Tick: 4, Kind: KindNanolathe,
		Producer: frame.ProducerBeam, NanolatheGeometryKnown: true,
	})
	svc.Advance(4, []Event{ev})
	got := svc.Snapshot()
	if len(got) != 1 {
		t.Fatalf("nanolathe effect publication = %+v", got)
	}
	if got[0].Strip != int8(frame.StripBeam) {
		t.Fatalf("nanolathe strip = %d, want %d", got[0].Strip, frame.StripBeam)
	}
	if !got[0].NanolatheGeometryKnown {
		t.Fatal("nanolathe geometry flag was dropped")
	}
}
