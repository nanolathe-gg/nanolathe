package main

import "testing"

func TestFPSCommandTogglesHostDisplayAcrossBattles(t *testing.T) {
	shell := &gameShell{}
	first := &battleSession{shell: shell}
	if first.fpsShown() {
		t.Fatal("FPS display started enabled")
	}
	first.dispatchLocalCommand("+FpS")
	if !first.fpsShown() || !(&battleSession{shell: shell}).fpsShown() {
		t.Fatal("FPS display did not stay enabled for the next battle")
	}
	first.dispatchLocalCommand("+fps")
	if first.fpsShown() {
		t.Fatal("second command did not disable the FPS display")
	}
}
