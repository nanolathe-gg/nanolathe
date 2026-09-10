package client

import "fmt"

// SetDebugDeviceCapture installs the window owner's diagnostic writer. The
// writer runs synchronously on the caller's game/window goroutine; it must not
// compose another frame or mutate authoritative state.
func (c *Client) SetDebugDeviceCapture(write func(directory string) error) {
	c.debugDeviceCapture = write
}

// WriteDebugDeviceCapture captures the last presented device state after joining
// the recorder. The host reports the absence of a device as an incomplete
// capture rather than synthesizing an image (DESIGN_PRESENTATION_CLIENT,
// "On-demand diagnostic capture").
func (c *Client) WriteDebugDeviceCapture(directory string) error {
	c.JoinPreRecord()
	if c.debugDeviceCapture == nil {
		return fmt.Errorf("nanolathe: capture device state: logical path %s, providers searched [client], expected installed window diagnostic writer", directory)
	}
	return c.debugDeviceCapture(directory)
}
