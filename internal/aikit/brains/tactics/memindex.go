package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// memIndex builds the index if it is not this think's (buildPicture builds
// it every think; unit tests may ask without a picture).
func (a *Army) memIndex(b *core.Board) {
	if !a.indexed || a.indexTick != b.Tick {
		a.indexMemory(b)
	}
}

// indexMemory sorts this think's armed ground enemies for the per-zone
// questions (enemyAt for every candidate zone, corridor for every squad
// and zone): remembered static defenses and radar blips as lists (a
// defense's reach is its own range), remembered mobiles bucketed by the
// zone they stand in, so a question visits only the zones around its
// point or line instead of the whole memory. The answers are the same
// integer sums in another order.
func (a *Army) indexMemory(b *core.Board) {
	a.indexTick, a.indexed = b.Tick, true
	o := b.O
	n := len(a.zones)
	if len(a.mobHead) != n+1 {
		a.mobHead = make([]int32, n+1)
	}
	head := a.mobHead
	for i := range head {
		head[i] = 0
	}
	a.memStat = a.memStat[:0]
	nm := 0
	for i := range o.Memory {
		r := &o.Memory[i]
		if r.Info.DPS <= 0 || r.Info.Role.Has(aikit.RoleAir) {
			continue
		}
		if r.Building {
			a.memStat = append(a.memStat, int32(i))
			continue
		}
		head[a.zoneOf(r.X, r.Z)+1]++
		nm++
	}
	for zi := 0; zi < n; zi++ {
		head[zi+1] += head[zi]
	}
	if cap(a.mobIdx) < nm {
		a.mobIdx = make([]int32, nm, nm*2)
	}
	a.mobIdx = a.mobIdx[:nm]
	// Fill with a moving cursor per zone (head[zi] advances to head[zi+1],
	// then shifts back one place).
	for i := range o.Memory {
		r := &o.Memory[i]
		if r.Info.DPS <= 0 || r.Info.Role.Has(aikit.RoleAir) || r.Building {
			continue
		}
		zi := a.zoneOf(r.X, r.Z)
		a.mobIdx[head[zi]] = int32(i)
		head[zi]++
	}
	for zi := n; zi > 0; zi-- {
		head[zi] = head[zi-1]
	}
	head[0] = 0
	a.blips = a.blips[:0]
	for i := range o.Enemy {
		if o.Enemy[i].Info == nil {
			a.blips = append(a.blips, int32(i))
		}
	}
}
