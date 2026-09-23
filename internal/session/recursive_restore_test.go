package session

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Extend the authored RNG fixture with a third unit and observable saved
// fields. No serialized phase is added: BobPhase remains constructor state
// [08 R-SAVE-02 §6][04 R-MOV-01 §5c].
func recursiveRestoreFixture(t *testing.T) (save.RetailProjection, RetailLoadDeps, []pool.Handle) {
	t.Helper()
	bank, deps := restoreRNGFixture(t)
	weapon := &content.WeaponDef{ID: 1, Name: "fixture"}
	weapon.RestoreActiveByte(9)
	deps.Catalog.Weapons = map[string]*content.WeaponDef{"fixture": weapon}
	deps.Catalog.Units["armcom"].Weapon1 = "fixture"
	deps.Catalog.Units["armcom"].Weapon1Def = weapon
	initial, err := LoadRetailSaveWithDeps(bank, deps)
	if err != nil {
		t.Fatal(err)
	}
	src := initial.Battle.Session
	ids := []pool.Handle{pool.Handle(initial.Battle.Image.Units.Records[0].StableID), pool.Handle(initial.Battle.Image.Units.Records[1].StableID)}
	first := src.Units.Unit(ids[0])
	third, err := src.Units.Create(first.Def, first.Owner, numeric.FixedFromInt(288), numeric.FixedFromInt(10), numeric.FixedFromInt(160))
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, third)
	for i, id := range ids {
		u := src.Units.Unit(id)
		src.Movement.EnsureUnit(u)
		u.Health, u.Kills = int32(17+i), int32(3+i)
		u.Move.Bank, u.Move.Heading, u.Move.Pitch = uint16(100+i), uint16(200+i), uint16(300+i)
		u.Remaining = .25
		u.Slots[0].Reload = int32(40 + i)
		u.GetScript().Pieces[0].SetTrans(0, numeric.FixedFromInt(int64(90+i)))
		orders.BindQueue(u, orders.NewQueueWith([]*orders.Node{{ID: orders.Lookup("Stop"), Owner: id}}, nil))
	}
	in, err := src.RetailBattleSaveInputs(initial.Summary, save.Camera{})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := src.RetailProjection(in)
	if err != nil {
		t.Fatal(err)
	}
	// Different saved bytes for a shared definition make the completion order
	// visible even if allocation was reordered in an otherwise eager pass.
	for i, rec := range projection.Units.Records {
		rec.Data[0x49] = byte(i + 1)
	}
	return projection, deps, ids
}

func recursiveRestoreBank(t *testing.T, projection save.RetailProjection) *save.Bank {
	t.Helper()
	data, err := projection.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	bank, err := save.OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	return bank
}

