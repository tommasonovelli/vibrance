//go:build perf

package perfgen

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"
)

// The budgets of the step S24 (DESIGN.md §14): the 95th percentile of each
// kind of request, with a warm database and one client.
const (
	budgetAlbumList   = 30 * time.Millisecond
	budgetAlbumDetail = 20 * time.Millisecond
	budgetSearch      = 50 * time.Millisecond
	budgetFavorites   = 30 * time.Millisecond
	budgetItemsPage   = 30 * time.Millisecond
	budgetAddAndMove  = 100 * time.Millisecond
	budgetStartupSec  = 3
	budgetIdleMB      = 150
)

// rest is how long the server is left alone before its memory at rest is
// read: the Go runtime gives memory back to the system a while after it is
// freed.
const rest = 30 * time.Second

// TestPerfFullDataset measures the server on the dataset of the step: 20,000
// albums, 200,000 tracks, 2,000 artists, 20 users, 500 playlists of 200
// items and 50,000 favorites. MusicLib's folder is empty, as with MusicLib
// not installed: the scanner leaves the index as it is (I14).
func TestPerfFullDataset(t *testing.T) {
	stateDir, musiclibDir := generate(t, Full, false)
	checkRows(t, stateDir, Full)
	bin := build(t, stateDir, musiclibDir)
	t.Run("Server", func(t *testing.T) { measureServer(t, bin) })
	t.Run("QueryPlans", func(t *testing.T) { checkQueryPlans(t, stateDir) })
	t.Run("SearchRows", func(t *testing.T) { measureSearchRows(t, stateDir) })
	// The last: it writes.
	t.Run("ArtistRename", func(t *testing.T) { measureArtistRename(t, stateDir) })
}

func measureServer(t *testing.T, bin string) {
	r := &report{t: t}
	defer r.print()

	// The first start of a new database writes its sort keys (DESIGN.md
	// §5.5): it is the start after an upgrade that changes the collation.
	srv := start(t, bin)
	r.value("first start: every sort key computed again", srv.startup.Seconds(), 0, "s")
	srv.stop()
	startup := 0.0
	for range 5 {
		srv = start(t, bin)
		startup = max(startup, srv.startup.Seconds())
		srv.stop()
	}
	r.value("startup, the slowest of 5", startup, budgetStartupSec, "s")
	srv = start(t, bin)
	time.Sleep(5 * time.Second)
	r.value("memory at rest after the startup (RSS)", float64(srv.memory("VmRSS"))/1024, budgetIdleMB, "MB")

	admin, user := srv.signIn(Admin), srv.signIn(User)
	albums := measureAlbumLists(r, admin)
	largest, smallest := measureArtistFilter(r, admin)
	measureTrackLists(r, admin, largest, smallest)
	tracks := measureAlbumDetails(r, admin, albums)
	measureSearch(r, admin)
	measureFavorites(r, admin)
	measureSummaries(r, admin)
	measurePlaylists(r, admin)
	measureLargePlaylist(r, user, tracks)
	measureDocs(r, admin)

	r.value("memory right after the load (RSS)", float64(srv.memory("VmRSS"))/1024, 0, "MB")
	time.Sleep(rest)
	r.value("memory at rest after the load (RSS)", float64(srv.memory("VmRSS"))/1024, budgetIdleMB, "MB")
	r.value("memory, the most the process ever held (peak RSS)", float64(srv.memory("VmHWM"))/1024, 0, "MB")
	srv.stop()
}

// albumPage is a page of GET /albums.
type albumPage struct {
	Albums []struct {
		ID string `json:"id"`
	} `json:"albums"`
	Next *string `json:"next"`
}

// The orders of GET /albums (DESIGN.md §8.5).
var albumSorts, albumOrders = []string{"title", "artist", "year", "added"}, []string{"asc", "desc"}

