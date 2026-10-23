package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

// writeCheckpointRuntime is the stored-value leaf of DESIGN_MULTIPLAYER
// §16.3.39. The parent must first attest actual Rules/CommunitySources/service
// bindings and the publication/tail boundary. Names below identify those
// already-verified bindings; this leaf neither verifies nor invokes them.
//
// Boundary precedes the lexical Session fields below. Other service/scenario
// payloads belong to their own sections. CommunitySources is a parent-checked
// frozen binding. pendingBattle and seatCommands.issuing are refusals, not
// payload. Clock pacing, seat-command queues/receipts/issuer, result views,
// rulesDefaultsApplied, player names/colours, publication/snapshot/scratch,
// diagnostics, observers, HUD and device state remain excluded (§16.3.5).
//
// runtime.Wind is a tag byte: bit 0 is the wind's presence, as the presence
// byte always was, and bit 1 says the session is an online battle, whose
// changeable seat state follows at its lexical place,
// runtime.onlineResults.sharedVictory — each row's shared-victory bit, which
// the SharedVictory seat command writes (online_alliance.go). A
// single-player session writes the tag as the presence byte and no section,
// so its bytes are unchanged. The online alliance matrix needs no section of
// its own: it is economy's row A of every player, already in that owner's
// payload; the lobby teams are configuration.
func (s *Session) writeCheckpointRuntime(e *checkpoint.Encoder, keys *content.CheckpointKeys, boundary CheckpointBoundary) error {
	if e == nil {
		return runtimeCheckpointError("runtime", "an encoder")
	}
	e.Field("runtime")
	if s == nil || keys == nil || s.Clock == nil {
		e.Fail(runtimeCheckpointError("runtime", "a session, frozen keys and clock"))
		return e.Err()
	}
	if boundary < CheckpointEntry || boundary > CheckpointFinalPumpTick {
		e.Fail(runtimeCheckpointError("runtime.boundary", "entry, interior tick or final pump tick"))
		return e.Err()
	}
	if s.pendingBattle || s.seatCommands.issuing {
		e.Fail(runtimeCheckpointError("runtime.boundary", "no pending battle transition or active seat-command dispatch"))
		return e.Err()
	}
	e.Field("runtime.boundary")
	e.U8(uint8(boundary))
	e.Field("runtime.CampaignSlot")
	e.I64(int64(s.CampaignSlot))
	e.Field("runtime.Clock.GlobalTick")
	e.U32(s.Clock.GlobalTick)
	e.Field("runtime.Community")
	if err := s.Community.WriteCheckpoint(e); err != nil {
		return err
	}
	e.Field("runtime.DefeatDone")
	e.Bool(s.DefeatDone)
	e.Field("runtime.EnemyOwner")
	e.U8(s.EnemyOwner)
	e.Field("runtime.EntryCommunity")
	if err := s.EntryCommunity.WriteCheckpoint(e); err != nil {
		return err
	}
	e.Field("runtime.Gameplay")
	e.String(string(s.Gameplay))
	e.Field("runtime.Latch.Bits")
	e.U16(s.Latch.Bits)
	e.Field("runtime.Latch.Countdown")
	e.I16(s.Latch.Countdown)
	e.Field("runtime.Latch.Pending")
	e.U8(uint8(s.Latch.Pending))
	e.Field("runtime.LocalOwner")
	e.U8(s.LocalOwner)
	e.Field("runtime.Meteor")
	writeCheckpointMeteor(e, keys, s.Meteor)
	e.Field("runtime.Mutators")
	writeCheckpointMutators(e, s.Mutators)
	e.Field("runtime.Progress.BetweenMissions")
	e.I64(int64(s.Progress.BetweenMissions))
	e.Field("runtime.Progress.Thumbs")
	for _, v := range s.Progress.Thumbs {
		e.U8(v)
	}
	e.Field("runtime.Progress.WL")
	for _, v := range s.Progress.WL {
		e.U8(v)
	}
	e.Field("runtime.RNGCrtSeed")
	e.U32(s.RNGCrtSeed)
	e.Field("runtime.RNGSimSeed")
	e.U32(s.RNGSimSeed)
	e.Field("runtime.Restrictions")
	restrictions := s.Restrictions.Entries()
	e.Count(len(restrictions))
	for _, r := range restrictions {
		e.U8(r.Count)
		e.String(r.Unit)
	}
	e.Field("runtime.Rules.Base")
	e.String(string(s.Rules.Base))
	e.Field("runtime.Rules.Name")
	e.String(s.Rules.Name)
	e.Field("runtime.Skirmish")
	writeCheckpointSkirmish(e, s.Skirmish)
	e.Field("runtime.State")
	e.U8(uint8(s.State))
	e.Field("runtime.VictoryDone")
	e.Bool(s.VictoryDone)
	e.Field("runtime.ViewingOwner")
	e.U8(s.ViewingOwner)
	e.Field("runtime.Wind")
	var tag uint8
	if s.Wind != nil {
		tag |= 1
	}
	if s.onlineResults != nil {
		tag |= 2
	}
	if len(s.economyUpkeepRestore) != 0 {
		tag |= 4
	}
	e.U8(tag)
	if s.Wind != nil {
		if err := s.Wind.WriteCheckpoint(e); err != nil {
			return err
		}
	}
	e.Field("runtime.battleEntryTailDone")
	e.Bool(s.battleEntryTailDone)
	e.Field("runtime.builderOptionsReady")
	e.Bool(s.builderOptionsReady)
	e.Field("runtime.campaignPlayerSide")
	for _, v := range s.campaignPlayerSide {
		e.I8(v)
	}
	e.Field("runtime.campaignPlayerSideKnown")
	for _, v := range s.campaignPlayerSideKnown {
		e.Bool(v)
	}
	e.Field("runtime.deathmatchActive")
	e.Bool(s.deathmatchActive)
	e.Field("runtime.deathmatchAttempts")
	e.U16(s.deathmatchAttempts)
	e.Field("runtime.deathmatchExhausted")
	e.Bool(s.deathmatchExhausted)
	e.Field("runtime.deathsWithNoRecordedCause")
	e.I64(int64(s.deathsWithNoRecordedCause))
	if len(s.economyUpkeepRestore) != 0 {
		e.Field("runtime.economyUpkeepRestore")
		e.Count(len(s.economyUpkeepRestore))
		for _, r := range s.economyUpkeepRestore {
			e.U8(r.Owner)
			e.U16(uint16(r.Unit.Handle))
			e.U64(r.Unit.Serial)
		}
	}
	e.Field("runtime.noShake")
	e.Bool(s.noShake)
	if s.onlineResults != nil {
		e.Field("runtime.onlineResults.sharedVictory")
		for _, v := range s.onlineResults.sharedVictory {
			e.Bool(v)
		}
	}
	e.Field("runtime.pendingCommanderDeaths")
	for _, v := range s.pendingCommanderDeaths {
		e.Bool(v)
	}
	e.Field("runtime.playerBuilderOptions")
	for _, v := range s.playerBuilderOptions {
		writeCheckpointBuilderOptions(e, v)
	}
	e.Field("runtime.result")
	writeCheckpointResult(e, s.result)
	e.Field("runtime.resultArmedTick")
	e.U32(s.resultArmedTick)
	e.Field("runtime.resultPending")
	e.Bool(s.resultPending)
	e.Field("runtime.resultPendingDraw")
	e.Bool(s.resultPendingDraw)
	e.Field("runtime.resultPendingLosers")
	writeCheckpointRuntimeSlots(e, s.resultPendingLosers)
	e.Field("runtime.resultPendingReason")
	e.String(s.resultPendingReason)
	e.Field("runtime.resultPendingWinner")
	e.I64(int64(s.resultPendingWinner))
	e.Field("runtime.rngCrt.State")
	e.U32(s.rngCrt.State)
	e.Field("runtime.rngCrt.draws")
	e.U64(s.rngCrt.Draws())
	e.Field("runtime.rngInitialized")
	e.Bool(s.rngInitialized)
	e.Field("runtime.rngSim.State")
	e.U32(s.rngSim.State)
	e.Field("runtime.rngSim.draws")
	e.U64(s.rngSim.Draws())
	e.Field("runtime.seatCommands.removed")
	for _, v := range s.seatCommands.removed {
		e.Bool(v)
	}
	e.Field("runtime.shakeActive")
	e.Bool(s.shakeActive)
	e.Field("runtime.shakeAmpX")
	e.I32(s.shakeAmpX)
	e.Field("runtime.shakeAmpY")
	e.I32(s.shakeAmpY)
	e.Field("runtime.shakeDuration")
	e.I32(s.shakeDuration)
	e.Field("runtime.shakeOffsetX")
	e.I32(s.shakeOffsetX)
	e.Field("runtime.shakeOffsetY")
	e.I32(s.shakeOffsetY)
	e.Field("runtime.shakeRemaining")
	e.I32(s.shakeRemaining)
	return e.Err()
}

