package main

import (
	"fmt"
	"os"

	"github.com/chonkpilot/chonkpilot/pkg/executor"
)

func main() {
	args := os.Args[1:]

	// Check for daemon mode
	for _, arg := range args {
		if arg == "--internal" {
			if err := executor.RunDaemon(args); err != nil {
				fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	if err := executor.Run(args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
