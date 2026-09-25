package main

import (
	"fmt"
	"os"

	"github.com/oldanidavide/portctl/internal/cli"
)

func main() {
	app, err := cli.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		os.Exit(1)
	}
	os.Exit(app.Run(os.Args[1:]))
}
