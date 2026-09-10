package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func carriedVisitFixture(t *testing.T, carrierFirst bool) (*System, *units.World, *units.Unit, *units.Unit) {
	t.Helper()
	s := NewSystem(syntheticTerrainForIntegrate(), Template(), NewOccupancyGrid())
	w := newMovementFixtureWorld(8)
	def := &content.UnitDef{UnitName: "visit", BMCode: 1, FootprintX: 1, FootprintZ: 1, MaxVelocity: 1 << 16, Acceleration: 1 << 16, BrakeRate: 1 << 16, TurnRate: 1 << 14}
	createCargo := func() pool.Handle {
		h, err := w.Create(def, 0, world.CellToWorld(2), 0, world.CellToWorld(2))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	createCarrier := func() pool.Handle {
		h, err := w.Create(def, 0, world.CellToWorld(6), 0, world.CellToWorld(6))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	var cargoHandle, carrierHandle pool.Handle
	if carrierFirst {
		carrierHandle, cargoHandle = createCarrier(), createCargo()
	} else {
		cargoHandle, carrierHandle = createCargo(), createCarrier()
	}
	s.BindWorld(w)
	s.EnsureUnit(w.Unit(cargoHandle))
	s.EnsureUnit(w.Unit(carrierHandle))
	if !AttachCargoMode(w, carrierHandle, cargoHandle, -1, 1) {
		t.Fatal("attach cargo")
	}
	return s, w, w.Unit(cargoHandle), w.Unit(carrierHandle)
}

func installCarrierRoute(s *System, carrier *units.Unit) {
	q := orders.QueueForUnit(carrier)
	q.Push(orders.Lookup("Move_Ground"), orders.Node{GoalX: world.CellToWorld(20), GoalZ: carrier.Z})
	setHandleRow(&s.Routes, carrier.Handle, &Route{Active: true, Count: 2, Points: [20]Point{
		{X: int32(carrier.X.Raw() >> 16), Z: int32(carrier.Z.Raw() >> 16)},
		{X: 320, Z: int32(carrier.Z.Raw() >> 16)},
	}})
}

func visitMovementTick(s *System, w *units.World, tick uint32, observe func(*units.Unit)) {
	s.BeginTick(tick)
	w.VisitActiveSlots(func(v units.SlotVisit) {
		s.StepUnit(v.Handle, tick)
		if observe != nil {
			observe(v.Unit)
		}
	})
	s.EndTick(tick)
}

func TestCarriedCommitOccursAtCargoVisit(t *testing.T) {
	t.Run("cargo before carrier has no tail overwrite", func(t *testing.T) {
		s, w, cargo, carrier := carriedVisitFixture(t, false)
		if cargo.Handle >= carrier.Handle {
			t.Fatal("fixture requires cargo before carrier")
		}
		installCarrierRoute(s, carrier)
		carrierBefore := carrier.X
		var atCargoVisit numeric.Fixed
		visitMovementTick(s, w, 1, func(u *units.Unit) {
			if u == cargo {
				atCargoVisit = cargo.X
			}
		})
		if carrier.X == carrierBefore {
			t.Fatal("fixture carrier did not advance through its mover visit")
		}
		if atCargoVisit != carrierBefore {
			t.Fatalf("cargo visit pose %d, want prior carrier pose %d", atCargoVisit, carrierBefore)
		}
		if cargo.X != atCargoVisit {
			t.Fatalf("tail overwrote cargo pose: at visit %d, end %d", atCargoVisit, cargo.X)
		}
	})

	t.Run("carrier before cargo publishes to later observer and occupancy", func(t *testing.T) {
		s, w, cargo, carrier := carriedVisitFixture(t, true)
		if carrier.Handle >= cargo.Handle {
			t.Fatal("fixture requires carrier before cargo")
		}
		// Use the air word for this observation so the cargo's carried stamp does
		// not share the carrier's grounded rectangle.
		cargo.Move.Mode = 2
		observerHandle, err := w.Create(cargo.Def, 0, world.CellToWorld(12), 0, world.CellToWorld(12))
		if err != nil {
			t.Fatal(err)
		}
		s.EnsureUnit(w.Unit(observerHandle))
		installCarrierRoute(s, carrier)
		carrierBefore := carrier.X
		var observed numeric.Fixed
		visitMovementTick(s, w, 1, func(u *units.Unit) {
			if u.Handle != observerHandle {
				return
			}
			observed = cargo.X
			anchor := handleRow(s.Collisions, cargo.Handle).CachedAnchor
			if got, ok := s.Grid.OccupantAtPlane(PlaneAir, anchor); !ok || got != handleRow(s.Collisions, cargo.Handle).ID {
				t.Fatalf("later observer sees cargo occupancy %d/%t at %+v", got, ok, anchor)
			}
		})
		if carrier.X == carrierBefore {
			t.Fatal("fixture carrier did not advance through its mover visit")
		}
		if observed != carrier.X || cargo.X != carrier.X {
			t.Fatalf("cargo visit did not publish advanced carrier pose: cargo %d, observer %d, carrier %d", cargo.X, observed, carrier.X)
		}
	})
}

func TestAttachmentChangesAfterBeginTickUseLiveMembership(t *testing.T) {
	t.Run("attach", func(t *testing.T) {
		s, w, cargo, carrier := carriedVisitFixture(t, true)
		if _, ok := DetachCargo(w, cargo.Handle); !ok {
			t.Fatal("prepare unattached cargo")
		}
		setHandleRow(&s.Routes, cargo.Handle, &Route{Active: true, Count: 2, Points: [20]Point{{X: 32, Z: 32}, {X: 64, Z: 32}}})
		s.BeginTick(1)
		if !AttachCargoMode(w, carrier.Handle, cargo.Handle, -1, 1) {
			t.Fatal("attach after BeginTick")
		}
		w.VisitActiveSlots(func(v units.SlotVisit) { s.StepUnit(v.Handle, 1) })
		s.EndTick(1)
		if route := handleRow(s.Routes, cargo.Handle); route != nil && route.Active {
			t.Fatal("live attach did not take carried branch")
		}
		if cargo.X != carrier.X {
			t.Fatalf("live attach cargo pose %d, want carrier pose %d", cargo.X, carrier.X)
		}
	})

	for _, tt := range []struct {
		name   string
		detach func(*units.World, *units.Unit) bool
	}{
		{name: "factory", detach: func(w *units.World, cargo *units.Unit) bool {
			_, ok := DetachFactoryProduct(w, cargo.Handle)
			return ok
		}},
		{name: "transport", detach: func(w *units.World, cargo *units.Unit) bool { _, ok := DetachCargo(w, cargo.Handle); return ok }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, w, cargo, carrier := carriedVisitFixture(t, false)
			if tt.name == "factory" {
				if _, ok := DetachCargo(w, cargo.Handle); !ok || !AttachFactoryProduct(w, carrier.Handle, cargo.Handle, -1) {
					t.Fatal("install factory product link")
				}
			}
			q := orders.QueueForUnit(cargo)
			q.Push(orders.Lookup("Move_Ground"), orders.Node{GoalX: world.CellToWorld(12), GoalZ: world.CellToWorld(2)})
			setHandleRow(&s.Routes, cargo.Handle, &Route{Active: true, Count: 2, Points: [20]Point{{X: 32, Z: 32}, {X: 64, Z: 32}}})
			s.BeginTick(1)
			if !tt.detach(w, cargo) {
				t.Fatal("detach after BeginTick")
			}
			w.VisitActiveSlots(func(v units.SlotVisit) { s.StepUnit(v.Handle, 1) })
			s.EndTick(1)
			if route := handleRow(s.Routes, cargo.Handle); route == nil || !route.Active {
				t.Fatal("stale carried state cleared the free mover route")
			}
		})
	}
}

func TestAnnulusGoalPointUsesSingleSharedBiasAtBoundaries(t *testing.T) {
	goal := path.AnnulusGoal(path.Cell{X: 20, Z: 20}, 128, 384)
	center := numeric.Fixed(328 << 16)
	const componentRounding int64 = 1 << 12
	for _, tt := range []struct {
		name        string
		dx, dz      int64
		wantBearing numeric.Angle
	}{
		{name: "residue 63", dx: 1, dz: 165, wantBearing: 63},
		{name: "residue 64", dx: 1, dz: 162, wantBearing: 64},
		{name: "residue 95", dx: 1, dz: 110, wantBearing: 95},
		{name: "residue 96", dx: 1, dz: 109, wantBearing: 96},
		{name: "wrap 65503", dx: -1, dz: 316, wantBearing: 65503},
		{name: "wrap 65504", dx: -1, dz: 326, wantBearing: 65504},
	} {
		dx, dz := tt.dx, tt.dz
		bearing := numeric.AngleFromAtan2(dx, dz)
		if bearing != tt.wantBearing {
			t.Fatalf("%s bearing %d, want %d", tt.name, bearing, tt.wantBearing)
		}
		u := &units.Unit{X: center + numeric.Fixed(dx<<16), Z: center + numeric.Fixed(dz<<16)}
		gotX, gotZ, ok := groundGoalPoint(goal, u, 1, 1)
		if !ok {
			t.Fatal("annulus declined goal point")
		}
		radius := int64(256 << 16)
		wantX := center + numeric.Fixed((radius*int64(numeric.Sin(bearing))+componentRounding)>>13)
		wantZ := center + numeric.Fixed((radius*int64(numeric.Cos(bearing))+componentRounding)>>13)
		if gotX != wantX || gotZ != wantZ {
			t.Fatalf("%s: got (%d,%d), want shared-bias (%d,%d)", tt.name, gotX, gotZ, wantX, wantZ)
		}
	}
}

func TestHoverBobComponentUsesSingleSharedBias(t *testing.T) {
	bob := hoverBob{counter: 0, amp: 1 << 16, phase: 81}
	angle := numeric.Angle(uint16(81))
	if got, want := bob.component(0), numeric.MulRound(numeric.Sin(angle), bob.amp); got != want {
		t.Fatalf("hover component %d, want shared-bias result %d", got, want)
	}
}
