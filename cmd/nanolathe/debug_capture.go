package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/debugcapture"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// handleDebugCapture owns the entire triggering host sample, before modal or
// gameplay dispatch. This is a development shortcut, not a retail binding.
func (b *battleSession) handleDebugCapture(cl *client.Client) bool {
	if b == nil || b.sess == nil || cl == nil {
		return false
	}
	in := cl.Input()
	if in == nil || in.Kbd == nil || !in.Kbd.KeyDown(input.KeyF11) || !in.Kbd.HasShift() || !in.Kbd.KeyHeld(input.KeyCtrl) {
		return false
	}
	cl.JoinPreRecord()
	in.DiscardTokens(in.PendingTokens())
	in.FlushPointers()
	in.Kbd.ResetEdges()
	in.Mouse.ResetEdges()
	if b.debugCaptureBusy {
		return true
	}
	b.debugCaptureBusy = true
	defer func() { b.debugCaptureBusy = false }()
	b.cl = cl
	before := b.sess.Clock != nil && b.sess.Clock.Paused
	b.applyBattleSchedule(ui.BattleScheduleIntent{PauseSet: true, Pause: true})
	directory, err := b.writeDebugCapture(cl, before)
	b.debugCapturePath = directory
	b.debugCaptureError = err
	message := "Diagnostic capture: " + directory
	if err != nil {
		message += " (incomplete: " + err.Error() + ")"
	}
	fmt.Fprintln(os.Stderr, message)
	if ring := b.messageRing(); ring != nil {
		status := "Diagnostic capture saved at " + time.Now().Format("15:04:05") + "."
		if err != nil {
			status = "Diagnostic capture incomplete; see stderr/manifest.json."
		}
		ring.Append(status, 4, 0, 10, b.currentTick())
		if directory != "" {
			parent := filepath.Dir(directory)
			if home, homeErr := os.UserHomeDir(); homeErr == nil {
				parent = strings.Replace(parent, home+string(os.PathSeparator), "~/", 1)
			}
			ring.Append("Folder: "+parent, 4, 0, 10, b.currentTick())
		}
	}
	return true
}

func (b *battleSession) writeDebugCapture(cl *client.Client, pausedBefore bool) (string, error) {
	metadata := map[string]any{"map": b.sess.Skirmish.MapName,
		"authoritative_tick": b.sess.DebugClock().GlobalTick,
		"paused_before":      pausedBefore,
		"paused_after":       b.sess.DebugClock().Paused,
		"fixed_point_units":  "raw signed 16.16; angle circle 65536",
		"boundary":           "game owner before session step; recorder joined; synchronous capture"}
	if b.sess.Snapshot != nil {
		if f := b.sess.Snapshot.Current(); f != nil {
			metadata["committed_tick"] = f.Tick
			metadata["committed_paused"] = f.Paused
		}
	}
	capture, err := debugcapture.Begin(b.debugCaptureBase, metadata)
	if err != nil {
		return "", err
	}
	capture.Profiles()
	capture.ProcessMemory()
	capture.JSON("session.json", b.sess.DebugSnapshot())
	capture.Write("units.jsonl", func(w io.Writer) error {
		encoder := json.NewEncoder(w)
		return b.sess.VisitDebugUnits(func(unit session.DebugUnit) error { return encoder.Encode(unit) })
	})
	capture.JSON("client.json", cl.DebugSnapshot())
	capture.JSON("battle.json", map[string]any{"collected_at": time.Now(),
		"modal":                b.battleState().Modal(),
		"battle_input":         b.battleState().Input,
		"footer_hover_unit":    b.footerHoverUnit,
		"footer_hover_feature": b.footerHoverFeature,
		"tick_fraction":        b.lastTickFraction,
		"drag_scroll":          b.dragScrollActive,
		"chat_active":          b.chat.active})
	if b.sess.Movement != nil {
		capture.JSON("movement.json", b.sess.Movement.ParitySnapshot(b.sess.Units, b.sess.DebugClock().GlobalTick))
	} else {
		capture.Unavailable("movement.json", "movement service absent")
	}
	if b.sess.Features != nil {
		capture.JSON("features.json", b.sess.Features.DebugSnapshot())
	} else {
		capture.Unavailable("features.json", "feature service absent")
	}
	if b.sess.Combat != nil {
		capture.JSON("projectiles.json", b.sess.Combat.DebugSnapshot())
	} else {
		capture.Unavailable("projectiles.json", "combat service absent")
	}
	if b.sess.Build != nil {
		capture.JSON("construction.json", b.sess.Build.SnapshotLinks())
		capture.JSON("construction-admissions.json", b.sess.Build.DebugAdmissionSnapshot())
	} else {
		capture.Unavailable("construction.json", "construction service absent")
		capture.Unavailable("construction-admissions.json", "construction service absent")
	}
	aiStates := make([]any, len(b.sess.AI))
	for i, m := range b.sess.AI {
		if m != nil {
			aiStates[i] = m.DebugSnapshot()
		}
	}
	capture.JSON("ai.json", aiStates)
	capture.Device(cl.WriteDebugDeviceCapture)
	capture.Manifest.Omissions = append(capture.Manifest.Omissions,
		"Diagnostic projection is not a restartable save. No save projection is attempted: callbacks and transient runtime state have no complete faithful restore contract.",
		"Private scheduler pending/slew/flags, mission trigger state, visibility grids and path-search internal heaps are not captured.",
		"Orders retain primary/secondary queues and scalar payloads; route geometry is in movement.json. Existing snapshot bounds/truncation flags apply. Callback closures are represented only by presence, never serialized.",
		"Feature snapshot omits animation cursors and active-event order. Combat snapshot omits pending aim registry and target registries. Construction admission history is bounded; historical occupancy grids and AI selection/placement decisions are not retained. AI omits per-definition strategic vectors and profiles.",
		"Renderer captures the last retained offscreen composition. Exact presented tick and historical storage peaks are unavailable. GPU image bytes are logical estimates, not total driver memory.")
	return capture.Directory, capture.Finish()
}
