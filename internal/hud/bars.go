package hud

import "github.com/nanolathe/nanolathe/internal/sim/numeric"

// Bars: health/metal/energy bar geometry from the anchor data per [02 §6]
// "SIDE and battle interface data" and [07 §6] battle-HUD. Pure presentation-side
// math (no sim reads beyond passed-in values). Anchors are stored verbatim as
// x1,y1,x2,y2 corners, never normalized [02 §6] C8 [07 §6]; bar-fill helpers
// normalize only for drawing via Rect.Ordered, preserving verbatim storage.

// clampFrac clamps fraction to [0,1].
func clampFrac(f float32) float32 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// BarFillHorizontal returns the filled sub-rectangle for a horizontal bar that
// fills left-to-right within the anchor's ordered bounds [02 §6] [07 §6].
// Fraction is clamped to [0,1]; width is trunc(fraction * width) toward zero
// per [01 R-DET-01 §1], retaining the low word. Height is preserved
// full. If the anchor is verbatim inverted (x2 < x1), Ordered normalizes it
// for presentation; the stored anchor remains verbatim.
func BarFillHorizontal(anchor Rect, fraction float32) Rect {
	fraction = clampFrac(fraction)
	left, top, right, bottom := anchor.Ordered()
	w := right - left
	if w <= 0 || fraction == 0 {
		return Rect{X1: left, Y1: top, X2: left, Y2: bottom}
	}
	if fraction >= 1 {
		return Rect{X1: left, Y1: top, X2: right, Y2: bottom}
	}
	// Truncate toward zero [01 §8]; w >=0 so trunc == floor.
	fill := numeric.TruncateFloat32ToLow32(fraction * float32(w))
	return Rect{X1: left, Y1: top, X2: left + fill, Y2: bottom}
}

// BarFillVertical returns the filled sub-rectangle for a vertical bar that
// fills top-to-bottom within the anchor's ordered bounds [02 §6] [07 §6].
// Fraction clamped [0,1]; height trunc(fraction * height) [01 §8]. Width full.
// Direction is top-to-bottom to match left-to-right horizontal convention for
// presentation; callers needing bottom-to-top can flip result.
func BarFillVertical(anchor Rect, fraction float32) Rect {
	fraction = clampFrac(fraction)
	left, top, right, bottom := anchor.Ordered()
	h := bottom - top
	if h <= 0 || fraction == 0 {
		return Rect{X1: left, Y1: top, X2: right, Y2: top}
	}
	if fraction >= 1 {
		return Rect{X1: left, Y1: top, X2: right, Y2: bottom}
	}
	fill := numeric.TruncateFloat32ToLow32(fraction * float32(h))
	return Rect{X1: left, Y1: top, X2: right, Y2: top + fill}
}

// EnergyBarFill is the ENERGYBAR geometry left-to-right [02 §6] [07 §6].
func EnergyBarFill(anchor Rect, fraction float32) Rect {
	return BarFillHorizontal(anchor, fraction)
}

// MetalBarFill is the METALBAR geometry left-to-right [02 §6] [07 §6].
func MetalBarFill(anchor Rect, fraction float32) Rect {
	return BarFillHorizontal(anchor, fraction)
}

// HealthBarFill is the DAMAGEBAR/DAMAGEBAR2 health geometry left-to-right
// [02 §6] [07 §6]. Health fraction is current/max clamped.
func HealthBarFill(anchor Rect, fraction float32) Rect {
	return BarFillHorizontal(anchor, fraction)
}

// EnergyBarFromAnchors is a convenience: fetch ENERGYBAR anchor by index and fill.
func EnergyBarFromAnchors(a Anchors, fraction float32) Rect {
	r, _ := a.ByIndex(AnchorEnergyBar)
	return EnergyBarFill(r, fraction)
}

// MetalBarFromAnchors fetches METALBAR anchor and fills.
func MetalBarFromAnchors(a Anchors, fraction float32) Rect {
	r, _ := a.ByIndex(AnchorMetalBar)
	return MetalBarFill(r, fraction)
}

// HealthBarFromAnchors fetches DAMAGEBAR and fills [02 §6] [07 §6].
func HealthBarFromAnchors(a Anchors, fraction float32) Rect {
	r, _ := a.ByIndex(AnchorDamageBar)
	return HealthBarFill(r, fraction)
}

// Fraction helpers: pure presentation math, truncated fraction [01 §8].
// No sim reads beyond passed-in values per WU-12-4.

// HealthFraction computes fraction from current/max hit points, clamped [0,1].
// Zero max returns 0 to avoid divide-by-zero; negative current clamped to 0.
func HealthFraction(current, max int32) float32 {
	if max <= 0 {
		return 0
	}
	f := float32(current) / float32(max)
	return clampFrac(f)
}

// ResourceFraction computes fraction for energy/metal bars from current/max
// stocks. Zero max returns 0; values beyond max clamp to 1.
func ResourceFraction(current, max float32) float32 {
	if max <= 0 {
		return 0
	}
	f := current / max
	return clampFrac(f)
}
