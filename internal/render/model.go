// Package render implements model draw [03 §2.4][03 §5.2] presentation only (I6).
//
// Contracts C10, C12, C13 plus the projectile offset math are owned here:
//   - C10 palette indices convert to RGBA at present time only; model lighting selects an SHD row [03 §4.3]
//   - C12 world-space draw positions from snapshot.Lerp(prev,cur,alpha) [03 §2.4]; angles sampled as committed [PLAN_06 C22]
//   - C13 per drawn unit cached orientation triple differs >7 triggers rebuild; dirty frame resets subtrees from pristine and reapplies ancestor-after-descendant [03 §5.2]; position never enters piece math [03 §5.2][03 §2.4] C24
//
// Uses internal/model Compose/FoldRootAngles [03 §2.4] and projectile reuse with yaw in Y, pitch in X each carrying -32768 offset [03 §5.2].
// Produces per-piece world transforms + primitive draw lists in load-fixed order [03 §2.4] C20 [GAP 02-A6].
// Presentation only — never writes sim state (I6).
package render

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// ModelOrientationThreshold is the per-axis delta that triggers a rebuild [03 §5.2] C13.
const ModelOrientationThreshold = 7 // [03 §5.2] C13

// ModelShadeMidRow is the placeholder SHD row until the selection formula is traced [03 §4.3].
// TODO(question): SHD row selection formula not traced [03 §4.3]; use identity mid row 16 [PLAN_13 Explicit unknowns] (A23).
// Canonical definition lives in shade.go SHDMidRow; this alias preserves API.
const ModelShadeMidRow = SHDMidRow // [03 §4.3] A23 alias

// halfCircle is the authored model-facing offset for projectile models [03 §5.2].
// Projectile yaw in Y and pitch in X each carry -32768 (-32768 == +32768 mod 65536 = 0x8000) [03 §5.2].
const halfCircle = 0x8000 // 32768 [03 §5.2]

// OrientationCache holds the cached orientation triple for one drawn unit [03 §5.2] C13.
// The renderer compares its cached triple against the unit's bank, heading, pitch;
// when ANY axis differs by more than 7 it refreshes the cache and schedules a rebuild [03 §5.2] C13.
type OrientationCache struct {
	Heading uint16 // Y [03 §2.4] C24 [03 §5.2]
	Pitch   uint16 // X [03 §2.4] C24 [03 §5.2]
	Bank    uint16 // Z [03 §2.4] C24 [03 §5.2]
	Valid   bool
}

// angleDiff returns the minimal circular difference between two uint16 angles 0..32768 [04 §5.1].
func angleDiff(a, b uint16) int {
	d := int(a) - int(b)
	if d < 0 {
		d = -d
	}
	if d > 32768 {
		d = 65536 - d
	}
	return d
}

// NeedsRebuild reports whether any axis differs by more than 7 [03 §5.2] C13.
func (c *OrientationCache) NeedsRebuild(heading, pitch, bank uint16) bool {
	if c == nil {
		return true
	}
	if !c.Valid {
		return true
	}
	if angleDiff(c.Heading, heading) > ModelOrientationThreshold {
		return true
	}
	if angleDiff(c.Pitch, pitch) > ModelOrientationThreshold {
		return true
	}
	if angleDiff(c.Bank, bank) > ModelOrientationThreshold {
		return true
	}
	return false
}

// Update refreshes the cache when NeedsRebuild is true and reports whether a rebuild was scheduled [03 §5.2] C13.
// The cache is refreshed at most once per dirty detection; callers use the return to schedule subtree rebuild [03 §5.2].
func (c *OrientationCache) Update(heading, pitch, bank uint16) bool {
	if c == nil {
		return false
	}
	if c.NeedsRebuild(heading, pitch, bank) {
		c.Heading = heading
		c.Pitch = pitch
		c.Bank = bank
		c.Valid = true
		return true
	}
	return false
}

