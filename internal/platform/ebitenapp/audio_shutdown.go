package ebitenapp

import "github.com/nanolathe-gg/nanolathe/internal/audio"

// shutdownAudioOutput is process cleanup at the device boundary. Injected
// outputs retain their own lifetime unless they opt in to the same shutdown.
func shutdownAudioOutput(output audio.Output) {
	if device, ok := output.(interface{ Shutdown() }); ok {
		device.Shutdown()
	}
}
