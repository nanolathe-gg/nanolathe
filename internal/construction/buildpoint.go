// Where a build happens: the cell snap, the factory's build-piece world
// position and the builder's nanolathe piece [05 "Construction target
// state"][03 §5.5].
//
// Moved out of factory.go by CL-5, which split that file by concern; the code
// is unchanged.

package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The cell snap of [05 "Factory production lifecycle"] C16 — each coordinate
// biased by half its extent before the floor to a cell — lives in
// internal/world as SnapFootprintAnchor, which every construction site reaches
// through SnapFactoryPlacement or SnapMobilePlacement. A second copy of it
// used to stand here under the name SnapWorldToCell, with its own arithmetic
// (a floor-to-cell followed by an integer half-extent subtraction) that agreed
// with the placement seam only on cell-aligned inputs; only the seam's is
// wired, so the copy is gone.

// QueryBuildWorldPosition resolves the authored exit transform in full world
// X/Y/Z per [05 "Factory production lifecycle"] C16.
// Exact order: query factory script's build-info piece with query argument PRE-INITIALIZED to -1;
// resolve piece transform + factory origin to world position; store position on order node is done by caller;
// load product definition and snap to map cells using packed footprint extents each biased by half extent.
func (s *Service) QueryBuildWorldPosition(factory *units.Unit, m *model.Model) (world.ModelWorldPosition, bool) {
	_, position, ok := s.queryBuildPiecePosition(factory, m)
	return position, ok
}

// queryBuildPiecePosition performs the synchronous QueryBuildInfo once and
// retains both values state 2 consumes: the signed-byte cargo piece and its
// composed position [04 R-FAC-02 §1][04 R-REV-02].
func (s *Service) queryBuildPiecePosition(factory *units.Unit, m *model.Model) (int, world.ModelWorldPosition, bool) {
	if factory == nil || m == nil {
		return -1, world.ModelWorldPosition{}, false
	}
	// QueryBuildInfo is a synchronous mode-Q callback. Production uses the
	// strict binding bridge [R-P0-09][04 §5.3].
	pieceIdx := int32(-1)
	if binding := factory.COBBinding(); binding != nil && binding.Callbacks != nil {
		pieceIdx = binding.Callbacks.QueryBuildInfo().QueryValue()
	} else {
		return -1, world.ModelWorldPosition{}, false
	}
	if pieceIdx < 0 {
		return -1, world.ModelWorldPosition{}, false
	}
	modelPiece := pieceIdx
	if binding := factory.COBBinding(); binding != nil && int(pieceIdx) < len(binding.PieceMap) {
		modelPiece = int32(binding.PieceMap[pieceIdx])
	}
	if modelPiece < 0 || int(modelPiece) >= len(m.Pieces) {
		return -1, world.ModelWorldPosition{}, false
	}
	// 2. resolve piece transform plus factory origin to world position. Strict
	// bindings own PieceMap, hierarchy state, and unit orientation; construction
	// must not duplicate that composition [04 §4.1][03 §2.4].
	var pos [3]numeric.Fixed
	if binding := factory.COBBinding(); binding != nil {
		var composed bool
		pos, composed = binding.ComposePiece(int(pieceIdx), factory.Move.Heading, factory.Move.Pitch, factory.Move.Bank)
		if !composed {
			return -1, world.ModelWorldPosition{}, false
		}
	} else {
		return -1, world.ModelWorldPosition{}, false
	}
	// ComposePiece is retail's piece locator and hands back the WORLD offset
	// `(x, y, −z)`; the build plate is that triple added to the factory's own
	// position with no further sign change [03 R-RAST-01 §8]. The model/world
	// Z mirror [03 R-RAST-01 §2] is applied once inside the locator, where it
	// used to be applied per call site.
	//
	// The authored data settles the same sense independently, and the check is
	// worth keeping: the exit footprint has to land on cells the yard releases
	// when it opens — the 'c'/'C' region, stamped only while closed
	// [04 R-COLL-01 §4] — because the state-2 area test runs with a null self
	// identity and any non-zero ground word blocks it [04 R-FAC-02 §5].
	// Measured over the six stock factories at their authored build angles:
	// with the mirror dropped, ARMAP, CORVP and CORAP put the exit footprint
	// on always-stamped `o` cells, where no product could ever validate; with
	// it applied, all six land inside their own released corridor. Only one
	// sign choice lets the stock models and the stock yard maps agree, and it
	// is the locator's.
	worldX := factory.X.Add(pos[0])
	worldY := factory.Y.Add(pos[1])
	worldZ := factory.Z.Add(pos[2])
	return int(pieceIdx), world.NewModelWorldPosition(worldX, worldY, worldZ), true
}

