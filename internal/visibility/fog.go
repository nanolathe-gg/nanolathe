// Package visibility fog implements C13, C14, C15 [PLAN_05 WU-05-5].
package visibility

// FogCache is presentation-only fog [03 §3.3] C13.
// It rebuilds lazily when valid bit clears, never writes word mask.
type FogCache struct {
	w, h  int32
	ch0   []uint8 // channel zero: values 0..15
	ch1   []uint8 // channel one: values 0..15
	valid bool
}

// Fog returns the presentation fog cache [PLAN_05 Public API].
func (s *Service) Fog() *FogCache { return &s.fog }

// IsValid reports whether the cache is valid.
func (f *FogCache) IsValid() bool { return f != nil && f.valid }

// Invalidate clears the valid bit, waking composer [03 §3.3] C13.
func (f *FogCache) Invalidate() {
	if f != nil {
		f.valid = false
	}
}

// Validate sets valid after rebuild.
func (f *FogCache) Validate() {
	if f != nil {
		f.valid = true
	}
}

// Channel returns the two channel values for cell (x,y) for tests.
func (f *FogCache) Channel(x, y int32) (uint8, uint8) {
	if f == nil || f.ch0 == nil {
		return 0, 0
	}
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return 0, 0
	}
	idx := int(y*f.w + x)
	return f.ch0[idx], f.ch1[idx]
}

// SetChannel sets channel values for tests (presentation only).
func (f *FogCache) SetChannel(x, y int32, c0, c1 uint8) {
	if f == nil || f.ch0 == nil {
		return
	}
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	idx := int(y*f.w + x)
	f.ch0[idx] = c0 & 0x0F
	f.ch1[idx] = c1 & 0x0F
}

// RebuildFog lazily rebuilds the two-channel cache [03 §3.3] C13.
// The cache never writes word mask; values 15 are solid dark (channel0) or patterned fill (channel1).
// Values 1..14 select GAF frame value-1 from variant families keyed by cell parity plus camera phase.
// Channel one renders first.
//
// TODO(question): the engine-side conversion producing the cached channel
// values is an unresolved residual of [03 §3.3] — how visible/hidden history
// and current coverage map into the two nibbles, and which option bit selects
// the patterned fill. The fill below is a placeholder (15 solid dark when not
// currently visible, 0 otherwise); it is NOT attested retail output.
func (s *Service) RebuildFog(cameraX, cameraY int32) {
	if s == nil || s.fog.ch0 == nil {
		return
	}
	if s.fog.valid {
		return
	}
	// Deterministic fill: channel0 = 15 if word bit set? Actually fog = not-visible.
	// But we lack history bit; use wordMask local bit as history proxy.
	bit := cellBit(s.local)
	for y := int32(0); y < s.H; y++ {
		for x := int32(0); x < s.W; x++ {
			idx := int(y*s.W + x)
			visible := false
			if idx < len(s.wordMask) && s.wordMask[idx]&bit != 0 {
				visible = true
			}
			// Also consider byte grid
			if s.mode&ModeCurrentEnabled != 0 && int(s.local) < len(s.byteGrids) && s.byteGrids[s.local] != nil {
				if s.byteGrids[s.local][idx] != 0 {
					visible = true
				}
			}
			var c0, c1 uint8
			if !visible {
				// Placeholder fill; see TODO(question) above — not attested.
				c0 = 15
				c1 = 0
			} else {
				c0 = 0
				c1 = 0
			}
			// Variant families keyed by (cellX+cellY+cameraPhase)&3 omitted for now; values 0..15 as above.
			_ = cameraX
			_ = cameraY
			s.fog.ch0[idx] = c0
			s.fog.ch1[idx] = c1
		}
	}
	s.fog.valid = true
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// This helper operates on world.PlotCell flag byte; caller supplies flag pointer.
// Height >=10 immediate mark is enforced by caller scanning feature height.
func MarkUnexplored(flag *uint8, height int32) {
	if flag == nil {
		return
	}
	if height >= 10 {
		*flag |= 0x04 // set |4 when tall feature skipped [C14]
	}
}

// ClearUnexplored clears &0xFB when drawn [C14].
func ClearUnexplored(flag *uint8) {
	if flag == nil {
		return
	}
	*flag &^= 0x04
}