// FoldProjectileAngles folds projectile orientation into the ROOT piece's accumulators [03 §5.2].
// Projectile models reuse the identical rotation helper — yaw in Y, pitch in X, each with constant -32768 offset [03 §5.2].
// The offset is -32768 == +32768 mod 65536 (0x8000) so yaw-32768 wraps [03 §5.2].
// Presentation only; caller must operate on a presentation copy, never sim state (I6).
func FoldProjectileAngles(st []model.PieceState, root int, yaw, pitch uint16) { // [03 §5.2]
	if root < 0 || root >= len(st) {
		return
	}
	st[root].RotY += yaw + halfCircle   // [03 §5.2] Y = yaw with -32768 offset (wrapping via uint16 overflow)
	st[root].RotX += pitch + halfCircle // [03 §5.2] X = pitch with -32768 offset
}

// FoldPropellerSpin folds a propeller-style spin angle through the same slot machinery [03 §5.2].
// TODO(question): propeller variant slot (Y vs Z) and whether it carries the -32768 offset not fully established [03 §5.2]; current feeds Y without offset as generic spin.
func FoldPropellerSpin(st []model.PieceState, piece int, spin uint16) { // [03 §5.2]
	if piece < 0 || piece >= len(st) {
		return
	}
	st[piece].RotY += spin // [03 §5.2] same slot machinery
}

// BuildUnitPieceStates returns a presentation copy of base with unit orientation folded into the root [03 §2.4] C24 [03 §5.2] C13.
// bank→Z, heading→Y, pitch→X as the outermost factor [03 §2.4] C24; position never enters piece math [03 §5.2][03 §2.4] C24.
// The returned slice is a new allocation so the caller's base is not mutated (I6).
func BuildUnitPieceStates(m *model.Model, base []model.PieceState, heading, pitch, bank uint16) []model.PieceState { // [03 §2.4] C24 [03 §5.2]
	if m == nil {
		return nil
	}
	n := len(m.Pieces)
	out := make([]model.PieceState, n)
	if base != nil {
		copy(out, base)
	}
	model.FoldRootAngles(out, m.Root, heading, pitch, bank) // [03 §2.4] C24 bank→Z heading→Y pitch→X
	return out
}

// BuildProjectilePieceStates returns a presentation copy with projectile yaw/pitch folded [03 §5.2].
// Each carries -32768 offset [03 §5.2]; presentation only (I6).
func BuildProjectilePieceStates(m *model.Model, base []model.PieceState, yaw, pitch uint16) []model.PieceState { // [03 §5.2]
	if m == nil {
		return nil
	}
	n := len(m.Pieces)
	out := make([]model.PieceState, n)
	if base != nil {
		copy(out, base)
	}
	FoldProjectileAngles(out, m.Root, yaw, pitch) // [03 §5.2]
	return out
}

// UnitTransforms returns per-piece world transforms for all pieces in stable index order [03 §2.4] C21 (I1).
// Each transform composes as ordered in-place rotate-then-translate, ancestors after descendants, via float trig round-to-nearest [03 §2.4] C21 (I2).
func UnitTransforms(m *model.Model, states []model.PieceState) []model.Transform { // [03 §2.4] C21
	if m == nil {
		return nil
	}
	n := len(m.Pieces)
	out := make([]model.Transform, n)
	for i := 0; i < n; i++ {
		out[i] = model.Compose(m, states, i) // [03 §2.4] C21 ancestor-after-descendant from pristine vertices
	}
	return out
}

// LerpUnitWorldPos interpolates world-space draw position from snapshot.Lerp [03 §2.4] C12 (I6).
// Piece rotation accumulators are sampled as committed — do not interpolate angles [03 §2.4] C12 [PLAN_06 C22].
func LerpUnitWorldPos(prev, cur snapshot.UnitView, alpha float32) [3]numeric.Fixed { // [03 §2.4] C12
	return [3]numeric.Fixed{
		snapshot.Lerp(prev.X, cur.X, alpha), // [03 §2.4] C12 sanctioned divergence I6
		snapshot.Lerp(prev.Y, cur.Y, alpha),
		snapshot.Lerp(prev.Z, cur.Z, alpha),
	}
}

