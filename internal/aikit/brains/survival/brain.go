// Package survival is the Modern AI's computer survivor for Survival battles
// (docs/DESIGN_SURVIVAL.md "Computer survivors under the Modern AI"). A
// Survival battle has no enemy base to find and nothing to attack: waves walk
// in from the map edges toward the team's buildings, grow every few minutes
// and climb the build tree. The survivor therefore plays one side of a team
// that holds ground:
//
//   - a compact base on its own side of the start site, facing outward, that
//     leaves the human's commander the ground around the site;
//   - rings of towers around the team — its own buildings and the human's
//     start site — weighted toward the directions the warnings announce and
//     the attackers have come from, split with the other buddy by bearing so
//     the two do not tower the same lane; short wall segments in front of
//     them, with corridors between;
//   - an army that never attacks out: it gathers between the team's core and
//     the threatened side and fights what comes;
//   - its commander kept behind the towers, damaged buildings repaired.
//
// It is not a new brain from scratch. The util+tac layers (utility strategy,
// economy and production; tactics army) do the economy, production and the
// fighting, reconfigured through their parameters; this package wraps each
// layer, shows it the Survival geometry through the board, and adds the
// survival-only planning — the tower ring, walls, repairs, the commander's
// refuge — on builders it takes from the economy.
//
// Everything is integer arithmetic over the observation, the scenario the
// session published at battle entry and the brain's own state. Wave warnings
// are read through Feed for the observation's tick only, so a think gives
// the same answer on either thread.
package survival

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Scenario is what the battle told this survivor at entry: the start site
// (the human's commander placement), the team and where each member began.
type Scenario struct {
	CentreX, CentreZ int32
	Me               uint8
	// Team lists the survivors; Computer marks the computer buddies and
	// Starts holds each one's commander placement, all in Team order.
	Team     []uint8
	Computer []bool
	Starts   [][2]int32
	// Feed gives the wave warnings announced so far; nil when none are
	// published (a fixture).
	Feed Feed
}

// Warning is one announced wave (ai.SurvivalWarning).
type Warning struct {
	Wave   int32
	Tick   uint32 // the warning began
	Arrive uint32 // its units begin to arrive
	Groups []Approach
}

// Approach is one announced direction: the bearing from the start site
// (65536 per circle, x by cosine and z by sine), the entry point in world
// units and the announced theme.
type Approach struct {
	Angle        uint16
	X, Z         int32
	Air, Naval   bool
	Hover, Other bool // Other: ground or amphibious, announced as a plain direction
}

// Feed yields the warnings announced at or before tick, oldest first. It
// must answer from data published before the observation was taken, so a
// think on either thread sees the same list.
type Feed interface {
	Warnings(tick uint32, dst []Warning) []Warning
}

// Params are the survival layer's own settings (the util+tac layers keep
// theirs). The defaults are the tuned values (docs/DESIGN_SURVIVAL.md).
type Params struct {
	// TowerShare is the percent of the income received so far the survivor
	// keeps standing as towers of its own planning.
	TowerShare int32
	// Walls turns the wall segments in front of towers on (1) or off (0).
	Walls int32
	// ClaimShare is the percent of constructors the survival layer may take
	// from the economy for its towers, walls and repairs; doubled while a
	// warned wave is inbound.
	ClaimShare int32
}

// DefaultParams are the tuned survival settings (Specs' defaults).
func DefaultParams() Params {
	var p Params
	for i := range Specs {
		switch sp := &Specs[i]; sp.Name {
		case "sv_tower":
			p.TowerShare = sp.Default
		case "sv_walls":
			p.Walls = sp.Default
		case "sv_claim":
			p.ClaimShare = sp.Default
		}
	}
	return p
}

// Layers are the util+tac layers the survivor wraps, in core order.
type Layers struct {
	Strategy, Economy, Army, Production core.Policy
}

// New composes the survivor's brain around the util+tac layers.
func New(sc Scenario, p Params, in Layers) *core.Brain {
	st := &state{sc: sc, p: p}
	return core.New("survival",
		&Strategy{st: st, inner: in.Strategy},
		&Economy{st: st, inner: in.Economy},
		&Army{st: st, inner: in.Army},
		in.Production)
}

