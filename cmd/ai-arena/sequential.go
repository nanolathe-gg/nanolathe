package main

import (
	"fmt"
	"math"
	"slices"
)

// SequentialSpec plays a two-player tournament in blocks of seeds and stops a
// pairing at a planned look once its verdict is clear
// (docs/MODERN_AI_RESEARCH.md §5.3). A block is one seed on every map, in
// every slot order the spec plays, so every look sees every map with the
// slot order balanced.
type SequentialSpec struct {
	// Looks lists the number of seeds (a prefix of the spec's seeds, in
	// order) played before each look; increasing, the last all of them.
	Looks []int `json:"looks"`
	// Alpha is the two-sided error rate over all looks (default 0.10: the
	// protocol's 90% intervals).
	Alpha float64 `json:"alpha,omitempty"`
	// Futility stops a pairing for "no difference" at a look before the last
	// when the chance of a verdict at the last look, on the current trend, is
	// below it (default 0.20; negative never stops for futility).
	Futility float64 `json:"futility,omitempty"`
}

// Verdicts: the first contestant of the pair (A) against the second.
const (
	verdictBetter = "better"
	verdictWorse  = "worse"
	verdictNone   = "no difference"
)

// seqPlan is a validated sequential design with its boundary constant.
type seqPlan struct {
	looks    []int
	alpha    float64
	futility float64
	c        float64 // O'Brien–Fleming constant on the score (B-value) scale
}

func newSeqPlan(s *SequentialSpec, seeds int) (*seqPlan, error) {
	if len(s.Looks) == 0 {
		return nil, fmt.Errorf("sequential: no looks")
	}
	for i, n := range s.Looks {
		if n < 1 || (i > 0 && n <= s.Looks[i-1]) {
			return nil, fmt.Errorf("sequential: looks %v must increase from 1", s.Looks)
		}
	}
	if s.Looks[len(s.Looks)-1] != seeds {
		return nil, fmt.Errorf("sequential: the last look %d must be every seed (%d)", s.Looks[len(s.Looks)-1], seeds)
	}
	p := &seqPlan{looks: s.Looks, alpha: s.Alpha, futility: s.Futility}
	if p.alpha == 0 {
		p.alpha = 0.10
	}
	if p.alpha <= 0 || p.alpha >= 1 {
		return nil, fmt.Errorf("sequential: alpha %v out of (0, 1)", p.alpha)
	}
	if p.futility == 0 {
		p.futility = 0.20
	}
	p.c = obfConstant(p.fracs(), p.alpha)
	return p, nil
}

// fracs is each look's information fraction: seeds played over all seeds.
func (p *seqPlan) fracs() []float64 {
	out := make([]float64, len(p.looks))
	for i, n := range p.looks {
		out[i] = float64(n) / float64(p.looks[len(p.looks)-1])
	}
	return out
}

// lookOf is the index of the first look that includes seed index s.
func (p *seqPlan) lookOf(s int) int {
	for k, n := range p.looks {
		if s < n {
			return k
		}
	}
	return len(p.looks) - 1
}

// seqGame is one finished game as a look reads it: its map, seed index and
// A's points (1 win, 0.5 draw, 0 loss).
type seqGame struct {
	mapName string
	seed    int
	points  float64
}

// seqLook is one look's statistic and decision.
type seqLook struct {
	Seeds int     `json:"seeds"`
	Games int     `json:"games"`
	Maps  int     `json:"maps"`
	Share float64 `json:"share"` // A's points share: the mean of the map means
	SE    float64 `json:"se"`    // standard error of the share across maps
	T     float64 `json:"t"`     // (share - 0.5) / SE
	DF    int     `json:"df"`
	Bound float64 `json:"bound"` // |T| at or above it decides
	// Power is the chance of a verdict at the last look on the current trend
	// (looks before the last).
	Power    float64 `json:"power,omitempty"`
	Decision string  `json:"decision,omitempty"`
}

// look evaluates look k over the games of the first p.looks[k] seeds. The
// statistic is A's mean points on each map, blocks (a map and seed, every
// slot order) weighted alike; the share is the mean over maps and its error
// comes from the spread of the map means, with maps - 1 degrees of freedom,
// because games on one map are not independent evidence. The boundary is
// O'Brien–Fleming's nominal level for the look, read on that t
// distribution.
func (p *seqPlan) look(k int, games []seqGame) seqLook {
	n := p.looks[k]
	type block struct {
		sum float64
		n   int
	}
	blocks := map[string]map[int]*block{}
	var maps []string
	l := seqLook{Seeds: n}
	for _, g := range games {
		if g.seed >= n {
			continue
		}
		bm := blocks[g.mapName]
		if bm == nil {
			bm = map[int]*block{}
			blocks[g.mapName] = bm
			maps = append(maps, g.mapName)
		}
		b := bm[g.seed]
		if b == nil {
			b = &block{}
			bm[g.seed] = b
		}
		b.sum += g.points
		b.n++
		l.Games++
	}
	slices.Sort(maps)
	means := make([]float64, 0, len(maps))
	for _, m := range maps {
		seeds := make([]int, 0, len(blocks[m]))
		for s := range blocks[m] {
			seeds = append(seeds, s)
		}
		slices.Sort(seeds)
		var sum float64
		for _, s := range seeds {
			b := blocks[m][s]
			sum += b.sum / float64(b.n)
		}
		means = append(means, sum/float64(len(seeds)))
	}
	l.Maps = len(means)
	if l.Maps < 2 {
		l.Share = 0.5
		if l.Maps == 1 {
			l.Share = means[0]
		}
		return l
	}
	mean := 0.0
	for _, v := range means {
		mean += v
	}
	mean /= float64(len(means))
	ss := 0.0
	for _, v := range means {
		ss += (v - mean) * (v - mean)
	}
	l.Share = mean
	l.DF = len(means) - 1
	l.SE = math.Sqrt(ss/float64(l.DF)) / math.Sqrt(float64(len(means)))
	d := mean - 0.5
	switch {
	case l.SE > 0:
		l.T = d / l.SE
	case d != 0:
		// Every map agrees exactly: an unbounded statistic, kept finite so
		// the record stays valid JSON.
		l.T = math.Copysign(unboundedT, d)
	}
	t := p.fracs()[k]
	zb := p.c / math.Sqrt(t)
	l.Bound = tQuantile(normCDF(zb), l.DF)
	last := k == len(p.looks)-1
	switch {
	case l.T >= l.Bound:
		l.Decision = verdictBetter
	case l.T <= -l.Bound:
		l.Decision = verdictWorse
	case last:
		l.Decision = verdictNone
	default:
		// Conditional power on the current trend: the look's statistic,
		// moved to the normal scale through its t distribution, is the
		// score path's value at t; the drift it implies carries the path to
		// the end, where the last look's boundary is p.c.
		z := normQuantile(tCDF(l.T, l.DF))
		drift := z / math.Sqrt(t)
		sd := math.Sqrt(1 - t)
		l.Power = 1 - normCDF((p.c-drift)/sd) + normCDF((-p.c-drift)/sd)
		if p.futility > 0 && l.Power < p.futility {
			l.Decision = verdictNone
		}
	}
	return l
}