// LerpWorldPos interpolates a single Fixed coordinate [03 §2.4] C12.
func LerpWorldPos(prev, cur numeric.Fixed, alpha float32) numeric.Fixed { // [03 §2.4] C12
	return snapshot.Lerp(prev, cur, alpha)
}

// PrimitiveDraw is one primitive draw record in load-fixed order [03 §2.4] C20 [GAP 02-A6].
// Geometry is in world space (transformed + lerpPos) for the renderer to project [03 §2.5][03 §5.2].
type PrimitiveDraw struct {
	ColorIndex    uint32
	TextureName   string
	IsColored     int32
	VertexIndices []uint16           // in load-fixed order [03 §2.4] C20
	WorldVerts    [][3]numeric.Fixed // world-space vertices for this primitive [03 §5.2] C13 position only at final placement [03 §2.4] C24
	ShadeRow      int                // placeholder mid row [03 §4.3] TODO(question)
}

// PieceDraw is the per-piece draw list for one unit piece [03 §2.4] C21 [03 §5.2].
type PieceDraw struct {
	Index            int
	Name             string
	Transform        model.Transform    // [03 §2.4] C21 without world pos (position never enters piece math) [03 §5.2]
	LocalOrigin      [3]numeric.Fixed   // transform.Origin [03 §2.4] C21
	WorldOrigin      [3]numeric.Fixed   // LocalOrigin + lerpPos [03 §5.2] position only at final placement [03 §2.4] C24
	WorldVertices    [][3]numeric.Fixed // transformed vertices + lerpPos; leaf attachments surface here [03 §2.4] C23
	Primitives       []PrimitiveDraw    // load-fixed order [03 §2.4] C20
	IsLeafAttachment bool               // leaf with vertex but no primitive [03 §2.4] C23
	IsDirty          bool               // orientation cache triggered rebuild this frame [03 §5.2] C13
}

// UnitDraw is the per-unit model draw result presentation only (I6).
type UnitDraw struct {
	Model        *model.Model
	PieceStates  []model.PieceState // presentation copy with folded angles [03 §5.2]
	Transforms   []model.Transform  // per piece in stable order [03 §2.4] C21 (I1)
	Pieces       []PieceDraw        // per piece draw lists in stable order (I1) primitives in load-fixed order [03 §2.4] C20
	LerpPos      [3]numeric.Fixed   // interpolated world draw position [03 §2.4] C12
	NeedsRebuild bool               // whether orientation cache triggered rebuild [03 §5.2] C13
}

