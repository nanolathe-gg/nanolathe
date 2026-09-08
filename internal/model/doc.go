// Package model implements the 3DO piece hierarchy and transform composition [03 §2.4] [PLAN_06 WU-06-8].
//
// Contracts C20–C24 plus the model portion of the Public API block are owned here.
// The lossless formats parser retains authored 3DO primitive order and selection;
// this package compiles retail's one-time selection swap and stable mean-Y order
// without mutating that parse [02 "Model archive (3DO)"][03 §2.4]. This package
// also owns the recursive half-turn negation pass and world transform composition.
package model
