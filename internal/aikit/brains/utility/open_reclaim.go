package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Early reclaim (open_reclaim; README §13.13). On maps with metal-bearing
// rocks and wrecks near the start, human openings reclaim heavily: on
// Painted Desert the recorded players reclaimed 27–42 features by minute
// five and produced about twice this brain's metal. Reclaiming a feature
// takes a fixed fifteen ticks plus half its energy and metal pools,
// whatever the builder's build power [05 R-WORK-01 §5], so a builder next
// to a pile of rocks gathers metal quickly; but an extractor or an energy
// building pays for the rest of the game, and reclaiming whenever a pile
// was near cost the brain its early extractors (ten-minute screens: metal
// produced by minute five fell 16–26% at weights 200–300). So a Clear is
// offered only while metal is short — spending above income and the metal
// store below half, fully from an eighth — when the builders already at
// work spend the whole income and one more building would only share it.
//
// Each metal feature near the start (Obs.Features) is a candidate job: a
// Clear at it (Kit.Clear reclaims up to four features holding metal
// within the radius, the richest per distance first). The job's metal and
// work are those of the four richest features around it; its time adds
// the walk there and from it to each. The score is the metal need × the
// job's metal rate against openRefRate × site threat (squared for the
// commander, which also keeps its leash) × the short-metal gate × a fade
// (full to minute 6, none from minute 12), at the switch's weight. The
// rest of the game keeps the layout switch's wreck clearing
// (econ_unblock.go).

const (
	openClearR      = 160   // radius of an opening Clear, world units
	openClearPicks  = 4     // features one Clear reclaims (Kit.Clear)
	openRefRate     = 5000  // milli-metal per second of job rate rated 1000
	openRateCap     = 3000  // most a job's rate can count, permille
	openReclaimFull = 10800 // full weight to minute 6
	openReclaimEnd  = 21600 // none from minute 12
	openRecentTicks = 330   // a pile just sent to is skipped until the listing refreshes
	openRecentN     = 8
	openStallHi     = 500 // metal store permille at which the gate shuts (open fully from a quarter of it)
	openReclaimW    = 200 // the default weight (open_reclaim)
)

// openJob is the estimate of one opening Clear at a metal feature.
type openJob struct {
	x, z  int32
	metal int32 // metal the Clear gathers
	work  int32 // ticks of reclaim work
	walk  int32 // world units walked between the picks (from the job's feature to each)
	picks int32
}

// openRecent is a pile a builder was sent to lately.
type openRecent struct {
	x, z int32
	tick uint32
}

// openReclaim is the early-reclaim part's state.
type openReclaim struct {
	w    int32 // weight, percent (0 = off)
	hi   int32 // metal gate: store permille at which it shuts (0 = openStallHi)
	end  int32 // minute from which there is none (0 = openReclaimEnd)
	jobs []openJob
	tick uint32 // jobs are for this think
	ok   bool
	// Scratch: metal features bucketed by openClearR cells (openJobs).
	start, item, fill []int32
	recent            [openRecentN]openRecent
	next              int
	// Report: Clears ordered and the metal they were estimated to gather.
	orders int32
	metal  int64
}

// openJobs estimates this think's jobs once (every builder reads them).
// Metal features are bucketed by openClearR cells, so each job reads only
// the 3×3 cells around its feature.
func (s *shared) openJobs(o *aikit.Obs) []openJob {
	r := &s.open.rec
	if r.ok && r.tick == s.tick {
		return r.jobs
	}
	r.ok, r.tick = true, s.tick
	r.jobs = r.jobs[:0]
	R := int32(openClearR)
	var x0, z0, x1, z1 int32
	first := true
	for i := range o.Features {
		if g := &o.Features[i]; g.Reclaimable && g.Metal > 0 {
			if first {
				x0, z0, x1, z1, first = g.X, g.Z, g.X, g.Z, false
			}
			x0, z0, x1, z1 = min(x0, g.X), min(z0, g.Z), max(x1, g.X), max(z1, g.Z)
		}
	}
	if first {
		return r.jobs
	}
	w, h := (x1-x0)/R+1, (z1-z0)/R+1
	cell := func(x, z int32) int32 { return (z-z0)/R*w + (x-x0)/R }
	r.start = append(r.start[:0], make([]int32, w*h+1)...)
	for i := range o.Features {
		if g := &o.Features[i]; g.Reclaimable && g.Metal > 0 {
			r.start[cell(g.X, g.Z)+1]++
		}
	}
	for c := int32(1); c <= w*h; c++ {
		r.start[c] += r.start[c-1]
	}
	r.item = append(r.item[:0], make([]int32, r.start[w*h])...)
	r.fill = append(r.fill[:0], r.start[:w*h]...)
	for i := range o.Features {
		if g := &o.Features[i]; g.Reclaimable && g.Metal > 0 {
			c := cell(g.X, g.Z)
			r.item[r.fill[c]] = int32(i)
			r.fill[c]++
		}
	}
	for i := range o.Features {
		f := &o.Features[i]
		if !f.Reclaimable || f.Metal <= 0 {
			continue
		}
		// The four richest metal features within the Clear's radius (the
		// feature itself included), ties to the earlier listed.
		var top [openClearPicks]int32 // indices into o.Features, by metal
		n := 0
		cx, cz := (f.X-x0)/R, (f.Z-z0)/R
		for zz := max(cz-1, 0); zz <= min(cz+1, h-1); zz++ {
			for xx := max(cx-1, 0); xx <= min(cx+1, w-1); xx++ {
				c := zz*w + xx
				for _, j := range r.item[r.start[c]:r.start[c+1]] {
					g := &o.Features[j]
					if aikit.Dist2(g.X, g.Z, f.X, f.Z) > int64(R)*int64(R) {
						continue
					}
					pos := n
					for pos > 0 && (o.Features[top[pos-1]].Metal < g.Metal ||
						(o.Features[top[pos-1]].Metal == g.Metal && top[pos-1] > j)) {
						pos--
					}
					if pos >= openClearPicks {
						continue
					}
					end := n
					if end >= openClearPicks {
						end = openClearPicks - 1
					}
					for k := end; k > pos; k-- {
						top[k] = top[k-1]
					}
					top[pos] = j
					if n < openClearPicks {
						n++
					}
				}
			}
		}
		job := openJob{x: f.X, z: f.Z, picks: int32(n)}
		for k := 0; k < n; k++ {
			g := &o.Features[top[k]]
			job.metal += g.Metal
			job.work += 15 + (g.Energy+g.Metal)/2
			job.walk += aikit.Dist(f.X, f.Z, g.X, g.Z)
		}
		r.jobs = append(r.jobs, job)
	}
	return r.jobs
}

