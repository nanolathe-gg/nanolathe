package pool

import (
	"fmt"
)

// Handle is a pool slot index. Slot 0 is the null sentinel and is never
// allocated. There are no generation bits; a stale handle that has been freed
// and reused aliases the new occupant [04 §2.3], [06 §5.1], [01 §6.1], [P0-16].
type Handle uint16

const (
	// PlayerCount is the fixed number of player slots in a battle [04 §2.1].
	PlayerCount = 10

	// ProjectileCapacity is the fixed retail capacity per [01 §6.1] and
	// [06 §5.1]: exactly 300 records of 107 bytes. Allocation appends at the
	// tail and never fills holes until compaction [01 §6.1].
	ProjectileCapacity = 300
)

// PlayerPermutation lists the logical player slots in the order used for
// assigning unit-pool slices. The element at index i owns slice i; every value
// from 0 through 9 must occur exactly once [04 §2.1][R-P0-16-A].
type PlayerPermutation [PlayerCount]uint8

// IdentityPlayerPermutation is the non-mode-3 order. It is also the stable
// starting order for the mode-3 insertion sort [R-P0-16-A].
func IdentityPlayerPermutation() PlayerPermutation {
	var order PlayerPermutation
	for i := range order {
		order[i] = uint8(i)
	}
	return order
}

// ValidatePlayerPermutation rejects anything other than a total permutation
// of the ten logical player slots. Validation runs before pool allocation so a
// malformed battle entry cannot leave a partially initialized pool.
func ValidatePlayerPermutation(order PlayerPermutation) error {
	var seen [PlayerCount]bool
	for _, player := range order {
		if player >= PlayerCount {
			return fmt.Errorf("pool: player permutation value %d out of range", player)
		}
		if seen[player] {
			return fmt.Errorf("pool: player permutation repeats %d", player)
		}
		seen[player] = true
	}
	return nil
}

// PlayerPermutationForMode computes the pool slice order at battle entry.
// Retail compares the fixed player-record order by the record's 32-bit sort
// key only in mission mode 3; all other modes use the original slot order.
// Equal keys retain slot order because the insertion loop moves an element
// only when its key is strictly less than the preceding key [R-P0-16-A].
func PlayerPermutationForMode(mode int, sortKeys [PlayerCount]uint32) PlayerPermutation {
	order := IdentityPlayerPermutation()
	if mode != 3 {
		return order
	}
	// Keep this fixed-size insertion sort explicit: retail's comparison is an
	// unsigned strict-less test, and equal keys therefore remain stable.
	for i := 1; i < len(order); i++ {
		player := order[i]
		j := i
		for j > 0 && sortKeys[player] < sortKeys[order[j-1]] {
			order[j] = order[j-1]
			j--
		}
		order[j] = player
	}
	return order
}

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

// Units is the fixed pool of unit records [01 §6.1] [P0-16]. The pool is
// always sliced per player: capacity is the game value derived from the
// definition count (maxDefs*10+1 records), each player owning maxDefs slots
// via the sorted player order [P0-16 §3.1]. Allocation scans the owning
// player's slice for the lowest free slot with slot 0 reserved as null
// [04 §2.3], [01 §6.1], and reuses it immediately [P0-16 §3.2]. Freed slots
// carry no generation tag; stale handles alias the new occupant [P0-16]
// [06 §5.1]. Forced-slot verification (save reconstruction) is constrained
// to the owning player's slice [P0-16 §3.3].
// [P2-03] Allocator failure: nil return, zero RNG draws.
//
// There is no retail zero-fill byte count to match, and the question of one was
// the wrong question (marker retired 2026-09-04, WU-19-155). [01 §6] is
// explicit and corrected: the allocation helpers "do not zero or pattern-fill
// by default" — no fill happens at all unless the `-memset` diagnostic switch is
// present — and callers initialize the blocks they receive. So a reused record's
// residue is decided entirely by which fields the unit constructor writes, and
// that is `internal/units`' contract, not this pool's: doc 04 names those writes
// one at a time (creation zeroes the first state byte and the low nibble of the
// next; the constructor zeroes the pending word; and so on). This pool stores
// only the three words it owns — alive, defID and slotIndex — and its behavior
// already matches: Free clears alive and defID and deliberately RETAINS the
// stale slotIndex [P0-16 §3.4], which is residue reproduced rather than
// scrubbed. There is no "remainder" here to have a policy about.
type Units struct {
	alive     []bool   // index 0 is sentinel, never allocated; len = totalRecords
	defID     []uint16 // occupancy identity per slot, 0 = free [P0-16 §2.1]
	slotIndex []uint16 // slot number stamped at init, retained stale after free [P0-16 §3.4]
	// used mirrors the count Used() would compute by scanning. The pool is
	// sized at maxDefs*10+1 records — several thousand for a retail catalog —
	// so the scan cost is set by the catalog, not by how many units exist, and
	// the per-tick caller paid it in full on a battle with fifty units. Alloc
	// and Free are the only writers of alive/defID, and each sets or clears
	// both together, so the maintained count is exactly the scan's answer.
	used    int
	maxDefs int
	slices  [10]struct{ start, end int } // inclusive per-player bounds [P0-16 §3.1]
	sliced  bool
}

