package presentation

import "github.com/nanolathe/nanolathe/internal/sim/rng"

// CRTLedgerEntry records one presentation-random draw. Labels are diagnostic
// only; the draw sequence and call order remain the behavior [01 §7.2], [I4].
type CRTLedgerEntry struct {
	Consumer string
	Draw     uint64
	Value    int32
}

// CRTRandom owns the one session-local presentation CRT stream. It is a value
// wrapper rather than a package-global generator, so a battle can be torn
// down without leaking random state into the next session. Simulation code
// must not retain this object [03 §1], [I4].
type CRTRandom struct {
	stream *rng.CRT
	ledger []CRTLedgerEntry
}

// WrapCRT creates the presentation view of an existing session-local CRT
// stream. Battle setup should use this constructor so shake, audio, music,
// and other presentation consumers share one call order [01 §7.2], [I4].
func WrapCRT(stream *rng.CRT) *CRTRandom { return &CRTRandom{stream: stream} }

// NewCRTRandomFromCRT is the descriptive alias for WrapCRT.
func NewCRTRandomFromCRT(stream *rng.CRT) *CRTRandom { return WrapCRT(stream) }

// NewCRTRandom constructs a standalone deterministic stream for tests and
// tools. A live session must use WrapCRT with its already-owned CRT stream;
// constructing this convenience stream beside a session stream would split
// the established call order [01 §7.2], [I4].
func NewCRTRandom(seed uint32) *CRTRandom {
	stream := rng.NewCRT(seed)
	return WrapCRT(&stream)
}

// NewCRTRandomFromState resumes a stream at an explicit state for diagnostics
// and native save tooling. Retail's ordinary save does not persist this state
// [01 §7.3].
func NewCRTRandomFromState(state uint32) *CRTRandom {
	stream := rng.CRTFromState(state)
	return WrapCRT(&stream)
}

// Draw consumes one CRT value and records it with an optional consumer label.
// Calling Draw without a label is equivalent to Rand and still appears in the
// ledger, preserving an exact count for silent resolutions [03 §8.3].
func (r *CRTRandom) Draw(consumer ...string) int32 {
	if r == nil {
		return 0
	}
	if r.stream == nil {
		return 0
	}
	v := r.stream.Rand()
	label := ""
	if len(consumer) != 0 {
		label = consumer[0]
	}
	r.ledger = append(r.ledger, CRTLedgerEntry{Consumer: label, Draw: r.stream.Draws(), Value: v})
	return v
}

// Rand consumes one unlabeled CRT value. It mirrors rng.CRT.Rand for callers
// that do not need diagnostics.
func (r *CRTRandom) Rand() int32 { return r.Draw() }

// Sample consumes the established CRT bounded sample and labels every raw
// draw used by a wide-bound concatenation [01 §7.2]. A bound of one still
// consumes a draw; zero consumes a draw and returns zero as the safe API
// representation of retail's invalid modulo path.
func (r *CRTRandom) Sample(consumer string, bound uint32) uint32 {
	if r == nil {
		return 0
	}
	if bound <= 0x7FFF {
		v := uint32(r.Draw(consumer))
		if bound == 0 {
			return 0
		}
		return v % bound
	}
	result := uint32(0x7FFF)
	mask := uint32(0x7FFF)
	for mask < bound {
		result = (result << 15) | uint32(r.Draw(consumer))
		mask = (mask << 15) | 0x7FFF
		if mask == ^uint32(0) {
			break
		}
	}
	return result % bound
}

// Uint32n is the unlabeled bounded CRT helper.
func (r *CRTRandom) Uint32n(bound uint32) uint32 { return r.Sample("", bound) }

// State returns the underlying 32-bit recurrence state without advancing it.
func (r *CRTRandom) State() uint32 {
	if r == nil || r.stream == nil {
		return 0
	}
	return r.stream.State
}

// Draws reports the total number of raw CRT draws, including silent resolves.
func (r *CRTRandom) Draws() uint64 {
	if r == nil || r.stream == nil {
		return 0
	}
	return r.stream.Draws()
}

// CRT exposes the wrapped stream for adapters that still accept the base RNG
// type. New consumers should accept *CRTRandom directly so the ledger remains
// complete; this method does not allocate or copy the stream [I4].
func (r *CRTRandom) CRT() *rng.CRT {
	if r == nil {
		return nil
	}
	return r.stream
}

// Ledger returns a detached diagnostic copy in draw order.
func (r *CRTRandom) Ledger() []CRTLedgerEntry {
	if r == nil || len(r.ledger) == 0 {
		return nil
	}
	out := make([]CRTLedgerEntry, len(r.ledger))
	copy(out, r.ledger)
	return out
}

// ResetLedger clears diagnostics without rewinding the stream.
func (r *CRTRandom) ResetLedger() {
	if r != nil {
		r.ledger = r.ledger[:0]
	}
}
