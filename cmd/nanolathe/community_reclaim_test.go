package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func TestPreparedBuildKeepsActiveQuickKeyToggle(t *testing.T) {
	for _, tc := range []struct {
		name              string
		mode              gameplay.Mode
		enabled, prepared bool
		status, want      int
	}{
		{"strict", gameplay.Strict31, true, true, 1, 0},
		{"community", gameplay.Community39, true, true, 1, 1},
		{"modern", gameplay.Modern, true, true, 1, 1},
		{"disabled", gameplay.Community39, false, true, 1, 0},
		{"no build", gameplay.Community39, true, false, 1, 0},
		{"inactive", gameplay.Community39, true, true, 0, 1},
		{"low byte only", gameplay.Community39, true, true, 256, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &session.Session{Rules: session.RuleSetForMode(tc.mode), Community: community.Features{ReclaimToggleKeepsBuild: tc.enabled}}
			p := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{
				{Kind: gui.KindPanel},
				{Kind: gui.KindButton, Name: "BUILD", Active: 1, Attribs: 0x40, QuickKey: 'B'},
			}})
			p.SetStatusAt(1, tc.status)
			result := p.ServiceFrame(ui.WidgetFrame{Tokens: []input.Token{{Kind: input.TokenText, Rune: 'b'}}}, ui.WidgetHooks{
				PreserveActiveToggle: func(_ int, status int) bool { return s.PreservePreparedBuildToggle(tc.prepared, uint8(status)) },
			})
			if !result.Fired || result.FiredIndex != 1 || p.StatusAt(1) != tc.want {
				t.Fatalf("status=%d result=%+v, want status %d and ordinary firing", p.StatusAt(1), result, tc.want)
			}
		})
	}
}
