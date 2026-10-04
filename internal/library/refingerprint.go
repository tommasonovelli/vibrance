package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"vibrance/internal/store"
)

// A fingerprint is compared only with those of the same ffmpeg (DESIGN.md
// §5.4). When the server runs another ffmpeg than the one that computed the
// fingerprint of a track, the fingerprint is computed again from the file
// the track has now and written on the same row, so that the id of the
// track does not depend on the tool (§6.6).
//
// The work is the fingerprints goroutine of the scanner. It starts whenever
// an available row has another fp_version than the current one, also after
// the whole index was done once: a row that comes back unchanged from the
// trash still has the fingerprint it had. It reads one file at a time, it
// can be stopped at any moment and started again, and what it has written
// is not done again.

// stalePage is how many rows the job reads from the index at a time: it
// never holds the list of all the tracks.
const stalePage = 100

// fingerprints runs a pass of the job after every cycle of the scanner
// that went through the library, until ctx ends.
func (s *Scanner) fingerprints(ctx context.Context) {
	// The files the current ffmpeg cannot fingerprint, by the id of their
	// track and their SHA-256: they are not read again at every cycle while
	// they stay the same, like the albums that failed (§6.2).
	failed := map[string]string{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stale:
		}
		done, err := s.refingerprint(ctx, failed)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			s.log.Error("computing the fingerprints again failed", "code", "internal", "err", err.Error())
		case done > 0:
			s.log.Info("fingerprints computed again", "tracks", done, "version", s.ix.media.Version())
		}
	}
}

// refingerprint is one pass: every available row whose fingerprint is of
// another ffmpeg gets the fingerprint of its file, and when none is left
// meta.ffmpeg_version says so. done is how many rows were written.
func (s *Scanner) refingerprint(ctx context.Context, failed map[string]string) (done int, err error) {
	version := s.ix.media.Version()
	left := 0
	for after := int64(0); ; {
		var rows []store.ListStaleFingerprintsRow
		err := s.ix.store.Read(ctx, func(q *store.Queries) (err error) {
			rows, err = q.ListStaleFingerprints(ctx, store.ListStaleFingerprintsParams{FpVersion: version, AfterSeq: after, PageSize: stalePage})
			return err
		})
		if err != nil {
			return done, fmt.Errorf("listing the fingerprints of another ffmpeg: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			after = row.Seq
			if failed[row.ID] == row.FileSha256 {
				left++
				continue
			}
			written, err := s.refingerprintTrack(ctx, row)
			switch {
			case ctx.Err() != nil:
				return done, ctx.Err()
			case Code(err) == CodeFingerprintFailed:
				failed[row.ID] = row.FileSha256
				s.log.Warn("the fingerprint of a track cannot be computed again",
					"code", CodeFingerprintFailed, "track_id", row.ID, "err", err.Error())
				left++
			case err != nil:
				return done, err
			case written:
				done++
			default:
				left++
			}
		}
	}
	if left > 0 {
		return done, nil
	}
	return done, s.recordFFmpegVersion(ctx, version)
}

// refingerprintTrack computes the fingerprint of the file of one row and
// writes it on the row. written is false, with no error, when the row is
// left to the scanner: its file is not there, or is not the file the row
// describes (another size or another time of modification: MusicLib wrote
// the album again), or the row changed while the file was read. An error
// with CodeFingerprintFailed is a failure of ffmpeg on the file; any other
// is a failure of the database, or the end of ctx.
func (s *Scanner) refingerprintTrack(ctx context.Context, row store.ListStaleFingerprintsRow) (written bool, err error) {
	f, err := s.ix.root.Open(row.AlbumRelPath + "/" + row.RelPath)
	if err != nil {
		return false, nil
	}
	info, err := f.Stat()
	if err != nil || info.Size() != row.FileSize || info.ModTime().UnixNano() != row.FileMtimeNs {
		return false, f.Close()
	}
	fingerprint, version, err := s.ix.media.Fingerprint(ctx, f, containerOf(row.RelPath))
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return false, &Error{Code: CodeFingerprintFailed, Msg: fmt.Sprintf("%q: %v", row.AlbumRelPath+"/"+row.RelPath, err)}
	}
	others := store.ListOccurrencesParams{AlbumID: row.AlbumID, Fingerprint: fingerprint, ID: row.ID}
	set := store.SetTrackFingerprintParams{
		Fingerprint: fingerprint, FpVersion: version, Occurrence: row.Occurrence, UpdatedAt: s.ix.now().UnixMilli(),
		ID: row.ID, OldFingerprint: row.Fingerprint, OldFpVersion: row.FpVersion,
		FileSize: row.FileSize, FileMtimeNs: row.FileMtimeNs,
	}
	err = s.ix.store.WithWriteTx(ctx, func(q *store.Queries) error { return commitFingerprint(ctx, q, others, set, &written) })
	return written && err == nil, err
}

// recordFFmpegVersion writes meta.ffmpeg_version when it is not version:
// every fingerprint of the available tracks is of that ffmpeg now.
func (s *Scanner) recordFFmpegVersion(ctx context.Context, version string) error {
	recorded := ""
	err := s.ix.store.Read(ctx, func(q *store.Queries) (err error) {
		if recorded, err = q.GetMeta(ctx, metaFFmpegVersion); errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("reading %s: %w", metaFFmpegVersion, err)
	}
	if recorded == version {
		return nil
	}
	return s.ix.store.WithWriteTx(ctx, func(q *store.Queries) error { return commitMeta(ctx, q, metaFFmpegVersion, version) })
}
