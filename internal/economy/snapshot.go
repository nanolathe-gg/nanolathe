package economy

import "github.com/nanolathe/nanolathe/internal/pool"

// SnapshotUnitBuckets returns a copy of unit buckets indexed by handle.
// Slot 0 is included as zero value sentinel [P0-I11].
func (s *Service) SnapshotUnitBuckets() []UnitEconomy {
	if s == nil {
		return nil
	}
	out := make([]UnitEconomy, len(s.unitBuckets))
	copy(out, s.unitBuckets)
	return out
}

// RestoreUnitBuckets restores unit buckets from snapshot [P0-I11].
func (s *Service) RestoreUnitBuckets(snapshot []UnitEconomy) {
	if s == nil {
		return
	}
	if len(snapshot) == 0 {
		s.unitBuckets = nil
		return
	}
	nb := make([]UnitEconomy, len(snapshot))
	copy(nb, snapshot)
	s.unitBuckets = nb
}

// SnapshotUnitBucketsMap returns map handle->UnitEconomy for iteration determinism [I1].
func (s *Service) SnapshotUnitBucketsMap() map[int]UnitEconomy {
	if s == nil {
		return nil
	}
	m := make(map[int]UnitEconomy, len(s.unitBuckets))
	for i, ue := range s.unitBuckets {
		if i == 0 {
			continue
		}
		empty := UnitEconomy{}
		if ue == empty {
			continue
		}
		m[i] = ue
	}
	return m
}

// RestoreUnitBucketsMap restores from handle map [P0-I11].
func (s *Service) RestoreUnitBucketsMap(m map[int]UnitEconomy) {
	if s == nil {
		return
	}
	if len(m) == 0 {
		s.unitBuckets = nil
		return
	}
	max := 0
	for h := range m {
		if h > max {
			max = h
		}
	}
	nb := make([]UnitEconomy, max+1)
	for h, ue := range m {
		if h >= 0 && h < len(nb) {
			nb[h] = ue
		}
	}
	s.unitBuckets = nb
}

// EnsureUnitBucketsSize ensures slice covers handle, used in restore [P0-I11].
func (s *Service) EnsureUnitBucketsSize(handle pool.Handle) {
	s.ensureUnitBuckets(handle)
}