// NewUnitsSliced creates a sliced retail pool for maxDefs catalog
// definitions: total records = maxDefs*10+1, per-player slices of maxDefs
// each via sorted player order [P0-16 §3.1] [01 §6.1]. This is the only
// production shape: retail's unit pool is always sliced per player, and
// slot 0 is the null sentinel.
func NewUnitsSliced(maxDefs int) *Units {
	u := &Units{}
	_ = u.InitSlicedWithOrder(maxDefs, IdentityPlayerPermutation())
	return u
}

// NewUnitsSlicedWithOrder creates a sliced unit pool after validating the
// battle-entry player permutation [R-P0-16-A].
func NewUnitsSlicedWithOrder(maxDefs int, order PlayerPermutation) (*Units, error) {
	u := &Units{}
	if err := u.InitSlicedWithOrder(maxDefs, order); err != nil {
		return nil, err
	}
	return u, nil
}

// InitSliced configures a retail-sliced pool for maxDefs definitions.
// It allocates total = maxDefs*10+1 records (including slot 0). Per-player
// slices hold maxDefs each; allocation scans for the lowest free slot per
// slice with immediate reuse [P0-16 §3.2]. This identity wrapper uses
// identity order; production battle entry calls InitSlicedWithOrder after
// applying the retail mode comparator [R-P0-16-A].
func (p *Units) InitSliced(maxDefs int) {
	_ = p.InitSlicedWithOrder(maxDefs, IdentityPlayerPermutation())
}

