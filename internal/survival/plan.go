package survival

import "github.com/nanolathe-gg/nanolathe/internal/sim/rng"

// Group is one entry direction of a wave: the angle it arrives from, its
// theme and the pool indices of the units to create, in creation order.
type Group struct {
	Angle  uint16 // 0..65535 per circle, measured from the centre site
	Domain Domain
	Picks  []int
}

// Wave is one planned wave.
type Wave struct {
	Number int
	Budget int64
	Groups []Group
}

// Units counts the wave's planned units.
func (w Wave) Units() int {
	n := 0
	for _, g := range w.Groups {
		n += len(g.Picks)
	}
	return n
}

// Entry answers whether pool unit i can enter the battle from an angle: that
// some spawn cell for its movement class on that edge reaches the base
// (DESIGN_SURVIVAL §6.6). Air units always can.
type Entry func(angle uint16, unit int) bool

// Options are the setup screen's wave switches.
type Options struct {
	NoAir, NoNaval bool
}

// angleJitter is ±30° as a fraction of the uint16 circle.
const angleJitter = 65536 / 12

// Plan builds wave n, planned at tick (DESIGN_SURVIVAL §6.2–§6.4). Draw order on the
// simulation stream: the first angle; then per group after the first its
// jitter; then per group its theme and signature count and each signature
// pick. The fill draws nothing. A draw whose bound is below two is not taken,
// which is the stream's own rule.
func Plan(n int, tick uint32, pool *Pool, t Tuning, opts Options, enter Entry, r *rng.Simulation) Wave {
	w := Wave{Number: n}
	if pool == nil || len(pool.Units) == 0 || r == nil {
		return w
	}
	w.Budget = t.Budget(tick, pool.Tier1Median)
	top := t.UnlockedTier(w.Budget, pool)
	k := t.Directions(tick)
	first := uint16(r.Uint32n(65536))
	angles := make([]uint16, k)
	for i := range angles {
		a := int64(first) + int64(i)*65536/int64(k)
		if i > 0 {
			a += int64(r.Uint32n(2*angleJitter+1)) - angleJitter
		}
		angles[i] = uint16(a & 0xffff)
	}
	share := w.Budget / int64(k)
	for _, angle := range angles {
		// Theme: weighted among domains that have an unlocked unit able to
		// enter from this angle. A direction nothing can enter from is
		// turned an eighth at a time, drawing nothing; if no direction
		// admits anything, air comes early rather than the wave being empty.
		var weights [DomainCount]int
		total := 0
		for turn := 0; turn < 8 && total == 0; turn++ {
			try := angle + uint16(turn*8192)
			weights, total = themeWeights(pool, top, tick, t, opts, enter, try, false)
			if total > 0 {
				angle = try
			}
		}
		if total == 0 {
			weights, total = themeWeights(pool, top, tick, t, opts, enter, angle, true)
		}
		if total == 0 {
			continue
		}
		g := Group{Angle: angle}
		pick := int(r.Uint32n(uint32(total)))
		for d := Domain(0); d < DomainCount; d++ {
			if pick < weights[d] {
				g.Domain = d
				break
			}
			pick -= weights[d]
		}
		// Candidates: unlocked units of the theme (ground also takes
		// amphibious) that can enter here and fit the share, weighted by
		// tier. If none fits, the theme's cheapest unit alone.
		var cand []int
		var cw []int
		cheapest := -1
		for i, u := range pool.Units {
			if u.Tier > top {
				continue
			}
			if u.Domain != g.Domain && !(g.Domain == Ground && u.Domain == Amphibious) {
				continue
			}
			if enter != nil && !enter(angle, i) {
				continue
			}
			if cheapest < 0 || u.Cost < pool.Units[cheapest].Cost {
				cheapest = i
			}
			if u.Cost > share {
				continue
			}
			cand = append(cand, i)
			if u.Tier == top {
				cw = append(cw, t.NewTierWeight)
			} else {
				cw = append(cw, 1)
			}
		}
		if len(cand) == 0 && cheapest >= 0 {
			cand, cw = []int{cheapest}, []int{1}
		}
		if len(cand) == 0 {
			continue
		}
		sigs := 1 + int(r.Uint32n(3))
		if sigs > len(cand) {
			sigs = len(cand)
		}
		var sig []int
		for s := 0; s < sigs; s++ {
			sum := 0
			for _, v := range cw {
				sum += v
			}
			p := int(r.Uint32n(uint32(sum)))
			for j, v := range cw {
				if v == 0 {
					continue
				}
				if p < v {
					sig = append(sig, cand[j])
					cw[j] = 0
					break
				}
				p -= v
			}
		}
		g.Picks = fill(pool, sig, share)
		if len(g.Picks) > 0 {
			w.Groups = append(w.Groups, g)
		}
	}
	return w
}

// themeWeights are the weights of the domains with an unlocked unit able to
// enter from angle. early admits air before AirFrom.
func themeWeights(pool *Pool, top int, tick uint32, t Tuning, opts Options, enter Entry, angle uint16, early bool) ([DomainCount]int, int) {
	var weights [DomainCount]int
	var seen [DomainCount]bool
	total := 0
	for i, u := range pool.Units {
		if u.Tier > top || seen[u.Domain] {
			continue
		}
		allowed := domainAllowed(u.Domain, tick, t, opts)
		if early && u.Domain == Air && !opts.NoAir {
			allowed = true
		}
		if !allowed || (enter != nil && !enter(angle, i)) {
			continue
		}
		seen[u.Domain] = true
		// Amphibious units ride with ground waves; they never theme one.
		weights[u.Domain] = t.ThemeWeights[u.Domain]
		if early && u.Domain == Air && weights[u.Domain] == 0 {
			weights[u.Domain] = 1
		}
		total += weights[u.Domain]
	}
	return weights, total
}

// fill adds the signature types round-robin while the next one fits in the
// budget. A group always gets at least its cheapest signature unit.
func fill(pool *Pool, sig []int, budget int64) []int {
	var picks []int
	left := budget
	for {
		added := false
		for _, i := range sig {
			if c := pool.Units[i].Cost; c <= left {
				picks = append(picks, i)
				left -= c
				added = true
			}
		}
		if !added {
			break
		}
	}
	if len(picks) == 0 && len(sig) > 0 {
		cheapest := sig[0]
		for _, i := range sig[1:] {
			if pool.Units[i].Cost < pool.Units[cheapest].Cost {
				cheapest = i
			}
		}
		picks = append(picks, cheapest)
	}
	return picks
}

func domainAllowed(d Domain, tick uint32, t Tuning, opts Options) bool {
	switch d {
	case Air:
		return !opts.NoAir && tick >= t.AirFrom
	case Naval:
		return !opts.NoNaval
	}
	return true
}