// walkAlbums reads every page of a list of the albums and adds the time of
// each to m, when m is not nil. It returns the ids, and the cursor of the
// last page.
func walkAlbums(c *client, query string, limit int, m *measure) (ids []string, last string) {
	c.t.Helper()
	after := ""
	for {
		var page albumPage
		took := c.get("/api/v1/albums?"+query+"&limit="+strconv.Itoa(limit)+after, &page)
		if m != nil {
			m.add(took)
		}
		for _, a := range page.Albums {
			ids = append(ids, a.ID)
		}
		if page.Next == nil {
			return ids, last
		}
		last = *page.Next
		after = "&after=" + url.QueryEscape(last)
	}
}

// measureAlbumLists walks the list of the albums in its eight orders, 50
// albums a page, and then asks again and again for the last page, the
// 400th, and for the 1,000th of pages of 20. It returns the ids of the
// albums.
func measureAlbumLists(r *report, c *client) []string {
	r.t.Helper()
	var ids []string
	last50 := r.measure("albums: the last page of 50 (the 400th), 8 orders", budgetAlbumList)
	last20 := r.measure("albums: the last page of 20 (the 1,000th), 8 orders", budgetAlbumList)
	for _, sort := range albumSorts {
		for _, order := range albumOrders {
			query := "sort=" + sort + "&order=" + order
			// Once to warm the database, once to measure.
			walkAlbums(c, query, 50, nil)
			m := r.measure("albums: every page of 50, sort="+sort+" order="+order, budgetAlbumList)
			var cursor string
			ids, cursor = walkAlbums(c, query, 50, m)
			if len(ids) != Full.Albums || len(m.samples) != Full.Albums/50 {
				r.t.Fatalf("%s: %d albums in %d pages", query, len(ids), len(m.samples))
			}
			for range 25 {
				var page albumPage
				last50.add(c.get("/api/v1/albums?"+query+"&limit=50&after="+url.QueryEscape(cursor), &page))
				if len(page.Albums) != 50 || page.Next != nil {
					r.t.Fatalf("%s: the last page has %d albums", query, len(page.Albums))
				}
			}
			_, cursor = walkAlbums(c, query, 20, nil)
			for range 25 {
				var page albumPage
				last20.add(c.get("/api/v1/albums?"+query+"&limit=20&after="+url.QueryEscape(cursor), &page))
				if len(page.Albums) != 20 || page.Next != nil {
					r.t.Fatalf("%s: the last page has %d albums", query, len(page.Albums))
				}
			}
		}
	}
	return ids
}

// measureArtistFilter measures the list of the albums of one artist, in the
// eight orders: of the artist with the most albums and of artists with one
// album, which is the worst case of a list that walks the index of its
// order and skips the albums of the others. It returns those artists.
func measureArtistFilter(r *report, c *client) (largestID string, smallestIDs []string) {
	r.t.Helper()
	type artistSummary struct {
		ID         string `json:"id"`
		AlbumCount int    `json:"album_count"`
	}
	var (
		artists []artistSummary
		pages   = r.measure("artists: every page of 50", 0)
		after   string
	)
	for {
		var page struct {
			Artists []artistSummary `json:"artists"`
			Next    *string         `json:"next"`
		}
		pages.add(c.get("/api/v1/artists?limit=50"+after, &page))
		artists = append(artists, page.Artists...)
		if page.Next == nil {
			break
		}
		after = "&after=" + url.QueryEscape(*page.Next)
	}
	if len(artists) != Full.Artists {
		r.t.Fatalf("%d artists listed", len(artists))
	}
	slices.SortStableFunc(artists, func(a, b artistSummary) int { return b.AlbumCount - a.AlbumCount })
	largest, smallest := artists[0], artists[len(artists)-10:]

	detail := r.measure(fmt.Sprintf("artist detail, the artist with the most albums (%d)", largest.AlbumCount), 0)
	of := r.measure(fmt.Sprintf("albums ?artist= of the artist with the most albums (%d), 8 orders", largest.AlbumCount), budgetAlbumList)
	ofOne := r.measure("albums ?artist= of artists with one album, 8 orders", budgetAlbumList)
	for pass := range 2 {
		for _, sort := range albumSorts {
			for _, order := range albumOrders {
				query := "/api/v1/albums?limit=50&sort=" + sort + "&order=" + order + "&artist="
				for range 3 {
					var page albumPage
					took := c.get(query+largest.ID, &page)
					if len(page.Albums) != min(50, largest.AlbumCount) {
						r.t.Fatalf("%d albums of the largest artist", len(page.Albums))
					}
					if pass == 1 {
						of.add(took)
					}
				}
				for _, a := range smallest {
					var page albumPage
					took := c.get(query+a.ID, &page)
					if len(page.Albums) != a.AlbumCount {
						r.t.Fatalf("%d albums of an artist with %d", len(page.Albums), a.AlbumCount)
					}
					if pass == 1 {
						ofOne.add(took)
					}
				}
			}
		}
		for range 10 {
			if took := c.get("/api/v1/artists/"+largest.ID, nil); pass == 1 {
				detail.add(took)
			}
		}
	}
	for _, a := range smallest {
		smallestIDs = append(smallestIDs, a.ID)
	}
	return largest.ID, smallestIDs
}

