package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"vibrance/internal/api"
	"vibrance/internal/library"
	"vibrance/internal/names"
	"vibrance/internal/search"
	"vibrance/internal/store"
)

// The search (DESIGN.md §10, step S17) over the API, on the fixture library
// indexed by the real indexer. What a text finds and in which order is
// proved in internal/search; here every answer is also checked against the
// specification (I10).

// search asks for a search as a; the answer conforms.
func (w *world) search(query string, a *account) api.SearchResult {
	w.t.Helper()
	rec := w.get("/search?"+query, a)
	wantStatus(w.t, "GET /search?"+query, rec, http.StatusOK)
	return decode[api.SearchResult](w.t, rec)
}

// namesOf are the names of the artists, the titles of the albums and those
// of the tracks of a result, in its order.
func namesOf(r api.SearchResult) (artists, albums, tracks []string) {
	for _, a := range r.Artists {
		artists = append(artists, a.Name)
	}
	for _, a := range r.Albums {
		albums = append(albums, a.Title)
	}
	for _, t := range r.Tracks {
		tracks = append(tracks, t.Title)
	}
	return artists, albums, tracks
}

func TestSearch(t *testing.T) {
	w := newWorld(t, apiOrigin)
	// An empty index: three empty lists.
	if rec := w.get("/search?q=echo", w.anna); strings.TrimSpace(rec.Body.String()) != `{"albums":[],"artists":[],"tracks":[]}` {
		t.Fatalf("an empty index: %d %s", rec.Code, rec.Body)
	}
	w.indexFixture()

	// Accents, case and prefixes: what the album and the artist show
	// elsewhere.
	e := decode[api.AlbumDetail](t, w.get("/albums/"+albumE, w.anna))
	for _, q := range []string{"echo+cafe", "ECHO+CAFÉ", "ech+caf", "%C3%A9cho,caf%C3%A9!", "cafe%20echo"} {
		got := w.search("q="+q, w.anna)
		if len(got.Artists) != 1 || got.Artists[0] != (api.ArtistSummary{Id: e.Artist.Id, Name: "Écho Café", AlbumCount: 1}) {
			t.Fatalf("%s: the artists %+v", q, got.Artists)
		}
		if len(got.Albums) != 1 || got.Albums[0].Id != albumE || got.Albums[0].Title != e.Title || got.Albums[0].TrackCount != 3 ||
			got.Albums[0].Artist != e.Artist {
			t.Fatalf("%s: the albums %+v", q, got.Albums)
		}
		var ids, want []string
		for _, tr := range got.Tracks {
			ids = append(ids, tr.Id)
			if !tr.Available || tr.Favorite || tr.Album.Id != albumE {
				t.Errorf("%s: the track %+v", q, tr)
			}
		}
		for _, tr := range e.Tracks {
			want = append(want, tr.Id)
		}
		slices.Sort(ids)
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Fatalf("%s: the tracks %v, want those of the album %v", q, ids, want)
		}
	}

	// Every word must be there.
	if got := w.search("q=echo+bravo", w.anna); len(got.Artists)+len(got.Albums)+len(got.Tracks) != 0 {
		t.Fatalf("two words of two albums: %+v", got)
	}
	// A track is the one GET /tracks/{id} shows.
	one := w.search("q=second+wave", w.anna)
	if len(one.Tracks) != 1 || len(one.Albums) != 0 || len(one.Artists) != 0 {
		t.Fatalf("one track: %+v", one)
	}
	if alone := decode[api.Track](t, w.get("/tracks/"+one.Tracks[0].Id, w.anna)); mustJSON(t, alone) != mustJSON(t, one.Tracks[0]) {
		t.Fatalf("the track found %+v, and by itself %+v", one.Tracks[0], alone)
	}

	// The favorites are those of the user who asks.
	w.favorite(w.anna, one.Tracks[0].Id)
	for who, want := range map[*account]bool{w.anna: true, w.bob: false, w.admin: false} {
		if got := w.search("q=second+wave", who); len(got.Tracks) != 1 || got.Tracks[0].Favorite != want {
			t.Errorf("as %s: %+v", who.name, got.Tracks)
		}
	}

	// The kinds: those left out are empty lists, never missing keys.
	for types, want := range map[string][3]bool{
		"artist":             {true, false, false},
		"album":              {false, true, false},
		"track":              {false, false, true},
		"track,artist":       {true, false, true},
		"artist,album,track": {true, true, true},
		"album,album":        {false, true, false},
	} {
		rec := w.get("/search?q=echo&types="+types, w.anna)
		got := decode[api.SearchResult](t, rec)
		if (len(got.Artists) != 0) != want[0] || (len(got.Albums) != 0) != want[1] || (len(got.Tracks) != 0) != want[2] {
			t.Errorf("types=%s: %+v", types, got)
		}
		var raw map[string][]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || raw["artists"] == nil || raw["albums"] == nil || raw["tracks"] == nil {
			t.Errorf("types=%s: a list is missing or null: %s", types, rec.Body)
		}
	}
}

