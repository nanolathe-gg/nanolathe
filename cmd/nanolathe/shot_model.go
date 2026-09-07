package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/frame"
	model3d "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/platform/gpurender"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// runModelShot is a reproducible P3 review route. The supplied "open" pose is
// deliberately labeled synthetic: it feeds committed PieceView lanes and does
// not run a COB script [03 R-REN-03A §3].
func runModelShot(opts Options, cs *contentSet) error {
	if cs == nil || cs.fs == nil {
		return fmt.Errorf("nanolathe: shot model: retail VFS is unavailable")
	}
	r, err := client.NewModelPreviewRenderer(cs.fs)
	if err != nil {
		return err
	}
	w, h := 192, 160
	if opts.ShotSize != "" {
		if _, err := fmt.Sscanf(opts.ShotSize, "%dx%d", &w, &h); err != nil || w <= 0 || h <= 0 {
			return fmt.Errorf("nanolathe: shot model: --shot-size wants positive WxH, got %q", opts.ShotSize)
		}
	}
	requestedModel := strings.TrimSpace(opts.ShotModel)
	catalog, err := content.Compile(cs.fs)
	if err != nil {
		return fmt.Errorf("nanolathe: shot model: compile unit definitions: %w", err)
	}
	definition, err := resolveModelPreviewDefinition(catalog, requestedModel)
	if err != nil {
		return err
	}
	preview := client.ModelPreviewOptions{
		Model: definition.ObjectName, Width: w, Height: h,
		Heading: uint16(opts.ShotModelHeading), Scale: float32(opts.ShotModelScale),
		KeyPlane: definition.ZBuffer, Structure: definition.Structure,
	}
	if definition.Structure {
		// The GPU prototype accepts the scale-one structure subset only when
		// anti-alias resolve is explicitly disabled. Other structure captures
		// remain an honest CPU fallback in the production path.
		preview.DisableAntiAlias = true
	}
	if opts.ShotModelPose == "open" {
		if !strings.EqualFold(definition.ObjectName, "armsolar") {
			return fmt.Errorf("nanolathe: shot model: synthetic open pose is only defined for armsolar")
		}
		preview.PiecePoses = client.ARMSOLAROpenPreviewPose()
		fmt.Fprintln(os.Stderr, "nanolathe: shot model pose=open (synthetic PieceView pose; COB was not run)")
	} else if opts.ShotModelPose == "activated" {
		if !strings.EqualFold(definition.ObjectName, "armsolar") {
			return fmt.Errorf("nanolathe: shot model: activated pose is only defined for armsolar")
		}
		poses, poseErr := actualActivatedModelPose(cs.fs, definition.Definition)
		if poseErr != nil {
			return poseErr
		}
		preview.PiecePoses = poses
		fmt.Fprintln(os.Stderr, "nanolathe: shot model pose=activated (production UnitDef + unit-bound COB Activate; pose derived from VM)")
	}
	record, err := r.RecordModel(preview)
	if err != nil {
		return err
	}
	modern, stats, err := captureModernModel(record, w, h)
	if err != nil {
		return err
	}
	if stats.MissingSource > 0 {
		return fmt.Errorf("nanolathe: shot model: modern preview encountered %d CPU fallback(s) without a ModelSource; refusing an unclaimed image", stats.MissingSource)
	}
	fmt.Fprintf(os.Stderr, "nanolathe: shot model route: gpu=%d cpu-fallback=%d shadows=%d missing-source=%d unsupported-geometry=%d face=%d missing-texture=%d\n", stats.GPU, stats.CPUFallback, stats.Shadows, stats.MissingSource, stats.UnsupportedGeometry, stats.UnsupportedFace, stats.MissingTexture)
	switch opts.ShotRenderer {
	case "", "modern":
		return encodeShotPNG(opts.Shot, modern)
	case "classic":
		return encodeShotPNG(opts.Shot, record.Image)
	case "both":
		return compareShotImages(opts.Shot, record.Image, modern, opts.ShotRendererMax)
	default:
		return fmt.Errorf("nanolathe: shot model: --shot-renderer wants \"classic\", \"modern\" or \"both\", got %q", opts.ShotRenderer)
	}
}

