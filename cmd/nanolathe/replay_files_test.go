package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// fileTestHeader is a header the Writer accepts; these tests never compose
// its configuration, so its bytes are only opaque.
func fileTestHeader(kind replay.Kind, mapName string, started time.Time) replay.Header {
	return replay.Header{Kind: kind, Config: []byte{1}, Identity: netproto.Identity{Rules: netproto.RuleIdentity{Name: "strict-3.1"}},
		MapName: mapName, Started: started.UnixMilli(),
		Seats: []replay.Seat{{Name: "Ann", Role: session.MatchRoleHuman}, {Name: "", Role: session.MatchRoleComputer}}}
}

// record writes one replay of ticks one-tick pumps through the host's
// recording file handling, finishing it with reason.
func recordTestReplay(t *testing.T, target replayTarget, h replay.Header, ticks uint32, reason replay.EndReason) string {
	t.Helper()
	rec, err := startReplayRecording(target, h)
	if err != nil {
		t.Fatal(err)
	}
	for tick := uint32(1); tick <= ticks; tick++ {
		if err := rec.w.Pump(tick, 1); err != nil {
			t.Fatal(err)
		}
	}
	path, err := rec.finish(reason, ticks)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReplayFileNames(t *testing.T) {
	at := time.Date(2026, 10, 9, 14, 32, 5, 0, time.Local)
	for _, c := range []struct {
		kind replay.Kind
		name string
		want string
	}{
		{replay.KindSkirmish, "Ashap Plateau", "2026-10-09_14-32-05_ashap-plateau_skirmish"},
		{replay.KindSurvival, "The_Pass!!", "2026-10-09_14-32-05_the-pass_survival"},
		{replay.KindOnlineSkirmish, "  Lava & Run ", "2026-10-09_14-32-05_lava-run_skirmish_online"},
		{replay.KindOnlineSurvival, "../..", "2026-10-09_14-32-05_map_survival_online"},
		{replay.KindSkirmish, "Évian", "2026-10-09_14-32-05_vian_skirmish"},
		{replay.KindSkirmish, strings.Repeat("a", 90), "2026-10-09_14-32-05_" + strings.Repeat("a", replayMapNameBytes) + "_skirmish"},
	} {
		if got := replayFileStem(at, c.name, c.kind); got != c.want {
			t.Errorf("%q %v: %q, want %q", c.name, c.kind, got, c.want)
		}
	}
}

// A recording is written as .part, listed as incomplete and recording while
// it runs, refused for deletion, and renamed into place at its end; its
// listing then reads the header and the final tick. A recording that ran no
// tick leaves nothing, and two started in the same second get two names.
func TestReplayRecordingFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 10, 9, 14, 32, 5, 0, time.Local)
	h := fileTestHeader(replay.KindSkirmish, "Ashap Plateau", started)
	rec, err := startReplayRecording(replayTarget{dir: dir}, h)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "2026-10-09_14-32-05_ashap-plateau_skirmish.nlreplay")
	if rec.final != want || rec.part != want+replayPartExt {
		t.Fatalf("recording at %s, %s", rec.part, rec.final)
	}
	for tick := uint32(1); tick <= 90; tick++ {
		if err := rec.w.Pump(tick, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := rec.flush(); err != nil {
		t.Fatal(err)
	}
	list, err := listReplays(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("listing %v: %v", list, err)
	}
	if l := list[0]; !l.Incomplete || !l.Recording || l.FinalTick != 90 || l.Err != nil || l.Map() != "Ashap Plateau" {
		t.Fatalf("live listing %+v", l)
	}
	if err := deleteReplay(rec.part); err == nil {
		t.Fatal("deleted the replay being recorded")
	}
	// A second recording in the same second takes the next name.
	other, err := startReplayRecording(replayTarget{dir: dir}, h)
	if err != nil {
		t.Fatal(err)
	}
	if other.final != strings.TrimSuffix(want, replayExt)+"-2"+replayExt {
		t.Fatalf("second recording at %s", other.final)
	}
	if path, err := other.finish(replay.EndLeft, 0); err != nil || path != "" {
		t.Fatalf("a recording of no tick was kept: %q, %v", path, err)
	}
	if _, err := os.Stat(other.part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty recording left %v", err)
	}
	for tick := uint32(91); tick <= 100; tick++ {
		if err := rec.w.Pump(tick, 1); err != nil {
			t.Fatal(err)
		}
	}
	path, err := rec.finish(replay.EndFinished, 100)
	if err != nil || path != want {
		t.Fatalf("finished at %q: %v", path, err)
	}
	if _, err := os.Stat(rec.part); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the .part file outlived its rename")
	}
	list, err = listReplays(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("listing %v: %v", list, err)
	}
	l := list[0]
	if l.Incomplete || l.Recording || l.FinalTick != 100 || l.End != replay.EndFinished || l.Duration != 100*time.Second/30 ||
		!l.Started.Equal(started) || l.Kind() != replay.KindSkirmish || len(l.Players()) != 2 || l.Players()[0].Name != "Ann" {
		t.Fatalf("finished listing %+v", l)
	}
	if err := deleteReplay(path); err != nil {
		t.Fatal(err)
	}
	if list, _ := listReplays(dir); len(list) != 0 {
		t.Fatalf("deleted replay still listed: %v", list)
	}
	if err := deleteReplay(filepath.Join(dir, "settings.json")); err == nil {
		t.Fatal("deleted a file that is not a replay")
	}
}

