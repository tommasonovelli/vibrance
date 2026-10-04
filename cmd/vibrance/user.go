package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"vibrance/internal/app"
	"vibrance/internal/auth"
	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

const userUsage = "usage: vibrance user create --username U --role admin|user --password-stdin" +
	" | vibrance user reset-password --username U --password-stdin | vibrance user list"

// user is `vibrance user ...` (DESIGN.md §7.4): the accounts, managed from
// the machine of the server. It works on the database while the server
// runs: the sessions are in the database, so a password reset here signs
// the user out there at once.
//
// The password comes from standard input only, never from the command
// line, which other users of the machine and the shell history can read
// (I5). The arguments are never logged: what was typed where a password
// does not belong may be one.
//
// Exit codes (§11.4): 0 done; 2 refused before anything was done (the
// arguments, a name or a password that is not valid, running as root);
// 1 anything else (no such user, a name that is taken, the database).
func user(args []string, euid int, stdin io.Reader, stdout io.Writer, stateDir string, log *slog.Logger) int {
	c, ok := parseUser(args)
	if !ok {
		log.Error(userUsage, "code", "usage")
		return exitUsage
	}
	if euid == 0 {
		// Root would create files of the database that the server, which
		// never runs as root, could not open.
		log.Error("vibrance must not run as root: run it as the user of the server", "code", app.CodeRunAsRoot)
		return exitUsage
	}
	if c.name == "create" {
		// A name that cannot be one is refused before the database is
		// opened, or created (§11.4).
		if err := auth.CheckUsername(c.username); err != nil {
			return userFailed(log, err)
		}
	}
	syscall.Umask(0o022)

	password := ""
	if c.passwordStdin {
		var err error
		if password, err = readPassword(stdin); err != nil {
			return userFailed(log, err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	accounts, closeAccounts, err := app.OpenAccounts(ctx, stateDir, log)
	if err != nil {
		return userFailed(log, err)
	}
	err = c.run(ctx, accounts, password, stdout)
	if cerr := closeAccounts(); cerr != nil {
		// What the command did is committed. This is only the write-ahead
		// log, which could not be emptied because the server is reading:
		// the server does it when it stops.
		log.Warn(cerr.Error(), "code", store.Code(cerr))
	}
	if err != nil {
		return userFailed(log, err)
	}
	return exitOK
}

// userCommand is a parsed `vibrance user` command line.
type userCommand struct {
	name          string // create, reset-password or list
	username      string
	role          string
	passwordStdin bool
}

// parseUser accepts exactly the three forms of userUsage: every flag a form
// names is required, and nothing else is accepted.
func parseUser(args []string) (userCommand, bool) {
	if len(args) == 0 {
		return userCommand{}, false
	}
	c := userCommand{name: args[0]}
	flags := flag.NewFlagSet("user", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	switch c.name {
	case "create":
		flags.StringVar(&c.username, "username", "", "")
		flags.StringVar(&c.role, "role", "", "")
		flags.BoolVar(&c.passwordStdin, "password-stdin", false, "")
	case "reset-password":
		flags.StringVar(&c.username, "username", "", "")
		flags.BoolVar(&c.passwordStdin, "password-stdin", false, "")
	case "list":
	default:
		return userCommand{}, false
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return userCommand{}, false
	}
	switch c.name {
	case "create":
		return c, c.username != "" && (c.role == auth.RoleAdmin || c.role == auth.RoleUser) && c.passwordStdin
	case "reset-password":
		return c, c.username != "" && c.passwordStdin
	default:
		return c, true
	}
}

// readPassword reads the password from standard input: everything up to
// the end, without the one line break that `echo` and a file end with. It
// reads little more than the longest password: more than that is refused
// by the rules of a password, like anything else that is not one.
func readPassword(stdin io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(stdin, auth.MaxPasswordBytes+3))
	if err != nil {
		// The text of the error could quote what was read: it is not kept.
		return "", errors.New("reading the password from standard input failed")
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	if err := auth.CheckPassword(password); err != nil {
		return "", err
	}
	return password, nil
}

func (c userCommand) run(ctx context.Context, accounts *auth.Service, password string, stdout io.Writer) error {
	switch c.name {
	case "create":
		_, err := accounts.CreateUser(ctx, c.username, password, c.role)
		return err
	case "reset-password":
		return accounts.ResetPassword(ctx, c.username, password)
	default:
		users, err := accounts.ListUsers(ctx)
		if err != nil {
			return err
		}
		return printUsers(stdout, users)
	}
}

// printUsers writes one line per account, under a header.
func printUsers(stdout io.Writer, users []auth.User) error {
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "USERNAME\tROLE\tSTATE\tCREATED\tID"); err != nil {
		return fmt.Errorf("writing the accounts: %w", err)
	}
	for _, u := range users {
		state := "enabled"
		if u.Disabled {
			state = "disabled"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", u.Username, u.Role, state, u.CreatedAt.Format(time.RFC3339), u.ID); err != nil {
			return fmt.Errorf("writing the accounts: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing the accounts: %w", err)
	}
	return nil
}

// userFailed logs why the command did nothing and returns its exit code. A
// name or a password that is not valid is a refusal of the arguments.
func userFailed(log *slog.Logger, err error) int {
	var refusal *httpx.Error
	if errors.As(err, &refusal) {
		log.Error(refusal.Message, "code", refusal.Code)
		if refusal.Code == auth.CodeUsernameInvalid || refusal.Code == auth.CodePasswordInvalid {
			return exitUsage
		}
		return exitFailure
	}
	log.Error(err.Error(), "code", app.Code(err))
	return exitFailure
}
