//go:build retail

package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These scenes exercise the approved Nanolathe Modern policy in
// DESIGN_UNITS_ORDERS_COB "Constructor patrol audit — 2026-10-09". The stock
// scripts, movement, resource admission and work executors run on session ticks.
// Only initial fixture state is staged; no observer changes a work verdict,
// build stance, flight state or remaining fraction once the scene starts.
func TestModernConstructorPatrolLateWorkRetail(t *testing.T) {
	for _, key := range []string{"armcv", "armca"} {
		for stance := uint32(0); stance < 3; stance++ {
			t.Run(fmt.Sprintf("%s/stance=%d", key, stance), func(t *testing.T) {
				f := newConstructorPatrolRuntime(t, key, stance, nil)
				target := f.lateNanoframe(160)
				paid := f.observeWork(target)
				f.finishAndResume(target, paid)
			})
		}
	}
}

func TestModernConstructorPatrolPaidRepairRetail(t *testing.T) {
	for _, key := range []string{"armcv", "armca"} {
		t.Run(key, func(t *testing.T) {
			// The saved preference is deliberately different in each stance;
			// stance 2 must read Assist, including in a full-storage battle.
			options := orders.BuilderOptions{Patrol: [3]orders.PatrolWorkOption{
				orders.PatrolReclaimOnly, orders.PatrolBoth, orders.PatrolAssistOnly,
			}}
			f := newConstructorPatrolRuntime(t, key, 2, &options)
			x, z := f.helper.X+numeric.FixedFromInt(40), f.helper.Z-numeric.FixedFromInt(160)
			target := placeCompleteRetailUnit(t, f.s, "armsolar", 0, x, z)
			target.Health = target.Def.MaxDamage - 12
			f.s.Movement.EnsureUnit(target)
			paid := f.observeWork(target)
			f.finishAndResume(target, paid)
			if paid.repair == 0 || paid.energy <= 0 {
				t.Fatalf("no paid repair: %+v", paid)
			}
		})
	}
}

type constructorPatrolPoint struct{ x, y, z numeric.Fixed }

func constructorPatrolPosition(u *units.Unit) constructorPatrolPoint {
	return constructorPatrolPoint{u.X, u.Y, u.Z}
}

func (p constructorPatrolPoint) distanceSquared(q constructorPatrolPoint) int64 {
	dx, dz := int64(p.x-q.x)>>16, int64(p.z-q.z)>>16
	return dx*dx + dz*dz
}

func constructorPatrolName(n *orders.Node) string {
	if n == nil {
		return "<nil>"
	}
	return orders.DescriptorFor(n.ID).Name
}

type constructorPatrolRuntime struct {
	t              *testing.T
	s              *Session
	helper         *units.Unit
	q              *orders.Queue
	driver         int32
	route          []*orders.Node
	goals          []constructorPatrolPoint
	successor      *orders.Node
	patrol         string
	energyStock    float32
	metalStock     float32
	acquired       constructorPatrolPoint
	acquiredTarget pool.Handle
	acquiredTick   uint32
}

