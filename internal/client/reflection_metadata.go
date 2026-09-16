package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// reflectionWaterAt admits Enhanced reflections over valid ordinary water
// (GPU design §26). The body's origin may be submerged: the executor clips
// physical corners, so an above-water piece still contributes.
func (c *Client) reflectionWaterAt(x, z numeric.Fixed) bool {
	// Reflections belong to the player's Water switch (§30): with it off no
	// site is admitted, so no reflected geometry is recorded at all.
	if c == nil || c.terrain == nil || !c.effects.Water {
		return false
	}
	t := c.terrain
	if len(t.Plot) == 0 || t.CellW <= 0 || t.CellH <= 0 || t.LavaWorld || (t.WaterDoesDamage != 0 && t.WaterDamage != 0) {
		return false
	}
	height := t.HeightAt(x, z)
	return height >= 0 && height < c.seaLevel()
}

func (c *Client) reflectionHeight(y numeric.Fixed) float32 {
	return float32((y - c.seaLevel()).Raw()) / 65536 * float32(c.modelScale().Float())
}

func (c *Client) setLineReflection(line *drawlist.Line, a, b render.ProjectilePoint) {
	line.ReflectWater = c.reflectionWaterAt(a.X, a.Z) || c.reflectionWaterAt(b.X, b.Z)
	line.ReflectionHeight0 = c.reflectionHeight(a.Y)
	line.ReflectionHeight1 = c.reflectionHeight(b.Y)
}

// lightingGround is the terrain height under a light source, in the recording
// units WorldHeight uses. The Enhanced ground pass subtracts it, so a pool is
// attenuated by the source's height above the GROUND rather than above the sea
// datum — the missing receiver height of DESIGN_GPU_RENDERER §31.3, without
// which a small pool is suppressed outright anywhere the map rises
// (§31.5, §31.7). Off-map or unknown terrain reports zero, which leaves the
// datum measurement in place.
func (c *Client) lightingGround(x, z numeric.Fixed, scale float32) float32 {
	// groundHeightUnder is the one presentation terrain sampler; it returns the
	// terrain's own out-of-bounds sentinel, which is negative, and zero when no
	// terrain is bound. Both mean "no receiver height" here.
	h := c.groundHeightUnder(x, z)
	if h <= 0 {
		return 0
	}
	return float32(h.Raw()) / 65536 * scale
}
