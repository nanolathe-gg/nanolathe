package client

import "time"

// BenchmarkFrame separates CPU model composition/recording from classic replay.
// Like Frame, it drains presentation events once and uses the CPU model path.
func (c *Client) BenchmarkFrame() (pixels []byte, composeMS, replayMS float64) {
	wasRecording, wasOnly := c.recordModelGeometry, c.geometryOnlyModels
	c.recordModelGeometry, c.geometryOnlyModels = false, false
	start := time.Now()
	c.recordFrame()
	composeMS = float64(time.Since(start)) / 1e6
	c.recordModelGeometry, c.geometryOnlyModels = wasRecording, wasOnly
	start = time.Now()
	c.list.Replay(c.classicSink())
	replayMS = float64(time.Since(start)) / 1e6
	return c.rgba, composeMS, replayMS
}
