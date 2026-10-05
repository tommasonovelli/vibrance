package catalog

import (
	"context"

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

// pageKey is what a list of the albums of one artist, which is one query
// for its first page and for the others, takes for the cursor k: without
// one, first_page is 1 and the key is one of values that are never read.
func pageKey(k []httpx.CursorKey, kinds []httpx.CursorKind) (firstPage int64, key []httpx.CursorKey) {
	if k == nil {
		return 1, make([]httpx.CursorKey, len(kinds))
	}
	return 0, k
}

// albumOrders are the orders of the list of the albums, by sort and order,
// with the three queries of each.
var albumOrders = map[[2]string]albumOrder{
	{SortTitle, OrderAsc}: {
		kinds: titleKinds,
		key:   titleKey,
		first: func(ctx context.Context, q *store.Queries, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByTitleAsc(ctx, n))
		},
		after: func(ctx context.Context, q *store.Queries, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByTitleAscAfter(ctx, store.ListAlbumsByTitleAscAfterParams{
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, artist string, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			first, k := pageKey(k, titleKinds)
			return albumRows(q.ListArtistAlbumsByTitleAsc(ctx, store.ListArtistAlbumsByTitleAscParams{ArtistID: artist, FirstPage: first,
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
	{SortTitle, OrderDesc}: {
		kinds: titleKinds,
		key:   titleKey,
		first: func(ctx context.Context, q *store.Queries, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByTitleDesc(ctx, n))
		},
		after: func(ctx context.Context, q *store.Queries, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByTitleDescAfter(ctx, store.ListAlbumsByTitleDescAfterParams{
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, artist string, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			first, k := pageKey(k, titleKinds)
			return albumRows(q.ListArtistAlbumsByTitleDesc(ctx, store.ListArtistAlbumsByTitleDescParams{ArtistID: artist, FirstPage: first,
				TitleKey: k[0].Bytes(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
	{SortArtist, OrderAsc}: {
		kinds: artistKinds,
		key:   artistKey,
		first: func(ctx context.Context, q *store.Queries, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByArtistAsc(ctx, n))
		},
		after: func(ctx context.Context, q *store.Queries, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByArtistAscAfter(ctx, store.ListAlbumsByArtistAscAfterParams{
				ArtistKey: k[0].Bytes(), YearKey: k[1].Int(), TitleKey: k[2].Bytes(), AfterID: k[3].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, artist string, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			first, k := pageKey(k, artistKinds)
			return albumRows(q.ListArtistAlbumsByArtistAsc(ctx, store.ListArtistAlbumsByArtistAscParams{ArtistID: artist, FirstPage: first,
				ArtistKey: k[0].Bytes(), YearKey: k[1].Int(), TitleKey: k[2].Bytes(), AfterID: k[3].ID(), PageSize: n}))
		},
	},
	{SortArtist, OrderDesc}: {
		kinds: artistKinds,
		key:   artistKey,
		first: func(ctx context.Context, q *store.Queries, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByArtistDesc(ctx, n))
		},
		after: func(ctx context.Context, q *store.Queries, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByArtistDescAfter(ctx, store.ListAlbumsByArtistDescAfterParams{
				ArtistKey: k[0].Bytes(), YearKey: k[1].Int(), TitleKey: k[2].Bytes(), AfterID: k[3].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, artist string, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			first, k := pageKey(k, artistKinds)
			return albumRows(q.ListArtistAlbumsByArtistDesc(ctx, store.ListArtistAlbumsByArtistDescParams{ArtistID: artist, FirstPage: first,
				ArtistKey: k[0].Bytes(), YearKey: k[1].Int(), TitleKey: k[2].Bytes(), AfterID: k[3].ID(), PageSize: n}))
		},
	},
	{SortYear, OrderAsc}: {
		kinds: yearKinds,
		key:   yearKey,
		first: func(ctx context.Context, q *store.Queries, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByYearAsc(ctx, n))
		},
		after: func(ctx context.Context, q *store.Queries, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByYearAscAfter(ctx, store.ListAlbumsByYearAscAfterParams{
				YearKey: k[0].Int(), TitleKey: k[1].Bytes(), AfterID: k[2].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, artist string, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			first, k := pageKey(k, yearKinds)
			return albumRows(q.ListArtistAlbumsByYearAsc(ctx, store.ListArtistAlbumsByYearAscParams{ArtistID: artist, FirstPage: first,
				YearKey: k[0].Int(), TitleKey: k[1].Bytes(), AfterID: k[2].ID(), PageSize: n}))
		},
	},
	{SortYear, OrderDesc}: {
		kinds: yearKinds,
		key:   yearKey,
		first: func(ctx context.Context, q *store.Queries, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByYearDesc(ctx, n))
		},
		after: func(ctx context.Context, q *store.Queries, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByYearDescAfter(ctx, store.ListAlbumsByYearDescAfterParams{
				YearKey: k[0].Int(), TitleKey: k[1].Bytes(), AfterID: k[2].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, artist string, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			first, k := pageKey(k, yearKinds)
			return albumRows(q.ListArtistAlbumsByYearDesc(ctx, store.ListArtistAlbumsByYearDescParams{ArtistID: artist, FirstPage: first,
				YearKey: k[0].Int(), TitleKey: k[1].Bytes(), AfterID: k[2].ID(), PageSize: n}))
		},
	},
	{SortAdded, OrderAsc}: {
		kinds: addedKinds,
		key:   addedKey,
		first: func(ctx context.Context, q *store.Queries, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByAddedAsc(ctx, n))
		},
		after: func(ctx context.Context, q *store.Queries, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByAddedAscAfter(ctx, store.ListAlbumsByAddedAscAfterParams{
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, artist string, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			first, k := pageKey(k, addedKinds)
			return albumRows(q.ListArtistAlbumsByAddedAsc(ctx, store.ListArtistAlbumsByAddedAscParams{ArtistID: artist, FirstPage: first,
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
	{SortAdded, OrderDesc}: {
		kinds: addedKinds,
		key:   addedKey,
		first: func(ctx context.Context, q *store.Queries, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByAddedDesc(ctx, n))
		},
		after: func(ctx context.Context, q *store.Queries, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			return albumRows(q.ListAlbumsByAddedDescAfter(ctx, store.ListAlbumsByAddedDescAfterParams{
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
		ofArtist: func(ctx context.Context, q *store.Queries, artist string, k []httpx.CursorKey, n int64) ([]albumRow, error) {
			first, k := pageKey(k, addedKinds)
			return albumRows(q.ListArtistAlbumsByAddedDesc(ctx, store.ListArtistAlbumsByAddedDescParams{ArtistID: artist, FirstPage: first,
				FirstSeenAt: k[0].Int(), AfterID: k[1].ID(), PageSize: n}))
		},
	},
}
