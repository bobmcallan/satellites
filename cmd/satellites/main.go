package main

import (
	"os"

	"github.com/bobmcallan/satellites/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
