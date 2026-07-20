package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ghostbladexyz/forge-rescue/internal/cli"
	"github.com/ghostbladexyz/forge-rescue/internal/gitmirror"
)

// main routes operation-scoped Git credential prompts before entering the public command interface.
func main() {
	if gitmirror.HandleAskPass(os.Args, os.Stdout) {
		return
	}
	if err := cli.Run(context.Background(), os.Args[1:], cli.Env{}, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
