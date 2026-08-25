package session

import (
	"sort"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/kernel"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// TestP0I11_SaveContinuationMatchesUninterrupted verifies P0-I11 [08 "Save"].
// Save during simultaneous movement, construction, COB sleep, projectile flight,
// burning feature, countdown - after load next 300 ticks and RNG draw counts match byte-for-byte.
// It uses the sole codec internal/save via CaptureStateV1/RestoreStateV1 with forced slot identity [01 §6.1][P0-I11].
func TestP0I11_SaveContinuationMatchesUninterrupted(t *testing.T) {
	rng.SeedGlobal(12345, 67890)
	cat := minimalCatalogForStrict()
	if cat.Weapons == nil {
		cat.Weapons = make(map[string]*content.WeaponDef)
	}
	cat.Weapons["testweapon"] = &content.WeaponDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testweapon"},
		ID:               1,
		Name:             "testweapon",
		WeaponVelocity:   65536 * 5,
		Range:            5000,
		ReloadTime:       10,
	}
	cat.Weapons["testweapon"].CanonicalKey = "testweapon"
	if cat.Features == nil {
		cat.Features = make(map[string]*content.FeatureDef)
	}
	featDef := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"},
		FootprintX:       1, FootprintZ: 1,
		Object:     "tree",
		BurnWeapon: "testweapon",
	}
	featDef.CanonicalKey = "tree"
	featDef.FootprintX = 1
	featDef.FootprintZ = 1
	cat.Features["tree"] = featDef

	terrain := minimalTerrain()
	m := syntheticMission()
	buildSession := func() *Session {
		s := &Session{
			Catalog:  cat,
			World:    terrain,
			Mission:  m,
			Clock:    &clock.State{Requested: 10, Active: 10},
			Kernel:   &kernel.Kernel{},
			Snapshot: &snapshot.Buffer{},
			Econ:     &economy.Service{},
			Latch:    NewEndLatch(),
		}
		w, _ := newSlicedWorld(cat)
		s.Units = w
		for i := 0; i < 2; i++ {
			s.Econ.Players[i].Exists = true
			s.Econ.Players[i].ControllerState = uint8(1 + i%2)
			s.Econ.Players[i].UpdateTime = 0
			s.Econ.Players[i].WinLoseTime = 0
			s.Econ.Players[i].Stock[0] = 1000
			s.Econ.Players[i].Stock[1] = 1000
		}
		s.Econ.SeedDeadlines(0)
		crt := rng.Global.Crt
		if crt == nil {
			tmp := rng.NewCRT(67890)
			crt = &tmp
		}
		s.InitWindForSession(crt, 0)
		if err := createAndBindServices(s); err != nil {
			t.Fatalf("createAndBindServices: %v", err)
		}
		s.RegisterAll()
		return s
	}
	sA := buildSession()
	// Create units
	hMove, _ := sA.Units.Create(cat.Units["armcom"], 0, numeric.Fixed(10*65536), 0, numeric.Fixed(10*65536))
	hBuilder, _ := sA.Units.Create(cat.Units["armcom"], 0, numeric.Fixed(20*65536), 0, numeric.Fixed(20*65536))
	if hMove == 0 || hBuilder == 0 {
		t.Fatalf("unit create failed %v %v", hMove, hBuilder)
	}
	if u := sA.Units.Unit(hBuilder); u != nil {
		u.Remaining = 0.5
		u.MaxHealth = 1000
		u.Health = 500
	}
	if sA.Movement != nil {
		route := &movement.Route{Count: 3, Active: true, Dirty: true}
		route.Points[0] = movement.Point{X: 10, Z: 10}
		route.Points[1] = movement.Point{X: 20, Z: 20}
		route.Points[2] = movement.Point{X: 30, Z: 30}
		sA.Movement.Routes[hMove] = route
		if sA.Movement.Scheduler != nil {
			req := path.Request{
				Unit:   hMove,
				Player: 0,
				Start:  path.Cell{X: 10, Z: 10},
				Goal:   path.PointGoal(path.Cell{X: 30, Z: 30}, 0),
			}
			sA.Movement.Scheduler.Submit(req)
		}
		if u := sA.Units.Unit(hMove); u != nil {
			u.Move.Mode = 2
			u.Move.Heading = 0x2000
			u.Move.Speed = numeric.Fixed(1 * 65536)
		}
	}
	if u := sA.Units.Unit(hMove); u != nil {
		prog := &cob.Program{Code: make([]uint32, 1), Statics: 2, Pieces: []string{}, Scripts: map[string]int{}}
		vm := cob.NewVM(prog)
		vm.Threads[0].Status = cob.ThreadSleeping
		vm.Threads[0].Sleep = 15
		vm.Threads[0].SP = 0
		u.SetScript(vm)
	}
	if u := sA.Units.Unit(hMove); u != nil {
		q := orders.QueueForUnit(u)
		id := orders.Lookup("Move_Ground")
		if id == 0 {
			id = orders.Lookup("QMove")
		}
		if id != 0 {
			node := orders.NewMoveNode(id, numeric.Fixed(30*65536), numeric.Fixed(30*65536), sA.Clock.GlobalTick, hMove, false)
			q.Push(id, node)
		}
	}
	if sA.Combat != nil {
		hProj, ok := sA.Combat.Reserve()
		if ok {
			idx := int(hProj) - 1
			if idx >= 0 && idx < len(sA.Combat.Records) {
				p := &sA.Combat.Records[idx]
				p.WeaponID = 1
				p.Pos = combat.Vec3{X: numeric.Fixed(15 * 65536), Y: numeric.Fixed(10 * 65536), Z: numeric.Fixed(15 * 65536)}
				p.StartPos = p.Pos
				p.TargetPos = combat.Vec3{X: numeric.Fixed(30 * 65536), Y: 0, Z: numeric.Fixed(30 * 65536)}
				p.Velocity = combat.Vec3{X: numeric.Fixed(1 * 65536), Y: 0, Z: numeric.Fixed(1 * 65536)}
				p.Speed = numeric.Fixed(1 * 65536)
				p.Yaw = 0x2000
				p.Shooter = hMove
				p.ShooterSide = 0
				p.CreationTick = sA.Clock.GlobalTick
				p.BurstRemaining = 0
				p.ExpiryTick = sA.Clock.GlobalTick + 1000
			}
		}
	}
	if sA.Features != nil {
		inst := sA.Features.PlaceAt(5, 5, featDef)
		if inst != nil {
			inst.IsBurning = true
			inst.BurnTicks = 10
			inst.BurnCountdown = 5
			inst.BurnDuration = 100
			inst.Health = 100
			inst.MaxHealth = 100
		}
	}
	sA.Latch.Arm()
	sA.Latch.Pending = 1

	for i := 0; i < 50; i++ {
		sA.Step(int32(i))
	}
	st := sA.CaptureStateV1()
	if st == nil {
		t.Fatalf("CaptureStateV1 nil")
	}
	sort.Slice(st.Units, func(i, j int) bool { return st.Units[i].Slot < st.Units[j].Slot })
	b := save.NewBuilder(save.RetailTag)
	save.WriteStateV1(b, st)
	bankBytes := b.Bytes()
	bank, err := save.OpenBytes(bankBytes, save.RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	decoded, err := save.ReadStateV1(bank, cat.Hash, cat.Manifest)
	if err != nil {
		t.Fatalf("ReadStateV1: %v", err)
	}
	if decoded.SimDraws != st.SimDraws || decoded.CrtDraws != st.CrtDraws {
		t.Fatalf("RNG draws mismatch after bank round-trip: sim %d/%d crt %d/%d", decoded.SimDraws, st.SimDraws, decoded.CrtDraws, st.CrtDraws)
	}
	for i := 50; i < 350; i++ {
		sA.Step(int32(i))
	}
	finalAUnits := snapshotUnitsSave(sA)
	finalAProj := snapshotProjectilesSave(sA)
	finalAFeatures := snapshotFeaturesSave(sA)
	finalALatch := sA.Latch
	finalASimDraws := rng.Global.Sim.Draws()
	finalACrtDraws := rng.Global.Crt.Draws()
	finalAClock := sA.Clock.GlobalTick

	// Restore into B
	sB := buildSession()
	if err := sB.RestoreStateV1(decoded); err != nil {
		t.Fatalf("RestoreStateV1: %v", err)
	}
	for _, rec := range decoded.Units {
		u := sB.Units.Unit(pool.Handle(rec.Slot))
		if u == nil {
			t.Fatalf("forced slot %d not restored", rec.Slot)
		}
		if int32(u.Handle) != rec.Slot {
			t.Fatalf("slot mismatch %d vs %d", u.Handle, rec.Slot)
		}
	}
	start := decoded.Clock.GlobalTick + 1
	for i := 0; i < 300; i++ {
		sB.Step(int32(start) + int32(i))
	}
	finalBUnits := snapshotUnitsSave(sB)
	finalBProj := snapshotProjectilesSave(sB)
	finalBFeatures := snapshotFeaturesSave(sB)
	finalBLatch := sB.Latch
	finalBSimDraws := rng.Global.Sim.Draws()
	finalBCrtDraws := rng.Global.Crt.Draws()
	finalBClock := sB.Clock.GlobalTick

	if finalAClock != finalBClock {
		t.Fatalf("clock mismatch A %d B %d", finalAClock, finalBClock)
	}
	if finalASimDraws != finalBSimDraws || finalACrtDraws != finalBCrtDraws {
		t.Fatalf("RNG draws mismatch after 300 ticks: A sim %d crt %d vs B sim %d crt %d", finalASimDraws, finalACrtDraws, finalBSimDraws, finalBCrtDraws)
	}
	if finalALatch != finalBLatch {
		t.Fatalf("latch mismatch A %+v B %+v", finalALatch, finalBLatch)
	}
	if len(finalAUnits) != len(finalBUnits) {
		t.Fatalf("unit count mismatch %d vs %d", len(finalAUnits), len(finalBUnits))
	}
	for i := range finalAUnits {
		a := finalAUnits[i]
		b := finalBUnits[i]
		if a.Slot != b.Slot || a.X != b.X || a.Z != b.Z || a.Health != b.Health || a.Remaining != b.Remaining {
			t.Fatalf("unit %d mismatch A %+v B %+v", a.Slot, a, b)
		}
	}
	if len(finalAProj) != len(finalBProj) {
		t.Fatalf("projectile count mismatch %d vs %d", len(finalAProj), len(finalBProj))
	}
	for i := range finalAProj {
		if finalAProj[i] != finalBProj[i] {
			t.Fatalf("projectile %d mismatch %+v vs %+v", i, finalAProj[i], finalBProj[i])
		}
	}
	if len(finalAFeatures) != len(finalBFeatures) {
		t.Fatalf("feature count mismatch %d vs %d", len(finalAFeatures), len(finalBFeatures))
	}
	for i := range finalAFeatures {
		if finalAFeatures[i] != finalBFeatures[i] {
			t.Fatalf("feature %d mismatch %+v vs %+v", i, finalAFeatures[i], finalBFeatures[i])
		}
	}
}