func newConstructorPatrolRuntime(t *testing.T, key string, stance uint32, options *orders.BuilderOptions) *constructorPatrolRuntime {
	t.Helper()
	fixture := loadRetailFixture(t)
	fixture.cfg.Gameplay = gameplay.Modern
	s, err := NewSkirmishWithEntryOptions(fixture.fs, fixture.cat, fixture.cfg, SkirmishEntryOptions{BuilderOptions: options})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateComposition(); err != nil {
		t.Fatal(err)
	}
	f := &constructorPatrolRuntime{t: t, s: s, patrol: "RepairPatrol", energyStock: 100000, metalStock: 100000}
	for i := 0; i < 5; i++ {
		f.step()
	}
	at := numeric.FixedFromInt
	f.helper = placeCompleteRetailUnit(t, s, key, 0, at(600), at(600))
	f.helper.Flags = f.helper.Flags&^(units.StandingFieldMask<<units.StandingMoveShift) | stance<<units.StandingMoveShift
	s.Movement.EnsureUnit(f.helper)
	s.bindOrderQueue(f.helper)
	f.q = orders.QueueForUnit(f.helper)
	f.q.CancelAll()
	if f.helper.Def.CanFly {
		f.patrol = "VTOL_RepairPatrol"
		s.Movement.SetMoverMode(f.helper, 2)
		if !s.Movement.PlaceUnit(orders.PlaceRequest{Unit: f.helper.Handle, X: f.helper.X, Y: f.helper.Y + at(int64(f.helper.Def.CruiseAlt)), Z: f.helper.Z}) {
			t.Fatal("fixture: airborne constructor placement failed")
		}
	}
	f.order(f.helper, 9, 0, at(1800), at(600), false)
	for i := 0; i < 600; i++ {
		f.step()
		if f.helper.X >= at(760) && constructorPatrolName(f.q.Head()) == f.patrol {
			break
		}
	}
	if f.helper.X < at(760) || constructorPatrolName(f.q.Head()) != f.patrol {
		t.Fatalf("fixture: no established moving patrol: tick=%d pos=(%d,%d) head=%s", s.Clock.GlobalTick, f.helper.X.Floor(), f.helper.Z.Floor(), constructorPatrolName(f.q.Head()))
	}
	for _, n := range f.q.Primary() {
		if constructorPatrolName(n) == f.patrol {
			f.route = append(f.route, n)
			f.goals = append(f.goals, constructorPatrolPoint{n.GoalX, n.GoalY, n.GoalZ})
		}
	}
	if len(f.route) != 2 {
		t.Fatalf("fixture: expected waypoint and return-to-start patrol rows, got %d", len(f.route))
	}
	// Preserve an ordinary queued successor as well as both patrol waypoints.
	// It stays behind the current leg during the observation window.
	f.order(f.helper, 2, 0, at(1800), at(900), true)
	f.step()
	for _, n := range f.q.Primary() {
		if constructorPatrolName(n) == "Move_Ground" || constructorPatrolName(n) == "VTOL_Move" {
			f.successor = n
		}
	}
	if f.successor == nil {
		t.Fatal("fixture: queued successor absent")
	}
	t.Logf("moving patrol staged at tick=%d pos=(%d,%d) sight=%d", s.Clock.GlobalTick, f.helper.X.Floor(), f.helper.Z.Floor(), f.helper.Def.SightDistance)
	return f
}

func (f *constructorPatrolRuntime) step() {
	f.t.Helper()
	p := &f.s.Econ.Players[0]
	p.Capacity = [2]float32{100000, 100000}
	p.Stock = [2]float32{f.metalStock, f.energyStock}
	var position constructorPatrolPoint
	wasPatrolling := f.q != nil && constructorPatrolName(f.q.Head()) == f.patrol
	if f.helper != nil {
		position = constructorPatrolPosition(f.helper)
	}
	f.driver++
	f.s.Step(f.driver)
	if wasPatrolling && f.q.Head() != nil && (f.q.Head().Target != 0 || constructorPatrolName(f.q.Head()) == "Reclaim" || constructorPatrolName(f.q.Head()) == "VTOL_Reclaim") {
		f.acquired, f.acquiredTarget, f.acquiredTick = position, f.q.Head().Target, f.s.Clock.GlobalTick
	}
	if f.s.State != StateBattle {
		f.t.Fatalf("fixture: battle ended at tick %d", f.s.Clock.GlobalTick)
	}
}

