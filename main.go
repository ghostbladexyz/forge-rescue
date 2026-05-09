package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ghostbladexyz/forge-rescue/internal/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Args[1:], cli.Env{}, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
