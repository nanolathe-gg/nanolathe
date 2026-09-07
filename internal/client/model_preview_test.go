package client

import (
	"bytes"
	"testing"

	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestModelPreviewRejectsUnusableInputs(t *testing.T) {
	if _, err := NewModelPreviewRenderer(nil); err == nil {
		t.Fatal("nil VFS unexpectedly accepted")
	}
	r := &ModelPreviewRenderer{client: &Client{}, palette: nil}
	if _, err := r.RenderModel(ModelPreviewOptions{Model: "armcom", Width: 128, Height: 128}); err == nil {
		t.Fatal("uninitialized renderer unexpectedly accepted")
	}
}

func TestModelPreviewRendersRetailArmCommanderDeterministically(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Skipf("retail assets not mountable: %v", err)
	}
	defer fs.Close()
	r, err := NewModelPreviewRenderer(fs)
	if err != nil {
		t.Skipf("retail presentation assets not loadable: %v", err)
	}
	opts := ModelPreviewOptions{
		Model: "armcom", Owner: 0, Width: 192, Height: 160,
		Scale: 2, Background: 0, KeyPlane: true,
	}
	record, err := r.RecordModel(opts)
	if err != nil {
		t.Fatalf("front: %v", err)
	}
	front := record.Image
	models := record.List.ModelCommands()
	if len(models) != 1 || models[0].Geometry == nil {
		t.Fatalf("recorded preview did not retain its model geometry: %#v", models)
	}
	if !models[0].Geometry.Eligible {
		t.Fatalf("ordinary preview geometry unexpectedly fell back: %v", models[0].Geometry.Fallback)
	}
	again, err := r.RenderModel(opts)
	if err != nil {
		t.Fatalf("front repeat: %v", err)
	}
	if !bytes.Equal(front.Pix, again.Pix) {
		t.Fatal("same model inputs produced different pixels")
	}
	opts.Model = "armcom.3do"
	withExtension, err := r.RenderModel(opts)
	if err != nil {
		t.Fatalf("extension shorthand: %v", err)
	}
	if !bytes.Equal(front.Pix, withExtension.Pix) {
		t.Fatal("armcom and armcom.3do resolved to different model pixels")
	}
	opts.Model = "armcom"
	background := r.palette.Base[opts.Background]
	foreground := 0
	for i := 0; i < len(front.Pix); i += 4 {
		if front.Pix[i] != background[0] || front.Pix[i+1] != background[1] || front.Pix[i+2] != background[2] {
			foreground++
		}
	}
	if foreground == 0 {
		t.Fatal("ARM Commander preview contains no model pixels")
	}

	opts.Heading = 16384
	right, err := r.RenderModel(opts)
	if err != nil {
		t.Fatalf("right: %v", err)
	}
	if bytes.Equal(front.Pix, right.Pix) {
		t.Fatal("front and right headings produced identical pixels")
	}

	// This is an explicit static pose, not a claim that preview runs activation
	// COB. The model's real dish names and the 135-degree Z pose are locked by
	// the asset-gated renderer review recipe [03 R-REN-03A §3]. It keeps the
	// production structure/shaded branch and disables only its 2x resolve so
	// the P2 preview exercises the scale-one geometry packet.
	solar, err := r.RecordModel(ModelPreviewOptions{
		Model: "armsolar", Owner: 0, Width: 192, Height: 160, Scale: 2,
		Background: 0, KeyPlane: true, Structure: true, DisableAntiAlias: true,
		PiecePoses: ARMSOLAROpenPreviewPose(),
	})
	if err != nil {
		t.Fatalf("open solar static pose: %v", err)
	}
	solarModels := solar.List.ModelCommands()
	if len(solarModels) != 1 || solarModels[0].Geometry == nil || !solarModels[0].Geometry.Eligible {
		t.Fatalf("open solar did not produce scale-one geometry: %#v", solarModels)
	}
	hasShadedTexture := false
	for _, face := range solarModels[0].Geometry.Faces {
		if face.Shaded && face.Texture != nil {
			hasShadedTexture = true
			break
		}
	}
	if !hasShadedTexture {
		t.Fatal("open solar scale-one packet lost the production shaded texture path")
	}
	closed, err := r.RenderModel(ModelPreviewOptions{
		Model: "armsolar", Owner: 0, Width: 192, Height: 160, Scale: 2,
		Background: 0, KeyPlane: true, Structure: true, DisableAntiAlias: true,
	})
	if err != nil {
		t.Fatalf("closed solar static pose: %v", err)
	}
	if bytes.Equal(closed.Pix, solar.Image.Pix) {
		t.Fatal("open solar pose matched closed solar; dish pose was not applied")
	}
	// A later default preview restores the renderer's regular structure AA
	// setting. Its modern packet remains native-scale geometry even while the
	// classic reference below uses its ordinary supersample resolve.
	defaultSolar, err := r.RecordModel(ModelPreviewOptions{
		Model: "armsolar", Owner: 0, Width: 192, Height: 160, Scale: 2,
		Background: 0, KeyPlane: true, Structure: true,
	})
	if err != nil {
		t.Fatalf("default solar preview: %v", err)
	}
	defaultModels := defaultSolar.List.ModelCommands()
	if len(defaultModels) != 1 || defaultModels[0].Geometry == nil || !defaultModels[0].Geometry.Eligible || defaultModels[0].Geometry.Scale != 1 {
		t.Fatalf("default solar did not retain native modern geometry: %#v", defaultModels)
	}
	if !r.client.antiAlias {
		t.Fatal("preview DisableAntiAlias leaked into the next render")
	}
	modernSolar, err := r.RecordGeometry(ModelPreviewOptions{
		Model: "armsolar", Owner: 0, Width: 192, Height: 160, Scale: 2,
		Background: 0, KeyPlane: true, Structure: true,
	})
	if err != nil {
		t.Fatalf("modern solar geometry: %v", err)
	}
	if modernSolar.Image != nil {
		t.Fatal("geometry-only preview returned a CPU image")
	}
	modernModels := modernSolar.List.ModelCommands()
	if len(modernModels) != 1 || modernModels[0].Geometry == nil || !modernModels[0].Geometry.Eligible || modernModels[0].Geometry.Scale != 1 {
		t.Fatalf("modern solar did not record native geometry: %#v", modernModels)
	}
	if got := len(r.client.modelCommits); got != 0 {
		t.Fatalf("geometry-only preview retained %d CPU model commits", got)
	}

	loaded, err := compiledmodel.Load(fs, "objects3d/armsolar.3do")
	if err != nil {
		t.Fatalf("load solar piece names: %v", err)
	}
	available := make(map[string]bool, len(loaded.Pieces))
	for _, piece := range loaded.Pieces {
		available[piece.Name] = true
	}
	for _, pose := range ARMSOLAROpenPreviewPose() {
		if !available[pose.Name] {
			t.Fatalf("stock ARMSOLAR is missing posed piece %q", pose.Name)
		}
	}
}

func TestARMSOLAROpenPreviewPose(t *testing.T) {
	poses := ARMSOLAROpenPreviewPose()
	want := []string{"dish1", "dish2", "dish3", "dish4"}
	if len(poses) != len(want) {
		t.Fatalf("dish pose count = %d, want %d", len(poses), len(want))
	}
	for i := range want {
		if poses[i].Name != want[i] || poses[i].RotZ != 24576 {
			t.Fatalf("pose %d = %#v, want %s at 135 degrees", i, poses[i], want[i])
		}
	}
}
