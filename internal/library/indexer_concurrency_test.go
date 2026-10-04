package library

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"vibrance/internal/store"
)

// copyAlbum copies the album folder from to the folder to of the same
// library, as another album: the same files under another album_id.
func copyAlbum(t *testing.T, dir, from, to, albumID string) {
	t.Helper()
	if err := os.CopyFS(inLib(dir, to), os.DirFS(inLib(dir, from))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(inLib(dir, to), ReceiptName)
	receipt, err := ParseReceipt(readFile(t, path))
	if err != nil {
		t.Fatal(err)
	}
	receipt.AlbumID = albumID
	if err := os.WriteFile(path, encodeReceipt(receipt), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Several albums are indexed at once, each in its own transaction, two of
// them of the same artist: the index is the one that indexing them one
// after the other gives.
func TestIndexAlbumsInParallel(t *testing.T) {
	e := newIndexEnv(t)
	const copyB, copyID = "Bravo Tones/Beta Copy", "01a0f459-ebc8-7081-991c-2b1c9331e157"
	const copyF, copyFID = "Foxtrot Twins/Phi Copy", "01a0f459-ec9d-7dc9-825a-074f90185add"
	copyAlbum(t, e.dir, albumB, copyB, copyID)
	copyAlbum(t, e.dir, albumF, copyF, copyFID)
	albums := append([]string{copyB, copyF}, fixtureOrder...)

	indexAll := func() {
		t.Helper()
		var wg sync.WaitGroup
		errs := make([]error, len(albums))
		for i, rel := range albums {
			c := e.candidate(rel)
			wg.Go(func() {
				warnings, err := e.ix.IndexAlbum(t.Context(), c)
				if err == nil && len(warnings) != 0 {
					err = fmt.Errorf("warnings: %v", warnings)
				}
				errs[i] = err
			})
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("IndexAlbum(%q): %v", albums[i], err)
			}
		}
	}
	indexAll()

	if p, f := e.media.probes.Load(), e.media.fingerprints.Load(); p != 18 || f != 18 {
		t.Errorf("%d probes and %d fingerprints for 18 new tracks", p, f)
	}
	for _, want := range fixtureIndex {
		id := fixtureAlbums[want.rel]
		a := e.album(id)
		rows := e.tracks(id)
		if a.Album.Title != want.title || a.ArtistName != want.artist || a.Album.TrackCount != int64(len(want.tracks)) || len(rows) != len(want.tracks) {
			t.Fatalf("%s: %+v with %d rows", want.rel, a, len(rows))
		}
		for i, w := range want.tracks {
			if rows[i].RelPath != w.path || rows[i].Title != w.title || rows[i].Fingerprint != w.fingerprint || rows[i].Occurrence != w.occurrence {
				t.Errorf("%s/%s: %+v", want.rel, w.path, rows[i])
			}
		}
	}
	// One artist for the two albums that have it, found once.
	b, c := e.album(fixtureAlbums[albumB]), e.album(copyID)
	if b.Album.ArtistID != c.Album.ArtistID || c.Album.TrackCount != 2 || c.Album.RelPath != copyB {
		t.Fatalf("the two albums of one artist: %+v and %+v", b, c)
	}
	if got := e.match(matchArtists, "Bravo Tones"); len(got) != 1 {
		t.Fatalf("searching the artist of two albums finds the rows %v", got)
	}
	if got := e.match(matchAlbums, "Bravo Tones"); len(got) != 2 {
		t.Fatalf("searching the albums of the artist finds the rows %v", got)
	}
	dump := e.dump()
	for table, want := range map[string]int{"artists ": 6, "albums ": 8, "tracks ": 18, "search_artists ": 6, "search_albums ": 8, "search_tracks ": 18} {
		got := 0
		for _, line := range strings.Split(dump, "\n") {
			if strings.HasPrefix(line, table) {
				got++
			}
		}
		if got != want {
			t.Errorf("%d rows in %s, want %d", got, table, want)
		}
	}

	// Again, at once: nothing runs and nothing changes.
	e.media.reset()
	indexAll()
	if n := e.media.runs(); n != 0 {
		t.Errorf("indexing the same albums again ran %d processes", n)
	}
	e.wantUnchanged(dump)
}

// The same album indexed by several goroutines at once is written once:
// each indexing either writes it, finds it written, or gives up because the
// rows changed under it. No track is ever there twice.
func TestIndexAlbumSameAlbumAtOnce(t *testing.T) {
	e := newIndexEnv(t)
	c := e.candidate(albumA)
	const workers = 6
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := range workers {
		wg.Go(func() { _, errs[i] = e.ix.IndexAlbum(t.Context(), c) })
	}
	wg.Wait()
	done := 0
	for _, err := range errs {
		switch {
		case err == nil:
			done++
		case Code(err) != CodeAlbumChanged:
			t.Errorf("IndexAlbum: %v", err)
		}
	}
	if done == 0 {
		t.Fatal("no indexing wrote the album")
	}
	rows := e.tracks(fixtureAlbums[albumA])
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3", len(rows))
	}
	if a := e.album(fixtureAlbums[albumA]).Album; a.TrackCount != 3 || a.DurationMs != 6000 {
		t.Fatalf("the album: %+v", a)
	}
	before := e.dump()
	e.index(albumA)
	e.wantUnchanged(before)
}

// What a reader may see of album A while the test below replaces it again
// and again: all of one version or all of the other, in the tables and in
// the full-text tables at once.
const albumState = `
SELECT albums.title, albums.track_count, albums.duration_ms, albums.album_revision,
    (SELECT count(*) FROM tracks WHERE tracks.album_id = albums.id AND tracks.available = 1),
    (SELECT count(*) FROM tracks WHERE tracks.album_id = albums.id),
    (SELECT coalesce(sum(tracks.duration_ms), 0) FROM tracks WHERE tracks.album_id = albums.id AND tracks.available = 1),
    (SELECT count(*) FROM tracks WHERE tracks.album_id = albums.id AND tracks.available = 1 AND tracks.title LIKE '%(second)'),
    (SELECT count(*) FROM search_tracks JOIN tracks ON tracks.seq = search_tracks.rowid
        WHERE tracks.album_id = albums.id AND tracks.available = 1
          AND search_tracks.title = tracks.title AND search_tracks.album = albums.title),
    (SELECT count(*) FROM search_tracks JOIN tracks ON tracks.seq = search_tracks.rowid WHERE tracks.album_id = albums.id),
    (SELECT count(*) FROM search_albums WHERE search_albums.rowid = albums.seq AND search_albums.title = albums.title)
FROM albums WHERE albums.id = ?`

type albumView struct {
	title                                      string
	trackCount, durationMS, revision           int64
	available, rows, duration, second          int64
	searchTracks, searchTrackRows, searchAlbum int64
}

// whole reports whether the view is one of the two versions of the album,
// complete.
func (v albumView) whole() bool {
	first := albumView{"Alpha: Light?", 3, 6000, v.revision, 3, 3, 6000, 0, 3, 3, 1}
	second := albumView{"Alpha (second)", 2, 4000, v.revision, 2, 3, 4000, 2, 2, 2, 1}
	return v == first || v == second
}

// A reader never sees half an album: while an album is replaced by another
// version of it, with another title, other track titles and a track less,
// every read transaction sees one of the two versions whole, in the rows,
// in the counters and in the full-text tables.
func TestIndexAlbumReaderNeverSeesHalfAnAlbum(t *testing.T) {
	e := newIndexEnv(t)
	id := fixtureAlbums[albumA]
	// The second version of the album, next to the library, and a place
	// for the one that is not in the library.
	second := filepath.Join(filepath.Dir(e.dir), "second")
	if err := os.CopyFS(second, os.DirFS(inLib(e.dir, albumA))); err != nil {
		t.Fatal(err)
	}
	spare := filepath.Join(filepath.Dir(e.dir), "spare")
	e.index(albumA)
	first := e.tracks(id)

	swap := func() {
		rename(t, inLib(e.dir, albumA), spare)
		rename(t, second, inLib(e.dir, albumA))
		second, spare = spare, second
	}
	swap()
	remove(t, e.inAlbum(albumA, "03 - Third_.flac"))
	for _, f := range []string{"01 - First Light.flac", "02 - Second Wave.flac"} {
		retag(t, e.inAlbum(albumA, f), "album=Alpha (second)", "title="+strings.TrimSuffix(f[5:], ".flac")+" (second)")
	}
	rerender(t, e.dir, albumA)

	ctx, cancel := context.WithCancel(t.Context())
	var readers sync.WaitGroup
	seen := make([]map[int64]int, 3)
	for r := range seen {
		seen[r] = map[int64]int{}
		readers.Go(func() {
			for ctx.Err() == nil {
				var v albumView
				err := e.store.Read(ctx, func(q *store.Queries) error {
					return q.Conn().QueryRowContext(ctx, albumState, id).Scan(&v.title, &v.trackCount, &v.durationMS, &v.revision,
						&v.available, &v.rows, &v.duration, &v.second, &v.searchTracks, &v.searchTrackRows, &v.searchAlbum)
				})
				if err != nil {
					if ctx.Err() == nil {
						t.Errorf("reading the album: %v", err)
					}
					return
				}
				if !v.whole() {
					t.Errorf("a reader saw half an album: %+v", v)
					return
				}
				seen[r][v.trackCount]++
			}
		})
	}

	// The album goes back and forth between its two versions.
	for range 8 {
		e.index(albumA)
		swap()
	}
	cancel()
	readers.Wait()
	// The last version indexed is the first one: the three tracks are the
	// rows they were at the start.
	last := e.tracks(id)
	for i := range first {
		if len(last) != 3 || last[i].ID != first[i].ID || last[i].Title != first[i].Title || last[i].Available != 1 {
			t.Fatalf("the rows after the last round: %+v", last)
		}
	}
	for r, counts := range seen {
		if counts[2]+counts[3] == 0 {
			t.Errorf("reader %d saw nothing", r)
		}
	}
}

// The write transactions of the package do no I/O (I11): everything that
// runs inside one is in commit.go, which has no way to the disk or to a
// process. The test reads the sources: every write transaction of the
// package has a function that only calls one function of commit.go, and
// each of those is the function of exactly one transaction: the indexing of
// an album, and those of the scanner. commit.go imports nothing that
// reaches outside the database, and names neither the indexer, nor the
// scanner, nor the Root, nor the media adapter.
func TestTheWriteTransactionDoesNoIO(t *testing.T) {
	allowedImports := map[string]bool{
		"bytes": true, "context": true, "database/sql": true, "errors": true, "fmt": true, "reflect": true, "slices": true,
		"vibrance/internal/search": true, "vibrance/internal/store": true,
	}
	forbiddenNames := map[string]bool{
		"Indexer": true, "Scanner": true, "Root": true, "Media": true, "CoverWarmer": true, "media": true, "os": true,
		"OpenRoot": true, "Lstat": true, "Open": true, "ReadReceipt": true, "Probe": true, "Fingerprint": true, "Warm": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	// The functions of the write transactions, and how many transactions
	// call each.
	transactions := map[string]int{"commitAlbum": 0, "commitAbsent": 0, "commitReferences": 0, "commitFingerprint": 0, "commitMeta": 0, "commitSortKeys": 0}
	declared := map[string]bool{}
	sawCommit := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if name == "commit.go" {
			sawCommit = true
			for _, imp := range file.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if !allowedImports[path] {
					t.Errorf("commit.go imports %q: the code of the write transaction reaches only the database", path)
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && forbiddenNames[id.Name] {
					t.Errorf("%s: commit.go names %s: the write transaction does no I/O", fset.Position(id.Pos()), id.Name)
				}
				if fn, ok := n.(*ast.FuncDecl); ok {
					declared[fn.Name.Name] = true
				}
				return true
			})
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WithWriteTx" {
				return true
			}
			callee := onlyCall(call)
			if _, ok := transactions[callee]; !ok {
				t.Errorf("%s: the function of a write transaction must only return a call of a function of commit.go, not %q",
					fset.Position(call.Pos()), callee)
			}
			transactions[callee]++
			return true
		})
	}
	if !sawCommit {
		t.Fatal("commit.go was not checked")
	}
	for name, n := range transactions {
		if !declared[name] {
			t.Errorf("%s is not a function of commit.go", name)
		}
		if n != 1 {
			t.Errorf("%d write transactions call %s, want 1", n, name)
		}
	}
}

