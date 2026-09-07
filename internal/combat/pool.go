package combat

import (
	"github.com/nanolathe/nanolathe/internal/content"
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
// retail field this reproduces by name — the old-index marker — is kept as
// identity via its OldMarker field below [06 §5.2], [GAP T21].
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
	PropellerYaw numeric.Angle // propeller child-roll visual [06 §6.1]
	// Roll is the first orientation-block word. Meteors advance it from their
	// velocity; no non-meteor writer is established, so reservation retains it.
	// TODO(question): census non-meteor writes of this retained roll word.
	Roll        numeric.Angle
	MeteorPitch numeric.Angle // meteor visual pitch accumulator [06 §6.5]

	// Collision cache [06 §5.1] "collision cache values" — the quantized cell
	// pair that suppresses a repeated feature contact [06 §8.1]. Its ONLY writer
	// is the collision gate's feature step; no reservation, creator or common
	// initializer touches it, the pool is zero-filled once at battle start, and
	// compaction copies survivors downward, so a reused record carries its last
	// occupant's pair until this record's first feature contact overwrites it
	// [R-DMG-01 §13].
	CacheCellX int32
	CacheCellZ int32

	// CachedFloorHeight is the record's cached average floor height in whole
	// world units, `(cell.maxHeight + cell.minHeight) / 2` over the plot cell of
	// the post-motion point, written by the collision gate on every in-map tick
	// after the in-map test and before the unit-slot tests [06 §8.1] step 2,
	// [R-DMG-01 §14]. No gameplay test reads it; the projectile draw pass
	// anchors the ground shadow against half of it [03 §5.4], through the
	// committed frame's copy.
	CachedFloorHeight int16

	// State byte [P1-08 §2.8]: bit1 0x02 dead, bit0 0x01 beamLatch,
	// bits 0x30 (0x10|0x20) two-phase state [P1-08 §2.8] [06 §6.6]. Go bool fields
	// mirror bits; raw byte kept for exact replay.
	State69 uint8 // raw state bits: &2 dead, &1 beamLatch, &0x30 two-phase [P1-08 §2.8]

	// Old-index marker written before any copy [06 §5.2], [GAP T21].
	// Compaction writes each original record's old pool index into this field
	// before copying survivors downward; the second repair pass searches live
	// records for this marker to rewrite moved follower links [06 §5.2].
	OldMarker int16 // old-index marker [06 §5.2]
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

	// scanCursor is the autonomous target scan's persistent per-player
	// round-robin cursor [06 §3.2]; service.go owns it. It is per-session state
	// like the two maps above, not configuration.
	scanCursor autonomousScanCursor

	// targets is the per-side target registry of [06 §3.1] — its primary and
	// secondary candidate lists and its secondary-list gate, rebuilt on the
	// 30-tick cadence; service.go owns it. Per-session state, not
	// configuration.
	targets targetRegistry

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

	// OpaqueLiquidMode is the mission's nonzero `nosealeveltrigger` mode.
	// It suppresses terrain-water impacts and downward crossing art when there
	// is no direct unit [06 §8.2][06 §9.1].
	OpaqueLiquidMode bool `json:"-"`

	// weaponByID is the catalog's per-identifier weapon lookup, bound once per
	// catalog. Binding a method value allocates, and the projectile phase needs
	// the lookup every tick, so the bound value and the catalog it came from
	// are kept here rather than re-formed each tick.
	weaponByIDCatalog *content.Catalog
	weaponByID        func(id int32) (*content.WeaponDef, bool)
}

// weaponLookupFor returns the catalog's per-identifier weapon lookup
// [02 "Weapon record"], bound once per catalog.
//
// The projectile phase used to ask the catalog for its whole compiled index
// instead. That call returns a defensive COPY of the map, so a table that
// never changes was rebuilt thirty times a second, and the fixture arm beside
// it ranged the weapon table inside the tick — a map range on a sim-visible
// path [I1]. Neither is needed: the catalog's own lookup answers one
// identifier at a time and owns the fixture ordering rule.
func (s *Service) weaponLookupFor(catalog *content.Catalog) func(id int32) (*content.WeaponDef, bool) {
	if s == nil || catalog == nil {
		return nil
	}
	if s.weaponByIDCatalog != catalog || s.weaponByID == nil {
		s.weaponByIDCatalog = catalog
		s.weaponByID = catalog.WeaponByID
	}
	return s.weaponByID
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
		// The reservation clears exactly TWO fields, not the record. [06 §4.1]
		// enumerates it as "test the live count against the hard cap of 300;
		// take the record at that index; increment the count; clear the
		// record's dead bit; clear its retained unit target", and [06 §5.1]
		// gives the same two clears. Everything else a new record needs is
		// written by the common initializer and the family creator; a field
		// neither writes keeps the PREVIOUS OCCUPANT's value, and [06 §4.1]
		// says so outright — the initializer "does not clear the whole reused
		// record".
		//
		// The retention is load-bearing where the aim point is null: the
		// common initializer copies the aim point into the stored target point
		// "only when it is non-null, leaving the previous occupant's stored
		// target point in place otherwise" [06 §4.1], and the ballistic,
		// dropped and meteor creators all pass a null aim point [06 §6.1],
		// [06 §6.5]. A zero-fill here erased the value retail keeps.
		//
		// The zero-fill that used to stand here (WU-19-154's conservative
		// placeholder, kept because InitCommon copied the aim point
		// unconditionally) is retired by WU-19-164: InitCommon is now
		// conditional and every family creator has been audited to write each
		// field it reads — see the audit block above InitCommon in motion.go.
		// The dead bit is authoritative in Slots (I5), which Slots.Reserve
		// already cleared; the shadow copy is kept in step here.
		s.Records[idx].Dead = false   // [06 §4.1] clear the record's dead bit
		s.Records[idx].TargetUnit = 0 // [06 §4.1] clear its retained unit target
	}
	return h, true
}

