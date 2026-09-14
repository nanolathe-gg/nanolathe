package gpurender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/platform/benchlock"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Optional static art review, using the installed models, palette and terrain.
// Each sheet has the ordinary silhouette on the left and the Enhanced aircraft
// shadow on the right. Both replay the same recorded list. Heights and poses
// are staged visual examples, not simulated flight or retail shadow claims.
// The existing silhouette comes from the finished body [03 R-REN-03D §1, §3].
func checkAircraftShadowCaptures() error {
	dir := os.Getenv("NANOLATHE_AIRCRAFT_SHOTS")
	if dir == "" {
		return nil
	}
	root := os.Getenv(testsupport.RetailAssetsEnv)
	if root == "" {
		return fmt.Errorf("aircraft captures require %s", testsupport.RetailAssetsEnv)
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(root); err != nil {
		return err
	}
	preview, err := client.NewModelPreviewRenderer(fs)
	if err != nil {
		return err
	}
	units, err := content.CompileUnits(fs)
	if err != nil {
		return err
	}
	mapName := os.Getenv("NANOLATHE_AIRCRAFT_MAP")
	if mapName == "" {
		mapName = "Seven Islands"
	}
	terrain, err := world.Load(fs, nil, mapName)
	if err != nil {
		return err
	}
	sites, err := aircraftCaptureSites(terrain)
	if err != nil {
		return fmt.Errorf("aircraft capture map %q: %w", mapName, err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	const w, h = 640, 480
	var renderer *Renderer
	for _, unitName := range []string{"armfig", "armthund"} {
		unit := units[unitName]
		if unit == nil || !unit.CanFly || unit.ObjectName == "" {
			return fmt.Errorf("aircraft capture requires installed aircraft definition %s", unitName)
		}
		for _, scale := range []int32{1, 2} {
			record, err := preview.RecordGeometry(client.ModelPreviewOptions{
				Model: unit.ObjectName, Width: w, Height: h, Scale: float32(scale),
				Heading: 8192, KeyPlane: unit.ZBuffer,
			})
			if err != nil {
				return err
			}
			if renderer == nil {
				renderer, err = NewChecked(record.Palette, w, h)
				if err != nil {
					return err
				}
				defer renderer.ResetSources()
			}
			var body *drawlist.ModelGeometry
			record.List.VisitModels(func(m drawlist.Model) {
				if m.Geometry != nil {
					body = m.Geometry.Clone()
				}
			})
			if body == nil || !body.Eligible {
				return fmt.Errorf("aircraft capture has no eligible geometry: %s", unitName)
			}
			for _, site := range sites {
				for _, altitude := range []int32{32, 200} {
					g := body.Clone()
					const shadowY = h * 3 / 5
					g.AnchorX = w/2 - 5*scale
					g.AnchorY = shadowY - altitude*scale/2
					g.AircraftShadowHeight = float32(altitude * scale)
					g.AircraftShadowScale = float32(scale)
					_, surface, _ := terrain.CursorToWorldMapPixels(int32(site.point.X), int32(site.point.Y))
					g.WorldHeight = float32((int32(surface>>16) + altitude) * scale)
					g.ReflectionSea = float32(int32(terrain.SeaLevel) * scale)
					g.ReflectWater = true
					g.Shadow = &drawlist.ModelGeometry{
						Eligible: true, KeyPlane: true, Silhouette: true,
						Width: g.Width, Height: g.Height, OriginX: g.OriginX, OriginY: g.OriginY,
						AnchorX: w / 2, AnchorY: shadowY, Scale: g.Scale,
					}
					// Later water phases make a short sequence for the shoreline
					// example. Aircraft and shadow placement remain frozen.
					phases := []uint32{30}
					if site.name == "shore" && unitName == "armthund" && altitude == 200 {
						phases = []uint32{30, 33, 36, 39, 42, 45}
						if scale == 2 {
							phases = make([]uint32, 60)
							for i := range phases {
								phases[i] = 30 + uint32(i)*2
							}
						}
					}
					for frame, tick := range phases {
						sheet := image.NewRGBA(image.Rect(0, 0, 2*w, h))
						var before []byte
						// The soft treatment has no switch: a recorded clearance
						// of zero is what selects the ordinary silhouette route,
						// so the left panel records that instead (§34).
						for col, soft := range []bool{false, true} {
							subject := g
							if !soft {
								subject = g.Clone()
								subject.AircraftShadowHeight = 0
							}
							var list drawlist.List
							list.RecordClear()
							list.RecordTerrain(drawlist.Terrain{
								Terrain: terrain, DstW: w, DstH: h, Scale: camera.ViewScale(scale * 2),
								OriginX: int32(site.point.X) - w/(2*scale), OriginY: int32(site.point.Y) - shadowY/scale,
								Water: drawlist.WaterSurface{Enabled: true, Tick: tick, Energy: .7},
							})
							list.RecordModel(drawlist.Model{Geometry: subject})
							list.RecordExpand()
							pixels := make([]byte, w*h*4)
							renderer.Execute(&list, w, h).ReadPixels(pixels)
							if col == 0 {
								before = pixels
							} else if bytes.Equal(before, pixels) {
								return fmt.Errorf("aircraft shadow comparison unchanged: %s %s scale=%d altitude=%d", unitName, site.name, scale, altitude)
							}
							panel := &image.RGBA{Pix: pixels, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
							draw.Draw(sheet, image.Rect(col*w, 0, (col+1)*w, h), panel, image.Point{}, draw.Src)
						}
						name := fmt.Sprintf("%s-%s-%dx-height%d-frame%02d.png", unitName, site.name, scale, altitude, frame)
						if os.Getenv("NANOLATHE_AIRCRAFT_PROFILE") == "1" && unitName == "armfig" && scale == 2 && altitude == 200 && site.name != "shore" {
							if err := profileAircraftShadows(dir, site.name, renderer, g); err != nil {
								return err
							}
						}
						if err := saveAircraftCapture(filepath.Join(dir, name), sheet); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	manifest := fmt.Sprintf("Static installed-art comparisons on %s. Left: ordinary silhouette. Right: Enhanced aircraft shadows.\nHeights 32 and 200 are staged world-pixel clearances above the projected surface. Poses are static, heading 8192; no flight or COB simulation.\nNative and 2x use the same painted map anchors. Shore bomber height200 sequence uses water ticks 30,33,36,39,42,45 at native scale and 30 through 148, step 2, at 2x.\n", mapName)
	for _, site := range sites {
		manifest += fmt.Sprintf("%s: painted map anchor %d,%d\n", site.name, site.point.X, site.point.Y)
	}
	return os.WriteFile(filepath.Join(dir, "README.txt"), []byte(manifest), 0644)
}

type aircraftCaptureSite struct {
	name  string
	point image.Point
}

// Choose sample sites from the same projected wet/dry classification used by
// the effect. A coast requires adjacent wet and dry mask texels; interior sites
// sample a surrounding square, leaving room for the model footprint.
func aircraftCaptureSites(terrain *world.Terrain) ([]aircraftCaptureSite, error) {
	pixels, w, h, step, _, _, _ := waterMaskPixels(terrain)
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("terrain has no projected wet/dry mask")
	}
	sites := []aircraftCaptureSite{{name: "land"}, {name: "water"}, {name: "shore"}}
	best := [3]int{int(^uint(0) >> 1), int(^uint(0) >> 1), int(^uint(0) >> 1)}
	margin, radius := (360+step-1)/step, max(1, 64/step)
	for y := margin; y < h-margin; y++ {
		for x := margin; x < w-margin; x++ {
			score := (x-w/2)*(x-w/2) + (y-h/2)*(y-h/2)
			wet := func(dx, dy int) bool { return pixels[((y+dy)*w+x+dx)*4] != 0 }
			dry := func(dx, dy int) bool { return pixels[((y+dy)*w+x+dx)*4+2] != 0 }
			kind := -1
			if (wet(0, 0) && dry(1, 0)) || (dry(0, 0) && wet(1, 0)) ||
				(wet(0, 0) && dry(0, 1)) || (dry(0, 0) && wet(0, 1)) {
				kind = 2
			} else {
				allWet, allDry := true, true
				for dy := -radius; dy <= radius; dy += radius {
					for dx := -radius; dx <= radius; dx += radius {
						allWet = allWet && wet(dx, dy)
						allDry = allDry && dry(dx, dy)
					}
				}
				if allWet {
					kind = 1
				} else if allDry {
					kind = 0
				}
			}
			if kind >= 0 && score < best[kind] {
				best[kind] = score
				sites[kind].point = image.Pt(x*step+step/2, y*step+step/2)
			}
		}
	}
	for _, site := range sites {
		if site.point == (image.Point{}) {
			return nil, fmt.Errorf("no suitable %s aircraft capture site; select another NANOLATHE_AIRCRAFT_MAP", site.name)
		}
	}
	return sites, nil
}

func saveAircraftCapture(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	encodeErr := png.Encode(f, img)
	closeErr := f.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}

// Optional paired completed-frame timing on frozen installed fighter geometry.
// Readback includes driver synchronization; these are not GPU timestamps.
func profileAircraftShadows(dir, site string, renderer *Renderer, body *drawlist.ModelGeometry) error {
	lockPath, err := benchlock.Path()
	if err != nil {
		return err
	}
	lock, err := benchlock.Acquire(lockPath, nil)
	if err != nil {
		return err
	}
	defer lock.Close()
	type result struct {
		Site                                                string
		Count, Run                                          int
		Enabled                                             bool
		Width, Height, Frames, Warmup                       int
		SubmitMedianMS, CompletionMedianMS, CompletionP95MS float64
		Draws, Passes                                       int
	}
	var results []result
	const w, h = 1280, 960
	for _, count := range []int{1, 64} {
		// Clearance zero selects the ordinary silhouette route, so the two
		// arms of the pairing differ in the recording, not in a switch (§34).
		build := func(soft bool) *drawlist.List {
			list := &drawlist.List{}
			list.RecordClear()
			terrain := renderer.water.record
			terrain.DstW, terrain.DstH = w, h
			list.RecordTerrain(terrain)
			for i := 0; i < count; i++ {
				g := body.Clone()
				x, y := int32(80+(i%8)*155), int32(125+(i/8)*115)
				g.AnchorX, g.AnchorY = x-10, y-80
				g.Shadow.AnchorX, g.Shadow.AnchorY = x, y
				if !soft {
					g.AircraftShadowHeight = 0
				}
				list.RecordModel(drawlist.Model{Geometry: g})
			}
			list.RecordExpand()
			return list
		}
		for run, on := range []bool{false, true, true, false} {
			list := build(on)
			var submits, completions []float64
			var img *ebiten.Image
			var pixel [4]byte
			for frame := 0; frame < 240; frame++ {
				start := time.Now()
				img = renderer.Execute(list, w, h)
				submitted := time.Since(start)
				img.SubImage(image.Rect(0, 0, 1, 1)).(*ebiten.Image).ReadPixels(pixel[:])
				completed := time.Since(start)
				if frame >= 60 {
					submits = append(submits, float64(submitted)/float64(time.Millisecond))
					completions = append(completions, float64(completed)/float64(time.Millisecond))
				}
			}
			sort.Float64s(submits)
			sort.Float64s(completions)
			stats := renderer.ModelStats()
			results = append(results, result{Site: site, Count: count, Run: run, Enabled: on, Width: w, Height: h, Frames: 180, Warmup: 60, SubmitMedianMS: submits[90], CompletionMedianMS: completions[90], CompletionP95MS: completions[171], Draws: stats.DeviceDraws, Passes: stats.Passes})
			if count == 64 && run < 2 {
				pixels := make([]byte, w*h*4)
				img.ReadPixels(pixels)
				if err := saveAircraftCapture(filepath.Join(dir, fmt.Sprintf("dense-%s-%v.png", site, on)), &image.RGBA{Pix: pixels, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}); err != nil {
					return err
				}
			}
		}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "profile-"+site+".json"), data, 0644)
}
