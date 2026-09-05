// Fog presentation: C13, C14, C15 [PLAN_05 WU-05-5].

package visibility

// FogCache is presentation-only fog [03 §3.3] C13. Its validity is solely
// Service.mode bit 3; the cache itself never carries a second validity flag.
type FogCache struct {
	w, h int32
	ch0  []uint8 // channel zero: values 0..15
	ch1  []uint8 // channel one: values 0..15
	// originX/originZ identify the first cache cell in a viewport-aligned window.
	originX, originZ int32
	// out0/out1 back Channels' result. See Channels for why they are retained.
	out0, out1 []uint8
}

// Fog returns the presentation fog cache [PLAN_05 Public API].
func (s *Service) Fog() *FogCache { return &s.fog }

// Dimensions returns the fog grid dimensions [03 §3.1] C1.
func (f *FogCache) Dimensions() (int32, int32) {
	if f == nil {
		return 0, 0
	}
	return f.w, f.h
}

// Origin returns the map-cell origin of a viewport-aligned cache window.
// Map-sized snapshots return (0,0). [03 §3.3]
func (f *FogCache) Origin() (int32, int32) {
	if f == nil {
		return 0, 0
	}
	return f.originX, f.originZ
}

// Channels returns copies of the two channel slices for snapshot presentation [03 §3.3] C13.
// The slices are copies: writing through them does not affect the cache (I6).
//
// They are not fresh copies. The publication boundary calls this once per tick
// and copies the result straight into the frame's own buffers, so allocating
// two map-sized slices per tick only to discard them was a per-tick allocation
// funding nothing — and the zeroing of the new slices, immediately overwritten
// by the copy, was the visible cost. The result is therefore backed by storage
// the cache retains and refills, which keeps the documented guarantee (the
// cache's own channels are still untouched by a caller's writes) while costing
// nothing per tick.
//
// The consequence a caller must respect: a second call invalidates the slices
// the first one returned. Consume or copy the result before calling again.
func (f *FogCache) Channels() ([]uint8, []uint8) {
	if f == nil || f.ch0 == nil {
		return nil, nil
	}
	if cap(f.out0) < len(f.ch0) {
		f.out0 = make([]uint8, len(f.ch0))
	}
	if cap(f.out1) < len(f.ch1) {
		f.out1 = make([]uint8, len(f.ch1))
	}
	f.out0, f.out1 = f.out0[:len(f.ch0)], f.out1[:len(f.ch1)]
	copy(f.out0, f.ch0)
	copy(f.out1, f.ch1)
	return f.out0, f.out1
}

