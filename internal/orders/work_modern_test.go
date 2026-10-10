package orders

import (
	"fmt"
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// These fixtures and policies are authored by Nanolathe; retail's patrol
// visitor, random picks and resource ladder remain covered by their own tests.
type modernWorkFixture struct {
	q                       *Queue
	u                       *units.Unit
	patrol, next, successor *Node
	units                   []*units.Unit
	features                []FeatureView
	resources               ResourceView
	sim                     rng.Simulation
	workCalls, scans        int
	air                     bool
}

func newModernWorkFixture(air bool) *modernWorkFixture {
	q, u, _ := workFixture()
	f := &modernWorkFixture{q: q, u: u, air: air, sim: rng.NewSimulation(7), resources: ResourceView{Stock: [2]float32{100, 100}, Capacity: [2]float32{100, 100}}}
	u.X, u.Z = 0, 0
	u.Def.CanFly, u.Def.SightDistance = air, 1024
	u.Move.Mode, u.Move.ModeMirror = 1, 1
	if air {
		u.Move.Mode, u.Move.ModeMirror = 2, 2
	}
	q.binding.Rules, q.binding.SimRNG = &ModernRules{}, &f.sim
	q.binding.SetResources(func(uint8) (ResourceView, bool) { return f.resources, true })
	q.binding.SetHostility(func(a, b *units.Unit) bool { return a.Owner != b.Owner })
	q.binding.SetLookup(func(h pool.Handle) *units.Unit {
		if h == u.Handle {
			return u
		}
		for _, target := range f.units {
			if target.Handle == h {
				return target
			}
		}
		return nil
	})
	q.binding.World = NewWorldQueryAdapter(WorldQueryAdapterConfig{
		SeaLevel: func() uint8 { return 0 },
		ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
			f.scans++
			for _, target := range f.units {
				if visit(target.Handle, target) {
					break
				}
			}
		},
		ForEachFeature: func(visit func(FeatureView) bool) {
			f.scans++
			for _, feature := range f.features {
				if visit(feature) {
					break
				}
			}
		},
	})
	q.binding.Work.SetAssist(func(*units.Unit, *Node, uint32) bool { f.workCalls++; return false })
	q.binding.Work.SetRepair(func(*units.Unit, *units.Unit, *Node, uint32) bool { f.workCalls++; return false })
	id := rowRepairPatrol
	if air {
		id = rowVTOLRepairPatrol
	}
	f.patrol = newNode(id, NewNodeForOrder(id, 0, numeric.Fixed(600<<16), 0, 0, 0, u.Handle, false))
	f.next = newNode(id, NewNodeForOrder(id, 0, 0, 0, 0, 0, u.Handle, true))
	f.patrol.Phase, f.next.Phase = 1, 1
	f.successor = newNode(rowMoveGround, NewNodeForOrder(rowMoveGround, 0, 0, 0, numeric.Fixed(1000<<16), 0, u.Handle, true))
	q.primary = []*Node{f.patrol, f.next, f.successor}
	return f
}

func (f *modernWorkFixture) visit(tick uint32) Code {
	if f.air {
		return vtolRepairPatrolHandler(f.u, f.patrol, 0, tick)
	}
	return repairPatrolHandler(f.u, f.patrol, 0, tick)
}

func modernPatient(h pool.Handle, x, z int32, unfinished bool) *units.Unit {
	u := &units.Unit{Handle: h, Alive: true, Health: 50, MaxHealth: 100, X: numeric.Fixed(int64(x) << 16), Z: numeric.Fixed(int64(z) << 16), Def: &content.UnitDef{MaxDamage: 100, FootprintX: 2, FootprintZ: 2}}
	u.Move.Mode, u.Move.ModeMirror = 1, 1
	if unfinished {
		u.Remaining = 1
	}
	return u
}

func modernFeature(id uint16, x, z int32, metal, energy int32) FeatureView {
	return FeatureView{ID: id, CX: x / 16, CZ: z / 16, X: numeric.Fixed(int64(x) << 16), Z: numeric.Fixed(int64(z) << 16), Metal: metal, Energy: energy, Reclaimable: true, Autoreclaimable: true}
}