// trackPage is a page of GET /tracks.
type trackPage struct {
	Tracks []struct {
		ID string `json:"id"`
	} `json:"tracks"`
	Next *string `json:"next"`
}

// The orders of GET /tracks (docs/proposals/web-client-api.md A1).
var trackSorts = []string{"title", "artist", "album", "added"}

// measureTrackLists measures the list of the tracks (step W1), in its eight
// orders, 50 tracks a page: the first page, and the 40 pages after it
// (2,000 of the 200,000 tracks; the plans of QueryPlans prove that a page
// after a cursor costs what the first does, wherever it is). Then the
// tracks of the albums of one artist, of the artist with the most albums
// and of artists with one album. Every one has the budget of the lists of
// DESIGN.md §8.5, the budget of the albums.
func measureTrackLists(r *report, c *client, largest string, smallest []string) {
	r.t.Helper()
	first := r.measure("tracks: the first page of 50, 8 orders", budgetAlbumList)
	for _, sort := range trackSorts {
		for _, order := range albumOrders {
			query := "/api/v1/tracks?limit=50&sort=" + sort + "&order=" + order
			m := r.measure("tracks: 40 pages of 50 after the first, sort="+sort+" order="+order, budgetAlbumList)
			for pass := range 2 {
				var page trackPage
				took := c.get(query, &page)
				if pass == 1 {
					first.add(took)
				}
				seen := len(page.Tracks)
				for range 40 {
					if page.Next == nil {
						r.t.Fatalf("%s: no page after %d tracks", query, seen)
					}
					after := *page.Next
					page = trackPage{}
					took := c.get(query+"&after="+url.QueryEscape(after), &page)
					if pass == 1 {
						m.add(took)
					}
					seen += len(page.Tracks)
				}
				if seen != 41*50 {
					r.t.Fatalf("%s: %d tracks in 41 pages", query, seen)
				}
			}
		}
	}
	of := r.measure("tracks ?artist= of the artist with the most albums, first page of 50, 8 orders", budgetAlbumList)
	ofOne := r.measure("tracks ?artist= of artists with one album, 8 orders", budgetAlbumList)
	for pass := range 2 {
		for _, sort := range trackSorts {
			for _, order := range albumOrders {
				query := "/api/v1/tracks?limit=50&sort=" + sort + "&order=" + order + "&artist="
				for range 3 {
					var page trackPage
					took := c.get(query+largest, &page)
					if len(page.Tracks) != 50 {
						r.t.Fatalf("%d tracks of the largest artist", len(page.Tracks))
					}
					if pass == 1 {
						of.add(took)
					}
				}
				for _, a := range smallest {
					var page trackPage
					took := c.get(query+a, &page)
					if len(page.Tracks) == 0 || page.Next != nil {
						r.t.Fatalf("%d tracks of an artist with one album, next %v", len(page.Tracks), page.Next)
					}
					if pass == 1 {
						ofOne.add(took)
					}
				}
			}
		}
	}
}

