package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// pickFeature shares one hit policy between the cursor/footer and commands.
// Unbound, Strict and Community retain the mapped ground-cell probe [07 §8].
// Modern also admits projected submerged corpse bodies in the main viewport
// (interface design "Modern submerged wreck picking").
func (b *battleSession) pickFeature(sx, sy int32, pos orders.ResolvePos) (frame.FeatureView, orders.ResolvePos, bool) {
	f, ok := b.currentSnapshot()
	if !ok {
		return frame.FeatureView{}, pos, false
	}
	cx, cz := world.WorldToCell(pos.X), world.WorldToCell(pos.Z)
	var ground frame.FeatureView
	groundHit := false
	for _, v := range f.Features {
		fx, fz := max(int32(v.FootX), 1), max(int32(v.FootZ), 1)
		if cx >= v.CX && cx < v.CX+fx && cz >= v.CZ && cz < v.CZ+fz && snapshotFeatureMappedAt(f, pos.X, pos.Y, pos.Z, f.ViewingPlayer) {
			ground, groundHit = v, true
			break
		}
	}
	if b.sess.Rules.Orders == nil || !b.sess.Rules.Orders.PicksSubmergedWrecks() || b.cl == nil || b.cat == nil ||
		b.classifyPointer(sx, sy) != battlePointerViewport || b.megamapOwnsPointer(sx, sy) {
		return ground, pos, groundHit
	}
	// A visible unit keeps its ordinary target word; a wreck behind it must
	// not substitute a different command point or feature capability.
	if h, _, hit := b.pickPresentedUnit(f, sx, sy, f.ViewingPlayer); hit && h != 0 {
		return ground, pos, groundHit
	}
	// Stable committed feature order breaks a projected overlap tie. The
	// visual hit takes precedence over an unrelated water-surface ground cell.
	for _, v := range f.Features {
		if !v.Reclaimable || v.Y >= f.Visibility.SeaLevel || !b.isCorpseName(v.DefName) || !snapshotFeatureVisible(f, v, f.ViewingPlayer) {
			continue
		}
		def := b.cat.Features[content.CanonicalKey(v.DefName)]
		if def == nil || def.Object == "" {
			continue
		}
		wx, wz := world.PlacementCenter(v.CX, v.CZ, max(int32(v.FootX), 1), max(int32(v.FootZ), 1))
		if !snapshotFeatureMappedAt(f, wx, f.Visibility.SeaLevel, wz, f.ViewingPlayer) || !b.cl.FeatureContainsPoint(v, b.cam, sx, sy) {
			continue
		}
		if groundHit && ground.InstanceID == v.InstanceID && ground.CX == v.CX && ground.CZ == v.CZ {
			return ground, pos, true // the ordinary probe already names this wreck
		}
		pos.X, pos.Y, pos.Z = wx, f.Visibility.SeaLevel, wz
		return v, pos, true
	}
	return ground, pos, groundHit
}

// A new visual contextual work click acts only on its feature workers; other
// selected units keep their orders. Pure movers and explicit MOVE keep the raw
// point. Filtering before dispatch reuses existing captured actor lists and
// leaves order resolution, queues and the wire unchanged (interface design
// "Modern submerged wreck picking"). The ordinary surface probe is untouched.
func (b *battleSession) pickContextualWreckWorkers(sx, sy int32, command *session.HumanOrderCommand) {
	_, goal, hit := b.pickFeature(sx, sy, command.Position)
	if !hit || (goal.X == command.Position.X && goal.Y == command.Position.Y && goal.Z == command.Position.Z) {
		return
	}
	for _, actor := range b.selectedCommandUnits() {
		id := orders.Resolve(command.Code, actor, nil, &command.Position)
		if id == orders.Lookup("Reclaim") || id == orders.Lookup("VTOL_Reclaim") || id == orders.Lookup("Resurrect") {
			command.Handles = append(command.Handles, actor.Handle)
		}
	}
	if len(command.Handles) != 0 {
		command.Position = goal
	}
}
