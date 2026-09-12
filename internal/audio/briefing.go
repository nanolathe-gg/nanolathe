package audio

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
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

// MusicTracks returns file-backed CD tracks in logical order. The packaged
// soundtrack uses physical files 2..17 for logical tracks 1..16; files 0 and 1
// are duplicate extras [03 R-AUD-01 §4 "Packaged MP3 media"].
func MusicTracks(fs vfs.FSOps) []string {
	if fs == nil {
		return nil
	}
	for _, dir := range []string{"music", "sounds/music", "cdaudio"} {
		entries, err := fs.ReadDir(dir)
		if err != nil {
			continue
		}
		var paths []string
		for _, e := range entries {
			if e.IsDir {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Path))
			if ext == ".mp3" || ext == ".wav" {
				paths = append(paths, e.Path)
			}
		}
		// Recognize the shipped media set before applying the portable
		// directory policy. Never renumber its duplicate extras as CD tracks.
		var packaged []string
		for physical := 2; physical <= 17; physical++ {
			want := fmt.Sprintf("%s/%d.mp3", dir, physical)
			for _, path := range paths {
				if strings.EqualFold(path, want) {
					packaged = append(packaged, path)
					break
				}
			}
		}
		if len(packaged) == 16 {
			return packaged
		}
		// Nanolathe's host policy for other file sets is numeric basename
		// order, then lexical order. It is not a retail CD format contract.
		sort.Slice(paths, func(i, j int) bool {
			a, ae := strconv.Atoi(strings.TrimSuffix(filepath.Base(paths[i]), filepath.Ext(paths[i])))
			b, be := strconv.Atoi(strings.TrimSuffix(filepath.Base(paths[j]), filepath.Ext(paths[j])))
			if ae == nil && be == nil && a != b {
				return a < b
			}
			if (ae == nil) != (be == nil) {
				return ae == nil
			}
			return paths[i] < paths[j]
		})
		if len(paths) > 99 {
			paths = paths[:99]
		}
		if len(paths) > 0 {
			return paths
		}
	}
	return nil
}

// ProbeMusicTracks shares the controller's logical track mapping with the UI.
func ProbeMusicTracks(fs vfs.FSOps) int { return len(MusicTracks(fs)) }

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

func packagedMusicTracks(paths []string) bool {
	if len(paths) != 16 {
		return false
	}
	for i, path := range paths {
		if !strings.EqualFold(filepath.Base(path), fmt.Sprintf("%d.mp3", i+2)) {
			return false
		}
	}
	return true
}
