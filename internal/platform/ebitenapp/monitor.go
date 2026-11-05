package ebitenapp

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
)

// DisplayMonitor is a host display choice, independent of gameplay and resolution.
type DisplayMonitor struct {
	ID    string
	Label string
}

// monitorID distinguishes same-name displays without depending on other names'
// enumeration order. Ebitengine exposes no persistent hardware identifier.
func monitorID(names []string, index int) string {
	occurrence := 0
	for _, name := range names[:index] {
		if name == names[index] {
			occurrence++
		}
	}
	return fmt.Sprintf("%d:%s", occurrence, names[index])
}

func monitorNames(monitors []*ebiten.MonitorType) []string {
	names := make([]string, len(monitors))
	for i, monitor := range monitors {
		names[i] = monitor.Name()
	}
	return names
}

// DisplayMonitors snapshots the currently attached displays. Call on the main
// thread before RunGame, or from the running window's update.
func DisplayMonitors() []DisplayMonitor {
	monitors := ebiten.AppendMonitors(nil)
	names := monitorNames(monitors)
	choices := make([]DisplayMonitor, len(monitors))
	for i, monitor := range monitors {
		w, h := monitor.Size()
		choices[i] = DisplayMonitor{ID: monitorID(names, i), Label: fmt.Sprintf("%d: %s · %d×%d", i+1, names[i], w, h)}
	}
	return choices
}

// SelectMonitor applies a saved choice before or during RunGame. Empty or
// disconnected choices use the system primary rather than the window's initial
// position (which can lie on another screen). The caller retains the saved ID.
func SelectMonitor(id string) {
	monitors := ebiten.AppendMonitors(nil)
	if len(monitors) == 0 {
		return
	}
	names := monitorNames(monitors)
	selected := monitors[0]
	for i, monitor := range monitors {
		if monitorID(names, i) == id {
			selected = monitor
			break
		}
	}
	ebiten.SetMonitor(selected)
}
