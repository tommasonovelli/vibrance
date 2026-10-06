package app

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"vibrance/internal/api"
)

// The covers of the playlists and the summaries of the catalog and of the
// favorites (docs/proposals/web-client-api.md A2, A3, A4, step W2) over the
// API, on the fixture library indexed by the real indexer with the pinned
// tools. Their rules are proved in internal/catalog on random indexes; here
// every answer is also checked against the specification (I10).

// Every answer that carries a playlist carries its covers, as a ready URL
// of the cover of the album, which serves the image.
func TestPlaylistCoversOverTheAPI(t *testing.T) {
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	anna := w.as(w.anna)
	b1, a2, a1 := w.fixtureTrack(albumB, 1), w.fixtureTrack(albumA, 2), w.fixtureTrack(albumA, 1)

	rec := w.do("POST", "/playlists", map[string]any{"name": "Covers", "description": ""}, anna)
	wantStatus(t, "createPlaylist", rec, http.StatusCreated)
	p := decode[api.Playlist](t, rec)
	if p.Covers == nil || len(p.Covers) != 0 {
		t.Fatalf("a new playlist has the covers %+v", p.Covers)
	}

	// B has no cover; A has one, given once for its two items.
	rec = w.do("POST", "/playlists/"+p.Id+"/items", map[string]any{"track_ids": []string{b1, a2, a1}, "position": nil}, anna)
	wantStatus(t, "addPlaylistItems", rec, http.StatusOK)
	added := decode[api.AddPlaylistItemsResult](t, rec)
	album := decode[api.AlbumDetail](t, w.get("/albums/"+albumA, w.anna))
	if album.Cover == nil {
		t.Fatal("the album A has no cover")
	}
	want := []api.Cover{*album.Cover}
	if !slices.Equal(added.Playlist.Covers, want) {
		t.Fatalf("the covers after adding: %+v, want %+v", added.Playlist.Covers, want)
	}
	got := w.playlist(w.anna, p.Id)
	list := decode[api.PlaylistList](t, w.get("/playlists", w.anna))
	if !slices.Equal(got.Covers, want) || len(list.Playlists) != 1 || !slices.Equal(list.Playlists[0].Covers, want) {
		t.Fatalf("the covers read %+v and listed %+v, want %+v", got.Covers, list.Playlists, want)
	}
	rec = w.fetch("GET", strings.TrimPrefix(want[0].Url, api.BasePath), anna)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("the URL of the cover: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = w.do("PUT", "/playlists/"+p.Id, map[string]any{"name": "Renamed", "description": ""}, anna)
	wantStatus(t, "updatePlaylist", rec, http.StatusOK)
	renamed := decode[api.Playlist](t, rec)
	if !slices.Equal(renamed.Covers, want) {
		t.Fatalf("the covers after a rename: %+v", renamed.Covers)
	}

	// The album of the cover goes: the playlist keeps its items, and has no
	// cover left, without a new revision.
	w.unavailable(albumA)
	if after := w.playlist(w.anna, p.Id); after.Covers == nil || len(after.Covers) != 0 || after.Revision != renamed.Revision {
		t.Fatalf("with A gone: %+v", after)
	}
}

// The summary of the catalog counts what the lists list, and nothing that
// is not available; the summary of the favorites counts every favorite of
// the user of the request and the durations of the available ones.
func TestSummariesOverTheAPI(t *testing.T) {
	w := newWorld(t, apiOrigin)
	empty := decode[api.CatalogSummary](t, w.get("/catalog/summary", w.anna))
	if empty != (api.CatalogSummary{}) {
		t.Fatalf("an empty index: %+v", empty)
	}
	w.indexFixture()

	lists := func() api.CatalogSummary {
		t.Helper()
		var s api.CatalogSummary
		s.Artists = len(decode[api.ArtistList](t, w.get("/artists?limit=200", w.anna)).Artists)
		s.Albums = len(decode[api.AlbumList](t, w.get("/albums?limit=200", w.anna)).Albums)
		tracks := decode[api.TrackList](t, w.get("/tracks?limit=200", w.anna))
		if tracks.Next != nil {
			t.Fatal("more than 200 tracks")
		}
		s.Tracks = len(tracks.Tracks)
		for _, tr := range tracks.Tracks {
			if tr.DurationMs != nil {
				s.DurationMs += *tr.DurationMs
			}
		}
		return s
	}
	before := decode[api.CatalogSummary](t, w.get("/catalog/summary", w.admin))
	if want := lists(); before != want || before.Tracks != 14 || before.DurationMs == 0 {
		t.Fatalf("the summary is %+v, the lists say %+v", before, want)
	}

	tracks := w.fixtureTracks(w.anna)
	var ofA, ofB []api.Track
	for _, tr := range tracks {
		switch tr.Album.Id {
		case albumA:
			ofA = append(ofA, tr)
		case albumB:
			ofB = append(ofB, tr)
		}
	}
	w.favorite(w.anna, ofA[0].Id)
	w.favorite(w.anna, ofB[0].Id)
	w.favorite(w.bob, ofB[0].Id)
	favorites := decode[api.FavoritesSummary](t, w.get("/me/favorites/summary", w.anna))
	if want := (api.FavoritesSummary{TrackCount: 2, DurationMs: *ofA[0].DurationMs + *ofB[0].DurationMs}); favorites != want {
		t.Fatalf("the favorites of anna: %+v, want %+v", favorites, want)
	}

	w.unavailable(albumB)
	after := decode[api.CatalogSummary](t, w.get("/catalog/summary", w.anna))
	var gone int64
	for _, tr := range ofB {
		gone += *tr.DurationMs
	}
	if want := lists(); after != want || after.Albums != before.Albums-1 || after.Tracks != before.Tracks-len(ofB) ||
		after.DurationMs != before.DurationMs-gone {
		t.Fatalf("with B gone the summary is %+v, the lists say %+v; before %+v", after, want, before)
	}
	for who, want := range map[string]api.FavoritesSummary{
		"anna":  {TrackCount: 2, DurationMs: *ofA[0].DurationMs},
		"bob":   {TrackCount: 1, DurationMs: 0},
		"admin": {},
	} {
		a := map[string]*account{"anna": w.anna, "bob": w.bob, "admin": w.admin}[who]
		if got := decode[api.FavoritesSummary](t, w.get("/me/favorites/summary", a)); got != want {
			t.Fatalf("the favorites of %s with B gone: %+v, want %+v", who, got, want)
		}
	}
}