func snapshotUnitsSave(s *Session) []save.UnitRecord {
	if s == nil || s.Units == nil {
		return nil
	}
	var out []save.UnitRecord
	for _, u := range s.Units.Iter() {
		out = append(out, save.UnitRecord{
			Slot: int32(u.Handle), X: int32(u.X.Raw()), Z: int32(u.Z.Raw()), Health: u.Health, Remaining: u.Remaining,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

func snapshotProjectilesSave(s *Session) []save.ProjectileRecord {
	if s == nil || s.Combat == nil {
		return nil
	}
	var out []save.ProjectileRecord
	for i := 0; i < s.Combat.Count(); i++ {
		h := pool.Handle(i + 1)
		if i < len(s.Combat.Records) {
			p := s.Combat.Records[i]
			out = append(out, save.ProjectileRecord{
				Handle: int32(h), PosX: int32(p.Pos.X.Raw()), PosZ: int32(p.Pos.Z.Raw()), WeaponID: p.WeaponID,
			})
		}
	}
	return out
}

func snapshotFeaturesSave(s *Session) []save.FeatureInstanceRecord {
	if s == nil || s.Features == nil {
		return nil
	}
	_, _, m := s.Features.SnapshotWithKeys()
	var out []save.FeatureInstanceRecord
	for _, inst := range m {
		if inst == nil || inst.Def == nil {
			continue
		}
		out = append(out, save.FeatureInstanceRecord{
			CX: int32(inst.CX), CZ: int32(inst.CZ), Health: inst.Health, IsBurning: inst.IsBurning, BurnTicks: inst.BurnTicks,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CX != out[j].CX {
			return out[i].CX < out[j].CX
		}
		return out[i].CZ < out[j].CZ
	})
	return out
}
