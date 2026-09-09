package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// SetFragmentMaterialResolver binds the battle-owned texture registry's scalar
// snapshot operation. It reads no drawn geometry or pixels and cannot veto
// physical admission [04 R-COB-04 §3][I6].
func (s *Session) SetFragmentMaterialResolver(resolver func(uint16, int, int, uint8) render.FrozenFragmentMaterial) {
	s.fragmentMaterialResolver = resolver
}

// AdmitShatter samples the current COB pose, including root orientation, at
// the callback. This deliberate departure from retail's retained drawing
// history keeps geometry authoritative and independent of rendering
// [DESIGN_UNITS_ORDERS_COB §3.3 "Shatter core API"][I6].
func (sink *cobExplosionSink) AdmitShatter(request cob.ShatterExplosion) bool {
	if sink == nil || sink.presentation == nil || sink.presentation.session == nil {
		return false
	}
	s := sink.presentation.session
	if s.Units == nil || s.World == nil || s.publication == nil || s.publication.effects == nil {
		return false
	}
	u := s.Units.Unit(sink.presentation.source)
	if u == nil {
		return false
	}
	binding := u.COBBinding()
	if binding == nil || binding.Model == nil || binding.VM == nil {
		return false
	}
	cobPiece := request.Source.Identity.COBPiece
	if cobPiece < 0 || cobPiece >= len(binding.PieceMap) {
		return false
	}
	pieceIndex := binding.PieceMap[cobPiece]
	if pieceIndex < 0 || pieceIndex >= len(binding.Model.Pieces) {
		return false
	}
	piece := &binding.Model.Pieces[pieceIndex]
	states := make([]model.PieceState, len(binding.Model.Pieces))
	for cobIndex, modelIndex := range binding.PieceMap {
		if cobIndex < len(binding.VM.Pieces) && modelIndex >= 0 && modelIndex < len(states) {
			states[modelIndex] = binding.VM.Pieces[cobIndex]
		}
	}
	model.FoldRootAngles(states, binding.Model.Root, u.Move.Heading, u.Move.Pitch, u.Move.Bank)
	transform := model.Compose(binding.Model, states, pieceIndex)
	quads := make([]render.FragmentQuad, 0, len(piece.Primitives))
	for index, primitive := range piece.Primitives {
		// The primitive's flag bit zero is authored IsColored bit zero. The
		// designated ground plate remains at compiled slot zero [03 R-REN-03A §5]
		// [04 R-COB-04 §3].
		if len(primitive.VertexIndices) != 4 || primitive.IsColored&1 != 0 || piece.Selection && index == 0 {
			continue
		}
		quad := render.FragmentQuad{PrimitiveIndex: index}
		valid := true
		for corner, vertex := range primitive.VertexIndices {
			if int(vertex) >= len(piece.Vertices) {
				valid = false
				break
			}
			quad.Vertices[corner] = transform.Apply(piece.Vertices[vertex])
		}
		if valid {
			quads = append(quads, quad)
		}
	}
	var velocity [3]numeric.Fixed
	if s.Movement != nil && s.Movement.HasMover(u.Handle) {
		velocity = [3]numeric.Fixed{u.Move.VelX, u.Move.VelY, u.Move.VelZ}
	}
	colour, _ := s.colourForOwner(int(u.Owner))
	return s.publication.effects.AdmitShatter(render.FragmentRequest{
		UnitDefID: s.Units.DefIDForHandle(u.Handle), PieceIndex: pieceIndex,
		Position: effectWorldPoint(u, transform.Position()), MoverVelocity: velocity,
		Gravity: s.World.Gravity, ExplodeOnHit: request.Flags&cob.ExplosionShatterOnHit != 0, Quads: quads,
		Freeze: func(def uint16, piece int, quad render.FragmentQuad) render.FrozenFragmentMaterial {
			if s.fragmentMaterialResolver != nil {
				return s.fragmentMaterialResolver(def, piece, quad.PrimitiveIndex, colour)
			}
			return render.FrozenFragmentMaterial{UnitDefID: def, PieceIndex: piece, PrimitiveIndex: quad.PrimitiveIndex}
		},
	}, s.SimRNG().Uint32n)
}

func (s *Session) bindFragmentStepContext(tick uint32) {
	context := render.FragmentStepContext{WaterEffectsWordZero: s.waterEffectsEnabled(), Impact: fragmentImpactSink{debrisImpactSink{s, tick}}}
	if s.World != nil {
		context.TerrainHeight, context.SeaLevel = s.World.HeightAt, s.World.SeaLevelWorld()
		context.Gravity, context.Lava = s.World.Gravity, s.World.LavaWorld
	}
	s.publication.effects.SetFragmentStepContext(context)
}

type fragmentImpactSink struct{ debrisImpactSink }

func (sink fragmentImpactSink) GroundFragmentImpact(impact render.GroundFragmentImpact) {
	sink.GroundDebrisImpact(render.GroundDebrisImpact(impact))
}
func (sink fragmentImpactSink) WaterFragmentImpact(impact render.WaterFragmentImpact) {
	sink.WaterDebrisImpact(render.WaterDebrisImpact(impact))
}

func (s *Session) publishFragments(out []frame.FragmentView) []frame.FragmentView {
	if s.publication == nil || s.publication.effects == nil {
		return out[:0]
	}
	p := s.publication
	p.fragments = p.effects.FragmentMetadataInto(p.fragments)
	for _, fragment := range p.fragments {
		material := fragment.Material
		out = append(out, frame.FragmentView{Slot: uint16(fragment.Slot + 1), UnitDefID: material.UnitDefID,
			PieceIndex: material.PieceIndex, PrimitiveIndex: material.PrimitiveIndex, FrameIndex: material.FrameIndex, MaterialValid: material.Valid,
			Position: fragment.Position, Angles: fragment.Angles, Vertices: fragment.Vertices})
	}
	return out
}
