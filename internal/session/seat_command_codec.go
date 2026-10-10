package session

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The wire form of command schema version 1 (docs/DESIGN_MULTIPLAYER.md
// §7.4.1–§7.4.4, contracts M2-C4, M2-C5 and M2-C11). A payload is one kind
// byte and that kind's fields in the order of the §7.4.2 table, written in
// the shared primitives of internal/netproto: u8 and bool one byte; u16, u32
// and u64 the shortest varint; s32 and s64 zigzag then varint; `fixed` the
// s64 of a raw numeric.Fixed; `point` X, Y, Z fixed; `ref` handle u16 then
// allocation serial u64; `actors` a u16 count of non-null refs; `key` a u16
// length and 1..255 bytes of a canonical content key; `amount` four
// little-endian bytes of binary32; `position` a point, InterfaceType u8 and
// HasFeature bool. These are Nanolathe protocol contracts, not retail
// records. The schema version is negotiated outside the payload, and the
// context is the admitted session's or replay header's, never a payload
// field.
//
// Decoding establishes schema validity before any gameplay code runs: a
// payload over 4 MiB is refused before it is parsed; every count is checked
// against its ceiling and the bytes left before anything is allocated from
// it; unknown and context-forbidden kinds, invalid booleans and enumerations,
// overflowing and overlong integers, half-null references, repeated actors,
// noncanonical keys and amounts, a replay-only field in an online payload,
// invalid flag combinations and trailing bytes are refused; and an accepted
// payload is exactly the encoding of the value it decodes to. A codec
// refusal leaves nothing to enqueue, so no queue, resource, random stream or
// simulation state can move. The receiver (seat_command_apply.go) still
// checks what needs the session: the role, the agreed unit limit, the online
// work limits and coordinate range, the cheat permission, content keys and
// actor ownership.
//
// Design readings, each a Nanolathe protocol choice:
//
//   - The codec checks every context-only rule of §7.4.1–§7.4.2, the flag
//     combinations of §7.4.2 included, so the encoder accepts exactly what the
//     decoder accepts. The online work limits (§15 Q28) stay the receiver's:
//     §7.4.1 gives the context-only codec the representation ceilings, 3276
//     actors and 65535 area entries.
//   - Online, the encoder spells a negative-zero amount as positive zero, as
//     §7.4.1 says, and the decoder refuses negative zero. Every other
//     noncanonical value — an unsorted ordinary order, a noncanonical key, an
//     unregistered rule set — is refused by the encoder rather than rewritten.
//   - Online `Give` is refused at the codec when no producer can generate its
//     amount: not finite, not whole, negative zero, or outside ±2^31
//     (§7.4.1, §15 Q28). Whether a zero or negative whole gift needs the
//     room's cheat permission is the receiver's question.
//   - Deferred (D) kinds encode in both contexts, so M2 can test their codec;
//     the receiver refuses their online application until M5. The
//     single-player replay kinds NoShake, SetLogo, DeveloperSpawn and Gameplay
//     are refused by the online codec, local interface kinds and the reserved numbers 35..45
//     by both.
//   - A Gameplay record encodes its mode exactly as given and only when the
//     mode is a rule set this build registered; a word the applier would
//     normalize to Modern must be normalized by its producer first.
//   - A decoded empty list is nil.

// ErrSeatCommandNoWireForm reports a reference or actor list the local
// adapter can produce but command schema version 1 cannot carry: a
// reference captured from a freed slot, whose allocation serial is zero, or
// a repeated actor. See seatWireReference for the gap and its proposal.
var ErrSeatCommandNoWireForm = errors.New("nanolathe: seat command has no version-1 wire form")

// seatAreaEntryMinBytes and seatActorMinBytes are the smallest encodings of
// an area entry (a null ref and a one-byte-per-field position) and of a
// non-null ref, the per-record floors a count is checked against.
const (
	seatAreaEntryMinBytes = 2 + 5
	seatActorMinBytes     = 2
)

// EncodeSeatCommand returns the canonical version-1 payload of c in a
// context. It refuses every value DecodeSeatCommand would refuse in that
// context, so a payload it returns always decodes back to c (with an online
// negative-zero amount spelled as zero).
func EncodeSeatCommand(context CommandContext, c SeatCommand) ([]byte, error) {
	if err := seatCodecContext(context); err != nil {
		return nil, err
	}
	if context == OnlineCommand {
		switch c.Kind {
		case SeatSetResource:
			c.SetResource.Amount = canonicalOnlineAmount(c.SetResource.Amount)
		case SeatGive:
			c.Give.Amount = canonicalOnlineAmount(c.Give.Amount)
		}
	}
	if err := checkSeatWire(context, &c); err != nil {
		return nil, err
	}
	var w netproto.Writer
	writeSeatCommand(&w, context, &c)
	return w.Bytes(), nil
}

// DecodeSeatCommand reads a version-1 payload in the context the admitted
// session or replay header supplies. It returns no partially valid value.
func DecodeSeatCommand(context CommandContext, payload []byte) (SeatCommand, error) {
	if err := seatCodecContext(context); err != nil {
		return SeatCommand{}, err
	}
	if len(payload) > netproto.MaxCommandBytes {
		return SeatCommand{}, seatCodecError(context, "payload", fmt.Sprintf("at most %d bytes, got %d", netproto.MaxCommandBytes, len(payload)))
	}
	r := netproto.NewReader(payload, func(path, expected string) error { return seatCodecError(context, path, expected) })
	c := readSeatCommand(r, context)
	if err := r.End(); err != nil {
		return SeatCommand{}, err
	}
	if err := checkSeatWire(context, &c); err != nil {
		return SeatCommand{}, err
	}
	var w netproto.Writer
	writeSeatCommand(&w, context, &c)
	if !bytes.Equal(w.Bytes(), payload) {
		return SeatCommand{}, seatCodecError(context, "payload", "the canonical encoding of the command it decodes to")
	}
	return c, nil
}

func seatCodecContext(context CommandContext) error {
	if context != OnlineCommand && context != SinglePlayerReplay {
		return seatCodecError(context, "context", "OnlineCommand or SinglePlayerReplay, supplied by the admitted session or replay header")
	}
	return nil
}

// seatCodecProvider names the schema and context a refusal was decided in.
func seatCodecProvider(context CommandContext) string {
	switch context {
	case OnlineCommand:
		return "command schema v1 online"
	case SinglePlayerReplay:
		return "command schema v1 single-player replay"
	}
	return fmt.Sprintf("command schema v1 context %d", context)
}

func seatCodecError(context CommandContext, path, expected string) error {
	return fmt.Errorf("nanolathe: seat command payload rejected: logical path %s, providers searched [%s], expected %s", path, seatCodecProvider(context), expected)
}

func seatNoWireForm(context CommandContext, path, expected string) error {
	return fmt.Errorf("%w: logical path %s, providers searched [%s], expected %s", ErrSeatCommandNoWireForm, path, seatCodecProvider(context), expected)
}

func canonicalOnlineAmount(a float32) float32 {
	return math.Float32frombits(netproto.CanonicalAmount(math.Float32bits(a)))
}

// seatCodecKind is the codec's class column (§7.4.2): why a kind has no
// payload in a context, or "".
func seatCodecKind(context CommandContext, k SeatCommandKind) string {
	switch k {
	case SeatOrder, SeatStop, SeatActivation, SeatMobileBuild, SeatFactoryBuild, SeatCancelProduction, SeatStockpile,
		SeatGroupAssign, SeatStance, SeatCloak, SeatSelfDestruct, SeatATM, SeatSetResource, SeatView, SeatGive,
		SeatMakeSelectable, SeatVisibility, SeatDoubleShot, SeatHalfShot, SeatMeteor, SeatCancelQueuedMove, SeatSpawn,
		SeatBuilderOptions, SeatCommunityOrderDrag, SeatCommunityKickout:
		return ""
	case SeatNoShake, SeatSetLogo, SeatDeveloperSpawn, SeatGameplay:
		if context == SinglePlayerReplay {
			return ""
		}
		return "a kind with an online payload; NoShake, SetLogo, DeveloperSpawn and Gameplay are single-player replay records (online a local preference or the lobby's)"
	case SeatSelectionReplace, SeatSelectionToggle, SeatSelectionClear, SeatBuildPage, SeatGroupRecall, SeatBigBrother, SeatShiftState:
		return "a kind with a payload; local interface kinds are reserved and never encoded"
	case SeatShareMetal, SeatShareEnergy, SeatShareMapping, SeatShareRadar, SeatShareAll, SeatSetShareMetal,
		SeatSetShareEnergy, SeatShareGift, SeatDeclareAlliance, SeatSharedVictory, SeatShootAll:
		return "a kind with a version-1 payload; this number is reserved for a later schema"
	}
	return "a kind number listed in command schema version 1"
}

// checkSeatWire is every context-only rule of §7.4.1–§7.4.2. The encoder and
// the decoder both run it, so they accept one set of values.
func checkSeatWire(context CommandContext, c *SeatCommand) error {
	fail := func(path, expected string) error { return seatCodecError(context, path, expected) }
	if why := seatCodecKind(context, c.Kind); why != "" {
		return fail("kind", why)
	}
	// An unselected amount must be zero in its bits too: negative zero
	// compares equal to zero, but it is data the encoding would discard.
	if !c.unselectedZero() || c.Kind != SeatSetResource && math.Float32bits(c.SetResource.Amount) != 0 ||
		c.Kind != SeatGive && math.Float32bits(c.Give.Amount) != 0 {
		return fail("payload", "every payload record but the kind's own to be zero")
	}
	online := context == OnlineCommand
	switch c.Kind {
	case SeatOrder:
		p := &c.Order
		if err := checkWireActors(context, "order.actors", p.Actors, len(p.Targets) == 0); err != nil {
			return err
		}
		if p.Code < 1 || p.Code > 14 {
			return fail("order.code", "an order code 1..14")
		}
		if err := checkWireRef(context, "order.target", p.Target, true); err != nil {
			return err
		}
		if p.Position.InterfaceType > 1 {
			return fail("order.position.interfaceType", "an interface type 0 left or 1 right")
		}
		if len(p.Targets) > seatMaxAreaEntries {
			return fail("order.targets", fmt.Sprintf("at most %d area entries", seatMaxAreaEntries))
		}
		for i := range p.Targets {
			// Paths are formatted only on failure: an area list holds up to
			// 65535 entries.
			if t := &p.Targets[i]; !wireRefValid(t.Target, true) {
				return checkWireRef(context, fmt.Sprintf("order.targets[%d].target", i), t.Target, true)
			} else if t.Position.InterfaceType > 1 {
				return fail(fmt.Sprintf("order.targets[%d].position.interfaceType", i), "an interface type 0 left or 1 right")
			}
		}
		// The combinations of §7.4.2, which describe the gesture producers.
		if p.AssignedPosition && (len(p.Actors) != 1 || p.Code != 2 || p.Target.Handle != 0 || len(p.Targets) != 0 || p.TrackQueuedMove) {
			return fail("order.assignedPosition", "an assigned position with one actor, code 2, no target and no area list")
		}
		if p.TrackQueuedMove && (p.Code != 2 || !p.Queued || p.Target.Handle != 0 || len(p.Targets) != 0) {
			return fail("order.trackQueuedMove", "a tracked move with code 2, queued, no target and no area list")
		}
		if len(p.Targets) != 0 && p.Target.Handle != 0 {
			return fail("order.target", "a null outer target beside an area list")
		}
	case SeatStop:
		return checkWireActors(context, "stop.actors", c.Stop.Actors, false)
	case SeatActivation:
		return checkWireRef(context, "activation.unit", c.Activation.Unit, false)
	case SeatMobileBuild:
		p := &c.MobileBuild
		if err := checkWireRef(context, "mobileBuild.builder", p.Builder, false); err != nil {
			return err
		}
		if why := validKey(p.Product, false); why != "" {
			return fail("mobileBuild.product", why)
		}
		if p.Facing > 3 {
			return fail("mobileBuild.facing", "a facing 0..3")
		}
	case SeatFactoryBuild:
		p := &c.FactoryBuild
		if err := checkWireRef(context, "factoryBuild.builder", p.Builder, false); err != nil {
			return err
		}
		if why := validKey(p.Product, false); why != "" {
			return fail("factoryBuild.product", why)
		}
		if why := validCount(p.Count, online); why != "" {
			return fail("factoryBuild.count", why)
		}
	case SeatCancelProduction:
		return checkWireRef(context, "cancelProduction.unit", c.CancelProduction.Unit, false)
	case SeatStockpile:
		if err := checkWireRef(context, "stockpile.unit", c.Stockpile.Unit, false); err != nil {
			return err
		}
		if why := validCount(c.Stockpile.Count, online); why != "" {
			return fail("stockpile.count", why)
		}
	case SeatGroupAssign:
		if c.GroupAssign.Group < 1 || c.GroupAssign.Group > 9 {
			return fail("groupAssign.group", "a group 1..9")
		}
		return checkWireActors(context, "groupAssign.members", c.GroupAssign.Members, false)
	case SeatStance:
		if c.Stance.Value > 2 {
			return fail("stance.value", "a stance value 0..2")
		}
		return checkWireActors(context, "stance.actors", c.Stance.Actors, false)
	case SeatCloak:
		return checkWireActors(context, "cloak.actors", c.Cloak.Actors, false)
	case SeatSelfDestruct:
		return checkWireActors(context, "selfDestruct.actors", c.SelfDestruct.Actors, false)
	case SeatSetResource:
		p := &c.SetResource
		if online && p.Player != 0 {
			return fail("setResource.player", "no player field online; the issuing seat's own stock is written")
		}
		if p.Player > 9 {
			return fail("setResource.player", "a player 0..9")
		}
		if _, ok := seatWireResource(p.Resource); !ok {
			return fail("setResource.resource", "resource 0 metal or 1 energy")
		}
		if online && !netproto.OnlineAmount(math.Float32bits(p.Amount)) {
			return fail("setResource.amount", "a finite amount, positive zero only")
		}
	case SeatSetLogo:
		if c.SetLogo.Player > 9 {
			return fail("setLogo.player", "a player 0..9")
		}
	case SeatView:
		if c.View.Player > 9 {
			return fail("view.player", "a player 0..9")
		}
	case SeatGive:
		p := &c.Give
		if p.Player > 9 {
			return fail("give.player", "a recipient 0..9")
		}
		if _, ok := seatWireResource(p.Resource); !ok {
			return fail("give.resource", "resource 0 metal or 1 energy")
		}
		if online && !wholeGiftAmount(p.Amount) {
			return fail("give.amount", "a whole amount from -2^31 to 2^31 that the gift's only producer can generate")
		}
	case SeatVisibility:
		if c.Visibility.ToggleMask > 7 || c.Visibility.ClearMask > 7 {
			return fail("visibility", "visibility masks 0..7")
		}
	case SeatMeteor:
		if !c.Meteor.ArgumentPresent && c.Meteor.Enabled {
			return fail("meteor.enabled", "Enabled false when no argument is present")
		}
	case SeatCancelQueuedMove:
		if c.CancelQueuedMove.Sequence == 0 {
			return fail("cancelQueuedMove.sequence", "a tracked-move sequence of at least 1")
		}
		return checkWireActors(context, "cancelQueuedMove.actors", c.CancelQueuedMove.Actors, false)
	case SeatDeveloperSpawn:
		if why := validKey(c.DeveloperSpawn.Pattern, false); why != "" {
			return fail("developerSpawn.pattern", why)
		}
	case SeatSpawn:
		if why := validKey(c.Spawn.Unit, false); why != "" {
			return fail("spawn.unit", why)
		}
	case SeatBuilderOptions:
		p := &c.BuilderOptions
		if online && p.Owner != 0 {
			return fail("builderOptions.owner", "no owner field online; the issuing seat's options change")
		}
		if p.Owner > 9 {
			return fail("builderOptions.owner", "an owner 0..9")
		}
		for i := range p.Guard {
			if p.Guard[i] > 2 || p.Patrol[i] > 2 {
				return fail("builderOptions", "builder option values 0..2")
			}
		}
	case SeatCommunityOrderDrag:
		p := &c.CommunityOrderDrag
		if err := checkWireRef(context, "communityOrderDrag.unit", p.Unit, false); err != nil {
			return err
		}
		if p.Target != (pool.UnitRef{}) {
			return fail("communityOrderDrag.target", "a null drag target in command schema version 1")
		}
		if why := validKey(p.BuildProduct, true); why != "" {
			return fail("communityOrderDrag.buildProduct", why)
		}
		if p.BuildFacing > 3 {
			return fail("communityOrderDrag.buildFacing", "a build facing 0..3")
		}
	case SeatCommunityKickout:
		return checkWireRef(context, "communityKickout.unit", c.CommunityKickout.Unit, false)
	case SeatGameplay:
		name := string(c.Gameplay.Mode)
		if why := validKey(name, false); why != "" {
			return fail("gameplay.mode", why)
		}
		if _, ok := LookupRuleSet(name); !ok {
			return fail("gameplay.mode", fmt.Sprintf("a rule set this build registered, got %q", name))
		}
	}
	return nil
}

// checkWireRef is the `ref` primitive: null (both halves zero) where the
// field is nullable, otherwise a handle and a serial that are both nonzero.
func checkWireRef(context CommandContext, path string, r pool.UnitRef, nullable bool) error {
	switch {
	case r.Handle == 0 && r.Serial == 0:
		if nullable {
			return nil
		}
		return seatCodecError(context, path, "a non-null actor reference")
	case r.Handle == 0:
		return seatCodecError(context, path, "a reference whose handle and serial are both zero or both nonzero")
	case r.Serial == 0:
		return seatWireReference(context, path)
	}
	return nil
}

// seatWireReference refuses a nonzero handle with a zero serial.
//
// Design reading (U2's open question, §16.2): the local adapter captures a
// handle whose slot was already freed as a reference with serial 0
// (Session.captureRef), and version 1's `ref` has no form for it, since
// exactly one zero half is invalid (§7.4.1). Online the receiver would refuse
// it anyway. In the single-player replay context the encoder cannot preserve
// it either, so it refuses with ErrSeatCommandNoWireForm rather than write a
// different command: dropping a stale actor would apply identically, but
// rewriting an ordinary order's stale target would not, because the
// single-player result keeps the dead handle in the ground order's node
// (§7.4.3).
//
// The single-player recorder needs no form for either (replay_convert.go): it
// converts at the instant phase 1 applies the command, where a zero-serial
// capture takes a serial no allocation has had — same handle, resolving to
// no unit in the recording and its playback — and a repeated actor that
// resolves to none, or of an idempotent kind, is kept once. A live repeat of
// any other kind stops the recording.
//
// TODO(question): whether version 1 should carry a live repeated actor
// natively. Proposal, with no byte-layout change: in the single-player
// replay context only, `actors` keeps repeats in captured order for every
// kind but the ordinary Order, whose consumer visits a set; phase-1 replay
// validation (seatSchema) widens to match, and the online context keeps the
// refusal. Settle by approving or replacing that proposal in
// DESIGN_MULTIPLAYER §7.4.1.
func seatWireReference(context CommandContext, path string) error {
	return seatNoWireForm(context, path, "a reference with an allocation serial; a handle captured from a freed slot (serial 0) has no version-1 form")
}

// checkWireActors is the `actors` primitive: at most 3276 non-null
// references with no repeated handle, and for an ordinary order strictly
// ascending handles (§7.4.1, §7.4.3).
//
// Design reading: a repeated actor is refused with ErrSeatCommandNoWireForm
// in both contexts. The local adapter keeps repeats in a Stop, SelfDestruct,
// CancelQueuedMove or area-order list and applies each, so dropping one
// would change the replayed result; see seatWireReference's proposal.
func checkWireActors(context CommandContext, path string, rs []pool.UnitRef, ascending bool) error {
	if len(rs) > seatMaxActors {
		return seatCodecError(context, path, fmt.Sprintf("at most %d actors", seatMaxActors))
	}
	var seen [1 << 16 / 64]uint64
	for i, r := range rs {
		at := func() string { return fmt.Sprintf("%s[%d]", path, i) }
		if !wireRefValid(r, false) {
			return checkWireRef(context, at(), r, false)
		}
		if seen[r.Handle/64]&(1<<(r.Handle%64)) != 0 {
			return seatNoWireForm(context, at(), "actors without a repeated handle; a repeated actor has no version-1 form")
		}
		seen[r.Handle/64] |= 1 << (r.Handle % 64)
		if ascending && i > 0 && r.Handle < rs[i-1].Handle {
			return seatCodecError(context, at(), "an ordinary order's actors in strictly ascending handle order")
		}
	}
	return nil
}

// wireRefValid is checkWireRef's verdict without its diagnostic.
func wireRefValid(r pool.UnitRef, nullable bool) bool {
	if r.Handle == 0 && r.Serial == 0 {
		return nullable
	}
	return r.Handle != 0 && r.Serial != 0
}

// seatWireResource is the explicit wire mapping of a resource (§7.4.2):
// 0 metal, 1 energy.
func seatWireResource(r economy.Res) (uint8, bool) {
	switch r {
	case economy.Metal:
		return 0, true
	case economy.Energy:
		return 1, true
	}
	return 0, false
}

// writeSeatCommand writes a command checkSeatWire accepted.
func writeSeatCommand(w *netproto.Writer, context CommandContext, c *SeatCommand) {
	w.U8(uint8(c.Kind))
	replay := context == SinglePlayerReplay
	switch c.Kind {
	case SeatOrder:
		p := &c.Order
		writeWireActors(w, p.Actors)
		w.U8(p.Code)
		writeWireRef(w, p.Target)
		writeWirePosition(w, p.Position)
		w.Bool(p.Queued)
		w.Bool(p.AssignedPosition)
		w.Bool(p.TrackQueuedMove)
		w.U16(uint16(len(p.Targets)))
		for _, t := range p.Targets {
			writeWireRef(w, t.Target)
			writeWirePosition(w, t.Position)
		}
	case SeatStop:
		writeWireActors(w, c.Stop.Actors)
	case SeatActivation:
		writeWireRef(w, c.Activation.Unit)
		w.Bool(c.Activation.Activate)
		w.Bool(c.Activation.Queued)
	case SeatMobileBuild:
		p := &c.MobileBuild
		writeWireRef(w, p.Builder)
		w.Key(p.Product)
		writeWirePoint(w, p.Position)
		w.U8(p.Facing)
		w.Bool(p.Queued)
		w.Bool(p.AppendOnly)
	case SeatFactoryBuild:
		writeWireRef(w, c.FactoryBuild.Builder)
		w.Key(c.FactoryBuild.Product)
		w.S32(c.FactoryBuild.Count)
	case SeatCancelProduction:
		writeWireRef(w, c.CancelProduction.Unit)
	case SeatStockpile:
		writeWireRef(w, c.Stockpile.Unit)
		w.S32(c.Stockpile.Count)
	case SeatGroupAssign:
		w.U8(c.GroupAssign.Group)
		writeWireActors(w, c.GroupAssign.Members)
	case SeatStance:
		writeWireActors(w, c.Stance.Actors)
		w.Bool(c.Stance.Fire)
		w.U8(c.Stance.Value)
	case SeatCloak:
		writeWireActors(w, c.Cloak.Actors)
		w.Bool(c.Cloak.Cloak)
	case SeatSelfDestruct:
		writeWireActors(w, c.SelfDestruct.Actors)
		w.Bool(c.SelfDestruct.Queued)
	case SeatSetResource:
		p := &c.SetResource
		if replay {
			w.U8(p.Player) // the replay-only field precedes Resource
		}
		resource, _ := seatWireResource(p.Resource)
		w.U8(resource)
		w.Amount(math.Float32bits(p.Amount))
	case SeatSetLogo:
		w.U8(c.SetLogo.Player)
		w.U8(c.SetLogo.Logo)
	case SeatView:
		w.U8(c.View.Player)
	case SeatGive:
		p := &c.Give
		w.U8(p.Player)
		resource, _ := seatWireResource(p.Resource)
		w.U8(resource)
		w.Amount(math.Float32bits(p.Amount))
	case SeatVisibility:
		w.U8(c.Visibility.ToggleMask)
		w.U8(c.Visibility.ClearMask)
	case SeatMeteor:
		w.Bool(c.Meteor.ArgumentPresent)
		w.Bool(c.Meteor.Enabled)
	case SeatCancelQueuedMove:
		w.U64(c.CancelQueuedMove.Sequence)
		writeWireActors(w, c.CancelQueuedMove.Actors)
	case SeatDeveloperSpawn:
		w.Key(c.DeveloperSpawn.Pattern)
		w.U8(c.DeveloperSpawn.Owner)
		writeWirePoint(w, c.DeveloperSpawn.Position)
	case SeatSpawn:
		w.Key(c.Spawn.Unit)
		writeWirePoint(w, c.Spawn.Position)
	case SeatBuilderOptions:
		p := &c.BuilderOptions
		if replay {
			w.U8(p.Owner) // the replay-only field comes first
		}
		for _, g := range p.Guard {
			w.U8(g)
		}
		for _, v := range p.Patrol {
			w.U8(v)
		}
	case SeatCommunityOrderDrag:
		p := &c.CommunityOrderDrag
		writeWireRef(w, p.Unit)
		w.U16(p.Index)
		w.S32(p.DescriptorID)
		w.U32(p.CreationTick)
		writeWireRef(w, p.Target)
		writeWirePoint(w, p.Goal)
		w.Key(p.BuildProduct)
		w.U8(p.BuildFacing)
		writeWirePoint(w, p.Destination)
	case SeatCommunityKickout:
		writeWireRef(w, c.CommunityKickout.Unit)
		writeWirePoint(w, c.CommunityKickout.Destination)
	case SeatGameplay:
		w.Key(string(c.Gameplay.Mode))
	}
}

func writeWireRef(w *netproto.Writer, r pool.UnitRef) {
	w.U16(uint16(r.Handle))
	w.U64(r.Serial)
}

func writeWireActors(w *netproto.Writer, rs []pool.UnitRef) {
	w.U16(uint16(len(rs)))
	for _, r := range rs {
		writeWireRef(w, r)
	}
}

func writeWirePoint(w *netproto.Writer, p CommandPoint) {
	w.S64(int64(p.X))
	w.S64(int64(p.Y))
	w.S64(int64(p.Z))
}

func writeWirePosition(w *netproto.Writer, p CommandPosition) {
	writeWirePoint(w, CommandPoint{X: p.X, Y: p.Y, Z: p.Z})
	w.U8(p.InterfaceType)
	w.Bool(p.HasFeature)
}

// readSeatCommand reads the kind byte, refuses a kind with no payload in the
// context before reading further, then reads that kind's fields. Field
// domains are checkSeatWire's, except the resource byte, which has no Go
// value outside its mapping.
func readSeatCommand(r *netproto.Reader, context CommandContext) SeatCommand {
	var c SeatCommand
	c.Kind = SeatCommandKind(r.U8())
	if r.Err() != nil {
		return SeatCommand{}
	}
	if why := seatCodecKind(context, c.Kind); why != "" {
		r.FailAt(0, why)
		return SeatCommand{}
	}
	replay := context == SinglePlayerReplay
	switch c.Kind {
	case SeatOrder:
		p := &c.Order
		p.Actors = readWireActors(r)
		p.Code = r.U8()
		p.Target = readWireRef(r)
		p.Position = readWirePosition(r)
		p.Queued = r.Bool()
		p.AssignedPosition = r.Bool()
		p.TrackQueuedMove = r.Bool()
		if n := r.Count16(seatMaxAreaEntries, seatAreaEntryMinBytes); n > 0 {
			p.Targets = make([]CommandTarget, n)
			for i := range p.Targets {
				p.Targets[i].Target = readWireRef(r)
				p.Targets[i].Position = readWirePosition(r)
			}
		}
	case SeatStop:
		c.Stop.Actors = readWireActors(r)
	case SeatActivation:
		c.Activation.Unit = readWireRef(r)
		c.Activation.Activate = r.Bool()
		c.Activation.Queued = r.Bool()
	case SeatMobileBuild:
		p := &c.MobileBuild
		p.Builder = readWireRef(r)
		p.Product = r.Key()
		p.Position = readWirePoint(r)
		p.Facing = r.U8()
		p.Queued = r.Bool()
		p.AppendOnly = r.Bool()
	case SeatFactoryBuild:
		c.FactoryBuild.Builder = readWireRef(r)
		c.FactoryBuild.Product = r.Key()
		c.FactoryBuild.Count = r.S32()
	case SeatCancelProduction:
		c.CancelProduction.Unit = readWireRef(r)
	case SeatStockpile:
		c.Stockpile.Unit = readWireRef(r)
		c.Stockpile.Count = r.S32()
	case SeatGroupAssign:
		c.GroupAssign.Group = r.U8()
		c.GroupAssign.Members = readWireActors(r)
	case SeatStance:
		c.Stance.Actors = readWireActors(r)
		c.Stance.Fire = r.Bool()
		c.Stance.Value = r.U8()
	case SeatCloak:
		c.Cloak.Actors = readWireActors(r)
		c.Cloak.Cloak = r.Bool()
	case SeatSelfDestruct:
		c.SelfDestruct.Actors = readWireActors(r)
		c.SelfDestruct.Queued = r.Bool()
	case SeatSetResource:
		p := &c.SetResource
		if replay {
			p.Player = r.U8()
		}
		p.Resource = readWireResource(r)
		p.Amount = math.Float32frombits(r.Amount())
	case SeatSetLogo:
		c.SetLogo.Player = r.U8()
		c.SetLogo.Logo = r.U8()
	case SeatView:
		c.View.Player = r.U8()
	case SeatGive:
		c.Give.Player = r.U8()
		c.Give.Resource = readWireResource(r)
		c.Give.Amount = math.Float32frombits(r.Amount())
	case SeatVisibility:
		c.Visibility.ToggleMask = r.U8()
		c.Visibility.ClearMask = r.U8()
	case SeatMeteor:
		c.Meteor.ArgumentPresent = r.Bool()
		c.Meteor.Enabled = r.Bool()
	case SeatCancelQueuedMove:
		c.CancelQueuedMove.Sequence = r.U64()
		c.CancelQueuedMove.Actors = readWireActors(r)
	case SeatDeveloperSpawn:
		c.DeveloperSpawn.Pattern = r.Key()
		c.DeveloperSpawn.Owner = r.U8()
		c.DeveloperSpawn.Position = readWirePoint(r)
	case SeatSpawn:
		c.Spawn.Unit = r.Key()
		c.Spawn.Position = readWirePoint(r)
	case SeatBuilderOptions:
		p := &c.BuilderOptions
		if replay {
			p.Owner = r.U8()
		}
		for i := range p.Guard {
			p.Guard[i] = r.U8()
		}
		for i := range p.Patrol {
			p.Patrol[i] = r.U8()
		}
	case SeatCommunityOrderDrag:
		p := &c.CommunityOrderDrag
		p.Unit = readWireRef(r)
		p.Index = r.U16()
		p.DescriptorID = r.S32()
		p.CreationTick = r.U32()
		p.Target = readWireRef(r)
		p.Goal = readWirePoint(r)
		p.BuildProduct = r.OptionalKey()
		p.BuildFacing = r.U8()
		p.Destination = readWirePoint(r)
	case SeatCommunityKickout:
		c.CommunityKickout.Unit = readWireRef(r)
		c.CommunityKickout.Destination = readWirePoint(r)
	case SeatGameplay:
		c.Gameplay.Mode = gameplay.Mode(r.Key())
	}
	if r.Err() != nil {
		return SeatCommand{}
	}
	return c
}

func readWireRef(r *netproto.Reader) pool.UnitRef {
	h := r.U16()
	return pool.UnitRef{Handle: pool.Handle(h), Serial: r.U64()}
}

// readWireActors reads an `actors` count, bounded by the 3276-actor ceiling
// and the bytes left before the list is allocated, and its references.
func readWireActors(r *netproto.Reader) []pool.UnitRef {
	n := r.Count16(seatMaxActors, seatActorMinBytes)
	if n == 0 {
		return nil
	}
	out := make([]pool.UnitRef, n)
	for i := range out {
		out[i] = readWireRef(r)
	}
	return out
}

func readWirePoint(r *netproto.Reader) CommandPoint {
	x := numeric.Fixed(r.S64())
	y := numeric.Fixed(r.S64())
	return CommandPoint{X: x, Y: y, Z: numeric.Fixed(r.S64())}
}

func readWirePosition(r *netproto.Reader) CommandPosition {
	p := readWirePoint(r)
	return CommandPosition{X: p.X, Y: p.Y, Z: p.Z, InterfaceType: r.U8(), HasFeature: r.Bool()}
}

// readWireResource maps the resource byte back through §7.4.2's explicit
// table; any other byte is refused where it stands.
func readWireResource(r *netproto.Reader) economy.Res {
	v := r.U8()
	switch {
	case r.Err() != nil:
		return economy.Metal
	case v == 0:
		return economy.Metal
	case v == 1:
		return economy.Energy
	}
	r.FailAt(r.Offset()-1, "resource 0 metal or 1 energy")
	return economy.Metal
}
