package units

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// StructureFacing is the Community structure-rotation index: south, east,
// north and west in increasing quarter turns. South is the zero value so
// untagged orders, AI construction and Strict 3.1 retain retail behavior.
// The persistent representation is Move.Heading; this value is derived
// creation geometry and is deliberately absent from the save projection.
type StructureFacing uint8

const (
	FacingSouth StructureFacing = iota
	FacingEast
	FacingNorth
	FacingWest
)

// FacingFromHeading recovers the nearest cardinal facing from a heading. The
// half-circle heading is south; adding one eighth-turn before division rounds
// authored build-angle jitter to the nearest quarter [community patch engine
// behavior, CP-CON-5].
func FacingFromHeading(heading uint16) StructureFacing {
	delta := uint16(heading - 32768)
	return StructureFacing(((uint32(delta) + 8192) & 0xffff) / 16384)
}

// HeadingWithFacing adds the selected quarter turn to the allocator-produced
// heading. It preserves the existing build-angle draw and its jitter.
func HeadingWithFacing(heading uint16, facing StructureFacing) uint16 {
	return heading + uint16(facing&3)*16384
}

// FacingMask returns the authored mask bit corresponding to facing.
func FacingMask(facing StructureFacing) content.FacingMask {
	return content.FacingSouth << (facing & 3)
}

// OrientedFootprint returns a definition's footprint for one cardinal facing.
// Quarter turns transpose the extents without changing the shared definition.
func OrientedFootprint(def *content.UnitDef, facing StructureFacing) (int32, int32) {
	if def == nil {
		return 0, 0
	}
	x, z := def.FootprintX, def.FootprintZ
	if facing&1 != 0 {
		x, z = z, x
	}
	return x, z
}

// OrientedYardMap parses the authored yard against its original dimensions,
// then returns an independently allocated cardinal rotation. The direction
// follows the heading convention: east is a counter-clockwise world rotation
// [community patch engine behavior, CP-CON-5]. UnitDef is never changed.
func OrientedYardMap(def *content.UnitDef, facing StructureFacing) ([]world.YardCell, error) {
	if def == nil || def.BMCode != 0 {
		return nil, fmt.Errorf("units: building yard unavailable")
	}
	w, h := int(def.FootprintX), int(def.FootprintZ)
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("units: invalid building footprint %dx%d", w, h)
	}
	src, err := world.ParseYardMap(def.YardMap, w, h)
	if err != nil {
		return nil, err
	}
	facing &= 3
	if facing == FacingSouth {
		return src, nil
	}
	newW, newH := w, h
	if facing&1 != 0 {
		newW, newH = h, w
	}
	dst := make([]world.YardCell, newW*newH)
	for nz := 0; nz < newH; nz++ {
		for nx := 0; nx < newW; nx++ {
			var ox, oz int
			switch facing {
			case FacingEast:
				ox, oz = w-1-nz, nx
			case FacingNorth:
				ox, oz = w-1-nx, h-1-nz
			case FacingWest:
				ox, oz = nz, h-1-nx
			}
			dst[nz*newW+nx] = src[oz*w+ox]
		}
	}
	return dst, nil
}
