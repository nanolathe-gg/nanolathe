package airdiag

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// WU-19-226 play-test repro: a gunship (`hoverattack`) and two fighters
// ordered to attack a ground unit. The maintainer reported that the Brawler
// "won't attack properly" — retail keeps it hovering near the target, facing
// it, firing constantly — and that the Hawk and the Freedom Fighter "won't
// attack the ground" at all, where retail flies strafing runs
// [04 R-AIR-01 §8].
//
// The probe writes one line per tick to the file named by
// NANOLATHE_AIRDIAG_OUT (a directory), because go test summarises stdout
// away. With the variable unset it only asserts.

const diagVictim = "CORAK" // a CORE kbot: a ground target with no anti-air

// attackTrace is the per-tick observation the attack probes add on top of Row:
// slot 0's state, the shot count attributed to the shooter, and the victim.
type attackTrace struct {
	Row
	SlotTargetKind uint8
	SlotFlags      uint8
	SlotReload     int32
	AimReady       bool
	AimPending     bool
	ShotsSoFar     int
	LiveShots      int
	VictimHealth   int32
	Distance       float64
}

func (a attackTrace) format() string {
	return fmt.Sprintf("%s | slot0 tk=%d fl=%#02x rl=%d aimReady=%v aimPend=%v | shots=%d live=%d victimHP=%d dist=%.1f",
		a.Row.Format(), a.SlotTargetKind, a.SlotFlags, a.SlotReload, a.AimReady, a.AimPending, a.ShotsSoFar, a.LiveShots, a.VictimHealth, a.Distance)
}

