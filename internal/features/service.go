// This file implements the service: instance lifetime, cell occupancy and the
// per-tick step.

package features

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Instance is a live feature instance [05 "Feature instance and terrain cell"].
// Retail's 48-byte size is record identity; Go stores named fields and a plot
// reference rather than reproducing a packed layout [I13].
type Instance struct {
	Def     *content.FeatureDef // immutable catalog definition [02 "Feature record"]
	Terrain *world.Terrain      // plot reference [01 §6.1]
	CX, CZ  int                 // anchor cell coordinates

	// Reclaim progress; the definition's `damage` is the completion threshold
	// for reclaim [05 "Feature reclaim"]. There is no health word: weapon hits
	// accrue on DamageAccumulator below, never on a countdown.
	ReclaimProgress int32

	// The mode bits of a sprite EVENT record [05 R-FEAT-01 §5 step 5]
	// [05 R-FEAT-01 §9 step 3]: IsBurning is the burning bit; IsAnimating
	// marks a die/reclaim record (burning bit clear, sequence attached), with
	// AnimationSelector below carrying the reclaim-animation bit as the save
	// family's selector. A resting sprite has neither, and this build keeps an
	// Instance for it anyway (see hasEventRecordAt).
	IsBurning   bool
	IsAnimating bool
	// BurnCountdown is the burn event's spark countdown [05 R-FEAT-01 §9]
	// step 4 — a byte in retail, decremented once per visit while nonzero and
	// the remote bit is clear, firing the event once at zero. It is NOT the
	// cursor's frame delay: the two count different things and are kept apart
	// (the save keeps only this one's high nibble [08 R-SAVE-FEATURE-01]).
	BurnCountdown    int32
	RemoteSuppressed bool // the remote bit: a remotely commanded ignition never runs its burn event

	// cursor is the record's ONE animation cursor — frame index and per-frame
	// delay countdown — for whichever event sequence the selector names. The
	// feature phase advances it, EventSequence publishes its frame, the save
	// writer takes its frame byte and RestoreAt writes that byte back
	// [05 R-FEAT-01 §10][08 R-SAVE-FEATURE-01]. There is no second progress
	// word anywhere.
	cursor eventCursor

	// Sinking state [05 "Feature sinking and water interaction"]: the vertical
	// velocity word of the 3D branch [05 R-FEAT-01 §13]. IsSinking mirrors
	// "velocity nonzero" for presentation; Settled records the dormant move —
	// the record left the active list and is not visited again until
	// re-stamped [05 R-FEAT-01 §10] pass 3.
	Y         numeric.Fixed // world Y
	Vy        numeric.Fixed // vertical velocity
	IsSinking bool
	Settled   bool

	// Active-list membership [05 R-FEAT-01 §10] pass 3 (see active.go), and
	// whether the record holds one of the arena's 2048 slots.
	nextActive, prevActive *Instance
	onActive               bool
	arenaBilled            bool

	// runtimeLive is the independent representation of the anchor's attached
	// runtime record. A resting sprite still has this package's convenience
	// lookup Instance, but it has neither an arena record nor this bit
	// [05 R-FEAT-01 §2][05 R-FEAT-01 §3]. runtimeShadowEnabled is the attached sprite
	// record's shadow-present bit; it is meaningful only while runtimeLive is
	// true [03 R-RAST-01 §6]. They are published through RuntimeView rather
	// than inferred from lookup-record presence or a sequence name.
	runtimeLive          bool
	runtimeShadowEnabled bool

	// Animation/status byte bit0 clear means GAF at rest [06 §13.1].
	Status uint8

	// World position derived from anchor cell (centre)
	X, Z numeric.Fixed

	// Orientation is the live record's angle triple [05 "Feature instance and
	// terrain cell"]: three 16-bit words in the unit record's own order and
	// units (65536 per circle). The stamp takes an OPTIONAL source triple
	// exactly as it takes an optional position — the corpse placement passes
	// the dying unit's bank/heading/pitch, so a wreck keeps the orientation its
	// unit died in, and every other placement (map-authored, successor,
	// mission, reproduction, a restore with no saved triple) passes none and
	// stores three zeros. The resurrection transplant copies the triple back
	// into the replacement unit [05 R-WORK-01 §7].
	Orientation

	// Footprint cached from Def for removal without re-reading Def after clear.
	FootprintX, FootprintZ int32

	// SavedAnchorWord is the normal-family record's accumulator word copied
	// back to the anchor cell on reload [08 R-SAVE-FEATURE-01].
	SavedAnchorWord uint16
	// AnimationSelector is the animating save family's selector: 0 burn, 1
	// death, 2 reclaim [08 R-SAVE-FEATURE-01]. Live, it is the record's
	// reclaim-animation bit (selector 2) that [05 R-FEAT-01 §5] step 6 reads.
	AnimationSelector uint8

	// DamageAccumulator is the record's damage word, the one value all three
	// saved families keep at the same place [08 R-SAVE-FEATURE-01]. It is the
	// live 3D damage state and the only such state: DamageFeature adds each
	// weapon hit's `[DAMAGE] default` word (int16 as stored) into it with
	// 16-bit wrap and fires the death transition when the definition's 16-bit
	// `damage` is at or below it, compared unsigned [05 R-FEAT-01 §8] step 7.
	// The stamp zeroes it, so a fresh wreck starts at 0 and `damage = 0` dies
	// on its first hit of any strength. A sprite instance's copy is written by
	// no path and read by none (the sprite entry has no accumulating branch,
	// step 8); a feature with no instance keeps its running sum in the anchor
	// cell's word instead (step 6). It is NOT an animation word — 3D instances
	// have no sprite cursor for it to belong to.
	DamageAccumulator uint16
}

// Orientation is the feature record's angle triple: bank, heading and pitch,
// each 0..65535 per circle, in the same order and units as the unit record's
// triple [05 "Feature instance and terrain cell"]. It is a value type so a
// placement can hand the stamp one by pointer (supplied) or nil (three zeros),
// which is the same optional shape the position triple already has.
type Orientation struct {
	Bank    uint16
	Heading uint16
	Pitch   uint16
}

// Cause selects the successor hop [05 "Removal and successor replacement"].
type Cause int

const (
	CauseDead    Cause = iota // ordinary destruction uses featuredead
	CauseReclaim              // reclaim completion uses featurereclamate
	CauseBurnt                // burning uses featureburnt
)

// Pool limits per [P1-10][P1-15]: anim slots 0x800, plot cell 0xD stride.
//
// There is no catalog limit. A `FeatureCatalogLimit = 0x100` used to stand
// here, refusing a definition the map did not author once the terrain held 256
// records. It read doc 05's per-entry RECORD SIZE as a record COUNT; the
// parser trace states the opposite as Established: "the catalog is a
// reallocated array — there is no fixed catalog cap; 'catalog exhaustion' is
// not a retail failure mode" [05 R-FEAT-01 §1]. The real bound is the
// live-instance arena below.
const (
	// FeatureAnimSlots is the live-instance arena of [05 R-FEAT-01 §2]: 2048
	// slots allocated once at map load, handed out by a free list. It is NOT a
	// cap on how many features a map may carry. Its occupants are exactly two
	// kinds [05 R-FEAT-01 §3 steps 4-5]: every 3D definition (flag bit 0
	// clear), which pops a slot at the stamp and holds it until teardown, and
	// every ACTIVE sprite event record — an ignition [§9], or a die/reclaim
	// animation [§5 step 5]. A resting sprite feature takes no slot at all:
	// step 5 writes the anchor's ordinal, a zero in the slot word and a cleared
	// instance bit. See arenaOccupies.
	FeatureAnimSlots     = 0x800  // 2048 live-instance arena slots [05 R-FEAT-01 §2]
	PlotCellStride       = 0x0D   // 13 bytes per cell [P1-15]
	FeatureSuccessorNone = 0xFFFF // sentinel no successor [P1-10][P1-15]
)

