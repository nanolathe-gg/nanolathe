package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// DebugSnapshot reads an already stopped owner. It does not publish, allocate
// missing services, draw RNG, or refresh any clock or phase state.
func (s *Session) DebugSnapshot() map[string]any {
	if s == nil {
		return nil
	}
	d := map[string]any{"pending_human_commands": s.PendingHumanCommands(), "state": s.State,
		"pending_battle":           s.pendingBattle,
		"gameplay":                 s.Gameplay.Normalize(),
		"gameplay_features":        s.Community,
		"entry_gameplay_features":  s.EntryCommunity,
		"gameplay_features_digest": s.Community.Digest(),
		"schema_spawn_attempts":    append([]string(nil), s.communitySchema.diagnostics...),
		"rules":                    s.Rules.Name,
		"rng_sim_state":            s.rngSim.State,
		"rng_crt_state":            s.rngCrt.State,
		"rng_initialized":          s.rngInitialized,
		"initial_rng_sim_seed":     s.RNGSimSeed,
		"initial_rng_crt_seed":     s.RNGCrtSeed,
		"local_owner":              s.LocalOwner,
		"viewing_owner":            s.ViewingOwner,
		"enemy_owner":              s.EnemyOwner,
		"campaign_slot":            s.CampaignSlot,
		"end_latch":                s.Latch,
		"result_pending":           s.resultPending,
		"pending_winner":           s.resultPendingWinner,
		"pending_losers":           append([]int(nil), s.resultPendingLosers...),
		"pending_reason":           s.resultPendingReason,
		"pending_draw":             s.resultPendingDraw,
		"result_armed_tick":        s.resultArmedTick,
		"pending_commander_deaths": s.pendingCommanderDeaths,
		"meteor":                   s.Meteor,
		"phase_trace":              append([]string(nil), s.phaseTrace...),
		"phase_draw_trace":         append([]PhaseDrawDelta(nil), s.phaseDrawTrace...)}
	if s.Combat != nil {
		d["area_damage_saturations"] = s.Combat.CommunityAreaSaturations()
	}
	if s.Clock != nil {
		c := *s.Clock
		d["clock"] = &c
		d["clock_private_fields"] = "pending/slew/flags unavailable; exported clock fields only"
	}
	if s.Snapshot != nil {
		if f := s.Snapshot.Current(); f != nil {
			d["committed_tick"] = f.Tick
			d["committed_paused"] = f.Paused
		}
	}
	result := s.result
	result.Losers = append([]int(nil), s.result.Losers...)
	result.Winners = append([]int(nil), s.result.Winners...)
	result.Scores = append([]frame.ResultScore(nil), s.result.Scores...)
	d["result"] = result
	if s.Build != nil {
		d["repair_bank_fallbacks"] = s.Build.RepairBankFallbacks
	}
	if s.Econ != nil {
		d["economy"] = s.Econ.ParitySnapshot(s.Units)
	}
	if s.publication != nil {
		d["unit_identity_entries"] = len(s.publication.unitIdentities)
		d["unit_identity_capacity"] = cap(s.publication.unitIdentities)
		d["fragment_metadata_count"] = len(s.publication.fragments)
		d["staged_events"] = s.publication.events.Snapshot()
	}
	return d
}

// DebugUnit carries the existing publication identity only if it names this
// exact live occupant. Zero explicitly means not yet published; no ID is minted.
type DebugUnit struct {
	PublishedIdentity uint64
	Selected          bool
	Callbacks         *cob.DebugCallbacks
	Unit              ParityUnit
	Orders            *orders.DebugState
	Script            *cob.DebugState
}

func (s *Session) VisitDebugUnits(visit func(DebugUnit) error) error {
	if s == nil || s.Units == nil {
		return nil
	}
	for _, u := range s.Units.IterSliced() {
		if u == nil {
			continue
		}
		d := DebugUnit{Selected: u.Flags&units.SelectedStatus != 0, Callbacks: u.ScriptBridge().DebugSnapshot(), Unit: s.parityUnit(u), Orders: orders.QueueOfUnit(u).DebugSnapshot(u.Handle), Script: u.GetScript().DebugSnapshot()}
		d.PublishedIdentity = s.debugUnitIdentity(u)
		if err := visit(d); err != nil {
			return err
		}
	}
	return nil
}

// debugUnitIdentity observes only an existing publication identity. Diagnostic
// histories must resolve this when recording, before the pool slot can be reused.
func (s *Session) debugUnitIdentity(u *units.Unit) uint64 {
	if s != nil && u != nil && s.publication != nil && int(u.Handle) < len(s.publication.unitIdentities) {
		id := s.publication.unitIdentities[int(u.Handle)]
		if id.unit == u {
			return id.id
		}
	}
	return 0
}

// DebugClock is a detached exported clock projection for callers without a full capture.
func (s *Session) DebugClock() clock.State {
	if s != nil && s.Clock != nil {
		return *s.Clock
	}
	return clock.State{}
}
