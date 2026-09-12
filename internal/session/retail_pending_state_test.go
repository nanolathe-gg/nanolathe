package session

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func pendingStateImage(t *testing.T, s *Session) save.UnitImage {
	t.Helper()
	in := RetailSaveInputs{StableIDs: map[pool.Handle]uint16{}, UnitWriterScratch: map[pool.Handle]units.RetailUnitWriterScratch{}}
	for n := 1; n < s.Units.TotalRecords(); n++ {
		h := pool.Handle(n)
		in.StableIDs[h] = uint16(h)
		in.UnitWriterScratch[h] = units.RetailUnitWriterScratch{}
	}
	image, err := projectUnitImage(s.Units, s.Econ, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

// A later unit can die after its shooter's visit; save retains that held slot
// without running another weapon visit [08 R-SAVE-WEAPON-01].
func TestRetailSaveAfterLaterTargetFinalization(t *testing.T) {
	src, f := newRestoreCoreFixture(t, 2)
	shooter, target := src.Units.Unit(f[0].handle), src.Units.Unit(f[1].handle)
	weapon := &content.WeaponDef{ID: 7, Range: 1000, WeaponVelocity: 65536}
	shooter.InstallWeapon(0, weapon)
	shooter.Slots[0].Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	shooter.Slots[0].Reload = 100
	svc := combat.Service{ControlByte: func(uint8) uint8 { return combat.ControlByteHuman }}
	src.Units.VisitActiveSlots(func(v units.SlotVisit) {
		if v.Handle == shooter.Handle {
			svc.StepWeaponsForUnit(shooter, 1, src.Units, nil, nil, src.Econ, nil, src.SimRNG(), src.CrtRNG())
		}
		if v.Handle == target.Handle {
			result := svc.AcceptDamage(src.Units, 1, combat.DamageInput{Victim: target.Handle, Attacker: shooter.Handle, Nominal: 200, Kind: 1})
			if !result.DeathLatched {
				t.Fatal("ordinary damage did not latch later target death")
			}
			src.Units.FinalizeDeath(target.Handle, 1)
		}
	})
	if src.Units.Unit(target.Handle) != nil || shooter.Slots[0].Target.Unit != target.Handle {
		t.Fatal("scenario did not leave a held free target after shooter visit")
	}
	before := shooter.Slots[0]
	sim, crt := src.SimRNG().Draws(), src.CrtRNG().Draws()
	image := pendingStateImage(t, src)
	if shooter.Slots[0] != before || src.SimRNG().Draws() != sim || src.CrtRNG().Draws() != crt {
		t.Fatal("save changed weapon state or consumed random draws")
	}
	if got := binary.LittleEndian.Uint32(image.Records[0].Data[0x41:]); got != uint32(target.Handle)|0x80000000 {
		t.Fatalf("held pair=%x", got)
	}
	for _, reuse := range []bool{false, true} {
		dst, g := newRestoreCoreFixture(t, 1)
		loaded := dst.Units.Unit(g[0].handle)
		localWeapon := *weapon
		loaded.InstallWeapon(0, &localWeapon)
		stage := &RetailBattleStage{Session: dst, StableUnit: stableUnitMap(g), Image: &save.BattleImage{Units: image}}
		if err := RestoreRetailBattleCore(stage); err != nil {
			t.Fatal(err)
		}
		if loaded.Slots[0].Target.Unit != target.Handle {
			t.Fatal("restore lost a free target slot")
		}
		if reuse {
			if _, err := dst.Units.CreateWithForcedSlot(loaded.Def, 0, 0, 0, 0, target.Handle); err != nil {
				t.Fatal(err)
			}
		}
		svc.StepWeaponsForUnit(loaded, 2, dst.Units, nil, nil, dst.Econ, nil, dst.SimRNG(), dst.CrtRNG())
		if reuse && loaded.Slots[0].Target.Unit != target.Handle {
			t.Fatal("slot reuse introduced a generation check")
		}
		if !reuse && loaded.Slots[0].Target.Kind != units.TargetNone {
			t.Fatal("ordinary target resolver failed to clear restored free slot")
		}
	}
}

// A damage packet delivered after the unit visit leaves one pending death for
// the next visit. Scalar loading must not finalize it [08 R-SAVE-02 §6].
func TestRetailPendingDamageResumesOneFinalization(t *testing.T) {
	src, f := newRestoreCoreFixture(t, 2)
	victim, attacker := src.Units.Unit(f[0].handle), src.Units.Unit(f[1].handle)
	svc := combat.Service{ControlByte: func(uint8) uint8 { return combat.ControlByteHuman }}
	result := svc.AcceptDamage(src.Units, 1, combat.DamageInput{Victim: victim.Handle, Attacker: attacker.Handle, Nominal: 200, Kind: 1})
	if !result.DeathLatched {
		t.Fatal("fatal packet did not latch")
	}
	image := pendingStateImage(t, src)
	if binary.LittleEndian.Uint32(image.Records[0].Data[0xb4:])&(1<<20) == 0 {
		t.Fatal("pending death absent from encoded record")
	}
	dst, g := newRestoreCoreFixture(t, 2)
	count := 0
	dst.Units.OnDeath = func(_ pool.Handle, cause units.DeathCause, u *units.Unit) {
		count++
		if cause != units.DeathKilled || u.EngagementTarget != attacker.Handle {
			t.Fatal("restored death lost cause or attacker")
		}
	}
	stage := &RetailBattleStage{Session: dst, StableUnit: stableUnitMap(g), Image: &save.BattleImage{Units: image}}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatal(err)
	}
	if count != 0 || !dst.Units.NeedsDeathFinalization(victim.Handle) {
		t.Fatal("load executed or omitted pending finalization")
	}
	first := dst.Units.FinalizeDeath(victim.Handle, 2)
	second := dst.Units.FinalizeDeath(victim.Handle, 2)
	if !first.Freed || second.Freed || count != 1 {
		t.Fatalf("finalization first=%+v second=%+v calls=%d", first, second, count)
	}
}

func TestRetailSharedDefinitionByteAndEnabledSlotAreIndependent(t *testing.T) {
	s, f := newRestoreCoreFixture(t, 2)
	shared := &content.WeaponDef{ID: 0}
	s.Units.Unit(f[0].handle).Def.Weapon1Def = shared
	records := make([]save.UnitRecord, 2)
	for i := range records {
		data := unitRecordData(false)
		for slot := 0; slot < 3; slot++ {
			binary.LittleEndian.PutUint16(data[0x43+slot*0x18:], 0x8000)
		}
		binary.LittleEndian.PutUint16(data[0x51:], 100)
		// Both slots are disabled, but the later record activates the shared
		// definition for its next constructor [08 R-SAVE-WEAPON-01].
		data[0x49] = byte(i * 17)
		records[i] = save.UnitRecord{StableID: f[i].stableID, Data: data}
	}
	stage := &RetailBattleStage{Session: s, StableUnit: stableUnitMap(f), Image: &save.BattleImage{Units: save.UnitImage{Records: records}}}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatal(err)
	}
	var svc combat.Service
	for _, unit := range f {
		u := s.Units.Unit(unit.handle)
		if u.Slots[0].Weapon != shared || u.Slots[0].Weapon.ActiveByte() != 17 {
			t.Fatal("earlier restored slot lost shared definition identity")
		}
		svc.StepWeaponsForUnit(u, 2, s.Units, nil, nil, s.Econ, nil, nil, nil)
		if u.Slots[0].Reload != 100 || u.Slots[0].IsEnabled() {
			t.Fatal("definition activation overrode the separately saved disabled slot")
		}
	}
}

