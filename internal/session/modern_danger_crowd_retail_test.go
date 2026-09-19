package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Observed Nanolathe state from the Great Divide diagnostic at tick 108440.
// These are independently captured simulation positions, not retail file bytes.
var dangerCrowdLayout = []struct {
	slot                 int
	key                  string
	x, y, z              int64
	heading, pitch, bank uint16
	ax, az               int32
}{
	{4, "armcv", 100663297, 5603328, 162403356, 27970, 0, 65319, 95, 153},
	{71, "armflash", 96538814, 5537792, 157338505, 42342, 65210, 0, 91, 149},
	{92, "armflash", 96586645, 5537792, 159482946, 42333, 65210, 0, 91, 151},
	{110, "armflash", 94450813, 5472256, 159436658, 42854, 64561, 0, 89, 151},
	{116, "armflash", 96516939, 5603328, 152046593, 49119, 1297, 65015, 91, 144},
	{118, "armflash", 98612190, 5537792, 152006342, 56033, 64885, 0, 93, 144},
	{126, "armflash", 96491111, 5603328, 161455403, 57701, 0, 0, 91, 153},
	{136, "armflash", 98537517, 5603328, 159354423, 9183, 0, 0, 93, 151},
	{137, "armflash", 98561210, 5603328, 161477734, 14264, 0, 0, 93, 153},
	{139, "armflash", 94440106, 5472256, 157331030, 42982, 0, 65015, 89, 149},
	{140, "armflash", 94408573, 5537792, 161477153, 49894, 65210, 0, 89, 153},
	{141, "armflash", 98531192, 5537792, 157300041, 20220, 326, 65015, 93, 149},
	{142, "armflash", 96403707, 5537792, 155158960, 12373, 0, 0, 91, 147},
	{143, "armflash", 92324768, 5472256, 159416751, 43304, 326, 0, 87, 151},
}

func capturedDangerCrowd(t *testing.T) (*Session, *units.Unit, *units.Unit, []*units.Unit) {
	t.Helper()
	f := loadRetailFixture(t)
	f.cfg.MapName = "Great Divide"
	s := f.session(t)
	s.SetGameplay(gameplay.Modern)
	stepRetail(s, 2)
	for _, ai := range s.AI {
		if ai != nil {
			for i := range ai.Deadlines {
				ai.Deadlines[i] = ^uint32(0)
			}
		}
	}
	eligible, observers := visibilityModeRefreshInputs(s, 3)
	s.Vis.RefreshMode(3, true, eligible, observers)
	var flash *units.Unit
	var crowd []*units.Unit
	for _, row := range dangerCrowdLayout {
		u := placeCompleteRetailUnit(t, s, row.key, 0, numeric.Fixed(row.x), numeric.Fixed(row.z))
		if !s.Movement.PlaceUnit(orders.PlaceRequest{Unit: u.Handle, X: numeric.Fixed(row.x), Y: numeric.Fixed(row.y), Z: numeric.Fixed(row.z)}) {
			t.Fatal("placement")
		}
		u.Move.Heading, u.Move.Pitch, u.Move.Bank = row.heading, row.pitch, row.bank
		c := s.Movement.Collisions[u.Handle]
		c.Heading = row.heading
		s.Movement.Steers[u.Handle].Heading = row.heading
		s.Movement.Steers[u.Handle].PendingHeading = row.heading
		if c.CachedAnchor != (movement.Cell{X: row.ax, Z: row.az}) {
			t.Fatalf("captured slot%d anchor=%v expected(%d,%d)", row.slot, c.CachedAnchor, row.ax, row.az)
		}
		u.Flags = (u.Flags &^ (units.StandingFieldMask<<units.StandingMoveShift | units.StandingFieldMask<<units.StandingFireShift)) | 2<<units.StandingMoveShift | 2<<units.StandingFireShift
		s.bindOrderQueue(u)
		orders.QueueOfUnit(u).Push(orders.Lookup("Standby"), orders.Node{Owner: u.Handle, Flags: orders.FlagAutoOp})
		crowd = append(crowd, u)
		if row.slot == 140 {
			flash = u
			u.Health = 40
		}
	}
	tower := placeCompleteRetailUnit(t, s, "CORRL", 1, numeric.FixedFromInt(968), numeric.FixedFromInt(2792))
	tower.SlotAt(0).Flags &^= units.SlotFlagEnabled // only the injected, nonlethal accepted hit
	return s, flash, tower, crowd
}