func (f *constructorPatrolRuntime) order(u *units.Unit, code int, target pool.Handle, x, z numeric.Fixed, queued bool) {
	f.t.Helper()
	if err := f.s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
		Handles: []pool.Handle{u.Handle}, Code: code, Target: target, Queued: queued, AssignedPosition: true,
		Position: orders.ResolvePos{X: x, Y: f.s.World.HeightAt(x, z), Z: z, InterfaceType: orders.InterfaceTypeRightClick},
	}}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *constructorPatrolRuntime) lateNanoframe(offset int64) *units.Unit {
	f.t.Helper()
	x, z := f.helper.X+numeric.FixedFromInt(40), f.helper.Z-numeric.FixedFromInt(offset)
	def, ok := f.s.Catalog.Unit("armsolar")
	if !ok {
		f.t.Fatal("fixture: armsolar absent")
	}
	h, err := f.s.Units.CreateNanoframe(def, 0, x, f.s.World.HeightAt(x, z), z)
	if err != nil {
		f.t.Fatal(err)
	}
	u := f.s.Units.Unit(h)
	// Fifty or so ordinary stock work quanta finish this staged frame. The
	// construction service still owns every subsequent fraction and health write.
	u.Remaining = .02
	u.Health = int32(float32(def.MaxDamage) * (1 - u.Remaining))
	f.s.Movement.EnsureUnit(u)
	if constructorPatrolPosition(f.helper).distanceSquared(constructorPatrolPosition(u)) > int64(f.helper.Def.SightDistance)*int64(f.helper.Def.SightDistance) {
		f.t.Fatal("fixture: late frame is outside current authored sight radius")
	}
	return u
}

type constructorPatrolPayment struct {
	assist, repair, carried, refused int
	energy, metal                    float32
}

func (f *constructorPatrolRuntime) observeWork(target *units.Unit) *constructorPatrolPayment {
	paid := &constructorPatrolPayment{}
	work := f.q.Binding().Work
	assist, repair := work.AssistHook(), work.RepairHook()
	observe := func(builder *units.Unit, before [2]economy.Bucket) {
		after := f.s.Econ.UnitBuckets(builder.Handle)
		paid.metal += after[economy.Metal].Accepted - before[economy.Metal].Accepted
		paid.energy += after[economy.Energy].Accepted - before[economy.Energy].Accepted
	}
	work.SetAssist(func(builder *units.Unit, n *orders.Node, tick uint32) bool {
		before := *f.s.Econ.UnitBuckets(builder.Handle)
		fraction := target.Remaining
		result := assist(builder, n, tick)
		if builder == f.helper && n.Target == target.Handle && !result {
			paid.refused++
		}
		if builder == f.helper && n.Target == target.Handle && target.Remaining < fraction {
			paid.assist++
			observe(builder, before)
			if target.Attachment.Carrier != 0 {
				paid.carried++
			}
		}
		return result
	})
	work.SetRepair(func(builder, patient *units.Unit, n *orders.Node, tick uint32) bool {
		before := *f.s.Econ.UnitBuckets(builder.Handle)
		health := patient.Health
		result := repair(builder, patient, n, tick)
		if builder == f.helper && patient == target && patient.Health > health {
			paid.repair++
			observe(builder, before)
		}
		return result
	})
	return paid
}

func (f *constructorPatrolRuntime) assertRetained() {
	f.t.Helper()
	for i, n := range f.route {
		found := false
		for _, current := range f.q.Primary() {
			found = found || current == n
		}
		if !found || (constructorPatrolPoint{n.GoalX, n.GoalY, n.GoalZ}) != f.goals[i] {
			f.t.Fatalf("patrol waypoint %d lost or rewritten at tick %d", i, f.s.Clock.GlobalTick)
		}
	}
	found := false
	for _, current := range f.q.Primary() {
		found = found || current == f.successor
	}
	if !found {
		f.t.Fatalf("queued successor lost at tick %d", f.s.Clock.GlobalTick)
	}
}

func (f *constructorPatrolRuntime) finishAndResume(target *units.Unit, paid *constructorPatrolPayment) {
	f.t.Helper()
	construction := target.Remaining != 0
	f.finishCycle(func(n *orders.Node) bool { return n.Target == target.Handle }, func() bool { return target.Remaining == 0 && (construction || target.Health >= target.Def.MaxDamage) })
	if paid.assist+paid.repair == 0 || paid.energy <= 0 || paid.assist != 0 && paid.metal <= 0 {
		f.t.Fatalf("work completed without ordinary payment: %+v", paid)
	}
	f.t.Logf("paid contributions: %+v", paid)
}

