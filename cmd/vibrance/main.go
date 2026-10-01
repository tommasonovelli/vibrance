// Command vibrance is the Vibrance server and its operational subcommands
// (DESIGN.md §3.1, §11.4):
//
//	vibrance serve        run the server until SIGTERM or SIGINT
//	vibrance healthcheck  query /health/ready on VIBRANCE_HTTP_ADDR; exit 0 or 1
//	vibrance version      print the version stamped at build time
//
// Configuration comes only from the environment (§11.1). Logs are JSON
// lines on stdout (§11.5).
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"vibrance/internal/buildinfo"
)

// Exit codes (§11.4), as in MusicLib.
const (
	exitOK      = 0
	exitFailure = 1 // the operation failed
	exitUsage   = 2 // refused before doing anything: invalid arguments or configuration, running as root
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	os.Exit(run(os.Args[1:], os.Getenv, os.Geteuid(), os.Stdout, log))
}

// run is the whole program. euid is the effective user id of the process.
func run(args []string, getenv func(string) string, euid int, stdout io.Writer, log *slog.Logger) int {
	switch {
	case len(args) == 1 && args[0] == "serve":
		return serve(getenv, euid, log)
	case len(args) == 1 && args[0] == "healthcheck":
		return healthcheck(getenv, log)
	case len(args) == 1 && args[0] == "version":
		return printVersion(stdout, log)
	default:
		log.Error("usage: vibrance serve|healthcheck|version", "code", "usage", "args", args)
		return exitUsage
	}
}

// printVersion is `vibrance version`: one line, `version: <version>`. It
// reads no environment and needs neither the database nor the volumes.
func printVersion(stdout io.Writer, log *slog.Logger) int {
	if _, err := fmt.Fprintf(stdout, "version: %s\n", buildinfo.Version); err != nil {
		log.Error("cannot write the version", "code", "version_output", "err", err)
		return exitFailure
	}
	return exitOK
}
