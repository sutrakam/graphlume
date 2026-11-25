// Command graphlume provides deterministic code intelligence & change safety for AI coding agents.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/sutrakam/graphlume/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx)
	stop()
	os.Exit(code)
}