func (f *constructorPatrolRuntime) finishCycle(matches func(*orders.Node) bool, done func() bool) {
	f.t.Helper()
	var acquired, returned, resumed constructorPatrolPoint
	var selected, completed, returnSeen, returnArrived, travelled uint32
	var returnNode *orders.Node
	for i := 0; i < 1800; i++ {
		before := constructorPatrolPosition(f.helper)
		beforeHead := f.q.Head()
		f.step()
		f.assertRetained()
		head := f.q.Head()
		if selected == 0 && head != nil && matches(head) {
			selected, acquired = f.s.Clock.GlobalTick, before
			if f.acquiredTick != 0 {
				selected, acquired = f.acquiredTick, f.acquired
			}
		}
		if selected != 0 && done() && completed == 0 {
			completed = f.s.Clock.GlobalTick
		}
		for _, n := range f.q.Primary() {
			name := constructorPatrolName(n)
			if n != f.successor && (name == "Move_Ground" || name == "VTOL_Move") && returnNode == nil {
				returnNode, returnSeen = n, f.s.Clock.GlobalTick
				returned = constructorPatrolPoint{n.GoalX, n.GoalY, n.GoalZ}
				// Ordinary aircraft moves snap X/Z to the mover footprint; allow
				// that cell conversion, while rejecting a waypoint or side-job goal.
				if returned.distanceSquared(acquired) > 24*24 || int64(returned.y-acquired.y) > 24<<16 || int64(acquired.y-returned.y) > 24<<16 {
					f.t.Fatalf("return goal is not acquisition position: acquired=%+v goal=%+v", acquired, returned)
				}
			}
		}
		if returnNode != nil && (constructorPatrolPoint{returnNode.GoalX, returnNode.GoalY, returnNode.GoalZ}) != returned {
			f.t.Fatalf("saved-position return goal changed at tick %d: saved=%+v current=%+v", f.s.Clock.GlobalTick, returned, constructorPatrolPoint{returnNode.GoalX, returnNode.GoalY, returnNode.GoalZ})
		}
		if completed != 0 && returnSeen != 0 && (head == returnNode || beforeHead == returnNode) && constructorPatrolPosition(f.helper).distanceSquared(returned) <= 24*24 {
			returnArrived = f.s.Clock.GlobalTick
		}
		if completed != 0 && head != nil && constructorPatrolName(head) == f.patrol {
			if returnArrived == 0 {
				f.t.Fatalf("patrol resumed without actual saved-position return: selected=%d completed=%d returnSeen=%d", selected, completed, returnSeen)
			}
			if resumed == (constructorPatrolPoint{}) {
				resumed = constructorPatrolPosition(f.helper)
			}
			if resumed.distanceSquared(constructorPatrolPosition(f.helper)) >= 64*64 {
				travelled = f.s.Clock.GlobalTick
				break
			}
		}
		if i == 399 && selected == 0 {
			break
		}
	}
	if selected == 0 || completed == 0 || returnSeen == 0 || returnArrived == 0 || travelled == 0 {
		f.t.Fatalf("no completed work/return/travel cycle: selected=%d completed=%d returnSeen=%d returnArrived=%d travelled=%d head=%s pos=(%d,%d)", selected, completed, returnSeen, returnArrived, travelled, constructorPatrolName(f.q.Head()), f.helper.X.Floor(), f.helper.Z.Floor())
	}
	f.t.Logf("selected=%d completed=%d return=%d arrival=%d actualRouteTravel=%d", selected, completed, returnSeen, returnArrived, travelled)
}