// actualActivatedModelPose runs the production unit binding for one compiled
// definition. It intentionally does not encode ARMSOLAR's dish angles: the
// stock COB writes the per-piece accumulators and this snapshot publishes the
// resulting values [03 §2.4][04 §4.1].
func actualActivatedModelPose(fs *vfs.FS, def *content.UnitDef) ([]frame.PieceView, error) {
	if fs == nil || def == nil {
		return nil, fmt.Errorf("nanolathe: shot model: activation pose requires a compiled unit definition")
	}
	objectName := strings.TrimSpace(def.ObjectName)
	if objectName == "" || def.Script == nil {
		return nil, fmt.Errorf("nanolathe: shot model: activation pose: unit %q has no compiled object or COB", def.UnitName)
	}
	mdl, err := model3d.Load(fs, "objects3d/"+objectName+".3do")
	if err != nil {
		return nil, fmt.Errorf("nanolathe: shot model: activation pose: load %s: %w", objectName, err)
	}
	// Create runs with the unit's initial inactive state. The rising edge below
	// calls the same unit-owned activation writer used by production settlement;
	// no dish angle is supplied by the preview.
	u := &units.Unit{Def: def, Health: def.MaxDamage, MaxHealth: def.MaxDamage}
	binding, err := units.BindCOBWithPortsAndVisibilityForUnit(fs, u, mdl, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("nanolathe: shot model: activation pose: bind %s: %w", def.UnitName, err)
	}
	if binding == nil || binding.VM == nil || binding.Callbacks == nil {
		return nil, fmt.Errorf("nanolathe: shot model: activation pose: unit %q returned incomplete binding", def.UnitName)
	}
	settle := func() {
		// The preview probe's bounded settle is deliberately presentation-only;
		// it gives authored sleeps and piece moves enough ticks to reach their
		// settled snapshot without changing simulation state.
		for i := 0; i < 300; i++ {
			binding.Callbacks.Drain(1)
		}
	}
	settle()
	u.SetActivated(true)
	settle()
	flags := binding.VM.SnapshotFlags()
	poses := make([]frame.PieceView, len(binding.VM.Pieces))
	for i, state := range binding.VM.Pieces {
		name := ""
		if i < len(binding.Program.Pieces) {
			name = binding.Program.Pieces[i]
		}
		poses[i] = frame.PieceView{Index: i, Name: name, RotX: state.RotX, RotY: state.RotY, RotZ: state.RotZ, Tx: state.Trans[0], Ty: state.Trans[1], Tz: state.Trans[2], DontShade: state.DontShade, Hidden: state.Hidden, DontShadow: state.DontShadow}
		if i < len(flags) {
			poses[i].Hidden = flags[i]&1 == 0
			poses[i].DontShade = flags[i]&4 == 0
			poses[i].DontShadow = flags[i]&8 == 0
		}
	}
	return poses, nil
}

type modelPreviewDefinition struct {
	ObjectName string
	Structure  bool
	ZBuffer    bool
	Definition *content.UnitDef
}

