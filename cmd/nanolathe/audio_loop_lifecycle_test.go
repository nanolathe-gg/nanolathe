package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type shellLoopOutput struct {
	loops int
	stops int
}

func (*shellLoopOutput) PlaySample(*audio.Sample, float64, float64) error { return nil }
func (*shellLoopOutput) PlayRegisteredSample(*audio.Sample, float64, float64) error {
	return nil
}
func (o *shellLoopOutput) PlayLoopingRegisteredSample(*audio.Sample, float64, float64) error {
	o.loops++
	return nil
}
func (o *shellLoopOutput) StopVoices() { o.stops++ }

func loopTestWAV() []byte {
	data := []byte{128, 192}
	wav := make([]byte, 44+len(data))
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 11025)
	binary.LittleEndian.PutUint32(wav[28:], 11025)
	binary.LittleEndian.PutUint16(wav[32:], 1)
	binary.LittleEndian.PutUint16(wav[34:], 8)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(data)))
	copy(wav[44:], data)
	return wav
}

func TestMenuLoopLifecycleDefersAndStopsAtTransitions(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sounds"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sounds", "bgm.wav"), loopTestWAV(), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	old := audio.GlobalOutput()
	output := &shellLoopOutput{}
	audio.SetGlobalOutput(output)
	t.Cleanup(func() { audio.SetGlobalOutput(old) })

	shell := &gameShell{cs: &contentSet{fs: fs}, frontend: ui.NewFrontend(modeMenuMain), audioPrefs: settings.DefaultAudio()}
	shell.ensureFrontendAudio().Registry.RegisterPath(menuBGMAlias, "sounds/bgm.wav")
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 64, Height: 48})
	if err != nil {
		t.Fatal(err)
	}
	shell.openMenu(modeMenuMain)
	if output.loops != 0 {
		t.Fatal("openMenu started BGM before common presentation step")
	}
	shell.step(0, cl)
	if output.loops != 1 {
		t.Fatalf("first common step BGM starts = %d, want 1", output.loops)
	}
	shell.openMenu(modeMenuMain)
	shell.step(0, cl)
	if output.loops != 2 {
		t.Fatalf("MAINMENU rebuild BGM starts = %d, want 2", output.loops)
	}
	oldPanel, oldAssets, oldState := optionsPanel, optionsAssets, optionsState
	t.Cleanup(func() { optionsPanel, optionsAssets, optionsState = oldPanel, oldAssets, oldState })
	optionsPanel = ui.NewPanel(&gui.Window{})
	optionsAssets = &retailPanelAssets{window: optionsPanel.Window}
	optionsState = &retailOptionsState{}
	shell.frontend.Panels.Replace(optionsPanel)
	shell.audioPrefs.SoundMode = settings.SoundModeOff
	if !shell.activateRetailOptionsGadget("MODE") {
		t.Fatal("sound MODE did not reach its active options handler")
	}
	if output.loops != 2 {
		t.Fatal("MODE reissue started BGM before common presentation step")
	}
	shell.step(0, cl)
	if output.loops != 3 {
		t.Fatalf("Mono transition BGM starts = %d, want 3", output.loops)
	}

	shell.beginFreshBattleLoad("", modeMenuMain, freshBattleRequest{}, nil)
	if output.stops != 1 {
		t.Fatalf("fresh loading stop calls = %d, want 1", output.stops)
	}

	// A failed saved-load preflight returns before the successful-commit stop.
	if err := shell.loadRetailSavePath(filepath.Join(root, "missing.save")); err == nil {
		t.Fatal("missing save unexpectedly loaded")
	}
	if output.stops != 1 {
		t.Fatal("saved-load preflight failure stopped frontend voices")
	}
	shell.commitBattleCandidate(&battleSession{})
	if output.stops != 2 {
		t.Fatalf("successful battle commit stop calls = %d, want 2", output.stops)
	}
}
