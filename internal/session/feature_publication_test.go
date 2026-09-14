package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// The per-slot cache must retain a view exactly when its inputs are unchanged
// and rebuild it the publication after any input moves; either way the frame
// carries what a full rebuild would.
func TestPublishFeaturesRetainsUnchangedViewsPerSlot(t *testing.T) {
	terrain := strictMinimalTerrain()
	def := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"},
		Filename:         "trees",
		SeqNameDie:       "die",
		FootprintX:       1,
		FootprintZ:       1,
	}
	svc := features.NewService(terrain, nil, nil, nil)
	if svc.PlaceAt(1, 1, def) == nil || svc.PlaceAt(2, 2, def) == nil || svc.PlaceAt(3, 3, def) == nil {
		t.Fatal("place features")
	}
	s := &Session{Snapshot: frame.NewBuffer(), Features: svc}
	publication := s.ensurePublicationState()

	rebuilt := func(tick uint32) (int, *frame.Frame) {
		f := s.Snapshot.BeginWrite()
		n := s.publishFeatures(f, publication)
		if err := s.Snapshot.Publish(tick); err != nil {
			t.Fatal(err)
		}
		return n, f
	}
	if n, _ := rebuilt(1); n != 3 {
		t.Fatalf("first slot built %d views, want 3", n)
	}
	if n, _ := rebuilt(2); n != 3 {
		t.Fatalf("second slot built %d views, want 3 (a slot never written has nothing to retain)", n)
	}
	n, f := rebuilt(3)
	if n != 0 {
		t.Fatalf("unchanged features rebuilt %d views on a slot already holding them, want 0", n)
	}
	if len(f.Features) != 3 || f.Features[1].CX != 2 || f.Features[1].DefName != "tree" {
		t.Fatalf("retained publication = %#v", f.Features)
	}

	// Mutating one instance rebuilds that element on both slots, once each,
	// and the rebuilt view matches a fresh build from the live inputs.
	inst := svc.InstanceAt(2, 2)
	inst.IsBurning = true
	inst.Status |= 1
	for tick := uint32(4); tick <= 5; tick++ {
		n, f := rebuilt(tick)
		if n != 1 {
			t.Fatalf("tick %d rebuilt %d views after one mutation, want 1", tick, n)
		}
		var want frame.FeatureView
		var in featureViewInputs
		fillFeatureInputs(&in, inst, publication)
		writeFeatureView(&want, &in)
		if f.Features[1] != want || !f.Features[1].IsBurning || f.Features[1].Status != 1 {
			t.Fatalf("tick %d rebuilt view = %#v, want %#v", tick, f.Features[1], want)
		}
	}
	if n, _ := rebuilt(6); n != 0 {
		t.Fatalf("settled mutation still rebuilt %d views", n)
	}

	// Removing a feature shifts the later ones down: those positions rebuild
	// because their identity changed, never silently keeping the old view.
	svc.RemoveFeatureAt(1, 1, features.CauseDead)
	svc.SequenceFrames = nil
	n, f = rebuilt(7)
	if len(f.Features) == 3 && n < 1 {
		t.Fatalf("removal kept every view: rebuilt %d of %d", n, len(f.Features))
	}
	for i := range f.Features {
		src := svc.InstanceAt(int(f.Features[i].CX), int(f.Features[i].CZ))
		if src == nil {
			t.Fatalf("view %d names an absent feature at (%d,%d)", i, f.Features[i].CX, f.Features[i].CZ)
		}
		var want frame.FeatureView
		var in featureViewInputs
		fillFeatureInputs(&in, src, publication)
		writeFeatureView(&want, &in)
		if f.Features[i] != want {
			t.Fatalf("view %d after removal = %#v, want %#v", i, f.Features[i], want)
		}
	}
}
