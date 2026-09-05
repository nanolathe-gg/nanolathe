// Package model implements the 3DO piece hierarchy and transform composition [03 §2.4] [PLAN_06 WU-06-8].
//
// Contracts C20–C24 plus the model portion of the Public API block are owned here.
// Load-time primitive reordering (selection swap + mean-Y bubble sort) is already
// applied by formats.ThreeDO per [GAP 02-A6] — see formats/three_do.go primitive
// reordering loop — and is NOT redone here. This package owns the recursive
// half-turn negation pass and the world transform composition.
package model