// Carrier loading, local attachment, and engagement loading determine the
// constructor sequence. Reaching an already live recursive ancestor spends no
// additional draw [08 R-SAVE-02 §6]. Final RNG counts alone cannot catch a
// permutation of the per-unit phases [04 R-MOV-01 §5c].
func TestRetailRestoreRecursiveConstructorPhases(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31, gameplay.Community39} {
		for _, tc := range []struct {
			name       string
			carrier    [3]int
			engagement [3]int
			allocation []int
			last       int
		}{
			{"forward engagement", [3]int{-1, -1, -1}, [3]int{2, -1, -1}, []int{0, 2, 1}, 1},
			{"forward carrier", [3]int{2, -1, -1}, [3]int{-1, -1, -1}, []int{0, 2, 1}, 1},
			{"carrier then engagement", [3]int{2, 2, -1}, [3]int{1, -1, -1}, []int{0, 2, 1}, 0},
			{"engagement cycle", [3]int{-1, -1, -1}, [3]int{2, -1, 0}, []int{0, 2, 1}, 1},
			{"carrier engagement back-reference", [3]int{2, -1, -1}, [3]int{-1, -1, 0}, []int{0, 2, 1}, 1},
			{"self engagement", [3]int{-1, -1, -1}, [3]int{0, -1, -1}, []int{0, 1, 2}, 2},
		} {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				projection, deps, ids := recursiveRestoreFixture(t)
				deps.Gameplay = mode
				for i, rec := range projection.Units.Records {
					if target := tc.carrier[i]; target >= 0 {
						binary.LittleEndian.PutUint16(rec.Data[0x89:], uint16(ids[target]))
						rec.Data[0x8d] = 0
					}
					if target := tc.engagement[i]; target >= 0 {
						binary.LittleEndian.PutUint16(rec.Data[0x8b:], uint16(ids[target]))
					}
				}
				result, err := LoadRetailSaveWithDeps(recursiveRestoreBank(t, projection), deps)
				if err != nil {
					t.Fatal(err)
				}
				s := result.Battle.Session
				want := rng.NewSimulation(deps.SimSeed)
				landW, landH := want.Uint32n(10)+11, want.Uint32n(3)+11
				want.Uint32n(landW)
				want.Uint32n(landH)
				waterW, waterH := want.Uint32n(20)+14, want.Uint32n(3)+14
				want.Uint32n(waterW)
				want.Uint32n(waterH)
				want.Uint32n(75) // burning feature precedes every constructor
				for _, i := range tc.allocation {
					want.Uint32n(4096)
					phase := int16(uint16(want.Uint32n(65536)))
					want.Uint32n(65536) // authored Create random(0, 65535)
					if got := s.Units.Unit(ids[i]).BobPhase; got != phase {
						t.Fatalf("unit %d phase = %d, want recursive draw %d", ids[i], got, phase)
					}
				}
				if s.SimRNG().State != want.State || s.SimRNG().Draws() != want.Draws() {
					t.Fatalf("constructor RNG = %d/%d, want %d/%d", s.SimRNG().State, s.SimRNG().Draws(), want.State, want.Draws())
				}
				if got := s.Catalog.Weapons["fixture"].ActiveByte(); got != byte(tc.last+1) {
					t.Fatalf("shared weapon byte = %d, want last recursive completion %d", got, tc.last+1)
				}
				if tc.name == "carrier then engagement" {
					if got := s.Units.Unit(ids[2]).Attachment.Cargo; !slices.Equal(got, []pool.Handle{ids[1], ids[0]}) {
						t.Fatalf("cargo head order = %v, want engagement after caller attachment", got)
					}
				}
			})
		}
	}
}

// Constructor observers distinguish recursive restoration from merely sorting
// allocations: early fields are visible before descending, while a completed
// carrier's queue, script and weapon state precede the next constructor
// [08 R-SAVE-02 §6].
func TestRetailRestoreRecursiveVisitsPublishFieldsInOrder(t *testing.T) {
	projection, deps, ids := recursiveRestoreFixture(t)
	firstRecord := projection.Units.Records[0].Data
	binary.LittleEndian.PutUint16(firstRecord[0x89:], uint16(ids[2]))
	binary.LittleEndian.PutUint16(firstRecord[0x8b:], uint16(ids[1]))
	firstRecord[0x8d] = 0
	binary.LittleEndian.PutUint32(firstRecord[0xb4:], binary.LittleEndian.Uint32(firstRecord[0xb4:])|(1<<20))
	stage, err := StageRetailBattle(recursiveRestoreBank(t, projection), deps)
	if err != nil {
		t.Fatal(err)
	}
	s := stage.Session
	if s.Units.Used() != 0 {
		t.Fatal("staging constructed records before recursive restoration")
	}
	var allocated []pool.Handle
	onCreate := s.Units.OnCreate
	s.Units.OnCreate = func(h pool.Handle, u *units.Unit) {
		if onCreate != nil {
			onCreate(h, u)
		}
		allocated = append(allocated, h)
		if h == ids[0] {
			return
		}
		first := s.Units.Unit(ids[0])
		if first.Health != 17 || first.Kills != 3 || first.Move.Bank != 100 || first.Move.Heading != 200 || first.Move.Pitch != 300 {
			t.Fatalf("recursive constructor did not observe saved caller pose/health: %+v", first.Move)
		}
		if first.Remaining != 0 || first.Dying {
			t.Fatal("caller received later scalars before its reference recursion completed")
		}
		if h == ids[1] {
			carrier := s.Units.Unit(ids[2])
			if first.Attachment.Carrier != carrier.Handle || !slices.Equal(carrier.Attachment.Cargo, []pool.Handle{first.Handle}) {
				t.Fatal("engagement constructor ran before the caller's local attachment")
			}
			if carrier.Remaining != .25 || carrier.Slots[0].Reload != 42 || carrier.GetScript().Pieces[0].GetTrans(0) != numeric.FixedFromInt(92) || orders.QueueOfUnit(carrier).Head() == nil {
				t.Fatal("engagement constructor ran before carrier scalar/order/script/weapon restoration")
			}
			if got := u.Slots[0].Weapon.ActiveByte(); got != 3 {
				t.Fatalf("later constructor observed shared weapon byte %d, want completed carrier byte 3", got)
			}
		}
	}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(allocated, []pool.Handle{ids[0], ids[2], ids[1]}) {
		t.Fatalf("constructor visits = %v", allocated)
	}
	if !s.Units.Unit(ids[0]).Dying {
		t.Fatal("saved pending-death latch was not restored after attachment")
	}
}

