package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestModernGuardPadSelectionKeepsGuardAndSuccessor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modern bool
		health int32
		pads   bool
		want   bool
	}{
		{"modern hurt", true, 74, true, true},
		{"strict hurt", false, 74, true, false},
		{"threshold", true, 75, true, false},
		{"no pads", true, 74, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAirGuardFixture(t)
			q := QueueForUnit(f.guard)
			b := q.Binding()
			b.ModernGuardAssistance = tc.modern
			f.guard.Health = tc.health
			pad := &units.Unit{Handle: 3, Alive: true, Activated: true, Def: &content.UnitDef{Builder: true, IsAirBase: true}, X: f.guard.X, Z: f.guard.Z}
			carrier := &units.Unit{Handle: 4, Alive: true, Activated: true, Def: &content.UnitDef{Builder: true, IsAirBase: true, CanMove: true}, X: f.guard.X, Z: f.guard.Z}
			b.Lookup = func(h pool.Handle) *units.Unit {
				switch h {
				case f.ward.Handle:
					return f.ward
				case pad.Handle:
					return pad
				case carrier.Handle:
					return carrier
				}
				return nil
			}
			queries := 0
			b.Movement.AirBases = func(uint8) []pool.Handle {
				queries++
				if !tc.pads {
					return nil
				}
				return []pool.Handle{pad.Handle, carrier.Handle}
			}
			n := airGuardNode(f)
			n.Phase, n.Param1 = 2, 123
			rear := &Node{ID: Lookup("Move"), Owner: f.guard.Handle}
			q.primary = []*Node{n, rear}
			random := *f.sim
			code := guardHandler(f.guard, n, 0, 100)
			if tc.want {
				pick := random.Uint32n(2)
				if code != 2 || len(q.primary) != 3 || q.primary[0].ID != Lookup("VTOL_Landing") || q.primary[0].Target != []pool.Handle{pad.Handle, carrier.Handle}[pick] || q.primary[1] != n || q.primary[2] != rear || n.DynamicGate != gateDeadline || n.Deadline != 130 || n.Param1 != 123 {
					t.Fatalf("pad visit lost queue or selection: code=%d queue=%v", code, q.primary)
				}
				// Apply the real pump result: selection must retain phase and orbit.
				q.applyPrimaryResultCode(n, code, 100)
				if n.Phase != 2 || n.Param1 != 123 {
					t.Fatal("pad visit reset the guard admission")
				}
				// The landing/healing executor leaves the healed unit attached.
				f.guard.Attachment.Carrier = carrier.Handle
				f.guard.Attachment.AttachPiece = 2
				carrier.Attachment.Cargo = []pool.Handle{f.guard.Handle}
				f.guard.Move.Mode, f.guard.Move.ModeMirror = 0, 0
				q.primary = q.primary[1:]
				f.guard.Health = 100
				if got := guardHandler(f.guard, n, 0, 101); got != 2 || n.Target != f.ward.Handle || len(f.air) != 1 || q.primary[1] != rear {
					t.Fatal("healed aircraft did not resume its original guard")
				}
				if f.guard.Attachment.Carrier != 0 || len(carrier.Attachment.Cargo) != 0 || f.guard.Move.Mode&3 != 2 {
					t.Fatal("healed guard remained attached to carrier")
				}
			} else if code != 2 || len(q.primary) != 2 || q.primary[0] != n || q.primary[1] != rear {
				t.Fatal("unselected pad changed the guard queue")
			}
			if *f.sim != random {
				t.Fatal("pad selection consumed unexpected RNG")
			}
			if (!tc.modern || tc.health >= 75) && queries != 0 {
				t.Fatal("bypassed seek queried pads")
			}
		})
	}
}