// measureAlbumDetails reads one album in twenty with its tracks, and
// returns the ids of the tracks it saw.
func measureAlbumDetails(r *report, c *client, albums []string) (tracks []string) {
	r.t.Helper()
	m := r.measure("album detail", budgetAlbumDetail)
	one := r.measure("track", 0)
	for pass := range 2 {
		tracks = tracks[:0]
		for i := 0; i < len(albums); i += 20 {
			var album struct {
				Tracks []struct {
					ID string `json:"id"`
				} `json:"tracks"`
			}
			took := c.get("/api/v1/albums/"+albums[i], &album)
			if len(album.Tracks) == 0 {
				r.t.Fatalf("the album %s has no tracks", albums[i])
			}
			for _, t := range album.Tracks {
				tracks = append(tracks, t.ID)
			}
			if pass == 1 {
				m.add(took)
				one.add(c.get("/api/v1/tracks/"+album.Tracks[0].ID, nil))
			}
		}
	}
	return tracks
}

// searches are what a user types: whole words and beginnings of words,
// several words, other scripts, and a word nothing has. None of them is in
// more than about a tenth of the tracks.
var searches = []string{"love", "lov", "i love you", "blue night", "symphony no", "allegro ma non troppo",
	"c major", "live", "feat", "pt 2", "1975", "miles davis", "the beatles", "beatles", "o connor", "de andré", "amore", "città",
	"cœur", "nuit", "straße", "corazón", "saudade", "miłość", "aşk", "Любовь", "ночь", "αγάπη", "東京", "愛", "さよなら", "月亮",
	"사랑", "حب", "אהבה", "प्यार", "รัก", "zzzzqqq", "love heart night"}

// broadSearches are the first keys of a search as it is typed, and the
// commonest words: each is in a third to a half of the tracks of the
// dataset, as "the" is in a real collection. The full-text index ranks
// every row a search matches, so their time grows with the collection, and
// on this dataset it is over the budget (NOTES.md N-161).
var broadSearches = []string{"s", "a", "th", "the", "of the"}

// measureSearch searches the three kinds at once, with the default limit
// of 10 for each kind, which is where the budget is checked, and with the
// largest, 50.
func measureSearch(r *report, c *client) {
	r.t.Helper()
	for _, limit := range []int{10, 50} {
		for _, kind := range []struct {
			name    string
			queries []string
			open    string
			ceiling time.Duration
		}{{"words in under a tenth of the tracks", searches, "", 0}, {"the commonest words, one letter", broadSearches, "N-161", 300 * time.Millisecond}} {
			m := r.measure(fmt.Sprintf("search, 3 types, limit %d: %s", limit, kind.name), budgetSearch)
			m.open, m.ceiling = kind.open, kind.ceiling
			if limit != 10 {
				m.budget = 0
			}
			found, slowest := 0, map[string]time.Duration{}
			for pass := range 4 {
				for _, q := range kind.queries {
					var res struct {
						Artists, Albums, Tracks []struct{}
					}
					took := c.get("/api/v1/search?q="+url.QueryEscape(q)+"&limit="+strconv.Itoa(limit), &res)
					if pass == 0 {
						found += len(res.Artists) + len(res.Albums) + len(res.Tracks)
						continue
					}
					m.add(took)
					slowest[q] = max(slowest[q], took)
				}
			}
			queries := slices.SortedFunc(maps.Keys(slowest), func(a, b string) int { return int(slowest[b] - slowest[a]) })
			fmt.Printf("PERF search limit %d, %s: %d rows found by %d queries; the slowest:", limit, kind.name, found, len(queries))
			for _, q := range queries[:5] {
				fmt.Printf(" %q %s;", q, ms(slowest[q]))
			}
			fmt.Println()
		}
	}
}

// measureFavorites walks the favorites of a user, 2,500, the newest first.
func measureFavorites(r *report, c *client) {
	r.t.Helper()
	for _, limit := range []int{50, 200} {
		budget := budgetFavorites
		if limit != 50 {
			budget = 0
		}
		m := r.measure(fmt.Sprintf("favorites: every page of %d", limit), budget)
		for pass := range 3 {
			n, after := 0, ""
			for {
				var page struct {
					Favorites []struct{} `json:"favorites"`
					Next      *string    `json:"next"`
				}
				took := c.get("/api/v1/me/favorites/tracks?limit="+strconv.Itoa(limit)+after, &page)
				if pass > 0 {
					m.add(took)
				}
				n += len(page.Favorites)
				if page.Next == nil {
					break
				}
				after = "&after=" + url.QueryEscape(*page.Next)
			}
			if n != Full.Favorites/Full.Users {
				r.t.Fatalf("%d favorites listed", n)
			}
		}
	}
}

