// ai_bind.go — production binding of the typed AI build request into the
// ordinary construction queues [RX-01][ON-06 F-P0-004][05 "Factory production
// lifecycle"]. Mobile sites carry the selected coordinates through to
// QueueMobileBuild; factory products queue on the factory path. The AI never
// receives privileged world mutation — this is the same command surface the
// human order path reaches.
package session

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/construction"
)

func bindAIQueue(mgr *ai.Manager, s *Session) {
	if mgr == nil || s == nil {
		return
	}
	mgr.QueueBuildTyped = func(req ai.BuildRequest) error {
		if s.Units == nil || s.Catalog == nil {
			return fmt.Errorf("ai build: session units/catalog unavailable")
		}
		builder := s.Units.Unit(req.Builder)
		if builder == nil || !builder.Alive {
			return fmt.Errorf("ai build: builder handle %d not alive", req.Builder)
		}
		switch req.Kind {
		case ai.BuildKindMobileSite:
			return construction.QueueMobileBuild(builder, req.UnitKey, req.X, req.Z, req.Count, s.Catalog)
		default:
			return construction.QueueFactoryBuild(builder, req.UnitKey, req.Count, s.Catalog)
		}
	}
}
