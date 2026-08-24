// Package pool implements the fixed-capacity deterministic pools that underlie
// the simulation. Retail's pool behaviour is load-bearing for determinism:
// iteration order, allocation order, and compaction order are part of the
// contract [01 §6.1], [01 §6.2].
package pool

// Handle is a pool slot index. Slot 0 is the null sentinel and is never
// allocated. There are no generation bits; a stale handle that has been freed
// and reused aliases the new occupant [04 §2.3], [06 §5.1], [01 §6.1].
type Handle uint16

const (
	// ProjectileCapacity is the fixed retail capacity per [01 §6.1] and
	// [06 §5.1]: exactly 300 records of 107 bytes. Allocation appends at the
	// tail and never fills holes until compaction [01 §6.1].
	ProjectileCapacity = 300

	// unitsDefaultCapacity is the fallback capacity used when a Units pool is
	// used without an explicit Init. The retail unit maximum is a runtime
	// map-state limit, not a single universal constant [01 §6.1], [04 §2.3]
	// ("The exact maximum unit count is not resolved"). This placeholder is
	// configurable via Init / NewUnits; it is not a retail constant.
	unitsDefaultCapacity = 500
)

// Units is the fixed pool of 280-byte unit records [01 §6.1].
// Allocation scans for the lowest free slot with slot 0 reserved as null
// [04 §2.3], [01 §6.1]. Freed slots are immediately reusable.
type Units struct {
	alive []bool // index 0 is sentinel, never allocated; len = capacity+1
}

// NewUnits creates a Units pool with the given capacity. Capacity is the
// number of usable slots; slot 0 remains null [04 §2.3].
func NewUnits(capacity int) *Units {
	u := &Units{}
	u.Init(capacity)
	return u
}

// Init configures the pool capacity. If capacity <= 0 the pool becomes empty
// and Alloc always fails. Slot 0 is reserved as null.
func (p *Units) Init(capacity int) {
	if capacity < 0 {
		capacity = 0
	}
	p.alive = make([]bool, capacity+1)
}

// ensureInit lazily initializes a zero-value Units so that a simple
// var u Units; u.Alloc() sequence does not panic and provides a usable
// placeholder. The retail maximum is not a fixed constant [01 §6.1], so this
// default is configurable via Init.
func (p *Units) ensureInit() {
	if p.alive == nil {
		p.Init(unitsDefaultCapacity)
	}
}

// Alloc returns the lowest free handle, or 0,false if the pool is exhausted.
// It scans ascending from 1, matching retail's lowest-free-first order
// [01 §6.1], [04 §2.3].
func (p *Units) Alloc() (Handle, bool) {
	if p == nil {
		return 0, false
	}
	p.ensureInit()
	for i := 1; i < len(p.alive); i++ {
		if !p.alive[i] {
			p.alive[i] = true
			return Handle(i), true
		}
	}
	return 0, false
}

// Free releases the slot identified by h. Slot 0 is ignored and out-of-range
// handles are ignored. Freed slots are immediately reusable [01 §6.1].
func (p *Units) Free(h Handle) {
	if p == nil || p.alive == nil {
		return
	}
	idx := int(h)
	if idx <= 0 || idx >= len(p.alive) {
		return
	}
	p.alive[idx] = false
}

// Alive reports whether h names a currently live unit. Slot 0 is always dead.
func (p *Units) Alive(h Handle) bool {
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
		if p.alive[i] {
			n++
		}
	}
	return n
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
