package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/nanolathe/nanolathe/internal/cleanroom"
)

func main() {
	f, err := cleanroom.Scan(os.Args[1])
	if err != nil {
		panic(err)
	}
	counts := cleanroom.CountsByFile(f)
	keys := make([]string, 0, len(counts))
	total := 0
	for k, v := range counts {
		keys = append(keys, k)
		total += v
	}
	sort.Strings(keys)
	fmt.Println("package cleanroom")
	fmt.Println()
	fmt.Println("// Baseline is the census of raw-forensics occurrences that predate the")
	fmt.Println("// clean-room lint, taken with tools/cleanroom-baseline. It is a debt")
	fmt.Println("// register, not a permission list: every entry is a comment or research")
	fmt.Println("// line that still has to be rewritten as clean-room prose, with its")
	fmt.Println("// address-level trail moved to $HOME/ta-decompile/notes/.")
	fmt.Println("//")
	fmt.Printf("// Total at baseline: %d occurrences across %d files.\n", total, len(keys))
	fmt.Println("//")
	fmt.Println("// A file absent from this map must have zero occurrences. Counts may only")
	fmt.Println("// go down, and going down requires updating this file in the same change.")
	fmt.Println("var Baseline = map[string]int{")
	for _, k := range keys {
		fmt.Printf("\t%q: %d,\n", k, counts[k])
	}
	fmt.Println("}")
}