func TestModernBuilderDefaultsAndIndependentPreferences(t *testing.T) {
	for _, r := range []Rules{StrictRules{}, &CommunityRules{}, &ModernRules{}} {
		f := newModernWorkFixture(false)
		f.q.binding.Rules = r
		defaults := r.DefaultBuilderOptions()
		want := DefaultBuilderOptions()
		if _, modern := r.(*ModernRules); modern {
			want.Patrol = [3]PatrolWorkOption{PatrolBoth, PatrolBoth, PatrolBoth}
		}
		if defaults != want {
			t.Fatalf("%T defaults=%v want=%v", r, defaults, want)
		}
		for mode := uint32(0); mode < 3; mode++ {
			f.u.Flags = mode << units.StandingMoveShift
			for _, choice := range []PatrolWorkOption{PatrolReclaimOnly, PatrolBoth, PatrolAssistOnly} {
				options := defaults
				options.Patrol[mode] = choice
				f.q.binding.SetBuilderOptions(func(uint8) BuilderOptions { return options })
				f.q.binding.Community = community.Features{} // Modern does not need CP-CON-3.
				got := r.PatrolWork(PatrolWorkRequest{Builder: f.u})
				want := PatrolBoth
				if _, modern := r.(*ModernRules); modern {
					want = choice
				}
				if got != want {
					t.Fatalf("%T mode%d choice%d got%d", r, mode, choice, got)
				}
			}
		}
		if _, modern := r.(*ModernRules); !modern {
			random, resources := f.sim, f.resources
			if _, handled := r.PatrolWorkVisit(f.u, f.patrol, 100); handled || !r.AutomaticWorkValid(f.u, &Node{workAssignment: f.patrol}) || f.scans != 0 || f.sim != random || f.resources != resources {
				t.Fatalf("%T changed strict/community hook state", r)
			}
		}
	}
}

func TestModernPatrolPrioritizesFactoryNanoframeBeforeRepairAndReclaim(t *testing.T) {
	for _, air := range []bool{false, true} {
		t.Run(fmt.Sprint(air), func(t *testing.T) {
			f := newModernWorkFixture(air)
			factory := modernPatient(10, 300, 0, false)
			factory.Health = 100
			factory.Def.Builder = true
			product := modernPatient(7, 400, 0, true)
			product.Def.CanFly = air
			closer := modernPatient(5, 1, 0, false)
			laterTie := modernPatient(9, 400, 0, true)
			f.units = []*units.Unit{closer, laterTie, factory, product}
			wq := QueueForUnit(factory)
			wq.Push(rowBuildingBuild, Node{Owner: factory.Handle, Target: product.Handle})
			production := wq.Head()
			production.BindTarget(product.Handle)
			f.features = []FeatureView{modernFeature(2, 0, 0, 10, 0)}
			f.resources.Stock[0] = 0
			random, resources := f.sim, f.resources
			if code := f.visit(100); code != 2 {
				t.Fatalf("visit=%d", code)
			}
			head := f.q.Head()
			wantID := rowHelpBuild
			if air {
				wantID = rowVTOLHelpBuild
			}
			if head.ID != wantID || head.Target != product.Handle || head.workAssignment != f.patrol || len(f.q.primary) != 5 || !f.q.primary[1].IsPatrolReturn() || f.q.primary[2] != f.patrol || f.q.primary[3] != f.next || f.q.primary[4] != f.successor {
				t.Fatal("factory frame was not selected with original route and successors retained")
			}
			if f.workCalls != 0 || f.sim != random || f.resources != resources || product.Remaining != 1 || product.Health != 50 {
				t.Fatal("selection performed construction, repair, ledger or RNG work")
			}
			f.q.primary = f.q.primary[2:]
			// The factory's inter-product gap provides no allocated product.
			production.BindTarget(0)
			f.units = []*units.Unit{factory}
			f.features = nil
			if code := f.visit(101); code != 2 || f.q.Head() != f.patrol || production.Target != 0 {
				t.Fatal("gap invented factory assistance")
			}
			// With construction absent, deterministic distance then handle owns repair.
			f.units = []*units.Unit{modernPatient(6, 2, 0, false), modernPatient(4, 2, 0, false), modernPatient(3, 3, 0, false)}
			f.visit(102)
			if f.q.Head().Target != 4 {
				t.Fatal("repair distance/handle tie order changed")
			}
		})
	}
}

