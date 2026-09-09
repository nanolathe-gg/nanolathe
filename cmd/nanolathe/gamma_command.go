package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// The slider and battle-entry conversion differs from +Gamma's direct tenth
// multiplier [07 R-FE-01 §11][07 R-CAM-01 §6]. Applying display buttons must
// therefore leave the current factor alone.
func applyGammaOption(cl *client.Client, value int) {
	if cl != nil {
		cl.SetGammaFactor(float32(0.5 - float64(value)*float64(float32(-1.0/24))))
	}
}

func (b *battleSession) setGammaCommand(value int) {
	if b.cl == nil {
		return
	}
	b.gammaSetting = value
	b.cl.SetGammaFactor(float32(float64(value) * float64(float32(0.1))))
	if b.shell != nil {
		b.shell.display.Gamma = value
		b.shell.saveSettings()
	} else {
		b.saveDirectChatSetting(func(s *settings.Settings) { s.Display.Gamma = value })
	}
}
