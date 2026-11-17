package session

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Codec tests of DESIGN_MULTIPLAYER §7.4.1–§7.4.4 and contracts M2-C4,
// M2-C5 and M2-C11. The wire form is Nanolathe protocol, not retail data.

// le32 is a binary32's four little-endian bytes, written independently of
// the codec.
func le32(f float32) []byte {
	b := math.Float32bits(f)
	return []byte{byte(b), byte(b >> 8), byte(b >> 16), byte(b >> 24)}
}

func cat(parts ...any) []byte {
	var out []byte
	for _, p := range parts {
		switch v := p.(type) {
		case int:
			out = append(out, byte(v))
		case string:
			out = append(out, v...)
		case []byte:
			out = append(out, v...)
		default:
			panic(fmt.Sprintf("cat: %T", p))
		}
	}
	return out
}

// seatWireCase is one command in one context. since is the first command
// schema version with its payload; zero means version 1.
type seatWireCase struct {
	name    string
	context CommandContext
	command SeatCommand
	wire    []byte
	since   uint16
}

// first is the case's first command schema version.
func (c seatWireCase) first() uint16 { return max(c.since, seatSchemaV1) }

// seatSchemaVersions is every command schema version this build speaks.
func seatSchemaVersions() []uint16 {
	var out []uint16
	for v := seatSchemaV1; v <= netproto.CommandSchemaVersion; v++ {
		out = append(out, v)
	}
	return out
}

// seatSharingKinds are the kinds whose payloads command schema version 2
// adds.
var seatSharingKinds = []SeatCommandKind{SeatShareMetal, SeatShareEnergy, SeatShareMapping, SeatShareRadar, SeatShareAll,
	SeatSetShareMetal, SeatSetShareEnergy, SeatShareGift, SeatDeclareAlliance, SeatSharedVictory}

// seatWireLayouts is every kind with a payload, in every context and from
// the first schema version that admits it, with its bytes written out by
// hand from the §7.4.2 table: the field order, widths and signedness are
// locked here, independently of the encoder. The version-1 cases are the
// version-1 golden encodings, unchanged under version 2.
func seatWireLayouts() []seatWireCase {
	ref := func(h pool.Handle, s uint64) pool.UnitRef { return pool.UnitRef{Handle: h, Serial: s} }
	var cases []seatWireCase
	both := func(name string, c SeatCommand, wire []byte) {
		cases = append(cases, seatWireCase{name: name + " online", context: OnlineCommand, command: c, wire: wire},
			seatWireCase{name: name + " replay", context: SinglePlayerReplay, command: c, wire: wire})
	}
	replay := func(name string, c SeatCommand, wire []byte) {
		cases = append(cases, seatWireCase{name: name, context: SinglePlayerReplay, command: c, wire: wire})
	}
	online := func(name string, c SeatCommand, wire []byte) {
		cases = append(cases, seatWireCase{name: name, context: OnlineCommand, command: c, wire: wire})
	}
	// The sharing and alliance kinds are online-only from version 2.
	sharing := func(name string, c SeatCommand, wire []byte) {
		cases = append(cases, seatWireCase{name: name, context: OnlineCommand, command: c, wire: wire, since: seatSchemaV2})
	}
	both("ordinary order", SeatCommand{Kind: SeatOrder, Order: OrderPayload{
		Actors: []pool.UnitRef{ref(2, 9), ref(5, 1)}, Code: 2, Target: ref(7, 300),
		Position: CommandPosition{X: 5, Y: -3, Z: 64, InterfaceType: 1, HasFeature: true}, Queued: true}},
		cat(4, 2, 2, 9, 5, 1, 2, 7, 0xac, 0x02, 0x0a, 0x05, 0x80, 0x01, 1, 1, 1, 0, 0, 0))
	both("area order", SeatCommand{Kind: SeatOrder, Order: OrderPayload{
		Actors: []pool.UnitRef{ref(5, 1), ref(2, 9)}, Code: 12,
		Targets: []CommandTarget{{Target: ref(3, 4), Position: CommandPosition{X: 1}}, {Position: CommandPosition{X: -1, HasFeature: true}}}}},
		cat(4, 2, 5, 1, 2, 9, 12, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 3, 4, 2, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 1))
	both("assigned position", SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{ref(1, 1)}, Code: 2, AssignedPosition: true}},
		cat(4, 1, 1, 1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0))
	both("tracked move", SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{ref(1, 1)}, Code: 2, Queued: true, TrackQueuedMove: true}},
		cat(4, 1, 1, 1, 2, 0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0))
	both("stop", SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{ref(1, 1)}}}, cat(5, 1, 1, 1))
	both("empty stop", SeatCommand{Kind: SeatStop}, cat(5, 0))
	both("activation", SeatCommand{Kind: SeatActivation, Activation: ActivationPayload{Unit: ref(3, 300), Activate: true}}, cat(6, 3, 0xac, 0x02, 1, 0))
	both("mobile build", SeatCommand{Kind: SeatMobileBuild, MobileBuild: MobileBuildPayload{Builder: ref(4, 5), Product: "armsolar",
		Position: CommandPoint{X: 2, Y: 4, Z: -2}, Facing: 3, Queued: true, AppendOnly: true}},
		cat(7, 4, 5, 8, "armsolar", 4, 8, 3, 3, 1, 1))
	both("factory build", SeatCommand{Kind: SeatFactoryBuild, FactoryBuild: FactoryBuildPayload{Builder: ref(4, 5), Product: "scout", Count: -3}},
		cat(8, 4, 5, 5, "scout", 5))
	both("cancel production", SeatCommand{Kind: SeatCancelProduction, CancelProduction: CancelProductionPayload{Unit: ref(6, 7)}}, cat(9, 6, 7))
	both("stockpile", SeatCommand{Kind: SeatStockpile, Stockpile: StockpilePayload{Unit: ref(6, 7), Count: 2}}, cat(10, 6, 7, 4))
	both("group assign", SeatCommand{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Group: 9, Members: []pool.UnitRef{ref(1, 2)}}}, cat(12, 9, 1, 1, 2))
	both("group clear", SeatCommand{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Group: 1}}, cat(12, 1, 0))
	both("stance", SeatCommand{Kind: SeatStance, Stance: StancePayload{Actors: []pool.UnitRef{ref(1, 2)}, Fire: true, Value: 2}}, cat(14, 1, 1, 2, 1, 2))
	both("cloak", SeatCommand{Kind: SeatCloak, Cloak: CloakPayload{Actors: []pool.UnitRef{ref(1, 2)}, Cloak: true}}, cat(15, 1, 1, 2, 1))
	both("self-destruct", SeatCommand{Kind: SeatSelfDestruct, SelfDestruct: SelfDestructPayload{Actors: []pool.UnitRef{ref(1, 2)}, Queued: true}}, cat(16, 1, 1, 2, 1))
	replay("no shake", SeatCommand{Kind: SeatNoShake}, cat(17))
	both("ATM", SeatCommand{Kind: SeatATM}, cat(18))
	online("set resource online", SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Resource: economy.Energy, Amount: 1.5}},
		cat(19, 1, le32(1.5)))
	replay("set resource replay", SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Player: 2, Resource: economy.Metal, Amount: -1}},
		cat(19, 2, 0, le32(-1)))
	replay("set logo", SeatCommand{Kind: SeatSetLogo, SetLogo: SetLogoPayload{Player: 3, Logo: 200}}, cat(20, 3, 200))
	both("view", SeatCommand{Kind: SeatView, View: ViewPayload{Player: 4}}, cat(21, 4))
	both("give", SeatCommand{Kind: SeatGive, Give: GivePayload{Player: 0, Resource: economy.Metal, Amount: 20}}, cat(22, 0, 0, le32(20)))
	both("make selectable", SeatCommand{Kind: SeatMakeSelectable}, cat(23))
	both("visibility", SeatCommand{Kind: SeatVisibility, Visibility: VisibilityPayload{ToggleMask: 5, ClearMask: 2}}, cat(24, 5, 2))
	both("double shot", SeatCommand{Kind: SeatDoubleShot}, cat(25))
	both("half shot", SeatCommand{Kind: SeatHalfShot}, cat(26))
	both("meteor", SeatCommand{Kind: SeatMeteor, Meteor: MeteorPayload{ArgumentPresent: true, Enabled: true}}, cat(27, 1, 1))
	both("cancel queued move", SeatCommand{Kind: SeatCancelQueuedMove, CancelQueuedMove: CancelQueuedMovePayload{Sequence: 300, Actors: []pool.UnitRef{ref(1, 2)}}},
		cat(30, 0xac, 0x02, 1, 1, 2))
	both("spawn", SeatCommand{Kind: SeatSpawn, Spawn: SpawnPayload{Unit: "scout", Position: CommandPoint{X: 1, Z: 1}}}, cat(31, 5, "scout", 2, 0, 2))
	online("builder options online", SeatCommand{Kind: SeatBuilderOptions, BuilderOptions: BuilderOptionsPayload{Guard: [3]uint8{0, 1, 2}, Patrol: [3]uint8{2, 1, 0}}},
		cat(32, 0, 1, 2, 2, 1, 0))
	replay("builder options replay", SeatCommand{Kind: SeatBuilderOptions, BuilderOptions: BuilderOptionsPayload{Owner: 3, Guard: [3]uint8{0, 1, 2}, Patrol: [3]uint8{2, 1, 0}}},
		cat(32, 3, 0, 1, 2, 2, 1, 0))
	both("community order drag", SeatCommand{Kind: SeatCommunityOrderDrag, CommunityOrderDrag: CommunityOrderDragPayload{
		Unit: ref(2, 3), Index: 4, DescriptorID: -5, CreationTick: 600, Goal: CommandPoint{X: 1, Y: 2, Z: 3}, BuildFacing: 2,
		Destination: CommandPoint{X: -1, Y: -2, Z: -3}}},
		cat(33, 2, 3, 4, 9, 0xd8, 0x04, 0, 0, 2, 4, 6, 0, 2, 1, 3, 5))
	both("community order drag with product", SeatCommand{Kind: SeatCommunityOrderDrag, CommunityOrderDrag: CommunityOrderDragPayload{
		Unit: ref(2, 3), BuildProduct: "armsolar"}},
		cat(33, 2, 3, 0, 0, 0, 0, 0, 0, 0, 0, 8, "armsolar", 0, 0, 0, 0))
	both("community kickout", SeatCommand{Kind: SeatCommunityKickout, CommunityKickout: CommunityKickoutPayload{Unit: ref(2, 3), Destination: CommandPoint{X: 10, Z: -10}}},
		cat(34, 2, 3, 20, 0, 19))
	replay("developer spawn", SeatCommand{Kind: SeatDeveloperSpawn, DeveloperSpawn: DeveloperSpawnPayload{Pattern: "arm*", Owner: 255, Position: CommandPoint{X: 1, Z: -1}}}, cat(46, 4, "arm*", 255, 2, 0, 1))
	replay("gameplay", SeatCommand{Kind: SeatGameplay, Gameplay: GameplayPayload{Mode: gameplay.Strict31}}, cat(255, 10, "strict-3.1"))
	for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
		cases = append(cases, seatWireCase{name: "economy activity", context: context,
			command: SeatCommand{Kind: SeatEconomyActivity, EconomyActivity: EconomyActivityPayload{Activity: economy.ActivityConstruction, Pause: true}},
			wire:    cat(47, 2, 1), since: seatSchemaV3})
	}
	// Version 4's `+ShootAll` record: the kind byte alone, single-player
	// replay only.
	cases = append(cases, seatWireCase{name: "shoot all", context: SinglePlayerReplay, command: SeatCommand{Kind: SeatShootAll}, wire: cat(45), since: seatSchemaV4})

	sharing("share metal", SeatCommand{Kind: SeatShareMetal, ShareMetal: ShareMetalPayload{On: true}}, cat(35, 1))
	sharing("share energy off", SeatCommand{Kind: SeatShareEnergy}, cat(36, 0))
	sharing("share mapping", SeatCommand{Kind: SeatShareMapping, ShareMapping: ShareMappingPayload{On: true}}, cat(37, 1))
	sharing("share radar", SeatCommand{Kind: SeatShareRadar, ShareRadar: ShareRadarPayload{On: true}}, cat(38, 1))
	sharing("share all", SeatCommand{Kind: SeatShareAll, ShareAll: ShareAllPayload{Metal: true, Mapping: true}}, cat(39, 1, 0, 1, 0))
	sharing("share all other half", SeatCommand{Kind: SeatShareAll, ShareAll: ShareAllPayload{Energy: true, Radar: true}}, cat(39, 0, 1, 0, 1))
	sharing("set share metal", SeatCommand{Kind: SeatSetShareMetal, SetShareMetal: SetShareMetalPayload{Amount: 300}}, cat(40, 0xd8, 0x04))
	sharing("set share energy", SeatCommand{Kind: SeatSetShareEnergy, SetShareEnergy: SetShareEnergyPayload{Amount: -1}}, cat(41, 1))
	sharing("share gift", SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 3, Metal: 250, Energy: -2,
		Actors: []pool.UnitRef{ref(5, 1), ref(2, 9)}, MapInfo: true}},
		cat(42, 3, 0xf4, 0x03, 3, 2, 5, 1, 2, 9, 1))
	sharing("empty share gift", SeatCommand{Kind: SeatShareGift}, cat(42, 0, 0, 0, 0, 0))
	sharing("declare alliance", SeatCommand{Kind: SeatDeclareAlliance, DeclareAlliance: DeclareAlliancePayload{Target: 9, Value: true}}, cat(43, 9, 1))
	sharing("break alliance", SeatCommand{Kind: SeatDeclareAlliance, DeclareAlliance: DeclareAlliancePayload{Target: 2}}, cat(43, 2, 0))
	sharing("shared victory", SeatCommand{Kind: SeatSharedVictory, SharedVictory: SharedVictoryPayload{On: true}}, cat(44, 1))
	return cases
}

