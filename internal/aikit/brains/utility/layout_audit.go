package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// layoutAudit is the zones' instrumentation for the arena's Report, never
// read by a decision: per zone class, how many request points were asked
// for, how many a cap (the share of the way to the enemy, or the absolute
// limit) cut short and how many the home-ground rule pulled back; and the
// enemy-base estimate the zones used at minutes 10 and 20 (distance from
// home, and whether an enemy building had been seen).
type layoutAudit struct {
	req, capped, pulled [zMaker + 1]int32
	enemyD, baseD       [2]int64
	known               [2]bool
	sampled             [2]bool
}

// sample records the board's enemy estimate and enemyBase's the first
// think at or after minutes 10 and 20.
func (la *layoutAudit) sample(s *shared, b *core.Board) {
	for i, m := range [2]uint32{10, 20} {
		if !la.sampled[i] && s.tick >= m*1800 {
			la.sampled[i] = true
			la.enemyD[i] = int64(aikit.Dist(b.EnemyX, b.EnemyZ, b.HomeX, b.HomeZ))
			la.baseD[i] = int64(aikit.Dist(s.zones.f2.baseX, s.zones.f2.baseZ, b.HomeX, b.HomeZ))
			la.known[i] = b.EnemyKnown
		}
	}
}

// report publishes the counters.
func (la *layoutAudit) report(add func(name string, value int64)) {
	names := [zMaker + 1]string{"", "factory", "energy", "maker"}
	for zc := zFactory; zc <= zMaker; zc++ {
		add("layout_req_"+names[zc], int64(la.req[zc]))
		add("layout_capped_"+names[zc], int64(la.capped[zc]))
		add("layout_pulled_"+names[zc], int64(la.pulled[zc]))
	}
	for i, m := range [2]string{"10", "20"} {
		if !la.sampled[i] {
			continue
		}
		add("layout_enemy_d_"+m, la.enemyD[i])
		add("layout_base_d_"+m, la.baseD[i])
		k := int64(0)
		if la.known[i] {
			k = 1
		}
		add("layout_enemy_known_"+m, k)
	}
}
