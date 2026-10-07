package catalog

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The playlists of DESIGN.md §8.6. A playlist is private: every operation
// names the user it is asked by, and the playlist of another user is 404
// playlist_not_found, like one that does not exist (I6). Every change is
// one short write transaction that reads the playlist, compares If-Match
// with its revision, changes it, leaves the positions of its items dense
// (0..n-1) and gives it a new revision.

// The codes of the refusals of the playlists (§8.4).
const (
	CodePlaylistNotFound     = "playlist_not_found"
	CodeItemNotFound         = "item_not_found"
	CodePreconditionFailed   = "precondition_failed"
	CodePreconditionRequired = "precondition_required"
	CodeInvalidRequest       = "invalid_request"
	CodeTooManyPlaylists     = "too_many_playlists"
	CodeTooManyItems         = "too_many_items"
	CodeUnknownTrack         = "unknown_track"
	CodeInvalidPosition      = "invalid_position"
)

// The limits of the playlists (§5.2).
const (
	MaxPlaylists           = 500
	MaxPlaylistItems       = 10000
	maxPlaylistName        = 200
	maxPlaylistDescription = 2000
)

// sortPosition is the order of the list of the items of a playlist, in its
// cursors: it differs from every order of the other lists, so that a cursor
// of one list is refused by the others.
const sortPosition = "position"

// Playlist is a playlist of a user, without its items.
type Playlist struct {
	ID          string
	Name        string
	Description string
	// ItemCount counts every item, also the ones whose track is not
	// available: the positions of the items are 0..ItemCount-1.
	ItemCount int
	// DurationMS adds up the tracks that are available.
	DurationMS int64
	// Revision grows at every change of the playlist or of its items.
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
	// Covers are the covers of the mosaic of the playlist: at most
	// MaxPlaylistCovers, of distinct albums, from the items whose track is
	// available, in the order of the items, skipping the albums without a
	// cover. Never nil: empty when there is none.
	Covers []AlbumCover
}

// AlbumCover is the cover of an album: Hash is the SHA-256 of its file.
type AlbumCover struct {
	AlbumID string
	Hash    string
}

// MaxPlaylistCovers is how many covers a playlist shows at most, the four
// of a 2x2 mosaic (docs/proposals/web-client-api.md A2).
const MaxPlaylistCovers = 4

// ETag is the strong entity tag of the revision of the playlist, with its
// quotes (§8.1): the value a client sends back as If-Match.
func (p Playlist) ETag() string {
	return `"playlist:` + p.ID + ":" + strconv.FormatInt(p.Revision, 10) + `"`
}

// PlaylistItem is one place in a playlist. Its id is of the item, not of
// the track: a track can be in a playlist more than once.
type PlaylistItem struct {
	ID       string
	Position int
	AddedAt  time.Time
	Track    Track
}

// PlaylistItemPage is a page of the items of a playlist. ETag is the entity
// tag of the playlist as it was when the page was read, and ItemCount how
// many items it had then. Next is the cursor of the page after it, "" on the
// last page.
type PlaylistItemPage struct {
	ETag      string
	ItemCount int
	Items     []PlaylistItem
	Next      string
}

// AddedItem is an item AddPlaylistItems made.
type AddedItem struct {
	ItemID   string
	TrackID  string
	Position int
}

func playlistNotFound() *httpx.Error {
	return &httpx.Error{Status: http.StatusNotFound, Code: CodePlaylistNotFound, Message: "There is no such playlist."}
}

func itemNotFound() *httpx.Error {
	return &httpx.Error{Status: http.StatusNotFound, Code: CodeItemNotFound, Message: "The playlist has no such item."}
}

func invalidPosition() *httpx.Error {
	return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: CodeInvalidPosition,
		Message: "The position is not one of the playlist."}
}

func invalidPlaylist(rule string) *httpx.Error {
	return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: CodeInvalidRequest, Message: rule}
}

// refusedTracks is the 422 of the tracks that cannot be added, with their
// ids in details.track_ids.
func refusedTracks(code, message string, ids []string) *httpx.Error {
	return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: code, Message: message,
		Details: map[string]any{"track_ids": ids}}
}