// seatWireExtremes are valid commands at the edges of their fields'
// representations: maximum handles and serials, the signed 64-bit extremes of
// a fixed coordinate, 255-byte keys, counts at their bounds and the replay
// context's exceptional amounts.
func seatWireExtremes() []seatWireCase {
	maxRef := pool.UnitRef{Handle: math.MaxUint16, Serial: math.MaxUint64}
	far := CommandPosition{X: math.MinInt64, Y: math.MaxInt64, Z: -1, InterfaceType: 1, HasFeature: true}
	key := strings.Repeat("k", 255)
	nan := math.Float32frombits(0x7fc00123)
	var out []seatWireCase
	for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
		count := int32(math.MaxInt32)
		if context == OnlineCommand {
			count = -32767
		}
		add := func(name string, c SeatCommand) {
			out = append(out, seatWireCase{name: fmt.Sprintf("%s (context %d)", name, context), context: context, command: c})
		}
		add("far order", SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{{Handle: 1, Serial: 1}, maxRef}, Code: 14, Target: maxRef, Position: far}})
		add("far area", SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{maxRef, {Handle: 1, Serial: 1}}, Code: 1, Position: far, Queued: true,
			Targets: []CommandTarget{{Target: maxRef, Position: far}, {Position: far}, {Position: far}}}})
		add("long key", SeatCommand{Kind: SeatFactoryBuild, FactoryBuild: FactoryBuildPayload{Builder: maxRef, Product: key, Count: count}})
		add("far kickout", SeatCommand{Kind: SeatCommunityKickout, CommunityKickout: CommunityKickoutPayload{Unit: maxRef, Destination: CommandPoint{X: math.MaxInt64, Y: math.MinInt64}}})
		add("far drag", SeatCommand{Kind: SeatCommunityOrderDrag, CommunityOrderDrag: CommunityOrderDragPayload{Unit: maxRef, Index: math.MaxUint16,
			DescriptorID: math.MinInt32, CreationTick: math.MaxUint32, Goal: CommandPoint{X: math.MinInt64}, BuildProduct: key, BuildFacing: 3,
			Destination: CommandPoint{Z: math.MaxInt64}}})
		add("last sequence", SeatCommand{Kind: SeatCancelQueuedMove, CancelQueuedMove: CancelQueuedMovePayload{Sequence: math.MaxUint64, Actors: []pool.UnitRef{maxRef}}})
		add("largest gift", SeatCommand{Kind: SeatGive, Give: GivePayload{Player: 9, Resource: economy.Energy, Amount: 2147483648}})
		add("most negative gift", SeatCommand{Kind: SeatGive, Give: GivePayload{Player: 9, Resource: economy.Energy, Amount: -2147483648}})
	}
	add := func(name string, c SeatCommand) {
		out = append(out, seatWireCase{name: name, context: SinglePlayerReplay, command: c})
	}
	add("replay NaN gift", SeatCommand{Kind: SeatGive, Give: GivePayload{Player: 1, Amount: nan}})
	add("replay negative zero", SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Player: 9, Amount: math.Float32frombits(0x80000000)}})
	add("replay infinity", SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Amount: float32(math.Inf(-1))}})
	add("replay fraction", SeatCommand{Kind: SeatGive, Give: GivePayload{Amount: 0.1}})
	add("replay community gameplay", SeatCommand{Kind: SeatGameplay, Gameplay: GameplayPayload{Mode: gameplay.Community39}})
	sharing := func(name string, c SeatCommand) {
		out = append(out, seatWireCase{name: name, context: OnlineCommand, command: c, since: seatSchemaV2})
	}
	sharing("widest gift", SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 9, Metal: math.MinInt32, Energy: math.MaxInt32,
		Actors: []pool.UnitRef{maxRef, {Handle: 1, Serial: 1}}, MapInfo: true}})
	sharing("lowest gift", SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Metal: math.MaxInt32, Energy: math.MinInt32}})
	sharing("lowest metal threshold", SeatCommand{Kind: SeatSetShareMetal, SetShareMetal: SetShareMetalPayload{Amount: math.MinInt32}})
	sharing("highest energy threshold", SeatCommand{Kind: SeatSetShareEnergy, SetShareEnergy: SetShareEnergyPayload{Amount: math.MaxInt32}})
	sharing("every option on", SeatCommand{Kind: SeatShareAll, ShareAll: ShareAllPayload{Metal: true, Energy: true, Mapping: true, Radar: true}})
	sharing("every option off", SeatCommand{Kind: SeatShareAll})
	return out
}

// sameSeatCommand compares two commands, amounts by their bits so a NaN
// payload compares equal to itself.
func sameSeatCommand(a, b SeatCommand) bool {
	if math.Float32bits(a.Give.Amount) != math.Float32bits(b.Give.Amount) || math.Float32bits(a.SetResource.Amount) != math.Float32bits(b.SetResource.Amount) {
		return false
	}
	a.Give.Amount, b.Give.Amount, a.SetResource.Amount, b.SetResource.Amount = 0, 0, 0, 0
	return reflect.DeepEqual(a, b)
}

// checkSeatDecode is the codec's one property: a payload is refused with no
// value, or decodes to a value whose encoding in the same schema version and
// context is that payload exactly. A decoder never accepts one value and
// hands back another.
func checkSeatDecode(t testing.TB, version uint16, context CommandContext, payload []byte) bool {
	t.Helper()
	_, ok := decodeSeatChecked(t, version, context, payload)
	return ok
}

func decodeSeatChecked(t testing.TB, version uint16, context CommandContext, payload []byte) (SeatCommand, bool) {
	t.Helper()
	c, err := DecodeSeatCommandVersion(version, context, payload)
	if err != nil {
		if !reflect.DeepEqual(c, SeatCommand{}) {
			t.Fatalf("a refused payload %x returned a value: %v", payload, err)
		}
		return SeatCommand{}, false
	}
	again, err := EncodeSeatCommandVersion(version, context, c)
	if err != nil || !bytes.Equal(again, payload) {
		t.Fatalf("an accepted payload re-encodes differently in version %d context %d:\n got %x, %v\nwant %x", version, context, again, err, payload)
	}
	return c, true
}

