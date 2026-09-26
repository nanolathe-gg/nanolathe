package survival

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Army wraps the tactics army. The posture already forbids an offensive;
// the wrapper also shows the army a Survival world: its home is the
// defence centre between the start site and this survivor's start, the
// enemy lies along the threatened bearing (an allied building under fire,
// else a warned direction, else the attackers in view, else this
// survivor's outer side), and only attackers within reach of the team's
// ground are in its picture, so it gathers on the threatened side of the
// towers and fights what comes instead of chasing stragglers toward a map
// edge.
type Army struct {
	st    *state
	inner core.Policy
	view  aikit.Obs
}

// Init implements core.Policy.
func (a *Army) Init(b *core.Board) {
	if a.inner != nil {
		a.inner.Init(b)
	}
}

// Plan implements core.Policy.
func (a *Army) Plan(b *core.Board) {
	st := a.st
	if !st.ready {
		st.setup(b)
	}
	if a.inner == nil {
		return
	}
	o := b.O
	hx, hz, ex, ez, known := b.HomeX, b.HomeZ, b.EnemyX, b.EnemyZ, b.EnemyKnown
	b.HomeX, b.HomeZ = st.dx, st.dz
	b.EnemyX, b.EnemyZ = st.threatPoint(b)
	b.EnemyKnown = false
	b.O = a.near(b)
	a.inner.Plan(b)
	b.O = o
	b.HomeX, b.HomeZ, b.EnemyX, b.EnemyZ, b.EnemyKnown = hx, hz, ex, ez, known
}

// armyReach is how far beyond the survivor's outermost building the army's
// picture reaches.
const armyReach = 1400

// near is the observation the army sees: this think's, with the enemy
// contacts and memories beyond the team's ground left out.
func (a *Army) near(b *core.Board) *aikit.Obs {
	st := a.st
	o := b.O
	r := int64(humanRoom)
	for s := range st.radius {
		r = max(r, int64(st.radius[s]))
	}
	r += armyReach
	r2 := r * r
	v := &a.view
	enemy, memory := v.Enemy[:0], v.Memory[:0]
	*v = *o
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if aikit.Dist2(c.X, c.Z, st.cx, st.cz) <= r2 {
			enemy = append(enemy, *c)
		}
	}
	for i := range o.Memory {
		m := &o.Memory[i]
		if aikit.Dist2(m.X, m.Z, st.cx, st.cz) <= r2 {
			memory = append(memory, *m)
		}
	}
	v.Enemy, v.Memory = enemy, memory
	return v
}

// threatDist is how far from the defence centre the army's enemy point
// stands on the threatened bearing: the tactics army gathers a quarter to
// under half of the way there, on the towers' line.
const threatDist = 1600

// threatPoint is the army's enemy point: along the bearing of the allied
// building under fire this survivor weighs most, else of the warned ground
// approach it weighs most, else of the armed attackers in view near the
// team's ground, else this survivor's outer side.
func (st *state) threatPoint(b *core.Board) (int32, int32) {
	m := b.K.Map
	var vx, vz int64
	best := int64(-1)
	// An allied building under fire comes first: the waves go for the
	// building nearest them, and a human's base draws them to the start
	// site the army holds.
	if a := &st.ally; a.hit && aikit.Dist2(a.hitX, a.hitZ, st.dx, st.dz) >= 64*64 {
		vx, vz = int64(a.hitX-st.dx), int64(a.hitZ-st.dz)
		best = 1 << 62
	}
	for _, g := range st.live {
		if g.Air {
			continue
		}
		s := sectorOfAngle(g.Angle)
		w := st.weight[s]
		if !st.mine[s] {
			// Not this survivor's side: it still comes if nobody's is.
			w = 0
		}
		if w > best {
			best = w
			vx, vz = sectorDir[s][0], sectorDir[s][1]
		}
	}
	if best < 0 {
		var tx, tz, tw int64
		o := b.O
		for i := range o.Enemy {
			c := &o.Enemy[i]
			if c.Info == nil || c.Info.DPS == 0 || !c.Info.Role.Has(aikit.RoleMobile) {
				continue
			}
			if aikit.Dist2(c.X, c.Z, st.cx, st.cz) > 2500*2500 {
				continue
			}
			w := int64(c.Info.Value)
			tx += int64(c.X) * w
			tz += int64(c.Z) * w
			tw += w
		}
		if tw > 0 {
			vx, vz = tx/tw-int64(st.dx), tz/tw-int64(st.dz)
		}
	}
	if vx == 0 && vz == 0 {
		vx, vz = st.outX, st.outZ
	}
	d := max(aikit.ISqrt64(vx*vx+vz*vz), 1)
	return clampWorld(int64(st.dx)+vx*threatDist/d, m.WorldW), clampWorld(int64(st.dz)+vz*threatDist/d, m.WorldH)
}

// Explain implements core.Explaining.
func (a *Army) Explain(b *core.Board, x *aikit.Explain) {
	if e, ok := a.inner.(core.Explaining); ok {
		e.Explain(b, x)
	}
}

// Report implements aikit.Reporter for the army's own counters.
func (a *Army) Report(add func(name string, value int64)) {
	if r, ok := a.inner.(aikit.Reporter); ok {
		r.Report(add)
	}
}
