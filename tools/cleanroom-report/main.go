package main

import (
	"fmt"
	"os"

	"github.com/nanolathe/nanolathe/internal/cleanroom"
)

func main() {
	f, err := cleanroom.Scan(os.Args[1])
	if err != nil {
		panic(err)
	}
	byPattern := map[string]int{}
	for _, x := range f {
		byPattern[x.Pattern]++
		if len(os.Args) > 2 && os.Args[2] == x.Pattern {
			fmt.Printf("%s:%d\n", x.Path, x.Line)
		}
	}
	for k, v := range byPattern {
		fmt.Fprintf(os.Stderr, "%s: %d\n", k, v)
	}
}
