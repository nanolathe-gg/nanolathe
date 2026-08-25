// Package session — shutdown cleanup order [01 §2.1][01 §2.3][P2-03].
//
// Startup order per [01 §2.1] (diagnostic init → singleton semaphore
//
//	`Total Annihilation` Open-or-Create 1/1 → CRT TLS seed → CLI + exe CWD via
//	GetModuleFileNameA 256 + SetCurrentDirectoryA (returns unchecked) → window
//	CS_DBLCLKS / WS_POPUP|WS_VISIBLE|WS_SYSMENU → 30-unit timebase → VFS mount
//	rev.GP3/CCX/UFO/≤10 local HPI/CDROM → language CLI>registry>English → registry
//	+ display/sound → pump/dispatcher).
//
// Shutdown partially traced [01 §2.3][01 §10 + Missing]: AudioCD registry
// shell value restore is proven; display/sound/archive handles/semaphore
// lifetime/worker termination not traced to a single finalizer. A clean
// implementation must make each resource's ownership explicit and preserve
// observed failure paths rather than assuming exit() is the only cleanup
// [01 §10].
//
// This file implements explicit reverse-init-order shutdown with
// singleton-semaphore release as the final handle close. It is a deliberate
// safe divergence from retail's unchecked truncation/failure on
// GetModuleFileNameA / SetCurrentDirectoryA: Nanolathe fails with diagnostic
// instead of reproducing unsafe reads [P2-03].
package session

import "sync"

// ShutdownOrder documents release order. Lower index = earlier startup.
// Shutdown runs reverse: window → display/sound → archives → semaphore.
// AudioCD registry restore is proven to run during shutdown [01 §2.1].
var ShutdownOrder = []string{
	"window",        // WndProc / WS_POPUP handle
	"display",       // GDI/DirectDraw surface
	"sound",         // DirectSound / waveOut
	"archives",      // lazy-reopened providers closed after validation
	"semaphore",     // named semaphore `Total Annihilation` CloseHandle exactly once (creator only)
	"registryAudio", // AudioCD shell value restore [01 §2.1]
}

// Shutdown manages ordered resource release. It is presentation/session only;
// simulation pools (units/projectiles/COB) are freed via their own pool.Free
// paths, not here.
type Shutdown struct {
	mu       sync.Mutex
	steps    []func() error
	released bool
}

// Register adds a shutdown step in startup order. Shutdown() will invoke
// steps in reverse (last startup = first shutdown) per reverse-init-order
// contract [P2-03].
func (s *Shutdown) Register(fn func() error) {
	if s == nil || fn == nil {
		return
	}
	s.mu.Lock()
	s.steps = append(s.steps, fn)
	s.mu.Unlock()
}

// Run executes registered steps in reverse order, collecting the first error
// but running all steps (partial initialization: even if startup failed mid-way,
// every registered resource is still released). It is idempotent: second call
// is no-op with the remembered first error.
func (s *Shutdown) Run() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return nil
	}
	s.released = true
	steps := append([]func() error(nil), s.steps...)
	s.mu.Unlock()
	var first error
	for i := len(steps) - 1; i >= 0; i-- {
		if err := steps[i](); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// SemaphoreName is the retail singleton semaphore name [01 §2.1].
const SemaphoreName = "Total Annihilation"

// SemaphoreResult models the singleton check: Open→exists? return -1 without
// handoff : Create 1/1. Second instance never receives a handle to close.
type SemaphoreResult struct {
	Exists bool // true = second instance path (Open succeeded)
	Handle any  // non-nil only for creator path; CloseHandle happens in shutdown reverse order
}
