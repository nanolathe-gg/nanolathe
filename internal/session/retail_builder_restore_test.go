package session

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Author the target, phase, gate and count directly in their documented save
// positions, independently of the order writer [08 R-SAVE-ORDER-01].
func restoredBuildOrder(owner, target uint16, name string, phase uint8) save.OrderRecord {
	main := make([]byte, save.OrderBoxSize)
	binary.LittleEndian.PutUint16(main, owner)
	binary.LittleEndian.PutUint16(main[2:], target)
	main[9] = phase
	binary.LittleEndian.PutUint32(main[0x0a:], 0xa)
	binary.LittleEndian.PutUint32(main[0x0e:], 101)
	binary.LittleEndian.PutUint32(main[0x2a:], 3)
	binary.LittleEndian.PutUint32(main[0x32:], 0x10010c)
	return save.OrderRecord{ParentStableID: owner, Main: main, DescriptorName: name}
}

// Restored producer references must immediately publish the same progress as
// fresh construction. No allocation or handler visit may be needed to rebuild
// the host index [08 R-SAVE-02 §11].
func TestRetailRestorePublishesProducerProgress(t *testing.T) {
	for _, name := range []string{"BuildingBuild", "MobileBuild", "VTOL_MobileBuild"} {
		t.Run(name, func(t *testing.T) {
			s, ids := newRestoreCoreFixture(t, 2)
			builder, product := s.Units.Unit(ids[0].handle), s.Units.Unit(ids[1].handle)
			if name == "BuildingBuild" {
				builder.Flags |= units.BuildingClassStatus
			}
			s.Build = construction.NewService(nil, nil, s.Units, s.Econ)
			s.Snapshot = frame.NewBuffer()
			s.Clock.GlobalTick = 100
			productBody := unitRecordData(false)
			binary.LittleEndian.PutUint32(productBody[0xa7:], math.Float32bits(.5))
			image := &save.BattleImage{Units: save.UnitImage{
				Records: []save.UnitRecord{
					{StableID: ids[0].stableID, Data: unitRecordData(false)},
					{StableID: ids[1].stableID, Data: productBody},
				},
				Orders: []save.OrderRecord{
					restoredBuildOrder(ids[0].stableID, ids[1].stableID, name, 3),
					restoredBuildOrder(ids[1].stableID, ids[0].stableID, "GetBuilt", 0),
				},
			}}
			if err := RestoreRetailBattleCore(&RetailBattleStage{Session: s, StableUnit: stableUnitMap(ids), Image: image}); err != nil {
				t.Fatal(err)
			}
			node := orders.QueueOfUnit(builder).Head()
			if node.Phase != 3 || node.Deadline != 101 || node.Param2 != 3 || product.Remaining != .5 {
				t.Fatal("reconstruction advanced saved construction")
			}
			s.publishSnapshot(100)
			builds := s.Snapshot.Current().Builds
			if len(builds) != 1 || builds[0].Builder != builder.Handle || builds[0].Product != product.Handle || builds[0].Remaining != .5 || builds[0].Factory != (name == "BuildingBuild") {
				t.Fatalf("restored build progress = %+v", builds)
			}

			// The restored order is itself an observer. Even without the host
			// progress index it receives target removal [04 R-ORD-01 §6].
			s.Build.ClearBuilderLink(product.Handle)
			orders.TargetRemoved(s.Units, product.Handle)
			if node.Target != 0 || node.Satisfied&construction.InterruptStop == 0 {
				t.Fatal("restored producer missed target removal")
			}
			if name == "BuildingBuild" {
				s.Build.RegisterOrderHandlers(orders.QueueOfUnit(builder))
				orders.QueueOfUnit(builder).Pump(builder, 100)
				if node.Phase != 0 || node.Param2 != 2 {
					t.Fatalf("removed restored product left phase=%d count=%d", node.Phase, node.Param2)
				}
			}
		})
	}
}

// Cancelled or finished production may leave a GetBuilt reference on the
// product. HelpBuild/repair/guard references do not identify its producer
// [05 "Build request and factory queue behavior"][08 R-SAVE-02 §11].
func TestRetailRestoreProgressUsesOnlyProducerTargets(t *testing.T) {
	for _, name := range []string{"BuildingBuild", "MobileBuild", "HelpBuild", "RepairUnit", "Guard_NoMove"} {
		t.Run(name, func(t *testing.T) {
			s, ids := newRestoreCoreFixture(t, 2)
			s.Build = construction.NewService(nil, nil, s.Units, s.Econ)
			s.Snapshot = frame.NewBuffer()
			// A stale host entry must be discarded even if the producer's saved
			// order no longer targets its previous product.
			s.Build.SetBuilderLink(ids[1].handle, ids[0].handle)
			target := ids[1].stableID
			if name == "BuildingBuild" || name == "MobileBuild" {
				target = 0
			}
			productBody := unitRecordData(false)
			if name == "MobileBuild" {
				// A cancelled mobile order leaves its unfinished frame behind.
				binary.LittleEndian.PutUint32(productBody[0xa7:], math.Float32bits(.5))
			}
			image := &save.BattleImage{Units: save.UnitImage{
				Records: []save.UnitRecord{
					{StableID: ids[0].stableID, Data: unitRecordData(false)},
					{StableID: ids[1].stableID, Data: productBody},
				},
				Orders: []save.OrderRecord{
					restoredBuildOrder(ids[0].stableID, target, name, 0),
					restoredBuildOrder(ids[1].stableID, ids[0].stableID, "GetBuilt", 0),
				},
			}}
			if err := RestoreRetailBattleCore(&RetailBattleStage{Session: s, StableUnit: stableUnitMap(ids), Image: image}); err != nil {
				t.Fatal(err)
			}
			s.publishSnapshot(0)
			if builds := s.Snapshot.Current().Builds; len(builds) != 0 {
				t.Fatalf("nonproducing reference published build progress: %+v", builds)
			}
		})
	}
}
