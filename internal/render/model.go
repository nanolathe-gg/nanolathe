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

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
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
	Model *model.Model
	// SourceModel is the LOADED model a standalone piece was copied from, and
	// is nil for an ordinary unit or feature draw (where Model is already the
	// loaded model). A bounded projectile or debris call rebuilds one piece
	// into a per-call scratch model, and retail resolves that piece's animated
	// texture through the cursor stored in the LOADED model's primitive record
	// -- the same record and the same read the unit renderer uses, so a
	// detached piece animates in lockstep with its living parent
	// [03 R-COMP-02 §6]. Carrying the loaded model here is what lets the
	// compose walk reach that cursor.
	SourceModel  *model.Model
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
	// lazy is the scratch whose pending pieces Materialize still has to build
	// (BuildUnitDrawDeferredInto); nil once every piece is built.
	lazy *DrawScratch
}

// buildPieceDrawsInto is the one per-piece traversal: it produces the draw
// records with world transforms and primitive lists in load-fixed order over
// borrowed scratch [03 §2.4] C20 [03 §5.2]; presentation only (I6). worldPos is
// the committed world position [03 §2.4] C12 and piece math itself does not
// include it [03 §5.2][03 §2.4] C24. UnitDraw constructors take the returned
// transform slice directly rather than composing the chain a second time.
func buildPieceDrawsInto(m *model.Model, states []model.PieceState, worldPos [3]numeric.Fixed, dirty, shaded bool, scratch *DrawScratch) ([]PieceDraw, []model.Transform) { // [03 §2.4] C21 [03 §5.2] C13
	return buildPieceDrawsDeferrable(m, states, worldPos, dirty, shaded, false, scratch)
}

// buildPieceDrawsDeferrable is buildPieceDrawsInto that, with deferGeometry,
// stops after the transforms, the hidden verdicts and the record headers and
// leaves every visible piece's geometry pending (UnitDraw.Materialize).
func buildPieceDrawsDeferrable(m *model.Model, states []model.PieceState, worldPos [3]numeric.Fixed, dirty, shaded, deferGeometry bool, scratch *DrawScratch) ([]PieceDraw, []model.Transform) {
	if m == nil {
		return nil, nil
	}
	scratch.pending = reuseDrawSlice(scratch.pending, len(m.Pieces))
	clear(scratch.pending)
	scratch.shaded = shaded
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
		if deferGeometry {
			// The piece's corners, shading and primitive records are filled on
			// demand (UnitDraw.Materialize); until then the piece carries none.
			record.WorldVertices, record.Primitives, record.IsLeafAttachment = nil, nil, false
			scratch.pending[i] = true
			continue
		}
		materializePiece(m, i, states, worldPos, shaded, scratch)
	}
	return out, transforms
}

