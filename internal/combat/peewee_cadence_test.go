// Play-test lock for the reported "Peewee seems to shoot slower and no
// projectiles render when firing". It lives in the external test package so it
// can drive the authoritative session, which is the only vantage point from
// which the whole cadence — reload countdown, burst scheduler, publication —
// is visible at once.
package combat_test

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestPeeweeFiringCadenceAndProjectilePublication drives one stock Peewee onto
// one stock target and locks two things about what comes out of it.
//
// The cadence. The Peewee has ONE weapon slot, `emg`, authored
// `reloadtime=0.4` `burst=3` `burstrate=0.1` — 12, 3 and 3 ticks after the
// catalog's `× 30` truncation. The slot's reload is written on the shot and
// decremented once per tick at the top of its own pass, so a stored reload of
// n blocks n subsequent ticks and roots fire 12 ticks apart [06 §4.2]. Each
// root is a stationary anchor whose attempts fall due at `creation + burstrate`
// and advance by `burstrate` rather than re-anchoring, so an authored burst of
// N launches N pellets — here at +3, +6 and +9 — and the anchor retires
// silently as the last one launches [06 §4.3]. The pellet ticks therefore run
// 3, 3, 6, 3, 3, 6 … and a build that dropped a pellet or re-anchored a
// deadline would show a wider gap.
//
// The publication. `emg` is `rendertype=4` `color=2`, so its pellets draw the
// ground `shadow` frame plus a frame of `plasmamd`, the entry the `color` byte
// selects [06 R-WFX-01 §4]. That selector has to reach the committed view: it
// was published as the unconditional −1 suppression sentinel, which is what
// made every gun and shell in the game invisible in flight.
func TestPeeweeFiringCadenceAndProjectilePublication(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	cfg := session.SkirmishConfig{MapName: "ashap plateau", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0
	cfg.Players[1].Controller = 1
	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioSkirmish, Map: "ashap plateau", Skirmish: cfg, LocalOwner: 0, FS: fs,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := composed.Session
	scaled := sess.Clock.ScaledAnchor
	step := func(n int) {
		for i := 0; i < n; i++ {
			scaled++
			sess.Step(scaled)
		}
	}
	step(60)

	var com *units.Unit
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Def != nil && u.Def.Commander && u.Owner == 0 {
			com = u
			break
		}
	}
	if com == nil {
		t.Skip("skirmish composed without a human commander")
	}
	shooterDef := sess.Catalog.Units[content.CanonicalKey("armpw")]
	victimDef := sess.Catalog.Units[content.CanonicalKey("armsolar")]
	if shooterDef == nil || victimDef == nil {
		t.Skip("stock armpw/armsolar absent from the catalog")
	}
	shooterH, err := sess.Units.Create(shooterDef, 0, com.X.Add(numeric.FixedFromInt(96)), com.Y, com.Z)
	if err != nil {
		t.Fatalf("create the Peewee: %v", err)
	}
	victimH, err := sess.Units.Create(victimDef, 1, com.X.Add(numeric.FixedFromInt(176)), com.Y, com.Z)
	if err != nil {
		t.Fatalf("create the target: %v", err)
	}
	victim := sess.Units.Unit(victimH)

	// The target is held alive so the scenario measures cadence rather than how
	// quickly a solar collector dies.
	var rootTicks, pelletTicks []uint32
	var selector, renderType int32
	selector = -2
	for tick := 0; tick < 260 && len(pelletTicks) < 12; tick++ {
		step(1)
		victim.Health = victim.MaxHealth
		now := sess.Clock.GlobalTick
		for i := 0; i < sess.Combat.Count(); i++ {
			p := sess.Combat.Records[i]
			if p.Shooter != shooterH || p.CreationTick != now {
				continue
			}
			if p.BurstRemaining > 0 {
				rootTicks = append(rootTicks, now)
			} else {
				pelletTicks = append(pelletTicks, now)
			}
		}
		if snap := sess.Snapshot.Current(); snap != nil {
			for _, v := range snap.Projectiles {
				if v.Shooter == pool.Handle(shooterH) {
					selector, renderType = v.Selector, v.RenderType
				}
			}
		}
	}
	if len(rootTicks) < 4 || len(pelletTicks) < 12 {
		t.Fatalf("the Peewee did not settle into a firing cadence: %d roots, %d pellets", len(rootTicks), len(pelletTicks))
	}
	// Roots 12 ticks apart: the authored `reloadtime` of 0.4 s [06 §4.2].
	for i := 1; i < len(rootTicks); i++ {
		if gap := rootTicks[i] - rootTicks[i-1]; gap != 12 {
			t.Fatalf("root %d fired %d ticks after the previous one, want the authored reload of 12 [06 §4.2]: %v", i, gap, rootTicks)
		}
	}
	// Three pellets per root, at +3, +6 and +9 [06 §4.3].
	for i, launch := range pelletTicks[:12] {
		want := rootTicks[i/3] + uint32(3*(i%3+1))
		if launch != want {
			t.Fatalf("pellet %d launched on tick %d, want %d (root %d + %d × burstrate) [06 §4.3]: roots %v pellets %v",
				i, launch, want, i/3, i%3+1, rootTicks, pelletTicks)
		}
	}
	// The presentation half: `emg` is rendertype 4 with `color=2`, so the
	// committed view must carry selector 2 — `plasmamd` [06 R-WFX-01 §4].
	if renderType != render.RenderTypeSelectorGAF || selector != 2 {
		t.Fatalf("published rendertype %d selector %d, want %d and 2 (the `color` byte, not the −1 suppression sentinel) [06 R-WFX-01 §4]",
			renderType, selector, render.RenderTypeSelectorGAF)
	}
}