// A failed descendant must not make its caller attach again on retry: attachment
// inserts at the carrier's head, so repeating it reverses the cargo sequence
// [08 R-SAVE-02 §6][04 R-COB-03 §5].
func TestRetailRestoreRecursiveRetryPreservesCargoOrder(t *testing.T) {
	projection, deps, ids := recursiveRestoreFixture(t)
	for i := 0; i < 2; i++ {
		data := projection.Units.Records[i].Data
		binary.LittleEndian.PutUint16(data[0x89:], uint16(ids[2]))
		data[0x8d] = 0
	}
	binary.LittleEndian.PutUint16(projection.Units.Records[0].Data[0x8b:], uint16(ids[1]))
	stage, err := StageRetailBattle(recursiveRestoreBank(t, projection), deps)
	if err != nil {
		t.Fatal(err)
	}
	good := stage.Image.Units.Scripts[1].Data
	stage.Image.Units.Scripts[1].Data = nil
	if err := RestoreRetailBattleCore(stage); err == nil {
		t.Fatal("invalid descendant script accepted")
	}
	before := stage.Session.SimRNG().Draws()
	stage.Image.Units.Scripts[1].Data = good
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatal(err)
	}
	if got := stage.Session.Units.Unit(ids[2]).Attachment.Cargo; !slices.Equal(got, []pool.Handle{ids[1], ids[0]}) {
		t.Fatalf("cargo after retry = %v, want descendant before caller", got)
	}
	if stage.Session.SimRNG().Draws() != before {
		t.Fatal("retry repeated a constructor draw")
	}
}

// Engagement cycles can revisit an allocated ancestor, but carrier ownership
// must remain acyclic [08 R-SAVE-02 §6].
func TestRetailRestoreRejectsCarrierCyclesBeforeConstruction(t *testing.T) {
	projection, deps, ids := recursiveRestoreFixture(t)
	stage, err := StageRetailBattle(recursiveRestoreBank(t, projection), deps)
	if err != nil {
		t.Fatal(err)
	}
	// The bank decoder also rejects cycles. Core-only detached callers keep
	// the same containment guard without relying on that decode boundary.
	binary.LittleEndian.PutUint16(stage.Image.Units.Records[0].Data[0x89:], uint16(ids[1]))
	binary.LittleEndian.PutUint16(stage.Image.Units.Records[1].Data[0x89:], uint16(ids[0]))
	before := stage.Session.SimRNG().Draws()
	if err := RestoreRetailBattleCore(stage); err == nil {
		t.Fatal("carrier cycle accepted")
	}
	if stage.Session.Units.Used() != 0 || stage.Session.SimRNG().Draws() != before {
		t.Fatal("invalid containment constructed a unit")
	}
}