// checkSeatDecodeEverywhere holds checkSeatDecode in every schema version and
// context, and the version property of §7.4.1: each new version accepts
// earlier payloads unchanged, plus only the new kinds in that version.
// It reports whether any decode accepted the payload.
func checkSeatDecodeEverywhere(t testing.TB, payload []byte) bool {
	t.Helper()
	accepted := false
	for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
		v1, ok1 := decodeSeatChecked(t, seatSchemaV1, context, payload)
		v2, ok2 := decodeSeatChecked(t, seatSchemaV2, context, payload)
		v3, ok3 := decodeSeatChecked(t, seatSchemaV3, context, payload)
		v4, ok4 := decodeSeatChecked(t, seatSchemaV4, context, payload)
		sharing := ok2 && slices.Contains(seatSharingKinds, v2.Kind)
		if ok1 != (ok2 && !sharing) || ok1 && !sameSeatCommand(v1, v2) {
			t.Fatalf("payload %x in context %d: version 1 accepted %v (%+v), version 2 accepted %v (%+v)", payload, context, ok1, v1, ok2, v2)
		}
		if sharing && context != OnlineCommand {
			t.Fatalf("payload %x: a sharing kind decoded in context %d", payload, context)
		}
		activity := ok3 && v3.Kind == SeatEconomyActivity
		if ok2 != (ok3 && !activity) || ok2 && !sameSeatCommand(v2, v3) {
			t.Fatalf("payload %x in context %d: version 2 accepted %v (%+v), version 3 accepted %v (%+v)", payload, context, ok2, v2, ok3, v3)
		}
		shootAll := ok4 && v4.Kind == SeatShootAll
		if ok3 != (ok4 && !shootAll) || ok3 && !sameSeatCommand(v3, v4) {
			t.Fatalf("payload %x in context %d: version 3 accepted %v (%+v), version 4 accepted %v (%+v)", payload, context, ok3, v3, ok4, v4)
		}
		if shootAll && context != SinglePlayerReplay {
			t.Fatalf("payload %x: ShootAll decoded in context %d", payload, context)
		}
		accepted = accepted || ok4
	}
	return accepted
}

// Every kind's wire layout is the §7.4.2 table, field by field, and decodes
// back to the same value (§7.4.4). A version-1 layout is the same bytes in
// every version; a version-2 layout has no form in version 1.
func TestSeatCommandWireLayouts(t *testing.T) {
	type key struct {
		version uint16
		context CommandContext
		kind    SeatCommandKind
	}
	laid := map[key]bool{}
	for _, c := range seatWireLayouts() {
		for _, v := range seatSchemaVersions() {
			got, err := EncodeSeatCommandVersion(v, c.context, c.command)
			back, derr := DecodeSeatCommandVersion(v, c.context, c.wire)
			if v < c.first() {
				if err == nil || derr == nil {
					t.Fatalf("%s: version %d encoded (%v) or decoded (%v) a version-%d kind", c.name, v, err, derr, c.first())
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s, version %d: %v", c.name, v, err)
			}
			if !bytes.Equal(got, c.wire) {
				t.Fatalf("%s, version %d:\n got %x\nwant %x", c.name, v, got, c.wire)
			}
			if derr != nil || !sameSeatCommand(back, c.command) {
				t.Fatalf("%s, version %d: decoded %+v, %v", c.name, v, back, derr)
			}
			laid[key{v, c.context, c.command.Kind}] = true
		}
	}
	// Every kind with a payload in a version and context is laid out above.
	for _, v := range seatSchemaVersions() {
		for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
			for k := 0; k < 256; k++ {
				if seatCodecKind(seatCodec{v, context}, SeatCommandKind(k)) == "" && !laid[key{v, context, SeatCommandKind(k)}] {
					t.Errorf("kind %d has a version-%d payload in context %d but no layout case", k, v, context)
				}
			}
		}
	}
}

// The version-1 golden layouts encode identically through the unversioned
// entry points, which speak netproto.CommandSchemaVersion. Older versions
// refuse exactly the kinds added after them.
func TestSeatCommandNewVersionsKeepEarlierPayloads(t *testing.T) {
	if netproto.CommandSchemaVersion != seatSchemaV4 {
		t.Fatalf("this build speaks command schema version %d, want 4", netproto.CommandSchemaVersion)
	}
	for _, c := range seatWireLayouts() {
		got, err := EncodeSeatCommand(c.context, c.command)
		if err != nil || !bytes.Equal(got, c.wire) {
			t.Fatalf("%s: %x, %v", c.name, got, err)
		}
		if !checkSeatDecodeEverywhere(t, c.wire) {
			t.Fatalf("%s: decoded nowhere", c.name)
		}
		for _, version := range seatSchemaVersions() {
			accepted := checkSeatDecode(t, version, c.context, c.wire)
			if accepted != (version >= c.first()) {
				t.Fatalf("%s: version %d accepted %v", c.name, version, accepted)
			}
		}
	}
	for _, v := range []uint16{0, netproto.CommandSchemaVersion + 1, math.MaxUint16} {
		for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
			if _, err := EncodeSeatCommandVersion(v, context, SeatCommand{Kind: SeatATM}); err == nil || !strings.Contains(err.Error(), "logical path version") {
				t.Errorf("version %d encoded: %v", v, err)
			}
			if _, err := DecodeSeatCommandVersion(v, context, []byte{18}); err == nil || !strings.Contains(err.Error(), "logical path version") {
				t.Errorf("version %d decoded: %v", v, err)
			}
		}
	}
}

// The representation extremes round-trip, and the replay context keeps every
// bit of an amount, NaN payload and negative zero included (§7.4.1).
func TestSeatCommandExtremesRoundTrip(t *testing.T) {
	for _, c := range seatWireExtremes() {
		payload, err := EncodeSeatCommand(c.context, c.command)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		back, err := DecodeSeatCommand(c.context, payload)
		if err != nil || !sameSeatCommand(back, c.command) {
			t.Fatalf("%s: decoded %+v, %v", c.name, back, err)
		}
		if c.first() > seatSchemaV1 && checkSeatDecode(t, seatSchemaV1, c.context, payload) {
			t.Fatalf("%s: decoded in version 1", c.name)
		}
	}
}

// seatMutationSeeds are the valid payloads the mutation sweeps start from.
func seatMutationSeeds(t testing.TB) []seatWireCase {
	var out []seatWireCase
	for _, c := range append(seatWireLayouts(), seatWireExtremes()...) {
		payload, err := EncodeSeatCommandVersion(c.first(), c.context, c.command)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		c.wire = payload
		out = append(out, c)
	}
	return out
}

// Every proper prefix of a valid payload is refused, and so is any byte after
// its end, in every schema version that has the kind; flipping any byte under
// several masks is refused or an exactly re-encoding command in every
// version and context (M2-C4).
func TestSeatCommandTruncationsAndFlips(t *testing.T) {
	accepted := 0
	for _, c := range seatMutationSeeds(t) {
		for _, v := range seatSchemaVersions() {
			if v < c.first() {
				continue
			}
			for n := 0; n < len(c.wire); n++ {
				if checkSeatDecode(t, v, c.context, c.wire[:n]) {
					t.Fatalf("%s: a %d-byte prefix of %d decoded in version %d", c.name, n, len(c.wire), v)
				}
			}
			for _, tail := range [][]byte{{0}, {1}, {0xff}} {
				if checkSeatDecode(t, v, c.context, append(append([]byte(nil), c.wire...), tail...)) {
					t.Fatalf("%s: trailing %x decoded in version %d", c.name, tail, v)
				}
			}
		}
		for i := range c.wire {
			for _, mask := range []byte{0x01, 0x02, 0x04, 0x40, 0x80, 0xff} {
				m := append([]byte(nil), c.wire...)
				m[i] ^= mask
				if checkSeatDecodeEverywhere(t, m) {
					accepted++
				}
			}
		}
	}
	if accepted == 0 {
		t.Fatal("no flip was accepted: the sweep cannot tell a strict decoder from a broken one")
	}
}

// Bounded random edits — replaced, deleted and inserted bytes and cut tails —
// hold the same property, decoded in both contexts. The generator is a fixed
// xorshift, so every run edits the same way.
func TestSeatCommandRandomEdits(t *testing.T) {
	seeds := seatMutationSeeds(t)
	state := uint64(0x9e3779b97f4a7c15)
	next := func(n int) int {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % uint64(n))
	}
	for i := 0; i < 20000; i++ {
		p := append([]byte(nil), seeds[i%len(seeds)].wire...)
		for k := 1 + next(4); k > 0 && len(p) > 0; k-- {
			at := next(len(p))
			switch next(4) {
			case 0:
				p[at] = byte(next(256))
			case 1:
				p = append(p[:at], p[at+1:]...)
			case 2:
				p = append(p[:at], append([]byte{byte(next(256))}, p[at:]...)...)
			case 3:
				p = p[:at]
			}
		}
		checkSeatDecodeEverywhere(t, p)
	}
}

// FuzzDecodeSeatCommand holds the codec property over arbitrary input in
// every schema version and context, with the version property of
// checkSeatDecodeEverywhere, seeded with every kind's layout, the sharing
// and alliance kinds included. The fast tier runs the seeds; `go test -fuzz`
// explores further.
func FuzzDecodeSeatCommand(f *testing.F) {
	for _, c := range seatWireLayouts() {
		f.Add(c.wire)
	}
	f.Add([]byte{})
	f.Add([]byte{4, 0xff, 0xff, 0x03})
	f.Add([]byte{42, 9, 0xff, 0xff, 0xff, 0xff, 0x0f, 0xfe, 0xff, 0xff, 0xff, 0x0f, 1, 0xff, 0xff, 0x03, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01, 1})
	f.Fuzz(func(t *testing.T, payload []byte) {
		checkSeatDecodeEverywhere(t, payload)
	})
}

