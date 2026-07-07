package main

import (
	"os"

	"github.com/yegor-usoltsev/magichop/cmd"
)

func main() {
	os.Exit(cmd.Run(os.Args[1:]))
}
