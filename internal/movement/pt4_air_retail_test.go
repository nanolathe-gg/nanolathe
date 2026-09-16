//go:build retail

package movement_test

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The two air play-test items of round PT4, on the authored corpus:
//
//   - a construction aircraft ordered to build far away must fly to within
//     `builddistance` of the site before its nanoframe exists
//     [04 R-ORD-02 §2];
//   - a fighter on patrol at fire at will must engage what its opportunity
//     scan finds along the leg [04 R-ORD-02 §2][04 R-STANCE-01 §3].
//
// Both compose the ordinary skirmish session on `ashap plateau`, place the
// aircraft next to the ARM commander through the ordinary creator, and drive
// the session's own Step loop — the same path the play-test exercised.

const (
	pt4Map   = "ashap plateau"
	pt4ARM   = "ARMCOM"
	pt4CA    = "ARMCA"
	pt4FIG   = "ARMFIG"
	pt4Solar = "ARMSOLAR"
	pt4AK    = "CORAK"
	pt4Vamp  = "CORVAMP"
)

type pt4Fixture struct {
	s   *session.Session
	cat *content.Catalog
	now int32
}

func pt4Session(t *testing.T) *pt4Fixture {
	t.Helper()
	cat, fs := retailcat.Shared(t)
	if _, ok := cat.Maps[content.CanonicalKey(pt4Map)]; !ok {
		t.Skipf("retail fixture map %q is absent", pt4Map)
	}
	for _, key := range []string{pt4ARM, pt4CA, pt4FIG, pt4Solar, pt4AK, pt4Vamp} {
		if def, ok := cat.Unit(key); !ok || def == nil {
			t.Skipf("retail fixture unit %q is absent", key)
		}
	}
	cfg := session.SkirmishConfig{MapName: pt4Map, NumPlayers: 2, RNGSimSeed: 12345, RNGCrtSeed: 67890}
	cfg.ApplyDefaults()
	cfg.Players[0].Side, cfg.Players[0].Controller = 0, session.SkirmishControllerHuman
	cfg.Players[1].Side, cfg.Players[1].Controller = 1, session.SkirmishControllerComputer
	cfg.Players[0].AllyGroup, cfg.Players[1].AllyGroup = 5, 5
	s, err := session.NewSkirmishWithProgress(fs, cat, cfg, nil)
	if err != nil {
		t.Fatalf("construct retail %q: %v", pt4Map, err)
	}
	if err := s.ValidateComposition(); err != nil {
		t.Fatalf("validate retail composition: %v", err)
	}
	f := &pt4Fixture{s: s, cat: cat}
	// The first dispatch completes loading and the next runs the state-6
	// handler before ticking [08 "Session states"].
	f.step(2)
	return f
}

func (f *pt4Fixture) step(ticks int) {
	for i := 0; i < ticks; i++ {
		f.now++
		f.s.Step(f.now)
	}
}

func (f *pt4Fixture) unit(owner uint8, key string) *units.Unit {
	for _, u := range f.s.Units.IterSliced() {
		if u != nil && u.Alive && u.Owner == owner && u.Def != nil && strings.EqualFold(u.Def.UnitName, key) {
			return u
		}
	}
	return nil
}

// place creates an authored unit through the ordinary already-built creator
// and gives it the mover registration battle-entry placement gives every
// authored unit [01 §6.1].
func (f *pt4Fixture) place(t *testing.T, key string, owner uint8, x, z numeric.Fixed) *units.Unit {
	t.Helper()
	def, ok := f.cat.Unit(key)
	if !ok || def == nil {
		t.Fatalf("authored %s missing", key)
	}
	h, err := f.s.Units.Create(def, owner, x, f.s.World.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("place authored %s: %v", key, err)
	}
	u := f.s.Units.Unit(h)
	if u == nil {
		t.Fatalf("created %s not resolvable", key)
	}
	f.s.Movement.EnsureUnit(u)
	return u
}

