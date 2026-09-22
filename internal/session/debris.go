package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func (s *Session) ensureDebris() {
	if s != nil && s.debris == nil {
		s.debris = render.NewDebrisPoolWithCapacity(s.EntryCommunity.DebrisCapacity)
	}
}

func (s *Session) stepDebris(tick uint32) {
	if s == nil || s.debris == nil {
		return
	}
	ctx := render.DebrisStepContext{}
	if s.World != nil {
		ctx.TerrainHeight = s.World.HeightAt
		ctx.SeaLevel = s.World.SeaLevelWorld()
		ctx.Gravity = s.World.Gravity
		ctx.Lava = s.World.LavaWorld
	}
	ctx.WaterEffectsWordZero = s.waterEffectsEnabled()
	s.debris.Step(ctx, debrisImpactSink{session: s, tick: tick})
}

func (s *Session) publishDebris(out []frame.DebrisView) []frame.DebrisView {
	if s == nil || s.debris == nil {
		return out[:0]
	}
	out = s.debris.SnapshotViewsInto(out)
	for i := range out {
		v := &out[i]
		// A dead source slot remains readable until reuse; after reuse the raw
		// record deliberately supplies the new occupant's owner [P0-16][I6].
		if s.Units != nil {
			if raw := s.Units.RawUnitRecord(v.RawSlot); raw != nil {
				v.OwnerColor, v.OwnerColorKnown = radarOwnerPalette(s, raw.Owner, true)
			}
		}
	}
	return out
}

func (s *Session) waterEffectsEnabled() bool {
	// Battle composition samples nosealeveltrigger once into Combat's immutable
	// opaque-liquid mode. Fixtures without that required service retain its
	// composition default: water effects enabled [06 §8.2][06 §9.1].
	if s == nil || s.Combat == nil {
		return true
	}
	return !s.Combat.OpaqueLiquidMode
}

type debrisImpactSink struct {
	session *Session
	tick    uint32
}

func (d debrisImpactSink) GroundDebrisImpact(impact render.GroundDebrisImpact) {
	if d.admit(impact.Position, impact.Graphic, impact.CalculatedFrameTable, true) && impact.AboveSeaFlash &&
		d.sessionAboveSea(impact.Position[1]) {
		// The ground bitmap has claimed its fixed-pool slot before the class-7
		// parameter-15 dust producer runs [04 R-COB-04 §2, §4][03 R-FX-01 §3].
		d.session.appendStripSmokePuffer(9, impact.Position, SmokePuffLandDust)
	}
}

func (d debrisImpactSink) WaterDebrisImpact(impact render.WaterDebrisImpact) {
	d.admit(impact.Position, impact.Graphic, 0, false)
}

func (d debrisImpactSink) admit(position [3]numeric.Fixed, graphic string, table uint8, calculated bool) bool {
	if d.session == nil || d.session.ensurePublicationState() == nil || d.session.publication.effects == nil {
		return false
	}
	event := frame.Event{Kind: frame.KindExplosion, Tick: d.tick, Graphic: graphic, AssetID: "fx", X: position[0], Y: position[1], Z: position[2]}
	if calculated {
		event.HasCalculatedFlash = true
		event.CalculatedTable = table
		event.DurationsB = render.FlashFrameDurations(int(table))
	}
	return d.session.publication.effects.Admit(d.tick, event)
}

func (d debrisImpactSink) sessionAboveSea(y numeric.Fixed) bool {
	if d.session == nil || d.session.World == nil {
		return false
	}
	return int32(y.Raw())>>numeric.FractionBits > int32(d.session.World.SeaLevel)
}
