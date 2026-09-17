package frame

import "github.com/nanolathe-gg/nanolathe/internal/sim/numeric"

// Strip identifies the established effect-strip destination. Event kind is
// not a routing key: producer identity is required by the retail strip
// dispatch [03 §1].
type Strip uint8

const (
	StripUnknown   Strip = 0xff
	StripShockwave Strip = 2
	StripCrater    Strip = 4
	StripBeam      Strip = 6
	StripLightning Strip = 7
	StripSmoke     Strip = 9
)

// StripProducer names the effect family that submitted an event. It is what
// RouteForProducer maps to a strip [03 R-STRIP-01].
type StripProducer uint8

const (
	ProducerUnknown StripProducer = iota
	ProducerShockwave
	ProducerCrater
	ProducerBeam
	ProducerLightning
	ProducerSmoke
)

// RouteForProducer maps only established producer families. Unknown producers
// remain unresolved rather than acquiring a guessed route [03 §1][I9].
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

// RouteEvent applies the explicit producer route and preserves an existing
// route, including the unresolved sentinel.
func RouteEvent(e *Event) {
	if e == nil || (e.Strip != -1 && e.Strip != 0) {
		return
	}
	route := RouteForProducer(e.Producer)
	if route == StripUnknown {
		e.Strip = -1
		return
	}
	e.Strip = int8(route)
}

// RoutedEvent returns a copy of e with its duration slices detached and its
// strip assigned by RouteEvent.
func RoutedEvent(e Event) Event {
	e.DurationsA = append([]int32(nil), e.DurationsA...)
	e.DurationsB = append([]int32(nil), e.DurationsB...)
	RouteEvent(&e)
	return e
}

// NanolatheMode is which nanolathe beam a builder is drawing [03 R-STRIP-01].
// The producer census of [05 R-WORK-01 §8] separates feature reclaim from the
// rest: it is the only producer that submits two segments on one accepted work
// visit, so it is its own mode rather than a label borrowed from build.
type NanolatheMode uint8

const (
	// NanolatheBuild covers every one-segment builder→target producer: mobile
	// and building/factory build, build assist, the VTOL twins, all four
	// repair executors and the resurrection wait [05 R-WORK-01 §8].
	NanolatheBuild NanolatheMode = 1
	// NanolatheReclaim is UNIT reclaim — one segment, target box → builder
	// nano piece [05 R-WORK-01 §8].
	NanolatheReclaim NanolatheMode = 2
	// NanolatheCapture is capture — one segment, target box → builder nano
	// piece [05 R-WORK-01 §8].
	NanolatheCapture NanolatheMode = 3
	// NanolatheFeatureReclaim is feature reclaim, the engine's only
	// two-segment producer, and only while the order node's countdown exceeds
	// its threshold [05 R-WORK-01 §8][05 R-WORK-01 §5].
	NanolatheFeatureReclaim NanolatheMode = 4
)

// NanolatheSegment is one drawn beam segment: its index in the beam, its two
// world endpoints and its palette colour.
type NanolatheSegment struct {
	Index               int32
	FromX, FromY, FromZ numeric.Fixed
	ToX, ToY, ToZ       numeric.Fixed
	Color               uint8
}

// NanolatheSegmentCount is how many segments a mode submits on one ACCEPTED
// work visit: every ordinary producer submits one, and feature reclaim — the
// only two-segment producer in the engine — submits two [05 R-WORK-01 §8].
//
// The count is not a function of the tick. What varies per producer is the
// retry interval after a refused visit (one tick for the build and repair
// family, two for unit reclaim, capture and feature reclaim), and that belongs
// to the executor that decides whether a visit happens at all, never to the
// emission the visit produces. This helper used to fold a tick parity in —
// "build draws two per tick, reclaim and capture one on even ticks" — which
// contradicted the census in both directions and forced its one production
// caller to label feature reclaim as build to obtain the two.
func NanolatheSegmentCount(mode NanolatheMode) int {
	switch mode {
	case NanolatheFeatureReclaim:
		return 2
	case NanolatheBuild, NanolatheReclaim, NanolatheCapture:
		return 1
	}
	return 0
}

// BuildNanolatheSegments preserves producer endpoints. Exact footprint
// offsets are not established by the retail contract, so no offsets are
// invented here [03 §5.5][I9].
func BuildNanolatheSegments(e Event) []NanolatheSegment {
	n := NanolatheSegmentCount(NanolatheMode(e.Mode))
	if n == 0 {
		return nil
	}
	out := make([]NanolatheSegment, n)
	for i := range out {
		out[i] = NanolatheSegment{
			Index: int32(i),
			FromX: e.X, FromY: e.Y, FromZ: e.Z,
			ToX: e.TargetX, ToY: e.TargetY, ToZ: e.TargetZ,
			Color: 6, // GUI palette index [03 §5.5]
		}
	}
	return out
}

// EmitNanolatheSegments admits the established segments in admission order;
// EventBuffer remains the sole ID/sequence owner [I6].
func (c *EventBuffer) EmitNanolatheSegments(e Event, tick uint32) int {
	if c == nil {
		return 0
	}
	segments := BuildNanolatheSegments(e)
	count := 0
	for _, segment := range segments {
		segmentEvent := e
		segmentEvent.Tick = tick
		segmentEvent.NanolatheIndex = segment.Index
		segmentEvent.NanolatheCount = int32(len(segments))
		segmentEvent.PaletteRow = int16(segment.Color)
		segmentEvent.X, segmentEvent.Y, segmentEvent.Z = segment.FromX, segment.FromY, segment.FromZ
		segmentEvent.TargetX, segmentEvent.TargetY, segmentEvent.TargetZ = segment.ToX, segment.ToY, segment.ToZ
		if c.EmitNanolathe(segmentEvent) {
			count++
		}
	}
	return count
}
