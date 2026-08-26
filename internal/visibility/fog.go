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

// Dimensions returns the fog grid dimensions [03 §3.1] C1.
func (f *FogCache) Dimensions() (int32, int32) {
	if f == nil {
		return 0, 0
	}
	return f.w, f.h
}

// Channels returns copies of the two channel slices for snapshot presentation [03 §3.3] C13.
// The slices are copies; mutation does not affect the cache (I6).
func (f *FogCache) Channels() ([]uint8, []uint8) {
	if f == nil || f.ch0 == nil {
		return nil, nil
	}
	c0 := make([]uint8, len(f.ch0))
	copy(c0, f.ch0)
	c1 := make([]uint8, len(f.ch1))
	copy(c1, f.ch1)
	return c0, c1
}

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

// NewFogCacheFromChannels creates a presentation FogCache from snapshot channels [03 §3.3] (I6).
// It allocates w*h entries and copies ch0/ch1 (each 0..15) then marks valid.
func NewFogCacheFromChannels(w, h int32, ch0, ch1 []uint8) *FogCache {
	if w <= 0 || h <= 0 {
		return &FogCache{w: w, h: h, valid: true}
	}
	n := int(w * h)
	fc := &FogCache{w: w, h: h, ch0: make([]uint8, n), ch1: make([]uint8, n), valid: true}
	if len(ch0) >= n {
		copy(fc.ch0, ch0[:n])
	} else if len(ch0) > 0 {
		copy(fc.ch0, ch0)
	}
	if len(ch1) >= n {
		copy(fc.ch1, ch1[:n])
	} else if len(ch1) > 0 {
		copy(fc.ch1, ch1)
	}
	for i := range fc.ch0 {
		fc.ch0[i] &= 0x0F
	}
	for i := range fc.ch1 {
		fc.ch1[i] &= 0x0F
	}
	return fc
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

// RebuildFog lazily rebuilds the two-channel cache [03 §3.3] C13 [rr-16].
// The cache never writes word mask; values 15 are solid dark (channel0 Black/history) or patterned fill (channel1 Gray/current).
// Values 1..14 select GAF frame value-1 from variant families keyed by cell parity plus camera phase.
// Channel one (hi/Gray/current) renders first, then channel zero (lo/Black/history) [03 §3.3] [rr-16 §6.1/6.2].
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// lo accumulates word-grid history mask 1<<player regardless [rr-16 §6.1]; each holds a 4-bit nibble 0..15 via four bounded
// OR 1,2,4,8 sites (0 transparent, 15 solid dark, 1..14 index value-1 into Gray=hi/current and Black=lo/history four-way variant families
// with variant=(col+row+camPhase)&3 deterministically from floorMod(camera,32) residues [rr-16 §6.1/6.2]), edge rows/cols forced to 15
// when viewport extends beyond map [rr-16 §6.1]. Corner→bit 1=NW,2=NE,4=SW,8=SE remains supported inference pending asymmetric probe [rr-16 §6.1].
// Camera residues/offX are used for viewport-sized cache alignment; for Nanolathe's map-sized cache we generate for the whole map and
// let BuildFogOps handle viewport clipping via hard 32 edges [03 §3.3] C13 — viewport edge forcing is therefore a render-time concern
// and the cache remains map-aligned for simplicity (divergence documented as TODO(question) for exact viewport-sized residue alignment).
func (s *Service) RebuildFog(cameraX, cameraY int32) {
	if s == nil || s.fog.ch0 == nil {
		return
	}
	if s.fog.valid {
		return
	}
	// Clear entire cache (rep stos) [rr-16 §6.1].
	for i := range s.fog.ch0 {
		s.fog.ch0[i] = 0
		s.fog.ch1[i] = 0
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// is sufficient for correctness of nibble values since each visibility tile's fog contribution is local to its 4 neighbours).
	w := int(s.fog.w)
	h := int(s.fog.h)
	if w <= 0 || h <= 0 {
		s.fog.valid = true
		return
	}
	// Hi channel (ch1) — current visibility, only when mode bit 1 (ModeCurrentEnabled, 0x2) is set [rr-16 §6.1: TEST 2].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if s.mode&ModeCurrentEnabled != 0 {
		localGrid := s.byteGrids[s.local]
		if localGrid != nil {
			visW := int(s.W)
			visH := int(s.H)
			for gy := 0; gy < visH; gy++ {
				for gx := 0; gx < visW; gx++ {
					idxVis := gy*visW + gx
					if idxVis < 0 || idxVis >= len(localGrid) {
						continue
					}
					if localGrid[idxVis] != 0 {
						continue // visible -> no fog contribution
					}
					// Fogged visibility tile (cur==0) contributes to up to 4 cache neighbours.
					// Mapping per RR-16 §6.1: cache (gx,gy) bit 1, (gx-1,gy) bit 2, (gx,gy-1) bit 4, (gx-1,gy-1) bit 8.
					if gx >= 0 && gy >= 0 && gx < w && gy < h {
						s.fog.ch1[gy*w+gx] |= 1
					}
					if gx-1 >= 0 && gy >= 0 && gx-1 < w && gy < h {
						s.fog.ch1[gy*w+(gx-1)] |= 2
					}
					if gx >= 0 && gy-1 >= 0 && gx < w && gy-1 < h {
						s.fog.ch1[(gy-1)*w+gx] |= 4
					}
					if gx-1 >= 0 && gy-1 >= 0 && gx-1 < w && gy-1 < h {
						s.fog.ch1[(gy-1)*w+(gx-1)] |= 8
					}
				}
			}
		}
	}
	// Lo channel (ch0) — history/unexplored, always from word grid [rr-16 §6.1: after hi loop, lo loop unconditional].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	bit := cellBit(s.local)
	if len(s.wordMask) > 0 {
		visW := int(s.W)
		visH := int(s.H)
		for gy := 0; gy < visH; gy++ {
			for gx := 0; gx < visW; gx++ {
				idxVis := gy*visW + gx
				if idxVis < 0 || idxVis >= len(s.wordMask) {
					continue
				}
				if s.wordMask[idxVis]&bit != 0 {
					continue // explored -> no lo contribution
				}
				if gx >= 0 && gy >= 0 && gx < w && gy < h {
					s.fog.ch0[gy*w+gx] |= 1
				}
				if gx-1 >= 0 && gy >= 0 && gx-1 < w && gy < h {
					s.fog.ch0[gy*w+(gx-1)] |= 2
				}
				if gx >= 0 && gy-1 >= 0 && gx < w && gy-1 < h {
					s.fog.ch0[(gy-1)*w+gx] |= 4
				}
				if gx-1 >= 0 && gy-1 >= 0 && gx-1 < w && gy-1 < h {
					s.fog.ch0[(gy-1)*w+(gx-1)] |= 8
				}
			}
		}
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// For Nanolathe's map-sized cache, the viewport-sized equivalent is that cells beyond the map are considered
	// fogged (out-of-bounds visibility considered unexplored). Retail forces those border cache rows/cols to 15
	// when camera tile start <0 or end > map. We approximate by treating virtual out-of-bounds tiles as fogged for
	// outer ring when the corresponding visibility border is fogged — handled implicitly by the OR pattern where
	// missing neighbours would have contributed bits 2/4/8 at the edge but were skipped. To ensure map outer edge
	// appears solid when the edge is fogged, we optionally force outer ring partially-fogged cells to 15.
	// This is a supported inference for map-sized cache; full viewport-sized residue handling remains TODO(question)
	// for exact offX/offZ alignment [rr-16 §7/10].
	// No additional forcing here preserves partial transition at map edge, which matches the hard 32 edge without extra fill.
	// Callers that need beyond-map solid can rely on BuildFogOps viewport culling leaving out-of-bounds as no-cache (treated as visible
	// in current BuildFogOps, but terrain void beyond map is already black via BlitTerrain clipping).
	_ = cameraX
	_ = cameraY
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
