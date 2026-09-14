package render

// Model draw [03 §2.4][03 §5.2] presentation only (I6).
//
// Contracts C10, C12, C13 plus the projectile offset math are owned here:
//   - C10 palette indices convert to RGBA at present time only; model lighting selects an SHD row [03 §4.3]
//   - C12 world-space draw positions from committed state [03 §2.4]; angles sampled as committed [PLAN_06 C22]
//   - C13 per drawn unit cached orientation triple differs >7 triggers rebuild; dirty frame resets subtrees from pristine and reapplies ancestor-after-descendant [03 §5.2]; position never enters piece math [03 §5.2][03 §2.4] C24
//
// Uses internal/model Compose/FoldRootAngles [03 §2.4] and projectile reuse with yaw in Y, pitch in X each carrying -32768 offset [03 §5.2].
// Produces per-piece world transforms + primitive draw lists in load-fixed order [03 §2.4] C20 [GAP 02-A6].
// Presentation only — never writes sim state (I6).

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// ModelOrientationThreshold is the per-axis delta that triggers a rebuild [03 §5.2] C13.
const ModelOrientationThreshold = 7 // [03 §5.2] C13

// PieceLane selects the published pieces a bounded composition pass may draw.
// An unfinished unit overrides every selector to All [03 R-REN-03A §4].
type PieceLane uint8

const (
	PieceLaneCached PieceLane = iota
	PieceLaneLive
	PieceLaneAll
)

// Includes reports whether a published piece belongs in lane. Hidden pieces
// are excluded by the geometry walk itself; this is only the cache-bit split.
func (l PieceLane) Includes(dontCache, underConstruction bool) bool {
	if underConstruction || l == PieceLaneAll {
		return true
	}
	if l == PieceLaneCached {
		return !dontCache
	}
	return dontCache
}

// halfCircle is the authored model-facing offset for projectile models [03 §5.2].
// Projectile yaw in Y and pitch in X each carry -32768 (-32768 == +32768 mod 65536 = 0x8000) [03 §5.2].
const halfCircle = 0x8000 // 32768 [03 §5.2]

// OrientationCache holds the cached orientation triple for one drawn unit [03 §5.2] C13.
// The renderer compares its cached triple against the unit's bank, heading, pitch;
// when ANY axis differs by more than 7 it refreshes the cache and reports a rebuild [03 §5.2] C13.
type OrientationCache struct {
	Model   string // model identity is part of the cached visual state [03 §5.2]
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
	return c.UpdateKey("", heading, pitch, bank)
}