// InitSlicedWithOrder configures a retail-sliced pool using the already
// computed battle-entry player order. It validates before changing receiver
// state, and never sorts or remaps slices after initialization [R-P0-16-A].
func (p *Units) InitSlicedWithOrder(maxDefs int, order PlayerPermutation) error {
	if err := ValidatePlayerPermutation(order); err != nil {
		return err
	}
	if maxDefs < 0 {
		maxDefs = 0
	}
	total := CapacityForDefs(maxDefs) // includes slot 0
	if total < 1 {
		total = 1
	}
	p.alive = make([]bool, total)
	p.defID = make([]uint16, total)
	p.used = 0
	p.slotIndex = make([]uint16, total)
	for i := 0; i < total; i++ {
		p.slotIndex[i] = uint16(i) // slot index stamped at init, retained after free [P0-16 §3.4]
	}
	p.maxDefs = maxDefs
	p.sliced = maxDefs > 0
	if p.sliced {
		for sortedIdx, playerValue := range order {
			player := int(playerValue)
			start := maxDefs*sortedIdx + 1
			end := maxDefs * (sortedIdx + 1)
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
	return nil
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

// SlotIndex returns the slot number stamped at init for the handle
// [P0-16 §3.4]. It is stamped at pool init and retained after free (stale)
// [P0-16 §3.4]; slot 0 and OOB return 0.
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

// DefID returns the occupancy definition identity for the slot. 0 means free
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

// AllocForPlayerWithDef is the canonical per-player allocator [P0-16 §3.2]:
// the sole allocation site for every creation path [01 §6.1]. If
// limitEnabled and limit != -1, it first counts occupants in the player's
// slice with the given definition identity and fails when limit <= cnt
// [P0-16 §2.1]. Otherwise it scans the owning player's slice for the lowest
// free slot (definition identity clear, alive flag clear) and marks it
// occupied with that identity; freed slots are reused immediately [P0-16
// §3.2]. A slice-full failure is reported even when other players have free
// slots [P0-16 §7.3]. The caller always supplies a real definition identity —
// retail's allocator never allocates without one, and identity 0 is the free
// sentinel, so a zero identity fails without allocating. Slot 0 is never
// returned. Zero RNG draws [P0-16 §5].
func (p *Units) AllocForPlayerWithDef(player int, defID uint16, limitEnabled bool, limit int32) (Handle, bool) {
	if p == nil || defID == 0 {
		return 0, false
	}
	if player < 0 || player >= 10 {
		return 0, false
	}
	s := p.slices[player]
	if s.start <= 0 || s.end < s.start {
		return 0, false
	}
	// Per-def limit gate [P0-16 §3.2]
	if limitEnabled && limit != -1 {
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
			p.defID[i] = defID
			p.used++
			return Handle(i), true
		}
	}
	return 0, false
}

// AllocForcedWithDef implements the save/reconstructor forced-slot path:
// the candidate slot is validated against the owning player's slice bounds
// and free occupancy, the per-def limit is re-checked, and only then is the
// slot allocated [P0-16 §3.2][P0-16 §3.3]. Out-of-slice, occupied, or
// limit-exceeded candidates return failure with no allocation. Zero RNG
// draws.
func (p *Units) AllocForcedWithDef(player int, defID uint16, forced Handle, limitEnabled bool, limit int32) (Handle, bool) {
	if p == nil || defID == 0 || forced == 0 {
		return 0, false
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
	if limitEnabled && limit != -1 {
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
	p.defID[idx] = defID
	p.used++
	return forced, true
}

// Free releases the slot identified by h. Slot 0 is ignored and out-of-range
// handles are ignored. Freed slots are immediately reusable [01 §6.1]
// [P0-16 §3.4]. Retail's free clears the occupancy identity, the alive mask,
// the per-unit heaps, order queues, attachments, and the per-player live
// counter; here the pool-level equivalents are cleared (occupancy identity
// and alive flag) and the slot number is RETAINED stale for save/reconstructor
// forced-slot identity [P0-16 §3.4]. Zero RNG draws.
func (p *Units) Free(h Handle) {
	if p == nil || p.alive == nil {
		return
	}
	idx := int(h)
	if idx <= 0 || idx >= len(p.alive) {
		return
	}
	// Retain slotIndex stale [P0-16 §3.4]; clear occupancy and alive. A free
	// of an already-free slot is a no-op for the count, exactly as it is for
	// the flags.
	if p.alive[idx] && p.defID[idx] != 0 {
		p.used--
	}
	p.alive[idx] = false
	p.defID[idx] = 0
	// slotIndex[idx] untouched
}

// Alive reports whether h names a currently live unit. Slot 0 is always dead.
// Validation is only slot nonzero and alive-flag set [P0-16 §3.2][P0-16 §6];
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

// Capacity returns the number of usable slots (excluding the null sentinel).
func (p *Units) Capacity() int {
	if p == nil || p.alive == nil {
		return 0
	}
	return len(p.alive) - 1
}

// Used returns the number of currently allocated slots. It is the maintained
// count, not a scan; countUsed is the scan the count mirrors and exists so a
// test can hold the two against each other.
func (p *Units) Used() int {
	if p == nil || p.alive == nil {
		return 0
	}
	return p.used
}

// countUsed recomputes the allocated-slot count by scanning. Production reads
// Used; this is the reference the maintained count is tested against.
func (p *Units) countUsed() int {
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
