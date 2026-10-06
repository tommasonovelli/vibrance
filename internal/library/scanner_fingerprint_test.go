package library

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"vibrance/internal/media"
	"vibrance/internal/names"
	"vibrance/internal/store"
)

// otherFFmpeg is the version of an ffmpeg that is not the one of the image.
const otherFFmpeg = "7.0-old"

// asAnotherFFmpegLeftIt rewrites the index as if another ffmpeg had computed
// every fingerprint, each different from what the current one computes, and
// as if that version were the one the server remembers.
func (e *scanEnv) asAnotherFFmpegLeftIt() {
	e.t.Helper()
	// The two tracks of album F have one audio: with an ffmpeg that gave
	// them two fingerprints each is the first occurrence of its own.
	e.write(`UPDATE tracks SET fp_version = ?, fingerprint = 'old-' || seq, occurrence = 1`, otherFFmpeg)
	e.write(`INSERT INTO meta ("key", value) VALUES ('ffmpeg_version', ?) ON CONFLICT ("key") DO UPDATE SET value = excluded.value`, otherFFmpeg)
}

// wantFixtureFingerprints checks that every track of the fixture has the
// fingerprint and the occurrence of the current ffmpeg, on the row it had.
func (e *scanEnv) wantFixtureFingerprints(ids map[string]map[string]string) {
	e.t.Helper()
	for _, want := range fixtureIndex {
		id := fixtureAlbums[want.rel]
		rows := e.tracks(id)
		if len(rows) != len(want.tracks) {
			e.t.Fatalf("%s has %d rows, want %d", want.rel, len(rows), len(want.tracks))
		}
		for i, w := range want.tracks {
			row := rows[i]
			if row.ID != ids[id][w.path] || row.RelPath != w.path || row.Fingerprint != w.fingerprint ||
				row.FpVersion != media.PinnedVersion || row.Occurrence != w.occurrence || row.Available != 1 || row.Title != w.title {
				e.t.Errorf("%s/%s after the fingerprints were computed again: %+v", want.rel, w.path, row)
			}
		}
	}
}

func (e *scanEnv) fixtureIDs() map[string]map[string]string {
	e.t.Helper()
	ids := map[string]map[string]string{}
	for _, id := range fixtureAlbums {
		ids[id] = e.ids(id)
	}
	return ids
}

// When the server runs another ffmpeg than the one that computed the
// fingerprints, each available track gets the fingerprint of its file on
// the row it has: no id changes, no row is added. Two rows that the new
// ffmpeg gives one fingerprint take two occurrences. At the end the server
// remembers the version, and a second pass has nothing to do.
func TestRefingerprintAfterAnotherFFmpeg(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	ids := e.fixtureIDs()
	e.asAnotherFFmpegLeftIt()
	e.media.reset()

	done, err := e.sc.refingerprint(t.Context(), map[string]string{})
	if err != nil || done != 14 {
		t.Fatalf("refingerprint: %d rows, %v; want 14", done, err)
	}
	e.wantFixtureFingerprints(ids)
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v: the fingerprints are written on the rows there are", got)
	}
	if p, f := e.media.probes.Load(), e.media.fingerprints.Load(); p != 0 || f != 14 {
		t.Fatalf("%d probes and %d fingerprints, want only the 14 fingerprints", p, f)
	}
	if got := e.meta(metaFFmpegVersion); got != media.PinnedVersion {
		t.Fatalf("meta.ffmpeg_version is %q, want %q", got, media.PinnedVersion)
	}

	before := e.dumpAll()
	e.media.reset()
	if done, err := e.sc.refingerprint(t.Context(), map[string]string{}); err != nil || done != 0 {
		t.Fatalf("a second pass: %d rows, %v", done, err)
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("a second pass ran %d processes", n)
	}
	e.wantAllUnchanged(before)
	// The scanner finds every album as it was indexed.
	wantEvent(t, e.scan(), map[string]any{"indexed": 0, "absent": 0})
	e.wantAllUnchanged(before)
}

