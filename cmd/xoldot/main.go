package main

import (
	"os"

	"github.com/xolan/xoldot/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version); err != nil {
		_ = cli.WriteError(os.Stderr, err)
		os.Exit(1)
	}
}
