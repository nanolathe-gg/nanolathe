package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
)

// The save and load dialogs are one authored file, `LOADGAME.GUI`, opened in
// two modes: a different backdrop and a different set of hidden gadgets
// [08 R-SAVE-02 §1] [07 R-FE-01 §8].
type saveLoadMode uint8

const (
	saveScreenMode saveLoadMode = iota
	loadScreenMode
)

// saveLoadBackdrop is the authored backdrop each mode installs [07 R-FE-01 §8].
func (m saveLoadMode) backdrop() string {
	if m == saveScreenMode {
		return "bitmaps/dsavegame2.pcx"
	}
	return "bitmaps/dloadgame2.pcx"
}

// saveGameEntry is one surviving row of the slot list. `File` is the file's
// base name — the name `DELETE` addresses and the stem a later save reuses —
// while `Description` is what the `GAMES` gadget displays [08 R-SAVE-02 §1].
type saveGameEntry struct {
	Path        string
	File        string
	Description string
	// TimeWord is the 32-bit time word the enumerator records per entry and
	// stably sorts ascending on, so the oldest file is first
	// [08 R-SAVE-02 §1].
	TimeWord uint32
	Summary  save.Summary
}

// retailSaveDir is the directory both screens enumerate and write into. Retail
// assembles `SAVEGAME\<name>.SAV` under the install root and creates the
// directory when the save screen opens [08 R-SAVE-02 §1] [07 R-FE-01 §8].
//
// The language-prefixed `<language>-SAVEGAME` location is deliberately absent:
// retail tries it first but "uses it only when a file already exists there; a
// fresh save never lands there", and this build mounts no language directory
// [08 R-SAVE-02 §1].
func retailSaveDir(root string) string {
	if strings.TrimSpace(root) == "" {
		return session.RetailSaveDirName
	}
	return filepath.Join(root, session.RetailSaveDirName)
}

// enumerateRetailSaves builds the slot list exactly as the enumerator does:
// every `*.SAV` in the directory, `.` and `..` excluded and nothing else
// filtered by attribute; one 32-bit time word per entry; a stable sort
// ascending on that word; then each file's `Summary` account opened through a
// filtered read and its `Description` taken. A file with no readable bank, no
// `Summary`, or no `Description` is dropped from both lists, so it can be
// neither loaded nor deleted through the interface [08 R-SAVE-02 §1].
//
// The sort word is the **last-modification** time: the enumerator copies the
// third of the four leading fields the content layer's find record carries, and
// that record is the C run-time's find-data block, whose three time fields are
// creation, last access and last write in that order, each a `time_t` in
// seconds [08 R-SAVE-02 §1]. `ModTime().Unix()` below is the same quantity. An
// entry served from an archive rather than from a loose file reads back zero
// there; the save directory is loose-only, so that case does not arise.
func enumerateRetailSaves(dir string) []saveGameEntry {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	entries := make([]saveGameEntry, 0, len(items))
	for _, item := range items {
		if item.IsDir() || !strings.EqualFold(filepath.Ext(item.Name()), session.RetailSaveExt) {
			continue
		}
		word := uint32(0)
		if info, err := item.Info(); err == nil {
			word = uint32(info.ModTime().Unix())
		}
		entries = append(entries, saveGameEntry{
			Path:     filepath.Join(dir, item.Name()),
			File:     item.Name(),
			TimeWord: word,
		})
	}
	sortSaveEntries(entries)
	// The name list is compacted in place after the descriptions are read, so
	// the surviving order is the sorted order [08 R-SAVE-02 §1].
	surviving := entries[:0]
	for _, entry := range entries {
		summary, ok, err := save.ReadSummaryFile(entry.Path)
		if err != nil {
			continue
		}
		if !ok || strings.TrimSpace(summary.Description) == "" {
			continue
		}
		entry.Summary = summary
		entry.Description = summary.Description
		surviving = append(surviving, entry)
	}
	return surviving
}

// sortSaveEntries uses the standard stable sort: ascending on the time word, oldest first and newest last. Files
// sharing a word keep the order the directory read produced
// [08 R-SAVE-02 §1].
func sortSaveEntries(entries []saveGameEntry) {
	slices.SortStableFunc(entries, func(a, b saveGameEntry) int {
		if a.TimeWord < b.TimeWord {
			return -1
		}
		if a.TimeWord > b.TimeWord {
			return 1
		}
		return 0
	})
}

// saveLoadCommand is the concrete effect one authored control has. Routing and
// file I/O stay with the composition owner; this screen owns only which
// authored choice was made [07 §3].
type saveLoadCommand uint8

const (
	saveLoadNone saveLoadCommand = iota
	saveLoadCommit
	saveLoadDelete
	saveLoadCancel
	saveLoadToSave
	saveLoadToLoad
)

// saveLoadScreen is the mutable state of the one `LOADGAME.GUI` surface. It
// holds no session handle and performs no I/O, so both directions are drivable
// without a window [I6].
type saveLoadScreen struct {
	mode     saveLoadMode
	dir      string
	entries  []saveGameEntry
	selected int
	// sideNames is the dialog-owned presentation copy [08 R-SAVE-02 §3].
	sideNames []string
	// name is the `GAMENAME` edit's text: the file stem a save writes under,
	// and the description a selection copies into the edit [08 R-SAVE-02 §1].
	name string
	// source records which surface opened the screen. Retail reaches both
	// directions from `SINGLE`, from the in-battle options menu and from the
	// results panel, and what a save writes differs between them
	// [07 R-FE-01 §8] [08 R-CAMP-01 §8].
	source saveLoadSource
}

