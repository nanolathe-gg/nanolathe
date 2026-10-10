package economy

import (
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// CheckpointContext shares the capture's singleton terrain owner. Economy adds
// no reference tables (DESIGN_MULTIPLAYER §16.3.13).
type CheckpointContext struct {
	World *world.CheckpointContext

	bindingService   *Service
	bindingWind      *world.Wind
	bindingAuthority *checkpoint.BindingAuthority
}

// NewCheckpointContext binds the world context without reading service state.
func NewCheckpointContext(w *world.CheckpointContext) *CheckpointContext {
	return &CheckpointContext{World: w}
}

// CollectCheckpointReferences validates composition without registering objects.
func (s *Service) CollectCheckpointReferences(c *CheckpointContext) (int, error) {
	return 0, s.checkpointBindings(c)
}

// WriteCheckpoint writes the economy payload only, in source-field lexical
// order: CloakCost, CloakDue, Community, EconomySelector, EndCondition,
// Networked, Players, ReferencePlayer, Terrain, Wind, unitBuckets. Callbacks,
// Wind and Terrain use validated presence tags; EconomySelector is a tag byte
// then i64 when present. The tag's bit 0 is the selector's presence and bit 1
// says the player rows carry their own words (OwnSelector, Selector), so a
// ledger in which no player has one writes the tag as the presence byte it
// has always been and its player rows unchanged (docs/DESIGN_MULTIPLAYER.md
// §6.6). Players is a fixed array; unitBuckets has its stored count and
// includes every physical row, even unused
// rows and slot zero. UnitEconomy is Archived then Buckets; resource pairs are
// always Metal then Energy. No accessor grows or resets a bucket during capture
// (DESIGN_MULTIPLAYER §16.3.5–§16.3.6, §16.3.13).
func (s *Service) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error {
	e.Field("economy.Service")
	if err := s.checkpointBindings(c); err != nil {
		e.Fail(err)
		return e.Err()
	}
	e.Field("economy.Service.CloakCost")
	e.Bool(s.cloakCost != nil)
	e.Field("economy.Service.CloakDue")
	e.Bool(s.cloakDue != nil)
	if err := s.Community.WriteCheckpoint(e); err != nil {
		return err
	}
	seats := s.hasPlayerSelectors()
	var tag uint8
	if s.EconomySelector != nil {
		tag |= 1
	}
	if seats {
		tag |= 2
	}
	e.Field("economy.Service.EconomySelector")
	e.U8(tag)
	if s.EconomySelector != nil {
		e.I64(int64(*s.EconomySelector))
	}
	e.Field("economy.Service.EndCondition")
	e.Bool(s.endCondition != nil)
	e.Field("economy.Service.Networked")
	e.Bool(s.Networked)
	for slot := range s.Players {
		writeCheckpointPlayer(e, &s.Players[slot], fmt.Sprintf("economy.Service.Players[%d]", slot), seats)
	}
	e.Field("economy.Service.ReferencePlayer")
	e.I64(int64(s.ReferencePlayer))
	e.Field("economy.Service.Terrain")
	e.Bool(s.Terrain != nil)
	e.Field("economy.Service.Wind")
	e.Bool(s.Wind != nil)
	e.Field("economy.Service.unitBuckets")
	e.Count(len(s.unitBuckets))
	for slot := range s.unitBuckets {
		writeCheckpointUnitEconomy(e, &s.unitBuckets[slot], slot)
	}
	return e.Err()
}

// Player fields: AIConsumption, AIProduction, Allies, ArchivedMirror,
// AutoShareEnergy, AutoShareMetal, AutoShareSensor, Capacity, CommanderKills,
// CommanderLosses, ControllerState, EndGameCountdown, EnergyShareThreshold,
// Exists, FullIncome, GameEnded, IsObserver, Kills, Losses, MetalShareThreshold,
// Mirror, OptionKind, OwnSelector, PassConsumed, PassProduced, RejectionReason,
// ResultAuxiliary, Selector, Side, Stock, StorageBonus, StorageBonusEnabled,
// TotalConsumed, TotalProduced, UpdateTime, Waste, Watcher. OwnSelector (bool)
// and Selector (i64) are written only when seats is set, which the service's
// EconomySelector tag announces. Names, logos, rank, WinLoseTime,
// DisplayTimer and aiAggregatesPrepared have the reviewed exclusions; capture
// never applies a profile or reconstructs stored accounting.
func writeCheckpointPlayer(e *checkpoint.Encoder, p *Player, path string, seats bool) {
	writeCheckpointF32Pair(e, p.AIConsumption, path+".AIConsumption")
	writeCheckpointF32Pair(e, p.AIProduction, path+".AIProduction")
	e.FieldChild(path, "Allies")
	for _, v := range p.Allies {
		e.Bool(v)
	}
	writeCheckpointArchived(e, p.ArchivedMirror, path+".ArchivedMirror")
	e.FieldChild(path, "AutoShareEnergy")
	e.Bool(p.AutoShareEnergy)
	e.FieldChild(path, "AutoShareMetal")
	e.Bool(p.AutoShareMetal)
	e.FieldChild(path, "AutoShareSensor")
	e.Bool(p.AutoShareSensor)
	writeCheckpointF32Pair(e, p.Capacity, path+".Capacity")
	e.FieldChild(path, "CommanderKills")
	e.I16(p.CommanderKills)
	e.FieldChild(path, "CommanderLosses")
	e.I16(p.CommanderLosses)
	e.FieldChild(path, "ControllerState")
	e.U8(p.ControllerState)
	e.FieldChild(path, "EndGameCountdown")
	e.I32(p.EndGameCountdown)
	e.FieldChild(path, "EnergyShareThreshold")
	e.F32(p.EnergyShareThreshold)
	e.FieldChild(path, "Exists")
	e.Bool(p.Exists)
	e.FieldChild(path, "FullIncome")
	e.Bool(p.FullIncome)
	e.FieldChild(path, "GameEnded")
	e.Bool(p.GameEnded)
	e.FieldChild(path, "IsObserver")
	e.Bool(p.IsObserver)
	e.FieldChild(path, "Kills")
	e.I16(p.Kills)
	e.FieldChild(path, "Losses")
	e.I16(p.Losses)
	e.FieldChild(path, "MetalShareThreshold")
	e.F32(p.MetalShareThreshold)
	writeCheckpointBuckets(e, p.Mirror, path+".Mirror")
	e.FieldChild(path, "OptionKind")
	e.U8(p.OptionKind)
	if seats {
		e.FieldChild(path, "OwnSelector")
		e.Bool(p.OwnSelector)
	}
	writeCheckpointF32Pair(e, p.PassConsumed, path+".PassConsumed")
	writeCheckpointF32Pair(e, p.PassProduced, path+".PassProduced")
	e.FieldChild(path, "RejectionReason")
	e.U8(p.RejectionReason)
	e.FieldChild(path, "ResultAuxiliary")
	e.U32(p.ResultAuxiliary)
	if seats {
		e.FieldChild(path, "Selector")
		e.I64(int64(p.Selector))
	}
	e.FieldChild(path, "Side")
	e.U8(p.Side)
	writeCheckpointF32Pair(e, p.Stock, path+".Stock")
	writeCheckpointF32Pair(e, p.StorageBonus, path+".StorageBonus")
	e.FieldChild(path, "StorageBonusEnabled")
	e.Bool(p.StorageBonusEnabled)
	writeCheckpointF64Pair(e, p.TotalConsumed, path+".TotalConsumed")
	writeCheckpointF64Pair(e, p.TotalProduced, path+".TotalProduced")
	e.FieldChild(path, "UpdateTime")
	e.U32(p.UpdateTime)
	writeCheckpointF64Pair(e, p.Waste, path+".Waste")
	e.FieldChild(path, "Watcher")
	e.Bool(p.Watcher)
}

// hasPlayerSelectors reports whether any player row holds a word of its own
// or a stored word, the state the EconomySelector tag's bit 1 announces.
func (s *Service) hasPlayerSelectors() bool {
	for slot := range s.Players {
		if p := &s.Players[slot]; p.OwnSelector || p.Selector != 0 {
			return true
		}
	}
	return false
}

func writeCheckpointF32Pair(e *checkpoint.Encoder, pair [2]float32, path string) {
	for resource, v := range pair {
		e.FieldIndex(path, resource, "")
		e.F32(v)
	}
}

func writeCheckpointF64Pair(e *checkpoint.Encoder, pair [2]float64, path string) {
	for resource, v := range pair {
		e.FieldIndex(path, resource, "")
		e.F64(v)
	}
}

// Bucket field order is Accepted, Carry, Production, Requested; the resource
// order wraps the record, so one whole metal bucket precedes the energy bucket.
func writeCheckpointBuckets(e *checkpoint.Encoder, pair [2]Bucket, path string) {
	for resource, v := range pair {
		e.FieldIndex(path, resource, ".Accepted")
		e.F32(v.Accepted)
		e.FieldIndex(path, resource, ".Carry")
		e.F32(v.Carry)
		e.FieldIndex(path, resource, ".Production")
		e.F32(v.Production)
		e.FieldIndex(path, resource, ".Requested")
		e.F32(v.Requested)
	}
}

func writeCheckpointArchived(e *checkpoint.Encoder, pair [2]ArchivedBucket, path string) {
	for resource, v := range pair {
		e.FieldIndex(path, resource, ".Production")
		e.F32(v.Production)
		e.FieldIndex(path, resource, ".Requested")
		e.F32(v.Requested)
	}
}

// Select the physical row lazily, with fixed resource suffixes, so unused
// storage costs no diagnostic path allocations (DESIGN_MULTIPLAYER §16.3.81).
func writeCheckpointUnitEconomy(e *checkpoint.Encoder, row *UnitEconomy, slot int) {
	const path = "economy.Service.unitBuckets"
	archivedPaths := [2][2]string{
		{".Archived[0].Production", ".Archived[0].Requested"},
		{".Archived[1].Production", ".Archived[1].Requested"},
	}
	for resource, v := range row.Archived {
		e.FieldIndex(path, slot, archivedPaths[resource][0])
		e.F32(v.Production)
		e.FieldIndex(path, slot, archivedPaths[resource][1])
		e.F32(v.Requested)
	}
	bucketPaths := [2][4]string{
		{".Buckets[0].Accepted", ".Buckets[0].Carry", ".Buckets[0].Production", ".Buckets[0].Requested"},
		{".Buckets[1].Accepted", ".Buckets[1].Carry", ".Buckets[1].Production", ".Buckets[1].Requested"},
	}
	for resource, v := range row.Buckets {
		e.FieldIndex(path, slot, bucketPaths[resource][0])
		e.F32(v.Accepted)
		e.FieldIndex(path, slot, bucketPaths[resource][1])
		e.F32(v.Carry)
		e.FieldIndex(path, slot, bucketPaths[resource][2])
		e.F32(v.Production)
		e.FieldIndex(path, slot, bucketPaths[resource][3])
		e.F32(v.Requested)
	}
}

func (s *Service) checkpointBindings(c *CheckpointContext) error {
	if s == nil {
		return economyCheckpointError("economy.Service", "a present service")
	}
	if c == nil || c.World == nil {
		return economyCheckpointError("economy.context", "a world checkpoint context")
	}
	if s.Terrain != c.World.Terrain {
		return economyCheckpointError("economy.Service.Terrain", "the collected terrain identity")
	}
	return s.validateCheckpointBindings(c)
}

// AppendCheckpointSummary appends the cheap economy row directly from stored
// state. Per player: Stock, Capacity, Mirror (Production, Requested, Accepted,
// Carry), AIProduction, AIConsumption, UpdateTime, GameEnded, EndGameCountdown,
// Allies; then unitBuckets length and each physical row's Buckets (Production,
// Requested, Accepted, Carry). Resource pairs are Metal then Energy. The fixed
// player array has no count. Configuration/bindings, archived accounting and
// pass counters are deliberately outside this row (DESIGN_MULTIPLAYER §16.3.7).
// No producer, encoder or graph walk is called; failure leaves the caller's
// accumulator unchanged, and the successful path allocates nothing.
func (s *Service) AppendCheckpointSummary(summary *checkpoint.Summary) error {
	if s == nil {
		return economyCheckpointError("economy.Service", "a present service")
	}
	if summary == nil {
		return economyCheckpointError("economy.summary", "a summary accumulator")
	}
	next := *summary
	for slot := range s.Players {
		p := &s.Players[slot]
		for _, pair := range []struct {
			name   string
			values [2]float32
		}{
			{"Stock", p.Stock}, {"Capacity", p.Capacity},
		} {
			for resource, v := range pair.values {
				if err := appendCheckpointFloat(&next, v, "Players", slot, pair.name, resource); err != nil {
					return err
				}
			}
		}
		if err := appendCheckpointBuckets(&next, p.Mirror, "Players", slot, "Mirror"); err != nil {
			return err
		}
		for _, pair := range []struct {
			name   string
			values [2]float32
		}{
			{"AIProduction", p.AIProduction}, {"AIConsumption", p.AIConsumption},
		} {
			for resource, v := range pair.values {
				if err := appendCheckpointFloat(&next, v, "Players", slot, pair.name, resource); err != nil {
					return err
				}
			}
		}
		next.Word(uint64(p.UpdateTime))
		next.Word(checkpointBoolWord(p.GameEnded))
		next.Word(uint64(int64(p.EndGameCountdown)))
		for _, allied := range p.Allies {
			next.Word(checkpointBoolWord(allied))
		}
	}
	next.Word(uint64(len(s.unitBuckets)))
	for slot := range s.unitBuckets {
		if err := appendCheckpointBuckets(&next, s.unitBuckets[slot].Buckets, "unitBuckets", slot, "Buckets"); err != nil {
			return err
		}
	}
	*summary = next
	return nil
}

func appendCheckpointBuckets(summary *checkpoint.Summary, pair [2]Bucket, table string, slot int, field string) error {
	for resource, b := range pair {
		for _, v := range []struct {
			name  string
			value float32
		}{
			{"Production", b.Production}, {"Requested", b.Requested}, {"Accepted", b.Accepted}, {"Carry", b.Carry},
		} {
			bits := math.Float32bits(v.value)
			if bits&0x7f800000 == 0x7f800000 && bits&0x007fffff != 0 {
				return economyCheckpointError(fmt.Sprintf("economy.Service.%s[%d].%s[%d].%s", table, slot, field, resource, v.name), "non-NaN binary32")
			}
			summary.Word(uint64(bits))
		}
	}
	return nil
}

func appendCheckpointFloat(summary *checkpoint.Summary, v float32, table string, slot int, field string, resource int) error {
	bits := math.Float32bits(v)
	if bits&0x7f800000 == 0x7f800000 && bits&0x007fffff != 0 {
		return economyCheckpointError(fmt.Sprintf("economy.Service.%s[%d].%s[%d]", table, slot, field, resource), "non-NaN binary32")
	}
	summary.Word(uint64(bits))
	return nil
}

func checkpointBoolWord(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}

func economyCheckpointError(path, expected string) error {
	return fmt.Errorf("nanolathe: checkpoint capture failed: logical path %s, providers searched [economy], expected %s", path, expected)
}
