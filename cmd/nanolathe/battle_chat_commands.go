package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

const (
	localCommandWords = 20
	localCommandBytes = 126
	lastCommandBytes  = 79
)

func localCommandSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// tokenizeLocalCommand reproduces the fixed word-vector input: ASCII CRT
// whitespace separates words, # ends the command even inside a word, ; is
// ordinary content, and the twenty words share 126 bytes including their NUL
// terminators [07 R-CAM-01 §6].
func tokenizeLocalCommand(command string) []string {
	words := make([]string, 0, localCommandWords)
	used := 0
	for at := 0; at < len(command) && len(words) < localCommandWords && used < localCommandBytes; {
		for at < len(command) && localCommandSpace(command[at]) {
			at++
		}
		if at >= len(command) || command[at] == '#' {
			break
		}
		start := at
		for at < len(command) && !localCommandSpace(command[at]) && command[at] != '#' {
			at++
		}
		available := localCommandBytes - used - 1
		if available <= 0 {
			break
		}
		end := at
		if end-start > available {
			end = start + available
		}
		words = append(words, command[start:end])
		used += end - start + 1
		if end != at || at < len(command) && command[at] == '#' {
			break
		}
	}
	return words
}

func localCommandInt(words []string, index int) int {
	if index < 0 || index >= len(words) {
		return 0
	}
	return int(formats.ParseTDFInteger(words[index]))
}

func (b *battleSession) saveDirectChatSetting(change func(*settings.Settings)) {
	if b == nil || b.shell != nil || change == nil {
		return
	}
	s := loadedSettings()
	if b.cl != nil {
		master, vehicle, shading := b.cl.ShadowOptions()
		s.Display.AntiAlias = boolInt(b.cl.AntiAlias())
		s.Display.Shadows = boolInt(master)
		s.Display.VehicleShadows = boolInt(vehicle)
		s.Display.FeatureShadows = boolInt(b.cl.FeatureShadows())
		s.Display.Shading = boolInt(shading)
	}
	change(&s)
	if err := s.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", err)
	}
}