// measureSummaries reads the summary of the catalog, which counts every
// available album, and the one of the favorites of a user of 2,500
// favorites (step W2). They have no budget of their own.
func measureSummaries(r *report, c *client) {
	r.t.Helper()
	catalog := r.measure("summary of the catalog: 20,000 albums", 0)
	favorites := r.measure("summary of the favorites: 2,500", 0)
	for range 20 {
		var sum struct {
			Albums int `json:"albums"`
			Tracks int `json:"tracks"`
		}
		catalog.add(c.get("/api/v1/catalog/summary", &sum))
		if sum.Albums != Full.Albums || sum.Tracks != Full.Tracks {
			r.t.Fatalf("the summary of the catalog: %+v", sum)
		}
		var fav struct {
			TrackCount int `json:"track_count"`
		}
		favorites.add(c.get("/api/v1/me/favorites/summary", &fav))
		if fav.TrackCount != Full.Favorites/Full.Users {
			r.t.Fatalf("the summary of the favorites: %+v", fav)
		}
	}
}

// measurePlaylists lists the playlists of the user that has the most a user
// can have, 500, each of 200 items.
func measurePlaylists(r *report, c *client) {
	r.t.Helper()
	list := r.measure("playlists of a user: 500 playlists of 200 items", 0)
	one := r.measure("playlist of 200 items, without its items", 0)
	items := r.measure("playlist of 200 items: page of 50", budgetItemsPage)
	var listed struct {
		Playlists []struct {
			ID        string `json:"id"`
			ItemCount int    `json:"item_count"`
		} `json:"playlists"`
	}
	for range 20 {
		list.add(c.get("/api/v1/playlists", &listed))
	}
	if len(listed.Playlists) != Full.Playlists || listed.Playlists[0].ItemCount != Full.PlaylistItems {
		r.t.Fatalf("%d playlists listed", len(listed.Playlists))
	}
	for _, p := range listed.Playlists[:50] {
		one.add(c.get("/api/v1/playlists/"+p.ID, nil))
		items.add(c.get("/api/v1/playlists/"+p.ID+"/items?limit=50", nil))
	}
}

// playlist is what the changes of a playlist answer with.
type playlist struct {
	ID        string `json:"id"`
	ItemCount int    `json:"item_count"`
	ETag      string `json:"etag"`
}

// added is the answer of POST /playlists/{id}/items.
type added struct {
	Playlist playlist `json:"playlist"`
	Added    []struct {
		ItemID string `json:"item_id"`
	} `json:"added"`
}