// NewFogCacheFromChannelsAt reconstructs the detached cache with its
// viewport origin preserved across the snapshot boundary [03 §3.3].
func NewFogCacheFromChannelsAt(w, h, originX, originZ int32, ch0, ch1 []uint8) *FogCache {
	if w <= 0 || h <= 0 {
		return &FogCache{w: w, h: h, originX: originX, originZ: originZ}
	}
	n := int(w * h)
	fc := &FogCache{w: w, h: h, originX: originX, originZ: originZ, ch0: make([]uint8, n), ch1: make([]uint8, n)}
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

// ReplaceChannelsAt refreshes a detached presentation cache in place. It
// retains channel storage when dimensions are unchanged so frame presentation
// does not allocate a new cache on every publication [03 §3.3][I6].
func (f *FogCache) ReplaceChannelsAt(w, h, originX, originZ int32, ch0, ch1 []uint8) bool {
	if f == nil || w <= 0 || h <= 0 {
		return false
	}
	product := int64(w) * int64(h)
	maxInt := int64(^uint(0) >> 1)
	if product <= 0 || product > maxInt {
		return false
	}
	n := int(product)
	if len(ch0) != n || len(ch1) != n {
		return false
	}
	if f.w != w || f.h != h || cap(f.ch0) < n || cap(f.ch1) < n {
		f.ch0 = make([]uint8, n)
		f.ch1 = make([]uint8, n)
	} else {
		f.ch0 = f.ch0[:n]
		f.ch1 = f.ch1[:n]
	}
	f.w, f.h, f.originX, f.originZ = w, h, originX, originZ
	copy(f.ch0, ch0)
	copy(f.ch1, ch1)
	for i := 0; i < n; i++ {
		f.ch0[i] &= 0x0F
		f.ch1[i] &= 0x0F
	}
	return true
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

// RebuildFog lazily rebuilds the two-channel cache [03 §3.3] C13 [03 §3.3].
// The cache never writes word mask; values 15 are solid dark (channel0 Black/history) or patterned fill (channel1 Gray/current).
// Values 1..14 select GAF frame value-1 from variant families keyed by cell parity plus camera phase.
// Channel one (hi/Gray/current) renders first, then channel zero (lo/Black/history) [03 §3.3] [03 §3.3].
//
// Established: hi accumulates per-player byte-grid (cur==0) only when mode bit 1 is set else zeroed [03 §3.3],
// lo accumulates word-grid history mask 1<<player regardless [03 §3.3]; each holds a 4-bit nibble 0..15 via four bounded
// OR 1,2,4,8 sites (0 transparent, 15 solid dark, 1..14 index value-1 into Gray=hi/current and Black=lo/history four-way variant families
// with variant=(col+row+camPhase)&3 deterministically from floorMod(camera,32) residues [03 §3.3]), edge rows/cols forced to 15
// when viewport extends beyond map [03 §3.3]. Corner→bit 1=NW,2=NE,4=SW,8=SE remains supported inference pending asymmetric probe [03 §3.3].
// Camera residues/offX are used for viewport-sized cache alignment; for Nanolathe's map-sized cache we generate for the whole map and
// let BuildFogOps handle viewport clipping via hard 32 edges [03 §3.3] C13 — viewport edge forcing is therefore a render-time concern
// and the cache remains map-aligned. That is a deliberate layout divergence, not
// an open retail question: [03 §3.3] establishes the residues and the hard-32
// edge forcing, and BuildFogOps applies both at viewport clip time, so a
// map-aligned cache produces the same ops a viewport-aligned one would.
func (s *Service) RebuildFog(cameraX, cameraY int32) {
	if s == nil || s.fog.ch0 == nil || s.mode.FogCacheValid() {
		return
	}
	// Keep the historical entry point as a compatibility adapter. The only
	// producer is the viewport-aligned builder; a full-map view preserves the
	// old snapshot call while retaining edge residues and fixups.
	s.RebuildFogWindow(cameraX, cameraY, s.W*32, s.H*32)
}

// RebuildFogWindow rebuilds a cache sized to the camera viewport plus the
// one-cell border.  The cache is aligned to visibility-cell corners and is
// derived from the authoritative stores without modifying them. [03 §3.3]
//
// The cache is always window-addressed, including a zero-origin window.
func (s *Service) RebuildFogWindow(cameraX, cameraZ, viewW, viewH int32) {
	if s == nil || viewW <= 0 || viewH <= 0 || s.W <= 0 || s.H <= 0 {
		return
	}
	// Window cells cover the camera viewport and one border cell on each side.
	startX := floorDivFog(cameraX-16, 32) - 1
	startZ := floorDivFog(cameraZ-16, 32) - 1
	endX := floorDivFog(cameraX+viewW-16+31, 32) + 1
	endZ := floorDivFog(cameraZ+viewH-16+31, 32) + 1
	w, h := endX-startX, endZ-startZ
	if w <= 0 || h <= 0 {
		return
	}
	if s.mode.FogCacheValid() && s.fog.originX == startX && s.fog.originZ == startZ && s.fog.w == w && s.fog.h == h {
		return
	}
	n := int(w * h)
	if s.fog.w != w || s.fog.h != h || len(s.fog.ch0) != n {
		s.fog.w, s.fog.h = w, h
		s.fog.ch0 = make([]uint8, n)
		s.fog.ch1 = make([]uint8, n)
	}
	s.fog.originX, s.fog.originZ = startX, startZ
	for i := range s.fog.ch0 {
		s.fog.ch0[i], s.fog.ch1[i] = 0, 0
	}
	put := func(dst []uint8, gx, gz int32, bit uint8) {
		if gx < startX || gx >= endX || gz < startZ || gz >= endZ {
			return
		}
		dst[int((gz-startZ)*w+(gx-startX))] |= bit
	}
	seed := func(dst []uint8, gx, gz int32) {
		put(dst, gx, gz, 1)
		put(dst, gx-1, gz, 2)
		put(dst, gx, gz-1, 4)
		put(dst, gx-1, gz-1, 8)
	}
	local := s.local
	bit := cellBit(local)
	for gz := int32(0); gz < s.H; gz++ {
		for gx := int32(0); gx < s.W; gx++ {
			i := int(gz*s.W + gx)
			if s.mode.CurrentEnabled() && i < len(s.byteGrids[local]) && s.byteGrids[local][i] == 0 {
				seed(s.fog.ch1, gx, gz)
			}
			if i < len(s.wordMask) && s.wordMask[i]&bit == 0 {
				seed(s.fog.ch0, gx, gz)
			}
		}
	}
	// Border fixups are conditional ORs and run in the established order.
	fix := func(dst []uint8, gx, gz int32, a, b, c, d uint8) {
		if gx < startX || gx >= endX || gz < startZ || gz >= endZ {
			return
		}
		i := int((gz-startZ)*w + gx - startX)
		if dst[i]&a != 0 {
			dst[i] |= b
		}
		if dst[i]&c != 0 {
			dst[i] |= d
		}
	}
	if startZ < 0 {
		for gx := startX; gx < endX; gx++ {
			fix(s.fog.ch1, gx, startZ, 4, 1, 8, 2)
			fix(s.fog.ch0, gx, startZ, 4, 1, 8, 2)
		}
	}
	if endZ > s.H {
		for gx := startX; gx < endX; gx++ {
			fix(s.fog.ch1, gx, endZ-2, 1, 4, 2, 8)
			fix(s.fog.ch0, gx, endZ-2, 1, 4, 2, 8)
		}
	}
	if startX < 0 {
		for gz := startZ; gz < endZ; gz++ {
			fix(s.fog.ch1, startX, gz, 8, 4, 2, 1)
			fix(s.fog.ch0, startX, gz, 8, 4, 2, 1)
		}
	}
	if endX > s.W {
		for gz := startZ; gz < endZ; gz++ {
			fix(s.fog.ch1, endX-2, gz, 4, 8, 1, 2)
			fix(s.fog.ch0, endX-2, gz, 4, 8, 1, 2)
		}
	}
	s.mode |= ModeFogCacheValid
}

func floorDivFog(a, b int32) int32 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// MarkUnexplored implements C14: plot flag byte (PlotCell byte 0x0C) bit 0x04 set/clear [03 §3.3].
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
