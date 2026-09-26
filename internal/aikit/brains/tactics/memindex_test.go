package tactics

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The zone index answers exactly what a scan of the whole memory does:
// the same defenders around every point and the same mobiles along every
// corridor, including entries remembered off the map's edge.
func TestMemoryIndexMatchesScan(t *testing.T) {
	a, _ := testArmy(40, 30)
	seed := uint32(12345)
	rnd := func(n int32) int32 {
		seed = seed*1103515245 + 12345
		return int32(seed>>8) % n
	}
	infos := []*aikit.UnitInfo{
		{Role: aikit.RoleMobile | aikit.RoleCombat, DPS: 40, HP: 900, Range: 300, Value: 150},
		{Role: aikit.RoleMobile | aikit.RoleCombat | aikit.RoleCommander, DPS: 100, HP: 3000, Range: 300, Value: 3000},
		{Role: aikit.RoleDefense, DPS: 120, HP: 2000, Range: 700, Value: 400},
		{Role: aikit.RoleMobile | aikit.RoleCombat | aikit.RoleAir, DPS: 60, HP: 500, Range: 400, Value: 300},
		{Role: aikit.RoleExtractor, HP: 400, Value: 60},
	}
	o := &aikit.Obs{Tick: 3000}
	for i := 0; i < 600; i++ {
		info := infos[rnd(int32(len(infos)))]
		r := aikit.Remembered{H: pool.Handle(i + 1), Gen: 1, Info: info, X: rnd(5600) - 200, Z: rnd(4200) - 200,
			LastSeen: uint32(3000 - rnd(1800)), Building: !info.Role.Has(aikit.RoleMobile)}
		o.Memory = append(o.Memory, r)
	}
	for i := 0; i < 40; i++ {
		o.Enemy = append(o.Enemy, aikit.Contact{H: pool.Handle(1000 + i), X: rnd(5120), Z: rnd(3840)})
	}
	b := &core.Board{O: o, Tick: 3000}
	a.indexMemory(b)
	for q := 0; q < 200; q++ {
		x, z := rnd(5120), rnd(3840)
		var stat, mob, ws, wm force
		a.enemyAt(b, x, z, &stat, &mob)
		scanEnemyAt(b, x, z, &ws, &wm)
		if stat != ws || mob != wm {
			t.Fatalf("enemyAt(%d,%d) = %+v %+v, scan %+v %+v", x, z, stat, mob, ws, wm)
		}
		ax, az := rnd(5120), rnd(3840)
		var got, want force
		a.corridor(b, ax, az, x, z, &got)
		scanCorridor(b, ax, az, x, z, &want)
		if got != want {
			t.Fatalf("corridor (%d,%d)-(%d,%d) = %+v, scan %+v", ax, az, x, z, got, want)
		}
	}
}

// scanEnemyAt is enemyAt over the whole memory.
func scanEnemyAt(b *core.Board, x, z int32, stat, mob *force) {
	o := b.O
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		if info.DPS <= 0 || info.Role.Has(aikit.RoleAir) {
			continue
		}
		d2 := aikit.Dist2(r.X, r.Z, x, z)
		if r.Building {
			if reach := int64(info.Range + staticSlack); d2 <= reach*reach {
				stat.add(info, int64(info.HP), 1000)
			}
			continue
		}
		w := freshness(r, b.Tick)
		switch {
		case d2 <= nearMobile*nearMobile:
		case d2 <= reinforceDist*reinforceDist:
			w /= 2
		default:
			continue
		}
		mob.addEnemy(info, int64(info.HP), w)
	}
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if c.Info == nil && aikit.Dist2(c.X, c.Z, x, z) <= nearMobile*nearMobile {
			mob.addGeneric(1000)
		}
	}
}

// scanCorridor is corridor's mobile part over the whole memory.
func scanCorridor(b *core.Board, ax, az, bx, bz int32, en *force) {
	o := b.O
	dx, dz := int64(bx-ax), int64(bz-az)
	l2 := dx*dx + dz*dz
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		if r.Building || info.DPS <= 0 || info.Role.Has(aikit.RoleAir) {
			continue
		}
		if segDist2(ax, az, dx, dz, l2, r.X, r.Z) > corridorWidth*corridorWidth {
			continue
		}
		w := freshness(r, b.Tick)
		dt := aikit.Dist2(r.X, r.Z, bx, bz)
		switch {
		case dt <= nearMobile*nearMobile:
			continue
		case dt <= reinforceDist*reinforceDist:
			w /= 2
		}
		en.addEnemy(info, int64(info.HP), w)
	}
}

func benchPicture(n, cands int) (*Army, *core.Board, [][2]int32) {
	a, _ := testArmy(96, 96)
	seed := uint32(99)
	rnd := func(m int32) int32 { seed = seed*1103515245 + 12345; return int32(seed>>8) % m }
	tank := &aikit.UnitInfo{Role: aikit.RoleMobile | aikit.RoleCombat, DPS: 40, HP: 900, Range: 300, Value: 150}
	tower := &aikit.UnitInfo{Role: aikit.RoleDefense, DPS: 120, HP: 2000, Range: 700, Value: 400}
	mex := &aikit.UnitInfo{Role: aikit.RoleExtractor, HP: 400, Value: 60}
	o := &aikit.Obs{Tick: 3000}
	world := int32(96 * aikit.SectorWorld)
	for i := 0; i < n; i++ {
		info := tank
		switch i % 5 {
		case 0:
			info = tower
		case 1:
			info = mex
		}
		// clustered: half near the enemy base, the rest spread
		x, z := rnd(world), rnd(world)
		if i%2 == 0 {
			x, z = world*3/4+rnd(3000)-1500, world*3/4+rnd(3000)-1500
		}
		o.Memory = append(o.Memory, aikit.Remembered{H: pool.Handle(i + 1), Gen: 1, Info: info, X: x, Z: z, LastSeen: 3000 - uint32(rnd(900)), Building: info != tank})
	}
	b := &core.Board{O: o, Tick: 3000}
	var pts [][2]int32
	for i := 0; i < cands; i++ {
		pts = append(pts, [2]int32{rnd(world), rnd(world)})
	}
	return a, b, pts
}

// BenchmarkZoneQuestions times one think's zone questions (enemyAt per
// candidate, corridor per candidate for three squads) with the index and
// with full scans.
func BenchmarkZoneQuestions(bm *testing.B) {
	for _, n := range []int{100, 300, 600} {
		a, b, pts := benchPicture(n, n/3)
		bm.Run(fmt.Sprintf("index/%d", n), func(bm *testing.B) {
			for i := 0; i < bm.N; i++ {
				b.Tick++
				a.indexMemory(b)
				for _, p := range pts {
					var s, m, c force
					a.enemyAt(b, p[0], p[1], &s, &m)
					for q := 0; q < 3; q++ {
						a.corridor(b, 1000+int32(q)*500, 1000, p[0], p[1], &c)
					}
				}
			}
		})
		bm.Run(fmt.Sprintf("scan/%d", n), func(bm *testing.B) {
			for i := 0; i < bm.N; i++ {
				for _, p := range pts {
					var s, m, c force
					scanEnemyAt(b, p[0], p[1], &s, &m)
					for q := 0; q < 3; q++ {
						scanCorridor(b, 1000+int32(q)*500, 1000, p[0], p[1], &c)
					}
				}
			}
		})
	}
}
