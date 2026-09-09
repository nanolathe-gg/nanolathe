package features

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

func TestRuntimeViewFollowsArenaAttachmentRatherThanLookupPresence(t *testing.T) {
	terrain := newEmptyTerrain(4, 4)
	svc := NewService(terrain, nil, nil, nil)
	sprite := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "sprite"},
		Filename:         "trees",
		SeqNameDie:       "die",
		SeqNameDieShad:   "dieshad",
		FootprintX:       1,
		FootprintZ:       1,
	}
	if inst := svc.spawnFeatureAt(1, 1, sprite); inst == nil {
		t.Fatal("spawn resting sprite")
	}
	resting := svc.InstanceAt(1, 1)
	if got := resting.RuntimeView(); got.Live || got.ShadowEnabled {
		t.Fatalf("resting convenience record reported runtime state %#v", got)
	}

	svc.SequenceFrames = func(*content.FeatureDef, uint8) []int32 { return []int32{1} }
	svc.ShadowSequenceResolved = func(*content.FeatureDef, string) bool { return true }
	if !svc.transitionFeatureAt(1, 1, sprite, false) {
		t.Fatal("death transition did not attach the runtime record")
	}
	if got := resting.RuntimeView(); !got.Live || !got.ShadowEnabled {
		t.Fatalf("attached sprite runtime state %#v, want live shadow-enabled", got)
	}

	svc.deleteInstance(1 + int(terrain.CellW))
	if got := resting.RuntimeView(); got.Live || got.ShadowEnabled {
		t.Fatalf("released record retained runtime state %#v", got)
	}
}

func TestRuntimeViewReportsThreeDRecordWithoutSpriteShadow(t *testing.T) {
	terrain := newEmptyTerrain(4, 4)
	svc := NewService(terrain, nil, nil, nil)
	model := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wreck"},
		Object:           "wreck.3do",
		FootprintX:       1,
		FootprintZ:       1,
	}
	inst := svc.spawnFeatureAt(1, 1, model)
	if inst == nil {
		t.Fatal("spawn 3D feature")
	}
	if got := inst.RuntimeView(); !got.Live || got.ShadowEnabled {
		t.Fatalf("3D runtime state %#v, want live without sprite shadow", got)
	}
}

func TestRuntimeViewRestoreReattachesTheSavedSpriteRecord(t *testing.T) {
	terrain := newEmptyTerrain(4, 4)
	svc := NewService(terrain, nil, nil, nil)
	sprite := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "restored-sprite"},
		Filename:         "trees",
		SeqNameDie:       "die",
		SeqNameDieShad:   "dieshad",
		FootprintX:       1,
		FootprintZ:       1,
	}
	svc.SequenceFrames = func(*content.FeatureDef, uint8) []int32 { return []int32{3} }
	svc.ShadowSequenceResolved = func(*content.FeatureDef, string) bool { return true }
	payload := make([]byte, RetailRestorePayloadSize(1))
	payload[9] = featureAnimSelectorDie
	inst, err := svc.RestoreAt(2, 2, sprite, 1, payload)
	if err != nil || inst == nil {
		t.Fatalf("restore = (%#v, %v), want attached sprite", inst, err)
	}
	if got := inst.RuntimeView(); !got.Live || !got.ShadowEnabled {
		t.Fatalf("restored sprite runtime state %#v, want live shadow-enabled", got)
	}
}

func TestRuntimeViewDisablesAnUnresolvedEventShadow(t *testing.T) {
	terrain := newEmptyTerrain(4, 4)
	svc := NewService(terrain, nil, nil, nil)
	sprite := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "sprite"},
		Filename:         "trees",
		SeqNameDie:       "die",
		SeqNameDieShad:   "missing-shadow",
		FootprintX:       1,
		FootprintZ:       1,
	}
	if svc.spawnFeatureAt(1, 1, sprite) == nil {
		t.Fatal("spawn resting sprite")
	}
	svc.SequenceFrames = func(*content.FeatureDef, uint8) []int32 { return []int32{1} }
	svc.ShadowSequenceResolved = func(*content.FeatureDef, string) bool { return false }
	if !svc.transitionFeatureAt(1, 1, sprite, false) {
		t.Fatal("death transition did not attach the runtime record")
	}
	if got := svc.InstanceAt(1, 1).RuntimeView(); !got.Live || got.ShadowEnabled {
		t.Fatalf("unresolved shadow published runtime state %#v, want live without shadow", got)
	}
}
