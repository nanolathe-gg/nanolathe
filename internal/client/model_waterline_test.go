package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestWaterlineSplitAndTint locks the whole of [R-REN-03A §8]'s waterline pass
// against [R-WATER-01 §2] and [R-RAST-01 §4]: where the split falls, that it is
// inclusive, that the two arms are selected by ownership rather than by
// geometry, and that the tint arm leaves the image background alone.
//
// The split is the part that regresses silently. `t = seaLevel - hi16(unitY)`
// and the threshold is `t + 50`, on the same base the height key carries, so a
// hull whose origin sits four world units under the surface has its lowest 54
// key steps below water. Off-by-one here reads as "the tint is roughly right",
// which is why the boundary keys are asserted either side rather than sampled.
func TestWaterlineSplitAndTint(t *testing.T) {
	const sea, unitY = 10, 6 // four world units of the hull are submerged
	threshold, submerged := waterlineThreshold(numeric.Fixed(sea<<16), numeric.Fixed(unitY<<16), false)
	if !submerged {
		t.Fatal("a unit four units under the surface is not submerged")
	}
	if want := uint8(4 + presentationrender.NanoframeHeightBias); threshold != want {
		t.Fatalf("threshold = %d, want %d (t + 50)", threshold, want)
	}

	// A Digger raises every key by 75, so its threshold moves with them
	// [R-REN-03A §8].
	if got, _ := waterlineThreshold(numeric.Fixed(sea<<16), numeric.Fixed(unitY<<16), true); got != threshold+uint8(diggerKeyBias) {
		t.Fatalf("digger threshold = %d, want %d", got, threshold+uint8(diggerKeyBias))
	}

	// The pass runs only when t is strictly positive. A hull exactly at the
	// surface is dry: t == 0 and nothing is selected.
	if _, ok := waterlineThreshold(numeric.Fixed(sea<<16), numeric.Fixed(sea<<16), false); ok {
		t.Fatal("a unit exactly at sea level ran the waterline pass")
	}
	if _, ok := waterlineThreshold(numeric.Fixed(sea<<16), numeric.Fixed((sea+1)<<16), false); ok {
		t.Fatal("a unit above sea level ran the waterline pass")
	}
	// Both narrowings floor, so a unit a fraction below a whole height rounds
	// down and is a whole unit deeper, not a fraction.
	if got, _ := waterlineThreshold(numeric.Fixed(sea<<16), numeric.Fixed(unitY<<16)-1, false); got != threshold+1 {
		t.Fatalf("threshold just under %d = %d, want %d (floor)", unitY, got, threshold+1)
	}

	// The compare is inclusive: the pixel exactly at the threshold is below the
	// water plane, the next key up is above it.
	blue := &[256]byte{}
	for i := range blue {
		blue[i] = byte(255 - i) // a stand-in permutation; the table's own build is palette_test's
	}
	img := newModelImage(3, 1, 0, 0, 0, 0, true, 1)
	img.height[0], img.color[0], img.covered[0] = threshold, 40, true
	img.height[1], img.color[1], img.covered[1] = threshold+1, 41, true
	// A background pixel below the waterline: the recolour helper skips pixels
	// already equal to the image's transparent index, so it must survive
	// untouched rather than be painted a blue the blit would then stamp down.
	img.height[2], img.color[2], img.covered[2] = threshold, transparentModelIndex, false

	tinted := *img
	tinted.color = append([]uint8(nil), img.color...)
	tinted.covered = append([]bool(nil), img.covered...)
	tinted.tintAtOrBelow(threshold, blue)
	if got := tinted.color[0]; got != blue[40] {
		t.Fatalf("submerged pixel = %d, want the blue-table entry %d", got, blue[40])
	}
	if !tinted.covered[0] {
		t.Fatal("the tint dropped coverage; a tinted pixel is still a drawn pixel")
	}
	if got := tinted.color[1]; got != 41 {
		t.Fatalf("pixel one key above the waterline = %d, want the untinted 41", got)
	}
	if got := tinted.color[2]; got != transparentModelIndex {
		t.Fatalf("background below the waterline = %d, want the transparent index left alone", got)
	}

	// The other arm cuts the same pixels off instead of recolouring them.
	img.eraseAtOrBelow(threshold)
	if img.covered[0] || img.color[0] != transparentModelIndex {
		t.Fatalf("erase arm kept the submerged pixel: colour %d covered=%v", img.color[0], img.covered[0])
	}
	if !img.covered[1] || img.color[1] != 41 {
		t.Fatalf("erase arm cut above the waterline: colour %d covered=%v", img.color[1], img.covered[1])
	}
}

// TestWaterlineArmSelection locks the erase-versus-tint bit of [R-RAST-01 §4].
// It is an ownership question, not a geometry one, and the two halves are
// genuinely separate: the sensor sweep skips the viewer's own units when it
// sets the sonar-contact bit, so a unit you own carries no contact bit and
// still has to be tinted rather than cut off. A 3DO feature is tinted always.
func TestWaterlineArmSelection(t *testing.T) {
	const viewer, enemy uint8 = 0, 1
	buf := frame.NewBuffer()
	buf.BeginWrite().Selection = frame.SelectionView{LocalPlayer: viewer}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	c := &Client{buffer: buf}

	own := &presentationrender.UnitDraw{}
	if !c.waterlineTints(own, viewer, modelCursorUnit) {
		t.Fatal("a unit the viewer owns must be tinted, not cut off")
	}
	if c.waterlineTints(own, enemy, modelCursorUnit) {
		t.Fatal("an enemy unit with no sonar contact must be cut off at the surface")
	}
	onSonar := &presentationrender.UnitDraw{SonarContact: true}
	if !c.waterlineTints(onSonar, enemy, modelCursorUnit) {
		t.Fatal("an enemy unit held on sonar must be tinted")
	}
	if !c.waterlineTints(own, enemy, modelCursorFeature) {
		t.Fatal("a 3DO feature is always tinted, never cut [R-RAST-01 §6]")
	}
}
