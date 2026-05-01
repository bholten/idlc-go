package main

import (
	"os"

	"github.com/bholten/tools/idlc-go/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
