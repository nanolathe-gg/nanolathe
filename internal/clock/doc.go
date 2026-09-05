// Package clock implements the retail 30 Hz fixed-step budget.
//
// This is the single multiplayer-aware timebase that decides how many
// simulation sub-ticks run each pump iteration. Simulation never reads
// wall-clock state [01 §4.2], [01 §4.3], [01 §4.4]; presentation samples the
// committed frame without interpolation [03 §2.4].
package clock