// materializePiece fills one visible piece's world corners, shading rows and
// primitive records from its already composed transform. Pieces are
// independent here: each reads only its own transform and authored vertices.
func materializePiece(m *model.Model, i int, states []model.PieceState, worldPos [3]numeric.Fixed, shaded bool, scratch *DrawScratch) {
	piece := &m.Pieces[i]
	store := &scratch.storage[i]
	tr := &scratch.transforms[i]
	record := &scratch.pieces[i]
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
	// Keep the strict interpretation: the flag marks strict leaves only.
	record.WorldVertices, record.Primitives, record.IsLeafAttachment = worldVerts, prims, isLeaf
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

// BuildUnitDrawInto builds the full per-unit draw for presentation
// [03 §2.4][03 §5.2] (I6). It copies base states, folds unit orientation
// bank→Z heading→Y pitch→X [03 §2.4] C24 [03 §5.2], optionally uses cache to
// decide rebuild, projects the committed world position [03 §2.4] C12, and
// produces transforms and primitive lists in load-fixed order [03 §2.4] C20.
// Caller must supply a presentation copy of base states or nil; the function
// never writes sim state (I6). A supplied cache retains unit rotation until an
// axis differs by more than seven; the owner updates the reference after
// rebuilding [03 §5.2] C13.
//
// The result borrows scratch until its next use; this only changes ownership
// of the presentation result, never base or committed state.
func BuildUnitDrawInto(m *model.Model, base []model.PieceState, heading, pitch, bank uint16, current frame.UnitView, cache *OrientationCache, scratch *DrawScratch) *UnitDraw { // [03 §2.4] C24 [03 §5.2] C13
	return buildUnitDraw(m, base, heading, pitch, bank, current, cache, scratch, false)
}

// BuildUnitDrawDeferredInto is BuildUnitDrawInto with every visible piece's
// world corners, shading rows and primitive records left unbuilt: the draw's
// transforms, hidden verdicts, piece headers and flags are complete, and
// Materialize fills the pieces of a lane on demand. A consumer that needs only
// some pieces — a retained body's live lane — then pays for those alone. Until
// a piece is materialized it carries no corners and no primitives, so the
// caller must materialize every lane it reads before reading it.
func BuildUnitDrawDeferredInto(m *model.Model, base []model.PieceState, heading, pitch, bank uint16, current frame.UnitView, cache *OrientationCache, scratch *DrawScratch) *UnitDraw {
	return buildUnitDraw(m, base, heading, pitch, bank, current, cache, scratch, true)
}

// Materialize builds the pending pieces of lane (BuildUnitDrawDeferredInto).
// Each piece is built exactly as the eager traversal builds it; a draw with
// nothing pending is unchanged.
func (d *UnitDraw) Materialize(lane PieceLane) {
	s := d.lazy
	if s == nil {
		return
	}
	all := true
	for i := range d.Pieces {
		if i >= len(s.pending) || !s.pending[i] {
			continue
		}
		if !lane.Includes(d.Pieces[i].DontCache, d.UnderConstruction) {
			all = false
			continue
		}
		materializePiece(d.Model, i, d.PieceStates, d.WorldPos, s.shaded, s)
		s.pending[i] = false
	}
	if all {
		d.lazy = nil
	}
}

// HasFaces reports whether any visible piece carries a primitive, counting
// pieces not yet materialized by their authored primitives. A hidden piece
// keeps its index and drops its primitives.
func (d *UnitDraw) HasFaces() bool {
	for i := range d.Pieces {
		if len(d.Pieces[i].Primitives) != 0 {
			return true
		}
		if s := d.lazy; s != nil && i < len(s.pending) && s.pending[i] && i < len(d.Model.Pieces) && len(d.Model.Pieces[i].Primitives) != 0 {
			return true
		}
	}
	return false
}

// ShadedFaces reports whether any visible primitive carries a shade row,
// which is whether the shaded renderer drew any face of this pose. A piece not
// yet materialized counts by its authored primitives and the draw's shading
// verdict, which is exactly what materializing it would record.
func (d *UnitDraw) ShadedFaces() bool {
	for i := range d.Pieces {
		for _, primitive := range d.Pieces[i].Primitives {
			if primitive.ShadeRow != NoShadeRow {
				return true
			}
		}
		if s := d.lazy; s != nil && s.shaded && i < len(s.pending) && s.pending[i] && i < len(d.Model.Pieces) && len(d.Model.Pieces[i].Primitives) != 0 {
			return true
		}
	}
	return false
}

func buildUnitDraw(m *model.Model, base []model.PieceState, heading, pitch, bank uint16, current frame.UnitView, cache *OrientationCache, scratch *DrawScratch, deferGeometry bool) *UnitDraw {
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
	// buildPieceDrawsInto is the one traversal. Derive the public transform view from
	// its records so a frame cannot apply the hierarchy twice [03 §2.4] C21.
	// Script pose changes use the retained unit orientation until the strict
	// orientation threshold requests its refresh [03 R-COMP-01 §4][03 §5.2].
	// BMcode=0 selects the shaded piece renderer only while the global
	// display option is enabled; all other units take the no-SHD path
	// [R-RND-02A].
	pieces, transforms := buildPieceDrawsDeferrable(m, states, worldPos, needsRebuild, !current.BMCode && Shading, deferGeometry, scratch) // [03 §2.4] C20
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
	if deferGeometry {
		scratch.draw.lazy = scratch
	}
	return &scratch.draw
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

// BuildProjectileModelPiecesInto builds the bounded standalone model calls for
// a projectile over borrowed storage, so a frame that draws a hundred
// projectiles allocates nothing per projectile: type 1 draws its root then its
// first child while the strict deadline permits it; other types draw the root
// only. It intentionally does not traverse grandchildren — retail's effect
// entry receives one model piece per call, and the projectile dispatcher
// supplies only the model header and its child slot [03 §5.4][03 R-COMP-02 §6].
// parentScratch and childScratch must be distinct slots.
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
		SourceModel: m,
		PieceStates: states,
		Transforms:  transforms,
		Pieces:      pieces,
		WorldPos:    worldPos,
	}
	return &s.result
}

// This file computes a unit draw; it resolves no pixel. The four span writers
// of [03 R-REN-03A §5] — shaded/unshaded x textured/flat — live in
// internal/client's model raster, which interpolates PrimitiveDraw.ShadeRows
// itself, narrows the row with spanShadeRow and reads PALETTE.PAL and the SHD
// row directly. Colour-resolution helpers that stood here (PrimitiveRGBA, then
// PaletteRGBA and ShadeRGBA) were second statements of that resolve with no
// caller on any draw path; internal/client's
// TestShadedFlatWriterResolvesThroughSHD, TestUnshadedFlatWriterEmitsTheRawColour
// and TestSpanShadeRowClampsToTheTable hold the contract on the shipped code.
//
// Model-space vertex attachment points (EmitPoint and the leaf walks) stood
// here too, with no consumer anywhere; the projectile and effect paths carry
// their own world positions [03 §5.2].
//
// Projection likewise: camera.WorldToScreen is the orthographic formula of
// [03 §2.5], and the world-object form for rotated model geometry drawn at a
// unit position — the 3DO handedness flip of [R-RAST-01 §2] — is
// internal/client's Client.worldObjectScreen [03 R-WATER-01 §1].