// legalSite finds the nearest legal footprint anchor for product key about
// (x, z), walking outward in two-cell rings exactly as the session package's
// own retail fixture does.
func (f *pt4Fixture) legalSite(t *testing.T, key string, x, z numeric.Fixed) (numeric.Fixed, numeric.Fixed) {
	t.Helper()
	def, ok := f.cat.Unit(key)
	if !ok || def == nil {
		t.Fatalf("product %q absent", key)
	}
	extent, err := world.NewFootprintExtent(int32(def.FootprintX), int32(def.FootprintZ))
	if err != nil {
		t.Fatalf("footprint for %q: %v", key, err)
	}
	yard, err := world.ParseYardMap(def.YardMap, int(def.FootprintX), int(def.FootprintZ))
	if err != nil {
		t.Fatalf("yardmap for %q: %v", key, err)
	}
	rules, err := world.PlacementRulesForUnit(f.cat, def)
	if err != nil {
		t.Fatalf("placement rules for %q: %v", key, err)
	}
	for r := 0; r <= 24; r += 2 {
		for dz := -r; dz <= r; dz += 2 {
			for dx := -r; dx <= r; dx += 2 {
				if r != 0 && dx != -r && dx != r && dz != -r && dz != r {
					continue
				}
				cx := x.Add(world.CellToWorld(int32(dx)))
				cz := z.Add(world.CellToWorld(int32(dz)))
				anchor, err := world.SnapFootprintAnchor(cx, cz, extent)
				if err != nil {
					continue
				}
				rect, err := world.NewFootprintRect(anchor, extent)
				if err != nil {
					continue
				}
				if _, err := f.s.World.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules, Mobile: def.BMCode != 0}); err == nil {
					return cx, cz
				}
			}
		}
	}
	t.Fatalf("no legal %q site near the requested point", key)
	return 0, 0
}

// levelPoint finds a point `cells` cells from (x, z), trying sixteen bearings
// in a fixed order, whose terrain height is within `slack` world units of the
// height at (x, z), so a scenario about weapon geometry is not confounded by a
// plateau edge.
func (f *pt4Fixture) levelPoint(t *testing.T, x, z numeric.Fixed, cells int32, slack int64) (numeric.Fixed, numeric.Fixed) {
	t.Helper()
	base := int64(f.s.World.HeightAt(x, z)) >> 16
	for i := 0; i < 16; i++ {
		angle := uint16(i * 4096)
		ox, oz := pt4Offset(angle, world.CellToWorld(cells))
		px, pz := x+ox, z+oz
		if px <= 0 || pz <= 0 {
			continue
		}
		h := int64(f.s.World.HeightAt(px, pz)) >> 16
		d := h - base
		if d < 0 {
			d = -d
		}
		if d <= slack {
			return px, pz
		}
	}
	t.Fatalf("no point %d cells from (%d,%d) within %d world units of its height", cells, int64(x)>>16, int64(z)>>16, slack)
	return 0, 0
}

// pt4Offset is a plain integer-cosine/sine displacement for scenario setup
// only; nothing authoritative reads it.
func pt4Offset(angle uint16, r numeric.Fixed) (numeric.Fixed, numeric.Fixed) {
	// 16 fixed bearings at 22.5-degree steps; cos/sin scaled by 1024.
	cos := []int64{1024, 946, 724, 392, 0, -392, -724, -946, -1024, -946, -724, -392, 0, 392, 724, 946}
	i := int(angle / 4096)
	return numeric.Fixed(int64(r) * cos[i] / 1024), numeric.Fixed(int64(r) * cos[(i+12)%16] / 1024)
}

func pt4Dist(a *units.Unit, x, z numeric.Fixed) int64 {
	dx := int64(a.X-x) >> 16
	dz := int64(a.Z-z) >> 16
	return numeric.ISqrt64(dx*dx + dz*dz)
}