// Meteor fields are lexical, with all stored scheduler operands preserved
// rather than recalculated from authored parameters [08 "Meteor showers"].
func writeCheckpointMeteor(e *checkpoint.Encoder, keys *content.CheckpointKeys, m MeteorState) {
	e.Field("runtime.Meteor.Active")
	e.Bool(m.Active)
	e.Field("runtime.Meteor.DurationTicks")
	e.I32(m.DurationTicks)
	e.Field("runtime.Meteor.Enabled")
	e.Bool(m.Enabled)
	e.Field("runtime.Meteor.Initialized")
	e.Bool(m.Initialized)
	e.Field("runtime.Meteor.IntervalTicks")
	e.I32(m.IntervalTicks)
	e.Field("runtime.Meteor.NextHit")
	e.U32(m.NextHit)
	e.Field("runtime.Meteor.NextStrike")
	e.U32(m.NextStrike)
	e.Field("runtime.Meteor.OriginX")
	e.I32(m.OriginX)
	e.Field("runtime.Meteor.OriginZ")
	e.I32(m.OriginZ)
	e.Field("runtime.Meteor.PerHitDelay")
	e.I32(m.PerHitDelay)
	e.Field("runtime.Meteor.Radius")
	e.I32(m.Radius)
	e.Field("runtime.Meteor.StrikeEnds")
	e.U32(m.StrikeEnds)
	e.Field("runtime.Meteor.TargetX")
	e.I32(m.TargetX)
	e.Field("runtime.Meteor.TargetZ")
	e.I32(m.TargetZ)
	e.Field("runtime.Meteor.Weapon")
	e.Bool(m.Weapon != nil)
	if m.Weapon != nil {
		ref, err := keys.Weapon(m.Weapon)
		if err != nil {
			e.Fail(err)
			return
		}
		e.Definition(ref)
	}
	e.Field("runtime.Meteor.WeaponName")
	e.String(m.WeaponName)
}

