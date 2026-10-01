package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// readMeta reads one value in a read transaction; a key never set is "".
func readMeta(t *testing.T, s *Store, key string) string {
	t.Helper()
	var value string
	err := s.Read(t.Context(), func(q *Queries) (err error) {
		value, err = q.GetMeta(t.Context(), key)
		return err
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reading meta %q: %v", key, err)
	}
	return value
}

func writeMeta(t *testing.T, s *Store, key, value string) {
	t.Helper()
	if err := s.WithWriteTx(t.Context(), func(q *Queries) error {
		return q.SetMeta(t.Context(), SetMetaParams{Key: key, Value: value})
	}); err != nil {
		t.Fatalf("writing meta %q: %v", key, err)
	}
}

// bump adds one to a counter kept in meta: it reads it, then writes it.
func bump(ctx context.Context, q *Queries, key string) error {
	n := 0
	switch value, err := q.GetMeta(ctx, key); {
	case err == nil:
		if n, err = strconv.Atoi(value); err != nil {
			return err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	return q.SetMeta(ctx, SetMetaParams{Key: key, Value: strconv.Itoa(n + 1)})
}

// increment bumps a counter in a write transaction of its own.
func increment(ctx context.Context, s *Store, key string) error {
	return s.WithWriteTx(ctx, func(q *Queries) error { return bump(ctx, q, key) })
}

// A write transaction commits when fn returns nil, and what fn wrote is
// visible to fn before that.
func TestWithWriteTxCommits(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	err := s.WithWriteTx(ctx, func(q *Queries) error {
		if err := q.SetMeta(ctx, SetMetaParams{Key: "a", Value: "1"}); err != nil {
			return err
		}
		if err := q.SetMeta(ctx, SetMetaParams{Key: "b", Value: "2"}); err != nil {
			return err
		}
		got, err := q.GetMeta(ctx, "a")
		if err != nil || got != "1" {
			t.Errorf("inside the transaction a = %q, %v", got, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if a, b := readMeta(t, s, "a"), readMeta(t, s, "b"); a != "1" || b != "2" {
		t.Fatalf("after the commit a = %q, b = %q", a, b)
	}
}

// An error of fn rolls back everything fn wrote, and comes back as it is.
func TestWithWriteTxRollsBackOnError(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	writeMeta(t, s, "kept", "old")

	boom := errors.New("boom")
	err := s.WithWriteTx(ctx, func(q *Queries) error {
		if err := q.SetMeta(ctx, SetMetaParams{Key: "kept", Value: "new"}); err != nil {
			return err
		}
		if err := q.SetMeta(ctx, SetMetaParams{Key: "added", Value: "x"}); err != nil {
			return err
		}
		return boom
	})
	if err != boom {
		t.Fatalf("WithWriteTx returned %v, want the error of fn itself", err)
	}
	if kept, added := readMeta(t, s, "kept"), readMeta(t, s, "added"); kept != "old" || added != "" {
		t.Fatalf("after the rollback kept = %q, added = %q", kept, added)
	}

	// A failed statement is an error of fn like any other: the statements
	// before it are rolled back with it.
	f := &fixture{t: t, db: s.write}
	session := f.valid("sessions")
	session["user_id"] = uuid(424242)
	err = s.WithWriteTx(ctx, func(q *Queries) error {
		if err := q.SetMeta(ctx, SetMetaParams{Key: "added", Value: "x"}); err != nil {
			return err
		}
		return insertRow(ctx, q.db, "sessions", session)
	})
	if sqliteCode(err) != sqliteConstraintFK {
		t.Fatalf("WithWriteTx returned %v, want the foreign key failure", err)
	}
	if added := readMeta(t, s, "added"); added != "" {
		t.Fatalf("after the failed statement added = %q", added)
	}
	// The write connection is free again.
	writeMeta(t, s, "kept", "newer")
}

// A panic in fn rolls the transaction back and frees the only write
// connection: the next transaction does not wait forever.
func TestWithWriteTxRollsBackOnPanic(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("recovered %v, want the panic of fn", r)
			}
		}()
		_ = s.WithWriteTx(ctx, func(q *Queries) error {
			if err := q.SetMeta(ctx, SetMetaParams{Key: "k", Value: "from the panic"}); err != nil {
				t.Error(err)
			}
			panic("boom")
		})
		t.Error("WithWriteTx returned after a panic in fn")
	}()

	if err := increment(ctx, s, "n"); err != nil {
		t.Fatalf("the write transaction after the panic: %v", err)
	}
	if got := readMeta(t, s, "k"); got != "" {
		t.Fatalf("the transaction that panicked left k = %q", got)
	}
}

// awaitRollback returns once database/sql has ended the transaction of q,
// whose context is over: from then on a statement with a context of its
// own fails with sql.ErrTxDone.
func awaitRollback(t *testing.T, q *Queries) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := q.GetMeta(t.Context(), "cancelled")
		if errors.Is(err, sql.ErrTxDone) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the transaction is still open after the end of its context: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

// The end of the context stops a write transaction wherever it is: before
// it begins, while fn runs, or between fn and the commit. Nothing is
// committed, the error says that the context ended, and the write
// connection is free afterwards.
func TestWithWriteTxContext(t *testing.T) {
	s := newStore(t)

	t.Run("cancelled before", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		called := false
		err := s.WithWriteTx(ctx, func(*Queries) error { called = true; return nil })
		if !errors.Is(err, context.Canceled) || called {
			t.Fatalf("WithWriteTx returned %v (fn called: %v), want context.Canceled without calling fn", err, called)
		}
	})

	t.Run("cancelled in fn, which fails", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		err := s.WithWriteTx(ctx, func(q *Queries) error {
			if err := q.SetMeta(ctx, SetMetaParams{Key: "cancelled", Value: "1"}); err != nil {
				return err
			}
			cancel()
			return ctx.Err()
		})
		if err != context.Canceled {
			t.Fatalf("WithWriteTx returned %v, want context.Canceled as fn returned it", err)
		}
	})

	// The context ends in fn, which returns nil all the same. database/sql
	// rolls the transaction back from a goroutine of its own, and its
	// Commit answers with the error of the context or with sql.ErrTxDone,
	// whichever came first. The caller must see the end of its context
	// both ways.
	t.Run("cancelled in fn, which succeeds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		err := s.WithWriteTx(ctx, func(q *Queries) error {
			if err := q.SetMeta(ctx, SetMetaParams{Key: "cancelled", Value: "2"}); err != nil {
				return err
			}
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "committing") {
			t.Fatalf("WithWriteTx returned %v, want the failure of the commit with context.Canceled", err)
		}
	})

	t.Run("cancelled in fn, which succeeds after the rollback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		err := s.WithWriteTx(ctx, func(q *Queries) error {
			if err := q.SetMeta(ctx, SetMetaParams{Key: "cancelled", Value: "3"}); err != nil {
				return err
			}
			cancel()
			awaitRollback(t, q)
			return nil
		})
		if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "committing") {
			t.Fatalf("WithWriteTx returned %v, want the failure of the commit with context.Canceled", err)
		}
		// The test did take the other way through Commit.
		if !errors.Is(err, sql.ErrTxDone) {
			t.Fatalf("WithWriteTx returned %v, which does not keep sql.ErrTxDone", err)
		}
	})

	// fn runs no statement with the context, so that the test does not
	// depend on how long the deadline leaves it.
	t.Run("deadline in fn, which succeeds after the rollback", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		err := s.WithWriteTx(ctx, func(q *Queries) error {
			<-ctx.Done()
			awaitRollback(t, q)
			return nil
		})
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "committing") {
			t.Fatalf("WithWriteTx returned %v, want the failure of the commit with context.DeadlineExceeded", err)
		}
	})

	// What fn returns once the context is over says that it ended too. A
	// statement of fn that meets the rollback of database/sql fails with
	// sql.ErrTxDone when its own check of the context came just before the
	// end; here its context is another one, to get there every time.
	t.Run("cancelled in fn, whose statement fails after the rollback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		err := s.WithWriteTx(ctx, func(q *Queries) error {
			if err := q.SetMeta(ctx, SetMetaParams{Key: "cancelled", Value: "4"}); err != nil {
				return err
			}
			cancel()
			awaitRollback(t, q)
			return q.SetMeta(t.Context(), SetMetaParams{Key: "cancelled", Value: "4"})
		})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, sql.ErrTxDone) {
			t.Fatalf("WithWriteTx returned %v, want context.Canceled in front of sql.ErrTxDone", err)
		}
	})

	t.Run("cancelled in fn, which fails for another reason", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		boom := errors.New("boom")
		err := s.WithWriteTx(ctx, func(q *Queries) error {
			if err := q.SetMeta(ctx, SetMetaParams{Key: "cancelled", Value: "5"}); err != nil {
				return err
			}
			cancel()
			return boom
		})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, boom) {
			t.Fatalf("WithWriteTx returned %v, want context.Canceled in front of the error of fn", err)
		}
	})

	t.Run("deadline while waiting for the writer", func(t *testing.T) {
		inFn, release := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- s.WithWriteTx(t.Context(), func(*Queries) error {
				close(inFn)
				<-release
				return nil
			})
		}()
		<-inFn
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		err := s.WithWriteTx(ctx, func(*Queries) error {
			t.Error("fn ran while another write transaction was open")
			return nil
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("WithWriteTx returned %v, want context.DeadlineExceeded", err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})

	if got := readMeta(t, s, "cancelled"); got != "" {
		t.Fatalf("a cancelled transaction committed: cancelled = %q", got)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := increment(ctx, s, "n"); err != nil {
		t.Fatalf("the write transaction after the cancelled ones: %v", err)
	}
}

// SQLite itself waits for the write lock of another process
// (busy_timeout), and the end of the context does not cut that wait short:
// the transaction fails with SQLITE_BUSY when the timeout is over. The
// error still says that the context ended, and keeps the cause.
func TestWithWriteTxContextEndsWaitingForAnotherProcess(t *testing.T) {
	path := dbPath(t)
	server := openStore(t, path)
	defer closeStore(t, server)
	command := openStore(t, path)
	defer closeStore(t, command)

	inFn, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- server.WithWriteTx(t.Context(), func(*Queries) error {
			close(inFn)
			<-release
			return nil
		})
	}()
	<-inFn

	// The deadline falls well inside the wait, which lasts 5 seconds.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err := command.WithWriteTx(ctx, func(*Queries) error {
		t.Error("fn ran while another process held the write lock")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "beginning") {
		t.Errorf("WithWriteTx returned %v, want the failure of the begin with context.DeadlineExceeded", err)
	}
	if sqliteCode(err) != sqliteBusy {
		t.Errorf("WithWriteTx returned %v (SQLite code %d), which does not keep SQLITE_BUSY", err, sqliteCode(err))
	}
	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
}

