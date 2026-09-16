package main

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The host pins publication identities, not live unit pointers. Enabled is
// separate so a no-hover shortcut can disable a probe while retaining its pin.
type developerProbeTarget struct {
	Slot       pool.Handle
	InstanceID uint64
	Enabled    bool
}

type developerProbeSelection struct {
	State, Builder developerProbeTarget
	Font           *formats.FNT // COMIX, bound by the battle HUD owner
	Layout         *developerProbeLayout
}

// Both restored painters share the previous text-end y [07 R-CAM-01 §9].
// The battle owner keeps this presentation state; no package global survives
// into a different battle.
type developerProbeLayout struct {
	Bottom      int
	startBottom int
	key         developerProbeLayoutKey
}

type developerProbeLayoutKey struct {
	Observation    *frame.DeveloperView
	Tick           uint32
	State, Builder developerProbeTarget
	Font           *formats.FNT
}

type developerProbeRow struct {
	Text           string
	Score          int32
	ScoreAvailable bool
}

// developerProbeSubject refuses slot reuse and mixed-tick observations. These
// are Nanolathe inspection safeguards (DESIGN_DEVELOPER_TOOLS §3.1), not the
// dormant retail painter's raw-reference lifetime [07 R-CAM-01 §9].
func developerProbeSubject(f *frame.Frame, target developerProbeTarget) (*frame.UnitView, *frame.DeveloperUnit, string) {
	if target.Slot == 0 || target.InstanceID == 0 {
		return nil, nil, "Subject unavailable: no publication identity"
	}
	if f == nil || f.Developer == nil {
		return nil, nil, "Observation unavailable: awaiting publication"
	}
	if f.Developer.Tick != f.Tick {
		return nil, nil, fmt.Sprintf("Observation stale: tick %d (frame %d)", f.Developer.Tick, f.Tick)
	}
	var unit *frame.UnitView
	for i := range f.Units {
		if f.Units[i].Slot == target.Slot {
			if f.Units[i].InstanceID != target.InstanceID {
				return nil, nil, "Subject stale: pool slot has been reused"
			}
			unit = &f.Units[i]
			break
		}
	}
	if unit == nil {
		return nil, nil, "Subject unavailable: no longer published"
	}
	for i := range f.Developer.Units {
		probe := &f.Developer.Units[i]
		if probe.Slot == target.Slot && probe.InstanceID == target.InstanceID {
			if probe.Dying {
				return nil, nil, "Subject unavailable: dying"
			}
			return unit, probe, ""
		}
	}
	return nil, nil, "Observation unavailable: subject data missing"
}

func developerProbeRows(f *frame.Frame, target developerProbeTarget, builder bool) []developerProbeRow {
	rows := []developerProbeRow{{Text: "Unit State Probe"}, {Text: "================"}}
	if builder {
		rows[0].Text, rows[1].Text = "Unit Builder Probe", "=================="
	}
	line := func(format string, values ...any) {
		rows = append(rows, developerProbeRow{Text: fmt.Sprintf(format, values...)})
	}
	u, probe, unavailable := developerProbeSubject(f, target)
	if unavailable != "" {
		line("%s", unavailable)
		return rows
	}
	// Preserve the diagnostic strings, including authored newlines. Each call
	// advances exactly one line pitch [07 R-CAM-01 §9].
	if builder {
		line("uid: %03d '%s'\n", u.Slot, probe.DisplayName)
	} else {
		line("uid: %03d/%04x '%s'\n", u.Slot, u.Slot, probe.DisplayName)
	}
	var owner frame.PlayerRow
	if int(u.Owner) < len(f.Players) {
		owner = f.Players[u.Owner]
	}
	local := owner.Present && (owner.Controller == 1 || owner.Controller == 2)
	where, kind := "REMOTE", "BUILDING"
	if local {
		where = "LOCAL"
	}
	if u.BMCode {
		kind = "MOBILE"
	}
	if owner.Present {
		line("playerno: %d '%s' %s - %s\n", u.Owner, owner.Name, where, kind)
		if builder {
			line("controller: %d\n\n", owner.Controller)
		} else {
			line("controller: %d\n", owner.Controller)
		}
	} else {
		line("playerno: %d unavailable - %s\n", u.Owner, kind)
		line("controller: unavailable\n")
	}
	if builder {
		if local {
			line("Units I can build, and the probabilities:\n")
			options := probe.Builds
			if !probe.BuildsAvailable {
				line("Build options unavailable\n")
				options = nil
			}
			for _, option := range options {
				if option.ScoreAvailable {
					rows = append(rows, developerProbeRow{
						Text:  fmt.Sprintf("       %3d %% - '%s'\n", option.Score, option.Name),
						Score: option.Score, ScoreAvailable: true,
					})
				} else {
					// TODO(question): candidate-score publication is unavailable;
					// the owning AI score trace must settle its exact intermediates
					// before a painter can present numbers [08 R-P0-05 §3].
					line("       unavailable - '%s'\n", option.Name)
				}
			}
		}
	} else {
		line("buildtimeleft: %1.3f\n", u.BuildRemaining)
		line("damage: %d\n", u.Health)
		occupancy := "unavailable"
		if u.MoverMode < 3 {
			occupancy = [...]string{"NONE", "GROUND", "AIR"}[u.MoverMode]
		}
		line("occupy: %s\n", occupancy)
		if local {
			if probe.AutoTargetAvailable {
				flags := [3]byte{'-', '-', '-'}
				for i, set := range probe.AutoTarget {
					if set {
						flags[i] = 'X'
					}
				}
				line("autotarget w[pri:sec:spe]: w[%c:%c:%c]\n", flags[0], flags[1], flags[2])
			} else {
				line("autotarget w[pri:sec:spe]: unavailable\n")
			}
			for _, queue := range f.OrderQueues {
				if queue.Unit != u.Slot {
					continue
				}
				rows = developerProbeOrders(rows, f, "Mission Q:", queue.Primary, queue.PrimaryTruncated)
				rows = developerProbeOrders(rows, f, "Background Mission Q:", queue.Secondary, queue.SecondaryTruncated)
				break
			}
		}
	}
	// Identify the restored tool and committed observation independently from
	// retail's labels. Pausing never requests an extra simulation tick [I6].
	paused := ""
	if f.Paused {
		paused = " (paused)"
	}
	line("Nanolathe restored probe: tick %d%s", f.Developer.Tick, paused)
	return rows
}