// TestRetailConstructionAircraftFliesToTheSiteBeforeBuilding is play-test
// item PT4 "construction of nanoframes far away begins too early".
// `VTOL_MobileBuild` phase 1 installs a point marker at the snapped site with
// horizontal arrival radius `builddistance` and arms gate `0xE0`; phase 2 —
// the placement validator and the nanoframe creator — is dispatched only by
// that marker's arrival [04 R-ORD-02 §2]. The nanoframe must therefore not
// exist until the aircraft has closed to within `builddistance` of the site.
func TestRetailConstructionAircraftFliesToTheSiteBeforeBuilding(t *testing.T) {
	f := pt4Session(t)
	com := f.unit(0, pt4ARM)
	if com == nil {
		t.Fatal("ARM commander not spawned")
	}
	ca := f.place(t, pt4CA, 0, com.X.Add(world.CellToWorld(4)), com.Z.Add(world.CellToWorld(4)))
	if ca.Def.BuildDistance <= 0 {
		t.Fatalf("authored %s builddistance = %d", pt4CA, ca.Def.BuildDistance)
	}
	// Forty cells east: about 640 world units, sixteen times the
	// builddistance of 40.
	siteX, siteZ := f.legalSite(t, pt4Solar, ca.X.Add(world.CellToWorld(40)), ca.Z)
	if err := f.s.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanMobileBuild, MobileBuild: session.HumanMobileBuildCommand{
		Builder: ca.Handle, Product: pt4Solar, WX: siteX, WZ: siteZ, WY: f.s.World.HeightAt(siteX, siteZ),
	}}); err != nil {
		t.Fatalf("enqueue build: %v", err)
	}

	var product *units.Unit
	var createdTick int
	// The builder's position at the start of each tick: the flight command
	// producer runs the marker's arrival test on the position the previous
	// tick committed, before the integrator moves the aircraft, and the pump
	// that creates the product runs on the tick after the arrival. The
	// position that satisfied `hypot < builddistance` is therefore the one
	// two ticks before creation.
	type pos struct{ x, z numeric.Fixed }
	history := []pos{{ca.X, ca.Z}}
	startDist := pt4Dist(ca, siteX, siteZ)
	for tick := 1; tick <= 1500 && product == nil; tick++ {
		f.step(1)
		history = append(history, pos{ca.X, ca.Z})
		if p := f.unit(0, pt4Solar); p != nil {
			product = p
			createdTick = tick
		}
	}
	if product == nil {
		head := orders.QueueForUnit(ca).Head()
		name := "<none>"
		if head != nil {
			name = orders.DescriptorFor(head.ID).Name
		}
		t.Fatalf("no nanoframe after 1500 ticks; builder at %d world units from the site, head %s", pt4Dist(ca, siteX, siteZ), name)
	}
	if createdTick < 2 {
		t.Fatalf("nanoframe created at tick %d, before the aircraft could fly anywhere", createdTick)
	}
	tested := history[createdTick-2]
	dx := int64(tested.x-product.X) >> 16
	dz := int64(tested.z-product.Z) >> 16
	testedDist := numeric.ISqrt64(dx*dx + dz*dz)
	if testedDist >= int64(ca.Def.BuildDistance) {
		t.Fatalf("nanoframe created at tick %d with the aircraft %d world units from the product (started %d from the site); the air leg arrives only inside builddistance %d [04 R-ORD-02 §2]",
			createdTick, testedDist, startDist, ca.Def.BuildDistance)
	}
	t.Logf("nanoframe created at tick %d, builder %d world units from the product when the marker arrived (started %d from the site, builddistance %d)", createdTick, testedDist, startDist, ca.Def.BuildDistance)
}

// pt4Patrol issues the patrol click of [07 §9] code 9 on the fighter at a
// point along the fixture's east-west line.
func pt4Patrol(t *testing.T, f *pt4Fixture, u *units.Unit, x, z numeric.Fixed) {
	t.Helper()
	if err := f.s.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{
		Handles: []pool.Handle{u.Handle}, Code: 9, Position: orders.ResolvePos{X: x, Y: f.s.World.HeightAt(x, z), Z: z},
	}}); err != nil {
		t.Fatalf("enqueue patrol: %v", err)
	}
}

