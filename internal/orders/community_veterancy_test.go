package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func TestCommunityCaptureUsesTargetsUnboundedVeterancy(t *testing.T) {
	binding := &QueueBinding{Community: community.Features{Veterancy: true}}
	targetDef := &content.UnitDef{VeterancyThresholds: []uint32{3, 7}}
	level := (CommunityRules{}).CaptureVeteranLevel(CaptureVeteranRequest{
		Binding: binding, Definition: targetDef, Kills: 17,
	})
	if level != 4 { // 2 + (17-7)/(7-3)
		t.Fatalf("target unbounded level = %d, want 4", level)
	}
	if got := captureBudget(0, 0, 100, 100, 0, level); got != 210 {
		t.Fatalf("capture budget with target level = %d, want 210", got)
	}
	if got := (StrictRules{}).CaptureVeteranLevel(CaptureVeteranRequest{Binding: binding, Definition: targetDef, Kills: 17}); got != 3 {
		t.Fatalf("Strict read authored target metadata: level = %d, want retail 3", got)
	}
}
