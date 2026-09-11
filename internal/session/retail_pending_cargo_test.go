package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A passenger can become death-pending after its unit visit and remain attached
// through the save boundary. The reader attaches before restoring that latch
// [08 R-SAVE-02 §6][04 R-MOV-03 §1].
func TestRetailRestoreAttachedPendingDeath(t *testing.T) {
	src, ids := newRestoreCoreFixture(t, 2)
	cargo, carrier := src.Units.Unit(ids[0].handle), src.Units.Unit(ids[1].handle)
	if !movement.AttachCargoMode(src.Units, carrier.Handle, cargo.Handle, 3, 1) {
		t.Fatal("fixture attachment failed")
	}
	src.Units.Destroy(cargo.Handle, units.DeathKilled)
	if !cargo.Alive || !cargo.Dying || cargo.Attachment.Carrier != carrier.Handle {
		t.Fatal("fixture must retain the pending passenger until finalization")
	}
	image := &save.BattleImage{Units: pendingStateImage(t, src)}
	dst, restoredIDs := newRestoreCoreFixture(t, 2)
	stage := &RetailBattleStage{Session: dst, StableUnit: stableUnitMap(restoredIDs), Image: image}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatalf("restore attached pending death: %v", err)
	}
	restored := dst.Units.Unit(cargo.Handle)
	if !restored.Alive || !restored.Dying || restored.Flags&units.DeathPendingStatus == 0 {
		t.Fatal("restore discarded the passenger's pending death")
	}
	if restored.Attachment.Carrier != carrier.Handle || restored.Attachment.AttachPiece != 3 || restored.Move.Mode != 1 {
		t.Fatalf("restored passenger relationship/mode = %+v / %d", restored.Attachment, restored.Move.Mode)
	}
	children := dst.Units.Unit(carrier.Handle).Attachment.Cargo
	if len(children) != 1 || children[0] != cargo.Handle {
		t.Fatalf("restored carrier list = %v", children)
	}
	if movement.AttachCargoMode(dst.Units, carrier.Handle, cargo.Handle, 3, 1) {
		t.Fatal("ordinary live attach admitted a pending-death passenger")
	}
}