// Service is the features runtime [PLAN_08 WU-08-6].
type Service struct {
	Terrain                     *world.Terrain
	Sim                         *rng.Simulation // nil => rng.Global.Sim (I4)
	Crt                         *rng.CRT        // for smoke jitter, not sim draws [05 "Feature burning"]
	Wind                        *world.Wind
	definitionAdmissionObserver func(*content.FeatureDef)
	definitionRestoreStart      int
	definitionRestoreStarted    bool

	cursor int // global cursor descending from W*H-1 with wrap-skip [06 §13.1]

	// instances is the cell-index LOOKUP: key = cz*W+cx. It holds a record for
	// every stamped anchor — resting sprites included, as the publication and
	// query record — and is never the source of simulation order; that is the
	// active list below [05 R-FEAT-01 §10] pass 3. Deterministic walks over
	// it (publication, the grid resync) use sortedInstanceKeys (I1).
	instances map[int]*Instance
	// instanceKeys is the sorted key list sortedInstanceKeys hands out, and
	// instanceKeysStale says whether it still describes the map. Every writer
	// of instances goes through setInstance/deleteInstance/resetInstances,
	// which is what keeps the flag honest — see sortedInstanceKeys.
	instanceKeys      []int
	instanceKeysStale bool

	// activeHead is the head of the active list of [05 R-FEAT-01 §2]: LIFO,
	// the most recently stamped or ignited record first (active.go).
	activeHead *Instance
	// arenaHeld counts the live-arena slots currently held — every 3D
	// instance and every sprite event record — so the free-list-empty test
	// of the stamp, ignition and transition is a read, not a walk.
	arenaHeld int

	// LastReproIdx is the last cell visited by the reproduction walker, -1 if
	// the wrap-skip cell was the cursor (W*H-1 never scanned) [06 §13.1].
	LastReproIdx int

	// SequenceFrames reports the per-frame delay words of one of a
	// definition's three EVENT sequences — the one the selector names (0
	// burn, 1 death, 2 reclaim; EventSequenceName) — or nil when the
	// definition names none or the name does not resolve [fmt gaf]. It is the
	// immutable content metadata every event cursor is built from
	// [05 R-FEAT-01 §10]: the session binds it to the battle's content table
	// (bindFeatureStripProducers) before any feature exists, so ignition, the
	// death and reclaim transitions and the save reload all time their records
	// from the same authored words. A nil seam, or a nil result, is "no
	// sequence": the transition replaces at once [05 R-FEAT-01 §5 step 3] and
	// ignition refuses [05 R-FEAT-01 §9 step 1]. Nothing is invented for a
	// miss.
	SequenceFrames func(def *content.FeatureDef, selector uint8) []int32
	// sequences memoises SequenceFrames per definition and selector; it is
	// read by key only.
	sequences map[sequenceKey][]int32
	// ShadowSequenceResolved reports whether an attached sprite event record's
	// named shadow entry resolved in the battle's immutable art metadata. It
	// has no cursor or timing role: the main event sequence alone supplies the
	// cursor's delay words. The renderer admits the opaque live-record shadow
	// only when this answer is true [05 R-FEAT-01 §9][03 R-RAST-01 §6]. A nil
	// seam answers false, preserving the missing-entry outcome rather than
	// inferring a shadow from its authored name.
	ShadowSequenceResolved func(def *content.FeatureDef, sequence string) bool

	// BurnWeapon is the ordinary weapon request the burn event fires its
	// `burnweapon` through [05 R-FEAT-01 §11 step 3]: the weapon name and the
	// footprint centre at the bilinear terrain height. The projectile and
	// damage subsystem is combat's, which this package cannot import, so the
	// session binds the request — resolving the name and pushing the impact
	// through the shared splash entry as a null-shooter, zero-side record
	// [06 §13.1]. A nil seam is a fixture: no weapon fires.
	BurnWeapon func(weapon string, pos [3]numeric.Fixed)

	// GeothermalSteam is the steam-strip producer of [05 R-ECO-02 §3], reached
	// from the feature stamp and nowhere else. The stamp calls it once, with the
	// footprint centre and the sampled terrain height, for every successfully
	// placed definition carrying the `geothermal` flag. The strip table it
	// appends to is session state, so this is a seam rather than a call; a nil
	// hook means no steam, which is what every fixture that does not compose a
	// session gets.
	GeothermalSteam func(x, y, z numeric.Fixed)

	// BurnFrameGeometry reports the burn animation's CURRENT frame geometry —
	// the GAF frame's width and height and its two authored offsets [fmt gaf] —
	// for the frame a cursor is on after `visit` visits of the max(delay, 1)
	// cadence. The walk asks with the cursor frame's first visit, so the
	// answer is that frame exactly. It is what the smoke jitter of
	// [05 R-FEAT-01 §10] pass 3a scales its two CRT draws by; burnSmokeJitter
	// in burn.go is that arithmetic. A nil hook means no geometry is known and
	// the puff is emitted unjittered at the footprint centre, which is the
	// established base position of the same paragraph.
	BurnFrameGeometry func(def *content.FeatureDef, visit int32) (w, h, xoff, yoff int32)

	// BurnSmoke is the strip-5 burning-feature smoke producer of
	// [R-STRIP-01 §1 strip 5], called once per burning instance on every third
	// tick with the already-jittered world position [05 R-FEAT-01 §10] pass 3a.
	// The strip table is session state, so this is a seam rather than a call,
	// exactly like GeothermalSteam above; a nil hook means no puff, which is
	// what every fixture that does not compose a session gets.
	BurnSmoke func(pos [3]numeric.Fixed)

	// pendingBurnReplacement is a bounded hand-off for burn.go's established
	// clear-then-spawn sequence. It is consumed and cleared by the next spawn
	// (including every failure path), so a failed successor cannot suppress a
	// later unrelated blocking placement [05 "Feature burning"].
	pendingBurnReplacement *burnReplacement
}

// SetDefinitionAdmissionObserver observes the terrain definition table in its
// existing order, then each successful append. It does not alter feature
// simulation state.
func (s *Service) SetDefinitionAdmissionObserver(observer func(*content.FeatureDef)) {
	if s == nil {
		return
	}
	s.definitionAdmissionObserver = observer
	if observer == nil || s.Terrain == nil {
		return
	}
	for _, def := range s.Terrain.FeatureDefs {
		if def != nil {
			observer(def)
		}
	}
}

// DefinitionRestoreStart returns the definition-table boundary captured before
// the first saved feature row is replayed. Without a restore, the current
// table length is the ordinary-entry boundary [03 R-CRD-005 §1].
func (s *Service) DefinitionRestoreStart() int {
	if s == nil || s.Terrain == nil {
		return 0
	}
	if s.definitionRestoreStarted {
		return s.definitionRestoreStart
	}
	return len(s.Terrain.FeatureDefs)
}

type burnReplacement struct {
	cx, cz int
}

// NewService creates a service bound to terrain.
// If sim is nil the global simulation stream is used (I4).
func NewService(terrain *world.Terrain, sim *rng.Simulation, crt *rng.CRT, wind *world.Wind) *Service {
	s := &Service{
		Terrain:      terrain,
		Sim:          sim,
		Crt:          crt,
		Wind:         wind,
		instances:    make(map[int]*Instance),
		LastReproIdx: -1,
	}
	if terrain != nil {
		total := int(terrain.CellW * terrain.CellH)
		if total > 0 {
			s.cursor = total - 1 // start at W*H-1, which is never scanned [06 §13.1]
		} else {
			s.cursor = -1
		}
	}
	return s
}

func (s *Service) sim() *rng.Simulation {
	if s.Sim != nil {
		return s.Sim
	}
	return nil // DET-01: no global fallback; injected via Service.Sim
}

func (s *Service) crt() *rng.CRT {
	if s.Crt != nil {
		return s.Crt
	}
	return nil // DET-01: no global fallback
}

// TickMotion reconciles externally written grid state during phase 4.
// Reproduction belongs to phase 6, after the catalog rest cursors and before
// the active walk [01 §4.4][05 R-FEAT-01 §10].
func (s *Service) TickMotion(tick uint32) {
	_ = tick
	s.syncInstancesToGrid()
}

// TickLifecycle runs reproduction, then pass 3 of feature phase 6
// [01 §4.4][05 R-FEAT-01 §10]: ONE walk of the active list, LIFO, in which a
// moving 3D record integrates its descent (or goes dormant at zero velocity),
// a die/reclaim record advances its cursor and replaces itself at the visit
// its sequence ends, and a burning record smokes, advances, completes or
// counts down to its burn event. There is no separate sinking pass and no
// deferred completion queue: what a later record observes is what the earlier
// visits left.
func (s *Service) TickLifecycle(tick uint32) {
	s.reproduceTick()
	s.activeWalk(tick)
}

// PlaceAt stamps a feature at anchor cell (cx,cz) via the common placement
// helper with no position/velocity override [06 §13.1][P1-10][P1-15]. It is the
// public entry for mission and corpse placement; deterministic order is the
// caller's responsibility [I1]. Returns the new instance or nil on silent pool
// failure [P1-10].
func (s *Service) PlaceAt(cx, cz int, def *content.FeatureDef) *Instance {
	return s.spawnFeatureAt(cx, cz, def)
}

