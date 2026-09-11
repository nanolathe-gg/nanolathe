package client

// Deterministic isolated 3DO previews use the same model loading, hierarchy,
// texture resolution, composition image, and indexed raster path as live units.
// This file only supplies the committed presentation inputs that a live unit
// would otherwise receive from a frame [03 §2.4][03 §2.4.1][R-REN-03A].

import (
	"fmt"
	"image"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

const maxModelPreviewDimension = 8192

// ModelPreviewOptions describes one isolated model view. Owner is the retail
// player colour byte used for its LOGOS selector. Structure and KeyPlane are explicit because they
// are unit-definition properties, not properties stored in a 3DO [fmt 3do].
// Heading uses the engine's uint16 full-circle representation [03 §2.4].
// Background is a physical PALETTE.PAL index.
type ModelPreviewOptions struct {
	Model   string
	Owner   uint8
	Heading uint16
	Pitch   uint16
	Bank    uint16
	Width   int
	Height  int
	// Scale magnifies the unchanged orthographic game-camera projection through
	// the client's presentation view scale. Zero means native scale 1. The view
	// scale is an integer, so only 1 and 2 are accepted
	// (DESIGN_GPU_RENDERER §14.1); a fractional magnification no longer exists.
	Scale      float32
	Background uint8
	Structure  bool
	KeyPlane   bool
	// These supplied committed presentation lanes allow isolated construction
	// and submerged/digger review. They do not advance a simulation or COB.
	BuildRemaining   float32
	WorldHeight      int32
	UnderwaterExempt bool
	Digger           bool
	// DisableAntiAlias suppresses only the structure 2x composition resolve for
	// this preview invocation. Its zero value preserves the renderer's default
	// preview behavior; it does not change the structure/shaded model path.
	DisableAntiAlias bool
	// HiddenPieces is an explicit tooling pose override. It lets a contact
	// sheet omit script-driven flash/locator geometry without claiming an
	// initial COB state or changing the authored model.
	HiddenPieces []string
	// PiecePoses is an explicit static presentation pose. It uses the committed
	// PieceView lanes so a tool can reproduce a researched model pose without
	// claiming to run its COB activation script [03 §2.4]. Entries are matched
	// by name, as they are on ordinary published unit views.
	PiecePoses []frame.PieceView
	// Children supplies attached committed views for composition review. Their
	// positions are relative to this preview's world origin; identities and
	// definition-derived fields are provided explicitly by the caller.
	Children []frame.UnitView
}

// ModelPreviewRecord is one reproducible static preview. Image is the classic
// reference render; List contains the same model command with its durable
// geometry packet for a device consumer. Background and Palette supply the
// neutral pixel plane required to replay a model-only list.
type ModelPreviewRecord struct {
	Image      *image.RGBA
	List       drawlist.List
	Background uint8
	Palette    *palette.Tables
}

// ModelPreviewRenderer retains the production model and texture caches while
// rendering any number of isolated headings from one mounted VFS. It is
// presentation-only and never advances texture playback or simulation state.
type ModelPreviewRenderer struct {
	client  *Client
	palette *palette.Tables
}

// NewModelPreviewRenderer loads the retail palette tables and binds the
// production model texture index to fs.
func NewModelPreviewRenderer(fs *vfs.FS) (*ModelPreviewRenderer, error) {
	if fs == nil {
		return nil, fmt.Errorf("nanolathe: creating model preview: logical path palettes/palette.pal, providers searched [], expected mounted retail VFS")
	}
	tables, err := palette.Load(fs)
	if err != nil {
		return nil, fmt.Errorf("nanolathe: creating model preview: logical path palettes/palette.pal, providers searched %s, expected retail palette tables: %w", previewProviders(fs), err)
	}
	c, err := New(Options{Width: 1, Height: 1})
	if err != nil {
		return nil, fmt.Errorf("nanolathe: creating model preview: %w", err)
	}
	c.SetPalette(tables)
	c.SetModelFS(fs)
	return &ModelPreviewRenderer{client: c, palette: tables}, nil
}

// RenderModel renders one model at one heading through the production unit
// hierarchy and raster path. Every call starts with the requested palette
// background and centers the unit origin in an identically sized frame.
func (r *ModelPreviewRenderer) RenderModel(opts ModelPreviewOptions) (*image.RGBA, error) {
	record, err := r.RecordModel(opts)
	if err != nil {
		return nil, err
	}
	return record.Image, nil
}

// RecordModel produces both the classic reference image and the immutable
// model packet which P3 can replay without a client, camera or model cache.
// It is a static pose renderer: PiecePoses represent supplied committed lanes,
// not COB activation or animation.
func (r *ModelPreviewRenderer) RecordModel(opts ModelPreviewOptions) (ModelPreviewRecord, error) {
	return r.recordModel(opts, false)
}

// RecordGeometry produces a modern-only preview packet. It performs the model
// transform and texture resolution but never allocates or rasterizes a CPU
// composition image. Its Image is nil; callers replay List on the GPU.
func (r *ModelPreviewRenderer) RecordGeometry(opts ModelPreviewOptions) (ModelPreviewRecord, error) {
	return r.recordModel(opts, true)
}

func (r *ModelPreviewRenderer) recordModel(opts ModelPreviewOptions, geometryOnly bool) (ModelPreviewRecord, error) {
	if r == nil || r.client == nil || r.palette == nil {
		return ModelPreviewRecord{}, fmt.Errorf("nanolathe: rendering model preview: renderer is not initialized")
	}
	name := strings.TrimSpace(opts.Model)
	if name == "" {
		return ModelPreviewRecord{}, fmt.Errorf("nanolathe: rendering model preview: logical path objects3d, providers searched %s, expected model name", previewProviders(r.client.modelFS))
	}
	// The production loader accepts either a bare model name or a complete
	// logical path. Make the command's documented "armcom.3do" shorthand a
	// complete path before entering that loader.
	renderName := name
	if strings.HasSuffix(strings.ToLower(name), ".3do") && !strings.ContainsAny(name, `/\`) {
		renderName = "objects3d/" + name
	}
	if opts.Width <= 0 || opts.Height <= 0 || opts.Width > maxModelPreviewDimension || opts.Height > maxModelPreviewDimension {
		return ModelPreviewRecord{}, fmt.Errorf("nanolathe: rendering model preview: output size %dx%d outside 1..%d", opts.Width, opts.Height, maxModelPreviewDimension)
	}
	// The view scale is one of the three views: 0 or 1 is native, 1.5 the
	// mid view and 2 the detail view (DESIGN_GPU_RENDERER §14.1). Any other
	// request is rejected rather than rounded, so a caller learns the
	// magnification is unavailable instead of silently receiving another one.
	if opts.Scale != 0 && opts.Scale != 1 && opts.Scale != 1.5 && opts.Scale != 2 {
		return ModelPreviewRecord{}, fmt.Errorf("nanolathe: rendering model preview: scale %.3g is not 1, 1.5 or 2", opts.Scale)
	}

	c := r.client
	previousAntiAlias := c.antiAlias
	previousGeometryOnly := c.geometryOnlyModels
	c.antiAlias = !opts.DisableAntiAlias
	c.geometryOnlyModels = geometryOnly
	defer func() {
		c.antiAlias = previousAntiAlias
		c.geometryOnlyModels = previousGeometryOnly
	}()
	c.width, c.height = opts.Width, opts.Height
	c.recordW, c.recordH = opts.Width, opts.Height
	if !geometryOnly {
		c.indexed = make([]uint8, opts.Width*opts.Height)
		c.rgba = make([]byte, opts.Width*opts.Height*4)
	}
	// The preview records the model into c.list and replays it once, the same
	// record-then-replay the committed frame uses (WU-1.8). The list opens
	// with a clear and the background fill, so either executor composes the
	// model over the same background: a device capture that painted the
	// background outside the list lost it to Execute's own frame reset.
	c.list.Reset()
	c.pointArena = c.pointArena[:0]
	c.list.RecordClear()
	c.list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: int32(opts.Width), H: int32(opts.Height)}, Index: opts.Background, Style: drawlist.FillSolid})
	// World position is chosen so modelAnchor lands on the image centre. Scale
	// magnifies the ordinary orthographic game-camera projection without
	// changing its angle or shear [03 §2.5][R-REN-03A §1].
	scale := camera.ViewScale(opts.Scale * 2).Norm()
	anchorX := int64(scale.Inverse(int32(opts.Width / 2)))
	anchorZ := int64(scale.Inverse(int32(opts.Height/2))) + int64(opts.WorldHeight>>1)
	c.cam = &camera.Camera{
		ViewW: int32(opts.Width), ViewH: int32(opts.Height),
		MapW: int32(opts.Width), MapH: int32(opts.Height), Scale: scale,
	}
	view := frame.UnitView{
		Slot:       1,
		InstanceID: 1,
		Owner:      opts.Owner,
		OwnerColor: opts.Owner,
		// A preview has an explicit colour-byte input even when it is outside
		// the LOGOS entry. The resolver will leave those team faces empty;
		// rejecting or wrapping it would invent a visible colour.
		OwnerColorKnown:  true,
		Model:            renderName,
		Heading:          opts.Heading,
		Pitch:            opts.Pitch,
		Bank:             opts.Bank,
		X:                numeric.Fixed(anchorX << 16),
		Z:                numeric.Fixed(anchorZ << 16),
		BMCode:           !opts.Structure,
		ZBuffer:          opts.KeyPlane,
		NoShadow:         true,
		BuildRemaining:   opts.BuildRemaining,
		Y:                numeric.Fixed(int64(opts.WorldHeight) << 16),
		UnderwaterExempt: opts.UnderwaterExempt,
		Digger:           opts.Digger,
	}
	view.Pieces = append(view.Pieces, opts.PiecePoses...)
	for _, name := range opts.HiddenPieces {
		if name = strings.TrimSpace(name); name != "" {
			view.Pieces = append(view.Pieces, frame.PieceView{Name: name, Hidden: true})
		}
	}
	wasRecordingGeometry := c.recordModelGeometry
	c.recordModelGeometry = true
	children := append([]frame.UnitView(nil), opts.Children...)
	for i := range children {
		children[i].X += view.X
		children[i].Y += view.Y
		children[i].Z += view.Z
	}
	drawn := c.composeCarrier(view, int32(opts.Width/2), int32(opts.Height/2), children)
	c.recordModelGeometry = wasRecordingGeometry
	if !drawn {
		path := renderName
		if !strings.HasSuffix(strings.ToLower(path), ".3do") {
			path = "objects3d/" + path + ".3do"
		}
		return ModelPreviewRecord{}, fmt.Errorf("nanolathe: rendering model preview: logical path %s, providers searched %s, expected drawable 3DO model", path, previewProviders(c.modelFS))
	}
	if geometryOnly {
		return ModelPreviewRecord{List: c.list.Clone(), Background: opts.Background, Palette: r.palette}, nil
	}
	// Replay the recorded model commit into the indexed surface, then expand.
	c.list.Replay(c.classicSink())
	c.convertIndexedToRGBA()
	out := image.NewRGBA(image.Rect(0, 0, opts.Width, opts.Height))
	copy(out.Pix, c.rgba)
	return ModelPreviewRecord{
		Image: out, List: c.list.Clone(), Background: opts.Background, Palette: r.palette,
	}, nil
}

// ARMSOLAROpenPreviewPose is the geometry-only open-dish recipe used for
// renderer review. The stock model's four dishes are named dish1 through dish4.
// Retail composition evidence rotates each by 135 degrees about its Z
// accumulator [03 R-REN-03A §3]. This helper does not emulate COB activation;
// callers must label output as a static pose.
func ARMSOLAROpenPreviewPose() []frame.PieceView {
	return []frame.PieceView{
		{Name: "dish1", RotZ: 24576},
		{Name: "dish2", RotZ: 24576},
		{Name: "dish3", RotZ: 24576},
		{Name: "dish4", RotZ: 24576},
	}
}

func previewProviders(fs *vfs.FS) string {
	if fs == nil {
		return "[]"
	}
	providers := fs.Providers()
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, provider.ID)
	}
	return "[" + strings.Join(names, ", ") + "]"
}