func TestModernConstructorPatrolSavedReclaimChoiceRetail(t *testing.T) {
	for _, key := range []string{"armcv", "armca"} {
		t.Run(key, func(t *testing.T) {
			options := orders.BuilderOptions{Patrol: [3]orders.PatrolWorkOption{
				orders.PatrolAssistOnly, orders.PatrolReclaimOnly, orders.PatrolBoth,
			}}
			f := newConstructorPatrolRuntime(t, key, 1, &options)
			target := f.lateNanoframe(160)
			paid := f.observeWork(target)
			for i := 0; i < 180; i++ {
				f.step()
				f.assertRetained()
				if f.q.Head() != nil && f.q.Head().Target == target.Handle {
					t.Fatal("saved Reclaim-only choice borrowed construction")
				}
			}
			if paid.assist != 0 || paid.repair != 0 || target.Remaining < .02 {
				t.Fatalf("saved Reclaim-only choice delivered assistance: %+v remaining=%g", paid, target.Remaining)
			}
		})
	}
}

func TestModernConstructorPatrolPaymentRefusalRetail(t *testing.T) {
	for _, key := range []string{"armcv", "armca"} {
		t.Run(key, func(t *testing.T) {
			f := newConstructorPatrolRuntime(t, key, 2, nil)
			target := f.lateNanoframe(160)
			paid := f.observeWork(target)
			f.waitForBorrow(target)
			for i := 0; i < 500 && paid.assist == 0; i++ {
				f.step()
			}
			if paid.assist == 0 {
				t.Fatal("no ordinary paid contribution before resource shortage")
			}
			// Ordinary admission reads the helper's carried energy debt. A
			// depleted player cannot settle it; restoring stock later repays it
			// through the real ledger, without editing a work verdict or frame.
			f.energyStock = 0
			f.s.Econ.UnitBuckets(f.helper.Handle)[economy.Energy].Carry = 10000
			fraction, contributions := target.Remaining, paid.assist
			for i := 0; i < 60; i++ {
				f.step()
				f.assertRetained()
				if f.q.Head() == nil || f.q.Head().Target != target.Handle {
					t.Fatal("resource refusal abandoned the borrowed job")
				}
			}
			if paid.refused == 0 || paid.assist != contributions || target.Remaining != fraction {
				t.Fatalf("shortage did not refuse ordinary payment/progress: before=%g after=%g paid=%+v", fraction, target.Remaining, paid)
			}
			f.energyStock = 100000
			f.finishAndResume(target, paid)
		})
	}
}