// Context is the session's, never the payload's: the replay-only kinds and
// fields are refused online before dispatch, the same bytes keep their
// meaning in replay, and local interface kinds, reserved numbers and
// unlisted numbers have no payload in either context (§7.4.1, §7.4.2,
// M2-C11).
func TestSeatCommandContextIsNotThePayloads(t *testing.T) {
	replayOnly := map[string]bool{"no shake": true, "set logo": true, "developer spawn": true, "gameplay": true, "set resource replay": true, "builder options replay": true,
		"shoot all": true}
	seen := 0
	for _, c := range seatWireLayouts() {
		if !replayOnly[c.name] {
			continue
		}
		seen++
		// NoShake, SetLogo, Gameplay and the replay forms of SetResource and
		// BuilderOptions: refused online whether as a value or as bytes.
		if _, err := EncodeSeatCommand(OnlineCommand, c.command); err == nil {
			t.Errorf("%s: encoded online", c.name)
		}
		for _, v := range seatSchemaVersions() {
			if checkSeatDecode(t, v, OnlineCommand, c.wire) {
				t.Errorf("%s: its replay bytes decoded online in version %d", c.name, v)
			}
		}
	}
	if seen != len(replayOnly) {
		t.Fatalf("found %d of the %d replay-only layouts", seen, len(replayOnly))
	}
	for _, c := range seatWireLayouts() {
		if c.context == OnlineCommand && (c.command.Kind == SeatSetResource || c.command.Kind == SeatBuilderOptions) && checkSeatDecode(t, seatSchemaV2, SinglePlayerReplay, c.wire) {
			t.Errorf("%s: its online bytes decoded in replay", c.name)
		}
	}
	for _, k := range []SeatCommandKind{0, SeatSelectionReplace, SeatSelectionToggle, SeatSelectionClear, SeatBuildPage, SeatGroupRecall,
		SeatBigBrother, SeatShiftState, 48, 128, 254} {
		for _, v := range seatSchemaVersions() {
			for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
				if _, err := EncodeSeatCommandVersion(v, context, SeatCommand{Kind: k}); err == nil {
					t.Errorf("kind %d encoded in version %d context %d", k, v, context)
				}
				for _, payload := range [][]byte{{byte(k)}, {byte(k), 0}, {byte(k), 1, 1, 1}} {
					if checkSeatDecode(t, v, context, payload) {
						t.Errorf("kind %d decoded in version %d context %d from %x", k, v, context, payload)
					}
				}
			}
		}
	}
	// The sharing and alliance kinds have payloads online from version 2
	// only: version 1 refuses them in both contexts, and no version has a
	// single-player replay form, because retail runs them only in a
	// networked session.
	sharing := 0
	for _, c := range seatWireLayouts() {
		if !slices.Contains(seatSharingKinds, c.command.Kind) {
			continue
		}
		sharing++
		for _, try := range []struct {
			version uint16
			context CommandContext
			why     string
		}{
			{seatSchemaV1, OnlineCommand, "from command schema version 2"},
			{seatSchemaV1, SinglePlayerReplay, "from command schema version 2"},
			{seatSchemaV2, SinglePlayerReplay, "only in a networked session"},
		} {
			_, err := EncodeSeatCommandVersion(try.version, try.context, c.command)
			if err == nil || !strings.Contains(err.Error(), try.why) || !strings.Contains(err.Error(), fmt.Sprintf("providers searched [%s]", seatCodecProvider(seatCodec{try.version, try.context}))) {
				t.Errorf("%s: version %d context %d encoded or refused for another reason: %v", c.name, try.version, try.context, err)
			}
			if _, err := DecodeSeatCommandVersion(try.version, try.context, c.wire); err == nil || !strings.Contains(err.Error(), try.why) {
				t.Errorf("%s: version %d context %d decoded or refused for another reason: %v", c.name, try.version, try.context, err)
			}
		}
	}
	if sharing < len(seatSharingKinds) {
		t.Fatalf("found %d sharing layouts, fewer than the %d kinds", sharing, len(seatSharingKinds))
	}
	// ShootAll has a payload from version 4 and in the single-player replay
	// context only: versions 1 to 3 refuse it in both contexts, and no
	// version has an online form until its seat semantics are designed (Q8).
	for _, v := range seatSchemaVersions() {
		for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
			why := "from command schema version 4"
			if v >= seatSchemaV4 {
				if context == SinglePlayerReplay {
					continue
				}
				why = "a single-player replay record"
			}
			_, err := EncodeSeatCommandVersion(v, context, SeatCommand{Kind: SeatShootAll})
			if err == nil || !strings.Contains(err.Error(), why) {
				t.Errorf("ShootAll: version %d context %d encoded or refused for another reason: %v", v, context, err)
			}
			if _, err := DecodeSeatCommandVersion(v, context, []byte{45}); err == nil || !strings.Contains(err.Error(), why) {
				t.Errorf("ShootAll: version %d context %d decoded or refused for another reason: %v", v, context, err)
			}
		}
	}
	// Replay-only fields must be zero in an online value; they are absent
	// from online bytes, never silently dropped.
	for _, c := range []SeatCommand{
		{Kind: SeatSetResource, SetResource: SetResourcePayload{Player: 1, Amount: 1}},
		{Kind: SeatBuilderOptions, BuilderOptions: BuilderOptionsPayload{Owner: 1}},
	} {
		if _, err := EncodeSeatCommand(OnlineCommand, c); err == nil {
			t.Errorf("kind %d with a replay-only field encoded online", c.Kind)
		}
		if _, err := EncodeSeatCommand(SinglePlayerReplay, c); err != nil {
			t.Errorf("kind %d with its replay field: %v", c.Kind, err)
		}
	}
	for _, context := range []CommandContext{0, 3} {
		if _, err := EncodeSeatCommand(context, SeatCommand{Kind: SeatATM}); err == nil {
			t.Errorf("context %d encoded", context)
		}
		if _, err := DecodeSeatCommand(context, []byte{18}); err == nil {
			t.Errorf("context %d decoded", context)
		}
	}
}