func TestModernPatrolChoicesAndResourceCapacity(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, choice := range []PatrolWorkOption{PatrolReclaimOnly, PatrolBoth, PatrolAssistOnly} {
			for _, tc := range []struct {
				name          string
				stock         [2]float32
				metal, energy int32
				want          bool
			}{
				{"full metal", [2]float32{100, 0}, 10, 0, false},
				{"full energy", [2]float32{0, 100}, 0, 10, false},
				{"both full", [2]float32{100, 100}, 10, 10, false},
				{"metal room above twenty percent", [2]float32{99, 100}, 10, 0, true},
				{"energy room above twenty percent", [2]float32{100, 99}, 0, 10, true},
				{"mixed yield", [2]float32{100, 99}, 10, 10, true},
			} {
				t.Run(fmt.Sprintf("air%v/choice%d/%s", air, choice, tc.name), func(t *testing.T) {
					f := newModernWorkFixture(air)
					f.resources.Stock = tc.stock
					options := (&ModernRules{}).DefaultBuilderOptions()
					options.Patrol = [3]PatrolWorkOption{choice, choice, choice}
					f.q.binding.SetBuilderOptions(func(uint8) BuilderOptions { return options })
					f.features = []FeatureView{modernFeature(3, 16, 0, tc.metal, tc.energy)}
					f.features[0].DefinitionKey = "authored_tree_rock_or_metal" // identity never decides resource type.
					random, resources := f.sim, f.resources
					f.visit(100)
					want := tc.want && choice != PatrolAssistOnly
					if got := f.q.Head() != f.patrol; got != want {
						t.Fatalf("reclaim=%v want%v", got, want)
					}
					if want {
						id := rowReclaim
						if air {
							id = rowVTOLReclaim
						}
						if f.q.Head().ID != id || f.q.Head().workAssignment != f.patrol {
							t.Fatal("reclaim bypassed ordinary row or assignment")
						}
					}
					if f.resources != resources || f.sim != random {
						t.Fatal("feature selection changed ledger or RNG")
					}
				})
			}
			f := newModernWorkFixture(air)
			options := (&ModernRules{}).DefaultBuilderOptions()
			options.Patrol[0] = choice
			f.q.binding.SetBuilderOptions(func(uint8) BuilderOptions { return options })
			f.units = []*units.Unit{modernPatient(2, 32, 0, true)}
			f.features = []FeatureView{modernFeature(1, 0, 0, 10, 0)}
			f.resources.Stock[0] = 0
			f.visit(100)
			if got := f.q.Head().Target != 0; got != (choice != PatrolReclaimOnly) {
				t.Fatalf("air%v choice%d assist=%v", air, choice, got)
			}
			// Ordinary assistance energy admission still precedes either build or repair.
			f.q.primary = []*Node{f.patrol, f.next, f.successor}
			f.features = nil
			f.resources.Stock[1] = 19
			f.visit(101)
			if f.q.Head() != f.patrol {
				t.Fatal("low energy admitted assistance")
			}
		}
	}
}

