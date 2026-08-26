// Command cleanroom-report lists the file and line of every raw-forensics
// occurrence the clean-room lint finds, so a scrub can be worked file by file:
//
//	go run ./tools/cleanroom-report . executable-address
//
// Pattern names are executable-address, decompiler-symbol, decompiler-local
// and structure-offset; a per-pattern total is written to stderr regardless.
// See AGENTS.md §"Clean-room discipline".
package main
