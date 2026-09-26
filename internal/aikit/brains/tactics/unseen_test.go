package tactics

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The remembered enemy land army keeps each part at its largest sighting
// (anti-air seen one minute, tanks the next) and fades slowly; only the
// part out of sight is added, and only near where the army was seen or
// near the enemy base. unseen=0 adds nothing.
func TestLandUnseen(t *testing.T) {
	a, _ := testArmy(40, 40)
	tank := &aikit.UnitInfo{Index: 0, Role: aikit.RoleMobile | aikit.RoleCombat, DPS: 100, HP: 1000, Range: 200, Value: 150}
	truck := &aikit.UnitInfo{Index: 1, Role: aikit.RoleMobile | aikit.RoleCombat, DPS: 16, AirDPS: 36, HP: 650, Range: 600, Value: 136}
	a.classes = []uclass{{kind: ukGround}, {kind: ukGround}}
	a.dt = 15
	see := func(tick uint32, units ...*aikit.UnitInfo) {
		o := &aikit.Obs{Tick: tick}
		for i, u := range units {
			o.Memory = append(o.Memory, aikit.Remembered{H: pool.Handle(1 + i), Info: u, X: 1000, Z: 1000, LastSeen: tick})
		}
		a.landPicture(&core.Board{O: o, Tick: tick})
	}
	see(100, truck)
	see(115, tank, tank)
	if a.landMem.aa < 35 || a.landMem.dps < 199 {
		t.Fatalf("memory aa %d dps %d, want the trucks' anti-air and the tanks' fire kept", a.landMem.aa, a.landMem.dps)
	}
	// Out of sight: fading with a five-minute time constant, not truncated.
	for tick := uint32(130); tick < 130+900; tick += 15 {
		see(tick)
	}
	if a.landMem.aa < 30 || a.landMem.aa > 35 {
		t.Errorf("anti-air after 30 s out of sight %d, want about 36·e^(-1/10)", a.landMem.aa)
	}
	b := &core.Board{O: &aikit.Obs{}, EnemyKnown: true, EnemyX: 9000, EnemyZ: 1000}
	if f, ok := a.landUnseen(b, 1500, 1000); !ok || f.dps < 150 || f.aa < 30 {
		t.Errorf("near the army: unseen %+v %v", f, ok)
	}
	if _, ok := a.landUnseen(b, 9000, 3000); !ok {
		t.Error("near the enemy base: the army defends it")
	}
	if _, ok := a.landUnseen(b, 5000, 4000); ok {
		t.Error("far from both: nothing")
	}
	a.P.Unseen = false
	if _, ok := a.landUnseen(b, 1500, 1000); ok || a.aaReserve(b, 1500, 1000) != 0 {
		t.Error("unseen=0 adds nothing")
	}
}

// The strike calibration: sorties that lost more than the model expected
// and destroyed less raise the loss and lower the gain of the next
// judgment, within bounds; a prior keeps one sortie from swinging it fully.
func TestStrikeCal(t *testing.T) {
	a, _ := testArmy(10, 10)
	if l, g := a.strikeCal(); l != 1000 || g != 1000 {
		t.Fatalf("no sortie yet: %d %d", l, g)
	}
	a.calExpLoss, a.calLoss, a.calExpGain, a.calGain = 308, 900, 757, 0
	l, g := a.strikeCal()
	if l != (900+calPrior)*1000/(308+calPrior) || g != calPrior*1000/(757+calPrior) {
		t.Errorf("after one bad sortie: loss ×%d‰ gain ×%d‰", l, g)
	}
	a.calExpLoss, a.calLoss, a.calExpGain, a.calGain = 1000, 100, 1000, 3000
	if l, g := a.strikeCal(); l != 1000 || g != 1000 {
		t.Errorf("better than expected is not rewarded: %d %d", l, g)
	}
	a.calExpLoss, a.calLoss, a.calExpGain, a.calGain = 0, 50000, 50000, 0
	if l, g := a.strikeCal(); l != 5000 || g != 200 {
		t.Errorf("bounds: %d %d", l, g)
	}
}

// Every fading memory reaches zero on its time constant: about e⁻¹ of the
// value is left after one time constant, something is left while the
// value is still above one unit, nothing once it is below. In whole units
// the per-step decrement truncated to zero and each memory stopped at a
// floor (anti-air and sea hits at 35, the threat at 119) or faded far too
// slowly (the fleet, a 15000-tick time constant instead of 9000).
func TestMemoriesFadeToZero(t *testing.T) {
	const v0 = 400 // ln(400) ≈ 6: gone after six time constants, not five
	type fader struct {
		name string
		tau  int64
		dt   int64
		init func(a *Army)
		step func(a *Army, tick uint32)
		get  func(a *Army) int64
	}
	cell := int32(3*40 + 3)
	for _, f := range []fader{
		{"anti-air memory", aaMemTicks, memFadeTicks,
			func(a *Army) { a.aaMem.V[cell] = v0 },
			func(a *Army, _ uint32) { fadeGrid(a.aaMem, a.aaMemK, memFadeTicks, aaMemTicks, a.aa) },
			func(a *Army) int64 { return int64(a.aaMem.V[cell]) }},
		{"sea hits", seaHurtTicks, memFadeTicks,
			func(a *Army) { a.seaHurt.V[cell] = v0 },
			func(a *Army, _ uint32) { fadeGrid(a.seaHurt, a.seaHurtK, memFadeTicks, seaHurtTicks, nil) },
			func(a *Army) int64 { return int64(a.seaHurt.V[cell]) }},
		{"threat", threatTicks, 15,
			func(a *Army) { a.threatK, a.threatTick = v0*1000, 1 },
			func(a *Army, tick uint32) { a.fadeThreat(tick) },
			func(a *Army) int64 { return a.threatMemory }},
		{"fleet", fleetMemTicks, 15,
			func(a *Army) {
				a.fleetMemK = force{dps: v0 * 1000, hp: v0 * 1000}
				a.fleetMem = force{dps: v0, hp: v0}
			},
			func(a *Army, _ uint32) { a.fadeFleet(15) },
			func(a *Army) int64 { return a.fleetMem.dps }},
	} {
		a, _ := testArmy(40, 40)
		f.init(a)
		tick := uint32(1)
		// run steps the fade dt ticks at a time until the tick reaches until
		// and returns the value then.
		run := func(until int64) int64 {
			for int64(tick) < until {
				tick += uint32(f.dt)
				f.step(a, tick)
			}
			return f.get(a)
		}
		if v := run(f.tau); v < v0*34/100 || v > v0*38/100 {
			t.Errorf("%s after one time constant: %d, want about %d", f.name, v, v0*37/100)
		}
		if v := run(5 * f.tau); v <= 0 {
			t.Errorf("%s gone after five time constants (%d)", f.name, v)
		}
		if v := run(7 * f.tau); v != 0 {
			t.Errorf("%s after seven time constants: %d, want 0", f.name, v)
		}
	}
}
