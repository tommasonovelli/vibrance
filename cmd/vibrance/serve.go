package main

import (
	"context"
	"log/slog"
	"os/signal"
	"runtime"
	"syscall"

	"vibrance/internal/app"
	"vibrance/internal/auth"
	"vibrance/internal/config"
)

// serve is `vibrance serve`: the server, until SIGTERM or SIGINT. A
// refusal of what the operator configured exits 2: running as root, an
// invalid configuration, and the variables of the first admin, which are
// read only when the database has no account (DESIGN.md §7.4). Any other
// failure exits 1.
func serve(getenv func(string) string, euid int, stateDir, musiclibDir string, log *slog.Logger) int {
	// Whatever the runtime or the entrypoint set: files the server creates
	// are never writable by group or others.
	syscall.Umask(0o022)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	// GOMAXPROCS is the number of CPUs available to the process: it honors
	// the CPU limit of the container as well as the affinity.
	err := app.Run(ctx, getenv, euid, runtime.GOMAXPROCS(0), stateDir, musiclibDir, log)
	if err == nil {
		return exitOK
	}
	code := app.Code(err)
	log.Error(err.Error(), "code", code)
	switch code {
	case app.CodeRunAsRoot, config.Code,
		auth.CodeAdminPasswordMissing, auth.CodeAdminPasswordInvalid, auth.CodeAdminUsernameInvalid:
		return exitUsage
	}
	return exitFailure
}