// RestoreAt places a saved feature through the ordinary stamp and then copies
// only the family state words defined by the battle-save format.  The placement
// helper remains the sole owner of terrain/plot writes [08 R-SAVE-02 §11].
//
// The 3D family is the one that hands the stamp its two optional pointers: the
// saved position triple and the saved orientation triple go in verbatim, so the
// stamp neither recomputes the footprint centre nor re-samples the terrain
// height, and a wreck saved mid-descent comes back at its saved Y
// [08 R-SAVE-FEATURE-01 "3D record restore, exactly"]. Velocity is not
// serialized and is not re-derived: the restored instance starts at zero and
// the lifecycle pass retires it, so such a wreck resumes SUSPENDED and never
// descends further. That is retail's behavior, not an omission — do not latch
// the sinking velocity back on here from the height/sea-level relation.
func (s *Service) RestoreAt(cx, cz int, def *content.FeatureDef, family int, data []byte) (*Instance, error) {
	want := RetailRestorePayloadSize(family)
	if want == 0 || len(data) != want {
		return nil, fmt.Errorf("features: retail restore: family %d payload size %d", family, len(data))
	}
	var pos *[3]numeric.Fixed
	var orient *Orientation
	if family == 2 {
		p := [3]numeric.Fixed{
			fixedFromWire(data[0x08:]),
			fixedFromWire(data[0x0c:]),
			fixedFromWire(data[0x10:]),
		}
		o := Orientation{
			Bank:    binary.LittleEndian.Uint16(data[0x14:]),
			Heading: binary.LittleEndian.Uint16(data[0x16:]),
			Pitch:   binary.LittleEndian.Uint16(data[0x18:]),
		}
		pos, orient = &p, &o
	}
	inst := s.stampFeature(cx, cz, def, pos, orient)
	if inst == nil {
		return nil, fmt.Errorf("features: retail restore: placement rejected at (%d,%d)", cx, cz)
	}
	switch family {
	case 0:
		inst.SavedAnchorWord = binary.LittleEndian.Uint16(data[6:8])
		idx := cz*int(s.Terrain.CellW) + cx
		if idx < 0 || idx >= len(s.Terrain.Plot) {
			return nil, fmt.Errorf("features: retail restore: anchor outside terrain")
		}
		s.Terrain.Plot[idx].SetAnchorWord(inst.SavedAnchorWord)
	case 1:
		// The animating record: the low nibble of its state byte selects the
		// already-established transition family — 0 re-runs the burn
		// (ignition), 1 the death transition, 2 the reclaim transition — and
		// the reader THEN overwrites the allocated record's accumulator word,
		// cursor frame byte and countdown byte with the saved copies
		// [08 R-SAVE-FEATURE-01]. Other selector values do not establish a
		// fourth transition and stay uninterpreted: the feature rests.
		//
		// Re-running the family is what binds the sequence: ignition and the
		// transition allocate the cursor fresh from the same content metadata
		// a live battle uses, at frame 0 with frame 0's delay, and for a burn
		// seed the countdown exactly as ignition does — simulation draw
		// included [05 R-FEAT-01 §9]. The draw is consumed and its value then
		// discarded under the saved nibble; a reload therefore advances the
		// simulation stream by one per saved burn, as retail's does.
		//
		// The family's own refusals stand: no resolved `seqnameburn` and the
		// burn never restarts (the feature rests); no `seqnamedie` or
		// `seqnamereclamate` and the transition replaces at once
		// [05 R-FEAT-01 §5 step 3] — the successor is what the cell holds
		// after the load, and there is no record for the reader to write.
		accumulator := binary.LittleEndian.Uint16(data[6:8])
		frame := data[8]
		selector := data[9] & 0x0f
		nibble := data[9] >> 4
		switch selector {
		case featureAnimSelectorBurn:
			s.igniteAt(cx, cz, def)
		case featureAnimSelectorDie, featureAnimSelectorReclaim:
			isReclaim := selector == featureAnimSelectorReclaim
			if !s.transitionFeatureAt(cx, cz, def, isReclaim) {
				succ := def.FeatureDeadDef
				if isReclaim {
					succ = def.FeatureReclamateDef
				}
				s.replaceFeatureAt(cx, cz, succ)
			}
		}
		idx := cz*int(s.Terrain.CellW) + cx
		inst = s.instances[idx]
		if inst == nil || !s.hasEventRecordAt(idx) || inst.Def != def {
			return inst, nil // no record was allocated; nothing to overwrite
		}
		// The three overwrites, in the reader's order. The cursor's DELAY is
		// not saved and stays at frame 0's word: the restored frame holds for
		// that delay before the sequence continues at its own cadence — a
		// documented save loss, not a contract to repair. The countdown byte
		// comes back as its saved high nibble with a zero low nibble (the
		// save keeps only the nibble), so a burn saved at 149 resumes at 144
		// and one saved below 16 resumes at 0 — never fires its event.
		inst.DamageAccumulator = accumulator
		inst.cursor.frame = int32(frame)
		inst.BurnCountdown = int32(nibble) << 4
	case 2:
		// The accumulator is written AFTER the stamp because the stamp zeroes
		// it, and it is the only thing the reader writes beyond what it handed
		// the stamp [08 R-SAVE-FEATURE-01 "3D record restore, exactly"]. The
		// position and orientation are already in place, verbatim.
		inst.DamageAccumulator = binary.LittleEndian.Uint16(data[6:8])
		idx := cz*int(s.Terrain.CellW) + cx
		if idx >= 0 && idx < len(s.Terrain.Plot) {
			s.Terrain.Plot[idx].SetOccupied(true)
		}
	default:
		return nil, fmt.Errorf("features: retail restore: unknown family %d", family)
	}
	return inst, nil
}

// RetailRestorePayloadSize returns the exact byte count for one saved feature
// family. A switch keeps the wire contract explicit and avoids a mutable
// lookup table on the restore path [08 R-SAVE-FEATURE-01].
func RetailRestorePayloadSize(family int) int {
	switch family {
	case 0:
		return 8
	case 1:
		return 10
	case 2:
		return 26
	default:
		return 0
	}
}

// ResetForRestore drops map-authored instances before the saved feature rows
// are replayed.  It intentionally uses the service's footprint clear helper,
// rather than allowing the session to mutate plot cells directly.
func (s *Service) ResetForRestore() {
	if s == nil {
		return
	}
	if !s.definitionRestoreStarted && s.Terrain != nil {
		s.definitionRestoreStart = len(s.Terrain.FeatureDefs)
		s.definitionRestoreStarted = true
	}
	for _, inst := range s.Instances() {
		if inst != nil {
			s.clearFootprintNoRevision(inst.CX, inst.CZ, inst.Def)
		}
	}
	s.resetInstances()
}

// PlaceAtWorld stamps a feature at world position (x,z) using floor-corrected
// cell conversion [03 §2.1] I3 and then PlaceAt. Used for corpse placement from
// unit world coordinates.
func (s *Service) PlaceAtWorld(x, z numeric.Fixed, def *content.FeatureDef) *Instance {
	if s.Terrain == nil || def == nil {
		return nil
	}
	cx := int(world.WorldToCell(x))
	cz := int(world.WorldToCell(z))
	return s.PlaceAt(cx, cz, def)
}

// PlaceCorpse stamps a corpse feature for a dying unit and initiates sinking
// when submerged [05 "Feature sinking and water interaction"].
//
// `pos` is the dying unit's EXACT position triple, and the two things it feeds
// are deliberately separate [05 R-FEAT-01 §13 "The corpse creator's chain and
// stamp"]: the corpse is stamped at the unit's plot cell, derived from X and Z
// by the floor-corrected cell conversion [03 §2.1] I3, while the instance's
// stored position is the triple verbatim — not the footprint centre, and not
// the terrain floor under it, which is what the null-position stamp of
// [05 R-FEAT-01 §3] step 4 computes instead.
//
// The Y is what makes a sinking wreck a wreck. Handing this helper only X and
// Z left every corpse starting at the coarse floor, so a surface ship's wreck
// was already resting on the seabed on its first lifecycle visit — settled,
// never descending — and its horizontal position jumped to the cell centre.
//
// `orient` is the dying unit's own bank/heading/pitch, the second optional
// pointer the stamp takes [05 "Feature instance and terrain cell"]. The corpse
// placement is the ONLY source that supplies one, which is why a wreck stands
// the way its unit fell while a map-authored or successor feature is at three
// zeros, and it is the triple the resurrection transplant copies back into the
// replacement unit [05 R-WORK-01 §7].
//
// fromIsFeature is the dying unit's IsFeature flag; isfeature corpses never
// descend. Chain depth is already resolved by the caller from the Killed-variant
// low nibble [04 §5.1][06 §12.1] C23; this helper just stamps the resolved def.
// Returns the corpse instance or nil.
func (s *Service) PlaceCorpse(pos [3]numeric.Fixed, orient Orientation, def *content.FeatureDef, fromIsFeature bool) *Instance {
	if def == nil || s.Terrain == nil {
		return nil
	}
	cx := int(world.WorldToCell(pos[0]))
	cz := int(world.WorldToCell(pos[2]))
	inst := s.stampFeature(cx, cz, def, &pos, &orient)
	if inst == nil {
		return nil
	}
	// Submerged start latches vy to -11468 when terrain at or below sea level
	// and dying definition lacks isfeature flag [05 "Feature sinking and water interaction"].
	s.StartSinking(inst, fromIsFeature)
	return inst
}

