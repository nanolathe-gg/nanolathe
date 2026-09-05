// Package cob is the compiled-script machine: the COB loader, the opcode
// interpreter and its eight threads, the piece surface, and the engine ports
// and callbacks that join a script to the simulation [fmt cob]
// [02 "Compiled script archive (COB)"] [04 §4.1] [04 §4.2] [04 §4.3]
// [04 §4.4] [04 §5].
//
// A VM belongs to one unit. It owns no simulation state: everything it reads
// or writes outside its own threads and piece words goes through a bound port
// or a callback, which internal/units and internal/session install.
package cob
