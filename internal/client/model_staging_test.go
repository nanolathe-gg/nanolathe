package client

import "testing"

// stagingBody builds a carrier image: `w` by `h` at the given anchor, every
// pixel covered with one colour and one key.
func stagingBody(w, h int, anchorX, anchorY int32, color, key uint8) *modelTarget {
	t := newModelImage(w, h, 0, 0, anchorX, anchorY, true, 1)
	for i := range t.color {
		t.color[i], t.covered[i], t.height[i] = color, true, key
	}
	return t
}

// TestStagingBoxIsTheUnionOfCarrierAndChildren locks the staging box of
// [R-REN-03A §4]: the union of the carrier's own box with the boxes of all its
// attached children, offset by each child's position relative to the carrier.
//
// The images already carry that offset in their framebuffer anchors, so the
// union is taken in framebuffer space and the carrier's anchor is kept.
func TestStagingBoxIsTheUnionOfCarrierAndChildren(t *testing.T) {
	body := stagingBody(4, 4, 100, 100, 10, 60)
	// A child four pixels right and three up of the carrier's own origin.
	child := stagingBody(4, 4, 104, 97, 20, 60)

	staging := newStagingImage(body, []stagingChild{{model: composedModel{image: child}}})
	if staging == nil {
		t.Fatal("no staging image")
	}
	// Carrier spans screen x 100..103, y 100..103; child spans 104..107, 97..100.
	if got := staging.screenX(0); got != 100 {
		t.Fatalf("staging left edge = %d, want the carrier's 100", got)
	}
	if got := staging.screenY(0); got != 97 {
		t.Fatalf("staging top edge = %d, want the child's 97", got)
	}
	if staging.width != 8 || staging.heightPx != 7 {
		t.Fatalf("staging box = %dx%d, want the union 8x7", staging.width, staging.heightPx)
	}
	if staging.anchorX != body.anchorX || staging.anchorY != body.anchorY {
		t.Fatal("the staging image must keep the carrier's anchor")
	}
	if staging.height == nil {
		t.Fatal("a carrier with a key plane must stage with one")
	}
	// The cached image is copied in, both planes.
	i := int(staging.imageY(100))*staging.width + int(staging.imageX(100))
	if staging.color[i] != 10 || staging.height[i] != 60 || !staging.covered[i] {
		t.Fatalf("carrier pixel not copied into the staging image: colour=%d key=%d covered=%v", staging.color[i], staging.height[i], staging.covered[i])
	}
}

// TestStagingCarrierWithoutAKeyPlaneStagesWithoutOne locks that the staging
// image follows the carrier's own plane: with no key plane there is nothing to
// resolve against, which is the painter path of [R-REN-03A §4].
func TestStagingCarrierWithoutAKeyPlaneStagesWithoutOne(t *testing.T) {
	body := newModelImage(4, 4, 0, 0, 100, 100, false, 1)
	staging := newStagingImage(body, nil)
	if staging.height != nil {
		t.Fatal("a keyless carrier must not stage a key plane")
	}
}

// TestCompositeChildResolvesOnTheKeyTest locks the per-pixel admission of
// [R-REN-03A §4] step 2: a child pixel is written when it is not the child
// image's transparent index and `stagingKey <= childKey + heightDelta`. Equal
// keys admit, so the child — composited later — wins a tie.
func TestCompositeChildResolvesOnTheKeyTest(t *testing.T) {
	cases := []struct {
		name              string
		carrierKey        uint8
		childKey          uint8
		delta             int32
		wantChild         bool
		wantResultingKey  uint8
		wantCarrierColour uint8
	}{
		{name: "child above the carrier", carrierKey: 60, childKey: 90, wantChild: true, wantResultingKey: 90},
		{name: "child below the carrier", carrierKey: 90, childKey: 60, wantChild: false, wantResultingKey: 90, wantCarrierColour: 10},
		{name: "equal keys admit the later child", carrierKey: 70, childKey: 70, wantChild: true, wantResultingKey: 70},
		{name: "the delta lifts a child that would lose", carrierKey: 90, childKey: 60, delta: 40, wantChild: true, wantResultingKey: 100},
		{name: "the delta drops a child that would win", carrierKey: 60, childKey: 90, delta: -40, wantChild: false, wantResultingKey: 60, wantCarrierColour: 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := stagingBody(2, 2, 100, 100, 10, tc.carrierKey)
			child := stagingBody(2, 2, 100, 100, 20, tc.childKey)
			staging := newStagingImage(body, []stagingChild{{model: composedModel{image: child}, keyDelta: tc.delta}})
			staging.compositeChild(child, tc.delta)

			i := int(staging.imageY(100))*staging.width + int(staging.imageX(100))
			if tc.wantChild {
				if staging.color[i] != 20 {
					t.Fatalf("colour = %d, want the child's 20", staging.color[i])
				}
			} else if staging.color[i] != tc.wantCarrierColour {
				t.Fatalf("colour = %d, want the carrier's %d", staging.color[i], tc.wantCarrierColour)
			}
			if staging.height[i] != tc.wantResultingKey {
				t.Fatalf("key = %d, want %d", staging.height[i], tc.wantResultingKey)
			}
		})
	}
}

// TestCompositeChildWritesWhereTheCarrierIsTransparent locks that the child
// still lands where the carrier's image never covered anything: the staging
// image's own background carries key zero, so the child's key admits.
func TestCompositeChildWritesWhereTheCarrierIsTransparent(t *testing.T) {
	body := stagingBody(2, 2, 100, 100, 10, 200)
	child := stagingBody(2, 2, 104, 100, 20, 30) // no overlap with the carrier
	staging := newStagingImage(body, []stagingChild{{model: composedModel{image: child}}})
	staging.compositeChild(child, 0)

	i := int(staging.imageY(100))*staging.width + int(staging.imageX(104))
	if staging.color[i] != 20 || !staging.covered[i] {
		t.Fatalf("child pixel outside the carrier was not written: colour=%d covered=%v", staging.color[i], staging.covered[i])
	}
}

// TestCompositeChildSkipsTheChildBackground locks the other half of the
// admission: a pixel the child image never covered is its transparent index and
// contributes nothing, so the carrier shows through [R-REN-03A §4].
func TestCompositeChildSkipsTheChildBackground(t *testing.T) {
	body := stagingBody(2, 2, 100, 100, 10, 60)
	child := newModelImage(2, 2, 0, 0, 100, 100, true, 1) // all background
	staging := newStagingImage(body, []stagingChild{{model: composedModel{image: child}}})
	staging.compositeChild(child, 0)

	i := int(staging.imageY(100))*staging.width + int(staging.imageX(100))
	if staging.color[i] != 10 {
		t.Fatalf("colour = %d, want the carrier's 10 to show through", staging.color[i])
	}
	if staging.height[i] != 60 {
		t.Fatalf("key = %d, want the carrier's 60 undisturbed", staging.height[i])
	}
}

// TestClampKeyByteHoldsTheStoreWidth locks the byte store's edges. See the
// TODO(question) on compositeChild: the clamp is this client's choice at a
// boundary stock content does not reach.
func TestClampKeyByteHoldsTheStoreWidth(t *testing.T) {
	for _, tc := range []struct {
		in   int32
		want uint8
	}{{-1, 0}, {0, 0}, {255, 255}, {256, 255}, {1000, 255}} {
		if got := clampKeyByte(tc.in); got != tc.want {
			t.Fatalf("clampKeyByte(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
