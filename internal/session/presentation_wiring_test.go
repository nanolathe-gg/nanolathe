package session

import "testing"

func TestCreateAndBindServicesOwnsPresentationCollector(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 17, 19)
	if s == nil || s.Presentation == nil {
		t.Fatal("canonical session wiring did not initialize presentation collector")
	}
	first := s.Presentation
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	if s.Presentation != first {
		t.Fatal("re-binding replaced the session-owned presentation collector")
	}
}