func TestRetailStagedDefinitionByteDoesNotLeakToAnotherBattle(t *testing.T) {
	f := loadRetailFixture(t)
	src := f.session(t)
	original := retailUnit(src, 0, retailARM)
	weapon := original.Slots[0].Weapon
	if weapon == nil || weapon.ActiveByte() == 0 {
		t.Fatal("fixture requires an active primary weapon")
	}
	freshByte := weapon.ActiveByte()
	in, err := src.RetailBattleSaveInputs(RetailBattleSummary(src, "active byte", "1", src.Skirmish.UnitLimit), save.Camera{})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := src.RetailProjection(in)
	if err != nil {
		t.Fatal(err)
	}
	// Author the byte independently of the writer, for every alias of this
	// definition so a later record cannot overwrite the intended final value.
	for i := range projection.Units.Records {
		rec := &projection.Units.Records[i]
		u := src.Units.Unit(pool.Handle(rec.StableID))
		for slot := range u.Slots {
			if u.Slots[slot].Weapon == weapon {
				rec.Data[0x49+slot*0x18] = 0
			}
		}
	}
	data, err := projection.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	bank, err := save.OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := StageRetailBattle(bank, RetailLoadDeps{FS: f.fs, Catalog: f.cat, SimSeed: 1, CRTSeed: 2, UnitLimit: src.Skirmish.UnitLimit})
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatal(err)
	}
	loaded := stage.Session.Units.Unit(original.Handle)
	if stage.Session.Catalog == f.cat || loaded.Slots[0].Weapon == weapon || !content.IsWeaponInactive(loaded.Slots[0].Weapon) {
		t.Fatal("saved byte did not reach an isolated definition")
	}
	if weapon.ActiveByte() != freshByte {
		t.Fatal("restoration changed the source battle or shared catalog")
	}
	// The definition byte's ordinary constructor consumer now disables the
	// next unit's primary slot, independently of the old unit's saved controls.
	next, err := stage.Session.Units.Create(loaded.Def, loaded.Owner, loaded.X, loaded.Y, loaded.Z)
	if err != nil {
		t.Fatal(err)
	}
	if stage.Session.Units.Unit(next).Slots[0].IsEnabled() {
		t.Fatal("future slot initialization ignored the restored definition byte")
	}
}
