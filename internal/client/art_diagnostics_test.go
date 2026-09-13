package client

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestArtDiagnosticsPreserveNegativeCacheAndBoundIdentities(t *testing.T) {
	root := t.TempDir()
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	c := &Client{modelFS: fs}
	if c.EffectBank("missing") != nil {
		t.Fatal("missing bank resolved")
	}
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "art", Frames: []formats.GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "anims"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "anims", "missing.gaf"), data, 0644); err != nil {
		t.Fatal(err)
	}
	freshFS := vfs.New()
	if err := freshFS.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = freshFS.Close() })
	// Make the file available without resetting the client’s failed bank entry.
	c.modelFS = freshFS
	for i := 0; i < 10; i++ {
		if c.EffectBank("MISSING") != nil {
			t.Fatal("negative bank cache retried")
		}
	}
	if len(c.artDiagnostics) != 1 || c.artDiagnostics[0].Reason == "" {
		t.Fatalf("diagnostics = %+v", c.artDiagnostics)
	}
	bank, err := formats.LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	c.effectBanks["good"] = bank
	for i := 0; i < maxArtDiagnostics*2; i++ {
		c.effectEntry("good", fmt.Sprint("missing", i))
	}
	if len(c.artDiagnostics) != maxArtDiagnostics || !c.artDiagnosticsTruncated {
		t.Fatal("diagnostic storage is not bounded")
	}
	d := c.DebugSnapshot()
	d["art_diagnostics"].([]ArtDiagnostic)[0].Path = "caller mutation"
	if c.artDiagnostics[0].Path == "caller mutation" {
		t.Fatal("debug snapshot borrows mutable storage")
	}
	c.SetModelFS(freshFS)
	if len(c.artDiagnostics) != 0 || c.artDiagnosticsTruncated || c.EffectBank("missing") == nil {
		t.Fatal("source reset did not retire failures and negative cache")
	}
}

func TestArtDiagnosticsExposeRecordingResolutionCounters(t *testing.T) {
	c := stripTestClient(t)
	f := stripTestFrame(true, frame.StripView{Strip: 0, Family: frame.StripFamilySmokePuff, Entry: "absent", Bank: "fx"})
	f.Effects = []frame.EffectView{{ID: 1, Kind: frame.KindExplosion.String(), Strip: -1, Graphic: "absent", AssetID: "fx", ActiveA: true}}
	c.drawFixedEffects(f)
	c.drawStripSlot(f, 0)
	d := c.DebugSnapshot()
	if d["recorded_effect_stats"].(EffectDrawStats).Skipped != 1 || d["recorded_strip_stats"].(StripDrawStats).Unresolved != 1 {
		t.Fatalf("resolution counters = %+v", d)
	}
	if len(c.artDiagnostics) != 1 {
		t.Fatal("repeated missing entry was not deduplicated")
	}
	c.composeIndexed(nil, false)
	if c.effectStats != (EffectDrawStats{}) || c.stripStats != (StripDrawStats{}) {
		t.Fatal("recording counters accumulated across passes")
	}
}