// QueryBuildInfo preserves the established cell-returning API. Its cell is
// the independently snapped validation anchor; callers allocating a product
// must retain QueryBuildWorldPosition separately [R-P0-02].
func (s *Service) QueryBuildInfo(factory *units.Unit, m *model.Model) (world.Cell, bool) {
	position, ok := s.QueryBuildWorldPosition(factory, m)
	if !ok {
		return world.Cell{}, false
	}

	// 4. Load product definition and snap using packed footprint extents each biased by half extent [05 C16][P0-I05].
	footX, footZ := 1, 1 // default 1x1 when the product is unknown
	if q := s.queueForUnit(factory); q != nil && q.LenPrimary() > 0 {
		head := q.Primary()[0]
		var def *content.UnitDef
		if head.BuildDefKey != "" && s.Catalog != nil {
			if d, ok := s.Catalog.Unit(head.BuildDefKey); ok {
				def = d
			}
		}
		if def == nil {
			if pid := head.Param1; pid != 0 {
				def = s.productDef(uint32(pid))
			}
		}
		if def != nil {
			footX = int(def.FootprintX)
			footZ = int(def.FootprintZ)
			// Authored empty mobile extents retain the same snap [04 R-P0-08-C].
		}
	}
	extent, err := world.NewFootprintExtent(int32(footX), int32(footZ))
	if err != nil {
		return world.Cell{}, false
	}
	placement, err := world.SnapFactoryPlacement(position, extent)
	if err != nil {
		return world.Cell{}, false
	}
	return placement.Anchor().Cell(), true
}

