package combat

import "github.com/nanolathe-gg/nanolathe/internal/content"

// AuthoredVeteranLevel implements the shared CP-UD-1 threshold algorithm.
// The bounded lookup is the source's upper-bound search, not a linear count;
// authored order is therefore preserved even for malformed unsorted lists.
// The unbounded tail retains unsigned subtraction and division, including the
// divide fault for equal final thresholds.
func AuthoredVeteranLevel(def *content.UnitDef, kills uint16, unbounded bool) uint32 {
	if def == nil {
		return 0
	}
	thresholds := def.VeterancyThresholds
	n := uint32(len(thresholds))
	if n == 0 || uint32(kills) < thresholds[0] {
		return 0
	}
	last := thresholds[n-1]
	if !unbounded || uint32(kills) <= last {
		// This is the conventional upper_bound binary search. Do not replace it
		// with a count: malformed authored ordering is intentionally untouched.
		lo, hi := uint32(0), n
		for lo < hi {
			mid := lo + (hi-lo)/2
			if uint32(kills) < thresholds[mid] {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		return lo
	}
	if n == 1 {
		return uint32(kills) / thresholds[0]
	}
	rate := last - thresholds[n-2]
	return n + (uint32(kills)-last)/rate
}

// VeteranLevel exposes the live bounded answer used by damage and reload and
// by committed presentation. A nil or unbound service answers as Strict 3.1.
func (s *Service) VeteranLevel(def *content.UnitDef, kills int32) uint32 {
	return s.rules().VeteranLevel(VeteranLevelRequest{Service: s, Definition: def, Kills: uint16(kills)})
}

func (s *Service) veteranLeadAdmitted(def *content.UnitDef, kills int32) bool {
	return s.rules().VeteranLeadAdmitted(VeteranRequest{Service: s, Definition: def, Kills: uint16(kills)})
}

func (s *Service) veteranSpreadDivisor(def *content.UnitDef, kills int32) uint32 {
	return s.rules().VeteranSpreadDivisor(VeteranRequest{Service: s, Definition: def, Kills: uint16(kills)})
}

func (s *Service) storedReload(def *content.UnitDef, health, maxHealth, kills, authoredReload int32) int32 {
	level := s.VeteranLevel(def, kills)
	if level > 16 {
		level = 16
	}
	return computeStoredReloadAtLevel(health, maxHealth, int32(level), authoredReload)
}
