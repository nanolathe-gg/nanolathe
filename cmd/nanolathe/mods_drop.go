package main

// Drop-to-install (docs/DESIGN_MODS_MUTATORS.md §4.5): a zip or a folder
// dropped onto the window installs through the same path as --install-mod,
// the library's local install with the standard content check against the
// base install. The install runs off the render thread as the process's one
// mod install (§8.2), so a drop and a catalogue download never overlap.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
)

// modDropState is the finished drop install's outcome, waiting for the render
// thread to show it. The download job reports the progress; this carries the
// installed mod's own name, which the job's catalogue entry cannot know
// before the package is opened.
type modDropState struct {
	mu      sync.Mutex
	outcome string
	ready   bool
}

var modDrop modDropState

func (s *modDropState) finish(outcome string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcome, s.ready = outcome, true
}

// take returns a finished drop's outcome once the job that ran it has ended,
// so an outcome is never shown while the job still reads as running.
func (s *modDropState) take(job *modDownloadJob) (string, bool) {
	if job.view().running {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return "", false
	}
	outcome := s.outcome
	s.outcome, s.ready = "", false
	return outcome, true
}

// startModDrop starts installing the one dropped package on job, the
// process's single mod install, and returns the notice to show now. A drop of
// several items installs none of them, because each would need its own
// outcome and only one install runs at a time. openLibrary is called on the
// install's goroutine; base is the base install the package is validated
// against.
func startModDrop(job *modDownloadJob, state *modDropState, paths []string, base []string, openLibrary func() (*modlibrary.Library, error)) string {
	if len(paths) != 1 {
		return "Drop one mod zip or folder at a time"
	}
	path := paths[0]
	name := filepath.Base(filepath.Clean(path))
	// The job's entry names the package in the Get more mods dialog while it
	// installs; there is nothing to download, so the install starts at once.
	entry := modfetch.Entry{Metadata: modlibrary.Metadata{Name: name}}
	base = append([]string(nil), base...)
	started := job.start(entry, func(context.Context, func(done, total int64)) error { return nil }, func() error {
		lib, err := openLibrary()
		if err == nil {
			var mod modlibrary.Mod
			if mod, err = lib.InstallLocal(path, base); err == nil {
				state.finish(fmt.Sprintf("%s %s installed", mod.Name, mod.Version))
				return nil
			}
		}
		fmt.Fprintf(os.Stderr, "nanolathe: installing the dropped %s failed: %v\n", name, err)
		state.finish("Install failed: " + noticeReason(err))
		return err
	})
	if !started {
		return "Another mod install is running; drop again when it finishes"
	}
	return "Installing " + name + "..."
}

// pollModDrop runs once per shell update. It starts an install for a drop on
// the menus and shows the outcome of a finished one: on the Mods & Mutators
// screen when it is open, whose installed list the download job's install
// count refreshes (pollModsFetch), else as the main-menu notice. A drop
// during a battle or its loading screen is ignored: nothing is installed and
// nothing is said, so a stray drop never interrupts play. The installed mod
// joins the list and is not selected.
func (g *gameShell) pollModDrop(cl *client.Client) {
	if cl != nil {
		if paths := cl.Input().DroppedPaths; len(paths) > 0 && g.frontend.Mode != modeBattle && g.frontend.Mode != modeLoading && g.cs != nil {
			g.showModDropNotice(startModDrop(&modDownload, &modDrop, paths, g.cs.baseRoots, openModLibrary))
		}
	}
	if outcome, ok := modDrop.take(&modDownload); ok {
		// The outcome is shown here, not as the Get more mods dialog's
		// unseen download outcome.
		modDownload.acknowledge()
		g.showModDropNotice(outcome)
	}
}

func (g *gameShell) showModDropNotice(notice string) {
	if modsUI != nil {
		modsUI.notice = notice
		g.refreshModsPanel()
		return
	}
	if g.cs == nil {
		return
	}
	g.cs.modNotice = notice
	if p := g.activePanel(); p != nil && g.frontend.Mode == modeMenuMain {
		g.refreshMainMenuModStatus(p)
	}
}