func TestModernGuardNearbyWorkPriorityAndBypass(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, modern := range []bool{false, true} {
			t.Run(fmt.Sprintf("air=%v/modern=%v", air, modern), func(t *testing.T) {
				f := newGuardFixture(t, 1, 1)
				q := QueueForUnit(f.guard)
				b := q.Binding()
				b.ModernGuardAssistance = modern
				f.guard.Def.Builder, f.guard.Def.CanReclamate, f.guard.Def.CanResurrect = true, true, true
				f.guard.Def.CanFly, f.guard.Def.BMCode, f.guard.Def.SightDistance = air, 1, 128
				b.Movement.InstallAir = func(AirGoalRequest) bool { return true }
				b.World = &WorldQueryAdapter{SeaLevel: func() uint8 { return 0 }}
				patient := &units.Unit{Handle: 3, Alive: true, Def: &content.UnitDef{MaxDamage: 100}, Health: 50, MaxHealth: 100, X: f.guard.X + numeric.Fixed(128<<16), Z: f.guard.Z}
				patient.Move.ModeMirror = 1
				outside := *patient
				outside.Handle = 4
				outside.X += numeric.Fixed(1 << 16)
				enemy := *patient
				enemy.Handle = 5
				enemy.Owner = 1
				reclaim := *patient
				reclaim.Handle = 6
				reclaim.LastDamageCause = 5
				b.Hostility = func(a, c *units.Unit) bool { return a.Owner != c.Owner }
				unitScans, featureScans, workCalls := 0, 0, 0
				b.World.ForEachUnit = func(visit func(pool.Handle, *units.Unit) bool) {
					unitScans++
					for _, candidate := range []*units.Unit{&outside, &enemy, &reclaim, patient} {
						if visit(candidate.Handle, candidate) {
							break
						}
					}
				}
				wreck := FeatureView{DefinitionKey: "unit_dead", Reclaimable: true, X: f.guard.X, Z: f.guard.Z}
				b.World.ForEachFeature = func(visit func(FeatureView) bool) { featureScans++; visit(wreck) }
				b.Work = &WorkAdapter{CanResurrectFeature: func(v FeatureView) bool { return v.DefinitionKey == wreck.DefinitionKey }, Repair: func(*units.Unit, *units.Unit, *Node, uint32) bool { workCalls++; return true }, Resurrect: func(*units.Unit, *Node, uint32) bool { workCalls++; return true }}
				resources := ResourceView{Stock: [2]float32{0, 20}, Capacity: [2]float32{100, 100}}
				b.Resources = func(uint8) (ResourceView, bool) { return resources, true }
				n := guardNode(f)
				n.Phase = 1
				n.GoalX = numeric.Fixed(16 << 16)
				n.Param1 = 64
				if air {
					n.ID = Lookup("VTOL_Follow")
					n.Phase = 2
				}
				rear := &Node{ID: Lookup("Move"), Owner: f.guard.Handle}
				q.primary = []*Node{n, rear}
				random := *f.sim
				code := guardHandler(f.guard, n, 0, 100)
				if modern {
					want := Lookup("RepairUnit")
					if air {
						want = Lookup("VTOL_RepairUnit")
					}
					if code != 2 || len(q.primary) != 3 || q.primary[0].ID != want || q.primary[0].Target != patient.Handle || q.primary[1] != n || q.primary[2] != rear || featureScans != 0 {
						t.Fatal("nearby repair did not precede resurrection with Guard retained")
					}
					q.applyPrimaryResultCode(n, code, 100)
					if int(n.Phase) != guardLegPhase(air) {
						t.Fatal("nearby work reset guard admission")
					}
					q.primary = q.primary[1:]
					patient.Health = 100
					if got := guardHandler(f.guard, n, 0, 101); got != 2 || q.primary[0].ID != Lookup("Resurrect") || q.primary[1] != n {
						t.Fatal("guard did not proceed to nearby resurrection")
					}
					q.primary = q.primary[1:]
					b.World.ForEachFeature = nil
					if got := guardHandler(f.guard, n, 0, 102); got != 2 || q.primary[0] != n || q.primary[1] != rear || n.Target != f.ward.Handle {
						t.Fatal("finished work did not resume original Guard")
					}
					if !air && n.GoalX != numeric.Fixed(16<<16) {
						t.Fatal("work changed follow offset")
					}
				} else if code != 2 || len(q.primary) != 2 || unitScans != 0 || featureScans != 0 {
					t.Fatal("Strict guard performed Modern scans")
				}
				if *f.sim != random || workCalls != 0 || resources.Stock != [2]float32{0, 20} || f.guard.Health != 100 {
					t.Fatal("selection executed work, paid resources or changed RNG")
				}
			})
		}
	}
}

