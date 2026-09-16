package main

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"strconv"
	"strings"
)

func (b *battleSession) configureDeveloperShot(opts Options) error {
	if opts.ShotProbe != "" && opts.ShotProbe != "state" && opts.ShotProbe != "builder" && opts.ShotProbe != "both" {
		return fmt.Errorf("nanolathe: developer capture: logical path --shot-probe, providers searched [command line], expected state, builder or both")
	}
	if opts.ShotDeveloper != "" {
		mode, err := strconv.Atoi(opts.ShotDeveloper)
		if err != nil || mode < 0 || mode > 4 {
			return fmt.Errorf("nanolathe: developer capture: logical path --shot-developer, providers searched [command line], expected integer 0..4")
		}
		b.dispatchLocalCommand("+Now Film Chris Include Reload Assert")
		b.handleDeveloperShortcuts(input.StateFromSample(input.Sample{ShortcutTokenMode: true, ShortcutToken: input.Token{Kind: input.TokenEdit, Key: input.KeyF11}}), b.cl)
		b.developer.information = true
		b.sess.ResetDebugDisplayMode()
		for i := 0; i < mode; i++ {
			b.sess.CycleDebugDisplayMode()
		}
	}
	if opts.ShotContour != "" {
		words := append([]string{"Contour"}, strings.Fields(opts.ShotContour)...)
		spacing, ok := developerContourArgument(words, 1)
		_, offsetOK := developerContourArgument(words, 2)
		if len(words) < 2 || len(words) > 3 || !ok || !offsetOK || spacing < 0 {
			return fmt.Errorf("nanolathe: developer capture: logical path --shot-contour, providers searched [command line], expected nonnegative spacing and finite offset")
		}
		b.developerCommand(words)
	}
	if opts.ShotProbe != "" {
		b.developer.authorized = true
	}
	b.syncDeveloperView()
	return nil
}

func (b *battleSession) selectDeveloperShotProbes(probe string) {
	if probe == "" {
		return
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	for _, u := range f.Units {
		if u.Owner == f.Selection.LocalPlayer {
			target := developerProbeTarget{Slot: u.Slot, InstanceID: u.InstanceID, Enabled: true}
			if probe == "state" || probe == "both" {
				b.developer.probes.State = target
			}
			if probe == "builder" || probe == "both" {
				b.developer.probes.Builder = target
			}
			break
		}
	}
	b.syncDeveloperView()
}
