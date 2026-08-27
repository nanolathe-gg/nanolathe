package presentation

import "github.com/nanolathe/nanolathe/internal/sim/numeric"

// Strip identifies the established effect-strip destination.  Values are
// deliberately named rather than inferred from a visual family at draw time;
// admission order remains the ordering authority [03 §1].
type Strip uint8

const (
	StripUnknown   Strip = 0xff
	StripShockwave Strip = 2
	StripCrater    Strip = 4
	StripBeam      Strip = 6
	StripLightning Strip = 7
	StripSmoke     Strip = 9
)

// StripProducer is an explicit producer identity.  Event kind is deliberately
// not a routing key: generic impact, explosion, corpse, and COB SFX events do
// not prove which painter owns them [03 §1].
type StripProducer uint8

const (
	ProducerUnknown StripProducer = iota
	ProducerShockwave
	ProducerCrater
	ProducerBeam
	ProducerLightning
	ProducerSmoke
)

// RouteForProducer maps only the producer families established by the strip
// census.  A caller without one of these identities must leave its event
// unresolved rather than guessing from a generic event kind [03 §1][I9].
func RouteForProducer(producer StripProducer) Strip {
	switch producer {
	case ProducerShockwave:
		return StripShockwave
	case ProducerCrater:
		return StripCrater
	case ProducerBeam:
		return StripBeam
	case ProducerLightning:
		return StripLightning
	case ProducerSmoke:
		return StripSmoke
	default:
		return StripUnknown
	}
}

// RouteForKind returns the established strip owner for an event family.  The
// early strip producers have no bounded census, so they remain explicitly
// unresolved rather than acquiring a guessed route [03 §1][I9].
func RouteForKind(kind Kind) Strip {
	_ = kind
	return StripUnknown
}

// RouteEvent applies the established route only when the producer did not
// supply one. An explicit route is retained, including an unresolved route,
// because producer identity is part of the immutable event contract.
func RouteEvent(e *Event) {
	if e == nil || (e.Strip != -1 && e.Strip != 0) {
		return
	}
	r := RouteForProducer(e.Producer)
	if r != StripUnknown {
		e.Strip = int8(r)
	} else {
		e.Strip = -1
	}
}

// RoutedEvent returns a detached copy with the established strip route. It
// does not admit the event or mutate the caller's value.
func RoutedEvent(e Event) Event {
	e.DurationsA = append([]int32(nil), e.DurationsA...)
	e.DurationsB = append([]int32(nil), e.DurationsB...)
	RouteEvent(&e)
	return e
}

// NanolatheMode identifies the two cadence families with established output.
// Other construction modes are left unresolved and emit no guessed segments
// [03 §5.5][I9].
type NanolatheMode uint8

const (
	NanolatheBuild   NanolatheMode = 1
	NanolatheReclaim NanolatheMode = 2
	NanolatheCapture NanolatheMode = 3
)

// NanolatheSegment is one immutable line request. The endpoints are copied
// from the authoritative builder/target event; the renderer owns projection.
type NanolatheSegment struct {
	Index               int32
	FromX, FromY, FromZ numeric.Fixed
	ToX, ToY, ToZ       numeric.Fixed
	Color               uint8
}

// NanolatheSegmentCount returns the established per-tick emission count.
// Reclaim/capture emits one segment on alternating ticks; build assist emits
// two each tick [03 §5.5].
func NanolatheSegmentCount(mode NanolatheMode, tick uint32) int {
	switch mode {
	case NanolatheBuild:
		return 2
	case NanolatheReclaim, NanolatheCapture:
		if tick&1 == 0 {
			return 1
		}
	}
	return 0
}

// BuildNanolatheSegments creates line requests from an admitted event. It
// does not invent offsets for the second build-assist segment: both requests
// retain the exact builder/target geometry until an authored offset is
// established [03 §5.5].
func BuildNanolatheSegments(e Event, tick uint32) []NanolatheSegment {
	n := NanolatheSegmentCount(NanolatheMode(e.Mode), tick)
	if n == 0 {
		return nil
	}
	out := make([]NanolatheSegment, n)
	for i := range out {
		out[i] = NanolatheSegment{
			Index: int32(i),
			FromX: e.X, FromY: e.Y, FromZ: e.Z,
			ToX: e.TargetX, ToY: e.TargetY, ToZ: e.TargetZ,
			Color: 6, // fixed palette index 6 [03 §5.5]
		}
	}
	// TODO(question): exact per-segment target-footprint offsets are not
	// established by [03 §5.5]. Preserve the producer's builder/target request,
	// cadence/count/color metadata, but the client must not rasterize this shared
	// baseline as duplicate retail geometry until those offsets are traced.
	return out
}

// EmitNanolatheSegments admits the established segment requests in order.
// The collector remains the sole event-ID/sequence owner; missing artwork is
// handled later by the compatibility resolver as a no-op [I6][I9].
func (c *Collector) EmitNanolatheSegments(e Event, tick uint32) int {
	if c == nil {
		return 0
	}
	segments := BuildNanolatheSegments(e, tick)
	admitted := 0
	for _, segment := range segments {
		segmentEvent := e
		segmentEvent.Tick = tick
		segmentEvent.NanolatheIndex = segment.Index
		segmentEvent.NanolatheCount = int32(len(segments))
		segmentEvent.PaletteRow = int16(segment.Color)
		segmentEvent.NanolatheGeometryKnown = e.NanolatheGeometryKnown
		segmentEvent.X, segmentEvent.Y, segmentEvent.Z = segment.FromX, segment.FromY, segment.FromZ
		segmentEvent.TargetX, segmentEvent.TargetY, segmentEvent.TargetZ = segment.ToX, segment.ToY, segment.ToZ
		if c.EmitNanolathe(segmentEvent) {
			admitted++
		}
	}
	return admitted
}
