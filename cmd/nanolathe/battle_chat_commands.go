package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/render"
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
		s.Display.Glow = boolInt(b.cl.Glow())
		s.Display.DitheredFog = boolInt(b.cl.DitheredFog())
		// The Enhanced effect switches and strengths are live client values in
		// a direct battle too, so the write-all captures them beside the
		// display bits (DESIGN_GPU_RENDERER §30). Renderer and FPS stay as
		// stored.
		storeEffects(&s.Presentation, b.cl.Effects())
		s.Presentation.TrailStrength = b.cl.TrailStrength()
		s.Presentation.GroundLightStrength = b.cl.GroundLightStrength()
		s.Presentation.BlastRingStrength = b.cl.BlastRingStrength()
	}
	s.Display.Gamma = b.gammaSetting
	// A direct battle owns the live clock preference just as it owns the live
	// display values above. Every write-all captures it before applying the
	// requested setting mutation, so an unrelated command cannot restore a
	// stale value from disk [07 R-CAM-01 §6].
	s.Clock = boolInt(b.clockVisible)
	change(&s)
	if err := s.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", err)
	}
}

// toggleEffectChatSetting flips one Enhanced effect family on the live client
// and persists the presentation block (DESIGN_GPU_RENDERER §30). A family is On
// while any of its switches is, and the command writes every one of them to
// the other value (effectFamily). The windowed shell owns the preference the
// host polls, so it writes there; a direct battle owns the client values and
// the write-all above captures them.
func (b *battleSession) toggleEffectChatSetting(command string) {
	if b == nil || b.cl == nil {
		return
	}
	var family *effectFamily
	for i := range effectFamilies {
		if effectFamilies[i].command == command {
			family = &effectFamilies[i]
		}
	}
	if family == nil {
		return
	}
	if b.shell != nil {
		p := &b.shell.presentation
		family.set(p, !family.on(*p))
		b.cl.SetEffects(presentationEffects(*p))
		b.cl.SetTrailStrength(p.TrailStrength)
		b.shell.saveSettings()
		return
	}
	var p settings.Presentation
	storeEffects(&p, b.cl.Effects())
	p.TrailStrength = b.cl.TrailStrength()
	family.set(&p, !family.on(p))
	b.cl.SetEffects(presentationEffects(p))
	b.cl.SetTrailStrength(p.TrailStrength)
	// The write-all captures the live switches and trail strength just set.
	b.saveDirectChatSetting(func(*settings.Settings) {})
}

// retailCheatCommands are the mask-2 (cheat) entries this build dispatches
// [07 R-CAM-01 §6]; MakePoster is not implemented.
var retailCheatCommands = []string{"radar", "atm", "view", "los", "mapping", "doubleshot", "halfshot", "nowisee", "meteor"}

