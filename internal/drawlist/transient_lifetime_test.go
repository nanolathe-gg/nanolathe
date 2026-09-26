package drawlist

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"testing"
)

func TestResetDropsRetiredSpriteAndEmitterReferences(t *testing.T) {
	var l List
	f := &formats.GAFFrame{Transient: true}
	l.RecordSprite(Sprite{Frame: f, LightingKind: SpriteLightingExplosion})
	l.Reset()
	if l.sprite[:cap(l.sprite)][0].Frame != nil || l.lightSources[:cap(l.lightSources)][0].Frame != nil {
		t.Fatal("reset retained inactive frame pointers")
	}
}
