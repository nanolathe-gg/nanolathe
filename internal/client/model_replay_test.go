package client

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func classicReplayImage(color []byte, coverage []bool, key []byte, x, y int32) *drawlist.ClassicModelImage {
	return &drawlist.ClassicModelImage{
		Color: color, Coverage: coverage, Key: key,
		Width: int32(len(color)), Height: 1, AnchorX: x, AnchorY: y,
		Transparent: transparentModelIndex,
	}
}

// TestClassicModelPacketReplaysAfterRecorderReset locks the durable classic
// half of C-G5. A records body and shadow operands, B changes their placement,
// then A still replays byte-for-byte after the recorder and its source slices
// have been reused. The test also makes the carried child's shadow its own
// earlier command: its ALP write precedes the carrier's sole body blit.
func TestClassicModelPacketReplaysAfterRecorderReset(t *testing.T) {
	c := &Client{width: 8, height: 4, indexed: make([]byte, 32), pal: &palette.Tables{}}
	for i := range c.indexed {
		c.indexed[i] = 7
	}
	// A shadow source index of zero darkens the current 7 to 3. Its own body
	// then covers the centre with 9, while the separately staged-child shadow
	// remains visible at the next pixel.
	c.pal.Alpha[0*256+7] = 3
	c.pal.Alpha[0*256+9] = 4
	childShadow := classicReplayImage([]byte{0}, []bool{true}, []byte{60}, 3, 1)
	carrierShadow := classicReplayImage([]byte{0}, []bool{true}, []byte{61}, 1, 1)
	body := classicReplayImage([]byte{9, 1}, []bool{true, false}, []byte{62, 63}, 1, 1)

	var observed []string
	c.list.RecordModel(drawlist.Model{ShadowOnly: true, Classic: &drawlist.ClassicModel{
		Shadow:   childShadow,
		Observer: func(*drawlist.ClassicModelImage, []byte, int, int) { observed = append(observed, "child-shadow") },
	}})
	c.list.RecordModel(drawlist.Model{Classic: &drawlist.ClassicModel{
		Shadow: carrierShadow, Body: body,
		Observer: func(_ *drawlist.ClassicModelImage, pixels []byte, _ int, _ int) {
			observed = append(observed, "carrier-body")
			pixels[0] = 99 // observer gets a read snapshot, never the framebuffer
		},
	}})

	recorded := c.list.Clone()
	// Mutating all three mutable plane kinds after cloning must not change A.
	childShadow.Color[0], childShadow.Coverage[0], childShadow.Key[0] = 99, false, 99
	carrierShadow.Color[0], carrierShadow.Coverage[0], carrierShadow.Key[0] = 99, false, 99
	body.Color[0], body.Coverage[0], body.Key[0] = 99, false, 99
	c.list.Reset()
	c.list.RecordModel(drawlist.Model{Classic: &drawlist.ClassicModel{Body: classicReplayImage([]byte{12}, []bool{true}, []byte{80}, 6, 2)}})

	for i := range c.indexed {
		c.indexed[i] = 7
	}
	recorded.Replay(c.classicSink())
	want := []byte{
		7, 7, 7, 7, 7, 7, 7, 7,
		7, 9, 7, 3, 7, 7, 7, 7,
		7, 7, 7, 7, 7, 7, 7, 7,
		7, 7, 7, 7, 7, 7, 7, 7,
	}
	if !bytes.Equal(c.indexed, want) {
		t.Fatalf("replayed A = %v, want %v", c.indexed, want)
	}
	if got, want := len(observed), 2; got != want || observed[0] != "child-shadow" || observed[1] != "carrier-body" {
		t.Fatalf("observer order = %v, want child shadow then carrier body", observed)
	}

	// Nil and no-body commands are valid record shapes and cannot alter pixels.
	before := append([]byte(nil), c.indexed...)
	var none drawlist.List
	none.RecordModel(drawlist.Model{})
	none.RecordModel(drawlist.Model{Classic: &drawlist.ClassicModel{}})
	none.Replay(c.classicSink())
	if !bytes.Equal(c.indexed, before) {
		t.Fatal("nil/no-body classic commands changed pixels")
	}
}

// TestRecordedModelPoseSurvivesNextRecord uses the actual composer rather than
// a hand-authored packet: it records pose A, records a visibly different B,
// then replays A through classic after the recorder has been reset. The frozen
// packet must still reproduce A exactly [03 R-REN-03A §1][C-G5].
func TestRecordedModelPoseSurvivesNextRecord(t *testing.T) {
	c := testModelTextureClient()
	c.width, c.height = 640, 480
	c.indexed = make([]byte, c.width*c.height)
	c.pal = &palette.Tables{}
	base := [][3]numeric.Fixed{
		fixedVertex(0, 1, 0), fixedVertex(8, 1, 0), fixedVertex(8, 1, -8), fixedVertex(0, 1, -8),
	}
	draw := testPrimitiveDraw(presentationrender.PrimitiveDraw{
		IsColored: 1, ColorIndex: 19, VertexIndices: []uint16{0, 1, 2, 3},
	}, base)
	draw.KeyPlane, draw.CastsShadow = true, true
	c.modelScratch.active = true

	if !c.drawModel(draw, 0, teamColor{}, 1, modelCursorUnit, nil, 0) {
		t.Fatal("pose A did not record")
	}
	poseA := c.list.Clone()
	models := poseA.ModelCommands()
	if len(models) != 1 || models[0].Classic == nil || models[0].Classic.Body == nil || models[0].Classic.Shadow == nil {
		t.Fatalf("pose A lacks owned classic body/shadow: %#v", models)
	}
	poseA.Replay(c.classicSink())
	want := append([]byte(nil), c.indexed...)

	// Change B's composed model-space pose: the right edge becomes a taller,
	// slanted shape. It is a fresh record in the same reusable List.
	c.list.Reset()
	c.modelScratch.reset()
	draw.Pieces[0].WorldVertices[1][1] += numeric.Fixed(6 << 16)
	draw.Pieces[0].WorldVertices[2][1] += numeric.Fixed(6 << 16)
	if !c.drawModel(draw, 0, teamColor{}, 2, modelCursorUnit, nil, 0) {
		t.Fatal("pose B did not record")
	}
	for i := range c.indexed {
		c.indexed[i] = 0
	}
	poseA.Replay(c.classicSink())
	if !bytes.Equal(c.indexed, want) {
		t.Fatal("replaying pose A after recording B changed its classic pixels")
	}
}