func TestModernPatrolFeatureIdentityAndAuthoredPredicates(t *testing.T) {
	f := newModernWorkFixture(false)
	f.resources.Stock[0] = 99
	unreclaimable := modernFeature(1, 0, 0, 10, 0)
	unreclaimable.Reclaimable = false
	manualOnly := unreclaimable
	manualOnly.Reclaimable, manualOnly.Autoreclaimable = true, false
	zeroYield := modernFeature(2, 0, 0, 0, 0)
	f.features = []FeatureView{unreclaimable, manualOnly, zeroYield, modernFeature(9, 16, 0, 10, 0), modernFeature(3, -16, 0, 10, 0)}
	f.visit(100)
	if f.q.Head().GoalX != numeric.Fixed(-16<<16) {
		t.Fatal("feature identity tie or authored admission changed")
	}
}

func TestModernPatrolRetainsWorkingPrecisionEnergyAdmission(t *testing.T) {
	for _, air := range []bool{false, true} {
		f := newModernWorkFixture(air)
		f.units = []*units.Unit{modernPatient(2, 32, 0, true)}
		f.resources.Stock[1], f.resources.Capacity[1] = 40.6, 203
		before := f.sim
		f.visit(100)
		if f.q.Head() != f.patrol {
			t.Fatal("stored stock below the patrol working threshold admitted work")
		}
		f.resources.Stock[1] = math.Nextafter32(f.resources.Stock[1], float32(math.Inf(1)))
		f.visit(101)
		if f.q.Head() == f.patrol || f.sim != before {
			t.Fatal("next stored stock did not admit work without a selection draw")
		}
	}
}

func TestModernWorkCircleFixedBoundary(t *testing.T) {
	for _, tc := range []struct {
		ax, az, x, z numeric.Fixed
		radius       int32
		want         bool
	}{
		{0, 0, 128 << 16, 0, 128, true}, {0, 0, 128 << 16, 1, 128, false},
		{0, 0, 60 << 16, 80 << 16, 100, true}, {0, 0, 60 << 16, 80<<16 + 1, 100, false},
		{numeric.Fixed(math.MinInt32), numeric.Fixed(math.MinInt32), numeric.Fixed(math.MaxInt32), numeric.Fixed(math.MaxInt32), 128, false},
	} {
		if got := modernWithinPoint(tc.ax, tc.az, tc.x, tc.z, tc.radius); got != tc.want {
			t.Fatalf("circle=%v want %v for %+v", got, tc.want, tc)
		}
	}
}

func TestModernRestoredWorkKeepsUnknownProvenanceAndRecoversPatrolRoute(t *testing.T) {
	f := newModernWorkFixture(false)
	patient := modernPatient(2, 300, 1000, false)
	f.units = []*units.Unit{patient}
	work := newNode(rowRepairUnit, NewNodeForOrder(rowRepairUnit, patient.Handle, patient.X, 0, patient.Z, 100, f.u.Handle, false))
	work.workAssignment, work.automaticWork = f.patrol, true
	f.q.primary = append([]*Node{work}, f.q.primary...)
	ids := map[pool.Handle]uint16{f.u.Handle: 1, patient.Handle: 2}
	images, err := RetailOrderImagesWithPayload(f.u, func(h pool.Handle) (uint16, bool) { id, ok := ids[h]; return id, ok }, func(pool.Handle) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]save.OrderRecord, len(images))
	for i, image := range images {
		records[i] = save.OrderRecord{ParentStableID: 1, Sequence: uint32(i), Main: image.Main, DescriptorName: image.DescriptorName, SubtypeCode: image.SubtypeCode, Subtype: image.Subtype}
	}
	if err := RetailRestoreOrdersAtTick(f.u, records, map[uint16]pool.Handle{1: f.u.Handle, 2: patient.Handle}, f.q.binding, 101); err != nil {
		t.Fatal(err)
	}
	q := QueueOfUnit(f.u)
	if q.Head().workAssignment != nil || q.Head().automaticWork || !q.binding.rules().AutomaticWorkValid(f.u, q.Head()) {
		t.Fatal("restore inferred an automatic producer from queue adjacency")
	}
	q.RemoveHead()
	f.q, f.patrol, f.next, f.successor = q, q.primary[0], q.primary[1], q.primary[2]
	patient.Z = numeric.Fixed(128 << 16)
	f.visit(200)
	if q.Head().workAssignment != f.patrol {
		t.Fatal("restored patrol did not bind its newly selected job")
	}
}

