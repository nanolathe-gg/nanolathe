package client

import "github.com/nanolathe-gg/nanolathe/internal/input"

// MouseState is internal/input's mouse vocabulary, re-exported at the client
// edge so UI and command code share one state model.
type MouseState = input.MouseState

// KeyboardState is internal/input's key vocabulary at the client edge.
type KeyboardState = input.KeyboardState

// InputState is the combined per-frame input the client hands to the UI.
type InputState = input.State

func newInputState() *input.State { return input.NewState() }
