package utility

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

var defClassNames = [dcCount]string{"none", "ground", "anti-air", "water"}

// explainDefense describes the defense plan for the replay viewer. It runs
// between thinks on the simulation thread and may allocate. Economy.Explain
// calls it when the plan is on.
func (e *Economy) explainDefense(b *core.Board, x *aikit.Explain) {
	if e.s.p.DefPlan == 0 {
		return
	}
	pl := e.plan()
	if !pl.fresh {
		return
	}
	x.Notes = append(x.Notes, fmt.Sprintf("defense plan: budget %d (share %d‰ of income, floor %d), recent losses %d of %d protected",
		pl.budG, pl.share, pl.floorG(e.s), pl.lossRecent, pl.protect))
	if pl.front {
		x.Notes = append(x.Notes, fmt.Sprintf("defense front rules: mix %d missile of %d ground-plan towers, missile tower builder %v, %d choke posts", pl.mixD, pl.mixG, pl.dualCons, len(pl.chokes)))
		for i := range pl.chokes {
			c := &pl.chokes[i]
			z := pl.nGrid + int32(i)
			act := pl.zGen[z] == pl.gen
			x.Notes = append(x.Notes, fmt.Sprintf("defense choke %d at (%d,%d) width %d along %d‰ guards start %v, %d spots; sites %v; active %v assets %d towers %d",
				i, c.X, c.Z, c.Width, c.Along, c.Home, c.Spots, c.Sites[:c.NSites], act, pl.zAsset[z], pl.chokeTowers(z)))
		}
	}
	for c := dcGround; c < dcCount; c++ {
		x.Notes = append(x.Notes, fmt.Sprintf("defense %s: have %d (built %d, frames %d, orders %d; %d towers), deficit %d, next zone %d at (%d,%d) urgency %d",
			defClassNames[c], pl.have[c], pl.built[c], pl.frame[c], pl.commit[c], pl.cnt[c], pl.deficit[c], pl.zone[c], pl.x[c], pl.z[c], pl.urg[c]))
	}
	m := b.K.Map
	flow := make([]int32, len(pl.flow))
	for i := range pl.flow {
		flow[i] = int32(clamp(pl.flowAt(int32(i))/defCross, 0, 1<<30))
	}
	x.Grids = append(x.Grids, aikit.NamedGrid{Name: "def_flow", W: m.SectorW, H: m.SectorH, Values: flow})
	for _, z := range pl.active {
		pg, _ := pl.priority(e.s, b, z, dcGround)
		pa, _ := pl.priority(e.s, b, z, dcAir)
		x.Notes = append(x.Notes, fmt.Sprintf("defense zone %d anchor (%d,%d): assets %d/%d, front %d, ground %d (%d near) prio %d, air %d prio %d, raid heat %d, threat heat %d, attacks %d",
			z, pl.zAncX[z], pl.zAncZ[z], pl.zAsset[z], pl.zAssetA[z], front(e.s, b, pl.zAncX[z], pl.zAncZ[z]),
			pl.zBuilt[dcGround][z]+max64(pl.zFrame[dcGround][z], pl.zCommit[dcGround][z]), pl.towersAt(z, dcGround), pg,
			pl.zBuilt[dcAir][z]+max64(pl.zFrame[dcAir][z], pl.zCommit[dcAir][z]), pa, pl.heatLoss[z], pl.heatThr[z]/30, pl.atkW[z]/30))
	}
}