func TestModernGuardBorrowedWorkTracksWardButDirectAssistanceDoesNot(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, resurrect := range []bool{false, true} {
			f := newGuardFixture(t, 1, 1)
			q := QueueOfUnit(f.guard)
			b := q.binding
			b.Rules = &ModernRules{}
			f.guard.Def.Builder, f.guard.Def.CanReclamate, f.guard.Def.CanResurrect, f.guard.Def.CanFly = true, true, resurrect, air
			f.guard.Def.BMCode, f.guard.Def.SightDistance = 1, 1024
			f.ward.X, f.ward.Z = f.guard.X, f.guard.Z
			patient := modernPatient(3, int32(f.ward.X.Raw()>>16)+128, int32(f.ward.Z.Raw()>>16), false)
			b.SetLookup(func(h pool.Handle) *units.Unit {
				if h == patient.Handle {
					return patient
				}
				if h == f.ward.Handle {
					return f.ward
				}
				return nil
			})
			feature := FeatureView{X: patient.X, Z: patient.Z, Reclaimable: true}
			b.World = NewWorldQueryAdapter(WorldQueryAdapterConfig{
				ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
					if !resurrect {
						visit(patient.Handle, patient)
					}
				},
				ForEachFeature: func(visit func(FeatureView) bool) { visit(feature) },
			})
			b.Work = NewWorkAdapter(WorkAdapterConfig{CanResurrectFeature: func(FeatureView) bool { return true }})
			b.SetResources(func(uint8) (ResourceView, bool) {
				return ResourceView{Stock: [2]float32{100, 100}, Capacity: [2]float32{100, 100}}, true
			})
			n := guardNode(f)
			n.Phase = 1
			if air {
				n.ID, n.Phase = rowVTOLFollow, 2
			}
			successor := &Node{ID: rowMoveGround, Owner: f.guard.Handle}
			q.primary = []*Node{n, successor}
			if !b.rules().GuardWorksNearby(f.guard, n, 100) || q.Head().workAssignment != n || !b.rules().AutomaticWorkValid(f.guard, q.Head()) {
				t.Fatal("guard boundary did not select bounded work")
			}
			work := q.Head()
			if !resurrect {
				patient.X += 1
				if b.rules().AutomaticWorkValid(f.guard, work) {
					t.Fatal("guard followed moving target outside ward circle")
				}
				patient.X -= 1
			}
			work.DynamicGate, work.Deadline = gateBuildStance, 1000
			before := *f.sim
			// The ward's moving centre shrinks this side of the work circle.
			f.ward.X -= 1
			q.Pump(f.guard, 101)
			if q.Head() != n || len(q.primary) != 2 || q.primary[1] != successor || *f.sim != before {
				t.Fatal("borrowed guard work ignored moving ward or changed successors/RNG")
			}
			// Outside candidates are refused at selection, even when in sight.
			if b.rules().GuardWorksNearby(f.guard, n, 102) {
				t.Fatal("guard selected work outside ward circle")
			}
			// Direct ward assistance retains the ordinary contract; queue adjacency
			// alone is no producer receipt, also for a copied factory product.
			for _, target := range []pool.Handle{f.ward.Handle, patient.Handle} {
				direct := &Node{ID: rowHelpBuild, Target: target, Owner: f.guard.Handle, automaticWork: true}
				q.primary = []*Node{direct, n, successor}
				if !b.rules().AutomaticWorkValid(f.guard, direct) {
					t.Fatal("leash changed direct ward/production assistance")
				}
			}
			// A revived patient's repair stays under the same borrowed assignment.
			work.Target = patient.Handle
			q.primary = []*Node{work, n, successor}
			spawnResurrectionRepair(f.guard, work)
			if q.Head().workAssignment != n {
				t.Fatal("resurrection repair dropped borrowed assignment")
			}
		}
	}
}

