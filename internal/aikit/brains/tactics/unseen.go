package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// The unseen enemy land army (unseen switch, default on).
//
// Ships and aircraft judge a target by what is remembered around it, and a
// remembered mobile fades from the picture within a minute of leaving
// sight. The enemy's land army mostly waits at its base, out of sight, so a
// fleet shelling that coast or a strike wing over it met the whole army
// with nothing of it in the prediction: on pillopeens the fleet sailed into
// fire from sixty tanks and lost seven destroyers, and on shore to shore
// bombers went back to a base guarded by missile trucks every three
// minutes, as their anti-air memory faded, and lost 1,200-2,100 in value.
//
// The army keeps the largest enemy land army it has seen (ground units:
// not aircraft, ships or hovercraft, not the commander), fading with a
// five-minute time constant without a new sighting, and where it was last
// seen in strength (at least half that value).
// The part of it not in sight now is counted:
//   - by the fleet, at naval targets and when it sails to explore, within
//     landReserveR of where that army was last seen or of the enemy base;
//   - by the strike wing, as anti-air at targets within the same radius.
//
// Elsewhere it counts for nothing: a land army defends its base, and
// outlying coasts and economy are what ships and bombers should raid.
//
// The strike wing also calibrates its model by the sorties it has flown
// this game: the loss it expected against what it lost, the gain it
// expected against what it destroyed. The anti-air picture is always
// partial and a wing shot down destroys nothing: on shore to shore the
// first sortie expected to lose 308 for a gain of 757 and lost 900 for
// nothing, and every later one was judged by the same optimistic model.

const (
	landMemTicks = 9000 // time constant of the land army memory's fade
	calPrior     = 400  // strike calibration prior (value) on the model's and the real side
	landReserveR = 2500 // it defends within this distance of where it was seen or of the enemy base
)

// landPicture refreshes the enemy land army in sight (freshness-weighted)
// and its memory.
func (a *Army) landPicture(b *core.Board) {
	o := b.O
	a.enemyLand = force{}
	var x, z, w int64
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		if r.Building || info.DPS <= 0 || info.Role.Any(aikit.RoleAir|aikit.RoleCommander) {
			continue
		}
		if k := a.cls(info).kind; k == ukNaval || k == ukHover {
			continue
		}
		f := freshness(r, b.Tick)
		a.enemyLand.add(info, int64(info.HP), f)
		x += int64(r.X) * f
		z += int64(r.Z) * f
		w += f
	}
	// The memory is an envelope: each part (firepower, hit points,
	// anti-air, value) keeps the largest amount seen, fading with a time
	// constant of landMemTicks. Parts are kept apart because sightings are
	// partial: the anti-air trucks are seen one minute and the tanks the
	// next.
	const ppm = 1000000
	keep := int64(ppm) - int64(a.dt)*ppm/landMemTicks
	if keep < 0 {
		keep = 0
	}
	mk := &a.landMemK // thousandths, so small amounts fade rather than truncate away
	cur := &a.enemyLand
	mk.dps = max64(mk.dps*keep/ppm, cur.dps*1000)
	mk.hp = max64(mk.hp*keep/ppm, cur.hp*1000)
	mk.aa = max64(mk.aa*keep/ppm, cur.aa*1000)
	mk.value = max64(mk.value*keep/ppm, cur.value*1000)
	mk.rngW = max64(mk.rngW*keep/ppm, cur.rngW*1000)
	m := &a.landMem
	m.dps, m.hp, m.aa, m.value, m.rngW = mk.dps/1000, mk.hp/1000, mk.aa/1000, mk.value/1000, mk.rngW/1000
	if w > 0 && cur.value*2 >= m.value {
		a.landX, a.landZ = int32(x/w), int32(z/w)
	}
}

// landUnseen is the part of the remembered land army not in sight now,
// where it defends (x, z); false when the point is not near it.
func (a *Army) landUnseen(b *core.Board, x, z int32) (force, bool) {
	if !a.P.Unseen {
		return force{}, false
	}
	near := aikit.Dist2(x, z, a.landX, a.landZ) <= landReserveR*landReserveR ||
		(b.EnemyKnown && aikit.Dist2(x, z, b.EnemyX, b.EnemyZ) <= landReserveR*landReserveR)
	if !near {
		return force{}, false
	}
	f := a.landMem
	cur := &a.enemyLand
	f.dps -= cur.dps
	f.hp -= cur.hp
	f.aa -= cur.aa
	f.rngW -= cur.rngW
	f.value -= cur.value
	if f.dps <= 0 || f.hp <= 0 {
		return force{}, false
	}
	if f.rngW < 0 {
		f.rngW = 0
	}
	if f.aa < 0 {
		f.aa = 0
	}
	return f, true
}

// landReserve adds the unseen land army to the enemy a ship judges at
// (x, z).
func (a *Army) landReserve(b *core.Board, x, z int32, en *force) {
	if f, ok := a.landUnseen(b, x, z); ok {
		en.dps += f.dps
		en.hp += f.hp
		en.rngW += f.rngW
		en.value += f.value
	}
}

// aaReserve is the unseen land army's anti-air (dps) over (x, z).
func (a *Army) aaReserve(b *core.Board, x, z int32) int64 {
	if f, ok := a.landUnseen(b, x, z); ok {
		return f.aa
	}
	return 0
}

// strikeCal is the calibration of the strike model by the sorties flown
// this game, permille: the loss factor is what they lost over what the
// model expected, at least 1 and at most 5; the gain factor what they
// destroyed over what it expected, at most 1 and at least 1/5. A prior of
// calPrior on both sides keeps one sortie from swinging it fully.
func (a *Army) strikeCal() (loss, gain int64) {
	if !a.P.Unseen {
		return 1000, 1000
	}
	loss = (a.calLoss + calPrior) * 1000 / (a.calExpLoss + calPrior)
	gain = (a.calGain + calPrior) * 1000 / (a.calExpGain + calPrior)
	if loss < 1000 {
		loss = 1000
	}
	if loss > 5000 {
		loss = 5000
	}
	if gain > 1000 {
		gain = 1000
	}
	if gain < 200 {
		gain = 200
	}
	return loss, gain
}

// noteStrike adds a sortie to the calibration once it is home (or lost):
// the model's loss and gain at launch against the value of its members at
// launch less those still alive (withdrawn ones included), and the gain
// of the targets it destroyed.
func (a *Army) noteStrike(b *core.Board, s *squad) {
	if !a.P.Unseen || s.sortie == 0 || s.launchValue <= 0 {
		return
	}
	o := b.O
	var alive int64
	for i := range o.Own {
		u := &o.Own[i]
		if int(u.H) < len(a.units) && a.units[u.H].info == u.Info && a.units[u.H].gen == u.Gen && a.units[u.H].sortie == s.sortie {
			alive += int64(u.Info.Value)
		}
	}
	lost := s.launchValue - alive
	if lost < 0 {
		lost = 0
	}
	a.calExpLoss += s.launchLoss
	a.calLoss += lost
	a.calExpGain += s.launchGain
	a.calGain += s.sortieGain
	s.launchValue = 0
}
