package gpurender

import (
	"image"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// ModelStats is the per-Execute accounting for the modern executor's model
// lane and its scheduler (docs/DESIGN_GPU_RENDERER.md §22). It is diagnostic
// only and never reaches simulation state.
type ModelStats struct {
	MaterialFaces, ScorchQuads int
	// Phases is the phases this frame submitted and Passes the device
	// destination switches the executor issued: a switch is counted whenever
	// the destination image of a device call differs from the previous call's,
	// which is the unit of device cost Ebitengine's backends pay for
	// [DESIGN_GPU_RENDERER.md §11.5].
	Phases, Passes int
	// SubmittedVertices/Indices count actual triangle submissions.
	SubmittedVertices, SubmittedIndices, MaxSubmissionVertices int
	// PointPixels is the screen pixels the frame's lit point batches covered,
	// PointPlanes the lit point plane regions they committed through, and
	// PointQuads the device quads those pixels compiled into; Vertices is every
	// vertex the scheduler handed the device this frame.
	PointPixels, PointQuads, PointPlanes, Vertices int
	// GlowQuads is the emissive quads the Enhanced glow layer batched this
	// frame and GlowPasses the device passes its resolve spent (§19).
	GlowQuads, GlowPasses int
	// BattleLights and LitModelFaces describe the bounded Enhanced prototype.
	BattleLights, LitModelFaces, LitSmokeSprites int
	// BattleLightKinds counts the selected sources by family, indexed by the
	// executor's light kind in declaration order: explosion, nanolathe, fire,
	// projectile, wreck (§31).
	BattleLightKinds [lightKindCount]int
	// GroundLights is the clipped light discs the terrain illumination pass
	// batched this frame; zero means the pass drew and copied nothing (§31).
	GroundLights int
	// ReflectionVertices is the bounded coastal reflection source geometry.
	ReflectionVertices int
	BlastWaves         int
	// UnderwaterCommits counts subjects committed through the underwater
	// refraction (§26.5).
	UnderwaterCommits int
	// HeatPlumes is all heat; WreckHeatPlumes is its wreck subset.
	HeatPlumes      int
	WreckHeatPlumes int
	// The model lane's accounting (§22): subjects and shadows placed on the
	// atlas, faces appended and rings culled, packets the atlas could not hold
	// (which took the fallback), the lane's passes (two a page, plus those the
	// construction-group waves open), the atlas rows the regions reached over
	// every page, and the pages used.
	DirectSubjects, DirectShadows, DirectFaces, DirectCulled, DirectOverflow, DirectPasses int
	DirectAtlasRows, DirectPages                                                           int
	// DirectOutlineRings is the construction outline rings drawn as one device
	// primitive each, DirectOutlineTexels the 2× texels those primitives
	// covered, and DirectOutlineWalked the rings whose endpoints the CPU
	// walked instead — five or more corners, a key the device cannot make
	// exact, or no room in the parameter image (model_outline.go). A carried
	// child's solo image counts its rings again.
	DirectOutlineRings, DirectOutlineTexels, DirectOutlineWalked int
	// DirectCargoImages is the carried children composed a second time in a
	// region of their own, because their shadow is cut from the child's own
	// finished image and the group region holds the carrier's texels too
	// [03 R-REN-03D §1] (§22).
	DirectCargoImages int
	// Construction groups merge finished child pixels after the atlas passes.
	// Pixels counts the 2x rectangles processed; scratch is two RGBA planes.
	DirectGroupMerges, DirectGroupPixels, DirectGroupScratchBytes int
	// DirectRetained is the packets (bodies and projected shadows) whose
	// packed vertices were replayed from the retained store rather than
	// packed again, DirectWarm the packets appended warm — a key miss whose
	// body the store's index held under another revision, packed from this
	// frame's corners with the texture products of the last cold append —
	// and DirectCaptured the packets captured into the store this frame,
	// from a cold or a warm append (model_retain.go). Together with
	// DirectSubjects + DirectShadows they say how much of the frame's face
	// work the store absorbed.
	DirectRetained, DirectWarm, DirectCaptured int
	// DirectRetainedLive and DirectRetainedOutline are the replayed packets
	// that also carried a per-frame live lane or an outline, which were
	// appended cold after the replayed cached lane (model_retain.go).
	DirectRetainedLive, DirectRetainedOutline int
	// The cold packets by reason, so a fall in DirectRetained can be read
	// without a profiler. A packet is counted once, at the first reason that
	// applies, in this order: DirectColdNoKey has no reusable key (the direct
	// lanes, a subject without a retained body, a shadow projected per frame)
	// — DirectColdNoKeyOutline and DirectColdNoKeyLive are the subset of those
	// that carried an outline or a live lane, which the recorder used to
	// zero the key for; DirectColdGroup is a group child, its delta or its
	// solo pass; DirectColdReflect a body reflecting in water this frame;
	// DirectColdPrimed a key's first sighting (the stub); DirectColdShape a
	// captured key whose packet shape changed (captured again);
	// DirectColdParams a replay or capture the parameter image could not
	// take. DirectRetainEvicted is the store entries the frame's inserts
	// evicted — store pressure when it is not zero.
	DirectColdNoKey, DirectColdNoKeyOutline, DirectColdNoKeyLive int
	DirectColdGroup, DirectColdReflect, DirectColdPrimed         int
	DirectColdShape, DirectColdParams, DirectRetainEvicted       int
	// DeviceDraws is every device draw Execute issued this frame.
	DeviceDraws int
	// GPU is the model subjects committed, Skipped the packets omitted (a child
	// the lane cannot merge), Shadows the shadow commits, ShadowsOmitted the
	// shadows without a region, NoBody the commands whose recorder declared no
	// body commit, Flashes the lit-disc quads (§13.11).
	GPU, Skipped, Shadows, ShadowsOmitted, NoBody, Flashes int
	// Silhouettes is the shadows committed from their body's own raster
	// (§22), counted within Shadows.
	Silhouettes int
}

// ModelStats reports the most recent Execute's accounting.
func (r *Renderer) ModelStats() ModelStats {
	if r == nil {
		return ModelStats{}
	}
	return r.modelStats
}

// Model replays one recorded model command: the subject's shadow commit and
// body commit through the model lane, in record order (§22). A shadow-only
// command commits the carried child's shadow alone; its body is composed by
// the later carrier command [03 R-REN-03A §4].
func (r *Renderer) Model(cmd drawlist.Model) {
	if r == nil || r.surfaces[0] == nil {
		return
	}
	g := cmd.Geometry
	if g == nil {
		r.modelStats.Skipped++
		return
	}
	if g.Fallback == drawlist.ModelFallbackNoBodyCommit {
		r.modelStats.NoBody++
		return
	}
	if !g.Eligible {
		r.modelStats.Skipped++
		return
	}
	if g.Shadow != nil {
		if g.Shadow.Silhouette {
			r.commitModelSilhouetteShadow(g)
		} else {
			r.commitModelDirectShadow(g)
		}
	}
	if cmd.ShadowOnly {
		return
	}
	r.commitModelDirect(g)
	r.modelStats.GPU++
}

// modelLocalBounds is a packet's composition box: the producer's, or the
// corners' union for a packet without one.
func modelLocalBounds(g *drawlist.ModelGeometry) image.Rectangle {
	if g.Width > 0 && g.Height > 0 {
		return image.Rect(0, 0, int(g.Width), int(g.Height))
	}
	var b image.Rectangle
	for _, faces := range [][]drawlist.ModelFace{g.Faces, g.LiveFaces, g.Outline} {
		for _, f := range faces {
			for _, v := range f.Vertices {
				b = b.Union(image.Rect(int(v.X), int(v.Y), int(v.X)+1, int(v.Y)+1))
			}
		}
	}
	return b
}

// modelWorldBounds is the packet's composition box on the framebuffer.
func modelWorldBounds(g *drawlist.ModelGeometry) image.Rectangle {
	return modelLocalBounds(g).Add(image.Pt(int(g.AnchorX-g.OriginX), int(g.AnchorY-g.OriginY)))
}

// modelSlotBounds is the union of a packet's box and every corner of its
// faces, outline included: the doubled lane's box can be narrower than the
// half-pixel-offset corners it carries (§17.3).
func modelSlotBounds(g *drawlist.ModelGeometry) image.Rectangle {
	b := modelLocalBounds(g)
	if g.Width <= 0 || g.Height <= 0 {
		return b
	}
	x0, y0, x1, y1 := int32(b.Min.X), int32(b.Min.Y), int32(b.Max.X), int32(b.Max.Y)
	for _, faces := range [][]drawlist.ModelFace{g.Faces, g.LiveFaces, g.Outline} {
		for i := range faces {
			for _, v := range faces[i].Vertices {
				x0, y0 = min(x0, v.X), min(y0, v.Y)
				x1, y1 = max(x1, v.X+1), max(y1, v.Y+1)
			}
		}
	}
	return image.Rect(int(x0), int(y0), int(x1), int(y1))
}

// mergeableChild reports whether an attached child joins its carrier's
// composition: a keyed packet with no children of its own [03 R-REN-03A §4].
func mergeableChild(cg *drawlist.ModelGeometry) bool {
	return cg != nil && cg.KeyPlane && len(cg.Children) == 0
}

// modelGroupBounds is the world rectangle a group composes over: the parent's
// box unioned with every mergeable child's.
func modelGroupBounds(g *drawlist.ModelGeometry) image.Rectangle {
	b := modelWorldBounds(g)
	for _, child := range g.Children {
		if mergeableChild(child.Geometry) {
			b = b.Union(modelWorldBounds(child.Geometry))
		}
	}
	return b
}
