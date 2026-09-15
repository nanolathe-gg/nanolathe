package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
)

// beginArrival is an authored modern skirmish opening, not a retail behavior.
// It resolves the commander from published units and immutable definitions;
// saves and campaign placement never call it (DESIGN_GPU_RENDERER §36).
func (b *battleSession) beginArrival(cl *client.Client) bool {
	if b == nil || b.sess == nil || b.sess.Mission == nil || b.sess.Mission.Type != mission.TypeSkirmish || cl == nil || cl.Buffer() == nil {
		return false
	}
	if !b.sess.PublishOpeningFrame() {
		return false
	}
	u, ok := arrivalCommander(cl.Buffer().Current(), b.cat)
	if !ok {
		return false
	}
	// The opening frames the landing after final viewport/zoom selection.
	// This is presentation choreography, including the unit's height shear.
	if b.cam != nil {
		b.cam.JumpToBattleViewCenter(int32(u.X>>16), int32(u.Z>>16)-int32(u.Y>>17))
	}
	cl.StartArrival(u)
	return true
}

func arrivalCommander(cur *frame.Frame, cat *content.Catalog) (frame.UnitView, bool) {
	if cur != nil && cat != nil {
		for _, u := range cur.Units {
			if u.Owner != cur.Selection.LocalPlayer {
				continue
			}
			if def := cat.Units[content.CanonicalKey(u.DefName)]; def != nil && def.Commander {
				return u, true
			}
		}
	}
	return frame.UnitView{}, false
}

// stepArrival holds input and the authoritative pump before gameplay. Rebase
// its host anchor at handoff so intro time cannot turn into catch-up ticks.
// This deliberately does not use retail pause, whose resume burst is real
// behavior [01 §4.3]; the intro is outside that scheduler (GPU §36).
func (b *battleSession) stepArrival(delta float64, cl *client.Client) bool {
	if !cl.ArrivalActive() {
		return false
	}
	seconds := cl.ArrivalSeconds()
	if cl.IsFocused() && delta > 0 {
		// Loading/device stalls must not consume the entire opening unseen.
		seconds += float32(min(delta, 0.05))
	}
	if in := cl.Input(); in != nil {
		if in.Kbd.KeyDown(input.KeyEscape) {
			seconds = drawlist.ArrivalDurationSeconds
		}
		in.DiscardTokens(in.PendingTokens())
	}
	cl.SetArrivalSeconds(seconds)
	if !cl.ArrivalActive() && b.sess != nil && b.sess.Clock != nil {
		if b.millisSource == nil {
			b.millisSource = newMonotonicMillisSource()
		}
		b.sess.Clock.ScaledAnchor = clock.ScaledNow(b.millisSource.Millis32())
		b.tickFiredValid = false
	}
	return true
}
