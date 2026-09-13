package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The unavailable notice is host policy. Exercise its actual menu callback
// and the ordinary modal's input ownership, without requiring a video decoder.
func TestIntroUnavailableReturnsToMainMenu(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { clPtr = previous })
	main := g.activePanel()
	audioOwner := g.audioOwner

	for _, tc := range []struct {
		name    string
		present bool
		want    string
	}{
		{"missing", false, "is missing"},
		{"present", true, "was found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.present {
				if err := os.Mkdir(filepath.Join(root, "Data"), 0755); err != nil {
					t.Fatal(err)
				}
				// Authored stand-in tests existence only; no retail movie bytes.
				if err := os.WriteFile(filepath.Join(root, "Data", "2.zrb"), []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			fs := vfs.New()
			if err := fs.MountDirectory(root, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { fs.Close() })
			g.cs = &contentSet{fs: fs}
			queuedWindowTail(cl)
			g.activateGadget("INTRO")
			modal := g.frontend.Panels.Modal()
			if modal == nil || !strings.Contains(modal.Message(), tc.want) || !strings.Contains(modal.Message(), "Data/2.zrb") || !strings.Contains(modal.Message(), "not yet supported") {
				t.Fatalf("Intro notice = %q", modal.Message())
			}
			if cl.Input().PendingTokens() != 0 {
				t.Fatal("Intro notice retained the opening input tail")
			}
			if dir := os.Getenv("NANOLATHE_OPTIONS_SHOT"); dir != "" {
				writeShellShot(t, cl, filepath.Join(dir, "intro-"+tc.name+".png"))
			}
			cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
			g.menuInput(cl)
			if g.frontend.Panels.Modal() != nil || g.frontend.Mode != modeMenuMain || g.activePanel() != main || g.audioOwner != audioOwner {
				t.Fatal("closing Intro notice failed to preserve the main menu and audio owner")
			}
		})
	}
}
