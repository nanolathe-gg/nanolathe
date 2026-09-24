//go:build pathbench && retail

package session

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Everything here is an authored experiment, not a retail behavior claim.
// Registration describes test inputs only; gameplay uses Session.SetRules.
type pbCase struct {
	ID, Family, Description string
	Sizes                   []int
	Ticks                   int
	Build                   func(*testing.T, string, int) *pbScene
}

var pbCases []pbCase

func pbRegister(cases ...pbCase) { pbCases = append(pbCases, cases...) }

type pbScene struct {
	S       *Session
	Actors  []*pbActor
	Events  []pbEvent
	Regions []pbRegion
	Notes   []string
	Inputs  []string
}
type pbActor struct {
	Handle          pool.Handle
	Key, Cohort     string
	Start           [2]int32
	Goal            [2]numeric.Fixed
	GoalActive      bool
	GoalTick        int
	Radius          int32 // authored reporting radius in world units, not order acceptance
	CaptureGoal     bool  // take actual group-adjusted destination from installed order
	ExpectedRemoval bool
}
type pbEvent struct {
	Tick  int
	Label string // exact parameters belong here, or in Inputs
	Apply func(*testing.T, *pbScene)
}
type pbRegion struct {
	Name           string
	X0, Z0, X1, Z1 int32 // half-open cell rectangle
}