// BuildPieceDraws produces per-piece draw lists with world transforms and primitive lists in load-fixed order [03 §2.4] C20 [03 §5.2] presentation only (I6).
// lerpPos is the interpolated world position [03 §2.4] C12; piece math itself does not include it [03 §5.2][03 §2.4] C24.
// tables supplies the palette/SHD lookup at present time [03 §4.3] C10; pass nil for headless ordering tests.
func BuildPieceDraws(m *model.Model, states []model.PieceState, lerpPos [3]numeric.Fixed, dirty bool) []PieceDraw { // [03 §2.4] C21 [03 §5.2] C13
	if m == nil {
		return nil
	}
	transforms := UnitTransforms(m, states) // [03 §2.4] C21
	out := make([]PieceDraw, 0, len(m.Pieces))
	for i, piece := range m.Pieces {
		tr := transforms[i]
		localOrigin := tr.Origin // [03 §2.4] C21 without world pos [03 §5.2]
		worldOrigin := [3]numeric.Fixed{
			localOrigin[0].Add(lerpPos[0]), // position only at final placement [03 §2.4] C24 [03 §5.2]
			localOrigin[1].Add(lerpPos[1]),
			localOrigin[2].Add(lerpPos[2]),
		}
		// World vertices: transform each authored vertex via the same chain then offset by lerpPos [03 §2.4] C21
		worldVerts := make([][3]numeric.Fixed, len(piece.Vertices))
		for vi, v := range piece.Vertices {
			local := tr.Apply(v) // [03 §2.4] C21 from pristine vertices ancestor-after-descendant
			worldVerts[vi] = [3]numeric.Fixed{
				local[0].Add(lerpPos[0]),
				local[1].Add(lerpPos[1]),
				local[2].Add(lerpPos[2]),
			}
		}
		// Primitives in load-fixed order [03 §2.4] C20 [GAP 02-A6] — never resort here
		prims := make([]PrimitiveDraw, len(piece.Primitives))
		for pi, pr := range piece.Primitives {
			idxCopy := append([]uint16(nil), pr.VertexIndices...)
			worldPrimVerts := make([][3]numeric.Fixed, len(pr.VertexIndices))
			for k, vi := range pr.VertexIndices {
				if int(vi) < len(piece.Vertices) {
					localV := piece.Vertices[vi]
					worldLocal := tr.Apply(localV) // [03 §2.4] C21
					worldPrimVerts[k] = [3]numeric.Fixed{
						worldLocal[0].Add(lerpPos[0]),
						worldLocal[1].Add(lerpPos[1]),
						worldLocal[2].Add(lerpPos[2]),
					}
				}
			}
			prims[pi] = PrimitiveDraw{
				ColorIndex:    pr.ColorIndex,
				TextureName:   pr.TextureName,
				IsColored:     pr.IsColored,
				VertexIndices: idxCopy,
				WorldVerts:    worldPrimVerts,
				ShadeRow:      ModelShadeMidRow, // TODO(question) [03 §4.3]
			}
		}
		isLeaf := len(piece.Primitives) == 0 && len(piece.Vertices) > 0 // [03 §2.4] C23
		// Also leaf in hierarchy sense: sibling/child links depth-first [fmt 3do] — a piece with no primitives but with children is not a leaf;
		// the spec says leaf pieces with vertex but no primitive are valid attachments, so require no children as well.
		if isLeaf && len(piece.Children) != 0 {
			// Still allow: spec says "Leaf pieces" meaning no children; honor that strictly.
			isLeaf = false
		}
		// If leaf check above is too strict, also consider any piece with vertex but no primitive as emit-capable;
		// retain isLeaf true for those cases per [03 §2.4] wording that allows leaf interpretation.
		// For determinism preserve both: treat as leaf when primitives==0 && vertices>0 regardless of children?
		// Re-evaluate: earlier we cleared isLeaf when children !=0; but spec says leaf implies no children,
		// so a non-leaf with vertex+no primitive is not counted as leaf attachment by the strict leaf definition.
		// Keep the strict interpretation but also expose via helper EmitPoints for any piece.
		out = append(out, PieceDraw{
			Index:            i,
			Name:             piece.Name,
			Transform:        tr,
			LocalOrigin:      localOrigin,
			WorldOrigin:      worldOrigin,
			WorldVertices:    worldVerts,
			Primitives:       prims,
			IsLeafAttachment: isLeaf,
			IsDirty:          dirty,
		})
	}
	return out
}

// BuildUnitDraw builds the full per-unit draw for presentation [03 §2.4][03 §5.2] (I6).
// It copies base states, folds unit orientation bank→Z heading→Y pitch→X [03 §2.4] C24 [03 §5.2],
// optionally uses cache to decide rebuild, interpolates world pos via Lerp [03 §2.4] C12,
// and produces transforms and primitive lists in load-fixed order [03 §2.4] C20.
// Caller must supply a presentation copy of base states or nil; the function never writes sim state (I6).
// If cache is non-nil it is consulted and updated atomically (needs >7 any axis) [03 §5.2] C13.
func BuildUnitDraw(m *model.Model, base []model.PieceState, heading, pitch, bank uint16, prev, cur snapshot.UnitView, alpha float32, cache *OrientationCache) *UnitDraw { // [03 §2.4] C24 [03 §5.2] C13 [03 §2.4] C12
	if m == nil {
		return nil
	}
	needsRebuild := false
	if cache != nil {
		needsRebuild = cache.Update(heading, pitch, bank) // [03 §5.2] C13
	} else {
		// Without cache, treat as dirty if any orientation non-zero to force rebuild path coverage; but spec says per drawn unit comparison.
		// For determinism without cache, rebuild is false — caller must handle.
		needsRebuild = false
	}
	states := BuildUnitPieceStates(m, base, heading, pitch, bank) // [03 §2.4] C24
	lerpPos := LerpUnitWorldPos(prev, cur, alpha)                 // [03 §2.4] C12
	transforms := UnitTransforms(m, states)                       // [03 §2.4] C21
	pieces := BuildPieceDraws(m, states, lerpPos, needsRebuild)   // [03 §2.4] C20
	// BuildPieceDraws recomputed transforms internally; reuse the earlier transforms slice for determinism/stability.
	// Ensure the draws' transforms match the precomputed slice (they are recomputed identically, but preserve reference).
	for i := range pieces {
		if i < len(transforms) {
			pieces[i].Transform = transforms[i]
			pieces[i].LocalOrigin = transforms[i].Origin
			pieces[i].WorldOrigin = [3]numeric.Fixed{
				transforms[i].Origin[0].Add(lerpPos[0]),
				transforms[i].Origin[1].Add(lerpPos[1]),
				transforms[i].Origin[2].Add(lerpPos[2]),
			}
		}
	}
	return &UnitDraw{
		Model:        m,
		PieceStates:  states,
		Transforms:   transforms,
		Pieces:       pieces,
		LerpPos:      lerpPos,
		NeedsRebuild: needsRebuild,
	}
}

