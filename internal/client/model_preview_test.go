package client

import (
	"bytes"
	"testing"

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
	front, err := r.RenderModel(opts)
	if err != nil {
		t.Fatalf("front: %v", err)
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
}
