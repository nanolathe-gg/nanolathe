package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

func TestPublishSnapshotCopiesFeatureRuntimeStateIndependentlyOfEventName(t *testing.T) {
	terrain := strictMinimalTerrain()
	def := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "runtime-sprite"},
		Filename:         "trees",
		SeqNameDie:       "die",
		SeqNameDieShad:   "dieshad",
		FootprintX:       1,
		FootprintZ:       1,
	}
	svc := features.NewService(terrain, nil, nil, nil)
	if svc.PlaceAt(2, 2, def) == nil {
		t.Fatal("place resting sprite")
	}
	s := &Session{Snapshot: frame.NewBuffer(), Features: svc}
	s.publishSnapshot(1)
	first := s.Snapshot.Current()
	if first == nil || len(first.Features) != 1 {
		t.Fatalf("resting publication = %#v", first)
	}
	if first.Features[0].RuntimeLive || first.Features[0].ShadowEnabled || first.Features[0].EventSeqName != "" {
		t.Fatalf("resting feature published runtime state %#v", first.Features[0])
	}

	svc.SequenceFrames = func(*content.FeatureDef, uint8) []int32 { return []int32{4} }
	svc.ShadowSequenceResolved = func(*content.FeatureDef, string) bool { return true }
	svc.RemoveFeatureAt(2, 2, features.CauseDead)
	s.publishSnapshot(2)
	second := s.Snapshot.Current()
	if second == nil || len(second.Features) != 1 {
		t.Fatalf("runtime publication = %#v", second)
	}
	got := second.Features[0]
	if !got.RuntimeLive || !got.ShadowEnabled || got.EventSeqName != "die" || got.EventSeqNameShad != "dieshad" {
		t.Fatalf("runtime feature publication = %#v, want attached event state", got)
	}
	if first.Features[0].RuntimeLive || first.Features[0].ShadowEnabled {
		t.Fatal("later runtime mutation changed the immutable first frame")
	}
}
