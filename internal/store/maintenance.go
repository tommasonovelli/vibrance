package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	"github.com/pressly/goose/v3"

	"vibrance/migrations"
)

// CodeSchemaOld: the database was migrated by an older binary and the
// server has not migrated it yet. Only the server migrates: a command that
// reads the database with the queries of this binary refuses it.
const CodeSchemaOld = "store_schema_old"

// The statements that check and copy the database file. Like the other
// PRAGMA of this package they are not in sql/, which sqlc reads (NOTES.md
// N-023). VACUUM INTO writes a copy that holds one committed state of the
// database, whatever is written to it in the meantime, and keeps every
// rowid that is a declared key, so the full-text rows still point to their
// tracks (T6). Its only value is the path of the copy.
const (
	pragmaIntegrityCheck  = `PRAGMA integrity_check`
	pragmaForeignKeyCheck = `PRAGMA foreign_key_check`
	vacuumInto            = `VACUUM INTO ?`
)

// Reader is a database that is only read, and never migrated: the one of
// the server, which may be running, or a copy of it in a backup. It has
// no write handle, and its connections refuse every write.
type Reader struct {
	db              *sql.DB
	version, latest int64
}

// OpenReader opens the database file at path, which must exist: nothing is
// created. A frozen database is one that no process changes, a copy in a
// backup, which has no write-ahead log: it is opened without a lock and
// without one, so that reading it leaves no file next to it and not one
// byte of it changes.
//
// A file without a schema version is refused (CodeOpen), and so is one that
// a newer binary migrated (CodeSchemaTooNew). One that an older binary
// migrated is opened: CheckCurrent tells.
func OpenReader(ctx context.Context, path string, frozen bool) (*Reader, error) {
	// The settings are applied in this order: query_only is the last.
	q := url.Values{"_pragma": {"busy_timeout(5000)", "foreign_keys(ON)", "query_only(ON)"}}
	// rw opens the file as it is and never creates it.
	q.Set("mode", "rw")
	if frozen {
		q.Set("mode", "ro")
		q.Set("immutable", "1")
	}
	u := url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, &Error{Code: CodeOpen, Msg: "cannot open the database file", Err: err}
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, closeAfter(&Error{Code: CodeOpen, Msg: "cannot open the database file", Err: err}, db)
	}
	_, version, latest, err := schemaVersions(ctx, db)
	if err != nil {
		if Code(err) != CodeSchemaTooNew {
			// goose cannot create its table here, which is what it tries
			// on a file that has none.
			err = &Error{Code: CodeOpen, Msg: "the file is not a database of Vibrance: it has no readable schema version", Err: err}
		}
		return nil, closeAfter(err, db)
	}
	return &Reader{db: db, version: version, latest: latest}, nil
}

// schemaVersions returns the embedded migrations, the schema version of the
// database and the latest one this binary knows. It refuses a database
// that is ahead (I13), and applies nothing.
func schemaVersions(ctx context.Context, db *sql.DB) (p *goose.Provider, current, latest int64, err error) {
	p, err = goose.NewProvider(goose.DialectSQLite3, db, migrations.FS, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return nil, 0, 0, &Error{Code: CodeMigrate, Msg: "cannot load the migrations", Err: err}
	}
	if current, err = p.GetDBVersion(ctx); err != nil {
		return nil, 0, 0, &Error{Code: CodeMigrate, Msg: "cannot read the schema version", Err: err}
	}
	sources := p.ListSources()
	if latest = sources[len(sources)-1].Version; current > latest {
		return nil, 0, 0, &Error{Code: CodeSchemaTooNew, Msg: fmt.Sprintf(
			"the database has schema version %d and this binary knows up to %d: use the newer version of Vibrance that wrote it",
			current, latest)}
	}
	return p, current, latest, nil
}

// SchemaVersion is the schema version of the database, which is never
// newer than the binary.
func (r *Reader) SchemaVersion() int64 { return r.version }

