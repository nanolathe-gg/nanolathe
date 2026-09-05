// Package audiobackend owns the desktop PCM device boundary.
//
// Keeping the Ebitengine audio import here lets authoritative packages import
// internal/audio without initializing a graphical platform backend [I6].
package audiobackend
