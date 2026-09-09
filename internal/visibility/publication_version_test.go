package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestPresentationVersionsFollowLocalInvalidationAndFogRebuild(t *testing.T) {
	s := New(&world.Terrain{CellW: 16, CellH: 16}, ModeHistoryEnabled|ModeCurrentEnabled)
	if s == nil {
		t.Fatal("nil service")
	}
	mapping0 := s.MappingVersion()
	s.RebuildAll(nil)
	if s.MappingVersion() <= mapping0 {
		t.Fatalf("bulk rebuild did not advance mapping version: %d -> %d", mapping0, s.MappingVersion())
	}
	fog0 := s.FogVersion()
	s.RebuildFog(0, 0)
	if s.FogVersion() <= fog0 {
		t.Fatalf("completed fog rebuild did not advance fog version: %d -> %d", fog0, s.FogVersion())
	}
	stableMode := s.MappingVersion()
	s.SetMode(s.Mode() &^ ModeFogCacheValid)
	if s.MappingVersion() != stableMode || !s.FogCacheValid() {
		t.Fatal("SetMode treated the internal fog-valid bit as a semantic mode change")
	}
	stable := s.MappingVersion()
	s.SetLocal(0)
	if s.MappingVersion() != stable {
		t.Fatal("selecting the current local slot changed mapping version")
	}
	s.SetLocal(1)
	if s.MappingVersion() <= stable || s.FogCacheValid() {
		t.Fatal("changing the local viewer did not invalidate mapping and fog")
	}
}
