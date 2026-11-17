package session

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Receiver admission of stamped seat commands (docs/DESIGN_MULTIPLAYER.md
// §7.2, §7.4.1–§7.4.4, §16.2 M2-C2–M2-C4). Command authorization is
// Nanolathe protocol, not retail arithmetic: retail checks roles only in the
// sending interface and trusts admitted payloads [08 "Packet framing and
// dispatch"]. Every check below reads only the payload, the admitted
// configuration and state every honest replica holds identically at phase 1,
// and every refusal happens before any queue, resource or random stream moves.

// Protocol limits of command schema version 1 (§7.4.1). They are Nanolathe
// protocol limits, not retail constants.
const (
	// seatMaxActors is the representation ceiling of an `actors` field: the
	// largest startup unit limit, 3276.
	seatMaxActors = 3276
	// seatMaxAreaEntries is the representation ceiling of an area list.
	seatMaxAreaEntries = 65535
	// seatOnlineMaxAreaEntries and seatOnlineMaxAreaWork are the online
	// per-command work limits of §15 Q28: an area list holds at most the
	// order queue guard's capacity, and actors times entries is at most 2^20.
	seatOnlineMaxAreaEntries = 10000
	seatOnlineMaxAreaWork    = 1 << 20
	// seatMaxKeyBytes is the `key` primitive's byte ceiling.
	seatMaxKeyBytes = 255
	// seatOnlineMaxCount bounds online counted production (§7.4.2).
	seatOnlineMaxCount = 32767
	// seatOnlineMaxGift is 2^31, the largest positive value the only `Give`
	// producer can generate: a typed signed 32-bit integer converted to
	// single precision [07 R-CAM-01 §6] (§7.4.1, §15 Q8, Q28).
	seatOnlineMaxGift float32 = 2147483648
)

// EnqueueSeatCommand queues one stamped stream entry for phase 1 of its tick.
// The admitted session chooses the context: OnlineCommand once an online
// configuration is admitted, SinglePlayerReplay otherwise. It validates only
// the stamp — seat 0..9, a nonzero position above every position already
// accepted, and a tick the session has not yet run — and deep-copies the
// payload. A stamp error returns an error, queues nothing and consumes no
// position. Every payload, role, permission and actor check happens at phase
// 1, where a refusal still consumes the entry's position and yields a
// CommandRejected receipt, so all replicas refuse it identically.
//
// "Unsealed" is defined against the session's committed tick: the entry's
// tick must be one phase 1 has not run, at or after GlobalTick+1. The stream
// driver that owns grants (M6) must also never bind an entry to a tick it
// has already granted; that rule is the driver's, not checked here.
//
// Positions are checked across the whole stream, not per seat: the relay's
// stream is one append-only sequence with one position per entry (§4.2), so
// a position at or below any earlier entry's is a repeat or a backward
// position, and queue order then equals stream order without a sort.
func (s *Session) EnqueueSeatCommand(stamp CommandStamp, c SeatCommand) error {
	if s == nil {
		return fmt.Errorf("nanolathe: seat command not queued: logical path <session>, providers searched [session], expected a session")
	}
	if stamp.Seat > 9 {
		return fmt.Errorf("nanolathe: seat command not queued: logical path seat %d, providers searched [stream stamp], expected seat 0..9", stamp.Seat)
	}
	if stamp.Position == 0 {
		return fmt.Errorf("nanolathe: seat command not queued: logical path seat %d position 0, providers searched [stream stamp], expected a nonzero stream position", stamp.Seat)
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	var committed uint32
	if s.Clock != nil {
		committed = s.Clock.GlobalTick
	}
	if stamp.Tick <= committed {
		return fmt.Errorf("nanolathe: seat command not queued: logical path seat %d tick %d, providers searched [stream stamp], expected an unsealed tick after committed tick %d", stamp.Seat, stamp.Tick, committed)
	}
	if stamp.Position <= s.seatCommands.lastPosition {
		return fmt.Errorf("nanolathe: seat command not queued: logical path seat %d position %d, providers searched [stream stamp], expected a stream position after %d", stamp.Seat, stamp.Position, s.seatCommands.lastPosition)
	}
	context := SinglePlayerReplay
	if s.seatCommands.online != nil {
		context = OnlineCommand
	}
	s.seatCommands.lastPosition = stamp.Position
	s.pendingHuman = append(s.pendingHuman, HumanCommand{
		DueTick: stamp.Tick,
		seat:    &seatQueued{stamp: stamp, command: c.clone(), context: context},
	})
	return nil
}

// DrainCommandReceipts returns and forgets the receipts phase 1 produced, in
// application order. Call it outside the tick. The returned slice is the
// caller's; the session keeps no reference to it.
func (s *Session) DrainCommandReceipts() []CommandReceipt {
	if s == nil {
		return nil
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	out := s.seatCommands.receipts
	s.seatCommands.receipts = nil
	return out
}

// setOnlineSeatCommands admits the online command context against an
// admitted configuration. It is the only way to reach OnlineCommand: no
// admitted multi-seat session exists until NewAdmittedSkirmish and M5 land,
// so today only tests call it. The local adapter then refuses every kind
// that changes the world. It is a function rather than a Session method so
// that the Session's reflectable method set does not keep the configuration
// API reachable before a shipped caller exists.
func setOnlineSeatCommands(s *Session, cfg EffectiveMatchConfig) error {
	r := cfg.Request()
	if s == nil || len(r.Seats) == 0 {
		return fmt.Errorf("nanolathe: online command context refused: logical path <session>, providers searched [match configuration], expected an admitted configuration")
	}
	roles := make([]MatchRole, len(r.Seats))
	for i := range r.Seats {
		roles[i] = r.Seats[i].Role
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	s.seatCommands.online = &onlineCommandConfig{roles: roles, unitLimit: r.UnitLimit, cheatsAllowed: r.CheatsAllowed}
	// The lobby teams the alliance admission reads; a battle composition
	// has its result rows by now (online_alliance.go).
	s.onlineResults.setTeams(r.Seats)
	return nil
}

// markSeatRemovedForCommands records a seat's final removal (§11.1), after
// which phase 1 refuses its commands. The relay-authored removal event that
// calls it is M6's; it runs on the simulation goroutine, as phase 1 does.
func (s *Session) markSeatRemovedForCommands(seat uint8) {
	if s != nil && seat < 10 {
		s.seatCommands.removed[seat] = true
	}
}

// applyQueuedSeatCommand authorizes one stamped entry, applies it through the
// shared payload implementation and records its receipt.
func (s *Session) applyQueuedSeatCommand(q *seatQueued, tick uint32) {
	issuer, why := s.admitSeatCommand(q)
	outcome, diagnostic := CommandRejected, ""
	if why != "" {
		diagnostic = fmt.Sprintf("nanolathe: seat command rejected: logical path seat %d tick %d position %d kind %d, providers searched [session], expected %s", q.stamp.Seat, q.stamp.Tick, q.stamp.Position, q.command.Kind, why)
	} else if sharingSeatKind(q.command.Kind) {
		// The sharing and alliance kinds have no single-player operation, so
		// they apply from the seat payload itself (online_sharing.go).
		outcome = s.applyOnlineSharing(issuer, &q.command, tick)
	} else {
		b := s.bindSeatCommand(q, issuer)
		outcome = s.applyBound(&b, tick)
	}
	s.humanMu.Lock()
	s.seatCommands.receipts = append(s.seatCommands.receipts, CommandReceipt{Stamp: q.stamp, Outcome: outcome, Diagnostic: diagnostic})
	s.humanMu.Unlock()
}

// admitSeatCommand is the receiver's validation boundary. It returns the
// issuing seat, or a nonempty "expected" phrase naming the refusal.
func (s *Session) admitSeatCommand(q *seatQueued) (uint8, string) {
	c := &q.command
	seat := q.stamp.Seat
	switch q.context {
	case OnlineCommand:
		if why := s.onlineSeatRole(seat); why != "" {
			return 0, why
		}
		if why := onlineKindAdmission(c.Kind); why != "" && !(s.onlineResults != nil && c.Kind == SeatMobileBuild) {
			return 0, why
		}
	case SinglePlayerReplay:
		// A single-player stream is the local player's: its seat is the
		// own/controlling slot the local adapter acts for. Design reading:
		// any other seat is refused rather than acted for.
		if seat != s.LocalOwner {
			return 0, fmt.Sprintf("the local seat %d in a single-player session", s.LocalOwner)
		}
		if why := replayKindAdmission(c.Kind); why != "" {
			return 0, why
		}
	default:
		return 0, "an admitted command context"
	}
	if why := s.seatSchema(q.context, c); why != "" {
		return 0, why
	}
	if q.context == OnlineCommand {
		if why := s.onlinePermission(c); why != "" {
			return 0, why
		}
	}
	if why := s.seatAvailability(q.context, c); why != "" {
		return 0, why
	}
	if q.context == OnlineCommand {
		if why := s.onlineForeignActors(seat, c); why != "" {
			return 0, why
		}
		if c.Kind == SeatMobileBuild && !s.onlineBuildSiteKnown(seat, c.MobileBuild) {
			return 0, "a build site known to the issuing seat"
		}
		if why := s.admitOnlineSharing(seat, c); why != "" {
			return 0, why
		}
	}
	return seat, ""
}

// onlineSeatRole admits a seat that may issue commands: a human row of the
// admitted configuration that has not been finally removed and is still
// playing. Watchers, computers and the Survival attacker issue nothing; a
// human that has become a watcher is refused like a watcher row (§7.2,
// §11.4).
func (s *Session) onlineSeatRole(seat uint8) string {
	cfg := s.seatCommands.online
	if cfg == nil || int(seat) >= len(cfg.roles) {
		return "a seat of the admitted configuration"
	}
	if cfg.roles[seat] != MatchRoleHuman {
		return "a human seat; watchers, computers and the scenario attacker issue no commands"
	}
	if s.seatCommands.removed[seat] {
		return "a seat that has not been finally removed"
	}
	if s.Econ == nil {
		return "a playing seat"
	}
	p := &s.Econ.Players[seat]
	if !p.Exists || p.Watcher || p.IsObserver || s.onlineSeatEnded(int(seat)) {
		return "a playing seat, not a watcher"
	}
	return ""
}

// m5Gate names the milestone a deferred kind waits for.
const m5Gate = "a kind available online: its application needs M5's per-seat perspectives (DESIGN_MULTIPLAYER §16.2)"

// onlineKindAdmission is §7.4.2's class column for the online context: S
// kinds are admitted, D kinds wait for M5, L and R kinds and Gameplay are
// never online seat commands, and every other number is invalid.
func onlineKindAdmission(k SeatCommandKind) string {
	switch k {
	case SeatOrder, SeatStop, SeatActivation, SeatFactoryBuild, SeatCancelProduction, SeatStockpile,
		SeatGroupAssign, SeatStance, SeatCloak, SeatSelfDestruct, SeatATM, SeatSetResource, SeatGive,
		SeatMakeSelectable, SeatMeteor, SeatCancelQueuedMove, SeatSpawn, SeatBuilderOptions, SeatCommunityKickout, SeatEconomyActivity:
		return ""
	case SeatMobileBuild, SeatCommunityOrderDrag:
		return m5Gate + ": known-site admission reads the issuing seat's map knowledge"
	case SeatView, SeatVisibility:
		return m5Gate + ": viewing slot and visibility history application"
	case SeatDoubleShot, SeatHalfShot:
		return m5Gate + ": the issuing seat's own damage gates"
	case SeatShareMetal, SeatShareEnergy, SeatShareMapping, SeatShareRadar, SeatShareAll, SeatSetShareMetal,
		SeatSetShareEnergy, SeatShareGift, SeatDeclareAlliance, SeatSharedVictory:
		// Command schema version 2's sharing and alliance kinds; their
		// state-dependent admission is admitOnlineSharing's.
		return ""
	case SeatShootAll:
		// Retail's bit is per machine, so online it would govern one seat's
		// shooters only; that semantics and its cheat permission (Q8) are not
		// designed, so ShootAll has no online form yet.
		return "a single-player replay kind; ShootAll has no online seat semantics yet"
	case SeatSelectionReplace, SeatSelectionToggle, SeatSelectionClear, SeatBuildPage, SeatGroupRecall, SeatBigBrother, SeatShiftState:
		return "a seat command kind; selection, build pages, group recall, BigBrother and Shift are local interface state"
	case SeatDeveloperSpawn:
		return "a single-player replay kind; developer spawning has no online payload"
	case SeatNoShake, SeatSetLogo:
		return "a seat command kind; online it is a local presentation preference"
	case SeatGameplay:
		return "a seat command kind; the rule set is chosen in the lobby"
	}
	return "a kind number listed in command schema version 1"
}

// replayKindAdmission is the single-player replay context's class column:
// every S, D and R kind whose local operation exists is admitted; local-only
// kinds, the online-only sharing kinds and unlisted numbers are not.
func replayKindAdmission(k SeatCommandKind) string {
	switch k {
	case SeatOrder, SeatStop, SeatActivation, SeatMobileBuild, SeatFactoryBuild, SeatCancelProduction,
		SeatStockpile, SeatGroupAssign, SeatStance, SeatCloak, SeatSelfDestruct, SeatNoShake, SeatATM,
		SeatSetResource, SeatSetLogo, SeatView, SeatGive, SeatMakeSelectable, SeatVisibility, SeatDoubleShot,
		SeatHalfShot, SeatMeteor, SeatCancelQueuedMove, SeatSpawn, SeatBuilderOptions, SeatCommunityOrderDrag,
		SeatCommunityKickout, SeatDeveloperSpawn, SeatEconomyActivity, SeatShootAll, SeatGameplay:
		return ""
	case SeatSelectionReplace, SeatSelectionToggle, SeatSelectionClear, SeatBuildPage, SeatGroupRecall, SeatBigBrother, SeatShiftState:
		return "a recorded kind; local interface state has no replay payload"
	case SeatShareMetal, SeatShareEnergy, SeatShareMapping, SeatShareRadar, SeatShareAll, SeatSetShareMetal,
		SeatSetShareEnergy, SeatShareGift, SeatDeclareAlliance, SeatSharedVictory:
		// Retail's share commands, SHARE screen and Allies window act only in
		// a networked session [07 R-CAM-01 §6] [05 R-SHARE-01 §5].
		return "a kind with a single-player replay payload; sharing and alliance commands act only in a networked session"
	}
	return "a kind number listed in command schema version 1"
}

// onlinePermission enforces the room's cheat permission on every receiving
// simulation, whatever any local developer state says (§7.1, §15 Q8). A
// positive whole gift within its producer's range is ordinary sharing; any
// other amount that producer can generate needs the permission, and an
// amount no producer generates is refused in every room.
func (s *Session) onlinePermission(c *SeatCommand) string {
	cheats := s.seatCommands.online != nil && s.seatCommands.online.cheatsAllowed
	switch c.Kind {
	case SeatATM, SeatSetResource, SeatMakeSelectable, SeatMeteor, SeatSpawn:
		if !cheats {
			return "the room's cheat permission"
		}
	case SeatGive:
		a := c.Give.Amount
		if !wholeGiftAmount(a) {
			return "a whole gift amount from -2^31 to 2^31"
		}
		if !cheats && (a < 1 || a > seatOnlineMaxGift) {
			return "a positive whole gift amount from 1 to 2^31, or the room's cheat permission"
		}
	case SeatShareGift:
		// A SHARE slider reads back a whole number; zero gives nothing and a
		// positive amount is ordinary sharing (§15 Q8). A negative one
		// reverses the transfer [05 R-SHARE-01 §2], which only the room's
		// cheat permission admits.
		if !cheats && (c.ShareGift.Metal < 0 || c.ShareGift.Energy < 0) {
			return "gift amounts of zero or more, or the room's cheat permission"
		}
	}
	return ""
}

// wholeGiftAmount reports a finite whole binary32 from -2^31 to 2^31 that is
// not negative zero, the amounts `+Give` can generate. Wholeness is read from
// the encoding — no fraction bit below the binary point is set — so no
// floating conversion is involved.
func wholeGiftAmount(a float32) bool {
	bits := math.Float32bits(a)
	if bits == 0x80000000 || !(a >= -seatOnlineMaxGift && a <= seatOnlineMaxGift) {
		return false
	}
	if bits&0x7fffffff == 0 {
		return true
	}
	exponent := int((bits>>23)&0xff) - 127
	if exponent < 0 {
		return false // a nonzero magnitude below one
	}
	if exponent >= 23 {
		return true
	}
	return bits&(1<<(23-exponent)-1) == 0
}

// onlineAmount is the online `amount` domain: finite, and positive zero only.
func onlineAmount(a float32) bool {
	return math.Float32bits(a) != 0x80000000 && a >= -math.MaxFloat32 && a <= math.MaxFloat32
}

// seatSchema checks the payload against §7.4.1–§7.4.2's field domains and
// combinations. Online it also applies the agreed unit limit, the per-command
// work limits and the replay-only fields' required zeros.
func (s *Session) seatSchema(context CommandContext, c *SeatCommand) string {
	if !c.unselectedZero() || !c.unselectedSharingZero() {
		return "every payload record but the kind's own to be zero"
	}
	online := context == OnlineCommand
	limit := seatMaxActors
	if online && s.seatCommands.online != nil {
		limit = int(s.seatCommands.online.unitLimit)
	}
	switch c.Kind {
	case SeatOrder:
		p := &c.Order
		if why := validActors(p.Actors, limit, len(p.Targets) == 0); why != "" {
			return why
		}
		if p.Code < 1 || p.Code > 14 {
			return "an order code 1..14"
		}
		if !validNullableRef(p.Target) {
			return "a null or complete order target reference"
		}
		if why := validPosition(p.Position, online); why != "" {
			return why
		}
		if len(p.Targets) > seatMaxAreaEntries {
			return "at most 65535 area entries"
		}
		if online && len(p.Targets) > seatOnlineMaxAreaEntries {
			return "at most 10000 area entries online"
		}
		if online && len(p.Targets) != 0 && len(p.Actors)*len(p.Targets) > seatOnlineMaxAreaWork {
			return "an area order of at most 2^20 actor-entry visits online"
		}
		for i := range p.Targets {
			if !validNullableRef(p.Targets[i].Target) {
				return "a null or complete area target reference"
			}
			if why := validPosition(p.Targets[i].Position, online); why != "" {
				return why
			}
		}
		if p.AssignedPosition && (len(p.Actors) != 1 || p.Code != 2 || p.Target.Handle != 0 || len(p.Targets) != 0 || p.TrackQueuedMove) {
			return "an assigned position with one actor, code 2, no target and no area list"
		}
		if p.TrackQueuedMove && (p.Code != 2 || !p.Queued || p.Target.Handle != 0 || len(p.Targets) != 0 || p.AssignedPosition) {
			return "a tracked move with code 2, queued, no target and no area list"
		}
		if len(p.Targets) != 0 && (p.Target.Handle != 0 || p.AssignedPosition || p.TrackQueuedMove) {
			return "an area order with no outer target and neither special flag"
		}
	case SeatStop:
		return validActors(c.Stop.Actors, limit, false)
	case SeatActivation:
		return validActor(c.Activation.Unit)
	case SeatMobileBuild:
		p := &c.MobileBuild
		if why := validActor(p.Builder); why != "" {
			return why
		}
		if why := validKey(p.Product, false); why != "" {
			return why
		}
		if p.Facing > 3 {
			return "a facing 0..3"
		}
		return validPoint(p.Position, online)
	case SeatFactoryBuild:
		if why := validActor(c.FactoryBuild.Builder); why != "" {
			return why
		}
		if why := validKey(c.FactoryBuild.Product, false); why != "" {
			return why
		}
		return validCount(c.FactoryBuild.Count, online)
	case SeatCancelProduction:
		return validActor(c.CancelProduction.Unit)
	case SeatStockpile:
		if why := validActor(c.Stockpile.Unit); why != "" {
			return why
		}
		return validCount(c.Stockpile.Count, online)
	case SeatGroupAssign:
		if c.GroupAssign.Group < 1 || c.GroupAssign.Group > 9 {
			return "a group 1..9"
		}
		return validActors(c.GroupAssign.Members, limit, false)
	case SeatStance:
		if c.Stance.Value > 2 {
			return "a stance value 0..2"
		}
		return validActors(c.Stance.Actors, limit, false)
	case SeatCloak:
		return validActors(c.Cloak.Actors, limit, false)
	case SeatSelfDestruct:
		return validActors(c.SelfDestruct.Actors, limit, false)
	case SeatSetResource:
		p := &c.SetResource
		if online && p.Player != 0 {
			return "no player field online; the issuing seat's own stock is written"
		}
		if p.Player > 9 {
			return "a player 0..9"
		}
		if p.Resource != economy.Metal && p.Resource != economy.Energy {
			return "resource 0 metal or 1 energy"
		}
		if online && !onlineAmount(p.Amount) {
			return "a finite amount, positive zero only"
		}
	case SeatSetLogo:
		if c.SetLogo.Player > 9 {
			return "a player 0..9"
		}
	case SeatView:
		if c.View.Player > 9 {
			return "a player 0..9"
		}
	case SeatGive:
		if c.Give.Player > 9 {
			return "a recipient 0..9"
		}
		if c.Give.Resource != economy.Metal && c.Give.Resource != economy.Energy {
			return "resource 0 metal or 1 energy"
		}
	case SeatVisibility:
		if c.Visibility.ToggleMask > 7 || c.Visibility.ClearMask > 7 {
			return "visibility masks 0..7"
		}
	case SeatMeteor:
		if !c.Meteor.ArgumentPresent && c.Meteor.Enabled {
			return "Enabled false when no argument is present"
		}
	case SeatCancelQueuedMove:
		if c.CancelQueuedMove.Sequence == 0 {
			return "a tracked-move sequence of at least 1"
		}
		return validActors(c.CancelQueuedMove.Actors, limit, false)
	case SeatDeveloperSpawn:
		return validKey(c.DeveloperSpawn.Pattern, false)
	case SeatSpawn:
		if why := validKey(c.Spawn.Unit, false); why != "" {
			return why
		}
		return validPoint(c.Spawn.Position, online)
	case SeatBuilderOptions:
		p := &c.BuilderOptions
		if online && p.Owner != 0 {
			return "no owner field online; the issuing seat's options change"
		}
		if p.Owner > 9 {
			return "an owner 0..9"
		}
		for i := range p.Guard {
			if p.Guard[i] > 2 || p.Patrol[i] > 2 {
				return "builder option values 0..2"
			}
		}
	case SeatCommunityOrderDrag:
		p := &c.CommunityOrderDrag
		if why := validActor(p.Unit); why != "" {
			return why
		}
		if p.Target != (pool.UnitRef{}) {
			// Every draggable order is targetless today; a target needs a
			// future schema and target-lifetime contract (§7.4.3).
			return "a null drag target in command schema version 1"
		}
		if why := validKey(p.BuildProduct, true); why != "" {
			return why
		}
		if p.BuildFacing > 3 {
			return "a build facing 0..3"
		}
		if why := validPoint(p.Goal, online); why != "" {
			return why
		}
		return validPoint(p.Destination, online)
	case SeatCommunityKickout:
		if why := validActor(c.CommunityKickout.Unit); why != "" {
			return why
		}
		return validPoint(c.CommunityKickout.Destination, online)
	case SeatShareGift:
		if c.ShareGift.Target > 9 {
			return "a target seat 0..9"
		}
		return validActors(c.ShareGift.Actors, limit, false)
	case SeatDeclareAlliance:
		if c.DeclareAlliance.Target > 9 {
			return "a target seat 0..9"
		}
	case SeatEconomyActivity:
		if !validEconomyActivity(c.EconomyActivity.Activity) {
			return "an activity from Generation through Reclaim"
		}
	}
	return ""
}

// seatAvailability checks what the payload names against the session: an
// admitted content key, a registered rule set, and the gameplay that owns an
// extension command.
func (s *Session) seatAvailability(context CommandContext, c *SeatCommand) string {
	switch c.Kind {
	case SeatEconomyActivity:
		if !s.economyActivityHoldsAllowed() {
			return "a rule set with temporary economy activity holds"
		}
	case SeatMobileBuild:
		return s.admittedUnitKey(c.MobileBuild.Product)
	case SeatFactoryBuild:
		return s.admittedUnitKey(c.FactoryBuild.Product)
	case SeatSpawn:
		if why := s.admittedUnitKey(c.Spawn.Unit); why != "" {
			return why
		}
		// The Modern testing spawn keeps its owning rule's permission
		// (§7.4.2 kind 31). Single-player replay keeps the local applier's
		// silent refusal instead.
		if context == OnlineCommand && s.Gameplay.Normalize() != gameplay.Modern {
			return "the Modern rule set, which owns the spawn command"
		}
	case SeatCommunityKickout:
		if context == OnlineCommand && (s.Build == nil || s.Build.Rules == nil || !s.Build.Rules.KickoutEnabled(s.Build)) {
			return "the Community construction kickout feature"
		}
	case SeatGameplay:
		if _, err := ResolveCommunity(c.Gameplay.Mode.Normalize(), s.CommunitySources); err != nil {
			return "a registered rule set"
		}
	}
	return ""
}

func (s *Session) admittedUnitKey(key string) string {
	if s.Catalog == nil {
		return "an admitted unit definition key"
	}
	if def, ok := s.Catalog.Unit(key); !ok || def == nil {
		return "an admitted unit definition key"
	}
	return ""
}

// onlineForeignActors refuses the whole command when any actor reference
// names a live unit the stamped seat does not own. A dead or serial-mismatched
// actor is stale, not foreign, and is dropped later (§7.4.3). Targets may
// belong to any seat.
func (s *Session) onlineForeignActors(seat uint8, c *SeatCommand) string {
	foreign := func(r pool.UnitRef) bool {
		if s.Units == nil {
			return false
		}
		u := s.Units.LookupReference(r)
		return u != nil && u.Alive && u.Owner != seat
	}
	var list []pool.UnitRef
	var single pool.UnitRef
	switch c.Kind {
	case SeatOrder:
		list = c.Order.Actors
	case SeatStop:
		list = c.Stop.Actors
	case SeatGroupAssign:
		list = c.GroupAssign.Members
	case SeatStance:
		list = c.Stance.Actors
	case SeatCloak:
		list = c.Cloak.Actors
	case SeatSelfDestruct:
		list = c.SelfDestruct.Actors
	case SeatCancelQueuedMove:
		list = c.CancelQueuedMove.Actors
	case SeatShareGift:
		list = c.ShareGift.Actors
	case SeatActivation:
		single = c.Activation.Unit
	case SeatFactoryBuild:
		single = c.FactoryBuild.Builder
	case SeatCancelProduction:
		single = c.CancelProduction.Unit
	case SeatStockpile:
		single = c.Stockpile.Unit
	case SeatCommunityKickout:
		single = c.CommunityKickout.Unit
	}
	if single != (pool.UnitRef{}) && foreign(single) {
		return "actors the issuing seat owns"
	}
	for _, r := range list {
		if foreign(r) {
			return "actors the issuing seat owns"
		}
	}
	return ""
}

// validActor is a singular actor: a non-null reference.
func validActor(r pool.UnitRef) string {
	if r.Handle == 0 || r.Serial == 0 {
		return "a non-null actor reference"
	}
	return ""
}

// validNullableRef is a target: null, or both halves nonzero.
func validNullableRef(r pool.UnitRef) bool {
	return (r.Handle == 0) == (r.Serial == 0)
}

// validActors is the `actors` primitive: at most limit non-null references,
// no duplicate reference and no repeated handle. An ordinary order's actors
// are a set the encoder emits in ascending handle order, so an unsorted list
// is refused (§7.4.3).
func validActors(rs []pool.UnitRef, limit int, ascending bool) string {
	if len(rs) > limit {
		return fmt.Sprintf("at most %d actors", limit)
	}
	handles := make([]pool.Handle, len(rs))
	for i, r := range rs {
		if r.Handle == 0 || r.Serial == 0 {
			return "non-null actor references"
		}
		if ascending && i > 0 && r.Handle <= rs[i-1].Handle {
			return "an ordinary order's actors in strictly ascending handle order"
		}
		handles[i] = r.Handle
	}
	slices.Sort(handles)
	for i := 1; i < len(handles); i++ {
		if handles[i] == handles[i-1] {
			return "actors without a repeated handle"
		}
	}
	return ""
}

// validKey is the `key` primitive: 1..255 bytes, no NUL, and already the
// canonical form content.CanonicalKey returns. An optional key may be empty.
func validKey(k string, optional bool) string {
	if k == "" {
		if optional {
			return ""
		}
		return "a content key"
	}
	if len(k) > seatMaxKeyBytes || strings.IndexByte(k, 0) >= 0 || content.CanonicalKey(k) != k {
		return "a canonical content key of 1..255 bytes"
	}
	return ""
}

// validCount is counted production's domain: online -32767..-1 and 1..32767;
// single-player replay any nonzero signed 32-bit count but MinInt32, whose
// negation the cancel path cannot represent (§7.4.2).
func validCount(n int32, online bool) string {
	if online {
		if n == 0 || n < -seatOnlineMaxCount || n > seatOnlineMaxCount {
			return "a count of -32767..-1 or 1..32767 online"
		}
		return ""
	}
	if n == 0 || n == math.MinInt32 {
		return "a nonzero count other than -2^31"
	}
	return ""
}

// validPosition is the `position` primitive.
func validPosition(p CommandPosition, online bool) string {
	if p.InterfaceType > 1 {
		return "an interface type 0 left or 1 right"
	}
	return validPoint(CommandPoint{X: p.X, Y: p.Y, Z: p.Z}, online)
}

// validPoint keeps every coordinate within the signed 32-bit raw 16.16 range
// online. Design reading of §7.4.3's audit: the order goal and the kickout
// destination reach consumers that narrow a goal to that width — the
// transport landing goal, the retail save writer and the crowded-arrival
// anchor — so a wider value would be narrowed by accident there. Off-map
// points inside the range keep their existing meaning. The single-player
// replay context keeps the full representation, which the local producer may
// have applied.
//
// TODO(question): the formation offset and a Modern destination slot are
// added to an admitted point after this check; a point within one map width
// of the 32-bit edge can still be pushed past it. Settle by bounding the
// offset by the map extent and narrowing the admitted range by that much.
func validPoint(p CommandPoint, online bool) string {
	if !online {
		return ""
	}
	for _, v := range [3]numeric.Fixed{p.X, p.Y, p.Z} {
		if v < math.MinInt32 || v > math.MaxInt32 {
			return "coordinates within the signed 32-bit 16.16 range online"
		}
	}
	return ""
}

// unselectedZero reports that every payload record but the kind's own is
// zero, so no field of another kind can ride along unread (§7.4.4).
func (c *SeatCommand) unselectedZero() bool {
	d := *c
	switch c.Kind {
	case SeatOrder:
		d.Order = OrderPayload{}
	case SeatStop:
		d.Stop = StopPayload{}
	case SeatActivation:
		d.Activation = ActivationPayload{}
	case SeatMobileBuild:
		d.MobileBuild = MobileBuildPayload{}
	case SeatFactoryBuild:
		d.FactoryBuild = FactoryBuildPayload{}
	case SeatCancelProduction:
		d.CancelProduction = CancelProductionPayload{}
	case SeatStockpile:
		d.Stockpile = StockpilePayload{}
	case SeatGroupAssign:
		d.GroupAssign = GroupAssignPayload{}
	case SeatStance:
		d.Stance = StancePayload{}
	case SeatCloak:
		d.Cloak = CloakPayload{}
	case SeatSelfDestruct:
		d.SelfDestruct = SelfDestructPayload{}
	case SeatSetResource:
		d.SetResource = SetResourcePayload{}
	case SeatSetLogo:
		d.SetLogo = SetLogoPayload{}
	case SeatView:
		d.View = ViewPayload{}
	case SeatGive:
		d.Give = GivePayload{}
	case SeatVisibility:
		d.Visibility = VisibilityPayload{}
	case SeatMeteor:
		d.Meteor = MeteorPayload{}
	case SeatCancelQueuedMove:
		d.CancelQueuedMove = CancelQueuedMovePayload{}
	case SeatDeveloperSpawn:
		d.DeveloperSpawn = DeveloperSpawnPayload{}
	case SeatSpawn:
		d.Spawn = SpawnPayload{}
	case SeatBuilderOptions:
		d.BuilderOptions = BuilderOptionsPayload{}
	case SeatCommunityOrderDrag:
		d.CommunityOrderDrag = CommunityOrderDragPayload{}
	case SeatCommunityKickout:
		d.CommunityKickout = CommunityKickoutPayload{}
	case SeatGameplay:
		d.Gameplay = GameplayPayload{}
	case SeatEconomyActivity:
		d.EconomyActivity = EconomyActivityPayload{}
	}
	o := &d.Order
	return len(o.Actors) == 0 && o.Code == 0 && o.Target == (pool.UnitRef{}) && o.Position == (CommandPosition{}) &&
		!o.Queued && !o.AssignedPosition && !o.TrackQueuedMove && len(o.Targets) == 0 &&
		len(d.Stop.Actors) == 0 &&
		d.Activation == (ActivationPayload{}) &&
		d.MobileBuild == (MobileBuildPayload{}) &&
		d.FactoryBuild == (FactoryBuildPayload{}) &&
		d.CancelProduction == (CancelProductionPayload{}) &&
		d.Stockpile == (StockpilePayload{}) &&
		d.GroupAssign.Group == 0 && len(d.GroupAssign.Members) == 0 &&
		len(d.Stance.Actors) == 0 && !d.Stance.Fire && d.Stance.Value == 0 &&
		len(d.Cloak.Actors) == 0 && !d.Cloak.Cloak &&
		len(d.SelfDestruct.Actors) == 0 && !d.SelfDestruct.Queued &&
		d.SetResource == (SetResourcePayload{}) &&
		d.SetLogo == (SetLogoPayload{}) &&
		d.View == (ViewPayload{}) &&
		d.Give == (GivePayload{}) &&
		d.Visibility == (VisibilityPayload{}) &&
		d.Meteor == (MeteorPayload{}) &&
		d.CancelQueuedMove.Sequence == 0 && len(d.CancelQueuedMove.Actors) == 0 &&
		d.Spawn == (SpawnPayload{}) &&
		d.DeveloperSpawn == (DeveloperSpawnPayload{}) &&
		d.BuilderOptions == (BuilderOptionsPayload{}) &&
		d.CommunityOrderDrag == (CommunityOrderDragPayload{}) &&
		d.CommunityKickout == (CommunityKickoutPayload{}) &&
		d.Gameplay == (GameplayPayload{}) &&
		d.EconomyActivity == (EconomyActivityPayload{})
}

// bindSeatCommand widens an admitted seat command into the shared payload
// implementation's form. Nothing is narrowed: every seat field fits the
// local record's type.
func (s *Session) bindSeatCommand(q *seatQueued, issuer uint8) boundCommand {
	c := &q.command
	b := boundCommand{issuer: issuer, online: q.context == OnlineCommand, stamped: true, sequence: q.stamp.Position}
	h := &b.c
	switch c.Kind {
	case SeatOrder:
		p := &c.Order
		h.Kind = HumanOrder
		h.Order = HumanOrderCommand{Code: int(p.Code), Target: p.Target.Handle, Position: resolvePosition(p.Position),
			Queued: p.Queued, AssignedPosition: p.AssignedPosition, TrackQueuedMove: p.TrackQueuedMove}
		b.actors, b.target = p.Actors, p.Target
		if len(p.Targets) != 0 {
			h.Order.Targets = make([]HumanOrderTarget, len(p.Targets))
			b.targets = make([]pool.UnitRef, len(p.Targets))
			for i, t := range p.Targets {
				h.Order.Targets[i] = HumanOrderTarget{Target: t.Target.Handle, Position: resolvePosition(t.Position)}
				b.targets[i] = t.Target
			}
		}
	case SeatStop:
		h.Kind, b.actors = HumanStop, c.Stop.Actors
	case SeatActivation:
		h.Kind, b.unit = HumanActivation, c.Activation.Unit
		h.Activation = HumanActivationCommand{Unit: c.Activation.Unit.Handle, Activate: c.Activation.Activate, Queued: c.Activation.Queued}
	case SeatMobileBuild:
		p := &c.MobileBuild
		h.Kind, b.unit = HumanMobileBuild, p.Builder
		h.MobileBuild = HumanMobileBuildCommand{Facing: units.StructureFacing(p.Facing), Builder: p.Builder.Handle, Product: p.Product,
			WX: p.Position.X, WY: p.Position.Y, WZ: p.Position.Z, Queued: p.Queued, AppendOnly: p.AppendOnly}
	case SeatFactoryBuild:
		h.Kind, b.unit = HumanFactoryBuild, c.FactoryBuild.Builder
		h.FactoryBuild = HumanFactoryBuildCommand{Builder: c.FactoryBuild.Builder.Handle, Product: c.FactoryBuild.Product, Count: int(c.FactoryBuild.Count)}
	case SeatCancelProduction:
		h.Kind, b.unit = HumanCancelProduction, c.CancelProduction.Unit
		h.CancelProduction = HumanCancelProductionCommand{Unit: c.CancelProduction.Unit.Handle}
	case SeatStockpile:
		h.Kind, b.unit = HumanStockpile, c.Stockpile.Unit
		h.Stockpile = HumanStockpileCommand{Unit: c.Stockpile.Unit.Handle, Count: int(c.Stockpile.Count)}
	case SeatGroupAssign:
		h.Kind, b.actors = HumanGroupAssign, c.GroupAssign.Members
		h.Group = HumanGroupCommand{Group: int(c.GroupAssign.Group)}
	case SeatStance:
		h.Kind, b.actors = HumanStance, c.Stance.Actors
		h.Stance = HumanStanceCommand{Fire: c.Stance.Fire, Value: int32(c.Stance.Value)}
	case SeatCloak:
		h.Kind, b.actors = HumanCloak, c.Cloak.Actors
		h.Cloak = HumanCloakCommand{Cloak: c.Cloak.Cloak}
	case SeatSelfDestruct:
		h.Kind, b.actors = HumanSelfDestruct, c.SelfDestruct.Actors
		h.SelfDestruct = HumanSelfDestructCommand{Queued: c.SelfDestruct.Queued}
	case SeatNoShake:
		h.Kind = HumanNoShake
	case SeatATM:
		h.Kind = HumanATM
	case SeatSetResource:
		player := int(c.SetResource.Player)
		if b.online {
			player = int(issuer) // the issuing seat's own stock (§7.1)
		}
		h.Kind = HumanSetResource
		h.SetResource = HumanSetResourceCommand{Player: player, Resource: c.SetResource.Resource, Amount: c.SetResource.Amount}
	case SeatSetLogo:
		h.Kind = HumanSetLogo
		h.SetLogo = HumanSetLogoCommand{Player: int(c.SetLogo.Player), Logo: c.SetLogo.Logo}
	case SeatView:
		h.Kind = HumanView
		h.View = HumanViewCommand{Player: c.View.Player}
	case SeatGive:
		h.Kind = HumanGive
		h.Give = HumanGiveCommand{Player: int(c.Give.Player), Resource: c.Give.Resource, Amount: c.Give.Amount}
	case SeatMakeSelectable:
		h.Kind = HumanMakeSelectable
	case SeatVisibility:
		h.Kind = HumanVisibility
		h.Visibility = HumanVisibilityCommand{ToggleMask: visibility.Mode(c.Visibility.ToggleMask), ClearMask: visibility.Mode(c.Visibility.ClearMask)}
	case SeatDoubleShot:
		h.Kind = HumanDoubleShot
	case SeatHalfShot:
		h.Kind = HumanHalfShot
	case SeatShootAll:
		h.Kind = HumanShootAll
	case SeatMeteor:
		h.Kind = HumanMeteor
		h.Meteor = HumanMeteorCommand{ArgumentPresent: c.Meteor.ArgumentPresent, Enabled: c.Meteor.Enabled}
	case SeatCancelQueuedMove:
		h.Kind, b.actors = HumanCancelQueuedMove, c.CancelQueuedMove.Actors
		h.CancelQueuedMove = HumanCancelQueuedMoveCommand{Sequence: c.CancelQueuedMove.Sequence}
	case SeatDeveloperSpawn:
		p := c.DeveloperSpawn
		h.Kind = HumanDeveloperSpawn
		h.DeveloperSpawn = HumanDeveloperSpawnCommand{Pattern: p.Pattern, Owner: p.Owner, X: p.Position.X, Y: p.Position.Y, Z: p.Position.Z}
	case SeatSpawn:
		h.Kind = HumanSpawn
		h.Spawn = HumanSpawnCommand{Unit: c.Spawn.Unit, X: c.Spawn.Position.X, Y: c.Spawn.Position.Y, Z: c.Spawn.Position.Z}
	case SeatBuilderOptions:
		p := &c.BuilderOptions
		owner := p.Owner
		if b.online {
			owner = issuer // the issuing seat's own options (§7.1)
		}
		var options orders.BuilderOptions
		for i := range p.Guard {
			options.Guard[i] = orders.GuardHomeOption(p.Guard[i])
			options.Patrol[i] = orders.PatrolWorkOption(p.Patrol[i])
		}
		h.Kind = HumanBuilderOptions
		h.BuilderOptions = HumanBuilderOptionsCommand{Owner: owner, Options: options}
	case SeatCommunityOrderDrag:
		p := &c.CommunityOrderDrag
		h.Kind, b.unit = HumanCommunityOrderDrag, p.Unit
		h.CommunityOrderDrag = HumanCommunityOrderDragCommand{
			Receipt: orders.CommunityOrderDragReceipt{Unit: p.Unit.Handle, Index: p.Index, DescriptorID: p.DescriptorID,
				CreationTick: p.CreationTick, Target: p.Target.Handle, GoalX: p.Goal.X, GoalY: p.Goal.Y, GoalZ: p.Goal.Z,
				BuildProduct: p.BuildProduct, BuildFacing: p.BuildFacing},
			Position: orders.CommunityOrderDragDestination{X: p.Destination.X, Y: p.Destination.Y, Z: p.Destination.Z},
		}
	case SeatCommunityKickout:
		p := &c.CommunityKickout
		h.Kind, b.unit = HumanCommunityKickout, p.Unit
		h.CommunityKickout = HumanCommunityKickoutCommand{Unit: p.Unit.Handle, X: p.Destination.X, Y: p.Destination.Y, Z: p.Destination.Z}
	case SeatGameplay:
		h.Kind = HumanGameplay
		h.Gameplay = c.Gameplay.Mode
	case SeatEconomyActivity:
		h.Kind = HumanEconomyActivity
		h.EconomyActivity = HumanEconomyActivityCommand(c.EconomyActivity)
	}
	return b
}

// resolvePosition widens a captured position into the resolver's record.
// IsWreck and FeatureResurrectable have no authoritative reader and no wire
// field; the adapter leaves them false (§7.4.1).
func resolvePosition(p CommandPosition) orders.ResolvePos {
	return orders.ResolvePos{X: p.X, Y: p.Y, Z: p.Z, InterfaceType: int(p.InterfaceType), HasFeature: p.HasFeature}
}