// resolveModelPreviewDefinition makes --shot-model data-driven. The argument
// names a compiled unit key, or an object name shared by exactly one set of
// unit display properties. A bare arbitrary 3DO is rejected because it has no
// authored BMCode/ZBuffer values to select the production model path.
func resolveModelPreviewDefinition(catalog *content.Catalog, requested string) (modelPreviewDefinition, error) {
	if catalog == nil {
		return modelPreviewDefinition{}, fmt.Errorf("nanolathe: shot model: unit catalog is unavailable")
	}
	name := strings.TrimSpace(requested)
	base := name
	if slash := strings.LastIndexAny(base, "/\\"); slash >= 0 {
		base = base[slash+1:]
	}
	if strings.EqualFold(filepath.Ext(base), ".3do") {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if def, ok := catalog.Unit(base); ok {
		if strings.TrimSpace(def.ObjectName) == "" {
			return modelPreviewDefinition{}, fmt.Errorf("nanolathe: shot model: unit %q has no objectname", base)
		}
		return modelPreviewDefinition{ObjectName: strings.TrimSpace(def.ObjectName), Structure: def.BMCode == 0, ZBuffer: def.ZBuffer, Definition: def}, nil
	}
	var resolved modelPreviewDefinition
	found := false
	for _, key := range catalog.SortedUnitKeys() {
		def, ok := catalog.Unit(key)
		if !ok || !strings.EqualFold(strings.TrimSpace(def.ObjectName), base) {
			continue
		}
		candidate := modelPreviewDefinition{ObjectName: strings.TrimSpace(def.ObjectName), Structure: def.BMCode == 0, ZBuffer: def.ZBuffer, Definition: def}
		if found && (candidate.Structure != resolved.Structure || candidate.ZBuffer != resolved.ZBuffer) {
			return modelPreviewDefinition{}, fmt.Errorf("nanolathe: shot model: object %q has conflicting unit display properties; name a unit key", base)
		}
		resolved, found = candidate, true
	}
	if !found {
		return modelPreviewDefinition{}, fmt.Errorf("nanolathe: shot model: logical path %s, providers searched [compiled unit definitions], expected a unit key or a 3DO objectname with authored BMCode/ZBuffer", name)
	}
	return resolved, nil
}

type modelShotGame struct {
	record client.ModelPreviewRecord
	w, h   int
	gpu    *gpurender.Renderer
	out    *image.RGBA
	stats  gpurender.ModelStats
	done   bool
	err    error
}

func (g *modelShotGame) Update() error {
	if g.done {
		return ebiten.Termination
	}
	return nil
}
func (g *modelShotGame) Draw(screen *ebiten.Image) {
	if g.gpu == nil {
		var err error
		g.gpu, err = gpurender.NewChecked(g.record.Palette, g.w, g.h)
		if err != nil {
			g.err = err
			g.done = true
			return
		}
	}
	g.gpu.Clear()
	g.gpu.Fill(drawlist.Fill{Rect: drawlist.Rect{W: int32(g.w), H: int32(g.h)}, Index: g.record.Background, Style: drawlist.FillSolid})
	img := g.gpu.Execute(&g.record.List, g.w, g.h)
	if img == nil {
		g.err = fmt.Errorf("nanolathe: shot model: GPU executor returned no surface")
		g.done = true
		return
	}
	g.gpu.Expand()
	buf := make([]byte, 4*g.w*g.h)
	img.ReadPixels(buf)
	g.out = image.NewRGBA(image.Rect(0, 0, g.w, g.h))
	copy(g.out.Pix, buf)
	g.stats = g.gpu.ModelStats()
	screen.DrawImage(img, &ebiten.DrawImageOptions{})
	g.done = true
}
func (g *modelShotGame) Layout(int, int) (int, int) { return g.w, g.h }

func captureModernModel(record client.ModelPreviewRecord, w, h int) (*image.RGBA, gpurender.ModelStats, error) {
	g := &modelShotGame{record: record, w: w, h: h}
	ebiten.SetWindowVisible(false)
	ebiten.SetWindowSize(w, h)
	if err := ebiten.RunGame(g); err != nil {
		return nil, gpurender.ModelStats{}, fmt.Errorf("nanolathe: shot model capture loop: %w", err)
	}
	if g.err != nil {
		return nil, gpurender.ModelStats{}, g.err
	}
	if g.out == nil {
		return nil, gpurender.ModelStats{}, fmt.Errorf("nanolathe: shot model capture produced no frame")
	}
	return g.out, g.stats, nil
}