// BuildUnitDrawSimple builds a unit draw without interpolation or cache, for tests [03 §2.4] C24 [03 §5.2].
// lerpPos may be zero to test pure piece math without world offset [03 §2.4] C24; angles are as given.
func BuildUnitDrawSimple(m *model.Model, base []model.PieceState, heading, pitch, bank uint16, lerpPos [3]numeric.Fixed) *UnitDraw { // [03 §2.4] C24
	if m == nil {
		return nil
	}
	states := BuildUnitPieceStates(m, base, heading, pitch, bank)
	transforms := UnitTransforms(m, states)
	pieces := BuildPieceDraws(m, states, lerpPos, false)
	return &UnitDraw{
		Model:       m,
		PieceStates: states,
		Transforms:  transforms,
		Pieces:      pieces,
		LerpPos:     lerpPos,
	}
}

// BuildProjectileDraw builds a projectile draw with yaw/pitch offsets [03 §5.2] presentation only (I6).
func BuildProjectileDraw(m *model.Model, base []model.PieceState, yaw, pitch uint16, worldPos [3]numeric.Fixed) *UnitDraw { // [03 §5.2]
	if m == nil {
		return nil
	}
	states := BuildProjectilePieceStates(m, base, yaw, pitch) // [03 §5.2] each with -32768 offset
	transforms := UnitTransforms(m, states)
	pieces := BuildPieceDraws(m, states, worldPos, false)
	return &UnitDraw{
		Model:       m,
		PieceStates: states,
		Transforms:  transforms,
		Pieces:      pieces,
		LerpPos:     worldPos,
	}
}

// EmitPoint returns the world-space position of a vertex attachment on a piece [03 §2.4] C23.
// It applies the composed transform then adds the interpolated world position [03 §2.4] C21 [03 §5.2].
// Presentation only; returns false when piece or vertex index out of range.
func EmitPoint(m *model.Model, states []model.PieceState, pieceIdx int, vertexIdx int, lerpPos [3]numeric.Fixed) ([3]numeric.Fixed, bool) { // [03 §2.4] C23
	if m == nil || pieceIdx < 0 || pieceIdx >= len(m.Pieces) {
		return [3]numeric.Fixed{}, false
	}
	piece := m.Pieces[pieceIdx]
	if vertexIdx < 0 || vertexIdx >= len(piece.Vertices) {
		return [3]numeric.Fixed{}, false
	}
	tr := model.Compose(m, states, pieceIdx) // [03 §2.4] C21
	local := tr.Apply(piece.Vertices[vertexIdx])
	world := [3]numeric.Fixed{
		local[0].Add(lerpPos[0]),
		local[1].Add(lerpPos[1]),
		local[2].Add(lerpPos[2]),
	}
	return world, true
}

