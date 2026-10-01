package main

import (
	"context"
	"log/slog"
	"os/signal"
	"runtime"
	"syscall"

	"vibrance/internal/app"
	"vibrance/internal/config"
)

// serve is `vibrance serve`: the server, until SIGTERM or SIGINT. A
// refusal before anything is opened (running as root, an invalid
// configuration) exits 2; any later failure exits 1.
func serve(getenv func(string) string, euid int, log *slog.Logger) int {
	// Whatever the runtime or the entrypoint set: files the server creates
	// are never writable by group or others.
	syscall.Umask(0o022)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	// GOMAXPROCS is the number of CPUs available to the process: it honors
	// the CPU limit of the container as well as the affinity.
	err := app.Run(ctx, getenv, euid, runtime.GOMAXPROCS(0), log)
	if err == nil {
		return exitOK
	}
	code := app.Code(err)
	log.Error(err.Error(), "code", code)
	if code == app.CodeRunAsRoot || code == config.Code {
		return exitUsage
	}
	return exitFailure
}
