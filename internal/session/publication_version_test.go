package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestPublishVisibilityDoesNotRestoreReplacementService(t *testing.T) {
	terrain := &world.Terrain{CellW: 4, CellH: 4}
	first := visibility.New(terrain, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	first.WordMask()[0] = 1
	first.ByteGrid(0)[0] = 1

	var published frame.Frame
	publishVisibilityView(first, 0, &published)
	published.Reset()

	second := visibility.New(terrain, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	publishVisibilityView(second, 0, &published)
	if got, want := published.Visibility.MappingSource, second.PresentationIdentity(); got != want {
		t.Fatalf("mapping source = %d, want replacement service %d", got, want)
	}
	if published.Visibility.WordVisible[0] != 0 || published.Visibility.Visible[0] != 0 {
		t.Fatalf("replacement service restored stale grids: word=%v current=%v", published.Visibility.WordVisible, published.Visibility.Visible)
	}
}