// LeafEmitPoints returns world-space emit points for all leaf attachments in the model [03 §2.4] C23.
// Each leaf that has a vertex but no primitive yields its first vertex transformed to world space [03 §2.4] C23.
// Order is stable piece-index ascending (I1).
func LeafEmitPoints(m *model.Model, states []model.PieceState, lerpPos [3]numeric.Fixed) [][3]numeric.Fixed { // [03 §2.4] C23
	if m == nil {
		return nil
	}
	var out [][3]numeric.Fixed
	for i, piece := range m.Pieces {
		if len(piece.Primitives) != 0 || len(piece.Vertices) == 0 {
			continue
		}
		if len(piece.Children) != 0 {
			continue // strict leaf [03 §2.4] C23
		}
		tr := model.Compose(m, states, i)
		for _, v := range piece.Vertices {
			local := tr.Apply(v)
			world := [3]numeric.Fixed{
				local[0].Add(lerpPos[0]),
				local[1].Add(lerpPos[1]),
				local[2].Add(lerpPos[2]),
			}
			out = append(out, world)
			break // one emit point per leaf piece (first vertex) [03 §2.4] C23
		}
	}
	return out
}

// AnyEmitPoints returns emit points for any piece with vertex but no primitive, even if non-leaf, for broader emit coverage [03 §2.4] C23.
// This helper surfaces attachment points even when the strict leaf check fails; caller can choose strict vs any.
func AnyEmitPoints(m *model.Model, states []model.PieceState, lerpPos [3]numeric.Fixed) [][3]numeric.Fixed { // [03 §2.4] C23
	if m == nil {
		return nil
	}
	var out [][3]numeric.Fixed
	for i, piece := range m.Pieces {
		if len(piece.Primitives) != 0 || len(piece.Vertices) == 0 {
			continue
		}
		tr := model.Compose(m, states, i)
		local := tr.Apply(piece.Vertices[0])
		world := [3]numeric.Fixed{
			local[0].Add(lerpPos[0]),
			local[1].Add(lerpPos[1]),
			local[2].Add(lerpPos[2]),
		}
		out = append(out, world)
	}
	return out
}

// PaletteRGBA resolves an indexed pixel to RGBA at present time [03 §4.3] C10 via the 256-byte Logical table [03 §4.3].
func PaletteRGBA(tables *palette.Tables, idx byte) (r, g, b, a uint8) { // [03 §4.3] C10
	if tables == nil {
		return 0, 0, 0, 255
	}
	return tables.RGBA(idx) // C10 logical→physical at present time [03 §4.3]
}

// ShadeRGBA resolves a palette index through an SHD row for model lighting [03 §4.3] C10.
// TODO(question): exact SHD row selection not established [03 §4.3]; callers should pass ModelShadeMidRow (16) placeholder [PLAN_13 Explicit unknowns].
func ShadeRGBA(tables *palette.Tables, idx byte, row int) (r, g, b, a uint8) { // [03 §4.3] C10
	if tables == nil {
		return 0, 0, 0, 255
	}
	if row < 0 {
		row = 0
	}
	if row >= 32 {
		row = 31
	}
	phys := tables.Logical[idx]       // [03 §4.3] logical→physical at present time
	shaded := tables.Shade[row][phys] // [03 §4.3] SHD row
	e := tables.Base[shaded]
	return e[0], e[1], e[2], 255
}

// PrimitiveRGBA resolves a primitive's color to RGBA, selecting shading per type [03 §4.3] C10.
// Flat-colored primitives and laser lines bypass SHD; textured primitives go through SHD [03 §4.3].
func PrimitiveRGBA(tables *palette.Tables, prim PrimitiveDraw) (r, g, b, a uint8) { // [03 §4.3] C10
	if prim.IsColored != 0 || prim.TextureName == "" {
		// Flat-colored bypasses SHD [03 §4.3]
		return PaletteRGBA(tables, byte(prim.ColorIndex&0xFF))
	}
	// Textured via SHD row placeholder [03 §4.3] TODO(question)
	return ShadeRGBA(tables, byte(prim.ColorIndex&0xFF), prim.ShadeRow)
}

