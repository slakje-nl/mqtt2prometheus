package main

import (
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if _, err := fmt.Fprintf(os.Stdout, "mqtt2prometheus %s (%s)\n", version, commit); err != nil {
		os.Exit(1)
	}
}
