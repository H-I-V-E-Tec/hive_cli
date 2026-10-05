package main

import (
	"os"

	"github.com/H-I-V-E-Tec/hive_cli/internal/cli"
)

// Version is injected at release build time (-X main.Version=vX.Y.Z).
var Version = "dev"

func main() {
	os.Exit(cli.Main(Version, os.Args[1:]))
}