// pt4Engagement is what one patrol scenario observed: the tick the fighter's
// queue head became an attack record on the victim, the tick its first missile
// was launched with the victim retained as the projectile's unit target, and
// the tick the victim first took damage (0 when it never did).
type pt4Engagement struct{ engaged, launched, damaged int }

// pt4RunPatrolEngagement drives the fighter's patrol for `limit` ticks and
// reports what happened. The head sequence is logged so an engagement that is
// issued and then dropped is visible in the test output.
func pt4RunPatrolEngagement(t *testing.T, f *pt4Fixture, fig, victim *units.Unit, limit int) pt4Engagement {
	t.Helper()
	var e pt4Engagement
	startHealth := victim.Health
	last := ""
	for tick := 1; tick <= limit; tick++ {
		f.step(1)
		head := orders.QueueForUnit(fig).Head()
		name := "<none>"
		if head != nil {
			name = orders.DescriptorFor(head.ID).Name
		}
		if name != last {
			t.Logf("tick %d: head %s, fighter %d world units from the victim, victim health %d/%d",
				tick, name, pt4Dist(fig, victim.X, victim.Z), victim.Health, startHealth)
			last = name
		}
		if e.engaged == 0 && head != nil && strings.HasPrefix(name, "Air") && head.Target == victim.Handle {
			e.engaged = tick
		}
		if e.launched == 0 {
			f.s.Combat.ForEachAliveInEntrySpan(func(h pool.Handle, p *combat.Projectile) {
				if e.launched == 0 && p.Shooter == fig.Handle && p.TargetUnit == victim.Handle {
					e.launched = tick
					t.Logf("tick %d: missile launched at the victim from %d world units", tick, pt4Dist(fig, victim.X, victim.Z))
				}
			})
		}
		if e.damaged == 0 && (victim.Health < startHealth || !victim.Alive) {
			e.damaged = tick
			t.Logf("tick %d: victim damaged (%d/%d)", tick, victim.Health, startHealth)
		}
		if e.engaged != 0 && e.launched != 0 && e.damaged != 0 {
			break
		}
	}
	return e
}

// TestRetailFighterOnPatrolEngagesAnEnemyAircraft is play-test item PT4
// "Freedom Fighter set to fire at will on patrol will not attack enemy
// units". `VTOL_Patrol` phase 2 runs the opportunity scan on every leg visit
// and feeds its target to the auto-engage issuer [04 R-ORD-02 §2]; the scan
// searches only at fire at will, which the authored ARMFIG starts at
// [04 R-STANCE-01 §3][04 R-STANCE-01 §6].
func TestRetailFighterOnPatrolEngagesAnEnemyAircraft(t *testing.T) {
	f := pt4Session(t)
	com := f.unit(0, pt4ARM)
	if com == nil {
		t.Fatal("ARM commander not spawned")
	}
	fig := f.place(t, pt4FIG, 0, com.X.Add(world.CellToWorld(4)), com.Z.Add(world.CellToWorld(6)))
	if fig.Flags>>units.StandingFireShift&units.StandingFieldMask != 2 {
		t.Fatalf("authored %s does not start at fire at will", pt4FIG)
	}
	// The victim starts thirty cells out on level ground and patrols back
	// across the fighter's leg so that it is airborne when the two meet. It is
	// disarmed so that the engagement observed is the fighter's own. Clearing
	// its standing-fire field alone does not achieve that: side 1 is the
	// fixture's AI controller and its planner puts its own units back at fire
	// at will within thirty ticks, after which the CORVAMP wins the duel and
	// the fighter dies before it ever fires. Clearing the ARMED status bit as
	// well ends the acquisition scan's visit to this unit before any slot is
	// looked at [06 §3.2 "The third clause is the armed bit"].
	vx, vz := f.levelPoint(t, fig.X, fig.Z, 30, 16)
	vamp := f.place(t, pt4Vamp, 1, vx, vz)
	vamp.Flags &^= units.StandingFieldMask << units.StandingFireShift
	vamp.Flags &^= units.ArmedStatus
	pt4Patrol(t, f, vamp, fig.X, fig.Z)
	fx, fz := fig.X.Add((vx-fig.X)*2), fig.Z.Add((vz-fig.Z)*2)
	pt4Patrol(t, f, fig, fx, fz)
	e := pt4RunPatrolEngagement(t, f, fig, vamp, 1800)
	if e.engaged == 0 {
		t.Fatalf("fighter never engaged the enemy aircraft in 1800 ticks (stance %#x)", fig.Flags>>units.StandingFireShift&units.StandingFieldMask)
	}
	if e.launched == 0 {
		t.Fatalf("fighter engaged at tick %d but launched no missile at the enemy aircraft by tick 1800", e.engaged)
	}
	// The missile must then connect. A `guidance` weapon pursues, in order,
	// its linked projectile, its retained unit target while that unit is live,
	// and only then its stored target point [06 §6.7]; the stored point is the
	// LOST-target fallback [06 §6.8]. Steering at the stored point on every
	// tick — which internal/combat used to do — aims every missile at where
	// the target stood when the shot was created, so no moving aircraft was
	// ever hit and this assertion was a log line.
	if e.damaged == 0 {
		t.Fatalf("fighter launched at tick %d (engaged %d) but never damaged the enemy aircraft by tick 1800: a guided missile must pursue its retained unit target, not its launch point [06 §6.7]", e.launched, e.engaged)
	}
	t.Logf("engaged at tick %d, first missile at tick %d, damaged at tick %d", e.engaged, e.launched, e.damaged)
}

