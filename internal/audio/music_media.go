package audio

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// MusicOutput is the optional presentation device for file-backed CD audio.
// Music has its own gain and lifetime, separate from wave cues and narration
// [03 R-AUD-01 §2][03 R-AUD-01 §4]. The caller retains source ownership.
type MusicOutput interface {
	NewMusicPlayer(source io.ReadSeeker, extension string) (MusicPlayer, error)
}

type MusicPlayer interface {
	Play()
	Pause()
	IsPlaying() bool
	SetVolume(float64)
	PositionMillis() int
	Completed() bool
	Err() error
	Close() error
}

type musicFilePlayer struct {
	MusicPlayer
	file vfs.File
	path string
}

func (p *musicFilePlayer) Close() error {
	err := p.MusicPlayer.Close()
	if closeErr := p.file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func musicError(path, provider string, err error) error {
	return fmt.Errorf("nanolathe: music playback failed (%w): logical path %s, providers searched [%s], expected decodable music", err, path, provider)
}

func (p *musicFilePlayer) Err() error {
	if err := p.MusicPlayer.Err(); err != nil {
		return musicError(p.path, p.file.Info().Source.SourcePath, err)
	}
	return nil
}

func (a *Service) openMusicTrack(track int) (MusicPlayer, error) {
	output, ok := GlobalOutput().(MusicOutput)
	if !ok {
		// Device-free consumers retain the controller's modeled state.
		return nil, nil
	}
	if track < 1 || track > len(a.musicTracks) {
		return nil, musicError("music", "", fmt.Errorf("track %d unavailable", track))
	}
	path := a.musicTracks[track-1]
	file, err := a.fs.Open(path)
	if err != nil {
		return nil, musicError(path, "", err)
	}
	provider := file.Info().Source.SourcePath
	player, err := output.NewMusicPlayer(file, filepath.Ext(path))
	if err == nil && player == nil {
		err = fmt.Errorf("music output returned no player")
	}
	if err != nil {
		_ = file.Close()
		return nil, musicError(path, provider, err)
	}
	return &musicFilePlayer{MusicPlayer: player, file: file, path: path}, nil
}

// StartMusic arms battle entry until the desktop output is installed. It never
// opens a device during detached session construction [I6].
func (a *Service) StartMusic() { a.musicStartPending = true }

// ServiceMusic runs on the host presentation pump, including while simulation
// is paused. Only a drained, successful track produces a completion notification
// [03 R-AUD-01 §4]; a stopped/failed player never synthesizes one.
func (a *Service) ServiceMusic() error {
	if a == nil || a.Music == nil {
		return nil
	}
	c := a.Music
	// Timers can replace the current track. Preserve failures before they
	// can close its source or start another track.
	if c.player != nil {
		if err := c.player.Err(); err != nil {
			c.Stop()
			a.musicStartPending = false
			c.mediaError = nil
			return err
		}
	}
	if a.musicStartPending && GlobalOutput() != nil {
		a.musicStartPending = false
		if c.IsEnabled() {
			c.tickFromMedia()
		}
	}
	c.ServiceTimers()
	if c.player != nil {
		c.position = c.player.PositionMillis()
		if err := c.player.Err(); err != nil {
			c.mediaError = err
			c.Stop()
		} else if c.status == StatusPlaying && c.player.Completed() {
			c.NotifySuccessfulCompletion()
		}
	}
	err := c.mediaError
	c.mediaError = nil
	return err
}
