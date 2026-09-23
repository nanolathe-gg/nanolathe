package ebitenapp

// Double-click recognition at the platform edge.
//
// Retail never recognized a double-click itself: the window procedure received
// the operating system's own double-click message, and the interval and
// rectangle that produced it were the user's OS settings [07 R-WGT-01 §4]
// [01 R-PLAT-01 §6]. Ebiten polls devices and delivers no such message, so the
// pair has to be reconstructed here from the timestamps and positions the
// pointer records already carry. The values below are recorded HOST POLICY —
// the Windows defaults, not a retail measurement; see
// docs/DESIGN_INTERFACE_HUD_INPUT.md §5.
const (
	// doubleClickInterval is the 500 ms default interval expressed in the
	// scaled 30 Hz host-clock unit the pointer timestamp already uses
	// (500 × 30 / 1000 = 15). The unit quantizes the window to that clock;
	// the edge does not invent a finer host time than it publishes.
	doubleClickInterval uint32 = 15
	// doubleClickTolerance is half of the 4×4 pixel default rectangle: a
	// second press within ±2 surface pixels of the first still pairs.
	doubleClickTolerance int32 = 2
)

type doubleClickCandidate struct {
	armed bool
	x, y  int32
	at    uint32
}

// doubleClickRecognizer retains independent candidates for the left and right
// buttons. Interleaved presses therefore cannot complete a cross-button pair.
type doubleClickRecognizer struct {
	left  doubleClickCandidate
	right doubleClickCandidate
}

// press classifies one transition for the selected button and reports whether
// it completes a pair. A completed pair is consumed, so a third press inside
// the same window arms a new candidate instead of producing a triple. A press
// outside the interval or rectangle also becomes the new candidate, matching
// an OS recognizer that restarts on the press it rejected.
func (r *doubleClickRecognizer) press(left bool, x, y int32, at uint32) bool {
	candidate := &r.right
	if left {
		candidate = &r.left
	}
	if candidate.armed && at >= candidate.at && at-candidate.at <= doubleClickInterval &&
		within(x, candidate.x) && within(y, candidate.y) {
		candidate.armed = false
		return true
	}
	candidate.armed, candidate.x, candidate.y, candidate.at = true, x, y, at
	return false
}

func within(a, b int32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= doubleClickTolerance
}

// nativeDoubleClick is the production recognizer, one per process like the
// scroll collector. Tests drive applyInputWith with their own instance.
var nativeDoubleClick doubleClickRecognizer