// The job starts by itself after a cycle of the scanner that runs as in the
// server, and the playlists and favorites of the users still point to the
// same tracks.
func TestRefingerprintRunsAfterACycle(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	ids := e.fixtureIDs()
	user := e.user("ann")
	one := e.trackAt(fixtureAlbums[albumB], "01 - One.mp3").ID
	playlist := e.playlist(user, "Mine", one)
	e.favorite(user, one, 50)
	items := e.items(playlist)
	e.asAnotherFFmpegLeftIt()

	e.run(e.scanner(testWorkers, time.Hour))
	eventually(t, "the job has gone through the index", func() bool {
		return len(e.logs.events(t, "fingerprints computed again")) == 1 && e.meta(metaFFmpegVersion) == media.PinnedVersion
	})
	e.wantFixtureFingerprints(ids)
	e.wantItems(playlist, items, one)
	e.wantRevision(playlist, 1)
	e.wantFavorites(user, one+"@50")
	done := e.logs.events(t, "fingerprints computed again")
	if len(done) != 1 || done[0]["tracks"] != float64(14) || done[0]["version"] != media.PinnedVersion {
		t.Fatalf("the log of the job: %v", done)
	}
}

// The job does not wait for meta.ffmpeg_version to be old: a row that is
// available with a fingerprint of another ffmpeg is computed again whenever
// it is found, as a track that comes back from the trash unchanged long
// after the job ended. A row that is not available is left as it is.
func TestRefingerprintARowThatCameBack(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	if done, err := e.sc.refingerprint(t.Context(), map[string]string{}); err != nil || done != 0 {
		t.Fatalf("a pass on a current index: %d rows, %v", done, err)
	}
	if got := e.meta(metaFFmpegVersion); got != media.PinnedVersion {
		t.Fatalf("meta.ffmpeg_version is %q after a pass on a new index", got)
	}
	idB, idC := fixtureAlbums[albumB], fixtureAlbums[albumC]
	ids := e.fixtureIDs()
	// B and C were indexed by another ffmpeg, and C is in the trash.
	e.write(`UPDATE tracks SET fp_version = ?, fingerprint = 'old-' || seq WHERE album_id IN (?, ?)`, otherFFmpeg, idB, idC)
	trash := e.trash(albumC)
	e.scan()
	e.media.reset()

	if done, err := e.sc.refingerprint(t.Context(), map[string]string{}); err != nil || done != 2 {
		t.Fatalf("refingerprint: %d rows, %v; want the two tracks of B", done, err)
	}
	for _, row := range e.tracks(idC) {
		if row.Available != 0 || row.FpVersion != otherFFmpeg || !strings.HasPrefix(row.Fingerprint, "old-") {
			t.Errorf("a row that is not available was computed again: %+v", row)
		}
	}
	if f := e.media.fingerprints.Load(); f != 2 {
		t.Fatalf("%d fingerprints, want 2", f)
	}

	// The album comes back with the bytes it had: F1 pairs its rows, which
	// keep the fingerprint of the old ffmpeg, and the job takes them.
	rename(t, trash, inLib(e.dir, albumC))
	e.scan()
	for _, row := range e.tracks(idC) {
		if row.Available != 1 || row.FpVersion != otherFFmpeg {
			t.Fatalf("a row that came back by its bytes: %+v", row)
		}
	}
	if done, err := e.sc.refingerprint(t.Context(), map[string]string{}); err != nil || done != 2 {
		t.Fatalf("refingerprint after the album came back: %d rows, %v", done, err)
	}
	e.wantFixtureFingerprints(ids)
}

// The new pair (fingerprint, occurrence) may belong to another row of the
// album, added while the old fingerprint was not comparable: the row takes
// the lowest free occurrence.
func TestRefingerprintTakesAFreeOccurrence(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumF]
	first, second := e.trackAt(id, "01 - Same Audio.flac"), e.trackAt(id, "02 - Same Audio Again.flac")
	real := first.Fingerprint
	// The first row has the real fingerprint with occurrences 1 and 3 taken
	// by rows that are not on disk; the second row has an old fingerprint
	// with occurrence 1.
	e.write(`UPDATE tracks SET fp_version = ?, fingerprint = 'old', occurrence = 1 WHERE id = ?`, otherFFmpeg, second.ID)
	e.write(`INSERT INTO tracks (id, album_id, fingerprint, fp_version, occurrence, disc, "no", title, artist, rel_path,
			file_size, file_mtime_ns, file_sha256, codec, sample_rate, channels, available, updated_at)
		SELECT ?, album_id, fingerprint, fp_version, 3, disc, 9, 'Gone', artist, 'gone.flac',
			1, 1, 'x', codec, sample_rate, channels, 0, 1 FROM tracks WHERE id = ?`, newID(t), first.ID)

	if done, err := e.sc.refingerprint(t.Context(), map[string]string{}); err != nil || done != 1 {
		t.Fatalf("refingerprint: %d rows, %v", done, err)
	}
	got := e.trackAt(id, "02 - Same Audio Again.flac")
	if got.ID != second.ID || got.Fingerprint != real || got.FpVersion != media.PinnedVersion || got.Occurrence != 2 || got.UpdatedAt <= second.UpdatedAt {
		t.Fatalf("the row: %+v, want the fingerprint %s and the occurrence 2", got, real)
	}
	if kept := e.trackAt(id, "01 - Same Audio.flac"); !reflect.DeepEqual(kept, first) {
		t.Fatalf("the other row changed: %+v", kept)
	}
	if n := len(e.tracks(id)); n != 3 {
		t.Fatalf("%d rows, want 3", n)
	}
}