// transitionFeatureAt is the transition of [05 R-FEAT-01 §5]: what damage
// death, the reclaim payout, the multiplayer state commands and save reload
// call. It reports whether the caller must NOT replace: either because it
// attached an event animation — the feature phase then drives the record to
// completion and runs the replacement itself [05 R-FEAT-01 §10] pass 3 — or
// because the cause was dropped outright (step 4's existing record, or step
// 5's empty arena), which leaves the feature standing.
//
// The steps are the section's own, in order:
//
//  1. walk a fringe back to its anchor; a word at or above the sentinel band
//     returns silently (the anchor walk is the resolver the callers already
//     use, so it happens at the call site);
//  2. for a SPRITE definition select the event sequence — `seqnamedie` when
//     the cause is death, `seqnamereclamate` when it is reclaim; for a 3D
//     definition the selection is always "none";
//  3. no sequence means immediate replacement, which is the caller's default;
//  4. sequence present and the cell already carries an event record means
//     return with no effect — the death or reclaim is dropped, not queued;
//  5. sequence present and no record: attach it at frame 0, record the anchor,
//     and set the mode bits — burning clear, reclaim-animation set from the
//     cause.
func (s *Service) transitionFeatureAt(cx, cz int, def *content.FeatureDef, isReclaim bool) bool {
	if s == nil || s.Terrain == nil || def == nil {
		return false
	}
	// Step 2: a 3D definition never selects a sequence, so it always replaces.
	// Object naming is the 3D discriminator the rest of this package uses.
	if def.Object != "" {
		return false
	}
	selector := featureAnimSelectorDie
	sequence := def.SeqNameDie
	if isReclaim {
		selector = featureAnimSelectorReclaim
		sequence = def.SeqNameReclamate
	}
	if sequence == "" {
		return false // step 3
	}
	// Step 2 selects a RESOLVED sequence: its per-frame delay words are the
	// content metadata the cursor runs on. A name that does not resolve is no
	// sequence (see sequenceDelays), which is step 3's immediate replacement.
	delays := s.sequenceDelays(def, selector)
	if delays == nil {
		return false
	}
	idx := cz*int(s.Terrain.CellW) + cx
	if idx < 0 || idx >= len(s.Terrain.Plot) {
		return false
	}
	inst, ok := s.instances[idx]
	if !ok || inst == nil {
		// Retail pops a fresh animation slot and attaches it to the cell. In
		// this build every stamped anchor already owns its instance, so a cell
		// with none is one the service does not track; replacing is then the
		// only honest outcome.
		return false
	}
	if inst.IsBurning || inst.IsAnimating {
		return true // step 4: dropped, and the caller must not replace either
	}
	// Step 5 opens by popping an arena slot, and "empty pool ⇒ return, no
	// effect — the feature simply stays" [05 R-FEAT-01 §5 step 5]. That is a
	// DROPPED death or reclaim, not a fall-through to step 3's replacement, so
	// it reports the same "do not replace" the step-4 drop does.
	if s.arenaOccupants() >= FeatureAnimSlots {
		return true
	}
	// Step 5: attach the record, start the sequence at frame 0 and set the
	// mode bits — burning := 0, reclaim-animation := isReclaim (the selector).
	inst.IsBurning = false
	inst.IsAnimating = true
	inst.AnimationSelector = selector
	inst.BurnCountdown = 0
	inst.RemoteSuppressed = false
	inst.cursor.start(delays)
	s.attachEventRecord(inst)
	s.Terrain.Plot[idx].SetOccupied(true) // the anchor's instance-attached bit
	return true
}

// CorpseDefFor resolves the corpse feature for depth low-nibble [06 §12.1] C23 [04 §5.1].
// It mirrors combat.ResolveCorpse but lives in features so callers without
// combat import can still resolve. Depth is the Killed-variant low nibble
// [04 §5.1][06 §12.1] C23, not a constant: Depth 0 => nil; depth 1 => authored Corpse;
// larger depths follow featuredead chain depth-1 times. This is the sole
// corpse-depth source, replacing the constant switch in the corpse stamper.
func CorpseDefFor(unitDef *content.UnitDef, features map[string]*content.FeatureDef, depth uint8) *content.FeatureDef {
	depth &= 0x0F // [06 §12.1] C23 low four bits from Killed variant
	if depth == 0 || unitDef == nil {
		return nil
	}
	name := unitDef.Corpse
	if name == "" {
		return nil
	}
	ck := content.CanonicalKey(name)
	cur := features[ck]
	if cur == nil {
		return nil
	}
	if depth == 1 {
		return cur
	}
	for i := uint8(1); i < depth; i++ {
		if cur == nil {
			return nil
		}
		var nxt *content.FeatureDef
		if cur.FeatureDeadDef != nil {
			nxt = cur.FeatureDeadDef
		} else if cur.FeatureDead != "" {
			ck2 := content.CanonicalKey(cur.FeatureDead)
			nxt = features[ck2]
		} else {
			return nil
		}
		if nxt == nil {
			return nil
		}
		cur = nxt
	}
	return cur
}

// Reclaim performs feature reclaim. The payout is a one-time completion event
// adding the full feature pools to the builder [05 "Feature reclaim"].
// It verifies reclaimable and not indestructible, returns the metal/energy
// pools as float32, and replaces the feature with its reclaimed successor or
// removes it when none exists [05 "Feature reclaim"]. Burning features cannot
// be reclaimed until burn completes [05 "Feature burning"].
func (s *Service) Reclaim(u *units.Unit, f *Instance, tick uint32) (metal, energy float32) {
	if f == nil || f.Def == nil || s.Terrain == nil {
		return 0, 0
	}
	// Burning FILENAME-BASED features cannot be reclaimed — the block is
	// scoped to definitions that carry a filename (the shipped ignitables),
	// not to every burning instance [05 "Feature burning"].
	if f.IsBurning && f.Def.Filename != "" {
		return 0, 0
	}
	// A cell already carrying an event record — burning, dying or reclaiming —
	// is inert to every further cause, and the reclaim executor's payout
	// refuses along with the rest [05 R-FEAT-01 §5 "same-tick precedence"]
	// [05 R-FEAT-01 §15]. Without this the same tree could pay out once per
	// visit while its animation ran, and the transition below would drop the
	// replacement at step 4 with the credit already spent.
	if f.IsAnimating || f.IsBurning {
		return 0, 0
	}
	def := f.Def
	// Verify reclaimable and not indestructible [05 "Feature reclaim"].
	if !def.Reclaimable || def.Indestructible {
		return 0, 0
	}
	// Also check indestructible via def flag; if set, no reclaim.
	metal = float32(def.Metal)   // I2 allowlist: resource pools as float32 [05 "Feature reclaim"]
	energy = float32(def.Energy) // same
	// The two pools are returned RAW. The special-player scaling of
	// [05 R-WORK-01 §5] step 4 is applied where retail applies it — at the
	// credit, separately to each of the two additions, gated on the BUILDER's
	// player record (the record exists and its control byte is 2, the computer
	// player) and selected by the difficulty word [05 R-ECO-01 §3]
	// [05 R-ECO-01 §11]. The ledger owns both the records and the selector, so
	// the ladder lives in economy.CreditFeatureReclaim and this package never
	// sees a scaled pool. Scaling here as well would apply the discount twice,
	// and scaling here INSTEAD would narrow to single before the credit's
	// subtraction, which §3 says rounds differently.
	//
	// The payout is settled above; the cell itself goes through the transition
	// of [05 R-FEAT-01 §5], which plays `seqnamereclamate` when the definition
	// names one and replaces immediately when it does not.
	if !s.transitionFeatureAt(f.CX, f.CZ, def, true) {
		s.replaceFeatureAt(f.CX, f.CZ, def.FeatureReclamateDef)
	}
	_ = u
	_ = tick
	return metal, energy
}

// replaceFeatureAt performs removal and placement as one logical transition at
// the same world location [05 "Removal and successor replacement"]. Missing
// successor means final removal. It clears the whole stamped footprint and
// returns the plot cell to the free sentinel 0xFFFF [05 "Removal and successor replacement"].
func (s *Service) replaceFeatureAt(cx, cz int, successor *content.FeatureDef) {
	s.replaceFeatureStamp(cx, cz, successor, true)
}

func (s *Service) replaceFeatureStamp(cx, cz int, successor *content.FeatureDef, preserveTransform bool) {
	if s.Terrain == nil {
		return
	}
	s.abandonPendingBurnReplacement()
	// Locate current instance to derive footprint for clearing.
	idx := cz*int(s.Terrain.CellW) + cx
	var def *content.FeatureDef
	var pos *[3]numeric.Fixed
	var orient *Orientation
	if inst, ok := s.instances[idx]; ok && inst != nil {
		def = inst.Def
		// Only an attached runtime record supplies a transform; a resting
		// sprite lookup record does not [05 R-FEAT-01 §5 step 6].
		if preserveTransform && inst.runtimeLive {
			position := [3]numeric.Fixed{inst.X, inst.Y, inst.Z}
			orientation := inst.Orientation
			pos, orient = &position, &orientation
		}
	} else {
		// Fallback: try to resolve feature at cell via world.ResolveFeature with
		// signed-offset reading [SPEC_CONFLICTS SC6] [04 §6.2][02 "Terrain file"].
		if feat, ok := world.ResolveFeature(s.Terrain.Plot, int(s.Terrain.CellW), int(s.Terrain.CellH), cx, cz); ok {
			if d, ok := s.Terrain.FeatureDefAt(feat); ok {
				def = d
			}
		}
	}
	// Clear and successor placement are one logical static mutation. Avoid a
	// clear-side bump and advance once after both definitions are known.
	s.clearFootprintNoRevision(cx, cz, def)
	placed := false
	if successor != nil {
		placed = s.stampFeature(cx, cz, successor, pos, orient) != nil
	}
	// If the old blocking footprint was removed, the transition changes the
	// static layer even when a successor is absent or cannot be stamped. A
	// successful blocking successor bumps in stampFeature, so this remains
	// one bump for the whole replacement [05 "Removal and successor replacement"].
	if def != nil && def.Blocking && (!placed || successor == nil || !successor.Blocking) {
		s.Terrain.BumpStaticObstacleRevision()
	}
}

// clearFootprint clears the whole stamped footprint and releases live state,
// returning cells to the free sentinel [05 "Removal and successor replacement"].
// Sentinel for free cells is 0xFFFF [GAP T14][02 "Terrain file"].
func (s *Service) clearFootprint(cx, cz int, def *content.FeatureDef) {
	s.abandonPendingBurnReplacement()
	s.clearFootprintNoRevision(cx, cz, def)
	if s == nil || s.Terrain == nil {
		return
	}
	// Burning invokes clearFootprint followed by a direct successor spawn in
	// burn.go. Defer a blocking-successor transition to that spawn so the
	// actual result (success or failure) decides the single revision bump.
	if def != nil && def.Blocking && def.FeatureBurntDef != nil && def.FeatureBurntDef.Blocking {
		s.pendingBurnReplacement = &burnReplacement{cx: cx, cz: cz}
		return
	}
	if def != nil && def.Blocking {
		s.Terrain.BumpStaticObstacleRevision()
	}
}