// Schema refusals the codec decides alone: enumerations, combinations,
// counts, keys, references, required zeros and the online amount domains.
// Each is refused by the encoder as a value and by the decoder as bytes.
func TestSeatCommandCodecRefusals(t *testing.T) {
	a, b := pool.UnitRef{Handle: 1, Serial: 1}, pool.UnitRef{Handle: 2, Serial: 2}
	order := func(mut func(*OrderPayload)) SeatCommand {
		c := SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{a}, Code: 2}}
		mut(&c.Order)
		return c
	}
	actors := func(n int) []pool.UnitRef {
		out := make([]pool.UnitRef, n)
		for i := range out {
			out[i] = pool.UnitRef{Handle: pool.Handle(i + 1), Serial: 1}
		}
		return out
	}
	nan := float32(math.NaN())
	cases := []struct {
		name     string
		contexts []CommandContext
		c        SeatCommand
	}{
		{"code 0", nil, order(func(o *OrderPayload) { o.Code = 0 })},
		{"code 15", nil, order(func(o *OrderPayload) { o.Code = 15 })},
		{"interface type 2", nil, order(func(o *OrderPayload) { o.Position.InterfaceType = 2 })},
		{"area interface type 2", nil, order(func(o *OrderPayload) { o.Targets = []CommandTarget{{Position: CommandPosition{InterfaceType: 2}}} })},
		{"unsorted ordinary actors", nil, order(func(o *OrderPayload) { o.Actors = []pool.UnitRef{b, a} })},
		{"half-null target", nil, order(func(o *OrderPayload) { o.Target = pool.UnitRef{Serial: 3} })},
		{"half-null area target", nil, order(func(o *OrderPayload) { o.Targets = []CommandTarget{{Target: pool.UnitRef{Serial: 3}}} })},
		{"null actor", nil, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{{}}}}},
		{"half-null actor", nil, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{{Serial: 4}}}}},
		{"null singular actor", nil, SeatCommand{Kind: SeatActivation}},
		{"3277 actors", nil, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: actors(3277)}}},
		{"assigned with two actors", nil, order(func(o *OrderPayload) { o.Actors = []pool.UnitRef{a, b}; o.AssignedPosition = true })},
		{"assigned with code 3", nil, order(func(o *OrderPayload) { o.Code = 3; o.AssignedPosition = true })},
		{"assigned and tracked", nil, order(func(o *OrderPayload) { o.Queued = true; o.AssignedPosition = true; o.TrackQueuedMove = true })},
		{"tracked and unqueued", nil, order(func(o *OrderPayload) { o.TrackQueuedMove = true })},
		{"tracked with a target", nil, order(func(o *OrderPayload) { o.Queued = true; o.TrackQueuedMove = true; o.Target = b })},
		{"area with outer target", nil, order(func(o *OrderPayload) { o.Targets = make([]CommandTarget, 1); o.Target = b })},
		{"area and tracked", nil, order(func(o *OrderPayload) { o.Targets = make([]CommandTarget, 1); o.Queued = true; o.TrackQueuedMove = true })},
		{"unselected record", nil, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{a}}, Give: GivePayload{Amount: 1}}},
		{"fieldless kind with a record", nil, SeatCommand{Kind: SeatATM, View: ViewPayload{Player: 1}}},
		{"unselected negative zero", nil, SeatCommand{Kind: SeatATM, Give: GivePayload{Amount: math.Float32frombits(0x80000000)}}},
		{"facing 4", nil, SeatCommand{Kind: SeatMobileBuild, MobileBuild: MobileBuildPayload{Builder: a, Product: "scout", Facing: 4}}},
		{"empty product", nil, SeatCommand{Kind: SeatMobileBuild, MobileBuild: MobileBuildPayload{Builder: a}}},
		{"noncanonical key", nil, SeatCommand{Kind: SeatFactoryBuild, FactoryBuild: FactoryBuildPayload{Builder: a, Product: "Scout", Count: 1}}},
		{"spaced key", nil, SeatCommand{Kind: SeatSpawn, Spawn: SpawnPayload{Unit: " scout"}}},
		{"256-byte key", nil, SeatCommand{Kind: SeatSpawn, Spawn: SpawnPayload{Unit: strings.Repeat("k", 256)}}},
		{"key with NUL", nil, SeatCommand{Kind: SeatSpawn, Spawn: SpawnPayload{Unit: "sc\x00out"}}},
		{"count 0", nil, SeatCommand{Kind: SeatFactoryBuild, FactoryBuild: FactoryBuildPayload{Builder: a, Product: "scout"}}},
		{"count 32768 online", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatStockpile, Stockpile: StockpilePayload{Unit: a, Count: 32768}}},
		{"count -32768 online", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatStockpile, Stockpile: StockpilePayload{Unit: a, Count: -32768}}},
		{"count -2^31", nil, SeatCommand{Kind: SeatStockpile, Stockpile: StockpilePayload{Unit: a, Count: math.MinInt32}}},
		{"group 0", nil, SeatCommand{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Members: []pool.UnitRef{a}}}},
		{"group 10", nil, SeatCommand{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Group: 10}}},
		{"stance 3", nil, SeatCommand{Kind: SeatStance, Stance: StancePayload{Value: 3}}},
		{"resource 2", nil, SeatCommand{Kind: SeatGive, Give: GivePayload{Resource: 2, Amount: 1}}},
		{"recipient 10", nil, SeatCommand{Kind: SeatGive, Give: GivePayload{Player: 10, Amount: 1}}},
		{"view 10", nil, SeatCommand{Kind: SeatView, View: ViewPayload{Player: 10}}},
		{"logo player 10", []CommandContext{SinglePlayerReplay}, SeatCommand{Kind: SeatSetLogo, SetLogo: SetLogoPayload{Player: 10}}},
		{"set resource player 10", []CommandContext{SinglePlayerReplay}, SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Player: 10}}},
		{"toggle mask 8", nil, SeatCommand{Kind: SeatVisibility, Visibility: VisibilityPayload{ToggleMask: 8}}},
		{"clear mask 8", nil, SeatCommand{Kind: SeatVisibility, Visibility: VisibilityPayload{ClearMask: 8}}},
		{"meteor enabled without argument", nil, SeatCommand{Kind: SeatMeteor, Meteor: MeteorPayload{Enabled: true}}},
		{"sequence 0", nil, SeatCommand{Kind: SeatCancelQueuedMove, CancelQueuedMove: CancelQueuedMovePayload{Actors: []pool.UnitRef{a}}}},
		{"builder option 3", nil, SeatCommand{Kind: SeatBuilderOptions, BuilderOptions: BuilderOptionsPayload{Patrol: [3]uint8{0, 0, 3}}}},
		{"owner 10", []CommandContext{SinglePlayerReplay}, SeatCommand{Kind: SeatBuilderOptions, BuilderOptions: BuilderOptionsPayload{Owner: 10}}},
		{"drag target", nil, SeatCommand{Kind: SeatCommunityOrderDrag, CommunityOrderDrag: CommunityOrderDragPayload{Unit: a, Target: b}}},
		{"drag facing 4", nil, SeatCommand{Kind: SeatCommunityOrderDrag, CommunityOrderDrag: CommunityOrderDragPayload{Unit: a, BuildFacing: 4}}},
		{"drag noncanonical product", nil, SeatCommand{Kind: SeatCommunityOrderDrag, CommunityOrderDrag: CommunityOrderDragPayload{Unit: a, BuildProduct: "ARMSOLAR"}}},
		{"unregistered rule set", []CommandContext{SinglePlayerReplay}, SeatCommand{Kind: SeatGameplay, Gameplay: GameplayPayload{Mode: "no-such-rules"}}},
		{"empty rule set", []CommandContext{SinglePlayerReplay}, SeatCommand{Kind: SeatGameplay}},
		{"online NaN resource", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Amount: nan}}},
		{"online infinite resource", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Amount: float32(math.Inf(1))}}},
		{"online fractional gift", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatGive, Give: GivePayload{Amount: 0.5}}},
		{"online NaN gift", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatGive, Give: GivePayload{Amount: nan}}},
		{"online infinite gift", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatGive, Give: GivePayload{Amount: float32(math.Inf(-1))}}},
		{"online gift above 2^31", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatGive, Give: GivePayload{Amount: 4294967296}}},
		{"online gift below -2^31", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatGive, Give: GivePayload{Amount: -2147483904}}},
		// Version 2's sharing and alliance payloads, online.
		{"gift target 10", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 10}}},
		{"gift target 255", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 255, Metal: 1}}},
		{"alliance target 10", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatDeclareAlliance, DeclareAlliance: DeclareAlliancePayload{Target: 10, Value: true}}},
		{"gift with 3277 actors", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Actors: actors(3277)}}},
		{"gift null actor", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Actors: []pool.UnitRef{a, {}}}}},
		{"gift half-null actor", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Actors: []pool.UnitRef{{Serial: 4}}}}},
		{"sharing record beside a version-1 kind", nil, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{a}}, ShareGift: ShareGiftPayload{MapInfo: true}}},
		{"sharing record beside a fieldless kind", nil, SeatCommand{Kind: SeatATM, SharedVictory: SharedVictoryPayload{On: true}}},
		{"gift actors beside a version-1 kind", nil, SeatCommand{Kind: SeatCloak, ShareGift: ShareGiftPayload{Actors: []pool.UnitRef{a}}}},
		{"threshold beside a version-1 kind", nil, SeatCommand{Kind: SeatGive, Give: GivePayload{Amount: 1}, SetShareMetal: SetShareMetalPayload{Amount: 5}}},
		{"another sharing record", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatShareMetal, ShareEnergy: ShareEnergyPayload{On: true}}},
		{"alliance beside share all", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatShareAll, DeclareAlliance: DeclareAlliancePayload{Target: 1}}},
		{"version-1 record beside a gift", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatShareGift, Give: GivePayload{Amount: 1}}},
		{"version-1 actors beside a gift", []CommandContext{OnlineCommand}, SeatCommand{Kind: SeatShareGift, Stop: StopPayload{Actors: []pool.UnitRef{a}}}},
	}
	for _, tc := range cases {
		contexts := tc.contexts
		if contexts == nil {
			contexts = []CommandContext{OnlineCommand, SinglePlayerReplay}
		}
		for _, context := range contexts {
			if _, err := EncodeSeatCommand(context, tc.c); err == nil {
				t.Errorf("%s: encoded in context %d", tc.name, context)
			} else if !strings.Contains(err.Error(), "logical path ") || !strings.Contains(err.Error(), fmt.Sprintf("providers searched [command schema v%d ", netproto.CommandSchemaVersion)) {
				t.Errorf("%s: diagnostic shape %q", tc.name, err)
			}
		}
	}

	// The same refusals as bytes: a valid payload with one field spliced.
	for _, tc := range []struct {
		name    string
		context CommandContext
		wire    []byte
	}{
		{"boolean 2", OnlineCommand, cat(6, 3, 4, 2, 0)},
		{"overlong handle", OnlineCommand, cat(9, 0x86, 0x00, 7)},
		{"handle over 16 bits", OnlineCommand, cat(9, 0x80, 0x80, 0x04, 7)},
		{"serial over 64 bits", OnlineCommand, cat(9, 6, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02)},
		{"half-null ref bytes", OnlineCommand, cat(9, 0, 7)},
		{"resource byte 2", OnlineCommand, cat(22, 0, 2, le32(1))},
		{"online negative zero gift", OnlineCommand, cat(22, 0, 0, 0, 0, 0, 0x80)},
		{"online negative zero resource", OnlineCommand, cat(19, 0, 0, 0, 0, 0x80)},
		{"online NaN resource", OnlineCommand, cat(19, 0, 0x01, 0, 0xc0, 0x7f)},
		{"actor count over 3276", SinglePlayerReplay, cat(5, uvarintBytes(3277), bytes.Repeat([]byte{1, 1}, 3277))},
		{"actor count over the bytes", SinglePlayerReplay, cat(5, uvarintBytes(3276), 1, 1)},
		{"area count over the bytes", SinglePlayerReplay, cat(4, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0x03, 0, 0)},
		{"overlong actor count", OnlineCommand, cat(5, 0x81, 0x00, 1, 1)},
		{"unsorted ordinary actors", OnlineCommand, cat(4, 2, 5, 1, 2, 9, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)},
		{"repeated actor", OnlineCommand, cat(5, 2, 1, 1, 1, 1)},
		{"zero serial", SinglePlayerReplay, cat(5, 1, 1, 0)},
		{"key with NUL", OnlineCommand, cat(31, 3, "a\x00b", 0, 0, 0)},
		{"empty key", OnlineCommand, cat(31, 0, 0, 0, 0)},
		{"noncanonical key bytes", OnlineCommand, cat(31, 5, "Scout", 0, 0, 0)},
		{"drag target bytes", OnlineCommand, cat(33, 2, 3, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0)},
		// Version 2's sharing and alliance payloads.
		{"share boolean 2", OnlineCommand, cat(35, 2)},
		{"share radar boolean 0xff", OnlineCommand, cat(38, 0xff)},
		{"share all first boolean 2", OnlineCommand, cat(39, 2, 0, 0, 0)},
		{"share all last boolean 2", OnlineCommand, cat(39, 1, 1, 1, 2)},
		{"share all with three options", OnlineCommand, cat(39, 1, 1, 1)},
		{"threshold over 32 bits", OnlineCommand, cat(40, 0x80, 0x80, 0x80, 0x80, 0x10)},
		{"overlong threshold", OnlineCommand, cat(41, 0x81, 0x00)},
		{"threshold without its amount", OnlineCommand, cat(40)},
		{"gift target 10 bytes", OnlineCommand, cat(42, 10, 0, 0, 0, 0)},
		{"overlong gift metal", OnlineCommand, cat(42, 0, 0x80, 0x00, 0, 0, 0)},
		{"gift energy over 32 bits", OnlineCommand, cat(42, 0, 0, 0xff, 0xff, 0xff, 0xff, 0x1f, 0, 0)},
		{"gift map information 2", OnlineCommand, cat(42, 0, 0, 0, 0, 2)},
		{"gift actor count over 3276", OnlineCommand, cat(42, 0, 0, 0, uvarintBytes(3277), bytes.Repeat([]byte{1, 1}, 3277), 0)},
		{"gift actor count over the bytes", OnlineCommand, cat(42, 0, 0, 0, uvarintBytes(3276), 1, 1, 0)},
		{"gift repeated actor", OnlineCommand, cat(42, 0, 0, 0, 2, 1, 1, 1, 1, 0)},
		{"gift zero serial", OnlineCommand, cat(42, 0, 0, 0, 1, 1, 0, 0)},
		{"gift half-null actor bytes", OnlineCommand, cat(42, 0, 0, 0, 1, 0, 7, 0)},
		{"gift without map information", OnlineCommand, cat(42, 0, 0, 0, 0)},
		{"alliance target 10 bytes", OnlineCommand, cat(43, 10, 1)},
		{"alliance value 2", OnlineCommand, cat(43, 1, 2)},
		{"victory boolean 2", OnlineCommand, cat(44, 2)},
		{"victory with a trailing byte", OnlineCommand, cat(44, 1, 0)},
		{"share metal bytes in replay", SinglePlayerReplay, cat(35, 1)},
		{"gift bytes in replay", SinglePlayerReplay, cat(42, 0, 0, 0, 0, 0)},
	} {
		if checkSeatDecode(t, seatSchemaV2, tc.context, tc.wire) {
			t.Errorf("%s: decoded", tc.name)
		}
		checkSeatDecodeEverywhere(t, tc.wire)
	}
	if checkSeatDecode(t, seatSchemaV2, SinglePlayerReplay, make([]byte, netproto.MaxCommandBytes+1)) {
		t.Fatal("a payload over 4 MiB decoded")
	}
	if _, err := DecodeSeatCommand(OnlineCommand, append([]byte{5, 0}, make([]byte, netproto.MaxCommandBytes)...)); err == nil || !strings.Contains(err.Error(), "at most 4194304 bytes") {
		t.Fatalf("the size check did not come first: %v", err)
	}
}

