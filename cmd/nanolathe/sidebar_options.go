package main

import "github.com/nanolathe-gg/nanolathe/internal/settings"

// The live shell owns options preview and rollback. Detached captures use the
// same deterministic defaults as their other presentation preferences.
func (b *battleSession) expandedSidebarEnabled() bool {
	if b.shell != nil {
		return b.shell.presentation.ExpandedSidebar != 0
	}
	return settings.DefaultPresentation().ExpandedSidebar != 0
}

// expandedSidebarActive reports whether the flat Expanded sidebar layout is
// selected: the Modern renderer with the preference on.
func (b *battleSession) expandedSidebarActive() bool {
	return b != nil && b.cl != nil && b.cl.Enhanced() && b.expandedSidebarEnabled()
}