// checkPlaylistInput refuses, with 422 invalid_request, a name that is not
// 1 to 200 characters or a description over 2000 (§5.2). A character is a
// Unicode code point, as for the checks of the database. U+0000 is refused
// in both: SQLite measures a text up to it, so the checks of the database
// would see another length.
func checkPlaylistInput(name, description string) error {
	switch n := utf8.RuneCountInString(name); {
	case n < 1 || n > maxPlaylistName:
		return invalidPlaylist("The name of a playlist has 1 to 200 characters.")
	case utf8.RuneCountInString(description) > maxPlaylistDescription:
		return invalidPlaylist("The description of a playlist has at most 2000 characters.")
	case strings.ContainsRune(name, 0) || strings.ContainsRune(description, 0) ||
		!utf8.ValidString(name) || !utf8.ValidString(description):
		return invalidPlaylist("The name and the description of a playlist are text, without the character U+0000.")
	}
	return nil
}

// etagListed tells whether an If-Match header names etag: the header is a
// list of entity tags separated by commas, and a W/ in front of a tag is
// ignored (§8.1). Anything else, "*" included, names nothing.
func etagListed(ifMatch, etag string) bool {
	for tag := range strings.SplitSeq(ifMatch, ",") {
		if strings.TrimPrefix(strings.Trim(tag, " \t"), "W/") == etag {
			return true
		}
	}
	return false
}

// checkRevision compares the If-Match of a change with the entity tag of the
// playlist it is about to change: nil is no header, which is 428
// precondition_required when the change needs one; a header that does not name the current
// revision is 412 precondition_failed.
func checkRevision(etag string, ifMatch *string, required bool) error {
	switch {
	case ifMatch == nil && required:
		return &httpx.Error{Status: http.StatusPreconditionRequired, Code: CodePreconditionRequired,
			Message: "This change needs If-Match."}
	case ifMatch != nil && !etagListed(*ifMatch, etag):
		return &httpx.Error{Status: http.StatusPreconditionFailed, Code: CodePreconditionFailed,
			Message: "The playlist was changed in the meantime."}
	}
	return nil
}