// A row whose file is not the file it describes is left to the scanner:
// MusicLib wrote the album again. The job does not write it, and does not
// say that the whole index is of the current ffmpeg.
func TestRefingerprintSkipsAFileThatChanged(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumB]
	e.asAnotherFFmpegLeftIt()
	one, two := e.trackAt(id, "01 - One.mp3"), e.trackAt(id, "02 - Two.mp3")
	// Another time of modification for one, another size for the other,
	// and a file that is gone.
	e.write(`UPDATE tracks SET file_mtime_ns = file_mtime_ns + 1 WHERE id = ?`, one.ID)
	e.write(`UPDATE tracks SET file_size = file_size + 1 WHERE id = ?`, two.ID)
	gone := e.trackAt(fixtureAlbums[albumC], "01 - One.m4a")
	remove(t, e.inAlbum(albumC, "01 - One.m4a"))
	// And a row that is not available, although its file is there: only
	// the scanner says what is available, and the job leaves the row alone.
	unavailable := e.trackAt(fixtureAlbums[albumD], "01 - One.m4a")
	e.write(`UPDATE tracks SET available = 0 WHERE id = ?`, unavailable.ID)
	unavailable.Available = 0
	e.media.reset()

	if done, err := e.sc.refingerprint(t.Context(), map[string]string{}); err != nil || done != 10 {
		t.Fatalf("refingerprint: %d rows, %v; want the 10 others", done, err)
	}
	if got := e.track(unavailable.ID); !reflect.DeepEqual(got, unavailable) {
		t.Fatalf("the row that is not available was written: %+v", got)
	}
	for _, old := range []store.Track{one, two, gone} {
		got := e.track(old.ID)
		if got.Fingerprint != old.Fingerprint || got.FpVersion != otherFFmpeg || got.UpdatedAt != old.UpdatedAt {
			t.Errorf("a row whose file changed was written: %+v", got)
		}
	}
	if f := e.media.fingerprints.Load(); f != 10 {
		t.Fatalf("%d fingerprints, want 10: a file that changed is not read", f)
	}
	if got := e.meta(metaFFmpegVersion); got != otherFFmpeg {
		t.Fatalf("meta.ffmpeg_version is %q with rows still to do", got)
	}
}

// A row that the scanner writes while its file is being read is not
// written by the job with what it read before.
func TestRefingerprintGivesUpARowThatChanged(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	id := fixtureAlbums[albumD]
	e.write(`UPDATE tracks SET fp_version = ?, fingerprint = 'old-' || seq WHERE album_id = ?`, otherFFmpeg, id)
	one := e.trackAt(id, "01 - One.m4a")
	// While the first file is read, an indexing gives its row another
	// fingerprint.
	e.media.setHook(func(kind string, n int64) {
		if kind == "fingerprint" && n == 1 {
			e.write(`UPDATE tracks SET fingerprint = 'written meanwhile' WHERE id = ?`, one.ID)
		}
	})
	e.media.reset()

	if done, err := e.sc.refingerprint(t.Context(), map[string]string{}); err != nil || done != 1 {
		t.Fatalf("refingerprint: %d rows, %v; want only the second track", done, err)
	}
	if got := e.track(one.ID); got.Fingerprint != "written meanwhile" || got.FpVersion != otherFFmpeg {
		t.Fatalf("the row that changed meanwhile: %+v", got)
	}
	if got := e.meta(metaFFmpegVersion); got == media.PinnedVersion {
		t.Fatal("meta.ffmpeg_version is current with a row still to do")
	}
}