// Online, the encoder spells a negative-zero amount as positive zero and the
// decoder refuses negative zero; the replay context keeps it (§7.4.1).
func TestSeatCommandNegativeZeroAmounts(t *testing.T) {
	negZero := math.Float32frombits(0x80000000)
	for _, c := range []SeatCommand{
		{Kind: SeatSetResource, SetResource: SetResourcePayload{Amount: negZero}},
		{Kind: SeatGive, Give: GivePayload{Amount: negZero}},
	} {
		online, err := EncodeSeatCommand(OnlineCommand, c)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(online[len(online)-4:], []byte{0, 0, 0, 0}) {
			t.Fatalf("kind %d: online negative zero written as %x", c.Kind, online)
		}
		replay, err := EncodeSeatCommand(SinglePlayerReplay, c)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(replay[len(replay)-4:], []byte{0, 0, 0, 0x80}) {
			t.Fatalf("kind %d: replay negative zero written as %x", c.Kind, replay)
		}
	}
}

// The references U2 left open: a handle captured from a freed slot carries
// serial 0, and a local list can repeat an actor. Version 1 has no form for
// either, so both contexts refuse them with ErrSeatCommandNoWireForm rather
// than write another command; a merely unsorted ordinary order is an
// ordinary refusal (§7.4.1, §7.4.3; design readings in seatWireReference).
func TestSeatCommandLocalReferencesWithoutAWireForm(t *testing.T) {
	f := newSeatFixture(t, false, false)
	freed := f.own1a
	f.s.Units.FreeImmediate(freed)
	captured := f.s.captureRef(freed)
	if captured.Handle != freed || captured.Serial != 0 {
		t.Fatalf("the local adapter captured %+v from a freed slot, want serial 0", captured)
	}
	live := f.ref(f.own0)
	for _, tc := range []struct {
		name string
		c    SeatCommand
	}{
		{"freed actor", SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{live, captured}}}},
		{"freed singular actor", SeatCommand{Kind: SeatActivation, Activation: ActivationPayload{Unit: captured}}},
		{"freed ordinary target", SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{live}, Code: 2, Target: captured}}},
		{"freed area target", SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{live}, Code: 12, Targets: []CommandTarget{{Target: captured}}}}},
		{"repeated actor", SeatCommand{Kind: SeatSelfDestruct, SelfDestruct: SelfDestructPayload{Actors: []pool.UnitRef{live, live}}}},
		{"repeated area actor", SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{live, live}, Code: 12, Targets: make([]CommandTarget, 1)}}},
	} {
		for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
			if _, err := EncodeSeatCommand(context, tc.c); !errors.Is(err, ErrSeatCommandNoWireForm) {
				t.Errorf("%s in context %d: %v, want ErrSeatCommandNoWireForm", tc.name, context, err)
			}
		}
	}
	// A gift's actors are an `actors` list too, online only.
	for _, c := range []SeatCommand{
		{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 2, Actors: []pool.UnitRef{live, captured}}},
		{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 2, Actors: []pool.UnitRef{live, live}}},
	} {
		if _, err := EncodeSeatCommand(OnlineCommand, c); !errors.Is(err, ErrSeatCommandNoWireForm) {
			t.Errorf("gift actors %+v: %v, want ErrSeatCommandNoWireForm", c.ShareGift.Actors, err)
		}
	}
	// A gift keeps its actors' captured order.
	captureOrder := SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 2, Actors: []pool.UnitRef{f.ref(f.comp2), live}}}
	if payload, err := EncodeSeatCommand(OnlineCommand, captureOrder); err != nil {
		t.Fatalf("a gift's captured actor order: %v", err)
	} else if back, err := DecodeSeatCommand(OnlineCommand, payload); err != nil || !reflect.DeepEqual(back, captureOrder) {
		t.Fatalf("a gift's captured actor order decoded as %+v, %v", back, err)
	}
	unsorted := SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{f.ref(f.comp2), live}, Code: 2}}
	if _, err := EncodeSeatCommand(SinglePlayerReplay, unsorted); err == nil || errors.Is(err, ErrSeatCommandNoWireForm) {
		t.Fatalf("an unsorted ordinary order: %v", err)
	}
	// An area order keeps its actors' captured order.
	unsorted.Order.Code, unsorted.Order.Targets = 12, make([]CommandTarget, 1)
	if _, err := EncodeSeatCommand(SinglePlayerReplay, unsorted); err != nil {
		t.Fatalf("an area order's captured actor order: %v", err)
	}
}

// seatState is everything a refused entry must leave untouched: both random
// streams, every player record (stocks, production slots, alliances), every
// unit's queue and group, the live unit count and the local slots.
type seatState struct {
	streams        streamPositions
	players        [10]economy.Player
	queues, groups [4]int
	used           int
	noShake        bool
	gameplay       gameplay.Mode
	viewing, local uint8
}

func (f *seatFixture) state() seatState {
	st := seatState{streams: f.streams(), used: f.s.Units.Used(), noShake: f.s.NoShake(), gameplay: f.s.Gameplay,
		viewing: f.s.ViewingOwner, local: f.s.LocalOwner}
	copy(st.players[:], f.s.Econ.Players[:])
	for i, h := range []pool.Handle{f.own0, f.own1a, f.own1b, f.comp2} {
		st.queues[i] = f.queueLen(h)
		if u := f.s.Units.Unit(h); u != nil {
			st.groups[i] = int(u.Group)
		}
	}
	return st
}

// deliver is the stream driver's path for one entry's bytes: decode them in
// the session's context and enqueue them stamped only if they decode; phase
// 1 then runs. A codec refusal enqueues nothing.
func (f *seatFixture) deliver(t *testing.T, context CommandContext, seat uint8, payload []byte) (CommandReceipt, error) {
	t.Helper()
	c, err := DecodeSeatCommand(context, payload)
	if err != nil {
		if rs := f.tick(); len(rs) != 0 {
			t.Fatalf("a refused payload produced receipts %+v", rs)
		}
		return CommandReceipt{}, err
	}
	return f.issue(t, seat, c), nil
}

func giveBytes(amount []byte) []byte { return cat(22, 0, 0, amount) }

// refBytes is a `ref`'s bytes, written independently of the codec.
func refBytes(r pool.UnitRef) []byte {
	return append(uvarintBytes(uint64(r.Handle)), uvarintBytes(r.Serial)...)
}