// dispatchLocalCommand handles the bounded command set currently owned by
// the engine. Unknown names remain plain local chat and do not create a
// placeholder command framework [07 R-CAM-01 §6][07 R-FE-02 §12].
func (b *battleSession) dispatchLocalCommand(text string) {
	trimmed := strings.TrimLeft(text, " ")
	if len(trimmed) == 0 || trimmed[0] != '+' {
		return
	}
	command := trimmed[1:]
	b.chat.lastCommand = command
	if len(b.chat.lastCommand) > lastCommandBytes {
		b.chat.lastCommand = b.chat.lastCommand[:lastCommandBytes]
	}
	words := tokenizeLocalCommand(command)
	if len(words) == 0 {
		return
	}
	switch strings.ToLower(words[0]) {
	case "noshake":
		if b.sess != nil {
			_ = b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanNoShake})
		}
	case "atm":
		if b.sess != nil && battleSessionKind(b) == 2 {
			_ = b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanATM})
		}
	case "cdplay":
		if b.sess != nil && b.sess.Audio != nil && b.sess.Audio.Music != nil {
			music := b.sess.Audio.Music
			track := localCommandInt(words, 1)
			if track == 0 {
				if music.IsEnabled() {
					music.Tick(music.IsPlaying())
				}
			} else {
				_ = music.Play(track)
			}
		}
	case "cdstop":
		if b.sess != nil && b.sess.Audio != nil && b.sess.Audio.Music != nil {
			b.sess.Audio.Music.Stop()
		}
	case "sound3d":
		audio.ToggleOutput3D()
		if b.shell != nil {
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(*settings.Settings) {})
		}
	case "nometal", "noenergy":
		if b.sess == nil {
			return
		}
		player := int(b.sess.LocalOwner)
		if len(words) > 1 {
			player = int(uint8(localCommandInt(words, 1)))
		}
		resource := economy.Metal
		if strings.EqualFold(words[0], "noenergy") {
			resource = economy.Energy
		}
		_ = b.sess.EnqueueHumanCommand(session.HumanCommand{
			Kind: session.HumanSetResource,
			SetResource: session.HumanSetResourceCommand{
				Player: player, Resource: resource, Amount: float32(localCommandInt(words, 2)),
			},
		})
	case "selectable":
		if b.sess != nil {
			_ = b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanMakeSelectable})
		}
	case "los":
		if b.sess == nil || battleSessionKind(b) != 2 {
			return
		}
		_ = b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanVisibility, Visibility: session.HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}})
		if b.shell != nil {
			b.shell.saveSettings()
		} else {
			setup := b.sess.Skirmish
			b.saveDirectChatSetting(func(s *settings.Settings) {
				s.Skirmish.Mapping = setup.Mapping
				s.Skirmish.LineOfSight = setup.LineOfSight
				s.Skirmish.LOSType = setup.LOSType
			})
		}
	case "mapping":
		if b.sess == nil || battleSessionKind(b) != 2 {
			return
		}
		_ = b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanVisibility, Visibility: session.HumanVisibilityCommand{ToggleMask: visibility.ModeHistoryEnabled}})
		if b.shell != nil {
			b.shell.saveSettings()
		} else {
			setup := b.sess.Skirmish
			b.saveDirectChatSetting(func(s *settings.Settings) {
				s.Skirmish.Mapping = setup.Mapping
				s.Skirmish.LineOfSight = setup.LineOfSight
				s.Skirmish.LOSType = setup.LOSType
			})
		}
	case "lostype":
		if b.sess != nil {
			_ = b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanVisibility, Visibility: session.HumanVisibilityCommand{ToggleMask: visibility.ModeTerrainRay}})
		}
	case "nowisee":
		if b.sess != nil && battleSessionKind(b) == 2 {
			_ = b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanVisibility, Visibility: session.HumanVisibilityCommand{ClearMask: visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled}})
		}
	case "doubleshot":
		if b.sess != nil && battleSessionKind(b) == 2 {
			_ = b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanDoubleShot})
		}
	case "halfshot":
		if b.sess != nil && battleSessionKind(b) == 2 {
			_ = b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanHalfShot})
		}
	case "radar":
		if b.sess != nil && battleSessionKind(b) == 2 {
			b.radarOptions ^= radarAllContactsOption
		}
	case "switchalt":
		persist := len(words) == 1
		value := localCommandInt(words, 1) & 1
		if persist && !b.switchAlt {
			value = 1
		}
		b.switchAlt = value != 0
		if b.shell != nil {
			b.shell.switchAlt = b.switchAlt
			if persist {
				b.shell.saveSettings()
			}
		} else if persist {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.SwitchAlt = value })
		}
	case "screenchat":
		if b.cl == nil {
			return
		}
		value := int(b.cl.ToggleScreenChat())
		if b.shell != nil {
			b.shell.messages.ScreenChat = value
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.Messages.ScreenChat = value })
		}
	case "scrollspeed":
		value := byte(localCommandInt(words, 1))
		b.scrollSpeedByte, b.scrollSpeedPrimed = value, true
		if b.shell != nil {
			b.shell.scrollSpeed = int(value)
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.ScrollSpeed = int(value) })
		}
	case "iface":
		value := localCommandInt(words, 1)
		b.interfaceType = value
		if b.shell != nil {
			b.shell.interfaceType = value
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.InterfaceType = value })
		}
	case "antialias":
		if b.cl == nil {
			return
		}
		value := !b.cl.AntiAlias()
		b.cl.SetAntiAlias(value)
		if b.shell != nil {
			b.shell.display.AntiAlias = boolInt(value)
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.Display.AntiAlias = boolInt(value) })
		}
	case "shading":
		if b.cl == nil {
			return
		}
		master, vehicle, shading := b.cl.ShadowOptions()
		shading = !shading
		b.cl.SetShadowOptions(master, vehicle, shading)
		if b.shell != nil {
			b.shell.display.Shading = boolInt(shading)
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.Display.Shading = boolInt(shading) })
		}
	case "shadow":
		if b.cl == nil {
			return
		}
		master, vehicle, shading := b.cl.ShadowOptions()
		master = !master
		b.cl.SetShadowOptions(master, vehicle, shading)
		if b.shell != nil {
			b.shell.display.Shadows = boolInt(master)
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.Display.Shadows = boolInt(master) })
		}
	case "tshadow":
		if b.cl == nil {
			return
		}
		master, vehicle, shading := b.cl.ShadowOptions()
		vehicle = !vehicle
		b.cl.SetShadowOptions(master, vehicle, shading)
		if b.shell != nil {
			b.shell.display.VehicleShadows = boolInt(vehicle)
		}
	case "fshadow":
		if b.cl == nil {
			return
		}
		value := !b.cl.FeatureShadows()
		b.cl.SetFeatureShadows(value)
		if b.shell != nil {
			b.shell.display.FeatureShadows = boolInt(value)
		}
	}
}
