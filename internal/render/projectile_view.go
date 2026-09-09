package render

// Snapshot projectile presentation adapters.
//
// The active client must consume immutable snapshot data rather than the
// combat pool.  This file deliberately contains no asset lookup and no
// framebuffer writes: it turns the complete ProjectileView into a typed
// render instruction that a client backend can draw.  Keeping dispatch here
// also makes it impossible for a missing model to silently select a generic
// marker path.

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// ProjectilePoint is a world-space endpoint in the immutable presentation
// plan.  Coordinates remain 16.16 fixed point until projection [I2].
type ProjectilePoint struct {
	X, Y, Z numeric.Fixed
}

// ProjectileDraw is one typed projectile draw instruction.  Kind is the
// established rendertype family, not an asset guess.  A client may omit an
// instruction only when Suppressed is true or when its authored asset is
// unavailable; it must not replace it with a generic marker.
type ProjectileDraw struct {
	Handle               uint16
	RenderType           int32
	Kind                 string
	Graphic              string
	Model                string
	Frame                int
	Selector             int32
	Head                 ProjectilePoint
	Tail                 ProjectilePoint
	Segments             []ProjectilePoint
	Segments2            []ProjectilePoint
	Color                int32
	Color2               int32
	Suppressed           bool
	Aborted              bool
	OrientationLow       uint16
	OrientationHigh      uint16
	HasDirectOrientation bool
	SecondaryModel       string
	SecondaryModelUntil  uint32
	BaseFrame            *formats.GAFFrame // common projectile sprite for model families
	FrameAsset           *formats.GAFFrame // selected authored GAF frame for GAF families
}

// ProjectileGAFRequest identifies one authored GAF lookup. The renderer does
// not know which retail archive/entry supplies a family; the immutable
// content boundary resolves that identity and returns false when absent.
type ProjectileGAFRequest struct {
	View     frame.ProjectileView
	Family   int32
	Sequence int32
	Frame    int
	Base     bool
	AssetID  string // resolved authored identity; empty means absent [03 §5.4]
}

// ProjectileDispatchOptions supplies presentation metadata that is not part
// of the per-projectile frame.  Frame counts are resolved from immutable
// content by the caller; a missing count stays missing and suppresses only
// the frame-selected family.  No fallback constant is invented [I9].
type ProjectileDispatchOptions struct {
	FrameCount func(frame.ProjectileView) (count int, ok bool)
	Color      func(frame.ProjectileView) (primary, secondary int32, ok bool)
	// ResolveGAF admits only authored frames. Returning false is an unresolved
	// asset, never permission to substitute a marker or empty sprite.
	ResolveGAF func(ProjectileGAFRequest) (*formats.GAFFrame, bool)
	// SegmentPoints admits both researched CRT jitter passes for rendertype 7.
	// Returning ok=false is an explicit unresolved result, not a fallback.
	SegmentPoints func(frame.ProjectileView) (first, second []ProjectilePoint, ok bool)
}

func point(x, y, z numeric.Fixed) ProjectilePoint { return ProjectilePoint{X: x, Y: y, Z: z} }

func projectileHead(v frame.ProjectileView) ProjectilePoint {
	return point(v.X, v.Y, v.Z)
}

func projectileTail(v frame.ProjectileView) ProjectilePoint {
	return point(v.TailX, v.TailY, v.TailZ)
}

// SnapshotSegmentedPoints applies the established rendertype-7 span and CRT
// jitter helper to immutable snapshot endpoints. A nil source intentionally
// yields nil: presentation must not silently consume a different RNG stream
// or draw an invented straight-line substitute [03 §5.4][I4].
func SnapshotSegmentedPoints(v frame.ProjectileView, random CRTRandomSource) []ProjectilePoint {
	if random == nil {
		return nil
	}
	pts := SegmentedBeamPoints(
		combat.Vec3{X: v.X, Y: v.Y, Z: v.Z},
		combat.Vec3{X: v.TailX, Y: v.TailY, Z: v.TailZ},
		random,
	)
	if len(pts) == 0 {
		return nil
	}
	out := make([]ProjectilePoint, len(pts))
	for i, p := range pts {
		out[i] = point(p.X, p.Y, p.Z)
	}
	return out
}