func TestModernConstructorPatrolMovingTargetAbandonsRetail(t *testing.T) {
	for _, key := range []string{"armcv", "armca"} {
		t.Run(key, func(t *testing.T) {
			f := newConstructorPatrolRuntime(t, key, 2, nil)
			// This initial target is inside the old corridor too, isolating
			// the acquisition-anchored pursuit rule from the selection change.
			x, z := f.helper.X+numeric.FixedFromInt(40), numeric.FixedFromInt(664)
			target := placeCompleteRetailUnit(t, f.s, "armflash", 0, x, z)
			target.Health = target.Def.MaxDamage / 2
			f.s.Movement.EnsureUnit(target)
			f.s.bindOrderQueue(target)
			orders.QueueForUnit(target).CancelAll()
			f.waitForBorrow(target)
			anchor := f.acquired
			f.order(target, 2, 0, target.X+numeric.FixedFromInt(1000), target.Z, false)
			var outside uint32
			for i := 0; i < 500; i++ {
				f.step()
				if anchor.distanceSquared(constructorPatrolPosition(target)) > int64(f.helper.Def.SightDistance)*int64(f.helper.Def.SightDistance) {
					outside = f.s.Clock.GlobalTick
					break
				}
			}
			if outside == 0 {
				t.Fatal("fixture: ordinary target move did not leave acquisition circle")
			}
			for i := 0; i < 5; i++ {
				f.step()
			}
			if f.q.Head() != nil && f.q.Head().Target == target.Handle {
				t.Fatalf("still pursuing outside acquisition circle: outside=%d now=%d", outside, f.s.Clock.GlobalTick)
			}
			// Eligible fresh work beside the saved acquisition point must not
			// restart the failure loop while returning or travelling to a waypoint.
			def, _ := f.s.Catalog.Unit("armsolar")
			h, err := f.s.Units.CreateNanoframe(def, 0, anchor.x+numeric.FixedFromInt(60), f.s.World.HeightAt(anchor.x+numeric.FixedFromInt(60), anchor.z-numeric.FixedFromInt(40)), anchor.z-numeric.FixedFromInt(40))
			if err != nil {
				t.Fatal(err)
			}
			decoy := f.s.Units.Unit(h)
			decoy.Remaining, decoy.Health = .02, int32(float32(def.MaxDamage)*.98)
			f.s.Movement.EnsureUnit(decoy)
			var returnNode *orders.Node
			var arrived, resumed, travelled bool
			var resumePosition constructorPatrolPoint
			for i := 0; i < 700; i++ {
				beforeHead := f.q.Head()
				f.step()
				f.assertRetained()
				head := f.q.Head()
				if head != nil && head.Target != 0 {
					t.Fatalf("borrowed another job before reaching a retained waypoint: %s target=%d", constructorPatrolName(head), head.Target)
				}
				for _, n := range f.q.Primary() {
					name := constructorPatrolName(n)
					if n != f.successor && (name == "Move_Ground" || name == "VTOL_Move") {
						returnNode = n
						if (constructorPatrolPoint{n.GoalX, n.GoalY, n.GoalZ}).distanceSquared(anchor) > 24*24 {
							t.Fatal("failed pursuit did not return to acquisition position")
						}
					}
				}
				arrived = arrived || returnNode != nil && (head == returnNode || beforeHead == returnNode) && constructorPatrolPosition(f.helper).distanceSquared(anchor) <= 24*24
				if constructorPatrolName(head) == f.patrol && arrived {
					if !resumed {
						resumed, resumePosition = true, constructorPatrolPosition(f.helper)
					}
					if resumePosition.distanceSquared(constructorPatrolPosition(f.helper)) >= 64*64 {
						travelled = true
						break
					}
				}
			}
			if returnNode == nil || !arrived || !travelled || decoy.Remaining < .02 {
				t.Fatalf("failed pursuit did not return and travel with work paused: return=%v arrival=%v travel=%v decoy=%g", returnNode != nil, arrived, travelled, decoy.Remaining)
			}
		})
	}
}

func (f *constructorPatrolRuntime) waitForBorrow(target *units.Unit) {
	f.t.Helper()
	for i := 0; i < 240; i++ {
		f.step()
		f.assertRetained()
		if f.q.Head() != nil && f.q.Head().Target == target.Handle {
			return
		}
	}
	f.t.Fatalf("no late work acquisition: target=%d pos=(%d,%d) head=%s", target.Handle, f.helper.X.Floor(), f.helper.Z.Floor(), constructorPatrolName(f.q.Head()))
}