// dispatchLocalCommand handles the bounded command set currently owned by
// the engine. Unknown names remain plain local chat and do not create a
// placeholder command framework [07 R-CAM-01 §6][07 R-FE-02 §12]. It reports
// whether the line ran a cheat entry: the dispatcher's returned mask has bit
// 2, which sends the line as chat to everyone [07 R-CAM-01 §6]. Every cheat
// here dispatches only in skirmish; online none dispatches.
func (b *battleSession) dispatchLocalCommand(text string) (cheat bool) {
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
	if b.onlineBattle() {
		// Online play has no cheat permission. Keep the established local
		// interface gestures and the share commands; refuse every other
		// +command before developer dispatch and exact-unit spawn fallbacks.
		// The line is still sent as chat (commitLocalChat).
		switch strings.ToLower(words[0]) {
		case "bigbrother", "noshake", "logo", "switchalt", "fps", "net":
			// Local displays and interface gestures only.
		case "sharemetal", "shareenergy", "sharemapping", "shareradar", "shareall", "setsharemetal", "setshareenergy", "give":
			// Seat commands acting for this seat (battle_share_commands.go).
			if !b.onlineShareCommand(words) {
				b.onlineNotice("This +command is unavailable in multiplayer games")
			}
			return
		default:
			b.onlineNotice("This +command is unavailable in multiplayer games")
			return
		}
	}
	if b.developerCommand(words) {
		return
	}
	cheat = b.sess != nil && battleSessionKind(b) == 2 && slices.Contains(retailCheatCommands, strings.ToLower(words[0]))
	switch strings.ToLower(words[0]) {
	case "light":
		render.SetModelLight(int32(localCommandInt(words, 1)), int32(localCommandInt(words, 2)), int32(localCommandInt(words, 3)))
		if b.cl != nil {
			b.cl.InvalidateModelImages()
		}
	case "rcache":
		if b.cl != nil {
			b.cl.InvalidateModelImages()
		}
	case "bigbrother":
		// BigBrother is local interface state (DESIGN_MULTIPLAYER §7.1, §7.3).
		if b.sess != nil {
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanBigBrother})
		}
	case "noshake":
		// Single-player toggles the authoritative driver; online it is a local
		// presentation preference (submitHumanCommand).
		if b.sess != nil {
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanNoShake})
		}
	case "shootall":
		// Mask 1, so it runs in skirmish, Survival and campaign alike, is no
		// cheat and posts nothing [07 R-CAM-01 §6]. The session toggles the
		// combat gate at phase 1 [06 §3.2 "The option bit of check 2"]. An
		// online battle or a playback refuses it above, as it refuses every
		// command it has no seat form for (DESIGN_MULTIPLAYER §7.1).
		if b.sess != nil {
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanShootAll})
		}
	case "spawn":
		b.spawnChatCommand(words)
	case "atm":
		if b.sess != nil && battleSessionKind(b) == 2 {
			_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanATM})
		}
	case "musicmode":
		if b.sess != nil && b.sess.Audio != nil && b.sess.Audio.Music != nil {
			b.sess.Audio.Music.SetDesired(int32(localCommandInt(words, 1)))
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
	case "gamma":
		b.setGammaCommand(localCommandInt(words, 1))
	case "bps":
		b.bpsVisible = !b.bpsVisible
	case "fps":
		b.fpsVisible = !b.fpsShown()
		if b.shell != nil {
			b.shell.fpsVisible = b.fpsVisible
		}
	case "net":
		// The online network overlay (online_net.go); host display only.
		visible := b.netVisible
		if b.shell != nil {
			visible = b.shell.netVisible
		}
		b.netVisible = !visible
		if b.shell != nil {
			b.shell.netVisible = b.netVisible
		}
	case "clock":
		value := !b.clockShown()
		b.clockVisible = value
		if b.shell != nil {
			b.shell.clockVisible = value
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.Clock = boolInt(value) })
		}
	case "showranges":
		b.showRanges = !b.rangesShown()
		if b.shell != nil {
			b.shell.showRanges = b.showRanges
		}
		// This is a renderer preference, never an authoritative rule or save
		// field. Classic keeps the process-only command [07 R-P0-11 §3];
		// Modern retains the player's choice (GPU design §20).
		if b.modernRangePresentation() {
			value := boolInt(b.showRanges)
			if b.shell != nil {
				b.shell.presentation.ShowRanges = value
				b.shell.saveSettings()
			} else {
				p := b.hostPreferences()
				p.ShowRanges = value
				b.hostPresentation = &p
				b.saveDirectChatSetting(func(s *settings.Settings) {
					if b.cs == nil || b.cs.mod == nil {
						s.Presentation.ShowRanges = value
						return
					}
					// Direct entries load the same per-mod layers as the shell.
					// Save only this choice into that layer so a previous mod
					// override cannot restore the old toggle on the next entry.
					key := modSettingsKey(b.cs.mod)
					patch, err := settings.Merge(s.ModSettings[key], []byte(fmt.Sprintf(`{"presentation":{"showRanges":%d}}`, value)))
					if err != nil {
						fmt.Fprintf(os.Stderr, "nanolathe: settings for %s: %v\n", key, err)
						return
					}
					if s.ModSettings == nil {
						s.ModSettings = make(map[string]json.RawMessage)
					}
					s.ModSettings[key] = patch
				})
			}
		}
	case "sound3d":
		audio.ToggleOutput3D()
		if b.shell != nil {
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(*settings.Settings) {})
		}
	case "sing":
		if b.sess != nil && b.sess.Audio != nil && b.sess.Audio.Queue != nil {
			b.sess.Audio.Queue.ToggleSing()
		}
	case "view":
		if b.sess != nil && battleSessionKind(b) == 2 {
			_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanView, View: session.HumanViewCommand{Player: uint8(localCommandInt(words, 1))}})
		}
	case "give":
		if b.sess == nil || len(words) < 4 {
			return
		}
		var resource economy.Res
		switch strings.ToLower(words[3]) {
		case "metal":
			resource = economy.Metal
		case "energy":
			resource = economy.Energy
		default:
			return
		}
		_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanGive, Give: session.HumanGiveCommand{
			Player: int(uint8(localCommandInt(words, 1))), Resource: resource, Amount: float32(localCommandInt(words, 2)),
		}})
	case "logo":
		logo := localCommandInt(words, 1)
		player := int(uint8(localCommandInt(words, 2)))
		valid := logo >= 0 && b.hud != nil && b.hud.logos != nil
		if valid {
			entry, ok := b.hud.logos.Find(sideLogoEntry)
			valid = ok && logo < int(entry.FrameCount)
		}
		if valid {
			current, ok := b.currentSnapshot()
			valid = ok && player < len(current.Players)
			if valid {
				row := current.Players[player]
				valid = row.Present && row.Controller >= 1 && row.Controller <= 3 && row.Side != 10
			}
		}
		if !valid {
			if ring := b.messageRing(); ring != nil {
				ring.Append("Invalid logo setting", 2, 0, 10, b.currentTick())
			}
			return
		}
		// Single-player writes the player record; online it is a local
		// presentation override (submitHumanCommand).
		_ = b.enqueueHumanCommand(session.HumanCommand{
			Kind: session.HumanSetLogo,
			SetLogo: session.HumanSetLogoCommand{
				Player: player,
				Logo:   uint8(logo),
			},
		})
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
		_ = b.enqueueLocalBattleCommand(session.HumanCommand{
			Kind: session.HumanSetResource,
			SetResource: session.HumanSetResourceCommand{
				Player: player, Resource: resource, Amount: float32(localCommandInt(words, 2)),
			},
		})
	case "selectable":
		if b.sess != nil {
			_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanMakeSelectable})
		}
	case "los":
		if b.sess == nil || battleSessionKind(b) != 2 {
			return
		}
		_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanVisibility, Visibility: session.HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}})
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
		_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanVisibility, Visibility: session.HumanVisibilityCommand{ToggleMask: visibility.ModeHistoryEnabled}})
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
			_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanVisibility, Visibility: session.HumanVisibilityCommand{ToggleMask: visibility.ModeTerrainRay}})
		}
	case "nowisee":
		if b.sess != nil && battleSessionKind(b) == 2 {
			_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanVisibility, Visibility: session.HumanVisibilityCommand{ClearMask: visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled}})
		}
	case "doubleshot":
		if b.sess != nil && battleSessionKind(b) == 2 {
			_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanDoubleShot})
		}
	case "halfshot":
		if b.sess != nil && battleSessionKind(b) == 2 {
			_ = b.enqueueLocalBattleCommand(session.HumanCommand{Kind: session.HumanHalfShot})
		}
	case "radar":
		if b.sess != nil && battleSessionKind(b) == 2 {
			if b.survivalBest != nil {
				b.survivalBest.excluded = "cheats used"
			}
			b.radarOptions ^= radarAllContactsOption
			// The strategic marker layer answers the same gate (§16.11).
			if b.cl != nil {
				b.cl.SetRadarOptions(b.radarOptions)
			}
		}
	case "meteor":
		if b.sess != nil && battleSessionKind(b) == 2 {
			argumentPresent := len(words) > 1
			_ = b.enqueueLocalBattleCommand(session.HumanCommand{
				Kind: session.HumanMeteor,
				Meteor: session.HumanMeteorCommand{
					ArgumentPresent: argumentPresent,
					Enabled:         localCommandInt(words, 1) != 0,
				},
			})
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
	case "glow":
		// The Enhanced glow layer toggle, a Nanolathe command with no retail
		// counterpart (docs/DESIGN_GPU_RENDERER.md §19). Persisted like the
		// retail display bits so the choice survives the session.
		if b.cl == nil {
			return
		}
		value := !b.cl.Glow()
		b.cl.SetGlow(value)
		if b.shell != nil {
			b.shell.display.Glow = boolInt(value)
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.Display.Glow = boolInt(value) })
		}
	case "water", "lights", "finish", "heat", "marks":
		// The five Enhanced effect family shortcuts, Nanolathe commands with no
		// retail counterpart (docs/DESIGN_GPU_RENDERER.md §30). They mirror the
		// options page's rows and persist in the presentation block.
		b.toggleEffectChatSetting(strings.ToLower(words[0]))
	case "dither":
		if b.cl == nil {
			return
		}
		value := !b.cl.DitheredFog()
		b.cl.SetDitheredFog(value)
		if b.shell != nil {
			b.shell.display.DitheredFog = boolInt(value)
			b.shell.saveSettings()
		} else {
			b.saveDirectChatSetting(func(s *settings.Settings) { s.Display.DitheredFog = boolInt(value) })
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
	default:
		// Retail offers unmatched first words to a unit-name default handler
		// [07 R-CAM-01 §6]; developer access selects it, otherwise the
		// Modern exact-name shorthand keeps its own contract.
		b.unitNameChatCommand(words)
	}
	return
}