// failingFingerprints is the media adapter with an ffmpeg that fails.
type failingFingerprints struct{ Media }

func (failingFingerprints) Fingerprint(context.Context, *os.File, media.Container) (string, string, error) {
	return "", "", errors.New("ffmpeg failed")
}

// A file the current ffmpeg cannot fingerprint is not read again at every
// cycle while it stays the same; its row stays as it is, with its id, and
// the failure is logged without what the tool said.
func TestRefingerprintDoesNotRepeatAFailure(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	before := e.fixtureIDs()
	e.asAnotherFFmpegLeftIt()
	stale := e.dumpAll()
	working := e.media.tools
	e.media.tools = failingFingerprints{working}
	e.media.reset()

	failed := map[string]string{}
	if done, err := e.sc.refingerprint(t.Context(), failed); err != nil || done != 0 {
		t.Fatalf("refingerprint: %d rows, %v", done, err)
	}
	if f := e.media.fingerprints.Load(); f != 14 || len(failed) != 14 {
		t.Fatalf("%d fingerprints and %d failures remembered, want 14 and 14", f, len(failed))
	}
	e.wantAllUnchanged(stale)
	warnings := e.logs.events(t, "the fingerprint of a track cannot be computed again")
	if len(warnings) != 14 || warnings[0]["level"] != "WARN" || warnings[0]["code"] != CodeFingerprintFailed {
		t.Fatalf("the log of the failures: %v", warnings)
	}

	if done, err := e.sc.refingerprint(t.Context(), failed); err != nil || done != 0 {
		t.Fatalf("a second pass: %d rows, %v", done, err)
	}
	if f := e.media.fingerprints.Load(); f != 14 {
		t.Fatalf("%d fingerprints after the second pass: the failures were tried again", f)
	}
	e.wantAllUnchanged(stale)

	// A server that starts again, with an ffmpeg that works, does them.
	e.media.tools = working
	if done, err := e.sc.refingerprint(t.Context(), map[string]string{}); err != nil || done != 14 {
		t.Fatalf("a pass with a working ffmpeg: %d rows, %v", done, err)
	}
	e.wantFixtureFingerprints(before)
}

// The job can be stopped at any moment: what it wrote stays, and the next
// pass does the rest.
func TestRefingerprintStoppedAndStartedAgain(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	ids := e.fixtureIDs()
	e.asAnotherFFmpegLeftIt()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e.media.setHook(func(kind string, n int64) {
		if kind == "fingerprint" && n == 6 {
			cancel()
		}
	})
	e.media.reset()

	done, err := e.sc.refingerprint(ctx, map[string]string{})
	if !errors.Is(err, context.Canceled) || done != 5 {
		t.Fatalf("the pass that was stopped: %d rows, %v", done, err)
	}
	if got := e.meta(metaFFmpegVersion); got != otherFFmpeg {
		t.Fatalf("meta.ffmpeg_version is %q after a pass that was stopped", got)
	}
	e.media.setHook(nil)
	e.media.reset()
	if done, err := e.sc.refingerprint(t.Context(), map[string]string{}); err != nil || done != 9 {
		t.Fatalf("the next pass: %d rows, %v; want the 9 that were left", done, err)
	}
	e.wantFixtureFingerprints(ids)
}

// The job and the cycles run at once in the server: with every row of the
// index to compute again and albums changing on disk, the index ends as the
// library is, with the ids it had and every fingerprint current.
func TestRefingerprintWhileTheScannerWorks(t *testing.T) {
	e := newScanEnv(t)
	e.scan()
	ids := e.fixtureIDs()
	e.asAnotherFFmpegLeftIt()
	sc := e.scanner(testWorkers, time.Hour)
	e.run(sc)

	for i := range 4 {
		retag(t, e.inAlbum(albumA, "01 - First Light.flac"), "comment=round "+string(rune('a'+i)))
		rerender(t, e.dir, albumA)
		sc.Trigger(ReasonRequest)
		time.Sleep(20 * time.Millisecond)
	}
	sc.Trigger(ReasonRequest)
	eventually(t, "every fingerprint is current and the album is indexed", func() bool {
		if e.meta(metaFFmpegVersion) != media.PinnedVersion {
			return false
		}
		receipt, err := ParseReceipt(readFile(t, inLib(e.dir, albumA+"/"+ReceiptName)))
		if err != nil {
			t.Fatal(err)
		}
		if e.album(fixtureAlbums[albumA]).Album.AlbumRevision != receipt.AlbumRevision {
			sc.Trigger(ReasonRequest)
			return false
		}
		for _, id := range fixtureAlbums {
			for _, row := range e.tracks(id) {
				if row.FpVersion != media.PinnedVersion {
					sc.Trigger(ReasonRequest)
					return false
				}
			}
		}
		return true
	})
	e.wantFixtureFingerprints(ids)
	if got := e.rows(); got != (rowCounts{6, 6, 14}) {
		t.Fatalf("rows %+v", got)
	}
}