// The factor fields are lexical. Den precedes Num and raw zero factors are
// retained: content normalization is entry work, not capture (§16.3.39).
// BuilderSpeed sorts after BuildSpeed (DESIGN_MODS_MUTATORS "Builder speed")
// and SmartMakers after Sight ("Smart makers").
func writeCheckpointMutators(e *checkpoint.Encoder, m content.Mutators) {
	for _, factor := range [...]content.Factor{m.AreaOfEffect, m.BuildCost, m.BuildSpeed, m.BuilderSpeed, m.Damage, m.FireRate, m.Health, m.Income, m.Radar, m.Salvage, m.Sight, m.SmartMakers, m.UnitSpeed} {
		e.U8(factor.Den)
		e.U8(factor.Num)
	}
}

// Guard then Patrol, each in stored domain order, including inactive options.
func writeCheckpointBuilderOptions(e *checkpoint.Encoder, o orders.BuilderOptions) {
	for _, v := range o.Guard {
		e.U8(uint8(v))
	}
	for _, v := range o.Patrol {
		e.U8(uint8(v))
	}
}

// The two result lists preserve duplicates and stored order. Kind, WinnerTeam,
// Scores, ColumnMaxima and Survival are presentation views (§16.3.5).
func writeCheckpointResult(e *checkpoint.Encoder, r Result) {
	e.U32(r.ArmedTick)
	e.I16(r.Countdown)
	e.Bool(r.Draw)
	e.Bool(r.Ended)
	writeCheckpointRuntimeSlots(e, r.Losers)
	e.String(r.Reason)
	e.U32(r.Tick)
	writeCheckpointRuntimeSlots(e, r.Winners)
}