func sameTrack(a, b api.Track) bool { return a.Id == b.Id }

// §10.2: q has 1 to 100 characters; a q with nothing to look for finds
// nothing and is no error; nothing in q is an operator (T18).
func TestSearchQueries(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	empty := func(q string) {
		t.Helper()
		got := w.search("q="+url.QueryEscape(q), w.anna)
		if len(got.Artists)+len(got.Albums)+len(got.Tracks) != 0 {
			t.Errorf("%q finds %+v", q, got)
		}
	}
	for _, q := range []string{"!", "?!", " ", `"`, `""`, "*", "-", "()", "'", "%", "\x00", "…", "🎵",
		// Would find the artist, if OR, a column or a prefix mark were read.
		"echo OR zzzz", "name:echo", "title:echo", "zzzz* OR echo*", "NEAR(echo cafe)", "echo NOT cafe", "echo AND cafe",
		`echo"; DROP TABLE search_artists; --`, "echo -zzzz",
		// 100 characters, of one and of four bytes.
		strings.Repeat("a", 100), strings.Repeat("é", 100), strings.Repeat("!", 100), strings.Repeat("𝔘", 100),
		strings.Repeat("z ", 50)} {
		empty(q)
	}
	for _, q := range []string{`"echo"`, "echo*", "^echo", "-echo", "+echo", "(echo)", `"cafe echo"`, "echo\x00cafe",
		strings.Repeat(" ", 96) + "echo"} {
		if got := w.search("q="+url.QueryEscape(q), w.anna); len(got.Artists) != 1 || got.Artists[0].Name != "Écho Café" {
			t.Errorf("%q finds the artists %+v, want Écho Café", q, got.Artists)
		}
	}
	// §10.2 as amended: a combining accent does not end a word, so a q in
	// decomposed form finds what its composed form finds; an accent alone has
	// no token and finds nothing. The 100 characters count q as it is sent,
	// accents included.
	const acute = string(rune(0x301))
	for _, q := range []string{"E" + acute + "cho", "e" + acute + "cho cafe" + acute, "Cafe" + acute + " E" + acute + "ch",
		acute + " echo", strings.Repeat(" ", 95) + "E" + acute + "cho"} {
		if got := w.search("q="+url.QueryEscape(q), w.anna); len(got.Artists) != 1 || got.Artists[0].Name != "Écho Café" {
			t.Errorf("%q finds the artists %+v, want Écho Café", q, got.Artists)
		}
	}
	empty(acute)
	empty(strings.Repeat(acute, 100))
	empty("E" + acute + " cho")
	// §10.2 as amended: q is split only at ASCII punctuation, spaces and
	// controls. Punctuation that is not ASCII stays in its word, which is
	// then a phrase with an order; a word of it alone is left out.
	for _, q := range []string{"«echo»", "écho—café", "echo…caf", "— echo", "echo — café …", "echo cafe", "cafe　echo"} {
		if got := w.search("q="+url.QueryEscape(q), w.anna); len(got.Artists) != 1 || got.Artists[0].Name != "Écho Café" {
			t.Errorf("%q finds the artists %+v, want Écho Café", q, got.Artists)
		}
	}
	empty("café—écho")
	empty("ech—café")
	empty("—")
	empty("— … «»")
	empty(strings.Repeat("—", 100))
	// 100 characters that are 50 letters.
	empty(strings.Repeat("e"+acute, 50))
	wantCode(t, "101 characters, 51 of them letters", w.get("/search?q="+url.QueryEscape(strings.Repeat("e"+acute, 50)+"x"), w.anna), http.StatusBadRequest, "invalid_request")
	// More than eight words: the ninth does not count.
	if got := w.search("q="+url.QueryEscape("echo cafe e c e c e c zzzz"), w.anna); len(got.Artists) != 1 {
		t.Errorf("a ninth word counted: %+v", got)
	}
	if got := w.search("q=echo", w.anna); len(got.Artists) != 1 {
		t.Fatal("the index was changed by a search")
	}

	for path, parameter := range map[string]string{
		"/search":                               "q",
		"/search?q=":                            "q",
		"/search?q=" + strings.Repeat("a", 101): "q",
		"/search?q=" + url.QueryEscape(strings.Repeat("é", 101)): "q",
		"/search?q=" + url.QueryEscape(strings.Repeat(" ", 101)): "q",
		"/search?q=a&q=b":               "q",
		"/search?q=a&limit=0":           "limit",
		"/search?q=a&limit=51":          "limit",
		"/search?q=a&limit=x":           "limit",
		"/search?q=a&types=":            "types",
		"/search?q=a&types=artist,song": "types",
		"/search?q=a&types=artist%20OR": "types",
	} {
		rec := w.get(path, w.anna)
		wantCode(t, path, rec, http.StatusBadRequest, "invalid_request")
		if !strings.Contains(rec.Body.String(), `\"`+parameter+`\"`) {
			t.Errorf("%s: the answer does not name %s: %s", path, parameter, rec.Body)
		}
	}
}