// saveLoadSource is the surface the screen was opened over.
type saveLoadSource uint8

const (
	saveLoadFromFrontend saveLoadSource = iota
	saveLoadFromBattle
	saveLoadFromResults
)

func newSaveLoadScreen(mode saveLoadMode, dir string, source saveLoadSource) *saveLoadScreen {
	s := &saveLoadScreen{mode: mode, dir: dir, selected: -1, source: source}
	s.Refresh()
	return s
}

// Source reports which surface opened the screen.
func (s *saveLoadScreen) Source() saveLoadSource {
	if s == nil {
		return saveLoadFromFrontend
	}
	return s.source
}

// Refresh re-enumerates the directory and re-clamps the selection. `DELETE`
// rebuilds the list this way and does not confirm or check its result
// [08 R-SAVE-02 §1].
func (s *saveLoadScreen) Refresh() {
	if s == nil {
		return
	}
	s.entries = enumerateRetailSaves(s.dir)
	if s.selected >= len(s.entries) {
		s.selected = len(s.entries) - 1
	}
	if len(s.entries) == 0 {
		s.selected = -1
	}
}

func (s *saveLoadScreen) Entries() []saveGameEntry {
	if s == nil {
		return nil
	}
	return s.entries
}

// Descriptions is what the `GAMES` gadget displays — the descriptions, not the
// file names; selection index n maps to the n-th surviving file
// [08 R-SAVE-02 §1].
func (s *saveLoadScreen) Descriptions() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, entry.Description)
	}
	return out
}

func (s *saveLoadScreen) Selected() int {
	if s == nil {
		return -1
	}
	return s.selected
}

func (s *saveLoadScreen) SelectedEntry() (saveGameEntry, bool) {
	if s == nil || s.selected < 0 || s.selected >= len(s.entries) {
		return saveGameEntry{}, false
	}
	return s.entries[s.selected], true
}

// Select refreshes the summary panel and copies the entry's description into
// the `GAMENAME` edit, which is why saving over a selected slot is a silent
// truncate-overwrite with no prompt [08 R-SAVE-02 §1].
func (s *saveLoadScreen) Select(index int) {
	if s == nil || index < 0 || index >= len(s.entries) {
		return
	}
	s.selected = index
	s.name = s.entries[index].Description
}

func (s *saveLoadScreen) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

// SetName accepts the edit's text verbatim. There is no character-set filter
// of its own on the save path: whatever the edit gadget admits reaches the
// file system call [08 R-SAVE-02 §1].
func (s *saveLoadScreen) SetName(text string) {
	if s != nil {
		s.name = text
	}
}

func (s *saveLoadScreen) Mode() saveLoadMode {
	if s == nil {
		return loadScreenMode
	}
	return s.mode
}

// SetMode switches the one window between its two directions and rebuilds the
// list, the way the `SaveGame` / `LoadGame` route buttons do [08 R-SAVE-02 §1].
func (s *saveLoadScreen) SetMode(mode saveLoadMode) {
	if s == nil {
		return
	}
	s.mode = mode
	s.Refresh()
}

// CommitPath is the file a save writes to: the typed text as the stem under
// `SAVEGAME\`, through retail's strip-last-dot-then-append rule. An empty name
// yields "" and the save action then does nothing at all — no file, no message
// [08 R-SAVE-02 §1].
func (s *saveLoadScreen) CommitPath() string {
	if s == nil {
		return ""
	}
	return session.RetailSavePath(s.dir, s.name)
}

// HiddenControls is the authored gadget set each direction hides. The save
// screen hides `LoadGame`, and `DELETE` when the list is empty; the load screen
// hides `DELETE`, `GAMENAME` and `SaveGame` [08 R-SAVE-02 §1] [07 R-FE-01 §8].
func (s *saveLoadScreen) HiddenControls() []string {
	if s == nil {
		return nil
	}
	if s.mode == saveScreenMode {
		hidden := []string{"LoadGame"}
		if len(s.entries) == 0 {
			hidden = append(hidden, "DELETE")
		}
		return hidden
	}
	return []string{"DELETE", "GAMENAME", "SaveGame"}
}

// Activate applies one authored control. `LOAD`, `GAMES` and `GAMENAME` are
// treated identically by the handler, which is why an activation on the list or
// the edit commits under the current edit text [08 R-SAVE-02 §1].
func (s *saveLoadScreen) Activate(name string) saveLoadCommand {
	if s == nil {
		return saveLoadNone
	}
	switch gui.CallbackName(name) {
	case "LOAD", "GAMES", "GAMENAME":
		return saveLoadCommit
	case "DELETE":
		if s.mode != saveScreenMode || s.selected < 0 {
			return saveLoadNone
		}
		return saveLoadDelete
	case "CANCEL":
		return saveLoadCancel
	case "SaveGame":
		if s.mode == saveScreenMode {
			return saveLoadNone
		}
		return saveLoadToSave
	case "LoadGame":
		if s.mode == loadScreenMode {
			return saveLoadNone
		}
		return saveLoadToLoad
	}
	return saveLoadNone
}

// deleteSelectedSave removes `SAVEGAME\<selected file name>`, ignores the
// result and rebuilds the list; there is no confirmation [08 R-SAVE-02 §1].
func (s *saveLoadScreen) deleteSelected() {
	entry, ok := s.SelectedEntry()
	if !ok {
		return
	}
	_ = os.Remove(entry.Path)
	s.Refresh()
}
