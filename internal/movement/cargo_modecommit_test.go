package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A rejected first ground commit preserves the carried mirror and hang height
// even when the cargo is upright [04 R-AIR-01 §10 item 2].
func TestCargoModeCommitRefusalKeepsHangHeight(t *testing.T) {
	s, w, carrier, cargo, _, _ := transportFixture(t)
	carrier.Y = numeric.Fixed(40 << 16)
	if !AttachCargo(w, carrier.Handle, cargo.Handle, -1) {
		t.Fatal("attach")
	}
	modeCommitVisit(s, cargo, 1)
	c := handleRow(s.Collisions, cargo.Handle)
	anchor := c.CachedAnchor
	s.releaseUnloadCargo(w, carrier, cargo)
	if cargo.Move.ModeMirror != 0 || c.CachedMode != 0 {
		t.Fatal("release changed mirror")
	}
	// The ground carrier stamp occupies the hang point, though the executor's
	// separately validated goal need not [04 R-AIR-01 §10 item 2].
	result := modeCommitVisit(s, cargo, 2)
	if !result.Blocked || cargo.Y != numeric.Fixed(40<<16) || cargo.Move.ModeMirror != 0 || c.CachedMode != 0 || c.CachedAnchor != anchor {
		t.Fatal("failed cargo ground commit lost carried mirror or hang height")
	}
	s.SetMoverMode(carrier, 2)
	modeCommitVisit(s, carrier, 3)
	result = modeCommitVisit(s, cargo, 4)
	if result.Blocked || cargo.Move.ModeMirror != 1 || cargo.Y != s.Terrain.HeightAt(cargo.X, cargo.Z) {
		t.Fatal("accepted ground commit did not settle cargo")
	}
}

// The shared helper enforces the runtime class gate for script callers too
// [04 R-UNIT-06 §3]. Definition capability flags cannot override it.
func TestCargoModeCommitScriptRejectsBuildingChild(t *testing.T) {
	s, w, carrier, cargo, _, _ := transportFixture(t)
	cargo.Flags |= units.BuildingClassStatus
	oldMode := cargo.Move.Mode
	if AttachCargoMode(w, carrier.Handle, cargo.Handle, 0, 0) || s.ScriptAttachCargo(w, carrier.Handle, cargo.Handle, 0, 0) {
		t.Fatal("building-class child was attached")
	}
	if cargo.Attachment.Carrier != 0 || len(carrier.Attachment.Cargo) != 0 || cargo.Move.Mode != oldMode {
		t.Fatal("rejected attachment changed linkage or mode")
	}
}