// QueryNanoPiece synchronously resolves the builder's authored nano piece and
// transforms it through the current model hierarchy. Cell zero is seeded to 0;
// no engine-side piece alternation is permitted [R-P0-06 §2][04 §5.3]. Stock
// multi-emitter builders alternate their spray piece from inside the script —
// ARMAP returns beam1/beam2 and ARMACK rnanospray/lnanospray on successive
// calls — so the caller must issue exactly one query per accepted work step
// and never a speculative one: an extra call per tick rotates the script past
// the emitter the work step would have used and pins the spray to one piece
// [R-P0-06 §4][R-P0-06 §6].
func (s *Service) QueryNanoPiece(builder *units.Unit) (int32, world.ModelWorldPosition, bool) {
	if s == nil || builder == nil {
		return 0, world.ModelWorldPosition{}, false
	}
	m := s.ModelForUnit
	if m == nil {
		m = s.ModelForFactory
	}
	var mdl *model.Model
	if m != nil {
		mdl = m(builder)
	}
	if mdl == nil {
		if binding := builder.COBBinding(); binding != nil {
			mdl = binding.Model
		}
	}
	if mdl == nil {
		return 0, world.ModelWorldPosition{}, false
	}
	piece := int32(0)
	if binding := builder.COBBinding(); binding != nil && binding.Callbacks != nil {
		piece = binding.Callbacks.QueryNanoPiece().QueryValue()
	} else {
		return 0, world.ModelWorldPosition{}, false
	}
	modelPiece := piece
	if binding := builder.COBBinding(); binding != nil && piece >= 0 && int(piece) < len(binding.PieceMap) {
		modelPiece = int32(binding.PieceMap[piece])
	}
	if modelPiece < 0 || int(modelPiece) >= len(mdl.Pieces) {
		return piece, world.ModelWorldPosition{}, false
	}
	var pos [3]numeric.Fixed
	if binding := builder.COBBinding(); binding != nil {
		var composed bool
		pos, composed = binding.ComposePiece(int(piece), builder.Move.Heading, builder.Move.Pitch, builder.Move.Bank)
		if !composed {
			return piece, world.ModelWorldPosition{}, false
		}
	} else {
		return piece, world.ModelWorldPosition{}, false
	}
	// The spray source is the locator's world offset added to the builder's own
	// position, with no further sign change — the nano case of
	// [03 R-RAST-01 §8], which closed [03 §5.5]'s Supported inference at the
	// submission site. ComposePiece applies the model/world Z mirror once, on
	// its output, exactly as the build-plate query above receives it.
	//
	// The sense is worth stating in pixels, because an inverted one is visible:
	// mirroring the emitter about the builder's own centre moves the spray
	// origin down the screen by twice the piece's depth offset — the screen
	// ordinate is `Z - Y/2`, so for the Arm aircraft plant's beam pieces that
	// is roughly seventy whole pixels, and a nano piece authored on top of the
	// building sprays from the ground. `unit + (x, y, −z)` is the pixel the
	// model pass actually draws the piece at: it composes the same offset and
	// emits it at `hi16(-vz)` relative to the unit's blit anchor [03 §2.4]
	// [03 §2.5]. It also agrees with the build-plate sign that the stock yard
	// maps settled independently.
	return piece, world.NewModelWorldPosition(builder.X.Add(pos[0]), builder.Y.Add(pos[1]), builder.Z.Add(pos[2])), true
}

func (s *Service) emitAcceptedNano(tick uint32, builder, product *units.Unit) {
	if s == nil || s.Presentation == nil || builder == nil || product == nil {
		return
	}
	piece, source, ok := s.QueryNanoPiece(builder)
	if !ok {
		return
	}
	// Selector 6 is the established construction segment selector. The source
	// is the QueryNanoPiece world position; the target is the product's world
	// anchor [R-P0-06]. One event per accepted work step (mobile construction
	// emits one segment, unlike build assist's two) [R-P0-06 §1][R-P0-06 §3].
	// The producer identity routes the event to effect strip 6 (beam/muzzle/
	// nanolathe) and the geometry flag opens the client's nanolathe draw gate
	// [03 §5.5][R-P0-06 §5].
	//
	// The ordinary work direction sprays FROM the builder's nano piece INTO
	// the product, so the box below sits at the DESTINATION end
	// (NanolatheBoxAtSource stays false) [05 R-WORK-01 §8]. Publishing the
	// product's own box here — the same derivation the reversed reclaim/
	// capture producers use [02 R-CAT-01 §7] — lets the client stop
	// re-deriving the destination from real model geometry, which is the
	// wrong shape: the record is footprint-derived in X/Z, not the model's
	// silhouette.
	boxMin, boxMax := product.NanolatheBox()
	s.Presentation.EmitNanolathe(frame.Event{
		Tick: tick, Source: builder.Handle, Target: product.Handle, Piece: piece,
		X: source.X(), Y: source.Y(), Z: source.Z(),
		TargetX: product.X, TargetY: product.Y, TargetZ: product.Z,
		EffectID: 6, Mode: 1, Team: builder.Owner,
		Producer:                frame.ProducerBeam,
		PaletteRow:              6,
		NanolatheActiveUntil:    tick + 300,
		NanolatheGeometryKnown:  true,
		NanolatheTargetBoxKnown: true,
		NanolatheTargetMin:      boxMin,
		NanolatheTargetMax:      boxMax,
	})
}
