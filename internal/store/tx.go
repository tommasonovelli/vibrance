package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// WithWriteTx runs fn in a write transaction and commits it if fn returns
// nil. Any error of fn, a panic in it, or the end of ctx rolls everything
// back. A nil error always means that the commit is done, and an error
// that nothing was committed.
//
// The error of fn is returned as it is while ctx lasts. Once ctx is over,
// every error says so, whichever step it comes from: errors.Is(err,
// ctx.Err()) holds, and the cause is still in the chain (see endOf).
//
// There is one write connection, and the transaction takes SQLite's write
// lock when it begins (BEGIN IMMEDIATE): write transactions run one at a
// time, in this process and across processes, and a second one waits for
// the first. So fn must be short and must do no I/O: no process, no file,
// no network (DESIGN.md I11). It must not start another transaction of
// this store either, which would wait forever for the connection fn
// holds.
func (s *Store) WithWriteTx(ctx context.Context, fn func(q *Queries) error) (err error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning a write transaction: %w", endOf(ctx, err))
	}
	committed := false
	// Deferred, so that a panic in fn does not leave the transaction open
	// on the only write connection.
	defer func() {
		if committed {
			return
		}
		if rbErr := rollback(tx); rbErr != nil {
			err = errors.Join(err, rbErr)
		}
	}()
	if err := fn(New(tx)); err != nil {
		return endOf(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing a write transaction: %w", endOf(ctx, err))
	}
	committed = true
	return nil
}

// Read runs fn in a read transaction: every query of fn sees the same
// committed state of the database, whatever is written in the meantime.
// Readers never wait for the writer, nor the writer for them (WAL). The
// queries of fn cannot write. The errors are those of WithWriteTx: the
// error of fn as it is while ctx lasts, and one that says that ctx ended
// once it is over.
func (s *Store) Read(ctx context.Context, fn func(q *Queries) error) (err error) {
	tx, err := s.read.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning a read transaction: %w", endOf(ctx, err))
	}
	// A read transaction has nothing to commit.
	defer func() {
		if rbErr := rollback(tx); rbErr != nil {
			err = errors.Join(err, rbErr)
		}
	}()
	if err := fn(New(tx)); err != nil {
		return endOf(ctx, err)
	}
	return nil
}

// endOf returns err, which is not nil, with the error of ctx in front of
// it when ctx is over. The caller of a transaction must be able to tell
// the end of its context from a failure of the database, and what
// database/sql and the driver answer then depends on timing. When ctx
// ends, database/sql rolls the transaction back from a goroutine of its
// own: a statement or a commit that meets it returns the error of ctx or
// sql.ErrTxDone, whichever of the two goroutines came first. A BEGIN that
// the end of ctx interrupts fails with SQLITE_INTERRUPT, and one that
// waited for the write lock of another process with SQLITE_BUSY. err
// stays in the chain: nothing is hidden.
func endOf(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
		return fmt.Errorf("%w: %w", ctxErr, err)
	}
	return err
}

// rollback ends tx without committing it. A transaction that is over
// already is not an error: database/sql rolls it back itself when its
// context ends, and the driver does when a commit fails.
func rollback(tx *sql.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("store: rolling back a transaction: %w", err)
	}
	return nil
}