// playlistOf is the playlist of a row, with its covers read in the
// transaction q that read the row.
func playlistOf(ctx context.Context, q *store.Queries, r store.GetPlaylistOfUserRow) (Playlist, error) {
	covers, err := playlistCovers(ctx, q, r.Playlist.ID)
	if err != nil {
		return Playlist{}, err
	}
	return Playlist{ID: r.Playlist.ID, Name: r.Playlist.Name, Description: r.Playlist.Description,
		ItemCount: int(r.ItemCount), DurationMS: r.DurationMs, Revision: r.Playlist.Revision,
		CreatedAt: time.UnixMilli(r.Playlist.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(r.Playlist.UpdatedAt).UTC(),
		Covers: covers}, nil
}

// playlistCovers reads, in the transaction q, the covers of the mosaic of a
// playlist: the cover of the album of the first item whose track is
// available and whose album has a cover, then of the first such item of
// another album, and so on up to MaxPlaylistCovers. Each query starts after
// the item the one before it found and leaves out the albums found, so the
// items are read once, and a playlist whose first items give four albums
// reads only those.
func playlistCovers(ctx context.Context, q *store.Queries, playlistID string) ([]AlbumCover, error) {
	covers := make([]AlbumCover, 0, MaxPlaylistCovers)
	// No position is negative: the first item is the one after this key.
	// An album id is never "": it stands for an album not found yet.
	next := store.NextPlaylistCoverParams{PlaylistID: playlistID, AfterPosition: -1}
	for len(covers) < MaxPlaylistCovers {
		row, err := q.NextPlaylistCover(ctx, next)
		switch {
		case noRows(ctx, err):
			return covers, nil
		case err != nil:
			return nil, err
		}
		covers = append(covers, AlbumCover{AlbumID: row.AlbumID, Hash: row.CoverSha256})
		next.AfterPosition, next.AfterID = row.Position, row.ID
		switch len(covers) {
		case 1:
			next.Seen1 = row.AlbumID
		case 2:
			next.Seen2 = row.AlbumID
		case 3:
			next.Seen3 = row.AlbumID
		}
	}
	return covers, nil
}

// readPlaylist reads, in the transaction q, the playlist id of userID. One
// that does not exist or is of another user is 404 playlist_not_found:
// this is the only way a playlist is reached, so it is the check of its
// owner.
func readPlaylist(ctx context.Context, q *store.Queries, userID, id string) (Playlist, error) {
	row, err := q.GetPlaylistOfUser(ctx, store.GetPlaylistOfUserParams{ID: id, UserID: userID})
	switch {
	case noRows(ctx, err):
		return Playlist{}, playlistNotFound()
	case err != nil:
		return Playlist{}, err
	}
	return playlistOf(ctx, q, row)
}

// playlistHead reads, in the transaction q, the revision of the playlist id
// of userID, as its entity tag, and how many items it has. It is readPlaylist
// without the duration, which costs a read of the track of every item: a
// change reads the whole playlist once, when it is done. Like readPlaylist it
// is a check of the owner: a playlist that does not exist or is of another
// user is 404 playlist_not_found.
func playlistHead(ctx context.Context, q *store.Queries, userID, id string) (etag string, items int, err error) {
	row, err := q.GetPlaylistStateOfUser(ctx, store.GetPlaylistStateOfUserParams{ID: id, UserID: userID})
	switch {
	case noRows(ctx, err):
		return "", 0, playlistNotFound()
	case err != nil:
		return "", 0, err
	}
	return Playlist{ID: row.Playlist.ID, Revision: row.Playlist.Revision}.ETag(), int(row.ItemCount), nil
}

// openPlaylist begins a change, in the write transaction q: it reads the
// revision of the playlist and checks If-Match against it, so the comparison
// and the change are one transaction (§8.1). It returns how many items the
// playlist has.
func openPlaylist(ctx context.Context, q *store.Queries, userID, id string, ifMatch *string, required bool) (items int, err error) {
	etag, items, err := playlistHead(ctx, q, userID, id)
	if err != nil {
		return 0, err
	}
	return items, checkRevision(etag, ifMatch, required)
}

// itemsChanged ends a change of the items, in its transaction: the
// positions are made dense again, in the order they have, the playlist gets
// a new revision, and it is read as it is now (§8.6).
func itemsChanged(ctx context.Context, q *store.Queries, userID, id string, now int64) (Playlist, error) {
	if err := q.RenumberPlaylistItems(ctx, id); err != nil {
		return Playlist{}, err
	}
	if err := q.TouchPlaylist(ctx, store.TouchPlaylistParams{UpdatedAt: now, ID: id}); err != nil {
		return Playlist{}, err
	}
	return readPlaylist(ctx, q, userID, id)
}

// ListPlaylists returns every playlist of userID, the oldest first.
func (s *Service) ListPlaylists(ctx context.Context, userID string) ([]Playlist, error) {
	var playlists []Playlist
	err := s.store.Read(ctx, func(q *store.Queries) error {
		rows, err := q.ListPlaylistsOfUser(ctx, userID)
		if err != nil {
			return err
		}
		playlists = make([]Playlist, 0, len(rows))
		for _, r := range rows {
			p, err := playlistOf(ctx, q, store.GetPlaylistOfUserRow(r))
			if err != nil {
				return err
			}
			playlists = append(playlists, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: listing the playlists: %w", err)
	}
	return playlists, nil
}

// PlaylistRef is a playlist by its id and its name.
type PlaylistRef struct {
	ID   string
	Name string
}

// ListTrackPlaylists returns the playlists of userID that have at least one
// item with the track trackID, available or not, the oldest first, as
// ListPlaylists (docs/proposals/web-client-api.md B4). The playlists of
// other users are never among them. A track that does not exist is 404
// track_not_found.
func (s *Service) ListTrackPlaylists(ctx context.Context, userID, trackID string) ([]PlaylistRef, error) {
	var rows []store.ListPlaylistRefsOfUserWithTrackRow
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		if err = knownTrack(ctx, q, trackID); err != nil {
			return err
		}
		rows, err = q.ListPlaylistRefsOfUserWithTrack(ctx, store.ListPlaylistRefsOfUserWithTrackParams{TrackID: trackID, UserID: userID})
		return err
	})
	if err != nil {
		return nil, changeFailure("listing the playlists of a track", err)
	}
	refs := make([]PlaylistRef, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, PlaylistRef{ID: r.ID, Name: r.Name})
	}
	return refs, nil
}

// CreatePlaylist makes an empty playlist of userID. A name or a description
// out of their limits is 422 invalid_request; a user that has 500 playlists
// already, 422 too_many_playlists (§5.2). An account that was deleted since
// the request was authenticated creates nothing and is answered as its next
// request will be: 401 login_required.
func (s *Service) CreatePlaylist(ctx context.Context, userID, name, description string) (Playlist, error) {
	if err := checkPlaylistInput(name, description); err != nil {
		return Playlist{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Playlist{}, fmt.Errorf("catalog: making the id of a playlist: %w", err)
	}
	now := s.now().UnixMilli()
	var p Playlist
	err = s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		n, err := q.CountPlaylistsOfUser(ctx, userID)
		if err != nil {
			return err
		}
		if n >= MaxPlaylists {
			return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: CodeTooManyPlaylists,
				Message: "A user has at most 500 playlists."}
		}
		created, err := q.CreatePlaylist(ctx, store.CreatePlaylistParams{ID: id.String(), Name: name, Description: description,
			CreatedAt: now, UserID: userID})
		if err != nil {
			return err
		}
		if created == 0 {
			return &httpx.Error{Status: http.StatusUnauthorized, Code: "login_required", Message: "A session is required: sign in."}
		}
		p, err = readPlaylist(ctx, q, userID, id.String())
		return err
	})
	return p, changeFailure("creating a playlist", err)
}

