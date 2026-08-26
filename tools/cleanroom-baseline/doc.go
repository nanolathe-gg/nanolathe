// Command cleanroom-baseline regenerates internal/cleanroom/baseline.go, the
// per-file census of raw-forensics occurrences the clean-room lint ratchets
// down. Run it after rewriting comments or research into clean-room prose:
//
//	go run ./tools/cleanroom-baseline . > internal/cleanroom/baseline.go
//	gofmt -w internal/cleanroom/baseline.go
//
// See AGENTS.md §"Clean-room discipline".
package main