func TestModernRetailCapturedFlashCrowdEscapesImpact(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			s, flash, tower, crowd := capturedDangerCrowd(t)
			s.SetGameplay(mode)
			x, z := flash.X, flash.Z
			bearing := numeric.AngleFromAtan2(-927581, 670502)
			noticed := false
			notice := s.Combat.ImpactNotice
			s.Combat.ImpactNotice = func(v, a *units.Unit, b numeric.Angle, tick uint32) {
				if v == flash {
					noticed = true
					bearing = b
				}
				notice(v, a, b, tick)
			}
			if s.dangerVisible(flash, tower) {
				t.Fatal("captured hidden source became visible")
			}
			sim, crt, stock := *s.SimRNG(), *s.CrtRNG(), s.Econ.Players[flash.Owner].Stock
			result := s.Combat.AcceptDamage(s.Units, s.Clock.GlobalTick, combat.DamageInput{Victim: flash.Handle, Attacker: tower.Handle, Nominal: 1, Kind: combat.KindOrdinary, ImpactVelocityX: 927581, ImpactVelocityZ: -670502})
			if !result.Accepted || flash.Health != 39 || noticed != (mode == gameplay.Modern) {
				t.Fatalf("accepted cue absent result=%+v hp%d noticed%v", result, flash.Health, noticed)
			}
			hx := x + numeric.FixedFromInt(int64(numeric.MulRound(numeric.Sin(bearing), 256)))
			hz := z + numeric.FixedFromInt(int64(numeric.MulRound(numeric.Cos(bearing), 256)))
			distance := func(a, b numeric.Fixed) int64 { dx, dz := (a - hx).Int(), (b - hz).Int(); return dx*dx + dz*dz }
			initial := distance(x, z)
			// Lock the failure's premise: every improving straight candidate is
			// blocked in this crowd, although a short sideways detour is possible.
			for _, r := range []int64{64, 32, 16} {
				for _, dir := range [][2]int64{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}} {
					step := r
					if dir[0] != 0 && dir[1] != 0 {
						step = r * 181 / 256
					}
					cx, cz := x+numeric.Fixed(dir[0]*step<<16), z+numeric.Fixed(dir[1]*step<<16)
					if distance(cx, cz) > initial && s.Movement.DangerStepFeasible(flash, cx, cz) {
						t.Fatal("crowd no longer blocks the greedy retreat choices")
					}
				}
			}
			orders.StepDangerResponse(flash, s.Clock.GlobalTick)
			if *s.SimRNG() != sim || *s.CrtRNG() != crt || s.Econ.Players[flash.Owner].Stock != stock {
				t.Fatal("impact/local query/danger response changed RNG or resources")
			}
			response := false
			for i := 0; i < 180; i++ {
				s.Step(s.Clock.ScaledAnchor + 1)
				for i, neighbor := range crowd {
					if neighbor == flash {
						continue
					}
					row := dangerCrowdLayout[i]
					if s.Units.Unit(neighbor.Handle) != neighbor || !neighbor.Alive || neighbor.X != numeric.Fixed(row.x) || neighbor.Z != numeric.Fixed(row.z) {
						t.Fatalf("captured neighbor%d moved or disappeared", row.slot)
					}
					c := s.Movement.Collisions[neighbor.Handle]
					for zz := int32(0); zz < int32(c.FootPrintZ); zz++ {
						for xx := int32(0); xx < int32(c.FootPrintX); xx++ {
							id, ok := s.Movement.Grid.OccupantAtPlane(movement.PlaneGround, movement.Cell{X: row.ax + xx, Z: row.az + zz})
							if !ok || id != int(neighbor.Handle) {
								t.Fatalf("captured neighbor%d lost its original stamp", row.slot)
							}
						}
					}
				}
				if s.dangerVisible(flash, tower) || flash.SlotAt(0).Target.Unit == tower.Handle {
					t.Fatal("escape learned hidden attacker identity")
				}
				n := orders.QueueOfUnit(flash).Head()
				if n != nil && n.ID == orders.Lookup("Move_Ground") {
					response = true
				}
			}
			dx, dz := (flash.X - x).Int(), (flash.Z - z).Int()
			moved := dx*dx+dz*dz > 16*16
			separated := distance(flash.X, flash.Z) > initial
			if response != (mode == gameplay.Modern) || moved != (mode == gameplay.Modern) || (mode == gameplay.Modern && !separated) {
				t.Fatalf("crowded Flash failed to escape accepted impact: response%v moved(%d,%d) head=%+v", response, dx, dz, orders.QueueOfUnit(flash).Head())
			}
		})
	}
}
