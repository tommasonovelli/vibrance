// Command vibrance is the Vibrance server and its operational subcommands
// (DESIGN.md §3.1, §11.4):
//
//	vibrance serve        run the server until SIGTERM or SIGINT
//	vibrance healthcheck  query /health/ready on VIBRANCE_HTTP_ADDR; exit 0 or 1
//	vibrance version      print the version stamped at build time
//	vibrance user ...     create an account, reset a password, list the accounts
//	vibrance backup       copy the database into a new, verified backup under /backup
//	vibrance restore      put a backup in place as the database of an empty state folder
//	vibrance doctor       inspect the database; change nothing
//	vibrance rebuild-search  make the search index again from the index of the library
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

// stateDir is the folder of the server's state: the database, and later the
// thumbnails. It is fixed, not a setting (§3.4). It is a variable only so
// that the process tests can link a binary that keeps its state in a
// temporary folder (-ldflags "-X main.stateDir=...").
var stateDir = "/var/lib/vibrance"

// musiclibDir is the folder MusicLib's data volume is mounted at, read
// only. It is fixed too (§3.4), and a variable for the same reason.
var musiclibDir = "/musiclib"

// backupDir is the folder of the backups, the mount of VIBRANCE_BACKUP. It
// is fixed too (§3.4), and a variable for the same reason.
var backupDir = "/backup"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	os.Exit(run(os.Args[1:], os.Getenv, os.Geteuid(), os.Stdin, os.Stdout, log))
}

// run is the whole program. euid is the effective user id of the process.
func run(args []string, getenv func(string) string, euid int, stdin io.Reader, stdout io.Writer, log *slog.Logger) int {
	switch {
	case len(args) == 1 && args[0] == "serve":
		return serve(getenv, euid, stateDir, musiclibDir, log)
	case len(args) == 1 && args[0] == "healthcheck":
		return healthcheck(getenv, log)
	case len(args) == 1 && args[0] == "version":
		return printVersion(stdout, log)
	case len(args) >= 1 && args[0] == "user":
		return user(args[1:], euid, stdin, stdout, stateDir, log)
	case len(args) >= 1 && args[0] == "backup":
		return backup(args[1:], euid, stdout, stateDir, backupDir, log)
	case len(args) >= 1 && args[0] == "restore":
		return restore(args[1:], euid, stdout, stateDir, backupDir, log)
	case len(args) >= 1 && args[0] == "doctor":
		return doctor(args[1:], euid, stdout, stateDir, log)
	case len(args) >= 1 && args[0] == "rebuild-search":
		return rebuildSearch(args[1:], euid, stdout, stateDir, log)
	default:
		// Only the number of the arguments: a mistyped command line can hold
		// a password, and the log is kept (I5).
		log.Error("usage: vibrance serve|healthcheck|version|user|backup|restore|doctor|rebuild-search", "code", "usage", "args", len(args))
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
