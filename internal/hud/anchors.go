package hud

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/content"
)

// Rect is a corner rectangle stored verbatim as x1,y1,x2,y2 — not normalized,
// so x2 < x1 survives [02 §6 "SIDE and battle interface data"] C8 [07 §6].
type Rect struct {
	X1 int32
	Y1 int32
	X2 int32
	Y2 int32
}

// Anchors holds the 30 mandatory interface anchors, each stored verbatim as
// x1,y1,x2,y2 corners — never normalized [02 §6] C8 [07 §6].
// Order matches content.sideAnchorNames per [02 §6].
type Anchors [30]Rect

// AnchorNames is the exact 30 mandatory anchors [02 §6] C8 [07 §6].
// Order is the retail file order observed in gamedata/sidedata.tdf and used for
// hashing in content; hud preserves the same order for index stability.
var AnchorNames = [30]string{
	"LOGO",
	"ENERGYBAR",
	"ENERGYNUM",
	"ENERGYMAX",
	"ENERGY0",
	"METALBAR",
	"METALNUM",
	"METALMAX",
	"METAL0",
	"TOTALUNITS",
	"TOTALTIME",
	"ENERGYPRODUCED",
	"ENERGYCONSUMED",
	"METALPRODUCED",
	"METALCONSUMED",
	"LOGO2",
	"UNITNAME",
	"DAMAGEBAR",
	"UNITMETALMAKE",
	"UNITMETALUSE",
	"UNITENERGYMAKE",
	"UNITENERGYUSE",
	"MISSIONTEXT",
	"UNITNAME2",
	"DAMAGEBAR2",
	"NAME",
	"DESCRIPTION",
	"RELOAD1",
	"RELOAD2",
	"RELOAD3",
}

// Anchor index constants for direct array access [02 §6] C8.
const (
	AnchorLogo           = 0
	AnchorEnergyBar      = 1
	AnchorEnergyNum      = 2
	AnchorEnergyMax      = 3
	AnchorEnergy0        = 4
	AnchorMetalBar       = 5
	AnchorMetalNum       = 6
	AnchorMetalMax       = 7
	AnchorMetal0         = 8
	AnchorTotalUnits     = 9
	AnchorTotalTime      = 10
	AnchorEnergyProduced = 11
	AnchorEnergyConsumed = 12
	AnchorMetalProduced  = 13
	AnchorMetalConsumed  = 14
	AnchorLogo2          = 15
	AnchorUnitName       = 16
	AnchorDamageBar      = 17
	AnchorUnitMetalMake  = 18
	AnchorUnitMetalUse   = 19
	AnchorUnitEnergyMake = 20
	AnchorUnitEnergyUse  = 21
	AnchorMissionText    = 22
	AnchorUnitName2      = 23
	AnchorDamageBar2     = 24
	AnchorName           = 25
	AnchorDescription    = 26
	AnchorReload1        = 27
	AnchorReload2        = 28
	AnchorReload3        = 29
)

// AnchorIndex returns the array index for an anchor name (case-insensitive via
// content.CanonicalKey), and whether it exists [02 §6] C8.
func AnchorIndex(name string) (int, bool) {
	ck := content.CanonicalKey(name)
	for i, n := range AnchorNames {
		if content.CanonicalKey(n) == ck {
			return i, true
		}
	}
	return -1, false
}

// AnchorsFromSide loads verbatim anchors from a compiled SideDef [02 §6] C8 [07 §6].
// All 30 anchors are mandatory: a missing anchor returns a fatal error naming
// the missing section and side, per [02 §6] "missing anchor is a data error".
// Corners are stored verbatim — never normalized, so x2 < x1 survives.
func AnchorsFromSide(side *content.SideDef) (Anchors, error) {
	if side == nil {
		return Anchors{}, fmt.Errorf("hud: nil SideDef [02 §6] C8")
	}
	var out Anchors
	for i, name := range AnchorNames {
		r, ok := side.Anchor(name)
		if !ok {
			return Anchors{}, fmt.Errorf("hud: side %s missing anchor %s is fatal [02 §6] C8", side.Name, name)
		}
		out[i] = Rect{X1: r.X1, Y1: r.Y1, X2: r.X2, Y2: r.Y2}
	}
	return out, nil
}

// MustAnchors is like AnchorsFromSide but panics on error. Useful for tests
// and init-time side loading where a missing anchor is fatal [02 §6] C8.
func MustAnchors(side *content.SideDef) Anchors {
	a, err := AnchorsFromSide(side)
	if err != nil {
		panic(err)
	}
	return a
}

// ByName returns the rect for an anchor name if present.
func (a Anchors) ByName(name string) (Rect, bool) {
	idx, ok := AnchorIndex(name)
	if !ok {
		return Rect{}, false
	}
	return a[idx], true
}

// ByIndex returns the rect at idx, or zero if out of range.
func (a Anchors) ByIndex(idx int) (Rect, bool) {
	if idx < 0 || idx >= len(a) {
		return Rect{}, false
	}
	return a[idx], true
}

// Width returns X2 - X1 verbatim; may be negative if x2 < x1 [02 §6] C8.
func (r Rect) Width() int32 { return r.X2 - r.X1 }

// Height returns Y2 - Y1 verbatim; may be negative if y2 < y1 [02 §6] C8.
func (r Rect) Height() int32 { return r.Y2 - r.Y1 }

// IsEmpty reports whether the rect has zero area in verbatim space.
func (r Rect) IsEmpty() bool { return r.X1 == r.X2 || r.Y1 == r.Y2 }

// Contains reports inclusive containment [07 §3] after ordering bounds for
// presentation. Storage remains verbatim; this helper normalizes only for test.
func (r Rect) Contains(x, y int32) bool {
	left, top, right, bottom := r.Ordered()
	return x >= left && x <= right && y >= top && y <= bottom
}

// Ordered returns left,top,right,bottom normalized to min/max for presentation.
// The stored Rect remains verbatim; this is only for drawing/hit-test math [07 §3] [07 §4].
func (r Rect) Ordered() (left, top, right, bottom int32) {
	left, right = r.X1, r.X2
	if left > right {
		left, right = right, left
	}
	top, bottom = r.Y1, r.Y2
	if top > bottom {
		top, bottom = bottom, top
	}
	return
}
