package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Temporary sight sources ("eyeballs") [01 R-PLAT-02 §5][08 R-SESS-01 §3]
// [03 R-COMP-02 §2].
//
// A unit the local player loses keeps revealing its sight radius for sixty
// ticks after death under Circular/True line of sight. The record is a
// self-contained LOS observer that is not a unit: it carries its own stored
// coverage tile pair and coverage byte, so the visibility service's per-unit
// footprint store is never involved. The list is fixed at twenty records,
// starts empty at battle entry, and is consumed by the executor tail's expiry
// pass, which runs after the twelve phases and the deadline-ring slide as the
// tail's last step. Iteration is by list order only; no RNG draw is made.

const (
	// eyeballCapacity is the fixed record count of the block allocated at
	// battle entry; the append at capacity is silently dropped [01 R-PLAT-02 §5].
	eyeballCapacity = 20
	// eyeballLifetimeTicks is added to the global tick at death to form the
	// record's expiry [08 R-SESS-01 §3].
	eyeballLifetimeTicks = 60
)

// eyeballRecord is one temporary sight source [01 R-PLAT-02 §5]. The first
// group is the record as retail fills it; the second is the record's own
// inline stored footprint — the tile pair and coverage byte the refresh
// throttle and the decrement publisher read [03 R-VIS-01 §2].
type eyeballRecord struct {
	owner         visibility.PlayerID
	sightDistance int16 // definition sightdistance, signed 16-bit [fmt fbi]
	heightByte    uint8 // low byte of the definition's model-top field [04 R-SPEC-01 §15]
	x, y, z       numeric.Fixed
	expiry        uint32

	// Stored footprint. published is the nanolathe form of "a raster was
	// written for this tile pair": an out-of-bounds origin stores the tile
	// pair with nothing published [03 R-VIS-01 §2].
	cx, cz    int32
	emitter   uint8 // stored coverage byte as the terrain-ray branch defines it
	published bool
}

// eyeballList is the fixed-capacity temporary-sight list. Records are held
// in append order and compacted stably [03 R-COMP-02 §2].
type eyeballList struct {
	records []eyeballRecord
}

// appendDeathEyeball is the producer: the central unit-death handler's
// temporary-sight append [08 R-SESS-01 §3]. It appends only when, in order,
// the victim still carries the live bit, its owner is the local slot, the LOS
// mode word has bit 1 set (Circular/True — never Permanent), and the list
// holds fewer than twenty records. The coverage is published before the count
// is incremented.
func (s *Session) appendDeathEyeball(u *units.Unit) {
	if s == nil || s.Vis == nil || u == nil || !u.Alive || s.Clock == nil {
		return
	}
	if int(u.Owner) != localPlayerForSession(s) {
		return
	}
	mode := s.Vis.Mode()
	if !mode.CurrentEnabled() {
		return
	}
	state := postLoopStateFor(s)
	if state == nil || len(state.eyeballs.records) >= eyeballCapacity {
		return
	}
	rec := eyeballRecord{
		owner:  visibility.PlayerID(u.Owner),
		x:      u.X,
		y:      u.Y,
		z:      u.Z,
		expiry: s.Clock.GlobalTick + eyeballLifetimeTicks,
	}
	if u.Def != nil {
		rec.sightDistance = int16(u.Def.SightDistance)
		rec.heightByte = uint8(u.Def.ModelTop)
	}
	// The record's Y is raised to at least (SeaLevel + 1) << 16 so both rasters
	// see the raised value [03 R-VIS-01 §2].
	if floor := numeric.Fixed(int64(seaLevelFor(s))+1) << 16; rec.y < floor {
		rec.y = floor
	}
	// Coverage publish, then count increment [08 R-SESS-01 §3]. The record
	// enters the throttled refresh with an empty stored footprint, so there is
	// nothing to remove and the only work is the publish: the same emitter and
	// tile arithmetic the per-unit stamp uses, then the raster the mode word's
	// bit 2 selects [03 R-VIS-01 §2].
	rec.emitter = heightByteAt(u, seaLevelFor(s))
	rec.cx, rec.cz = observerCell(s, u, rec.emitter)
	w, h := s.Vis.GridDimensions()
	if uint32(rec.cx) < uint32(w) && uint32(rec.cz) < uint32(h) {
		s.Vis.Publish(rec.owner, rec.cx, rec.cz, rec.emitter, int32(rec.sightDistance))
		rec.published = true
	}
	state.eyeballs.records = append(state.eyeballs.records, rec)
}

// expire is the post-loop expiry pass [03 R-COMP-02 §2]: every record whose
// expiry is strictly below the tick (unsigned) has its byte-grid footprint
// removed through the decrement publisher, then the survivors are compacted
// in place, stably, and the count becomes their number. A record still covers
// on its expiry tick and is removed on the next. The history footprint is
// never removed. Retail's "any expired" flag is uninitialised when nothing
// expired, so the compaction may run with nothing to remove; it then changes
// nothing, which is also what this loop does.
func (l *eyeballList) expire(vis *visibility.Service, tick uint32) {
	if l == nil {
		return
	}
	write := 0
	for i := range l.records {
		rec := l.records[i]
		if rec.expiry < tick {
			// The removal call is the same decrement the unit stamp uses, with
			// the record's own stored tile pair and coverage byte and no
			// coverage-byte guard [03 R-COMP-02 §2]. A record whose origin was
			// out of bounds stored nothing and has nothing to decrement.
			if vis != nil && rec.published {
				vis.Unpublish(rec.owner, rec.cx, rec.cz, rec.emitter, int32(rec.sightDistance))
			}
			continue
		}
		l.records[write] = rec
		write++
	}
	l.records = l.records[:write]
}

// EyeballCount reports the temporary-sight list's live record count. It is
// diagnostic state for tests and presentation, not a phase output.
func (s *Session) EyeballCount() int {
	state := postLoopStateFor(s)
	if state == nil {
		return 0
	}
	return len(state.eyeballs.records)
}
