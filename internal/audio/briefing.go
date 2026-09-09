package audio

import (
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// BriefingAlias resolves the mission briefing sound alias with fallback
// [03 §8.4][P1-02 §2.1] presentation. It prefers GlamourSound, then Brief,
// then Narration and MissionHint in that order. Empty string means no alias
// and is not fatal [P1-02 §2.2] degrade. Caller may then try SampleCache.Load
// or Controller fallback; missing alias degrades silently [P1-02 §2.2].
func BriefingAlias(glamourSound, brief, narration, missionHint string) string {
	if s := strings.TrimSpace(glamourSound); s != "" {
		return s
	}
	if s := strings.TrimSpace(brief); s != "" {
		// Brief may be a GAF key like Greenbrief, not an alias; treat as alias
		// only when it looks like a sound name (no path, no extension check
		// deferred to cache). Keep as is for degrade fallback.
		return s
	}
	if s := strings.TrimSpace(narration); s != "" {
		return s
	}
	if s := strings.TrimSpace(missionHint); s != "" {
		return s
	}
	return ""
}

// ProbeMusicTracks counts music tracks for the CD/MCI controller's fallback
// [03 §8.4]. It scans VFS logical paths in order: "music", "sounds/music",
// and any "*.wav" under "music" recursively via ReadDir. It returns the
// count capped at 99 [03 §8.4] and is presentation-only [I4][I6].
// Zero means no CD / missing media → silence but not fatal [03 §8.4].
func ProbeMusicTracks(fs vfs.FSOps) int {
	if fs == nil {
		return 0
	}
	candidates := []string{"music", "sounds/music", "cdaudio"}
	total := 0
	for _, dir := range candidates {
		entries, err := fs.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Path))
			if ext == ".wav" || ext == ".mp3" || ext == ".ogg" {
				total++
				if total >= 99 {
					return 99
				}
			}
		}
		if total > 0 {
			break
		}
	}
	if total > 99 {
		total = 99
	}
	return total
}

// SelectMusicMode chooses the controller play mode for a mission. It uses
// a sequential default suitable for briefing/music/CD fallback; random and
// category-shuffle are selectable via options but not inferred here.
// Presentation-only [03 §8.4][I4].
func SelectMusicMode(numTracks int, hasBriefing bool) PlayMode {
	if numTracks == 0 {
		return ModeIdle
	}
	if hasBriefing {
		return ModeSingle
	}
	return ModeSequential
}