func TestModernConstructorPatrolCarriedFactoryProductRetail(t *testing.T) {
	for _, key := range []string{"armcv", "armca"} {
		t.Run(key, func(t *testing.T) {
			f := newConstructorPatrolRuntime(t, key, 2, nil)
			factoryX := f.helper.X + numeric.FixedFromInt(240)
			if f.helper.Def.CanFly {
				factoryX = numeric.FixedFromInt(1200)
			}
			factory := placeCompleteRetailUnit(t, f.s, "armvp", 0, factoryX, numeric.FixedFromInt(664))
			f.s.Movement.EnsureUnit(factory)
			f.s.bindOrderQueue(factory)
			if err := f.s.EnqueueHumanCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: factory.Handle, Product: "armflash", Count: 1}}); err != nil {
				t.Fatal(err)
			}
			var first *units.Unit
			for i := 0; i < 400; i++ {
				f.step()
				head := f.q.Head()
				if head != nil && head.Target != 0 {
					product := f.s.Units.Unit(head.Target)
					if product != nil && product.Attachment.Carrier == factory.Handle && product.Remaining != 0 {
						first = product
						break
					}
				}
			}
			if first == nil {
				t.Fatal("no acquisition of the producing factory's actual carried product")
			}
			paid := f.observeWork(first)
			f.finishAndResume(first, paid)
			if paid.carried == 0 {
				t.Fatalf("no paid assistance while product was actually carried: %+v", paid)
			}
			// Start the next production batch only after real return and route
			// travel. A successful borrow may otherwise acquire the next product
			// immediately, which would make uninterrupted patrol travel an
			// invalid requirement. This gap uses ordinary factory commands.
			if err := f.s.EnqueueHumanCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: factory.Handle, Product: "armflash", Count: 3}}); err != nil {
				t.Fatal(err)
			}
			var next *units.Unit
			for i := 0; i < 1200; i++ {
				f.step()
				head := f.q.Head()
				if head != nil && head.Target != 0 && head.Target != first.Handle {
					product := f.s.Units.Unit(head.Target)
					if product != nil && product.Attachment.Carrier == factory.Handle && product.Remaining != 0 {
						next = product
						break
					}
				}
			}
			if next == nil {
				t.Fatal("patrol did not reacquire real production after a product completion/gap")
			}
			selected := f.s.Clock.GlobalTick
			second := f.observeWork(next)
			for i := 0; i < 500 && second.carried == 0; i++ {
				f.step()
			}
			if second.carried == 0 || second.energy <= 0 || second.metal <= 0 {
				t.Fatalf("next carried product received no paid assistance: %+v", second)
			}
			t.Logf("production resumed: selected=%d first=%d next=%d paid=%+v", selected, first.Handle, next.Handle, second)
		})
	}
}

func TestModernConstructorPatrolDeficitReclaimRetail(t *testing.T) {
	for _, key := range []string{"armcv", "armca"} {
		t.Run(key, func(t *testing.T) {
			options := orders.BuilderOptions{Patrol: [3]orders.PatrolWorkOption{
				orders.PatrolBoth, orders.PatrolBoth, orders.PatrolReclaimOnly,
			}}
			f := newConstructorPatrolRuntime(t, key, 2, &options)
			def := f.s.Catalog.Features["armflash_heap"]
			if def == nil || !def.Reclaimable || !def.Autoreclaimable || def.Metal <= 0 {
				t.Fatal("fixture: stock armflash_heap is not automatically reclaimable metal")
			}
			forward := int64(40)
			if f.helper.Def.CanFly {
				forward = 160
			}
			inst := f.s.Features.PlaceAtWorld(f.helper.X+numeric.FixedFromInt(forward), f.helper.Z-numeric.FixedFromInt(160), def)
			if inst == nil {
				t.Fatal("fixture: feature placement failed")
			}
			name := "Reclaim"
			if f.helper.Def.CanFly {
				name = "VTOL_Reclaim"
			}
			for i := 0; i < 20; i++ {
				f.step()
				if constructorPatrolName(f.q.Head()) == name {
					t.Fatal("full resource stores admitted automatic reclaim")
				}
			}
			f.metalStock = 99999
			var reclaimed, credited bool
			original := f.q.Binding().ReclaimFeatureHook()
			f.q.Binding().SetReclaimFeature(func(cx, cz int) (metal, energy float32, ok bool) {
				beforeDef, ax, az, found := features.FeatureAt(f.s.World, world.CellToWorld(int32(cx)), world.CellToWorld(int32(cz)))
				metal, energy, ok = original(cx, cz)
				if found && beforeDef == def && ax == inst.CX && az == inst.CZ && ok {
					reclaimed = true
				}
				return
			})
			f.finishCycle(func(n *orders.Node) bool { return constructorPatrolName(n) == name }, func() bool {
				credited = credited || reclaimed && (f.s.Econ.UnitBuckets(f.helper.Handle)[economy.Metal].Production > 0 || f.s.Econ.UnitArchived(f.helper.Handle)[economy.Metal].Production > 0)
				return credited
			})
			if !reclaimed || !credited {
				t.Fatal("ordinary feature removal did not credit the helper's real production ledger")
			}
		})
	}
}
