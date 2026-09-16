package main

import (
	"math"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

// Access, film and information deliberately have separate lifetimes
// [07 R-CAM-01 §9]. None of these controls is an authoritative command.
type battleDeveloperState struct {
	host                          developerHostProfile
	authorized, film, information bool
	quickkeysDisabled             bool
	contourSpacing, contourOffset int32
	probes                        developerProbeSelection
	probeLayout                   developerProbeLayout
}

func (b *battleSession) developerCommand(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch strings.ToLower(words[0]) {
	case "dev":
		// Modern host convenience; the historical password remains available
		// in both modes (DESIGN_DEVELOPER_TOOLS §2.1).
		if b.sess == nil || b.sess.Gameplay.Normalize() != gameplay.Modern || len(words) != 1 {
			return true
		}
		b.developer.authorized = true
	case "now":
		b.developer.authorized = len(words) == 6 && words[1] == "Film" && words[2] == "Chris" && words[3] == "Include" && words[4] == "Reload" && words[5] == "Assert"
	case "hostprofile":
		if b.developer.authorized {
			b.developer.host.enabled = !b.developer.host.enabled
		}
	case "contour":
		spacing, ok := developerContourArgument(words, 1)
		offset, offsetOK := developerContourArgument(words, 2)
		if !ok || !offsetOK || spacing < 0 {
			return true
		}
		b.developer.contourSpacing, b.developer.contourOffset = spacing, offset
	default:
		return false
	}
	b.syncDeveloperView()
	return true
}

// Contour parameters use 1/256 height, not world fixed-point [07 R-FE-02 §11].
// Host input rejects unsafe values under DESIGN_DEVELOPER_TOOLS §3.1.
func developerContourArgument(words []string, index int) (int32, bool) {
	if index >= len(words) {
		return 0, true
	}
	f, err := strconv.ParseFloat(words[index], 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || index == 1 && f < 0 {
		return 0, false
	}
	f *= 256
	if f < math.MinInt32 || f > math.MaxInt32 {
		return 0, false
	}
	return int32(f), true
}

// Called after the ordinary switch with the same residual token; film never
// steals an event from TALK or a modal, and access is not rechecked for its
// second dispatch [07 R-CAM-01 §9].
func (b *battleSession) handleDeveloperShortcuts(in *input.State, cl *client.Client) {
	if b == nil || in == nil {
		return
	}
	kbd := battleShortcutKeyboard(in)
	if kbd == nil {
		return
	}
	if !kbd.KeyHeld(input.KeyCtrl) {
		if kbd.KeyDown(input.KeyF11) && b.developer.authorized {
			b.developer.film = !b.developer.film
			b.developer.quickkeysDisabled = b.developer.film
			if !b.developer.film {
				b.developer.information = false
				if b.sess != nil {
					b.sess.ResetDebugDisplayMode()
				}
			}
		}
		if kbd.HasShift() {
			if kbd.KeyDown(input.KeyF1) {
				b.armDeveloperProbe(&b.developer.probes.State)
			}
			if kbd.KeyDown(input.KeyF2) {
				b.armDeveloperProbe(&b.developer.probes.Builder)
			}
		}
	}
	var ch rune
	if in.ShortcutTokenMode {
		if in.ShortcutToken.Kind == input.TokenText && !in.ShortcutToken.Ctrl {
			ch = in.ShortcutToken.Rune
		}
	} else if !kbd.KeyHeld(input.KeyCtrl) {
		// Hand-authored controller samples retain their established key adapter.
		for _, p := range []struct {
			k input.Key
			r rune
		}{{input.KeyI, 'i'}, {input.KeyM, 'm'}, {input.KeyP, 'p'}} {
			if kbd.KeyDown(p.k) {
				ch = p.r
				if kbd.HasShift() {
					ch -= 'a' - 'A'
				}
				break
			}
		}
	}
	if ch == '\\' && b.developer.authorized && b.chat.lastCommand != "" {
		// Retained text excludes '+'. Dispatching this same bounded string neither
		// echoes TALK nor changes its value [07 R-CAM-01 §9].
		b.dispatchLocalCommand("+" + b.chat.lastCommand)
	}
	if b.developer.film {
		switch ch {
		case 'i':
			b.developer.information = !b.developer.information
		case 'm':
			if b.sess != nil {
				b.sess.CycleDebugDisplayMode()
			}
		case 'P':
			if cl != nil {
				cl.SetPointerCaptured(true)
			}
		case 'p':
			if cl != nil {
				cl.SetPointerCaptured(false)
			}
			// Stock refill and dying-unit edits require separate typed commands;
			// their deferred scope is DESIGN_DEVELOPER_TOOLS §2.3.
		}
	}
	b.syncDeveloperView()
}

func (b *battleSession) armDeveloperProbe(target *developerProbeTarget) {
	if target == nil {
		return
	}
	target.Enabled = false
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	if v, found := snapshotUnitByHandle(f, b.footerHoverUnit); found && v.InstanceID != 0 {
		target.Slot, target.InstanceID, target.Enabled = v.Slot, v.InstanceID, true
	}
}

func (b *battleSession) syncDeveloperView() {
	if b == nil {
		return
	}
	d := &b.developer
	enabled := d.authorized || d.film || d.contourSpacing != 0 || d.probes.State.Enabled || d.probes.Builder.Enabled
	if b.sess != nil {
		b.sess.SetDeveloperDiagnostics(enabled)
	}
	if b.cl == nil {
		return
	}
	options := client.DeveloperOptions{Information: d.film && d.information, ContourSpacing: d.contourSpacing, ContourOffset: d.contourOffset}
	if b.sess != nil {
		options.Mode = b.sess.DebugDisplayMode
	}
	// Pick from the committed diagnostic heightfield, never live terrain.
	if f, ok := b.currentSnapshot(); ok && f.Developer != nil && b.cam != nil && options.Mode == 2 {
		pointer, _ := b.cl.Input().PointerSample()
		if b.overWorld(int32(pointer.X), int32(pointer.Y)) {
			options.PickX, options.PickZ, options.PickValid = developerPick(b, f.Developer, int32(pointer.X), int32(pointer.Y))
		}
	}
	b.cl.SetDeveloperOptions(options)
}
