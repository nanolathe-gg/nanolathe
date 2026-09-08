package main

import "github.com/nanolathe/nanolathe/internal/client"

// flushWindowTokens runs at an actual window open, before its next service
// pass. Cached asset lookups and redraws must not discard new input
// [07 R-WGT-02 §5].
func flushWindowTokens(cl *client.Client) {
	if cl != nil && cl.Input() != nil {
		cl.Input().DrainTokens()
	}
}
