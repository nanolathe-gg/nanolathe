// Package pool implements the fixed-capacity deterministic pools that underlie
// the simulation. Retail's pool behaviour is load-bearing for determinism:
// iteration order, allocation order, and compaction order are part of the
// contract [01 §6.1], [01 §6.2].
package pool

// Handle is a pool slot index. Slot 0 is the null sentinel and is never
// allocated. There are no generation bits; a stale handle that has been freed
// and reused aliases the new occupant [04 §2.3], [06 §5.1], [01 §6.1], [P0-16].
type Handle uint16

const (
	// ProjectileCapacity is the fixed retail capacity per [01 §6.1] and
	// [06 §5.1]: exactly 300 records of 107 bytes. Allocation appends at the
	// tail and never fills holes until compaction [01 §6.1].
	ProjectileCapacity = 300

	// unitsDefaultCapacity is the fallback capacity used when a Units pool is
	// used without an explicit Init. The retail physical cap is
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// [P0-16] [01 §6.1] — stock ~2000-5001, not the 500 folklore. This
	// placeholder remains configurable via Init / NewUnits and InitSliced;
	// it is not a retail constant.
	unitsDefaultCapacity = 500

	// UnitRecordSize is the retail unit record stride 0x118=280 bytes
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	UnitRecordSize = 0x118
)

// CapacityForDefs returns the total record count including slot 0 for a
// catalog with maxDefs definitions: maxDefs*10+1 [P0-16] [01 §6.1].
// For maxDefs=200 the result is 2001 records (2000 usable + slot 0).
func CapacityForDefs(maxDefs int) int {
	if maxDefs < 0 {
		maxDefs = 0
	}
	return maxDefs*10 + 1
}

// UsableCapacityForDefs returns the usable slot count excluding the null
// sentinel: maxDefs*10 [P0-16].
func UsableCapacityForDefs(maxDefs int) int {
	if maxDefs < 0 {
		maxDefs = 0
	}
	return maxDefs * 10
}

// Units is the fixed pool of 280-byte unit records [01 §6.1] [P0-16].
// Allocation scans for the lowest free slot with slot 0 reserved as null
// [04 §2.3], [01 §6.1]. Freed slots are immediately reusable with no
// generation tag; stale handles alias the new occupant [P0-16] [06 §5.1].
// When sliced, the pool is partitioned per-player as maxDefs slots each via
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// constrained to the owning player's slice [P0-16 §3.2].
type Units struct {
	alive     []bool   // index 0 is sentinel, never allocated; len = totalRecords
	defID     []uint16 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	slotIndex []uint16 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	maxDefs   int
	slices    [10]struct{ start, end int } // inclusive per-player bounds [P0-16 §3.1]
	sliced    bool
}

// NewUnits creates a Units pool with the given usable capacity.
// Capacity is the number of usable slots; slot 0 remains null [04 §2.3].
// This is the legacy unsliced constructor; for retail slicing use
// NewUnitsSliced [P0-16].
func NewUnits(capacity int) *Units {
	u := &Units{}
	u.Init(capacity)
	return u
}

// NewUnitsSliced creates a sliced retail pool for maxDefs catalog
// definitions: total records = maxDefs*10+1 at 0x14357, per-player slices
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func NewUnitsSliced(maxDefs int) *Units {
	u := &Units{}
	u.InitSliced(maxDefs)
	return u
}

// Init configures the pool capacity as a legacy unsliced pool.
// If capacity <= 0 the pool becomes empty and Alloc always fails.
// Slot 0 is reserved as null. Slicing is disabled.
func (p *Units) Init(capacity int) {
	if capacity < 0 {
		capacity = 0
	}
	total := capacity + 1 // include slot 0
	p.alive = make([]bool, total)
	p.defID = make([]uint16, total)
	p.slotIndex = make([]uint16, total)
	for i := 0; i < total; i++ {
		p.slotIndex[i] = uint16(i) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	}
	p.maxDefs = 0
	p.sliced = false
	for i := range p.slices {
		p.slices[i] = struct{ start, end int }{0, -1}
	}
}