// TestRetailFighterOnPatrolEngagesAGroundUnit: the authored ARMFIG missile
// carries no `toairweapon` and the fighter's `badtargetcategory` is NOTAIR,
// so ground units are the fallback bucket of the acquisition of [06 §3.2] —
// a fighter with no aircraft in reach engages the ground unit it patrols
// over.
func TestRetailFighterOnPatrolEngagesAGroundUnit(t *testing.T) {
	f := pt4Session(t)
	com := f.unit(0, pt4ARM)
	if com == nil {
		t.Fatal("ARM commander not spawned")
	}
	fig := f.place(t, pt4FIG, 0, com.X.Add(world.CellToWorld(4)), com.Z.Add(world.CellToWorld(6)))
	if fig.Def.Weapon1Def == nil || fig.Def.Weapon1Def.ToAirWeapon {
		t.Skipf("authored %s weapon is air-only; a ground engagement is not retail", pt4FIG)
	}
	// Start close enough for the normal visibility and opportunity scans to
	// acquire while the approach is still shallow. The fixed launcher must
	// pass both angular gates [06 §3.3]; the thirty-cell fixture acquired too
	// late to satisfy both gates during any of its fly-throughs.
	vx, vz := f.levelPoint(t, fig.X, fig.Z, 20, 16)
	ak := f.place(t, pt4AK, 1, vx, vz)
	ak.Flags &^= units.StandingFieldMask << units.StandingFireShift
	fx, fz := fig.X.Add((vx-fig.X)*2), fig.Z.Add((vz-fig.Z)*2)
	pt4Patrol(t, f, fig, fx, fz)
	e := pt4RunPatrolEngagement(t, f, fig, ak, 1800)
	if e.engaged == 0 {
		t.Fatalf("fighter never engaged the ground unit in 1800 ticks (stance %#x)", fig.Flags>>units.StandingFireShift&units.StandingFieldMask)
	}
	if e.damaged == 0 {
		t.Fatalf("fighter engaged at tick %d (first missile at tick %d) but never damaged the ground unit by tick 1800", e.engaged, e.launched)
	}
	t.Logf("engaged at tick %d, first missile at tick %d, damaged at tick %d", e.engaged, e.launched, e.damaged)
}