func openTraceFile(t *testing.T, name string) *os.File {
	t.Helper()
	dir := os.Getenv("NANOLATHE_AIRDIAG_OUT")
	if dir == "" {
		return nil
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// runAttackProbe spawns the attacker near the commander, a victim `cells`
// cells WEST of it — the ARM start on this map is near the east edge, and a
// victim placed beyond it is an off-map target, which is a different retail
// behaviour [04 R-AIR-01 §5] — orders the attack, and traces n ticks.
func runAttackProbe(t *testing.T, key string, cells int32, n int) (*Harness, *units.Unit, *units.Unit, []attackTrace) {
	t.Helper()
	h := newHarness(t)
	u := spawnAircraft(t, h, key, 6, 6)
	anchor := h.Unit(0, diagAnchor)
	vx := anchor.X.Sub(world.CellToWorld(cells - 6))
	vz := anchor.Z.Add(world.CellToWorld(6))
	victim, err := h.Spawn(1, diagVictim, vx, vz)
	if err != nil {
		t.Skipf("spawn %s: %v", diagVictim, err)
	}
	if err := h.Order(u, int(input.LatchAttack), victim.Handle, victim.X, victim.Y, victim.Z); err != nil {
		t.Fatalf("submit attack: %v", err)
	}

	f := openTraceFile(t, strings.ToLower(key)+"_attack.log")
	logf := func(format string, args ...any) {
		if f != nil {
			fmt.Fprintf(f, format+"\n", args...)
		}
	}
	w1 := "nil"
	if u.Def.Weapon1Def != nil {
		w := u.Def.Weapon1Def
		w1 = fmt.Sprintf("%s(%s) range=%d toair=%v dropped=%v ballistic=%v noautorange=%v reload=%v turret=%v los=%v selfprop=%v vlaunch=%v guidance=%v tol=%d pitchtol=%d",
			u.Def.Weapon1, w.Name, w.Range, w.ToAirWeapon, w.Dropped, w.Ballistic, w.NoAutoRange, w.ReloadTime,
			w.Turret, w.LineOfSight, w.SelfProp, w.VLaunch, w.Guidance, w.Tolerance, w.PitchTolerance)
	}
	logf("map cells %dx%d (%d x %d world units)", h.Session.World.CellW, h.Session.World.CellH, h.Session.World.CellW*16, h.Session.World.CellH*16)
	logf("attacker=%s hoverattack=%v cruisealt=%d maxvel=%d attackrunlength=%d w1=[%s] victim=%s at (%.1f,%.1f,%.1f) hp=%d",
		u.Def.UnitName, u.Def.HoverAttack, u.Def.CruiseAlt, u.Def.MaxVelocity, u.Def.AttackRunLength, w1,
		victim.Def.UnitName, fx(victim.X), fx(victim.Y), fx(victim.Z), victim.Health)

	rows := make([]attackTrace, 0, n)
	shots := 0
	prevReload := int32(0)
	if s := u.SlotAt(0); s != nil {
		prevReload = s.Reload
	}
	for i := 0; i < n; i++ {
		h.Step(1)
		a := attackTrace{Row: h.Observe(u), VictimHealth: victim.Health}
		if s := u.SlotAt(0); s != nil {
			a.SlotTargetKind, a.SlotFlags, a.SlotReload = uint8(s.Target.Kind), s.Flags, s.Reload
			a.AimReady, a.AimPending = s.Aim.Ready, s.Aim.IssueBit
			// A reload countdown that rises is a shot: the pipeline reloads
			// only after admission [06 §3.3].
			if s.Reload > prevReload {
				shots++
				logf("t=%d FIRE slot0 reload %d -> %d", a.Tick, prevReload, s.Reload)
			}
			prevReload = s.Reload
		}
		if c := h.Session.Combat; c != nil {
			for idx := 0; idx < c.Count() && idx < len(c.Records); idx++ {
				if !c.Records[idx].Dead && c.Records[idx].Shooter == u.Handle {
					a.LiveShots++
				}
			}
		}
		a.ShotsSoFar = shots
		dx, dz := fx(u.X)-fx(victim.X), fx(u.Z)-fx(victim.Z)
		a.Distance = sqrtf(dx*dx + dz*dz)
		rows = append(rows, a)
		logf("%s", a.format())
	}
	return h, u, victim, rows
}

func sqrtf(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 40; i++ {
		x = (x + v/x) / 2
	}
	return x
}

// TestAirAttackBrawlerHovers: `hoverattack` resolves `AirToGroundHover`; the
// gunship must reach its standoff and fire on the target repeatedly.
func TestAirAttackBrawlerHovers(t *testing.T) {
	_, u, victim, rows := runAttackProbe(t, "ARMBRAWL", 40, 900)
	last := rows[len(rows)-1]
	t.Logf("%s: head=%q phase=%d shots=%d victimHP=%d dist=%.1f", u.Def.UnitName, last.HeadName, last.HeadPhase, last.ShotsSoFar, last.VictimHealth, last.Distance)
	// Before WU-19-226 the standoff marker carried no target, the gunship
	// faced its line of flight, and its line-of-sight weapon passed the
	// `tolerance` yaw gate [06 R-WPN-03 §2] about once per orbit leg: nine
	// shots in 900 ticks against an 18-tick reload. Bound to the target the
	// gunship faces it while it slides, and fires on every reload until the
	// target dies [04 R-AIR-01 §8][04 R-AIR-01 §4].
	if last.ShotsSoFar < 5 || last.VictimHealth >= 265 {
		t.Errorf("the gunship fired %d shots and left %s at %d health in %d ticks (head=%q phase=%d dist=%.1f): the standoff is not facing its target",
			last.ShotsSoFar, victim.Def.UnitName, last.VictimHealth, len(rows), last.HeadName, last.HeadPhase, last.Distance)
	}
}

// TestAirAttackFighterStrafesGround: a fighter with a non-dropped primary
// resolves `AirToGround` against a ground target and flies strafing runs.
func TestAirAttackFighterStrafesGround(t *testing.T) {
	for _, key := range []string{"ARMHAWK", "ARMFIG"} {
		t.Run(key, func(t *testing.T) {
			_, u, victim, rows := runAttackProbe(t, key, 40, 900)
			last := rows[len(rows)-1]
			t.Logf("%s: head=%q phase=%d shots=%d victimHP=%d dist=%.1f", u.Def.UnitName, last.HeadName, last.HeadPhase, last.ShotsSoFar, last.VictimHealth, last.Distance)
			// One missile per pass is the 90-tick reload against a pass that
			// crosses the target in under 60 ticks; what the fix restores is
			// the pass cadence — a fly-through of three ranges beyond the
			// target, the break at one range, and the return to within range
			// [04 R-AIR-01 §8] — which the recovery-marker detour used to
			// stretch past 700 ticks whenever the fly-through left the map.
			if last.ShotsSoFar < 2 || last.VictimHealth >= 265 {
				t.Errorf("the fighter fired %d shots and left %s at %d health in %d ticks (head=%q phase=%d dist=%.1f)",
					last.ShotsSoFar, victim.Def.UnitName, last.VictimHealth, len(rows), last.HeadName, last.HeadPhase, last.Distance)
			}
		})
	}
}
