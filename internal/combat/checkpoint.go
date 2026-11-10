package combat

import (
	"fmt"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// CheckpointContext shares admitted content and allocation identities with the
// unit and world owners. Combat adds no tables (DESIGN_MULTIPLAYER §16.3.16).
type CheckpointContext struct {
	Units   *units.CheckpointContext
	World   *world.CheckpointContext
	binding checkpointBindings
}

// NewCheckpointContext binds capture-local owners without reading their state.
func NewCheckpointContext(u *units.CheckpointContext, w *world.CheckpointContext) *CheckpointContext {
	return &CheckpointContext{Units: u, World: w}
}

// CollectCheckpointReferences visits retained edges in lexical field order:
// TransportDeaths.passengers, sorted deathNotified values, incoming shooter then
// target. A dead projectile still occupies its count span [06 §5.1]. Incoming
// tails are never read; unlike projectile residues, reservation clears them.
func (s *Service) CollectCheckpointReferences(c *CheckpointContext) (added int, err error) {
	if err = s.validateCheckpoint(c); err != nil {
		return 0, err
	}
	add := func(u *units.Unit) error {
		_, known := c.Units.Allocations.Find(u)
		if _, err := c.Units.Allocations.Add(u); err != nil {
			return err
		}
		if !known {
			added++
		}
		return nil
	}
	for _, row := range s.TransportDeaths.passengers {
		if err = add(row.unit); err != nil {
			return added, err
		}
	}
	for _, h := range s.checkpointDeathHandles() {
		if err = add(s.deathNotified[h]); err != nil {
			return added, err
		}
	}
	for _, row := range s.incoming[:s.Slots.Count()] {
		if err = add(row.shooter); err != nil {
			return added, err
		}
		if err = add(row.target); err != nil {
			return added, err
		}
	}
	return added, nil
}

// WriteCheckpoint writes source fields in lexical order. The resolved Community
// table, every initialized Records row, Slots metadata, transport captures,
// area cells/nodes/hit generations, death identities, option gates, incoming
// count span, both Modern ticks, scan cursors and stored target registries are
// retained. External ports carry validated presence; rules use their closed
// kind tag (DESIGN_MULTIPLAYER §16.3.70). No allocation is re-resolved through
// its current raw handle.
//
// Excluded fields (DESIGN_MULTIPLAYER §16.3.5): boxCentres and weaponByID plus
// weaponByIDCatalog derive from immutable content; pendingAims has no reader;
// candidateScratch, compactScratch, firingPosition, shotQuery, targetQuery,
// communityAreaUnits and targets.walkScratch are scoped scratch; presentationIDs
// and projectilePresentationSequence are presentation identities;
// communityAreaSaturations is diagnostic. impactStack, TransportDeaths.pending
// and communityAreaCurrentGen must be empty at capture and have no payload.
// No query, cache refresh, pruning, compaction, rule or callback runs here.
func (s *Service) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error {
	if e == nil {
		return combatCheckpointError("combat.encoder", "a checkpoint encoder")
	}
	e.Field("combat.Service")
	if err := s.validateCheckpoint(c); err != nil {
		e.Fail(err)
		return e.Err()
	}
	if err := s.Community.WriteCheckpoint(e); err != nil {
		return err
	}
	e.Field("combat.Service.ControlByte")
	e.Bool(s.controlByte != nil)
	e.Field("combat.Service.DamageActivity")
	e.Bool(s.damageActivity != nil)
	e.Field("combat.Service.DangerNotice")
	e.Bool(s.dangerNotice != nil)
	e.Field("combat.Service.Events")
	e.Bool(s.events != nil)
	e.Field("combat.Service.Features")
	e.Bool(s.Features != nil)
	e.Field("combat.Service.HealthLost")
	e.Bool(s.healthLost != nil)
	e.Field("combat.Service.ImpactNotice")
	e.Bool(s.impactNotice != nil)
	e.Field("combat.Service.InfectionThreat")
	e.Bool(s.infectionThreat != nil)
	e.Field("combat.Service.IsOffMapFiled")
	e.Bool(s.isOffMapFiled != nil)
	e.Field("combat.Service.OpaqueLiquidMode")
	e.Bool(s.OpaqueLiquidMode)
	e.Field("combat.Service.ProjectileWind")
	e.Bool(s.ProjectileWind != nil)
	s.Reaction.writeCheckpoint(e)
	e.Field("combat.Service.Records")
	e.Count(len(s.Records))
	for i := range s.Records {
		writeCheckpointProjectile(e, &s.Records[i], fmt.Sprintf("combat.Service.Records[%d]", i))
	}
	e.Field("combat.Service.Rules")
	rulesKind, err := CheckpointRulesKind(s.Rules)
	e.Fail(err)
	e.U8(rulesKind)
	if err := s.Slots.WriteCheckpoint(e); err != nil {
		return err
	}
	s.TransportDeaths.writeCheckpoint(e, c)
	e.Field("combat.Service.Visibility")
	e.Bool(s.visibility != nil)
	e.Field("combat.Service.VisitOffMapFiled")
	e.Bool(s.visitOffMapFiled != nil)
	e.Field("combat.Service.communityAreaBuilt")
	e.Bool(s.communityAreaBuilt)
	e.Field("combat.Service.communityAreaBuiltTick")
	e.U32(s.communityAreaBuiltTick)
	e.Field("combat.Service.communityAreaCells")
	e.Count(len(s.communityAreaCells))
	for i, row := range s.communityAreaCells {
		e.FieldIndex("combat.Service.communityAreaCells", i, ".count")
		e.I32(row.count)
		e.FieldIndex("combat.Service.communityAreaCells", i, ".head")
		e.I32(row.head)
		e.FieldIndex("combat.Service.communityAreaCells", i, ".stamp")
		e.U32(row.stamp)
		e.FieldIndex("combat.Service.communityAreaCells", i, ".tail")
		e.I32(row.tail)
	}
	e.Field("combat.Service.communityAreaGenCounter")
	e.U32(s.communityAreaGenCounter)
	e.Field("combat.Service.communityAreaHeight")
	e.I32(s.communityAreaHeight)
	e.Field("combat.Service.communityAreaHitGen")
	e.Count(len(s.communityAreaHitGen))
	for _, v := range s.communityAreaHitGen {
		e.U32(v)
	}
	e.Field("combat.Service.communityAreaLimit")
	e.I32(s.communityAreaLimit)
	e.Field("combat.Service.communityAreaNodes")
	e.Count(len(s.communityAreaNodes))
	for i, row := range s.communityAreaNodes {
		e.FieldIndex("combat.Service.communityAreaNodes", i, ".next")
		e.I32(row.next)
		e.FieldIndex("combat.Service.communityAreaNodes", i, ".unit")
		e.U32(uint32(row.unit))
	}
	e.Field("combat.Service.communityAreaStamp")
	e.U32(s.communityAreaStamp)
	e.Field("combat.Service.communityAreaWidth")
	e.I32(s.communityAreaWidth)
	e.Field("combat.Service.deathNotified")
	handles := s.checkpointDeathHandles()
	e.Count(len(handles))
	for _, h := range handles {
		e.Field("combat.Service.deathNotified.handle")
		e.U32(uint32(h))
		writeCheckpointUnitRef(e, c, s.deathNotified[h], "combat.Service.deathNotified.unit")
	}
	e.Field("combat.Service.doubleShot")
	e.Bool(s.doubleShot)
	e.Field("combat.Service.halfShot")
	e.Bool(s.halfShot)
	e.Field("combat.Service.incoming")
	e.Count(s.Slots.Count())
	for i, row := range s.incoming[:s.Slots.Count()] {
		writeCheckpointIncoming(e, c, row, fmt.Sprintf("combat.Service.incoming[%d]", i))
	}
	e.Field("combat.Service.modernNextProjectileTick")
	e.U32(s.modernNextProjectileTick)
	e.Field("combat.Service.modernTick")
	e.U32(s.modernTick)
	e.Field("combat.Service.scanCursor.next")
	for _, v := range s.scanCursor.next {
		e.I64(int64(v))
	}
	e.Field("combat.Service.shootAll")
	e.Bool(s.shootAll)
	e.Field("combat.Service.targets.gate")
	for _, v := range s.targets.gate {
		e.Bool(v)
	}
	e.Field("combat.Service.targets.lastRebuild")
	for _, v := range s.targets.lastRebuild {
		e.U32(v)
	}
	for i, row := range s.targets.primary {
		writeCheckpointHandles(e, row, fmt.Sprintf("combat.Service.targets.primary[%d]", i))
	}
	for i, row := range s.targets.secondary {
		writeCheckpointHandles(e, row, fmt.Sprintf("combat.Service.targets.secondary[%d]", i))
	}
	return e.Err()
}

// Only the sorted slice is consumed; map iteration never supplies wire order.
func (s *Service) checkpointDeathHandles() []pool.Handle {
	handles := make([]pool.Handle, 0, len(s.deathNotified))
	for h := range s.deathNotified {
		handles = append(handles, h)
	}
	slices.Sort(handles)
	return handles
}

func (s *Service) validateCheckpoint(c *CheckpointContext) error {
	if s == nil {
		return combatCheckpointError("combat.Service", "a present service")
	}
	if c == nil || c.Units == nil || c.World == nil || c.Units.Keys == nil || c.World.Keys != c.Units.Keys {
		return combatCheckpointError("combat.context", "unit and world contexts sharing admitted content keys")
	}
	capacity, count := s.Slots.Capacity(), s.Slots.Count()
	if capacity <= 0 || capacity > pool.MaxProjectileCapacity || count < 0 || count > capacity {
		return combatCheckpointError("combat.Service.Slots", "a bounded count and capacity")
	}
	if len(s.Records) != capacity {
		return combatCheckpointError("combat.Service.Records", "initialized records covering capacity")
	}
	if len(s.incoming) != capacity && (s.incoming != nil || count != 0) {
		return combatCheckpointError("combat.Service.incoming", "capacity-sized storage, or nil with zero count")
	}
	if len(s.impactStack) != 0 {
		return combatCheckpointError("combat.Service.impactStack", "an empty impact stack")
	}
	if s.TransportDeaths.pending != nil {
		return combatCheckpointError("combat.Service.TransportDeaths.pending", "an absent transport handoff")
	}
	if s.communityAreaCurrentGen != 0 {
		return combatCheckpointError("combat.Service.communityAreaCurrentGen", "zero outside an area-damage transaction")
	}
	if _, err := CheckpointRulesKind(s.Rules); err != nil {
		return err
	}
	if err := s.validateCheckpointBindings(c); err != nil {
		return err
	}
	for i, row := range s.incoming[:count] {
		if row.weapon != nil {
			if _, err := c.Units.Keys.Weapon(row.weapon); err != nil {
				return combatCheckpointError(fmt.Sprintf("combat.Service.incoming[%d].weapon", i), "an admitted weapon object")
			}
		}
	}
	return nil
}

func combatCheckpointError(path, expected string) error {
	return fmt.Errorf("nanolathe: combat checkpoint failed: logical path %s, providers searched [combat], expected %s", path, expected)
}
