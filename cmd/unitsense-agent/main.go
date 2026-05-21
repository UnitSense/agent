package main

import (
	"fmt"
	"os"

	"github.com/UnitSense/agent/internal/cli"
)

var (
	version = "0.1.0-dev"
	commit  = "unknown"
)

func main() {
	cli.Version = version
	cli.Commit = commit
	if err := cli.Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
