package client

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Authored asymmetric bounds make the northwest probe differ from both the
// draw base and any half-span reconstruction [06 §3.1]. Each mode lights one
// tile only; boundary/depth/hidden cases lock direct-versus-frame parity.
func TestSnapshotVisibilityHullMatchesDirectGate(t *testing.T) {
	const unit = numeric.FixedOne
	base := visibility.Target{Owner: 1, X: 64 * unit, Y: 19 * unit, Z: 96 * unit}
	min := [3]int32{-25 << 16, -3 << 16, -41 << 16}
	max := [3]int32{22 << 16, 2 << 16, 38 << 16}
	hull := visibility.TargetFromBounds(base, min, max)
	v := frame.UnitView{Owner: 1, X: base.X, Y: base.Y, Z: base.Z,
		HullOffsetX: numeric.Fixed(min[0]), HullOffsetY: numeric.Fixed(max[1]), HullOffsetZ: numeric.Fixed(min[2]),
		HullXExtent: hull.XExtent, HullYExtent: hull.YExtent, HullZExtent: hull.ZExtent}
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprintf("current=%v", current), func(t *testing.T) {
			mode := visibility.ModeHistoryEnabled
			if current {
				mode |= visibility.ModeCurrentEnabled
			}
			ter := &world.Terrain{CellW: 16, CellH: 16, SeaLevel: 20}
			vis := visibility.New(ter, mode)
			// Top northwest (39,21,55) projects to (1,1). The draw base projects
			// to (2,2), and the other three hull corners occupy separate tiles.
			vis.ByteGrid(0)[1*int(vis.W)+1] = 1
			vis.WordMask()[1*int(vis.W)+1] = 1
			f := &frame.Frame{Visibility: frame.VisibilityView{Valid: true, W: vis.W, H: vis.H, CoverageBytes: current, SeaLevel: 20 * unit,
				Visible: append([]uint8(nil), vis.ByteGrid(0)...), WordVisible: append([]uint16(nil), vis.WordMask()...)}}
			check := func(name string, target visibility.Target, view frame.UnitView, want bool) {
				t.Helper()
				direct, snapshot := vis.IsVisible(0, target), SnapshotVisible(f, view, 0)
				if direct != want || snapshot != want {
					t.Fatalf("%s: live/frame=%v/%v, want %v", name, direct, snapshot, want)
				}
			}
			check("northwest surfaced top", hull, v, true)
			topAtSea, atSea := hull, v
			topAtSea.Y -= unit
			atSea.Y -= unit
			check("top exactly at sea", topAtSea, atSea, true)
			under, submerged := hull, v
			under.Y -= 2 * unit
			submerged.Y -= 2 * unit
			check("top below sea", under, submerged, false)
			under.Status = visibility.SonarBit
			submerged.UnderwaterExempt = true
			check("sonar exemption", under, submerged, true)
			hidden, cloaked := hull, v
			hidden.Hidden = true
			hidden.Status |= visibility.DecloakBit
			cloaked.Cloaked = true
			cloaked.Decloaking = true
			check("hidden despite timer", hidden, cloaked, false)
			hidden.Owner = 0
			cloaked.Owner = 0
			check("owner before hidden", hidden, cloaked, true)
			wrapped, wrappedView := hull, v
			wrapped.X += 65536 * unit
			wrappedView.X += 65536 * unit
			check("signed high-word wrap", wrapped, wrappedView, true)
			offMap, offMapView := hull, v
			offMap.X -= 32768 * unit
			offMapView.X -= 32768 * unit
			check("negative narrowed coordinate", offMap, offMapView, false)
		})
	}
}

func TestSnapshotVisibilityUsesCommittedRuleAnswer(t *testing.T) {
	f := &frame.Frame{ViewingPlayer: 2}
	v := frame.UnitView{Owner: 1, X: -16 << 16, DirectVisibilityKnown: true, DirectlyVisible: true}
	if !SnapshotVisible(f, v, 2) {
		t.Fatal("discarded the committed border-aircraft answer")
	}
	v.DirectlyVisible = false
	if SnapshotVisible(f, v, 2) {
		t.Fatal("ignored the committed hidden answer")
	}
	v.DirectlyVisible = true
	if SnapshotVisible(f, v, 3) {
		t.Fatal("used another viewing player's visibility answer")
	}
}
