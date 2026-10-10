package main

// Local replay files (docs/DESIGN_MULTIPLAYER.md §10): where the game keeps
// them, what it names them, how many it keeps, and what a Replays screen
// lists. Everything here is host file handling; nothing reaches a tick.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

const (
	// replayExt names a finished replay; replayPartExt is appended while one
	// is being written, so a file a crash cut short is still recognisable
	// (and plays to its last whole chunk).
	replayExt     = ".nlreplay"
	replayPartExt = ".part"
	// replayKeep is how many replays the directory keeps. A new recording
	// prunes the oldest finished ones beyond it.
	replayKeep = 100
	// replayMapNameBytes bounds the map part of a file name.
	replayMapNameBytes = 40
)

// defaultReplayDir is the replay directory: $XDG_DATA_HOME/nanolathe/replays,
// else ~/.local/share/nanolathe/replays. Like the mod library and the
// settings file, the XDG layout is used on every platform, so the replays sit
// beside the mods in one documented place; the browser host's HOME is its
// persisted /settings tree (web/game.js), so replays persist there too.
func defaultReplayDir() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("nanolathe: locating the replay directory failed: logical path <replays>, providers searched [$XDG_DATA_HOME, $HOME], expected a data directory: %w", err)
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "nanolathe", "replays"), nil
}

// replayKindName is the kind part of a file name.
func replayKindName(k replay.Kind) string {
	name := "skirmish"
	if k.Survival() {
		name = "survival"
	}
	if k.Online() {
		name += "_online"
	}
	return name
}

// replayMapPart is a map name as a file name part: lower-case ASCII letters
// and digits, every other run of characters one hyphen, never an underscore
// (the parts' separator), and never empty.
func replayMapPart(name string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
			if b.Len() >= replayMapNameBytes {
				break
			}
			continue
		}
		hyphen = true
	}
	if b.Len() == 0 {
		return "map"
	}
	return b.String()
}

// replayFileStem is a recording's file name without extension:
// YYYY-MM-DD_HH-MM-SS_<map>_<skirmish|survival>[_online], in local time.
func replayFileStem(started time.Time, mapName string, kind replay.Kind) string {
	return started.Format("2006-01-02_15-04-05") + "_" + replayMapPart(mapName) + "_" + replayKindName(kind)
}

// activeReplays are the .part files this process is writing now. A listing
// marks them, and deleting one is refused.
var activeReplays = struct {
	sync.Mutex
	paths map[string]bool
}{paths: map[string]bool{}}

func markReplayActive(path string, active bool) {
	activeReplays.Lock()
	defer activeReplays.Unlock()
	if active {
		activeReplays.paths[path] = true
	} else {
		delete(activeReplays.paths, path)
	}
}

func replayActive(path string) bool {
	activeReplays.Lock()
	defer activeReplays.Unlock()
	return activeReplays.paths[path]
}

// createReplayPart creates the .part file of a new recording in dir named
// from stem, adding -2, -3 … when a finished or unfinished replay already has
// the name (two recordings started in the same second, perhaps by two game
// windows). It returns the open file and the finished replay's path.
func createReplayPart(dir, stem string) (*os.File, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("nanolathe: creating the replay directory failed: logical path %s, providers searched [replays], expected a writable directory: %w", dir, err)
	}
	for i := 1; i <= 99; i++ {
		name := stem
		if i > 1 {
			name = fmt.Sprintf("%s-%d", stem, i)
		}
		final := filepath.Join(dir, name+replayExt)
		if _, err := os.Stat(final); err == nil {
			continue
		}
		file, err := os.OpenFile(final+replayPartExt, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("nanolathe: creating a replay failed: logical path %s, providers searched [replays], expected a writable file: %w", final+replayPartExt, err)
		}
		return file, final, nil
	}
	return nil, "", fmt.Errorf("nanolathe: creating a replay failed: logical path %s, providers searched [replays], expected an unused name", filepath.Join(dir, stem+replayExt))
}

// replayFile is one replay in a directory, before reading it.
type replayFile struct {
	path     string
	partial  bool
	modified time.Time
	size     int64
}

