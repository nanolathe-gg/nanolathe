package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utility"
)

func init() {
	registerBrain("utility", func(params map[string]string) aikit.Brain {
		p, err := utility.ParseParams(params)
		if err != nil {
			panic(err)
		}
		return utility.New(p)
	})
}