func TestModernPatrolSightBoundary(t *testing.T) {
	for _, air := range []bool{false, true} {
		f := newModernWorkFixture(air)
		f.u.Def.SightDistance = 128
		patient := modernPatient(2, 128, 0, true)
		f.units = []*units.Unit{patient}
		f.visit(100)
		if f.q.Head() == f.patrol {
			t.Fatal("sight equality refused patrol work")
		}
		f.q.primary = []*Node{f.patrol, f.next, f.successor}
		patient.X += 1
		f.visit(101)
		if f.q.Head() != f.patrol {
			t.Fatal("patrol work exceeded sight by a fractional unit")
		}
	}
}

func TestModernGuardDirectWardAndProductionRemainUnbounded(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, production := range []bool{false, true} {
			f := newGuardFixture(t, 1, 1)
			f.guard.Def.Builder, f.guard.Def.CanReclamate, f.guard.Def.BMCode, f.guard.Def.CanFly = true, true, 1, air
			q := QueueOfUnit(f.guard)
			q.binding.Rules = &ModernRules{}
			product := modernPatient(3, 1000, 1000, true)
			lookup := q.binding.LookupHook()
			q.binding.SetLookup(func(h pool.Handle) *units.Unit {
				if h == product.Handle {
					return product
				}
				return lookup(h)
			})
			wantTarget := f.ward.Handle
			if production {
				f.ward.Def.Builder = true
				QueueOfUnit(f.ward).Push(rowBuildingBuild, Node{Owner: f.ward.Handle, Target: product.Handle})
				QueueOfUnit(f.ward).Head().BindTarget(product.Handle)
				wantTarget = product.Handle
			} else {
				f.ward.Health = 50
			}
			n := guardNode(f)
			n.Phase = 1
			if air {
				n.ID, n.Phase = rowVTOLFollow, 2
			}
			q.primary = []*Node{n}
			if code := guardHandler(f.guard, n, 0, 100); code != 3 || q.Head() == n || q.Head().Target != wantTarget || q.Head().workAssignment != nil || !q.binding.rules().AutomaticWorkValid(f.guard, q.Head()) {
				t.Fatalf("air%v production%v direct assistance changed", air, production)
			}
		}
	}
}

func TestModernWorkAssignmentCaptureUsesDetachedQueueOrdinals(t *testing.T) {
	f := newModernWorkFixture(false)
	f.units = []*units.Unit{modernPatient(2, 32, 0, true)}
	f.visit(100)
	before := f.sim
	d := f.q.DebugSnapshot(f.u.Handle)
	if len(d.WorkAssignments) != 2 || d.WorkAssignments[0] != (DebugWorkAssignment{1, 3}) || d.WorkAssignments[1] != (DebugWorkAssignment{2, 3}) || f.sim != before {
		t.Fatal("capture omitted assignment or changed RNG")
	}
	work := f.q.Head()
	if f.q.WorkAssignmentOrdinal(work) != 3 || f.q.WorkAssignmentOrdinal(f.patrol) != 0 {
		t.Fatal("assignment fingerprint did not use a retained ordinal")
	}
	f.q.primary = []*Node{work, f.next, f.patrol, f.successor}
	if f.q.WorkAssignmentOrdinal(work) != 3 || d.WorkAssignments[0] != (DebugWorkAssignment{1, 3}) {
		t.Fatal("capture aliases live queue bookkeeping")
	}
	f.q.primary = []*Node{work, f.next, f.successor}
	if f.q.WorkAssignmentOrdinal(work) != -1 {
		t.Fatal("missing assignment collapsed into unknown provenance")
	}
	for _, rules := range []Rules{StrictRules{}, &CommunityRules{}} {
		q := NewQueueWith([]*Node{{ID: rowRepairUnit}}, nil)
		q.binding = &QueueBinding{Rules: rules}
		if q.WorkAssignmentOrdinal(q.Head()) != 0 || q.DebugSnapshot(1).WorkAssignments != nil {
			t.Fatal("ordinary strict/community queue gained assignment state")
		}
	}
}