// state is what the four wrappers share.
type state struct {
	sc Scenario
	p  Params

	ready bool
	me    int // index of this survivor in the scenario's team, -1 if absent
	// Geometry, world units: the start site (the human's commander), this
	// survivor's home (its start, moved out to blastClear from the site),
	// the point its economy faces (outward from the site through its home)
	// and the defence centre its army holds.
	cx, cz     int32
	hx, hz     int32
	ex, ez     int32
	dx, dz     int32
	outX, outZ int64 // unit vector ×1000 from the site toward this start
	// mine marks the sectors this survivor towers: those whose bearing from
	// the site is nearer its own start's bearing than any other buddy's.
	mine [numSectors]bool
	// siteable marks the sectors with ground the survivor's builders reach
	// somewhere between towerRoom and the farthest perimeter; the others
	// get no share of the towers.
	siteable [numSectors]bool

	tab table
	// reach labels the dry land this survivor's commander class stands on
	// and homeRegion is its start's; its home, towers and walls must be
	// there.
	reach      *aikit.Reach
	homeRegion int32

	// Per think.
	tick     uint32
	warn     []Warning
	live     []Approach // approaches of the warnings in force now
	liveArr  uint32     // earliest arrival among them
	air      bool       // an air wave is warned or aircraft were seen
	weight   [numSectors]int64
	radius   [numSectors]int32
	have     [numSectors]int64 // tower value standing or framed
	towers   [numSectors]int32
	aaTowers [numSectors]int32
	walls    [numSectors]int32
	want     [numSectors]int64
	hist     [numSectors]int64 // attacker value seen by sector, decayed (×1000)
	histAt   uint32
	income   int64 // metal-equivalent received so far
	lastInc  uint32
	lastM    int32 // stocks at the last think
	lastE    int32
	// stock is this think's resource state; bankedSince is the tick from
	// which both stores have stood four-fifths full (0: they do not).
	stock       struct{ metal, energy aikit.Res }
	bankedSince uint32

	defense defense
	jobs    jobs
	// ally is this think's picture of the team's other members (allies.go).
	ally allyView
	// takeBusy lets free take a constructor at work (plan's last resort).
	takeBusy bool

	// Instrumentation (Report, Explain); never read by a decision.
	stat stats
}

// stats count what the survival layer did.
type stats struct {
	towers, walls, repairs, refuges, aa int32
	// allyRepairs counts repairs of allied buildings, blastMoves the
	// commander's moves away from an allied commander.
	allyRepairs, blastMoves int32
	towerFails, wallFails   int32
	// comWhy is the commander tower decision's last outcome.
	comWhy string
	// lastFails are the latest failed jobs, for Explain.
	lastFails [4]job
	nFails    int
}

// numSectors is the number of bearings around the start site the tower
// plan is kept in (22.5° each).
const numSectors = 16

// sectorAngle is a sector's width in angle units.
const sectorAngle = 65536 / numSectors

// The sector directions, unit vectors ×1000 from the simulation's integer
// sine table (x by cosine, z by sine, as the director measures bearings).
var sectorDir = func() (d [numSectors][2]int64) {
	for s := range d {
		a := uint32(s) * sectorAngle
		d[s] = [2]int64{cos1000(a), sin1000(a)}
	}
	return d
}()

// sectorBin is sectorDir at the sine table's own precision (×8192), for
// binning directions close to a sector's edge.
var sectorBin = func() (d [numSectors][2]int64) {
	for s := range d {
		a := numeric.Angle(uint16(uint32(s) * sectorAngle))
		d[s] = [2]int64{int64(numeric.Cos(a)), int64(numeric.Sin(a))}
	}
	return d
}()

// sectorOf bins a direction (not the zero vector) into the sector whose
// centre bearing it is nearest: the largest dot product.
func sectorOf(dx, dz int64) int {
	best, bestDot := 0, int64(-1<<62)
	for s := range sectorBin {
		if d := dx*sectorBin[s][0] + dz*sectorBin[s][1]; d > bestDot {
			best, bestDot = s, d
		}
	}
	return best
}

// sectorOfAngle is the sector of a bearing.
func sectorOfAngle(a uint16) int {
	return int((uint32(a)+sectorAngle/2)/sectorAngle) % numSectors
}

// ringDist is the distance between two sectors around the ring.
func ringDist(a, b int) int {
	d := a - b
	if d < 0 {
		d = -d
	}
	if d > numSectors/2 {
		d = numSectors - d
	}
	return d
}

func clampI(v, lo, hi int64) int64 {
	return min(max(v, lo), hi)
}

func clampWorld(v int64, hi int32) int32 {
	return int32(clampI(v, 64, int64(hi)-64))
}

// handleGen names one own unit instance.
type handleGen struct {
	h   pool.Handle
	gen uint32
}