func (s *Service) abandonPendingBurnReplacement() {
	if s == nil || s.pendingBurnReplacement == nil {
		return
	}
	s.pendingBurnReplacement = nil
	if s.Terrain != nil {
		s.Terrain.BumpStaticObstacleRevision()
	}
}

// clearFootprintNoRevision is used by replacement transitions that coalesce
// removal and successor placement into one static revision.
func (s *Service) clearFootprintNoRevision(cx, cz int, def *content.FeatureDef) {
	if s.Terrain == nil {
		return
	}
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	fx, fz := 1, 1
	if def != nil {
		if def.FootprintX > 0 {
			fx = int(def.FootprintX)
		}
		if def.FootprintZ > 0 {
			fz = int(def.FootprintZ)
		}
	} else {
		// Try to infer from existing instance's cached footprint.
		idx := cz*w + cx
		if inst, ok := s.instances[idx]; ok && inst != nil {
			if inst.FootprintX > 0 {
				fx = int(inst.FootprintX)
			}
			if inst.FootprintZ > 0 {
				fz = int(inst.FootprintZ)
			}
		}
	}
	for dz := 0; dz < fz; dz++ {
		for dx := 0; dx < fx; dx++ {
			px := cx + dx
			pz := cz + dz
			if px < 0 || px >= w || pz < 0 || pz >= h {
				continue
			}
			idx := pz*w + px
			// Return to free sentinel 0xFFFF [GAP T14].
			s.Terrain.Plot[idx].SetFeature(world.PlotFeatureNone)
			// Clear occupied and anchor bytes.
			s.Terrain.Plot[idx].SetFlagByte(0)
			s.Terrain.Plot[idx].SetAnchor(0, 0)
			// Release instance if anchor.
			if px == cx && pz == cz {
				s.deleteInstance(idx)
			} else {
				// Fringe cells: also delete any stray instance mapping if present.
				s.deleteInstance(idx)
			}
		}
	}
}

// spawnFeatureAt supplies neither optional transform to the common stamp:
// map and mission placement, reproduction, and burn completion snap to the
// footprint centre [05 R-FEAT-01 §3, §10]. General replacement uses the
// attached predecessor's transform instead [05 R-FEAT-01 §5].
func (s *Service) spawnFeatureAt(cx, cz int, def *content.FeatureDef) *Instance {
	return s.stampFeature(cx, cz, def, nil, nil)
}

// stampFeature is the one stamp routine of [05 R-FEAT-01 §3], taking the
// anchor cell, the definition and TWO optional triples — a position and an
// orientation. A non-nil position is stored on the instance verbatim (step 4);
// a nil one is the snapped footprint centre. A non-nil orientation is stored
// verbatim; a nil one is three zeros. The corpse creator supplies both; the
// saved-game 3D restore supplies both from the record it read; every other
// source supplies neither.
func (s *Service) stampFeature(cx, cz int, def *content.FeatureDef, pos *[3]numeric.Fixed, orient *Orientation) *Instance {
	if s == nil {
		return nil
	}
	pending := s.pendingBurnReplacement
	s.pendingBurnReplacement = nil
	matchesPending := pending != nil && pending.cx == cx && pending.cz == cz
	placed := false
	defer func() {
		if s.Terrain != nil && pending != nil && (!matchesPending || !placed) {
			s.Terrain.BumpStaticObstacleRevision()
		}
	}()
	if s.Terrain == nil || def == nil {
		return nil
	}
	// Normalize malformed for placement without mutating catalog [P1-I05]
	if IsMalformed(def) {
		def = NormalizeDef(def)
		if def == nil {
			return nil
		}
	}
	// The one pool that can silently refuse a placement is the live-instance
	// arena of 2048 slots [05 R-FEAT-01 §2]; the catalog itself has no cap
	// [05 R-FEAT-01 §1].
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	if cx < 0 || cx >= w || cz < 0 || cz >= h {
		return nil
	}
	idx := cz*w + cx
	featIdx := s.featureIndexForDef(def)
	if featIdx == world.PlotFeatureNone {
		// A definition the map did not author is admitted into the terrain's
		// own record list and takes the next index — the same admission
		// stampFeatureDef performs, so a successor the map never names can
		// still be placed [05 R-FEAT-01 §2]. The admission cannot fail: the
		// catalog is a reallocated array with no fixed cap, and "catalog
		// exhaustion" is not a retail failure mode [05 R-FEAT-01 §1].
		//
		// Two arms stood here, one for an empty record list and one for a
		// non-empty one. They computed the same index — appending to an empty
		// slice yields 0 — and the empty arm's comment described the result as
		// a synthesized placeholder for catalog-less fixture terrain, which
		// read as a second, non-retail admission rule where there is only one.
		// Collapsing them changes no index and no refusal.
		//
		// CL-4 filed a TODO(question) here asking whether the 0x100 refusal was
		// a retail cap at all; [05 R-FEAT-01 §1] answers it as Established —
		// it is not — so the refusal is gone.
		s.Terrain.FeatureDefs = append(s.Terrain.FeatureDefs, def)
		if s.definitionAdmissionObserver != nil {
			s.definitionAdmissionObserver(def)
		}
		featIdx = uint16(len(s.Terrain.FeatureDefs) - 1)
	}
	// Normalize zero/negative extents to the service's established 1x1
	// placement behavior before invoking the shared terrain writer.
	footX := def.FootprintX
	footZ := def.FootprintZ
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	// Step 3, the dense-pack rule [05 R-FEAT-01 §3][§3-A]. There is no
	// "already occupied" refusal: the stamp tears down every non-indestructible
	// feature its footprint covers — anchors and fringe cells alike, a fringe
	// walking back to its anchor and taking that whole footprint with it — and
	// only an indestructible definition, a void cell or a stale fringe vetoes
	// it. A veto leaves the cells torn so far torn. The teardown itself lives
	// in world.StampFeatureRect, which owns the plot words; what this has to do
	// is reconcile the animation side against it, in the same call, so no
	// instance outlives the grid entry it describes and the slots the teardown
	// released are back on the free list before step 4 pops one.
	//
	// Refusing a nonempty anchor here instead — which this did — meant a unit
	// dying over a tree left no wreck and no reclaim value at all, while the
	// same geometry with the overlap one cell off the anchor replaced the tree
	// normally.
	torn := s.coveredAnchors(cx, cz, footX, footZ)
	stampErr := s.Terrain.StampFeatureRect(int32(cx), int32(cz), featIdx, footX, footZ)
	tornAway := s.releaseTornInstances(torn)
	if stampErr != nil {
		if tornAway {
			// A partial teardown still changed the static layer even though the
			// stamp never wrote its own anchor.
			s.Terrain.BumpStaticObstacleRevision()
		}
		return nil
	}
	// Step 4's slot pop, and it is deliberately AFTER the teardown: a 3D
	// feature torn down just above pushed its slot back on the free list, so a
	// full arena can still admit its replacement. An empty free list returns 0
	// with the cells left torn [05 R-FEAT-01 §3 step 4], which is what undoing
	// the footprint write reproduces — retail writes the anchor inside step 4,
	// after the pop, so a refused stamp never leaves its own ordinal behind.
	// A sprite definition takes no slot (step 5) and is never refused here.
	if !isSpriteDef(def) && s.arenaOccupants() >= FeatureAnimSlots {
		s.clearFootprintNoRevision(cx, cz, def)
		s.Terrain.BumpStaticObstacleRevision()
		return nil
	}
	placed = true
	// One bump for the whole stamp. `tornAway` joins the two existing arms
	// because a footprint that replaced a blocking tree with a non-blocking
	// smudge changed the static layer just as much as a blocking stamp does.
	if def.Blocking || tornAway || matchesPending {
		s.Terrain.BumpStaticObstacleRevision()
	}
	s.Terrain.Plot[idx].SetFlagByte(0)
	// The stamp is one of the three writers of the anchor's instance-attached
	// bit, and it is the arm that fires for a 3D definition: "set by the stamp
	// for 3D definitions and by ignition and the die/reclaim transitions for
	// sprite definitions" [05 R-FEAT-01 §15]. A sprite definition therefore
	// leaves the anchor clear here and acquires the bit only when an animation
	// instance actually attaches (Ignite and startFeatureAnimation), which is
	// what makes the payout guard's conjunction mean "a sprite feature that is
	// burning or already playing its death or reclaim animation" rather than
	// "any stamped feature". A 3D wreck carries the bit from birth and its
	// definition bit is clear, so it stays reclaimable throughout.
	//
	// "3D definition" is read strictly — an authored `object` and no
	// `filename` — so that no definition can both take the bit here and answer
	// the sprite test below. It is also what keeps the write invisible to this
	// package's other readers of the same bit: the retail save writer selects
	// its 3D family from `object` before it ever consults the flag byte, and the
	// fire-spread scans read "has an instance" through cellHasInstance, which
	// already answers yes for every 3D definition.
	if is3DDef(def) {
		s.Terrain.Plot[idx].SetOccupied(true)
	}
	// TODO(question): retained mode and sprite-to-wreck effects need the
	// authored-observable or complete-writer decider in [05 R-FEAT-01 §14].
	// Fresh records keep the documented host-zero velocity policy and also
	// reset mode state; they do not emulate physical arena reuse (EC-G2).
	inst := &Instance{
		Def:        def,
		Terrain:    s.Terrain,
		CX:         cx,
		CZ:         cz,
		Status:     0,
		FootprintX: footX,
		FootprintZ: footZ,
	}
	// Step 4's position: "the supplied triple verbatim, or when null the
	// footprint centre with the terrain height snapped under it"
	// [05 R-FEAT-01 §3]. The corpse creator is the only source that supplies
	// one, and it supplies the dying unit's exact position — including its Y,
	// which is what a wreck sinks FROM.
	if pos != nil {
		inst.X, inst.Y, inst.Z = pos[0], pos[1], pos[2]
	} else {
		inst.X = world.CellToWorld(int32(cx)).Add(numeric.Fixed(int64(footX) * 1048576 / 2))
		inst.Z = world.CellToWorld(int32(cz)).Add(numeric.Fixed(int64(footZ) * 1048576 / 2))
		inst.Y = s.Terrain.HeightAt(inst.X, inst.Z) // centre sample [05 R-FEAT-01 §3]
	}
	// The second optional pointer, read the same way: the supplied triple
	// verbatim, or three zeros [05 "Feature instance and terrain cell"].
	if orient != nil {
		inst.Orientation = *orient
	}
	s.setInstance(idx, inst)
	// Service-owned flags remain separate from the shared feature/delta writer.
	for dz := 0; dz < int(footZ); dz++ {
		for dx := 0; dx < int(footX); dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			px := cx + dx
			pz := cz + dz
			if px < 0 || px >= w || pz < 0 || pz >= h {
				continue
			}
			fIdx := pz*w + px
			if s.Terrain.Plot[fIdx].IsFringe() {
				s.Terrain.Plot[fIdx].SetFlagByte(0)
			}
		}
	}
	// The stamp's own tail: a `geothermal` definition runs the steam-strip
	// producer with the footprint centre and the sampled height it just stored
	// [05 R-ECO-02 §3]. This is the producer's only reach in the whole image,
	// which is why the steam belongs to placement and not to the feature tick.
	if def.Geothermal && s.GeothermalSteam != nil {
		s.GeothermalSteam(inst.X, inst.Y, inst.Z)
	}
	return inst
}

