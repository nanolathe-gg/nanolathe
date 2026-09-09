package session

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func featurePublicationFixture(t testing.TB, n int) *Session {
	t.Helper()
	const side = 64
	attrs := make([]formats.TNTAttribute, side*side)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	terrain := &world.Terrain{CellW: side, CellH: side, Plot: world.ExpandPlot(attrs, side, side)}
	service := features.NewService(terrain, nil, nil, nil)
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "publication-fixture"}, FootprintX: 1, FootprintZ: 1, Damage: 100, Filename: "fixture", SeqName: "rest"}
	// Deliberately reverse insertion: presentation still walks row/anchor order.
	for i := n - 1; i >= 0; i-- {
		inst := service.PlaceAt(i%side, i/side, def)
		if inst == nil {
			t.Fatalf("place feature %d", i)
			return nil
		}
		inst.Heading = uint16(i)
	}
	return &Session{Snapshot: frame.NewBuffer(), Features: service}
}

func BenchmarkPublishFeatures(b *testing.B) {
	for _, n := range []int{0, 1024, 4096} {
		b.Run(fmt.Sprintf("features=%d", n), func(b *testing.B) {
			s := featurePublicationFixture(b, n)
			for tick := uint32(1); tick <= 3; tick++ {
				s.publishSnapshot(tick)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.publishSnapshot(uint32(i + 4))
			}
		})
	}
}

// Ordering and owned field copies are publication contracts [03 §2.4][I6].
func TestFeaturePublicationOrderedSnapshot(t *testing.T) {
	s := featurePublicationFixture(t, 128)
	s.publishSnapshot(1)
	first := s.Snapshot.Current()
	if len(first.Features) != 128 {
		t.Fatalf("published %d features", len(first.Features))
	}
	for i, feature := range first.Features {
		if feature.CX != int32(i%64) || feature.CZ != int32(i/64) || feature.Heading != uint16(i) {
			t.Fatalf("feature %d is out of anchor order: %+v", i, feature)
		}
	}
	before, err := json.Marshal(first.Features)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("authored feature snapshot sha256 %x", sha256.Sum256(before))
	s.Features.InstanceAt(0, 0).Heading = 300
	s.Features.InstanceAt(0, 0).Def.NoDrawUnderGray = true
	s.publishSnapshot(2)
	after, err := json.Marshal(first.Features)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("live mutation or next publication changed the prior frame")
	}
	current := s.Snapshot.Current()
	if current.Features[0].Heading != 300 || !current.Features[0].NoDrawUnderGray {
		t.Fatal("next frame missed live changes")
	}

	// Removing an anchor must invalidate the cached order and shrink the view,
	// even though publication reuses a formerly larger scratch buffer.
	s.Features.RemoveFeatureAt(0, 0, features.CauseDead)
	s.publishSnapshot(3)
	if got := s.Snapshot.Current().Features; len(got) != 127 || got[0].CX != 1 || got[0].CZ != 0 {
		t.Fatal("publication retained a removed anchor")
	}
}

func TestFeaturePublicationSteadyStateAllocations(t *testing.T) {
	s := featurePublicationFixture(t, 128)
	for tick := uint32(1); tick <= 3; tick++ {
		s.publishSnapshot(tick)
	}
	tick := uint32(4)
	if got := testing.AllocsPerRun(20, func() { s.publishSnapshot(tick); tick++ }); got != 0 {
		t.Fatalf("feature-only steady-state publication allocated %v times", got)
	}
}
