package client

// Presentation-only adapters for the active Ebitengine frame.  These helpers
// intentionally expose unresolved authored projectile/effect assets as false;
// the snapshot currently does not publish the shared projectile GAF identity,
// selector sequence names, frame counts, weapon color bytes, or LHT geometry.
// Choosing a guessed archive/entry would make the image look plausible while
// violating the clean-room contract [03 §5.4][I9].

import (
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// projectileVisibility is the one-point projectile gate from the immutable
// local-player coverage grid [03 §3.2].  Session publication copies the local
// player's byte grid into Visibility.Visible, so no live visibility service or
// owner-side state is read here.  An invalid/missing grid rejects admission.
func projectileVisibility(v snapshot.VisibilityView) func(snapshot.ProjectileView) bool {
	return func(p snapshot.ProjectileView) bool {
		if !v.Valid || v.W <= 0 || v.H <= 0 || len(v.Visible) != int(v.W*v.H) {
			return false
		}
		px := int32(int16(int64(p.X) >> 16))
		py := int32(int16(int64(p.Y) >> 16))
		pz := int32(int16(int64(p.Z) >> 16))
		// Visibility uses the same signed map-pixel narrowing and half-height
		// shear as the authoritative one-point predicate.  The unsigned bounds
		// check is deliberate: negative projected coordinates fail admission.
		u := (px >> 5)
		row := (pz - (py >> 1)) >> 5
		if u < 0 || row < 0 || u >= v.W || row >= v.H {
			return false
		}
		return v.Visible[int(row*v.W+u)] != 0
	}
}

// projectileDispatchOptions supplies only metadata established by the
// immutable publication boundary.  The current snapshot has no authored
// shared-GAF key/frame-count/color publication, so unresolved families stay
// suppressed rather than selecting a synthetic sprite or palette byte.
func (c *Client) projectileDispatchOptions() render.ProjectileDispatchOptions {
	return render.ProjectileDispatchOptions{}
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