// coveredAnchors lists, in the stamp's own row-major order, the distinct anchor
// cells the dense-pack teardown of [05 R-FEAT-01 §3] step 3 will visit for a
// footprint: every covered cell whose feature word is not empty, with a fringe
// cell hopped back to its anchor through the two stored signed offset bytes
// [05 R-FEAT-01 §3-A]. It reads the plot and writes nothing; the teardown is
// world's.
//
// The result is the reconciliation set for the animation side. It is normally
// empty (a stamp onto clear ground) and never larger than the footprint, so the
// small linear dedupe below beats a map both in cost and in determinism (I1).
func (s *Service) coveredAnchors(cx, cz int, footX, footZ int32) []int {
	if s == nil || s.Terrain == nil {
		return nil
	}
	w := int(s.Terrain.CellW)
	var anchors []int
	for dz := int32(0); dz < footZ; dz++ {
		for dx := int32(0); dx < footX; dx++ {
			px, pz := cx+int(dx), cz+int(dz)
			cell := s.Terrain.PlotAt(int32(px), int32(pz))
			if cell == nil || cell.Feature() == world.PlotFeatureNone {
				continue
			}
			if cell.IsFringe() {
				px += int(cell.AnchorDXSigned())
				pz += int(cell.AnchorDZSigned())
				if s.Terrain.PlotAt(int32(px), int32(pz)) == nil {
					continue
				}
			}
			anchorIdx := pz*w + px
			seen := false
			for _, have := range anchors {
				if have == anchorIdx {
					seen = true
					break
				}
			}
			if !seen {
				anchors = append(anchors, anchorIdx)
			}
		}
	}
	return anchors
}

// releaseTornInstances is the animation-side half of the dense-pack teardown.
// The grid is authoritative for what stands on a cell [05 R-FEAT-01 §3, §5], so
// an instance whose anchor no longer carries its own definition is the record
// of a feature the teardown just removed: its slot goes back to the free list
// [05 R-FEAT-01 §4 step 4] and its Instance goes away, synchronously, before
// the stamp pops a slot of its own.
//
// It reports whether anything was released, which is what tells the caller the
// static obstacle layer moved. Running it after a VETOED stamp is the point of
// the "leaving already-torn cells torn" rule: the cells that were torn before
// the veto stay torn on both sides.
func (s *Service) releaseTornInstances(anchors []int) bool {
	if s == nil || len(anchors) == 0 || s.Terrain == nil {
		return false
	}
	w := int(s.Terrain.CellW)
	released := false
	for _, idx := range anchors {
		inst := s.instances[idx]
		if inst == nil {
			continue
		}
		cell := s.Terrain.PlotAt(int32(idx%w), int32(idx/w))
		if cell != nil && cell.IsRealFeature() {
			if def, bound := s.Terrain.FeatureDefAt(cell.Feature()); bound && def != nil {
				if def == inst.Def || (inst.Def != nil && def.CanonicalKey == inst.Def.CanonicalKey) {
					continue // still standing: this anchor was not torn down
				}
			}
		}
		s.deleteInstance(idx)
		released = true
	}
	return released
}

func (s *Service) featureIndexForDef(def *content.FeatureDef) uint16 {
	if s.Terrain == nil || def == nil {
		return world.PlotFeatureNone
	}
	for i, d := range s.Terrain.FeatureDefs {
		if d == def {
			return uint16(i)
		}
	}
	for i, d := range s.Terrain.FeatureDefs {
		if d != nil && d.CanonicalKey == def.CanonicalKey {
			return uint16(i)
		}
	}
	return world.PlotFeatureNone
}