// unboundedT stands for the t statistic of maps that agree exactly.
const unboundedT = 1e12

// obfConstant is the O'Brien–Fleming constant c for looks at the given
// information fractions: a two-sided test that stops at look k when
// |Z_k| >= c/sqrt(t_k) rejects a true null with probability alpha. On the
// score scale (B(t) = Z sqrt(t), a Brownian motion under the null) the
// boundary is |B| >= c at every look, so the non-crossing probability is
// integrated look by look on a grid over (-c, c) and c found by bisection.
func obfConstant(fracs []float64, alpha float64) float64 {
	lo, hi := 0.5, 6.0
	for range 40 {
		c := (lo + hi) / 2
		if obfCrossing(fracs, c) > alpha {
			lo = c
		} else {
			hi = c
		}
	}
	return (lo + hi) / 2
}

// obfCrossing is the chance a standard Brownian motion observed at fracs
// reaches |B| >= c at one of them.
func obfCrossing(fracs []float64, c float64) float64 {
	const n = 801
	h := 2 * c / (n - 1)
	x := make([]float64, n)
	for i := range x {
		x[i] = -c + float64(i)*h
	}
	// Trapezoid weights over the continuation region.
	w := func(i int) float64 {
		if i == 0 || i == n-1 {
			return h / 2
		}
		return h
	}
	f := make([]float64, n)
	g := make([]float64, n)
	kernel := make([]float64, 2*n-1) // the increment's density at (j-i)h
	prev := 0.0
	for k, t := range fracs {
		sd := math.Sqrt(t - prev)
		if k == 0 {
			for j := range g {
				g[j] = normPDF(x[j]/sd) / sd
			}
		} else {
			for d := range kernel {
				kernel[d] = normPDF(float64(d-(n-1))*h/sd) / sd
			}
			for j := range g {
				s := 0.0
				for i := range f {
					s += f[i] * w(i) * kernel[j-i+n-1]
				}
				g[j] = s
			}
		}
		f, g = g, f
		prev = t
	}
	stay := 0.0
	for i := range f {
		stay += f[i] * w(i)
	}
	return 1 - stay
}

func normPDF(x float64) float64 { return math.Exp(-x*x/2) / math.Sqrt(2*math.Pi) }

func normCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

// normQuantile inverts normCDF; the tails are clamped so an infinite t
// statistic maps to a finite, decisive value.
func normQuantile(p float64) float64 {
	p = min(max(p, 1e-15), 1-1e-15)
	return -math.Sqrt2 * math.Erfcinv(2*p)
}

// tCDF is Student's t distribution function with df degrees of freedom.
func tCDF(t float64, df int) float64 {
	if t >= unboundedT {
		return 1
	}
	if t <= -unboundedT {
		return 0
	}
	v := float64(df)
	p := 0.5 * regIncBeta(v/2, 0.5, v/(v+t*t))
	if t > 0 {
		return 1 - p
	}
	return p
}

// tQuantile inverts tCDF by bisection.
func tQuantile(p float64, df int) float64 {
	lo, hi := -1e4, 1e4
	for range 200 {
		mid := (lo + hi) / 2
		if tCDF(mid, df) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// regIncBeta is the regularized incomplete beta function I_x(a, b), by its
// continued fraction.
func regIncBeta(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	front := math.Exp(lab - la - lb + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return front * betaCF(a, b, x) / a
	}
	return 1 - front*betaCF(b, a, 1-x)/b
}

// betaCF evaluates the incomplete beta continued fraction (modified Lentz).
func betaCF(a, b, x float64) float64 {
	const tiny = 1e-300
	c, d := 1.0, 1-(a+b)*x/(a+1)
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= 300; m++ {
		fm := float64(m)
		for _, num := range [2]float64{
			fm * (b - fm) * x / ((a + 2*fm - 1) * (a + 2*fm)),
			-(a + fm) * (a + b + fm) * x / ((a + 2*fm) * (a + 2*fm + 1)),
		} {
			d = 1 + num*d
			if math.Abs(d) < tiny {
				d = tiny
			}
			c = 1 + num/c
			if math.Abs(c) < tiny {
				c = tiny
			}
			d = 1 / d
			h *= d * c
		}
		if math.Abs(d*c-1) < 1e-15 {
			break
		}
	}
	return h
}
