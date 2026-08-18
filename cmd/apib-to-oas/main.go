// Command apib-to-oas converts API Blueprint to OpenAPI 3.x.
//
// Usage:
//
//	apib-to-oas convert <input.apib> [-o output.yaml] [--format yaml|json]
//	apib-to-oas version
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecoletti/apib-to-oas/internal/cli"
)

// Stamped at link time via -ldflags "-X main.<key>=...". The release
// pipeline fills all three; plain `go build` leaves the placeholders.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	app := cli.New(version, commit, date)
	if err := app.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "apib-to-oas: %v\n", err)
		os.Exit(cli.ExitCode(err))
	}
}
