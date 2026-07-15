package main

import (
	"fmt"
	"os"

	"github.com/chonkpilot/chonkpilot/pkg/executor"
)

func main() {
	if err := executor.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
