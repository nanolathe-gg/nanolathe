package features

// Snapshot captures feature pool for save [P0-I11][05].
func (s *Service) Snapshot() (cursor int, lastRepro int, instances []*Instance) {
	if s == nil {
		return 0, -1, nil
	}
	m := make(map[int]*Instance)
	// instances map is private but same package can access
	m = s.instances
	// collect deterministic sorted keys [I1]
	// we return slice sorted by key for determinism
	// caller must sort, but we do here
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// sort ascending (deterministic)
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	instances = make([]*Instance, 0, len(m))
	for _, k := range keys {
		if inst := m[k]; inst != nil {
			// shallow copy is okay, caller will deep copy fields
			instances = append(instances, inst)
		}
	}
	return s.cursor, s.LastReproIdx, instances
}

// SnapshotWithKeys returns cursor, lastRepro, and map for direct translation [P0-I11].
func (s *Service) SnapshotWithKeys() (int, int, map[int]*Instance) {
	if s == nil {
		return 0, -1, nil
	}
	cp := make(map[int]*Instance, len(s.instances))
	for k, v := range s.instances {
		cp[k] = v
	}
	return s.cursor, s.LastReproIdx, cp
}

// Restore restores feature pool from snapshot [P0-I11].
func (s *Service) Restore(cursor, lastRepro int, instances map[int]*Instance) {
	if s == nil {
		return
	}
	s.cursor = cursor
	s.LastReproIdx = lastRepro
	if instances == nil {
		s.instances = make(map[int]*Instance)
		return
	}
	cp := make(map[int]*Instance, len(instances))
	for k, v := range instances {
		cp[k] = v
	}
	s.instances = cp
}

// RestoreFromSlice restores from slice of instances with deterministic keys recomputed [P0-I11].
// Each instance's CX,CZ determines key = cz*W+cx.
func (s *Service) RestoreFromSlice(cursor, lastRepro int, list []*Instance) {
	if s == nil {
		return
	}
	s.cursor = cursor
	s.LastReproIdx = lastRepro
	if s.instances == nil {
		s.instances = make(map[int]*Instance)
	} else {
		for k := range s.instances {
			delete(s.instances, k)
		}
	}
	if s.Terrain != nil {
		w := int(s.Terrain.CellW)
		for _, inst := range list {
			if inst == nil {
				continue
			}
			key := inst.CZ*w + inst.CX
			s.instances[key] = inst
		}
	} else {
		for i, inst := range list {
			s.instances[i] = inst
		}
	}
}

// InstancesMap returns private map for save translation [P0-I11].
func (s *Service) InstancesMap() map[int]*Instance {
	if s == nil {
		return nil
	}
	return s.instances
}