// GetPlaylist returns the playlist id of userID, without its items.
func (s *Service) GetPlaylist(ctx context.Context, userID, id string) (Playlist, error) {
	var p Playlist
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		p, err = readPlaylist(ctx, q, userID, id)
		return err
	})
	return p, changeFailure("reading a playlist", err)
}

// UpdatePlaylist gives a playlist its name and its description, and a new
// revision, also when both are the ones it has. ifMatch is the If-Match of
// the request, nil without one: it is optional, and checked when sent.
func (s *Service) UpdatePlaylist(ctx context.Context, userID, id, name, description string, ifMatch *string) (Playlist, error) {
	now := s.now().UnixMilli()
	var p Playlist
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) (err error) {
		if _, err = openPlaylist(ctx, q, userID, id, ifMatch, false); err != nil {
			return err
		}
		if err = checkPlaylistInput(name, description); err != nil {
			return err
		}
		if err = q.RenamePlaylist(ctx, store.RenamePlaylistParams{Name: name, Description: description, UpdatedAt: now, ID: id}); err != nil {
			return err
		}
		p, err = readPlaylist(ctx, q, userID, id)
		return err
	})
	return p, changeFailure("updating a playlist", err)
}

// DeletePlaylist deletes a playlist with its items. ifMatch is optional,
// and checked when sent.
func (s *Service) DeletePlaylist(ctx context.Context, userID, id string, ifMatch *string) error {
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) error {
		if _, err := openPlaylist(ctx, q, userID, id, ifMatch, false); err != nil {
			return err
		}
		return q.DeletePlaylist(ctx, id)
	})
	return changeFailure("deleting a playlist", err)
}