func writeCheckpointRuntimeSlots(e *checkpoint.Encoder, slots []int) {
	e.Count(len(slots))
	for _, v := range slots {
		e.I64(int64(v))
	}
}

// Skirmish's lexical fields retain raw values; no defaults, rule selection or
// lobby normalization occurs. Fixed player rows omit their presentation-only
// Nickname and Color. rulesDefaultsApplied is entry bookkeeping (§16.3.39).
func writeCheckpointSkirmish(e *checkpoint.Encoder, c SkirmishConfig) {
	e.I64(int64(c.CommanderDeath))
	e.I64(int64(c.Difficulty))
	e.String(string(c.Gameplay))
	e.I64(int64(c.LOSType))
	e.I64(int64(c.LineOfSight))
	e.I64(int64(c.Location))
	e.String(c.MapName)
	e.I64(int64(c.Mapping))
	e.I64(int64(c.NumPlayers))
	for _, p := range c.Players {
		e.U8(uint8(p.AI))
		e.I64(int64(p.AllyGroup))
		e.I64(int64(p.Controller))
		e.I64(int64(p.Energy))
		e.I64(int64(p.Metal))
		e.I64(int64(p.Side))
	}
	e.U32(c.RNGCrtSeed)
	e.U32(c.RNGSimSeed)
	e.Bool(c.Survival.Enabled)
	e.Bool(c.Survival.NoAir)
	e.Bool(c.Survival.NoNaval)
	e.U8(uint8(c.Survival.Pace))
	e.I64(int64(c.UnitLimit))
}

func runtimeCheckpointError(path, expected string) error {
	return fmt.Errorf("nanolathe: checkpoint owner runtime: logical path %s, providers searched [], expected %s", path, expected)
}

// appendCheckpointRuntimeSummary implements exactly the selected words of
// §16.3.39. It does not inspect bindings, resolve content, initialize RNG or
// establish whole-session quiescence. Wind's selected NaN fails atomically.
func (s *Session) appendCheckpointRuntimeSummary(out *checkpoint.Summary) error {
	if s == nil || s.Clock == nil || out == nil {
		return runtimeCheckpointError("runtime.summary", "a session, clock and summary")
	}
	next := *out
	next.Word(uint64(s.Clock.GlobalTick))
	next.Word(uint64(s.State))
	next.Word(checkpointRuntimeBool(s.rngInitialized))
	next.Word(uint64(s.rngSim.State))
	next.Word(s.rngSim.Draws())
	next.Word(uint64(s.rngCrt.State))
	next.Word(s.rngCrt.Draws())
	next.Word(uint64(s.Latch.Bits))
	next.Word(uint64(int64(s.Latch.Countdown)))
	next.Word(uint64(s.Latch.Pending))
	next.Word(checkpointRuntimeBool(s.resultPending))
	next.Word(checkpointRuntimeBool(s.result.Ended))
	next.Word(checkpointRuntimeBool(s.result.Draw))
	for _, v := range s.pendingCommanderDeaths {
		next.Word(checkpointRuntimeBool(v))
	}
	next.Word(checkpointRuntimeBool(s.Wind != nil))
	if s.Wind != nil {
		if err := s.Wind.AppendCheckpointSummary(&next); err != nil {
			return err
		}
	}
	next.Word(checkpointRuntimeBool(s.Meteor.Active))
	next.Word(uint64(s.Meteor.NextStrike))
	next.Word(uint64(s.Meteor.StrikeEnds))
	next.Word(uint64(s.Meteor.NextHit))
	next.Word(checkpointRuntimeBool(s.shakeActive))
	next.Word(uint64(int64(s.shakeRemaining)))
	if len(s.economyUpkeepRestore) != 0 {
		next.Word(4) // runtime's optional upkeep-restoration extension
		next.Word(uint64(len(s.economyUpkeepRestore)))
		for _, r := range s.economyUpkeepRestore {
			next.Word(uint64(r.Owner))
			next.Word(uint64(r.Unit.Handle))
			next.Word(r.Unit.Serial)
		}
	}
	*out = next
	return nil
}

func checkpointRuntimeBool(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}