// measureLargePlaylist fills a playlist up to the 10,000 items a playlist
// can have, and measures its pages and its changes at that size.
func measureLargePlaylist(r *report, c *client, tracks []string) {
	r.t.Helper()
	if len(tracks) < 1000 {
		r.t.Fatalf("only %d tracks to add", len(tracks))
	}
	rng := rand.New(rand.NewPCG(24, 24))
	var p playlist
	c.must(http.StatusCreated, &p, http.MethodPost, "/api/v1/playlists", map[string]string{"name": "Ten thousand", "description": ""})
	items := "/api/v1/playlists/" + p.ID + "/items"
	add := func(m *measure, n int, position *int) {
		r.t.Helper()
		ids := make([]string, n)
		for i := range ids {
			ids[i] = tracks[rng.IntN(len(tracks))]
		}
		var res added
		header := []string{}
		if position != nil {
			header = []string{"If-Match", p.ETag}
		}
		took := c.must(http.StatusOK, &res, http.MethodPost, items, map[string]any{"track_ids": ids, "position": position}, header...)
		if m != nil {
			m.add(took)
		}
		p = res.Playlist
	}
	first := 0

	// The budget is that of adding one item; the largest block a request can
	// add is measured against it all the same, and at the front of 8,000
	// items it is over (NOTES.md N-162).
	blocks := r.measure("playlist: add 1,000 items at the end (to 0..7,000 items)", budgetAddAndMove)
	for range 8 {
		add(blocks, 1000, nil)
	}
	front1000 := r.measure("playlist of 8,000: add 1,000 items at the front", budgetAddAndMove)
	front1000.open, front1000.ceiling = "N-162", 200*time.Millisecond
	add(front1000, 1000, &first)
	add(nil, 880, nil)
	one := r.measure("playlist of 9,880..9,930: add 1 item at the end", budgetAddAndMove)
	for range 50 {
		add(one, 1, nil)
	}
	// At the front, an add or a move shifts every item: at the budget, over
	// it in some runs (NOTES.md N-162).
	front := r.measure("playlist of 9,930..9,980: add 1 item at the front", budgetAddAndMove)
	front.open, front.ceiling = "N-162", 150*time.Millisecond
	for range 50 {
		add(front, 1, &first)
	}
	add(nil, 20, nil)
	if p.ItemCount != 10000 {
		r.t.Fatalf("the playlist has %d items", p.ItemCount)
	}

	// Its pages, and with them the ids of its items in their order.
	var ids []string
	for _, limit := range []int{50, 200} {
		budget := budgetItemsPage
		if limit != 50 {
			budget = 0
		}
		m := r.measure(fmt.Sprintf("playlist of 10,000: every page of %d", limit), budget)
		for pass := range 2 {
			ids = ids[:0]
			after := ""
			for {
				var page struct {
					Items []struct {
						ID string `json:"id"`
					} `json:"items"`
					Next *string `json:"next"`
				}
				took := c.get(items+"?limit="+strconv.Itoa(limit)+after, &page)
				if pass == 1 {
					m.add(took)
				}
				for _, it := range page.Items {
					ids = append(ids, it.ID)
				}
				if page.Next == nil {
					break
				}
				after = "&after=" + url.QueryEscape(*page.Next)
			}
		}
	}
	if len(ids) != 10000 {
		r.t.Fatalf("%d items listed", len(ids))
	}
	whole := r.measure("playlist of 10,000, without its items", 0)
	for range 20 {
		whole.add(c.get("/api/v1/playlists/"+p.ID, nil))
	}

	move := func(m *measure, from, to int) {
		r.t.Helper()
		took := c.must(http.StatusOK, &p, http.MethodPost, items+"/"+ids[from]+"/move", map[string]int{"position": to}, "If-Match", p.ETag)
		m.add(took)
		id := ids[from]
		ids = slices.Delete(ids, from, from+1)
		ids = slices.Insert(ids, to, id)
	}
	ends := r.measure("playlist of 10,000: move an item from one end to the other", budgetAddAndMove)
	ends.open, ends.ceiling = "N-162", 150*time.Millisecond
	for range 25 {
		move(ends, 0, 9999)
		move(ends, 9999, 0)
	}
	near := r.measure("playlist of 10,000: move an item by 10 places", budgetAddAndMove)
	anywhere := r.measure("playlist of 10,000: move an item anywhere", budgetAddAndMove)
	for range 20 {
		from := rng.IntN(9990)
		move(near, from, from+10)
		move(anywhere, rng.IntN(10000), rng.IntN(10000))
	}

	remove := r.measure("playlist of 10,000: remove an item", 0)
	for range 20 {
		at := rng.IntN(len(ids))
		remove.add(c.must(http.StatusOK, &p, http.MethodDelete, items+"/"+ids[at], nil))
		ids = slices.Delete(ids, at, at+1)
	}
	measureOneAlbumPlaylist(r, c, tracks[0])
}

