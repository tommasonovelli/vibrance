package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"vibrance/internal/search"
	"vibrance/internal/store"
)

// The codes of the doctor (DESIGN.md §11.4): CodeDoctorFailed when the
// inspection could not be completed, and one per kind of finding.
const (
	CodeDoctorFailed        = "doctor_failed"
	CodeDoctorIntegrity     = "doctor_integrity"
	CodeDoctorForeignKey    = "doctor_foreign_key"
	CodeDoctorSearchIndex   = "doctor_search_index"
	CodeDoctorSearchExtra   = "doctor_search_extra"
	CodeDoctorSearchMissing = "doctor_search_missing"
	CodeDoctorSearchStale   = "doctor_search_stale"
	CodeDoctorAlbumCounters = "doctor_album_counters"
	CodeDoctorNoAdmin       = "doctor_no_admin"
)

// The advice of the findings. Doctor repairs nothing, and nothing in
// Vibrance repairs these: the database is written only by the server, so
// each of them is a fault of the server or of the disk.
const (
	adviceDamage = "the database file is damaged: stop the server, keep the file for investigation, " +
		"and restore the latest backup that vibrance doctor finds sound into a new state volume"
	adviceSearch = "searches that meet this row fail or miss it; it is a fault of the index, not of the library: " +
		"keep the database for investigation and restore the latest backup that vibrance doctor finds sound"
	adviceCounters = "the lists show a wrong number of tracks or a wrong duration for this album until the scanner indexes it again"
	adviceNoAdmin  = "nobody can administer the server: run vibrance user create --role admin, " +
		"or vibrance user reset-password for a disabled admin and enable it again over the API"
)

// Finding is one problem the doctor found.
type Finding struct {
	Code string
	// Entity is what it concerns: a table, a row or an id.
	Entity string
	// Message says what is wrong, and Advice what to do.
	Message, Advice string
}

// Doctor inspects the database in stateDir and returns what is wrong with
// it: nothing when it is sound (DESIGN.md §11.4). It only reads, and the
// server may be running. The checks run in one read transaction, so that
// they judge one state of the database. A refusal (a missing database, a
// schema this binary does not read) and a failure of the inspection are an
// *Error.
func Doctor(ctx context.Context, stateDir string) ([]Finding, error) {
	path := filepath.Join(stateDir, databaseFile)
	if err := checkCurrentDatabase(ctx, path); err != nil {
		return nil, err
	}
	return Inspect(ctx, path)
}

// Inspect is the inspection of Doctor on the database file at path, which
// must be at the schema of this binary. It is also how the tests judge the
// copy in a backup.
func Inspect(ctx context.Context, path string) (findings []Finding, err error) {
	r, err := store.OpenReader(ctx, path, false)
	if err != nil {
		return nil, storeRefusal(err)
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			err = errors.Join(err, failure(CodeDoctorFailed, "closing the database", adviceDoctorFailed, cerr))
		}
	}()
	err = r.Read(ctx, func(q *store.Queries) error {
		var err error
		findings, err = inspect(ctx, q)
		return err
	})
	if err != nil {
		return nil, failure(CodeDoctorFailed, "the inspection could not be completed; nothing was changed", adviceDoctorFailed, err)
	}
	return findings, nil
}

const adviceDoctorFailed = "fix the cause named in the log and run vibrance doctor again"

func inspect(ctx context.Context, q *store.Queries) ([]Finding, error) {
	var findings []Finding
	damage, err := q.IntegrityCheck(ctx)
	if err != nil {
		return nil, err
	}
	for _, line := range damage {
		findings = append(findings, Finding{Code: CodeDoctorIntegrity, Entity: "database", Message: line, Advice: adviceDamage})
	}
	violations, err := q.ForeignKeyCheck(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range violations {
		findings = append(findings, Finding{Code: CodeDoctorForeignKey, Entity: fmt.Sprintf("%s rowid %d", v.Table, v.Rowid),
			Message: "the row refers to a row of " + v.Parent + " that does not exist", Advice: adviceDamage})
	}
	faults, err := search.Check(ctx, q.Conn())
	if err != nil {
		return nil, err
	}
	for _, f := range faults {
		findings = append(findings, searchFinding(f))
	}
	counters, err := q.ListAlbumsWithWrongCounters(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range counters {
		findings = append(findings, Finding{Code: CodeDoctorAlbumCounters, Entity: "album " + a.ID, Message: fmt.Sprintf(
			"track_count %d and duration_ms %d, but its available tracks are %d and last %d ms",
			a.TrackCount, a.DurationMs, a.AvailableTracks, a.AvailableDurationMs), Advice: adviceCounters})
	}
	admins, err := q.CountEnabledAdmins(ctx)
	if err != nil {
		return nil, err
	}
	if admins == 0 {
		findings = append(findings, Finding{Code: CodeDoctorNoAdmin, Entity: "users",
			Message: "no admin is enabled", Advice: adviceNoAdmin})
	}
	return findings, nil
}

func searchFinding(f search.Fault) Finding {
	entity := fmt.Sprintf("%s rowid %d", f.Table, f.Rowid)
	if f.ID != "" {
		entity += " (" + f.ID + ")"
	}
	switch f.Kind {
	case search.FaultIndex:
		return Finding{Code: CodeDoctorSearchIndex, Entity: f.Table, Message: f.Detail, Advice: adviceSearch}
	case search.FaultExtra:
		return Finding{Code: CodeDoctorSearchExtra, Entity: entity, Message: "the full-text row describes nothing that is available", Advice: adviceSearch}
	case search.FaultMissing:
		return Finding{Code: CodeDoctorSearchMissing, Entity: entity, Message: "it is available and has no full-text row", Advice: adviceSearch}
	default:
		return Finding{Code: CodeDoctorSearchStale, Entity: entity, Message: "the full-text row holds other names than the index", Advice: adviceSearch}
	}
}
