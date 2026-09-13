package main

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// The five Enhanced effect toggles are Nanolathe commands with no retail
// counterpart (DESIGN_GPU_RENDERER §30). A direct battle owns the live value
// and its write-all captures it; the windowed shell owns the preference the
// host polls, so it writes there and saves the whole block.
func TestEffectChatCommandsToggleAndPersist(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	cl, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetEffects(presentationEffects(settings.DefaultPresentation()))
	if cl.Effects() != drawlist.AllEffects() {
		t.Fatalf("defaults did not reach the client: %+v", cl.Effects())
	}

	direct := &battleSession{cl: cl}
	for _, command := range []string{"+Water", "+lights", "+FINISH", "+heat", "+marks"} {
		direct.dispatchLocalCommand(command + " ignored")
	}
	if cl.Effects() != (drawlist.Effects{}) {
		t.Fatalf("direct toggles left %+v", cl.Effects())
	}
	stored, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Presentation.Water != 0 || stored.Presentation.Lighting != 0 || stored.Presentation.Finish != 0 ||
		stored.Presentation.Distortion != 0 || stored.Presentation.Marks != 0 {
		t.Fatalf("direct toggles stored %+v", stored.Presentation)
	}
	// Renderer and cap are not the commands' to write.
	if stored.Presentation.Renderer != settings.DefaultPresentation().Renderer {
		t.Fatalf("direct toggle rewrote the renderer: %+v", stored.Presentation)
	}

	shell := &gameShell{presentation: settings.DefaultPresentation(), settingsWritable: true}
	shellBattle := &battleSession{cl: cl, shell: shell}
	shellBattle.dispatchLocalCommand("+heat")
	if !cl.Effects().Distortion || shell.presentation.Distortion != 1 {
		t.Fatalf("shell toggle: live %+v shell %+v", cl.Effects(), shell.presentation)
	}
	stored, err = settings.Load()
	if err != nil || stored.Presentation.Distortion != 1 {
		t.Fatalf("shell toggle stored %+v err=%v", stored.Presentation, err)
	}
}
