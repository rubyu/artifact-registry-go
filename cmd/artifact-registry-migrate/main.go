package main

import (
	"context"
	"github.com/rubyu/artifact-registry-go/internal/tooling"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(tooling.Run(ctx, "migrate", os.Args[1:], os.Stdout))
}