// The end of the context ends a read transaction too, and what fn returns
// afterwards says so. A read transaction that fn ended without an error
// has no error: there is nothing to commit.
func TestReadContext(t *testing.T) {
	s := newStore(t)
	writeMeta(t, s, "k", "v")

	t.Run("cancelled in fn, whose statement fails after the rollback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		err := s.Read(ctx, func(q *Queries) error {
			if _, err := q.GetMeta(ctx, "k"); err != nil {
				return err
			}
			cancel()
			awaitRollback(t, q)
			_, err := q.GetMeta(t.Context(), "k")
			return err
		})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, sql.ErrTxDone) {
			t.Fatalf("Read returned %v, want context.Canceled in front of sql.ErrTxDone", err)
		}
	})

	t.Run("cancelled in fn, which fails for another reason", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		boom := errors.New("boom")
		err := s.Read(ctx, func(*Queries) error {
			cancel()
			return boom
		})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, boom) {
			t.Fatalf("Read returned %v, want context.Canceled in front of the error of fn", err)
		}
	})

	t.Run("cancelled in fn, which succeeds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var got string
		err := s.Read(ctx, func(q *Queries) (err error) {
			got, err = q.GetMeta(ctx, "k")
			cancel()
			awaitRollback(t, q)
			return err
		})
		if err != nil || got != "v" {
			t.Fatalf("Read returned %v after reading %q, want no error and v", err, got)
		}
	})

	if got := readMeta(t, s, "k"); got != "v" {
		t.Fatalf("the read transaction after the cancelled ones read %q", got)
	}
}

