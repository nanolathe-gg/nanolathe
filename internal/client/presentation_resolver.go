package client

// Presentation-only resolution at the active Ebitengine frame boundary.
// Authored projectile/effect assets whose archive route is not published stay
// unresolved; choosing a guessed archive/entry would make the image look
// plausible while violating the clean-room contract [03 §5.4][I9].

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// ProjectileVisibilityMode selects the published coverage representation.
// Byte coverage is the current local-player grid; zero selects the local bit
// in the one-point word grid [03 §5.4].
const ProjectileVisibilityModeBytes = 1

// ProjectileVisible evaluates exactly one gate for one projectile. The same
// sheared cell is used for either representation; no rendertype branch may
// call this again [03 §5.4].
func ProjectileVisible(v frame.VisibilityView, p frame.ProjectileView, mode uint8, localPlayer uint8) bool {
	return PointVisible(v, p.X, p.Y, p.Z, mode, localPlayer)
}

// PointVisible is the one-point coverage gate shared by every world-space
// presentation pixel: the projected tile is the world X and the world Z less
// half the world height, both in thirty-two unit tiles [03 §3.2][03 §5.4].
func PointVisible(v frame.VisibilityView, x, y, z numeric.Fixed, mode uint8, localPlayer uint8) bool {
	if !v.Valid || v.W <= 0 || v.H <= 0 {
		return false
	}
	px := int32(int16(int64(x) >> 16))
	py := int32(int16(int64(y) >> 16))
	pz := int32(int16(int64(z) >> 16))
	u := px >> 5
	row := (pz - (py >> 1)) >> 5
	if u < 0 || row < 0 || u >= v.W || row >= v.H {
		return false
	}
	idx := int(row*v.W + u)
	if mode&ProjectileVisibilityModeBytes != 0 || v.CoverageBytes {
		if _, ok := visibilityGridSize(v.W, v.H, len(v.Visible)); !ok {
			return false
		}
		return v.Visible[idx] != 0
	}
	if _, ok := visibilityGridSize(v.W, v.H, len(v.WordVisible)); !ok {
		return false
	}
	if localPlayer >= 10 {
		return false
	}
	return v.WordVisible[idx]&(uint16(1)<<localPlayer) != 0
}

// projectileDispatchOptions supplies only metadata established by the
// immutable publication boundary. Shared-GAF lookup remains unresolved because
// the committed view does not publish its archive/entry route; those families
// stay suppressed rather than selecting a synthetic sprite or palette byte.
func (c *Client) projectileDispatchOptions() render.ProjectileDispatchOptions {
	return render.ProjectileDispatchOptions{
		FrameCount: func(v frame.ProjectileView) (int, bool) {
			if v.FrameCount <= 0 {
				return 0, false
			}
			return int(v.FrameCount), true
		},
		Color: func(v frame.ProjectileView) (int32, int32, bool) {
			if !v.HasPrimaryColor {
				return 0, 0, false
			}
			var secondary int32
			if v.HasSecondaryColor {
				secondary = int32(v.SecondaryColor)
			}
			return int32(v.PrimaryColor), secondary, true
		},
		// GAF frames and segmented geometry are resolved by the battle asset
		// adapter. There is intentionally no compatibility fallback here.
		SegmentPoints: func(v frame.ProjectileView) (first, second []render.ProjectilePoint, ok bool) {
			if c == nil || c.crt == nil {
				return nil, nil, false
			}
			// The two passes consume the client's PRIVATE presentation CRT copy
			// in admission order; the authoritative session stream is never
			// drawn here (DET-01). No straight-line substitute is emitted when
			// the copy is absent [03 §5.4][I4].
			return render.SnapshotSegmentedPointPasses(v, c.crt)
		},
		ResolveGAF: func(req render.ProjectileGAFRequest) (*formats.GAFFrame, bool) {
			// TODO(question): the committed projectile view does not publish the
			// archive/entry route needed to resolve AssetID and sequence names.
			// Suppress only this unresolved instruction; global-GAF admission below
			// remains open so unrelated authored projectiles are not discarded.
			return nil, false
		},
	}
}

// effectDrawOptions keeps LHT admission terrain-bounded.  Authored LHT row,
// radius, and effect GAF sequence remain unresolved until the effect producer
// publishes them; DrawEffectViews therefore emits no fabricated effect.
func (c *Client) effectDrawOptions() EffectDrawOptions {
	return EffectDrawOptions{
		TerrainCoverage: c.terrainScreenCoverage,
	}
}

// terrainScreenCoverage identifies pixels that map to the loaded terrain
// rectangle in the shell's rebased screen coordinates.  It is intentionally
// evaluated against immutable world geometry and does not inspect the current
// indexed framebuffer, so LHT cannot brighten unit/effect/HUD pixels by
// accident.  Tile-level authored coverage beyond map bounds is not published
// by Terrain and remains TODO rather than guessed.
func (c *Client) terrainScreenCoverage(x, y int) bool {
	if c == nil || c.terrain == nil || c.cam == nil || c.terrain.CellW <= 0 || c.terrain.CellH <= 0 {
		return false
	}
	mapX := int64(x) + int64(c.cam.X)
	mapZ := int64(y) + int64(c.cam.Z)
	return mapX >= 0 && mapZ >= 0 && mapX < int64(c.terrain.CellW)*16 && mapZ < int64(c.terrain.CellH)*16
}
