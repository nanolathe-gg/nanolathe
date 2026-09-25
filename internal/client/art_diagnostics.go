package client

// ArtDiagnostic retains an authored identity and the first resolution failure.
// This is host diagnostics, not a change to the missing-art draw policy [I9].
type ArtDiagnostic struct {
	Path   string
	Entry  string
	Reason string
}

const maxArtDiagnostics = 64

// Called by the serial asset resolver, never by parallel model workers, and by
// the session's effect-timing resolver, which may run on the simulation
// goroutine; artMu orders the two. Failed banks remain negatively cached.
// Re-records can encounter an entry again but cannot append duplicate
// diagnostics or grow storage beyond this host limit.
func (c *Client) recordArtDiagnostic(path, entry, message string) {
	if mu := c.artMu; mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	c.recordArtDiagnosticLocked(path, entry, message)
}

func (c *Client) recordArtDiagnosticLocked(path, entry, message string) {
	for _, d := range c.artDiagnostics {
		if d.Path == path && d.Entry == entry {
			return
		}
	}
	if len(c.artDiagnostics) == maxArtDiagnostics {
		c.artDiagnosticsTruncated = true
		return
	}
	c.artDiagnostics = append(c.artDiagnostics, ArtDiagnostic{path, entry, message})
}

func (c *Client) addEffectStats(s EffectDrawStats) {
	c.effectStats.Admitted += s.Admitted
	c.effectStats.Sprites += s.Sprites
	c.effectStats.Models += s.Models
	c.effectStats.Halos += s.Halos
	c.effectStats.Skipped += s.Skipped
	c.effectStats.Strokes += s.Strokes
}

func (c *Client) addStripStats(s StripDrawStats) {
	c.stripStats.Blitted += s.Blitted
	c.stripStats.Filled += s.Filled
	c.stripStats.Unresolved += s.Unresolved
	c.stripStats.Gated += s.Gated
}