// ListPlaylistItems returns a page of the items of a playlist, by
// (position, id) (§8.5), with the entity tag and the number of items of the
// playlist as it is in the same read transaction. A track that is no longer available stays in the list, with
// the last data known. after is the cursor of the page before, nil for the
// first page; one that is not a cursor of this list, the empty string
// included, is 400 invalid_cursor.
func (s *Service) ListPlaylistItems(ctx context.Context, userID, id string, limit int, after *string) (PlaylistItemPage, error) {
	// No position is negative: the first page is the one after this key.
	params := store.ListPlaylistItemsParams{UserID: userID, PlaylistID: id, AfterPosition: -1, PageSize: int64(limit) + 1}
	if after != nil {
		keys, err := httpx.DecodeCursor(*after, sortPosition, OrderAsc, httpx.CursorInt, httpx.CursorID)
		if err != nil {
			return PlaylistItemPage{}, err
		}
		params.AfterPosition, params.AfterID = keys[0].Int(), keys[1].ID()
	}
	var (
		page PlaylistItemPage
		rows []store.ListPlaylistItemsRow
	)
	err := s.store.Read(ctx, func(q *store.Queries) (err error) {
		if page.ETag, page.ItemCount, err = playlistHead(ctx, q, userID, id); err != nil {
			return err
		}
		rows, err = q.ListPlaylistItems(ctx, params)
		return err
	})
	if err != nil {
		return PlaylistItemPage{}, changeFailure("listing the items of a playlist", err)
	}
	rows, more := cut(rows, limit)
	page.Items = make([]PlaylistItem, 0, len(rows))
	for _, r := range rows {
		track := trackInAlbum(trackRow{Track: r.Track, AlbumTitle: r.AlbumTitle, AlbumYear: r.AlbumYear,
			AlbumCoverSha256: r.AlbumCoverSha256, AlbumArtistID: r.AlbumArtistID, AlbumArtistName: r.AlbumArtistName,
			Favorite: r.Favorite})
		page.Items = append(page.Items, PlaylistItem{ID: r.ItemID, Position: int(r.Position),
			AddedAt: time.UnixMilli(r.AddedAt).UTC(), Track: track})
	}
	if more {
		last := rows[len(rows)-1]
		if page.Next, err = httpx.EncodeCursor(sortPosition, OrderAsc, httpx.IntKey(last.Position), httpx.IDKey(last.ItemID)); err != nil {
			return PlaylistItemPage{}, err
		}
	}
	return page, nil
}

// AddPlaylistItems adds the tracks to a playlist as a block, in the order
// given; a track may be there more than once. With a nil position the
// block goes at the end, and ifMatch is optional. With a position, from 0
// to the number of items, the block goes before the item that is there,
// and ifMatch is required: 428 precondition_required without it (§8.6).
//
// All the tracks are added, or none: a position out of range is 422
// invalid_position; more than 10000 items, 422 too_many_items; ids that are
// no tracks, 422 unknown_track, and tracks that are not available, 422
// track_unavailable, both with the ids in details.track_ids.
func (s *Service) AddPlaylistItems(ctx context.Context, userID, id string, trackIDs []string, position *int,
	ifMatch *string) (Playlist, []AddedItem, error) {
	added := make([]AddedItem, len(trackIDs))
	for i, trackID := range trackIDs {
		itemID, err := uuid.NewV7()
		if err != nil {
			return Playlist{}, nil, fmt.Errorf("catalog: making the id of a playlist item: %w", err)
		}
		added[i] = AddedItem{ItemID: itemID.String(), TrackID: trackID}
	}
	now := s.now().UnixMilli()
	var p Playlist
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) (err error) {
		count, err := openPlaylist(ctx, q, userID, id, ifMatch, position != nil)
		if err != nil {
			return err
		}
		at := count
		if position != nil {
			at = *position
		}
		switch {
		case at < 0 || at > count:
			return invalidPosition()
		case count+len(trackIDs) > MaxPlaylistItems:
			return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: CodeTooManyItems,
				Message: "A playlist has at most 10000 items."}
		}
		if err = addableTracks(ctx, q, trackIDs); err != nil {
			return err
		}
		err = q.ShiftPlaylistItems(ctx, store.ShiftPlaylistItemsParams{Places: int64(len(added)), PlaylistID: id,
			First: int64(at), Last: int64(count) - 1})
		if err != nil {
			return err
		}
		for i := range added {
			added[i].Position = at + i
			err = q.InsertPlaylistItem(ctx, store.InsertPlaylistItemParams{ID: added[i].ItemID, PlaylistID: id,
				TrackID: added[i].TrackID, Position: int64(added[i].Position), AddedAt: now})
			if err != nil {
				return err
			}
		}
		p, err = itemsChanged(ctx, q, userID, id, now)
		return err
	})
	if err != nil {
		return Playlist{}, nil, changeFailure("adding items to a playlist", err)
	}
	return p, added, nil
}

