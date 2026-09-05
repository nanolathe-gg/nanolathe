// Package audio is the presentation-side sound service: the eight-slot cue
// queue and its priorities, sample decode and caching, positional attenuation
// and pan, the music controller and briefing speech.
//
// It is presentation-only [I6]: it reads the committed frame's events and
// draws only from a private copy of the CRT stream, never from the simulation
// stream [I4]. The PCM device itself is behind internal/audiobackend
// [03 §8.2] [03 §8.3] [03 §8.4].
package audio
