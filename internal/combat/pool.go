package combat

import (
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// ProjectileCapacity is the fixed retail capacity [01 §6.1], [06 §5.1]: exactly
// 300 records of 107 bytes. Allocation appends at the tail and never fills
// holes until compaction (I5).
const ProjectileCapacity = pool.ProjectileCapacity

// NeutralSide is the shooter-less side byte [06 §6.5]: meteors spawned through
// the null-shooter path carry it so their explosions credit nobody. Side 0 is
// a real side.
const NeutralSide uint8 = 10

// Vec3 is a fixed-point world position (16.16 per component, I2) [03 §2.1].
type Vec3 struct {
	X numeric.Fixed
	Y numeric.Fixed
	Z numeric.Fixed
}

// Projectile is the named Go record parallel to pool.Projectiles metadata.
// Retail stores 300 records of 107 bytes [01 §6.1], [06 §5.1]; Go keeps named
// fields in a slot-indexed array parallel to the pool (I13). The pool is the
// sole allocation/dead/count authority (I5); this array holds the per-record
// simulation state and the compaction marker link.
// Field list transcribed from [06 §5.1] and [06 §6.1]; the single established
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
type Projectile struct {
	// Weapon definition [06 §5.1] "weapon definition"; [06 §6.1] "weapon definition and owner side".
	WeaponID int32

	// Current/head point and second tail/start/waypoint point [06 §6.1] "current/head point and a second tail/waypoint/start point"; [06 §5.1] "current and start positions".
	Pos      Vec3 // current/head point [06 §6.1]
	StartPos Vec3 // second point (tail/start/waypoint) [06 §6.1]

	// Stored target point [06 §6.1] "stored target point"; target position is optional per [06 §5.1].
	TargetPos Vec3

	// Retained unit target [06 §6.1] "retained unit target"; zero is null, no generation token [06 §5.1].
	TargetUnit pool.Handle // 0=null [06 §5.1]

	// Optional projectile-to-projectile link [06 §6.1] "separate optional projectile-to-projectile link"
	// — follower references (e.g., interceptor reservation) repaired at compaction [06 §5.2].
	TargetProjectile pool.Handle // 0=null; rewritten only for moved sources whose target survives as live marker [06 §5.2]

	// Velocity and speed [06 §5.1] "velocity and speed"; [06 §6.1] "velocity, yaw, pitch, and scalar speed".
	Velocity Vec3
	Speed    numeric.Fixed

	// Orientation: yaw/pitch are circular 16-bit angles, 65536 per circle (I2) [04 §5.1].
	Yaw   numeric.Angle // [06 §5.1] [06 §6.1]
	Pitch numeric.Angle // [06 §5.1] [06 §6.1]

	// Shooter and shooter side [06 §5.1]; owner side [06 §6.1].
	Shooter     pool.Handle // shooter unit slot [06 §6.1] "shooter"
	ShooterSide uint8       // owner side byte [06 §6.1]; NeutralSide (10) for shooter-less records [06 §6.5]

	// Muzzle-piece identity [06 §5.1] "muzzle-piece identity"; firing piece [06 §6.1] "firing piece, and shooter".
	MuzzlePiece int16

	// Timing: creation, expiry, smoke, burst deadlines [06 §5.1] "burst deadline and remaining count; expiry and smoke deadlines"; [06 §6.1] "creation, expiry, and smoke deadlines; burst remaining".
	CreationTick   uint32 // creation tick [06 §6.1]
	BurstDeadline  uint32 // burst deadline [06 §5.1]
	BurstRemaining int32  // burst remaining count [06 §5.1] [06 §6.1]
	ExpiryTick     uint32 // expiry [06 §6.1]
	SmokeDeadline  uint32 // smoke deadline [06 §5.1]

	// Phase/latch flags [06 §5.1] "phase/latch flags"; visual propeller orientation, beam latch, two-phase state [06 §6.1].
	BeamLatch    bool          // beam latch [06 §6.1]
	TwoPhase     bool          // two-phase state [06 §6.1]
	Dead         bool          // dead state [06 §5.1] — shadow; authoritative flag lives in Slots (I5)
	PropellerYaw numeric.Angle // visual propeller orientation [06 §6.1]
	MeteorPitch  numeric.Angle // meteor visual pitch accumulator, advanced with yaw [06 §6.5]

	// Collision cache [06 §5.1] "collision cache values" — quantized cell pair suppressing repeated feature contact [06 §8.1].
	CacheCellX int32
	CacheCellZ int32

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// collision after linked test as average of two cell height bytes; no reader
	// found in bounded 1326 TU (NEGATIVE-BOUNDED) — preserve write for parity
	// but no gameplay effect [P1-08 §2.5].
	Scratch5E int16 // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// bits 0x30 (0x10|0x20) two-phase state [P1-08 §2.8] [06 §6.6]. Go bool fields
	// mirror bits; raw byte kept for exact replay.
	State69 uint8 // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Compaction writes each original record's old pool index into this field
	// before copying survivors downward; the second repair pass searches live
	// records for this marker to rewrite moved follower links [06 §5.2].
	OldMarker int16 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// EventKind identifies authoritative combat-to-event records.
// Values are intentionally local to combat; the session adapter translates
// them to frame.EventKind without allowing the renderer into simulation
// [I6][06 §13.2].
type EventKind uint8

const (
	EventShake EventKind = iota + 1
	EventHitSound
	EventWaterSound
	EventEndSmoke
	EventExplosion
	EventWaterExplosion
	EventProjectileImpact
	EventUnitKilled
	EventCorpse
)

// Event is an immutable combat event emitted in simulation order. Graphic and
// Sound are authored identities; empty values remain empty rather than being
// replaced with guessed assets [06 §13.2].
type Event struct {
	Kind     EventKind
	Tick     uint32
	Source   pool.Handle
	Target   pool.Handle
	Position Vec3
	Sound    string
	// Graphic and Bank are the two halves of one authored art identity: the
	// GAF entry name and the bank that holds it. A weapon's explosion art is
	// `explosionart` inside `explosiongaf`, and BOTH keys must be present or
	// the holder stays null and the impact draws no art [06 R-WFX-01 §1].
	// Publishing one string for both was a defect: the two names come from
	// different keys and neither substitutes for the other.
	Graphic   string
	Bank      string
	Magnitude int32
	Duration  int32

	// HasCalculatedFlash and CalculatedTable carry the explosion pool's
	// secondary cursor: the procedurally generated disc every impact draws
	// under its art, whether or not it has any [06 R-WFX-01 §2].
	HasCalculatedFlash bool
	CalculatedTable    uint8

	// Smoke carries the weapon's start-smoke flag on the explosion events:
	// the land/water/lava impact effect variants each append a strip-9
	// smoke object under that second weapon flag [R-STRIP-01 §1 strip 9].
	Smoke bool
}

// pendingKey identifies a per-unit weapon slot pending Aim ON-04 [06 §3.3].
type pendingKey struct {
	Unit pool.Handle
	Slot int
}

// pendingAim tracks a dispatched Aim thread awaiting explicit return ON-04 [06 §3.3].
type pendingAim struct {
	ThreadIdx      int
	DispatchedTick uint32
}

// Service is the projectile pool owner. Slots (pool.Projectiles) is the sole
// allocation/dead/count authority per I5: Reserve appends at the active-span
// tail and never fills holes; MarkDead sets a flag without decrementing count;
// Compact is stable and runs at the projectile-phase tail including clones
// appended during the scan [01 §6.1], [01 §6.2], [06 §5.1], [06 §5.2].
// Records is the parallel named storage (107-byte retail identity, I13) moved
// identically to the metadata on compaction.
type Service struct {
	Slots   pool.Projectiles               // sole count/dead authority (I5) [06 §5.1]
	Records [ProjectileCapacity]Projectile // named records parallel to Slots

	Events        func(Event)               // optional ordered combat event sink; nil-safe
	pendingAims   map[pendingKey]pendingAim // Aim dispatch tracking ON-04 [06 §3.3]
	deathNotified map[pool.Handle]*units.Unit

	// Visibility is the per-session LOS predicate [03 §3.2] C8 [RS-P0-018].
	// Moved from package-global combat.VisibilityHook to per-Service field for session isolation [INVARIANTS I1][I6][RS-P0-018].
	Visibility func(viewer visibility.PlayerID, target visibility.Target) bool `json:"-"`

	// ControlByte reads the player slot's control byte, the operand of the
	// damage-intake gates of [06 R-DMG-01 §8]. The session binds it to the
	// authoritative player record; ControlByteAbsent means the slot named has
	// no record. Read it through PlayerControlByteFor, never directly.
	ControlByte func(owner uint8) uint8 `json:"-"`

	// Reaction binds the damage-intake reaction routine's seams [06 §9.1] step
	// 4. The session installs it at composition; with none installed the
	// routine's four parts are no-ops. See ReactionSeams in damage.go.
	Reaction *ReactionSeams `json:"-"`

	// Features is the feature runtime the area walk of [06 §9.3] hands its
	// accepted feature candidates to. Every cell inside a blast offers one, and
	// the entry it reaches is the feature damage of [06 §13.1] / [05 R-FEAT-01
	// §8] — the ignition test and the two accumulators. The session installs it
	// at composition; with none installed a blast reaches units only, which is
	// what every fixture that does not compose a session gets.
	Features *features.Service `json:"-"`
}

// Reserve appends a projectile record at the active-span tail [06 §5.1], [01 §6.1].
// It returns a non-zero handle (1..Count) on success; when the count is already
// at capacity it fails even if earlier holes exist because dead records still
// consume capacity until Compact [06 §5.1].
func (s *Service) Reserve() (pool.Handle, bool) {
	if s == nil {
		return 0, false
	}
	h, ok := s.Slots.Reserve()
	if !ok {
		return 0, false
	}
	idx := int(h) - 1
	if idx >= 0 && idx < len(s.Records) {
		s.Records[idx] = Projectile{} // TODO(T23): exact allocator zero-fill byte count for 107-byte projectile record not traced [01 §6.1][GAP T13]; Go zero-initializes the struct
	}
	return h, true
}

// CancelReserve rolls back an early ballistic reservation when the solver finds
// no solution. Retail did not reserve for that admission failure [06 §3.3],
// so the count must not leak. The vel0 #DE path keeps the leak per P0-10
// [06 §6.4] I11 and never calls this.
func (s *Service) CancelReserve(h pool.Handle) bool {
	if s == nil {
		return false
	}
	ok := s.Slots.CancelReserve(h)
	if ok {
		idx := int(h) - 1
		if idx >= 0 && idx < len(s.Records) {
			s.Records[idx] = Projectile{}
		}
	}
	return ok
}

// MarkDead sets the dead flag for h without changing the active-span count
// [06 §5.1]. The flag lives in Slots; the shadow copy in Records is updated
// for convenience but is not authoritative.
func (s *Service) MarkDead(h pool.Handle) {
	if s == nil {
		return
	}
	s.Slots.MarkDead(h)
	if h == 0 {
		return
	}
	idx := int(h) - 1
	if idx < 0 || idx >= len(s.Records) {
		return
	}
	if idx < s.Slots.Count() || s.Slots.Count() == 0 {
		// Only mark shadow if within current span; out-of-span handles are ignored by Slots anyway.
		s.Records[idx].Dead = true
	}
}

// Count returns the active-span length which includes dead records until Compact [06 §5.1].
func (s *Service) Count() int {
	if s == nil {
		return 0
	}
	return s.Slots.Count()
}

// Alive reports whether h is a live (non-dead) record within the active span.
func (s *Service) Alive(h pool.Handle) bool {
	if s == nil {
		return false
	}
	return s.Slots.Alive(h)
}

// IsDead reports whether the handle is marked dead.
func (s *Service) IsDead(h pool.Handle) bool {
	if s == nil {
		return false
	}
	return s.Slots.IsDead(h)
}

// ForEachAliveInEntrySpan iterates alive projectiles within the count captured
// once at entry [01 §6.2], [06 §5.1]. Clones appended during the scan lie
// outside the captured span and wait for the next phase [06 §5.2]; the tail
// compactor reads the current count and therefore does include them (I1, I5).
func (s *Service) ForEachAliveInEntrySpan(fn func(h pool.Handle, p *Projectile)) {
	if s == nil || fn == nil {
		return
	}
	entry := s.Slots.Count() // capture once at entry [01 §6.2], [06 §5.2]
	for i := 0; i < entry; i++ {
		h := pool.Handle(i + 1)
		if !s.Slots.Alive(h) {
			continue
		}
		fn(h, &s.Records[i])
	}
}

// ForEachInEntrySpanIncludingDead iterates ALL projectiles in the captured
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// vs families even for records whose dead bit was set before their turn
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (s *Service) ForEachInEntrySpanIncludingDead(fn func(h pool.Handle, p *Projectile, isDead bool)) {
	if s == nil || fn == nil {
		return
	}
	entry := s.Slots.Count() // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	for i := 0; i < entry; i++ {
		h := pool.Handle(i + 1)
		isDead := s.Slots.IsDead(h)  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		fn(h, &s.Records[i], isDead) // no dead filter [P1-07][P1-08 §2.2]
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (s *Service) ProjectileLinkAt(h pool.Handle) pool.Handle {
	if s == nil || h == 0 {
		return 0
	}
	idx := int(h) - 1
	if idx < 0 || idx >= len(s.Records) || idx >= s.Slots.Count() {
		return 0
	}
	return s.Records[idx].TargetProjectile // TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// Compact performs the stable tail compaction described in [06 §5.2] and
// [01 §6.2], reproducing the retail procedure verbatim including marker table
// rebuild order (WU-09-2 subtlest contract).
//
// Steps per [06 §5.2]:
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//   - scans current global count including clones appended after entry capture [01 §6.2];
//   - finds first dead hole, copies later survivors downward preserving relative order, publishes reduced count after scan;
//   - updates follow-camera pointer when the followed survivor moves (delegated to Slots.Compact);
//   - for each MOVED survivor carrying a non-null projectile-to-projectile link, records moved source's new index together with target's old index;
//   - second repair pass rewrites the moved source's link ONLY when a live record still carrying the target's old-index marker exists after compaction;
//   - if the linked target was removed, no rewrite happens and the copied source retains the old raw pointer (stale) [06 §5.2];
//   - sources BEFORE the first dead hole are never moved and never enter the repair table, so a link to a target that shifted left stays stale even though target survived [06 §5.2];
//   - nothing is checked at dereference [06 §5.2], [GAP T21].
//
// I5: Slots stays sole alloc/dead/count authority; this method snapshots named
// records + OLD markers, delegates stable metadata move to Slots.Compact, moves
// parallel records identically, then repairs follower links as above.
func (s *Service) Compact(follow *pool.Handle) {
	if s == nil {
		return
	}
	oldCount := s.Slots.Count()
	if oldCount == 0 {
		// pool's Compact also no-ops on zero, but we handle nil follow early.
		s.Slots.Compact(follow)
		return
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	for i := 0; i < oldCount; i++ {
		s.Records[i].OldMarker = int16(i)
	}
	// Snapshot dead flags and old links before any copy [06 §5.2].
	dead := make([]bool, oldCount)
	for i := 0; i < oldCount; i++ {
		dead[i] = s.Slots.IsDead(pool.Handle(i + 1))
	}
	oldLinks := make([]pool.Handle, oldCount)
	for i := 0; i < oldCount; i++ {
		oldLinks[i] = s.Records[i].TargetProjectile
	}
	// Find first dead hole [06 §5.2].
	firstHole := oldCount
	for i := 0; i < oldCount; i++ {
		if dead[i] {
			firstHole = i
			break
		}
	}
	// Build old->new map and newCount (stable order) [06 §5.2].
	oldToNew := make([]int, oldCount)
	for i := range oldToNew {
		oldToNew[i] = -1
	}
	dest := 0
	for i := 0; i < oldCount; i++ {
		if dead[i] {
			continue
		}
		oldToNew[i] = dest
		dest++
	}
	newCount := dest

	// Build repair table for MOVED survivors only [06 §5.2]:
	// "For each moved survivor carrying a non-null projectile-to-projectile link,
	//  compaction records the moved source's index together with the target's old index"
	// Sources BEFORE the first dead hole are never moved and never enter the table [06 §5.2].
	type repairEntry struct {
		srcNew int
		tgtOld int
	}
	var repairs []repairEntry
	if firstHole < oldCount {
		for i := 0; i < oldCount; i++ {
			if dead[i] {
				continue
			}
			newIdx := oldToNew[i]
			if newIdx == i {
				// Unmoved (before first hole) — never enters repair table [06 §5.2].
				continue
			}
			if newIdx < 0 {
				continue
			}
			link := oldLinks[i]
			if link == 0 {
				continue
			}
			tgtOld := int(link) - 1
			// Even if target index is out of oldCount range, we still record;
			// second pass will simply fail to find a marker and leave stale [06 §5.2].
			repairs = append(repairs, repairEntry{srcNew: newIdx, tgtOld: tgtOld})
		}
	}

	// Delegate stable metadata move to Slots, which also repairs follow-camera link [06 §5.2], [01 §6.1].
	// This publishes reduced count only after scan [06 §5.2] and uses current global span including clones [01 §6.2].
	s.Slots.Compact(follow)

	// Move parallel named records identically to Slots' stable slide [06 §5.2].
	// Preserve survivor order [06 §5.2].
	if newCount != oldCount {
		// Stable copy: walk old indices ascending, copy survivors down.
		dest = 0
		for i := 0; i < oldCount; i++ {
			if dead[i] {
				continue
			}
			if dest != i {
				s.Records[dest] = s.Records[i]
			}
			dest++
		}
		// Clear tail records beyond newCount (stale bytes past new active count) [06 §5.2].
		for i := newCount; i < oldCount; i++ {
			s.Records[i] = Projectile{}
		}
		// Dead shadows already cleared via copy; tail is zero.
	}

	// Second repair pass [06 §5.2]: rewrite moved source's link ONLY when a live
	// record still carrying the target's old-index marker exists after compaction.
	// "If the linked target was removed, no repair write happens and the copied
	//  source retains the old raw pointer" [06 §5.2]. Search is over current live
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	for _, e := range repairs {
		// e.srcNew is in [0,newCount). The source record at that slot currently
		// carries oldLink value; we will conditionally rewrite.
		if e.srcNew < 0 || e.srcNew >= newCount {
			continue
		}
		tgtNew := -1
		// Scan live records ascending (stable order) for marker == tgtOld.
		for j := 0; j < newCount; j++ {
			if int(s.Records[j].OldMarker) == e.tgtOld {
				tgtNew = j
				break
			}
		}
		if tgtNew >= 0 {
			s.Records[e.srcNew].TargetProjectile = pool.Handle(tgtNew + 1)
		} else {
			// Leave stale raw pointer [06 §5.2] — retains old handle which may
			// alias a different live record shifted into old address or stale bytes past new count.
			// Do not clear; keep s.Records[e.srcNew].TargetProjectile as oldLinks[?] value already there.
		}
	}
	// Note: nothing is checked at dereference — guidance, proximity, interceptor scans
	// never validate generations, bounds, dead bits, or weapon identity [06 §5.2].
}