// InitSliced configures a retail-sliced pool for maxDefs definitions.
// It allocates total = maxDefs*10+1 records (including slot 0) of 0x118
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P0-16 §3.2]. Slicing uses identity sorted order 0..9; the exact
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question) but does not affect per-slice isolation [P0-16 §3.1].
func (p *Units) InitSliced(maxDefs int) {
	if maxDefs < 0 {
		maxDefs = 0
	}
	total := CapacityForDefs(maxDefs) // includes slot 0
	if total < 1 {
		total = 1
	}
	p.alive = make([]bool, total)
	p.defID = make([]uint16, total)
	p.slotIndex = make([]uint16, total)
	for i := 0; i < total; i++ {
		p.slotIndex[i] = uint16(i) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	}
	p.maxDefs = maxDefs
	p.sliced = maxDefs > 0
	if p.sliced {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		for player := 0; player < 10; player++ {
			start := maxDefs*player + 1
			end := maxDefs * (player + 1)
			// start 1..maxDefs for player 0, etc.; covers 10*maxDefs usable
			if maxDefs == 0 {
				start = 0
				end = -1
			}
			p.slices[player] = struct{ start, end int }{start: start, end: end}
		}
	} else {
		for i := range p.slices {
			p.slices[i] = struct{ start, end int }{0, -1}
		}
	}
}

// InitSlicedWithOrder configures a sliced pool with an explicit sorted
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P0-16 §3.1]. Order must be a permutation of 0..9; nil/empty means identity.
// This is used for tests needing exact slice ownership proof.
func (p *Units) InitSlicedWithOrder(maxDefs int, order []int) {
	if order == nil || len(order) != 10 {
		p.InitSliced(maxDefs)
		return
	}
	// Validate permutation 0..9
	seen := make(map[int]bool, 10)
	for _, v := range order {
		if v < 0 || v >= 10 || seen[v] {
			p.InitSliced(maxDefs)
			return
		}
		seen[v] = true
	}
	total := CapacityForDefs(maxDefs)
	if total < 1 {
		total = 1
	}
	p.alive = make([]bool, total)
	p.defID = make([]uint16, total)
	p.slotIndex = make([]uint16, total)
	for i := 0; i < total; i++ {
		p.slotIndex[i] = uint16(i)
	}
	p.maxDefs = maxDefs
	p.sliced = maxDefs > 0
	if p.sliced {
		for sortedIdx, player := range order {
			start := maxDefs*sortedIdx + 1
			end := maxDefs * (sortedIdx + 1)
			p.slices[player] = struct{ start, end int }{start: start, end: end}
		}
	} else {
		for i := range p.slices {
			p.slices[i] = struct{ start, end int }{0, -1}
		}
	}
}

// ensureInit lazily initializes a zero-value Units so that a simple
// var u Units; u.Alloc() sequence does not panic and provides a usable
// placeholder. The retail maximum is not a fixed constant [01 §6.1] [P0-16],
// so this default is configurable via Init / InitSliced.
func (p *Units) ensureInit() {
	if p.alive == nil {
		p.Init(unitsDefaultCapacity)
	}
}

// IsSliced reports whether the pool was initialized with per-player slices
// [P0-16 §3.1].
func (p *Units) IsSliced() bool {
	if p == nil {
		return false
	}
	return p.sliced
}

// MaxDefs returns the catalog definition count used for slicing, or 0 if
// unsliced [P0-16 §3.1].
func (p *Units) MaxDefs() int {
	if p == nil {
		return 0
	}
	return p.maxDefs
}

