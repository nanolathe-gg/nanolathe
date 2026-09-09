package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestBuilderPageProbeDoesNotPoisonRequiredLoad(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "guis"), 0o755); err != nil {
		t.Fatalf("mkdir guis: %v", err)
	}
	guiBytes, err := os.ReadFile(filepath.Join("..", "..", "internal", "gui", "testdata", "defaults.gui"))
	if err != nil {
		t.Fatalf("read authored GUI fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "guis", "arm1.gui"), guiBytes, 0o644); err != nil {
		t.Fatalf("write GUI fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "anims"), 0o755); err != nil {
		t.Fatalf("mkdir anims: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "anims", "arm1.gaf"), []byte("malformed GAF"), 0o644); err != nil {
		t.Fatalf("write malformed GAF fixture: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatalf("mount fixture: %v", err)
	}

	probe := &retailBattleHUD{fs: fs, side: &content.SideDef{NamePrefix: "arm"}}
	if window, page := probe.loadWindowProbe("arm2"); window != nil || page != nil {
		t.Fatalf("first missing numbered page unexpectedly resolved: window=%v page=%v", window, page)
	}
	if probe.assetErr != nil {
		t.Fatalf("first missing numbered page poisoned construction: %v", probe.assetErr)
	}

	required := &retailBattleHUD{fs: fs, side: &content.SideDef{NamePrefix: "arm"}}
	if window, page := required.loadWindowProbe("arm1"); window == nil || page != nil {
		t.Fatalf("authored page probe = window %v, page %v; want GUI with optional page art", window, page)
	}
	if !required.pageChecked["arm1"] {
		t.Fatal("page probe did not validate the named art lookup")
	}
	// A later required access on the same HUD must retain the cached GUI while
	// still carrying the already-validated null page art through the support
	// GAF/BUTTONS0 fallback chain [07 §6]. Missing or malformed page GAF is not
	// a construction failure when that chain remains available.
	if window, page, err := required.loadWindowRequired("arm1"); err != nil || window == nil || page != nil {
		t.Fatalf("required cached page = window %v, page %v, err %v; want GUI and optional null art", window, page, err)
	}

	if _, _, err := required.loadWindowRequired("arm3"); err == nil {
		t.Fatal("selected existing page with missing GUI was accepted")
	} else if !strings.Contains(err.Error(), "logical path guis/arm3.gui") || !strings.Contains(err.Error(), "providers searched") {
		t.Fatalf("selected page GUI error lacks provider-aware logical path: %v", err)
	}
}