// UpdateKey invalidates the orientation cache when either the model identity
// or any orientation axis changes beyond the strict seven-unit threshold
// [03 §5.2].
func (c *OrientationCache) UpdateKey(modelKey string, heading, pitch, bank uint16) bool {
	if c == nil {
		return false
	}
	if c.Model != modelKey {
		c.Model = modelKey
		c.Valid = false
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

// FoldPropellerSpin folds a model projectile's spinning propeller angle into
// the piece's ROLL slot [03 §5.2][06 R-WFX-01 §4].
//
// This previously read `st[piece].RotY += spin` under an open-question marker,
// "propeller variant slot (Y vs Z) and whether it carries the -32768 offset not
// fully established". The slot was wrong. A model projectile
// is drawn from a three-word angle block whose words are, in order, roll, yaw
// and pitch, applied about Z, Y and X respectively — the same bank→Z,
// heading→Y, pitch→X assignment the unit root fold uses. `FoldProjectileAngles`
// above writes the second and third words; the `propeller` weapon flag
// substitutes the record's spinning propeller angle for the **first** word,
// which is the roll word, so the spin belongs in Z and not in Y.
//
// The half-circle offset does not apply: only the yaw and pitch words carry the
// -32768 model-facing offset. The first word is used verbatim in every model
// render type, whether it holds the record's roll or the propeller angle
// [06 R-WFX-01 §4].
//
// Scope: retail substitutes the word for the model's CHILD piece only, and only
// while the projectile's expiry tick is still ahead of the current tick
// (strict). The parent model is drawn first, from the unsubstituted block, so a
// propeller weapon's body does not spin — only its propeller piece does. The
// angle itself advances a fixed 1,024 units per tick in the simulation
// [06 §6.1][06 §7.1]; nothing in the draw path advances it.
//
// The accumulate-rather-than-assign form matches `FoldProjectileAngles`: retail
// builds the block fresh each draw, and the presentation copy this folds into
// starts from the model's authored piece state.
//
// The helper below consumes the published word and deadline.  Selecting and
// drawing the conditional child remains REND-13 work; this helper deliberately
// leaves the parent's yaw, pitch and base roll untouched.
//
// Presentation only; caller must operate on a presentation copy, never sim state (I6).
func FoldPropellerSpin(st []model.PieceState, piece int, spin uint16) { // [03 §5.2][06 R-WFX-01 §4]
	if piece < 0 || piece >= len(st) {
		return
	}
	st[piece].RotZ += spin // [06 R-WFX-01 §4] block word 0 is the roll slot; no -32768 offset
}

// FoldPublishedPropellerSpin applies the committed propeller word to one
// already-selected child. The strict deadline is part of the renderer contract:
// equality suppresses the child [06 R-WFX-01 §4]. Child selection and dispatch
// remain the separate REND-13 integration.
func FoldPublishedPropellerSpin(st []model.PieceState, piece int, v frame.ProjectileView, now uint32) { // [06 R-WFX-01 §4]
	if !v.Propeller || now >= v.ExpiryTick {
		return
	}
	FoldPropellerSpin(st, piece, v.PropellerRoll)
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

// PrimitiveDraw is one primitive draw record in load-fixed order [03 §2.4] C20 [GAP 02-A6].
// Geometry is in world space (transformed + worldPos) for the renderer to project [03 §2.5][03 §5.2].
type PrimitiveDraw struct {
	ColorIndex    uint32
	TextureName   string
	IsColored     int32
	VertexIndices []uint16 // borrowed immutable compiled indices in load-fixed order [03 §2.4] C20
	ShadeRow      int      // first corner's SHD row, or NoShadeRow [R-RND-02A]
	ShadeRows     []int    // one SHD row per corner; nil on the unshaded path [R-RND-02A]
}

// DefaultModelLight is the shipped model light direction [03 §2.4.1].
var DefaultModelLight = [3]float64{-0.8, 1.0, 0.25}

// SetModelLight replaces the global shaded-model direction [03 §2.4.1].
// The stored binary32 multiplier 0.01 is exactly 5368709 / 2^29. Round
// its integer product once to binary32, then scale by the exact power of two;
// this avoids rounding the input integer or a working product prematurely.
func SetModelLight(a, b, c int32) {
	for i, value := range [3]int32{a, b, c} {
		DefaultModelLight[i] = float64(float32(int64(value)*5368709) * 0x1p-29)
	}
}

// ShadeRowForNormal computes one textured-face corner's SHD row. The normal
// is deliberately not renormalized: retail averages already-normalized face
// normals and applies the dot product to that average [03 §2.4.1].
func ShadeRowForNormal(normal, light [3]float64, dontShade bool) int {
	if dontShade {
		return 15
	}
	dot := normal[0]*light[0] + normal[1]*light[1] + normal[2]*light[2]
	return int(numeric.TruncateFloat64ToLow32(dot*5.0)) & 31 // [01 R-DET-01 §1]
}

func faceNormal(a, b, c [3]numeric.Fixed) [3]float64 {
	ax := float64((b[0] - a[0]).Raw())
	ay := float64((b[1] - a[1]).Raw())
	az := float64((b[2] - a[2]).Raw())
	bx := float64((b[0] - c[0]).Raw())
	by := float64((b[1] - c[1]).Raw())
	bz := float64((b[2] - c[2]).Raw())
	n := [3]float64{ay*bz - az*by, az*bx - ax*bz, ax*by - ay*bx}
	l := math.Sqrt(n[0]*n[0] + n[1]*n[1] + n[2]*n[2])
	if l == 0 {
		return [3]float64{0, 1, 0}
	}
	return [3]float64{n[0] / l, n[1] / l, n[2] / l}
}

// PieceDraw is the per-piece draw list for one unit piece [03 §2.4] C21 [03 §5.2].
type PieceDraw struct {
	Index int
	// SourceIndex is the immutable loaded-model piece index. It normally equals
	// Index, but bounded projectile calls retain it after selecting one piece,
	// so parent and child animated-texture cursors do not alias [03 §5.2].
	SourceIndex      int
	Name             string
	Transform        model.Transform    // [03 §2.4] C21 without world pos (position never enters piece math) [03 §5.2]
	LocalOrigin      [3]numeric.Fixed   // transform.Origin [03 §2.4] C21
	WorldOrigin      [3]numeric.Fixed   // LocalOrigin + worldPos [03 §5.2] position only at final placement [03 §2.4] C24
	WorldVertices    [][3]numeric.Fixed // transformed vertices + worldPos; leaf attachments surface here [03 §2.4] C23
	Primitives       []PrimitiveDraw    // load-fixed order [03 §2.4] C20
	IsLeafAttachment bool               // leaf with vertex but no primitive [03 §2.4] C23
	IsDirty          bool               // orientation cache triggered rebuild this frame [03 §5.2] C13
	// DontCache is the published inverse of the render-piece cache bit. The
	// model consumer uses it to select cached, live, or all lanes [03 R-REN-03A §4].
	DontCache bool
}

// UnitDraw is the per-unit model draw result presentation only (I6).
type UnitDraw struct {
	Model        *model.Model
	PieceStates  []model.PieceState // presentation copy with folded angles [03 §5.2]
	Transforms   []model.Transform  // per piece in stable order [03 §2.4] C21 (I1)
	Pieces       []PieceDraw        // per piece draw lists in stable order (I1) primitives in load-fixed order [03 §2.4] C20
	WorldPos     [3]numeric.Fixed   // committed world draw position [03 §2.4] C12
	NeedsRebuild bool               // whether orientation cache triggered rebuild [03 §5.2] C13
	// Structure is the instance class bit retail derives from BMcode=0. It
	// gates both the shaded piece renderer and the composition supersample
	// [R-RND-02A][R-REN-03A §6].
	Structure bool
	// KeyPlane requests the composition image's per-pixel height plane. Retail
	// allocates it when the definition authors ZBuffer, when the unit is under
	// construction, or when the caller asks for one outright [R-REN-03A §2].
	KeyPlane bool
	// CastsShadow is the resolved branch-specific model-shadow gate. Diggers
	// and mobile subjects require the vehicle gates; ordinary structures do
	// not test canhover or floater [R-REN-03D §1].
	CastsShadow bool
	// GroundY is the terrain height under the unit. The shadow is sheared by
	// it rather than by the unit's own height, which is what slides a shadow
	// across a slope [R-REN-03D §3].
	GroundY numeric.Fixed
	// Airborne selects the Enhanced aircraft shadow treatment (GPU design §34).
	Airborne bool
	// DiggerClip erases everything at or below the model origin, which is the
	// buried half of a pop-up defence [R-REN-03A §8].
	DiggerClip bool
	// SonarContact is the sensor phase's sonar-contact bit for this subject —
	// the same runtime bit the direct-visibility predicate consults to accept a
	// fully submerged unit [R-VIS-01 §4][R-VIS-01 §5]. It is one half of the
	// waterline pass's erase-versus-tint choice: a submerged unit the viewer
	// neither owns nor holds on sonar is cut off at the surface, while one it
	// owns or has on sonar is recoloured through the BLUE TABLE instead
	// [R-RAST-01 §4]. The other half is ownership, which the composer resolves
	// because it is the side that knows the viewing player.
	SonarContact bool
	// UnderConstruction overrides the cached/live filter [03 R-REN-03A §4].
	UnderConstruction bool
}

// BuildPieceDraws produces per-piece draw lists with world transforms and primitive lists in load-fixed order [03 §2.4] C20 [03 §5.2] presentation only (I6).
// worldPos is the committed world position [03 §2.4] C12; piece math itself does not include it [03 §5.2][03 §2.4] C24.
// tables supplies the palette/SHD lookup at present time [03 §4.3] C10; pass nil for headless ordering tests.
func BuildPieceDraws(m *model.Model, states []model.PieceState, worldPos [3]numeric.Fixed, dirty bool) []PieceDraw { // [03 §2.4] C21 [03 §5.2] C13
	out, _ := buildPieceDraws(m, states, worldPos, dirty, true)
	return out
}

// buildPieceDraws is the shared traversal product. UnitDraw constructors use
// the returned transform slice directly, avoiding a second per-piece copy;
// BuildPieceDraws remains the narrow compatibility wrapper for callers that
// only need draw records [03 §2.4] C21.
func buildPieceDraws(m *model.Model, states []model.PieceState, worldPos [3]numeric.Fixed, dirty, shaded bool) ([]PieceDraw, []model.Transform) {
	return buildPieceDrawsInto(m, states, worldPos, dirty, shaded, &DrawScratch{})
}
func buildPieceDrawsInto(m *model.Model, states []model.PieceState, worldPos [3]numeric.Fixed, dirty, shaded bool, scratch *DrawScratch) ([]PieceDraw, []model.Transform) { // [03 §2.4] C21 [03 §5.2] C13
	if m == nil {
		return nil, nil
	}
	scratch.transforms = reuseDrawSlice(scratch.transforms, len(m.Pieces))
	transforms := scratch.transforms
	// Every composition below reads one model and one state slice, so the
	// chain trig is evaluated once per piece instead of once per ancestor
	// visit [03 §2.4] C21.
	scratch.compose.BeginModel(len(m.Pieces))
	for i := range transforms {
		transforms[i] = model.ComposeInto(m, states, i, transforms[i], &scratch.compose)
	}
	scratch.pieces = reuseDrawSlice(scratch.pieces, len(m.Pieces))
	scratch.storage = reuseDrawSlice(scratch.storage, len(m.Pieces))
	out := scratch.pieces
	scratch.hidden = reuseDrawSlice(scratch.hidden, len(m.Pieces))
	scratch.hiddenState = reuseDrawSlice(scratch.hiddenState, len(m.Pieces))
	scratch.hiddenStack = reuseDrawSlice(scratch.hiddenStack, len(m.Pieces))
	resolveHidden(m, states, scratch.hidden, scratch.hiddenState, scratch.hiddenStack[:0])
	for i := range m.Pieces {
		piece := &m.Pieces[i]
		store := &scratch.storage[i]
		tr := &transforms[i]
		// The slot carries the previous subject's record, so every field is
		// written here; a suppressed piece keeps its index and drops the rest.
		record := &out[i]
		record.Index, record.SourceIndex, record.Name = i, i, piece.Name
		record.Transform, record.LocalOrigin = *tr, tr.Origin
		record.WorldOrigin = [3]numeric.Fixed{
			tr.Origin[0].Add(worldPos[0]), // position only at final placement [03 §2.4] C24 [03 §5.2]
			tr.Origin[1].Add(worldPos[1]),
			tr.Origin[2].Add(worldPos[2]),
		}
		record.IsDirty = dirty
		record.DontCache = i < len(states) && states[i].DontCache
		if scratch.hidden[i] {
			// Keep the stable piece index while suppressing all authored geometry
			// under a hidden piece [03 §2.4.1].
			record.WorldVertices, record.Primitives, record.IsLeafAttachment = nil, nil, false
			continue
		}
		// World vertices: transform each authored vertex via the same chain then offset by worldPos [03 §2.4] C21
		store.world = reuseDrawSlice(store.world, len(piece.Vertices))
		worldVerts := store.world
		// [03 §2.4] C21 from pristine vertices ancestor-after-descendant.
		tr.ApplyOffsetInto(worldVerts, piece.Vertices, worldPos)
		var rows []int
		if shaded {
			// The shaded renderer builds smooth normals from transformed piece
			// geometry. The average remains unnormalized [03 §2.4.1]. The
			// unshaded renderer does not read the per-piece shade bit [R-RND-02A].
			store.normals = reuseDrawSlice(store.normals, len(piece.Vertices))
			clear(store.normals)
			normals := store.normals
			for primitiveIndex := range piece.Primitives {
				pr := &piece.Primitives[primitiveIndex]
				if piece.Selection && primitiveIndex == 0 {
					continue // selection plate is not part of model lighting [03 §2.4.1]
				}
				if len(pr.VertexIndices) < 3 {
					continue
				}
				a, b, c := pr.VertexIndices[0], pr.VertexIndices[1], pr.VertexIndices[2]
				if int(a) >= len(worldVerts) || int(b) >= len(worldVerts) || int(c) >= len(worldVerts) {
					continue
				}
				n := faceNormal(worldVerts[a], worldVerts[b], worldVerts[c])
				for _, vi := range pr.VertexIndices {
					if int(vi) >= len(normals) {
						continue
					}
					normals[vi].sum[0] += n[0]
					normals[vi].sum[1] += n[1]
					normals[vi].sum[2] += n[2]
					normals[vi].count++
				}
			}
			store.rows = reuseDrawSlice(store.rows, len(normals))
			rows = store.rows
			dontShade := i < len(states) && states[i].DontShade
			for vi := range normals {
				if normals[vi].count == 0 {
					rows[vi] = SHDIdentityRow
					continue
				}
				avg := normals[vi].sum
				count := float64(normals[vi].count)
				avg[0] /= count
				avg[1] /= count
				avg[2] /= count
				rows[vi] = ShadeRowForNormal(avg, DefaultModelLight, dontShade)
			}
		}
		// Primitives in load-fixed order [03 §2.4] C20 [GAP 02-A6] — never resort here
		store.prims = reuseDrawSlice(store.prims, len(piece.Primitives))
		prims := store.prims
		if shaded {
			corners := 0
			for pi := range piece.Primitives {
				corners += len(piece.Primitives[pi].VertexIndices)
			}
			// Every corner lane below is assigned, including the unresolvable
			// ones, so the arena needs no blanket erase.
			store.shades = reuseDrawSlice(store.shades, corners)
		}
		shadeOffset := 0
		for pi := range piece.Primitives {
			pr := &piece.Primitives[pi]
			pd := &prims[pi]
			pd.ColorIndex = pr.ColorIndex
			pd.TextureName = pr.TextureName
			pd.IsColored = pr.IsColored
			pd.VertexIndices = pr.VertexIndices
			pd.ShadeRow = NoShadeRow
			pd.ShadeRows = nil
			if shaded {
				// Default to the DONT_SHADE pin (row 15) rather than an invented
				// mid row: row 15 is the one retail value this field can hold
				// before its real per-corner row is known below, and it is
				// overwritten by the first corner's trunc(dot*5)&31 result
				// whenever that corner is resolvable [03 R-RAST-01 §5].
				pd.ShadeRow = SHDIdentityRow
				end := shadeOffset + len(pr.VertexIndices)
				pd.ShadeRows = store.shades[shadeOffset:end:end]
				shadeOffset = end
				for k, vi := range pr.VertexIndices {
					if int(vi) >= len(rows) {
						pd.ShadeRows[k] = 0
						continue
					}
					pd.ShadeRows[k] = rows[vi]
				}
				if len(pr.VertexIndices) > 0 && int(pr.VertexIndices[0]) < len(rows) {
					pd.ShadeRow = rows[pr.VertexIndices[0]] // [03 R-RAST-01 §5] real trunc(dot*5)&31 row
				}
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
		record.WorldVertices, record.Primitives, record.IsLeafAttachment = worldVerts, prims, isLeaf
	}
	return out, transforms
}

// resolveHidden fills one hidden flag per piece: a piece is suppressed when it
// or any ancestor is hidden [03 §2.4.1]. Resolving the whole model at once
// costs one ancestor visit per piece; testing each piece independently walked
// and erased a per-piece marker array, which is quadratic in the piece count.
//
// A cyclic ancestor chain is malformed and every piece on it is suppressed,
// rather than allowing a presentation walk to run forever.
func resolveHidden(m *model.Model, states []model.PieceState, hidden []bool, state []uint8, stack []int) {
	const (
		unresolved = iota
		inProgress
		visible
		suppressed
	)
	clear(state)
	for i := range m.Pieces {
		if state[i] != unresolved {
			continue
		}
		stack = stack[:0]
		cur := i
		result := uint8(visible)
		for {
			if cur < 0 || cur >= len(m.Pieces) {
				break // the chain leaves the model: nothing above suppresses it
			}
			if s := state[cur]; s != unresolved {
				if s == visible {
					break
				}
				// inProgress closes a cycle; suppressed is an already resolved
				// hidden ancestor. Both suppress everything below.
				result = suppressed
				break
			}
			if cur < len(states) && states[cur].Hidden {
				state[cur] = suppressed
				result = suppressed
				break
			}
			state[cur] = inProgress
			stack = append(stack, cur)
			cur = m.Pieces[cur].Parent
		}
		for _, idx := range stack {
			state[idx] = result
		}
	}
	for i := range hidden {
		hidden[i] = state[i] == suppressed
	}
}

// BuildUnitDraw builds the full per-unit draw for presentation [03 §2.4][03 §5.2] (I6).
// It copies base states, folds unit orientation bank→Z heading→Y pitch→X [03 §2.4] C24 [03 §5.2],
// optionally uses cache to decide rebuild, projects the committed world position [03 §2.4] C12,
// and produces transforms and primitive lists in load-fixed order [03 §2.4] C20.
// Caller must supply a presentation copy of base states or nil; the function never writes sim state (I6).
// A supplied cache retains unit rotation until an axis differs by more than
// seven; the owner updates the reference after rebuilding [03 §5.2] C13.
func BuildUnitDraw(m *model.Model, base []model.PieceState, heading, pitch, bank uint16, current frame.UnitView, cache *OrientationCache) *UnitDraw {
	return BuildUnitDrawInto(m, base, heading, pitch, bank, current, cache, &DrawScratch{})
}

// BuildUnitDrawInto borrows scratch until its next use. It never mutates base or
// committed state; this only changes ownership of the presentation result.
func BuildUnitDrawInto(m *model.Model, base []model.PieceState, heading, pitch, bank uint16, current frame.UnitView, cache *OrientationCache, scratch *DrawScratch) *UnitDraw { // [03 §2.4] C24 [03 §5.2] C13
	if m == nil {
		return nil
	}
	needsRebuild := false
	if cache != nil {
		// The client advances this cache only after it actually rebuilds a
		// retained body. Advancing on every pose build would turn a sequence of
		// small turns into a perpetual <=7 delta and lose the strict threshold.
		needsRebuild = cache.Model != m.Name || cache.NeedsRebuild(heading, pitch, bank) // [03 §5.2] C13
	} else {
		// Without cache, treat as dirty if any orientation non-zero to force rebuild path coverage; but spec says per drawn unit comparison.
		// For determinism without cache, rebuild is false — caller must handle.
		needsRebuild = false
	}
	scratch.states = reuseDrawSlice(scratch.states, len(m.Pieces))
	states := scratch.states
	// The base states are copied in and only the tail the caller did not
	// supply has to be erased; erasing the whole slot first rewrote every
	// element twice.
	clear(states[copy(states, base):])
	if cache != nil && !needsRebuild {
		heading, pitch, bank = cache.Heading, cache.Pitch, cache.Bank
	}
	model.FoldRootAngles(states, m.Root, heading, pitch, bank) // [03 §2.4] C24
	worldPos := [3]numeric.Fixed{current.X, current.Y, current.Z}
	// BuildPieceDraws is the one traversal. Derive the public transform view from
	// its records so a frame cannot apply the hierarchy twice [03 §2.4] C21.
	// Script pose changes use the retained unit orientation until the strict
	// orientation threshold requests its refresh [03 R-COMP-01 §4][03 §5.2].
	// BMcode=0 selects the shaded piece renderer only while the global
	// display option is enabled; all other units take the no-SHD path
	// [R-RND-02A].
	pieces, transforms := buildPieceDrawsInto(m, states, worldPos, needsRebuild, !current.BMCode && Shading, scratch) // [03 §2.4] C20
	scratch.draw = UnitDraw{
		Model:        m,
		PieceStates:  states,
		Transforms:   transforms,
		Pieces:       pieces,
		WorldPos:     worldPos,
		NeedsRebuild: needsRebuild,
		// The published sonar/underwater-exemption bit is the waterline pass's
		// erase-versus-tint selector [R-RAST-01 §4]; it rides the committed unit
		// view, so nothing here reads live sensor state [I6].
		SonarContact:      current.UnderwaterExempt,
		UnderConstruction: current.BuildRemaining > 0,
	}
	return &scratch.draw
}

// BuildUnitDrawSimple builds a unit draw without interpolation or cache, for tests [03 §2.4] C24 [03 §5.2].
// worldPos may be zero to test pure piece math without world offset [03 §2.4] C24; angles are as given.
func BuildUnitDrawSimple(m *model.Model, base []model.PieceState, heading, pitch, bank uint16, worldPos [3]numeric.Fixed) *UnitDraw { // [03 §2.4] C24
	if m == nil {
		return nil
	}
	states := BuildUnitPieceStates(m, base, heading, pitch, bank)
	pieces, transforms := buildPieceDraws(m, states, worldPos, false, true)
	return &UnitDraw{
		Model:       m,
		PieceStates: states,
		Transforms:  transforms,
		Pieces:      pieces,
		WorldPos:    worldPos,
	}
}

// BuildProjectileModelPieces builds the bounded standalone model calls for a
// projectile: type 1 draws its root then its first child while the strict
// deadline permits it; other types draw the root only. This intentionally does not traverse grandchildren: retail's
// effect entry receives one model piece per call, and the projectile dispatcher
// supplies only the model header and its child slot [03 §5.4][03 R-COMP-02 §6].
func BuildProjectileModelPieces(m *model.Model, v frame.ProjectileView, now uint32) (parent, child *UnitDraw) { // [03 §5.2][06 R-WFX-01 §4]
	return BuildProjectileModelPiecesInto(m, v, now, &ProjectileScratch{}, &ProjectileScratch{})
}

// ProjectileScratch retains one standalone model call's storage: the detached
// one-piece model, its single piece state and the piece-draw arrays. A
// projectile's parent and child calls are live at the same time, so each takes
// its own slot; a slot is valid until it is borrowed again.
type ProjectileScratch struct {
	model  model.Model
	pieces [1]model.Piece
	states [1]model.PieceState
	draw   DrawScratch
	result UnitDraw
}

// BuildProjectileModelPiecesInto is BuildProjectileModelPieces over borrowed
// storage, so a frame that draws a hundred projectiles allocates nothing per
// projectile. parentScratch and childScratch must be distinct slots.
func BuildProjectileModelPiecesInto(m *model.Model, v frame.ProjectileView, now uint32, parentScratch, childScratch *ProjectileScratch) (parent, child *UnitDraw) { // [03 §5.2][06 R-WFX-01 §4]
	if m == nil || m.Root < 0 || m.Root >= len(m.Pieces) {
		return nil, nil
	}
	pitch := v.Pitch
	if v.Meteor {
		pitch = v.MeteorPitch
	}
	modelFacing := v.RenderType != RenderTypeRecordOrientation
	worldPos := [3]numeric.Fixed{v.X, v.Y, v.Z}
	parent = buildProjectileStandalonePiece(m, m.Root, v.Roll, v.Yaw, pitch, modelFacing, worldPos, nil, now, parentScratch)
	if v.RenderType != RenderTypeBaseSpriteModel || now >= v.ExpiryTick || len(m.Pieces[m.Root].Children) == 0 {
		return parent, nil
	}
	childIndex := m.Pieces[m.Root].Children[0]
	if childIndex < 0 || childIndex >= len(m.Pieces) {
		return parent, nil
	}
	childRoll := v.Roll
	var propeller *frame.ProjectileView
	if v.Propeller {
		childRoll = 0
		propeller = &v
	}
	child = buildProjectileStandalonePiece(m, childIndex, childRoll, v.Yaw, pitch, modelFacing, worldPos, propeller, now, childScratch)
	return parent, child
}

// BuildDebrisModelPieceInto builds one detached whole-piece draw from its
// original vertices and current stepped angle words. The debris path carries
// no projectile-facing half turn and does not compose the former unit root
// again [04 R-COB-04 §2][03 R-COMP-02 §6].
func BuildDebrisModelPieceInto(m *model.Model, v frame.DebrisView, scratch *ProjectileScratch) *UnitDraw {
	if scratch == nil {
		scratch = &ProjectileScratch{}
	}
	return buildProjectileStandalonePiece(m, v.PieceIndex, v.Angles[2], v.Angles[1], v.Angles[0], false,
		[3]numeric.Fixed{v.X, v.Y, v.Z}, nil, 0, scratch)
}

// buildProjectileStandalonePiece translates the retail standalone model call
// into one UnitDraw. The passed piece is detached from the unit hierarchy: the
// projectile entry rotates and projects that one piece, then the conditional
// child is a second call rather than a recursive draw [03 R-COMP-02 §6].
func buildProjectileStandalonePiece(m *model.Model, piece int, roll, yaw, pitch uint16, modelFacing bool, worldPos [3]numeric.Fixed, propeller *frame.ProjectileView, now uint32, s *ProjectileScratch) *UnitDraw {
	if m == nil || piece < 0 || piece >= len(m.Pieces) {
		return nil
	}
	p := m.Pieces[piece]
	p.Parent = -1
	p.Children = nil
	// The effect entry rotates this piece's raw vertex buffer and adds only the
	// projectile world point. It does not compose authored object translation.
	p.Translate = [3]numeric.Fixed{}
	s.pieces[0] = p
	s.model = model.Model{Pieces: s.pieces[:1], Root: 0, Name: m.Name}
	single := &s.model
	states := s.states[:1]
	states[0] = model.PieceState{RotZ: roll} // word 0 has no model-facing offset [03 §5.2]
	if modelFacing {
		FoldProjectileAngles(states, 0, yaw, pitch)
	} else {
		states[0].RotY += yaw
		states[0].RotX += pitch
	}
	if propeller != nil {
		FoldPublishedPropellerSpin(states, 0, *propeller, now)
	}
	pieces, transforms := buildPieceDrawsInto(single, states, worldPos, false, false, &s.draw)
	if len(pieces) != 0 {
		pieces[0].SourceIndex = piece
	}
	s.result = UnitDraw{
		Model:       single,
		PieceStates: states,
		Transforms:  transforms,
		Pieces:      pieces,
		WorldPos:    worldPos,
	}
	return &s.result
}

// EmitPoint returns the world-space position of a vertex attachment on a piece [03 §2.4] C23.
// It applies the composed transform then adds the committed world position [03 §2.4] C21 [03 §5.2].
// Presentation only; returns false when piece or vertex index out of range.
func EmitPoint(m *model.Model, states []model.PieceState, pieceIdx int, vertexIdx int, worldPos [3]numeric.Fixed) ([3]numeric.Fixed, bool) { // [03 §2.4] C23
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
		local[0].Add(worldPos[0]),
		local[1].Add(worldPos[1]),
		local[2].Add(worldPos[2]),
	}
	return world, true
}

// LeafEmitPoints returns world-space emit points for all leaf attachments in the model [03 §2.4] C23.
// Each leaf that has a vertex but no primitive yields its first vertex transformed to world space [03 §2.4] C23.
// Order is stable piece-index ascending (I1).
func LeafEmitPoints(m *model.Model, states []model.PieceState, worldPos [3]numeric.Fixed) [][3]numeric.Fixed { // [03 §2.4] C23
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
				local[0].Add(worldPos[0]),
				local[1].Add(worldPos[1]),
				local[2].Add(worldPos[2]),
			}
			out = append(out, world)
			break // one emit point per leaf piece (first vertex) [03 §2.4] C23
		}
	}
	return out
}

// AnyEmitPoints returns emit points for any piece with vertex but no primitive, even if non-leaf, for broader emit coverage [03 §2.4] C23.
// This helper surfaces attachment points even when the strict leaf check fails; caller can choose strict vs any.
func AnyEmitPoints(m *model.Model, states []model.PieceState, worldPos [3]numeric.Fixed) [][3]numeric.Fixed { // [03 §2.4] C23
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
			local[0].Add(worldPos[0]),
			local[1].Add(worldPos[1]),
			local[2].Add(worldPos[2]),
		}
		out = append(out, world)
	}
	return out
}