func pbCell(c int32) numeric.Fixed { return world.CellToWorld(c) + numeric.FixedFromInt(8) }
func pbTerrain(t *testing.T, w, h int32, height, sea uint8) *world.Terrain {
	t.Helper()
	attrs := make([]formats.TNTAttribute, int(w*h))
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: height, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, int(w), int(h))
	ter := &world.Terrain{CellW: w, CellH: h, Plot: plot, Version: world.VersionCanonical, SeaLevel: sea, WindMin: 100, WindMax: 200}
	if err := ter.ApplySchema(nil, 0); err != nil {
		t.Fatal(err)
	}
	return ter
}
func pbNew(t *testing.T, rules string, ter *world.Terrain) *pbScene {
	t.Helper()
	cat, fs := retailcat.Shared(t)
	set, ok := LookupRuleSet(rules)
	if !ok {
		t.Fatalf("unknown benchmark rule set %q", rules)
	}
	mode := set.Base
	s := &Session{Gameplay: mode, Catalog: cat, World: ter, Mission: syntheticMission(), Clock: &clock.State{Requested: 10, Active: 10}, Snapshot: &frame.Buffer{}}
	if err := s.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	s.EntryCommunity = s.Community
	s.SeedSessionRNG(7, 11)
	var err error
	s.Units, err = newBattleSlicedWorldWithCOBSized(cat, fs, 0, [pool.PlayerCount]uint32{}, 500)
	if err != nil {
		t.Fatal(err)
	}
	s.Econ = economyForTest()
	for i := range s.Econ.Players {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = 2
		if i == 0 {
			p.ControllerState = 1
		}
		p.EndGameCountdown = -1
		for j := range p.Allies {
			p.Allies[j] = true
		}
	}
	s.Econ.SeedDeadlines(0)
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatal(err)
	}
	s.RegisterAll()
	s.State = StateBattle
	if err := s.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	s.SeedSessionRNG(7, 11)
	return &pbScene{S: s}
}
func pbAdd(t *testing.T, sc *pbScene, key, cohort string, owner uint8, cx, cz int32) *pbActor {
	t.Helper()
	def, ok := sc.S.Catalog.Unit(key)
	if !ok || def == nil {
		t.Fatalf("required retail unit %s absent", key)
	}
	// Resolve authored extents before stamping, using the production profile and
	// anchor quantizer. Cell centers are not footprint anchors for large units.
	profile := movement.NewScratchProfile(def)
	if mc := sc.S.Catalog.Movement[content.CanonicalKey(def.MovementClass)]; mc != nil {
		profile = movement.NewProfile(mc)
	}
	fx, fz := profile.FootPrintX, profile.FootPrintZ
	if def.BMCode == 0 {
		fx, fz = int16(def.FootprintX), int16(def.FootprintZ)
	}
	if fx <= 0 {
		fx = int16(max(1, def.FootprintX))
	}
	if fz <= 0 {
		fz = int16(max(1, def.FootprintZ))
	}
	trial := movement.CollisionState{X: int32(pbCell(cx)), Z: int32(pbCell(cz)), FootPrintX: fx, FootPrintZ: fz, Mode: 1}
	anchor := trial.ProposedAnchor(1)
	for z := anchor.Z; z < anchor.Z+int32(fz); z++ {
		for x := anchor.X; x < anchor.X+int32(fx); x++ {
			if x < 0 || z < 0 || x >= sc.S.World.CellW || z >= sc.S.World.CellH {
				t.Fatalf("%s spawn footprint outside terrain at %d,%d", key, x, z)
			}
			if prior, occupied := sc.S.Movement.Grid.OccupantAtPlane(movement.PlaneGround, movement.Cell{X: x, Z: z}); occupied {
				t.Fatalf("%s spawn overlaps %d at %d,%d", key, prior, x, z)
			}
		}
	}
	if !def.CanFly && !profile.IsPassableFootprint(sc.S.World, anchor.X, anchor.Z) {
		t.Fatalf("%s spawn terrain blocked at anchor %d,%d", key, anchor.X, anchor.Z)
	}
	u := placeCompleteRetailUnit(t, sc.S, key, owner, pbCell(cx), pbCell(cz))
	sc.S.Movement.EnsureUnit(u)
	if actual, ax, az, ok := sc.S.Movement.CommittedFootprint(u.Handle); ok && (actual != anchor || ax != fx || az != fz) {
		t.Fatalf("%s spawn footprint differs from preflight", key)
	}

	a := &pbActor{Handle: u.Handle, Key: strings.ToLower(key), Cohort: cohort, Start: [2]int32{cx, cz}, Radius: 16}
	sc.Actors = append(sc.Actors, a)
	return a
}
func pbMove(t *testing.T, sc *pbScene, actors []*pbActor, gx, gz int32, assigned, queued bool) {
	pbMoveWorld(t, sc, actors, pbCell(gx), pbCell(gz), assigned, queued)
}
func pbMoveWorld(t *testing.T, sc *pbScene, actors []*pbActor, gx, gz numeric.Fixed, assigned, queued bool) {
	t.Helper()
	handles := make([]pool.Handle, len(actors))
	for i, a := range actors {
		u := sc.S.Units.Unit(a.Handle)
		if u == nil || !u.Alive || u.Owner != sc.S.LocalOwner {
			t.Fatalf("human move actor %d is not a live unit of local owner %d", a.Handle, sc.S.LocalOwner)
		}
		handles[i] = a.Handle
		a.Goal = [2]numeric.Fixed{gx, gz}
		a.GoalActive = true
		a.GoalTick = int(sc.S.Clock.GlobalTick) + 1
		a.CaptureGoal = true
	}
	err := sc.S.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: handles, Code: 2, Position: orders.ResolvePos{X: gx, Y: sc.S.World.HeightAt(gx, gz), Z: gz, InterfaceType: orders.InterfaceTypeRightClick}, AssignedPosition: assigned, Queued: queued}})
	if err != nil {
		t.Fatal(err)
	}
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("move tick=%d handles=%v goal_raw=%d,%d assigned=%t queued=%t", sc.S.Clock.GlobalTick+1, handles, gx, gz, assigned, queued))
}
func pbWall(ter *world.Terrain, x0, z0, x1, z1 int32) {
	for z := z0; z < z1; z++ {
		for x := x0; x < x1; x++ {
			ter.PlotAt(x, z).SetFeature(world.PlotFeatureVoid)
		}
	}
}
func TestPathBenchFoundation(t *testing.T) {
	if os.Getenv("NANOLATHE_PATH_BENCH_SMOKE") == "" {
		t.Skip("opt-in foundation check")
	}
	sc := pbNew(t, "modern", pbTerrain(t, 64, 64, 20, 0))
	a := pbAdd(t, sc, "armflea", "single", 0, 8, 8)
	pbMove(t, sc, []*pbActor{a}, 48, 48, false, false)
	publishVisibilityForAll(sc.S)
	for i := 0; i < 10; i++ {
		tick := sc.S.Clock.BeginSubTick()
		sc.S.stepAuthoritativePhases(tick)
	}
	// A later command must still target the intended human after player phases.
	pbMove(t, sc, []*pbActor{a}, 16, 16, true, false)
	sc.S.stepAuthoritativePhases(sc.S.Clock.BeginSubTick())
	q := orders.QueueOfUnit(sc.S.Units.Unit(a.Handle))
	if q == nil || q.Head() == nil || q.Head().ID != orders.Lookup("Move_Ground") || q.Head().GoalX != pbCell(16) || q.Head().GoalZ != pbCell(16) {
		t.Fatal("later human move was not installed")
	}
}

func init() {
	pbRegister(pbCase{ID: "control_single_flea", Family: "control", Description: "One retail ARM Flea on open terrain; verifies the runner with an uncongested move.", Sizes: []int{1}, Ticks: 900, Build: func(t *testing.T, rules string, n int) *pbScene {
		sc := pbNew(t, rules, pbTerrain(t, 64, 64, 20, 0))
		a := pbAdd(t, sc, "armflea", "control", 0, 8, 8)
		pbMove(t, sc, []*pbActor{a}, 48, 48, false, false)
		return sc
	}})
}