// recentAt reports whether a pile at (x, z) was sent to lately.
func (r *openReclaim) recentAt(x, z int32, tick uint32) bool {
	for i := range r.recent {
		c := &r.recent[i]
		if c.tick != 0 && tick-c.tick < openRecentTicks && aikit.Dist2(c.x, c.z, x, z) <= openClearR*openClearR {
			return true
		}
	}
	return false
}

// sent records an opening Clear.
func (r *openReclaim) sent(x, z int32, tick uint32, metal int64) {
	r.recent[r.next] = openRecent{x: x, z: z, tick: tick}
	r.next = (r.next + 1) % openRecentN
	r.orders++
	r.metal += metal
}

// evalOpenReclaim offers the builder's best opening Clear.
func (e *Economy) evalOpenReclaim(b *core.Board, u *aikit.OwnUnit, d *decision) {
	s := e.s
	r := &s.open.rec
	end, full := int64(openReclaimEnd), int64(openReclaimFull)
	if r.end > 0 {
		end, full = int64(r.end)*1800, int64(r.end)*900
	}
	if r.w == 0 || int64(s.tick) >= end || u.Info.Def == nil || !u.Info.Def.CanReclamate {
		return
	}
	fade := lin(int64(s.tick), end, full)
	// Only while metal is short: spending above income and the store
	// below openStallHi (full from a quarter of that). The builders at
	// work already spend the income, so one more building would only
	// share it, while a Clear adds metal.
	var gate int64
	if s.mCap > 0 && s.mInc < s.mExp {
		hi := int64(openStallHi)
		if r.hi > 0 {
			hi = int64(r.hi)
		}
		gate = lin(s.mStock*1000/s.mCap, hi, hi/4)
	}
	base := mul(mul(mul(int64(r.w)*10, s.needM), fade), gate)
	if base == 0 {
		return
	}
	com := u.Info.Role.Has(aikit.RoleCommander)
	speed := speedOf(u.Info)
	lim := int64(s.p.ComRadius) * int64(s.p.ComRadius)
	best := cand{kind: cClear, spot: -1, spacing: openClearR}
	for _, j := range s.openJobs(b.O) {
		if com && aikit.Dist2(j.x, j.z, b.HomeX, b.HomeZ) > lim {
			continue
		}
		if r.recentAt(j.x, j.z, s.tick) {
			continue
		}
		dist := int64(aikit.Dist(u.X, u.Z, j.x, j.z))
		// Seconds: the walk there and between the picks, and the work.
		secs := (dist+int64(j.walk))/speed + int64(j.work)/30
		rate := int64(j.metal) * 1000 / max64(secs, 1)
		q := min64(rate*one/openRefRate, openRateCap)
		sc := mul(base, q)
		if sc <= best.score {
			continue
		}
		thr := half(int64(b.Threat.At(j.x, j.z)), int64(s.p.ThreatHalf))
		if com {
			thr = mul(thr, thr)
		}
		sc = mul(sc, thr)
		if sc > best.score {
			best.score, best.x, best.z, best.gainM = sc, j.x, j.z, int64(j.metal)
			best.f = [4]int64{s.needM, q, fade, thr}
		}
	}
	if best.score > 0 {
		d.offer(&best)
	}
}
