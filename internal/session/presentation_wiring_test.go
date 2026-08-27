package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
)

func TestCreateAndBindServicesOwnsPublicationState(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 17, 19)
	if s == nil || s.publication == nil || s.publication.events == nil || s.publication.effects == nil {
		t.Fatal("canonical session wiring did not initialize publication state")
	}
	first := s.publication
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	if s.publication != first {
		t.Fatal("re-binding replaced the session-owned publication state")
	}
	if first.events != s.publication.events || first.effects != s.publication.effects {
		t.Fatal("re-binding replaced a publication owner")
	}
	if !first.events.EmitImpact(frame.Event{Tick: 1, Graphic: "staged"}) {
		t.Fatal("stage publication event")
	}
	s.ensurePublicationState()
	if len(s.publication.events.Events()) != 1 {
		t.Fatal("ensurePublicationState discarded staged events")
	}
}
