package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"vibrance/internal/app"
)

// The usage of the operational commands of the database (DESIGN.md §11.4).
const (
	backupUsage        = "usage: vibrance backup --to /backup/NAME"
	restoreUsage       = "usage: vibrance restore --from /backup/NAME"
	doctorUsage        = "usage: vibrance doctor"
	rebuildSearchUsage = "usage: vibrance rebuild-search"
)

// backup is `vibrance backup --to /backup/NAME`: a new, verified backup of
// the database, also while the server runs. Exit codes (§11.4): 0 the
// backup is complete under its name; 2 refused, nothing was written; 1 it
// failed, and at most a temporary folder is left next to the destination.
func backup(args []string, euid int, stdout io.Writer, stateDir, backupDir string, log *slog.Logger) int {
	dest, ok := parsePathFlag(args, "to")
	if !ok {
		log.Error(backupUsage, "code", "usage")
		return exitUsage
	}
	if refused := refuseRoot(euid, log); refused {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if _, err := app.Backup(ctx, stateDir, backupDir, dest, time.Now); err != nil {
		return operationFailed(log, err)
	}
	return report(stdout, log, "Backup completed: %s\n", dest)
}

// restore is `vibrance restore --from /backup/NAME`: the database of a
// verified backup, into a state folder that has none. Exit codes: 0 the
// database is in place; 2 refused, nothing was written; 1 it failed, and
// the state folder still has no database.
func restore(args []string, euid int, stdout io.Writer, stateDir, backupDir string, log *slog.Logger) int {
	from, ok := parsePathFlag(args, "from")
	if !ok {
		log.Error(restoreUsage, "code", "usage")
		return exitUsage
	}
	if refused := refuseRoot(euid, log); refused {
		return exitUsage
	}
	// The database is created with the mode the server gives it.
	syscall.Umask(0o022)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if _, err := app.Restore(ctx, stateDir, backupDir, from); err != nil {
		return operationFailed(log, err)
	}
	return report(stdout, log, "Restore completed: %s. Start the server.\n", from)
}

// doctor is `vibrance doctor`: it inspects the database, also while the
// server runs, and changes nothing. It prints one line per finding and a
// last line. Exit codes: 0 no damage; 1 damage found, or an inspection
// that could not be completed; 2 refused.
func doctor(args []string, euid int, stdout io.Writer, stateDir string, log *slog.Logger) int {
	if len(args) != 0 {
		log.Error(doctorUsage, "code", "usage")
		return exitUsage
	}
	if refused := refuseRoot(euid, log); refused {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	findings, err := app.Doctor(ctx, stateDir)
	if err != nil {
		return operationFailed(log, err)
	}
	for _, f := range findings {
		if _, err := fmt.Fprintf(stdout, "%s %s: %s. Advice: %s.\n", f.Code, f.Entity, f.Message, f.Advice); err != nil {
			log.Error("cannot write the report", "code", "doctor_output", "err", err)
			return exitFailure
		}
	}
	if len(findings) == 0 {
		return report(stdout, log, "Doctor complete: no damage found.\n")
	}
	if status := report(stdout, log, "Doctor complete: %d problems found. Nothing was changed.\n", len(findings)); status != exitOK {
		return status
	}
	return exitFailure
}

// rebuildSearch is `vibrance rebuild-search`: it makes the full-text tables
// of the search again from the index of the library, in one transaction. It
// is the repair of what the doctor finds wrong in the search. Exit codes: 0
// the tables are rebuilt; 2 refused, nothing was changed; 1 it failed, and
// nothing was changed.
func rebuildSearch(args []string, euid int, stdout io.Writer, stateDir string, log *slog.Logger) int {
	if len(args) != 0 {
		log.Error(rebuildSearchUsage, "code", "usage")
		return exitUsage
	}
	if refused := refuseRoot(euid, log); refused {
		return exitUsage
	}
	// With the server stopped, opening the database creates the files of
	// its write-ahead log: they get the mode the server gives them.
	syscall.Umask(0o022)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := app.RebuildSearch(ctx, stateDir, log); err != nil {
		return operationFailed(log, err)
	}
	return report(stdout, log, "Search index rebuilt. Run vibrance doctor to check it.\n")
}

// parsePathFlag accepts exactly one flag, --name PATH, and nothing else.
func parsePathFlag(args []string, name string) (string, bool) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	path := flags.String(name, "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *path == "" {
		return "", false
	}
	return *path, true
}

// refuseRoot refuses uid 0: root would create files that the server, which
// never runs as root, could not open.
func refuseRoot(euid int, log *slog.Logger) bool {
	if euid != 0 {
		return false
	}
	log.Error("vibrance must not run as root: run it as the user of the server", "code", app.CodeRunAsRoot,
		"advice", "run the command as VIBRANCE_UID:VIBRANCE_GID, as Compose does")
	return true
}

// operationFailed logs why an operational command did not complete, with
// its code and advice, and returns its exit code: 2 for a refusal, which
// did nothing, 1 for a failure.
func operationFailed(log *slog.Logger, err error) int {
	var e *app.Error
	if !errors.As(err, &e) {
		log.Error(err.Error(), "code", app.Code(err))
		return exitFailure
	}
	log.Error(err.Error(), "code", e.Code, "advice", e.Advice)
	if e.Refusal {
		return exitUsage
	}
	return exitFailure
}

// report writes the last line of a command.
func report(stdout io.Writer, log *slog.Logger, format string, a ...any) int {
	if _, err := fmt.Fprintf(stdout, format, a...); err != nil {
		log.Error("cannot write the report", "code", "report_output", "err", err)
		return exitFailure
	}
	return exitOK
}
