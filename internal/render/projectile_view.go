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
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
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
	Handle     uint16
	RenderType int32
	Kind       string
	Graphic    string
	Model      string
	Frame      int
	Selector   int32
	Head       ProjectilePoint
	Tail       ProjectilePoint
	Segments   []ProjectilePoint
	Segments2  []ProjectilePoint
	Color      int32
	Color2     int32
	Suppressed bool
	Aborted    bool
	BaseFrame  *formats.GAFFrame // common projectile sprite for model families
	FrameAsset *formats.GAFFrame // selected authored GAF frame for GAF families
}

// ProjectileGAFRequest identifies one authored GAF lookup. The renderer does
// not know which retail archive/entry supplies a family; the immutable
// content boundary resolves that identity and returns false when absent.
type ProjectileGAFRequest struct {
	View     snapshot.ProjectileView
	Family   int32
	Sequence int32
	Frame    int
	Base     bool
}

// ProjectileDispatchOptions supplies presentation metadata that is not part
// of the per-projectile snapshot.  Frame counts are resolved from immutable
// content by the caller; a missing count stays missing and suppresses only
// the frame-selected family.  No fallback constant is invented [I9].
type ProjectileDispatchOptions struct {
	FrameCount func(snapshot.ProjectileView) (count int, ok bool)
	Color      func(snapshot.ProjectileView) (primary, secondary int32, ok bool)
	// ResolveGAF admits only authored frames. Returning false is an unresolved
	// asset, never permission to substitute a marker or empty sprite.
	ResolveGAF func(ProjectileGAFRequest) (*formats.GAFFrame, bool)
	// SegmentPoints admits both researched CRT jitter passes for rendertype 7.
	// Returning ok=false is an explicit unresolved result, not a fallback.
	SegmentPoints func(snapshot.ProjectileView) (first, second []ProjectilePoint, ok bool)
}

func point(x, y, z numeric.Fixed) ProjectilePoint { return ProjectilePoint{X: x, Y: y, Z: z} }

func projectileHead(v snapshot.ProjectileView) ProjectilePoint {
	return point(v.X, v.Y, v.Z)
}

func projectileTail(v snapshot.ProjectileView) ProjectilePoint {
	return point(v.TailX, v.TailY, v.TailZ)
}

// SnapshotSegmentedPoints applies the established rendertype-7 span and CRT
// jitter helper to immutable snapshot endpoints. A nil CRT intentionally
// yields nil: presentation must not silently consume a different RNG stream
// or draw an invented straight-line substitute [03 §5.4][I4].
func SnapshotSegmentedPoints(v snapshot.ProjectileView, crt *rng.CRT) []ProjectilePoint {
	if crt == nil {
		return nil
	}
	pts := SegmentedBeamPoints(
		combat.Vec3{X: v.X, Y: v.Y, Z: v.Z},
		combat.Vec3{X: v.TailX, Y: v.TailY, Z: v.TailZ},
		crt,
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
func SnapshotSegmentedPointPasses(v snapshot.ProjectileView, crt *rng.CRT) (first, second []ProjectilePoint, ok bool) {
	if crt == nil {
		return nil, nil, false
	}
	first = SnapshotSegmentedPoints(v, crt)
	second = SnapshotSegmentedPoints(v, crt)
	if len(first) == 0 || len(second) == 0 {
		return nil, nil, false
	}
	return first, second, true
}

// DispatchProjectileView selects the established rendertype branch using a
// snapshot record.  It mirrors DispatchRendertype's selector and lifetime
// arithmetic while avoiding a conversion back to mutable combat.Projectile.
// The returned instruction is presentation-only [I6].
func DispatchProjectileView(v snapshot.ProjectileView, now uint32, opts ProjectileDispatchOptions) ProjectileDraw {
	d := ProjectileDraw{
		Handle:     uint16(v.Handle),
		RenderType: v.RenderType,
		Kind:       "unknown",
		Graphic:    v.Graphic,
		Model:      v.Model,
		Selector:   v.Selector,
		Head:       projectileHead(v),
		Tail:       projectileTail(v),
	}
	colorOK := false
	if opts.Color != nil {
		d.Color, d.Color2, colorOK = opts.Color(v)
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
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: -1, Frame: 0, Base: true})
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
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: 0, Frame: 0})
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
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: -1, Frame: 0, Base: true})
		if !ok || frame == nil {
			d.Suppressed = true
			return d
		}
		d.BaseFrame = frame
	case RenderTypeSelectorGAF:
		d.Kind = "selector-gaf"
		if v.Selector == -1 || opts.FrameCount == nil {
			d.Suppressed = true
			return d
		}
		frameCount, ok := opts.FrameCount(v)
		if !ok || frameCount <= 0 {
			d.Suppressed = true
			return d
		}
		d.Frame = int((now - v.CreationTick) % uint32(frameCount)) // [03 §5.4]
		if opts.ResolveGAF == nil {
			d.Suppressed = true
			return d
		}
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: v.Selector, Frame: d.Frame})
		if !ok || frame == nil {
			d.Suppressed = true
			return d
		}
		d.FrameAsset = frame
	case RenderTypeLifetimeGAF:
		d.Kind = "lifetime-gaf"
		if v.Lifetime <= 0 || opts.FrameCount == nil {
			d.Suppressed = true
			return d
		}
		frameCount, ok := opts.FrameCount(v)
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
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: 0, Frame: d.Frame})
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
		frame, ok := opts.ResolveGAF(ProjectileGAFRequest{View: v, Family: v.RenderType, Sequence: -1, Frame: 0, Base: true})
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

// BuildProjectileDraws dispatches an already stable snapshot slice in order
// (I1). The visibility callback runs before rendertype dispatch [03 §5.4]. A
// global-GAF admission failure aborts the batch, preserving the researched
// whole-renderer abort behavior.
func BuildProjectileDraws(projectiles []snapshot.ProjectileView, now uint32, visible func(snapshot.ProjectileView) bool, admitGlobalGAF func(snapshot.ProjectileView) bool, opts ProjectileDispatchOptions) ([]ProjectileDraw, bool) {
	out := make([]ProjectileDraw, 0, len(projectiles))
	for _, v := range projectiles {
		if visible != nil && !visible(v) {
			continue
		}
		d := DispatchProjectileView(v, now, opts)
		if d.RenderType == RenderTypeGlobalGAF && admitGlobalGAF != nil && !admitGlobalGAF(v) {
			d.Suppressed = true
			d.Aborted = true
			return out, true
		}
		if !d.Suppressed {
			out = append(out, d)
		}
	}
	return out, false
}
