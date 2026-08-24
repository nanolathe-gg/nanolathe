// Command nanolathe runs the engine. Phase 0 boots headless: it resolves a
// retail install, reports what it found, and fails with a diagnostic naming
// the missing product and every provider searched.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nanolathe/nanolathe/internal/version"
)

func main() {
	opts, err := parseFlags(os.Args[1:], os.Stdout)
	if err != nil {
		if errors.Is(err, ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}
	if err := run(opts, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// seedFor resolves the simulation seed. Retail seeds at battle entry from a
// high-resolution counter [01 §7.1]; --seed makes a run reproducible, which
// matters because RNG state is not saved and is reseeded on load
// [08 "Scheduler and random state in saves"].
func seedFor(opts Options) uint32 {
	if opts.Seed >= 0 {
		return uint32(opts.Seed)
	}
	return uint32(time.Now().UnixNano())
}

func run(opts Options, out *os.File) error {
	fmt.Fprintf(out, "%s\n", version.ProfileID())

	content, err := openContent(opts)
	if err != nil {
		return err
	}
	defer content.Close()

	seed := seedFor(opts)
	fmt.Fprintf(out, "seed: %d\n", seed)

	// Gate 1: windowed terrain viewer when --map is set, --headless is false,
	// and no --dump is requested. This opens the Kaiju window and draws real
	// TNT terrain with camera pan and FNT overlay [PLAN_04A].
	if !opts.Headless && opts.Map != "" && opts.Dump == "" {
		if err := runViewer(opts, content); err != nil {
			// If viewer fails (e.g., no display), fall back to headless report
			// so CI and headless environments still produce useful output.
			fmt.Fprintf(os.Stderr, "nanolathe: viewer: %v (falling back to headless report)\n", err)
		} else {
			return nil
		}
	}

	return report(opts, content, out)
}
