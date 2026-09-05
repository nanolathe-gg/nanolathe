package render

import "github.com/nanolathe/nanolathe/internal/sim/numeric"

// StripObject is the strip lifecycle contract [03 §1] C4.
// The update dispatcher evaluates ShouldRemove BEFORE Update for every object.
type StripObject interface {
	ShouldRemove(tick uint32) bool
	Update(tick uint32)
}

// Strip is a vector descriptor for effect strips [03 §1].
// Each strip holds at most 401 records in steady state; producers append at
// the end and the oldest is destroyed first when the pre-insert count exceeds
// 400 [03 §1] C4.
type Strip struct {
	Objects []StripObject
}

// Append appends obj at the end, evicting the oldest first when the
// pre-insert count exceeds 400 [03 §1] C4. Steady state is at most 401.
func (s *Strip) Append(obj StripObject) {
	if s == nil || obj == nil {
		return
	}
	if len(s.Objects) > 400 { // pre-insert >400 destroys oldest first [03 §1]
		copy(s.Objects[0:], s.Objects[1:])
		s.Objects[len(s.Objects)-1] = nil
		s.Objects = s.Objects[:len(s.Objects)-1]
	}
	s.Objects = append(s.Objects, obj)
}

// Update evaluates removal BEFORE update for every object and stably compacts
// on a positive verdict so survivors keep their order [03 §1] C4.
// A terminal condition created during an Update is noticed only on the next
// invocation.
func (s *Strip) Update(tick uint32) {
	if s == nil || len(s.Objects) == 0 {
		return
	}
	n := len(s.Objects)
	write := 0
	for read := 0; read < n; read++ {
		obj := s.Objects[read]
		if obj == nil {
			continue
		}
		if obj.ShouldRemove(tick) { // removal BEFORE update [03 §1] C4
			continue
		}
		if write != read {
			s.Objects[write] = obj
		}
		obj.Update(tick)
		write++
	}
	for i := write; i < n; i++ {
		s.Objects[i] = nil
	}
	s.Objects = s.Objects[:write]
}

// FixedEffectCap is the fixed pool capacity [03 §1] C5 (I5).
const FixedEffectCap = 300 // 0x54-byte records, 300 entries [03 §1] (I5)

// FixedEffectPool is the fixed effect pool [03 §1] C5.
// It holds up to 300 fixed-size records; appends at or above the cap allocate
// nothing [03 §1]. Full integration lives in effects.go (WU-13-2).
type FixedEffectPool struct {
	records  []EffectRecord
	gravity  numeric.Fixed                          // default per-tick gravity when record.Gravity is zero [03 §2.2]
	heightAt func(x, z numeric.Fixed) numeric.Fixed // terrain height query; nil skips terrain/water contact [03 §1]
	seaLevel numeric.Fixed                          // sea level in world units byte*65536 [03 §2.2]
}
