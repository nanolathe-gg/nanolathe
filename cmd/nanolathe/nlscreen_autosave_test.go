package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// nlAutosaveCard is the Game page's Autosave interval card.
func nlAutosaveCard(t *testing.T, s *nlScreen) nlCard {
	t.Helper()
	for _, page := range s.pages() {
		if page.key != "game" {
			continue
		}
		for _, c := range page.cards {
			if c.key == "autosave" {
				return c
			}
		}
	}
	t.Fatal("no Autosave interval card on the Game page")
	return nlCard{}
}

// The Autosave interval card edits presentation.autosaveMinutes through the
// screen's draft: nothing changes until Apply, Apply saves it, Back drops it,
// and a hand-edited interval no step names survives an unrelated Apply
// (DESIGN_SESSIONS_AI_SAVE §5, DESIGN_INTERFACE_HUD_INPUT §3.17).
func TestNLScreenAutosaveIntervalCard(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g, s := settingsRegressionScreen(nil, settings.Defaults())
	g.settingsWritable = true
	c := nlAutosaveCard(t, s)
	if !slices.Equal(c.steps, []string{"Off", "5 min", "10 min", "15 min", "30 min", "60 min"}) {
		t.Fatalf("autosave choices = %v", c.steps)
	}
	if v := c.get(&s.draft); c.steps[v] != "10 min" || c.valueText(&s.draft) != "10 min" {
		t.Fatalf("default shows %q (%q), want 10 min", c.steps[v], c.valueText(&s.draft))
	}

	s.setCard(c, 0)
	if s.draft.pres.AutosaveMinutes != settings.AutosaveOff || g.presentation.AutosaveMinutes != 10 || s.dirty() != 1 {
		t.Fatalf("Off before Apply: draft %d, live %d, dirty %d", s.draft.pres.AutosaveMinutes, g.presentation.AutosaveMinutes, s.dirty())
	}
	s.hide() // Back drops the draft.
	if stored, err := settings.Load(); err != nil || stored.Presentation.AutosaveMinutes != 10 || g.presentation.AutosaveMinutes != 10 {
		t.Fatalf("Back kept the pending interval: stored %d, live %d, %v", stored.Presentation.AutosaveMinutes, g.presentation.AutosaveMinutes, err)
	}

	s.draft = s.freshDraft(g)
	s.setCard(c, 0)
	s.apply()
	stored, err := settings.Load()
	if err != nil || stored.Presentation.AutosaveMinutes != settings.AutosaveOff || g.autosaveIntervalTicks() != 0 || s.dirty() != 0 {
		t.Fatalf("Apply Off: stored %d, live ticks %d, dirty %d, %v", stored.Presentation.AutosaveMinutes, g.autosaveIntervalTicks(), s.dirty(), err)
	}
	if c.valueText(&s.draft) != "Off" {
		t.Fatalf("Off shows %q", c.valueText(&s.draft))
	}

	s.setCard(c, 4)
	s.apply()
	if stored, err = settings.Load(); err != nil || stored.Presentation.AutosaveMinutes != 30 || g.autosaveIntervalTicks() != 30*autosaveTicksPerMinute {
		t.Fatalf("Apply 30 min: stored %d, live ticks %d, %v", stored.Presentation.AutosaveMinutes, g.autosaveIntervalTicks(), err)
	}

	file := settings.Defaults()
	file.Presentation.AutosaveMinutes = 20
	g, s = settingsRegressionScreen(nil, file)
	g.settingsWritable = true
	c = nlAutosaveCard(t, s)
	if c.valueText(&s.draft) != "20 min" || s.dirty() != 0 {
		t.Fatalf("hand-edited interval shows %q with %d changes", c.valueText(&s.draft), s.dirty())
	}
	s.draft.unitLimit, s.touched["unitlimit"] = 500, true
	s.apply()
	if stored, err = settings.Load(); err != nil || stored.Presentation.AutosaveMinutes != 20 {
		t.Fatalf("an unrelated Apply changed the hand-edited interval to %d: %v", stored.Presentation.AutosaveMinutes, err)
	}
}
