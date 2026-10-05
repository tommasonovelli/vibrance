package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // registers the "sqlite" driver of database/sql
)

// The stable codes of the store's failures. The startup logs them; the
// operator acts differently on each.
const (
	// CodeOpen: the database file cannot be opened, or it is not in WAL
	// mode.
	CodeOpen = "store_open"
	// CodeSchemaTooNew: the database was migrated by a newer binary.
	// Migrations only go forward (DESIGN.md I13), so older code must not
	// run on it.
	CodeSchemaTooNew = "store_schema_too_new"
	// CodeMigrate: any other failure while applying the migrations.
	CodeMigrate = "store_migrate"
	// CodeClose: the database was not closed cleanly. Nothing is lost: the
	// next start recovers what the write-ahead log holds.
	CodeClose = "store_close"
)

// Error is a failure of the store itself, with its stable code. Err keeps
// the cause, from SQLite or from goose.
type Error struct {
	Code string
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// Code returns the code of the first *Error in err's tree, otherwise "".
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// The settings of every connection (DESIGN.md §5.1). They are per
// connection in SQLite, so they are part of the DSN: database/sql then
// applies them to each connection it opens, now or later (T4). An Exec on
// the handle would reach only one connection of the pool.
//
// synchronous=FULL because the data of the users matter more than the
// speed of the writes, which are few.
var connPragmas = []string{
	"busy_timeout(5000)",
	"foreign_keys(ON)",
	"journal_mode(WAL)",
	"synchronous(FULL)",
}

// The statements that configure and maintain the database file. sqlc does
// not read PRAGMA, so they are here and not in sql/ (NOTES.md N-023).
const (
	pragmaJournalMode = `PRAGMA journal_mode`
	pragmaOptimize    = `PRAGMA optimize`
	// The row it returns is (busy, log, checkpointed).
	pragmaCheckpoint = `PRAGMA wal_checkpoint(TRUNCATE)`
)

// Store is the database: one SQLite file behind two handles (§5.1).
// SQLite has one writer at a time, so every write goes through the single
// connection of the write handle, and the reads go through a pool of
// connections that cannot write.
type Store struct {
	write *sql.DB
	read  *sql.DB
}

// Open opens the database file at path, creating it if it does not exist,
// and applies the embedded migrations. The folder of path must exist and
// be on a local disk: the write-ahead log does not work on network
// filesystems. Every error is an *Error.
func Open(ctx context.Context, path string) (*Store, error) {
	write, err := openHandle(ctx, path, true)
	if err != nil {
		return nil, err
	}
	if err := checkWAL(ctx, write); err != nil {
		return nil, closeAfter(err, write)
	}
	if err := migrate(ctx, write); err != nil {
		return nil, closeAfter(err, write)
	}
	read, err := openHandle(ctx, path, false)
	if err != nil {
		return nil, closeAfter(err, write)
	}
	return &Store{write: write, read: read}, nil
}

// dsn is the address of the database for one of the two handles. The
// write handle begins its transactions with BEGIN IMMEDIATE: a
// transaction that began as a reader and then writes fails at once with
// SQLITE_BUSY when another connection wrote in between, whatever the busy
// timeout (T3). The read handle refuses every write (query_only).
func dsn(path string, write bool) string {
	q := url.Values{"_pragma": connPragmas}
	if write {
		q.Set("_txlock", "immediate")
	} else {
		q.Add("_pragma", "query_only(ON)")
	}
	// A URI, so that a path with `?`, `#` or `%` is still a path.
	u := url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: q.Encode()}
	return u.String()
}

// openHandle opens one of the two handles and its first connection.
func openHandle(ctx context.Context, path string, write bool) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path, write))
	if err != nil {
		return nil, &Error{Code: CodeOpen, Msg: "cannot open the database file", Err: err}
	}
	if write {
		db.SetMaxOpenConns(1)
	}
	// sql.Open opens nothing: the first connection is what creates the
	// file and applies the settings.
	if err := db.PingContext(ctx); err != nil {
		return nil, closeAfter(&Error{Code: CodeOpen, Msg: "cannot open the database file", Err: err}, db)
	}
	return db, nil
}

// closeAfter closes the handles after err, which it returns with the
// errors of the closing, if any.
func closeAfter(err error, handles ...*sql.DB) error {
	for _, db := range handles {
		if cerr := db.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("closing the database: %w", cerr))
		}
	}
	return err
}

// checkWAL refuses a database that is not in WAL mode. SQLite does not
// fail when it cannot switch to it (a filesystem without shared memory, a
// network disk): it answers with the mode it kept. Without the WAL a
// reader would block the writer, and the other way round.
func checkWAL(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, pragmaJournalMode).Scan(&mode); err != nil {
		return &Error{Code: CodeOpen, Msg: "cannot read the journal mode of the database", Err: err}
	}
	if mode != "wal" {
		return &Error{Code: CodeOpen, Msg: fmt.Sprintf(
			"the database is in journal mode %q and cannot be switched to WAL: its folder must be on a local disk", mode)}
	}
	return nil
}

// migrate applies the embedded migrations, forward only, each in its own
// transaction. A database that a newer binary migrated is refused before
// anything is applied (I13).
func migrate(ctx context.Context, db *sql.DB) error {
	p, _, _, err := schemaVersions(ctx, db)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return &Error{Code: CodeMigrate, Msg: "cannot apply the migrations", Err: err}
	}
	return nil
}

// Optimize lets SQLite refresh the statistics of its query planner for the
// tables that changed much since they were last analyzed (PRAGMA optimize).
// The scanner asks for it at the end of a cycle that changed much of the
// index (§6.1, T30). It runs on the write connection, outside any
// transaction, and waits for the one that is running.
func (s *Store) Optimize(ctx context.Context) error {
	if _, err := s.write.ExecContext(ctx, pragmaOptimize); err != nil {
		return fmt.Errorf("store: optimizing: %w", endOf(ctx, err))
	}
	return nil
}

// Close is the clean end (§11.2): it lets SQLite refresh the statistics
// of its query planner, moves the write-ahead log into the database file
// and empties it, and closes the two handles. No transaction may be
// running. It is not a protocol the data depend on: after a kill, the
// next Open recovers from the log (crash-only).
func (s *Store) Close() error {
	var errs []error
	// The readers first: the checkpoint cannot empty a log that a
	// connection still reads.
	if err := s.read.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing the read handle: %w", err))
	}
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, pragmaOptimize); err != nil {
		errs = append(errs, fmt.Errorf("optimizing: %w", err))
	}
	var busy, logFrames, checkpointed int
	switch err := s.write.QueryRowContext(ctx, pragmaCheckpoint).Scan(&busy, &logFrames, &checkpointed); {
	case err != nil:
		errs = append(errs, fmt.Errorf("checkpointing the write-ahead log: %w", err))
	case busy != 0:
		errs = append(errs, errors.New("checkpointing the write-ahead log: another connection still uses it"))
	}
	if err := s.write.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing the write handle: %w", err))
	}
	if len(errs) > 0 {
		return &Error{Code: CodeClose, Msg: "the database was not closed cleanly", Err: errors.Join(errs...)}
	}
	return nil
}