// The context of a transaction may end at any moment of it, and what
// database/sql and SQLite answer then depends on which goroutine came
// first. Whenever it ends, the error says so; a write transaction with an
// error committed nothing, and one without an error committed.
func TestTransactionsWhileTheContextEnds(t *testing.T) {
	s := newStore(t)
	writeMeta(t, s, "n", "0")
	const transactions = 2000
	// The deadlines are spread from no time at all to past the end of a
	// transaction, and denser where it begins.
	deadline := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(t.Context(), rand.N(rand.N(time.Millisecond)+1))
	}

	committed := 0
	for range transactions {
		ctx, cancel := deadline()
		err := increment(ctx, s, "n")
		cancel()
		switch {
		case err == nil:
			committed++
		case !errors.Is(err, context.DeadlineExceeded):
			t.Fatalf("a write transaction returned %v, want context.DeadlineExceeded", err)
		}
	}
	want := strconv.Itoa(committed)
	if got := readMeta(t, s, "n"); got != want {
		t.Fatalf("the counter is %s after %s write transactions without an error", got, want)
	}

	for range transactions {
		ctx, cancel := deadline()
		err := s.Read(ctx, func(q *Queries) error {
			got, err := q.GetMeta(ctx, "n")
			if err == nil && got != want {
				t.Errorf("a read transaction read %s, want %s", got, want)
			}
			return err
		})
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a read transaction returned %v, want context.DeadlineExceeded", err)
		}
	}
}