// The erratum R3 to §10.2, over the API: a q in decomposed form finds the
// names of scripts whose marks SQLite's tokenizer does not take away, a kana
// with a separate voiced mark and Greek with a separate breathing, because q
// is put in NFC, the form of the names. The limit of 100 characters still
// counts q as it is sent.
func TestSearchDecomposedQuery(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	// An album as the scanner writes it: its names through names.Normalize,
	// from tags in decomposed form.
	const voiced, breathing, acute = string(rune(0x3099)), string(rune(0x313)), string(rune(0x301))
	artist := names.Normalize("か" + voiced + "くや")
	title := names.Normalize("Α" + breathing + "θη" + acute + "να")
	if artist != "がくや" || title != "Ἀθήνα" {
		t.Fatalf("the names of the index are %+q and %+q", artist, title)
	}
	ctx := t.Context()
	artistID, albumID := names.ArtistID(artist), "0192a5f0-0000-7000-8000-0000000000d1"
	err := w.store.WithWriteTx(ctx, func(q *store.Queries) error {
		return errors.Join(
			q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artistID, Name: artist, SortKey: names.SortKey(artist)}),
			q.UpsertAlbum(ctx, store.UpsertAlbumParams{ID: albumID, ArtistID: artistID, ArtistKey: names.SortKey(artist),
				Title: title, TitleKey: names.SortKey(title), YearKey: 10000, RelPath: "decomposed", AlbumRevision: 1,
				RenderVersion: "r", ReceiptHash: "h", FirstSeenAt: 1, UpdatedAt: 1}),
			search.SyncAlbum(ctx, q.Conn(), albumID), search.SyncArtist(ctx, q.Conn(), artistID))
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"がくや", "か" + voiced + "くや", "か" + voiced, "ἀθήνα", "α" + breathing + "θη" + acute + "να", "α" + breathing + "θ",
		// Both words decomposed, 100 characters as sent.
		strings.Repeat(" ", 90) + "か" + voiced + " α" + breathing + "θη" + acute + "να"} {
		got := w.search("q="+url.QueryEscape(q), w.anna)
		if len(got.Albums) != 1 || got.Albums[0].Title != title || got.Albums[0].Artist.Name != artist {
			t.Errorf("%+q finds the albums %+v, want %s", q, got.Albums, title)
		}
	}
	if got := w.search("q="+url.QueryEscape("か"+voiced+"くや")+"&types=artist", w.anna); len(got.Artists) != 1 || got.Artists[0].Id != artistID {
		t.Errorf("the decomposed name finds the artists %+v", got.Artists)
	}
	wantCode(t, "101 characters as sent", w.get("/search?q="+url.QueryEscape(strings.Repeat(" ", 91)+"か"+voiced+" α"+breathing+"θη"+acute+"να"), w.anna),
		http.StatusBadRequest, "invalid_request")
}