// SliceForPlayer returns the inclusive handle bounds for the player's slice
// [P0-16 §3.1]. The second return is false if the pool is unsliced or player
// is out of range 0..9.
func (p *Units) SliceForPlayer(player int) (int, int, bool) {
	if p == nil || !p.sliced || player < 0 || player >= 10 {
		return 0, 0, false
	}
	s := p.slices[player]
	return s.start, s.end, true
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// It is stamped at pool init and retained after free (stale) [P0-16 §3.4];
// slot 0 and OOB return 0.
func (p *Units) SlotIndex(h Handle) uint16 {
	if p == nil || p.slotIndex == nil {
		return 0
	}
	idx := int(h)
	if idx < 0 || idx >= len(p.slotIndex) {
		return 0
	}
	return p.slotIndex[idx]
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P0-16 §2.1].
func (p *Units) DefID(h Handle) uint16 {
	if p == nil || p.defID == nil {
		return 0
	}
	idx := int(h)
	if idx < 0 || idx >= len(p.defID) {
		return 0
	}
	return p.defID[idx]
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P0-16 §3.2]. It does not change alive; callers should keep them consistent.
func (p *Units) SetDefID(h Handle, id uint16) {
	if p == nil || p.defID == nil {
		return
	}
	idx := int(h)
	if idx <= 0 || idx >= len(p.defID) {
		return
	}
	p.defID[idx] = id
}

// Alloc returns the lowest free handle in the global pool, or 0,false if
// exhausted. It scans ascending from 1, matching retail's lowest-free-first
// order [01 §6.1], [04 §2.3]. For sliced pools this is a legacy helper that
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// owning player's slice [P0-16 §3.2] — callers with a player should use
// AllocForPlayer / AllocForPlayerWithDef. Zero RNG draws [P0-16 §5].
func (p *Units) Alloc() (Handle, bool) {
	if p == nil {
		return 0, false
	}
	p.ensureInit()
	// For sliced pools, legacy Alloc scans entire range for test compatibility;
	// retail per-player isolation is via AllocForPlayer.
	for i := 1; i < len(p.alive); i++ {
		if !p.alive[i] && p.defID[i] == 0 {
			p.alive[i] = true
			// defID sentinel: mark occupied with 1 if caller didn't specify.
			// Preserve existing defID if already set (should be 0 here).
			if p.defID[i] == 0 {
				p.defID[i] = 1 // generic occupied sentinel when def unknown
			}
			return Handle(i), true
		}
	}
	return 0, false
}

// AllocForPlayer returns the lowest free handle within the player's slice
// [P0-16 §3.2] with zero RNG draws [P0-16 §5]. It validates the player range
// and per-slice isolation: a slice-full failure is reported even when other
// players have free slots [P0-16 §7.3]. Slot 0 is never returned.
func (p *Units) AllocForPlayer(player int) (Handle, bool) {
	if p == nil {
		return 0, false
	}
	p.ensureInit()
	if !p.sliced {
		return p.Alloc()
	}
	if player < 0 || player >= 10 {
		return 0, false
	}
	s := p.slices[player]
	if s.start <= 0 || s.end < s.start {
		return 0, false
	}
	for i := s.start; i <= s.end && i < len(p.alive); i++ {
		if !p.alive[i] && p.defID[i] == 0 {
			p.alive[i] = true
			if p.defID[i] == 0 {
				p.defID[i] = 1
			}
			return Handle(i), true
		}
	}
	return 0, false
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// limit gate plus lowest-free scan [P0-16 §3.2]. If defID !=0 and
// limitEnabled and limit != -1, it counts occupants in the player's slice
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// with defID. Zero RNG draws [P0-16 §5]. The player is truncated via &0xFF
// in retail; here 0..9 is required.
func (p *Units) AllocForPlayerWithDef(player int, defID uint16, limitEnabled bool, limit int32) (Handle, bool) {
	if p == nil {
		return 0, false
	}
	p.ensureInit()
	if !p.sliced {
		// Legacy global path with limit counted globally
		if defID != 0 && limitEnabled && limit != -1 {
			cnt := 0
			for i := 1; i < len(p.defID); i++ {
				if p.defID[i] == defID {
					cnt++
				}
			}
			if int32(cnt) >= limit {
				return 0, false
			}
		}
		return p.AllocWithDefID(defID)
	}
	if player < 0 || player >= 10 {
		return 0, false
	}
	s := p.slices[player]
	// Per-def limit gate [P0-16 §3.2]
	if defID != 0 && limitEnabled && limit != -1 {
		cnt := 0
		for i := s.start; i <= s.end && i < len(p.defID); i++ {
			if p.defID[i] == defID {
				cnt++
			}
		}
		if int32(cnt) >= limit {
			return 0, false
		}
	}
	for i := s.start; i <= s.end && i < len(p.alive); i++ {
		if !p.alive[i] && p.defID[i] == 0 {
			p.alive[i] = true
			if defID != 0 {
				p.defID[i] = defID
			} else {
				p.defID[i] = 1
			}
			return Handle(i), true
		}
	}
	return 0, false
}

// AllocWithDefID is a legacy global helper that allocates the lowest free
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (p *Units) AllocWithDefID(defID uint16) (Handle, bool) {
	if p == nil {
		return 0, false
	}
	p.ensureInit()
	for i := 1; i < len(p.alive); i++ {
		if !p.alive[i] && p.defID[i] == 0 {
			p.alive[i] = true
			if defID != 0 {
				p.defID[i] = defID
			} else {
				p.defID[i] = 1
			}
			return Handle(i), true
		}
	}
	return 0, false
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// returns NULL [P0-16 §3.2]. It does not check per-def limit; use
// AllocForcedWithDef for the limit-checked variant. Zero RNG draws.
func (p *Units) AllocForced(player int, forced Handle) (Handle, bool) {
	if p == nil || forced == 0 {
		return 0, false
	}
	p.ensureInit()
	if !p.sliced {
		idx := int(forced)
		if idx <= 0 || idx >= len(p.alive) {
			return 0, false
		}
		if p.alive[idx] || p.defID[idx] != 0 {
			return 0, false
		}
		p.alive[idx] = true
		if p.defID[idx] == 0 {
			p.defID[idx] = 1
		}
		return forced, true
	}
	if player < 0 || player >= 10 {
		return 0, false
	}
	s := p.slices[player]
	idx := int(forced)
	if idx < s.start || idx > s.end {
		return 0, false
	}
	if idx <= 0 || idx >= len(p.alive) {
		return 0, false
	}
	if p.alive[idx] || p.defID[idx] != 0 {
		return 0, false
	}
	p.alive[idx] = true
	if p.defID[idx] == 0 {
		p.defID[idx] = 1
	}
	return forced, true
}

// AllocForcedWithDef implements forcedSlot with per-def limit re-check as
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// allocation. Zero RNG draws.
func (p *Units) AllocForcedWithDef(player int, defID uint16, forced Handle, limitEnabled bool, limit int32) (Handle, bool) {
	if p == nil || forced == 0 {
		return 0, false
	}
	p.ensureInit()
	if !p.sliced {
		idx := int(forced)
		if idx <= 0 || idx >= len(p.alive) {
			return 0, false
		}
		if p.alive[idx] || p.defID[idx] != 0 {
			return 0, false
		}
		if defID != 0 && limitEnabled && limit != -1 {
			cnt := 0
			for i := 1; i < len(p.defID); i++ {
				if p.defID[i] == defID {
					cnt++
				}
			}
			if int32(cnt) >= limit {
				return 0, false
			}
		}
		p.alive[idx] = true
		if defID != 0 {
			p.defID[idx] = defID
		} else {
			p.defID[idx] = 1
		}
		return forced, true
	}
	if player < 0 || player >= 10 {
		return 0, false
	}
	s := p.slices[player]
	idx := int(forced)
	if idx < s.start || idx > s.end {
		return 0, false
	}
	if idx <= 0 || idx >= len(p.alive) {
		return 0, false
	}
	if p.alive[idx] || p.defID[idx] != 0 {
		return 0, false
	}
	if defID != 0 && limitEnabled && limit != -1 {
		cnt := 0
		for i := s.start; i <= s.end && i < len(p.defID); i++ {
			if p.defID[i] == defID {
				cnt++
			}
		}
		if int32(cnt) >= limit {
			return 0, false
		}
	}
	p.alive[idx] = true
	if defID != 0 {
		p.defID[idx] = defID
	} else {
		p.defID[idx] = 1
	}
	return forced, true
}

// Free releases the slot identified by h. Slot 0 is ignored and out-of-range
// handles are ignored. Freed slots are immediately reusable [01 §6.1]
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (p *Units) Free(h Handle) {
	if p == nil || p.alive == nil {
		return
	}
	idx := int(h)
	if idx <= 0 || idx >= len(p.alive) {
		return
	}
	// Retain slotIndex stale [P0-16 §3.4]; clear occupancy and alive.
	p.alive[idx] = false
	p.defID[idx] = 0
	// slotIndex[idx] untouched
}

// Alive reports whether h names a currently live unit. Slot 0 is always dead.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// there is no generation, no >cap check beyond slice, so stale reuse aliases
// silently [P0-16 §6].
func (p *Units) Alive(h Handle) bool {
	if p == nil || p.alive == nil {
		return false
	}
	idx := int(h)
	if idx <= 0 || idx >= len(p.alive) {
		return false
	}
	// DefID zero also means free, but alive bool is authoritative for pool.
	return p.alive[idx] && p.defID[idx] != 0
}

// IsAliveRaw reports the raw alive flag without defID gating, for tests that
// need to distinguish occupancy sentinel from alive.
func (p *Units) IsAliveRaw(h Handle) bool {
	if p == nil || p.alive == nil {
		return false
	}
	idx := int(h)
	if idx <= 0 || idx >= len(p.alive) {
		return false
	}
	return p.alive[idx]
}

// Capacity returns the number of usable slots (excluding the null sentinel).
func (p *Units) Capacity() int {
	if p == nil || p.alive == nil {
		return 0
	}
	return len(p.alive) - 1
}

// Used returns the number of currently allocated slots.
func (p *Units) Used() int {
	if p == nil || p.alive == nil {
		return 0
	}
	n := 0
	for i := 1; i < len(p.alive); i++ {
		if p.alive[i] && p.defID[i] != 0 {
			n++
		}
	}
	return n
}

// TotalRecords returns the total record count including slot 0 [P0-16 §3.1].
func (p *Units) TotalRecords() int {
	if p == nil || p.alive == nil {
		return 0
	}
	return len(p.alive)
}

// CountInSlice counts occupants with matching defID in the player's slice;
// defID 0 counts all occupants. Zero RNG.
func (p *Units) CountInSlice(player int, defID uint16) int {
	if p == nil || p.defID == nil {
		return 0
	}
	if !p.sliced {
		cnt := 0
		for i := 1; i < len(p.defID); i++ {
			if p.defID[i] != 0 && (defID == 0 || p.defID[i] == defID) {
				cnt++
			}
		}
		return cnt
	}
	if player < 0 || player >= 10 {
		return 0
	}
	s := p.slices[player]
	cnt := 0
	for i := s.start; i <= s.end && i < len(p.defID); i++ {
		if p.defID[i] != 0 && (defID == 0 || p.defID[i] == defID) {
			cnt++
		}
	}
	return cnt
}

// CobThreads models the per-unit COB VM thread mask. Each live unit has eight
// 164-byte thread records [01 §6.1]; allocation picks the lowest clear mask
// bit and clearing the bit frees the thread [04 §5.x]. This helper is the
// deterministic mask primitive; the full VM lives in internal/cob.
type CobThreads struct {
	mask uint8 // bit set = active
}

// AllocThread returns the lowest free thread index 0..7, or -1 when full.
// It sets the bit, matching retail's lowest-clear-bit scan [01 §6.1].
func (c *CobThreads) AllocThread() (int, bool) {
	if c == nil {
		return -1, false
	}
	for i := 0; i < 8; i++ {
		bit := uint8(1 << uint(i))
		if c.mask&bit == 0 {
			c.mask |= bit
			return i, true
		}
	}
	return -1, false
}

// FreeThread clears the bit for idx 0..7. Out-of-range indices are ignored.
func (c *CobThreads) FreeThread(idx int) {
	if c == nil || idx < 0 || idx >= 8 {
		return
	}
	c.mask &^= uint8(1 << uint(idx))
}

// ActiveMask returns the raw thread mask for debugging.
func (c *CobThreads) ActiveMask() uint8 {
	if c == nil {
		return 0
	}
	return c.mask
}

// Active reports whether thread idx is active.
func (c *CobThreads) Active(idx int) bool {
	if c == nil || idx < 0 || idx >= 8 {
		return false
	}
	return c.mask&(1<<uint(idx)) != 0
}

// ---------------------------------------------------------------------------
// Projectiles
// ---------------------------------------------------------------------------

// Projectiles is the 300-record projectile pool [01 §6.1], [06 §5.1].
// Each record is 107 bytes in the executable; here we model only the
// allocation metadata that is authoritative for determinism: the packed
// active span count, the dead flag, and stable order. Allocation appends at
// the tail and never fills holes [01 §6.1]. Retirement sets a dead flag
// without decrementing the count [06 §5.1]. Compaction is stable, runs at
// the projectile-phase tail, includes records appended during the phase
// [01 §6.2], [06 §5.2], preserves survivor order, and repairs the follow-
// camera link [01 §6.1], [06 §5.2].
type Projectiles struct {
	count int
	dead  [ProjectileCapacity]bool
	// payload holds an optional cookie for order-preservation diagnostics
	// (e.g., a shooter ID). It is not authoritative but is moved together with
	// the dead flag during compaction so that external tests can verify stable
	// order when they write via direct field access in the same package.
	payload [ProjectileCapacity]int
}

// Reserve appends a projectile record at the active-span tail [06 §5.1],
// [01 §6.1]. It returns a non-zero handle (1..count) on success. When the
// count is already at capacity (300) it fails even if earlier holes exist,
// because dead records still consume capacity until compaction [06 §5.1].
func (p *Projectiles) Reserve() (Handle, bool) {
	if p == nil || p.count >= ProjectileCapacity {
		return 0, false
	}
	h := Handle(p.count + 1) // 1-indexed; 0 is null
	p.dead[p.count] = false
	// payload at p.count is left as-is (zero) for the caller to fill;
	// Reserve clears the dead flag so a reused tail slot does not inherit it.
	p.count++
	return h, true
}

// CancelReserve rolls back the most recent Reserve when the ballistic solver
// finds no solution after an early reservation. Retail never reserved for that
// case, so the count must not leak; the vel0 #DE path keeps the leak per
// P0-10 [06 §6.4] I11 and does not call this.
func (p *Projectiles) CancelReserve(h Handle) bool {
	if p == nil || p.count == 0 {
		return false
	}
	if int(h) != p.count {
		return false
	}
	p.count--
	p.dead[p.count] = false
	p.payload[p.count] = 0
	return true
}

// MarkDead sets the dead flag for h without changing the active-span count
// [06 §5.1]. Out-of-range and null handles are ignored. Already-dead handles
// are idempotent.
func (p *Projectiles) MarkDead(h Handle) {
	if p == nil || h == 0 {
		return
	}
	idx := int(h) - 1
	if idx < 0 || idx >= p.count {
		return
	}
	p.dead[idx] = true
}

// IsDead reports whether the named handle is marked dead. It is a test aid
// and does not affect the compaction contract; null and out-of-range handles
// return false.
func (p *Projectiles) IsDead(h Handle) bool {
	if p == nil || h == 0 {
		return false
	}
	idx := int(h) - 1
	if idx < 0 || idx >= p.count {
		return false
	}
	return p.dead[idx]
}

// Alive reports whether h is a live (non-dead) projectile within the active span.
func (p *Projectiles) Alive(h Handle) bool {
	if p == nil || h == 0 {
		return false
	}
	idx := int(h) - 1
	if idx < 0 || idx >= p.count {
		return false
	}
	return !p.dead[idx]
}

// Count returns the active-span length, which includes dead records until
// Compact runs [06 §5.1].
func (p *Projectiles) Count() int {
	if p == nil {
		return 0
	}
	return p.count
}

// SetPayload / Payload are test aids that allow a caller to tag a projectile
// with an integer cookie and verify stable order after compaction. They are not
// part of the retail contract and are ignored by the compaction link repair
// except that the cookie moves with its record when the record is slid.
func (p *Projectiles) SetPayload(h Handle, v int) {
	if p == nil || h == 0 {
		return
	}
	idx := int(h) - 1
	if idx < 0 || idx >= p.count {
		return
	}
	p.payload[idx] = v
}

// Payload returns the cookie stored for h, and whether h is in range.
func (p *Projectiles) Payload(h Handle) (int, bool) {
	if p == nil || h == 0 {
		return 0, false
	}
	idx := int(h) - 1
	if idx < 0 || idx >= p.count {
		return 0, false
	}
	return p.payload[idx], true
}

// Compact performs the stable tail compaction described in [06 §5.2] and
// [01 §6.2]. It scans the current global count (including clones appended
// during the projectile-phase scan per [01 §6.2]), finds the first dead hole,
// copies later survivors downward preserving relative order, and publishes the
// reduced count only after the scan. It repairs the follow-camera link when
// the followed survivor moves [01 §6.1]. Records before the first hole are
// never moved, matching retail; the follow pointer has no such restriction
// because it is not a projectile record [06 §5.2] (supported inference note
// on repair eligibility is for projectile-to-projectile links, implemented in
// the combat pool; this generic pool repairs only the follow link).
//
// The mapping from old index to new index is captured before any copy, and the
// follow handle is rewritten only when a live record carrying the old target
// index exists after compaction [06 §5.2]. If the followed target was removed
// the follow is cleared to null (0).
func (p *Projectiles) Compact(follow *Handle) {
	if p == nil || p.count == 0 {
		return
	}
	oldCount := p.count

	// Build old->new index map for survivors. Use a slice indexed by old
	// position; -1 means the old record was dead and has no survivor.
	oldToNew := make([]int, oldCount)
	for i := range oldToNew {
		oldToNew[i] = -1
	}
	dest := 0
	for i := 0; i < oldCount; i++ {
		if p.dead[i] {
			continue
		}
		oldToNew[i] = dest
		dest++
	}
	newCount := dest
	if newCount == oldCount {
		// No holes; no compaction needed, but still nothing to repair.
		return
	}

	// Stable slide: compact both dead flags and payloads.
	dest = 0
	for i := 0; i < oldCount; i++ {
		if p.dead[i] {
			continue
		}
		if dest != i {
			p.dead[dest] = p.dead[i] // always false
			p.payload[dest] = p.payload[i]
		}
		dest++
	}
	for i := newCount; i < oldCount; i++ {
		p.dead[i] = false
		p.payload[i] = 0
	}
	p.count = newCount

	if follow == nil || *follow == 0 {
		return
	}
	oldHandle := *follow
	oldIdx := int(oldHandle) - 1
	if oldIdx < 0 || oldIdx >= oldCount {
		return
	}
	newIdx := oldToNew[oldIdx]
	if newIdx >= 0 {
		*follow = Handle(newIdx + 1)
	} else {
		// Target was dead and removed; clear the follow link [01 §6.1].
		*follow = 0
	}
}
