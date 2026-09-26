package survival

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// explain describes the survival layer for the visualizer: the sectors it
// owns with their weight, towers and share, the jobs in hand and the
// warnings in force. It runs between thinks and draws nothing.
func (st *state) explain(x *aikit.Explain) {
	if !st.ready {
		return
	}
	var owned, towers int32
	for s := range st.mine {
		if st.mine[s] {
			owned++
			towers += st.towers[s]
		}
	}
	c := &st.stat
	var allied int32
	for s := range st.mine {
		if st.mine[s] {
			allied += st.ally.towers[s]
		}
	}
	x.Notes = append(x.Notes, fmt.Sprintf("survival: %d sectors, %d towers (%d allied), %d jobs, %d warned directions, air %v; ordered %d towers (%d failed), %d walls, %d repairs (%d allied), %d refuges (%d from an allied commander)",
		owned, towers, allied, len(st.jobs.list), len(st.live), st.air, c.towers, c.towerFails, c.walls, c.repairs, c.allyRepairs, c.refuges, c.blastMoves))
	if c.comWhy != "" {
		x.Notes = append(x.Notes, "survival: commander towers: "+c.comWhy)
	}
	for i := 0; i < min(st.stat.nFails, len(st.stat.lastFails)); i++ {
		j := &st.stat.lastFails[i]
		key := ""
		if j.prod != nil {
			key = j.prod.Key
		}
		x.Notes = append(x.Notes, fmt.Sprintf("survival: failed %s at (%d,%d) sector %d, ordered t%d, builder %d", key, j.x, j.z, j.sector, j.since, j.who.h))
	}
	for s := range st.mine {
		if !st.mine[s] {
			continue
		}
		r := int64(st.radius[s])
		gx := st.cx + int32(sectorDir[s][0]*r/1000)
		gz := st.cz + int32(sectorDir[s][1]*r/1000)
		x.Goals = append(x.Goals, aikit.Goal{
			Label: fmt.Sprintf("sector %d: weight %d, towers %d (value %d of %d, allied %d), walls %d", s, st.weight[s], st.towers[s], st.have[s], st.want[s], st.ally.have[s], st.walls[s]),
			Score: int32(min(st.weight[s], 1<<30)),
			X:     gx, Z: gz,
		})
	}
	for i := range st.jobs.list {
		j := &st.jobs.list[i]
		label := [...]string{"none", "tower", "wall", "repair", "refuge"}[j.kind]
		if j.prod != nil {
			label += " " + j.prod.Key
		}
		x.Goals = append(x.Goals, aikit.Goal{Label: label, Chosen: true, X: j.x, Z: j.z})
	}
}