// A full-text row that describes nothing available is a fault of the index:
// the search answers 500 internal, says nothing of it to the client, and
// the log has the cause.
func TestSearchOnAnIndexThatDisagrees(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	// Unavailable without the full-text rows following, which the scanner
	// never does.
	w.unavailable(albumB)
	rec := w.get("/search?q=bravo", w.anna)
	wantCode(t, "a stale full-text row", rec, http.StatusInternalServerError, "internal")
	if body := rec.Body.String(); strings.Contains(body, "rowid") || strings.Contains(body, "full-text") {
		t.Fatalf("the answer tells the cause: %s", body)
	}
	if logs := w.logs.String(); !strings.Contains(logs, "the full-text row describes nothing that is available") {
		t.Fatalf("the log does not have the cause:\n%s", logs)
	}
	// The rest of the index is still searched.
	if got := w.search("q=echo", w.anna); len(got.Artists) != 1 {
		t.Fatalf("another search: %+v", got)
	}
}

// rescan runs one more cycle of the scanner of r and waits for its end.
func rescan(t *testing.T, r *running) {
	t.Helper()
	before := len(eventsOf(t, r.logs, "scan finished"))
	r.s.scanner.Load().Trigger(library.ReasonRequest)
	deadline := time.Now().Add(60 * time.Second)
	for len(eventsOf(t, r.logs, "scan finished")) == before {
		if time.Now().After(deadline) {
			t.Fatalf("no scan finished within 60s; logs:\n%s", r.logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// §10.1, with the real server, scanner and tools: the full-text tables
// follow the library through the four changes of step S17. An album that
// appears is found; a title that changes is found by the new words and not
// by the old; an album that leaves is not found, nor its tracks, nor its
// artist; and all return, with the same ids, when the album does.
func TestSearchFollowsTheScanner(t *testing.T) {
	musiclib := fixtureLibrary(t)
	folderB := filepath.Join(musiclib, "library", "Bravo Tones", "Beta MP3")
	away := filepath.Join(t.TempDir(), "Beta MP3")
	// B is not there at first.
	if err := os.Rename(folderB, away); err != nil {
		t.Fatal(err)
	}
	r := startServer(t, func(s *server) { s.musiclibDir = musiclib })
	waitReady(t, "http://"+r.addr)
	waitScanned(t, r)
	c := newClient(t, r)
	search := func(q string) api.SearchResult {
		t.Helper()
		resp, body := c.get("/search?limit=50&q=" + url.QueryEscape(q))
		var res api.SearchResult
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &res) != nil {
			t.Fatalf("searching %q: %d %s", q, resp.StatusCode, body)
		}
		return res
	}
	wantNames := func(where, q string, artists, albums, tracks []string) api.SearchResult {
		t.Helper()
		res := search(q)
		gotArtists, gotAlbums, gotTracks := namesOf(res)
		slices.Sort(gotTracks)
		if !slices.Equal(gotArtists, artists) || !slices.Equal(gotAlbums, albums) || !slices.Equal(gotTracks, tracks) {
			t.Fatalf("%s, searching %q: artists %q, albums %q, tracks %q; want %q, %q, %q", where, q,
				gotArtists, gotAlbums, gotTracks, artists, albums, tracks)
		}
		return res
	}
	bravo := []string{"Bravo Tones"}
	beta := []string{"Beta MP3"}

	wantNames("before the album is there", "bravo", nil, nil, nil)
	wantNames("before the album is there", "beta", nil, nil, nil)
	if got := search("echo"); len(got.Artists) != 1 {
		t.Fatalf("the albums that are there: %+v", got)
	}

	// It appears.
	if err := os.Rename(away, folderB); err != nil {
		t.Fatal(err)
	}
	rescan(t, r)
	first := wantNames("appeared", "bravo", bravo, beta, []string{"One", "Two"})
	wantNames("appeared", "beta mp3", nil, beta, []string{"One", "Two"})
	wantNames("appeared", "bravo one", nil, nil, []string{"One"})
	ids := map[string]string{}
	for _, tr := range first.Tracks {
		ids[tr.Title] = tr.Id
	}
	if first.Albums[0].Id != albumB {
		t.Fatalf("the album found: %+v", first.Albums[0])
	}

	// A title changes: the new words find the same track, the old do not.
	retag(t, filepath.Join(folderB, "01 - One.mp3"), "Uno Edited")
	rerender(t, folderB)
	rescan(t, r)
	wantNames("retitled", "bravo one", nil, nil, nil)
	changed := wantNames("retitled", "bravo uno", nil, nil, []string{"Uno Edited"})
	if changed.Tracks[0].Id != ids["One"] {
		t.Fatalf("the track has another id after the edit: %s, was %s", changed.Tracks[0].Id, ids["One"])
	}
	wantNames("retitled", "bravo", bravo, beta, []string{"Two", "Uno Edited"})

	// A track leaves: it is not found, the rest is.
	second := readFile(t, filepath.Join(folderB, "02 - Two.mp3"))
	if err := os.Remove(filepath.Join(folderB, "02 - Two.mp3")); err != nil {
		t.Fatal(err)
	}
	rerender(t, folderB)
	rescan(t, r)
	wantNames("a track deleted", "bravo", bravo, beta, []string{"Uno Edited"})
	wantNames("a track deleted", "two bravo", nil, nil, nil)
	// And returns, the same track.
	writeFile(t, filepath.Join(folderB, "02 - Two.mp3"), second)
	rerender(t, folderB)
	rescan(t, r)
	back := wantNames("a track restored", "two bravo", nil, nil, []string{"Two"})
	if back.Tracks[0].Id != ids["Two"] {
		t.Fatalf("the track returned with another id: %s, was %s", back.Tracks[0].Id, ids["Two"])
	}

	// The album leaves, as when MusicLib moves it to its trash.
	if err := os.Rename(folderB, away); err != nil {
		t.Fatal(err)
	}
	rescan(t, r)
	for _, q := range []string{"bravo", "beta", "uno", "bravo two"} {
		wantNames("the album gone", q, nil, nil, nil)
	}
	if resp, _ := c.get("/tracks/" + ids["One"]); resp.StatusCode != http.StatusOK {
		t.Fatalf("the track that is not available: %d", resp.StatusCode)
	}

	// And returns.
	if err := os.Rename(away, folderB); err != nil {
		t.Fatal(err)
	}
	rescan(t, r)
	again := wantNames("the album back", "bravo", bravo, beta, []string{"Two", "Uno Edited"})
	if again.Albums[0].Id != albumB || again.Artists[0].Id != first.Artists[0].Id {
		t.Fatalf("the album back: %+v, %+v", again.Albums, again.Artists)
	}
	for _, tr := range again.Tracks {
		want := ids["Two"]
		if tr.Title == "Uno Edited" {
			want = ids["One"]
		}
		if tr.Id != want || !tr.Available {
			t.Fatalf("the track %q back: %+v, want the id %s", tr.Title, tr, want)
		}
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

// §10.2: the limit counts for each kind, 10 when it is not said and 50 at
// most, and the same search answers the same.
func TestSearchLimits(t *testing.T) {
	w := newWorld(t, apiOrigin)
	ctx := t.Context()
	// 60 artists, each with one album of one track, all with one word.
	err := w.store.WithWriteTx(ctx, func(q *store.Queries) error {
		for i := range 60 {
			artist := fmt.Sprintf("0199a5c0-0000-5000-8000-0000000001%02d", i)
			album := fmt.Sprintf("0199a5c0-0000-7000-8000-0000000002%02d", i)
			name := fmt.Sprintf("Common Artist %d", i)
			key := names.SortKey(name)
			err := errors.Join(
				q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artist, Name: name, SortKey: key}),
				q.UpsertAlbum(ctx, store.UpsertAlbumParams{ID: album, ArtistID: artist, ArtistKey: key, Title: fmt.Sprintf("Common Album %d", i),
					TitleKey: key, YearKey: 10000, RelPath: album, AlbumRevision: 1, RenderVersion: "r", ReceiptHash: "h", FirstSeenAt: 1, UpdatedAt: 1}),
				q.UpsertTrack(ctx, store.UpsertTrackParams{ID: fmt.Sprintf("0199a5c0-0000-7000-8000-0000000003%02d", i), AlbumID: album,
					Fingerprint: "f", FpVersion: "v", Occurrence: 1, Disc: 1, No: 1, Title: fmt.Sprintf("Common Track %d", i), Artist: name,
					RelPath: "01.flac", FileSize: 1, FileMtimeNs: 1, FileSha256: "s", Codec: "flac", SampleRate: 44100, Channels: 2, UpdatedAt: 1}),
				q.UpdateAlbumCounters(ctx, album),
				search.SyncAlbum(ctx, q.Conn(), album),
				search.SyncArtist(ctx, q.Conn(), artist),
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	most := w.search("q=common&limit=50", w.anna)
	if len(most.Artists) != 50 || len(most.Albums) != 50 || len(most.Tracks) != 50 {
		t.Fatalf("limit=50: %d artists, %d albums, %d tracks", len(most.Artists), len(most.Albums), len(most.Tracks))
	}
	sameArtist := func(a, b api.ArtistSummary) bool { return a.Id == b.Id }
	sameAlbum := func(a, b api.AlbumSummary) bool { return a.Id == b.Id }
	for query, n := range map[string]int{"q=common": 10, "q=common&limit=1": 1, "q=common&limit=3": 3, "q=common&limit=50": 50} {
		got := w.search(query, w.bob)
		if !slices.EqualFunc(got.Artists, most.Artists[:n], sameArtist) || !slices.EqualFunc(got.Albums, most.Albums[:n], sameAlbum) ||
			!slices.EqualFunc(got.Tracks, most.Tracks[:n], sameTrack) {
			t.Errorf("%s: %d artists, %d albums, %d tracks; want the first %d of each", query, len(got.Artists), len(got.Albums), len(got.Tracks), n)
		}
	}
	if got := w.search("q=common&limit=2&types=track", w.anna); len(got.Tracks) != 2 || len(got.Albums)+len(got.Artists) != 0 {
		t.Errorf("limit=2&types=track: %+v", got)
	}
}
