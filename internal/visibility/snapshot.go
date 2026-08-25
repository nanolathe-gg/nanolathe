package visibility

// Snapshot captures grids and sensor maps for save [P0-I11][03 §3].
func (s *Service) Snapshot() (wordMask []uint16, byteGrids [10][]uint8, footprints map[ObserverID]footprint, local PlayerID, mode Mode, w, h int32) {
	if s == nil {
		return nil, [10][]uint8{}, nil, 0, 0, 0, 0
	}
	if s.wordMask != nil {
		wordMask = make([]uint16, len(s.wordMask))
		copy(wordMask, s.wordMask)
	}
	for i := 0; i < 10; i++ {
		if s.byteGrids[i] != nil {
			cp := make([]uint8, len(s.byteGrids[i]))
			copy(cp, s.byteGrids[i])
			byteGrids[i] = cp
		}
	}
	if s.footprints != nil {
		footprints = make(map[ObserverID]footprint, len(s.footprints))
		for k, v := range s.footprints {
			footprints[k] = v
		}
	}
	return wordMask, byteGrids, footprints, s.local, s.mode, s.W, s.H
}

// SnapshotStatus captures sensor status maps for session [03 §3.4][P0-I11].
// Session owns visStatus/visDecloak maps, not visibility.Service; these are accessed via session helpers directly.
// This helper just exposes footprints and grids.

// RestoreSnapshot restores grids and footprints [P0-I11][03 §3].
func (s *Service) RestoreSnapshot(wordMask []uint16, byteGrids [10][]uint8, footprints map[ObserverID]footprint, local PlayerID, mode Mode) {
	if s == nil {
		return
	}
	if wordMask != nil {
		s.wordMask = make([]uint16, len(wordMask))
		copy(s.wordMask, wordMask)
	} else {
		s.wordMask = nil
	}
	for i := 0; i < 10; i++ {
		if byteGrids[i] != nil {
			cp := make([]uint8, len(byteGrids[i]))
			copy(cp, byteGrids[i])
			s.byteGrids[i] = cp
		} else {
			s.byteGrids[i] = nil
		}
	}
	if footprints != nil {
		s.footprints = make(map[ObserverID]footprint, len(footprints))
		for k, v := range footprints {
			s.footprints[k] = v
		}
	} else {
		s.footprints = nil
	}
	s.local = local
	s.mode = mode
}

// GridSnapshot returns wordMask and byteGrids for save translation without footprints [P0-I11].
func (s *Service) GridSnapshot() ([]uint16, [10][]uint8) {
	if s == nil {
		return nil, [10][]uint8{}
	}
	var w []uint16
	if s.wordMask != nil {
		w = make([]uint16, len(s.wordMask))
		copy(w, s.wordMask)
	}
	var b [10][]uint8
	for i := 0; i < 10; i++ {
		if s.byteGrids[i] != nil {
			cp := make([]uint8, len(s.byteGrids[i]))
			copy(cp, s.byteGrids[i])
			b[i] = cp
		}
	}
	return w, b
}
