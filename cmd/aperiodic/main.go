package main

import (
	"os"

	"aperiodic"
)

func main() {
	os.Exit(aperiodic.NewCLI().Run(os.Args[1:]))
}
