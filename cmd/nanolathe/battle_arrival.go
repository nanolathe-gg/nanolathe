package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// beginArrival is the authored opening for fresh matches and missions.
// Missions without a local commander reveal the existing scene (GPU §36).
func (b *battleSession) beginArrival(cl *client.Client) bool {
	if b == nil || b.sess == nil || cl == nil || cl.Buffer() == nil {
		return false
	}
	if !b.sess.PublishOpeningFrame() {
		return false
	}
	u, ok := arrivalCommander(cl.Buffer().Current(), b.cat)
	if !ok {
		cl.StartMapReveal()
		return cl.ArrivalActive()
	}
	// The opening frames the landing after final viewport/zoom selection.
	// This is presentation choreography, including the unit's height shear.
	if b.cam != nil {
		b.cam.JumpToBattleViewCenter(int32(u.X>>16), int32(u.Z>>16)-int32(u.Y>>17))
	}
	cl.StartArrival(u)
	return true
}

// beginBattleArrival is shared by fresh entry, restart, and successful save
// adoption. Restored frames are already published and must not be republished.
func (b *battleSession) beginBattleArrival(opts Options, cl *client.Client, restored bool) {
	if !opts.Arrival || !modernRenderer(opts) || cl == nil {
		return
	}
	if restored {
		cl.StartMapReveal()
	} else {
		b.beginArrival(cl)
	}
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
		cl.StepArrivalCooling(delta)
		return false
	}
	duration, hasDrop := cl.ArrivalDuration(), cl.ArrivalHasDrop()
	previous := cl.ArrivalSeconds()
	seconds := previous
	if cl.ArrivalPresented() && cl.IsFocused() && delta > 0 {
		// Loading/device stalls must not consume the entire opening unseen.
		seconds += float32(min(delta, 0.05))
	}
	if in := cl.Input(); in != nil {
		if in.Kbd.KeyDown(input.KeyEscape) {
			seconds = duration
		}
		in.DiscardTokens(in.PendingTokens())
	}
	cl.SetArrivalSeconds(seconds)
	if hasDrop && previous < drawlist.ArrivalImpactSeconds && seconds >= drawlist.ArrivalImpactSeconds && seconds < duration {
		b.playArrivalImpact()
	}
	if !cl.ArrivalActive() && b.sess != nil && b.sess.Clock != nil {
		if b.millisSource == nil {
			b.millisSource = newMonotonicMillisSource()
		}
		b.sess.Clock.ScaledAnchor = clock.ScaledNow(b.millisSource.Millis32())
		b.tickFiredValid = false
	}
	return true
}

// User-selected stock sample and artistic gain for the opening (GPU §36).
// Crossing the impact time calls this once; redraws and Escape do not replay it.
func (b *battleSession) playArrivalImpact() {
	if b.sess == nil || b.sess.Audio == nil || b.sess.Audio.Cache == nil {
		return
	}
	output := audio.GlobalOutput()
	if output == nil {
		return
	}
	sample, err := b.sess.Audio.Cache.LoadPath("sounds/xplosml3.wav")
	if err != nil || sample == nil {
		return
	}
	// The ordinary backend applies master mute and the player's FX gain once.
	_ = output.PlaySample(sample, audio.VolumeFromCentibel(audio.VolInView)*0.75, 0)
}
