// Package triggers is the mission trigger machine: the authored trigger
// records, their parse, the per-tick evaluation of their conditions and
// actions, and the save form that carries their state across a load
// [08 R-TRIG-01].
//
// Evaluation is ordered and side-effect-free with respect to the simulation
// except through the actions it reports; internal/mission owns the records and
// internal/session runs the pass.
package triggers