// Online Give through decode and enqueue (M2-C4, §7.1, §7.4.1, §15 Q8, Q28):
// without the room's cheat permission, a negative, zero, non-whole,
// non-finite or out-of-range amount is refused — the ones no producer
// generates by the codec, the others by the receiver — before any stock or
// production slot changes; a positive whole gift to a seat that is not an
// ally applies the sharing transfer. With the permission a negative gift
// applies the signed transfer.
func TestOnlineGiveThroughTheCodec(t *testing.T) {
	f := newSeatFixture(t, true, false)
	if f.s.Econ.Players[1].Allies[0] || f.s.Econ.Players[0].Allies[1] {
		t.Fatal("the fixture's seats 0 and 1 are allied")
	}
	for _, tc := range []struct {
		name    string
		amount  []byte
		decodes bool
	}{
		{"negative", le32(-20), true},
		{"zero", le32(0), true},
		{"most negative", le32(-2147483648), true},
		{"negative zero", []byte{0, 0, 0, 0x80}, false},
		{"half", le32(0.5), false},
		{"fraction", le32(20.25), false},
		{"NaN", []byte{0, 0, 0xc0, 0x7f}, false},
		{"infinity", le32(float32(math.Inf(1))), false},
		{"negative infinity", le32(float32(math.Inf(-1))), false},
		{"2^32", le32(4294967296), false},
	} {
		before := f.state()
		r, err := f.deliver(t, OnlineCommand, 1, giveBytes(tc.amount))
		if (err == nil) != tc.decodes {
			t.Fatalf("%s: decode error %v, want decodes=%v", tc.name, err, tc.decodes)
		}
		if err == nil {
			expectOutcome(t, r, CommandRejected)
		}
		if f.state() != before {
			t.Fatalf("%s: a refused gift changed the battle", tc.name)
		}
	}
	r, err := f.deliver(t, OnlineCommand, 1, giveBytes(le32(20)))
	if err != nil {
		t.Fatal(err)
	}
	expectOutcome(t, r, CommandApplied)
	if f.s.Econ.Players[1].Stock[economy.Metal] != 980 || f.s.Econ.Players[0].Mirror[economy.Metal].Production != 20 {
		t.Fatal("a positive whole gift to a non-ally did not apply the sharing transfer")
	}
	if r, err := f.deliver(t, OnlineCommand, 1, giveBytes(le32(2147483648))); err != nil || r.Outcome != CommandApplied {
		t.Fatalf("the largest producible gift: %+v, %v", r, err)
	}

	f = newSeatFixture(t, true, true)
	r, err = f.deliver(t, OnlineCommand, 1, giveBytes(le32(-20)))
	if err != nil {
		t.Fatal(err)
	}
	expectOutcome(t, r, CommandApplied)
	if f.s.Econ.Players[1].Stock[economy.Metal] != 1020 || f.s.Econ.Players[0].Mirror[economy.Metal].Production != -20 {
		t.Fatal("a permitted negative gift did not apply the signed transfer")
	}
	for _, amount := range [][]byte{le32(0), le32(-2147483648)} {
		if r, err := f.deliver(t, OnlineCommand, 1, giveBytes(amount)); err != nil || r.Outcome != CommandApplied {
			t.Fatalf("a permitted gift of %x: %+v, %v", amount, r, err)
		}
	}
	for _, amount := range [][]byte{le32(0.5), {0, 0, 0xc0, 0x7f}, le32(4294967296), {0, 0, 0, 0x80}} {
		before := f.state()
		if _, err := f.deliver(t, OnlineCommand, 1, giveBytes(amount)); err == nil {
			t.Fatalf("an unproducible gift of %x decoded with the permission", amount)
		}
		if f.state() != before {
			t.Fatal("a refused gift changed the battle")
		}
	}
}

// areaBytes is an online area order of n stale synthetic actors and m
// targetless feature entries.
func areaBytes(t *testing.T, actors, entries int) []byte {
	t.Helper()
	c := SeatCommand{Kind: SeatOrder, Order: OrderPayload{Code: 12, Targets: make([]CommandTarget, entries)}}
	for i := 0; i < actors; i++ {
		c.Order.Actors = append(c.Order.Actors, pool.UnitRef{Handle: pool.Handle(2000 + i), Serial: 1})
	}
	for i := range c.Order.Targets {
		c.Order.Targets[i].Position = CommandPosition{X: numeric.Fixed(i) << 16, HasFeature: true}
	}
	payload, err := EncodeSeatCommand(OnlineCommand, c)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// The online per-command work limits (§7.4.1, §15 Q28) through decode and
// enqueue: the codec admits the representation ceilings, and the receiver
// refuses an area list over 10,000 entries or over 2^20 actor-entry visits
// whole, before any queue, resource or random stream changes. The limits
// themselves are admitted.
func TestOnlineWorkLimitsThroughTheCodec(t *testing.T) {
	f := newSeatFixture(t, true, false)
	for _, tc := range []struct {
		name            string
		actors, entries int
		want            CommandOutcome
	}{
		{"10,001 entries", 1, 10001, CommandRejected},
		{"163 x 6,433 = 2^20 + 3 visits", 163, 6433, CommandRejected},
		{"128 x 8,192 = 2^20 visits", 128, 8192, CommandNoOp},
		{"10,000 entries", 104, 10000, CommandNoOp},
	} {
		before := f.state()
		r, err := f.deliver(t, OnlineCommand, 1, areaBytes(t, tc.actors, tc.entries))
		if err != nil {
			t.Fatalf("%s: the codec refused a representable area order: %v", tc.name, err)
		}
		expectOutcome(t, r, tc.want)
		if f.state() != before {
			t.Fatalf("%s: changed the battle", tc.name)
		}
	}
	// A live actor with the largest admitted list applies.
	c := SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{f.ref(f.own1a)}, Code: 12, Targets: make([]CommandTarget, 10000)}}
	payload, err := EncodeSeatCommand(OnlineCommand, c)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := f.deliver(t, OnlineCommand, 1, payload); err != nil || r.Outcome != CommandApplied {
		t.Fatalf("10,000 entries for a live actor: %+v, %v", r, err)
	}
}

// Malformed bytes through the public path — decode in the session's
// context, enqueue only what decodes — leave queues, resources, both random
// streams and the session's settings exactly where they were (M2-C4).
func TestRefusedPayloadsLeaveTheBattleUnchanged(t *testing.T) {
	f := newSeatFixture(t, true, true)
	own := f.ref(f.own1a)
	stop, err := EncodeSeatCommand(OnlineCommand, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{own}}})
	if err != nil {
		t.Fatal(err)
	}
	hostile := [][]byte{
		stop[:len(stop)-1],
		append(append([]byte(nil), stop...), 0),
		cat(17),                    // NoShake online
		cat(255, 10, "strict-3.1"), // Gameplay online
		cat(20, 1, 3),              // SetLogo online
		cat(19, 1, 0, le32(5)),     // replay SetResource bytes online
		cat(32, 1, 0, 0, 0, 0, 0, 0),
		cat(1), cat(28), cat(35), cat(45), cat(200),
		cat(5, 2, refBytes(own), refBytes(own)),
		cat(14, 1, refBytes(own), 2, 0),
		cat(35, 2), cat(39, 1, 1, 1), cat(43, 10, 1), cat(44, 1, 0),
		cat(42, 1, 0, 0, 2, refBytes(own), refBytes(own), 0),
	}
	for _, payload := range hostile {
		before := f.state()
		if _, err := f.deliver(t, OnlineCommand, 1, payload); err == nil {
			t.Fatalf("%x decoded online", payload)
		}
		if f.state() != before {
			t.Fatalf("refused bytes %x changed the battle", payload)
		}
	}
	if r, err := f.deliver(t, OnlineCommand, 1, stop); err != nil || r.Outcome != CommandApplied || f.queueLen(f.own1a) == 0 {
		t.Fatalf("the valid stop after them: %+v, %v", r, err)
	}
}

// Version 2's sharing and alliance payloads decode online and reach their
// receiver, which admits or refuses each on the battle's state
// (online_sharing.go); the single-player replay context has neither their
// bytes nor their application, because retail runs them only in a networked
// session (§7.4.2) [07 R-CAM-01 §6].
func TestSharingKindsDecodeOnlineOnly(t *testing.T) {
	f := newSeatFixture(t, true, true)
	single := newSeatFixture(t, false, false)
	seen := map[SeatCommandKind]bool{}
	for _, c := range seatWireLayouts() {
		if !slices.Contains(seatSharingKinds, c.command.Kind) {
			continue
		}
		seen[c.command.Kind] = true
		r, err := f.deliver(t, OnlineCommand, 1, c.wire)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if strings.Contains(r.Diagnostic, "M5") || strings.Contains(r.Diagnostic, "version-1") {
			t.Fatalf("%s: still refused as a reserved kind: %q", c.name, r.Diagnostic)
		}
		before := single.state()
		if _, err := single.deliver(t, SinglePlayerReplay, 0, c.wire); err == nil {
			t.Fatalf("%s: decoded in the single-player replay context", c.name)
		}
		expectOutcome(t, single.issue(t, 0, c.command), CommandRejected)
		if single.state() != before {
			t.Fatalf("%s: a sharing command changed a single-player battle", c.name)
		}
	}
	if len(seen) != len(seatSharingKinds) {
		t.Fatalf("exercised %d of the %d sharing kinds", len(seen), len(seatSharingKinds))
	}
}

// M2-C11: single-player replay NoShake and Gameplay keep their effects
// through the codec. The same bytes are refused by the online codec, and a
// command decoded in the replay context but delivered to an online session
// is still refused there, because the session's admitted kind — never the
// decoder's context or the payload — chooses the context.
func TestReplayOnlyKindsKeepTheirEffects(t *testing.T) {
	noShake := cat(17)
	rules := cat(255, 10, "strict-3.1")
	f := newSeatFixture(t, false, false)
	r, err := f.deliver(t, SinglePlayerReplay, 0, noShake)
	if err != nil {
		t.Fatal(err)
	}
	expectOutcome(t, r, CommandApplied)
	if !f.s.NoShake() {
		t.Fatal("replay NoShake did not toggle the shake driver")
	}
	r, err = f.deliver(t, SinglePlayerReplay, 0, rules)
	if err != nil {
		t.Fatal(err)
	}
	expectOutcome(t, r, CommandApplied)
	if f.s.Gameplay != gameplay.Strict31 {
		t.Fatalf("replay Gameplay left the rule set %q", f.s.Gameplay)
	}

	online := newSeatFixture(t, true, true)
	for _, payload := range [][]byte{noShake, rules} {
		if _, err := DecodeSeatCommand(OnlineCommand, payload); err == nil {
			t.Fatalf("%x decoded online", payload)
		}
		c, err := DecodeSeatCommand(SinglePlayerReplay, payload)
		if err != nil {
			t.Fatal(err)
		}
		before := online.state()
		expectOutcome(t, online.issue(t, 1, c), CommandRejected)
		if online.state() != before {
			t.Fatalf("a replay-only kind %x changed an online battle", payload)
		}
	}
}