// measureOneAlbumPlaylist fills a playlist with 10,000 items of one track:
// the worst case of its covers (step W2), which then read every item to
// look for a second album.
func measureOneAlbumPlaylist(r *report, c *client, track string) {
	r.t.Helper()
	var p playlist
	c.must(http.StatusCreated, &p, http.MethodPost, "/api/v1/playlists", map[string]string{"name": "One album", "description": ""})
	items := "/api/v1/playlists/" + p.ID + "/items"
	add := func(m *measure, n int) {
		r.t.Helper()
		var res added
		took := c.must(http.StatusOK, &res, http.MethodPost, items, map[string]any{"track_ids": slices.Repeat([]string{track}, n), "position": nil})
		if m != nil {
			m.add(took)
		}
		p = res.Playlist
	}
	for range 9 {
		add(nil, 1000)
	}
	add(nil, 980)
	one := r.measure("playlist of 9,980..10,000 items of one track: add 1 item at the end", budgetAddAndMove)
	for range 20 {
		add(one, 1)
	}
	whole := r.measure("playlist of 10,000 items of one track, without its items", 0)
	var got struct {
		ItemCount int `json:"item_count"`
		Covers    []struct {
			Hash string `json:"hash"`
		} `json:"covers"`
	}
	for range 20 {
		whole.add(c.get("/api/v1/playlists/"+p.ID, &got))
	}
	if got.ItemCount != 10000 || len(got.Covers) > 1 {
		r.t.Fatalf("the playlist of one track: %d items, %d covers", got.ItemCount, len(got.Covers))
	}
}

// largePlaylists is the dataset of the measure of the step W2: that of the
// step S24 with every playlist at the limit of 10,000 items (DESIGN.md §5.2),
// 5,000,000 items, all of the account Admin.
var largePlaylists = Size{Artists: 2000, Albums: 20000, Tracks: 200000, Users: 20, Playlists: 500, PlaylistItems: 10000,
	Favorites: 50000}

// TestPerfLargePlaylists measures GET /playlists of a user with 500
// playlists of 10,000 items, with their counts and their covers (step W2),
// against the budget of a page of a playlist of 10,000 items of the step
// S24, the only budget of the playlists that a read of them has.
func TestPerfLargePlaylists(t *testing.T) {
	stateDir, musiclibDir := generate(t, largePlaylists, false)
	checkRows(t, stateDir, largePlaylists)
	bin := build(t, stateDir, musiclibDir)
	r := &report{t: t}
	defer r.print()
	srv := start(t, bin)
	defer srv.stop()
	c := srv.signIn(Admin)
	var listed struct {
		Playlists []struct {
			ID        string `json:"id"`
			ItemCount int    `json:"item_count"`
			Covers    []struct {
				Hash string `json:"hash"`
			} `json:"covers"`
		} `json:"playlists"`
	}
	// Over the budget by far: the counts of each playlist read every item
	// of it, 5,000,000 in all, with or without the covers (NOTES.md N-188).
	list := r.measure("playlists of a user: 500 playlists of 10,000 items", budgetItemsPage)
	list.open, list.ceiling = "N-188", 20*time.Second
	c.get("/api/v1/playlists", &listed)
	for range 5 {
		list.add(c.get("/api/v1/playlists", &listed))
	}
	if len(listed.Playlists) != largePlaylists.Playlists {
		t.Fatalf("%d playlists listed", len(listed.Playlists))
	}
	for _, p := range listed.Playlists {
		if p.ItemCount != largePlaylists.PlaylistItems || len(p.Covers) != 4 {
			t.Fatalf("a playlist of %d items with %d covers", p.ItemCount, len(p.Covers))
		}
	}
	one := r.measure("playlist of 10,000 items, without its items", 0)
	for _, p := range listed.Playlists[:50] {
		one.add(c.get("/api/v1/playlists/"+p.ID, nil))
	}
}

// measureDocs reads the script of the page of the documentation, which is
// the largest thing the server sends that is not a file of the library.
func measureDocs(r *report, c *client) {
	r.t.Helper()
	m := r.measure("the script of /api/docs, first load", 0)
	size := 0
	for range 10 {
		status, body, took := c.do(http.MethodGet, "/api/docs/scalar.js", nil)
		if status != http.StatusOK {
			r.t.Fatalf("scalar.js: status %d", status)
		}
		size = len(body)
		m.add(took)
	}
	r.value("the script of /api/docs, size", float64(size)/(1<<20), 0, "MB")
}
