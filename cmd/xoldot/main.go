package main

import (
	"fmt"
	"os"

	"github.com/xolan/xoldot/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version); err != nil {
		fmt.Fprintf(os.Stderr, "xoldot: %v\n", err)
		os.Exit(1)
	}
}