// CheckCurrent refuses a database whose schema is older than the one of
// this binary (CodeSchemaOld): the queries of sql/ are written for the
// latest schema.
func (r *Reader) CheckCurrent() error {
	if r.version != r.latest {
		return &Error{Code: CodeSchemaOld, Msg: fmt.Sprintf(
			"the database has schema version %d and this binary needs %d: start the server once, which migrates it",
			r.version, r.latest)}
	}
	return nil
}

// Read runs fn in a read transaction, as Store.Read does: every query of
// fn sees one committed state of the database.
func (r *Reader) Read(ctx context.Context, fn func(q *Queries) error) error {
	return read(ctx, r.db, fn)
}

// Close closes the database. There is nothing to checkpoint: a Reader
// changes nothing.
func (r *Reader) Close() error {
	if err := r.db.Close(); err != nil {
		return &Error{Code: CodeClose, Msg: "the database was not closed cleanly", Err: err}
	}
	return nil
}

// CopyInto writes a copy of the database file at path into the new file
// into, which must not exist: one committed state of the database, also
// while the server writes to it (VACUUM INTO). The source must exist, and
// is not changed. The copy is not in WAL mode and has no file next to it.
func CopyInto(ctx context.Context, path, into string) (err error) {
	// A connection that may write: SQLite refuses VACUUM INTO on a
	// query_only one, although it only writes the other file (NOTES.md
	// N-028). rw never creates the source.
	q := url.Values{"_pragma": {"busy_timeout(5000)"}, "mode": {"rw"}}
	u := url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return &Error{Code: CodeOpen, Msg: "cannot open the database file", Err: err}
	}
	defer func() {
		if cerr := db.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("store: closing the database: %w", cerr))
		}
	}()
	if _, err := db.ExecContext(ctx, vacuumInto, into); err != nil {
		return fmt.Errorf("store: copying the database: %w", endOf(ctx, err))
	}
	return nil
}

// IntegrityCheck returns what PRAGMA integrity_check finds wrong in the
// database file: nothing when it is sound. SQLite checks the full-text
// indexes too, and stops after a hundred lines.
func (q *Queries) IntegrityCheck(ctx context.Context) ([]string, error) {
	return IntegrityLines(ctx, q.db, pragmaIntegrityCheck)
}

// IntegrityLines runs a statement of the family of PRAGMA integrity_check
// and returns its lines, without the single "ok" that means no damage. It
// is exported for internal/search, which checks each full-text table with
// a statement of its own.
func IntegrityLines(ctx context.Context, db DBTX, statement string) (lines []string, err error) {
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return nil, fmt.Errorf("store: checking the integrity: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("store: checking the integrity: %w", cerr))
		}
	}()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, fmt.Errorf("store: checking the integrity: %w", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: checking the integrity: %w", err)
	}
	if len(lines) == 1 && lines[0] == "ok" {
		return nil, nil
	}
	return lines, nil
}

// ForeignKeyViolation is a row that refers to a row that does not exist.
type ForeignKeyViolation struct {
	// Table holds the row, and Parent is the table it refers to.
	Table, Parent string
	// Rowid is the rowid of the row.
	Rowid int64
}

// ForeignKeyCheck returns the rows that break a foreign key (PRAGMA
// foreign_key_check): none in a database that only this program wrote,
// whose connections all enforce them.
func (q *Queries) ForeignKeyCheck(ctx context.Context) (violations []ForeignKeyViolation, err error) {
	rows, err := q.db.QueryContext(ctx, pragmaForeignKeyCheck)
	if err != nil {
		return nil, fmt.Errorf("store: checking the foreign keys: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("store: checking the foreign keys: %w", cerr))
		}
	}()
	for rows.Next() {
		var (
			v     ForeignKeyViolation
			rowid sql.NullInt64 // null in a table without rowid; the schema has none
			fkid  int64
		)
		if err := rows.Scan(&v.Table, &rowid, &v.Parent, &fkid); err != nil {
			return nil, fmt.Errorf("store: checking the foreign keys: %w", err)
		}
		v.Rowid = rowid.Int64
		violations = append(violations, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: checking the foreign keys: %w", err)
	}
	return violations, nil
}