// The sort keys are computed again, all of them, when they were made with
// another collation than the compiled one, and the database remembers which
// made them; with the current one nothing is written.
func TestEnsureSortKeys(t *testing.T) {
	e := newScanEnv(t)
	// A new database: there is nothing to compute, and the version is
	// recorded.
	if recomputed, err := EnsureSortKeys(t.Context(), e.store); err != nil || recomputed {
		t.Fatalf("EnsureSortKeys on a new database: %v, %v", recomputed, err)
	}
	if got := e.meta(metaCollateVersion); got != names.CollateVersion {
		t.Fatalf("meta.collate_version is %q, want %q", got, names.CollateVersion)
	}
	e.scan()
	whole := e.dumpAll()
	if recomputed, err := EnsureSortKeys(t.Context(), e.store); err != nil || recomputed {
		t.Fatalf("EnsureSortKeys with current keys: %v, %v", recomputed, err)
	}
	e.wantAllUnchanged(whole)

	// The keys as another version of the collation left them.
	e.write(`UPDATE artists SET sort_key = x'00'`)
	e.write(`UPDATE albums SET title_key = x'01', artist_key = x'02'`)
	e.write(`UPDATE tracks SET title_key = x'03', artist_key = x'04', album_key = x'05'`)
	e.write(`UPDATE meta SET value = 'golang.org/x/text v0.1.0' WHERE "key" = 'collate_version'`)
	if recomputed, err := EnsureSortKeys(t.Context(), e.store); err != nil || !recomputed {
		t.Fatalf("EnsureSortKeys after the collation changed: %v, %v", recomputed, err)
	}
	e.wantAllUnchanged(whole)
	for _, want := range fixtureIndex {
		a := e.album(fixtureAlbums[want.rel])
		if !bytes.Equal(a.Album.TitleKey, names.SortKey(want.title)) || !bytes.Equal(a.Album.ArtistKey, names.SortKey(want.artist)) {
			t.Errorf("the keys of %s: %x and %x", want.rel, a.Album.TitleKey, a.Album.ArtistKey)
		}
	}
	if got := e.meta(metaCollateVersion); got != names.CollateVersion {
		t.Fatalf("meta.collate_version is %q", got)
	}

	// An unavailable album has keys too: it may come back.
	e.trash(albumA)
	e.scan()
	e.write(`UPDATE albums SET title_key = x'01', artist_key = x'02'`)
	e.write(`DELETE FROM meta WHERE "key" = 'collate_version'`)
	if recomputed, err := EnsureSortKeys(t.Context(), e.store); err != nil || !recomputed {
		t.Fatalf("EnsureSortKeys without a recorded version: %v, %v", recomputed, err)
	}
	if a := e.album(fixtureAlbums[albumA]); !bytes.Equal(a.Album.TitleKey, names.SortKey("Alpha: Light?")) || !bytes.Equal(a.Album.ArtistKey, names.SortKey("Aurora Sines")) {
		t.Fatalf("the keys of an unavailable album: %+v", a.Album)
	}

	// A context that is over: an error, and nothing half written.
	e.write(`UPDATE meta SET value = 'old' WHERE "key" = 'collate_version'`)
	e.write(`UPDATE artists SET sort_key = x'00'`)
	before := e.dumpAll()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if recomputed, err := EnsureSortKeys(ctx, e.store); !errors.Is(err, context.Canceled) || recomputed {
		t.Fatalf("EnsureSortKeys with a context that is over: %v, %v", recomputed, err)
	}
	e.wantAllUnchanged(before)
	if got := e.meta(metaCollateVersion); got != "old" {
		t.Fatalf("meta.collate_version is %q after a failure", got)
	}
}
