package client

import "github.com/nanolathe/nanolathe/internal/input"

// Input types are defined by internal/input; aliases keep the client edge
// source-compatible while ensuring UI and command code share one state model.
type MouseState = input.MouseState
type KeyboardState = input.KeyboardState
type InputState = input.State

func newInputState() *input.State { return input.NewState() }
