package session

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/units"
)

// buildRestoreBenchStage builds a synthetic battle image with n forced-slot
// mobile units, each carrying one u%04xacc box, one u%04xmob box (HasMover
// set), and three secondary orders. Secondary orders keep the front-queue
// pump from running (it needs an injected simulation RNG this fixture does
// not carry), which is unrelated to what this benchmark measures: the R06
// per-unit later passes' box/order lookup [08 R-SAVE-02 §6, §8].
func buildRestoreBenchStage(b *testing.B, n int) *RetailBattleStage {
	b.Helper()
	def := &content.UnitDef{
		UnitName:  "benchfixture",
		MaxDamage: 100,
		BMCode:    1, // mobile: EnsureUnit takes the rectangle-stamp path
		Script:    &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}},
	}
	// Player 0's slice is [1, maxDefs]; size it to exactly the stable ID range
	// this fixture forces [P0-16 §3.1, §3.3].
	w := units.NewSliced(n, nil)
	econ := &economy.Service{}
	s := &Session{
		Clock:    &clock.State{},
		Units:    w,
		Econ:     econ,
		Movement: movement.NewSystem(nil, movement.Profile{}, movement.NewOccupancyGrid()),
	}
	stopID := orders.Lookup("Stop")
	if stopID == 0 {
		b.Fatal("fixture requires the established Stop descriptor")
	}
	stable := make(map[uint16]pool.Handle, n)
	records := make([]save.UnitRecord, 0, n)
	other := make([]save.RawBox, 0, n*2)
	ordersOut := make([]save.OrderRecord, 0, n*3)
	for i := 0; i < n; i++ {
		id := uint16(1 + i)
		h, err := w.CreateWithForcedSlot(def, 0, 0, 0, 0, pool.Handle(id))
		if err != nil {
			b.Fatalf("create bench unit %d: %v", id, err)
		}
		econ.UnitBuckets(h)
		stable[id] = h

		data := make([]byte, save.UnitBoxSize)
		binary.LittleEndian.PutUint32(data[0x27:], 1) // established has-mover word
		// Cached occupancy anchor cell, one distinct cell per unit, so the
		// restore's re-stamp pass does not collide every unit's 1x1 footprint
		// onto cell (0,0) [08 R-SAVE-02 §6].
		binary.LittleEndian.PutUint16(data[0x93:], uint16(i))
		records = append(records, save.UnitRecord{StableID: id, Data: data})

		other = append(other,
			save.RawBox{Name: unitBoxName(id, "acc"), Data: make([]byte, 48)},
			save.RawBox{Name: unitBoxName(id, "mob"), Data: make([]byte, 35)},
		)

		for seq := 0; seq < 3; seq++ {
			main := make([]byte, save.OrderBoxSize)
			binary.LittleEndian.PutUint16(main[0:], id)
			main[8] = byte(stopID)
			ordersOut = append(ordersOut, save.OrderRecord{
				ParentStableID: id, Sequence: uint32(seq), Secondary: true, Main: main,
			})
		}
	}
	return &RetailBattleStage{
		Session:    s,
		StableUnit: stable,
		Image: &save.BattleImage{
			Units: save.UnitImage{Records: records, Other: other, Orders: ordersOut},
		},
	}
}

// BenchmarkRestoreRetailBattleCore times only RestoreRetailBattleCore itself
// over increasing unit/order/box counts, locking in the R06 repair: the
// per-unit account/mover/order lookups are indexed once before the unit loop
// instead of rescanning image.Units.Other and image.Units.Orders for every
// unit [08 R-SAVE-02 §6, §8].
func BenchmarkRestoreRetailBattleCore(b *testing.B) {
	for _, n := range []int{200, 1000, 3000} {
		b.Run(fmt.Sprintf("units=%d", n), func(b *testing.B) {
			stage := buildRestoreBenchStage(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := RestoreRetailBattleCore(stage); err != nil {
					b.Fatalf("RestoreRetailBattleCore: %v", err)
				}
			}
		})
	}
}
