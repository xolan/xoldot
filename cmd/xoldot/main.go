package main

import (
	"fmt"
	"os"

	"github.com/xolan/xoldot/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version); err != nil {
		message := fmt.Sprintf("xoldot: %v\n", err)
		if info, statErr := os.Stderr.Stat(); statErr == nil && info.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == "" {
			message = "\x1b[31m" + message + "\x1b[0m"
		}
		fmt.Fprint(os.Stderr, message)
		os.Exit(1)
	}
}
