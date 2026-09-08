package features

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestDefinitionRestoreStartLatchesFirstResetBoundary(t *testing.T) {
	first := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "first"}}
	second := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "second"}}
	terrain := &world.Terrain{FeatureDefs: []*content.FeatureDef{first, second}}
	service := NewService(terrain, nil, nil, nil)

	if got := service.DefinitionRestoreStart(); got != 2 {
		t.Fatalf("ordinary definition boundary = %d, want 2", got)
	}
	service.ResetForRestore()
	terrain.FeatureDefs = append(terrain.FeatureDefs, &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "saved"}})
	if got := service.DefinitionRestoreStart(); got != 2 {
		t.Fatalf("first restore boundary = %d, want 2", got)
	}
	service.ResetForRestore()
	if got := service.DefinitionRestoreStart(); got != 2 {
		t.Fatalf("repeated restore overwrote first boundary: %d", got)
	}
}