// M2-C5, the command-size proof of §7.4.1, measured with the encoder at the
// representation ceilings: references at their widest (a handle of three
// bytes, a serial of ten), coordinates at the signed 64-bit extremes, 3276
// actors, 65535 area entries.
//
// §7.4.1 records 2,991,718 bytes at the ceilings and 451,405 under the online
// work limits. Both sums count a 13-byte outer Target beside the area list,
// but §7.4.2 requires that Target to be null — the two bytes of a null ref —
// so the largest Order the schema admits is 11 bytes smaller in each:
// 2,991,707 and 451,394 (and 57,031 for 3,276 actors beside 320 entries,
// recorded as 57,042). The ordinary order's 42,641, whose Target may be
// full, is exact. The design's figures remain true upper bounds and its
// conclusion holds: one maximum selection and the largest admissible area
// list fit in one atomic command, far inside 4 MiB.
func TestSeatCommandSizeProof(t *testing.T) {
	widest := pool.UnitRef{Handle: math.MaxUint16, Serial: math.MaxUint64}
	far := CommandPosition{X: math.MinInt64, Y: math.MinInt64, Z: math.MaxInt64, InterfaceType: 1, HasFeature: true}
	var w netproto.Writer
	writeWireRef(&w, widest)
	refBytes := w.Len()
	writeWirePosition(&w, far)
	positionBytes := w.Len() - refBytes
	if refBytes != 13 || positionBytes != 32 {
		t.Fatalf("widest ref %d bytes and position %d, want the proof's 13 and 32", refBytes, positionBytes)
	}
	// actors are n distinct handles of three bytes each, descending, with
	// ten-byte serials; ascending gives the ordinary order's set order.
	actors := func(n int, ascending bool) []pool.UnitRef {
		out := make([]pool.UnitRef, n)
		for i := range out {
			out[i] = pool.UnitRef{Handle: pool.Handle(math.MaxUint16 - i), Serial: math.MaxUint64 - uint64(i)}
		}
		if ascending {
			for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
				out[i], out[j] = out[j], out[i]
			}
		}
		return out
	}
	entries := func(n int) []CommandTarget {
		out := make([]CommandTarget, n)
		for i := range out {
			out[i] = CommandTarget{Target: widest, Position: far}
		}
		return out
	}
	area := func(a, e int) SeatCommand {
		return SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: actors(a, false), Code: 14, Position: far, Queued: true, Targets: entries(e)}}
	}
	encode := func(context CommandContext, c SeatCommand) []byte {
		t.Helper()
		payload, err := EncodeSeatCommand(context, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(payload) > netproto.MaxCommandBytes {
			t.Fatalf("a %d-byte command is over the 4 MiB ceiling", len(payload))
		}
		return payload
	}
	// The design's sums, term by term as §7.4.1 writes them.
	designCeiling := 1 + 2 + 3276*13 + 1 + 13 + 32 + 3 + 3 + 65535*45
	designOnline := 1 + 1 + 104*13 + 1 + 13 + 32 + 3 + 2 + 10000*45
	designBeside := 1 + 2 + 3276*13 + 1 + 13 + 32 + 3 + 2 + 320*45
	designOrdinary := 1 + 2 + 3276*13 + 1 + 13 + 32 + 3 + 1
	if designCeiling != 2991718 || designOnline != 451405 || designBeside != 57042 || designOrdinary != 42641 {
		t.Fatalf("the design's sums are %d, %d, %d and %d", designCeiling, designOnline, designBeside, designOrdinary)
	}
	const fullMinusNullTarget = 13 - 2

	// At the representation ceilings, in the replay context that keeps them.
	ceiling := encode(SinglePlayerReplay, area(3276, 65535))
	if len(ceiling) != designCeiling-fullMinusNullTarget || len(ceiling) != 2991707 {
		t.Fatalf("largest Order at the ceilings is %d bytes, want 2,991,707", len(ceiling))
	}
	full := area(3276, 65535)
	full.Order.Target = widest
	if _, err := EncodeSeatCommand(SinglePlayerReplay, full); err == nil {
		t.Fatal("an area order with a full outer target encoded: the design's bound would be reachable")
	}
	// It is one command: every actor and entry in captured order, never split.
	back, err := DecodeSeatCommand(SinglePlayerReplay, ceiling)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Order.Actors) != 3276 || len(back.Order.Targets) != 65535 || back.Order.Actors[0].Handle != math.MaxUint16 ||
		back.Order.Actors[3275].Handle != math.MaxUint16-3275 || back.Order.Targets[65534] != (CommandTarget{Target: widest, Position: far}) {
		t.Fatal("the largest Order did not decode whole and in captured order")
	}

	// Under the online work limits: 10,000 entries beside 104 actors, the
	// largest product within 2^20. Every other actor count gives a smaller
	// command at its largest admissible list, by the encoder's own lengths.
	online := encode(OnlineCommand, area(104, 10000))
	if len(online) != designOnline-fullMinusNullTarget || len(online) != 451394 {
		t.Fatalf("largest admissible online Order is %d bytes, want 451,394", len(online))
	}
	if 104*10000 > seatOnlineMaxAreaWork || 105*10000 <= seatOnlineMaxAreaWork {
		t.Fatal("104 actors are not the most beside 10,000 entries")
	}
	for a := 1; a <= seatMaxActors; a++ {
		e := min(seatOnlineMaxAreaEntries, seatOnlineMaxAreaWork/a)
		size := 1 + netproto.UvarintLen(uint64(a)) + 13*a + 1 + 2 + 32 + 3 + netproto.UvarintLen(uint64(e)) + 45*e
		if size > len(online) {
			t.Fatalf("%d actors beside %d entries need %d bytes, more than the proof's maximum", a, e, size)
		}
	}
	if beside := encode(OnlineCommand, area(3276, 320)); len(beside) != designBeside-fullMinusNullTarget || len(beside) != 57031 {
		t.Fatalf("3,276 actors beside 320 entries take %d bytes, want 57,031", len(beside))
	}

	// The largest ordinary order, whose target may be full, is the design's
	// 42,641 in both contexts, and the advertised maximum selection fits.
	ordinary := SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: actors(3276, true), Code: 14, Target: widest, Position: far, Queued: true}}
	for _, context := range []CommandContext{OnlineCommand, SinglePlayerReplay} {
		if got := len(encode(context, ordinary)); got != designOrdinary {
			t.Fatalf("largest ordinary order is %d bytes, want 42,641", got)
		}
	}

	// Every other kind at its own widest is smaller still, the 255-byte
	// queue-receipt product included.
	key := strings.Repeat("k", 255)
	point := CommandPoint{X: math.MinInt64, Y: math.MinInt64, Z: math.MinInt64}
	list := actors(3276, false)
	for _, c := range []SeatCommand{
		{Kind: SeatStop, Stop: StopPayload{Actors: list}},
		{Kind: SeatActivation, Activation: ActivationPayload{Unit: widest, Activate: true, Queued: true}},
		{Kind: SeatMobileBuild, MobileBuild: MobileBuildPayload{Builder: widest, Product: key, Position: point, Facing: 3, Queued: true, AppendOnly: true}},
		{Kind: SeatFactoryBuild, FactoryBuild: FactoryBuildPayload{Builder: widest, Product: key, Count: math.MaxInt32}},
		{Kind: SeatCancelProduction, CancelProduction: CancelProductionPayload{Unit: widest}},
		{Kind: SeatStockpile, Stockpile: StockpilePayload{Unit: widest, Count: math.MaxInt32}},
		{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Group: 9, Members: list}},
		{Kind: SeatStance, Stance: StancePayload{Actors: list, Fire: true, Value: 2}},
		{Kind: SeatCloak, Cloak: CloakPayload{Actors: list, Cloak: true}},
		{Kind: SeatSelfDestruct, SelfDestruct: SelfDestructPayload{Actors: list, Queued: true}},
		{Kind: SeatSetResource, SetResource: SetResourcePayload{Player: 9, Resource: economy.Energy, Amount: -1}},
		{Kind: SeatGive, Give: GivePayload{Player: 9, Resource: economy.Energy, Amount: -1}},
		{Kind: SeatCancelQueuedMove, CancelQueuedMove: CancelQueuedMovePayload{Sequence: math.MaxUint64, Actors: list}},
		{Kind: SeatSpawn, Spawn: SpawnPayload{Unit: key, Position: point}},
		{Kind: SeatBuilderOptions, BuilderOptions: BuilderOptionsPayload{Owner: 9, Guard: [3]uint8{2, 2, 2}, Patrol: [3]uint8{2, 2, 2}}},
		{Kind: SeatCommunityOrderDrag, CommunityOrderDrag: CommunityOrderDragPayload{Unit: widest, Index: math.MaxUint16, DescriptorID: math.MinInt32,
			CreationTick: math.MaxUint32, Goal: point, BuildProduct: key, BuildFacing: 3, Destination: point}},
		{Kind: SeatCommunityKickout, CommunityKickout: CommunityKickoutPayload{Unit: widest, Destination: point}},
		{Kind: SeatGameplay, Gameplay: GameplayPayload{Mode: gameplay.Community39}},
	} {
		if got := len(encode(SinglePlayerReplay, c)); got >= designOrdinary {
			t.Fatalf("kind %d takes %d bytes, not less than the largest ordinary order", c.Kind, got)
		}
	}
	// Version 2's widest payload, a gift of the maximum selection with both
	// amounts at their widest, is online-only and smaller still.
	gift := SeatCommand{Kind: SeatShareGift, ShareGift: ShareGiftPayload{Target: 9, Metal: math.MinInt32, Energy: math.MinInt32, Actors: list, MapInfo: true}}
	if got := len(encode(OnlineCommand, gift)); got != 1+1+5+5+2+3276*13+1 || got >= designOrdinary {
		t.Fatalf("the widest gift takes %d bytes", got)
	}
	drag := SeatCommand{Kind: SeatCommunityOrderDrag, CommunityOrderDrag: CommunityOrderDragPayload{Unit: widest, Index: math.MaxUint16,
		DescriptorID: math.MinInt32, CreationTick: math.MaxUint32, Goal: point, BuildProduct: key, BuildFacing: 3, Destination: point}}
	if got := len(encode(OnlineCommand, drag)); got != 1+13+3+5+5+2+30+2+255+1+30 {
		t.Fatalf("the widest Community drag takes %d bytes", got)
	}
}
