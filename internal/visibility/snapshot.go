package visibility

// GridSnapshot returns copies of the visibility grids for presentation-only
// consumers such as positional audio [03 §3.1][03 §8.3]. It deliberately does
// not expose observer footprints or mutable service state.
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
