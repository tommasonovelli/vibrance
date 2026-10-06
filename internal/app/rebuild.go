package app

import (
	"context"
	"log/slog"
	"path/filepath"

	"vibrance/internal/search"
	"vibrance/internal/store"
)

// CodeRebuildSearchFailed: the full-text tables could not be made again.
// Nothing changed: the rebuild is one transaction.
const CodeRebuildSearchFailed = "rebuild_search_failed"

const adviceRebuildFailed = "nothing was changed: the rebuild is one transaction. Stop the server if it runs, " +
	"fix the cause named in the log and run vibrance rebuild-search again"

// RebuildSearch makes the full-text tables of the database in stateDir
// again from the index of the library (search.Rebuild), in one write
// transaction: it is the repair of what the doctor finds wrong in the search
// (DESIGN.md §11.4). It refuses what the doctor refuses, a missing database
// and a schema this binary does not read, and never migrates.
//
// The server may be running: it finds the old tables until the transaction
// commits. Its own writes wait for it, and fail if it lasts longer than
// they wait, so the operator is told to stop the server first. The returned
// error is an *Error.
func RebuildSearch(ctx context.Context, stateDir string, log *slog.Logger) error {
	path := filepath.Join(stateDir, databaseFile)
	if err := checkCurrentDatabase(ctx, path); err != nil {
		return err
	}
	// The database is at the schema of this binary: opening it applies no
	// migration.
	st, err := store.Open(ctx, path)
	if err != nil {
		return storeRefusal(err)
	}
	err = st.WithWriteTx(ctx, func(q *store.Queries) error {
		return search.Rebuild(ctx, q.Conn())
	})
	if cerr := st.Close(); cerr != nil {
		// What the command did is committed, or rolled back. This is only
		// the write-ahead log, which could not be emptied because the server
		// is reading: the server does it when it stops.
		log.Warn(cerr.Error(), "code", store.Code(cerr))
	}
	if err != nil {
		return failure(CodeRebuildSearchFailed, "the search index could not be rebuilt", adviceRebuildFailed, err)
	}
	return nil
}
