package ai

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// BuildKind identifies the construction path [P0-04][P0-07] F-P0-004.
// MobileSite is a mobile builder (commander, construction unit) placing a structure at X,Z via MobileBuild/VTOL_MobileBuild [04 §3.1][P0-I05].
// FactoryQueue is a factory enqueuing a product via BuildingBuild without site coordinates [05][P0-I05].
type BuildKind int

const (
	BuildKindMobileSite   BuildKind = iota // commander/base construction at selected site [P0-07] MobileSite
	BuildKindFactoryQueue                  // factory production without site [P0-07] FactoryQueue
)

// BuildRequest is the typed build request replacing the lossy QueueBuild callback [P0-07] F-P0-004.
// It preserves site coordinates selected by Place and distinguishes mobile vs factory paths.
// Builder is the pool handle of the constructing unit; UnitKey is canonical target def; X,Z are site anchor for MobileSite (ignored for FactoryQueue).
type BuildRequest struct {
	Builder pool.Handle
	UnitKey string
	X, Z    numeric.Fixed
	Count   int
	Kind    BuildKind
}