// addableTracks refuses, in the transaction q, a list of tracks one of
// which does not exist (422 unknown_track) or, when all exist, is not
// available (422 track_unavailable). Each refusal lists its ids once, in
// the order of the request.
func addableTracks(ctx context.Context, q *store.Queries, trackIDs []string) error {
	var unknown, unavailable []string
	for _, id := range distinct(trackIDs) {
		available, err := q.GetTrackAvailability(ctx, id)
		switch {
		case noRows(ctx, err):
			unknown = append(unknown, id)
		case err != nil:
			return err
		case available == 0:
			unavailable = append(unavailable, id)
		}
	}
	switch {
	case len(unknown) != 0:
		return refusedTracks(CodeUnknownTrack, "Some tracks do not exist.", unknown)
	case len(unavailable) != 0:
		return refusedTracks(CodeTrackUnavailable, "Some tracks are not available.", unavailable)
	}
	return nil
}

// RemovePlaylistItem removes one item of a playlist, by its id, and closes
// the place it left. An item the playlist does not have is 404
// item_not_found. ifMatch is optional, and checked when sent.
func (s *Service) RemovePlaylistItem(ctx context.Context, userID, id, itemID string, ifMatch *string) (Playlist, error) {
	now := s.now().UnixMilli()
	var p Playlist
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) (err error) {
		if _, err = openPlaylist(ctx, q, userID, id, ifMatch, false); err != nil {
			return err
		}
		if _, err = itemPosition(ctx, q, id, itemID); err != nil {
			return err
		}
		if err = q.DeletePlaylistItem(ctx, itemID); err != nil {
			return err
		}
		p, err = itemsChanged(ctx, q, userID, id, now)
		return err
	})
	return p, changeFailure("removing an item of a playlist", err)
}

// MovePlaylistItem moves one item of a playlist so that position, from 0 to
// the number of items less one, is where it is after the move; the other
// items keep their order (§8.6). ifMatch is required: 428
// precondition_required without it. An item the playlist does not have is
// 404 item_not_found; a position out of range, 422 invalid_position. The
// playlist gets a new revision also when the item is there already.
func (s *Service) MovePlaylistItem(ctx context.Context, userID, id, itemID string, position int, ifMatch *string) (Playlist, error) {
	now := s.now().UnixMilli()
	var p Playlist
	err := s.store.WithWriteTx(ctx, func(q *store.Queries) (err error) {
		count, err := openPlaylist(ctx, q, userID, id, ifMatch, true)
		if err != nil {
			return err
		}
		from, err := itemPosition(ctx, q, id, itemID)
		if err != nil {
			return err
		}
		to := int64(position)
		if position < 0 || position >= count {
			return invalidPosition()
		}
		// The items between the two places move by one towards the place
		// the item leaves.
		shift := store.ShiftPlaylistItemsParams{Places: -1, PlaylistID: id, First: from + 1, Last: to}
		if to < from {
			shift = store.ShiftPlaylistItemsParams{Places: 1, PlaylistID: id, First: to, Last: from - 1}
		}
		if err = q.ShiftPlaylistItems(ctx, shift); err != nil {
			return err
		}
		if err = q.SetPlaylistItemPosition(ctx, store.SetPlaylistItemPositionParams{Position: to, ID: itemID}); err != nil {
			return err
		}
		p, err = itemsChanged(ctx, q, userID, id, now)
		return err
	})
	return p, changeFailure("moving an item of a playlist", err)
}

// itemPosition returns, in the transaction q, where an item of a playlist
// is. An item the playlist does not have, one of another playlist
// included, is 404 item_not_found.
func itemPosition(ctx context.Context, q *store.Queries, playlistID, itemID string) (int64, error) {
	position, err := q.GetPlaylistItemPosition(ctx, store.GetPlaylistItemPositionParams{ID: itemID, PlaylistID: playlistID})
	if noRows(ctx, err) {
		return 0, itemNotFound()
	}
	return position, err
}