// onlyCall returns name when the function literal given to WithWriteTx is
// `func(q) error { return name(...) }`, and "" otherwise.
func onlyCall(withWriteTx *ast.CallExpr) string {
	if len(withWriteTx.Args) != 2 {
		return ""
	}
	fn, ok := withWriteTx.Args[1].(*ast.FuncLit)
	if !ok || len(fn.Body.List) != 1 {
		return ""
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return ""
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok {
		return ""
	}
	callee, ok := call.Fun.(*ast.Ident)
	if !ok {
		return ""
	}
	return callee.Name
}

// The planner's error for a file without a fingerprint cannot come from
// IndexAlbum, which examines every file F1 does not pair; and a file that
// was neither examined nor paired is refused before anything is written.
func TestBuildWriteRefusesAFileThatWasNotExamined(t *testing.T) {
	c := Candidate{RelPath: albumB, Receipt: albumReceipt(fixtureAlbums[albumB], 1)}
	tracks := []trackFile{{audio: AudioFile{ReceiptFile: c.Receipt.Files[0]}}}
	_, _, err := buildWrite(c, snapshot{}, tracks, Plan{Inserts: []Insert{{New: 0, Occurrence: 1}}}, coverData{}, clockStart)
	if err == nil || Code(err) != "" || !strings.Contains(err.Error(), "was not examined") {
		t.Fatalf("error %v", err)
	}
}
