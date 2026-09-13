package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// TestWreckLogosUsesPlayerZeroColour exercises both production recording
// paths and keeps the pseudo-unit owner distinct from the placer/viewer
// [03 R-RAST-01 §3]. Missing publication is an absent-input boundary.
func TestWreckLogosUsesPlayerZeroColour(t *testing.T) {
	for _, geometryOnly := range []bool{false, true} {
		for _, tc := range []struct {
			name                     string
			colour                   uint8
			present, publish, buffer bool
			want                     bool
		}{
			{"colour seven", 7, true, true, true, true},
			{"colour zero", 0, true, true, true, true},
			{"unassigned", 255, true, true, true, false},
			{"past entry", 10, true, true, true, false},
			{"absent player", 7, false, true, true, false},
			{"unpublished", 7, true, false, true, false},
			{"no buffer", 7, true, false, false, false},
		} {
			name := "classic/" + tc.name
			if geometryOnly {
				name = "modern/" + tc.name
			}
			t.Run(name, func(t *testing.T) {
				c := newPieceFixtureClient(t)
				c.geometryOnlyModels = geometryOnly
				c.shading = false
				frames := make([]formats.GAFFrameRef, 10)
				for i := range frames {
					frames[i].Frame = &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{byte(100 + i)}}
				}
				c.texIndex = map[string]texRef{"logo": {kind: texTeam, key: "logo", entry: &formats.GAFEntry{Frames: frames}}}
				c.models = map[string]*unitModel{"wreck": teamLogoTestModel()}
				c.buffer = nil
				if tc.buffer {
					c.buffer = frame.NewBuffer()
					if tc.publish {
						f := c.buffer.BeginWrite()
						f.Players[0] = frame.PlayerRow{Present: tc.present, Logo: tc.colour}
						f.Players[3] = frame.PlayerRow{Present: true, Logo: 2}
						f.Selection.LocalPlayer = 3
						if err := c.buffer.Publish(1); err != nil {
							t.Fatal(err)
						}
					}
				}
				c.resetListForTest()
				got := c.drawFeatureModel(frame.FeatureView{Model: "wreck", InstanceID: 1, Owner: 3, OwnerKnown: true})
				if got != tc.want {
					t.Fatalf("feature recorded=%v want %v", got, tc.want)
				}
				if !tc.want {
					return
				}
				records := c.list.ModelCommands()
				if len(records) != 1 {
					t.Fatalf("model commands=%d want 1", len(records))
				}
				if geometryOnly {
					if records[0].Classic != nil {
						t.Fatal("modern retained a software image")
					}
					g := records[0].Geometry
					if g == nil || !g.Eligible || len(g.Faces) != 1 || g.Faces[0].Texture != frames[tc.colour].Frame {
						t.Fatal("modern geometry did not retain selected LOGOS frame")
					}
				} else {
					c.replayForTest()
					found := false
					for _, pixel := range c.indexed {
						if pixel == 100+tc.colour {
							found = true
						}
						if pixel == 102 {
							t.Fatal("wreck borrowed placer colour")
						}
					}
					if !found {
						t.Fatal("classic omitted selected LOGOS pixels")
					}
				}
			})
		}
	}
}