// setup runs on the first think (the kit's map is ready by then).
func (st *state) setup(b *core.Board) {
	st.ready = true
	sc := &st.sc
	st.cx, st.cz = sc.CentreX, sc.CentreZ
	st.me = -1
	for i, p := range sc.Team {
		if p == sc.Me {
			st.me = i
		}
	}
	st.hx, st.hz = b.HomeX, b.HomeZ
	if st.me >= 0 && st.me < len(sc.Starts) {
		st.hx, st.hz = sc.Starts[st.me][0], sc.Starts[st.me][1]
	}
	// Outward: from the site through this start. A survivor on the site
	// itself (no human, or placed there) faces the map's farther half.
	vx, vz := int64(st.hx-st.cx), int64(st.hz-st.cz)
	if vx*vx+vz*vz < 64*64 {
		m := b.K.Map
		vx, vz = int64(m.WorldW/2-st.cx), int64(m.WorldH/2-st.cz)
		if vx*vx+vz*vz < 64*64 {
			vx, vz = 1000, 0
		}
		vx, vz = -vx, -vz
	}
	d := aikit.ISqrt64(vx*vx + vz*vz)
	st.outX, st.outZ = vx*1000/d, vz*1000/d
	m := b.K.Map
	// The economy's home stands blastClear from the site, on this start's
	// bearing (the session places buddies closer than that): a commander's
	// death explosion reaches the other commanders around it, and a
	// buddy's commander that died on its start took the human's with it.
	// Pulled back toward the start until it is on the start's ground.
	if shift := blastClear - d; shift > 0 {
		for ; shift > 0; shift -= 32 {
			x := clampWorld(int64(st.hx)+st.outX*shift/1000, m.WorldW)
			z := clampWorld(int64(st.hz)+st.outZ*shift/1000, m.WorldH)
			if st.reachable(x, z) {
				st.hx, st.hz = x, z
				break
			}
		}
	}
	st.ex = clampWorld(int64(st.hx)+st.outX*econFacing/1000, m.WorldW)
	st.ez = clampWorld(int64(st.hz)+st.outZ*econFacing/1000, m.WorldH)
	// The army holds between the site and this survivor's home.
	st.dx = st.cx + (st.hx-st.cx)*defendShare/100
	st.dz = st.cz + (st.hz-st.cz)*defendShare/100
	// Sector ownership: each sector goes to the computer buddy whose start
	// bearing is nearest; a survivor that is the only buddy owns them all.
	for s := range st.mine {
		best, bestDot := -1, int64(-1<<62)
		for i, p := range sc.Team {
			if i >= len(sc.Computer) || !sc.Computer[i] || i >= len(sc.Starts) {
				continue
			}
			bx, bz := int64(sc.Starts[i][0]-st.cx), int64(sc.Starts[i][1]-st.cz)
			if bx*bx+bz*bz < 64*64 {
				bx, bz = st.outX, st.outZ
				if p != sc.Me {
					bx, bz = -bx, -bz
				}
			}
			bl := aikit.ISqrt64(bx*bx + bz*bz)
			dot := (bx*sectorDir[s][0] + bz*sectorDir[s][1]) * 1000 / max(bl, 1)
			if dot > bestDot {
				best, bestDot = i, dot
			}
		}
		st.mine[s] = best < 0 || best == st.me
	}
	st.tab.build(b.K)
	for s := range st.siteable {
		for r := int64(maxPerimeter); r >= towerRoom; r -= 64 {
			if _, _, ok := st.siteOnBearing(b, s, r, 0); ok {
				st.siteable[s] = true
				break
			}
		}
	}
}

// Geometry constants, world units.
const (
	// econFacing is how far from its start the point the economy faces
	// stands: the utility zones cap each class's distance at a share of it
	// (factories 35–40 %, energy 45–50 %), so it also keeps the base
	// compact — about a human's economy radius at ten minutes.
	econFacing = 1500
	// defendShare is the percent of the way from the site to this
	// survivor's home where the army's defence centre stands: none, the
	// human's start itself. The team's weakest point is the human's
	// commander — the battle ends with it — and a wave that walks past the
	// buddy's side reaches it first.
	defendShare = 0
	// blastClear is how far from the start site the survivor's home — where
	// its commander works and shelters — stands at least: beyond a
	// commander's death explosion (stock: 950 wu across, 9,999 damage),
	// with a margin.
	blastClear = 560
	// humanRoom is the radius around the site kept for the human's own
	// buildings: none of the survivor's economy, towers only beyond it.
	humanRoom = 420
)
