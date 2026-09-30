// Command vibrance is the Vibrance server and its operational subcommands
// (DESIGN.md §3.1, §11.4). Only one exists so far:
//
//	vibrance version  print the version stamped at build time
//
// Logs are JSON lines on stdout (§11.5).
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
	exitUsage   = 2 // refused before doing anything: invalid arguments
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, slog.New(slog.NewJSONHandler(os.Stdout, nil))))
}

func run(args []string, stdout io.Writer, log *slog.Logger) int {
	if len(args) == 1 && args[0] == "version" {
		return printVersion(stdout, log)
	}
	log.Error("usage: vibrance version", "code", "usage", "args", args)
	return exitUsage
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
