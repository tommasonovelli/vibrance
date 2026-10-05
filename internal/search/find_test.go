package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"vibrance/internal/store"
)

// entry is an album a test puts in the index: its artist, its title and
// the titles of its tracks, whose artist is the one of the album.
type entry struct {
	artist, album string
	tracks        []string
}

// testID is the n-th id of a kind ('a' artist, 'b' album, 'c' track).
func testID(kind byte, n int) string {
	return fmt.Sprintf("0192a5f0-0000-7000-8000-%c%011d", kind, n)
}

// index writes the entries, in order, with their full-text rows: the
// albums, the tracks and the artists get growing seqs in the order given.
// An artist of two entries is one artist.
func index(t testing.TB, s *store.Store, entries ...entry) {
	t.Helper()
	ctx := t.Context()
	artists := map[string]string{}
	tracks := 0
	err := s.WithWriteTx(ctx, func(q *store.Queries) error {
		for i, e := range entries {
			artistID, ok := artists[e.artist]
			if !ok {
				artistID = testID('a', len(artists))
				artists[e.artist] = artistID
				if err := q.UpsertArtist(ctx, store.UpsertArtistParams{ID: artistID, Name: e.artist, SortKey: []byte{1}}); err != nil {
					return err
				}
			}
			albumID := testID('b', i)
			a := album(albumID, artistID, e.album)
			a.RelPath = albumID
			if err := q.UpsertAlbum(ctx, a); err != nil {
				return err
			}
			for n, title := range e.tracks {
				if err := q.UpsertTrack(ctx, track(testID('c', tracks), albumID, title, e.artist, int64(n+1))); err != nil {
					return err
				}
				tracks++
			}
			if err := errors.Join(SyncAlbum(ctx, q.Conn(), albumID), SyncArtist(ctx, q.Conn(), artistID)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var everything = Kinds{Artists: true, Albums: true, Tracks: true}

// found are the names of what a search found, in its order.
type found struct {
	artists, albums, tracks []string
}

func findIn(ctx context.Context, s *store.Store, text string, kinds Kinds, limit int) (found, error) {
	var res Results
	err := s.Read(ctx, func(q *store.Queries) (err error) {
		res, err = Find(ctx, q, "", Parse(text), kinds, limit)
		return err
	})
	var n found
	for _, r := range res.Artists {
		n.artists = append(n.artists, r.Artist.Name)
	}
	for _, r := range res.Albums {
		n.albums = append(n.albums, r.Album.Title)
	}
	for _, r := range res.Tracks {
		n.tracks = append(n.tracks, r.Track.Title)
	}
	return n, err
}

func findAll(t *testing.T, s *store.Store, text string) found {
	t.Helper()
	n, err := findIn(t.Context(), s, text, everything, 50)
	if err != nil {
		t.Fatalf("searching %q: %v", text, err)
	}
	return n
}

func list(names ...string) []string { return names }

func wantFound(t *testing.T, text, kind string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("searching %q: the %s %q, want %q", text, kind, got, want)
	}
}

var jazz = []entry{
	{"Beyoncé", "Lemonade", list("Formation", "Hold Up")},
	{"Miles Davis", "Kind of Blue", list("So What", "Blue in Green")},
	{"AC/DC", "Back in Black", list("T.N.T.", "Hells Bells")},
	{"Björk", "Homogenic", list("Jóga")},
}

// §10.2 and the tests of step S17: accents and case do not count, a word
// is found from its beginning, every word must be there, and punctuation
// only separates.
func TestFindWords(t *testing.T) {
	s := newStore(t)
	index(t, s, jazz...)
	for _, c := range []struct {
		text                    string
		artists, albums, tracks []string
	}{
		{"beyonce", list("Beyoncé"), list("Lemonade"), list("Formation", "Hold Up")},
		{"BEYONCÉ", list("Beyoncé"), list("Lemonade"), list("Formation", "Hold Up")},
		// The accent as a combining character of its own, at the end.
		{"beyoncé", list("Beyoncé"), list("Lemonade"), list("Formation", "Hold Up")},
		{"bjork joga", nil, nil, list("Jóga")},
		{"MILES davis", list("Miles Davis"), list("Kind of Blue"), list("So What", "Blue in Green")},
		// While the user types.
		{"m", list("Miles Davis"), list("Kind of Blue"), list("So What", "Blue in Green")},
		{"mil dav", list("Miles Davis"), list("Kind of Blue"), list("So What", "Blue in Green")},
		{"dav mil", list("Miles Davis"), list("Kind of Blue"), list("So What", "Blue in Green")},
		// Not from the middle of a word.
		{"iles", nil, nil, nil},
		// Every word, wherever it is: the title, the artist, the album.
		{"miles blue green", nil, nil, list("Blue in Green")},
		{"miles kind what", nil, nil, list("So What")},
		{"miles lemonade", nil, nil, nil},
		{"miles davis zzz", nil, nil, nil},
		// Punctuation separates and nothing more.
		{"ac/dc", list("AC/DC"), list("Back in Black"), list("Hells Bells", "T.N.T.")},
		{"AC-DC", list("AC/DC"), list("Back in Black"), list("Hells Bells", "T.N.T.")},
		{"acdc", nil, nil, nil},
		{"t.n.t", nil, nil, list("T.N.T.")},
		{"  hells,bells!!  ", nil, nil, list("Hells Bells")},
		{"«so» what?", nil, nil, list("So What")},
		{"¿what? ¡so!", nil, nil, list("So What")},
		// Nothing to search for.
		{"!?", nil, nil, nil},
		{" ", nil, nil, nil},
		{"\"*-", nil, nil, nil},
		{"", nil, nil, nil},
	} {
		got := findAll(t, s, c.text)
		wantFound(t, c.text, "artists", got.artists, c.artists)
		wantFound(t, c.text, "albums", got.albums, c.albums)
		wantFound(t, c.text, "tracks", got.tracks, c.tracks)
	}
}

// T18: nothing a user types is an operator of MATCH. Each of these would
// find something else, or be a syntax error, if FTS5 read it.
func TestFindReadsNoOperator(t *testing.T) {
	s := newStore(t)
	index(t, s,
		entry{"Alpha", "Alpha or Beta", list("One")},
		entry{"Gamma", "Alpha", list("Near Alpha Beta")},
		entry{"Title", "Delta", list("Not")},
	)
	both := list("Alpha", "Alpha or Beta")
	for _, c := range []struct {
		text   string
		albums []string
	}{
		// OR would find both albums; here "or" is a word that must be there.
		{"alpha OR zzz", nil},
		{"alpha OR beta", list("Alpha or Beta")},
		// NOT and - would take the first album away, or leave the second.
		{"alpha NOT beta", nil},
		{"alpha -beta", list("Alpha or Beta")},
		{"alpha AND beta", nil},
		// A phrase would ask for the order of the words.
		{`"beta alpha"`, list("Alpha or Beta")},
		{`"alpha" "`, both},
		// A column filter would find the albums.
		{"title:alpha", nil},
		{"artist:alpha", nil},
		{"{title artist}:alpha", nil},
		{"- title : alpha", nil},
		// The prefix mark and the start mark are punctuation.
		{"alph*", both},
		{"^alpha", both},
		{"al* + be*", list("Alpha or Beta")},
		// NEAR is a word.
		{"NEAR(alpha beta)", nil},
		{"NEAR(alpha beta, 1)", nil},
		// What would not parse.
		{"(alpha", both},
		{"alpha)) OR ((", list("Alpha or Beta")},
		{"alpha AND", nil},
		{"OR", list("Alpha or Beta")},
		{`alpha"; DROP TABLE search_albums; --`, nil},
		{"alpha\x00beta", list("Alpha or Beta")},
		{"*", nil},
		{`"`, nil},
		{`""`, nil},
		{"'", nil},
	} {
		wantFound(t, c.text, "albums", findAll(t, s, c.text).albums, c.albums)
	}
	for text, want := range map[string][]string{
		"NEAR(alpha beta)":    list("Near Alpha Beta"),
		"NEAR(":               list("Near Alpha Beta"),
		"NEAR(alpha beta, 1)": nil,
		"NOT":                 list("Not"),
		"alpha -beta":         list("Near Alpha Beta", "One"),
	} {
		wantFound(t, text, "tracks", findAll(t, s, text).tracks, want)
	}
	if got := rows(t, s, `SELECT count(*) FROM search_albums`); !slices.Equal(got, list("3")) {
		t.Fatalf("the full-text rows of the albums after the searches: %q", got)
	}
}

// §10.2: a word found in the name or the title comes before one found in
// the other columns, whatever the order of the rows; rows of one rank are
// in the order of their rowid; and the answer is always the same.
func TestFindOrder(t *testing.T) {
	s := newStore(t)
	index(t, s,
		// Found by its artist only, and first in the table.
		entry{"Blue", "Songs", list("First", "Second")},
		entry{"Miles Davis", "Kind of Blue", list("So What", "Blue in Green")},
		entry{"Joni Mitchell", "Blue", list("Blue", "River")},
		entry{"Twins", "Same", list("Twin", "Twin", "Twin", "Twin", "Twin")},
	)
	first := findAll(t, s, "blue")
	// The albums found by their title, then the one found by its artist.
	wantFound(t, "blue", "albums", first.albums, list("Blue", "Kind of Blue", "Songs"))
	// The tracks found by their title come before those found by their
	// album or their artist.
	if len(first.tracks) != 6 || !slices.Equal(first.tracks[:2], list("Blue", "Blue in Green")) {
		t.Errorf("searching \"blue\": the tracks %q, want the two found by their title first", first.tracks)
	}
	wantFound(t, "blue", "artists", first.artists, list("Blue"))
	for range 5 {
		if again := findAll(t, s, "blue"); !slices.Equal(again.albums, first.albums) || !slices.Equal(again.tracks, first.tracks) {
			t.Fatalf("the same search answers %+v and then %+v", first, again)
		}
	}

	// Five rows with the same words have the same rank: they come in the
	// order of their rowid, and a limit keeps the first.
	twins := func(limit int) []int64 {
		t.Helper()
		var res Results
		err := s.Read(t.Context(), func(q *store.Queries) (err error) {
			res, err = Find(t.Context(), q, "", Parse("twin"), Kinds{Tracks: true}, limit)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		var seqs []int64
		for _, r := range res.Tracks {
			seqs = append(seqs, r.Track.Seq)
		}
		return seqs
	}
	all := twins(50)
	if len(all) != 5 || !slices.IsSorted(all) {
		t.Fatalf("rows of the same rank are not in the order of their rowid: %v", all)
	}
	if two := twins(2); !slices.Equal(two, all[:2]) {
		t.Fatalf("the first two twins: %v of %v", two, all)
	}
}

// §10.2: the limit counts for each kind, and only the kinds asked for are
// searched.
func TestFindLimitAndKinds(t *testing.T) {
	s := newStore(t)
	var entries []entry
	for i := range 7 {
		entries = append(entries, entry{fmt.Sprintf("Common Artist %d", i), fmt.Sprintf("Common Album %d", i),
			list(fmt.Sprintf("Common Track %d", i), fmt.Sprintf("Common Other %d", i))})
	}
	index(t, s, entries...)
	for _, limit := range []int{1, 3, 7, 50} {
		got, err := findIn(t.Context(), s, "common", everything, limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.artists) != min(limit, 7) || len(got.albums) != min(limit, 7) || len(got.tracks) != min(limit, 14) {
			t.Errorf("limit %d: %d artists, %d albums, %d tracks", limit, len(got.artists), len(got.albums), len(got.tracks))
		}
	}
	for _, kinds := range []Kinds{{}, {Artists: true}, {Albums: true}, {Tracks: true}, {Artists: true, Tracks: true}, everything} {
		got, err := findIn(t.Context(), s, "common", kinds, 50)
		if err != nil {
			t.Fatal(err)
		}
		if (len(got.artists) != 0) != kinds.Artists || (len(got.albums) != 0) != kinds.Albums || (len(got.tracks) != 0) != kinds.Tracks {
			t.Errorf("kinds %+v: %d artists, %d albums, %d tracks", kinds, len(got.artists), len(got.albums), len(got.tracks))
		}
	}
}

// What is not available is not found, and is found again when it returns
// (§10.1): a track, then its whole album, with its artist.
func TestFindOnlyWhatIsAvailable(t *testing.T) {
	s := newStore(t)
	index(t, s, jazz...)
	ctx := t.Context()
	albumID, artistID := testID('b', 1), testID('a', 1)
	blueInGreen := testID('c', 3)
	resync := func(q *store.Queries) error {
		return errors.Join(SyncAlbum(ctx, q.Conn(), albumID), SyncArtist(ctx, q.Conn(), artistID))
	}

	write(t, s, func(q *store.Queries) error {
		return errors.Join(q.SetTrackUnavailable(ctx, store.SetTrackUnavailableParams{ID: blueInGreen, UpdatedAt: 2}), resync(q))
	})
	got := findAll(t, s, "miles")
	wantFound(t, "miles", "tracks", got.tracks, list("So What"))
	wantFound(t, "miles", "albums", got.albums, list("Kind of Blue"))
	wantFound(t, "miles", "artists", got.artists, list("Miles Davis"))

	write(t, s, func(q *store.Queries) error {
		return errors.Join(
			q.SetTracksOfAlbumUnavailable(ctx, store.SetTracksOfAlbumUnavailableParams{UpdatedAt: 3, AlbumID: albumID}),
			q.SetAlbumUnavailable(ctx, store.SetAlbumUnavailableParams{UpdatedAt: 3, ID: albumID}), resync(q))
	})
	if got := findAll(t, s, "miles"); got.artists != nil || got.albums != nil || got.tracks != nil {
		t.Fatalf("an album that is not available is found: %+v", got)
	}
	if got := findAll(t, s, "b"); !slices.Equal(got.artists, list("Beyoncé", "Björk")) {
		t.Fatalf("the other artists: %q", got.artists)
	}

	// Back, with the same rows.
	write(t, s, func(q *store.Queries) error {
		a := album(albumID, artistID, "Kind of Blue")
		a.RelPath = albumID
		return errors.Join(q.UpsertAlbum(ctx, a),
			q.UpsertTrack(ctx, track(testID('c', 2), albumID, "So What", "Miles Davis", 1)),
			q.UpsertTrack(ctx, track(blueInGreen, albumID, "Blue in Green", "Miles Davis", 2)), resync(q))
	})
	got = findAll(t, s, "miles")
	wantFound(t, "miles", "tracks", got.tracks, list("So What", "Blue in Green"))
	wantFound(t, "miles", "albums", got.albums, list("Kind of Blue"))
	wantFound(t, "miles", "artists", got.artists, list("Miles Davis"))
}

// The full-text tables and the tables they copy must agree. A row of the
// first that describes nothing available is an error of the search, never
// a result and never "no such row".
func TestFindRefusesAStaleRow(t *testing.T) {
	s := newStore(t)
	index(t, s, jazz...)
	ctx := t.Context()
	// Unavailable, and the full-text rows left as they were.
	write(t, s, func(q *store.Queries) error {
		return q.SetTrackUnavailable(ctx, store.SetTrackUnavailableParams{ID: testID('c', 3), UpdatedAt: 2})
	})
	_, err := findIn(ctx, s, "green", everything, 50)
	if !errors.Is(err, errNotAvailable) || !strings.Contains(err.Error(), "search: searching the tracks") {
		t.Fatalf("a full-text row of an unavailable track: %v", err)
	}
	write(t, s, func(q *store.Queries) error {
		return q.SetAlbumUnavailable(ctx, store.SetAlbumUnavailableParams{UpdatedAt: 3, ID: testID('b', 0)})
	})
	if _, err := findIn(ctx, s, "lemonade", Kinds{Albums: true}, 50); !errors.Is(err, errNotAvailable) {
		t.Fatalf("a full-text row of an unavailable album: %v", err)
	}
	if _, err := findIn(ctx, s, "beyonce", Kinds{Artists: true}, 50); !errors.Is(err, errNotAvailable) {
		t.Fatalf("a full-text row of an artist without albums: %v", err)
	}
	// The kinds that are not asked for are not read.
	if _, err := findIn(ctx, s, "beyonce", Kinds{}, 50); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = s.Read(ctx, func(q *store.Queries) error {
		_, err := Find(cancelled, q, "", Parse("miles"), everything, 50)
		return err
	})
	if !errors.Is(err, context.Canceled) || errors.Is(err, errNotAvailable) {
		t.Fatalf("a search with a context that ended: %v", err)
	}
}

// T19, §10.3: unicode61 does not split Chinese and Japanese. A run of
// ideographs is one word, found from its beginning and from nowhere else.
// This is the known limit, fixed here as it is.
func TestFindCJK(t *testing.T) {
	s := newStore(t)
	index(t, s,
		entry{"坂本龍一", "千のナイフ", list("東風", "千のナイフ")},
		entry{"周杰倫", "葉惠美", list("以父之名", "晴天")},
	)
	for text, want := range map[string][]string{
		"坂本龍一": list("坂本龍一"),
		"坂本":   list("坂本龍一"),
		"坂":    list("坂本龍一"),
		"龍一":   nil,
		"本龍":   nil,
		"周杰":   list("周杰倫"),
		"杰倫":   nil,
	} {
		wantFound(t, text, "artists", findAll(t, s, text).artists, want)
	}
	for text, want := range map[string][]string{
		"千のナ":    list("千のナイフ", "東風"),
		"ナイフ":    nil,
		"以父":     list("以父之名"),
		"之名":     nil,
		"晴天 周杰倫": list("晴天"),
		"晴天 杰倫":  nil,
	} {
		wantFound(t, text, "tracks", findAll(t, s, text).tracks, want)
	}
}

// §10.2 as amended: unicode61 keeps in a token the numbers that are no
// decimal digits and the combining marks, so a query split at them would
// look for the beginning of a token that does not exist: H₂O would be H and
// O, and would not find the album it names.
func TestFindNumbersAndMarks(t *testing.T) {
	const acute, circumflex, dotBelow = string(rune(0x301)), string(rune(0x302)), string(rune(0x323))
	s := newStore(t)
	index(t, s,
		entry{"Écho Café", "H₂O", list("x² + y²", "½ Full")},
		entry{"Việt", "Ⅷ Symphony", list("Hà Nội")},
		// The names of a library written in decomposed form.
		entry{"Zoe" + acute, "Re" + acute + "sume" + acute, list("Ame" + acute + "lie")},
	)
	for text, want := range map[string][]string{
		"H₂O": list("H₂O"),
		"h₂":  list("H₂O"),
		"h":   list("H₂O"),
		// The token is h₂o: neither of these is its beginning.
		"h2o":                         nil,
		"ho":                          nil,
		"o":                           nil,
		"Ⅷ":                           list("Ⅷ Symphony"),
		"ⅷ symph":                     list("Ⅷ Symphony"),
		"viii":                        nil,
		"resume":                      list("Re" + acute + "sume" + acute),
		"résumé":                      list("Re" + acute + "sume" + acute),
		"re" + acute:                  list("Re" + acute + "sume" + acute),
		"re" + acute + "sume" + acute: list("Re" + acute + "sume" + acute),
		// Marks alone have no token: the other words still count.
		acute + " resume " + circumflex:     list("Re" + acute + "sume" + acute),
		acute + "resume":                    list("Re" + acute + "sume" + acute),
		acute:                               nil,
		acute + circumflex + " " + dotBelow: nil,
	} {
		wantFound(t, text, "albums", findAll(t, s, text).albums, want)
	}
	for text, want := range map[string][]string{
		// Decomposed, with the accent inside the word and two marks on a letter.
		"E" + acute + "cho":                 list("Écho Café"),
		"e" + acute + "cho cafe" + acute:    list("Écho Café"),
		"E" + acute + "ch":                  list("Écho Café"),
		"Vie" + dotBelow + circumflex + "t": list("Việt"),
		"vie" + circumflex + dotBelow:       list("Việt"),
		"việt":                              list("Việt"),
		"viet":                              list("Việt"),
		"zoé":                               list("Zoe" + acute),
		"zo" + acute + "e":                  list("Zoe" + acute),
		"E" + acute + " cho":                nil,
	} {
		wantFound(t, text, "artists", findAll(t, s, text).artists, want)
	}
	for text, want := range map[string][]string{
		"x²":   list("x² + y²"),
		"y² x": list("x² + y²"),
		"x2":   nil,
		"½":    list("½ Full"),
		"½ fu": list("½ Full"),
		"1":    nil,
		"ha" + string(rune(0x300)) + " no" + circumflex + dotBelow + "i": list("Hà Nội"),
		"ame" + acute + "lie zoe": list("Ame" + acute + "lie"),
		"amélie":                  list("Ame" + acute + "lie"),
		"lie":                     nil,
	} {
		wantFound(t, text, "tracks", findAll(t, s, text).tracks, want)
	}
}

// §10.2: at most 8 words of at most 64 characters; the rest does not count.
// q is split at the ASCII characters that are no letter and no digit, at the
// Unicode spaces, at the control characters and at the bytes that are not
// UTF-8, and nowhere else.
func TestParse(t *testing.T) {
	long := strings.Repeat("é", 70)
	for _, c := range []struct{ text, match string }{
		{"", ""},
		{" \t\n", ""},
		{"!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", ""},
		{"mil dav", `"mil"* "dav"*`},
		{"Mil", `"Mil"*`},
		{"a1 2b", `"a1"* "2b"*`},
		{"٣ ３", `"٣"* "３"*`},
		{`say "hello"`, `"say"* "hello"*`},
		{"a OR b", `"a"* "OR"* "b"*`},
		{"title:x", `"title"* "x"*`},
		{"NEAR(a b, 2)", `"NEAR"* "a"* "b"* "2"*`},
		{"a* -b ^c", `"a"* "b"* "c"*`},
		{"one_two", `"one"* "two"*`},
		// A number that is not a decimal digit and a combining mark stay in
		// their word, as unicode61 keeps them in a token of the index.
		{"H₂O", `"H₂O"*`},
		{"x² ½ Ⅷ", `"x²"* "½"* "Ⅷ"*`},
		{"é", "\"é\"*"},
		{"Écho Café", "\"Écho\"* \"Café\"*"},
		{"Việt", "\"Việt\"*"},
		// So does everything that is not ASCII, a space or a control: SQLite
		// decides what is in a token, and the recent emoji and the private
		// use characters are.
		{"A🤝B", `"A🤝B"*`},
		{"🫶Love", `"🫶Love"*`},
		{"🫶", `"🫶"*`},
		{"Disco🪩Ball", `"Disco🪩Ball"*`},
		{"Old💔Heart", `"Old💔Heart"*`},
		{"AB", "\"AB\"*"},
		{"AppleMusic", "\"AppleMusic\"*"},
		{"5₺ ₿", `"5₺"* "₿"*`},
		{"Sigur—Rós", `"Sigur—Rós"*`},
		{"«miles»", `"«miles»"*`},
		{"miles、davis", `"miles、davis"*`},
		{"don’t", `"don’t"*`},
		{"miles…davis", `"miles…davis"*`},
		// A soft hyphen and a zero width space are format characters, not
		// spaces; U+FFFD sent as itself is a character.
		{"a­b a​b", "\"a­b\"* \"a​b\"*"},
		{"a�b", "\"a�b\"*"},
		// A word that will have no token is a word all the same: FTS5 leaves
		// it out. It counts among the eight.
		{"́", "\"́\"*"},
		{"—", `"—"*`},
		{"…", `"…"*`},
		{"́̂ ̣", "\"́̂\"* \"̣\"*"},
		{"a ́ b", "\"a\"* \"́\"* \"b\"*"},
		{"miles — davis", `"miles"* "—"* "davis"*`},
		{"́ 1 2 3 4 5 6 7 8 9", "\"́\"* \"1\"* \"2\"* \"3\"* \"4\"* \"5\"* \"6\"* \"7\"*"},
		{"— — — — — — — — miles", `"—"* "—"* "—"* "—"* "—"* "—"* "—"* "—"*`},
		{"́a", "\"́a\"*"},
		// The marks count among the 64 characters.
		{strings.Repeat("é", 40), `"` + strings.Repeat("é", 32) + `"*`},
		{strings.Repeat("́", 64) + "a b", `"` + strings.Repeat("́", 64) + `"* "b"*`},
		// The spaces that are not ASCII, the line and paragraph separators
		// and the controls of both ranges separate.
		{"a b c d e f　g", `"a"* "b"* "c"* "d"* "e"* "f"* "g"*`},
		{"a b c", `"a"* "b"* "c"*`},
		{"a\x00b\x1fc\x7fd\u0080e\u0085f\u009fg", `"a"* "b"* "c"* "d"* "e"* "f"* "g"*`},
		// So does a byte that is not UTF-8, alone or inside a cut sequence.
		{"a\x00b\xffc", `"a"* "b"* "c"*`},
		{"a\xe2\x82b\xf0\x9f", `"a"* "b"*`},
		{"\xff\xfe", ""},
		{"1 2 3 4 5 6 7 8", `"1"* "2"* "3"* "4"* "5"* "6"* "7"* "8"*`},
		{"1 2 3 4 5 6 7 8 9 10", `"1"* "2"* "3"* "4"* "5"* "6"* "7"* "8"*`},
		{long, `"` + strings.Repeat("é", 64) + `"*`},
		{"x " + long + " y", `"x"* "` + strings.Repeat("é", 64) + `"* "y"*`},
	} {
		q := Parse(c.text)
		if q.match != c.match {
			t.Errorf("Parse(%q) = %s, want %s", c.text, q.match, c.match)
		}
		if q.Empty() != (c.match == "") {
			t.Errorf("Parse(%q).Empty() = %v", c.text, q.Empty())
		}
		if !utf8.ValidString(q.match) {
			t.Errorf("Parse(%q) is not UTF-8", c.text)
		}
	}
}

// §10.2 as amended: what unicode61 keeps in a token is decided by the
// Unicode tables of SQLite, not by a category. The private use characters
// and the characters those tables do not know (the recent emoji, the recent
// currency signs) are in the token, so a query split at them would look for
// the beginning of a token that does not exist: the exact name would not be
// found. The query leaves them in the word and FTS5 splits it as it split
// the name.
func TestFindWhatSQLiteKeepsInAToken(t *testing.T) {
	const privateUse, appleLogo = "", ""
	s := newStore(t)
	index(t, s,
		entry{"A🤝B", "🫶Love", list("Disco🪩Ball", "5₺")},
		entry{"A" + privateUse + "B", "Apple" + appleLogo + "Music", list("Old💔Heart")},
		entry{"A B", "Love", list("Disco Ball")},
	)
	for text, want := range map[string][]string{
		"A🤝B":                  list("A🤝B"),
		"a🤝":                   list("A🤝B"),
		"A" + privateUse + "B": list("A" + privateUse + "B"),
		// The three names begin with a token that begins with a.
		"a": list("A🤝B", "A"+privateUse+"B", "A B"),
		// For SQLite the name with the emoji is one token: b begins none.
		"a b": list("A B"),
		"b":   list("A B"),
	} {
		wantFound(t, text, "artists", findAll(t, s, text).artists, want)
	}
	for text, want := range map[string][]string{
		"🫶Love":                       list("🫶Love"),
		"🫶":                           list("🫶Love"),
		"🫶lo":                         list("🫶Love"),
		"Apple" + appleLogo + "Music": list("Apple" + appleLogo + "Music"),
		"apple" + appleLogo:           list("Apple" + appleLogo + "Music"),
		"apple":                       list("Apple" + appleLogo + "Music"),
		"apple music":                 nil,
		"love":                        list("Love"),
		"🫶 love":                      nil,
	} {
		wantFound(t, text, "albums", findAll(t, s, text).albums, want)
	}
	for text, want := range map[string][]string{
		"Disco🪩Ball": list("Disco🪩Ball"),
		"disco🪩":     list("Disco🪩Ball"),
		// The known limit (§10.3): the words around such an emoji are not
		// words of the index.
		"disco ball": list("Disco Ball"),
		"ball":       list("Disco Ball"),
		"5₺":         list("5₺"),
		"5":          list("5₺"),
		// An emoji the tables of SQLite know is a separator, in the index
		// and in the string of the query.
		"Old💔Heart": list("Old💔Heart"),
		"old heart": list("Old💔Heart"),
		"heart":     list("Old💔Heart"),
		"heart old": list("Old💔Heart"),
		"heart💔old": nil,
	} {
		wantFound(t, text, "tracks", findAll(t, s, text).tracks, want)
	}
}

// §10.2 as amended: punctuation that is not ASCII stays in the word, and
// FTS5 splits the word inside its string. Words joined by it are a phrase,
// in their order, with the last as a beginning. A word that has no token
// at all is left out of the others by FTS5, and alone finds nothing.
func TestFindPhrasesAndWordsWithoutTokens(t *testing.T) {
	const acute = "́"
	s := newStore(t)
	index(t, s,
		entry{"Miles Davis", "Kind of Blue", list("So What", "Don’t Stop", "Don't Go")},
		entry{"Sigur Rós", "Ágætis byrjun", list("Svefn-g-englar")},
		entry{"がくや", "Ἀθήνα", list("東風")},
	)
	for text, want := range map[string][]string{
		"miles — davis":       list("Miles Davis"),
		acute + " miles":      list("Miles Davis"),
		"… miles":             list("Miles Davis"),
		"miles " + acute:      list("Miles Davis"),
		"— … « » miles 、 。 ·": list("Miles Davis"),
		"—":                   nil,
		acute:                 nil,
		"…":                   nil,
		"— …":                 nil,
		"«miles»":             list("Miles Davis"),
		"«mil":                list("Miles Davis"),
		"miles、davis":         list("Miles Davis"),
		"miles、dav":           list("Miles Davis"),
		"miles…davis":         list("Miles Davis"),
		"Sigur—Rós":           list("Sigur Rós"),
		"sigur—r":             list("Sigur Rós"),
		"sigur ros":           list("Sigur Rós"),
		"ros sigur":           list("Sigur Rós"),
		// A phrase has an order, and only its last word is a beginning.
		"Rós—Sigur":   nil,
		"davis、miles": nil,
		"mil、davis":   nil,
		// The known limit (§10.3): unicode61 does not take these marks away,
		// so the decomposed form does not find the composed name.
		"がくや":      list("がくや"),
		"がくや":     nil,
		"̓ sigur—": list("Sigur Rós"),
	} {
		wantFound(t, text, "artists", findAll(t, s, text).artists, want)
	}
	for text, want := range map[string][]string{
		// The typographic apostrophe separates in the index and in the string.
		"don’t":      list("Don’t Stop", "Don't Go"),
		"don't":      list("Don’t Stop", "Don't Go"),
		"don’t stop": list("Don’t Stop"),
		"stop don’t": list("Don’t Stop"),
		"don’s":      nil,
		"t’don":      nil,
		"svefn—g—en": list("Svefn-g-englar"),
		"g—svefn":    nil,
	} {
		wantFound(t, text, "tracks", findAll(t, s, text).tracks, want)
	}
	for text, want := range map[string][]string{
		"Ἀθήνα":  list("Ἀθήνα"),
		"ἀθήνα":  list("Ἀθήνα"),
		"ἀθήνα": nil,
	} {
		wantFound(t, text, "albums", findAll(t, s, text).albums, want)
	}
}

// sweepName is a name with r between two words no other name of the sweep
// has, so that a search for it can find it alone.
func sweepName(r rune) string {
	return fmt.Sprintf("a%06x%cb%06x", r, r, r)
}

// The rule of §10.2 on the real index, one character at a time: the name
// "a<r>b" is found by searching for itself. Parse may split only where
// unicode61 splits: for every character it splits at, without exception,
// the two halves must be two tokens of the index, or the second is found
// nowhere. The others stay in the word, whatever unicode61 does with them:
// a sample of every kind, and one character in 4099 of all of Unicode.
func TestFindAcrossEverySeparator(t *testing.T) {
	sample := []rune{
		0xe000, 0xf8ff, 0xf0000, 0x10fffd, // private use
		'🤝', '🫶', '🪩', '🥺', '🤍', // emoji SQLite does not know
		'💔', '🎵', '☺', '♥', // emoji it knows
		'₺', '₽', '₿', '€', '＄', // currency signs
		'—', '…', '«', '»', '、', '。', '’', '·', 0x2e3c, 0x2e52, 0x61c, // punctuation that is not ASCII
		0x301, 0x323, 0x3099, 0x313, // nonspacing marks (Mn)
		0x93e, 0x903, 0x1d165, // spacing marks (Mc)
		0x20dd, 0x488, // enclosing marks (Me)
		0xad, 0x200b, 0x200d, 0xfeff, // format characters
		'₂', '²', '½', 'Ⅷ', '٣', 'é', 'ß', '龍', 'か', // numbers and letters
		0x378, 0x50000, 0xe0080, 0xfffd, 0xffff, 0x10ffff, // not assigned, or no character
	}
	var separators, others []rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		switch {
		case !utf8.ValidRune(r):
		case separates(r):
			separators = append(separators, r)
		case r%4099 == 0 || slices.Contains(sample, r):
			others = append(others, r)
		}
	}
	// 66 of ASCII, the 32 controls after it, and the 18 spaces and line and
	// paragraph separators beyond.
	if len(separators) < 116 {
		t.Fatalf("Parse splits at %d characters: %U", len(separators), separators)
	}
	if !slices.Contains(separators, '"') || !slices.Contains(separators, 0) {
		t.Fatal("the quote or NUL does not separate")
	}
	all := slices.Concat(separators, others)
	entries := make([]entry, len(all))
	for i, r := range all {
		entries[i] = entry{sweepName(r), "x", nil}
	}
	s := newStore(t)
	index(t, s, entries...)
	artists := Kinds{Artists: true}
	for _, r := range all {
		name := sweepName(r)
		got, err := findIn(t.Context(), s, name, artists, 5)
		if err != nil {
			t.Fatalf("%U: searching %q: %v", r, name, err)
		}
		if !slices.Equal(got.artists, list(name)) {
			t.Errorf("%U (Parse splits: %v): searching %q (%s) finds %q", r, separates(r), name, Parse(name).match, got.artists)
		}
	}
}

// A word longer than the limit still finds what begins with its first 64
// characters: it is cut, not dropped.
func TestFindALongWord(t *testing.T) {
	s := newStore(t)
	word := strings.Repeat("a", 80)
	index(t, s, entry{"Artist", word, list("Track")})
	for _, text := range []string{word, word[:64], word[:70] + "b"} {
		wantFound(t, text, "albums", findAll(t, s, text).albums, list(word))
	}
	wantFound(t, "63 and b", "albums", findAll(t, s, word[:63]+"b").albums, nil)
}
