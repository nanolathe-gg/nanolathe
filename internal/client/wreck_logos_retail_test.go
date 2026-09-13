package client

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// TestRetailWreckLogosCensus bounds the stock impact without making an
// install-specific count a rendering contract [03 R-RAST-01 §3].
func TestRetailWreckLogosCensus(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	textures := newModelTextureRegistry(fs, true)
	names := make([]string, 0)
	seen := make(map[string]bool)
	definitions := 0
	for _, def := range cat.Features {
		if def == nil || def.Object == "" {
			continue
		}
		definitions++
		name := strings.ToLower(def.Object)
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	var teamModels, teamFaces int
	for _, name := range names {
		path := name
		if !strings.HasSuffix(path, ".3do") {
			path = "objects3d/" + name + ".3do"
		}
		m, err := compiledmodel.Load(fs, path)
		if err != nil {
			t.Fatalf("feature model %q: %v", path, err)
		}
		faces := 0
		for _, piece := range m.Pieces {
			for pi, pr := range piece.Primitives {
				if piece.Selection && pi == 0 {
					continue
				}
				ref, ok := textures.resolve(pr.TextureName)
				if ok && ref.kind == texTeam && pr.IsColored&1 == 0 && len(pr.VertexIndices) == 4 {
					faces++
				}
			}
		}
		if faces != 0 {
			teamModels++
			teamFaces += faces
			t.Logf("LOGOS feature model %s: %d textured quads", path, faces)
		}
	}
	t.Logf("retail feature LOGOS census: %d model-backed definitions, %d unique models, %d models with %d team quads", definitions, len(names), teamModels, teamFaces)
}

// TestRetailWreckLogosFacesRestore locks the stock manifestation of
// [03 R-RAST-01 §3]: supplying player zero restores team quads that the absent
// selector omits. The capture pair isolates that correction at identical pose.
func TestRetailWreckLogosFacesRestore(t *testing.T) {
	c, _ := captureClient(t, 320, 240)
	c.buffer = frame.NewBuffer()
	var changed bool
	var tick uint32
	for _, heading := range []uint16{0, 8192, 16384, 24576} {
		var previous []uint8
		for _, present := range []bool{false, true} {
			f := c.buffer.BeginWrite()
			f.Players[0] = frame.PlayerRow{Present: present, Logo: 7}
			f.Players[3] = frame.PlayerRow{Present: true, Logo: 2}
			f.Selection.LocalPlayer = 3
			f.Visibility.SeaLevel = -200 << 16
			tick++
			if err := c.buffer.Publish(tick); err != nil {
				t.Fatal(err)
			}
			c.resetListForTest()
			clearIndexed(c)
			if !c.drawFeatureModel(frame.FeatureView{Model: "corkrog_dead", InstanceID: 1, Heading: heading, Owner: 3, OwnerKnown: true}) {
				t.Fatal("Krogoth wreck did not draw")
			}
			c.replayForTest()
			writeCapture(t, c, fmt.Sprintf("corkrog-wreck-h%d-player-zero-%t", heading, present))
			if !present {
				previous = append([]uint8(nil), c.indexed...)
			} else if !bytes.Equal(previous, c.indexed) {
				changed = true
			}
		}
	}
	if !changed {
		t.Fatal("restored stock LOGOS faces changed no visible pixels at any inspected heading")
	}
}
