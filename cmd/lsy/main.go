package main

import (
	"context"
	"fmt"
	"os"

	"github.com/frinfo702/linear-doc-sync/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	app := cli.NewApp(cli.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	})
	if err := app.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
