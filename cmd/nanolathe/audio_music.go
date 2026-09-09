package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
)

// Music timers belong to the presentation busy pump. Each callback samples
// the actual scaled host clock, including nested timer registrations; neither
// simulation ticks nor the renderer's nominal delta can replace these reads
// [01 R-PLAT-02 §4][03 R-AUD-01 §4]. Binding is retained across battle entry.
func bindMusicClock(s *audio.Service) {
	if s == nil || s.Music == nil {
		return
	}
	millis := newMonotonicMillisSource()
	s.Music.SetPresentationClock(func() uint32 {
		return uint32(clock.ScaledNow(millis.Millis32()))
	})
}