// SnapshotSegmentedPointPasses consumes the presentation CRT stream in the
// same stable order as the two researched rendertype-7 passes. A missing CRT
// or zero-span endpoint suppresses both passes rather than drawing a guessed
// straight line [03 §5.4][I4].
func SnapshotSegmentedPointPasses(v frame.ProjectileView, random CRTRandomSource) (first, second []ProjectilePoint, ok bool) {
	if random == nil {
		return nil, nil, false
	}
	first = SnapshotSegmentedPoints(v, random)
	second = SnapshotSegmentedPoints(v, random)
	if len(first) == 0 || len(second) == 0 {
		return nil, nil, false
	}
	return first, second, true
}

// DispatchProjectileView selects the established rendertype branch using a
// snapshot record.  It mirrors DispatchRendertype's selector and lifetime
// arithmetic while avoiding a conversion back to mutable combat.Projectile.
// The returned instruction is presentation-only [I6].
func DispatchProjectileView(v frame.ProjectileView, now uint32, opts ProjectileDispatchOptions) ProjectileDraw {
	d := ProjectileDraw{
		Handle:               uint16(v.Handle),
		RenderType:           v.RenderType,
		Kind:                 "unknown",
		Graphic:              v.Graphic,
		Model:                v.Model,
		Selector:             v.Selector,
		Head:                 projectileHead(v),
		Tail:                 projectileTail(v),
		OrientationLow:       v.OrientationLow,
		OrientationHigh:      v.OrientationHigh,
		HasDirectOrientation: v.HasDirectOrientation,
		SecondaryModel:       v.SecondaryModel,
		SecondaryModelUntil:  v.SecondaryModelUntil,
	}
	colorOK := false
	if opts.Color != nil {
		d.Color, d.Color2, colorOK = opts.Color(v)
	} else if v.HasPrimaryColor {
		d.Color = int32(v.PrimaryColor)
		if v.HasSecondaryColor {
			d.Color2 = int32(v.SecondaryColor)
		}
		colorOK = true
	}

	switch v.RenderType {
	case RenderTypeBeam:
		d.Kind = "beam"
		if !colorOK {
			d.Suppressed = true
			return d
		}
	case RenderTypeBaseSpriteModel:
		d.Kind = "base-sprite+model"
		if v.Model == "" || opts.ResolveGAF == nil {
			d.Suppressed = true
			return d
		}
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: -1, Frame: 0, Base: true, AssetID: v.BaseAssetID})
		if !ok || frame == nil {
			d.Suppressed = true
			return d
		}
		d.BaseFrame = frame
	case RenderTypeGlobalGAF:
		d.Kind = "global-gaf"
		if opts.ResolveGAF == nil {
			d.Suppressed = true
			return d
		}
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: 0, Frame: 0, AssetID: v.AssetID})
		if !ok || frame == nil {
			d.Suppressed = true
			return d
		}
		d.FrameAsset = frame
	case RenderTypeBaseModelDistinct:
		d.Kind = "base+model-distinct"
		if v.Model == "" || opts.ResolveGAF == nil {
			d.Suppressed = true
			return d
		}
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: -1, Frame: 0, Base: true, AssetID: v.BaseAssetID})
		if !ok || frame == nil {
			d.Suppressed = true
			return d
		}
		d.BaseFrame = frame
	case RenderTypeSelectorGAF:
		d.Kind = "selector-gaf"
		selector := v.Selector
		// SelectorSequence has no presence bit in the committed view. Its zero
		// value therefore cannot stand in for an absent authoritative selector;
		// use only the published selector and preserve -1 suppression [I9].
		if selector == -1 {
			d.Suppressed = true
			return d
		}
		d.Selector = selector
		frameCount, ok := projectileFrameCount(v, opts)
		if !ok || frameCount <= 0 {
			d.Suppressed = true
			return d
		}
		d.Frame = int((now - v.CreationTick) % uint32(frameCount)) // [03 §5.4]
		if opts.ResolveGAF == nil {
			d.Suppressed = true
			return d
		}
		// Type 4 has the same fixed ground shadow as the model-bearing
		// families, followed by its selector-selected fx frame [03 §5.4][06
		// R-WFX-01 §4]. Resolve both through the shared route; a missing shadow
		// is an unresolved record, not permission to draw only the foreground.
		base, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: -1, Frame: 0, Base: true})
		if !ok || base == nil {
			d.Suppressed = true
			return d
		}
		d.BaseFrame = base
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: selector, Frame: d.Frame, AssetID: v.AssetID})
		if !ok || frame == nil {
			d.Suppressed = true
			return d
		}
		d.FrameAsset = frame
	case RenderTypeLifetimeGAF:
		d.Kind = "lifetime-gaf"
		if v.Lifetime <= 0 {
			d.Suppressed = true
			return d
		}
		frameCount, ok := projectileFrameCount(v, opts)
		if !ok || frameCount <= 0 {
			d.Suppressed = true
			return d
		}
		remaining := int64(0)
		if v.ExpiryTick > now {
			remaining = int64(v.ExpiryTick - now)
		}
		d.Frame = int(int64(frameCount) - (remaining*int64(frameCount))/int64(v.Lifetime)) // [03 §5.4][I3]
		if d.Frame < 0 || d.Frame >= frameCount {
			d.Suppressed = true
			return d
		}
		if opts.ResolveGAF == nil {
			d.Suppressed = true
			return d
		}
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: 0, Frame: d.Frame, AssetID: v.AssetID})
		if !ok || frame == nil {
			d.Suppressed = true
			return d
		}
		d.FrameAsset = frame
	case RenderTypeRecordOrientation:
		d.Kind = "record-orientation"
		if v.Model == "" || opts.ResolveGAF == nil {
			d.Suppressed = true
			return d
		}
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: -1, Frame: 0, Base: true, AssetID: v.BaseAssetID})
		if !ok || frame == nil {
			d.Suppressed = true
			return d
		}
		d.BaseFrame = frame
	case RenderTypeSegmented:
		d.Kind = "segmented"
		if !colorOK {
			d.Suppressed = true
			return d
		}
		if opts.SegmentPoints != nil {
			first, second, ok := opts.SegmentPoints(v)
			d.Segments, d.Segments2 = first, second
			if !ok || len(d.Segments) == 0 || len(d.Segments2) == 0 {
				d.Suppressed = true
			}
		} else {
			d.Suppressed = true
		}
		return d
	default:
		d.Suppressed = true // unknown remains unknown [I9]
	}
	return d
}