// endOf puts the error of a context that is over in front of any other
// error, once, and leaves everything else as it is.
func TestEndOf(t *testing.T) {
	boom := errors.New("boom")
	live := t.Context()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer cancel()

	if got := endOf(live, boom); got != boom {
		t.Errorf("with a live context: %v, want the error itself", got)
	}
	for _, c := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"cancelled", cancelled, context.Canceled},
		{"expired", expired, context.DeadlineExceeded},
	} {
		got := endOf(c.ctx, boom)
		if !errors.Is(got, c.want) || !errors.Is(got, boom) {
			t.Errorf("%s: %v, want %v and the cause", c.name, got, c.want)
		}
		if want := c.want.Error() + ": boom"; got.Error() != want {
			t.Errorf("%s: message %q, want %q", c.name, got, want)
		}
		// An error that says it already is not said twice.
		wrapped := fmt.Errorf("query: %w", c.want)
		if got := endOf(c.ctx, wrapped); got != wrapped {
			t.Errorf("%s: %v, want the error itself", c.name, got)
		}
	}
}

// A read transaction sees one state of the database from its first query
// to its last, whatever is committed in the meantime; the next one sees
// the new state. The error of fn comes back as it is.
func TestReadSeesOneSnapshot(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	writeMeta(t, s, "k", "before")

	err := s.Read(ctx, func(q *Queries) error {
		first, err := q.GetMeta(ctx, "k")
		if err != nil {
			return err
		}
		// A write in the middle, on the write connection.
		writeMeta(t, s, "k", "after")
		writeMeta(t, s, "new", "x")
		second, err := q.GetMeta(ctx, "k")
		if err != nil {
			return err
		}
		if first != "before" || second != "before" {
			t.Errorf("the read transaction saw %q then %q, want before both times", first, second)
		}
		if _, err := q.GetMeta(ctx, "new"); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("the read transaction saw a row committed after it began: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readMeta(t, s, "k"); got != "after" {
		t.Fatalf("the next read transaction saw %q, want after", got)
	}

	boom := errors.New("boom")
	if err := s.Read(ctx, func(*Queries) error { return boom }); err != boom {
		t.Fatalf("Read returned %v, want the error of fn itself", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Read(cancelled, func(*Queries) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read with a cancelled context returned %v", err)
	}
	// A panic in fn ends the read transaction: its connection is not
	// left holding a snapshot.
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("recovered %v, want the panic of fn", r)
			}
		}()
		_ = s.Read(ctx, func(q *Queries) error {
			if _, err := q.GetMeta(ctx, "k"); err != nil {
				t.Error(err)
			}
			panic("boom")
		})
	}()
	if got := s.read.Stats().InUse; got != 0 {
		t.Fatalf("%d read connections still in use", got)
	}
}

// Readers do not wait for a long write transaction and never get
// SQLITE_BUSY (WAL): while it is open they see the state before it, on as
// many connections as there are readers, and after the commit the new
// state.
func TestReadersDuringALongWrite(t *testing.T) {
	s := newStore(t)
	writeMeta(t, s, "k", "before")
	// A reader that waited for the writer would wait forever: bound it.
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	inFn, release := make(chan struct{}), make(chan struct{})
	written := make(chan error, 1)
	go func() {
		written <- s.WithWriteTx(ctx, func(q *Queries) error {
			if err := q.SetMeta(ctx, SetMetaParams{Key: "k", Value: "after"}); err != nil {
				return err
			}
			if err := q.SetMeta(ctx, SetMetaParams{Key: "new", Value: "x"}); err != nil {
				return err
			}
			close(inFn)
			<-release
			return nil
		})
	}()
	<-inFn

	const readers, reads = 8, 40
	var (
		wg      sync.WaitGroup
		holding sync.WaitGroup
	)
	holding.Add(readers)
	errs := make(chan error, readers)
	for range readers {
		wg.Go(func() {
			// Each reader first holds a read transaction until all of them
			// do: the readers need as many connections as they are.
			held := false
			for range reads {
				err := s.Read(ctx, func(q *Queries) error {
					value, err := q.GetMeta(ctx, "k")
					if err != nil {
						return err
					}
					if value != "before" {
						return errors.New("a reader saw the uncommitted value " + value)
					}
					if _, err := q.GetMeta(ctx, "new"); !errors.Is(err, sql.ErrNoRows) {
						return errors.Join(errors.New("a reader saw an uncommitted row"), err)
					}
					if !held {
						held = true
						holding.Done()
						holding.Wait()
					}
					return nil
				})
				if err != nil {
					if !held {
						holding.Done()
					}
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a reader during the write: %v (SQLite code %d)", err, sqliteCode(err))
	}

	select {
	case err := <-written:
		t.Fatalf("the write transaction ended before it was released: %v", err)
	default:
	}
	close(release)
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if k, added := readMeta(t, s, "k"), readMeta(t, s, "new"); k != "after" || added != "x" {
		t.Fatalf("after the commit k = %q, new = %q", k, added)
	}
}

// Two writers are serialized: write transactions of one store never
// overlap, each sees what the one before it committed, and none fails.
func TestWritersAreSerialized(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	const writers, writes = 8, 25
	var (
		wg      sync.WaitGroup
		inside  atomic.Int32
		overlap atomic.Int32
	)
	errs := make(chan error, writers)
	for range writers {
		wg.Go(func() {
			for range writes {
				err := s.WithWriteTx(ctx, func(q *Queries) error {
					if inside.Add(1) != 1 {
						overlap.Add(1)
					}
					defer inside.Add(-1)
					return bump(ctx, q, "n")
				})
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a writer: %v (SQLite code %d)", err, sqliteCode(err))
	}
	if n := overlap.Load(); n != 0 {
		t.Errorf("%d write transactions ran while another was open", n)
	}
	if got, want := readMeta(t, s, "n"), strconv.Itoa(writers*writes); got != want {
		t.Fatalf("the counter is %s after %s increments: updates were lost", got, want)
	}
}

// DESIGN.md T3: the same holds between processes, which share only the
// file: a subcommand may write while the server runs. Each write
// transaction takes the write lock when it begins, so one that reads and
// then writes never finds that the other process wrote in between
// (SQLITE_BUSY at once, whatever the busy timeout): it waits its turn.
func TestWritersOfTwoProcessesAreSerialized(t *testing.T) {
	path := dbPath(t)
	server := openStore(t, path)
	defer closeStore(t, server)
	command := openStore(t, path)
	defer closeStore(t, command)
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()

	const writes = 100
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, s := range []*Store{server, command, server, command} {
		wg.Go(func() {
			for range writes {
				if err := increment(ctx, s, "n"); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a writer: %v (SQLite code %d)", err, sqliteCode(err))
	}
	if got, want := readMeta(t, server, "n"), strconv.Itoa(4*writes); got != want {
		t.Fatalf("the counter is %s after %s increments: updates were lost", got, want)
	}
	if got := readMeta(t, command, "n"); got != strconv.Itoa(4*writes) {
		t.Fatalf("the other process reads %s", got)
	}
}

// A writer of another process waits for the write lock (busy_timeout)
// instead of failing at once, and a reader of another process does not
// wait at all.
func TestWriterOfAnotherProcessWaits(t *testing.T) {
	path := dbPath(t)
	server := openStore(t, path)
	defer closeStore(t, server)
	command := openStore(t, path)
	defer closeStore(t, command)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	writeMeta(t, server, "k", "before")

	inFn, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- server.WithWriteTx(ctx, func(q *Queries) error {
			if err := q.SetMeta(ctx, SetMetaParams{Key: "k", Value: "server"}); err != nil {
				return err
			}
			close(inFn)
			<-release
			return nil
		})
	}()
	<-inFn

	if got := readMeta(t, command, "k"); got != "before" {
		t.Fatalf("the other process reads %q during the write, want before", got)
	}
	waited := make(chan error, 1)
	var sawServer atomic.Bool
	go func() {
		waited <- command.WithWriteTx(ctx, func(q *Queries) error {
			value, err := q.GetMeta(ctx, "k")
			sawServer.Store(value == "server")
			if err != nil {
				return err
			}
			return q.SetMeta(ctx, SetMetaParams{Key: "k", Value: "command"})
		})
	}()
	// The lock is held well past the moment the other writer asks for it.
	select {
	case err := <-waited:
		t.Fatalf("the second writer did not wait for the first: %v (SQLite code %d)", err, sqliteCode(err))
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if err := <-waited; err != nil {
		t.Fatalf("the writer that waited: %v (SQLite code %d)", err, sqliteCode(err))
	}
	if !sawServer.Load() {
		t.Fatal("the writer that waited did not see what the first committed")
	}
	if got := readMeta(t, server, "k"); got != "command" {
		t.Fatalf("k = %q, want command", got)
	}
}