// PaletteRGBA resolves a final indexed pixel to RGBA at present time through
// PALETTE.PAL [03 §4.3] C10. Model texture and flat-colour bytes are already
// active palette indices, so the logical→physical map — a semantic-colour
// route — is not applied here [07 "Retail palette contract"].
func PaletteRGBA(tables *palette.Tables, idx byte) (r, g, b, a uint8) { // [03 §4.3] C10
	if tables == nil {
		return 0, 0, 0, 255
	}
	return tables.RGBA(idx) // PALETTE.PAL at present time [03 §4.3]
}

// ShadeRGBA resolves a palette index through an SHD row for model lighting
// [03 §4.3] C10. The row is the caller's real per-corner value — the shaded
// piece renderer's SHD row is trunc(dot*5.0) & 0x1F with DONT_SHADE pinning
// row 15, computed by ShadeRowForNormal [03 R-RAST-01 §5]. This helper never
// invents a row of its own; the production draw path in
// internal/client/model.go interpolates PrimitiveDraw.ShadeRows directly and
// does not call through here, so this stays a reusable, tested primitive for
// any other consumer of PrimitiveDraw.
func ShadeRGBA(tables *palette.Tables, idx byte, row int) (r, g, b, a uint8) { // [03 §4.3] C10 [03 R-RAST-01 §5]
	if tables == nil {
		return 0, 0, 0, 255
	}
	if row < 0 {
		row = 0
	}
	if row >= 32 {
		row = 31
	}
	// The texture byte is already a PALETTE.PAL index; the logical→physical
	// map resolves semantic colour fields, never image bytes [03 §4.3]
	// [07 "Retail palette contract"].
	shaded := tables.Shade[row][idx] // [03 §4.3] SHD row
	e := tables.Base[shaded]
	return e[0], e[1], e[2], 255
}