func TestModernGuardLowEnergyStillResurrectsOnlyEligibleNearbyWreck(t *testing.T) {
	for _, capable := range []bool{false, true} {
		t.Run(fmt.Sprintf("resurrect=%v", capable), func(t *testing.T) {
			f := newGuardFixture(t, 1, 1)
			q := QueueForUnit(f.guard)
			b := q.Binding()
			b.ModernGuardAssistance = true
			f.guard.Def.Builder, f.guard.Def.CanReclamate, f.guard.Def.CanResurrect = true, true, capable
			f.guard.Def.BMCode, f.guard.Def.SightDistance = 1, 128
			resources := ResourceView{Stock: [2]float32{0, 19}, Capacity: [2]float32{100, 100}}
			b.Resources = func(uint8) (ResourceView, bool) { return resources, true }
			b.World = &WorldQueryAdapter{
				ForEachUnit: func(func(pool.Handle, *units.Unit) bool) { t.Fatal("low energy scanned repairs") },
			}
			eligible := FeatureView{DefinitionKey: "unit_dead", Reclaimable: true, X: f.guard.X + numeric.Fixed(128<<16), Z: f.guard.Z}
			outside := eligible
			outside.X += numeric.Fixed(1 << 16)
			unreclaimable := eligible
			unreclaimable.Reclaimable = false
			tree := eligible
			tree.DefinitionKey = "tree"
			later := eligible
			later.X = f.guard.X
			featureScans, queries := 0, 0
			b.World.ForEachFeature = func(visit func(FeatureView) bool) {
				featureScans++
				for _, v := range []FeatureView{outside, unreclaimable, tree, eligible, later} {
					if visit(v) {
						break
					}
				}
			}
			b.Work = &WorkAdapter{CanResurrectFeature: func(v FeatureView) bool { queries++; return v.DefinitionKey == eligible.DefinitionKey }}
			n := guardNode(f)
			n.Phase = 1
			q.primary = []*Node{n}
			random := *f.sim
			code := guardHandler(f.guard, n, 0, 100)
			if capable {
				if code != 2 || len(q.primary) != 2 || q.primary[0].ID != Lookup("Resurrect") || q.primary[0].GoalX != eligible.X || q.primary[1] != n || queries != 2 {
					t.Fatal("resurrection ignored range, capability, catalog eligibility or stable order")
				}
			} else if code != 2 || len(q.primary) != 1 || featureScans != 0 || queries != 0 {
				t.Fatal("ordinary constructor attempted resurrection")
			}
			if *f.sim != random || resources.Stock != [2]float32{0, 19} {
				t.Fatal("resurrection selection changed resources or RNG")
			}
		})
	}
}

func TestGuardPadResumptionDoesNotReleaseStrictOrTransportCargo(t *testing.T) {
	for _, modern := range []bool{false, true} {
		f := newAirGuardFixture(t)
		q := QueueForUnit(f.guard)
		q.Binding().ModernGuardAssistance = modern
		carrier := &units.Unit{Handle: 3, Alive: true, Def: &content.UnitDef{Builder: true, IsAirBase: !modern}}
		q.Binding().Lookup = func(h pool.Handle) *units.Unit {
			if h == f.ward.Handle {
				return f.ward
			}
			if h == carrier.Handle {
				return carrier
			}
			return nil
		}
		f.guard.Attachment.Carrier = carrier.Handle
		carrier.Attachment.Cargo = []pool.Handle{f.guard.Handle}
		n := airGuardNode(f)
		n.Phase = 2
		random := *f.sim
		if code := guardHandler(f.guard, n, 0, 100); code != 7 || f.guard.Attachment.Carrier != carrier.Handle || len(carrier.Attachment.Cargo) != 1 || *f.sim != random {
			t.Fatalf("modern=%v released cargo outside Modern air-base policy", modern)
		}
	}
}

func TestModernGuardFailedLandingWaitsForMaintenanceRetry(t *testing.T) {
	f := newAirGuardFixture(t)
	q := QueueForUnit(f.guard)
	b := q.Binding()
	b.ModernGuardAssistance = true
	f.guard.Health = 50
	pad := &units.Unit{Handle: 3, Alive: true, Activated: true, Def: &content.UnitDef{Builder: true, IsAirBase: true}, X: f.guard.X, Z: f.guard.Z}
	b.Lookup = func(h pool.Handle) *units.Unit {
		if h == f.ward.Handle {
			return f.ward
		}
		if h == pad.Handle {
			return pad
		}
		return nil
	}
	b.Movement.AirBases = func(uint8) []pool.Handle { return []pool.Handle{pad.Handle} }
	landings := 0
	b.Movement.RunAir = func(_ *units.Unit, n *Node, _ uint32, _ uint32) (Code, bool) {
		if n.ID == Lookup("VTOL_Landing") {
			landings++
			return 8, true
		}
		return 0, false
	}
	n := airGuardNode(f)
	n.Phase = 2
	q.primary = []*Node{n}
	random := *f.sim
	q.Pump(f.guard, 100)
	if landings != 1 || len(q.primary) != 1 || q.primary[0] != n || n.Deadline != 130 || n.DynamicGate != gateDeadline {
		t.Fatalf("failed landing did not park original Guard: attempts=%d queue=%v", landings, q.primary)
	}
	q.Pump(f.guard, 129)
	if landings != 1 {
		t.Fatal("failed landing retried before maintenance deadline")
	}
	q.Pump(f.guard, 130)
	if landings != 2 || n.Phase != 2 || n.Deadline != 160 || *f.sim != random {
		t.Fatal("landing retry changed cadence, phase or RNG")
	}
}