// ModelProjectToScreen projects a world-space point to screen via the orthographic formula [03 §2.5].
// Uses camera.WorldToScreen with the half-height shear [03 §2.5].
func ModelProjectToScreen(cam *camera.Camera, world [3]numeric.Fixed) (sx, sy int32) { // [03 §2.5]
	if cam == nil {
		// Fallback without camera: worldX>>16 +128, worldZ>>16 - (worldY>>16)>>1 +32 [03 §2.5]
		wx := int32(int64(world[0]) >> 16)
		wy := int32(int64(world[1]) >> 16)
		wz := int32(int64(world[2]) >> 16)
		sx = wx + 128
		sy = wz - (wy >> 1) + 32
		return sx, sy
	}
	return cam.WorldToScreen(world[0], world[1], world[2]) // [03 §2.5]
}

// UnitScreenPositions returns screen positions for all piece world origins in stable order (I1) [03 §1] C3.
// It is presentation only (I6) and interpolates world position via Lerp [03 §2.4] C12.
func UnitScreenPositions(m *model.Model, states []model.PieceState, lerpPos [3]numeric.Fixed, cam *camera.Camera) [][2]int32 { // [03 §2.4] C12 [03 §2.5]
	if m == nil {
		return nil
	}
	transforms := UnitTransforms(m, states)
	out := make([][2]int32, len(transforms))
	for i, tr := range transforms {
		worldOrigin := [3]numeric.Fixed{
			tr.Origin[0].Add(lerpPos[0]),
			tr.Origin[1].Add(lerpPos[1]),
			tr.Origin[2].Add(lerpPos[2]),
		}
		sx, sy := ModelProjectToScreen(cam, worldOrigin) // [03 §2.5]
		out[i][0] = sx
		out[i][1] = sy
	}
	return out
}

// ComposerModelHook returns a hook closure for Composer.Hooks.UnitTraversal that drives model draw [03 §1][03 §5.2].
// It enumerates units in snapshot frame order (which is already Y-bucket plus stable slot order [03 §1] C3),
// checks the orientation cache, folds angles, builds draw lists, and would forward to the renderer.
// This is a seam helper; actual GPU blit is outside this package and remains presentation only (I6).
func ComposerModelHook(
	resolve func(defID uint16) *model.Model,
	stateResolve func(slot snapshot.UnitView) []model.PieceState,
	caches map[uint32]*OrientationCache,
	tables *palette.Tables,
	cam *camera.Camera,
	frame *snapshot.Frame,
	prevFrame *snapshot.Frame,
	alpha float32,
) func(kind string) {
	return func(kind string) {
		if frame == nil || resolve == nil {
			return
		}
		_ = tables
		_ = cam
		// Deterministic iteration: frame.Units already in stable pool ascending (I1) and bucket-ordered by Composer [03 §1] C3.
		for _, cur := range frame.Units {
			m := resolve(cur.DefID)
			if m == nil {
				continue
			}
			var states []model.PieceState
			if stateResolve != nil {
				states = stateResolve(cur) // presentation copy from COB VM (I6)
			}
			// Find prev view for same slot for Lerp [03 §2.4] C12
			var prev snapshot.UnitView
			if prevFrame != nil {
				for _, p := range prevFrame.Units {
					if p.Slot == cur.Slot {
						prev = p
						break
					}
				}
			} else {
				prev = cur
			}
			lerpPos := LerpUnitWorldPos(prev, cur, alpha) // [03 §2.4] C12
			// Cache per slot [03 §5.2] C13
			var cache *OrientationCache
			if caches != nil {
				cache = caches[uint32(cur.Slot)]
				if cache == nil {
					cache = &OrientationCache{}
					caches[uint32(cur.Slot)] = cache
				}
			}
			draw := BuildUnitDraw(m, states, cur.Heading, cur.Pitch, cur.Bank, prev, cur, alpha, cache) // [03 §2.4] C24 [03 §5.2]
			_ = draw
			_ = lerpPos
			// In a real renderer, draw.Pieces would be rasterized here in load-fixed primitive order [03 §2.4] C20
			// with palette at present time [03 §4.3] C10 and SHD mid row placeholder [03 §4.3] TODO(question).
		}
	}
}
