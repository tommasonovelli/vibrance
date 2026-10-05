package catalog

import (
	"context"
	"database/sql"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The keys of the orders of the list of the albums (DESIGN.md §8.5), each
// ending with the id: the values a cursor holds, in the order of the
// comparison.
var (
	titleKinds  = []httpx.CursorKind{httpx.CursorBytes, httpx.CursorID}
	artistKinds = []httpx.CursorKind{httpx.CursorBytes, httpx.CursorInt, httpx.CursorBytes, httpx.CursorID}
	yearKinds   = []httpx.CursorKind{httpx.CursorInt, httpx.CursorBytes, httpx.CursorID}
	addedKinds  = []httpx.CursorKind{httpx.CursorInt, httpx.CursorID}
)

func titleKey(a store.Album) []httpx.CursorKey {
	return []httpx.CursorKey{httpx.BytesKey(a.TitleKey), httpx.IDKey(a.ID)}
}

func artistKey(a store.Album) []httpx.CursorKey {
	return []httpx.CursorKey{httpx.BytesKey(a.ArtistKey), httpx.IntKey(a.YearKey), httpx.BytesKey(a.TitleKey), httpx.IDKey(a.ID)}
}

func yearKey(a store.Album) []httpx.CursorKey {
	return []httpx.CursorKey{httpx.IntKey(a.YearKey), httpx.BytesKey(a.TitleKey), httpx.IDKey(a.ID)}
}

func addedKey(a store.Album) []httpx.CursorKey {
	return []httpx.CursorKey{httpx.IntKey(a.FirstSeenAt), httpx.IDKey(a.ID)}
}

// albumOrders are the orders of the list of the albums, by sort and order,
// with the two queries of each.
var albumOrders = map[[2]string]albumOrder{
	{SortTitle, OrderAsc}: {
		kinds: titleKinds,
		key:   titleKey,
		first: func(ctx context.Context, q *store.Queries, artist sql.NullString, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByTitleAsc(ctx, store.ListAlbumsByTitleAscParams{ArtistID: artist, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, artist sql.NullString, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByTitleAscAfter(ctx, store.ListAlbumsByTitleAscAfterParams{ArtistID: artist,
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
	{SortTitle, OrderDesc}: {
		kinds: titleKinds,
		key:   titleKey,
		first: func(ctx context.Context, q *store.Queries, artist sql.NullString, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByTitleDesc(ctx, store.ListAlbumsByTitleDescParams{ArtistID: artist, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, artist sql.NullString, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByTitleDescAfter(ctx, store.ListAlbumsByTitleDescAfterParams{ArtistID: artist,
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
	{SortArtist, OrderAsc}: {
		kinds: artistKinds,
		key:   artistKey,
		first: func(ctx context.Context, q *store.Queries, artist sql.NullString, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByArtistAsc(ctx, store.ListAlbumsByArtistAscParams{ArtistID: artist, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, artist sql.NullString, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByArtistAscAfter(ctx, store.ListAlbumsByArtistAscAfterParams{ArtistID: artist,
				ArtistKey: k[0].Bytes(), YearKey: k[1].Int(), TitleKey: k[2].Bytes(), AfterID: k[3].ID(), PageSize: n}))
		},
	},
	{SortArtist, OrderDesc}: {
		kinds: artistKinds,
		key:   artistKey,
		first: func(ctx context.Context, q *store.Queries, artist sql.NullString, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByArtistDesc(ctx, store.ListAlbumsByArtistDescParams{ArtistID: artist, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, artist sql.NullString, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByArtistDescAfter(ctx, store.ListAlbumsByArtistDescAfterParams{ArtistID: artist,
				ArtistKey: k[0].Bytes(), YearKey: k[1].Int(), TitleKey: k[2].Bytes(), AfterID: k[3].ID(), PageSize: n}))
		},
	},
	{SortYear, OrderAsc}: {
		kinds: yearKinds,
		key:   yearKey,
		first: func(ctx context.Context, q *store.Queries, artist sql.NullString, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByYearAsc(ctx, store.ListAlbumsByYearAscParams{ArtistID: artist, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, artist sql.NullString, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByYearAscAfter(ctx, store.ListAlbumsByYearAscAfterParams{ArtistID: artist,
				YearKey: k[0].Int(), TitleKey: k[1].Bytes(), AfterID: k[2].ID(), PageSize: n}))
		},
	},
	{SortYear, OrderDesc}: {
		kinds: yearKinds,
		key:   yearKey,
		first: func(ctx context.Context, q *store.Queries, artist sql.NullString, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByYearDesc(ctx, store.ListAlbumsByYearDescParams{ArtistID: artist, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, artist sql.NullString, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByYearDescAfter(ctx, store.ListAlbumsByYearDescAfterParams{ArtistID: artist,
				YearKey: k[0].Int(), TitleKey: k[1].Bytes(), AfterID: k[2].ID(), PageSize: n}))
		},
	},
	{SortAdded, OrderAsc}: {
		kinds: addedKinds,
		key:   addedKey,
		first: func(ctx context.Context, q *store.Queries, artist sql.NullString, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByAddedAsc(ctx, store.ListAlbumsByAddedAscParams{ArtistID: artist, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, artist sql.NullString, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByAddedAscAfter(ctx, store.ListAlbumsByAddedAscAfterParams{ArtistID: artist,
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
	{SortAdded, OrderDesc}: {
		kinds: addedKinds,
		key:   addedKey,
		first: func(ctx context.Context, q *store.Queries, artist sql.NullString, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByAddedDesc(ctx, store.ListAlbumsByAddedDescParams{ArtistID: artist, PageSize: n}))
		},
		after: func(ctx context.Context, q *store.Queries, artist sql.NullString, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByAddedDescAfter(ctx, store.ListAlbumsByAddedDescAfterParams{ArtistID: artist,
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
}
