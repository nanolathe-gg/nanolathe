package main

// monitorCard uses the display snapshot captured when the screen opens, so a
// hotplug cannot change a stepper's meaning midway through an edit.
func (s *nlScreen) monitorCard() nlCard {
	ids := []string{""}
	labels := []string{"System primary"}
	for _, monitor := range s.monitors {
		ids = append(ids, monitor.ID)
		labels = append(labels, monitor.Label)
	}
	found := s.draft.monitor == ""
	for _, id := range ids {
		if id == s.draft.monitor {
			found = true
		}
	}
	if !found {
		ids = append(ids, s.draft.monitor)
		labels = append(labels, "Disconnected: "+s.draft.monitor)
	}
	return nlCard{
		key: "monitor", label: "Screen", pics: []string{"armrad", "armmark"}, kind: nlStepper,
		steps: labels,
		get: func(d *nlDraft) int {
			for i, id := range ids {
				if id == d.monitor {
					return i
				}
			}
			return 0
		},
		set:  func(d *nlDraft, v int) { d.monitor = ids[v] },
		copy: func(to, from *nlDraft) { to.monitor = from.monitor },
		valueText: func(d *nlDraft) string {
			for i, id := range ids {
				if id == d.monitor {
					return labels[i]
				}
			}
			return "Disconnected: " + d.monitor
		},
		desc: func(*nlDraft, int) string {
			return "The display for the game window and fullscreen. Apply moves the game; the choice is remembered for next launch. A disconnected display uses the system primary until reconnected. Reopen this screen to refresh the list."
		},
		scene: func(*nlDraft, int) string { return "armor" },
	}
}