func projectileFrameCount(v frame.ProjectileView, opts ProjectileDispatchOptions) (int, bool) {
	if v.FrameCount > 0 {
		return int(v.FrameCount), true
	}
	if opts.FrameCount == nil {
		return 0, false
	}
	return opts.FrameCount(v)
}

// BuildProjectileDraws dispatches an already stable snapshot slice in order
// (I1). The visibility callback runs before rendertype dispatch [03 §5.4]. A
// global-GAF admission failure aborts the batch, preserving the researched
// whole-renderer abort behavior.
func BuildProjectileDraws(projectiles []frame.ProjectileView, now uint32, visible func(frame.ProjectileView) bool, admitGlobalGAF func(frame.ProjectileView) bool, opts ProjectileDispatchOptions) ([]ProjectileDraw, bool) {
	return BuildProjectileDrawsInto(nil, projectiles, now, visible, admitGlobalGAF, opts)
}

// BuildProjectileDrawsInto is BuildProjectileDraws over a caller-retained
// buffer. dst is rewound, never read; the result aliases it, so a caller that
// keeps the buffer across frames stops allocating the dispatch list.
func BuildProjectileDrawsInto(dst []ProjectileDraw, projectiles []frame.ProjectileView, now uint32, visible func(frame.ProjectileView) bool, admitGlobalGAF func(frame.ProjectileView) bool, opts ProjectileDispatchOptions) ([]ProjectileDraw, bool) {
	out := dst[:0]
	for _, v := range projectiles {
		if visible == nil || !visible(v) { // [03 §5.4] absent visibility dependency fails closed
			continue
		}
		// The global sequence reserves/draws its destination before looking up
		// the frame. A failed admission aborts the whole renderer and must not
		// perform any later asset work [03 §5.4].
		if v.RenderType == RenderTypeGlobalGAF && admitGlobalGAF != nil && !admitGlobalGAF(v) {
			return out, true
		}
		d := DispatchProjectileView(v, now, opts)
		if !d.Suppressed {
			out = append(out, d)
		}
	}
	return out, false
}
