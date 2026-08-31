package client

// Presentation-only resolution at the active Ebitengine frame boundary.
// Authored projectile/effect assets whose archive route is not published stay
// unresolved; choosing a guessed archive/entry would make the image look
// plausible while violating the clean-room contract [03 §5.4][I9].

import (
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

const projectileGAFPath = "anims/fx.gaf"

// The fixed engine slots are the only shared projectile GAF identities closed
// by the retail contract. Selector 4 intentionally binds the same `plasmasm`
// entry as selector 1 [06 R-WFX-01 §1][06 R-WFX-01 §4].
var projectileSelectorSequences = [...]string{
	"cannonshell",
	"plasmasm",
	"plasmamd",
	"ultrashell",
	"plasmasm",
}

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
		// GAF frames resolve only the fixed shared fx.gaf slots closed by the
		// retail trace. There is intentionally no AssetID or model-name fallback.
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
		ResolveGAF: c.resolveProjectileGAF,
	}
}

// ensureProjectileGAF performs one lazy lookup of the shared projectile bank.
// A failed load is cached for this client, matching the existing feature/fog
// cache boundary and ensuring one missing bank cannot cause repeated VFS work
// during a render loop [03 §4.4][I6].
func (c *Client) ensureProjectileGAF() *formats.GAF {
	if c == nil || c.projectileGAFLoaded {
		if c == nil {
			return nil
		}
		return c.projectileGAF
	}
	c.projectileGAFLoaded = true
	if c.modelFS == nil {
		return nil
	}
	gaf, err := formats.LoadGAFFile(c.modelFS, projectileGAFPath)
	if err != nil {
		c.projectileGAFErr = err
		return nil
	}
	c.projectileGAF = gaf
	return gaf
}

// resolveProjectileGAF resolves only exact shared fx.gaf entries. Empty or
// content-supplied identities do not select a fallback: the authoritative
// frame carries no published route for those cases [03 §5.4][I9].
func (c *Client) resolveProjectileGAF(req render.ProjectileGAFRequest) (*formats.GAFFrame, bool) {
	var name string
	switch {
	case req.Base:
		// Render types 1, 3, 4, and 6 all use frame 0 of the fixed shadow entry.
		if req.Family != render.RenderTypeBaseSpriteModel && req.Family != render.RenderTypeBaseModelDistinct && req.Family != render.RenderTypeSelectorGAF && req.Family != render.RenderTypeRecordOrientation {
			return nil, false
		}
		name = "shadow"
	case req.Family == render.RenderTypeSelectorGAF:
		if req.Sequence < 0 || req.Sequence >= int32(len(projectileSelectorSequences)) {
			return nil, false
		}
		name = projectileSelectorSequences[req.Sequence]
	case req.Family == render.RenderTypeLifetimeGAF:
		// Type 5 has one fixed sequence; its lifetime/frame arithmetic is
		// performed before this resolver is called [03 §5.4].
		if req.Sequence != 0 {
			return nil, false
		}
		name = "flamestream"
	default:
		// Type 2's lens is a startup-built displacement frame rather than a
		// shared fx.gaf entry. Its pixel mechanics remain owned by doc 03.
		return nil, false
	}

	gaf := c.ensureProjectileGAF()
	if gaf == nil || strings.TrimSpace(name) == "" {
		return nil, false
	}
	entry, ok := gaf.Find(name)
	if !ok || entry == nil || entry.FrameCount == 0 || len(entry.Frames) == 0 {
		return nil, false
	}
	if req.Frame < 0 || req.Frame >= int(entry.FrameCount) || req.Frame >= len(entry.Frames) {
		return nil, false
	}
	frame := entry.Frames[req.Frame].Frame
	if frame == nil {
		return nil, false
	}
	return frame, true
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