func developerProbeOrders(rows []developerProbeRow, f *frame.Frame, title string, orders []frame.OrderView, truncated bool) []developerProbeRow {
	if len(orders) != 0 {
		rows = append(rows, developerProbeRow{Text: title})
	}
	for _, order := range orders {
		text := fmt.Sprintf("    '%s' state: %d\n", order.Kind, order.State)
		if order.Target != 0 {
			name := "unavailable"
			for _, target := range f.Developer.Units {
				if target.Slot == order.Target {
					name = target.DisplayName
					break
				}
			}
			text = fmt.Sprintf("    '%s' state: %d  tgt: '%s'\n", order.Kind, order.State, name)
		}
		rows = append(rows, developerProbeRow{Text: text})
	}
	if truncated {
		rows = append(rows, developerProbeRow{Text: "Remaining orders unavailable"})
	}
	return rows
}

// developerProbeBarWidth includes the leftmost pixel. Even score 1 has one
// filled column; only the bar saturates, never its numeric label
// [07 R-CAM-01 §9].
func developerProbeBarWidth(score int32) int {
	if score <= 0 {
		return 0
	}
	if score > 100 {
		score = 100
	}
	return 1 + int(26*score/100)
}

func drawDeveloperProbes(c *client.Client, f *frame.Frame, selection developerProbeSelection) {
	if c == nil || selection.Font == nil || selection.Layout == nil {
		return
	}
	// One observation/target pair has one layout input, even when both
	// executors record it or the paused compositor redraws it. The next
	// observation retains the shared previous end (DESIGN_DEVELOPER_TOOLS §5).
	key := developerProbeLayoutKey{State: selection.State, Builder: selection.Builder, Font: selection.Font}
	if f != nil {
		key.Tick = f.Tick
		key.Observation = f.Developer
	}
	layout := selection.Layout
	if layout.key != key {
		layout.key = key
		layout.startBottom = layout.Bottom
	}
	priorBottom := layout.startBottom
	for i, target := range [...]developerProbeTarget{selection.State, selection.Builder} {
		if !target.Enabled || target.Slot == 0 {
			continue
		}
		builder := i == 1
		rows := developerProbeRows(f, target, builder)
		pitch := int(selection.Font.Height) + 3
		top := 7 * pitch
		if builder {
			top = 3 * pitch
		}
		bottom := priorBottom
		if bottom == 0 {
			bottom = 20 * pitch
		}
		// Inclusive shade bounds and the extended outline retain the shared
		// previous-bottom quirk [07 R-CAM-01 §9].
		c.UIShadeRect(c.PaletteTables(), 131, top, 271, bottom-top+1, -24)
		c.UIFrameRect(131, top, 272, bottom-top+2, c.GUIColor(5))
		y := top + 3
		for _, row := range rows {
			if row.ScoreAvailable {
				c.UIFrameRect(136, y+1, 27, pitch-5, c.GUIColor(15))
				if width := developerProbeBarWidth(row.Score); width > 0 {
					c.UIFillRect(136, y+1, width, pitch-5, c.GUIColor(15))
				}
			}
			c.UITextWidth(selection.Font, row.Text, 134, y, 0, c.GUIColor(15))
			y += pitch
		}
		priorBottom = y
		selection.Layout.Bottom = y
	}
}