// sortedInstanceKeys returns deterministic iteration order (I1).
// The key set changes only when a feature is placed or removed, but the four
// per-tick callers — the sink pass, the burn pass, the grid resync and the
// publication walk — each rebuilt it from a map walk and a sort over every
// feature on the map. The list is therefore built once per change and handed
// out until a writer invalidates it. Every writer of instances goes through
// setInstance, deleteInstance or resetInstances, which is what makes that
// safe; nothing else in the package touches the map.
//
// The slice is the service's, not the caller's: it must not be retained past
// the next mutation, and callers that delete while walking it (the grid
// resync does) are walking a snapshot taken before their first delete, which
// is exactly what the old per-call rebuild gave them.
func (s *Service) sortedInstanceKeys() []int {
	if !s.instanceKeysStale && s.instanceKeys != nil {
		return s.instanceKeys
	}
	keys := make([]int, 0, len(s.instances))
	for k := range s.instances {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	s.instanceKeys, s.instanceKeysStale = keys, false
	return keys
}

// setInstance is the only way a feature instance enters the map. A record
// that takes an arena slot at its stamp — a 3D definition [05 R-FEAT-01 §3
// step 4] — is billed for it and joins the active list at the head here; a
// sprite event record built directly (fixtures) is treated the same, while a
// resting sprite is neither billed nor listed [§3 step 5]. A different record
// already at the cell is released first.
func (s *Service) setInstance(idx int, inst *Instance) {
	if s.instances == nil {
		s.instances = make(map[int]*Instance)
	}
	old, existed := s.instances[idx]
	if !existed {
		s.instanceKeysStale = true
	}
	if old != nil && old != inst {
		s.unlinkActive(old)
		s.releaseArena(old)
	}
	s.instances[idx] = inst
	if arenaOccupies(inst) {
		s.attachEventRecord(inst)
	}
}

// deleteInstance is the only way a feature instance leaves the map: the
// teardown that pushes its slot back on the free list [05 R-FEAT-01 §4].
func (s *Service) deleteInstance(idx int) {
	inst, existed := s.instances[idx]
	if existed {
		s.instanceKeysStale = true
		s.unlinkActive(inst)
		s.releaseArena(inst)
	}
	delete(s.instances, idx)
}

// arenaOccupies reports whether an instance holds one of the 2048 live-arena
// slots of [05 R-FEAT-01 §2]. The discriminator is the definition's flag bit 0
// — "sprite (filename-based) definition" [05 R-FEAT-01 §15] — exactly as the
// stamp reads it: bit 0 CLEAR takes a slot at the stamp (§3 step 4), bit 0 SET
// takes none (§3 step 5) and acquires one only when an event record actually
// attaches: ignition [§9] or a die/reclaim animation [§5 step 5].
//
// So a map's resting trees are free, however many of them there are, and the
// arena is spent only on wrecks, rocks, vents and burning/dying/reclaiming
// sprites. This build keeps an Instance for a resting sprite anyway — it is
// what publication draws from — but that record is not an arena occupant, and
// billing it against the arena is what used to make a 3,754-tree map refuse
// every wreck after its 2,048th anchor.
func arenaOccupies(inst *Instance) bool {
	if inst == nil || inst.Def == nil {
		return false
	}
	if !isSpriteDef(inst.Def) {
		return true // flag bit 0 clear: the stamp pops a slot [05 R-FEAT-01 §3 step 4]
	}
	return inst.IsBurning || inst.IsAnimating
}

// arenaOccupants reports the live-arena slots currently held — the free-list
// head test the stamp, ignition and transition stand on. It is a running
// count kept by billArena/releaseArena, so an ignition inside a burn event
// does not walk the whole instance map to learn whether a slot is free.
func (s *Service) arenaOccupants() int {
	return s.arenaHeld
}

// resetInstances empties the map, the active list and the arena — the
// zero-fill of the whole instance pool at battle entry [08 R-SAVE-FEATURE-01].
func (s *Service) resetInstances() {
	s.instances = make(map[int]*Instance)
	s.instanceKeys, s.instanceKeysStale = nil, true
	s.activeHead = nil
	s.arenaHeld = 0
}

// RemoveFeatureAt removes a feature at anchor cell with cause-specific successor
// [05 "Removal and successor replacement"]. It is the explicit successor
// replacement entry used by damage, reclaim, and burn paths.
func (s *Service) RemoveFeatureAt(cx, cz int, cause Cause) {
	if s.Terrain == nil {
		return
	}
	idx := cz*int(s.Terrain.CellW) + cx
	var def *content.FeatureDef
	if inst, ok := s.instances[idx]; ok && inst != nil {
		def = inst.Def
	} else {
		if feat, ok := world.ResolveFeature(s.Terrain.Plot, int(s.Terrain.CellW), int(s.Terrain.CellH), cx, cz); ok {
			if d, ok := s.Terrain.FeatureDefAt(feat); ok {
				def = d
			}
		}
	}
	// Death and reclaim are the transition of [05 R-FEAT-01 §5]: when the
	// definition names the matching event sequence the feature plays it out
	// first and the feature phase replaces at the end, so the removal here
	// stops. Burn completion is not the transition — pass 3c tears down and
	// stamps `featureburnt` directly — so it never consults a sequence.
	if cause == CauseDead || cause == CauseReclaim {
		if s.transitionFeatureAt(cx, cz, def, cause == CauseReclaim) {
			return
		}
	}
	var succ *content.FeatureDef
	if def != nil {
		switch cause {
		case CauseDead:
			succ = def.FeatureDeadDef // featuredead successor [05 ...]
		case CauseReclaim:
			succ = def.FeatureReclamateDef // featurereclamate [05 ...]
		case CauseBurnt:
			succ = def.FeatureBurntDef // featureburnt [05 ...]
		}
	}
	if cause == CauseBurnt {
		// Burn completion is a bare stamp [05 R-FEAT-01 §10 pass 3c].
		s.replaceFeatureStamp(cx, cz, succ, false)
		return
	}
	s.replaceFeatureAt(cx, cz, succ)
}

// InstanceAt returns the live instance at anchor cell or nil.
func (s *Service) InstanceAt(cx, cz int) *Instance {
	if s.Terrain == nil {
		return nil
	}
	idx := cz*int(s.Terrain.CellW) + cx
	return s.instances[idx]
}

// AppendInstances appends live records in ascending row/anchor order to dst.
// The caller owns the slice; records remain borrowed from the service. This
// read-only lookup order is separate from the active simulation list
// [05 "Feature instance and terrain cell"][I1].
func (s *Service) AppendInstances(dst []*Instance) []*Instance {
	for _, key := range s.sortedInstanceKeys() {
		if inst := s.instances[key]; inst != nil {
			dst = append(dst, inst)
		}
	}
	return dst
}

// Instances returns a pointer snapshot in deterministic row/anchor order.
// Callers that reuse storage can use AppendInstances instead.
func (s *Service) Instances() []*Instance {
	return s.AppendInstances(make([]*Instance, 0, len(s.instances)))
}

// PopulateFromTerrain scans the terrain plot and creates live instances for every
// anchor cell that already holds a real feature index. It is the bridge between
// world.Load's Plot expansion (which stamps 0xFFFF/0xFFFE/0xFFFD and fringe
// anchors) and the Features Service's authoritative instance map. Without this
// pass the map-authored forest on Great Divide would remain as plot sentinels
// for placement blocking but never appear in snapshot Features, so the renderer
// would draw an empty forest [05 "Feature instance and terrain cell"] [03 §5.1].
// It is idempotent: anchors already present in the map are skipped, fringe
// cells are never instanced, and void/empty sentinels are ignored. Presentation-only
// after battle entry (I6) — it does not advance RNG or sim state.
func (s *Service) PopulateFromTerrain() int {
	if s == nil || s.Terrain == nil || s.Terrain.Plot == nil {
		return 0
	}
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	if w <= 0 || h <= 0 {
		return 0
	}
	if len(s.Terrain.Plot) < w*h {
		return 0
	}
	n := 0
	// The arena is charged only by the anchors the stamp allocates a slot for
	// [05 R-FEAT-01 §3 steps 4-5]; a map's resting sprite anchors are free.
	// setInstance bills a 3D record and puts it on the active list, so the
	// count read below is live across the walk, which is idempotent and may
	// run again after a load.
	for cz := 0; cz < h; cz++ {
		for cx := 0; cx < w; cx++ {
			idx := cz*w + cx
			if idx < 0 || idx >= len(s.Terrain.Plot) {
				continue
			}
			// Only anchors carry real indices; fringe (0xFFFE) and voids are skipped
			// via IsEmpty/IsVoid checks, and ResolveFeature would follow fringe to
			// its anchor which we already handle.
			cell := s.Terrain.Plot[idx]
			if cell.IsFringe() || cell.IsVoid() || cell.IsEmpty() {
				continue
			}
			if !cell.IsRealFeature() {
				continue
			}
			if _, ok := s.instances[idx]; ok {
				continue // already instanced (e.g. mission feature placed earlier)
			}
			def, ok := s.Terrain.FeatureDefAt(cell.Feature())
			if !ok || def == nil {
				continue // unbound name or out-of-range index: blocking sentinel but no visual
			}
			footX := def.FootprintX
			footZ := def.FootprintZ
			if footX <= 0 {
				footX = 1
			}
			if footZ <= 0 {
				footZ = 1
			}
			// A 3D anchor pops an arena slot; an empty free list means the
			// stamp produced no live record for it [05 R-FEAT-01 §3 step 4].
			// The walk CONTINUES rather than stopping: retail's loader stamps
			// every remaining cell, and the sprite anchors after this one still
			// need no slot.
			if !isSpriteDef(def) && s.arenaOccupants() >= FeatureAnimSlots {
				continue
			}
			inst := &Instance{
				Def:        def,
				Terrain:    s.Terrain,
				CX:         cx,
				CZ:         cz,
				FootprintX: footX,
				FootprintZ: footZ,
			}
			inst.X = world.CellToWorld(int32(cx)).Add(numeric.Fixed(int64(footX) * 1048576 / 2))
			inst.Z = world.CellToWorld(int32(cz)).Add(numeric.Fixed(int64(footZ) * 1048576 / 2))
			inst.Y = s.Terrain.HeightAt(inst.X, inst.Z) // map stamp [05 R-FEAT-01 §3]
			s.setInstance(idx, inst)
			// A map-authored vent reaches its instance here rather than through
			// spawnFeatureAt, because the map loader writes the plot grid
			// directly and this walk builds the animation side from it. Retail
			// has one stamp for both cases, and the steam producer hangs off it
			// [05 R-ECO-02 §3], so both of Nanolathe's halves have to call it or
			// the vents a MAP places would be the ones that never steam.
			if def.Geothermal && s.GeothermalSteam != nil {
				s.GeothermalSteam(inst.X, inst.Y, inst.Z)
			}
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// The feature grid, reachable without this service
// ---------------------------------------------------------------------------
//
// Retail keeps two structures for one feature: the terrain's feature grid — the
// authoritative record of what stands on a cell, which the stamp writes and the
// transition rewrites [05 R-FEAT-01 §3, §5] — and the animation-instance side
// that carries the burning, sinking and reclaim-animation state, linked to the
// grid by the anchor cell's instance-attached bit [05 R-FEAT-01 §15]. Nanolathe
// splits them the same way: `world.Terrain.Plot` is the grid and this service's
// instance map is the animation side.
//
// The feature-reclaim executor lives in internal/orders, which reaches the
// terrain through its queue's economy service but has no reference to this
// service. The two functions below are therefore the grid half of the resolver
// and the transition, taking a terrain rather than a receiver, and
// syncInstancesToGrid is the animation-side half that follows them.

// FeatureAt is the world-position feature resolver of [05 R-ECO-02 §2]. Given a
// 16.16 world position it floors to the sixteen-unit attribute cell, hops a
// fringe cell to its anchor through the two stored signed offset bytes, and
// reports the ANCHOR cell together with the definition its index binds to.
// Off-map, an empty cell, a void threshold and a fringe whose anchor carries no
// live index all report not found — retail's NONE, which the feature-reclaim
// executor turns into `Reclamation failed` [04 R-ORD-01 §5].
//
// The shifts are arithmetic on signed words, so a position west or north of the
// map floors to a negative cell and reports off-map; world.WorldToCell is that
// floor [03 §2.1].
func FeatureAt(t *world.Terrain, x, z numeric.Fixed) (def *content.FeatureDef, cx, cz int, ok bool) {
	if t == nil || t.CellW <= 0 || t.CellH <= 0 {
		return nil, 0, 0, false
	}
	cx = int(world.WorldToCell(x))
	cz = int(world.WorldToCell(z))
	cell := t.PlotAt(int32(cx), int32(cz))
	if cell == nil {
		return nil, 0, 0, false
	}
	if cell.IsFringe() {
		// The hop uses the stored signed offsets exactly as the stamp wrote
		// them; retail does not re-test the hopped cell for off-map, but a Go
		// index out of range is a fault rather than a stale read, so PlotAt's
		// bounds test stands in for it [05 R-ECO-02 §2].
		cx += int(cell.AnchorDXSigned())
		cz += int(cell.AnchorDZSigned())
		cell = t.PlotAt(int32(cx), int32(cz))
		if cell == nil {
			return nil, 0, 0, false
		}
	}
	if !cell.IsRealFeature() {
		return nil, 0, 0, false
	}
	def, bound := t.FeatureDefAt(cell.Feature())
	if !bound || def == nil {
		return nil, 0, 0, false
	}
	return def, cx, cz, true
}

// ReclaimTransition is the grid half of the feature-reclaim payout
// [05 R-WORK-01 §5]: it reports the definition's WHOLE metal and energy pools —
// the payout is a one-time completion event, never a per-tick drip — and
// replaces the feature with its `featurereclamate` successor, or removes it
// when the definition names none [05 "Removal and successor replacement"].
// The caller owns the credit; this owns the grid.
//
// A definition that is not reclaimable, or is indestructible, pays nothing and
// is left standing, which is the executor's silent abandon arm.
//
// isSpriteDef reports the definition half of the payout guard of
// [05 R-FEAT-01 §15]: flag bit 0, "sprite (filename-based) definition". The
// authored key is `filename`, the sprite source used when a definition names no
// model [02 "Feature record"] — `tree1` is `filename trees`, while `armaap_dead`
// has none because it is a 3D wreck [05 R-WORK-01 §5-A].
func isSpriteDef(def *content.FeatureDef) bool {
	return def != nil && def.Filename != ""
}

// is3DDef is the complement the stamp writes the anchor's instance-attached bit
// for [05 R-FEAT-01 §15]: an authored 3DO model and no sprite source. The two
// predicates are deliberately not each other's negation — a definition naming
// neither is neither, and takes the bit from no site.
func is3DDef(def *content.FeatureDef) bool {
	return def != nil && def.Object != "" && def.Filename == ""
}

// The third refusal is the payout guard of [05 R-FEAT-01 §15]: the helper
// refuses outright when the anchor cell's instance-attached bit AND the
// definition's sprite bit are both set. The conjunction means "a sprite feature
// that currently has a live animation instance" — one that is burning, or
// already playing its death or reclaim animation — which is what "burning
// blocks reclaim" describes. It never applies to a 3D wreck: the stamp sets a
// 3D definition's instance bit always, but its definition bit is clear, so a
// sinking wreck stays reclaimable throughout.
func ReclaimTransition(t *world.Terrain, cx, cz int) (metal, energy float32, ok bool) {
	if t == nil {
		return 0, 0, false
	}
	cell := t.PlotAt(int32(cx), int32(cz))
	if cell == nil || !cell.IsRealFeature() {
		return 0, 0, false
	}
	def, bound := t.FeatureDefAt(cell.Feature())
	if !bound || def == nil {
		return 0, 0, false
	}
	if !def.Reclaimable || def.Indestructible {
		return 0, 0, false
	}
	if isSpriteDef(def) && cell.Occupied() {
		return 0, 0, false // the payout guard's two bits [05 R-FEAT-01 §15]
	}
	// I2 allowlist: the pools cross into the economy ledger as float32
	// contributions [05 "Feature reclaim"][05 R-ECO-01 §2].
	metal = float32(def.Metal)
	energy = float32(def.Energy)
	clearFeatureRect(t, cx, cz, def)
	successor := def.FeatureReclamateDef
	placed := false
	if successor != nil {
		placed = stampFeatureDef(t, cx, cz, successor, nil)
	}
	// One revision bump for the whole replacement: a blocking successor bumps
	// inside the stamp, so this covers only the case where the blocking
	// footprint went away [05 "Removal and successor replacement"].
	if def.Blocking && (!placed || successor == nil || !successor.Blocking) {
		t.BumpStaticObstacleRevision()
	}
	return metal, energy, true
}

// clearFeatureRect returns a stamped footprint to the free sentinel. It is the
// grid-only twin of clearFootprintNoRevision, which additionally releases this
// service's instances.
func clearFeatureRect(t *world.Terrain, cx, cz int, def *content.FeatureDef) {
	if t == nil {
		return
	}
	fx, fz := 1, 1
	if def != nil {
		if def.FootprintX > 0 {
			fx = int(def.FootprintX)
		}
		if def.FootprintZ > 0 {
			fz = int(def.FootprintZ)
		}
	}
	for dz := 0; dz < fz; dz++ {
		for dx := 0; dx < fx; dx++ {
			cell := t.PlotAt(int32(cx+dx), int32(cz+dz))
			if cell == nil {
				continue
			}
			cell.SetFeature(world.PlotFeatureNone) // free sentinel [GAP T14]
			cell.SetFlagByte(0)
			cell.SetAnchor(0, 0)
		}
	}
}

// stampFeatureDef writes a definition's footprint rectangle at an anchor cell,
// binding the definition into the terrain's own record list when the map did
// not author it — the same admission spawnFeatureAt performs, so a successor
// the map never names can still be stamped [05 R-FEAT-01 §2].
func stampFeatureDef(t *world.Terrain, cx, cz int, def *content.FeatureDef, observer func(*content.FeatureDef)) bool {
	if t == nil || def == nil {
		return false
	}
	idx := featureIndexIn(t, def)
	if idx == world.PlotFeatureNone {
		// The catalog has no cap: it is reallocated per record and "catalog
		// exhaustion" is not a retail failure mode [05 R-FEAT-01 §1].
		t.FeatureDefs = append(t.FeatureDefs, def)
		if observer != nil {
			observer(def)
		}
		idx = uint16(len(t.FeatureDefs) - 1)
	}
	footX, footZ := def.FootprintX, def.FootprintZ
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	if err := t.StampFeatureRect(int32(cx), int32(cz), idx, footX, footZ); err != nil {
		return false
	}
	if def.Blocking {
		t.BumpStaticObstacleRevision()
	}
	return true
}

func featureIndexIn(t *world.Terrain, def *content.FeatureDef) uint16 {
	if t == nil || def == nil {
		return world.PlotFeatureNone
	}
	for i, d := range t.FeatureDefs {
		if d == def {
			return uint16(i)
		}
	}
	for i, d := range t.FeatureDefs {
		if d != nil && d.CanonicalKey == def.CanonicalKey {
			return uint16(i)
		}
	}
	return world.PlotFeatureNone
}

// syncInstancesToGrid is the animation-side half of a grid transition performed
// by a caller that holds the terrain but not this service — the feature-reclaim
// executor of [04 R-ORD-01 §5] phase 5, and the resurrection grid removal. The
// grid is authoritative for what stands on a cell [05 R-FEAT-01 §3, §5]; an
// instance whose anchor no longer carries its definition is a stale animation
// record, and one whose anchor now carries a DIFFERENT definition is the
// successor that transition stamped. TickMotion runs it in phase 4, after
// the unit phase's order and construction work.
//
// The walk is over sorted instance keys, so it is deterministic (I1), and it is
// idempotent: with no grid transition since the last visit it changes nothing.
func (s *Service) syncInstancesToGrid() {
	if s == nil || s.Terrain == nil || s.Terrain.CellW <= 0 {
		return
	}
	w := int(s.Terrain.CellW)
	for _, idx := range s.sortedInstanceKeys() {
		inst := s.instances[idx]
		if inst == nil || inst.Def == nil {
			s.deleteInstance(idx)
			continue
		}
		cx, cz := idx%w, idx/w
		cell := s.Terrain.PlotAt(int32(cx), int32(cz))
		if cell == nil || !cell.IsRealFeature() {
			s.deleteInstance(idx)
			continue
		}
		def, bound := s.Terrain.FeatureDefAt(cell.Feature())
		if !bound || def == nil {
			s.deleteInstance(idx)
			continue
		}
		if def == inst.Def || def.CanonicalKey == inst.Def.CanonicalKey {
			continue
		}
		s.setInstance(idx, s.newInstanceAt(cx, cz, def))
	}
}

// newInstanceAt builds the animation record for a definition already stamped on
// the grid. It is the instance half of spawnFeatureAt, without the stamp.
func (s *Service) newInstanceAt(cx, cz int, def *content.FeatureDef) *Instance {
	footX, footZ := def.FootprintX, def.FootprintZ
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	inst := &Instance{
		Def:        def,
		Terrain:    s.Terrain,
		CX:         cx,
		CZ:         cz,
		FootprintX: footX,
		FootprintZ: footZ,
	}
	inst.X = world.CellToWorld(int32(cx)).Add(numeric.Fixed(int64(footX) * 1048576 / 2))
	inst.Z = world.CellToWorld(int32(cz)).Add(numeric.Fixed(int64(footZ) * 1048576 / 2))
	inst.Y = s.Terrain.HeightAt(inst.X, inst.Z) // reconstructed stamp [05 R-FEAT-01 §3]
	return inst
}

// Cursor returns the current global cursor value for tests [06 §13.1].
func (s *Service) Cursor() int { return s.cursor }

// SetCursor sets the cursor for tests.
func (s *Service) SetCursor(c int) { s.cursor = c }