// A file a crash left as .part lists as incomplete and reads to its last
// whole chunk; a damaged file lists with its error; anything else in the
// directory is ignored.
func TestReplayListingIncompleteAndDamaged(t *testing.T) {
	dir := t.TempDir()
	h := fileTestHeader(replay.KindOnlineSurvival, "Lava Run", time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local))
	rec, err := startReplayRecording(replayTarget{dir: dir}, h)
	if err != nil {
		t.Fatal(err)
	}
	for tick := uint32(1); tick <= 60; tick++ {
		_ = rec.w.Pump(tick, 1)
	}
	if err := rec.flush(); err != nil {
		t.Fatal(err)
	}
	// Entries after the last flush never reach the file: the crash.
	for tick := uint32(61); tick <= 75; tick++ {
		_ = rec.w.Pump(tick, 1)
	}
	close(rec.stop)
	<-rec.done
	_ = rec.file.Close()
	markReplayActive(rec.part, false)
	rec.setLive(false)
	if err := os.WriteFile(filepath.Join(dir, "broken.nlreplay"), []byte("not a replay"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := listReplays(dir)
	if err != nil || len(list) != 2 {
		t.Fatalf("listing %+v: %v", list, err)
	}
	var crashed, broken replayListing
	for _, l := range list {
		if l.Err != nil {
			broken = l
		} else {
			crashed = l
		}
	}
	if !crashed.Incomplete || crashed.Recording || crashed.FinalTick != 60 || crashed.End != 0 || crashed.Kind() != replay.KindOnlineSurvival || crashed.Name != filepath.Base(rec.part) {
		t.Fatalf("crashed listing %+v", crashed)
	}
	if !errors.Is(broken.Err, replay.ErrCorrupt) || broken.Name != "broken.nlreplay" {
		t.Fatalf("damaged listing %+v", broken)
	}
	if err := deleteReplay(crashed.Path); err != nil {
		t.Fatal(err)
	}
}

// A new recording keeps the newest finished replays, so the directory holds
// replayKeep once it ends; unfinished files are never pruned.
func TestReplayPruning(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	var paths []string
	for i := range replayKeep + 5 {
		at := base.Add(time.Duration(i) * time.Minute)
		path := recordTestReplay(t, replayTarget{dir: dir}, fileTestHeader(replay.KindSkirmish, "Ashap Plateau", at), 1, replay.EndFinished)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	orphan := filepath.Join(dir, "2025-12-31_23-00-00_old_skirmish.nlreplay.part")
	if err := os.WriteFile(orphan, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := base.Add(-time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	rec, err := startReplayRecording(replayTarget{dir: dir}, fileTestHeader(replay.KindSurvival, "Ashap Plateau", base.Add(24*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		_, err := os.Stat(path)
		if kept := i >= 6; kept != (err == nil) {
			t.Fatalf("replay %d kept %v: %v", i, kept, err)
		}
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("pruned an unfinished replay: %v", err)
	}
	_ = rec.w.Pump(1, 1)
	if _, err := rec.finish(replay.EndLeft, 1); err != nil {
		t.Fatal(err)
	}
	files, err := replayFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	finished := 0
	for _, f := range files {
		if !f.partial {
			finished++
		}
	}
	if finished != replayKeep {
		t.Fatalf("%d finished replays kept, want %d", finished, replayKeep)
	}
}

// --record-replay writes exactly the named file, keeping even a battle of no
// tick; a missing directory lists nothing.
func TestReplayExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.nlreplay")
	if got := recordTestReplay(t, replayTarget{path: path}, fileTestHeader(replay.KindSkirmish, "x", time.Unix(0, 0)), 0, replay.EndLeft); got != path {
		t.Fatalf("recorded to %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := replay.Summarize(data)
	if err != nil || sum.End != replay.EndLeft || sum.FinalTick != 0 {
		t.Fatalf("summary %+v: %v", sum, err)
	}
	if list, err := listReplays(filepath.Join(dir, "missing")); err != nil || len(list) != 0 {
		t.Fatalf("missing directory listed %v: %v", list, err)
	}
}

func TestDefaultReplayDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	if dir, err := defaultReplayDir(); err != nil || dir != filepath.Join("/data", "nanolathe", "replays") {
		t.Fatalf("replay directory %q: %v", dir, err)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/player")
	if dir, err := defaultReplayDir(); err != nil || dir != filepath.Join("/home/player", ".local", "share", "nanolathe", "replays") {
		t.Fatalf("replay directory %q: %v", dir, err)
	}
}

// A recording whose battle was never torn down — the window closed over a
// battle the menus started — is finished when the process's run ends, as its
// battle then stands.
func TestReplayLiveRecordingsFinishAtExit(t *testing.T) {
	dir := t.TempDir()
	rec, err := startReplayRecording(replayTarget{dir: dir}, fileTestHeader(replay.KindSkirmish, "Ashap Plateau", time.Date(2026, 10, 9, 10, 0, 0, 0, time.Local)))
	if err != nil {
		t.Fatal(err)
	}
	for tick := uint32(1); tick <= 45; tick++ {
		_ = rec.w.Pump(tick, 1)
	}
	rec.ending = func() (replay.EndReason, uint32) { return replay.EndFinished, 45 }
	finishLiveRecordings()
	list, err := listReplays(dir)
	if err != nil || len(list) != 1 || list[0].Incomplete || list[0].End != replay.EndFinished || list[0].FinalTick != 45 {
		t.Fatalf("listing %+v: %v", list, err)
	}
	finishLiveRecordings()
	if path, err := rec.finish(replay.EndLeft, 45); path != "" || err != nil {
		t.Fatalf("a finished recording finished again: %q, %v", path, err)
	}
}
