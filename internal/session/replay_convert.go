package session

import (
	"errors"
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The local command form → the single-player replay form
// (docs/DESIGN_MULTIPLAYER.md §7.4.2, §7.4.4, §10). A recording is played
// back through EnqueueSeatCommand, so the converted command must make phase 1
// do exactly what applying the local command did: the same queues, the same
// draws on both random streams, the same creations. The conversion runs at
// the instant phase 1 applies the local command, against that state, which is
// also the state its playback applies the stamped entry against. Every
// decision below that depends on the world — whether a reference resolves,
// whether a Community gesture's publication identity still names its unit —
// is therefore taken on the state both runs share.
//
// What the replay form cannot say exactly is refused, and the recording stops
// (ReplayRecorder.Failed). The local producers never send most of those
// shapes; they are listed so a new producer cannot silently record a
// different battle.

// replayCommand converts the local command phase 1 is about to apply.
func (s *Session) replayCommand(c HumanCommand) (SeatCommand, error) {
	if s == nil {
		return SeatCommand{}, errors.New("a session")
	}
	// Submission captured the references; a command handed to phase 1
	// directly is captured here, the instant phase 1 itself would capture it.
	s.captureLocalRefs(&c)
	last := s.Units.LastAllocationSerial()
	if last == math.MaxUint64 {
		return SeatCommand{}, errors.New("an allocation serial no unit holds")
	}
	k := replayConversion{s: s, issuer: s.LocalOwner, stale: last + 1}
	out, err := k.convert(&c)
	if err != nil {
		return SeatCommand{}, err
	}
	// The playback's own admission, on the state it will run against: a
	// command it would refuse is one this recording cannot reproduce, except
	// where the local application provably changes nothing either.
	if why := replayKindAdmission(out.Kind); why != "" {
		return SeatCommand{}, errors.New(why)
	}
	if why := s.seatSchema(SinglePlayerReplay, &out); why != "" {
		return SeatCommand{}, errors.New(why)
	}
	if why := s.seatAvailability(SinglePlayerReplay, &out); why != "" && !s.replayRefusalChangesNothing(out.Kind) {
		return SeatCommand{}, errors.New(why)
	}
	if _, err := EncodeSeatCommand(SinglePlayerReplay, out); err != nil {
		return SeatCommand{}, err
	}
	return out, nil
}

// replayRefusalChangesNothing names the kinds whose content-key refusal at
// playback is the local application's own outcome. A mobile build of a
// product the catalog lacks returns before any queue is bound or changed.
// A spawn of a unit the catalog lacks does nothing outside the Modern rule
// set, which owns the command; under it the refusal is announced, and the
// announcement advances the event record the canonical checkpoint carries,
// which a playback refusal would not.
func (s *Session) replayRefusalChangesNothing(k SeatCommandKind) bool {
	switch k {
	case SeatMobileBuild:
		return true
	case SeatSpawn:
		return s.Gameplay.Normalize() != gameplay.Modern
	}
	return false
}

// replayConversion converts one command. stale is a serial no allocation has
// had when phase 1 applies the command: LastAllocationSerial plus one.
type replayConversion struct {
	s      *Session
	issuer uint8
	stale  uint64
}

// ref names a captured reference in the replay form. A handle captured from
// a slot that held no allocation carries serial 0 locally, which no lookup
// accepts and version 1's `ref` cannot carry (seatWireReference). It takes
// the stale serial instead, which no lookup accepts either, so it resolves to
// no unit in the recording and in its playback; its handle is kept, because
// an ordinary order to a dead target keeps the dead handle in its node
// (§7.4.3, "Single-player stale targets differ").
func (k *replayConversion) ref(r pool.UnitRef) pool.UnitRef {
	if r.Handle != 0 && r.Serial == 0 {
		r.Serial = k.stale
	}
	return r
}

// unit is a singular actor. Locally a command naming no unit does nothing,
// but the replay form has no null actor.
func (k *replayConversion) unit(r pool.UnitRef) (pool.UnitRef, error) {
	if r.Handle == 0 {
		return pool.UnitRef{}, errors.New("a named actor; a command naming no unit has no replay form")
	}
	return k.ref(r), nil
}

// orderActors is an ordinary order's actor set: the local adapter visits its
// captured selection sorted by handle with repeats removed (bindLocalCommand),
// which is the ascending set the replay form carries. A null capture resolves
// to no actor and is dropped, as phase 1 drops it.
func (k *replayConversion) orderActors(rs []pool.UnitRef) []pool.UnitRef {
	set := sortedUniqueRefs(rs)
	out := set[:0]
	for _, r := range set {
		if r.Handle != 0 {
			out = append(out, k.ref(r))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// actors is an actor list phase 1 visits in captured order. The replay form
// has no repeated actor (checkWireActors). A repeat that resolves to no actor
// of the issuer is dropped by phase 1 at every visit, so one copy says the
// same; a live repeat is a second application, which only an idempotent kind
// (repeatable) may drop. A null capture resolves to no actor and is dropped.
func (k *replayConversion) actors(rs []pool.UnitRef, repeatable bool) ([]pool.UnitRef, error) {
	if len(rs) == 0 {
		return nil, nil
	}
	var seen [1 << 16 / 64]uint64
	out := make([]pool.UnitRef, 0, len(rs))
	for _, r := range rs {
		if r.Handle == 0 {
			continue
		}
		r = k.ref(r)
		bit := uint64(1) << (r.Handle % 64)
		if seen[r.Handle/64]&bit == 0 {
			seen[r.Handle/64] |= bit
			out = append(out, r)
			continue
		}
		for _, prev := range out {
			if prev.Handle == r.Handle && prev.Serial != r.Serial {
				return nil, fmt.Errorf("one allocation per actor handle, got handle %d twice with different serials", r.Handle)
			}
		}
		if !repeatable && k.s.commandActor(k.issuer, r) != nil {
			return nil, fmt.Errorf("actors without a repeat; unit %d is applied twice and the replay form has no repeated actor", r.Handle)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// publishedActor is a Community gesture's unit. Locally it is accepted only
// while the host's publication identity still names the issuer's live unit
// in that slot (publishedHumanUnit); playback names it by allocation instead.
// Decided here, on the state both runs share: an accepted unit is named by
// its allocation serial, which resolves to the same unit at playback, and a
// refused one by the stale serial, which resolves to none, so both runs
// apply or skip the gesture together.
func (k *replayConversion) publishedActor(h pool.Handle, instanceID uint64) (pool.UnitRef, error) {
	if h == 0 {
		return pool.UnitRef{}, errors.New("a named actor; a gesture naming no unit has no replay form")
	}
	if u := k.s.publishedHumanUnit(h, instanceID); u != nil {
		return pool.UnitRef{Handle: h, Serial: u.AllocationSerial}, nil
	}
	return pool.UnitRef{Handle: h, Serial: k.stale}, nil
}

// replayPosition is the `position` primitive. ResolvePos.IsWreck and
// FeatureResurrectable have no authoritative reader, so dropping them
// changes nothing (§7.4.1).
func replayPosition(p orders.ResolvePos) (CommandPosition, error) {
	if p.InterfaceType < 0 || p.InterfaceType > 1 {
		return CommandPosition{}, fmt.Errorf("an interface type 0 left or 1 right, got %d", p.InterfaceType)
	}
	return CommandPosition{X: p.X, Y: p.Y, Z: p.Z, InterfaceType: uint8(p.InterfaceType), HasFeature: p.HasFeature}, nil
}

// replayCount is a counted producer's count. Both appliers read zero as one
// round; the replay form keeps every other signed 32-bit count but MinInt32.
func replayCount(n int) (int32, error) {
	if n == 0 {
		n = 1
	}
	if n < math.MinInt32+1 || n > math.MaxInt32 {
		return 0, fmt.Errorf("a count within the signed 32-bit range other than -2^31, got %d", n)
	}
	return int32(n), nil
}

// replayPlayer is a player field the replay form carries as 0..9.
func replayPlayer(p int) (uint8, error) {
	if p < 0 || p > 9 {
		return 0, fmt.Errorf("a player 0..9, got %d", p)
	}
	return uint8(p), nil
}

// replayCanonicalKey requires a key the local applier would use as written.
func replayCanonicalKey(key string) (string, error) {
	if content.CanonicalKey(key) != key {
		return "", fmt.Errorf("a canonical content key, got %q", key)
	}
	return key, nil
}

func (k *replayConversion) convert(c *HumanCommand) (SeatCommand, error) {
	var out SeatCommand
	var err error
	switch c.Kind {
	case HumanOrder:
		p := &c.Order
		if p.StagedCount != 0 {
			return out, errors.New("no staged count; it is a replay-staging input, not a command field (§7.4.1)")
		}
		if p.Code < 1 || p.Code > 14 {
			return out, fmt.Errorf("an order code 1..14, got %d", p.Code)
		}
		o := OrderPayload{Code: uint8(p.Code), Queued: p.Queued}
		if o.Position, err = replayPosition(p.Position); err != nil {
			return out, err
		}
		if len(p.Targets) == 0 {
			o.Actors = k.orderActors(c.refs.actors)
			o.Target = k.ref(c.refs.target)
			o.AssignedPosition, o.TrackQueuedMove = p.AssignedPosition, p.TrackQueuedMove
		} else {
			// An area order reads neither the outer target nor the two
			// gesture flags (applyBoundOrderBatch), so the form that
			// requires them clear says the same.
			if o.Actors, err = k.actors(c.refs.actors, false); err != nil {
				return out, err
			}
			o.Targets = make([]CommandTarget, len(p.Targets))
			for i := range p.Targets {
				o.Targets[i].Target = k.ref(c.refs.targets[i])
				if o.Targets[i].Position, err = replayPosition(p.Targets[i].Position); err != nil {
					return out, err
				}
			}
		}
		out.Kind, out.Order = SeatOrder, o
	case HumanStop:
		out.Kind = SeatStop
		out.Stop.Actors, err = k.actors(c.refs.actors, false)
	case HumanActivation:
		out.Kind = SeatActivation
		out.Activation = ActivationPayload{Activate: c.Activation.Activate, Queued: c.Activation.Queued}
		out.Activation.Unit, err = k.unit(c.refs.unit)
	case HumanMobileBuild:
		p := &c.MobileBuild
		if p.Facing > 3 {
			return out, fmt.Errorf("a facing 0..3, got %d", p.Facing)
		}
		// The catalog lookup and the node both use the canonical key
		// (Catalog.Unit, orders.NewMobileBuildNode), so the canonical
		// spelling builds the same record.
		out.Kind = SeatMobileBuild
		out.MobileBuild = MobileBuildPayload{Product: content.CanonicalKey(p.Product), Position: CommandPoint{X: p.WX, Y: p.WY, Z: p.WZ},
			Facing: uint8(p.Facing), Queued: p.Queued, AppendOnly: p.AppendOnly}
		out.MobileBuild.Builder, err = k.unit(c.refs.unit)
	case HumanFactoryBuild:
		// A refused factory request records its product as written in the
		// rejection's text, so only the canonical spelling is the same.
		out.Kind = SeatFactoryBuild
		if out.FactoryBuild.Product, err = replayCanonicalKey(c.FactoryBuild.Product); err != nil {
			return out, err
		}
		if out.FactoryBuild.Count, err = replayCount(c.FactoryBuild.Count); err != nil {
			return out, err
		}
		out.FactoryBuild.Builder, err = k.unit(c.refs.unit)
	case HumanCancelProduction:
		out.Kind = SeatCancelProduction
		out.CancelProduction.Unit, err = k.unit(c.refs.unit)
	case HumanStockpile:
		out.Kind = SeatStockpile
		if out.Stockpile.Count, err = replayCount(c.Stockpile.Count); err != nil {
			return out, err
		}
		out.Stockpile.Unit, err = k.unit(c.refs.unit)
	case HumanGroupAssign:
		if c.Group.Group < 1 || c.Group.Group > 9 {
			return out, fmt.Errorf("a group 1..9, got %d", c.Group.Group)
		}
		// Assignment marks a membership set, so a repeat is the same set.
		out.Kind = SeatGroupAssign
		out.GroupAssign.Group = uint8(c.Group.Group)
		out.GroupAssign.Members, err = k.actors(c.refs.actors, true)
	case HumanStance:
		if c.Stance.Value < 0 || c.Stance.Value > 2 {
			return out, fmt.Errorf("a stance value 0..2, got %d", c.Stance.Value)
		}
		out.Kind = SeatStance
		out.Stance = StancePayload{Fire: c.Stance.Fire, Value: uint8(c.Stance.Value)}
		out.Stance.Actors, err = k.actors(c.refs.actors, false)
	case HumanCloak:
		out.Kind = SeatCloak
		out.Cloak.Cloak = c.Cloak.Cloak
		out.Cloak.Actors, err = k.actors(c.refs.actors, false)
	case HumanSelfDestruct:
		out.Kind = SeatSelfDestruct
		out.SelfDestruct.Queued = c.SelfDestruct.Queued
		out.SelfDestruct.Actors, err = k.actors(c.refs.actors, false)
	case HumanCancelQueuedMove:
		// A second visit finds no tracked move left with that receipt, so a
		// repeated actor changes nothing (applyBoundCancelQueuedMove).
		out.Kind = SeatCancelQueuedMove
		out.CancelQueuedMove.Sequence = c.CancelQueuedMove.Sequence
		out.CancelQueuedMove.Actors, err = k.actors(c.refs.actors, true)
	case HumanNoShake:
		out.Kind = SeatNoShake
	case HumanATM:
		out.Kind = SeatATM
	case HumanMakeSelectable:
		out.Kind = SeatMakeSelectable
	case HumanDoubleShot:
		out.Kind = SeatDoubleShot
	case HumanHalfShot:
		out.Kind = SeatHalfShot
	case HumanSetResource:
		p := &c.SetResource
		if p.Resource != economy.Metal && p.Resource != economy.Energy {
			return out, fmt.Errorf("resource metal or energy, got %d", p.Resource)
		}
		out.Kind = SeatSetResource
		out.SetResource = SetResourcePayload{Resource: p.Resource, Amount: p.Amount}
		out.SetResource.Player, err = replayPlayer(p.Player)
	case HumanSetLogo:
		out.Kind = SeatSetLogo
		out.SetLogo.Logo = c.SetLogo.Logo
		out.SetLogo.Player, err = replayPlayer(c.SetLogo.Player)
	case HumanView:
		out.Kind = SeatView
		out.View.Player, err = replayPlayer(int(c.View.Player))
	case HumanGive:
		p := &c.Give
		if p.Resource != economy.Metal && p.Resource != economy.Energy {
			return out, fmt.Errorf("resource metal or energy, got %d", p.Resource)
		}
		out.Kind = SeatGive
		out.Give = GivePayload{Resource: p.Resource, Amount: p.Amount}
		out.Give.Player, err = replayPlayer(p.Player)
	case HumanVisibility:
		p := &c.Visibility
		if p.ToggleMask > 7 || p.ClearMask > 7 {
			return out, fmt.Errorf("visibility masks 0..7, got %d and %d", p.ToggleMask, p.ClearMask)
		}
		out.Kind = SeatVisibility
		out.Visibility = VisibilityPayload{ToggleMask: uint8(p.ToggleMask), ClearMask: uint8(p.ClearMask)}
	case HumanMeteor:
		// The argument-free form never reads Enabled (applyBoundPlayerCommand).
		out.Kind = SeatMeteor
		out.Meteor = MeteorPayload{ArgumentPresent: c.Meteor.ArgumentPresent, Enabled: c.Meteor.ArgumentPresent && c.Meteor.Enabled}
	case HumanSpawn:
		// The catalog lookup is by canonical key (Catalog.Unit).
		p := &c.Spawn
		out.Kind = SeatSpawn
		out.Spawn = SpawnPayload{Unit: content.CanonicalKey(p.Unit), Position: CommandPoint{X: p.X, Y: p.Y, Z: p.Z}}
	case HumanDeveloperSpawn:
		// The pattern matcher folds ASCII case (developerUnitPattern) but
		// does not trim, so only a pattern the canonical form merely folds
		// matches the same records.
		p := &c.DeveloperSpawn
		pattern := content.CanonicalKey(p.Pattern)
		if len(pattern) != len(p.Pattern) {
			return out, fmt.Errorf("a developer pattern without surrounding whitespace, got %q", p.Pattern)
		}
		out.Kind = SeatDeveloperSpawn
		out.DeveloperSpawn = DeveloperSpawnPayload{Pattern: pattern, Owner: p.Owner, Position: CommandPoint{X: p.X, Y: p.Y, Z: p.Z}}
	case HumanBuilderOptions:
		p := &c.BuilderOptions
		out.Kind = SeatBuilderOptions
		out.BuilderOptions.Owner = p.Owner
		for i := range p.Options.Guard {
			out.BuilderOptions.Guard[i] = uint8(p.Options.Guard[i])
			out.BuilderOptions.Patrol[i] = uint8(p.Options.Patrol[i])
		}
	case HumanCommunityOrderDrag:
		d := &c.CommunityOrderDrag
		r := &d.Receipt
		if r.BuildProduct != "" {
			if _, err = replayCanonicalKey(r.BuildProduct); err != nil {
				return out, err
			}
		}
		out.Kind = SeatCommunityOrderDrag
		out.CommunityOrderDrag = CommunityOrderDragPayload{Index: r.Index, DescriptorID: r.DescriptorID, CreationTick: r.CreationTick,
			Goal: CommandPoint{X: r.GoalX, Y: r.GoalY, Z: r.GoalZ}, BuildProduct: r.BuildProduct, BuildFacing: r.BuildFacing,
			Destination: CommandPoint{X: d.Position.X, Y: d.Position.Y, Z: d.Position.Z}}
		if out.CommunityOrderDrag.Unit, err = k.publishedActor(r.Unit, d.InstanceID); err != nil {
			return out, err
		}
		if r.Target != 0 {
			// A queued record matches a receipt only with the receipt's
			// target, and is draggable only with none, so a targeted receipt
			// moves nothing (orders.DragCommunityOrder). Version 1 has no
			// target field; the stale actor says the same nothing.
			out.CommunityOrderDrag.Unit.Serial = k.stale
		}
	case HumanCommunityKickout:
		p := &c.CommunityKickout
		out.Kind = SeatCommunityKickout
		out.CommunityKickout.Destination = CommandPoint{X: p.X, Y: p.Y, Z: p.Z}
		out.CommunityKickout.Unit, err = k.publishedActor(p.Unit, p.InstanceID)
	case HumanGameplay:
		// SetGameplay normalizes before it binds the rule set, and the form
		// carries only a registered name.
		out.Kind = SeatGameplay
		out.Gameplay.Mode = c.Gameplay.Normalize()
	default:
		return out, fmt.Errorf("a kind with a replay form, got %d", c.Kind)
	}
	return out, err
}