// replayFiles are dir's replays, finished and unfinished, newest first by
// modification time, then by name. A missing directory has none.
func replayFiles(dir string) ([]replayFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("nanolathe: reading the replay directory failed: logical path %s, providers searched [replays], expected a readable directory: %w", dir, err)
	}
	var out []replayFile
	for _, e := range entries {
		name := e.Name()
		partial := strings.HasSuffix(name, replayExt+replayPartExt)
		if e.IsDir() || !partial && !strings.HasSuffix(name, replayExt) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, replayFile{path: filepath.Join(dir, name), partial: partial, modified: info.ModTime(), size: info.Size()})
	}
	slices.SortFunc(out, func(a, b replayFile) int {
		if c := b.modified.Compare(a.modified); c != 0 {
			return c
		}
		return strings.Compare(b.path, a.path)
	})
	return out, nil
}

// pruneReplays removes dir's oldest finished replays beyond keep. Unfinished
// ones are never pruned: another game window may be writing one.
func pruneReplays(dir string, keep int) error {
	files, err := replayFiles(dir)
	if err != nil {
		return err
	}
	kept := 0
	var first error
	for _, f := range files {
		if f.partial {
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = fmt.Errorf("nanolathe: pruning a replay failed: logical path %s, providers searched [replays], expected a removable file: %w", f.path, err)
		}
	}
	return first
}

// replayListing is one replay as a Replays screen shows it.
type replayListing struct {
	Path string
	// Name is the file name.
	Name string
	// Header is the replay's header: kind, map, seats, start time and the
	// identity a playback needs.
	Header replay.Header
	// FinalTick is the last recorded tick, and Duration the game time it is.
	FinalTick uint32
	Duration  time.Duration
	// Started is when the battle started: the header's time, else the file's.
	Started time.Time
	// End is the recording's end reason, zero when it has none.
	End replay.EndReason
	// Incomplete marks a recording without its end entry: one still being
	// written, or one a crash cut short, which plays to its last whole chunk.
	Incomplete bool
	// Recording marks the file this process is writing now.
	Recording bool
	Size      int64
	// Err is why the file cannot be read as a replay; only Path, Name,
	// Started, Incomplete, Recording and Size are filled then.
	Err error
}

// Map is the recorded map's name.
func (l replayListing) Map() string { return l.Header.MapName }

// Kind is what the replay records.
func (l replayListing) Kind() replay.Kind { return l.Header.Kind }

// Players are the recorded human and computer seats, in slot order.
func (l replayListing) Players() []replay.Seat {
	var out []replay.Seat
	for _, s := range l.Header.Seats {
		if s.Role == session.MatchRoleHuman || s.Role == session.MatchRoleComputer {
			out = append(out, s)
		}
	}
	return out
}

// listReplays reads dir's replays for a Replays screen, newest first. A file
// that is not a readable replay is listed with its error, so it can still be
// deleted.
func listReplays(dir string) ([]replayListing, error) {
	files, err := replayFiles(dir)
	if err != nil {
		return nil, err
	}
	out := make([]replayListing, 0, len(files))
	for _, f := range files {
		l := replayListing{Path: f.path, Name: filepath.Base(f.path), Started: f.modified, Incomplete: f.partial, Recording: replayActive(f.path), Size: f.size}
		data, err := os.ReadFile(f.path)
		var sum replay.Summary
		if err == nil {
			sum, err = replay.Summarize(data)
		}
		if err != nil {
			l.Err = err
			out = append(out, l)
			continue
		}
		l.Header, l.FinalTick, l.End = sum.Header, sum.FinalTick, sum.End
		l.Duration = time.Duration(sum.FinalTick) * time.Second / 30
		l.Incomplete = l.Incomplete || sum.End == 0
		if sum.Header.Started > 0 {
			l.Started = time.UnixMilli(sum.Header.Started)
		}
		out = append(out, l)
	}
	slices.SortStableFunc(out, func(a, b replayListing) int { return b.Started.Compare(a.Started) })
	return out, nil
}

// deleteReplay removes one replay file. It refuses anything but a replay, and
// the replay this process is recording now.
func deleteReplay(path string) error {
	name := filepath.Base(path)
	if !strings.HasSuffix(name, replayExt) && !strings.HasSuffix(name, replayExt+replayPartExt) {
		return fmt.Errorf("nanolathe: replay delete refused: logical path %s, providers searched [replays], expected a %s file", path, replayExt)
	}
	if replayActive(path) {
		return fmt.Errorf("nanolathe: replay delete refused: logical path %s, providers searched [replays], expected a replay that is not being recorded", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("nanolathe: replay delete failed: logical path %s, providers searched [replays], expected a removable file: %w", path, err)
	}
	return nil
}