// The four span writers of [03 R-REN-03A §5] — shaded/unshaded x
// textured/flat — live in internal/client's model raster, which interpolates
// PrimitiveDraw.ShadeRows itself. A PrimitiveRGBA helper used to stand here
// that resolved a primitive's colour by reading the authored 3DO IsColored
// flat/textured discriminator as if it were the shading one, so every flat
// primitive took the raw-palette arm whatever row it carried. That is the
// unshaded flat writer only, and §5 puts the split on the RENDERER. Nothing on
// the draw path called it; two tests did. They now call the writer they mean,
// PaletteRGBA or ShadeRGBA, and internal/client's
// TestShadedFlatWriterResolvesThroughSHD and
// TestUnshadedFlatWriterEmitsTheRawColour hold the real contract.

// ModelProjectToScreen projects a world-space point to screen via the orthographic formula [03 §2.5].
// Uses camera.WorldToScreen with the half-height shear [03 §2.5].
//
// This takes a genuine world point. A composed model vertex is not one — see
// ModelVertexToScreen — and passing one here mirrors the result in Z.
func ModelProjectToScreen(cam *camera.Camera, world [3]numeric.Fixed) (sx, sy int32) { // [03 §2.5]
	if cam == nil {
		return 0, 0
	}
	return cam.WorldToScreen(world[0], world[1], world[2]) // [03 §2.5]
}