// CancelReserve rolls back an early ballistic reservation when the solver finds
// no solution. Retail did not reserve for that admission failure [06 §3.3],
// so the count must not leak. The vel0 #DE path keeps the leak per P0-10
// [06 §6.4] I11 and never calls this.
//
// The rollback is a COUNT rollback only. It used to zero the record as well,
// which was the same defect Reserve carried: retail never reached the creator
// on this path, so the slot still holds the previous occupant's fields, and
// the next reservation of the slot is entitled to read them [06 §4.1],
// [06 §6.1]. The two fields Reserve cleared stay cleared, which costs nothing:
// the record is outside the active span until it is reserved again, and that
// reservation clears the same two fields.
func (s *Service) CancelReserve(h pool.Handle) bool {
	if s == nil {
		return false
	}
	return s.Slots.CancelReserve(h)
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
// span without dead filtering [P1-07][P1-08 §2.2] — retail's burst-dispatch
// loop captures the count at entry and walks the pool array ascending by
// record, dispatching burst vs families even for records whose dead bit was
// set before their turn (dead-before-update via the captured entry count)
// [P1-08 §2.2]. The loop does NOT test the state byte's dead bit at top; only
// later smoke/water tails gate on dead [P1-08 §2.2].
func (s *Service) ForEachInEntrySpanIncludingDead(fn func(h pool.Handle, p *Projectile, isDead bool)) {
	if s == nil || fn == nil {
		return
	}
	entry := s.Slots.Count() // capture once at entry [P1-08 §2.2]
	for i := 0; i < entry; i++ {
		h := pool.Handle(i + 1)
		isDead := s.Slots.IsDead(h)  // state byte &2 dead bit [P1-08 §2.8]
		fn(h, &s.Records[i], isDead) // no dead filter [P1-07][P1-08 §2.2]
	}
}

// ProjectileLinkAt is the projectile-to-projectile link [P1-08 §2.3].
// It is repaired via the old-index marker table [P1-07][P1-08 §2.3][06 §5.2].
func (s *Service) ProjectileLinkAt(h pool.Handle) pool.Handle {
	if s == nil || h == 0 {
		return 0
	}
	idx := int(h) - 1
	if idx < 0 || idx >= len(s.Records) {
		return 0
	}
	return s.Records[idx].TargetProjectile // [P1-08 §2.3]
}

// Compact preserves stable record movement and the bounded link repair of
// [06 §5.2]. Only moved sources have their projectile links repaired; a removed
// target or an unmoved source keeps its old raw handle. Tail records retain
// their bytes after the required old-index marker writes.
func (s *Service) Compact(follow *pool.Handle) {
	if s == nil {
		return
	}
	oldCount := s.Slots.Count()
	// Every survivor's marker is unique and equals its old index. This fixed
	// table therefore answers the later marker search without scanning each
	// target's live span. A negative entry means no surviving target.
	var oldToNew [ProjectileCapacity]int16
	newCount := 0
	for i := 0; i < oldCount; i++ {
		s.Records[i].OldMarker = int16(i)
		if s.Slots.IsDead(pool.Handle(i + 1)) {
			oldToNew[i] = -1
			continue
		}
		oldToNew[i] = int16(newCount)
		newCount++
	}
	if newCount == oldCount {
		// Marker writes still occur on no-death ticks. No record moves, so
		// neither projectile links nor the follow pointer can be repaired.
		return
	}

	// Slots owns metadata, count publication and follow-camera retirement.
	// Its stable slide and this parallel slide use the same survivor order.
	s.Slots.Compact(follow)
	for old := 0; old < oldCount; old++ {
		dest := int(oldToNew[old])
		if dest >= 0 && dest != old {
			s.Records[dest] = s.Records[old]
		}
	}

	// Copying downward leaves each source's original link intact until this
	// second pass. OldMarker distinguishes moved from unmoved sources, and
	// absent targets deliberately retain stale links [06 §5.2].
	for dest := 0; dest < newCount; dest++ {
		source := &s.Records[dest]
		if int(source.OldMarker) == dest || source.TargetProjectile == 0 {
			continue
		}
		target := int(source.TargetProjectile) - 1
		if target >= 0 && target < oldCount && oldToNew[target] >= 0 {
			source.TargetProjectile = pool.Handle(oldToNew[target] + 1)
		}
	}
}
