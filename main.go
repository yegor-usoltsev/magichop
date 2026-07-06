package main

import (
	"os"

	"github.com/yegor-usoltsev/MagicHop/cmd"
)

func main() {
	os.Exit(cmd.Run(os.Args[1:])) //nolint:forbidigo // main entry point requires os.Exit
}