// ModelVertexToScreen projects one composed model vertex: a piece-chain output
// with the unit's world position already added componentwise, which is what
// PieceDraw.WorldVertices holds and what a UnitTransforms origin plus the unit
// position is. It is not interchangeable with ModelProjectToScreen, and the
// two names exist so the space a caller is in is explicit at the call site.
//
// Model space is mirrored in Z against world space, so the model-relative part
// of the vertex reaches the screen Y lane with its sign flipped while the
// unit's own Z keeps its ordinary sign. [R-WATER-01 §1] item 3 states the
// world-object form for rotated model geometry drawn at a unit position:
//
//	sx = hi16(rx + ux - camX) + 128
//	sy = hi16((uz - camZ) - rz) - (hi16(ry + uy) >> 1) + 32
//
// with r the model-relative vertex and u the unit position — "the rotated Z is
// subtracted, the 3DO handedness flip of [R-RAST-01 §2]". Reflecting the
// composed vertex's Z about the unit's own Z produces exactly that: the
// model-relative part changes sign, the unit's part does not, and the shear
// still reads the composed height ry + uy.
//
// This is the sum-before-floor world-object form, not the cached composition
// image's anchor-plus-local form. [R-RAST-01 §2] requires both to exist
// separately: floor(a) + floor(b) is floor(a+b) or one less, so the same vertex
// can land a pixel apart under the two, and the two forms must not share a
// helper.
func ModelVertexToScreen(cam *camera.Camera, unit, v [3]numeric.Fixed) (sx, sy int32) { // [R-WATER-01 §1] [R-RAST-01 §2]
	if cam == nil {
		return 0, 0
	}
	flippedZ := unit[2].Sub(v[2].Sub(unit[2]))
	return cam.WorldToScreen(v[0], v[1], flippedZ)
}
